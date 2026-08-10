package render

// Gates for the bounded user-art tier (v1.90.0 W4 — design §Q2).
//
// The budget IS the product here. Theme media is PINNED, i.e. eviction-exempt: no
// LRU will ever take it back, so every byte admitted is a byte committed for as
// long as the theme is applied. That makes the door the only place the number can
// be enforced, and it makes an accounting bug at the door indistinguishable from a
// memory leak. These gates are the door.

import (
	"image"
	"strconv"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// newThemeMediaDecoded builds a one-frame decoded page of the given size.
//
// Deliberately a fresh allocation per call. UploadThemeMedia releases what it is
// handed on EVERY path, refusals included — a caller that had to remember to free a
// rejected buffer would make the budget check itself the leak — so a shared fixture
// would have the second use measuring a released page.
func newThemeMediaDecoded(w, h int) *assets.Decoded {
	d := &assets.Decoded{Width: w, Height: h}
	d.Frames = append(d.Frames, image.NewRGBA(image.Rect(0, 0, w, h)))
	return d
}

// themeMediaID names the i-th fixture page under the format's own id grammar
// ([a-z0-9_-]{1,24}).
func themeMediaID(i int) string { return "m" + strconv.Itoa(i) }

// pixelsForBytes is the largest square edge whose RGBA payload fits n bytes.
func pixelsForBytes(n int64) int {
	side := 1
	for int64((side+1)*(side+1))*4 <= n {
		side++
	}
	return side
}

// residentThemeMedia walks the pinned map and returns the theme-media keys, the
// bytes they really hold and the two counts — the GROUND TRUTH the ledger is
// checked against. Walking the map matters: an accounting bug that under-counts
// passes every assertion about the ledger forever while the memory grows.
func residentThemeMedia(s *TextureStore) (keys map[string]bool, bytes int64, images, gens int) {
	keys = map[string]bool{}
	for key, page := range s.pinned {
		if !IsThemeMediaKey(key) {
			continue
		}
		keys[key] = true
		bytes += page.bytes
		if strings.HasPrefix(key, ThemeGenKeyPrefix) {
			gens++
		} else {
			images++
		}
	}
	return keys, bytes, images, gens
}

// TestThemeMediaBudgetTracksTexBudgetMiB pins the SCALING rule: the allowance is a
// fraction of the user's own T1 budget, clamped at both ends.
//
// A fixed ceiling was the obvious alternative and it is wrong in both directions —
// 48 MiB is 75% of the default 64 MiB T1 and a 1.5x overcommit at the 32 MiB
// minimum, while the bare ratio gives 96 MiB at a 256 MiB budget, which is no
// longer a decoration allowance. This is what stops either end drifting.
func TestThemeMediaBudgetTracksTexBudgetMiB(t *testing.T) {
	if got, want := ThemeMediaByteCap(64), int64(24<<20); got != want {
		t.Errorf("ThemeMediaByteCap(64) = %d, want %d (3/8 of the default T1 budget)", got, want)
	}
	// Monotonic in the budget, so raising T1 can never LOWER the art allowance.
	prev := int64(0)
	for miB := 32; miB <= 256; miB += 8 {
		got := ThemeMediaByteCap(miB)
		if got < prev {
			t.Fatalf("ThemeMediaByteCap(%d) = %d, below the previous %d — the allowance must not fall as T1 rises",
				miB, got, prev)
		}
		if got < themeMediaByteFloor || got > themeMediaByteCeil {
			t.Fatalf("ThemeMediaByteCap(%d) = %d, outside the [%d, %d] clamp",
				miB, got, themeMediaByteFloor, themeMediaByteCeil)
		}
		prev = got
	}
	// The two clamps really clamp, at the two ends they exist for.
	//
	// The FLOOR is deliberately below the ratio's answer at the 32 MiB minimum
	// (3/8 of 32 is 12 MiB, which is what a minimum-budget install gets): it is a
	// backstop for a budget SMALLER than the settings row allows — a hand-edited
	// preferences file, or a future minimum — not the value anyone reaches by moving
	// the slider. A floor that bound at the published minimum would be the real cap
	// and the ratio would be decoration.
	if got, want := ThemeMediaByteCap(32), int64(12<<20); got != want {
		t.Errorf("at the 32 MiB minimum the allowance is %d, want the ratio's %d", got, want)
	}
	if got := ThemeMediaByteCap(1); got != themeMediaByteFloor {
		t.Errorf("below the floor's crossover the allowance is %d, want the floor %d — a hand-edited "+
			"budget must not make theme art impossible", got, themeMediaByteFloor)
	}
	if got := ThemeMediaByteCap(256); got != themeMediaByteCeil {
		t.Errorf("at 256 MiB the allowance is %d, want the ceiling %d — raising T1 for a big character "+
			"roster is not a request for 96 MiB of pinned decoration", got, themeMediaByteCeil)
	}
	// A nonsense budget falls back to the default rather than producing a nonsense
	// allowance: the value comes from a hand-editable preferences file.
	if got, want := ThemeMediaByteCap(0), ThemeMediaByteCap(int(T1BudgetBytes>>20)); got != want {
		t.Errorf("ThemeMediaByteCap(0) = %d, want the default-budget answer %d", got, want)
	}
	// The per-item cap is exactly half the per-asset decode cap, which is what makes
	// decimation predictable rather than emergent.
	if got, want := ThemeMediaItemByteCap(64), ThemeMediaByteCap(64)/themeMediaItemDiv; got != want {
		t.Errorf("ThemeMediaItemByteCap(64) = %d, want %d", got, want)
	}
	if ThemeMediaItemByteCap(64) >= ThemeMediaByteCap(64) {
		t.Error("the per-item cap is not below the total — one page could take the whole allowance")
	}
}

// TestThemeMediaKeysAreThemeNamespaced pins the collision bug the key scheme exists
// to prevent.
//
// The theme-swap prune drops keys the NEW theme does not declare. With bare ids
// (`theme://m/bg`) two themes that both declare `bg` leave the old page resident,
// the prune sees a key the new theme DOES declare, and the new theme silently
// renders the previous theme's texture — a defect that only appears on the second
// theme somebody applies, which is to say never in a test that applies one.
func TestThemeMediaKeysAreThemeNamespaced(t *testing.T) {
	a := ThemeMediaKey("umineko", "bg")
	b := ThemeMediaKey("thh_trial", "bg")
	if a == b {
		t.Fatalf("two themes' %q media share the key %q — the prune cannot tell them apart", "bg", a)
	}
	for _, k := range []string{a, b} {
		if !strings.HasPrefix(k, ThemeMediaKeyPrefix) {
			t.Errorf("%q is not under %q — it would miss the theme:// prune, the negative cache and reservedKey",
				k, ThemeMediaKeyPrefix)
		}
		if !IsThemeMediaKey(k) || !reservedKey(k) {
			t.Errorf("%q is not recognised as theme media / reserved", k)
		}
	}
	// Generator tiles are content-addressed, so the namespace question does not
	// arise: the same params ARE the same tile, whoever declared them.
	//
	// TWO SEPARATE CALLS, one per notional theme, captured before they are compared:
	// an assertion that spells the same expression on both sides can only ever agree
	// with itself. And the tail is read BACK to the hash, which is the half that
	// makes this a statement about the key being a function OF ITS HASH rather than
	// merely a deterministic string — a builder that dropped the value entirely
	// (every tile under one key, the second upload replacing the first) would pass
	// the equality on its own.
	//
	// SAID PLAINLY, so nobody reads the equality as the evidence: on its own it is
	// nearly vacuous. ThemeGenKey is a pure Sprintf, so only a stateful or
	// counter-based builder could ever fail it — a key mangled but still injective
	// (hash^0xff, say) walks straight through. The parse-back below is the
	// assertion that does the work; the equality is kept because the pair of calls
	// is what states the "same params ARE the same tile" claim in the first place.
	const tileHash = uint64(0x0123456789abcdef)
	byUmineko, byTrial := ThemeGenKey(tileHash), ThemeGenKey(tileHash)
	if byUmineko != byTrial {
		t.Errorf("two themes declaring the same tile got %q and %q — a content address that is not a "+
			"function of the hash alone would upload one tile twice and prune one of the copies", byUmineko, byTrial)
	}
	if got, err := strconv.ParseUint(strings.TrimPrefix(byUmineko, ThemeGenKeyPrefix), 16, 64); err != nil || got != tileHash {
		t.Errorf("ThemeGenKey(%#x) = %q, whose tail reads back as %#x (err %v) — the key must carry the "+
			"hash it was built from", tileHash, byUmineko, got, err)
	}
	if ThemeGenKey(1) == ThemeGenKey(2) {
		t.Error("two hashes produced one generator key")
	}
	g := ThemeGenKey(0xABCDEF)
	if !strings.HasPrefix(g, ThemeGenKeyPrefix) || !IsThemeMediaKey(g) || !reservedKey(g) {
		t.Errorf("generator key %q is not under the reserved scheme", g)
	}
	// Fixed width and lower case: a cache key must be byte-identical across runs.
	if len(ThemeGenKey(1)) != len(ThemeGenKey(^uint64(0))) {
		t.Errorf("generator keys are not fixed width: %q vs %q", ThemeGenKey(1), ThemeGenKey(^uint64(0)))
	}
	if g != strings.ToLower(g) {
		t.Errorf("generator key %q is not lower case", g)
	}
	// And an ordinary asset URL is emphatically not theme media.
	if IsThemeMediaKey("https://example.org/characters/phoenix/(a)normal") {
		t.Error("a server asset URL matched the theme-media predicate")
	}
}

// TestUploadPinnedRejectsThemeMediaPrefixes pins the SECOND DOOR being locked.
//
// UploadPinned is eviction-exempt and count-uncapped by nature. That is correct for
// the bounded stem tables the client itself uploads and catastrophic for art a
// stranger's theme declares, so the admission gate is only worth anything if
// UploadPinned cannot be used to walk around it. The rejection is at the door, not
// in a comment.
func TestUploadPinnedRejectsThemeMediaPrefixes(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		ThemeMediaKey("umineko", "bg"),
		ThemeGenKey(0xdeadbeef),
		ThemeMediaKeyPrefix + "x/y",
		ThemeGenKeyPrefix + "0",
	} {
		if err := store.UploadPinned(key, newThemeMediaDecoded(8, 8)); err == nil {
			t.Errorf("UploadPinned accepted %q — the admission gate can be walked around", key)
		}
		// Get, not Contains: Contains is the MANAGER's T1 probe and deliberately does
		// not see the pinned map (an asset URL is never pinned), so it would answer
		// false here whatever happened.
		if _, resident := store.Get(key); resident {
			t.Errorf("UploadPinned left %q resident after refusing it", key)
		}
	}
	// Ordinary theme chrome is untouched: this is a SUB-prefix rejection, not a
	// theme:// rejection.
	if err := store.UploadPinned("theme://chatbox", newThemeMediaDecoded(8, 8)); err != nil {
		t.Fatalf("UploadPinned refused ordinary theme chrome: %v", err)
	}
	if _, resident := store.Get("theme://chatbox"); !resident {
		t.Error("ordinary theme chrome did not land")
	}
	if store.ThemeMediaBytes() != 0 || store.ThemeMediaCount() != 0 {
		t.Errorf("chrome the CLIENT pins counted against the USER's art budget: %d bytes / %d images — "+
			"the two ledgers answer different questions and must not share a number",
			store.ThemeMediaBytes(), store.ThemeMediaCount())
	}
	// The gated door refuses a key outside both sub-namespaces, so neither function
	// can be used for the other's job.
	if err := store.UploadThemeMedia("theme://chatbox", newThemeMediaDecoded(8, 8), 64); err == nil {
		t.Error("UploadThemeMedia accepted ordinary chrome — it would then count against the art budget")
	}
}

