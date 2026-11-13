package ui

// Creator easter eggs: a displayed IC message naming a creator grows an animated glow ring around the chatbox.
// Detection reads the received text, so every AsyncAO client shows it with no wire changes.

import (
	"math"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Egg kinds in creatorEgg's match-priority order, the client lineage first; eggScatter is an inspiration, so last.
const (
	eggNone    uint8 = iota // no creator mentioned — the common case, no glow draws
	eggFanat                // FanatSors — creator of Attorney Online (rainbow ring)
	eggOmni                 // OmniTroid — creator of AO2 (blue<->gold pulse + scanner sweep)
	eggCrystal              // Crystalwarrior — AO2's keeper (prismatic shards over the STAGE + a refracting ring)
	eggNyah                 // SyntaxNyah — creator of AsyncAO (Mayo-pink heartbeat glow)
	eggScatter              // Scatterflower — petals over the STAGE + a petal-toned ring
	// Guests sort last because the switch is first-match: appending cannot change what any message already lit.
	eggNorthgate // Northgate — an aurora hanging over the STAGE + a shimmering ramp ring
	eggMint      // Mint — frost creeping in over the STAGE + a mint/cocoa banded ring
)

// Trigger substrings, matched case-insensitively. Full handles: "fanat" or "omni" would fire on "fanatic" or "android".
const (
	eggNameFanat = "fanatsors"
	eggNameOmni  = "omnitroid"
	// Whole: "crystal" and "warrior" alone are ordinary courtroom words.
	eggNameCrystal = "crystalwarrior"
	eggNameNyah    = "syntaxnyah"
	eggNameScatter = "scatterflower"
	// A compound that does not occur in ordinary English, so plain Contains is safe.
	eggNameNorthgate = "northgate"
	// An ordinary word ("mint condition", "peppermint"), so unlike the others it is matched whole (eggWordPresent).
	eggNameMint = "mint"
)

// creatorEgg returns the egg kind a message mentions, or eggNone; it allocates, so call it once per message.
func creatorEgg(text string) uint8 {
	if text == "" {
		return eggNone
	}
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, eggNameFanat):
		return eggFanat
	case strings.Contains(low, eggNameOmni):
		return eggOmni
	case strings.Contains(low, eggNameCrystal):
		return eggCrystal
	case strings.Contains(low, eggNameNyah):
		return eggNyah
	case strings.Contains(low, eggNameScatter):
		return eggScatter
	// Mint last: a line naming both lights the one that could not have matched by accident.
	case strings.Contains(low, eggNameNorthgate):
		return eggNorthgate
	case eggWordPresent(low, eggNameMint):
		return eggMint
	default:
		return eggNone
	}
}

// eggWordPresent reports whether word appears in low (already lower-cased) as a whole word.
// ASCII boundaries only: handles are ASCII, and a leading multi-byte letter reading as a boundary errs toward firing.
func eggWordPresent(low, word string) bool {
	if word == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(low[i:], word)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(word)
		if !eggWordChar(low, start-1) && !eggWordChar(low, end) {
			return true
		}
		i = start + 1
		if i >= len(low) {
			return false
		}
	}
}

// eggWordChar reports whether s[i] is an ASCII letter or digit; out of range is a boundary (false).
func eggWordChar(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	ch := s[i]
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

// eggSceneText returns the displayed message, or "" when the chatbox is hidden. It must agree with
// drawChatOverlay's early return, or petals fall over a stage whose chatbox never drew.
func eggSceneText(sc *courtroom.Scene) string {
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return ""
	}
	return sc.MessageText
}

// refreshEggKind rescans only when text differs from the last-scanned message, reporting whether it rescanned.
func (a *App) refreshEggKind(text string) (scanned bool) {
	if text == a.eggMsg {
		return false
	}
	a.eggMsg = text
	a.eggKind = creatorEgg(text)
	return true
}

// drawChatEgg is the egg entry point for both chatbox draw paths; call after the skin and border have drawn.
func (a *App) drawChatEgg(box sdl.Rect, text string) {
	a.refreshEggKind(text)
	if a.eggKind != eggNone && a.d.Prefs.Load().ScreenEffects && !a.d.Prefs.Load().ReduceMotionOn {
		a.drawCreatorEgg(box, a.eggKind)
		a.NoteAnimating()
	}
}

