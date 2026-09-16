package assets

import "image"

// DXT / BC compressed-texture FOURCC values (SDL_PIXELFORMAT_DXT1 / DXT5).
// go-sdl2 v0.4.40 does not expose these constants, so they are defined here as
// the raw SDL_DEFINE_PIXELFOURCC('D','X','T',n) values CreateTexture expects.
// The decode pool stamps Decoded.DXTFormat with one of these; the render thread
// passes it straight through to CreateTexture.
const (
	DXT1FourCC uint32 = 0x31545844 // '1','T','X','D'
	DXT5FourCC uint32 = 0x35545844 // '5','T','X','D'
)

// dxtBlockDim is the fixed 4x4 texel block size S3TC uses.
const dxtBlockDim = 4

// Block byte sizes: DXT1 (BC1) is 4 bpp, DXT5 (BC3) is 8 bpp.
const (
	dxt1BlockBytes = 8
	dxt5BlockBytes = 16
)

// CompressedBlockBytes returns the encoded size of one frame in the given
// FOURCC format. Width and height must already be multiples of 4.
func CompressedBlockBytes(format uint32, w, h int) int {
	bytes := dxt1BlockBytes
	if format == DXT5FourCC {
		bytes = dxt5BlockBytes
	}
	return (w / dxtBlockDim) * (h / dxtBlockDim) * bytes
}

// EncodeDXT1 compresses an RGBA image to DXT1 (BC1, 4 bpp). Width and height
// MUST be multiples of 4. Fully-opaque blocks use the 4-color mode; a block
// containing a semi-transparent texel uses the 3-color + transparent mode.
func EncodeDXT1(img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	bw, bh := w/dxtBlockDim, h/dxtBlockDim
	out := make([]byte, bw*bh*dxt1BlockBytes)
	di := 0
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			di = encodeDXT1Block(out, di, img, bx*dxtBlockDim, by*dxtBlockDim)
		}
	}
	return out
}

// EncodeDXT5 compresses an RGBA image to DXT5 (BC3, 8 bpp). Width and height
// MUST be multiples of 4. Alpha is interpolated (3-bit indices over an 8-value
// ramp), so soft sprite edges survive.
func EncodeDXT5(img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	bw, bh := w/dxtBlockDim, h/dxtBlockDim
	out := make([]byte, bw*bh*dxt5BlockBytes)
	di := 0
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			di = encodeDXT5Block(out, di, img, bx*dxtBlockDim, by*dxtBlockDim)
		}
	}
	return out
}

