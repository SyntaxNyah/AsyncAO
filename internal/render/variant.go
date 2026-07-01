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
	bp, ok := s.Get(base)
	if !ok || len(bp.Frames) == 0 || bp.W <= 0 || bp.H <= 0 {
		return nil, false
	}
	key := uint8(effect)
	if v, ok := bp.variants[key]; ok {
		return v, true
	}
	v, err := s.buildVariant(bp, key)
	if err != nil {
		return nil, false
	}
	if bp.variants == nil {
		bp.variants = make(map[uint8]*TexturePage, 1)
	}
	bp.variants[key] = v
	return v, true
}

// buildVariant transforms every frame of base into a new page (render thread).
func (s *TextureStore) buildVariant(base *TexturePage, effect uint8) (*TexturePage, error) {
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
		applyVariant(pix, int(base.W), int(base.H), effect)
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
