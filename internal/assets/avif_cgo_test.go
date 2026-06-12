//go:build cgo && !nocgo_avif

package assets

import (
	"testing"
)

// Fixtures were generated with MSYS2's avifenc (see test/fixtures/):
//   avifenc -s 10 a.png sprite_64x48.avif
//   avifenc -s 10 --fps 10 a.png b.png sprite_anim_64x48.avif

func TestDecodeAVIFStaticFixture(t *testing.T) {
	data := fixture(t, "sprite_64x48.avif")
	if got := Sniff(data); got != FormatAVIF {
		t.Fatalf("Sniff = %v, want avif", got)
	}
	d, err := DecodeImage(data, true)
	if err != nil {
		t.Fatalf("decode static avif: %v", err)
	}
	defer d.Release()
	if d.Animated || len(d.Frames) != 1 || d.Width != 64 || d.Height != 48 {
		t.Errorf("static avif: animated=%v frames=%d %dx%d", d.Animated, len(d.Frames), d.Width, d.Height)
	}
	if a := d.Frames[0].Pix[3]; a != 0xFF {
		t.Errorf("alpha = %d, want 255 (opaque gradient source)", a)
	}
}

func TestDecodeAVIFAnimatedFixture(t *testing.T) {
	data := fixture(t, "sprite_anim_64x48.avif")
	if got := Sniff(data); got != FormatAVIFAnim {
		t.Fatalf("Sniff = %v, want animated avif (avis brand)", got)
	}
	d, err := DecodeImage(data, true)
	if err != nil {
		t.Fatalf("decode animated avif: %v", err)
	}
	defer d.Release()
	if !d.Animated || len(d.Frames) != 2 || len(d.Delays) != 2 {
		t.Fatalf("animated=%v frames=%d delays=%d, want true/2/2", d.Animated, len(d.Frames), len(d.Delays))
	}
	for i, delay := range d.Delays {
		if delay <= 0 {
			t.Errorf("frame %d delay = %v, want > 0", i, delay)
		}
	}
	// The two source gradients differ; so must the decoded frames.
	if d.Frames[0].Pix[0] == d.Frames[1].Pix[0] && d.Frames[0].Pix[1] == d.Frames[1].Pix[1] {
		t.Error("animated avif frames identical; sequence decode broken")
	}
}

func TestDecodeAVIFAnimatedFirstFrameOnly(t *testing.T) {
	data := fixture(t, "sprite_anim_64x48.avif")
	d, err := DecodeImage(data, false) // Play Animations off
	if err != nil {
		t.Fatal(err)
	}
	defer d.Release()
	if !d.Animated {
		t.Error("payload animation flag must survive first-frame-only decode")
	}
	if len(d.Frames) != 1 {
		t.Errorf("frames = %d, want 1", len(d.Frames))
	}
}
