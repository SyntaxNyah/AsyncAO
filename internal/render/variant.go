package render

import (
	"math"
	"time"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Per-pixel variants: SetColorMod can't negate or mix channels and sprite textures aren't readable,
// so each frame is drawn unblended to a target, read back, transformed and re-uploaded.
// Built once per (base, effect) and cached on the base page, which destroys them on eviction.

// VariantPage returns base transformed by effect, built on first use; (nil, false) draws unstyled. Render thread.
func (s *TextureStore) VariantPage(base string, effect courtroom.VariantEffect) (*TexturePage, bool) {
	if effect == courtroom.VariantNone {
		return nil, false
	}
	return s.variantFor(base, variantKey{effect: uint8(effect)})
}

// variantPaint is outside VariantEffect: adding it would widen the wire's Restyle bound to a colourless paint.
const variantPaint uint8 = 255

// maxVariantPages caps one base's cached pages, as a hue-slider drag walks through many paint colours.
const maxVariantPages = 10

// PaintPage returns base colorized; splitPct != 0 is two-tone. Inputs are quantised so drags give bounded pages.
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

// variantFor looks up or builds a variant; past maxVariantPages it evicts, preferring cheap paint entries.
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

// evictVariant frees one page; safe mid-frame, as SDL flushes queued commands before destroying a texture.
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

// readbackFrame reads src by a 1:1 unblended draw, so nothing premultiplies; it restores target and blend mode.
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

	pix := make([]byte, int(w)*int(h)*texBytesPerPixel)
	if err := s.ren.ReadPixels(&rect, uint32(sdl.PIXELFORMAT_ABGR8888), unsafe.Pointer(&pix[0]), int(w)*texBytesPerPixel); err != nil {
		return nil, err
	}
	return pix, nil
}

func uploadPixels(ren *sdl.Renderer, pix []byte, w, h int32) (*sdl.Texture, error) {
	tex, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_STATIC, w, h)
	if err != nil {
		return nil, err
	}
	if err := tex.Update(nil, unsafe.Pointer(&pix[0]), int(w)*texBytesPerPixel); err != nil {
		_ = tex.Destroy()
		return nil, err
	}
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	return tex, nil
}

// Variant pixel maths; weights are in thousandths (weightScale).
const (
	channelMax     = math.MaxUint8
	midGrey        = 128 // the first channel value above the midpoint
	rgbChannels    = 3
	weightScale    = 1000
	rec601R        = 299 // Rec.601 luma weights
	rec601G        = 587
	rec601B        = 114
	posterizeShift = 6  // keep the top 2 bits: 4 levels
	posterizeStep  = 85 // 255 / 3, so the levels are 0, 85, 170, 255
	neonContrast   = 3  // neon scales contrast around mid-grey by 3/2
	percentMax     = 100
	redShift       = 16 // bit positions in a packed 0xRRGGBB
	greenShift     = 8
)

//nolint:mnd // the classic sepia matrix in thousandths, a named data table (rule 9)
var sepiaMatrix = [rgbChannels][rgbChannels]int{
	{393, 769, 189},
	{349, 686, 168},
	{272, 534, 131},
}

