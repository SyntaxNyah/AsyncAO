package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/archive"
	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// Scene maker (M16 [3/3]): an in-app editor over the SAME .aorec model the
// recorder/replayer use. Build a scene from scratch or load a recording, edit
// the ordered event stream (who speaks, emote, text, background, music), Preview
// it through the existing replay engine, and Save a fresh .aorec. It reuses the
// recording model + the replay renderer, so there is no second renderer and no
// new asset path — and because it's a full-window overlay drawn only while
// makerOpen, it costs nothing on the live render hot path.
//
// A scene is just a list of recEvents, and .aorec is pretty-printed JSON, so a
// scene built here is hand-editable in any text editor too (and vice-versa).

const (
	// defaultMakerEmote is the starter sprite stem for a new line — the bare
	// form, which the prefetch fallback chain probes as both "(a)normal" and
	// "normal" (CLAUDE.md: AO2-Client CharLayer::load_image tries both).
	defaultMakerEmote = "normal"
	// defaultMakerSide centres a new speaker on the witness stand, where a
	// talking character is visible without needing a specific background slot.
	defaultMakerSide = "wit"

	makerListW int32 = 300 // left event-list column width
	makerRowH  int32 = 24  // event-list row height

	// makerCharSuggest caps the character-autocomplete chips. The scan stops
	// once this many matches are found, so it stays cheap even on a 4000-char
	// server (you never list all of them — a dropdown would be unusable there).
	makerCharSuggest = 6
)

// makerSideLabels / makerSideValues map friendly position names to the AO side
// codes the URL builder understands (urlbuilder.go positionFolder).
var (
	makerSideLabels = []string{"Defense", "Prosecution", "Witness", "Judge", "Defense (hold)", "Prosecution (hold)", "Jury", "Seance"}
	makerSideValues = []string{"def", "pro", "wit", "jud", "hld", "hlp", "jur", "sea"}
)

func makerSideIndex(side string) int {
	for i, v := range makerSideValues {
		if v == side {
			return i
		}
	}
	return 2 // default: witness
}

// newMessageEvent builds a synthetic IC line. EmoteMod defaults to idle (NOT
// preanim): a from-scratch line must not wait on a preanim asset that may not
// exist at the origin, which would stall the preview (advisor note on the
// replay-delay thread). The user opts into a preanim explicitly.
func newMessageEvent(char, emote, text string) recEvent {
	if strings.TrimSpace(emote) == "" {
		emote = defaultMakerEmote
	}
	return recEvent{
		Kind: int(courtroom.EventMessage),
		Message: &protocol.ChatMessage{
			CharName: char,
			Emote:    emote,
			Message:  text,
			Side:     defaultMakerSide,
			EmoteMod: protocol.EmoteModIdle,
			DeskMod:  protocol.DeskShow, // stand behind the desk = grounded, natural AO framing (off = full-body character floating on the bg)
		},
	}
}

func newBackgroundEvent(name string) recEvent {
	return recEvent{Kind: int(courtroom.EventBackground), Text: name}
}

func newMusicEvent(track string) recEvent {
	return recEvent{Kind: int(courtroom.EventMusic), Text: track}
}

// sanitizeStem turns a user-typed scene name into a safe filename stem (no path
// separators or odd characters), so Save can never escape recordings\.
func sanitizeStem(s string) string {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), recordingExt))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = "scene"
	}
	return out
}

// cloneScene copies a scene so the maker edits its own buffer: the Events slice
// and each Message pointer are duplicated, so editing a field never reaches back
// into a live recording or a previously loaded file (and a Preview can't mutate
// the edit buffer).
func cloneScene(rec *sceneRecording) *sceneRecording {
	if rec == nil {
		return &sceneRecording{Version: recordingVersion}
	}
	out := *rec
	out.Events = make([]recEvent, len(rec.Events))
	for i, e := range rec.Events {
		out.Events[i] = cloneEvent(e)
	}
	if out.Version == 0 {
		out.Version = recordingVersion
	}
	return &out
}

// cloneEvent deep-copies one event (duplicating the Message pointer so an edit
// to the copy never touches the original).
func cloneEvent(e recEvent) recEvent {
	if e.Message != nil {
		m := *e.Message
		e.Message = &m
	}
	return e
}

// openSceneMaker shows the maker editing a CLONE of rec (so the source is never
// mutated in place). Recording and replay are mutually exclusive with it.
func (a *App) openSceneMaker(rec *sceneRecording, name string) {
	// This runs in a Settings/picker CLICK handler (drawSettings), which is NOT
	// recover-wrapped — so a panic here (cloneScene / ensureBgList / …) would
	// hard-crash the app with no log, which is the reported "Edit crashes, no
	// crash log" symptom (recoverMaker only guards the maker's own draw). Catch +
	// log it here so the cause finally names itself and the app survives.
	defer func() {
		if r := recover(); r != nil {
			a.warnLine = a.logMakerCrash(r)
			a.warnAt = time.Now()
			a.makerOpen = false
			a.makerScene = nil
		}
	}()
	if a.recActive {
		a.warnLine = "Stop recording first, then open the Scene Maker."
		a.warnAt = time.Now()
		return
	}
	if a.replaying {
		a.stopReplay()
	}
	a.makerScene = cloneScene(rec)
	a.makerName = sanitizeStem(name)
	a.makerSel = 0
	a.makerScroll = 0
	a.makerPickerOpen = false
	a.makerExportOpen = false
	a.makerTrimStart = -1 // no crop until the user sets In/Out
	a.makerTrimEnd = -1
	a.makerDragSeg = -1    // no reorder drag in progress
	a.makerPreviewIdx = -1 // force the preview pane to rebuild for the new scene
	a.makerOpen = true
	a.ensureBgList() // warm the background list once so the BG picker has suggestions
}

// newScene opens the maker on a fresh one-line scene, seeded with the asset
// origin of the connected server (if any) and the player's current character —
// so "New scene → Preview" shows a real sprite out of the box where possible.
func (a *App) newScene() {
	origin := a.urls.Origin() // the connected server's asset base, "" when offline
	bg := ""
	if a.sess != nil {
		bg = a.sess.Background // seed the live background so Preview lands on a real scene (with a desk), not a character floating on black
	}
	rec := &sceneRecording{
		Version: recordingVersion,
		Origin:  origin,
		StartBg: bg,
		Events:  []recEvent{newMessageEvent(a.activeCharName(), defaultMakerEmote, "Hello!")},
	}
	a.openSceneMaker(rec, "untitled")
}

