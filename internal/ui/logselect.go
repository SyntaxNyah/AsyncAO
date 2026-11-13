package ui

// Mouse text selection for the IC/OOC logs (drag to highlight, Ctrl+C to copy).
//
// The selection is anchored to content (source entry plus rune offset), never a screen row, so scrolling or a
// newly appended or capped line cannot corrupt it. Hit-testing binary-searches prefix widths via an injected
// measure func, avoiding per-rune-per-row metrics (ui.go pickMemo). All wiring is gated on an active selection.

import "strings"

// selPoint is a position in a log: a source-entry index and a rune offset within that entry's full unwrapped text.
type selPoint struct {
	entry int
	off   int
}

// before reports whether p sorts ahead of q (entry first, then offset).
func (p selPoint) before(q selPoint) bool {
	return p.entry < q.entry || (p.entry == q.entry && p.off < q.off)
}

// equal reports a zero-width selection.
func (p selPoint) equal(q selPoint) bool { return p.entry == q.entry && p.off == q.off }

// orderSel returns the two points low-to-high regardless of drag direction.
func orderSel(a, b selPoint) (lo, hi selPoint) {
	if b.before(a) {
		return b, a
	}
	return a, b
}

// hitTestRune returns the rune index in runes nearest pixel x by binary-searching prefix widths (measure is
// monotonic in i), so only the line under the cursor is measured. The result is in [0, len(runes)] and snaps to
// the nearer glyph edge.
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

// selectedText extracts the highlighted substring across entries; entryText returns a source entry's full text.
// lo/hi must be ordered (orderSel). Multi-entry selections join with '\n'. Offsets are clamped, so a stale offset
// cannot panic.
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