// applyVariant transforms an ABGR8888 buffer in place, preserving alpha; only pixel art needs w and h.
func applyVariant(pix []byte, w, h int, effect uint8) {
	switch courtroom.VariantEffect(effect) {
	case courtroom.VariantInvert:
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i], pix[i+1], pix[i+2] = channelMax-pix[i], channelMax-pix[i+1], channelMax-pix[i+2]
		}
	case courtroom.VariantGrayscale:
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			y := byte(luma601(pix[i], pix[i+1], pix[i+2]))
			pix[i], pix[i+1], pix[i+2] = y, y, y
		}
	case courtroom.VariantSepia:
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			r, g, b := int(pix[i]), int(pix[i+1]), int(pix[i+2])
			m := &sepiaMatrix
			pix[i] = byte(min((m[0][0]*r+m[0][1]*g+m[0][2]*b)/weightScale, channelMax))
			pix[i+1] = byte(min((m[1][0]*r+m[1][1]*g+m[1][2]*b)/weightScale, channelMax))
			pix[i+2] = byte(min((m[2][0]*r+m[2][1]*g+m[2][2]*b)/weightScale, channelMax))
		}
	case courtroom.VariantPosterize:
		// Quantise each channel to 4 levels (0/85/170/255) for a poster look.
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i] = (pix[i] >> posterizeShift) * posterizeStep
			pix[i+1] = (pix[i+1] >> posterizeShift) * posterizeStep
			pix[i+2] = (pix[i+2] >> posterizeShift) * posterizeStep
		}
	case courtroom.VariantSilhouette:
		// Flat white: ColorMod tints it to the outline or shadow colour, so one variant serves both.
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i], pix[i+1], pix[i+2] = 255, 255, 255
		}
	case courtroom.VariantRedscale:
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i], pix[i+1], pix[i+2] = byte(luma601(pix[i], pix[i+1], pix[i+2])), 0, 0
		}
	case courtroom.VariantGreenscale:
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i], pix[i+1], pix[i+2] = 0, byte(luma601(pix[i], pix[i+1], pix[i+2])), 0
		}
	case courtroom.VariantBluescale:
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i], pix[i+1], pix[i+2] = 0, 0, byte(luma601(pix[i], pix[i+1], pix[i+2]))
		}
	case courtroom.VariantSolarize: // flip channels above mid — a psychedelic tone-swap
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			for k := range rgbChannels {
				if pix[i+k] >= midGrey {
					pix[i+k] = channelMax - pix[i+k]
				}
			}
		}
	case courtroom.VariantThreshold: // 1-bit black & white by luma
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			v := byte(0)
			if luma601(pix[i], pix[i+1], pix[i+2]) >= midGrey {
				v = channelMax
			}
			pix[i], pix[i+1], pix[i+2] = v, v, v
		}
	case courtroom.VariantDuotone: // luma across a fixed indigo -> gold ramp
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			y := luma601(pix[i], pix[i+1], pix[i+2])
			pix[i] = byte(duoShadowR + (duoHiR-duoShadowR)*y/channelMax)
			pix[i+1] = byte(duoShadowG + (duoHiG-duoShadowG)*y/channelMax)
			pix[i+2] = byte(duoShadowB + (duoHiB-duoShadowB)*y/channelMax)
		}
	case courtroom.VariantWarm: // push warm: more red, less blue
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i] = clamp8(int(pix[i]) + restyleShift)
			pix[i+2] = clamp8(int(pix[i+2]) - restyleShift)
		}
	case courtroom.VariantCool: // push cool: more blue, less red
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i+2] = clamp8(int(pix[i+2]) + restyleShift)
			pix[i] = clamp8(int(pix[i]) - restyleShift)
		}
	case courtroom.VariantNeon: // hard contrast punch around mid-grey
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i] = clamp8((int(pix[i])-midGrey)*neonContrast/2 + midGrey)
			pix[i+1] = clamp8((int(pix[i+1])-midGrey)*neonContrast/2 + midGrey)
			pix[i+2] = clamp8((int(pix[i+2])-midGrey)*neonContrast/2 + midGrey)
		}
	case courtroom.VariantInfrared: // false-colour channel rotate (R<-G<-B<-R)
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			pix[i], pix[i+1], pix[i+2] = pix[i+1], pix[i+2], pix[i]
		}
	case courtroom.VariantPixelArt: // #77 mosaic (block-average) + palette quantise
		applyPixelArt(pix, w, h)
	}
}

// Pixel-art block size and per-channel palette levels.
const (
	pixelArtBlock  = 6
	pixelArtLevels = 6
)

// applyPixelArt averages each cell, alpha-weighted so transparent edges don't darken it, then quantises.
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
			var sr, sg, sb, sa, n int
			for y := by; y < y1; y++ {
				row := y * w * texBytesPerPixel
				for x := bx; x < x1; x++ {
					i := row + x*texBytesPerPixel
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
				row := y * w * texBytesPerPixel
				for x := bx; x < x1; x++ {
					i := row + x*texBytesPerPixel
					pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, av
				}
			}
		}
	}
}

