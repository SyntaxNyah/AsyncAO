package render

import (
	"math"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// Animated chat text (#M5): shake / wave / rainbow per-span effects in the chatbox. Each
// affected glyph is drawn from a WHITE GlyphCache texture, displaced and/or tinted per
// frame by cheap SCALAR math — no per-frame re-rasterise and no allocation. PLAIN messages
// never reach this path (the UI gates on effect spans), so the common case stays the
// untouched line raster. The math is deterministic in the clock, so a replay/export shows
// the same motion.

// Text effects — the wire codec and the renderer share these values.
const (
	EffectNone uint8 = iota
	EffectShake
	EffectWave
	EffectRainbow
	// #M5+ — MUST stay in the same order/values as courtroom.TextEffect* (pinned by
	// TestEffectIDsMatchRender). Motion effects first, then colour effects.
	EffectBounce
	EffectSway
	EffectShiver
	EffectWobble
	EffectTremble
	EffectFloat
	EffectPulse
	EffectGradient
	EffectBlink
	EffectSparkle
)

// EffectSpan marks Len consecutive runes (from Start, a rune index into the CLEAN visible
// text) with an effect. Spans cover only the affected runes; gaps are EffectNone.
type EffectSpan struct {
	Start  int
	Len    int
	Effect uint8
}

// EffectAnimates reports whether effect e moves/shimmers ON THE CLOCK — i.e. a message
// showing it needs frames to keep coming while it's on screen, or the motion freezes
// between redraws (the FX-text-stutters-at-idle report). Gradient is a STATIC per-rune
// band (identical every frame, see Draw) and None is plain text, so neither needs
// animation frames; under reduce-motion Draw renders EVERY effect static (rainbow pins
// to its static band), so nothing does. Must stay in agreement with Draw's switch.
func EffectAnimates(e uint8, reduceMotion bool) bool {
	if reduceMotion {
		return false
	}
	return e != EffectNone && e != EffectGradient
}

// FontResolver returns the face to lay out + draw rune r (at global clean rune index gi,
// the same index spans/colors use) plus whether it's a COLOUR-EMOJI glyph. The UI builds
// this from its existing per-rune fallback chain (CJK/broad faces + the colour-emoji face),
// so an effects line resolves fonts exactly the way a plain message does — no tofu, no
// uniform .notdef advances. Called ONCE per rune at layout time (never per frame), so it may
// allocate; a nil resolver (or a nil font return) means "use the single base font", the
// pre-fallback behaviour. emoji=true glyphs skip the colour tint at draw time (see Draw).
type FontResolver func(gi int, r rune) (font *ttf.Font, emoji bool)

// animRune is one laid-out glyph: its rune, base position (relative to the message origin),
// its effect, its base colour (rainbow overrides per frame), the face it was resolved to at
// layout time (mixed fonts coexist in the cache — glyphKey includes the font pointer), a
// baseline y-shift (faces differ in ascent — the shared-baseline align, mirrors emoji.go),
// and whether it's a colour-emoji glyph (drawn tint-free).
type animRune struct {
	r      rune
	x, y   int32
	yOff   int32 // ascent - font.Ascent(): drop shorter faces to the shared baseline
	effect uint8
	color  sdl.Color
	font   *ttf.Font // per-rune resolved face (nil → the base font passed to Draw/Warm)
	emoji  bool      // colour-emoji glyph: SetColorMod stays neutral so effects can't discolour it
}

// AnimatedText is the per-glyph layout of a message that has at least one effect. Built
// once by RasterizeAnimated; Draw resolves glyph textures from the cache and animates them
// with zero per-frame allocations.
type AnimatedText struct {
	runes []animRune
	lineH int32
	total int
	// timeAnimated: at least one laid-out rune carries a CLOCK-driven effect
	// (EffectAnimates) — precomputed so Animates is a field read per frame.
	timeAnimated bool
	dst          sdl.Rect // per-glyph scratch, reused so Draw never allocates
	// chainGen stamps the UI font-chain generation this layout was built at. The UI's
	// CJK/broad/emoji tiers load LAZILY (fontChainGen bumps when one lands); a line
	// rastered BEFORE its tier arrived must re-raster after — the chatbox keys msAnim's
	// validity on this the same way the log wrap caches key on fontChainGen. Zero for a
	// resolver-free (single-font / test) layout.
	chainGen int
}

// ChainGen reports the UI font-chain generation this layout was built at, so the chatbox
// can drop + rebuild an effects message once a lazily-loaded CJK/emoji tier lands (its
// glyphs would otherwise stay tofu). See AnimatedText.chainGen.
func (at *AnimatedText) ChainGen() int { return at.chainGen }

// Animates reports whether drawing this message again with a later clock produces a
// DIFFERENT picture — the UI's frame-pacing census hook: while an animating message is on
// screen the draw must keep marking frames (NoteAnimating), or the FX freeze at idle=0.
// False for gradient-only / effect-free layouts and under reduce-motion (Draw renders
// those identical every frame), so a static chatbox still parks at zero redraws.
func (at *AnimatedText) Animates(reduceMotion bool) bool {
	return !reduceMotion && at.timeAnimated
}

// Effect tuning — small + gentle so the text stays readable.
const (
	shakeAmpPx     = 1.6
	shakeFreqHz    = 21.0
	waveAmpPx      = 2.6
	waveSpeedHz    = 4.4
	waveRunePhase  = 0.55 // radians of wave phase per rune (the crest travels along the word)
	rainbowDegSec  = 95.0 // hue degrees per second
	rainbowDegRune = 16.0 // hue degrees per rune (a band sliding across the word)
	rainbowSat     = 0.85

	// #M5+ additional effects — same "small + gentle so text stays readable" budget.
	bounceAmpPx      = 3.0
	bounceFreqHz     = 3.0
	bounceRunePhase  = 0.6 // radians of hop phase per rune (the hop travels along the word)
	swayAmpPx        = 2.4
	swaySpeedHz      = 3.6
	swayRunePhase    = 0.5
	shiverAmpPx      = 1.2
	shiverFreqHz     = 30.0
	wobbleAmpPx      = 1.8
	wobbleSpeedHz    = 1.6
	wobbleRunePhase  = 0.9
	trembleAmpPx     = 1.1
	trembleFreqHz    = 27.0
	floatAmpPx       = 2.0
	floatSpeedHz     = 0.9 // slow, synchronised (no per-rune phase)
	pulseFreqHz      = 2.2
	pulseRunePhase   = 0.35
	pulseFloor       = 0.60 // dimmest brightness factor (stays readable)
	gradientDegRune  = 26.0 // hue degrees per rune for the static colour band
	gradientSat      = 0.85
	blinkFreqHz      = 2.6
	blinkFloor       = 0.35 // off-phase brightness (dim, not black — still legible)
	sparkleFreqHz    = 1.3  // twinkle rate per glyph
	sparkleRunePhase = 1.7  // desync each glyph's twinkle
	sparkleSharp     = 6.0  // higher = briefer, sharper flashes
)

// RasterizeAnimated lays out text (wrapped to wrapW) into per-glyph animRunes, tagging each
// rune with its effect (from spans), base colour (from colors, a per-clean-rune slice so
// inline \cN colours COMPOSE with shake/wave — a red shaking word) and its RESOLVED face
// (from resolve, the UI's per-rune fallback chain). The rainbow effect still overrides the
// colour per frame at draw time. colors is indexed by the same global rune index the effect
// spans + the resolver use, so colour + effect + face stay locked together; nil/short colors
// → white / the last colour. resolve may be nil (single-font / test) — every rune then uses
// font and no emoji flag, the pre-fallback behaviour. chainGen stamps the font-chain
// generation for lazy-tier re-raster (see AnimatedText.chainGen). No textures here — Draw
// resolves them from the GlyphCache. Render thread; call once per message.
func RasterizeAnimated(font *ttf.Font, text string, spans []EffectSpan, colors []sdl.Color, wrapW int32, resolve FontResolver, chainGen int) *AnimatedText {
	at := &AnimatedText{lineH: int32(font.Height()), chainGen: chainGen}
	runes := []rune(text)
	// Resolve every rune's face ONCE (layout time). A mixed-script / emoji line thus lays
	// out glyph-by-glyph on the faces that actually cover it — the wrap, the pen advance and
	// the baseline all follow from these, mirroring the plain fallback path (emoji.go).
	fonts := make([]*ttf.Font, len(runes))
	isEmoji := make([]bool, len(runes))
	for gi, r := range runes {
		f := font
		if resolve != nil {
			if rf, em := resolve(gi, r); rf != nil {
				f, isEmoji[gi] = rf, em
			}
		}
		fonts[gi] = f
	}
	// lineH / shared ascent over the faces ACTUALLY used: a CJK or emoji face is taller than
	// the base one, so a single-font height would clip it and the differing ascents would ride
	// glyphs high/low. Each rune's yOff drops its face to this shared baseline (mirrors
	// rasterizeFallbackLine). Height stays right too (Draw/typewriter step by lineH).
	var ascent int32
	for _, f := range fonts {
		if f == nil {
			continue
		}
		if h := int32(f.Height()); h > at.lineH {
			at.lineH = h
		}
		if a := int32(f.Ascent()); a > ascent {
			ascent = a
		}
	}
	for li, lr := range wrapAnimated(fonts, runes, wrapW) {
		baseY := int32(li) * at.lineH
		// Pen position runs per same-font RUN: within a run the glyph's x is measured
		// cumulatively (SizeUTF8 of the run prefix), so adjacent same-font glyphs keep their
		// KERNING (the old single-font path's behaviour, preserved for Latin); a font boundary
		// advances runBase by the run's full width, so CJK/emoji switch faces cleanly and stop
		// getting the base font's uniform advance. Mirrors buildSpan / rasterizeFallbackLine.
		var runBase int32
		for gi := lr.start; gi < lr.end; {
			f := fonts[gi]
			rj := gi + 1
			for rj < lr.end && fonts[rj] == f { // maximal same-font run
				rj++
			}
			for k := gi; k < rj; k++ {
				eff := effectAt(k, spans)
				if EffectAnimates(eff, false) {
					at.timeAnimated = true // rune-accurate: a span past the visible text can't hold frames
				}
				yOff := int32(0)
				if f != nil {
					yOff = ascent - int32(f.Ascent())
				}
				x := runBase
				if k > gi { // kerned pen position WITHIN this run
					x += runPrefixWidth(f, runes[gi:k])
				}
				at.runes = append(at.runes, animRune{
					r:      runes[k],
					x:      x,
					y:      baseY,
					yOff:   yOff,
					effect: eff,
					color:  colorAt(k, colors),
					font:   f,
					emoji:  isEmoji[k],
				})
			}
			runBase += runPrefixWidth(f, runes[gi:rj]) // advance past the whole run
			gi = rj
		}
	}
	at.total = len(at.runes)
	return at
}

// runPrefixWidth is the kerned pixel width of a same-font rune slice (SizeUTF8 of the whole
// slice) — the pen advance for a run, keeping intra-run kerning. 0 for a nil font / on error.
func runPrefixWidth(f *ttf.Font, runes []rune) int32 {
	if f == nil || len(runes) == 0 {
		return 0
	}
	if w, _, err := f.SizeUTF8(string(runes)); err == nil {
		return int32(w)
	}
	return 0
}

// runeAdvance is the pen advance for one rune under its OWN font — the per-rune metric that
// keeps CJK from collapsing to uniform .notdef boxes and sizes an emoji the way the plain
// wrap path does (its emoji face reports the same narrow-tofu width). 0 for a nil font or on
// a metrics error (harmless — the glyph still draws, only the next pen step is short).
func runeAdvance(f *ttf.Font, r rune) int32 {
	if f == nil {
		return 0
	}
	if w, _, err := f.SizeUTF8(string(r)); err == nil {
		return int32(w)
	}
	return 0
}

// wrapAnimated word-wraps runes to wrapW px measuring each rune with its OWN resolved font
// (a mixed-face line can't be measured with one font). Greedy, breaks on spaces, splits on
// '\n', mirrors wrapMultiFont's per-rune measurement (emoji.go). Unlike the old single-font
// wrap it keeps rune indices position-exact, so effect spans + colours (indexed by the clean
// rune index) stay aligned without the per-line +1 fudge. Build-time only.
func wrapAnimated(fonts []*ttf.Font, runes []rune, wrapW int32) []lineRange {
	n := len(runes)
	if wrapW <= 0 || n == 0 {
		return []lineRange{{0, n}}
	}
	var out []lineRange
	lineStart, lastSpace := 0, -1
	// i is advanced MANUALLY (not by a for-post statement) so a wrap-break can re-evaluate the
	// rune that overflowed under the NEW line — the wrapMultiFont structure (emoji.go). Width is
	// measured per-prefix; a mixed-face line can't be sized with one font, so no cached running
	// total (matches wrapMultiFont; a chat message is short, so O(line²) is negligible at build).
	for i := 0; i < n; {
		if runes[i] == '\n' {
			out = append(out, lineRange{lineStart, i})
			i++
			lineStart, lastSpace = i, -1
			continue
		}
		if runes[i] == ' ' {
			lastSpace = i
		}
		if measureAnimated(fonts, runes, lineStart, i+1) > wrapW && i > lineStart {
			if lastSpace > lineStart {
				out = append(out, lineRange{lineStart, lastSpace})
				lineStart = lastSpace + 1 // drop the breaking space
			} else {
				out = append(out, lineRange{lineStart, i})
				lineStart = i
			}
			lastSpace = -1
			continue // re-evaluate runes[i] on the new line
		}
		i++
	}
	out = append(out, lineRange{lineStart, n})
	return out
}

// measureAnimated is the pixel width of runes[lo:hi] under per-rune fonts (the wrap's
// per-prefix measure — a mixed-face line can't be sized with one font). Build-time only.
func measureAnimated(fonts []*ttf.Font, runes []rune, lo, hi int) int32 {
	var w int32
	for i := lo; i < hi; i++ {
		w += runeAdvance(fonts[i], runes[i])
	}
	return w
}

// effectAt returns the effect covering global rune index gi (EffectNone if none).
func effectAt(gi int, spans []EffectSpan) uint8 {
	for i := range spans {
		if gi >= spans[i].Start && gi < spans[i].Start+spans[i].Len {
			return spans[i].Effect
		}
	}
	return EffectNone
}

// colorAt returns the base colour for global rune index gi: white when colors is empty, the
// last colour when gi runs past the slice (a wrapped tail beyond the styled runs), else the
// per-rune colour. Lets a uniform message pass a single-element slice and have every rune
// take it (the clamp), while a \cN message passes a full per-rune slice.
func colorAt(gi int, colors []sdl.Color) sdl.Color {
	if len(colors) == 0 {
		return glyphWhite
	}
	if gi >= len(colors) {
		return colors[len(colors)-1]
	}
	return colors[gi]
}

// TotalRunes / Height mirror MessageRaster so the chatbox can size + reveal the same way.
func (at *AnimatedText) TotalRunes() int { return at.total }
func (at *AnimatedText) Height() int32 {
	if at.total == 0 {
		return 0
	}
	maxY := int32(0)
	for i := range at.runes {
		if at.runes[i].y > maxY {
			maxY = at.runes[i].y
		}
	}
	return maxY + at.lineH
}

// Warm pre-renders every glyph into the cache so subsequent Draws — including each frame of
// the typewriter reveal, when new runes appear — are allocation-free. Render thread; call
// once right after RasterizeAnimated (mirrors MessageRaster building all its line textures
// up front, so an effects message carries the same one-time build cost, not a per-frame one).
func (at *AnimatedText) Warm(ren *sdl.Renderer, gc *GlyphCache, font *ttf.Font) {
	for i := range at.runes {
		gc.glyph(ren, at.runeFont(&at.runes[i], font), at.runes[i].r)
	}
}

// runeFont returns the face to draw ar with: its per-rune resolved face, falling back to the
// base font when the resolver left it nil (single-font / test layouts). A pure field read —
// keeps Draw allocation-free while letting mixed fonts share the cache (glyphKey has the ptr).
func (at *AnimatedText) runeFont(ar *animRune, base *ttf.Font) *ttf.Font {
	if ar.font != nil {
		return ar.font
	}
	return base
}

// Draw renders the animated message up to visibleRunes (the typewriter reveal) at origin
// (ox, oy). reduceMotion pins rainbow to a static hue and stops all displacement — the
// accessibility / photosensitivity floor. ZERO allocations per frame on a warm glyph
// cache (every line below is a scratch-rect copy or scalar math).
func (at *AnimatedText) Draw(ren *sdl.Renderer, gc *GlyphCache, font *ttf.Font, clock time.Duration, visibleRunes int, ox, oy int32, reduceMotion bool) {
	t := clock.Seconds()
	for i := 0; i < len(at.runes) && i < visibleRunes; i++ {
		ar := &at.runes[i]
		cg := gc.glyph(ren, at.runeFont(ar, font), ar.r)
		if cg.tex == nil {
			continue // space / unrenderable
		}
		dx, dy := int32(0), int32(0)
		col := ar.color
		switch ar.effect {
		case EffectShake:
			if !reduceMotion {
				dx, dy = shakeOffset(t, i)
			}
		case EffectWave:
			if !reduceMotion {
				dy = waveOffset(t, i)
			}
		case EffectRainbow:
			if reduceMotion {
				col = hsvToRGB(math.Mod(float64(i)*rainbowDegRune, 360), rainbowSat, 1) // static band, no flashing
			} else {
				col = rainbowColor(t, i)
			}
		case EffectBounce:
			if !reduceMotion {
				dy = bounceOffset(t, i)
			}
		case EffectSway:
			if !reduceMotion {
				dx = swayOffset(t, i)
			}
		case EffectShiver:
			if !reduceMotion {
				dx = shiverOffset(t, i)
			}
		case EffectWobble:
			if !reduceMotion {
				dx, dy = wobbleGlyphOffset(t, i)
			}
		case EffectTremble:
			if !reduceMotion {
				dy = trembleOffset(t, i)
			}
		case EffectFloat:
			if !reduceMotion {
				dy = floatOffset(t)
			}
		case EffectPulse:
			if !reduceMotion {
				col = pulseColor(ar.color, t, i)
			}
		case EffectGradient:
			col = gradientColor(i) // static band — identical under reduceMotion
		case EffectBlink:
			if !reduceMotion {
				col = scaleColor(ar.color, blinkFactor(t))
			}
		case EffectSparkle:
			if !reduceMotion {
				col = sparkleColor(ar.color, t, i)
			}
		}
		if ar.emoji {
			// A colour emoji is drawn from the emoji face's own artwork: keep the tint
			// NEUTRAL so rainbow/gradient/pulse can't repaint it, but carry a brightness
			// effect over to its ALPHA so pulse/blink/sparkle still read on it, and let
			// displacement (dx/dy) apply as usual. Emoji glyphs use the emoji FACE, so they
			// never share a texture with a text glyph (glyphKey has the font ptr) — text
			// glyphs stay at the default alpha 255 and skip this extra per-glyph mod, keeping
			// a plain effects message's per-glyph cost exactly as before.
			_ = cg.tex.SetColorMod(255, 255, 255) // neutral: draw the emoji's own colours
			_ = cg.tex.SetAlphaMod(colorBrightnessAlpha(ar.color, col))
		} else {
			_ = cg.tex.SetColorMod(col.R, col.G, col.B)
		}
		at.dst = sdl.Rect{X: ox + ar.x + dx, Y: oy + ar.y + ar.yOff + dy, W: cg.w, H: cg.h}
		_ = ren.Copy(cg.tex, nil, &at.dst)
	}
}

// colorBrightnessAlpha maps a colour effect's RGB dimming back onto an opacity for a
// colour-emoji glyph (which can't be recoloured): the effect scaled base→eff on the darkest
// channel, applied as alpha so pulse/blink dim the emoji instead of tinting it. Rainbow/
// gradient don't dim (their eff is a full-brightness hue) → 255, the emoji stays solid.
// Pure + 0-alloc (Draw is on the per-frame path).
func colorBrightnessAlpha(base, eff sdl.Color) uint8 {
	// Use the brightest base channel as the reference so a pure-red base still yields a ratio.
	ref := base.R
	if base.G > ref {
		ref = base.G
	}
	if base.B > ref {
		ref = base.B
	}
	if ref == 0 {
		return 255 // black base has no brightness to scale — keep the emoji opaque
	}
	e := eff.R
	if eff.G > e {
		e = eff.G
	}
	if eff.B > e {
		e = eff.B
	}
	if e >= ref {
		return 255 // brightened / unchanged (rainbow, sparkle peak) — full opacity
	}
	return uint8(uint32(e) * 255 / uint32(ref))
}

// shakeOffset is a small deterministic jitter for glyph idx at time t (seconds). Two
// incommensurate frequencies for x/y read as a rattle, not a smooth sway. Pure, 0-alloc.
func shakeOffset(t float64, idx int) (int32, int32) {
	p := t*shakeFreqHz*2*math.Pi + float64(idx)*1.7
	return int32(math.Round(math.Sin(p) * shakeAmpPx)),
		int32(math.Round(math.Cos(p*1.37) * shakeAmpPx))
}

// waveOffset is a travelling vertical sine — a banner wave along the word. Pure, 0-alloc.
func waveOffset(t float64, idx int) int32 {
	p := t*waveSpeedHz*2*math.Pi + float64(idx)*waveRunePhase
	return int32(math.Round(math.Sin(p) * waveAmpPx))
}

// rainbowColor cycles the hue over time and along the word. Pure, 0-alloc.
func rainbowColor(t float64, idx int) sdl.Color {
	return hsvToRGB(math.Mod(t*rainbowDegSec+float64(idx)*rainbowDegRune, 360), rainbowSat, 1)
}

// hsvToRGB converts HSV (h in degrees, s/v in 0..1) to an opaque sdl.Color. Pure, 0-alloc.
func hsvToRGB(h, s, v float64) sdl.Color {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return sdl.Color{R: uint8((r + m) * 255), G: uint8((g + m) * 255), B: uint8((b + m) * 255), A: 255}
}

// --- #M5+ effect math (all pure + 0-alloc; deterministic in the clock so a replay/export matches) ---

// bounceOffset lifts glyph idx in a hopping sequence (up-only from the baseline).
func bounceOffset(t float64, idx int) int32 {
	p := t*bounceFreqHz*2*math.Pi - float64(idx)*bounceRunePhase
	return -int32(math.Round(math.Abs(math.Sin(p)) * bounceAmpPx))
}

// swayOffset is a travelling horizontal sine — the x-cousin of the wave.
func swayOffset(t float64, idx int) int32 {
	p := t*swaySpeedHz*2*math.Pi + float64(idx)*swayRunePhase
	return int32(math.Round(math.Sin(p) * swayAmpPx))
}

// shiverOffset is a fast, tiny horizontal tremor (a nervous shiver).
func shiverOffset(t float64, idx int) int32 {
	p := t*shiverFreqHz*2*math.Pi + float64(idx)*2.4
	return int32(math.Round(math.Sin(p) * shiverAmpPx))
}

// wobbleGlyphOffset drifts a glyph in a slow circle, phase-shifted per rune (a floaty roam). Named
// to avoid the sprite-layer wobbleOffset (viewport.go), which is a different, whole-sprite wobble.
func wobbleGlyphOffset(t float64, idx int) (int32, int32) {
	p := t*wobbleSpeedHz*2*math.Pi + float64(idx)*wobbleRunePhase
	return int32(math.Round(math.Cos(p) * wobbleAmpPx)), int32(math.Round(math.Sin(p) * wobbleAmpPx))
}

// trembleOffset is a fast, tiny VERTICAL tremor (the y-cousin of shiver).
func trembleOffset(t float64, idx int) int32 {
	p := t*trembleFreqHz*2*math.Pi + float64(idx)*2.1
	return int32(math.Round(math.Sin(p) * trembleAmpPx))
}

// floatOffset is a slow, synchronised vertical drift (calm — every glyph together).
func floatOffset(t float64) int32 {
	return int32(math.Round(math.Sin(t*floatSpeedHz*2*math.Pi) * floatAmpPx))
}

// scaleColor multiplies a colour's RGB by k (keeping alpha) — a brightness dim.
func scaleColor(base sdl.Color, k float64) sdl.Color {
	return sdl.Color{R: uint8(float64(base.R) * k), G: uint8(float64(base.G) * k), B: uint8(float64(base.B) * k), A: base.A}
}

// pulseColor shimmers a glyph's brightness with a wave travelling along the word (floored readable).
func pulseColor(base sdl.Color, t float64, idx int) sdl.Color {
	k := pulseFloor + (1-pulseFloor)*0.5*(1+math.Sin(t*pulseFreqHz*2*math.Pi-float64(idx)*pulseRunePhase))
	return scaleColor(base, k)
}

// blinkFactor is a brightness on/off (squared sine → more time bright, a snappy dim between).
func blinkFactor(t float64) float64 {
	s := math.Sin(t * blinkFreqHz * 2 * math.Pi)
	return blinkFloor + (1-blinkFloor)*s*s
}

// gradientColor is a STATIC per-rune hue band (no animation): colourful but calm, always readable
// (identical under ReduceMotion).
func gradientColor(idx int) sdl.Color {
	return hsvToRGB(math.Mod(float64(idx)*gradientDegRune, 360), gradientSat, 1)
}

// sparkleColor briefly flashes a glyph toward white on its own twinkle phase — a shimmer of stars.
func sparkleColor(base sdl.Color, t float64, idx int) sdl.Color {
	s := 0.5 * (1 + math.Sin(t*sparkleFreqHz*2*math.Pi+float64(idx)*sparkleRunePhase))
	f := math.Pow(s, sparkleSharp) // sharp, brief peaks
	return sdl.Color{
		R: uint8(float64(base.R) + (255-float64(base.R))*f),
		G: uint8(float64(base.G) + (255-float64(base.G))*f),
		B: uint8(float64(base.B) + (255-float64(base.B))*f),
		A: base.A,
	}
}
