package ui

// THE CHROME-ROW REFLOW GUARD (v1.90.0 W7b).
// PURE AND ALLOCATION-FREE. Fixed arrays, an injected measure, no App and no Ctx —
// so the geometry is gate-able headless and the per-frame draw pays nothing for it.

import "github.com/veandco/go-sdl2/sdl"

// ---------------------------------------------------------------------------
// Named caps and metrics (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// chromeRowMaxCtls bounds one row. Eight is the Music header's own worst case
	// (five actions, the readout, the search field and its count — musicHeaderMaxItems)
	// and it is what makes chromeRowPlan a stack value.
	chromeRowMaxCtls = 8

	// chromeRowH is one line's control height, and chromeRowLinePitch the step from
	// one line to the next. Aliases of the roster strip's metrics rather than second
	// copies: the Music header shares that strip's baseline by construction
	// (musicIconPx == plStripRowH), and two numbers that must agree are one number.
	chromeRowH         = plStripRowH
	chromeRowLinePitch = plStripLinePitch
	// chromeRowBodyGapPx is the gap between the last line and the body under it.
	chromeRowBodyGapPx = plStripBodyGapPx
	// chromeRowItemGapPx separates two controls sharing a line.
	chromeRowItemGapPx = plStripItemGapPx
	// chromeRowMinBodyPx is how much of the block's HEIGHT the thing under the row
	// keeps however many lines want to wrap. Same value, same argument, as
	// plStripMinBodyPx: wrapping is the cure for a narrow panel, but an unbounded wrap
	// in a SHORT panel eats the list the row is a header for.
	chromeRowMinBodyPx = plStripMinBodyPx

	// chromeCtlMinPx is the narrowest a control may be drawn at all. Below it a button
	// is an unlabelled box, which is the judgement themedChipMinW already makes one
	// tier up — and it is expressed as that constant so the chip strip and the header
	// cannot disagree about when a control has stopped saying anything.
	chromeCtlMinPx = themedChipMinW

	// chromeRowDropRounds bounds the drop-and-retry loop (hard rule 4). One round per
	// control plus the shrink pass is the most a total algorithm can need; the bound is
	// named so a future rung cannot turn the loop unbounded by accident.
	chromeRowDropRounds = chromeRowMaxCtls + 1
)

// chromeCtlNeverDrop is the priority a control that must never be dropped carries.
const chromeCtlNeverDrop uint8 = 0

// ---------------------------------------------------------------------------
// The spec
// ---------------------------------------------------------------------------

// chromeCtl is ONE control a row wants placed.
type chromeCtl struct {
	id  int
	w   int32
	min int32
	// prio orders the DROPS: higher goes first, chromeCtlNeverDrop never goes.
	prio uint8
	// brk forces a NEW LINE before this control. It is a property of the control
	// rather than a second planner call, and that is the whole structural point: a
	// caller that planned "row one" and "row two" separately would be back to two
	// independent anchors, which is the shape that put Expand all on top of Stop music
	// in the first place. One cursor, one plan, one guarantee.
	brk bool
}

// chromeSlot is one PLACED control.
type chromeSlot struct {
	id int
	r  sdl.Rect
}

// chromeRowPlan is one frame's row. h is the vertical room it took, which the caller
// subtracts before drawing the body; dropped counts what did not fit, so a caller can
// say so (design §3.3: name the offender) instead of silently losing a button.
type chromeRowPlan struct {
	slot    [chromeRowMaxCtls]chromeSlot
	n       int
	h       int32
	dropped int
}

// placed reports how many controls the plan actually drew.
func (p *chromeRowPlan) placed() int { return p.n }

// ---------------------------------------------------------------------------
// The planner
// ---------------------------------------------------------------------------

