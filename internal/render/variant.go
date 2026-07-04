package render

import (
	"time"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Per-pixel sprite-style variants (#103): invert and grayscale can't ride the cheap
// SetColorMod bracket the other transmitted effects use (multiply can't negate or mix
// channels), so the renderer builds a genuinely TRANSFORMED texture. The decoded RGBA
// is freed the instant it's uploaded, and sprite textures are STATIC (not readable),
// so the variant is built on demand from the resident base page: render each frame to
// an offscreen target with NO blending (an exact 1:1 copy → straight, non-premultiplied
// pixels), read it back, transform the pixels, and upload a new texture. The result is
// cached on the base page (TexturePage.variants), so it's a 0-alloc map hit every
// frame after the first and is destroyed with the base page on eviction. Build cost is
// one-time per (base, effect); steady-state drawing never re-generates.

// VariantPage returns `base` transformed by `effect`, building + caching it on first
// use. Render thread only. Returns (page, true), or (nil, false) when the base isn't
// resident or the build failed — the caller then draws the base unstyled.
func (s *TextureStore) VariantPage(base string, effect courtroom.VariantEffect) (*TexturePage, bool) {
	if effect == courtroom.VariantNone {
		return nil, false
	}
	return s.variantFor(base, variantKey{effect: uint8(effect)})
}

// variantPaint keys the hue-paint colorize in a page's variant cache. It is a render-
// PRIVATE pseudo-effect deliberately OUTSIDE courtroom's VariantEffect enum: adding it
// there would widen VariantCount — the wire's accepted Restyle bound — and let a crafted
// frame request a paint variant that carries no colour. 255 leaves the enum room to grow.
const variantPaint uint8 = 255

// maxVariantPages caps how many transformed pages one base page may cache (rule §17.4:
// no unbounded caches). The classic effects are one key each, but the hue-paint colorize
// is keyed by its (quantised) colour, and a hue-slider drag walks through many of those.
const maxVariantPages = 10

// PaintPage returns `base` colorized with the hue-paint look: every pixel takes the
// paint colour while keeping its own light and shadow (see applyPaint). splitPct != 0
// selects the TWO-TONE paint: rows above the split (a percent of sprite height) take
// (r,g,b), rows below take (r2,g2,b2), feathered across the boundary. Colours and the
// split are quantised before keying AND building, so a slider drag lands on a bounded
// set of cached pages and the key always matches the pixels. splitPct == 0 ignores the
// second colour entirely (one cache entry per colour A, however B wiggles).
func (s *TextureStore) PaintPage(base string, r, g, b, r2, g2, b2, splitPct uint8) (*TexturePage, bool) {
	key := variantKey{
		effect: variantPaint,
		paintA: packPaint(paintQuantize(r), paintQuantize(g), paintQuantize(b)),
	}
	if splitPct != 0 {
		key.split = paintSplitQuantize(splitPct)
		key.paintB = packPaint(paintQuantize(r2), paintQuantize(g2), paintQuantize(b2))
	}
	return s.variantFor(base, key)
}

// variantFor is the shared lookup/build/insert path for every variant page. Inserting
// past maxVariantPages evicts one cached entry first — preferring a PAINT entry (the
// classic effects + the silhouette are hot every frame when in use; paint entries are
// cheap to rebuild and pile up only on the one sprite being colour-tuned).
func (s *TextureStore) variantFor(base string, key variantKey) (*TexturePage, bool) {
	bp, ok := s.Get(base)
	if !ok || len(bp.Frames) == 0 || bp.W <= 0 || bp.H <= 0 {
		return nil, false
	}
	if v, ok := bp.variants[key]; ok {
		return v, true
	}
	v, err := s.buildVariant(bp, key)
	if err != nil {
		return nil, false
	}
	if bp.variants == nil {
		bp.variants = make(map[variantKey]*TexturePage, 1)
	}
	if len(bp.variants) >= maxVariantPages {
		evictVariant(bp)
	}
	bp.variants[key] = v
	return v, true
}

// evictVariant frees one cached variant page to admit a new one (see variantFor for
// the paint-first policy). Destroying is safe mid-frame: SDL flushes any queued render
// commands that still reference a texture before destroying it.
func evictVariant(bp *TexturePage) {
	var victim variantKey
	found := false
	for k := range bp.variants {
		if k.effect == variantPaint {
			victim, found = k, true
			break
		}
		if !found {
			victim, found = k, true
		}
	}
	if found {
		bp.variants[victim].destroy()
		delete(bp.variants, victim)
	}
}

// buildVariant transforms every frame of base into a new page (render thread).
func (s *TextureStore) buildVariant(base *TexturePage, key variantKey) (*TexturePage, error) {
	v := &TexturePage{
		Animated: base.Animated,
		W:        base.W,
		H:        base.H,
		Delays:   append([]time.Duration(nil), base.Delays...),
	}
	for _, frame := range base.Frames {
		pix, err := s.readbackFrame(frame, base.W, base.H)
		if err != nil {
			v.destroy()
			return nil, err
		}
		if key.effect == variantPaint {
			ar, ag, ab := unpackPaint(key.paintA)
			br, bg, bb := unpackPaint(key.paintB)
			applyPaint(pix, int(base.W), int(base.H), ar, ag, ab, br, bg, bb, key.split)
		} else {
			applyVariant(pix, int(base.W), int(base.H), key.effect)
		}
		tex, err := uploadPixels(s.ren, pix, base.W, base.H)
		if err != nil {
			v.destroy()
			return nil, err
		}
		v.Frames = append(v.Frames, tex)
		v.bytes += int64(len(pix))
	}
	return v, nil
}

// readbackFrame reads src's exact pixels (ABGR8888 = image.RGBA byte order) by drawing
// it 1:1 onto an offscreen target with NO blend (so no premultiply/blend with the
// target) and reading back. Restores whatever render target was active (the screen, or
// a capture target during an export) and src's normal blend mode.
func (s *TextureStore) readbackFrame(src *sdl.Texture, w, h int32) ([]byte, error) {
	target, err := s.ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_TARGET, w, h)
	if err != nil {
		return nil, err
	}
	defer target.Destroy()
	prev := s.ren.GetRenderTarget() // nil = the screen; non-nil during a capture/export
	if err := s.ren.SetRenderTarget(target); err != nil {
		return nil, err
	}
	defer s.ren.SetRenderTarget(prev)

	_ = s.ren.SetDrawColor(0, 0, 0, 0)
	_ = s.ren.Clear()
	_ = src.SetBlendMode(sdl.BLENDMODE_NONE) // exact copy: target pixels = src pixels
	rect := sdl.Rect{X: 0, Y: 0, W: w, H: h}
	_ = s.ren.Copy(src, nil, &rect)
	_ = src.SetBlendMode(sdl.BLENDMODE_BLEND) // restore (the base frame still draws normally)

	pix := make([]byte, int(w)*int(h)*4)
	if err := s.ren.ReadPixels(&rect, uint32(sdl.PIXELFORMAT_ABGR8888), unsafe.Pointer(&pix[0]), int(w)*4); err != nil {
		return nil, err
	}
	return pix, nil
}

// uploadPixels turns a transformed RGBA buffer into a STATIC blended texture (mirrors
// buildPage's per-frame upload).
func uploadPixels(ren *sdl.Renderer, pix []byte, w, h int32) (*sdl.Texture, error) {
	tex, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_STATIC, w, h)
	if err != nil {
		return nil, err
	}
	if err := tex.Update(nil, unsafe.Pointer(&pix[0]), int(w)*4); err != nil {
		_ = tex.Destroy()
		return nil, err
	}
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	return tex, nil
}