// TestThemeMediaAdmissionIsBounded pins both counts. Bytes alone are not a bound
// (hard rule 4): 24 tiny pages and 24 000 tiny pages cost the same bytes and very
// different map, prune and upload work.
func TestThemeMediaAdmissionIsBounded(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	const budgetMiB = 64

	// Small pages, so the COUNT is what refuses rather than the bytes.
	admitted := 0
	for i := 0; i < ThemeMediaCap*3; i++ {
		if err := store.UploadThemeMedia(ThemeMediaKey("t", themeMediaID(i)), newThemeMediaDecoded(8, 8), budgetMiB); err == nil {
			admitted++
		}
	}
	if admitted != ThemeMediaCap {
		t.Errorf("%d media pages admitted, cap is %d", admitted, ThemeMediaCap)
	}
	if store.ThemeMediaCount() != ThemeMediaCap {
		t.Errorf("ThemeMediaCount = %d, want %d", store.ThemeMediaCount(), ThemeMediaCap)
	}
	// The generator cap is SEPARATE — a theme at its image cap can still declare
	// tiles, because 12 tiles are 3 MiB and 24 images can be the whole allowance.
	gens := 0
	for i := 0; i < ThemeGenCap*3; i++ {
		if err := store.UploadThemeMedia(ThemeGenKey(uint64(i)), newThemeMediaDecoded(8, 8), budgetMiB); err == nil {
			gens++
		}
	}
	if gens != ThemeGenCap {
		t.Errorf("%d generator tiles admitted, cap is %d", gens, ThemeGenCap)
	}
	if store.ThemeGenCount() != ThemeGenCap {
		t.Errorf("ThemeGenCount = %d, want %d", store.ThemeGenCount(), ThemeGenCap)
	}
	// Re-uploading an EXISTING key is always allowed and never double-counts: it is
	// a heal after a purge, or the editor changing one image, and refusing it would
	// leave the theme worse than it was.
	before := store.ThemeMediaBytes()
	if err := store.UploadThemeMedia(ThemeMediaKey("t", themeMediaID(0)), newThemeMediaDecoded(8, 8), budgetMiB); err != nil {
		t.Errorf("re-uploading a resident key was refused at the cap: %v", err)
	}
	if store.ThemeMediaCount() != ThemeMediaCap || store.ThemeMediaBytes() != before {
		t.Errorf("a replacement double-counted: %d images / %d bytes (was %d)",
			store.ThemeMediaCount(), store.ThemeMediaBytes(), before)
	}
	// The ledger matches the map, not just itself.
	_, bytes, images, tiles := residentThemeMedia(store)
	if bytes != store.ThemeMediaBytes() || images != store.ThemeMediaCount() || tiles != store.ThemeGenCount() {
		t.Errorf("ledger %d/%d/%d vs map %d/%d/%d",
			store.ThemeMediaBytes(), store.ThemeMediaCount(), store.ThemeGenCount(), bytes, images, tiles)
	}
}

