// Package gifenc is the pure-Go GIF side of the scene exporter: quantize RGBA
// frames to a palette and encode them into an animated GIF. It has no SDL/CGO
// dependency, so the whole encode path is unit-testable headlessly (the capture
// side that feeds it lives in the UI/render layer). GIF is the v1 export format
// — inherently 256-colour, so gradients band; animated WebP (decode-only here
// today) is the eventual higher-quality path.
package gifenc

import (
	"bytes"
	"errors"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
)

// quantPalette is the shared 256-colour palette. Plan9 is a reasonable general
// spread; a per-frame median-cut would look better but GIF's 256-colour ceiling
// makes it diminishing returns — WebP is the real quality answer later.
var quantPalette = palette.Plan9

// Quantize converts an RGBA frame to a paletted frame with Floyd–Steinberg
// dithering. Pure Go — the caller can free the source RGBA immediately after, so
// only the small paletted frame (1 byte/px) is retained while frames accumulate
// (the export holds every frame for gif.EncodeAll, so per-frame footprint is the
// whole memory story — see the capped capture resolution).
func Quantize(src *image.RGBA) *image.Paletted {
	dst := image.NewPaletted(src.Bounds(), quantPalette)
	draw.FloydSteinberg.Draw(dst, src.Bounds(), src, src.Bounds().Min)
	return dst
}

// EncodeGIF encodes paletted frames into an animated GIF that loops forever,
// with a uniform per-frame delay in centiseconds (derive it from the fixed
// capture cadence — e.g. 12 fps → 8 cs — so playback is even).
func EncodeGIF(frames []*image.Paletted, delayCs int) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("gifenc: no frames to encode")
	}
	if delayCs < 1 {
		delayCs = 1
	}
	g := &gif.GIF{
		Image:     frames,
		Delay:     make([]int, len(frames)),
		LoopCount: 0, // 0 = loop forever
	}
	for i := range g.Delay {
		g.Delay[i] = delayCs
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