// applyVariant transforms an ABGR8888 buffer (R,G,B,A per pixel) IN PLACE, preserving
// alpha. Pure (no SDL) so the maths is unit-tested. Invert negates RGB; grayscale uses
// Rec.601 luma (integer, alloc-free). w,h are the frame dimensions — only the pixel-art
// mosaic needs them (the per-pixel effects ignore them).
func applyVariant(pix []byte, w, h int, effect uint8) {
	switch courtroom.VariantEffect(effect) {
	case courtroom.VariantInvert:
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2] = 255-pix[i], 255-pix[i+1], 255-pix[i+2]
		}
	case courtroom.VariantGrayscale:
		for i := 0; i+3 < len(pix); i += 4 {
			y := byte((299*int(pix[i]) + 587*int(pix[i+1]) + 114*int(pix[i+2])) / 1000)
			pix[i], pix[i+1], pix[i+2] = y, y, y
		}
	case courtroom.VariantSepia:
		// Classic sepia matrix (integer, alloc-free), clamped to 255. #34.
		for i := 0; i+3 < len(pix); i += 4 {
			r, g, b := int(pix[i]), int(pix[i+1]), int(pix[i+2])
			nr := (393*r + 769*g + 189*b) / 1000
			ng := (349*r + 686*g + 168*b) / 1000
			nb := (272*r + 534*g + 131*b) / 1000
			if nr > 255 {
				nr = 255
			}
			if ng > 255 {
				ng = 255
			}
			if nb > 255 {
				nb = 255
			}
			pix[i], pix[i+1], pix[i+2] = byte(nr), byte(ng), byte(nb)
		}
	case courtroom.VariantPosterize:
		// Quantise each channel to 4 evenly-spaced levels (0/85/170/255) — a poster /
		// cel-shaded look. Alloc-free. #34.
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i] = (pix[i] >> 6) * 85
			pix[i+1] = (pix[i+1] >> 6) * 85
			pix[i+2] = (pix[i+2] >> 6) * 85
		}
	case courtroom.VariantSilhouette:
		// Flat WHITE shape, alpha untouched: a per-pixel ColorMod tints it to the outline
		// (white) or shadow (dark) colour at draw time, so one variant serves both (#8).
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2] = 255, 255, 255
		}
	// --- the "10 more restyles" set (all alloc-free, alpha preserved) ---
	case courtroom.VariantRedscale:
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2] = byte(luma601(pix[i], pix[i+1], pix[i+2])), 0, 0
		}
	case courtroom.VariantGreenscale:
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2] = 0, byte(luma601(pix[i], pix[i+1], pix[i+2])), 0
		}
	case courtroom.VariantBluescale:
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2] = 0, 0, byte(luma601(pix[i], pix[i+1], pix[i+2]))
		}
	case courtroom.VariantSolarize: // flip channels above mid — a psychedelic tone-swap
		for i := 0; i+3 < len(pix); i += 4 {
			for k := 0; k < 3; k++ {
				if pix[i+k] > 127 {
					pix[i+k] = 255 - pix[i+k]
				}
			}
		}
	case courtroom.VariantThreshold: // 1-bit black & white by luma
		for i := 0; i+3 < len(pix); i += 4 {
			v := byte(0)
			if luma601(pix[i], pix[i+1], pix[i+2]) > 127 {
				v = 255
			}
			pix[i], pix[i+1], pix[i+2] = v, v, v
		}
	case courtroom.VariantDuotone: // luma across a fixed indigo -> gold ramp
		for i := 0; i+3 < len(pix); i += 4 {
			y := luma601(pix[i], pix[i+1], pix[i+2])
			pix[i] = byte(duoShadowR + (duoHiR-duoShadowR)*y/255)
			pix[i+1] = byte(duoShadowG + (duoHiG-duoShadowG)*y/255)
			pix[i+2] = byte(duoShadowB + (duoHiB-duoShadowB)*y/255)
		}
	case courtroom.VariantWarm: // push warm: more red, less blue
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i] = clamp8(int(pix[i]) + restyleShift)
			pix[i+2] = clamp8(int(pix[i+2]) - restyleShift)
		}
	case courtroom.VariantCool: // push cool: more blue, less red
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i+2] = clamp8(int(pix[i+2]) + restyleShift)
			pix[i] = clamp8(int(pix[i]) - restyleShift)
		}
	case courtroom.VariantNeon: // hard contrast punch around mid-grey
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i] = clamp8((int(pix[i])-128)*3/2 + 128)
			pix[i+1] = clamp8((int(pix[i+1])-128)*3/2 + 128)
			pix[i+2] = clamp8((int(pix[i+2])-128)*3/2 + 128)
		}
	case courtroom.VariantInfrared: // false-colour channel rotate (R<-G<-B<-R)
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2] = pix[i+1], pix[i+2], pix[i]
		}
	case courtroom.VariantPixelArt: // #77 mosaic (block-average) + palette quantise
		applyPixelArt(pix, w, h)
	}
}