// TestThemeMediaOneItemCannotEatTheBudget pins the per-item cap.
//
// Without it, one 4K wallpaper takes the entire allowance and every other element
// in the theme becomes an over-budget placeholder — which reads to the author as
// "the editor broke", because the thing they can see is fine and everything else
// vanished. The per-item cap makes the refusal land on the item that caused it,
// with a number the author can act on.
func TestThemeMediaOneItemCannotEatTheBudget(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	// The 32 MiB floor, so the fixture pages stay small enough to be cheap while the
	// arithmetic is identical (the ratio is clamped at both ends and the item cap is
	// always budget/8).
	const budgetMiB = 32
	item := ThemeMediaItemByteCap(budgetMiB)

	// A page one row past the item cap is refused, and nothing is left behind.
	big := pixelsForBytes(item) + 1
	key := ThemeMediaKey("t", "wallpaper")
	if err := store.UploadThemeMedia(key, newThemeMediaDecoded(big, big), budgetMiB); err == nil {
		t.Fatalf("a %dx%d page (over the %d-byte per-item cap) was admitted", big, big, item)
	}
	if _, resident := store.Get(key); resident || store.ThemeMediaBytes() != 0 || store.ThemeMediaCount() != 0 {
		t.Errorf("a refused page left state behind: resident=%v bytes=%d count=%d",
			resident, store.ThemeMediaBytes(), store.ThemeMediaCount())
	}
	// One that fits is admitted, and the ledger says exactly what it cost.
	ok := pixelsForBytes(item)
	if err := store.UploadThemeMedia(key, newThemeMediaDecoded(ok, ok), budgetMiB); err != nil {
		t.Fatalf("a page AT the per-item cap was refused: %v", err)
	}
	if got, want := store.ThemeMediaBytes(), int64(ok*ok*4); got != want {
		t.Errorf("ThemeMediaBytes = %d, want %d", got, want)
	}
	// And the TOTAL still refuses, separately: eight more of those would breach it
	// even though each one is individually legal.
	refused := 0
	for i := 0; i < themeMediaItemDiv+2; i++ {
		if err := store.UploadThemeMedia(ThemeMediaKey("t", themeMediaID(i)), newThemeMediaDecoded(ok, ok), budgetMiB); err != nil {
			refused++
		}
	}
	if refused == 0 {
		t.Error("the TOTAL allowance never refused anything — only the per-item cap is enforced")
	}
	if got := store.ThemeMediaBytes(); got > ThemeMediaByteCap(budgetMiB) {
		t.Errorf("theme media is %d bytes, over the %d-byte allowance", got, ThemeMediaByteCap(budgetMiB))
	}
}