// editRecordingInMaker loads a saved .aorec into the maker for editing (the
// Studio "Edit" entry point).
func (a *App) editRecordingInMaker(path string) {
	rec, err := loadRecording(path)
	if err != nil {
		a.warnLine = "Couldn't open recording: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	a.openSceneMaker(rec, strings.TrimSuffix(filepath.Base(path), recordingExt))
}

// closeSceneMaker hides the maker, frees its buffer, and tears down the preview
// room (restoring the viewport's preanim callback to the live room). The
// dispatch then falls back to whatever screen was underneath.
func (a *App) closeSceneMaker() {
	a.makerOpen = false
	a.makerScene = nil
	a.makerSel = 0
	a.makerScroll = 0
	a.makerPickerOpen = false
	a.makerExportOpen = false
	a.makerTrimStart = -1
	a.makerTrimEnd = -1
	a.makerDragSeg = -1
	a.makerDragMoved = false
	a.teardownMakerPreview()
}

// makerPreviewKey is the VISUAL identity of the previewed line — everything that
// changes what the pane should draw EXCEPT the message text (so typing doesn't
// re-trigger a rebuild on every keystroke). Compared field-by-field each frame,
// alloc-free.
type makerPreviewKey struct {
	bg, char, emote, side, pre string
	emoteMod, deskMod, kind    int
	offX, offY                 int
	flip, shake, flash         bool
}

// makerPreviewKeyFor computes the preview key for the selected event: the
// effective background (the selected BG itself, else the most recent BG before
// it, else StartBg) plus the message's visual fields.
func (a *App) makerPreviewKeyFor(sel int) makerPreviewKey {
	rec := a.makerScene
	e := rec.Events[sel]
	k := makerPreviewKey{kind: e.Kind}
	if courtroom.EventKind(e.Kind) == courtroom.EventBackground {
		k.bg = e.Text
	} else {
		k.bg = rec.StartBg
		for j := sel - 1; j >= 0; j-- {
			if courtroom.EventKind(rec.Events[j].Kind) == courtroom.EventBackground {
				k.bg = rec.Events[j].Text
				break
			}
		}
	}
	if m := e.Message; m != nil {
		k.char, k.emote, k.side, k.pre = m.CharName, m.Emote, m.Side, m.PreEmote
		k.emoteMod, k.deskMod, k.flip = m.EmoteMod, m.DeskMod, m.Flip
		k.offX, k.offY = m.SelfOffsetX, m.SelfOffsetY
		k.shake, k.flash = m.Screenshake, m.Realization // so the pane replays when an effect is toggled
	}
	return k
}

// ensureMakerPreview (re)builds the preview-pane room so it reflects the selected
// line — only when the origin, selection, or the line's visual identity changed
// (so it's idle most frames). Feeds the background context then the selected
// message, so the pane shows that line type out and settle, like the real stage.
func (a *App) ensureMakerPreview() {
	if a.makerScene == nil || a.d.Manager == nil {
		return
	}
	sel := a.makerSel
	if sel < 0 || sel >= len(a.makerScene.Events) {
		return
	}
	key := a.makerPreviewKeyFor(sel)
	if a.makerPreviewRoom != nil && a.makerPreviewOrig == a.makerScene.Origin &&
		a.makerPreviewIdx == sel && a.makerPreviewKey == key {
		return // unchanged — keep playing the current preview
	}
	room := courtroom.NewCourtroom(courtroom.NewURLBuilder(a.makerScene.Origin), a.d.Manager, nil, a.d.Audio)
	room.Typewriter.Interval, room.TextStay = a.replayTiming()
	room.CatchUp = false
	room.ReduceMotion = false // authored WYSIWYG: show the line's screenshake/flash even if live reduce-motion is on
	room.ForceCharNames = a.d.Prefs.ForceCharNamesOn()
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = room.NotifyPreanimDone
	}
	if key.bg != "" {
		room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: key.bg})
	}
	if e := a.makerScene.Events[sel]; courtroom.EventKind(e.Kind) == courtroom.EventMessage && e.Message != nil {
		room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: cloneEvent(e).Message}) // copy: the room must not alias the edit buffer
	}
	a.makerPreviewRoom = room
	a.makerPreviewOrig = a.makerScene.Origin
	a.makerPreviewIdx = sel
	a.makerPreviewKey = key
}

// driveMakerPreview advances the preview-pane room each frame (build-on-change +
// Update + viewport sync), wrapped so a bad scene/asset can't crash the maker.
func (a *App) driveMakerPreview(dt time.Duration) {
	defer a.recoverMakerPreview()
	if a.makerPickerOpen { // the picker covers the body — no pane to drive
		return
	}
	a.ensureMakerPreview()
	if a.makerPreviewRoom == nil {
		return
	}
	if a.d.Viewport != nil {
		// Re-assert each frame: a full ▶ Preview (replay) rebinds this to the
		// replay room and stopReplay points it at the live room — neither is right
		// once we're back in the pane.
		a.d.Viewport.OnPreanimDone = a.makerPreviewRoom.NotifyPreanimDone
		a.d.Viewport.SetSpriteFX(a.spriteFX())
	}
	a.makerPreviewRoom.Update(dt)
	if a.d.Viewport != nil {
		a.d.Viewport.Update(&a.makerPreviewRoom.Scene, dt)
	}
}

// recoverMakerPreview turns a panic in the preview drive/render into a clean
// teardown + debug line, never an app crash (the pane is optional).
func (a *App) recoverMakerPreview() {
	if r := recover(); r != nil {
		a.pushDebug("maker preview panic: " + fmt.Sprint(r))
		a.teardownMakerPreview()
	}
}

// recoverMaker turns a panic anywhere in the maker DRAW (list / editor / actions —
// the parts the preview's own recover doesn't cover) into a debug line + a clean
// close, never an app crash. It also SURFACES the cause: the panic value lands in
// the debug overlay so a hard-to-reproduce edit crash names itself.
func (a *App) recoverMaker() {
	if r := recover(); r != nil {
		a.warnLine = a.logMakerCrash(r)
		a.warnAt = time.Now()
		a.closeSceneMaker()
	}
}

