package render

import (
	"image"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestApplyVariant pins the per-pixel sprite-style transforms on an ABGR8888 buffer
// (R,G,B,A per pixel): invert negates RGB and keeps alpha; grayscale uses Rec.601
// luma and keeps alpha; none is a no-op. Pure maths — no SDL.
func TestApplyVariant(t *testing.T) {
	// Invert: RGB negated, alpha untouched (incl. a semi-transparent pixel).
	inv := []byte{10, 20, 30, 255, 200, 100, 50, 128}
	applyVariant(inv, 2, 1, uint8(courtroom.VariantInvert))
	if want := []byte{245, 235, 225, 255, 55, 155, 205, 128}; !equalBytes(inv, want) {
		t.Errorf("invert = %v, want %v", inv, want)
	}

	// Grayscale: each pixel → its luma, alpha kept. Red 255 → 76; mix → 140.
	gray := []byte{255, 0, 0, 255, 100, 150, 200, 64}
	applyVariant(gray, 2, 1, uint8(courtroom.VariantGrayscale))
	if want := []byte{76, 76, 76, 255, 140, 140, 140, 64}; !equalBytes(gray, want) {
		t.Errorf("grayscale = %v, want %v", gray, want)
	}

	// None: untouched.
	none := []byte{10, 20, 30, 255}
	applyVariant(none, 1, 1, uint8(courtroom.VariantNone))
	if want := []byte{10, 20, 30, 255}; !equalBytes(none, want) {
		t.Errorf("none changed the buffer: %v", none)
	}
}

// TestApplyVariantRestyles pins a few of the "10 more restyles" per-pixel transforms (#M5+):
// redscale keeps luma in the red channel, threshold is 1-bit, infrared rotates channels — and
// alpha is always preserved.
func TestApplyVariantRestyles(t *testing.T) {
	red := []byte{100, 150, 50, 200}
	applyVariant(red, 1, 1, uint8(courtroom.VariantRedscale)) // luma 123 → red channel only
	if want := []byte{123, 0, 0, 200}; !equalBytes(red, want) {
		t.Errorf("redscale = %v, want %v", red, want)
	}
	th := []byte{100, 150, 50, 200}
	applyVariant(th, 1, 1, uint8(courtroom.VariantThreshold)) // luma 123 ≤ 127 → black, alpha kept
	if want := []byte{0, 0, 0, 200}; !equalBytes(th, want) {
		t.Errorf("threshold = %v, want %v", th, want)
	}
	ir := []byte{100, 150, 50, 200}
	applyVariant(ir, 1, 1, uint8(courtroom.VariantInfrared)) // channel rotate R<-G<-B, alpha kept
	if want := []byte{150, 50, 100, 200}; !equalBytes(ir, want) {
		t.Errorf("infrared = %v, want %v", ir, want)
	}
}

// TestApplyPixelArt pins the #77 mosaic: a block is averaged ALPHA-WEIGHTED (so a
// transparent neighbour can't drag the colour toward black), palette-quantised, and
// written back uniformly; the block's alpha becomes the mean. Here w<block, so both
// pixels are one cell: colour = the opaque pixel's (200,100,50) quantised to the
// 6-level palette (204,102,51); alpha = mean(255,0) = 127.
func TestApplyPixelArt(t *testing.T) {
	pix := []byte{200, 100, 50, 255, 0, 0, 0, 0}
	applyVariant(pix, 2, 1, uint8(courtroom.VariantPixelArt))
	if want := []byte{204, 102, 51, 127, 204, 102, 51, 127}; !equalBytes(pix, want) {
		t.Errorf("pixel art = %v, want %v", pix, want)
	}
}

// TestVariantPageInverts is the end-to-end proof: upload a known 2×1 base, build its
// invert variant, and read the variant's pixels back — confirming the render-target
// readback yields STRAIGHT (non-premultiplied) pixels (the NONE-blend copy), the
// transform is applied, and a repeat call is cached. Skips if the headless renderer
// has no render-target support.
func TestVariantPageInverts(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	base := &assets.Decoded{
		Width: 2, Height: 1,
		Frames: []*image.RGBA{{
			Pix:    []byte{10, 20, 30, 255, 200, 100, 50, 128}, // one opaque + one semi-transparent pixel
			Stride: 8,
			Rect:   image.Rect(0, 0, 2, 1),
		}},
	}
	if err := store.Upload("base/x", base); err != nil {
		t.Fatalf("upload: %v", err)
	}

	v, ok := store.VariantPage("base/x", courtroom.VariantInvert)
	if !ok {
		t.Skip("render targets unavailable on this headless renderer")
	}
	if len(v.Frames) != 1 {
		t.Fatalf("variant frames = %d, want 1", len(v.Frames))
	}
	got, err := store.readbackFrame(v.Frames[0], 2, 1)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if want := []byte{245, 235, 225, 255, 55, 155, 205, 128}; !equalBytes(got, want) {
		t.Errorf("inverted variant pixels = %v, want %v (RGB negated, alpha kept)", got, want)
	}
	if v2, _ := store.VariantPage("base/x", courtroom.VariantInvert); v2 != v {
		t.Error("variant page must be cached (same pointer on a repeat call)")
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