// planChromeRow lays want across r, in want's own order, and returns a plan in which
// no two controls intersect and every control is inside r.
func planChromeRow(r sdl.Rect, want []chromeCtl, maxLines int32) chromeRowPlan {
	var active [chromeRowMaxCtls]chromeCtl
	n := len(want)
	if n > chromeRowMaxCtls {
		n = chromeRowMaxCtls
	}
	copy(active[:n], want[:n])
	dropped := len(want) - n

	best := chromeRowPlan{}
	for round := 0; round < chromeRowDropRounds; round++ {
		// TWO PASSES PER ROUND, natural then shrunk, and the natural one first so a row
		// that fits is never segmented into equal boxes it did not need.
		for _, shrink := range [2]bool{false, true} {
			p, ok := layChromeRow(r, active[:n], maxLines, shrink)
			if ok {
				p.dropped = dropped
				return p
			}
			if p.n > best.n {
				best = p // keep the fullest attempt, in case nothing ever fits everything
			}
		}
		i, found := chromeDropIndex(active[:n])
		if !found {
			break // nothing left that may be dropped; the best partial plan is the answer
		}
		copy(active[i:], active[i+1:n])
		n--
		dropped++
	}
	best.dropped = dropped + (n - best.n)
	if best.dropped < 0 {
		best.dropped = 0
	}
	return best
}

// chromeDropIndex picks the control to drop: the HIGHEST priority, and among equals
// the LAST one, so a row degrades from its tail — which is the order every caller
// writes its controls in (most important first).
func chromeDropIndex(ctls []chromeCtl) (int, bool) {
	best, found := -1, false
	for i := range ctls {
		if ctls[i].prio == chromeCtlNeverDrop {
			continue
		}
		if !found || ctls[i].prio >= ctls[best].prio {
			best, found = i, true
		}
	}
	return best, found
}

// layChromeRow is ONE placement attempt through ONE cursor. ok=false means at least
// one control did not fit; the partial plan comes back anyway so the caller can keep
// the fullest attempt.
func layChromeRow(r sdl.Rect, ctls []chromeCtl, maxLines int32, shrink bool) (chromeRowPlan, bool) {
	var p chromeRowPlan
	if r.W < chromeCtlMinPx || len(ctls) == 0 {
		return p, len(ctls) == 0
	}
	lines := chromeRowLineBudget(r.H, maxLines)
	if lines <= 0 {
		return p, false
	}
	share := int32(0)
	if shrink {
		share = (r.W - chromeRowItemGapPx*int32(len(ctls)-1)) / int32(len(ctls))
		if share < chromeCtlMinPx {
			share = chromeCtlMinPx
		}
	}
	x, y, line := r.X, r.Y, int32(1)
	ok := true
	for i := range ctls {
		w := ctls[i].w
		if share > 0 && share < w {
			w = share
		}
		if w < ctls[i].min {
			w = ctls[i].min
		}
		if w <= 0 || w > r.W {
			// Nothing to draw, or wider than the WHOLE line even at its minimum: it can
			// never be placed, and CLAMPING it is what put a 96 px chip 12 px past a 212 px
			// panel.
			ok = false
			continue
		}
		// A forced break only costs a line once something is already on the current one:
		brk := ctls[i].brk && x > r.X
		if !brk {
			if avail := r.X + r.W - x; w > avail {
				// A FLEX control SHRINKS INTO THE TAIL of this line rather than costing a
				// whole one — plStrip.placeFlex's own rule, kept: a clipped readout would
				// rather lose its last few words than push a real button onto a second row.
				if ctls[i].min < ctls[i].w && ctls[i].min <= avail {
					w = avail
				} else {
					brk = true
				}
			}
		}
		if brk {
			if line >= lines {
				ok = false
				continue // no further line: this control is dropped, not overlapped
			}
			x, y, line = r.X, y+chromeRowLinePitch, line+1
		}
		if p.n >= len(p.slot) {
			ok = false
			continue
		}
		p.slot[p.n] = chromeSlot{id: ctls[i].id, r: sdl.Rect{X: x, Y: y, W: w, H: chromeRowH}}
		p.n++
		x += w + chromeRowItemGapPx
	}
	if p.n > 0 {
		p.h = line*chromeRowLinePitch + chromeRowBodyGapPx
	}
	return p, ok
}

// chromeRowLineBudget is how many lines a block of this height may spend on its row
// while the body under it keeps chromeRowMinBodyPx. 0 means the row draws nothing —
// which is the correct answer for a panel with no room for both, and the reason the
// caller must always ask the plan rather than assume a header exists.
func chromeRowLineBudget(h, maxLines int32) int32 {
	room := h - chromeRowMinBodyPx - chromeRowBodyGapPx
	if room < chromeRowH {
		return 0
	}
	n := (room + chromeRowLinePitch - chromeRowH) / chromeRowLinePitch
	if maxLines > 0 && n > maxLines {
		n = maxLines
	}
	return n
}
