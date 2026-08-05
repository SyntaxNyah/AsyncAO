package ui

// Gates for the PRODUCER side of the theme-media tier (v1.90.0 W4 — design §Q2).
//
// internal/render's own gates prove the store's ceiling holds. These two prove the
// things only the producer can get wrong: that hitting the ceiling leaves the user
// with something to fix rather than a hole, and that landing one page incrementally
// does not restage the whole theme.

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// writeThemeMediaFile drops a real (tiny) file into a theme folder, so the planner's
// existence probe has something to find. Its CONTENT is irrelevant: the plan budgets
// against the [media] row's DECLARED byte count, which is the whole point of the
// author declaring it — a plan that had to decode before it could budget would defeat
// itself.
func writeThemeMediaFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("not really an image"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOverBudgetElementIsCreatedNotDropped is design §Q2's "the UX never
// dead-ends", as a gate.
//
// Three behaviours, and every one of them is a decision that could plausibly have
// gone the other way:
//
//   - the over-budget entry STAYS IN THE PLAN. Dropping it would make the theme
//     render differently depending on the order its author added elements, which is
//     a property nobody can reason about and no bug report can describe.
//   - it carries its own REASON, naming the file, its size and the cap it broke.
//     "Something did not fit" is not actionable; "18.2 MiB, over the 8.0 MiB
//     per-image cap" is.
//   - the ELEMENT still exists and still bakes. The theme saves, the element is
//     dormant until it is fixed, and nothing the author wrote is lost.
func TestOverBudgetElementIsCreatedNotDropped(t *testing.T) {
	dir := t.TempDir()
	writeThemeMediaFile(t, dir, "huge.png")
	writeThemeMediaFile(t, dir, "small.png")

	item := render.ThemeMediaItemByteCap(config.TexBudgetDefaultMiB)
	sc := theme.NewSidecar()
	sc.Media = []theme.MediaEntry{
		{ID: "huge", File: "huge.png", Bytes: item + 1}, // one byte over the per-item cap
		{ID: "small", File: "small.png", Bytes: 4096},
		{ID: "gone", File: "nowhere.png", Bytes: 4096}, // declared, not on disk
	}
	sc.Elements = []theme.Element{
		{ID: "wall", Kind: theme.ElemImage, Band: theme.BandBackdrop, Space: theme.SpaceCourtroom,
			Media: "huge", Rect: theme.Rect{X: 0, Y: 0, W: 200, H: 100}},
		{ID: "badge", Kind: theme.ElemImage, Band: theme.BandOverlay, Space: theme.SpaceCourtroom,
			Media: "small", Rect: theme.Rect{X: 10, Y: 10, W: 40, H: 40}},
		{ID: "ghost", Kind: theme.ElemImage, Band: theme.BandMid, Space: theme.SpaceCourtroom,
			Media: "gone", Rect: theme.Rect{X: 20, Y: 20, W: 40, H: 40}},
	}

	plan := planThemeMedia(sc, "fixture", dir, config.TexBudgetDefaultMiB)
	if len(plan.Entries) != len(sc.Media) {
		t.Fatalf("the plan holds %d entries for %d declared media — an entry was DROPPED, so the theme's "+
			"appearance now depends on the order its author added them", len(plan.Entries), len(sc.Media))
	}
	byID := map[string]themeMediaPlanEntry{}
	for _, e := range plan.Entries {
		byID[e.ID] = e
	}
	if got := byID["huge"]; got.State != mediaOverBudget {
		t.Errorf("the over-cap entry is in state %d, want mediaOverBudget", got.State)
	} else {
		for _, want := range []string{"huge.png", "MiB", "cap"} {
			if !strings.Contains(got.Why, want) {
				t.Errorf("the over-budget reason %q never mentions %q — it has to name the file and the "+
					"number, or there is nothing to act on", got.Why, want)
			}
		}
	}
	if got := byID["gone"]; got.State != mediaMissing || got.Why == "" {
		t.Errorf("a declared file that is not on disk planned as state %d / %q, want mediaMissing with a reason",
			got.State, got.Why)
	}
	if got := byID["small"]; got.State != mediaPlanned || got.Path == "" {
		t.Errorf("the entry that fits planned as state %d (path %q) — it must be admitted", got.State, got.Path)
	}
	// Only what fits is budgeted. An over-budget entry that still counted against the
	// allowance would starve the elements after it for art it is not holding.
	if plan.Bytes != 4096 {
		t.Errorf("the plan committed %d bytes, want only the 4096 that fit", plan.Bytes)
	}
	// The KEY exists for every element regardless of state: the inspector uses it to
	// name what the placeholder is waiting for.
	for i := range sc.Elements {
		if plan.ElemKey(i) == "" {
			t.Errorf("element %q resolved to no key — an over-budget or missing element still has an identity",
				sc.Elements[i].ID)
		}
	}
	// And an element pointing at a refused entry is NOT inert: it bakes, it is
	// dispatched, and it draws nothing until somebody fixes the file.
	for i := range sc.Elements {
		if sc.Elements[i].Inert() {
			t.Errorf("element %q reads as inert — the author's work would be silently gone", sc.Elements[i].ID)
		}
	}

	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()
	a.themeSidecar = sc
	a.themeMediaPlan = plan
	a.themeLay.valid = false
	var seen []*bakedElement
	restore := captureElementPaints(&seen)
	a.drawCourtroom(1280, 720)
	restore()
	if a.themeLay.elN != len(sc.Elements) {
		t.Fatalf("the bake produced %d elements for %d declared — an over-budget element must still exist",
			a.themeLay.elN, len(sc.Elements))
	}
	if len(seen) != len(sc.Elements) {
		t.Fatalf("%d painter calls for %d elements — a placeholder element must still be dispatched", len(seen), len(sc.Elements))
	}
	// Its page is nil (nothing landed), which is what makes the painter draw nothing
	// rather than probe — the state the editor decorates with the hatch placeholder.
	for i := 0; i < a.themeLay.elN; i++ {
		if a.themeLay.el[i].page != nil {
			t.Errorf("baked element %d resolved a page, but nothing was uploaded in this fixture", i)
		}
	}
}

// TestIncrementalMediaLandDoesNotRestartTheThemeClock pins the two things the
// incremental land path must NOT do.
//
// It exists for the editor: changing one image must not restage the theme. The two
// omissions are the whole feature —
//
//   - a.themeAt is the animation clock every looping generator, animated skin and
//     phased element reads through themeElapsed(). Re-anchoring it makes everything
//     on screen jump back to frame 0, which reads as the client glitching every time
//     the author nudges a value.
//   - the text-cache purge exists for a PALETTE change (label textures are
//     colour-keyed). A media page has no colour anyone cached, and purging would
//     re-raster every string in the client for one image.
func TestIncrementalMediaLandDoesNotRestartTheThemeClock(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	// A theme that has been on screen for a while, with a message already rastered.
	// Anchored to a.now() rather than to time.Now(): themeElapsed reads the FRAME's
	// clock snapshot, and a fixture that never drew a frame would otherwise measure
	// the gap between the snapshot and the wall clock.
	before := a.now().Add(-5 * time.Second)
	a.themeAt = before
	a.rasterText = "an already-rastered IC line"
	elapsedBefore := a.themeElapsed()
	if elapsedBefore < 4*time.Second {
		t.Fatalf("the fixture's theme clock reads %v — the gate cannot tell a restart from a fresh apply", elapsedBefore)
	}

	const side = 8
	d := &assets.Decoded{Width: side, Height: side}
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xFF
	}
	d.Frames = append(d.Frames, img)
	key := render.ThemeMediaKey("fixture", "swapped")
	if err := a.landThemeMediaPage(key, d); err != nil {
		t.Fatalf("landThemeMediaPage: %v", err)
	}

	if !a.themeAt.Equal(before) {
		t.Errorf("the theme clock moved from %v to %v — every animated element on screen just jumped back "+
			"to frame 0 because one image changed", before, a.themeAt)
	}
	if a.rasterText == "" {
		t.Error("the message raster was invalidated — a media page has no colour anyone cached, and " +
			"re-rastering every string in the client for one image is what the incremental path exists to avoid")
	}
	// It really did land, or the two assertions above would be measuring a no-op.
	if !a.themeMediaKeys[key] {
		t.Fatal("the page was not recorded in the declared set — the next swap's prune would not know about it")
	}
	if page, ok := a.themeMediaPage(key); !ok || page == nil {
		t.Fatal("the landed page does not resolve through the generation-keyed cache")
	}
	if a.d.Store.ThemeMediaBytes() == 0 {
		t.Error("the store's art ledger did not move — the page bypassed the admission gate")
	}
	// A refusal is reported rather than swallowed: the editor needs to be able to say
	// "that image is too big" at the moment the author drops it.
	huge := pixelsForThemeItemCap(config.TexBudgetDefaultMiB) + 1
	big := &assets.Decoded{Width: huge, Height: huge}
	big.Frames = append(big.Frames, image.NewRGBA(image.Rect(0, 0, huge, huge)))
	if err := a.landThemeMediaPage(render.ThemeMediaKey("fixture", "toobig"), big); err == nil {
		t.Error("an over-cap incremental land was accepted")
	}
}

