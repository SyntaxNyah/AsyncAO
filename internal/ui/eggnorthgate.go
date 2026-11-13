package ui

// The Northgate egg: an aurora hanging over the stage, and a ring that shimmers along the same ramp (eggs.go owns
// the trigger table, the enum and the two dispatches; this owns the pixels).
// It is wholly procedural, because the licensing stance forbids bundling art for a joke. A curtain is a column of
// stacked axis-aligned fills hanging from the top of the stage, its foot rippling on a travelling sine and its
// colour sampled off a green to violet ramp that slides sideways. Every property comes from eggHash(index), so the
// field is a pure function of (index, clock): no allocation, no seeding, and every client in the room draws the
// same sky.
// Unlike the other stage/ring eggs, which are fields of discrete objects scattered over the stage, this one is
// contiguous and anchored: neighbouring columns share a ripple phase so the feet form one hem, and it hangs from
// the top edge. The ring's three rings hold three points on one sliding ramp.

import (
	"math"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// eggNorthCurtains is the named cap on the curtain field (hard rule 4): the draw is exactly this many columns
	// whatever the stage size. Enough that the hem reads as continuous at 1280 px.
	eggNorthCurtains = 16
	// eggNorthBands is how many stacked rects build one curtain, top to foot. 5 is the fewest that still fades out
	// rather than ending in a hard line.
	eggNorthBands = 5
	// eggNorthHeightMinFrac / MaxFrac bound a curtain's hang as a fraction of stage height, so the sky scales with
	// the
	// stage. Never the full height: a curtain reaching the floor would curtain off the speaking sprite.
	eggNorthHeightMinFrac = 0.20
	eggNorthHeightMaxFrac = 0.52
	// eggNorthRippleSecs is how long the hem takes to complete one travelling wave, and eggNorthRipplePhasePerCol how
	// much of the wave sits between neighbouring columns. A small per-column phase makes the field one curtain.
	eggNorthRippleSecs        = 7.0
	eggNorthRipplePhasePerCol = 0.55
	// eggNorthRippleFrac is how much of its own height a curtain's foot travels over the ripple. Gentle.
	eggNorthRippleFrac = 0.22
	// eggNorthHueLo / eggNorthHueHi are the ramp's ends on the hue wheel: 0.34 is oxygen green, 0.78 the violet
	// fringe.
	// The ramp stays between them, away from the rainbow egg.
	eggNorthHueLo = 0.34
	eggNorthHueHi = 0.78
	// eggNorthDriftSecs is how long the ramp takes to slide once across the field. Slow.
	eggNorthDriftSecs = 13.0
	// eggNorthSaturation keeps the curtains luminous so the stage art reads through.
	eggNorthSaturation = 0.62
	// eggNorthAlphaHead is a curtain's opacity at its top edge and eggNorthAlphaFoot at its lowest band; the fade is
	// the silhouette.
	eggNorthAlphaHead = 118
	eggNorthAlphaFoot = 14
	// eggNorthShimmerSecs is the period of a curtain's brightness breath and eggNorthShimmerDepth how much alpha it
	// swings. Not the ripple period, so colour and shape don't pulse in lockstep.
	eggNorthShimmerSecs  = 3.7
	eggNorthShimmerDepth = 0.35
	// eggNorthRingRampStep offsets each successive chatbox ring along the ramp.
	eggNorthRingRampStep = 0.26
	// eggNorthRingShimmerSecs is the ring's own breath period, on its own clock like the curtains.
	eggNorthRingShimmerSecs = 2.9
)

// eggAuroraColor samples the aurora ramp at u in [0,1), wrapping by folding the top half back down. A straight
// lerp would snap from violet to green at the wrap point each drift cycle.
func eggAuroraColor(u, sat float64) sdl.Color {
	u = math.Mod(u, 1)
	if u < 0 {
		u += 1
	}
	if u > 0.5 {
		u = 1 - u
	}
	r, g, b := hsvToRGB(eggNorthHueLo+(eggNorthHueHi-eggNorthHueLo)*(u*2), sat, 1)
	return sdl.Color{R: r, G: g, B: b, A: 255}
}