// TestThemeMediaAdmissionByteTotalIsOrderIndependent pins design §Q2's "silent
// eviction is forbidden", and it is named for the property FCFS admission can
// actually have.
//
// FCFS is order-DEPENDENT by construction: whichever pages arrive first are the ones
// that stay, so the resident SET follows the declaration order and always will. The
// two invariants that do hold are the ones asserted below — the committed BYTE TOTAL
// is the same whichever order the same declaration set arrives in (the allowance is
// spent, not raced), and within each order admission is strictly first-come-first-
// served: whatever was admitted STAYED admitted, and no latecomer ever evicted an
// earlier page to make room for itself. That second half is the one an author can
// reason about and a bug report can describe; a refusal is explicit, and internal/ui
// turns it into a self-describing placeholder.
func TestThemeMediaAdmissionByteTotalIsOrderIndependent(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	const budgetMiB = 64

	admit := func(order []int) (map[string]bool, int64) {
		store, err := NewTextureStore(ren)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Purge()
		for _, i := range order {
			_ = store.UploadThemeMedia(ThemeMediaKey("t", themeMediaID(i)), newThemeMediaDecoded(8, 8), budgetMiB)
		}
		keys, bytes, _, _ := residentThemeMedia(store)
		return keys, bytes
	}

	forward := make([]int, ThemeMediaCap+8)
	for i := range forward {
		forward[i] = i
	}
	reverse := make([]int, len(forward))
	for i := range reverse {
		reverse[i] = forward[len(forward)-1-i]
	}

	fwdSet, fwdBytes := admit(forward)
	revSet, revBytes := admit(reverse)
	if fwdBytes != revBytes {
		t.Errorf("admitting the same set in two orders committed %d vs %d bytes", fwdBytes, revBytes)
	}
	if len(fwdSet) != ThemeMediaCap || len(revSet) != ThemeMediaCap {
		t.Fatalf("resident sets are %d and %d, want %d each — the boundary was never reached",
			len(fwdSet), len(revSet), ThemeMediaCap)
	}
	// The KEY property, in both directions: whatever was admitted STAYED admitted.
	// Each run must hold exactly the first ThemeMediaCap ids of its own order.
	check := func(what string, resident map[string]bool, order []int) {
		for i, id := range order {
			key := ThemeMediaKey("t", themeMediaID(id))
			want := i < ThemeMediaCap
			if resident[key] != want {
				t.Errorf("%s order: %q resident=%v, want %v — admission must be first-come-first-served, "+
					"never an eviction to make room for a latecomer", what, key, resident[key], want)
				return
			}
		}
	}
	check("forward", fwdSet, forward)
	check("reverse", revSet, reverse)
}

