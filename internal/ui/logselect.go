package ui

// Mouse text selection for the IC/OOC logs (drag to highlight, Ctrl+C to copy).
//
// The hard parts live here as PURE functions so they're unit-tested without
// SDL: the selection is anchored to CONTENT (source entry + rune offset within
// that entry), never to a screen row, so a scroll or a freshly appended/capped
// line can't corrupt it. Hit-testing binary-searches prefix widths via an
// injected measure func, so the cursor's line costs ~log2(len) width measures —
// never the per-rune-per-row metrics the kit warns against (ui.go pickMemo).
//
// The render + input wiring (which feeds these from the live logs) lives with
// the IC/OOC draw paths; all of it is gated on an active selection so a log
// with no selection draws exactly as before.

import "strings"

// selPoint is a position in a log: a source-entry index and a rune offset
// within that entry's full (unwrapped) text. Comparing two points orders the
// selection regardless of which end the drag started.
type selPoint struct {
	entry int
	off   int
}

// before reports whether p sorts ahead of q (entry first, then offset).
func (p selPoint) before(q selPoint) bool {
	return p.entry < q.entry || (p.entry == q.entry && p.off < q.off)
}

// equal reports a zero-width selection (nothing to highlight or copy).
func (p selPoint) equal(q selPoint) bool { return p.entry == q.entry && p.off == q.off }

// orderSel returns the two points low-to-high so callers don't care which way
// the user dragged.
func orderSel(a, b selPoint) (lo, hi selPoint) {
	if b.before(a) {
		return b, a
	}
	return a, b
}

// hitTestRune returns the rune index in runes nearest pixel x, binary-searching
// prefix widths (measure(runes[:i]) is monotonic in i). measure is called
// ~log2(len(runes)) times — only ever for the line under the cursor. The result
// is in [0, len(runes)] and snaps to the nearer glyph edge so a click in the
// middle of a character picks the closer boundary.
func hitTestRune(runes []rune, x int32, measure func([]rune) int32) int {
	n := len(runes)
	if n == 0 || x <= 0 {
		return 0
	}
	if measure(runes) <= x {
		return n
	}
	lo, hi := 0, n // smallest i with measure(runes[:i]) >= x
	for lo < hi {
		mid := (lo + hi) / 2
		if measure(runes[:mid]) >= x {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo > 0 && x-measure(runes[:lo-1]) < measure(runes[:lo])-x {
		return lo - 1
	}
	return lo
}

// selectedText extracts the highlighted substring across entries. entryText
// returns a source entry's full text (so this works for both logs without
// copying their backing stores); lo/hi must be ordered (orderSel). Multi-entry
// selections join with '\n'. Offsets are clamped, so a stale offset (entry
// shrank) can't panic.
func selectedText(entryText func(entry int) string, lo, hi selPoint) string {
	clampRunes := func(r []rune, off int) int {
		if off < 0 {
			return 0
		}
		if off > len(r) {
			return len(r)
		}
		return off
	}
	if lo.entry == hi.entry {
		r := []rune(entryText(lo.entry))
		a, b := clampRunes(r, lo.off), clampRunes(r, hi.off)
		if a > b {
			a, b = b, a
		}
		return string(r[a:b])
	}
	var sb strings.Builder
	first := []rune(entryText(lo.entry))
	sb.WriteString(string(first[clampRunes(first, lo.off):]))
	for e := lo.entry + 1; e < hi.entry; e++ {
		sb.WriteByte('\n')
		sb.WriteString(entryText(e))
	}
	last := []rune(entryText(hi.entry))
	sb.WriteByte('\n')
	sb.WriteString(string(last[:clampRunes(last, hi.off)]))
	return sb.String()
}
