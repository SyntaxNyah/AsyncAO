package ui

// GHOST TEXT — the IC log's QUEUED, not-yet-started messages, drawn faint. One
// concern, one file.
// NO PACING CENSUS. The ghost boundary only moves when the play queue does, and
// a playing message already holds the pacer at the talk tier and refuses
// SkipFrame (roomBusy) — a NoteAnimating here would be a second, redundant claim

import (
	"github.com/veandco/go-sdl2/sdl"
)

// ghostTextMixPct is how far a ghosted row's ink is pulled toward the surface
// behind it, in percent. 68 reads as "there, but not said yet" on the stock dark
// panel: still legible enough that a wrapped message keeps its shape, faint enough
// that the eye skips it and lands on the last SOLID line, which is the one the
// character is actually on. A blend rather than an alpha because the log draws its
// text through the font atlas, which has no per-draw alpha, and a blend against the
// panel is what alpha over the panel would have produced anyway.
const ghostTextMixPct = 68

// ghostInk is a row colour pulled ghostTextMixPct of the way toward bg.
// Allocation-free (three integer lerps on value types) and called at most once per
// drawn row, only while something is unspoken.
func ghostInk(col, bg sdl.Color) sdl.Color {
	mix := func(c, b uint8) uint8 {
		return uint8((int(c)*(100-ghostTextMixPct) + int(b)*ghostTextMixPct) / 100)
	}
	return sdl.Color{R: mix(col.R, bg.R), G: mix(col.G, bg.G), B: mix(col.B, bg.B), A: col.A}
}

// ghostSpan is the ghost boundary for one frame of the IC log: every row of
// entry `entry` and of everything after it is faint. ok=false means nothing is
// ghosted (the option is off, nothing is queued) and the log draws exactly as it
// always has.
// There is no row field. The message on stage is never partially ghosted, so a
// boundary INSIDE an entry has nothing left to express — and removing it is what
// stops the "ghost the current message's own lines" behaviour from coming back
type ghostSpan struct {
	entry int // first icLog index that has not started being spoken
	ok    bool
}

// ghosted reports whether the row at index ri, belonging to icLog entry e, is
// faint under this span. The zero value (ok=false) ghosts nothing. ri is
// accepted and ignored: the caller draws rows, and taking the argument keeps the
// call site honest about the fact that the answer is the same for every row of
// an entry.
func (g ghostSpan) ghosted(_ int, e int) bool {
	return g.ok && e >= g.entry
}

// queuedCount is how many IC messages the courtroom has taken in but not yet
// STARTED saying — the play queue, and only the play queue.
//
// The message on stage is deliberately excluded whatever phase it is in. It is
// the line the reader is watching; ghosting it (which the first cut did for
// PhaseShout / PhasePreanim / PhaseTalking) hides the text at the exact moment
// the chatbox is revealing it.
func queuedCount(queued int) int {
	if queued < 0 {
		return 0
	}
	return queued
}

// firstQueuedEntry walks the log BACKWARD over the `n` most recent speech lines
// and returns the index of the oldest of them — the first message still waiting
// in the queue. Backward because the pairing is with the TAIL of the log:
// system lines (music, evidence) interleave freely and are skipped, since they
// were true the instant they appeared and were never queued.
// Returns len(log) when n is 0 or the log holds fewer speech lines than the queue
// claims (a shed message, a catch-up skip): fewer ghosted rows is the safe way to
// be wrong — it can under-hide, never hide a line that has already been said.
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

// ghostLogSpan resolves this frame's ghost boundary for the live IC log.
// Every refusal below is a state where nothing should be hidden: the option is
// off, there is no courtroom, or nothing is waiting behind the current line.
//
// rows is unused now that the boundary is entry-granular. It stays in the
// signature because the caller has it and a future per-row rule would need it —
// and because dropping it would silently change every call site's shape in a
// change whose whole point is that the row dimension went away.
func (a *App) ghostLogSpan(_ []icWrapLine) ghostSpan {
	if a.link.room == nil || a.d.Prefs == nil || !a.d.Prefs.Load().GhostCrawlText {
		return ghostSpan{}
	}
	n := queuedCount(a.link.room.QueueLen())
	if n == 0 {
		return ghostSpan{} // nothing behind the current line: nothing to preview
	}
	entry := firstQueuedEntry(a.chatlog.icLog, n)
	if entry >= len(a.chatlog.icLog) {
		return ghostSpan{}
	}
	return ghostSpan{entry: entry, ok: true}
}
