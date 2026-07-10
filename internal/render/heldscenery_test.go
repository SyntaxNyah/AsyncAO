package render

import (
	"image"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// bigDecoded builds a decoded asset of frames×(w×h) RGBA frames — sized so a
// couple of them overflow a small test tier and force evictions.
func bigDecoded(frames, w, h int) *assets.Decoded {
	d := &assets.Decoded{Width: w, Height: h, Animated: frames > 1}
	for i := 0; i < frames; i++ {
		d.Frames = append(d.Frames, image.NewRGBA(image.Rect(0, 0, w, h)))
		d.Delays = append(d.Delays, 100*time.Millisecond)
	}
	return d
}

// TestHeldSceneryBridge pins the black-flash fix: when the LRU must evict the
// ON-SCREEN background (a cap-sized incoming page forces it — one page may be
// half the budget while the main tier is 7/8 of it, so two can never coexist),
// the store steals the page's first frame into the pinned tier under a held://
// key instead of letting the stage draw the black clear color. The bridge
// releases the moment the real page re-uploads, never fires for non-scenery
// bases, and never leaks through a Purge (whose tier purges fire the eviction
// callback per entry after the pinned map was already cleared).
func TestHeldSceneryBridge(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	// 12 MiB budget → small shield 4 MiB, main tier 8 MiB. Each 2-frame
	// 1024×768 page is ≈6 MiB: two of them can never coexist in the main tier.
	store, err := NewTextureStoreBudget(ren, 12<<20)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	store.SetLiveScenery(func(base string) bool { return base == "live://bg" })

	if err := store.Upload("live://bg", bigDecoded(2, 1024, 768)); err != nil {
		t.Fatalf("upload bg: %v", err)
	}
	if err := store.Upload("sprite://incoming", bigDecoded(2, 1024, 768)); err != nil {
		t.Fatalf("upload incoming: %v", err)
	}
	if store.Contains("live://bg") {
		t.Fatal("test setup: the incoming page must have evicted the bg")
	}
	held, ok := store.Get(HeldKeyPrefix + "live://bg")
	if !ok {
		t.Fatal("evicting the live bg must park a held:// bridge page")
	}
	if len(held.Frames) != 1 || held.Frames[0] == nil {
		t.Fatalf("the bridge holds exactly the stolen first frame, got %d frames", len(held.Frames))
	}

	// A non-scenery eviction must NOT steal: upload another page, evicting
	// the incoming sprite.
	if err := store.Upload("sprite://next", bigDecoded(2, 1024, 768)); err != nil {
		t.Fatalf("upload next: %v", err)
	}
	if _, ok := store.Get(HeldKeyPrefix + "sprite://incoming"); ok {
		t.Fatal("a non-scenery base must not be held")
	}

	// The real bg re-uploading releases the bridge (its Add evicts the
	// non-scenery "next" page — no steal for that one).
	if err := store.Upload("live://bg", bigDecoded(2, 1024, 768)); err != nil {
		t.Fatalf("re-upload bg: %v", err)
	}
	if _, ok := store.Get(HeldKeyPrefix + "live://bg"); ok {
		t.Fatal("the bridge must release once the real page is resident again")
	}

	// Purge with a live hold: force a fresh hold, then purge — nothing may
	// survive (the steal is suppressed while the tier purges replay onEvict).
	if err := store.Upload("sprite://evictor", bigDecoded(2, 1024, 768)); err != nil {
		t.Fatalf("upload evictor: %v", err)
	}
	if _, ok := store.Get(HeldKeyPrefix + "live://bg"); !ok {
		t.Fatal("test setup: the second eviction must re-hold the bg")
	}
	store.Purge()
	if _, ok := store.Get(HeldKeyPrefix + "live://bg"); ok {
		t.Fatal("a held page must not survive Purge")
	}
}