// quantizePixelArt snaps a channel to the nearest of pixelArtLevels values.
func quantizePixelArt(c byte) byte {
	lvl := (int(c)*(pixelArtLevels-1) + channelMax/2) / channelMax
	return byte(lvl * channelMax / (pixelArtLevels - 1))
}

const (
	restyleShift = 32 // warm/cool channel push
	// Duotone ramp: deep indigo to warm gold.
	duoShadowR, duoShadowG, duoShadowB = 40, 30, 80
	duoHiR, duoHiG, duoHiB             = 255, 210, 120
)

// Hue paint is a colorize ramp, as a multiply only removes light; the wire stays Tint+Grayscale.

// applyPaint colorizes in place, keeping alpha; splitPct != 0 uses A above that row, B below, with a feather.
func applyPaint(pix []byte, w, h int, ar, ag, ab, br, bg, bb uint8, splitPct uint8) {
	if splitPct == 0 || w <= 0 || h <= 0 || len(pix) < w*h*4 {
		for i := 0; i+3 < len(pix); i += texBytesPerPixel {
			y := luma601(pix[i], pix[i+1], pix[i+2])
			pix[i], pix[i+1], pix[i+2] = paintChannel(ar, y), paintChannel(ag, y), paintChannel(ab, y)
		}
		return
	}
	splitRow := h * int(splitPct) / percentMax
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
		base := row * w * texBytesPerPixel
		for x := 0; x < w; x++ {
			i := base + x*texBytesPerPixel
			y := luma601(pix[i], pix[i+1], pix[i+2])
			pix[i], pix[i+1], pix[i+2] = paintChannel(cr, y), paintChannel(cg, y), paintChannel(cb, y)
		}
	}
}

// The feather spans h/paintFeatherDivisor rows (at least paintFeatherMin), scaling with sprite resolution.
const (
	paintFeatherDivisor = 32
	paintFeatherMin     = 2
)

func lerp8(a, b uint8, t, span int) uint8 {
	return uint8((int(a)*(span-t) + int(b)*t) / span)
}

// paintSplitQuant is the split key granularity in percent, so a drag creates a page per step, not per pixel.
const paintSplitQuant = 2

// paintSplitQuantize snaps a 1..99 split, clamped so it stays a real split (never 0 or 100).
func paintSplitQuantize(s uint8) uint8 {
	q := s - s%paintSplitQuant
	if q < paintSplitQuant {
		q = paintSplitQuant
	}
	if q > percentMax-paintSplitQuant {
		q = percentMax - paintSplitQuant
	}
	return q
}

// paintChannel maps one channel of the ramp at luma y; continuous at the midpoint, exact at 0 and 255.
func paintChannel(c uint8, y int) byte {
	if y < midGrey {
		return byte(int(c) * y / (midGrey - 1))
	}
	return byte(int(c) + (channelMax-int(c))*(y-midGrey)/(midGrey-1))
}

// paintQuantizeLevels is invisible for a flat colour, and bounds the pages a full slider sweep can create.
const paintQuantizeLevels = 32

const paintQuantizeShift = 3

// paintQuantize snaps a channel to paintQuantizeLevels values, with exact endpoints.
func paintQuantize(c uint8) uint8 {
	return uint8((int(c) >> paintQuantizeShift) * channelMax / (paintQuantizeLevels - 1))
}

// packPaint and unpackPaint pack RGB into a variantKey's uint32 (0xRRGGBB).
func packPaint(r, g, b uint8) uint32 {
	return uint32(r)<<redShift | uint32(g)<<greenShift | uint32(b)
}
func unpackPaint(p uint32) (r, g, b uint8) {
	return uint8(p >> redShift), uint8(p >> greenShift), uint8(p)
}

func luma601(r, g, b byte) int {
	return (rec601R*int(r) + rec601G*int(g) + rec601B*int(b)) / weightScale
}

func clamp8(v int) byte { return byte(value.Clamp(v, 0, channelMax)) }
