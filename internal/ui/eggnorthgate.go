package ui

// THE NORTHGATE EGG: an aurora hanging over the stage, and a ring that shimmers
// along the same ramp. One concern, one file (eggs.go owns the trigger table, the
// enum and the two dispatches; this owns the pixels).
//
// WHOLLY PROCEDURAL, like the petals and the shards, because the licensing stance
// forbids bundling art for a joke. A curtain is a COLUMN of stacked axis-aligned
// fills hanging from the top of the stage, its foot rippling on a travelling sine
// and its colour sampled off a green→violet ramp that slides sideways. Every
// property comes from eggHash(index), so the field is a pure function of (index,
// clock): no allocation, no seeding, no state to reset when the message changes,
// and every AsyncAO client in the room draws the identical sky.
//
// WHY IT READS AS DIFFERENT FROM THE OTHER FOUR STAGE/RING EGGS. Scatterflower
// falls, Crystalwarrior rises, and both are fields of DISCRETE objects scattered
// across the whole stage. This one is CONTIGUOUS and ANCHORED: neighbouring
// columns share a ripple phase, so the feet form one continuous hem rather than a
// crowd of independent sprites, and the whole thing hangs from the top edge
// instead of drifting through the middle. The ring is the same distinction — its
// three rings hold three points on ONE sliding ramp (Crystalwarrior's hold three
// points on the hue wheel and flash white together; FanatSors' is one colour
// chasing round it).

import (
	"math"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	// eggNorthCurtains is the named cap on the curtain field (hard rule 4): the
	// draw is exactly this many columns whatever the stage measures, so the egg
	// costs the same at 4K as at 720p. Enough that the hem reads as continuous at
	// 1280 px without the columns becoming a picket fence.
	eggNorthCurtains = 16
	// eggNorthBands is how many stacked rects build one curtain, top to foot. The
	// per-frame fill count is pinned at eggNorthCurtains*eggNorthBands, and 5 is
	// the fewest that still fades out rather than ending in a hard line.
	eggNorthBands = 5
	// eggNorthHeightMinFrac / MaxFrac bound a curtain's hang as a fraction of
	// STAGE HEIGHT, so the sky scales with the stage instead of shrinking to a
	// stripe on a big screen (the mistake a pixel constant would make). Never the
	// full height: an aurora is weather at the top of the frame, and a curtain
	// that reached the floor would curtain off the speaking sprite.
	eggNorthHeightMinFrac = 0.20
	eggNorthHeightMaxFrac = 0.52
	// eggNorthRippleSecs is how long the hem takes to complete one travelling
	// wave, and eggNorthRipplePhasePerCol how much of that wave sits between two
	// neighbouring columns. Together they are what makes the field one CURTAIN
	// rather than sixteen independent bars: a small per-column phase means
	// neighbours move almost together.
	eggNorthRippleSecs        = 7.0
	eggNorthRipplePhasePerCol = 0.55
	// eggNorthRippleFrac is how much of its own height a curtain's foot travels
	// over the ripple, as a fraction. Gentle: the aurora breathes, it does not
	// flap.
	eggNorthRippleFrac = 0.22
	// eggNorthHueLo / eggNorthHueHi are the ramp's ends on the hue wheel: 0.34 is
	// the aurora's signature oxygen green and 0.78 the rarer violet fringe. The
	// ramp runs between them and nowhere else, which is what keeps the sky from
	// wandering into the rainbow egg's territory.
	eggNorthHueLo = 0.34
	eggNorthHueHi = 0.78
	// eggNorthDriftSecs is how long the ramp takes to slide once across the field.
	// Slow — the colour change should be something you notice having happened, not
	// something you watch happening.
	eggNorthDriftSecs = 13.0
	// eggNorthSaturation keeps the curtains luminous rather than poster-paint: an
	// aurora is light in the air, and the stage art must read straight through it.
	eggNorthSaturation = 0.62
	// eggNorthAlphaHead is a curtain's opacity at the top edge it hangs from and
	// eggNorthAlphaFoot at its lowest band. Both translucent, and the foot much
	// more so, which is the whole silhouette: the fade IS the shape.
	eggNorthAlphaHead = 118
	eggNorthAlphaFoot = 14
	// eggNorthShimmerSecs is the period of a curtain's brightness breath and
	// eggNorthShimmerDepth how much of its alpha that breath swings. Deliberately
	// NOT the ripple period, so a column's colour and its shape do not pulse in
	// lockstep and read as one blinking object.
	eggNorthShimmerSecs  = 3.7
	eggNorthShimmerDepth = 0.35
	// eggNorthRingRampStep offsets each successive chatbox ring along the ramp, so
	// the band reads as the same sky seen three steps apart.
	eggNorthRingRampStep = 0.26
	// eggNorthRingShimmerSecs is the ring's own breath period — its own clock
	// again, for the same reason the curtains' is not the ripple's.
	eggNorthRingShimmerSecs = 2.9
)

