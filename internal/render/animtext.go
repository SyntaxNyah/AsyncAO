package render

import (
	"math"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// Animated chat text: white cached glyphs moved and tinted per frame, deterministic in the clock so replays match.

// Text effects; the wire codec and the renderer share these values.
const (
	EffectNone uint8 = iota
	EffectShake
	EffectWave
	EffectRainbow
	// Must stay in the same order and values as courtroom.TextEffect*; motion effects first, then colour.
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

// EffectSpan marks Len runes from Start, an index into the clean visible text; gaps are EffectNone.
type EffectSpan struct {
	Start  int
	Len    int
	Effect uint8
}

// EffectAnimates reports whether e changes on the clock; it must agree with Draw's switch.
func EffectAnimates(e uint8, reduceMotion bool) bool {
	if reduceMotion {
		return false
	}
	return e != EffectNone && e != EffectGradient
}

// FontResolver returns the face for rune r at clean index gi and whether it is a colour emoji; nil = base font.
type FontResolver func(gi int, r rune) (font *ttf.Font, emoji bool)

// animRune is one laid-out glyph; yOff aligns face ascents to a shared baseline, and emoji draw untinted.
type animRune struct {
	r      rune
	x, y   int32
	yOff   int32 // ascent - font.Ascent(): drop shorter faces to the shared baseline
	effect uint8
	color  sdl.Color
	font   *ttf.Font // per-rune resolved face (nil → the base font passed to Draw/Warm)
	emoji  bool      // colour-emoji glyph: SetColorMod stays neutral so effects can't discolour it
}

// AnimatedText is the per-glyph layout of a message with at least one effect; Draw doesn't allocate.
type AnimatedText struct {
	runes []animRune
	lineH int32
	total int
	// timeAnimated is precomputed so Animates is a field read per frame.
	timeAnimated bool
	dst          sdl.Rect // per-glyph scratch, reused so Draw never allocates
	// chainGen is the font-chain generation at layout; tiers load lazily, so older lines must re-raster.
	chainGen int
}

// ChainGen reports the font-chain generation, so the chatbox can rebuild a message once a lazily loaded tier lands.
func (at *AnimatedText) ChainGen() int { return at.chainGen }

// Animates reports whether a later clock draws differently; if so the draw must call NoteAnimating or effects freeze.
func (at *AnimatedText) Animates(reduceMotion bool) bool {
	return !reduceMotion && at.timeAnimated
}

// Effect tuning: small and gentle so the text stays readable.
const (
	shakeAmpPx     = 1.6
	shakeFreqHz    = 21.0
	shakeRunePhase = 1.7  // radians of rattle phase per rune
	shakeYFreqMul  = 1.37 // y rattles at an incommensurate multiple of x
	waveAmpPx      = 2.6
	waveSpeedHz    = 4.4
	waveRunePhase  = 0.55 // radians of wave phase per rune (the crest travels along the word)
	rainbowDegSec  = 95.0 // hue degrees per second
	rainbowDegRune = 16.0 // hue degrees per rune (a band sliding across the word)
	rainbowSat     = 0.85

	bounceAmpPx      = 3.0
	bounceFreqHz     = 3.0
	bounceRunePhase  = 0.6 // radians of hop phase per rune (the hop travels along the word)
	swayAmpPx        = 2.4
	swaySpeedHz      = 3.6
	swayRunePhase    = 0.5
	shiverAmpPx      = 1.2
	shiverFreqHz     = 30.0
	shiverRunePhase  = 2.4
	wobbleAmpPx      = 1.8
	wobbleSpeedHz    = 1.6
	wobbleRunePhase  = 0.9
	trembleAmpPx     = 1.1
	trembleFreqHz    = 27.0
	trembleRunePhase = 2.1
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

// RasterizeAnimated lays out text into animRunes, all indexed by global rune index; render thread, once per message.
func RasterizeAnimated(font *ttf.Font, text string, spans []EffectSpan, colors []sdl.Color, wrapW int32, resolve FontResolver, chainGen int) *AnimatedText {
	at := &AnimatedText{lineH: FontLineSpacing(font), chainGen: chainGen}
	runes := []rune(text)
	// Resolve each rune's face once, so wrap, pen and baseline follow the faces that cover it, as in emoji.go.
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
	// A CJK or emoji face is taller, so one font's height would clip it; yOff drops each to the shared baseline.
	var ascent int32
	for _, f := range fonts {
		if f == nil {
			continue
		}
		if h := FontLineSpacing(f); h > at.lineH {
			at.lineH = h
		}
		if a := int32(f.Ascent()); a > ascent {
			ascent = a
		}
	}
	for li, lr := range wrapAnimated(fonts, runes, wrapW) {
		baseY := int32(li) * at.lineH
		// Measure cumulatively within a same-font run to keep kerning; a font change advances runBase.
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

// runPrefixWidth is the kerned width of a same-font rune slice; 0 for a nil font or on error.
func runPrefixWidth(f *ttf.Font, runes []rune) int32 {
	if f == nil || len(runes) == 0 {
		return 0
	}
	if w, _, err := f.SizeUTF8(string(runes)); err == nil {
		return int32(w)
	}
	return 0
}

// runeAdvance is one rune's advance under its own font, so CJK doesn't collapse to .notdef widths.
func runeAdvance(f *ttf.Font, r rune) int32 {
	if f == nil {
		return 0
	}
	if w, _, err := f.SizeUTF8(string(r)); err == nil {
		return int32(w)
	}
	return 0
}

// wrapAnimated measures each rune with its own font, keeping indices exact so spans and colours stay aligned.
func wrapAnimated(fonts []*ttf.Font, runes []rune, wrapW int32) []lineRange {
	n := len(runes)
	if wrapW <= 0 || n == 0 {
		return []lineRange{{0, n}}
	}
	var out []lineRange
	lineStart, lastSpace := 0, -1
	// i advances manually so a wrap break can re-evaluate the overflowing rune on the new line.
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

// measureAnimated is the width of runes[lo:hi] under per-rune fonts.
func measureAnimated(fonts []*ttf.Font, runes []rune, lo, hi int) int32 {
	var w int32
	for i := lo; i < hi; i++ {
		w += runeAdvance(fonts[i], runes[i])
	}
	return w
}

func effectAt(gi int, spans []EffectSpan) uint8 {
	for i := range spans {
		if gi >= spans[i].Start && gi < spans[i].Start+spans[i].Len {
			return spans[i].Effect
		}
	}
	return EffectNone
}

// colorAt returns gi's base colour: white when colors is empty, the last colour past the end.
func colorAt(gi int, colors []sdl.Color) sdl.Color {
	if len(colors) == 0 {
		return glyphWhite
	}
	if gi >= len(colors) {
		return colors[len(colors)-1]
	}
	return colors[gi]
}

// TotalRunes and Height mirror MessageRaster so the chatbox can size and reveal the same way.
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

// Warm pre-renders every glyph so Draws don't allocate; render thread, once after RasterizeAnimated.
func (at *AnimatedText) Warm(ren *sdl.Renderer, gc *GlyphCache, font *ttf.Font) {
	for i := range at.runes {
		gc.glyph(ren, at.runeFont(&at.runes[i], font), at.runes[i].r)
	}
}

// runeFont returns ar's resolved face, or base when nil.
func (at *AnimatedText) runeFont(ar *animRune, base *ttf.Font) *ttf.Font {
	if ar.font != nil {
		return ar.font
	}
	return base
}

// Draw renders up to visibleRunes without allocating on a warm cache; reduceMotion is the photosensitivity floor.
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
				col = hsvToRGB(math.Mod(float64(i)*rainbowDegRune, degreesPerTurn), rainbowSat, 1) // static band, no flashing
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
			// A neutral tint stops colour effects repainting an emoji; brightness goes to alpha instead.
			_ = cg.tex.SetColorMod(math.MaxUint8, math.MaxUint8, math.MaxUint8) // neutral: draw the emoji's own colours
			_ = cg.tex.SetAlphaMod(colorBrightnessAlpha(ar.color, col))
		} else {
			_ = cg.tex.SetColorMod(col.R, col.G, col.B)
		}
		at.dst = sdl.Rect{X: ox + ar.x + dx, Y: oy + ar.y + ar.yOff + dy, W: cg.w, H: cg.h}
		_ = ren.Copy(cg.tex, nil, &at.dst)
	}
}

// colorBrightnessAlpha maps a colour effect's dimming to opacity, as a colour emoji can't be recoloured.
func colorBrightnessAlpha(base, eff sdl.Color) uint8 {
	// The brightest base channel is the reference, so a pure-red base still yields a ratio.
	ref := base.R
	if base.G > ref {
		ref = base.G
	}
	if base.B > ref {
		ref = base.B
	}
	if ref == 0 {
		return math.MaxUint8 // black base has no brightness to scale — keep the emoji opaque
	}
	e := eff.R
	if eff.G > e {
		e = eff.G
	}
	if eff.B > e {
		e = eff.B
	}
	if e >= ref {
		return math.MaxUint8 // brightened / unchanged (rainbow, sparkle peak) — full opacity
	}
	return uint8(uint32(e) * math.MaxUint8 / uint32(ref))
}

// shakeOffset is a deterministic jitter; incommensurate x and y frequencies read as a rattle, not a sway.
func shakeOffset(t float64, idx int) (int32, int32) {
	p := t*shakeFreqHz*2*math.Pi + float64(idx)*shakeRunePhase
	return int32(math.Round(math.Sin(p) * shakeAmpPx)),
		int32(math.Round(math.Cos(p*shakeYFreqMul) * shakeAmpPx))
}

func waveOffset(t float64, idx int) int32 {
	p := t*waveSpeedHz*2*math.Pi + float64(idx)*waveRunePhase
	return int32(math.Round(math.Sin(p) * waveAmpPx))
}

func rainbowColor(t float64, idx int) sdl.Color {
	return hsvToRGB(math.Mod(t*rainbowDegSec+float64(idx)*rainbowDegRune, degreesPerTurn), rainbowSat, 1)
}

// Hue landmarks bounding the six 60-degree sectors of hsvToRGB.
const (
	hueSector  = 60
	hueYellow  = hueSector
	hueGreen   = 2 * hueSector
	hueCyan    = 3 * hueSector
	hueBlue    = 4 * hueSector
	hueMagenta = 5 * hueSector

	degreesPerTurn = 360
)

func hsvToRGB(h, s, v float64) sdl.Color {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/hueSector, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < hueYellow:
		r, g, b = c, x, 0
	case h < hueGreen:
		r, g, b = x, c, 0
	case h < hueCyan:
		r, g, b = 0, c, x
	case h < hueBlue:
		r, g, b = 0, x, c
	case h < hueMagenta:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return sdl.Color{R: uint8((r + m) * math.MaxUint8), G: uint8((g + m) * math.MaxUint8), B: uint8((b + m) * math.MaxUint8), A: math.MaxUint8}
}

// bounceOffset lifts glyph idx up from the baseline in a hopping sequence.
func bounceOffset(t float64, idx int) int32 {
	p := t*bounceFreqHz*2*math.Pi - float64(idx)*bounceRunePhase
	return -int32(math.Round(math.Abs(math.Sin(p)) * bounceAmpPx))
}

func swayOffset(t float64, idx int) int32 {
	p := t*swaySpeedHz*2*math.Pi + float64(idx)*swayRunePhase
	return int32(math.Round(math.Sin(p) * swayAmpPx))
}

func shiverOffset(t float64, idx int) int32 {
	p := t*shiverFreqHz*2*math.Pi + float64(idx)*shiverRunePhase
	return int32(math.Round(math.Sin(p) * shiverAmpPx))
}

// wobbleGlyphOffset drifts a glyph in a slow circle, phase-shifted per rune.
func wobbleGlyphOffset(t float64, idx int) (int32, int32) {
	p := t*wobbleSpeedHz*2*math.Pi + float64(idx)*wobbleRunePhase
	return int32(math.Round(math.Cos(p) * wobbleAmpPx)), int32(math.Round(math.Sin(p) * wobbleAmpPx))
}

func trembleOffset(t float64, idx int) int32 {
	p := t*trembleFreqHz*2*math.Pi + float64(idx)*trembleRunePhase
	return int32(math.Round(math.Sin(p) * trembleAmpPx))
}

// floatOffset is a slow vertical drift, the same for every glyph.
func floatOffset(t float64) int32 {
	return int32(math.Round(math.Sin(t*floatSpeedHz*2*math.Pi) * floatAmpPx))
}

func scaleColor(base sdl.Color, k float64) sdl.Color {
	return sdl.Color{R: uint8(float64(base.R) * k), G: uint8(float64(base.G) * k), B: uint8(float64(base.B) * k), A: base.A}
}

// pulseColor shimmers brightness along the word, floored to stay readable.
func pulseColor(base sdl.Color, t float64, idx int) sdl.Color {
	k := pulseFloor + (1-pulseFloor)*(1+math.Sin(t*pulseFreqHz*2*math.Pi-float64(idx)*pulseRunePhase))/2
	return scaleColor(base, k)
}

// blinkFactor is a squared sine, so more time is spent bright.
func blinkFactor(t float64) float64 {
	s := math.Sin(t * blinkFreqHz * 2 * math.Pi)
	return blinkFloor + (1-blinkFloor)*s*s
}

// gradientColor is a static per-rune hue band, identical under ReduceMotion.
func gradientColor(idx int) sdl.Color {
	return hsvToRGB(math.Mod(float64(idx)*gradientDegRune, degreesPerTurn), gradientSat, 1)
}

// sparkleColor briefly flashes a glyph toward white on its own phase.
func sparkleColor(base sdl.Color, t float64, idx int) sdl.Color {
	s := (1 + math.Sin(t*sparkleFreqHz*2*math.Pi+float64(idx)*sparkleRunePhase)) / 2
	f := math.Pow(s, sparkleSharp) // sharp, brief peaks
	return sdl.Color{
		R: uint8(float64(base.R) + (math.MaxUint8-float64(base.R))*f),
		G: uint8(float64(base.G) + (math.MaxUint8-float64(base.G))*f),
		B: uint8(float64(base.B) + (math.MaxUint8-float64(base.B))*f),
		A: base.A,
	}
}