const (
	// eggRingCount rings are drawn outset around the chatbox; ring 0 hugs the box.
	eggRingCount = 3
	// eggRingGap is the pixel step between rings.
	eggRingGap = int32(3)
	// eggRingAlphaOuter is the outermost ring's alpha; the innermost is full.
	eggRingAlphaOuter = 60
)

const (
	// eggHueCycleSecs is how long the rainbow ring takes to sweep the hue wheel.
	eggHueCycleSecs = 4.0
	// eggHueRingSpread offsets each ring's hue by this fraction of the wheel.
	eggHueRingSpread = 0.06
)

const (
	// eggOmniPulseSecs is the blue and gold breathing period.
	eggOmniPulseSecs = 2.0
	// eggOmniSweepSecs is the scanner segment's trip around the box, faster than the pulse so it reads separately.
	eggOmniSweepSecs = 1.6
	// eggOmniSweepFrac is the segment length as a fraction of the perimeter.
	eggOmniSweepFrac = 0.08
	// eggOmniSweepThick is the scanner segment's thickness in px.
	eggOmniSweepThick = int32(3)
)

// The AO2 courtroom colours the OmniTroid egg pulses between.
var (
	eggBlue = sdl.Color{R: 24, G: 52, B: 150, A: 255}  // deep courtroom blue
	eggGold = sdl.Color{R: 235, G: 190, B: 70, A: 255} // AO2 gold accent
)

const (
	// eggNyahHeartSecs is the heartbeat period: two quick pulses, then rest.
	eggNyahHeartSecs = 1.4
	// Beat phases within the cycle; the back of the cycle stays quiet.
	eggNyahBeat1 = 0.00
	eggNyahBeat2 = 0.22
	// eggNyahBeatWidth is each beat's width as a cycle fraction.
	eggNyahBeatWidth = 0.12
	// eggNyahAlphaFloor keeps the glow faintly visible between beats; eggNyahAlphaPeak is a beat's crest.
	eggNyahAlphaFloor = 40
	eggNyahAlphaPeak  = 200
)

// MayoPink is the SyntaxNyah egg's colour, the Mayo mascot's pink.
var MayoPink = sdl.Color{R: 0xff, G: 0x9e, B: 0xcb, A: 255}

const (
	// eggScatterPetals caps the petals over the stage (hard rule 4), bounding the per-frame fills at any stage size.
	eggScatterPetals = 26
	// eggScatterFallSecs is the base stage-crossing time; eggScatterFallVary scales it per petal to avoid lockstep.
	eggScatterFallSecs = 6.0
	eggScatterFallVary = 0.6
	// eggScatterSwayCycles is the sways per fall; eggScatterSwayFrac is the wander as a fraction of stage width.
	eggScatterSwayCycles = 2.5
	eggScatterSwayFrac   = 0.06
	// eggScatterSizeMin and eggScatterSizeMax bound a petal's long edge in px.
	eggScatterSizeMin = int32(2)
	eggScatterSizeMax = int32(6)
	// eggScatterSpinSecs is the flutter period: SDL_RenderFillRect cannot rotate, so width squashes by |cos| instead.
	eggScatterSpinSecs = 1.1
	// eggScatterFadeFrac is the part of the fall spent fading at each end, so petals never pop at the clip edge.
	eggScatterFadeFrac = 0.15
	// eggScatterAlphaPeak is translucent so the stage art still reads.
	eggScatterAlphaPeak = 210
	// eggScatterBloomSecs is one drift through the palette, slow so the ring does not compete with the petals;
	// eggScatterRingStep is how far along that cycle each ring sits.
	eggScatterBloomSecs = 5.0
	eggScatterRingStep  = 0.08
)

// eggScatterPalette is package-level so the per-frame draw indexes a fixed array.
var eggScatterPalette = [4]sdl.Color{
	{R: 0xff, G: 0xc2, B: 0xd6, A: 255}, // blossom pink
	{R: 0xff, G: 0xe4, B: 0xef, A: 255}, // near-white petal
	{R: 0xf2, G: 0x9c, B: 0xc4, A: 255}, // deeper rose
	{R: 0xff, G: 0xd9, B: 0xa8, A: 255}, // warm cream — a stray pollen mote
}

