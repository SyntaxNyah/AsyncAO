// Package gifenc is the pure-Go GIF side of the scene exporter: quantize RGBA
// frames to a palette and encode them into an animated GIF. It has no SDL/CGO
// dependency, so the whole encode path is unit-testable headlessly (the capture
// side that feeds it lives in the UI/render layer). GIF is the 256-colour export
// format — gradients band; animated WebP (internal/webpenc) is the higher-quality
// path. The size win that makes a long courtroom GIF practical is the inter-frame
// diff below: a mostly-static scene compresses to near-nothing per frame.
package gifenc

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
)

// transparentIndex is the palette slot reserved for inter-frame transparency. In
// a diffed frame, every pixel unchanged from the previous frame is set to this
// index; the GIF encoder treats the first alpha-0 palette entry as the frame's
// transparent colour, and with "do not dispose" the prior pixel shows through —
// so an almost-static frame ships only its handful of changed pixels. Reserving
// the TOP slot costs one real colour out of 256 (negligible), and the alpha gap
// keeps color.Palette.Index from ever choosing it for an opaque pixel.
const transparentIndex = 0xff

// quantPalette is 255 Plan9 colours + a transparent slot at transparentIndex.
// Plan9 is a reasonable general spread; GIF's 256-colour ceiling makes a per-
// frame median-cut diminishing returns — WebP is the real quality answer.
var quantPalette = buildQuantPalette()

func buildQuantPalette() color.Palette {
	p := make(color.Palette, 256)
	copy(p, palette.Plan9[:transparentIndex]) // indices 0..254
	p[transparentIndex] = color.RGBA{}        // {0,0,0,0}: alpha 0 → the transparent slot
	return p
}

// Quantize converts an RGBA frame to a paletted frame WITHOUT dithering
// (draw.Src = nearest palette colour). No dither is deliberate: GIF's LZW
// compresses runs of identical indices, and a courtroom scene is mostly flat
// regions that stay byte-identical between frames — exactly what EncodeGIF's
// inter-frame diff then collapses to nothing. Floyd–Steinberg scatters per-pixel
// error, destroying both the intra-frame runs and the frame-to-frame equality;
// that (plus storing every frame whole) is what made the v1 export enormous.
// The caller can free the source RGBA immediately after — only the small paletted
// frame (1 byte/px) is retained while frames accumulate for EncodeGIF.
func Quantize(src *image.RGBA) *image.Paletted {
	dst := image.NewPaletted(src.Bounds(), quantPalette)
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst
}

// EncodeGIF encodes paletted frames into an animated GIF that loops forever.
// Every frame after the first is inter-frame diffed: reduced to the sub-rectangle
// that changed, with unchanged pixels set transparent and disposal left as "do
// not dispose" so the previous pixels show through. A frame identical to its
// predecessor emits nothing — its delay folds into the previously emitted frame,
// preserving total duration with fewer frames. delayCs is the per-emitted-frame
// delay in centiseconds (derive it from the capture cadence — 12 fps → 8 cs).
//
// Diffing only runs when the frames' palette reserves a transparent slot (an
// alpha-0 entry, as Quantize's does). A palette without one falls back to whole
// frames, so EncodeGIF stays correct for any caller.
func EncodeGIF(frames []*image.Paletted, delayCs int) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("gifenc: no frames to encode")
	}
	if delayCs < 1 {
		delayCs = 1
	}
	ti, canDiff := transparentIdx(frames[0].Palette)

	g := &gif.GIF{LoopCount: 0} // 0 = loop forever
	// The first frame is whole — the keyframe every later frame diffs against.
	g.Image = append(g.Image, frames[0])
	g.Delay = append(g.Delay, delayCs)
	g.Disposal = append(g.Disposal, gif.DisposalNone)
	last := 0 // index in g.Image of the most recently emitted frame (for delay folding)

	for i := 1; i < len(frames); i++ {
		if !canDiff {
			g.Image = append(g.Image, frames[i])
			g.Delay = append(g.Delay, delayCs)
			g.Disposal = append(g.Disposal, gif.DisposalNone)
			last = len(g.Image) - 1
			continue
		}
		// The on-screen state before frame i is always frames[i-1]: a folded
		// (skipped) frame was identical to its predecessor, so disposal-none
		// leaves exactly that. Diffing against the immediate predecessor is correct.
		sub, changed := diffFrame(frames[i-1], frames[i], ti)
		if !changed {
			g.Delay[last] += delayCs // identical frame: extend the previous one's time
			continue
		}
		g.Image = append(g.Image, sub)
		g.Delay = append(g.Delay, delayCs)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
		last = len(g.Image) - 1
	}

	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// transparentIdx returns the first alpha-0 palette index (the slot the GIF
// encoder uses as the transparent colour), or ok=false if the palette has none.
func transparentIdx(p color.Palette) (int, bool) {
	for i, c := range p {
		if _, _, _, a := c.RGBA(); a == 0 {
			return i, true
		}
	}
	return 0, false
}

// diffFrame reduces cur to the sub-rectangle that differs from prev, with
// unchanged pixels set to the transparent index ti; changed reports whether
// anything differed at all (false → fold this frame into the previous delay).
// prev and cur must share bounds and palette (they do — same capture pipeline).
func diffFrame(prev, cur *image.Paletted, ti int) (sub *image.Paletted, changed bool) {
	b := cur.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		po := prev.PixOffset(b.Min.X, y)
		co := cur.PixOffset(b.Min.X, y)
		for x := b.Min.X; x < b.Max.X; x++ {
			if prev.Pix[po] != cur.Pix[co] {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
			po++
			co++
		}
	}
	if maxX < minX { // nothing changed
		return nil, false
	}
	rect := image.Rect(minX, minY, maxX+1, maxY+1)
	sub = image.NewPaletted(rect, cur.Palette)
	tb := byte(ti)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		po := prev.PixOffset(rect.Min.X, y)
		co := cur.PixOffset(rect.Min.X, y)
		so := sub.PixOffset(rect.Min.X, y)
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if prev.Pix[po] == cur.Pix[co] {
				sub.Pix[so] = tb // unchanged → transparent (prior pixel shows through)
			} else {
				sub.Pix[so] = cur.Pix[co]
			}
			po++
			co++
			so++
		}
	}
	return sub, true
}
