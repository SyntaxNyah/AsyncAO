package ui

// Inkscape-style alignment snapping for the classic layout editor (playtest,
// Tifera: "nothing was aligning properly... make an alignment thingy like how
// Inkscape does"). While a box is dragged or resized, its edges and centre
// are compared against every OTHER registered slot's edges/centres and the
// window's edges/centre; within alignSnapPx the box snaps flush and a
// full-length guide line shows WHAT it aligned to. Grid snapping alone could
// never line boxes up: the defaults aren't grid-aligned, so a snapped box
// always sat a few px off its unsnapped neighbour.

import "github.com/veandco/go-sdl2/sdl"

const (
	// alignSnapPx is the capture distance (logical px) for edge/centre
	// alignment — big enough to feel magnetic, small enough not to fight a
	// deliberate near-miss placement.
	alignSnapPx = 6
)

// layoutGridSizes are the snap-grid steps the editor's Grid chip cycles
// through (logical px). 8 is the long-standing default (layoutGridDesign).
var layoutGridSizes = []int{4, 8, 16, 32}

// nextLayoutGridSize returns the cycle successor of the current grid size —
// unknown/legacy values restart the cycle at the first entry.
func nextLayoutGridSize(cur int) int {
	for i, g := range layoutGridSizes {
		if g == cur {
			return layoutGridSizes[(i+1)%len(layoutGridSizes)]
		}
	}
	return layoutGridSizes[0]
}

// alignGuide is one matched alignment this drag frame: a full-length vertical
// line at pos (an x) or horizontal line at pos (a y), drawn by the editor so
// the user sees what the box just snapped to.
type alignGuide struct {
	vertical bool
	pos      int32
}

// axisCand is one snap candidate on a single axis: the dragged box's own
// coordinate (cur) and how to apply a delta to the rect.
type axisCand struct {
	cur int32
	// apply moves the rect so cur lands on target: whole-box translate for a
	// move, single-edge adjust for a resize.
	apply func(r sdl.Rect, d int32) sdl.Rect
}

// alignRect snaps r against the other slots' edges/centres and the window
// bounds/centre. move selects translate semantics; otherwise only the gripped
// edges participate. guides is appended in place (reused by the caller across
// frames) and returned. Pure — unit-tested without SDL.
func alignRect(r sdl.Rect, others []sdl.Rect, w, h int32, move bool, edges uint8, guides []alignGuide) (sdl.Rect, []alignGuide) {
	// X axis --------------------------------------------------------------
	moveX := func(r sdl.Rect, d int32) sdl.Rect { r.X += d; return r }
	growR := func(r sdl.Rect, d int32) sdl.Rect { r.W += d; return r }
	growL := func(r sdl.Rect, d int32) sdl.Rect { r.X += d; r.W -= d; return r }
	var xc []axisCand
	if move {
		xc = []axisCand{
			{r.X, moveX},         // left edge
			{r.X + r.W, moveX},   // right edge
			{r.X + r.W/2, moveX}, // centre
		}
	} else {
		if edges&edgeL != 0 {
			xc = append(xc, axisCand{r.X, growL})
		}
		if edges&edgeR != 0 {
			xc = append(xc, axisCand{r.X + r.W, growR})
		}
	}
	if len(xc) > 0 {
		if target, cand, ok := bestAlign(xc, others, w, true); ok {
			r = cand.apply(r, target-cand.cur)
			guides = append(guides, alignGuide{vertical: true, pos: target})
		}
	}
	// Y axis --------------------------------------------------------------
	moveY := func(r sdl.Rect, d int32) sdl.Rect { r.Y += d; return r }
	growB := func(r sdl.Rect, d int32) sdl.Rect { r.H += d; return r }
	growT := func(r sdl.Rect, d int32) sdl.Rect { r.Y += d; r.H -= d; return r }
	var yc []axisCand
	if move {
		yc = []axisCand{
			{r.Y, moveY},
			{r.Y + r.H, moveY},
			{r.Y + r.H/2, moveY},
		}
	} else {
		if edges&edgeT != 0 {
			yc = append(yc, axisCand{r.Y, growT})
		}
		if edges&edgeB != 0 {
			yc = append(yc, axisCand{r.Y + r.H, growB})
		}
	}
	if len(yc) > 0 {
		if target, cand, ok := bestAlign(yc, others, h, false); ok {
			r = cand.apply(r, target-cand.cur)
			guides = append(guides, alignGuide{vertical: false, pos: target})
		}
	}
	// A resize snap must not invert past the minimum box size.
	if !move {
		if r.W < classicMinPx {
			r.W = classicMinPx
		}
		if r.H < classicMinPx {
			r.H = classicMinPx
		}
	}
	return r, guides
}

// bestAlign finds the closest (candidate, target) pair within alignSnapPx on
// one axis. Targets: each other slot's low edge / high edge / centre, plus the
// window's 0 / extent / centre (the "anchor to corners and centre of the
// screen" ask). vertical selects the X axis of the others; extent is w or h.
func bestAlign(cands []axisCand, others []sdl.Rect, extent int32, vertical bool) (int32, axisCand, bool) {
	bestD := int32(alignSnapPx + 1)
	var bestT int32
	var bestC axisCand
	consider := func(target int32) {
		for _, c := range cands {
			d := c.cur - target
			if d < 0 {
				d = -d
			}
			if d < bestD {
				bestD, bestT, bestC = d, target, c
			}
		}
	}
	consider(0)
	consider(extent)
	consider(extent / 2)
	for i := range others {
		o := &others[i]
		if vertical {
			consider(o.X)
			consider(o.X + o.W)
			consider(o.X + o.W/2)
		} else {
			consider(o.Y)
			consider(o.Y + o.H)
			consider(o.Y + o.H/2)
		}
	}
	return bestT, bestC, bestD <= alignSnapPx
}
