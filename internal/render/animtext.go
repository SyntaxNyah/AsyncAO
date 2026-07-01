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

// animRune is one laid-out glyph: its rune, base position (relative to the message
// origin), its effect, and its base colour (rainbow overrides the colour per frame).
type animRune struct {
	r      rune
	x, y   int32
	effect uint8
	color  sdl.Color
}

// AnimatedText is the per-glyph layout of a message that has at least one effect. Built
// once by RasterizeAnimated; Draw resolves glyph textures from the cache and animates them
// with zero per-frame allocations.
type AnimatedText struct {
	runes []animRune
	lineH int32
	total int
	dst   sdl.Rect // per-glyph scratch, reused so Draw never allocates
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
// rune with its effect (from spans) and base colour (from colors, a per-clean-rune slice so
// inline \cN colours COMPOSE with shake/wave — a red shaking word). The rainbow effect still
// overrides the colour per frame at draw time. colors is indexed by the same global rune
// index the effect spans use, so colour + effect stay locked together; nil/short → white /
// the last colour. No textures here — Draw resolves them from the GlyphCache. Render thread;
// call once per message.
func RasterizeAnimated(font *ttf.Font, text string, spans []EffectSpan, colors []sdl.Color, wrapW int32) *AnimatedText {
	at := &AnimatedText{lineH: int32(font.Height())}
	gi := 0 // global rune index into the clean text (what spans + colors reference)
	for li, line := range wrapText(font, text, wrapW) {
		runes := []rune(line)
		baseY := int32(li) * at.lineH
		for i := 0; i < len(runes); i++ {
			x := int32(0)
			if i > 0 {
				if w, _, err := font.SizeUTF8(string(runes[:i])); err == nil { // kerned pen position
					x = int32(w)
				}
			}
			at.runes = append(at.runes, animRune{
				r:      runes[i],
				x:      x,
				y:      baseY,
				effect: effectAt(gi, spans),
				color:  colorAt(gi, colors),
			})
			gi++
		}
		gi++ // wrapText drops one breaking space per line; keep the global index aligned
	}
	at.total = len(at.runes)
	return at
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
		gc.glyph(ren, font, at.runes[i].r)
	}
}

// Draw renders the animated message up to visibleRunes (the typewriter reveal) at origin
// (ox, oy). reduceMotion pins rainbow to a static hue and stops all displacement — the
// accessibility / photosensitivity floor. ZERO allocations per frame on a warm glyph
// cache (every line below is a scratch-rect copy or scalar math).
func (at *AnimatedText) Draw(ren *sdl.Renderer, gc *GlyphCache, font *ttf.Font, clock time.Duration, visibleRunes int, ox, oy int32, reduceMotion bool) {
	t := clock.Seconds()
	for i := 0; i < len(at.runes) && i < visibleRunes; i++ {
		ar := &at.runes[i]
		cg := gc.glyph(ren, font, ar.r)
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
		_ = cg.tex.SetColorMod(col.R, col.G, col.B)
		at.dst = sdl.Rect{X: ox + ar.x + dx, Y: oy + ar.y + dy, W: cg.w, H: cg.h}
		_ = ren.Copy(cg.tex, nil, &at.dst)
	}
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
