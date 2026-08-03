package ui

// Creator easter eggs in the IC chatbox.
//
// When the DISPLAYED IC message's text mentions one of AsyncAO's creators, the
// chatbox grows an animated glow ring keyed to that person. Detection runs on
// the RECEIVED / displayed text (a plain case-insensitive substring check), so
// every AsyncAO client in the room renders the same effect with ZERO wire
// changes — nobody has to "send" an egg, mentioning the name is enough.
//
// The scan is once per message (creatorEgg, a pure function, table-tested) and
// cached on the App via refreshEggKind's compare-and-store, so a settled frame
// costs one string compare — not a rescan and not an allocation. The per-frame
// DRAW (drawCreatorEgg) is math + SetDrawColor + scratch-rect fills only, so it
// stays inside the whole-screen 0-alloc gate (TestDrawCourtroomZeroAlloc).

import (
	"math"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Egg kinds. The first three name the client lineage's creators, in the order it
// was built (Attorney Online → AO2 → AsyncAO), which is also the match-priority
// order in creatorEgg. eggScatter honours an INSPIRATION rather than a link in
// that lineage, so it sorts last: a message naming both a lineage creator and an
// inspiration lights the creator.
const (
	eggNone    uint8 = iota // no creator mentioned — the common case, no glow draws
	eggFanat                // FanatSors — creator of Attorney Online (rainbow ring)
	eggOmni                 // OmniTroid — creator of AO2 (blue<->gold pulse + scanner sweep)
	eggNyah                 // SyntaxNyah — creator of AsyncAO (Mayo-pink heartbeat glow)
	eggScatter              // Scatterflower — petals over the STAGE + a petal-toned ring
)

// Trigger substrings, matched case-insensitively against the displayed message
// text. These are the FULL handles, deliberately: matching the short suffix
// ("fanat", "omni") would light up on innocent words like "fanatic" / "android"
// — the whole point of the negative test rows. The same reasoning keeps
// eggNameScatter whole: "flower" alone is an ordinary English word that would
// fire on any garden-variety line of roleplay.
const (
	eggNameFanat   = "fanatsors"
	eggNameOmni    = "omnitroid"
	eggNameNyah    = "syntaxnyah"
	eggNameScatter = "scatterflower"
)

// creatorEgg scans a displayed IC message for a creator mention and returns the
// egg kind (eggNone when none is present). Case-insensitive substring match;
// priority is fanat > omni > nyah (creation lineage AO → AO2 → AsyncAO) then
// scatter, so a message naming two people lights the earliest in that lineage and
// an inspiration never outranks a creator. Callers cache the result — this
// allocates one ToLower string, which is fine ONCE per message but must never run
// per frame (see refreshEggKind's compare guard).
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
	case strings.Contains(low, eggNameNyah):
		return eggNyah
	case strings.Contains(low, eggNameScatter):
		return eggScatter
	default:
		return eggNone
	}
}

// eggSceneText returns the text the egg scan should see for sc: the DISPLAYED
// message, or "" when the chatbox is hidden. It mirrors drawChatOverlay's own
// early-return gate (screens.go — blankpost, or no message and no showname) and
// the two MUST agree: the stage half of the Scatterflower egg is refreshed from
// here while the ring half is refreshed from drawChatEgg, so a disagreement would
// leave petals falling over a stage whose chatbox never drew.
func eggSceneText(sc *courtroom.Scene) string {
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return ""
	}
	return sc.MessageText
}

// refreshEggKind is the compare-and-store cache guard: if text differs from the
// last-scanned message it rescans (creatorEgg) and stores both, returning true;
// otherwise it does nothing and returns false. drawChatOverlay calls it once per
// frame with the displayed text, so a settled frame (same text) is one string
// compare with no allocation and no rescan. Returning whether it rescanned makes
// the "same text twice ⇒ one scan" behaviour testable without SDL (the
// mixChannels / applyResumeDuck pure-split precedent).
func (a *App) refreshEggKind(text string) (scanned bool) {
	if text == a.eggMsg {
		return false
	}
	a.eggMsg = text
	a.eggKind = creatorEgg(text)
	return true
}