func encodeDXT1Block(out []byte, di int, img *image.RGBA, x0, y0 int) int {
	var r, g, b, a [16]uint8
	readBlock(img, x0, y0, &r, &g, &b, &a)

	hasTransparent := false
	opaqueCount := 0
	for i := 0; i < 16; i++ {
		if a[i] < 128 {
			hasTransparent = true
		} else {
			opaqueCount++
		}
	}

	if opaqueCount == 0 {
		// Fully transparent block: 3-color mode (c0 <= c1), every index = 3.
		out[di] = 0
		out[di+1] = 0
		out[di+2] = 0
		out[di+3] = 0
		out[di+4] = 0xFF
		out[di+5] = 0xFF
		out[di+6] = 0xFF
		out[di+7] = 0xFF
		return di + dxt1BlockBytes
	}

	minR, minG, minB, maxR, maxG, maxB := blockMinMax(r, g, b, a, hasTransparent)
	var c0, c1 uint16
	if hasTransparent {
		// 3-color mode (c0 <= c1): color3 is transparent.
		c0 = pack565(minR, minG, minB)
		c1 = pack565(maxR, maxG, maxB)
	} else {
		// 4-color mode (c0 > c1 when there is a range).
		c0 = pack565(maxR, maxG, maxB)
		c1 = pack565(minR, minG, minB)
	}

	r0, g0, b0 := unpack565(c0)
	r1, g1, b1 := unpack565(c1)
	var pal [4][3]uint8
	pal[0] = [3]uint8{r0, g0, b0}
	pal[1] = [3]uint8{r1, g1, b1}
	colors := 4
	if !hasTransparent {
		pal[2] = [3]uint8{(2*r0 + r1 + 1) / 3, (2*g0 + g1 + 1) / 3, (2*b0 + b1 + 1) / 3}
		pal[3] = [3]uint8{(r0 + 2*r1 + 1) / 3, (g0 + 2*g1 + 1) / 3, (b0 + 2*b1 + 1) / 3}
	} else {
		pal[2] = [3]uint8{(r0 + r1) / 2, (g0 + g1) / 2, (b0 + b1) / 2}
		pal[3] = [3]uint8{0, 0, 0} // transparent; never chosen for opaque texels
		colors = 3
	}

	var bits uint32
	for i := 0; i < 16; i++ {
		var ci uint8
		if hasTransparent && a[i] < 128 {
			ci = 3
		} else {
			ci = nearestColor(r[i], g[i], b[i], pal, colors)
		}
		bits |= uint32(ci) << (2 * i)
	}

	out[di] = byte(c0)
	out[di+1] = byte(c0 >> 8)
	out[di+2] = byte(c1)
	out[di+3] = byte(c1 >> 8)
	out[di+4] = byte(bits)
	out[di+5] = byte(bits >> 8)
	out[di+6] = byte(bits >> 16)
	out[di+7] = byte(bits >> 24)
	return di + dxt1BlockBytes
}

func encodeDXT5Block(out []byte, di int, img *image.RGBA, x0, y0 int) int {
	var r, g, b, a [16]uint8
	readBlock(img, x0, y0, &r, &g, &b, &a)

	// Alpha endpoints: a0 = max, a1 = min.
	aMin, aMax := uint8(255), uint8(0)
	for i := 0; i < 16; i++ {
		if a[i] < aMin {
			aMin = a[i]
		}
		if a[i] > aMax {
			aMax = a[i]
		}
	}
	a0, a1 := aMax, aMin

	var ramp [8]uint8
	if a0 > a1 {
		for i := 0; i < 8; i++ {
			ramp[i] = uint8((int(a0)*(7-i) + int(a1)*i) / 7)
		}
	} else {
		for i := 0; i < 6; i++ {
			ramp[i] = uint8((int(a0)*(5-i) + int(a1)*i) / 5)
		}
		ramp[6], ramp[7] = 0, 255
	}

	var alphaBits uint64
	for i := 0; i < 16; i++ {
		best, bestDist := uint8(0), int32(1<<30)
		for j := 0; j < 8; j++ {
			d := int32(a[i]) - int32(ramp[j])
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				bestDist, best = d, uint8(j)
			}
		}
		alphaBits |= uint64(best) << (3 * i)
	}

	// Color endpoints (4-color mode; alpha is separate).
	minR, minG, minB, maxR, maxG, maxB := blockMinMax(r, g, b, a, false)
	c0 := pack565(maxR, maxG, maxB)
	c1 := pack565(minR, minG, minB)
	r0, g0, b0 := unpack565(c0)
	r1, g1, b1 := unpack565(c1)
	pal := [4][3]uint8{
		{r0, g0, b0},
		{r1, g1, b1},
		{(2*r0 + r1 + 1) / 3, (2*g0 + g1 + 1) / 3, (2*b0 + b1 + 1) / 3},
		{(r0 + 2*r1 + 1) / 3, (g0 + 2*g1 + 1) / 3, (b0 + 2*b1 + 1) / 3},
	}
	var colorBits uint32
	for i := 0; i < 16; i++ {
		colorBits |= uint32(nearestColor(r[i], g[i], b[i], pal, 4)) << (2 * i)
	}

	out[di] = byte(a0)
	out[di+1] = byte(a1)
	for j := 0; j < 6; j++ {
		out[di+2+j] = byte(alphaBits >> (8 * j))
	}
	out[di+8] = byte(c0)
	out[di+9] = byte(c0 >> 8)
	out[di+10] = byte(c1)
	out[di+11] = byte(c1 >> 8)
	out[di+12] = byte(colorBits)
	out[di+13] = byte(colorBits >> 8)
	out[di+14] = byte(colorBits >> 16)
	out[di+15] = byte(colorBits >> 24)
	return di + dxt5BlockBytes
}

