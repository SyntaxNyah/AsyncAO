package assets

import (
	"encoding/binary"
	"image"
	"testing"
)

// decodeDXT1 / decodeDXT5 are minimal block decoders used only to round-trip
// the encoders in tests. They use the standard S3TC interpolation (truncating
// thirds), so the tests compare within a small tolerance to absorb the
// encoder's rounded-third interpolation and 5/6-bit color quantization.
func decodeDXT1(in []byte, w, h int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	di := 0
	for y := 0; y < h; y += 4 {
		for x := 0; x < w; x += 4 {
			c0 := binary.LittleEndian.Uint16(in[di:])
			c1 := binary.LittleEndian.Uint16(in[di+2:])
			r0, g0, b0 := unpack565(c0)
			r1, g1, b1 := unpack565(c1)
			var pal [4][4]uint8
			pal[0] = [4]uint8{r0, g0, b0, 255}
			pal[1] = [4]uint8{r1, g1, b1, 255}
			if c0 > c1 {
				pal[2] = [4]uint8{(2*r0 + r1) / 3, (2*g0 + g1) / 3, (2*b0 + b1) / 3, 255}
				pal[3] = [4]uint8{(r0 + 2*r1) / 3, (g0 + 2*g1) / 3, (b0 + 2*b1) / 3, 255}
			} else {
				pal[2] = [4]uint8{(r0 + r1) / 2, (g0 + g1) / 2, (b0 + b1) / 2, 255}
				pal[3] = [4]uint8{0, 0, 0, 0}
			}
			bits := binary.LittleEndian.Uint32(in[di+4:])
			for i := 0; i < 16; i++ {
				ci := (bits >> (2 * i)) & 3
				p := (y+i/4)*out.Stride + (x+i%4)*4
				out.Pix[p], out.Pix[p+1], out.Pix[p+2], out.Pix[p+3] = pal[ci][0], pal[ci][1], pal[ci][2], pal[ci][3]
			}
			di += dxt1BlockBytes
		}
	}
	return out
}

func decodeDXT5(in []byte, w, h int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	di := 0
	for y := 0; y < h; y += 4 {
		for x := 0; x < w; x += 4 {
			a0, a1 := in[di], in[di+1]
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
			for j := 0; j < 6; j++ {
				alphaBits |= uint64(in[di+2+j]) << (8 * j)
			}
			c0 := binary.LittleEndian.Uint16(in[di+8:])
			c1 := binary.LittleEndian.Uint16(in[di+10:])
			r0, g0, b0 := unpack565(c0)
			r1, g1, b1 := unpack565(c1)
			var pal [4][3]uint8
			pal[0] = [3]uint8{r0, g0, b0}
			pal[1] = [3]uint8{r1, g1, b1}
			pal[2] = [3]uint8{(2*r0 + r1) / 3, (2*g0 + g1) / 3, (2*b0 + b1) / 3}
			pal[3] = [3]uint8{(r0 + 2*r1) / 3, (g0 + 2*g1) / 3, (b0 + 2*b1) / 3}
			bits := binary.LittleEndian.Uint32(in[di+12:])
			for i := 0; i < 16; i++ {
				ai := (alphaBits >> (3 * i)) & 7
				ci := (bits >> (2 * i)) & 3
				p := (y+i/4)*out.Stride + (x+i%4)*4
				out.Pix[p], out.Pix[p+1], out.Pix[p+2], out.Pix[p+3] = pal[ci][0], pal[ci][1], pal[ci][2], ramp[ai]
			}
			di += dxt5BlockBytes
		}
	}
	return out
}

