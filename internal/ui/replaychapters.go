package ui

import (
	"fmt"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Replay auto-chapters (#70): a loaded replay gets a jump list of its natural
// beats — scene (background) changes, music changes, and shouts — so a long
// recording is navigable instead of watch-from-the-top. Chapters are derived
// once at startReplay (pure, bounded, unit-tested); jumping rebuilds the replay
// room and seeds the target's context (the last background/music/message before
// it, the buildClip trick) so the stage is RIGHT at the landing point without
// replaying — or audibly blasting — everything in between.

// maxReplayChapters bounds the jump list (hard rule §17.4): a marathon
// recording lists its first N beats rather than growing without limit.
const maxReplayChapters = 96

// replayChapter is one jump-list entry: the event index playback resumes from
// and a short label.
type replayChapter struct {
	idx   int
	label string
}

// shoutChapterLabel names a shout for the jump list.
func shoutChapterLabel(m *protocol.ChatMessage) string {
	who := m.Showname
	if who == "" {
		who = m.CharName
	}
	switch m.Objection {
	case protocol.ShoutHoldIt:
		return "⚡ " + who + ": Hold it!"
	case protocol.ShoutObjection:
		return "⚡ " + who + ": Objection!"
	case protocol.ShoutTakeThat:
		return "⚡ " + who + ": Take that!"
	default:
		if m.CustomShout != "" {
			return "⚡ " + who + ": " + m.CustomShout
		}
		return "⚡ " + who
	}
}

// buildReplayChapters derives the chapter list from a recording's events:
// every background change ("Scene: …"), every music change ("♪ …"), and every
// shouted message. The very first background/music (the recording's opening
// state) are chapters too — they mark "the start". Bounded by maxReplayChapters.
func buildReplayChapters(events []recEvent) []replayChapter {
	var out []replayChapter
	for i, e := range events {
		if len(out) >= maxReplayChapters {
			break
		}
		switch courtroom.EventKind(e.Kind) {
		case courtroom.EventBackground:
			if e.Text != "" {
				out = append(out, replayChapter{idx: i, label: "Scene: " + e.Text})
			}
		case courtroom.EventMusic:
			if e.Text != "" {
				out = append(out, replayChapter{idx: i, label: "♪ " + e.Text})
			}
		case courtroom.EventMessage:
			if e.Message != nil && e.Message.IsShout() {
				out = append(out, replayChapter{idx: i, label: shoutChapterLabel(e.Message)})
			}
		}
	}
	return out
}

// replayJumpContext scans events BEFORE idx for the state a jump must seed: the
// most recent background and music change, and the last message (so the right
// speaker stands on stage at the landing point). Pure; unit-tested.
func replayJumpContext(events []recEvent, idx int) (bg, music string, lastMsg int) {
	lastMsg = -1
	if idx > len(events) {
		idx = len(events)
	}
	for i := 0; i < idx; i++ {
		switch courtroom.EventKind(events[i].Kind) {
		case courtroom.EventBackground:
			bg = events[i].Text
		case courtroom.EventMusic:
			music = events[i].Text
		case courtroom.EventMessage:
			lastMsg = i
		}
	}
	return bg, music, lastMsg
}

// replayJumpTo restarts the replay AT chapter event idx: the room is rebuilt,
// the jump context (bg + music + the previous speaker, instantly settled) is
// seeded, and playback resumes from idx. The in-between messages are never fed,
// so nothing flashes or blares during the seek.
func (a *App) replayJumpTo(idx int) {
	if a.replayRec == nil || idx < 0 || idx > len(a.replayRec.Events) {
		return
	}
	rec := a.replayRec
	name := a.replayName
	a.startReplay(rec, name) // rebuild from the top (seeds rec.StartBg)
	if !a.replaying || a.replayRoom == nil {
		return
	}
	defer a.recoverReplay("jump")
	bg, music, lastMsg := replayJumpContext(rec.Events, idx)
	if bg != "" {
		a.replayRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: bg})
	}
	if music != "" {
		// Loop: true — replay soundtrack loops, same as eventFromRec (#15 default).
		a.replayRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventMusic, Text: music, Loop: true})
	}
	if lastMsg >= 0 {
		a.replayRoom.HandleEvent(eventFromRec(rec.Events[lastMsg]))
		a.replayRoom.SkipToIdle() // settle the previous speaker instantly (no crawl)
	}
	a.replayIdx = idx
	a.warnLine = fmt.Sprintf("⏩ Jumped to event %d / %d", idx+1, len(rec.Events))
	a.warnAt = time.Now()
}

// drawReplayChapters draws the Chapters toggle + the jump-list panel in the
// overlay player. Rows are plain buttons; clicking one jumps and closes the
// panel. Drawn above the transport strip so it never covers the stage.
func (a *App) drawReplayChapters(stage sdl.Rect, w int32) {
	c := a.ctx
	if len(a.replayChapters) == 0 {
		return
	}
	y := stage.Y + stage.H + 14
	btn := sdl.Rect{X: stage.X + stage.W - 110, Y: y, W: 110, H: 28}
	if c.Button(btn, "☰ Chapters") {
		a.replayChaptersOpen = !a.replayChaptersOpen
	}
	if !a.replayChaptersOpen {
		return
	}
	rowH := int32(22)
	n := int32(len(a.replayChapters))
	maxRows := (stage.H - 20) / rowH
	if n > maxRows {
		n = maxRows // the panel never outgrows the stage; the list is bounded anyway
	}
	pw := int32(300)
	panel := sdl.Rect{X: btn.X + btn.W - pw, Y: btn.Y - n*rowH - 10, W: pw, H: n*rowH + 8}
	if panel.X < stage.X {
		panel.X = stage.X
	}
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)
	for i := int32(0); i < n; i++ {
		ch := a.replayChapters[i]
		r := sdl.Rect{X: panel.X + 4, Y: panel.Y + 4 + i*rowH, W: panel.W - 8, H: rowH - 2}
		if c.hovering(r) {
			c.Fill(r, ColPanelHi)
		}
		cur := " "
		if a.replayIdx > ch.idx {
			cur = "·" // already passed — a subtle progress tick
		}
		c.LabelClipped(r.X+4, r.Y+3, r.W-8, cur+" "+ch.label, ColText)
		if c.clicked && c.hovering(r) {
			a.replayJumpTo(ch.idx)
			a.replayChaptersOpen = false
			return // the jump rebuilt the room — stop touching this frame's list
		}
	}
}