func readBlock(img *image.RGBA, x0, y0 int, r, g, b, a *[16]uint8) {
	idx := 0
	for y := 0; y < dxtBlockDim; y++ {
		p := (y0+y)*img.Stride + x0*4
		for x := 0; x < dxtBlockDim; x++ {
			r[idx], g[idx], b[idx], a[idx] = img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3]
			idx++
			p += 4
		}
	}
}

// blockMinMax returns the min/max RGB over the texels considered (opaque texels
// only when skipTransparent is true).
func blockMinMax(r, g, b, a [16]uint8, skipTransparent bool) (minR, minG, minB, maxR, maxG, maxB uint8) {
	minR, minG, minB = 255, 255, 255
	for i := 0; i < 16; i++ {
		if skipTransparent && a[i] < 128 {
			continue
		}
		if r[i] < minR {
			minR = r[i]
		}
		if g[i] < minG {
			minG = g[i]
		}
		if b[i] < minB {
			minB = b[i]
		}
		if r[i] > maxR {
			maxR = r[i]
		}
		if g[i] > maxG {
			maxG = g[i]
		}
		if b[i] > maxB {
			maxB = b[i]
		}
	}
	return
}

func pack565(r, g, b uint8) uint16 {
	return uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3)
}

func unpack565(c uint16) (uint8, uint8, uint8) {
	r := uint8((c>>11)&0x1F) << 3
	r |= r >> 5
	g := uint8((c>>5)&0x3F) << 2
	g |= g >> 6
	b := uint8(c&0x1F) << 3
	b |= b >> 5
	return r, g, b
}

func nearestColor(r, g, b uint8, pal [4][3]uint8, colors int) uint8 {
	best := uint8(0)
	bestDist := int32(1 << 30)
	for i := 0; i < colors; i++ {
		dr := int32(r) - int32(pal[i][0])
		dg := int32(g) - int32(pal[i][1])
		db := int32(b) - int32(pal[i][2])
		d := dr*dr + dg*dg + db*db
		if d < bestDist {
			bestDist, best = d, uint8(i)
		}
	}
	return best
}

// Texture-compression modes for DecoderPool.SetTextureCompression.
const (
	CompressOff  = 0 // raw RGBA (the default)
	CompressDXT5 = 1 // DXT5/BC3, 8 bpp, smooth alpha
	CompressDXT1 = 2 // DXT1/BC1, 4 bpp, 1-bit alpha
)

// compress encodes Frames to the given DXT format and releases the raw RGBA
// (returning pooled buffers to the pool immediately). No-op when the canvas is
// not a multiple of 4 (DXT blocks are 4x4), when there are no frames, or when
// the format is 0. Call exactly once, in the decode pool, before the Decoded
// is handed to the render thread.
func (d *Decoded) compress(format uint32) {
	if format == 0 || len(d.Frames) == 0 || d.Width%dxtBlockDim != 0 || d.Height%dxtBlockDim != 0 {
		return
	}
	comp := make([][]byte, len(d.Frames))
	for i, f := range d.Frames {
		if f == nil {
			continue
		}
		if format == DXT1FourCC {
			comp[i] = EncodeDXT1(f)
		} else {
			comp[i] = EncodeDXT5(f)
		}
	}
	d.Release() // returns raw RGBA buffers; clears Frames + Compressed
	d.Compressed = comp
	d.DXTFormat = format
}