// logMakerCrash records a recovered maker panic: it pushes a debug line and
// writes the panic + full stack to recordings\scene-maker-crash.log so the exact
// site is pinpointed (a hard-to-reproduce crash names itself). One-shot synchronous
// write on the exceptional recover path (not the steady render path). Returns the
// toast string the caller should show. Shared by the draw recover AND the
// open-from-Settings recover (the crash that produced no log was in the latter,
// unrecovered, path).
func (a *App) logMakerCrash(r any) string {
	msg := fmt.Sprint(r)
	a.pushDebug("scene maker panic: " + msg)
	writeCrashLog("scene maker panic: ", r)
	return "Scene Maker error: " + msg + " — details saved to recordings\\scene-maker-crash.log"
}

// writeCrashLog best-effort writes a panic + full stack to
// recordings\scene-maker-crash.log. It touches only os/debug, so it's safe to
// call from ANY goroutine (the background bg-list fetch can't recover up the main
// stack, so it logs here directly).
func writeCrashLog(header string, r any) {
	dir := recordingsDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "scene-maker-crash.log"),
		[]byte(header+fmt.Sprint(r)+"\n\n"+string(debug.Stack())), 0o644)
}

// drawMakerPreviewPane renders the selected line into a 4:3 stage — the WYSIWYG
// "studio" view, so you see your scene as you build it.
func (a *App) drawMakerPreviewPane(x, y, w, h int32) {
	defer a.recoverMakerPreview()
	c := a.ctx
	c.Label(x, y, "Live preview — selected line:", ColTextDim)
	y += 22
	stageW := w
	stageH := stageW * 3 / 4
	if stageH > h-44 {
		stageH = h - 44
		stageW = stageH * 4 / 3
	}
	stage := sdl.Rect{X: x + (w-stageW)/2, Y: y, W: stageW, H: stageH}
	c.Fill(stage, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	if a.makerPreviewRoom == nil || a.d.Viewport == nil {
		c.Label(stage.X+10, stage.Y+10, "Set an Origin/CDN above to preview.", ColTextDim)
		return
	}
	a.d.Viewport.Render(c.Ren, &a.makerPreviewRoom.Scene, stage)
	sc := &a.makerPreviewRoom.Scene
	if sc.ShownameText != "" || sc.MessageText != "" {
		c.LabelClipped(stage.X, stage.Y+stage.H+4, stage.W, strings.TrimSpace(sc.ShownameText+": "+sc.MessageText), ColText)
	}
}

// teardownMakerPreview disposes the preview-pane room and restores the
// viewport's one-shot preanim callback to the live room (or clears it).
func (a *App) teardownMakerPreview() {
	a.makerPreviewRoom = nil
	a.makerPreviewOrig = ""
	a.makerPreviewIdx = -1
	if a.d.Viewport != nil {
		if a.room != nil {
			a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
		} else {
			a.d.Viewport.OnPreanimDone = nil
		}
	}
}

// makerInsert adds an event just after the selection (bounded by the recording
// cap) and selects it.
func (a *App) makerInsert(ev recEvent) {
	if a.makerScene == nil {
		return
	}
	if len(a.makerScene.Events) >= maxRecordedEvents {
		a.warnLine = "Scene is at the event cap (" + fmt.Sprint(maxRecordedEvents) + ")."
		a.warnAt = time.Now()
		return
	}
	at := a.makerSel + 1
	if at < 0 || at > len(a.makerScene.Events) {
		at = len(a.makerScene.Events)
	}
	evs := append(a.makerScene.Events, recEvent{})
	copy(evs[at+1:], evs[at:])
	evs[at] = ev
	a.makerScene.Events = evs
	a.makerSel = at
}

// makerDuplicateSel clones the selected event right after it (the fastest way to
// build a back-and-forth: dup, then tweak the copy).
func (a *App) makerDuplicateSel() {
	if a.makerScene == nil {
		return
	}
	i := a.makerSel
	if i < 0 || i >= len(a.makerScene.Events) {
		return
	}
	a.makerInsert(cloneEvent(a.makerScene.Events[i]))
}

// makerNewLineSeed builds the event a fresh "+ Line" inserts. It INHERITS the
// speaker of the line you're inserting after — char/emote/position/colour/flip/
// desk — so a conversation doesn't make you retype the character every line; you
// just edit the text (and swap the character when the speaker changes). Falls
// back to your current character for the very first line.
func (a *App) makerNewLineSeed() recEvent {
	if a.makerScene != nil {
		i := a.makerSel
		if i >= 0 && i < len(a.makerScene.Events) {
			if m := a.makerScene.Events[i].Message; m != nil {
				return recEvent{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{
					CharName:  m.CharName,
					Emote:     m.Emote,
					Side:      m.Side,
					TextColor: m.TextColor,
					Flip:      m.Flip,
					DeskMod:   m.DeskMod,
					EmoteMod:  protocol.EmoteModIdle, // a fresh line, not a preanim
				}}
			}
		}
	}
	return newMessageEvent(a.activeCharName(), defaultMakerEmote, "")
}

// makerPreviewFrom previews the scene starting at idx — so you can iterate on a
// late line without watching the whole thing. It carries the most recent
// background before idx so the partial replay isn't blank.
func (a *App) makerPreviewFrom(idx int) {
	if a.makerScene == nil || idx < 0 || idx >= len(a.makerScene.Events) {
		return
	}
	sub := cloneScene(a.makerScene)
	for j := idx - 1; j >= 0; j-- { // last background change before idx becomes the start scene
		if courtroom.EventKind(sub.Events[j].Kind) == courtroom.EventBackground {
			sub.StartBg = sub.Events[j].Text
			break
		}
	}
	sub.Events = sub.Events[idx:]
	a.startReplay(sub, a.makerName+" (from line "+strconv.Itoa(idx+1)+")")
}

// trimActive reports whether a crop In or Out point is set (so the export /
// preview should use a sub-range, not the whole scene).
func (a *App) trimActive() bool {
	return a.makerTrimStart >= 0 || a.makerTrimEnd >= 0
}

// trimRange resolves the In/Out points to a concrete, in-bounds [start, end]
// (inclusive) over the current events: an unset/out-of-range end means "to the
// end", and an inverted range (after deletes/reorders) degrades to the whole
// scene rather than producing an empty export.
func (a *App) trimRange() (int, int) {
	n := 0
	if a.makerScene != nil {
		n = len(a.makerScene.Events)
	}
	if n == 0 {
		return 0, -1
	}
	s, e := a.makerTrimStart, a.makerTrimEnd
	if s < 0 || s >= n {
		s = 0
	}
	if e < 0 || e >= n {
		e = n - 1
	}
	if s > e {
		s, e = 0, n-1
	}
	return s, e
}

// inTrim reports whether event i is inside the crop range (always true when no
// crop is set) — used to dim the excluded rows in the list.
func (a *App) inTrim(i int) bool {
	if !a.trimActive() {
		return true
	}
	s, e := a.trimRange()
	return i >= s && i <= e
}

// trimmedScene returns a clone of the maker scene clipped to the crop range (or
// the whole scene when no crop is set). The clone is always fresh so a Preview /
// export can't mutate the edit buffer; StartBg is carried forward to the
// background in effect at the crop start so a mid-scene crop isn't blank (same as
// "Preview from this line"). Music before the crop is not carried (visual crop).
func (a *App) trimmedScene() *sceneRecording {
	sub := cloneScene(a.makerScene)
	if !a.trimActive() || len(sub.Events) == 0 {
		return sub
	}
	s, e := a.trimRange()
	for j := s - 1; j >= 0; j-- { // last background change before the crop becomes the start scene
		if courtroom.EventKind(sub.Events[j].Kind) == courtroom.EventBackground {
			sub.StartBg = sub.Events[j].Text
			break
		}
	}
	sub.Events = sub.Events[s : e+1]
	return sub
}

// makerDeleteSel removes the selected event.
func (a *App) makerDeleteSel() {
	if a.makerScene == nil {
		return
	}
	i := a.makerSel
	if i < 0 || i >= len(a.makerScene.Events) {
		return
	}
	a.makerScene.Events = append(a.makerScene.Events[:i], a.makerScene.Events[i+1:]...)
	if a.makerSel >= len(a.makerScene.Events) {
		a.makerSel = len(a.makerScene.Events) - 1
	}
	if a.makerSel < 0 {
		a.makerSel = 0
	}
}

// makerMoveSel moves the selection one slot in dir (the ▲/▼ buttons), delegating
// to the shared reorder path so the crop endpoints follow the move too. An
// out-of-range neighbour is a no-op (makerMoveEvent bounds-checks dst).
func (a *App) makerMoveSel(dir int) {
	if a.makerScene == nil {
		return
	}
	a.makerMoveEvent(a.makerSel, a.makerSel+dir)
}

// reindexAfterMove maps an index p to where it lands after the event at src is
// moved to dst (every other event keeping its relative order). Pure helper so the
// reorder can carry the selection AND the crop In/Out with their events, not their
// old slots. Tested directly.
func reindexAfterMove(p, src, dst int) int {
	switch {
	case p == src:
		return dst
	case src < dst && p > src && p <= dst:
		return p - 1
	case src > dst && p >= dst && p < src:
		return p + 1
	default:
		return p
	}
}

// makerMoveEvent moves the event at src to index dst within the scene, preserving
// every other event's order, and reindexes the selection + crop In/Out so they
// keep pointing at the SAME events. The single reorder path: the ▲/▼ buttons and
// the timeline drag both go through it.
func (a *App) makerMoveEvent(src, dst int) {
	if a.makerScene == nil {
		return
	}
	evs := a.makerScene.Events
	n := len(evs)
	if src < 0 || src >= n || dst < 0 || dst >= n || src == dst {
		return
	}
	ev := evs[src]
	if src < dst {
		copy(evs[src:dst], evs[src+1:dst+1]) // shift (src,dst] left one
	} else {
		copy(evs[dst+1:src+1], evs[dst:src]) // shift [dst,src) right one
	}
	evs[dst] = ev
	a.makerSel = reindexAfterMove(a.makerSel, src, dst)
	if a.makerTrimStart >= 0 {
		a.makerTrimStart = reindexAfterMove(a.makerTrimStart, src, dst)
	}
	if a.makerTrimEnd >= 0 {
		a.makerTrimEnd = reindexAfterMove(a.makerTrimEnd, src, dst)
	}
}

// makerPreview plays the scene through the replay engine on a copy, so playback
// can't mutate the edit buffer; on ■ Stop the maker (still makerOpen) reappears
// with the edit intact.
func (a *App) makerPreview() {
	if a.makerScene == nil || len(a.makerScene.Events) == 0 {
		a.warnLine = "Nothing to preview yet — add a line first."
		a.warnAt = time.Now()
		return
	}
	name := a.makerName + " (preview)"
	if a.trimActive() {
		s, e := a.trimRange()
		name = fmt.Sprintf("%s (crop %d–%d)", a.makerName, s+1, e+1)
	}
	a.startReplay(a.trimmedScene(), name) // honours the crop range
}

// makerSave writes the scene as a NEW timestamped .aorec — never overwriting the
// file you loaded (the user's recordings aren't ours to clobber). The write is
// off the render thread (saveSceneRecording, §17.2).
func (a *App) makerSave() {
	if a.makerScene == nil || len(a.makerScene.Events) == 0 {
		a.warnLine = "Nothing to save yet — add a line first."
		a.warnAt = time.Now()
		return
	}
	a.makerScene.Version = recordingVersion
	if a.makerScene.RecordedAt == "" {
		a.makerScene.RecordedAt = time.Now().Format(time.RFC3339)
	}
	stem := sanitizeStem(a.makerName) + "-" + time.Now().Format("20060102-150405")
	name, err := saveSceneRecording(a.makerScene, stem)
	if err != nil {
		a.warnLine = "Save failed: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	a.warnLine = "Scene saved: recordings\\" + name
	a.warnAt = time.Now()
}

// exportSceneArchive bundles the scene's assets into a self-contained folder so
// the .aorec keeps its visuals when the origin CDN dies. It downloads through the
// live Manager (the same one streaming the session), so it must run connected to
// the origin — off the UI thread, since it fetches many assets.
func (a *App) exportSceneArchive(scene *sceneRecording, name string) {
	if a.makerExporting {
		return
	}
	if scene == nil || len(scene.Events) == 0 || strings.TrimSpace(scene.Origin) == "" {
		a.warnLine = "Set the Origin/CDN and add at least one line before exporting an archive."
		a.warnAt = time.Now()
		return
	}
	snap := cloneScene(scene) // the goroutine must not race the edit buffer
	stem := sanitizeStem(name) + "-archive-" + time.Now().Format("20060102-150405")
	if a.makerExportCh == nil {
		a.makerExportCh = make(chan string, 1)
	}
	a.makerExporting = true
	a.warnLine = "Exporting self-contained archive… downloading assets (this can take a moment)."
	a.warnAt = time.Now()
	mgr := a.d.Manager
	go func() { a.makerExportCh <- runArchiveExport(mgr, snap, stem) }()
}

// runArchiveExport (off-thread) resolves+writes every asset the scene needs into
// recordings\<stem>\ and drops a bundled .aorec beside them. Returns a status
// line for the UI.
func runArchiveExport(mgr *assets.Manager, scene *sceneRecording, stem string) string {
	dir := recordingsDir()
	if dir == "" {
		return "Export failed: no recordings folder."
	}
	destDir := filepath.Join(dir, stem)
	events := make([]courtroom.Event, len(scene.Events))
	for i, e := range scene.Events {
		events[i] = eventFromRec(e)
	}
	res, err := archive.ExportAssets(context.Background(), mgr, scene.Origin, scene.StartBg, events, destDir)
	if err != nil {
		return "Export failed: " + err.Error()
	}
	out := cloneScene(scene)
	out.Bundled = true
	out.Formats = res.Formats
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "Export failed: " + err.Error()
	}
	if err := os.WriteFile(filepath.Join(destDir, stem+recordingExt), data, 0o644); err != nil {
		return "Export failed: " + err.Error()
	}
	return fmt.Sprintf("Archive exported (plays without the CDN): recordings\\%s — %d files, %.1f MB.",
		stem, res.Files, float64(res.Bytes)/(1024*1024))
}

// pollMakerExport delivers the archive-export result back to the UI (called each
// frame from Update).
func (a *App) pollMakerExport() {
	if !a.makerExporting || a.makerExportCh == nil {
		return
	}
	select {
	case msg := <-a.makerExportCh:
		a.makerExporting = false
		a.warnLine = msg
		a.warnAt = time.Now()
	default:
	}
}

// eventSummary returns a short tag + one-line description for the event list.
func eventSummary(e recEvent) (tag, text string) {
	switch courtroom.EventKind(e.Kind) {
	case courtroom.EventMessage:
		tag = "MSG"
		if e.Message != nil {
			name := e.Message.CharName
			if e.Message.Showname != "" {
				name = e.Message.Showname
			}
			text = strings.TrimSpace(name + ": " + e.Message.Message)
		}
	case courtroom.EventBackground:
		tag, text = "BG", e.Text
	case courtroom.EventMusic:
		tag, text = "MUS", e.Text
	default:
		tag = "?"
	}
	return tag, text
}

// drawSceneMaker paints the full-window editor overlay (dispatched instead of a
// screen while makerOpen, so it owns input). Left: the event list + add/reorder/
// delete. Right: the per-event editor. Top: name, origin, Preview / Save / New.
func (a *App) drawSceneMaker(winW, winH int32) {
	defer a.recoverMaker() // a draw panic closes the maker + names itself, never kills the app
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: winW, H: winH}, ColBackground)
	if a.makerScene == nil { // defensive: never draw an open maker with no scene
		a.newScene()
	}
	// Mouse-press edge for the timeline crop-handle grab (computed once per frame
	// so a handle is grabbed on press, not when a drag from elsewhere drifts in).
	press := c.mouseDown && !a.makerPrevDown
	a.makerPrevDown = c.mouseDown

	y := tabBarH + int32(8)
	c.Label(pad, y, "🎬 Scene Maker — build or edit a scene, then Preview / Save", ColText)
	if c.Button(sdl.Rect{X: winW - pad - 92, Y: y - 4, W: 92, H: btnH}, "✖ Close") {
		a.closeSceneMaker()
		return
	}
	y += 28

	// Name + Origin/CDN.
	c.Label(pad, y+5, "Name:", ColText)
	a.makerName, _ = c.TextField("mk_name", sdl.Rect{X: pad + 52, Y: y, W: 200, H: fieldH}, a.makerName, "scene name")
	c.Label(pad+266, y+5, "Origin/CDN:", ColText)
	originX := pad + 352
	originW := winW - originX - pad
	if originW < 160 {
		originW = 160
	}
	a.makerScene.Origin, _ = c.TextField("mk_origin", sdl.Rect{X: originX, Y: y, W: originW, H: fieldH}, a.makerScene.Origin, "https://your-cdn/base  — where characters/ and background/ live")
	y += 34

	// Actions.
	bx := pad
	if c.Button(sdl.Rect{X: bx, Y: y, W: 104, H: btnH}, "▶ Preview") {
		a.makerPreview()
		return // a Preview takes over the window this frame
	}
	bx += 112
	if c.Button(sdl.Rect{X: bx, Y: y, W: 88, H: btnH}, "📂 Open") {
		a.makerPickerOpen = !a.makerPickerOpen
		a.makerExportOpen = false
	}
	bx += 96
	if c.Button(sdl.Rect{X: bx, Y: y, W: 120, H: btnH}, "💾 Save .aorec") {
		a.makerSave()
	}
	bx += 128
	if a.makerExporting {
		c.Label(bx, y+6, "📦 exporting…", ColAccent)
	} else if c.Button(sdl.Rect{X: bx, Y: y, W: 136, H: btnH}, "📦 Export archive") {
		a.exportSceneArchive(a.makerScene, a.makerName) // CDN-proof: bundles the assets
	}
	bx += 144
	if c.Button(sdl.Rect{X: bx, Y: y, W: 120, H: btnH}, "🎞 Export GIF") {
		a.startSceneExport(a.trimmedScene(), a.makerName, exportGIF) // honours the crop range
	}
	bx += 128
	if c.Button(sdl.Rect{X: bx, Y: y, W: 104, H: btnH}, "🎬 WebP") {
		a.startSceneExport(a.trimmedScene(), a.makerName, exportWebP) // higher-quality animated WebP, cropped
	}
	bx += 112
	if c.Button(sdl.Rect{X: bx, Y: y, W: 104, H: btnH}, "🎥 Video") {
		a.startSceneExport(a.trimmedScene(), a.makerName, exportVideo) // MP4/WebM via ffmpeg (format in ⚙ Export)
	}
	bx += 112
	if c.Button(sdl.Rect{X: bx, Y: y, W: 110, H: btnH}, "🖼 Comic") {
		a.startSceneExport(a.trimmedScene(), a.makerName, exportComic) // PNG storyboard page (pure Go), cropped
	}
	bx += 118
	if c.Button(sdl.Rect{X: bx, Y: y, W: 104, H: btnH}, "⚙ Export") {
		a.makerExportOpen = !a.makerExportOpen // size / fps / quality / loop / speed
	}
	bx += 112
	if c.Button(sdl.Rect{X: bx, Y: y, W: 108, H: btnH}, "🆕 New scene") {
		a.newScene()
	}
	bx += 116
	if c.Button(sdl.Rect{X: bx, Y: y, W: 96, H: btnH}, "📁 Folder") {
		a.openRecordingsFolder()
	}
	y += btnH + 8
	c.Fill(sdl.Rect{X: pad, Y: y, W: winW - 2*pad, H: 1}, ColPanelHi)
	y += 8

	bodyY := y
	if a.makerPickerOpen { // the in-maker "Open a recording" list replaces the body
		a.drawMakerOpenPicker(pad, bodyY, winW-2*pad, winH-bodyY-pad)
		return
	}
	if a.makerExportOpen { // the in-maker export-options panel replaces the body
		a.drawMakerExportPanel(pad, bodyY)
		return
	}
	// Reserve the bottom strip for the timeline; the columns fill above it.
	tlY := winH - pad - makerTimelineH
	bodyH := tlY - bodyY - 10
	if bodyH < makerRowH*4 { // keep the columns usable on a short window
		bodyH = makerRowH * 4
	}
	a.drawMakerList(pad, bodyY, makerListW, bodyH)
	edX := pad + makerListW + 16
	edRight := winW - pad
	// Far-right live preview pane, when the window is wide enough to show it
	// without crushing the editor (keep >= makerEditorMinW for the fields).
	const makerPaneW, makerEditorMinW = 380, 320
	if winW-edX-16-makerPaneW >= makerEditorMinW {
		paneX := winW - pad - makerPaneW
		a.drawMakerPreviewPane(paneX, bodyY, makerPaneW, bodyH)
		edRight = paneX - 16
	}
	a.drawMakerEditor(edX, bodyY, edRight-edX)
	a.drawMakerTimeline(pad, tlY, winW-2*pad, press)
}

