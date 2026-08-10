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
	eggCrystal              // Crystalwarrior — AO2's keeper (prismatic shards over the STAGE + a refracting ring)
	eggNyah                 // SyntaxNyah — creator of AsyncAO (Mayo-pink heartbeat glow)
	eggScatter              // Scatterflower — petals over the STAGE + a petal-toned ring
	// The two GUEST eggs (v1.90.0 field 9). They sort below every name above and
	// that placement is the open–closed evidence rather than a preference: the
	// switch is first-match, so appending here is the only edit that cannot change
	// what any message already lit. Neither is in the client lineage and neither is
	// the named inspiration eggScatter honours, so there is no rung above the
	// bottom they could honestly claim.
	eggNorthgate // Northgate — an aurora hanging over the STAGE + a shimmering ramp ring
	eggMint      // Mint — frost creeping in over the STAGE + a mint/cocoa banded ring
)

// Trigger substrings, matched case-insensitively against the displayed message
// text. These are the FULL handles, deliberately: matching the short suffix
// ("fanat", "omni") would light up on innocent words like "fanatic" / "android"
// — the whole point of the negative test rows. The same reasoning keeps
// eggNameScatter whole: "flower" alone is an ordinary English word that would
// fire on any garden-variety line of roleplay.
const (
	eggNameFanat = "fanatsors"
	eggNameOmni  = "omnitroid"
	// eggNameCrystal is whole for the usual reason and one more: "crystal" on its
	// own is ordinary courtroom vocabulary (a crystal ball, a crystal-clear alibi),
	// and "warrior" is ordinary roleplay. Only the handle fires.
	eggNameCrystal = "crystalwarrior"
	eggNameNyah    = "syntaxnyah"
	eggNameScatter = "scatterflower"
	// eggNameNorthgate is whole and needs nothing else: it is a compound that does
	// not occur in ordinary courtroom English, so plain Contains is honest for it
	// exactly as it is for the five above.
	eggNameNorthgate = "northgate"
	// eggNameMint IS AN ORDINARY ENGLISH WORD, and that is why it is the one
	// trigger not matched by Contains. "mint condition", "peppermint", "minted"
	// and "the Mint" are all things a courtroom line says, and every one of them
	// contains this string — the negative rows in TestCreatorEgg exist precisely
	// to keep that class out. It is matched at WORD BOUNDARIES instead
	// (eggWordPresent), which is the same PRINCIPLE the full-handle rule serves
	// ("never fire on an innocent word") applied to a handle that cannot buy its
	// specificity from length.
	eggNameMint = "mint"
)

// creatorEgg scans a displayed IC message for a creator mention and returns the
// egg kind (eggNone when none is present). Case-insensitive substring match;
// priority is fanat > omni > crystal > nyah (creation lineage AO → AO2 → the
// person who keeps AO2 → AsyncAO) then scatter, so a message naming two people
// lights the earliest in that lineage and an inspiration never outranks a
// creator. Callers cache the result — this allocates one ToLower string, which is
// fine ONCE per message but must never run per frame (see refreshEggKind's
// compare guard).
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
	// The two guests, last (see the enum). Northgate first of the pair: it is the
	// unambiguous compound, while Mint is the one rung that had to buy its
	// specificity with a boundary rule — keeping the looser matcher at the very
	// bottom means a line naming both lights the one that could not have matched
	// by accident.
	case strings.Contains(low, eggNameNorthgate):
		return eggNorthgate
	case eggWordPresent(low, eggNameMint):
		return eggMint
	default:
		return eggNone
	}
}

// eggWordPresent reports whether word appears in low (already lower-cased) as a
// WHOLE WORD — i.e. with a non-letter, non-digit on both sides or a string edge.
//
// The one trigger that needs it is eggNameMint, for the reason stated at its
// declaration. It is deliberately NOT applied to the other five: those are
// compounds that cannot occur by accident, and switching them to boundaries would
// silently change what "fanatsors!!!" and "@omnitroid" light after years of
// Contains — a behaviour change dressed as a refactor.
//
// ASCII boundaries only, and that is the honest scope: an AO handle is ASCII by
// convention, and a rune-aware scan would need a decode per candidate on a path
// that runs once per message. A leading multi-byte letter reads as a boundary
// here, which errs toward FIRING rather than toward the false-positive class this
// exists to stop, and only for text that is not the word in the first place.
//
// Allocation-free: it indexes the string the caller already lower-cased, and the
// loop carries no closure.
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

