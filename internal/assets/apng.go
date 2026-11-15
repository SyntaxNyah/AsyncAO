package assets

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"time"
)

// APNG chunk layouts (https://wiki.mozilla.org/APNG_Specification).
const (
	pngIHDRLen     = 13
	apngFCTLLen    = 26
	apngSeqLen     = 4
	apngDelayUnits = 100 // a zero denominator means hundredths of a second

	apngDisposeBackground = 1
	apngDisposePrevious   = 2
	apngBlendSource       = 0
)

var (
	errAPNGDamaged = errors.New("assets: apng chunk runs past the end of the file")
	errAPNGFrame   = errors.New("assets: apng frame lies outside the canvas")
	pngIEND        = []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xAE, 0x42, 0x60, 0x82}
)

// apngFrame is one frame's control fields and its compressed data, still inside the file's bytes.
type apngFrame struct {
	rect               image.Rectangle
	delayNum, delayDen uint16
	dispose, blend     byte
	data               [][]byte
}

// apngFile is an APNG read as far as its chunks: frames decode one at a time from it.
type apngFile struct {
	width, height int
	ihdr          []byte
	// palette holds the PLTE and tRNS chunks, whole, which every frame's stream repeats.
	palette [][]byte
	frames  []apngFrame
	// still is the default image when no fcTL comes before IDAT: shown by viewers without APNG, not part of the animation.
	still *apngFrame
}

// parseAPNG walks the chunks once, keeping offsets into data, without inflating anything.
func parseAPNG(data []byte) (*apngFile, error) {
	f := &apngFile{}
	var current *apngFrame
	for offset := pngSigLen; offset+pngChunkHdrLen <= len(data); {
		length := int(binary.BigEndian.Uint32(data[offset:]))
		body := offset + pngChunkHdrLen
		next := body + length + pngChunkCRCLen
		if length < 0 || next <= offset || next > len(data) {
			return nil, errAPNGDamaged
		}
		chunk, content := string(data[offset+4:body]), data[body:body+length]
		switch chunk {
		case "IHDR":
			if length != pngIHDRLen {
				return nil, errAPNGDamaged
			}
			f.ihdr = content
			f.width, f.height = int(binary.BigEndian.Uint32(content)), int(binary.BigEndian.Uint32(content[4:]))
		case "PLTE", "tRNS":
			f.palette = append(f.palette, data[offset:next])
		case "fcTL":
			fr, err := f.control(content)
			if err != nil {
				return nil, err
			}
			f.frames = append(f.frames, fr)
			current = &f.frames[len(f.frames)-1]
		case "IDAT":
			if current == nil {
				f.still = &apngFrame{rect: image.Rect(0, 0, f.width, f.height)}
				current = f.still
			}
			current.data = append(current.data, content)
		case "fdAT":
			if current == nil || current == f.still || length < apngSeqLen {
				return nil, errAPNGDamaged
			}
			current.data = append(current.data, content[apngSeqLen:])
		case "IEND":
			return f, nil
		}
		offset = next
	}
	return f, nil
}

// control reads an fcTL chunk, refusing a frame that doesn't fit the canvas.
func (f *apngFile) control(c []byte) (apngFrame, error) {
	if len(c) != apngFCTLLen || f.ihdr == nil {
		return apngFrame{}, errAPNGDamaged
	}
	be := binary.BigEndian
	w, h := int64(be.Uint32(c[4:])), int64(be.Uint32(c[8:]))
	x, y := int64(be.Uint32(c[12:])), int64(be.Uint32(c[16:]))
	if w <= 0 || h <= 0 || x+w > int64(f.width) || y+h > int64(f.height) {
		return apngFrame{}, errAPNGFrame
	}
	return apngFrame{
		rect:     image.Rect(int(x), int(y), int(x+w), int(y+h)),
		delayNum: be.Uint16(c[20:]), delayDen: be.Uint16(c[22:]),
		dispose: c[24], blend: c[25],
	}, nil
}

// decode inflates one frame by handing image/png a stream of its own: IHDR at the frame's size, the palette, its data.
func (f *apngFile) decode(fr *apngFrame) (image.Image, error) {
	size := len(pngSignature) + 3*pngChunkHdrLen + pngIHDRLen + len(pngIEND)
	for _, c := range f.palette {
		size += len(c)
	}
	for _, d := range fr.data {
		size += pngChunkHdrLen + len(d) + pngChunkCRCLen
	}
	var buf bytes.Buffer
	buf.Grow(size)
	buf.Write(pngSignature)
	ihdr := make([]byte, pngIHDRLen)
	copy(ihdr, f.ihdr)
	binary.BigEndian.PutUint32(ihdr, uint32(fr.rect.Dx()))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(fr.rect.Dy()))
	writeChunk(&buf, "IHDR", ihdr)
	for _, c := range f.palette {
		buf.Write(c)
	}
	for _, d := range fr.data {
		writeChunk(&buf, "IDAT", d)
	}
	buf.Write(pngIEND)
	return png.Decode(&buf)
}

func writeChunk(buf *bytes.Buffer, kind string, data []byte) {
	var head [pngChunkHdrLen]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(data)))
	copy(head[4:], kind)
	buf.Write(head[:])
	buf.Write(data)
	crc := crc32.NewIEEE()
	crc.Write(head[4:])
	crc.Write(data)
	var sum [pngChunkCRCLen]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	buf.Write(sum[:])
}

func (fr *apngFrame) delay() time.Duration {
	den := fr.delayDen
	if den == 0 {
		den = apngDelayUnits
	}
	if d := time.Duration(fr.delayNum) * time.Second / time.Duration(den); d > 0 {
		return d
	}
	return defaultZeroFrameDelay
}
