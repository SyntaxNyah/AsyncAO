package ui

// The Mint egg: frost from the stage edges with cocoa flecks, and a ring banded mint and cocoa.
// Procedural, a pure function of (index, clock), so no state and every client draws the same frost.

import (
	"math"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// eggMintFingers caps the frost per edge (hard rule 4). Top and bottom only: the sides would cover the shoulders.
	eggMintFingers = 22
	// Finger reach bounds as fractions of stage height, so the frost scales with the stage.
	eggMintReachMinFrac = 0.04
	eggMintReachMaxFrac = 0.17
	// eggMintWidthMin and eggMintWidthMax bound a finger's width in px.
	eggMintWidthMin = int32(2)
	eggMintWidthMax = int32(7)
	// eggMintCreepSecs is one grow and retreat; eggMintCreepVary scales it per finger so the edge does not pump as one.
	eggMintCreepSecs = 5.5
	eggMintCreepVary = 0.7
	// eggMintReachFloor is the reach kept at the bottom of the breath, so the edge does not flicker out.
	eggMintReachFloor = 0.25
	// Finger opacity at the edge and at the tip; the taper is in alpha.
	eggMintAlphaRoot = 150
	eggMintAlphaTip  = 26
	// eggMintSegments is how many stacked rects build one finger's alpha taper.
	eggMintSegments = 3

	// eggMintFlecks caps the cocoa flecks.
	eggMintFlecks = 14
	// eggMintFleckDriftSecs is the time to cross the stage; eggMintFleckDriftVary is the per-fleck variance.
	eggMintFleckDriftSecs = 12.0
	eggMintFleckDriftVary = 0.5
	// eggMintFleckSizeMin and eggMintFleckSizeMax bound a fleck's edge in px.
	eggMintFleckSizeMin = int32(2)
	eggMintFleckSizeMax = int32(6)
	// eggMintFleckAlpha is the lowest opacity at which flecks stay legible.
	eggMintFleckAlpha = 130
	// Vertical wander as a fraction of stage height, and its cycles per crossing, so the lane is not a straight rail.
	eggMintFleckBobFrac   = 0.03
	eggMintFleckBobCycles = 1.5

	// eggMintBandSecs is how long the ring's colours take to march one ring outward.
	eggMintBandSecs = 2.4
)

// Each element takes four draws from eggHash(index), so a field of n owns 4n indices from its salt.
// The windows must not overlap, or elements share all four properties. Top edge starts at 0, and values are
// arbitrary beyond clearing the previous window: bottom edge 977..1064, flecks 1601..1656.
const (
	eggMintBottomSalt = uint32(977)
	eggMintFleckSalt  = uint32(1601)
)

// The egg's two flat colours, package-level so the per-frame draw copies a value.
var (
	eggMintGreen = sdl.Color{R: 0x9f, G: 0xe8, B: 0xc4, A: 255} // creme de menthe
	eggMintCocoa = sdl.Color{R: 0x3a, G: 0x24, B: 0x1c, A: 255} // dark cocoa
)

// eggMintCreep is a finger's reach as a fraction of its maximum: a cosine breath floored at eggMintReachFloor.
func eggMintCreep(t, phase, period float64) float64 {
	swell := 0.5 * (1 + math.Cos(2*math.Pi*(math.Mod(t/period, 1)+phase)))
	return eggMintReachFloor + (1-eggMintReachFloor)*swell
}

// eggMintBandIsCocoa reports whether ring i wears cocoa at time t; the assignment alternates and marches outward.
func eggMintBandIsCocoa(i int32, t float64) bool {
	step := int32(t / eggMintBandSecs)
	return (i+step)%2 == 1
}

// drawEggFrost paints the Mint stage half clipped to vp; rects are values through c.Fill, as a &local would escape.
func (a *App) drawEggFrost(vp sdl.Rect, t float64) {
	c := a.ctx
	if vp.W <= 0 || vp.H <= 0 {
		return
	}
	prev, had := c.pushClip(vp)
	a.drawEggFrostEdge(vp, t, false) // top edge, growing down
	a.drawEggFrostEdge(vp, t, true)  // bottom edge, growing up
	a.drawEggFlecks(vp, t)
	c.popClip(prev, had)
}