// TestPinnedBytesStayUnderThemeBudgetAcrossThreeApplies is the leak gate, and it
// takes three applies on purpose.
//
// A single-apply test cannot catch the failure mode this is written against: the
// swap that does not PRUNE. Pinned pages are eviction-exempt, so a theme whose art
// is not dropped when the next theme lands stays resident forever — and the symptom
// is not a crash but a client that is 24 MiB heavier after every theme the user
// tries, which nobody attributes to themes. Three heavy applies is the shortest
// sequence in which "apply → swap → apply again" is distinguishable from "apply".
func TestPinnedBytesStayUnderThemeBudgetAcrossThreeApplies(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	// The floor budget again: the leak this measures is about the PRUNE, not about
	// page sizes, and a smaller allowance makes the fixture cheap while keeping the
	// pages big enough that a missed prune would breach it inside three rounds.
	const budgetMiB = 32
	total := ThemeMediaByteCap(budgetMiB)
	// Two thirds of the item cap: three themes' worth would breach the allowance,
	// one theme's worth fits with room.
	side := pixelsForBytes(ThemeMediaItemByteCap(budgetMiB) * 2 / 3)

	// One heavy apply, in the order App.pollThemeApply performs it: PRUNE FIRST,
	// then admit.
	//
	// The order is load-bearing and it is the opposite of the chrome loop's. Chrome
	// uploads then prunes, because chrome has no budget and replacing a skin in
	// place avoids a one-frame gap. Theme media has a budget, and the previous
	// theme's art is not part of the new theme's allowance — admit-then-prune means
	// a swap between two heavy themes measures the incoming pages against an
	// allowance the OUTGOING theme is still holding, so the new theme admits nothing
	// and then the prune leaves the user with a blank one. Pruning first costs
	// nothing (both happen inside one pollThemeApply, before the frame paints) and
	// makes the allowance mean "this theme's art", which is what it says.
	apply := func(themeID string) {
		declared := map[string]bool{}
		for i := 0; i < ThemeMediaCap; i++ {
			declared[ThemeMediaKey(themeID, themeMediaID(i))] = true
		}
		resident, _, _, _ := residentThemeMedia(store)
		for key := range resident {
			if !declared[key] {
				store.Remove(key)
			}
		}
		for key := range declared {
			_ = store.UploadThemeMedia(key, newThemeMediaDecoded(side, side), budgetMiB)
		}
	}

	for round, id := range []string{"heavy_a", "heavy_b", "heavy_c"} {
		apply(id)
		if got := store.ThemeMediaBytes(); got > total {
			t.Fatalf("after apply %d (%s) theme media is %d bytes, over the %d-byte allowance — "+
				"the swap is not returning the previous theme's art", round+1, id, got, total)
		}
		keys, bytes, images, gens := residentThemeMedia(store)
		if bytes != store.ThemeMediaBytes() {
			t.Fatalf("after apply %d the ledger says %d bytes and the pinned map holds %d — "+
				"the accounting and the memory have diverged", round+1, store.ThemeMediaBytes(), bytes)
		}
		if images != store.ThemeMediaCount() || gens != store.ThemeGenCount() {
			t.Fatalf("after apply %d the counts are %d/%d and the map holds %d/%d",
				round+1, store.ThemeMediaCount(), store.ThemeGenCount(), images, gens)
		}
		if images == 0 {
			t.Fatalf("after apply %d nothing was admitted — the gate is measuring the empty case", round+1)
		}
		for key := range keys {
			if !strings.HasPrefix(key, ThemeMediaKeyPrefix+id+"/") {
				t.Fatalf("after applying %s, %q is still resident — the previous theme's art survived the swap", id, key)
			}
		}
	}

	// Swapping to a stock AO2 theme (no media at all) drains it to zero.
	keys, _, _, _ := residentThemeMedia(store)
	for key := range keys {
		store.Remove(key)
	}
	if store.ThemeMediaBytes() != 0 || store.ThemeMediaCount() != 0 || store.ThemeGenCount() != 0 {
		t.Errorf("after swapping to a theme with no media: %d bytes / %d images / %d tiles, want all zero",
			store.ThemeMediaBytes(), store.ThemeMediaCount(), store.ThemeGenCount())
	}
	// Purge zeroes it too — the window-recreate path must not leave a phantom
	// allowance that then refuses the heal that follows it.
	if err := store.UploadThemeMedia(ThemeGenKey(7), newThemeMediaDecoded(8, 8), budgetMiB); err != nil {
		t.Fatal(err)
	}
	store.Purge()
	if store.ThemeMediaBytes() != 0 || store.ThemeGenCount() != 0 {
		t.Errorf("after Purge: %d bytes / %d tiles, want zero — the heal would then refuse itself",
			store.ThemeMediaBytes(), store.ThemeGenCount())
	}
}
