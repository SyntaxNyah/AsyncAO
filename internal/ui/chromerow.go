package ui

// Chrome-row reflow guard: lays a row of chrome controls inside a theme-chosen rect so no two overlap.
//
// A themed panel can be squeezed until its row no longer fits. Every control goes through one cursor (planChromeRow),
// which cannot overlap itself, and when it runs out of room:
//	1. natural: every control at its label width.
//	2. shrink: an equal share of the line each, while the share is >= the control's minimum.
//	3. wrap onto more lines, while the block keeps chromeRowMinBodyPx for the body.
//	4. drop by priority: the least valuable control leaves and the rest are laid out again.
//
// For any rect a control is either placed inside it or not placed, and placed controls never intersect.
// Pure and allocation-free: fixed arrays and an injected measure, no App or Ctx.

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

const (
	// chromeRowMaxCtls bounds one row at the Music header's worst case (musicHeaderMaxItems).
	chromeRowMaxCtls = 8

	// chromeRowH is one line's control height and chromeRowLinePitch the step to the next; aliases of the roster
	// strip's metrics, which the Music header shares (musicIconPx == plStripRowH).
	chromeRowH         = plStripRowH
	chromeRowLinePitch = plStripLinePitch
	// chromeRowBodyGapPx is the gap between the last line and the body under it.
	chromeRowBodyGapPx = plStripBodyGapPx
	// chromeRowItemGapPx separates two controls sharing a line.
	chromeRowItemGapPx = plStripItemGapPx
	// chromeRowMinBodyPx is how much block height the body keeps however many lines wrap (as plStripMinBodyPx).
	chromeRowMinBodyPx = plStripMinBodyPx

	// chromeCtlMinPx is the narrowest a control may be drawn at all; it is themedChipMinW so chips and headers agree.
	chromeCtlMinPx = themedChipMinW

	// chromeRowDropRounds bounds the drop-and-retry loop (hard rule 4): one round per control plus the shrink pass.
	chromeRowDropRounds = chromeRowMaxCtls + 1
)

// chromeCtlNeverDrop is the priority of a control that must never be dropped, such as the overflow menu that is
// the only route to the rows it hosts.
const chromeCtlNeverDrop uint8 = 0

// chromeCtl is one control a row wants placed.
//
// min is the narrowest the control is worth drawing at, unlike chromeCtlMinPx: an icon button's glyph does not
// shrink, so its min is its natural width, while a readout may clip. A control whose min exceeds the line is
// dropped rather than clamped.
type chromeCtl struct {
	id  int
	w   int32
	min int32
	// prio orders the drops: higher goes first, chromeCtlNeverDrop never goes.
	prio uint8
	// brk forces a new line before this control; it is a property of the control so one plan keeps one cursor.
	brk bool
}

// chromeSlot is one placed control.
type chromeSlot struct {
	id int
	r  sdl.Rect
}

// chromeRowPlan is one frame's row. h is the vertical room it took, which the caller subtracts before drawing the
// body; dropped counts what did not fit.
type chromeRowPlan struct {
	slot    [chromeRowMaxCtls]chromeSlot
	n       int
	h       int32
	dropped int
}

// rect finds a placed control; ok=false means draw nothing for it (the plStrip.placeFlex contract).
// Only tests call it today; the planner's callers read placed().
func (p *chromeRowPlan) rect(id int) (sdl.Rect, bool) {
	for i := 0; i < p.n; i++ {
		if p.slot[i].id == id {
			return p.slot[i].r, true
		}
	}
	return sdl.Rect{}, false
}

// placed reports how many controls the plan actually drew.
func (p *chromeRowPlan) placed() int { return p.n }

// planChromeRow lays want across r in want's order; no two controls intersect and every control is inside r.
// maxLines bounds the wrap independently of the height; 0 means as many as the height allows.
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
		// Two passes per round, natural then shrunk, so a row that fits is never segmented needlessly.
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

// chromeDropIndex picks the control to drop: the highest priority, among equals the last (callers list best first).
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

// layChromeRow is one placement attempt through one cursor. ok=false means a control did not fit; the partial plan
// comes back so the caller can keep the fullest attempt.
//
// x only advances by the width taken plus the gap and a wrap resets it on a new line, so slots overlap only if the
// pitch were smaller than the height, a relationship between two named constants.
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
			// Nothing to draw, or wider than the line at its minimum: never placeable, and clamping would overhang.
			ok = false
			continue
		}
		// A forced break only costs a line once something is on the current one; an empty second half reserves nothing.
		brk := ctls[i].brk && x > r.X
		if !brk {
			if avail := r.X + r.W - x; w > avail {
				// A flex control shrinks into the tail of this line rather than costing a whole one;
				// a fixed control (min == natural width, every icon button) cannot, so it wraps.
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

// chromeRowLineBudget is how many lines a block of this height may spend while the body keeps chromeRowMinBodyPx.
// 0 means the row draws nothing, so callers must ask the plan rather than assume a header exists.
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
