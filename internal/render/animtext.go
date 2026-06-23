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
)

// RasterizeAnimated lays out text (wrapped to wrapW) into per-glyph animRunes, tagging
// each rune with its effect (from spans) and base colour. No textures here — Draw resolves
// them from the GlyphCache. Render thread; call once per message.
func RasterizeAnimated(font *ttf.Font, text string, spans []EffectSpan, color sdl.Color, wrapW int32) *AnimatedText {
	at := &AnimatedText{lineH: int32(font.Height())}
	gi := 0 // global rune index into the clean text (what spans reference)
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
				color:  color,
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
