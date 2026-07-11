package render

import (
	"image"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
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

// TestHeldSpeakerBridge pins the character-layer extension of the held-frame
// bridge (the last black-flash hole): the store now steals the CURRENT
// speaker/pair sprite's first frame too, not just scenery. When the on-screen
// speaker's page is evicted mid-display by a cap-sized incoming upload,
// drawSprite's hold-previous fallback can't help (lastGood == the base just
// drawn) and the thumbnail is opt-in default-OFF — so without the steal the
// character blanked. This test drives the REAL drawSprite miss path (not
// resolveHeld directly): with the thumbnail off and no previous sprite to hold,
// the held probe wired at viewport.go:1037-1040 is the only non-blank branch,
// so drawSprite drawing the stand-in geometry proves that wiring fires. (The ui
// widening that makes the speaker base steal-eligible — IsLiveStageBase — is
// pinned in the ui package; render can't import ui, so this test injects the
// equivalent predicate to exercise the store + draw path.)
func TestHeldSpeakerBridge(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	// 12 MiB budget → small shield 4 MiB, main tier 8 MiB. Each 2-frame
	// 1024×768 page is ≈6 MiB: two of them can never coexist in the main tier.
	store, err := NewTextureStoreBudget(ren, 12<<20)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	// The widened live-stage probe: the drawn speaker sprite is steal-eligible,
	// exactly like scenery (ui.IsLiveStageBase now returns true for it).
	const speaker = "live://characters/witch/(a)talk"
	store.SetLiveScenery(func(base string) bool { return base == speaker })

	// The speaker page has a DISTINCTIVE aspect (900×600) so its held stand-in
	// geometry is uniquely identifiable in v.dstRect below; the evictor is a
	// different size so it can't be mistaken for the held page.
	const spkW, spkH = 900, 600
	if err := store.Upload(speaker, bigDecoded(2, spkW, spkH)); err != nil {
		t.Fatalf("upload speaker: %v", err)
	}
	// A cap-sized incoming page (another sprite streaming in) evicts the speaker.
	if err := store.Upload("sprite://incoming", bigDecoded(2, 1024, 768)); err != nil {
		t.Fatalf("upload incoming: %v", err)
	}
	if store.Contains(speaker) {
		t.Fatal("test setup: the incoming page must have evicted the speaker")
	}
	held, ok := store.Get(HeldKeyPrefix + speaker)
	if !ok {
		t.Fatal("evicting the live speaker must park a held:// bridge page (the character-layer extension)")
	}
	if len(held.Frames) != 1 || held.Frames[0] == nil {
		t.Fatalf("the bridge holds exactly the stolen first frame, got %d frames", len(held.Frames))
	}

	// Drive the REAL drawSprite miss path. Thumbnails OFF and no prior sprite to
	// hold (lastGood == "") leave the held probe as the only non-blank branch, so
	// v.dstRect landing on the held page's geometry proves drawSprite consulted
	// resolveHeld ahead of the thumbnail/hold-previous paths.
	v := &Viewport{store: store}
	v.thumbSprites = false                // thumbnail path stays off (default)
	v.spriteLoadMode = SpriteLoadHoldPrev // hold-previous armed but has nothing to hold
	anim := &animState{}
	anim.reset(speaker) // precomputes heldKey = held://<speaker>, like the real path
	if _, ok := anim.resolve(store); ok {
		t.Fatal("the speaker's real page must be evicted (resolve misses)")
	}
	layer := &courtroom.SpriteLayer{Active: speaker, Visible: true}
	vp := sdl.Rect{X: 0, Y: 0, W: 1600, H: 900}
	v.dstRect = sdl.Rect{} // clear so we can prove drawSprite wrote it
	v.drawSprite(ren, layer, anim, vp, 0, 100)

	// The stand-in is scaled to viewport height preserving the 900×600 aspect and
	// horizontally centered (drawHeldSprite). A zero rect means the miss path drew
	// nothing — the black-flash the extension exists to close.
	wantW := vp.H * spkW / spkH
	wantX := vp.X + (vp.W-wantW)/2
	if v.dstRect.W != wantW || v.dstRect.H != vp.H || v.dstRect.X != wantX || v.dstRect.Y != vp.Y {
		t.Fatalf("drawSprite miss path did not draw the held stand-in: dstRect=%v, want W=%d H=%d X=%d Y=%d",
			v.dstRect, wantW, vp.H, wantX, vp.Y)
	}

	// The real speaker re-uploading releases the bridge.
	if err := store.Upload(speaker, bigDecoded(2, spkW, spkH)); err != nil {
		t.Fatalf("re-upload speaker: %v", err)
	}
	if _, ok := store.Get(HeldKeyPrefix + speaker); ok {
		t.Fatal("the bridge must release once the real speaker page is resident again")
	}
}