// drawCreatorEgg paints the active egg's ring outset around box; the caller checks accessibility and marks animating.
func (a *App) drawCreatorEgg(box sdl.Rect, kind uint8) {
	t := a.d.Viewport.AnimClock().Seconds()
	win := sdl.Rect{X: 0, Y: 0, W: a.winW, H: a.winH}
	switch kind {
	case eggFanat:
		a.drawEggRainbow(box, win, t)
	case eggOmni:
		a.drawEggOmni(box, win, t)
	case eggCrystal:
		a.drawEggCrystalRing(box, win, t)
	case eggNyah:
		a.drawEggNyah(box, win, t)
	case eggScatter:
		a.drawEggScatter(box, win, t)
	case eggNorthgate:
		a.drawEggAuroraRing(box, win, t)
	case eggMint:
		a.drawEggMintRing(box, win, t)
	}
}

// drawStageEgg paints the viewport half of an egg; drawn before the frame and chat overlay so it falls behind them.
// It refreshes detection itself: drawChatOverlay returns early on a blankpost, which would keep the old kind.
func (a *App) drawStageEgg(vp sdl.Rect, sc *courtroom.Scene) {
	a.refreshEggKind(eggSceneText(sc))
	if !eggDrawsOnStage(a.eggKind) || !a.d.Prefs.Load().ScreenEffects || a.d.Prefs.Load().ReduceMotionOn {
		return
	}
	t := a.d.Viewport.AnimClock().Seconds()
	switch a.eggKind {
	case eggScatter:
		a.drawEggPetals(vp, t)
	case eggCrystal:
		a.drawEggShards(vp, t)
	case eggNorthgate:
		a.drawEggAurora(vp, t)
	case eggMint:
		a.drawEggFrost(vp, t)
	}
	a.NoteAnimating()
}

// eggDrawsOnStage lists the eggs with a viewport half.
func eggDrawsOnStage(kind uint8) bool {
	switch kind {
	case eggScatter, eggCrystal, eggNorthgate, eggMint:
		return true
	}
	return false
}

func outsetRing(box sdl.Rect, step int32) sdl.Rect {
	return sdl.Rect{X: box.X - step, Y: box.Y - step, W: box.W + step*2, H: box.H + step*2}
}

// eggRingAlpha fades ring i linearly to eggRingAlphaOuter; eggRingCount is fixed, so the divisor is never zero.
func eggRingAlpha(i int32) uint8 {
	return uint8(255 - (255-eggRingAlphaOuter)*i/(eggRingCount-1))
}

// drawEggRainbow draws FanatSors' (Attorney Online) nested rings with a cycling hue.
func (a *App) drawEggRainbow(box, win sdl.Rect, t float64) {
	c := a.ctx
	base := math.Mod(t/eggHueCycleSecs, 1) // [0,1) position on the hue wheel
	for i := int32(0); i < eggRingCount; i++ {
		h := math.Mod(base+float64(i)*eggHueRingSpread, 1)
		r, g, b := hsvToRGB(h, 1, 1) // full sat/value — the ring is pure spectrum
		ring := outsetRing(box, (i+1)*eggRingGap)
		if !ringVisible(ring, win) {
			continue
		}
		c.Border(ring, sdl.Color{R: r, G: g, B: b, A: eggRingAlpha(i)})
	}
}

// drawEggOmni draws OmniTroid's (AO2) blue and gold pulsing border plus a travelling gold segment.
func (a *App) drawEggOmni(box, win sdl.Rect, t float64) {
	c := a.ctx
	phase := math.Mod(t/eggOmniPulseSecs, 1)
	mix := 1 - math.Abs(2*phase-1) // 0 at the ends, 1 at mid-cycle
	col := lerpColor(eggBlue, eggGold, mix)
	for i := int32(0); i < eggRingCount; i++ {
		ring := outsetRing(box, (i+1)*eggRingGap)
		if !ringVisible(ring, win) {
			continue
		}
		col.A = eggRingAlpha(i)
		c.Border(ring, col)
	}
	// Drawn last so it sits over the glow.
	a.drawEggSweep(outsetRing(box, eggRingCount*eggRingGap), win, t)
}

