package ui

// THE CHROME-ROW REFLOW GUARD (v1.90.0 W7b).
//
// ONE concern: lay a row of chrome controls inside a rect a THEME chose, so that no
// two of them can ever land on top of each other.
//
// THE DEFECT IT CLOSES, from a tester's screenshot of an imported AO2 theme: the
// Music panel's header stacked Expand all and Collapse all — and the roster chip
// above them — into the same pixels. That is the same class musicheader.go's own
// header describes ("Expand all was painted ON TOP of Stop music … ONE click fired
// BOTH"), returning through a different door: a panel whose rect is theme DATA can be
// squeezed until a row's controls no longer fit, and the strip that laid them out
// either refused them one at a time or handed the leftovers to a SECOND anchor —
// the chip strip and the header row are placed by two independent cursors, reconciled
// only by arithmetic that a short panel can falsify.
//
// SO THE GUARD IS STRUCTURAL, NOT ARITHMETIC. planChromeRow places every control
// through ONE cursor, and a cursor cannot overlap itself. What is new is what happens
// when the cursor runs out, because "refuse the control" is not good enough for a row
// whose controls are the only route to a feature:
//
//	1. NATURAL — everything at the width its label wants.
//	2. SHRINK — an equal share of the line each, while that share is >= the control's
//	   own declared minimum. This is fitThemedChips' segmented step, generalised.
//	3. WRAP — onto further lines, while the block still keeps chromeRowMinBodyPx for
//	   the thing the row is a header FOR.
//	4. DROP BY PRIORITY — the least valuable control leaves, and the rest are laid out
//	   again from scratch. This is the width-drop contract the IC bar already ships
//	   (the Flip segment drops at 720p by contract), applied to a themed panel: a
//	   control the panel cannot hold is dropped DELIBERATELY, in an order the caller
//	   chose, rather than by whichever placement happened to fail first.
//
// The result is total: for ANY rect, the plan either places a control inside the rect
// or does not place it at all, and never places two controls that intersect.
// TestChromeRowNeverOverlapsAtAnySize sweeps that, and
// TestSqueezedMusicPanelNeverStacksItsHeader drives the reported shape end to end.
//
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
// The ⋮ overflow menu is the live example: every row it hosts is reachable ONLY
// through it inside a theme's canvas, so dropping it deletes features rather than
// hiding them.
const chromeCtlNeverDrop uint8 = 0

// ---------------------------------------------------------------------------
// The spec
// ---------------------------------------------------------------------------

// chromeCtl is ONE control a row wants placed.
//
// `min` is the narrowest the control is still worth drawing at, which is NOT the same
// question as chromeCtlMinPx: an icon button's label is a vector glyph and does not
// shrink, so its min equals its natural width, while a flexible readout is happy to
// clip. A control whose min exceeds the line is dropped rather than clamped — clamping
// it is how a 96 px chip ends up 12 px past its panel.
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

// rect finds a placed control. ok=false means DRAW NOTHING for it — the same
// degradation contract plStrip.placeFlex publishes, and the reason every caller must
// branch on it rather than assume.
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

// ---------------------------------------------------------------------------
// The planner
// ---------------------------------------------------------------------------

// planChromeRow lays want across r, in want's own order, and returns a plan in which
// no two controls intersect and every control is inside r.
//
// maxLines bounds the wrap independently of the height (a header that may take at most
// two lines however tall the panel is); 0 means "as many as the height allows".
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
//
// THE CURSOR IS THE GUARANTEE. x only ever advances by the width just taken plus the
// gap, and a wrap resets it to the left edge on a NEW line — so two slots can only
// share pixels if the pitch were smaller than the height, which is a compile-time
// relationship between two named constants, not a runtime one.
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
		// an empty second half of a row must not reserve a blank line, which is the same
		// laziness plStrip.newline documents.
		brk := ctls[i].brk && x > r.X
		if !brk {
			if avail := r.X + r.W - x; w > avail {
				// A FLEX control SHRINKS INTO THE TAIL of this line rather than costing a
				// whole one — plStrip.placeFlex's own rule, kept: a clipped readout would
				// rather lose its last few words than push a real button onto a second row.
				// A fixed control (min == its natural width, every icon button) cannot, so
				// it wraps.
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

// chromeRowBody is the block body left UNDER a plan. A plan that placed nothing hands
// the whole block back rather than reserving room for a row that did not draw.
func chromeRowBody(block sdl.Rect, p *chromeRowPlan) sdl.Rect {
	if p == nil || p.n == 0 {
		return block
	}
	block.Y += p.h
	block.H -= p.h
	return block
}
