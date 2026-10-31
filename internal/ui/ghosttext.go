package ui

// Ghost text: the log gets a line when its packet arrives, ahead of speech, so entries still queued draw faint.
// Whole entries only: the message being spoken must not be ghosted, as the chatbox is revealing it.
// Paired by count from the queue length, so nothing is remembered and a shed message un-ghosts a frame later.
// No NoteAnimating: the boundary moves only with the play queue, and a playing message already holds the talk tier.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// ghostTextMixPct is how far a ghosted row's ink moves toward the surface behind it, in percent.
// A blend rather than alpha because the log draws through the font atlas, which has no per-draw alpha.
const ghostTextMixPct = 68

// ghostInk is a row colour pulled ghostTextMixPct of the way toward bg.
func ghostInk(col, bg sdl.Color) sdl.Color {
	mix := func(c, b uint8) uint8 {
		return uint8((int(c)*(100-ghostTextMixPct) + int(b)*ghostTextMixPct) / 100)
	}
	return sdl.Color{R: mix(col.R, bg.R), G: mix(col.G, bg.G), B: mix(col.B, bg.B), A: col.A}
}

// ghostSpan is one frame's boundary: every row of entry and of everything after it is faint; ok=false ghosts nothing.
type ghostSpan struct {
	entry int // first icLog index that has not started being spoken
	ok    bool
}

// ghosted reports whether a row belonging to icLog entry e is faint; the row index is ignored.
func (g ghostSpan) ghosted(_ int, e int) bool {
	return g.ok && e >= g.entry
}

// queuedCount is how many IC messages the courtroom has taken in but not yet started saying.
// The message on stage is excluded: ghosting it would hide the text as the chatbox reveals it.
func queuedCount(queued int) int {
	if queued < 0 {
		return 0
	}
	return queued
}

// firstQueuedEntry returns the index of the oldest of the n most recent speech lines, skipping system lines.
// Returns len(log) when n is 0 or the log holds too few: it can under-hide, never hide a line already said.
func firstQueuedEntry(log []icEntry, n int) int {
	if n <= 0 {
		return len(log)
	}
	for i := len(log) - 1; i >= 0; i-- {
		if !log[i].speech {
			continue
		}
		if n--; n == 0 {
			return i
		}
	}
	return len(log)
}

// ghostLogSpan resolves this frame's boundary; with the option off, no courtroom or nothing waiting, it hides nothing.
func (a *App) ghostLogSpan(_ []icWrapLine) ghostSpan {
	if a.room == nil || a.d.Prefs == nil || !a.d.Prefs.Load().GhostCrawlText {
		return ghostSpan{}
	}
	n := queuedCount(a.room.QueueLen())
	if n == 0 {
		return ghostSpan{} // nothing behind the current line: nothing to preview
	}
	entry := firstQueuedEntry(a.icLog, n)
	if entry >= len(a.icLog) {
		return ghostSpan{}
	}
	return ghostSpan{entry: entry, ok: true}
}