// eggNorthShimmer is a 0..1 breath for a curtain (or ring) offset by phase: a plain cosine, since this is a swell
// rather than a flash (eggShardGlint is the flash).
func eggNorthShimmer(t, phase, period float64) float64 {
	return 0.5 * (1 + math.Cos(2*math.Pi*(math.Mod(t/period, 1)+phase)))
}

// drawEggAurora paints the curtain field across vp: columns hanging from the stage's top edge, feet riding one
// travelling ripple, colour from a sliding green to violet ramp. Clipped to the stage and allocation-free: fixed
// counts, no slices or closures, and every rect passed by value through c.Fill (a &local would escape through cgo).
func (a *App) drawEggAurora(vp sdl.Rect, t float64) {
	c := a.ctx
	if vp.W <= 0 || vp.H <= 0 {
		return
	}
	prev, had := c.pushClip(vp)
	rampBase := math.Mod(t/eggNorthDriftSecs, 1)
	// One column width for the whole field so the curtains abut and the hem is continuous. +1 avoids a gap
	// column.
	colW := vp.W/eggNorthCurtains + 1
	for i := int32(0); i < eggNorthCurtains; i++ {
		// Two independent draws from one index, with separate salts, so a column's hang and breath never correlate.
		size := eggUnit(eggHash(uint32(i)*2 + 0))
		phase := eggUnit(eggHash(uint32(i)*2 + 1))

		// The ripple phase advances by a small step per column, so neighbours move together.
		ripple := math.Sin(2*math.Pi*math.Mod(t/eggNorthRippleSecs, 1) + float64(i)*eggNorthRipplePhasePerCol)
		frac := eggNorthHeightMinFrac + size*(eggNorthHeightMaxFrac-eggNorthHeightMinFrac)
		frac *= 1 + ripple*eggNorthRippleFrac
		h := int32(frac * float64(vp.H))
		if h < eggNorthBands {
			h = eggNorthBands // one row per band minimum, or the fade collapses
		}
		bandH := h / eggNorthBands
		if bandH < 1 {
			bandH = 1
		}
		// The column's own place on the ramp, with the whole ramp sliding.
		col := eggAuroraColor(rampBase+phase, eggNorthSaturation)
		breath := 1 - eggNorthShimmerDepth + eggNorthShimmerDepth*eggNorthShimmer(t, phase, eggNorthShimmerSecs)
		x := vp.X + i*vp.W/eggNorthCurtains
		for b := int32(0); b < eggNorthBands; b++ {
			// Alpha walks head to foot across the bands; the fade is the silhouette.
			f := float64(b) / float64(eggNorthBands-1)
			alpha := float64(eggNorthAlphaHead) + (float64(eggNorthAlphaFoot)-float64(eggNorthAlphaHead))*f
			col.A = uint8(alpha * breath)
			c.Fill(sdl.Rect{X: x, Y: vp.Y + b*bandH, W: colW, H: bandH}, col)
		}
	}
	c.popClip(prev, had)
}

// drawEggAuroraRing is the Northgate chatbox half: three rings holding three points on the same sliding ramp, the
// band breathing on its own clock. Unlike the Crystalwarrior ring (three hue-wheel points washing to white) and the
// FanatSors rainbow (one colour chasing the wheel).
func (a *App) drawEggAuroraRing(box, win sdl.Rect, t float64) {
	c := a.ctx
	base := math.Mod(t/eggNorthDriftSecs, 1)
	breath := 1 - eggNorthShimmerDepth + eggNorthShimmerDepth*eggNorthShimmer(t, 0, eggNorthRingShimmerSecs)
	for i := int32(0); i < eggRingCount; i++ {
		ring := outsetRing(box, (i+1)*eggRingGap)
		if !ringVisible(ring, win) {
			continue
		}
		col := eggAuroraColor(base+float64(i)*eggNorthRingRampStep, eggNorthSaturation)
		col.A = uint8(float64(eggRingAlpha(i)) * breath)
		c.Border(ring, col)
	}
}
