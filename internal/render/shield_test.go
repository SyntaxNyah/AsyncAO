package render

import (
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
)

// sizedFixture builds a decoded page of frames×(edge×edge) RGBA — the knob the
// shield tests use to create real byte pressure on a tier.
func sizedFixture(edge, frames int) *assets.Decoded {
	d := &assets.Decoded{Animated: frames > 1, Width: edge, Height: edge}
	for i := 0; i < frames; i++ {
		img := image.NewRGBA(image.Rect(0, 0, edge, edge))
		for p := 3; p < len(img.Pix); p += 4 {
			img.Pix[p] = 0xFF
		}
		d.Frames = append(d.Frames, img)
		d.Delays = append(d.Delays, 50*time.Millisecond)
	}
	return d
}

// TestSplitT1Budget pins the shield carve math: an eighth of the budget with a
// 4 MiB floor, capped at half, and main+small always equals the configured
// total (the 256 MiB memory-budget invariant).
func TestSplitT1Budget(t *testing.T) {
	cases := []struct {
		budget, main, small int64
	}{
		{64 << 20, 56 << 20, 8 << 20},      // default: 64 → 56 + 8
		{16 << 20, 12 << 20, 4 << 20},      // small budget: the 4 MiB floor wins over /8
		{4 << 20, 2 << 20, 2 << 20},        // tiny budget: the half-cap keeps main alive
		{1024 << 20, 896 << 20, 128 << 20}, // huge power-user budget scales linearly
	}
	for _, tc := range cases {
		main, small := splitT1Budget(tc.budget)
		if main != tc.main || small != tc.small {
			t.Errorf("splitT1Budget(%d) = (%d, %d), want (%d, %d)", tc.budget, main, small, tc.main, tc.small)
		}
		if main+small != tc.budget {
			t.Errorf("splitT1Budget(%d): tiers sum to %d, must equal the configured budget", tc.budget, main+small)
		}
	}
}

// TestDecodeCapFitsMainTier is the cross-package invariant for the decode-cap /
// T1-budget arithmetic (the stage-flash root cause): the per-asset decode cap
// (cache.MaxDecodedAssetBytes — the single source both the decoder default and
// main's live override derive from) must never exceed the render MAIN tier for
// ANY budget, and must stay <= main/2 so one landing page can't evict even half
// the on-screen working set. The two formulas live in different packages
// (cache owns the cap, render owns splitT1Budget); this pins that they stay
// compatible even if one changes.
func TestDecodeCapFitsMainTier(t *testing.T) {
	// Default, the two Settings-slider extremes (min 32 MiB, max 256 MiB), plus
	// pathological small/huge values that exercise the split's floor + half-cap.
	budgets := []int64{
		cache.DefaultT1BudgetBytes,
		32 << 20, 256 << 20, // TexBudgetMinMiB .. TexBudgetMaxMiB
		4 << 20, 1 << 20, 1024 << 20, // floor / half-cap / huge power-user
	}
	for _, b := range budgets {
		decodeCap := cache.MaxDecodedAssetBytes(b)
		main, _ := splitT1Budget(b)
		if decodeCap > main {
			t.Errorf("budget=%d: decode cap %d exceeds main tier %d — one page could evict the whole working set", b, decodeCap, main)
		}
		if decodeCap > main/2 {
			t.Errorf("budget=%d: decode cap %d exceeds main/2 (%d) — one page could evict half the working set", b, decodeCap, main/2)
		}
	}
}

// TestSmallTexTier pins the shield membership to exactly the decode-thumbnailed
// types — anything else in the shield could flood it with full-size art.
func TestSmallTexTier(t *testing.T) {
	for tt := assets.AssetType(0); tt < assets.AssetTypeCount; tt++ {
		want := tt == assets.AssetTypeCharIcon || tt == assets.AssetTypeEmoteButton
		if got := smallTexTier(tt); got != want {
			t.Errorf("smallTexTier(%v) = %v, want %v", tt.Name(), got, want)
		}
	}
}

