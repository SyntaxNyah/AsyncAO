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

// TestStreamAnimatedAVIFDelivery pins the AVIF streaming decoder: a 2-frame
// animated AVIF streams (streamed=true, total=2) and emits exactly one append
// chunk — frame 1 (frame 0 is the establishing chunk from the progressive
// phase) — with the right metadata.
func TestStreamAnimatedAVIFDelivery(t *testing.T) {
	data := fixture(t, "sprite_anim_64x48.avif")
	type chunk struct {
		offset  int
		partial bool
		source  int
	}
	var chunks []chunk
	streamed, total, err := decodeAVIFAnimStream(data, 0, func(d *Decoded) {
		chunks = append(chunks, chunk{offset: d.FrameOffset, partial: d.Partial, source: d.SourceFrames})
		d.Release()
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !streamed || total != 2 {
		t.Fatalf("streamed=%v total=%d, want true/2", streamed, total)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %+v, want exactly 1 (frame 1)", chunks)
	}
	if chunks[0].offset != 1 || !chunks[0].partial || chunks[0].source != 2 {
		t.Fatalf("frame-1 chunk = %+v, want offset=1 partial source=2", chunks[0])
	}
}

// BenchmarkDecodeAVIFAnim_64x48_2f is the §15 animated-AVIF decode gate: it
// times the cold decode of a 2-frame animated AVIF. It doubles as the
// regression probe for the maxThreads=NUMCPU threading change (a decode that
// regresses to serial would show up here, though a larger fixture is the
// real cold-load stand-in).
func BenchmarkDecodeAVIFAnim_64x48_2f(b *testing.B) {
	data := fixture(b, "sprite_anim_64x48.avif")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, err := DecodeImage(data, true)
		if err != nil {
			b.Fatal(err)
		}
		d.Release()
	}
}
