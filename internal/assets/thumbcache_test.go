package assets

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/webpenc"
)

// thumbEncoderAvailable reports whether this build carries the CGO webp encoder
// (the fallback build no-ops thumbnail stores by design).
func thumbEncoderAvailable() bool {
	enc, err := webpenc.New(2, 2, 20, 100, false)
	if err != nil {
		return false
	}
	enc.Close()
	return true
}

// thumbFixture is a 200×200 white sprite — tall enough that the stored thumb
// must come back downscaled to the default height.
func thumbFixture() *Decoded {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for p := range img.Pix {
		img.Pix[p] = 0xFF
	}
	return &Decoded{Width: 200, Height: 200, Frames: []*image.RGBA{img}, Delays: []time.Duration{50 * time.Millisecond}}
}

// TestThumbCacheRoundTrip pins the store→load pipeline: an enabled cache
// thumbnails a decoded sprite to ~the target height, persists it, and a
// RequestLoad delivers a decodable stand-in on Results. Disabled, Store is a
// no-op (the default-OFF promise).
func TestThumbCacheRoundTrip(t *testing.T) {
	if !thumbEncoderAvailable() {
		t.Skip("webpenc unavailable (fallback build): thumbnails are structurally disabled")
	}
	tc, err := NewThumbCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()

	// Disabled (the default): nothing is stored.
	tc.Store("base/ghost", thumbFixture())
	time.Sleep(50 * time.Millisecond) // generous: the worker must NOT write
	tc.SetEnabled(true)
	tc.RequestLoad("base/ghost")
	select {
	case r := <-tc.Results():
		t.Fatalf("a disabled Store must write nothing, got a result for %q", r.Base)
	case <-time.After(150 * time.Millisecond):
	}

	// Enabled: store → poll until the async encode lands → load → decoded thumb.
	tc.Store("base/ghost", thumbFixture())
	deadline := time.Now().Add(5 * time.Second)
	var got ThumbLoaded
	for {
		tc.RequestLoad("base/ghost")
		select {
		case got = <-tc.Results():
		case <-time.After(100 * time.Millisecond):
		}
		if got.Asset != nil || time.Now().After(deadline) {
			break
		}
	}
	if got.Asset == nil {
		t.Fatal("no thumbnail came back within the deadline")
	}
	if got.Base != "base/ghost" {
		t.Errorf("thumb base = %q, want base/ghost", got.Base)
	}
	if got.Asset.Height > ThumbHeightDefault || got.Asset.Height < 1 {
		t.Errorf("thumb height = %d, want ≤ the default %d", got.Asset.Height, ThumbHeightDefault)
	}
	if tc.Stored() == 0 {
		t.Error("Stored() should count the encode")
	}
}

// TestThumbPruneBudget pins the auto-prune: files past the byte budget are
// deleted OLDEST-first until under it. Driven directly against prune() with
// hand-written files and staggered mtimes (deterministic, no encoder needed).
func TestThumbPruneBudget(t *testing.T) {
	tc, err := NewThumbCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	root := tc.Root()
	blob := make([]byte, 1024)
	names := []string{"a.bin", "b.bin", "c.bin", "d.bin"}
	base := time.Now().Add(-time.Hour)
	for i, n := range names { // a = oldest … d = newest
		p := filepath.Join(root, n)
		if err := os.WriteFile(p, blob, 0o644); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	tc.SetBudget(2048) // room for two files
	tc.prune()
	for _, n := range []string{"a.bin", "b.bin"} { // oldest two go
		if _, err := os.Stat(filepath.Join(root, n)); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned (oldest-first)", n)
		}
	}
	for _, n := range []string{"c.bin", "d.bin"} { // newest two stay
		if _, err := os.Stat(filepath.Join(root, n)); err != nil {
			t.Errorf("%s should have survived the prune: %v", n, err)
		}
	}
	tc.SetBudget(1 << 30) // huge budget: prune is a no-op
	tc.prune()
	if _, err := os.Stat(filepath.Join(root, "d.bin")); err != nil {
		t.Error("an under-budget store must not be pruned")
	}
}

// TestThumbDefaultsPinned pins the config↔assets default constants equal (the
// two packages can't import each other's numbers into one place, so a drifted
// default would silently disagree between the Settings labels and the encoder).
func TestThumbDefaultsPinned(t *testing.T) {
	if config.ThumbHeightDefaultPx != ThumbHeightDefault {
		t.Errorf("config.ThumbHeightDefaultPx (%d) != assets.ThumbHeightDefault (%d)", config.ThumbHeightDefaultPx, ThumbHeightDefault)
	}
	if config.ThumbQualityDefault != ThumbQualityDefault {
		t.Errorf("config.ThumbQualityDefault (%d) != assets.ThumbQualityDefault (%d)", config.ThumbQualityDefault, ThumbQualityDefault)
	}
}
