//go:build cgo && !nocgo_webp

package webpenc

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func solidRGBA(w, h int, c color.RGBA) *image.RGBA {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(im, im.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	return im
}

// TestEncodeAnimatedWebP is the definition-of-done for the WebP export: feed a
// few distinct RGBA frames and assert the bytes are a valid, *animated* WebP
// (RIFF/WEBP container with the ANIM + ANMF chunks). That proves the libwebpmux
// WebPAnimEncoder binding produces a real animation, so the capture side just has
// to feed frames.
func TestEncodeAnimatedWebP(t *testing.T) {
	const w, h = 24, 18
	enc, err := New(w, h, 75, 80, true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer enc.Close()

	frames := []color.RGBA{
		{R: 220, G: 30, B: 30, A: 255},
		{R: 30, G: 220, B: 30, A: 255},
		{R: 30, G: 30, B: 220, A: 255},
	}
	for i, c := range frames {
		if err := enc.AddFrame(solidRGBA(w, h, c)); err != nil {
			t.Fatalf("AddFrame %d: %v", i, err)
		}
	}
	if enc.Frames() != len(frames) {
		t.Fatalf("Frames() = %d, want %d", enc.Frames(), len(frames))
	}

	data, err := enc.Assemble()
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(data) < 32 {
		t.Fatalf("encoded WebP is only %d bytes — implausibly small", len(data))
	}
	if !bytes.HasPrefix(data, []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
		t.Fatalf("output is not a RIFF/WEBP container: % x", data[:12])
	}
	if !bytes.Contains(data, []byte("ANIM")) {
		t.Error("no ANIM chunk — output is not an animated WebP")
	}
	if !bytes.Contains(data, []byte("ANMF")) {
		t.Error("no ANMF frame chunk — the animation has no frames")
	}
}

// TestSizeMismatchRejected guards the capture contract: a frame whose dimensions
// don't match the encoder must be rejected, not silently corrupt the stream.
func TestSizeMismatchRejected(t *testing.T) {
	enc, err := New(16, 16, 75, 80, true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer enc.Close()
	if err := enc.AddFrame(solidRGBA(16, 16, color.RGBA{A: 255})); err != nil {
		t.Fatalf("matching frame rejected: %v", err)
	}
	if err := enc.AddFrame(solidRGBA(20, 16, color.RGBA{A: 255})); err == nil {
		t.Error("mismatched frame size accepted, want error")
	}
}