// drawChatEgg is the shared egg entry point for BOTH chatbox draw paths — the
// classic overlay (drawChatOverlay) and the themed design-rect box
// (drawThemedChatBox). Call it AFTER the skin/panel/border have drawn: the glow
// rings sit AROUND the box on top of any skin (theme or char.ini) and, being
// OUTSET, never cover the art. Detection is once per message: refreshEggKind
// rescans only when the displayed text changed, so a settled frame is one string
// compare. Gated on the SAME accessibility convention as the AO2 screen effects
// (ScreenEffectsOn && !ReduceMotion). While an egg draws, NoteAnimating keeps the
// frame limiter feeding frames at idle (the msAnim precedent); it self-clears the
// moment the message leaves the box, because the detection cache resolves the new
// (non-triggering) text to eggNone. Callers early-return on blankpost / empty
// before reaching here, so an egg never draws without a chatbox. Sharing this one
// helper is why a THEMED layout lights the exact same egg the classic path does
// — otherwise the author's own SyntaxNyah egg would be invisible on full AO2
// themes (which define ao2_chatbox and take the themed path). box + text are
// value params, no closure, so a settled frame stays inside the 0-alloc gate.
func (a *App) drawChatEgg(box sdl.Rect, text string) {
	a.refreshEggKind(text)
	if a.eggKind != eggNone && a.d.Prefs.ScreenEffectsOn() && !a.d.Prefs.ReduceMotion() {
		a.drawCreatorEgg(box, a.eggKind)
		a.NoteAnimating()
	}
}

// --- egg glow tuning (every number named + WHY) --------------------------------

const (
	// eggRingCount rings are drawn OUTSET around the chatbox, each 1px thick and
	// spaced eggRingGap px apart. 3 gives a readable "glow" band without eating
	// much screen; ring 0 hugs the box, rings 1..2 fan outward, each dimmer.
	eggRingCount = 3
	// eggRingGap is the pixel step between successive outset rings (and the
	// thickness the innermost ring sits proud of the box). Small enough that the
	// rings read as one soft band, large enough that they don't alias into one line.
	eggRingGap = int32(3)
	// eggRingAlphaOuter is the alpha of the OUTERMOST ring; the innermost is full
	// (255). Rings fade linearly outward so the band reads as a glow falloff, not
	// a stack of hard outlines.
	eggRingAlphaOuter = 60
)

const (
	// eggHueCycleSecs is how long the rainbow ring takes to sweep the full hue
	// wheel. ~4s is lively but not seizure-fast (respects the same motion budget
	// the \s/\f screen effects live under; the whole egg is gated off ReduceMotion).
	eggHueCycleSecs = 4.0
	// eggHueRingSpread offsets each successive ring's hue by this fraction of the
	// wheel, so the rainbow reads as a moving gradient across the band rather than
	// three identically-coloured outlines.
	eggHueRingSpread = 0.06
)

const (
	// eggOmniPulseSecs is the deep-blue <-> gold breathing period for the AO2 egg.
	// ~2s (0.5 Hz) is a calm, courtly pulse.
	eggOmniPulseSecs = 2.0
	// eggOmniSweepSecs is the period of the bright scanner segment's trip around
	// the box perimeter. Faster than the pulse so the sweep clearly reads as a
	// separate travelling highlight over the slow glow.
	eggOmniSweepSecs = 1.6
	// eggOmniSweepFrac is the segment length as a fraction of the total perimeter
	// — a SHORT bright bar (a scanner sweep), not a long chase.
	eggOmniSweepFrac = 0.08
	// eggOmniSweepThick is the scanner segment's thickness in px — a thin bright
	// bar riding the outer ring edge.
	eggOmniSweepThick = int32(3)
)

// Deep-blue and gold are the AO2 courtroom colours the OmniTroid egg pulses
// between; the scanner segment paints in eggGold at full brightness.
var (
	eggBlue = sdl.Color{R: 24, G: 52, B: 150, A: 255}  // deep courtroom blue
	eggGold = sdl.Color{R: 235, G: 190, B: 70, A: 255} // AO2 gold accent
)

const (
	// eggNyahHeartSecs is the SyntaxNyah heartbeat period: two quick pulses then a
	// rest, ~1.4s a cycle (a resting human heart rhythm — "lub-dub ... rest").
	eggNyahHeartSecs = 1.4
	// The two beats fire early in the cycle; the remainder is the rest. These
	// phase fractions place beat 1 and beat 2 close together (the "lub-dub"),
	// leaving the back ~60% of the cycle quiet.
	eggNyahBeat1 = 0.00
	eggNyahBeat2 = 0.22
	// eggNyahBeatWidth is how wide (in cycle fraction) each beat's brightness bump
	// is — narrow, so the pulses feel like quick heartbeats, not slow swells.
	eggNyahBeatWidth = 0.12
	// eggNyahAlphaFloor keeps the pink glow faintly visible between beats (a soft
	// resting breath) rather than vanishing to nothing; eggNyahAlphaPeak is the
	// brightness at a beat's crest.
	eggNyahAlphaFloor = 40
	eggNyahAlphaPeak  = 200
)

