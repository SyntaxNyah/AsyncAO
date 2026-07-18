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
)

// Egg kinds. Each names its honoree — the three people the eggs celebrate, in
// the order the client lineage was built (Attorney Online → AO2 → AsyncAO),
// which is also the match-priority order in creatorEgg.
const (
	eggNone  uint8 = iota // no creator mentioned — the common case, no glow draws
	eggFanat              // FanatSors — creator of Attorney Online (rainbow ring)
	eggOmni               // OmniTroid — creator of AO2 (blue<->gold pulse + scanner sweep)
	eggNyah               // SyntaxNyah — creator of AsyncAO (Mayo-pink heartbeat glow)
)

// Trigger substrings, matched case-insensitively against the displayed message
// text. These are the FULL handles, deliberately: matching the short suffix
// ("fanat", "omni") would light up on innocent words like "fanatic" / "android"
// — the whole point of the negative test rows.
const (
	eggNameFanat = "fanatsors"
	eggNameOmni  = "omnitroid"
	eggNameNyah  = "syntaxnyah"
)

// creatorEgg scans a displayed IC message for a creator mention and returns the
// egg kind (eggNone when none is present). Case-insensitive substring match;
// priority is fanat > omni > nyah (creation lineage AO → AO2 → AsyncAO), so a
// message naming two creators lights the earliest in that lineage. Callers cache
// the result — this allocates one ToLower string, which is fine ONCE per message
// but must never run per frame (see refreshEggKind's compare guard).
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
	default:
		return eggNone
	}
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
	}
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
