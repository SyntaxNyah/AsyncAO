//go:build cgo && !nocgo_webp

package assets

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "test", "fixtures", name))
	if err != nil {
		t.Skipf("fixture %s unavailable: %v", name, err)
	}
	return data
}

func TestDecodeWebPStaticFixture(t *testing.T) {
	data := fixture(t, "sprite_256x192.webp")
	if got := Sniff(data); got != FormatWebP {
		t.Fatalf("Sniff = %v, want static webp", got)
	}
	d, err := DecodeImage(data, true)
	if err != nil {
		t.Fatalf("decode static webp: %v", err)
	}
	defer d.Release()
	if d.Animated || len(d.Frames) != 1 || d.Width != 256 || d.Height != 192 {
		t.Errorf("static webp: animated=%v frames=%d %dx%d", d.Animated, len(d.Frames), d.Width, d.Height)
	}
	// The fixture is an opaque gradient; alpha must be 255.
	if a := d.Frames[0].Pix[3]; a != 0xFF {
		t.Errorf("alpha = %d, want 255", a)
	}
}

func TestDecodeWebPAnimatedFixture(t *testing.T) {
	data := fixture(t, "sprite_anim_256x192.webp")
	if got := Sniff(data); got != FormatWebPAnim {
		t.Fatalf("Sniff = %v, want animated webp (VP8X ANIM flag)", got)
	}
	d, err := DecodeImage(data, true)
	if err != nil {
		t.Fatalf("decode animated webp: %v", err)
	}
	defer d.Release()
	if !d.Animated {
		t.Error("Animated flag not set")
	}
	if len(d.Frames) != 3 || len(d.Delays) != 3 {
		t.Fatalf("frames=%d delays=%d, want 3/3", len(d.Frames), len(d.Delays))
	}
	for i, delay := range d.Delays {
		if delay != 60*time.Millisecond {
			t.Errorf("frame %d delay = %v, want 60ms", i, delay)
		}
	}
	// Frames must differ (each source frame had a different gradient).
	if d.Frames[0].Pix[0] == d.Frames[1].Pix[0] && d.Frames[0].Pix[2] == d.Frames[1].Pix[2] {
		t.Error("animated frames identical; demux composition broken")
	}
}

func TestDecodeWebPAnimatedFirstFrameOnly(t *testing.T) {
	data := fixture(t, "sprite_anim_256x192.webp")
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

// TestProgressiveAnimatedDecode pins the two-phase delivery: an animated
// payload with PlayAnimations on yields a Partial single-frame result
// first, then the full set; statics deliver exactly once.
func TestProgressiveAnimatedDecode(t *testing.T) {
	pool := NewDecoderPool(1)
	defer pool.Close()

	type result struct {
		frames  int
		partial bool
	}
	deliver := func(data []byte) []result {
		out := make(chan result, 4)
		done := make(chan struct{})
		pool.Submit(DecodeRequest{
			URL: "x", Data: data, Type: AssetTypeCharSprite, PlayAnimations: true,
			OnDone: func(_ string, d *Decoded, err error) {
				if err != nil {
					t.Errorf("decode: %v", err)
					close(done)
					return
				}
				out <- result{frames: len(d.Frames), partial: d.Partial}
				if !d.Partial {
					close(done) // the full set is always the last delivery
				}
				d.Release()
			},
		})
		<-done
		close(out)
		var rs []result
		for r := range out {
			rs = append(rs, r)
		}
		return rs
	}

	anim := deliver(fixture(t, "sprite_anim_256x192.webp"))
	if len(anim) != 2 || !anim[0].partial || anim[0].frames != 1 ||
		anim[1].partial || anim[1].frames != 3 {
		t.Errorf("animated deliveries = %+v, want partial[1] then full[3]", anim)
	}

	static := deliver(fixture(t, "sprite_256x192.webp"))
	if len(static) != 1 || static[0].partial {
		t.Errorf("static deliveries = %+v, want one non-partial", static)
	}
}

// BenchmarkDecodeWebP_256x192 is the §15 gate: < 3 ms per static decode.
func BenchmarkDecodeWebP_256x192(b *testing.B) {
	data := fixture(b, "sprite_256x192.webp")
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