func closeByte(got, want uint8, tol int) bool {
	d := int(got) - int(want)
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestDXTEncodeSize(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	if got := len(EncodeDXT1(img)); got != (64/4)*(64/4)*8 {
		t.Fatalf("DXT1 size = %d, want %d", got, (64/4)*(64/4)*8)
	}
	if got := len(EncodeDXT5(img)); got != (64/4)*(64/4)*16 {
		t.Fatalf("DXT5 size = %d, want %d", got, (64/4)*(64/4)*16)
	}
	if got := CompressedBlockBytes(DXT5FourCC, 64, 64); got != (64/4)*(64/4)*16 {
		t.Fatalf("CompressedBlockBytes = %d", got)
	}
}

func TestDXT1SolidOpaqueRoundTrip(t *testing.T) {
	const w, h = 8, 8
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = 200, 40, 120, 255
	}
	dec := decodeDXT1(EncodeDXT1(img), w, h)
	for i := 0; i < w*h; i++ {
		if !closeByte(dec.Pix[i*4], 200, 8) || !closeByte(dec.Pix[i*4+1], 40, 8) || !closeByte(dec.Pix[i*4+2], 120, 8) || dec.Pix[i*4+3] != 255 {
			t.Fatalf("solid texel %d decoded to %v, want ~(200,40,120,255)", i, dec.Pix[i*4:i*4+4])
		}
	}
}

func TestDXT1TransparentRoundTrip(t *testing.T) {
	const w, h = 8, 8
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := (y*w + x) * 4
			if x < 4 { // left half opaque red, right half transparent
				img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = 255, 0, 0, 255
			} else {
				img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = 0, 0, 0, 0
			}
		}
	}
	dec := decodeDXT1(EncodeDXT1(img), w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := (y*w + x) * 4
			if x < 4 {
				if dec.Pix[p+3] != 255 {
					t.Fatalf("opaque texel (%d,%d) alpha = %d, want 255", x, y, dec.Pix[p+3])
				}
			} else {
				if dec.Pix[p+3] != 0 {
					t.Fatalf("transparent texel (%d,%d) alpha = %d, want 0", x, y, dec.Pix[p+3])
				}
			}
		}
	}
}

func TestDXT5AlphaRoundTrip(t *testing.T) {
	const w, h = 8, 8
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2] = 100, 150, 200
		img.Pix[i*4+3] = 200 // constant alpha: round-trips exactly
	}
	dec := decodeDXT5(EncodeDXT5(img), w, h)
	for i := 0; i < w*h; i++ {
		if !closeByte(dec.Pix[i*4+3], 200, 4) {
			t.Fatalf("alpha texel %d = %d, want ~200", i, dec.Pix[i*4+3])
		}
	}
}

func TestDecodedCompress(t *testing.T) {
	const w, h = 16, 16
	d := &Decoded{Width: w, Height: h}
	for i := 0; i < 3; i++ {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for p := 3; p < len(img.Pix); p += 4 {
			img.Pix[p] = 0xFF
		}
		d.Frames = append(d.Frames, img)
	}
	d.compress(DXT5FourCC)

	if d.DXTFormat != DXT5FourCC {
		t.Fatalf("DXTFormat = %#x, want DXT5", d.DXTFormat)
	}
	if len(d.Compressed) != 3 {
		t.Fatalf("Compressed len = %d, want 3", len(d.Compressed))
	}
	if d.Frames != nil {
		t.Fatal("Frames not released after compress")
	}
	if want := int64(3 * CompressedBlockBytes(DXT5FourCC, w, h)); d.PixelBytes() != want {
		t.Fatalf("PixelBytes = %d, want %d (compressed size)", d.PixelBytes(), want)
	}
}

func TestDecodedCompressSkipsUnaligned(t *testing.T) {
	d := &Decoded{Width: 10, Height: 10}
	d.Frames = append(d.Frames, image.NewRGBA(image.Rect(0, 0, 10, 10)))
	d.compress(DXT5FourCC)

	if d.DXTFormat != 0 {
		t.Fatal("unaligned canvas must not compress")
	}
	if d.Frames == nil {
		t.Fatal("unaligned canvas must keep raw frames")
	}
}