// TestShieldSurvivesSpriteChurn is the regression test for the "emote buttons
// visibly refresh/redraw" report: a shield-tier page whose LRU recency is never
// touched (exactly how the gen-keyed UI page caches behave on steady frames)
// must survive a sprite streaming burst that provably churns the main tier.
// Under the old single tier the same burst swept the buttons out and the next
// drawn frame flashed the grid back to fallbacks.
func TestShieldSurvivesSpriteChurn(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	// 16 MiB total → 12 MiB main + 4 MiB shield (splitT1Budget).
	store, err := NewTextureStoreBudget(ren, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	const button = "srv/characters/witch/emotions/button1_off"
	if err := store.UploadSmall(button, sizedFixture(40, 1)); err != nil {
		t.Fatalf("button upload: %v", err)
	}

	// Stream ~30 MiB of 1 MiB sprites through the 12 MiB main tier — far past
	// its budget, with zero recency touches on the button in between.
	churned := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		base := fmt.Sprintf("srv/characters/big/(a)sprite-%d", i)
		if err := store.Upload(base, sizedFixture(512, 1)); err != nil {
			t.Fatalf("churn upload %d: %v", i, err)
		}
		churned = append(churned, base)
		store.DrainDestroyQueue()
	}
	// Sanity: the burst really did evict in the main tier…
	if store.Contains(churned[0]) {
		t.Fatal("churn burst never exceeded the main budget — the test exerts no pressure")
	}
	// …and the untouched button rode it out in the shield.
	if !store.Contains(button) {
		t.Fatal("shield-tier button evicted by sprite churn — the emote grid would flash back to fallbacks")
	}
	if _, ok := store.Get(button); !ok {
		t.Fatal("shield-tier button not resolvable via Get")
	}
}

// TestShieldInternalEvictionAndIsolation pins the shield's own bounds: it is a
// real LRU (overfilling it evicts its own oldest, bumping the generation so
// cached page pointers re-resolve), and its churn never touches the main tier.
func TestShieldInternalEvictionAndIsolation(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 16<<20) // 4 MiB shield
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	const sprite = "srv/characters/witch/(a)normal"
	if err := store.Upload(sprite, sizedFixture(256, 1)); err != nil {
		t.Fatalf("sprite upload: %v", err)
	}

	// ~8 MiB of 64 KiB icons through the 4 MiB shield: its own LRU must evict.
	genBefore := store.Generation()
	first := "srv/characters/char000/char_icon"
	for i := 0; i < 128; i++ {
		base := fmt.Sprintf("srv/characters/char%03d/char_icon", i)
		if err := store.UploadSmall(base, sizedFixture(128, 1)); err != nil {
			t.Fatalf("icon upload %d: %v", i, err)
		}
		store.DrainDestroyQueue()
	}
	if store.Contains(first) {
		t.Error("shield never evicted internally — its byte budget is not being enforced")
	}
	if !store.Contains("srv/characters/char127/char_icon") {
		t.Error("newest shield entry missing after internal churn")
	}
	if store.Generation() == genBefore {
		t.Error("shield churn did not bump the generation — cached page pointers could dangle")
	}
	// Icon floods must never evict the sprites on stage (the reverse isolation).
	if !store.Contains(sprite) {
		t.Error("shield churn evicted a main-tier sprite — the tiers are not isolated")
	}
}

// TestUploadSmallOversizeFallsToMain pins the pathological case: a "small"-typed
// page bigger than the whole shield budget (a many-frame animated icon) must
// still become resident — in the main tier, without churn immunity — instead of
// being refused and never appearing.
func TestUploadSmallOversizeFallsToMain(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 32<<20) // 4 MiB shield, 28 MiB main
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	// 5 × 512² RGBA = 5 MiB > the 4 MiB shield, well under the 28 MiB main.
	const base = "srv/characters/witch/emotions/button9_on"
	if err := store.UploadSmall(base, sizedFixture(512, 5)); err != nil {
		t.Fatalf("oversize small upload must fall through to the main tier, got: %v", err)
	}
	if !store.Contains(base) {
		t.Fatal("oversize small page never became resident")
	}
	page, ok := store.Get(base)
	if !ok || len(page.Frames) != 5 {
		t.Fatalf("oversize small page broken after fallback: ok=%v frames=%d", ok, len(page.Frames))
	}
}

// TestStoreStatsAggregateTiers pins the HUD contract: Stats spans both LRU
// tiers and its Budget still reads as the ONE configured T1 budget.
func TestStoreStatsAggregateTiers(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	const budget = int64(16 << 20)
	store, err := NewTextureStoreBudget(ren, budget)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	if err := store.Upload("srv/a/(a)x", sizedFixture(64, 1)); err != nil {
		t.Fatal(err)
	}
	if err := store.UploadSmall("srv/b/char_icon", sizedFixture(64, 1)); err != nil {
		t.Fatal(err)
	}
	st := store.Stats()
	if st.Budget != budget {
		t.Errorf("aggregate Budget = %d, want the configured %d", st.Budget, budget)
	}
	if st.Entries != 2 {
		t.Errorf("aggregate Entries = %d, want 2 (one per tier)", st.Entries)
	}
	want := int64(2 * 64 * 64 * 4)
	if st.Bytes != want {
		t.Errorf("aggregate Bytes = %d, want %d", st.Bytes, want)
	}

	// Remove must clear a page from whichever tier holds it.
	store.Remove("srv/b/char_icon")
	if store.Contains("srv/b/char_icon") {
		t.Error("Remove left the shield-tier page resident")
	}
	store.Remove("srv/a/(a)x")
	if store.Contains("srv/a/(a)x") {
		t.Error("Remove left the main-tier page resident")
	}
}
