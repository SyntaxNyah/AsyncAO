package assets

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"github.com/kettek/apng"
)

const (
	apngTestSide   = 8
	apngTestFrames = 2
	apngFCTLXAt    = 12 // fcTL's x_offset, counted from the chunk's data
)

var apngStillRed = color.RGBA{R: 0xFF, A: 0xFF}

func TestAPNGLeavesTheDefaultStillOutOfTheAnimation(t *testing.T) {
	still := image.NewRGBA(image.Rect(0, 0, apngTestSide, apngTestSide))
	for p := 0; p < len(still.Pix); p += rgbaBytesPerPixel {
		copy(still.Pix[p:], []byte{apngStillRed.R, apngStillRed.G, apngStillRed.B, apngStillRed.A})
	}
	a := apng.APNG{Frames: []apng.Frame{{Image: still, IsDefault: true}}}
	for range apngTestFrames {
		a.Frames = append(a.Frames, apng.Frame{Image: image.NewRGBA(still.Rect), DelayNumerator: 1})
	}
	var buf bytes.Buffer
	if err := apng.Encode(&buf, a); err != nil {
		t.Fatal(err)
	}
	d, err := DecodeImage(buf.Bytes(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Frames) != apngTestFrames {
		t.Fatalf("%d frames, want the %d animation frames", len(d.Frames), apngTestFrames)
	}
	if got := d.Frames[0].RGBAAt(0, 0); got == apngStillRed {
		t.Fatal("the default still image played as the first frame")
	}
}

func TestAPNGRefusesAFrameOutsideTheCanvas(t *testing.T) {
	data := encodeAPNG(t, apngTestFrames)
	at := bytes.Index(data, []byte("fcTL")) + len("fcTL")
	binary.BigEndian.PutUint32(data[at+apngFCTLXAt:], apngTestSide)
	if _, err := DecodeImage(data, true); err == nil {
		t.Fatal("a frame past the canvas edge decoded")
	}
}

func TestAPNGSurvivesTruncation(t *testing.T) {
	data := encodeAPNG(t, apngTestFrames)
	for n := range len(data) {
		if d, err := DecodeImage(data[:n], true); err == nil && len(d.Frames) == 0 {
			t.Fatalf("%d bytes decoded to no frames without an error", n)
		}
	}
}