// drawMakerOpenPicker lists saved recordings to load straight into the maker
// (the "import existing recordings to edit" ask) — no trip out to Settings.
// drawMakerExportPanel is the in-maker "⚙ Export options" body: the shared export
// controls (size / fps / quality / loop / speed) plus guidance. The 🎞 GIF / 🎬
// WebP buttons in the actions row run the export with these settings.
func (a *App) drawMakerExportPanel(x, y int32) {
	c := a.ctx
	c.Label(x, y, "⚙ Export options — apply to 🎞 GIF / 🎬 WebP (buttons above)", ColAccent)
	y += 30
	y = a.drawExportOptions(y, true)
	y += 10
	c.Label(x, y, "Bigger size / higher frame-rate = a larger file and a shorter GIF (WebP can run longer).", ColTextDim)
	y += 20
	c.Label(x, y, "Tip: screenshake and busy animated backgrounds bloat a GIF — every pixel changes each frame, so it can't compress.", ColTextDim)
}

func (a *App) drawMakerOpenPicker(x, y, w, h int32) {
	c := a.ctx
	recs := listRecordings()
	c.Label(x, y, fmt.Sprintf("Open a recording to edit (%d found) — click one, or Cancel:", len(recs)), ColText)
	if c.Button(sdl.Rect{X: x + w - 90, Y: y - 4, W: 90, H: btnH}, "Cancel") {
		a.makerPickerOpen = false
		return
	}
	y += 28
	if len(recs) == 0 {
		c.Label(x, y, "No recordings yet — record a scene (Ctrl+W) or build one with 🆕 New scene.", ColTextDim)
		return
	}
	panel := sdl.Rect{X: x, Y: y, W: w, H: h - 28}
	c.Fill(panel, ColPanel)
	rowH := int32(26)
	maxRows := (panel.H - 8) / rowH
	for i := range recs {
		if int32(i) >= maxRows {
			c.Label(x+8, y+int32(maxRows)*rowH+2, fmt.Sprintf("… and %d more (use the 📁 Folder button).", len(recs)-int(maxRows)), ColTextDim)
			break
		}
		row := sdl.Rect{X: x + 4, Y: y + 4 + int32(i)*rowH, W: w - 8, H: rowH - 2}
		if c.hovering(row) && c.clicked {
			a.editRecordingInMaker(recs[i].path) // loads + closes the picker (openSceneMaker)
		}
		if c.hovering(row) {
			c.Fill(row, ColPanelHi)
		}
		c.LabelClipped(row.X+8, row.Y+4, row.W-16, recs[i].name, ColText)
	}
}