// MayoPink is the SyntaxNyah egg's colour — the Mayo mascot's soft pink
// (#ff9ecb-ish). Named so the author's own egg wears the project mascot's hue.
var MayoPink = sdl.Color{R: 0xff, G: 0x9e, B: 0xcb, A: 255}

const (
	// eggScatterPetals is how many petals drift over the stage at once. A named
	// cap (hard rule 4): the draw is exactly this many fills regardless of stage
	// size, so the egg's per-frame cost is bounded at 4K as it is at 720p. Two
	// dozen reads as a gentle scatter — a blizzard would bury the sprite the
	// message is about.
	eggScatterPetals = 26
	// eggScatterFallSecs is the base time a petal takes to cross the stage, and
	// eggScatterFallVary the fraction each petal scales that by. Without the
	// variance every petal lands in lockstep, which reads as a machine rather
	// than as falling petals.
	eggScatterFallSecs = 6.0
	eggScatterFallVary = 0.6
	// eggScatterSwayCycles is how many left-right sways a petal completes over one
	// fall; eggScatterSwayFrac is how far it wanders sideways, as a fraction of
	// stage width. Together they turn a straight drop into a lazy zig-zag.
	eggScatterSwayCycles = 2.5
	eggScatterSwayFrac   = 0.06
	// eggScatterSizeMin/Max bound a petal's long edge in px. Kept small: these are
	// petals behind the action, not confetti in front of it.
	eggScatterSizeMin = int32(2)
	eggScatterSizeMax = int32(6)
	// eggScatterSpinSecs is the flutter period. SDL_RenderFillRect cannot rotate,
	// so a petal "turns" by having its WIDTH squashed toward edge-on by |cos| over
	// this period — the cheap fake that sells the tumble without a texture.
	eggScatterSpinSecs = 1.1
	// eggScatterFadeFrac is the fraction of the fall spent fading in at the top and
	// out at the bottom, so petals never pop into or out of existence at the clip
	// edge.
	eggScatterFadeFrac = 0.15
	// eggScatterAlphaPeak is a petal's alpha at full opacity — translucent on
	// purpose, so the stage art underneath still reads.
	eggScatterAlphaPeak = 210
	// eggScatterBloomSecs is how long the chatbox ring takes to drift once through
	// the petal palette, and eggScatterRingStep how far along that cycle each
	// successive ring sits. Slow and calm: the STAGE petals carry this egg's
	// motion, so the ring must not compete with them for attention.
	eggScatterBloomSecs = 5.0
	eggScatterRingStep  = 0.08
)

// eggScatterPalette is the Scatterflower petal spread — blossom pinks with one
// warm cream mote so the fall isn't monochrome. Package-level so the per-frame
// draw indexes a fixed array instead of building one.
var eggScatterPalette = [4]sdl.Color{
	{R: 0xff, G: 0xc2, B: 0xd6, A: 255}, // blossom pink
	{R: 0xff, G: 0xe4, B: 0xef, A: 255}, // near-white petal
	{R: 0xf2, G: 0x9c, B: 0xc4, A: 255}, // deeper rose
	{R: 0xff, G: 0xd9, B: 0xa8, A: 255}, // warm cream — a stray pollen mote
}

// drawCreatorEgg paints the animated glow ring for the active egg AROUND box,
// after the skin/panel/border have drawn. Rings are OUTSET (they never cover the
// chatbox art). Everything is clamped to the window rect (0,0,winW,winH) so an
// edge-docked box never draws off-screen garbage. Called only when the egg is
// active AND ScreenEffectsOn && !ReduceMotion (the accessibility gate lives at
// the call site); the caller also marks NoteAnimating so the frame limiter keeps
// feeding frames while the egg animates. ZERO allocations: math + SetDrawColor +
// c.Fill / c.Border, which reuse the Ctx scratch rect (never &local into cgo).
func (a *App) drawCreatorEgg(box sdl.Rect, kind uint8) {
	t := a.d.Viewport.AnimClock().Seconds()
	win := sdl.Rect{X: 0, Y: 0, W: a.winW, H: a.winH}
	switch kind {
	case eggFanat:
		a.drawEggRainbow(box, win, t)
	case eggOmni:
		a.drawEggOmni(box, win, t)
	case eggNyah:
		a.drawEggNyah(box, win, t)
	case eggScatter:
		a.drawEggScatter(box, win, t)
	}
}

