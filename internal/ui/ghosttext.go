package ui

// GHOST TEXT — the IC log's not-yet-spoken tail, drawn faint. One concern, one file.
//
// THE PROBLEM. AsyncAO writes a line into the IC log the moment its packet
// ARRIVES (the EventMessage branch in app.go), while the courtroom speaks it
// later — after the ones already queued ahead of it, and then only one rune at a
// time. On a busy server the log therefore runs several whole messages ahead of
// the chatbox, and you read every punchline before the character says it.
//
// THE RULE (Crystalwarrior's ask, user-approved, default ON). A log row is drawn
// FAINT until the crawl has revealed all of its text:
//
//	rows of messages still waiting in the queue          → faint
//	rows of the message playing now, past the crawl      → faint
//	everything the courtroom has actually said           → solid
//
// ROW granularity, not rune granularity, and deliberately so. A row flips solid
// when its LAST rune is revealed, which for the single-line message that AO
// traffic is overwhelmingly made of means "faint for the whole crawl, solid when
// the line is finished" — exactly the wanted behaviour, at the cost of no extra
// measurement, no second draw per row and no allocation, so the log's draw path is
// untouched for everyone who leaves the option alone.
//
// PAIRING BY COUNT, not by identity. The number of log lines that are still
// unspoken is derivable from live state every frame — the courtroom's queue length
// plus the message on stage — so nothing has to be remembered, nothing can leak,
// and a message the queue sheds (or a catch-up skip) simply un-ghosts a frame
// later instead of stranding a line faint forever.
//
// NO PACING CENSUS. The ghost boundary only moves while a message is playing, and
// a playing message already holds the pacer at the talk tier and refuses
// SkipFrame (roomBusy) — a NoteAnimating here would be a second, redundant claim
// on the frame budget, which is precisely what the census doctrine warns against.

import (
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
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

// ghostSpan is the ghost boundary for one frame of the IC log: every row of an
// entry AFTER `entry` is faint, and within `entry` every row from `rowFrom` on is
// faint. ok=false means nothing is ghosted (the option is off, nothing is playing,
// or the log holds no unspoken line) and the log draws exactly as it always has.
type ghostSpan struct {
	entry   int // first icLog index that is not fully spoken
	rowFrom int // first row index of that entry still holding unrevealed text
	ok      bool
}

// ghosted reports whether the row at index ri, belonging to icLog entry e, is
// faint under this span. The zero value (ok=false) ghosts nothing.
func (g ghostSpan) ghosted(ri, e int) bool {
	if !g.ok || e < g.entry {
		return false
	}
	return e > g.entry || ri >= g.rowFrom
}

// unspokenCount is how many IC messages the courtroom has taken in but not yet
// finished saying: the queue, plus the one on stage while it is still shouting,
// playing a preanimation or typing. PhaseLinger is deliberately NOT counted — the
// text is fully revealed by then, the message HAS been said, and holding it faint
// through the linger would flip the last line solid at a moment nothing visible
// happens.
func unspokenCount(phase courtroom.MessagePhase, queued int) int {
	n := queued
	switch phase {
	case courtroom.PhaseShout, courtroom.PhasePreanim, courtroom.PhaseTalking:
		n++
	}
	return n
}

// firstUnspokenEntry walks the log BACKWARD over the `n` most recent speech lines
// and returns the index of the oldest of them — the message the courtroom is
// working on right now. Backward because the pairing is with the tail of the queue:
// system lines (music, evidence) interleave freely and are skipped, since they were
// true the instant they appeared and were never queued.
//
// Returns len(log) when n is 0 or the log holds fewer speech lines than the queue
// claims (a shed message, a catch-up skip): fewer ghosted rows is the safe way to
// be wrong — it can under-hide, never hide a line that has already been said.
func firstUnspokenEntry(log []icEntry, n int) int {
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

// ghostRowStart is the first row of `entry` that still contains an unrevealed
// rune, given that `unrevealed` runes remain at the END of the message.
//
// Row space, counted from the back: a row is unrevealed as soon as the tail
// reaches into it, i.e. as soon as the runes that come AFTER it are fewer than the
// tail. The row's own runes are what a soft wrap left in it, so a boundary that
// falls exactly on a dropped break-space can land one row early — which is
// invisible, because that row flips at the same instant either way.
//
// Returns the entry's first row when everything is unrevealed, and one past its
// last row when nothing is.
func ghostRowStart(rows []icWrapLine, entry, unrevealed int) int {
	lo, hi := -1, -1
	for i := range rows {
		if rows[i].entry != entry {
			continue
		}
		if lo < 0 {
			lo = i
		}
		hi = i + 1
	}
	if lo < 0 {
		return 0 // the entry is filtered out of the view entirely
	}
	if unrevealed <= 0 {
		return hi
	}
	// `after` is the rune count of the rows BELOW i. The tail reaches row i exactly
	// while after < unrevealed, and `after` only grows as i walks up, so the first
	// row it stops reaching ends the faint block.
	start, after := hi, 0
	for i := hi - 1; i >= lo; i-- {
		if after >= unrevealed {
			break
		}
		start = i
		after += utf8.RuneCountInString(rows[i].text)
	}
	return start
}

// ghostLogSpan resolves this frame's ghost boundary for the live IC log.
// Every refusal below is a state where nothing should be hidden: the option is
// off, there is no courtroom, or nothing is waiting to be said.
func (a *App) ghostLogSpan(rows []icWrapLine) ghostSpan {
	if a.room == nil || a.d.Prefs == nil || !a.d.Prefs.GhostCrawlTextOn() {
		return ghostSpan{}
	}
	n := unspokenCount(a.room.Phase(), a.room.QueueLen())
	if n == 0 {
		return ghostSpan{}
	}
	entry := firstUnspokenEntry(a.icLog, n)
	if entry >= len(a.icLog) {
		return ghostSpan{}
	}
	// The tail the typewriter has not reached, measured on the LIVE room's scene —
	// the message that entry is paired with. A shout or preanimation has revealed
	// nothing yet, so the whole line is faint, which is what VisibleRunes=0 gives.
	total := utf8.RuneCountInString(a.room.Scene.MessageText)
	unrevealed := total - a.room.Scene.VisibleRunes
	if unrevealed < 0 {
		unrevealed = 0
	}
	return ghostSpan{entry: entry, rowFrom: ghostRowStart(rows, entry, unrevealed), ok: true}
}