// drawEggSweep paints the OmniTroid scanner segment along the ring's perimeter from the animation clock.
func (a *App) drawEggSweep(ring, win sdl.Rect, t float64) {
	c := a.ctx
	if ring.W <= 0 || ring.H <= 0 {
		return
	}
	perim := 2 * float64(ring.W+ring.H) // total travel distance around the edge
	start := math.Mod(t/eggOmniSweepSecs, 1) * perim
	length := eggOmniSweepFrac * perim
	th := eggOmniSweepThick
	// One clamped fill per edge touched. A plain loop, since a capturing closure could escape to the heap.
	for length > 0 {
		r, consumed := perimSegment(ring, start, length, th)
		if consumed <= 0 {
			break
		}
		if clampRingToWindow(&r, win) {
			c.Fill(r, eggGold)
		}
		start = math.Mod(start+consumed, perim)
		length -= consumed
	}
}

// perimSegment maps a run at clockwise perimeter offset start to a rect on that edge, returning the length consumed.
func perimSegment(ring sdl.Rect, start, length float64, th int32) (sdl.Rect, float64) {
	w, h := float64(ring.W), float64(ring.H)
	top, right, bottom := w, w+h, w+h+w // edge boundary offsets along the perimeter
	switch {
	case start < top: // top edge, left→right
		run := math.Min(length, top-start)
		x := ring.X + int32(start)
		return sdl.Rect{X: x, Y: ring.Y, W: int32(run) + 1, H: th}, run
	case start < right: // right edge, top→bottom
		d := start - top
		run := math.Min(length, right-start)
		y := ring.Y + int32(d)
		return sdl.Rect{X: ring.X + ring.W - th, Y: y, W: th, H: int32(run) + 1}, run
	case start < bottom: // bottom edge, right→left
		d := start - right
		run := math.Min(length, bottom-start)
		x := ring.X + ring.W - int32(d)
		return sdl.Rect{X: x - int32(run), Y: ring.Y + ring.H - th, W: int32(run) + 1, H: th}, run
	default: // left edge, bottom→top
		d := start - bottom
		run := math.Min(length, 2*(w+h)-start)
		y := ring.Y + ring.H - int32(d)
		return sdl.Rect{X: ring.X, Y: y - int32(run), W: th, H: int32(run) + 1}, run
	}
}

// drawEggNyah draws SyntaxNyah's (AsyncAO) soft pink glow with a heartbeat rhythm.
func (a *App) drawEggNyah(box, win sdl.Rect, t float64) {
	c := a.ctx
	phase := math.Mod(t/eggNyahHeartSecs, 1)
	bump := math.Max(heartBeat(phase, eggNyahBeat1), heartBeat(phase, eggNyahBeat2))
	alpha := eggNyahAlphaFloor + int32(float64(eggNyahAlphaPeak-eggNyahAlphaFloor)*bump)
	for i := int32(0); i < eggRingCount; i++ {
		ring := outsetRing(box, (i+1)*eggRingGap)
		if !ringVisible(ring, win) {
			continue
		}
		ra := alpha * int32(eggRingAlpha(i)) / 255
		col := MayoPink
		col.A = uint8(ra)
		c.Border(ring, col)
	}
}

// heartBeat returns a 0..1 cosine bump for a beat centred at center (cycle fraction), eggNyahBeatWidth wide.
func heartBeat(phase, center float64) float64 {
	d := math.Abs(phase - center)
	if d > 1-d { // wrap distance across the cycle seam
		d = 1 - d
	}
	if d >= eggNyahBeatWidth {
		return 0
	}
	return 0.5 * (1 + math.Cos(math.Pi*d/eggNyahBeatWidth))
}

