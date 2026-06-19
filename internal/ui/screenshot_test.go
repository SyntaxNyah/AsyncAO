package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteScreenshotPNG pins the back-buffer → PNG encode: ABGR8888 bytes
// (image.RGBA order) round-trip to a valid PNG of the right size and colours,
// honouring a padded row stride.
func TestWriteScreenshotPNG(t *testing.T) {
	const w, h = 4, 3
	pitch := w*4 + 8 // deliberate row padding, to prove stride is honoured
	pix := make([]byte, pitch*h)
	off := 2*pitch + 1*4 // pixel (1,2)
	pix[off], pix[off+1], pix[off+2], pix[off+3] = 200, 0, 0, 255

	path := filepath.Join(t.TempDir(), "shot.png")
	if err := writeScreenshotPNG(path, pix, w, h, pitch); err != nil {
		t.Fatalf("writeScreenshotPNG: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
		t.Fatalf("dims = %dx%d, want %dx%d", b.Dx(), b.Dy(), w, h)
	}
	if r, g, bl, al := img.At(1, 2).RGBA(); r>>8 != 200 || g>>8 != 0 || bl>>8 != 0 || al>>8 != 255 {
		t.Errorf("pixel (1,2) = %d,%d,%d,%d, want 200,0,0,255", r>>8, g>>8, bl>>8, al>>8)
	}
}
