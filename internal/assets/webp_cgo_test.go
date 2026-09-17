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

// TestProgressiveAnimatedDecode pins the streaming delivery: an animated WebP
// with PlayAnimations on streams as an establishing frame-0 chunk, one append
// per remaining frame, then a finalize that clears Partial — the page grows
// frame-by-frame instead of being replaced by a full set. Statics deliver
// exactly once.
func TestProgressiveAnimatedDecode(t *testing.T) {
	pool := NewDecoderPool(1)
	defer pool.Close()

	type result struct {
		frames  int
		partial bool
		stream  bool
		offset  int
		source  int
	}
	deliver := func(data []byte) []result {
		out := make(chan result, 8)
		done := make(chan struct{})
		pool.Submit(DecodeRequest{
			URL: "x", Data: data, Type: AssetTypeCharSprite, PlayAnimations: true,
			OnDone: func(_ string, d *Decoded, err error) {
				if err != nil {
					t.Errorf("decode: %v", err)
					close(done)
					return
				}
				out <- result{frames: len(d.Frames), partial: d.Partial, stream: d.Stream, offset: d.FrameOffset, source: d.SourceFrames}
				if !d.Partial {
					close(done) // the finalize is always the last delivery
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
	if len(anim) != 4 {
		t.Fatalf("animated deliveries = %+v, want 4 (establish + 2 appends + finalize)", anim)
	}
	if anim[0].frames != 1 || !anim[0].partial || !anim[0].stream || anim[0].offset != 0 {
		t.Errorf("establishing chunk = %+v, want 1 frame partial stream offset=0", anim[0])
	}
	if anim[1].frames != 1 || !anim[1].partial || !anim[1].stream || anim[1].offset != 1 {
		t.Errorf("append 1 = %+v, want 1 frame partial stream offset=1", anim[1])
	}
	if anim[2].frames != 1 || !anim[2].partial || !anim[2].stream || anim[2].offset != 2 {
		t.Errorf("append 2 = %+v, want 1 frame partial stream offset=2", anim[2])
	}
	if anim[3].frames != 0 || anim[3].partial || !anim[3].stream || anim[3].source != 3 {
		t.Errorf("finalize = %+v, want 0 frames !partial stream source=3", anim[3])
	}

	static := deliver(fixture(t, "sprite_256x192.webp"))
	if len(static) != 1 || static[0].partial || static[0].stream {
		t.Errorf("static deliveries = %+v, want one non-partial non-stream", static)
	}
}

// TestPeekAnimatedDimsAndFitPrefix pins the resolution-consistency fix: the
// establishing frame-0 prefix is downscaled to the SAME budget-fit dimensions
// the stream's appends use, so the whole clip is one resolution (and the prefix
// doesn't pay a full-size downscale + upload).
func TestPeekAnimatedDimsAndFitPrefix(t *testing.T) {
	data := fixture(t, "sprite_anim_256x192.webp")

	w, h, n, ok := peekAnimatedDims(data)
	if !ok || w != 256 || h != 192 || n != 3 {
		t.Fatalf("peekAnimatedDims = (%d,%d,%d,%v), want (256,192,3,true)", w, h, n, ok)
	}

	first, err := DecodeImage(data, false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	// Native 256x192 fits the default 128 MiB budget: a no-op.
	if same := fitEstablishingPrefix(first, data, 0); same != first {
		t.Fatalf("fitEstablishingPrefix must be a no-op at native size")
	}

	// Force the budget below the native 3-frame payload (3×256×192×4 ≈ 0.56 MiB)
	// so the prefix must shrink to fit.
	SetAnimatedDecodedAssetBytes(512 << 10)
	defer SetAnimatedDecodedAssetBytes(0) // reset to the default

	shrunk := fitEstablishingPrefix(first, data, 0)
	defer shrunk.Release()
	if shrunk.Width >= 256 || shrunk.Height >= 192 {
		t.Fatalf("fitEstablishingPrefix did not downscale under a 512 KiB budget: %dx%d", shrunk.Width, shrunk.Height)
	}
	if shrunk.Width <= 0 || shrunk.Height <= 0 {
		t.Fatalf("fitEstablishingPrefix produced a degenerate canvas: %dx%d", shrunk.Width, shrunk.Height)
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

// BenchmarkDecodeWebPAnim_256x192_3f is the §15 animated-decode gate: it times
// the COLD decode of a small 3-frame animated WebP (every authored frame,
// composed then copied out). This is the unit-level probe for the animation
// cold-load cost — the real-world number needs a larger 60–150 frame fixture.
func BenchmarkDecodeWebPAnim_256x192_3f(b *testing.B) {
	data := fixture(b, "sprite_anim_256x192.webp")
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