// eggWordChar reports whether index i of s is an ASCII letter or digit. An index
// outside the string is a boundary (false), which is what makes a match at either
// end of the message count.
func eggWordChar(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	ch := s[i]
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
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
	if !eggDrawsOnStage(a.eggKind) || !a.d.Prefs.ScreenEffectsOn() || a.d.Prefs.ReduceMotion() {
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

// eggDrawsOnStage is the ONE list of eggs with a viewport half. A switch rather
// than a per-egg boolean field so adding a stage egg is a compile-time edit here
// and in drawStageEgg's dispatch, and a kind that grows a stage half but is left
// out of this list simply never draws it — which the census test catches.
func eggDrawsOnStage(kind uint8) bool {
	switch kind {
	case eggScatter, eggCrystal, eggNorthgate, eggMint:
		return true
	}
	return false
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

// --- Crystalwarrior: prismatic shards (stage) + a refracting ring (chatbox) -----
//
// Wholly PROCEDURAL, like the petals: no bundled art, no texture, no state. A
// shard is a stack of axis-aligned fills tapering to a point at each end (the
// only lozenge SDL_RenderFillRect can draw), tinted by sampling the hue wheel at
// a per-shard offset that drifts with the clock — which is what makes a plain
// rectangle read as a facet catching the light. Every property comes from
// eggHash(index), so the field is a pure function of (index, clock): no
// allocation, no seeding, and every AsyncAO client in the room shows the same
// shards.

const (
	// eggCrystalShards is the named cap on the shard field (hard rule 4): the draw
	// is exactly this many shards regardless of stage size, so the egg costs the
	// same at 4K as at 720p. Fewer than the petal field because a shard is a stack
	// of fills, not one — see eggCrystalSegments.
	eggCrystalShards = 12
	// eggCrystalSegments is how many stacked rects build one shard. 6 is enough for
	// the taper to read as a faceted spike rather than a bar, and pins the whole
	// egg's per-frame fill count at eggCrystalShards*eggCrystalSegments + 1.
	eggCrystalSegments = 6
	// eggCrystalRiseSecs is the base time a shard takes to drift up through the
	// stage, and eggCrystalRiseVary the fraction each shard scales that by — the
	// same anti-lockstep variance the petals use. Slower than the petal fall: these
	// are growing facets, not falling blossom.
	eggCrystalRiseSecs = 9.0
	eggCrystalRiseVary = 0.5
	// eggCrystalHeightMin/Max bound a shard's full height as a fraction of stage
	// height, so the field scales with the stage instead of shrinking to specks on a
	// big screen (the mistake a pixel constant would make here).
	eggCrystalHeightMinFrac = 0.10
	eggCrystalHeightMaxFrac = 0.26
	// eggCrystalWidthFrac is a shard's widest point as a fraction of its height —
	// slim, so the silhouette is a spike.
	eggCrystalWidthFrac = 0.22
	// eggCrystalHueDriftSecs is how long the shard field takes to walk the hue wheel
	// once, and eggCrystalHueSpread how far apart two shards' hues sit. Together
	// they keep the field prismatic (every shard a different colour) while the whole
	// spread rotates slowly.
	eggCrystalHueDriftSecs = 7.0
	eggCrystalHueSpread    = 0.35
	// eggCrystalGlintSecs is the period of a shard's brightness flash and
	// eggCrystalGlintFrac how much of that period is spent bright — a short, sharp
	// catch of the light rather than a pulse.
	eggCrystalGlintSecs = 2.3
	eggCrystalGlintFrac = 0.18
	// eggCrystalAlphaBase is a shard's resting opacity and eggCrystalAlphaGlint its
	// opacity at the crest of a glint. Both translucent: the stage art and the
	// speaking sprite must stay readable THROUGH the crystal, which is the whole
	// difference between a refraction effect and a curtain.
	eggCrystalAlphaBase  = 70
	eggCrystalAlphaGlint = 165
	// eggCrystalBeamSecs is how long the refraction beam takes to sweep the stage,
	// eggCrystalBeamFrac its width as a fraction of stage width, and
	// eggCrystalBeamAlpha its opacity — one pale vertical band travelling across,
	// the "light passing through the prism" that ties the field together.
	eggCrystalBeamSecs  = 5.0
	eggCrystalBeamFrac  = 0.035
	eggCrystalBeamAlpha = 46
	// eggCrystalRingGlintSecs is the chatbox ring's own glint period — deliberately
	// NOT the shards' (eggCrystalGlintSecs), so the box and the stage don't flash in
	// lockstep and read as one blinking object.
	eggCrystalRingGlintSecs = 3.1
	// eggCrystalRingHueStep offsets each successive ring along the hue wheel, so the
	// band splits into colours the way light does through a facet edge. Larger than
	// the rainbow egg's spread: FanatSors' ring is ONE colour sweeping, this one is
	// three colours at once.
	eggCrystalRingHueStep = 0.16
)

// eggCrystalSaturation keeps the facets pale rather than poster-paint: a crystal
// tints the light passing through it, it does not replace it. Full value so they
// still read as bright over a dark stage.
const eggCrystalSaturation = 0.45

// drawEggShards paints the Crystalwarrior facet field across vp: shards drifting
// upward through the hue wheel with a periodic glint, under one pale refraction
// beam. Clipped to the stage so a shard can never paint over the surrounding
// chrome, and ZERO-allocation — fixed counts, no slices, no closures, every rect a
// value through the Ctx scratch rect.
func (a *App) drawEggShards(vp sdl.Rect, t float64) {
	c := a.ctx
	if vp.W <= 0 || vp.H <= 0 {
		return
	}
	prev, had := c.pushClip(vp)
	hueBase := math.Mod(t/eggCrystalHueDriftSecs, 1)
	for i := uint32(0); i < eggCrystalShards; i++ {
		// Four independent draws from one index — separate salts, so no two
		// properties correlate (the petal field's rule).
		lane := eggUnit(eggHash(i*4 + 0))
		speed := eggUnit(eggHash(i*4 + 1))
		size := eggUnit(eggHash(i*4 + 2))
		phase := eggUnit(eggHash(i*4 + 3))

		// p walks 1 (stage bottom) → 0 (stage top): shards RISE, which is the
		// difference between a crystal growing into the frame and snow falling.
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
		// Fade a shard out as it nears either stage edge, for the same reason petals
		// fade: a facet that pops into existence on the clip line reads as a glitch.
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
	// The refraction beam, LAST so it lies over the facets: one pale vertical band
	// walking the stage. Colourless on purpose — it is the light, not a facet.
	bw := int32(eggCrystalBeamFrac * float64(vp.W))
	if bw < 1 {
		bw = 1
	}
	bx := vp.X + int32(math.Mod(t/eggCrystalBeamSecs, 1)*float64(vp.W))
	c.Fill(sdl.Rect{X: bx, Y: vp.Y, W: bw, H: vp.H},
		sdl.Color{R: 0xff, G: 0xff, B: 0xff, A: eggCrystalBeamAlpha})
	c.popClip(prev, had)
}

// eggShardTaper is segment s's width as a fraction of the shard's widest point:
// 0 at both ends, 1 in the middle, so the stack of rects reads as a double-pointed
// facet. Pure, so the silhouette is unit-testable without a renderer.
func eggShardTaper(s int32) float64 {
	mid := float64(eggCrystalSegments-1) / 2
	d := math.Abs(float64(s)-mid) / mid // 0 at the centre row, 1 at either end row
	return 1 - d*d                      // squared, so the taper is convex (a facet, not a triangle)
}

// eggShardGlint returns a 0..1 flash for a shard whose cycle is offset by phase:
// a cosine hump over the leading eggCrystalGlintFrac of each period, zero for the
// rest. Sharing one clock with a per-shard offset is what makes the field twinkle
// instead of strobing as a unit.
func eggShardGlint(t, phase float64) float64 {
	u := math.Mod(t/eggCrystalGlintSecs+phase, 1)
	if u >= eggCrystalGlintFrac {
		return 0
	}
	return 0.5 * (1 - math.Cos(2*math.Pi*u/eggCrystalGlintFrac))
}

// drawEggCrystalRing — the Crystalwarrior chatbox half. Three rings sitting at
// DIFFERENT points on the hue wheel at once (light split by a facet edge), the
// whole spread drifting slowly and flashing together on the ring's own glint
// clock. Distinct from the FanatSors rainbow, which is one colour chasing round
// the wheel with the rings barely apart.
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
		// The glint desaturates toward white, which is how a facet flashes: the
		// colour washes out at the crest instead of merely getting brighter.
		r, g, b := hsvToRGB(h, eggCrystalSaturation*(1-glint), 1)
		c.Border(ring, sdl.Color{R: r, G: g, B: b, A: eggRingAlpha(i)})
	}
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