// drawStageEgg paints the VIEWPORT half of the Scatterflower egg: petals drifting
// down over the stage. It is the only egg that draws outside the chatbox, so it
// gets its own entry point, called from renderViewportZoomed — the same single
// site drawStageFrame uses, which is why all three stages (classic, themed,
// theater) get it without three copies of the call. Drawn BEFORE the frame (that's
// chrome and belongs on top) and before drawChatOverlay runs later in the frame,
// so petals fall BEHIND the message box instead of across the words.
//
// It refreshes the detection cache itself instead of leaning on drawChatEgg,
// because drawChatOverlay early-returns on a blankpost and would leave the last
// kind latched — petals would keep falling after the message was gone. Passing
// eggSceneText (which mirrors that same gate) keeps the two halves in agreement,
// and the refresh is idempotent for the chatbox path that runs later in the frame:
// same text ⇒ one string compare, no rescan, no allocation. Gated on the SAME
// accessibility pair as the ring eggs and the AO2 screen effects.
func (a *App) drawStageEgg(vp sdl.Rect, sc *courtroom.Scene) {
	a.refreshEggKind(eggSceneText(sc))
	if a.eggKind != eggScatter || !a.d.Prefs.ScreenEffectsOn() || a.d.Prefs.ReduceMotion() {
		return
	}
	a.drawEggPetals(vp, a.d.Viewport.AnimClock().Seconds())
	a.NoteAnimating()
}

// outsetRing returns box grown by step px on every side (an outset ring rect).
func outsetRing(box sdl.Rect, step int32) sdl.Rect {
	return sdl.Rect{X: box.X - step, Y: box.Y - step, W: box.W + step*2, H: box.H + step*2}
}

// eggRingAlpha fades ring i (0 = innermost) linearly from full (255) to
// eggRingAlphaOuter across the eggRingCount rings, so the band reads as a glow
// falloff rather than a stack of equal outlines. eggRingCount is a fixed 3, so
// the divisor (eggRingCount-1) is never zero.
func eggRingAlpha(i int32) uint8 {
	return uint8(255 - (255-eggRingAlphaOuter)*i/(eggRingCount-1))
}

// drawEggRainbow — FanatSors (Attorney Online). Nested outset rings whose hue
// cycles smoothly over time, outer rings dimmer for a glow read.
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

// drawEggOmni — OmniTroid (AO2). A deep-blue<->gold pulsing glow border PLUS a
// single bright gold segment that travels the box perimeter like a scanner sweep.
func (a *App) drawEggOmni(box, win sdl.Rect, t float64) {
	c := a.ctx
	// Blue<->gold breathing: a 0..1 triangle drives the lerp so it eases at both ends.
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
	// Scanner sweep: a short bright bar riding the OUTERMOST ring, its head walking
	// the perimeter from the anim clock. Drawn last so it sits over the glow.
	a.drawEggSweep(outsetRing(box, eggRingCount*eggRingGap), win, t)
}