// TestThemeArtBudgetBarMemoizesItsLabel is the settings row's own gate.
//
// TWO properties, and the second is why the memo exists at all. The label has to
// quote the numbers the ADMISSION GATE uses — a bar that formatted its own idea of
// the budget would reassure a user about an allowance nothing enforces — and it has
// to cost nothing on the frames where those numbers have not moved, which is all of
// them: drawThemeCatalogRows next door went to some length to build no strings per
// frame, and a bar formatting "%.1f MiB" beside it would put the cost straight back.
func TestThemeArtBudgetBarMemoizesItsLabel(t *testing.T) {
	budget := render.ThemeMediaByteCap(config.TexBudgetDefaultMiB)
	var memo themeArtBarText
	memo.refresh(0, budget, 0, 0)
	first := memo.label
	if first == "" {
		t.Fatal("the bar produced no label for an empty theme — the row is permanent, including at zero")
	}
	if !strings.Contains(first, themeMediaMiB(budget)) {
		t.Errorf("label %q never quotes the live allowance %s — the bar must speak the admission gate's numbers",
			first, themeMediaMiB(budget))
	}

	// Unchanged inputs cost nothing. AllocsPerRun is the whole claim rather than a
	// proxy for it: the rebuild goes through fmt.Sprintf, so a memo that quietly
	// stopped memoizing could not possibly report zero here.
	memo.refresh(0, budget, 0, 0)
	if memo.label != first {
		t.Error("the label changed for identical inputs")
	}
	if n := testing.AllocsPerRun(200, func() { memo.refresh(0, budget, 0, 0) }); n != 0 {
		t.Errorf("a settled budget-bar refresh allocates %.1f/op, want 0 — the Theme tab would format a "+
			"string every frame, which is exactly what the catalog rows beside it avoid", n)
	}

	// Any of the four moving rebuilds it. Counts included: "24 / 24 images" is the
	// cap a user hits long before the bytes on a theme full of small badges.
	for _, tc := range []struct {
		name          string
		bytes, budget int64
		images, gens  int
	}{
		{"bytes", 1 << 20, budget, 0, 0},
		{"budget", 0, budget / 2, 0, 0},
		{"images", 0, budget, 3, 0},
		{"tiles", 0, budget, 0, 5},
	} {
		var m themeArtBarText
		m.refresh(0, budget, 0, 0)
		base := m.label
		m.refresh(tc.bytes, tc.budget, tc.images, tc.gens)
		if m.label == base {
			t.Errorf("moving %s did not change the label (%q) — the row would show a stale number", tc.name, base)
		}
	}

	// The danger threshold is a percentage of the LIVE budget, not a fixed byte
	// count, so it moves with the user's own texture budget.
	if themeArtOverWarnPct(budget*(themeArtWarnPct-10)/100, budget) {
		t.Error("the bar turned danger well under the warn threshold")
	}
	if !themeArtOverWarnPct(budget*(themeArtWarnPct+5)/100, budget) {
		t.Errorf("the bar did not turn danger past %d%% of the allowance", themeArtWarnPct)
	}
	// A zero budget cannot be "over" anything: that arm is reached before prefs load.
	if themeArtOverWarnPct(1<<20, 0) {
		t.Error("a zero budget reported over-warn — the row is drawn before preferences resolve")
	}
}

// pixelsForThemeItemCap is the square edge whose RGBA payload is at most the
// per-item allowance.
func pixelsForThemeItemCap(texBudgetMiB int) int {
	n := render.ThemeMediaItemByteCap(texBudgetMiB)
	side := 1
	for int64((side+1)*(side+1))*4 <= n {
		side++
	}
	return side
}
