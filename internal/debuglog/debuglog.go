// Package debuglog is the bounded failure log behind the debug overlay: a ring of
// stamped rows, folded by a time window.
// It is deliberately state-only — no SDL, no Ctx, no App, no env — which is what
// makes the boundary real: package ui imports this and can reach the ring only
// through Push/PushAt/Lines/Len. The fold policy is the bug fix pinned by the
// tests that live here (two interleaved chatty sources used to defeat the
// consecutive-duplicate counter, so real failures aged out behind them).
package debuglog

import (
	"fmt"
	"time"
)

const (
	// Cap bounds the ring (rule §17.4: nothing is unbounded); oldest rows fall off.
	Cap = 120
	// FoldWindow is how long a message stays foldable: seen again within this of
	// its last sighting, it collapses into the existing row's ×N counter instead
	// of appending another. Presentation only.
	FoldWindow = 90 * time.Second
	// stampLayout prefixes every row with the wall clock of its LATEST sighting
	// (the ring is diagnostics, so absolute time beats elapsed).
	stampLayout = "15:04:05 "
)

// Entry is one row of the ring. Text is derived from the other three and cached,
// so the fold costs one render per Push rather than per draw (the panel redraws
// at frame rate while it is open).
type Entry struct {
	Raw   string    // the caller's message — the fold key
	At    time.Time // latest sighting; the fold window is measured from it
	Count int       // sightings folded into this row
	Text  string    // rendered row: stamp + raw (+ "  ×N")
}

// Render rebuilds the displayed row from the entry's state.
func (e Entry) Render() string {
	if e.Count > 1 {
		return e.At.Format(stampLayout) + e.Raw + fmt.Sprintf("  ×%d", e.Count)
	}
	return e.At.Format(stampLayout) + e.Raw
}

// Sink is the ring. The zero value is ready to use.
type Sink struct {
	log []Entry
}

// Push appends one line, stamping it with the wall clock.
func (s *Sink) Push(line string) { s.PushAt(line, time.Now()) }

// PushAt is Push with the clock injected — the fold is a TIME window, so tests
// drive the window from here rather than by sleeping.
func (s *Sink) PushAt(line string, now time.Time) {
	// The ring is ordered by latest sighting (a fold moves its row to the tail),
	// so scanning backwards can stop at the first row outside the window: every
	// row before it is older still. That bounds the scan without a second cap.
	for i := len(s.log) - 1; i >= 0; i-- {
		e := s.log[i]
		if now.Sub(e.At) > FoldWindow {
			break
		}
		if e.Raw != line {
			continue
		}
		e.Count++
		e.At = now
		e.Text = e.Render()
		// Move to the tail: keeps the ordering the early exit above relies on,
		// and keeps a repeating source where the reader is looking (newest end)
		// instead of pinned at the position of its first sighting.
		copy(s.log[i:], s.log[i+1:])
		s.log[len(s.log)-1] = e
		return
	}
	e := Entry{Raw: line, At: now, Count: 1}
	e.Text = e.Render()
	s.log = append(s.log, e)
	if len(s.log) > Cap {
		s.log = s.log[len(s.log)-Cap:]
	}
}

// Lines is the ring for the panels that draw it (newest last).
func (s *Sink) Lines() []Entry { return s.log }

// Len is the ring's row count.
func (s *Sink) Len() int { return len(s.log) }