// drawEggSweep paints the OmniTroid scanner segment: a short bright bar whose
// position is computed from the anim clock along the ring's perimeter and drawn
// as thin filled rects on the edge it currently rides. Purely math + fills.
func (a *App) drawEggSweep(ring, win sdl.Rect, t float64) {
	c := a.ctx
	if ring.W <= 0 || ring.H <= 0 {
		return
	}
	perim := 2 * float64(ring.W+ring.H) // total travel distance around the edge
	start := math.Mod(t/eggOmniSweepSecs, 1) * perim
	length := eggOmniSweepFrac * perim
	th := eggOmniSweepThick
	// Walk the segment [start, start+length) around the perimeter, emitting one
	// clamped fill per edge it touches. perimSegment maps a 1-D perimeter span to
	// an edge-aligned rect; wrapping is handled by splitting at the perimeter seam.
	// A plain loop (no closure) keeps the draw allocation-free — a capturing
	// closure could tempt escape analysis onto the heap.
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

// perimSegment maps a run starting at 1-D perimeter offset `start` (clockwise
// from the ring's top-left, going right along the top) to an axis-aligned rect
// of thickness `th` on the edge that `start` sits on, consuming at most the rest
// of that edge. Returns the rect and how much perimeter length it consumed, so
// the caller can advance and continue onto the next edge.
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

// drawEggNyah — SyntaxNyah (AsyncAO). A soft Mayo-pink breathing glow with a
// HEARTBEAT rhythm: two quick pulses then a rest, ~1.4s a cycle. Subtle + classy
// — the author's own egg.
func (a *App) drawEggNyah(box, win sdl.Rect, t float64) {
	c := a.ctx
	phase := math.Mod(t/eggNyahHeartSecs, 1)
	// Two narrow brightness bumps (the "lub-dub") over an otherwise quiet cycle.
	bump := math.Max(heartBeat(phase, eggNyahBeat1), heartBeat(phase, eggNyahBeat2))
	alpha := eggNyahAlphaFloor + int32(float64(eggNyahAlphaPeak-eggNyahAlphaFloor)*bump)
	for i := int32(0); i < eggRingCount; i++ {
		ring := outsetRing(box, (i+1)*eggRingGap)
		if !ringVisible(ring, win) {
			continue
		}
		// Outer rings dimmer (glow falloff) AND modulated by the heartbeat.
		ra := alpha * int32(eggRingAlpha(i)) / 255
		col := MayoPink
		col.A = uint8(ra)
		c.Border(ring, col)
	}
}

// heartBeat returns a 0..1 brightness bump for a beat centred at `center` (in
// cycle fraction), eggNyahBeatWidth wide, shaped as a smooth cosine hump so the
// pulse rises and falls softly rather than snapping.
func heartBeat(phase, center float64) float64 {
	d := math.Abs(phase - center)
	if d > 1-d { // wrap distance across the cycle seam
		d = 1 - d
	}
	if d >= eggNyahBeatWidth {
		return 0
	}
	// Cosine hump: 1 at the centre, 0 at the edges.
	return 0.5 * (1 + math.Cos(math.Pi*d/eggNyahBeatWidth))
}

// drawEggScatter — Scatterflower, the chatbox half. Petal-toned rings that DRIFT
// through the palette on a slow bloom cycle rather than pulsing or sweeping: this
// egg's motion budget is spent on the stage petals (drawEggPetals), so the ring
// stays calm and just tints the box to match what's falling behind it.
func (a *App) drawEggScatter(box, win sdl.Rect, t float64) {
	c := a.ctx
	for i := int32(0); i < eggRingCount; i++ {
		ring := outsetRing(box, (i+1)*eggRingGap)
		if !ringVisible(ring, win) {
			continue
		}
		// Each ring sits a step further along the same cycle, so the band reads as
		// one colour drifting outward instead of three independent outlines.
		col := eggPaletteAt(math.Mod(t/eggScatterBloomSecs+float64(i)*eggScatterRingStep, 1))
		col.A = eggRingAlpha(i)
		c.Border(ring, col)
	}
}

// eggPaletteAt samples eggScatterPalette at u in [0,1), lerping between adjacent
// entries (and wrapping the last back to the first) so the cycle is continuous
// rather than stepping between four flat colours.
func eggPaletteAt(u float64) sdl.Color {
	n := len(eggScatterPalette)
	pos := u * float64(n)
	i := int(pos) % n
	return lerpColor(eggScatterPalette[i], eggScatterPalette[(i+1)%n], pos-math.Floor(pos))
}

// eggHash mixes a petal index into a well-spread uint32. The whole petal field is
// a pure function of (index, clock): every petal derives its lane, fall speed,
// size and phase from this hash, so there is no per-petal state to allocate, seed
// or reset when the message changes — and every AsyncAO client in the room draws
// the identical fall. math/rand is deliberately NOT used: it would need a seeded
// source (per-client divergence) and a call into a locked global.
// Mixer is MurmurHash3's finalizer (public domain).
func eggHash(i uint32) uint32 {
	i ^= i >> 16
	i *= 0x7feb352d
	i ^= i >> 15
	i *= 0x846ca68b
	i ^= i >> 16
	return i
}

// eggUnit maps a hashed value onto [0,1). The divisor is 2^32 as a float
// constant, so this is one multiply — no conversion through a big integer.
func eggUnit(h uint32) float64 { return float64(h) * (1.0 / 4294967296.0) }

// drawEggPetals paints the Scatterflower petal fall across vp. Clipped to the
// stage so a petal's sway can never paint over the surrounding chrome, and
// ZERO-allocation: fixed petal count, no slices, no closures, colours copied from
// a package-level array, every rect going through the Ctx scratch rect (a &local
// would escape through cgo and heap-allocate per fill).
func (a *App) drawEggPetals(vp sdl.Rect, t float64) {
	c := a.ctx
	if vp.W <= 0 || vp.H <= 0 {
		return
	}
	prev, had := c.pushClip(vp)
	for i := uint32(0); i < eggScatterPetals; i++ {
		// Four independent draws from one index — separate salts rather than slicing
		// bit-fields out of a single hash, so no two properties ever correlate.
		lane := eggUnit(eggHash(i*4 + 0))
		speed := eggUnit(eggHash(i*4 + 1))
		size := eggUnit(eggHash(i*4 + 2))
		phase := eggUnit(eggHash(i*4 + 3))

		// p walks 0 (stage top) → 1 (stage bottom) over this petal's own fall time.
		fall := eggScatterFallSecs * (1 - eggScatterFallVary/2 + speed*eggScatterFallVary)
		p := math.Mod(t/fall+phase, 1)

		sway := math.Sin((p*eggScatterSwayCycles + phase) * 2 * math.Pi)
		x := vp.X + int32(lane*float64(vp.W)) + int32(sway*eggScatterSwayFrac*float64(vp.W))
		y := vp.Y + int32(p*float64(vp.H))

		// Flutter: the petal turns edge-on and back, so only its width moves.
		h := eggScatterSizeMin + int32(size*float64(eggScatterSizeMax-eggScatterSizeMin))
		turn := math.Abs(math.Cos((t/eggScatterSpinSecs + phase) * 2 * math.Pi))
		w := 1 + int32(float64(h)*turn)

		col := eggScatterPalette[i%uint32(len(eggScatterPalette))]
		col.A = uint8(float64(eggScatterAlphaPeak) * eggPetalFade(p))
		c.Fill(sdl.Rect{X: x, Y: y, W: w, H: h}, col)
	}
	c.popClip(prev, had)
}

// eggPetalFade returns a 0..1 opacity for a petal at fall position p, ramping in
// over the first eggScatterFadeFrac of the drop and out over the last, so petals
// never pop in at the stage's top edge or vanish at its bottom one.
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

// lerpColor linearly interpolates two colours by t in [0,1] (alpha left at a.A;
// callers set it per ring). No closure so escape analysis can't drift it onto
// the heap — the per-frame egg draw must stay allocation-free.
func lerpColor(a, b sdl.Color, t float64) sdl.Color {
	return sdl.Color{
		R: lerpByte(a.R, b.R, t),
		G: lerpByte(a.G, b.G, t),
		B: lerpByte(a.B, b.B, t),
		A: a.A,
	}
}

// lerpByte interpolates one channel; split out so lerpColor needs no closure.
func lerpByte(x, y uint8, t float64) uint8 {
	return uint8(float64(x) + (float64(y)-float64(x))*t)
}

// ringVisible reports whether r overlaps the window rect AT ALL — a skip-only
// test for the OUTLINE rings. It must NOT clamp: DrawRect outlines all four
// sides of the rect it's given, so clamping a ring that runs off an edge to the
// window border would paint a spurious colored line hugging the screen edge
// (worst on a theater-mode box that's flush left/right). Instead we draw the
// full ring and let SDL clip the off-screen part to the render target — a
// partial, open ring is the correct edge-docked look ("SDL clips anyway"). Pure
// integer math — no cgo query (GetClipRect's named return escapes + heap-allocs).
func ringVisible(r, win sdl.Rect) bool {
	return r.X < win.X+win.W && r.X+r.W > win.X &&
		r.Y < win.Y+win.H && r.Y+r.H > win.Y
}

// clampRingToWindow intersects r with the window rect in place and reports
// whether anything remains visible. Used ONLY for the sweep's FILLED segments,
// where a clamped fill has no spurious edge (unlike an outline). Keeps the
// per-edge fills honest at a flush-docked box. Pure integer math — no cgo query.
func clampRingToWindow(r *sdl.Rect, win sdl.Rect) bool {
	x0, y0 := max32(r.X, win.X), max32(r.Y, win.Y)
	x1, y1 := min32(r.X+r.W, win.X+win.W), min32(r.Y+r.H, win.Y+win.H)
	if x1 <= x0 || y1 <= y0 {
		return false
	}
	r.X, r.Y, r.W, r.H = x0, y0, x1-x0, y1-y0
	return true
}
