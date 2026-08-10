package ui

// THE MINT EGG: frost creeping in from the stage edges with cocoa flecks drifting
// through it, and a ring banded mint / cocoa. One concern, one file (eggs.go owns
// the trigger table, the enum and the two dispatches; this owns the pixels).
//
// WHOLLY PROCEDURAL — no bundled art, per the licensing stance. A frost finger is
// ONE axis-aligned bar growing inward from an edge, its length breathing on its
// own phase; a fleck is one small dark square. Every property comes from
// eggHash(index), so the field is a pure function of (index, clock): no
// allocation, no seeding, no state to reset between messages, and every AsyncAO
// client in the room draws the identical frost.
//
// WHY IT READS AS DIFFERENT FROM THE OTHER STAGE EGGS. The other three are fields
// scattered through the MIDDLE of the stage and they travel on the vertical axis
// — petals fall, shards rise, the aurora hangs. This one is anchored to the
// PERIMETER and grows inward, and its flecks drift SIDEWAYS, so nothing about its
// motion collides with theirs. The ring is the same idea: two flat colours
// alternating ring by ring, the assignment marching outward, as against the
// continuous lerps and hue walks every other egg's ring does. Mint chocolate,
// said in the only two shapes a fill can make.

import (
	"math"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	// eggMintFingers is the named cap on the frost field PER EDGE (hard rule 4),
	// and the egg draws two edges — top and bottom — so the per-frame fill count
	// is pinned at 2*eggMintFingers + eggMintFlecks whatever the stage measures.
	// Only two edges: the left and right of a 4:3 stage are where a standing
	// sprite's shoulders are, and frost there covers the character the message is
	// about.
	eggMintFingers = 22
	// eggMintReachMinFrac / MaxFrac bound how far a finger grows in, as a fraction
	// of STAGE HEIGHT, so the frost scales with the stage rather than shrinking to
	// a hairline on a big screen. Kept shallow: this is a rime around the frame,
	// not a whiteout.
	eggMintReachMinFrac = 0.04
	eggMintReachMaxFrac = 0.17
	// eggMintWidthMin / Max bound one finger's width in px. Narrow and varied, so
	// the edge reads as crystal growth instead of a gradient bar.
	eggMintWidthMin = int32(2)
	eggMintWidthMax = int32(7)
	// eggMintCreepSecs is how long a finger takes to grow and retreat once, and
	// eggMintCreepVary the fraction each finger scales that by. The variance is
	// what stops the whole edge from pumping as one object.
	eggMintCreepSecs = 5.5
	eggMintCreepVary = 0.7
	// eggMintReachFloor is the fraction of its reach a finger keeps at the bottom
	// of its breath. Non-zero, so the rime never fully lets go of the frame and
	// the edge stops flickering in and out of existence.
	eggMintReachFloor = 0.25
	// eggMintAlphaRoot is a finger's opacity where it meets the edge and
	// eggMintAlphaTip at its inner point — the taper is done in ALPHA rather than
	// in width, which is the cheap way to get a soft crystal out of one rect.
	eggMintAlphaRoot = 150
	eggMintAlphaTip  = 26
	// eggMintSegments is how many stacked rects build one finger's alpha taper.
	// 3 is enough to read as a fade at these lengths and keeps the fill count low
	// enough that two edges of them cost less than the petal field.
	eggMintSegments = 3

	// eggMintFlecks is the named cap on the cocoa fleck field. Sparse on purpose:
	// they are the chips in the ice cream, not a second weather system.
	eggMintFlecks = 14
	// eggMintFleckDriftSecs is how long a fleck takes to cross the stage
	// horizontally and eggMintFleckDriftVary the per-fleck variance. Slow — a
	// chip suspended in something cold, not a chip being thrown.
	eggMintFleckDriftSecs = 12.0
	eggMintFleckDriftVary = 0.5
	// eggMintFleckSizeMin / Max bound a fleck's square edge in px.
	eggMintFleckSizeMin = int32(2)
	eggMintFleckSizeMax = int32(6)
	// eggMintFleckAlpha is a fleck's opacity at full fade-in. Dark on a lit stage
	// reads strongly, so this is the lowest value at which they are still legible.
	eggMintFleckAlpha = 130
	// eggMintFleckBobFrac is how far a fleck wanders vertically over its crossing,
	// as a fraction of STAGE HEIGHT (so the wander scales with the stage, like
	// every other length in this egg), and eggMintFleckBobCycles how many times it
	// does so. Small: the drift is horizontal, and the bob is only there to stop
	// the lane reading as a rail.
	eggMintFleckBobFrac   = 0.03
	eggMintFleckBobCycles = 1.5

	// eggMintBandSecs is how long the ring's two-tone assignment takes to march
	// one ring outward. Discrete, and slow enough to be a colour scheme rather
	// than a strobe: this egg spends its motion budget on the stage.
	eggMintBandSecs = 2.4
)