// Pixel-art tuning (#77): the mosaic block size and how many levels each colour
// channel is quantised to (a small palette → the flat, "anime pixel-art" look).
const (
	pixelArtBlock  = 6
	pixelArtLevels = 6
)

// applyPixelArt turns an ABGR8888 frame into blocky, palette-reduced pixel art IN
// PLACE: it averages each pixelArtBlock×pixelArtBlock cell (alpha-weighted, so
// transparent edge pixels don't darken the colour), quantises the block colour to
// a small palette, and writes it back to every pixel in the cell. Alpha is the
// block's mean, so cutout edges pixelate too. Pure + alloc-free.
func applyPixelArt(pix []byte, w, h int) {
	if w <= 0 || h <= 0 || len(pix) < w*h*4 {
		return
	}
	for by := 0; by < h; by += pixelArtBlock {
		y1 := by + pixelArtBlock
		if y1 > h {
			y1 = h
		}
		for bx := 0; bx < w; bx += pixelArtBlock {
			x1 := bx + pixelArtBlock
			if x1 > w {
				x1 = w
			}
			// Alpha-weighted colour sum + plain alpha sum over the block.
			var sr, sg, sb, sa, n int
			for y := by; y < y1; y++ {
				row := y * w * 4
				for x := bx; x < x1; x++ {
					i := row + x*4
					a := int(pix[i+3])
					sr += int(pix[i]) * a
					sg += int(pix[i+1]) * a
					sb += int(pix[i+2]) * a
					sa += a
					n++
				}
			}
			var r, g, b, av byte
			if n > 0 {
				av = byte(sa / n)
			}
			if sa > 0 { // alpha-weighted mean colour, then palette-quantised
				r = quantizePixelArt(byte(sr / sa))
				g = quantizePixelArt(byte(sg / sa))
				b = quantizePixelArt(byte(sb / sa))
			}
			for y := by; y < y1; y++ {
				row := y * w * 4
				for x := bx; x < x1; x++ {
					i := row + x*4
					pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, av
				}
			}
		}
	}
}