// drawEggScatter draws Scatterflower's chatbox half: rings drifting through the palette, calm beside the stage petals.
func (a *App) drawEggScatter(box, win sdl.Rect, t float64) {
	c := a.ctx
	for i := int32(0); i < eggRingCount; i++ {
		ring := outsetRing(box, (i+1)*eggRingGap)
		if !ringVisible(ring, win) {
			continue
		}
		// Each ring sits further along the cycle, so the band reads as one colour drifting outward.
		col := eggPaletteAt(math.Mod(t/eggScatterBloomSecs+float64(i)*eggScatterRingStep, 1))
		col.A = eggRingAlpha(i)
		c.Border(ring, col)
	}
}

// eggPaletteAt samples eggScatterPalette at u in [0,1), lerping and wrapping so the cycle is continuous.
func eggPaletteAt(u float64) sdl.Color {
	n := len(eggScatterPalette)
	pos := u * float64(n)
	i := int(pos) % n
	return lerpColor(eggScatterPalette[i], eggScatterPalette[(i+1)%n], pos-math.Floor(pos))
}

// eggHash mixes a petal index: the field is a pure function of (index, clock), so no state, and clients match.
// math/rand would need a seeded source, which diverges per client. Mixer is MurmurHash3's finalizer (public domain).
func eggHash(i uint32) uint32 {
	i ^= i >> 16
	i *= 0x7feb352d
	i ^= i >> 15
	i *= 0x846ca68b
	i ^= i >> 16
	return i
}

// eggUnit maps a hashed value onto [0,1); the divisor is 2^32.
func eggUnit(h uint32) float64 { return float64(h) * (1.0 / 4294967296.0) }

// drawEggPetals paints the petal fall clipped to vp; rects go through the Ctx scratch rect, as a &local would escape.
func (a *App) drawEggPetals(vp sdl.Rect, t float64) {
	c := a.ctx
	if vp.W <= 0 || vp.H <= 0 {
		return
	}
	prev, had := c.pushClip(vp)
	for i := uint32(0); i < eggScatterPetals; i++ {
		// Separate salts rather than bit-fields of one hash, so no two properties correlate.
		lane := eggUnit(eggHash(i*4 + 0))
		speed := eggUnit(eggHash(i*4 + 1))
		size := eggUnit(eggHash(i*4 + 2))
		phase := eggUnit(eggHash(i*4 + 3))

		fall := eggScatterFallSecs * (1 - eggScatterFallVary/2 + speed*eggScatterFallVary)
		p := math.Mod(t/fall+phase, 1)

		sway := math.Sin((p*eggScatterSwayCycles + phase) * 2 * math.Pi)
		x := vp.X + int32(lane*float64(vp.W)) + int32(sway*eggScatterSwayFrac*float64(vp.W))
		y := vp.Y + int32(p*float64(vp.H))

		h := eggScatterSizeMin + int32(size*float64(eggScatterSizeMax-eggScatterSizeMin))
		turn := math.Abs(math.Cos((t/eggScatterSpinSecs + phase) * 2 * math.Pi))
		w := 1 + int32(float64(h)*turn)

		col := eggScatterPalette[i%uint32(len(eggScatterPalette))]
		col.A = uint8(float64(eggScatterAlphaPeak) * eggPetalFade(p))
		c.Fill(sdl.Rect{X: x, Y: y, W: w, H: h}, col)
	}
	c.popClip(prev, had)
}

// Crystalwarrior: procedural shards over the stage and a ring on the chatbox, a pure function of (index, clock).
// A shard is a stack of axis-aligned fills tapering at both ends, the only lozenge SDL_RenderFillRect can draw.