// THE THREE HASH WINDOWS. Every element of this egg takes FOUR independent draws
// from eggHash(index) — lane, size, speed, phase — so a field of n elements owns
// the 4n indices starting at its salt. The windows must not overlap or two
// elements share all four of their properties, which is the correlation the
// "separate salts" rule in drawEggPetals exists to prevent; the three of them lay
// out like this:
//
//	top frost edge     0                 .. 4*eggMintFingers-1 (0..87)
//	bottom frost edge  eggMintBottomSalt .. +4*eggMintFingers-1 (977..1064)
//	cocoa flecks       eggMintFleckSalt  .. +4*eggMintFlecks-1  (1601..1656)
//
// The values themselves are arbitrary beyond clearing the window before them —
// eggHash is a murmur finalizer, so any disjoint run decorrelates as well as any
// other. TestMintHashWindowsAreDisjoint is what keeps them clear when a field's
// cap grows. (The flecks used to start at 0, i.e. entirely inside the top edge's
// window, so fleck i wore frost finger i's four numbers; the two spend them on
// different axes and different time bases, which is why it was invisible rather
// than harmless.)
const (
	eggMintBottomSalt = uint32(977)
	eggMintFleckSalt  = uint32(1601)
)

// eggMintGreen and eggMintCocoa are the two flat colours the whole egg is made
// of — a pale creme-de-menthe and a dark cocoa. Package-level so the per-frame
// draw copies a value instead of building one.
var (
	eggMintGreen = sdl.Color{R: 0x9f, G: 0xe8, B: 0xc4, A: 255} // creme de menthe
	eggMintCocoa = sdl.Color{R: 0x3a, G: 0x24, B: 0x1c, A: 255} // dark cocoa
)

// eggMintCreep is a finger's reach as a fraction of its own maximum at time t,
// for a finger whose cycle is offset by phase: a cosine breath floored at
// eggMintReachFloor, so the rime advances and retreats without ever vanishing.
func eggMintCreep(t, phase, period float64) float64 {
	swell := 0.5 * (1 + math.Cos(2*math.Pi*(math.Mod(t/period, 1)+phase)))
	return eggMintReachFloor + (1-eggMintReachFloor)*swell
}

// eggMintBandIsCocoa reports whether ring i wears cocoa at time t. The
// assignment alternates by ring and MARCHES outward one step per eggMintBandSecs,
// which is the whole motion of the chatbox half — two flat colours trading
// places, never a lerp.
func eggMintBandIsCocoa(i int32, t float64) bool {
	step := int32(t / eggMintBandSecs)
	return (i+step)%2 == 1
}

// drawEggFrost paints the Mint stage half: frost fingers growing in from the top
// and bottom edges, with cocoa flecks drifting sideways between them. Clipped to
// the stage so nothing reaches the surrounding chrome, and ZERO-allocation —
// fixed counts, no slices, no closures, every rect a value through c.Fill (a
// &local would escape through cgo and heap-allocate per fill).
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

// drawEggFrostEdge grows one edge's fingers. ONE function for both edges rather
// than two loops with the signs flipped: the mirrored copy is where a tuning
// change gets applied to the top and forgotten at the bottom.
func (a *App) drawEggFrostEdge(vp sdl.Rect, t float64, fromBottom bool) {
	c := a.ctx
	// A distinct salt per edge, so the two edges are not each other's mirror
	// image — which is exactly what a viewer notices. See the hash-window map
	// above the constants for why the bottom's starts where it does.
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
			// Alpha walks root→tip; the taper is the silhouette.
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

// drawEggFlecks paints the cocoa chips drifting SIDEWAYS across the stage — the
// one horizontal motion in the whole egg roster, which is most of what keeps this
// field from reading as somebody else's weather.
func (a *App) drawEggFlecks(vp sdl.Rect, t float64) {
	c := a.ctx
	for i := uint32(0); i < eggMintFlecks; i++ {
		// The flecks' own window, clear of both frost edges' (see the map above the
		// constants): a chip that shared a finger's four numbers would be a chip
		// whose lane, size, drift and phase were all decided by that finger.
		idx := i*4 + eggMintFleckSalt
		lane := eggUnit(eggHash(idx + 0))
		speed := eggUnit(eggHash(idx + 1))
		size := eggUnit(eggHash(idx + 2))
		phase := eggUnit(eggHash(idx + 3))

		// p walks 0 (stage left) → 1 (stage right) over this fleck's own crossing.
		cross := eggMintFleckDriftSecs * (1 - eggMintFleckDriftVary/2 + speed*eggMintFleckDriftVary)
		p := math.Mod(t/cross+phase, 1)

		bob := math.Sin((p*eggMintFleckBobCycles + phase) * 2 * math.Pi)
		x := vp.X + int32(p*float64(vp.W))
		y := vp.Y + int32(lane*float64(vp.H)) + int32(bob*eggMintFleckBobFrac*float64(vp.H))

		e := eggMintFleckSizeMin + int32(size*float64(eggMintFleckSizeMax-eggMintFleckSizeMin))
		col := eggMintCocoa
		// Fade in and out at the lane ends for the same reason petals do: a chip
		// that pops into existence on the clip line reads as a glitch.
		col.A = uint8(float64(eggMintFleckAlpha) * eggPetalFade(p))
		c.Fill(sdl.Rect{X: x, Y: y, W: e, H: e}, col)
	}
}

// drawEggMintRing — the Mint chatbox half. The three rings alternate the egg's
// two flat colours and the assignment marches outward, so the band reads as
// mint-chocolate stripes turning over rather than as a glow. Deliberately the
// only DISCRETE ring in the roster: every other one lerps or walks a wheel, so
// two flat colours trading places is the cheapest way to be unmistakable.
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
