// Package gifenc encodes animated GIFs for the scene exporter; inter-frame diffing makes long GIFs practical.
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

// transparentIndex marks pixels unchanged from the previous frame: the encoder treats the first alpha-0 entry
// as transparent, and the alpha gap keeps color.Palette.Index from choosing it for an opaque pixel.
const transparentIndex = 0xff

// quantPalette is 255 Plan9 colours plus the transparent entry; median-cut isn't worth it for GIF.
var quantPalette = buildQuantPalette()

func buildQuantPalette() color.Palette {
	p := make(color.Palette, 256)
	copy(p, palette.Plan9[:transparentIndex]) // indices 0..254
	p[transparentIndex] = color.RGBA{}        // {0,0,0,0}: alpha 0 → the transparent slot
	return p
}

// Quantize uses no dither on purpose: Floyd-Steinberg destroys the flat runs LZW needs and the frame-to-frame
// equality EncodeGIF diffs. The source can be freed right after.
func Quantize(src *image.RGBA) *image.Paletted {
	dst := image.NewPaletted(src.Bounds(), quantPalette)
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst
}

// EncodeGIF shrinks later frames to their changed rectangle with unchanged pixels transparent and "do not
// dispose"; an identical frame folds its delay into the previous one. delayCs is per emitted frame.
// A palette with no alpha-0 entry falls back to whole frames.
func EncodeGIF(frames []*image.Paletted, delayCs int, loop bool) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("gifenc: no frames to encode")
	}
	if delayCs < 1 {
		delayCs = 1
	}
	ti, canDiff := transparentIdx(frames[0].Palette)

	loopCount := 0 // 0 = loop forever
	if !loop {
		loopCount = -1 // -1 = play once, no loop
	}
	g := &gif.GIF{LoopCount: loopCount}
	// The first frame is whole; later frames diff against it.
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
		// The state before frame i is always frames[i-1], as a folded frame was identical to its predecessor.
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

// transparentIdx returns the first alpha-0 palette index, which the GIF encoder uses as transparent.
func transparentIdx(p color.Palette) (int, bool) {
	for i, c := range p {
		if _, _, _, a := c.RGBA(); a == 0 {
			return i, true
		}
	}
	return 0, false
}

// diffFrame reduces cur to the rectangle differing from prev; changed false means fold into the previous delay.
// prev and cur must share bounds and palette.
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