const (
	// eggCrystalShards caps the field (hard rule 4); fewer than the petals because a shard is a stack of fills.
	eggCrystalShards = 12
	// eggCrystalSegments is how many stacked rects build one shard.
	eggCrystalSegments = 6
	// eggCrystalRiseSecs is the base time to rise through the stage; eggCrystalRiseVary scales it per shard.
	eggCrystalRiseSecs = 9.0
	eggCrystalRiseVary = 0.5
	// Shard height bounds as fractions of stage height, so the field scales with the stage.
	eggCrystalHeightMinFrac = 0.10
	eggCrystalHeightMaxFrac = 0.26
	// eggCrystalWidthFrac is a shard's widest point as a fraction of its height.
	eggCrystalWidthFrac = 0.22
	// eggCrystalHueDriftSecs is one walk of the hue wheel; eggCrystalHueSpread is the hue gap between shards.
	eggCrystalHueDriftSecs = 7.0
	eggCrystalHueSpread    = 0.35
	// eggCrystalGlintSecs is the flash period; eggCrystalGlintFrac is the part of it spent bright.
	eggCrystalGlintSecs = 2.3
	eggCrystalGlintFrac = 0.18
	// Resting and glint opacity, both translucent so the speaking sprite stays readable through the crystal.
	eggCrystalAlphaBase  = 70
	eggCrystalAlphaGlint = 165
	// The refraction beam: sweep time, width as a fraction of stage width, and opacity.
	eggCrystalBeamSecs  = 5.0
	eggCrystalBeamFrac  = 0.035
	eggCrystalBeamAlpha = 46
	// eggCrystalRingGlintSecs differs from eggCrystalGlintSecs so the box and stage do not flash in lockstep.
	eggCrystalRingGlintSecs = 3.1
	// eggCrystalRingHueStep offsets each ring along the hue wheel, wider than the rainbow egg's spread.
	eggCrystalRingHueStep = 0.16
)

// eggCrystalSaturation keeps the facets pale; full value so they read bright over a dark stage.
const eggCrystalSaturation = 0.45

// drawEggShards paints the Crystalwarrior shards rising through the hue wheel under one beam, clipped to vp.
func (a *App) drawEggShards(vp sdl.Rect, t float64) {
	c := a.ctx
	if vp.W <= 0 || vp.H <= 0 {
		return
	}
	prev, had := c.pushClip(vp)
	hueBase := math.Mod(t/eggCrystalHueDriftSecs, 1)
	for i := uint32(0); i < eggCrystalShards; i++ {
		// Separate salts, so no two properties correlate.
		lane := eggUnit(eggHash(i*4 + 0))
		speed := eggUnit(eggHash(i*4 + 1))
		size := eggUnit(eggHash(i*4 + 2))
		phase := eggUnit(eggHash(i*4 + 3))

		// p walks from 1 (stage bottom) to 0 (stage top): shards rise.
		rise := eggCrystalRiseSecs * (1 - eggCrystalRiseVary/2 + speed*eggCrystalRiseVary)
		p := 1 - math.Mod(t/rise+phase, 1)

		hFrac := eggCrystalHeightMinFrac + size*(eggCrystalHeightMaxFrac-eggCrystalHeightMinFrac)
		h := int32(hFrac * float64(vp.H))
		if h < eggCrystalSegments {
			h = eggCrystalSegments // one row per segment minimum, or the taper collapses
		}
		w := int32(float64(h) * eggCrystalWidthFrac)
		if w < 1 {
			w = 1
		}
		cx := vp.X + int32(lane*float64(vp.W))
		top := vp.Y + int32(p*float64(vp.H)) - h/2

		hue := math.Mod(hueBase+phase*eggCrystalHueSpread, 1)
		cr, cg, cb := hsvToRGB(hue, eggCrystalSaturation, 1)
		alpha := eggCrystalAlphaBase
		if g := eggShardGlint(t, phase); g > 0 {
			alpha += int(float64(eggCrystalAlphaGlint-eggCrystalAlphaBase) * g)
		}
		// Fade near either stage edge, so a facet does not pop in on the clip line.
		alpha = int(float64(alpha) * eggPetalFade(p))
		if alpha <= 0 {
			continue
		}
		col := sdl.Color{R: cr, G: cg, B: cb, A: uint8(alpha)}
		segH := h / eggCrystalSegments
		if segH < 1 {
			segH = 1
		}
		for s := int32(0); s < eggCrystalSegments; s++ {
			sw := int32(float64(w) * eggShardTaper(s))
			if sw < 1 {
				sw = 1
			}
			c.Fill(sdl.Rect{X: cx - sw/2, Y: top + s*segH, W: sw, H: segH}, col)
		}
	}
	// Last, so it lies over the facets; colourless on purpose.
	bw := int32(eggCrystalBeamFrac * float64(vp.W))
	if bw < 1 {
		bw = 1
	}
	bx := vp.X + int32(math.Mod(t/eggCrystalBeamSecs, 1)*float64(vp.W))
	c.Fill(sdl.Rect{X: bx, Y: vp.Y, W: bw, H: vp.H},
		sdl.Color{R: 0xff, G: 0xff, B: 0xff, A: eggCrystalBeamAlpha})
	c.popClip(prev, had)
}