// drawMakerList renders the scrollable event list and its add/reorder/delete
// toolbar.
func (a *App) drawMakerList(x, y, w, h int32) {
	c := a.ctx
	evs := a.makerScene.Events
	c.Label(x, y, fmt.Sprintf("Events (%d) — click to edit:", len(evs)), ColText)
	y += 24

	const toolH = 170 // reserved for the toolbar rows (add / reorder / preview / crop) below the list
	listH := h - toolH
	if listH < makerRowH*3 {
		listH = makerRowH * 3
	}
	listRect := sdl.Rect{X: x, Y: y, W: w, H: listH}
	c.Fill(listRect, ColPanel)

	contentH := int32(len(evs)) * makerRowH
	maxScroll := contentH - listH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if c.hovering(listRect) && c.wheelY != 0 {
		c.wheelTaken = true
		a.makerScroll -= int32(c.wheelY) * makerRowH
	}
	if a.makerScroll > maxScroll {
		a.makerScroll = maxScroll
	}
	if a.makerScroll < 0 {
		a.makerScroll = 0
	}

	for i := range evs {
		ry := y + int32(i)*makerRowH - a.makerScroll
		if ry+makerRowH < y || ry > y+listH { // cull rows outside the list window
			continue
		}
		rowRect := sdl.Rect{X: x, Y: ry, W: w, H: makerRowH - 2}
		if i == a.makerSel {
			c.Fill(rowRect, ColPanelHi)
		}
		if c.hovering(rowRect) && c.clicked {
			a.makerSel = i
		}
		// Crop visuals: in-range rows get an accent stripe; excluded rows dim.
		tag, text := eventSummary(evs[i])
		tagCol, textCol := ColAccent, ColText
		if a.trimActive() {
			if a.inTrim(i) {
				c.Fill(sdl.Rect{X: x, Y: ry, W: 3, H: makerRowH - 2}, ColAccent)
			} else {
				c.Fill(rowRect, sdl.Color{R: 0, G: 0, B: 0, A: 110})
				tagCol, textCol = ColTextDim, ColTextDim
			}
		}
		c.Label(x+8, ry+4, fmt.Sprintf("%d", i+1), ColTextDim)
		c.Label(x+34, ry+4, tag, tagCol)
		c.LabelClipped(x+76, ry+4, w-82, text, textCol)
	}

	ty := y + listH + 8
	if c.Button(sdl.Rect{X: x, Y: ty, W: 62, H: btnH}, "+ Line") {
		a.makerInsert(a.makerNewLineSeed()) // inherits the previous speaker
	}
	if c.Button(sdl.Rect{X: x + 68, Y: ty, W: 56, H: btnH}, "+ BG") {
		a.makerInsert(newBackgroundEvent(""))
	}
	if c.Button(sdl.Rect{X: x + 130, Y: ty, W: 72, H: btnH}, "+ Music") {
		a.makerInsert(newMusicEvent(""))
	}
	ty += btnH + 6
	if c.Button(sdl.Rect{X: x, Y: ty, W: 40, H: btnH}, "▲") {
		a.makerMoveSel(-1)
	}
	if c.Button(sdl.Rect{X: x + 46, Y: ty, W: 40, H: btnH}, "▼") {
		a.makerMoveSel(1)
	}
	if c.Button(sdl.Rect{X: x + 92, Y: ty, W: 56, H: btnH}, "⎘ Dup") {
		a.makerDuplicateSel()
	}
	if c.Button(sdl.Rect{X: x + 154, Y: ty, W: 56, H: btnH}, "✖ Del") {
		a.makerDeleteSel()
	}
	ty += btnH + 6
	if c.Button(sdl.Rect{X: x, Y: ty, W: 184, H: btnH}, "▶ Preview from this line") {
		a.makerPreviewFrom(a.makerSel)
	}
	// Crop / trim: set In/Out around the funny moment; Preview + Export use the
	// range, and the excluded lines dim above (delete with ✖ Del to cut for good).
	ty += btnH + 6
	if c.Button(sdl.Rect{X: x, Y: ty, W: 76, H: btnH}, "⏮ Set In") {
		a.makerTrimStart = a.makerSel
		if a.makerTrimEnd >= 0 && a.makerTrimEnd < a.makerTrimStart {
			a.makerTrimEnd = -1 // Out before In is meaningless — clear it
		}
	}
	if c.Button(sdl.Rect{X: x + 82, Y: ty, W: 84, H: btnH}, "⏭ Set Out") {
		a.makerTrimEnd = a.makerSel
		if a.makerTrimStart > a.makerTrimEnd {
			a.makerTrimStart = -1
		}
	}
	if a.trimActive() {
		if c.Button(sdl.Rect{X: x + 172, Y: ty, W: 64, H: btnH}, "✕ Crop") {
			a.makerTrimStart, a.makerTrimEnd = -1, -1
		}
	}
	ty += btnH + 4
	if a.trimActive() {
		s, e := a.trimRange()
		c.Label(x, ty, fmt.Sprintf("Crop: lines %d–%d of %d — Preview / Export use this range", s+1, e+1, len(evs)), ColAccent)
	} else {
		c.Label(x, ty, "Crop: full scene — pick a line, then ⏮ Set In / ⏭ Set Out", ColTextDim)
	}
}