// drawEggFrostEdge grows one edge's fingers, shared so a tuning change cannot miss one edge.
func (a *App) drawEggFrostEdge(vp sdl.Rect, t float64, fromBottom bool) {
	c := a.ctx
	// A distinct salt per edge, so the two are not mirror images.
	salt := uint32(0)
	if fromBottom {
		salt = eggMintBottomSalt
	}
	for i := int32(0); i < eggMintFingers; i++ {
		idx := uint32(i)*4 + salt
		lane := eggUnit(eggHash(idx + 0))
		size := eggUnit(eggHash(idx + 1))
		speed := eggUnit(eggHash(idx + 2))
		phase := eggUnit(eggHash(idx + 3))

		period := eggMintCreepSecs * (1 - eggMintCreepVary/2 + speed*eggMintCreepVary)
		reach := (eggMintReachMinFrac + size*(eggMintReachMaxFrac-eggMintReachMinFrac)) * float64(vp.H)
		reach *= eggMintCreep(t, phase, period)
		h := int32(reach)
		if h < eggMintSegments {
			h = eggMintSegments // one row per segment minimum, or the taper collapses
		}
		w := eggMintWidthMin + int32(size*float64(eggMintWidthMax-eggMintWidthMin))
		x := vp.X + int32(lane*float64(vp.W))
		segH := h / eggMintSegments
		if segH < 1 {
			segH = 1
		}
		col := eggMintGreen
		for s := int32(0); s < eggMintSegments; s++ {
			f := float64(s) / float64(eggMintSegments-1)
			col.A = uint8(float64(eggMintAlphaRoot) + (float64(eggMintAlphaTip)-float64(eggMintAlphaRoot))*f)
			y := vp.Y + s*segH
			if fromBottom {
				y = vp.Y + vp.H - (s+1)*segH
			}
			c.Fill(sdl.Rect{X: x, Y: y, W: w, H: segH}, col)
		}
	}
}

// drawEggFlecks paints the cocoa flecks drifting sideways across the stage.
func (a *App) drawEggFlecks(vp sdl.Rect, t float64) {
	c := a.ctx
	for i := uint32(0); i < eggMintFlecks; i++ {
		// The flecks' own hash window, clear of both frost edges.
		idx := i*4 + eggMintFleckSalt
		lane := eggUnit(eggHash(idx + 0))
		speed := eggUnit(eggHash(idx + 1))
		size := eggUnit(eggHash(idx + 2))
		phase := eggUnit(eggHash(idx + 3))

		cross := eggMintFleckDriftSecs * (1 - eggMintFleckDriftVary/2 + speed*eggMintFleckDriftVary)
		p := math.Mod(t/cross+phase, 1)

		bob := math.Sin((p*eggMintFleckBobCycles + phase) * 2 * math.Pi)
		x := vp.X + int32(p*float64(vp.W))
		y := vp.Y + int32(lane*float64(vp.H)) + int32(bob*eggMintFleckBobFrac*float64(vp.H))

		e := eggMintFleckSizeMin + int32(size*float64(eggMintFleckSizeMax-eggMintFleckSizeMin))
		col := eggMintCocoa
		// Fade at the lane ends, so a fleck does not pop in on the clip line.
		col.A = uint8(float64(eggMintFleckAlpha) * eggPetalFade(p))
		c.Fill(sdl.Rect{X: x, Y: y, W: e, H: e}, col)
	}
}

// drawEggMintRing draws the Mint chatbox half: three rings alternating two flat colours, marching outward.
func (a *App) drawEggMintRing(box, win sdl.Rect, t float64) {
	c := a.ctx
	for i := int32(0); i < eggRingCount; i++ {
		ring := outsetRing(box, (i+1)*eggRingGap)
		if !ringVisible(ring, win) {
			continue
		}
		col := eggMintGreen
		if eggMintBandIsCocoa(i, t) {
			col = eggMintCocoa
		}
		col.A = eggRingAlpha(i)
		c.Border(ring, col)
	}
}