// quantizePixelArt snaps a 0..255 channel to one of pixelArtLevels evenly-spaced
// values (a small palette), rounding to the nearest level.
func quantizePixelArt(c byte) byte {
	lvl := (int(c)*(pixelArtLevels-1) + 127) / 255
	return byte(lvl * 255 / (pixelArtLevels - 1))
}

// Restyle pixel-math tuning + helpers (pure, alloc-free).
const (
	restyleShift = 32 // warm/cool channel push
	// Duotone shadow -> highlight ramp (deep indigo -> warm gold).
	duoShadowR, duoShadowG, duoShadowB = 40, 30, 80
	duoHiR, duoHiG, duoHiB             = 255, 210, 120
)

// --- hue paint (colorize) ----------------------------------------------------
//
// The v1.53.5 hue paint rendered as "grayscale variant × multiply tint": value-
// preserving on paper, but a multiply can only remove light, so every saturated
// hue crushed the highlights and the sprite read as the plain dark recolour
// (the playtest bug report). The paint is now its own per-pixel variant: a
// classic colorize ramp — shadows lerp black→colour, highlights lerp
// colour→white — so white stays white, black stays black, and the midtones
// carry the hue. The WIRE is unchanged (still Tint+Grayscale): an older client
// simply renders the old, darker composition of the same fields.