// eggAuroraColor samples the aurora ramp at u in [0,1), wrapping continuously by
// folding the top half of u back down the ramp. A straight lerp would snap from
// violet to green at the seam every drift cycle; folding makes the ramp travel
// out and back, which is both continuous and what a real curtain's colour does.
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

// eggNorthShimmer is a 0..1 breath for a curtain (or ring) whose cycle is offset
// by phase: a plain cosine, because this one IS a swell rather than a flash —
// eggShardGlint is the flash, and the two must not be confused.
func eggNorthShimmer(t, phase, period float64) float64 {
	return 0.5 * (1 + math.Cos(2*math.Pi*(math.Mod(t/period, 1)+phase)))
}

// drawEggAurora paints the Northgate curtain field across vp: columns hanging
// from the stage's top edge, their feet riding one travelling ripple, their
// colour sampled off a slowly sliding green→violet ramp. Clipped to the stage so
// a curtain can never paint over the surrounding chrome, and ZERO-allocation —
// fixed counts, no slices, no closures, every rect a value through c.Fill (a
// &local would escape through cgo and heap-allocate per fill).
func (a *App) drawEggAurora(vp sdl.Rect, t float64) {
	c := a.ctx
	if vp.W <= 0 || vp.H <= 0 {
		return
	}
	prev, had := c.pushClip(vp)
	rampBase := math.Mod(t/eggNorthDriftSecs, 1)
	// One column width for the whole field: the curtains ABUT, which is what makes
	// the hem continuous. +1 so integer division never leaves a bare seam column.
	colW := vp.W/eggNorthCurtains + 1
	for i := int32(0); i < eggNorthCurtains; i++ {
		// Two independent draws from one index — separate salts, so a column's hang
		// and its breath never correlate (the petal field's rule).
		size := eggUnit(eggHash(uint32(i)*2 + 0))
		phase := eggUnit(eggHash(uint32(i)*2 + 1))

		// The hang, rippled. The ripple phase advances by a SMALL step per column,
		// so neighbours move together and the feet form one hem.
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
		// The colour: this column's own place on the ramp, the whole ramp sliding.
		col := eggAuroraColor(rampBase+phase, eggNorthSaturation)
		breath := 1 - eggNorthShimmerDepth + eggNorthShimmerDepth*eggNorthShimmer(t, phase, eggNorthShimmerSecs)
		x := vp.X + i*vp.W/eggNorthCurtains
		for b := int32(0); b < eggNorthBands; b++ {
			// Alpha walks head→foot across the bands: the fade IS the silhouette, so
			// this is the shape and not a decoration on it.
			f := float64(b) / float64(eggNorthBands-1)
			alpha := float64(eggNorthAlphaHead) + (float64(eggNorthAlphaFoot)-float64(eggNorthAlphaHead))*f
			col.A = uint8(alpha * breath)
			c.Fill(sdl.Rect{X: x, Y: vp.Y + b*bandH, W: colW, H: bandH}, col)
		}
	}
	c.popClip(prev, had)
}

// drawEggAuroraRing — the Northgate chatbox half. Three rings holding three
// points on the SAME sliding ramp, the whole band breathing on its own clock.
// Distinct from the Crystalwarrior ring (three points on the full hue wheel,
// washing to white together) and from the FanatSors rainbow (one colour chasing
// the wheel with the rings barely apart).
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