// eggShardTaper is segment s's width as a fraction of the widest point: 0 at both ends, 1 in the middle.
func eggShardTaper(s int32) float64 {
	mid := float64(eggCrystalSegments-1) / 2
	d := math.Abs(float64(s)-mid) / mid // 0 at the centre row, 1 at either end row
	return 1 - d*d                      // squared, so the taper is convex (a facet, not a triangle)
}

// eggShardGlint returns a 0..1 flash over the leading eggCrystalGlintFrac of each period, offset by phase.
func eggShardGlint(t, phase float64) float64 {
	u := math.Mod(t/eggCrystalGlintSecs+phase, 1)
	if u >= eggCrystalGlintFrac {
		return 0
	}
	return 0.5 * (1 - math.Cos(2*math.Pi*u/eggCrystalGlintFrac))
}

// drawEggCrystalRing draws the Crystalwarrior chatbox half: rings at different hues, flashing on their own clock.
func (a *App) drawEggCrystalRing(box, win sdl.Rect, t float64) {
	c := a.ctx
	base := math.Mod(t/eggCrystalHueDriftSecs, 1)
	glint := eggShardGlint(t*eggCrystalGlintSecs/eggCrystalRingGlintSecs, 0)
	for i := int32(0); i < eggRingCount; i++ {
		ring := outsetRing(box, (i+1)*eggRingGap)
		if !ringVisible(ring, win) {
			continue
		}
		h := math.Mod(base+float64(i)*eggCrystalRingHueStep, 1)
		// The glint desaturates toward white: the colour washes out at the crest instead of just brightening.
		r, g, b := hsvToRGB(h, eggCrystalSaturation*(1-glint), 1)
		c.Border(ring, sdl.Color{R: r, G: g, B: b, A: eggRingAlpha(i)})
	}
}

// eggPetalFade returns a 0..1 opacity at fall position p, ramping over eggScatterFadeFrac at each end.
func eggPetalFade(p float64) float64 {
	switch {
	case p < eggScatterFadeFrac:
		return p / eggScatterFadeFrac
	case p > 1-eggScatterFadeFrac:
		return (1 - p) / eggScatterFadeFrac
	default:
		return 1
	}
}

// lerpColor interpolates two colours by t in [0,1], leaving alpha at a.A; no closure, to stay allocation-free.
func lerpColor(a, b sdl.Color, t float64) sdl.Color {
	return sdl.Color{
		R: lerpByte(a.R, b.R, t),
		G: lerpByte(a.G, b.G, t),
		B: lerpByte(a.B, b.B, t),
		A: a.A,
	}
}

func lerpByte(x, y uint8, t float64) uint8 {
	return uint8(float64(x) + (float64(y)-float64(x))*t)
}

// ringVisible reports whether r overlaps the window at all. It must not clamp: DrawRect outlines all four sides,
// so a clamped ring paints a false line along the screen edge. SDL clips the off-screen part.
func ringVisible(r, win sdl.Rect) bool {
	return r.X < win.X+win.W && r.X+r.W > win.X &&
		r.Y < win.Y+win.H && r.Y+r.H > win.Y
}

// clampRingToWindow intersects r with the window in place, for filled segments, where clamping leaves no false edge.
func clampRingToWindow(r *sdl.Rect, win sdl.Rect) bool {
	x0, y0 := max(r.X, win.X), max(r.Y, win.Y)
	x1, y1 := min(r.X+r.W, win.X+win.W), min(r.Y+r.H, win.Y+win.H)
	if x1 <= x0 || y1 <= y0 {
		return false
	}
	r.X, r.Y, r.W, r.H = x0, y0, x1-x0, y1-y0
	return true
}