// applyPaint colorizes an ABGR8888 buffer IN PLACE, keeping each pixel's alpha and
// mapping its Rec.601 luma through the ramp above. splitPct == 0 paints everything
// (ar,ag,ab); otherwise rows above the split row (splitPct% of h from the top) take
// colour A and rows below take colour B — "head red, rest blue" — with the colour
// LERPED across a thin feather band so the boundary doesn't read as a hard cut
// through the body. Pure (no SDL), integer, alloc-free.
func applyPaint(pix []byte, w, h int, ar, ag, ab, br, bg, bb uint8, splitPct uint8) {
	if splitPct == 0 || w <= 0 || h <= 0 || len(pix) < w*h*4 {
		for i := 0; i+3 < len(pix); i += 4 {
			y := luma601(pix[i], pix[i+1], pix[i+2])
			pix[i], pix[i+1], pix[i+2] = paintChannel(ar, y), paintChannel(ag, y), paintChannel(ab, y)
		}
		return
	}
	splitRow := h * int(splitPct) / 100
	feather := h / paintFeatherDivisor
	if feather < paintFeatherMin {
		feather = paintFeatherMin
	}
	for row := 0; row < h; row++ {
		// Row colour: A above the feather band, B below, lerped inside it.
		cr, cg, cb := ar, ag, ab
		switch d := row - splitRow; {
		case d >= feather:
			cr, cg, cb = br, bg, bb
		case d > -feather:
			t, span := d+feather, 2*feather // 0..span across the band
			cr, cg, cb = lerp8(ar, br, t, span), lerp8(ag, bg, t, span), lerp8(ab, bb, t, span)
		}
		base := row * w * 4
		for x := 0; x < w; x++ {
			i := base + x*4
			y := luma601(pix[i], pix[i+1], pix[i+2])
			pix[i], pix[i+1], pix[i+2] = paintChannel(cr, y), paintChannel(cg, y), paintChannel(cb, y)
		}
	}
}

// Two-tone feather tuning: the boundary blend spans ±(h/paintFeatherDivisor) rows
// (floored at paintFeatherMin) so it scales with sprite resolution.
const (
	paintFeatherDivisor = 32
	paintFeatherMin     = 2
)

// lerp8 linearly interpolates a..b at t/span (integer, span > 0).
func lerp8(a, b uint8, t, span int) uint8 {
	return uint8((int(a)*(span-t) + int(b)*t) / span)
}

// paintSplitQuant is the split-row key granularity (percent): a split-slider drag
// mints a page only every paintSplitQuant percent instead of one per pixel of drag.
const paintSplitQuant = 2

// paintSplitQuantize snaps a 1..99 split to paintSplitQuant steps, clamped so the
// result stays a real split (never 0 = single-colour, never 100 = empty band).
func paintSplitQuantize(s uint8) uint8 {
	q := s - s%paintSplitQuant
	if q < paintSplitQuant {
		q = paintSplitQuant
	}
	if q > 100-paintSplitQuant {
		q = 100 - paintSplitQuant
	}
	return q
}

// paintChannel maps one output channel of the colorize ramp at luma y: 0..127
// lerps 0→c (shadows), 128..255 lerps c→255 (highlights). Continuous at the
// midpoint (both halves yield c) and exact at the endpoints (0→0, 255→255).
func paintChannel(c uint8, y int) byte {
	if y < 128 {
		return byte(int(c) * y / 127)
	}
	return byte(int(c) + (255-int(c))*(y-128)/127)
}

// paintQuantizeLevels is how many levels each paint channel snaps to before
// keying a variant page: 32 steps (8 apart) are invisible for a flat paint
// colour, and they bound how many distinct pages a full slider sweep can mint.
const paintQuantizeLevels = 32

// paintQuantize snaps a paint channel to paintQuantizeLevels evenly-spaced
// values with the endpoints exact (0→0, 255→255).
func paintQuantize(c uint8) uint8 {
	return uint8((int(c) >> 3) * 255 / (paintQuantizeLevels - 1))
}

// packPaint / unpackPaint pack an RGB triple into a variantKey's uint32 (0xRRGGBB).
func packPaint(r, g, b uint8) uint32 {
	return uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}
func unpackPaint(p uint32) (r, g, b uint8) {
	return uint8(p >> 16), uint8(p >> 8), uint8(p)
}

// luma601 is the Rec.601 luma of an RGB triple (integer, 0..255).
func luma601(r, g, b byte) int { return (299*int(r) + 587*int(g) + 114*int(b)) / 1000 }

// clamp8 clamps an int to a 0..255 byte.
func clamp8(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}