// drawCharSuggestions shows up to makerCharSuggest server characters whose name
// contains the typed text, as click-to-fill chips under the Character field —
// the searchable picker (a flat dropdown can't list a 4000-char server). Hidden
// when offline or when the field is empty. Returns the new y (== y when hidden).
// The scan stops after makerCharSuggest hits and uses an alloc-free fold match,
// so it costs almost nothing even with the field open on a huge roster.
func (a *App) drawCharSuggestions(x, y, leftX, availW int32, m *protocol.ChatMessage) int32 {
	typed := strings.TrimSpace(m.CharName)
	if a.sess == nil || typed == "" {
		return y
	}
	var buf [makerCharSuggest]string
	n := 0
	for i := 0; i < len(a.sess.Chars) && n < makerCharSuggest; i++ {
		name := a.sess.Chars[i].Name
		if strings.EqualFold(name, typed) {
			continue // don't suggest the exact thing already typed
		}
		if containsFold(name, typed) {
			buf[n] = name
			n++
		}
	}
	return a.drawSuggestionChips(x, y, leftX, availW, buf[:n], &m.CharName)
}

// drawBgSuggestions is drawCharSuggestions for backgrounds: click-to-fill chips
// from the server's discovered background list (a.bgPick.server, the same names
// the background picker shows), so a BG line isn't a guess. Free-text stays for
// names a host's autoindex doesn't list.
func (a *App) drawBgSuggestions(x, y, leftX, availW int32, target *string) int32 {
	typed := strings.TrimSpace(*target)
	if typed == "" || len(a.bgPick.server) == 0 {
		return y
	}
	var buf [makerCharSuggest]string
	n := 0
	for i := 0; i < len(a.bgPick.server) && n < makerCharSuggest; i++ {
		name := a.bgPick.server[i]
		if strings.EqualFold(name, typed) {
			continue
		}
		if containsFold(name, typed) {
			buf[n] = name
			n++
		}
	}
	return a.drawSuggestionChips(x, y, leftX, availW, buf[:n], target)
}

// drawSuggestionChips renders up to makerCharSuggest click-to-fill chips (wrapped
// rows), writing the clicked name into *target. Shared by the character +
// background autocompletes. Returns the y below the chips (== y when none).
func (a *App) drawSuggestionChips(x, y, leftX, availW int32, names []string, target *string) int32 {
	if len(names) == 0 {
		return y
	}
	c := a.ctx
	c.Label(x, y+5, "Matches:", ColTextDim)
	cx, cy := leftX, y
	for _, name := range names {
		bw := c.TextWidth(name) + 16
		if bw > 170 {
			bw = 170
		}
		if cx+bw > leftX+availW && cx > leftX { // wrap to the next row
			cx = leftX
			cy += btnH + 4
		}
		if c.Button(sdl.Rect{X: cx, Y: cy, W: bw, H: btnH}, name) {
			*target = name
		}
		cx += bw + 6
	}
	return cy + btnH + 6
}

// containsFold reports whether s contains sub, ASCII-case-insensitively, WITHOUT
// allocating (strings.ToLower would alloc per name — this runs over the whole
// server char list while the autocomplete is open).
func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if lowerASCII(s[i+j]) != lowerASCII(sub[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// drawMakerEditor renders the fields for the selected event (message / bg /
// music). Edits write straight back into the cloned scene buffer.
// makerOffsetSlider draws a −100..100 % slider for a sprite offset at column fx,
// with a numeric readout, returning the new value (drag; 0 = centred). The kit
// slider is 0..max, so the value is biased by +100 and unbiased on the way out.
func makerOffsetSlider(c *Ctx, id string, fx, y int32, v int) int {
	track := sdl.Rect{X: fx, Y: y + 6, W: 150, H: 14}
	nv := int(c.Slider(id, track, int32(v+100), 200)) - 100
	c.Label(fx+162, y+5, fmt.Sprintf("%d%%", nv), ColAccent)
	return nv
}

func (a *App) drawMakerEditor(x, y, w int32) {
	c := a.ctx
	if a.makerScene == nil || a.makerSel < 0 || a.makerSel >= len(a.makerScene.Events) {
		c.Label(x, y, "No line selected — use + Line / + BG / + Music to add one.", ColTextDim)
		return
	}
	if w < 200 {
		w = 200
	}
	e := &a.makerScene.Events[a.makerSel]
	const labelW = 96
	fx := x + labelW
	fw := w - labelW

	switch courtroom.EventKind(e.Kind) {
	case courtroom.EventMessage:
		if e.Message == nil {
			e.Message = &protocol.ChatMessage{Side: defaultMakerSide, Emote: defaultMakerEmote}
		}
		m := e.Message
		c.Label(x, y, "Message line", ColAccent)
		y += 28
		c.Label(x, y+5, "Character:", ColText)
		m.CharName, _ = c.TextField("mk_char", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.CharName, "character folder, e.g. Phoenix")
		y += 32
		y = a.drawCharSuggestions(x, y, fx, fw, m) // searchable picker from the server roster
		c.Label(x, y+5, "Emote:", ColText)
		m.Emote, _ = c.TextField("mk_emote", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.Emote, "sprite stem, e.g. normal or (a)normal")
		y += 32
		c.Label(x, y+5, "Showname:", ColText)
		m.Showname, _ = c.TextField("mk_show", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.Showname, "optional display name (blank = character name)")
		y += 32
		c.Label(x, y+5, "Text:", ColText)
		m.Message, _ = c.TextField("mk_text", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.Message, "what the character says")
		y += 36
		c.Label(x, y+5, "Position:", ColText)
		if next, changed := c.Dropdown("mk_side", sdl.Rect{X: fx, Y: y, W: 150, H: fieldH}, makerSideLabels, makerSideIndex(m.Side)); changed {
			m.Side = makerSideValues[next]
		}
		c.Label(fx+162, y+5, "Colour:", ColText)
		col := m.TextColor
		if col < 0 || col >= render.TextColorCount {
			col = 0
		}
		if next, changed := c.Dropdown("mk_color", sdl.Rect{X: fx + 220, Y: y, W: 130, H: fieldH}, render.TextColorNames(), col); changed {
			m.TextColor = next
		}
		y += 36
		if next := c.Checkbox(x, y, "Flip the sprite horizontally", m.Flip); next != m.Flip {
			m.Flip = next
		}
		y += 26
		// Desk: with it on, the character stands behind the desk (grounded, the
		// normal AO look). Off shows the whole body floating on the background —
		// which reads as "zoomed in / clipping" with no desk to anchor the feet.
		showDesk := m.DeskMod != protocol.DeskHide
		if next := c.Checkbox(x, y, "Show the desk (character stands behind it — off = full body on the background)", showDesk); next != showDesk {
			if next {
				m.DeskMod = protocol.DeskShow
			} else {
				m.DeskMod = protocol.DeskHide
			}
		}
		y += 26
		pre := m.EmoteMod == protocol.EmoteModPreanim || m.EmoteMod == protocol.EmoteModPreanimZoom
		if next := c.Checkbox(x, y, "Play a pre-animation before the line", pre); next != pre {
			if next {
				m.EmoteMod = protocol.EmoteModPreanim
			} else {
				m.EmoteMod = protocol.EmoteModIdle
			}
			pre = next
		}
		y += 26
		if pre {
			c.Label(x, y+5, "Pre-anim:", ColText)
			m.PreEmote, _ = c.TextField("mk_pre", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.PreEmote, "pre-animation stem, e.g. (a)point")
			y += 32
		}
		// Effects — these render in the live preview AND the GIF/WebP export
		// (the export ignores the live reduce-motion pref so authored shakes show).
		shakeLabel := "Screenshake"
		if pre {
			shakeLabel = "Screenshake (only without a pre-anim)" // AO fires shake on idle/zoom only
		}
		if next := c.Checkbox(x, y, shakeLabel, m.Screenshake); next != m.Screenshake {
			m.Screenshake = next
		}
		if next := c.Checkbox(x+190, y, "Realization flash", m.Realization); next != m.Realization {
			m.Realization = next
		}
		y += 28
		// Move the character (offset as a percent of the viewport; 0 = centred).
		c.Label(x, y+5, "Move X:", ColText)
		m.SelfOffsetX = makerOffsetSlider(c, "mk_offx", fx, y, m.SelfOffsetX)
		y += 28
		c.Label(x, y+5, "Move Y:", ColText)
		m.SelfOffsetY = makerOffsetSlider(c, "mk_offy", fx, y, m.SelfOffsetY)
		y += 30
		// Sound effect — plays in Preview / a recording, but a GIF/WebP has no audio.
		c.Label(x, y+5, "Sound FX:", ColText)
		m.SFXName, _ = c.TextField("mk_sfx", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.SFXName, "sfx name — plays in replay, silent in the GIF")
		y += 34
		c.Label(x, y, "Effects + move show in Preview and the export. The character + emote must exist at the Origin/CDN.", ColTextDim)

	case courtroom.EventBackground:
		c.Label(x, y, "Background change", ColAccent)
		y += 28
		c.Label(x, y+5, "Background:", ColText)
		e.Text, _ = c.TextField("mk_bg", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, e.Text, "background folder, e.g. courtroom or gs4")
		y += 32
		y = a.drawBgSuggestions(x, y, fx, fw, &e.Text) // searchable picker from the server's background list
		c.Label(x, y, "Sets the scene from this point on (shown under every line that follows).", ColTextDim)

	case courtroom.EventMusic:
		c.Label(x, y, "Music change", ColAccent)
		y += 28
		c.Label(x, y+5, "Track:", ColText)
		e.Text, _ = c.TextField("mk_mus", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, e.Text, "music filename or full URL, e.g. trial.opus")
		y += 36
		c.Label(x, y, "Plays from this point on. Use a full URL for a CDN-hosted track.", ColTextDim)
	}
}
