package ui

// The W4 END-TO-END gate: a hand-authored sidecar that declares FILES and
// GENERATORS goes all the way from testdata to a resolved texture page on a baked
// element, headlessly.
//
// The unit gates around it each prove one link — the store's ceiling
// (render/thememedia_test.go), the planner's refusals (thememedia_test.go), the
// rasterisers' determinism (gentex_test.go). None of them proves the CHAIN, and the
// chain is where this feature actually lives: a key spelled one way by the planner
// and another by the bake, a plan landed after the canvases were invalidated, a
// generator deduplicated into a tile nobody uploaded — every one of those passes
// every unit gate and ships a theme that renders nothing.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// w4MediaFixture is the hand-authored media sidecar. Named so a grep for it finds
// both the file and every test that leans on it.
const w4MediaFixture = "w4_media.ini"

// writeThemePNG puts a real, decodable PNG inside a theme folder.
//
// REAL, not a stub: loadThemeMediaSet runs assets.DecodeImage on it, and a fixture
// of arbitrary bytes would take the "could not be decoded" arm and pass an
// end-to-end test by never reaching the end.
func writeThemePNG(t *testing.T, dir, rel string, side int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 0x40, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding the fixture PNG: %v", err)
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stageW4MediaTheme builds the fixture theme on disk (the sidecar's three declared
// files, one of which the sidecar lies about the size of), plans it, decodes it and
// lands it — the whole producer path a theme apply runs, minus the goroutine.
func stageW4MediaTheme(t *testing.T) (*App, *theme.Sidecar, themeMediaPlan, func()) {
	t.Helper()
	dir := t.TempDir()
	writeThemePNG(t, dir, "art/wall.png", 16)
	writeThemePNG(t, dir, "art/plate.png", 12)
	// `huge` is on disk and TINY. The sidecar declares it at 256 MiB, which is what
	// the planner budgets against — an author's declaration is what lets a plan
	// refuse before it decodes a stranger's file, and this fixture is the case that
	// proves the declaration is actually the input.
	writeThemePNG(t, dir, "art/huge.png", 8)

	sc := loadW3Sidecar(t, w4MediaFixture)
	plan := planThemeMedia(sc, "w4fixture", dir, config.TexBudgetDefaultMiB)

	res := themeApply{mediaPlan: plan}
	res.loadThemeMediaSet(false)

	a, cleanup := stageThemedCourtroom(t)
	a.themeSidecar = sc
	a.landThemeMedia(&res)
	a.themeLay.valid = false
	a.drawCourtroom(1280, 720)
	if !a.themeLay.valid {
		cleanup()
		t.Fatal("the media fixture never reached the themed branch — nothing was baked")
	}
	return a, sc, a.themeMediaPlan, cleanup
}

// elemIndexByID maps a sidecar element id to its declaration index, which is the
// index the plan's ElemKeys are addressed by.
func elemIndexByID(sc *theme.Sidecar, id string) int {
	for i := range sc.Elements {
		if sc.Elements[i].ID == id {
			return i
		}
	}
	return -1
}

// bakedByID finds the BAKED row for a declared element id. The bake sorts by
// (band, z, declaration order), so the baked index is not the declared one — and
// bakedElement carries no id, so this goes through the rect the fixture gave it.
func bakedByID(t *testing.T, a *App, sc *theme.Sidecar, id string) *bakedElement {
	t.Helper()
	di := elemIndexByID(sc, id)
	if di < 0 {
		t.Fatalf("%q is not in the fixture", id)
	}
	src := &sc.Elements[di]
	lay := &a.themeLay
	want, ok := a.resolveElementRect(lay, sc, src)
	if !ok {
		t.Fatalf("element %q resolved to no rect — it is inert, so nothing below can be measured", id)
	}
	for i := 0; i < lay.elN; i++ {
		if lay.el[i].r == want && lay.el[i].kind == src.Kind {
			return &lay.el[i]
		}
	}
	t.Fatalf("element %q (%v) is not in the baked array", id, want)
	return nil
}

// TestHandAuthoredMediaSidecarRendersEndToEnd walks testdata/sidecars/w4_media.ini
// from disk to a page pointer on a baked element.
func TestHandAuthoredMediaSidecarRendersEndToEnd(t *testing.T) {
	a, sc, plan, cleanup := stageW4MediaTheme(t)
	defer cleanup()

	// --- the plan: four declarations, three outcomes ------------------------
	if len(plan.Entries) != len(sc.Media) {
		t.Fatalf("the plan holds %d entries for %d [media] rows — an entry was dropped", len(plan.Entries), len(sc.Media))
	}
	wantState := map[string]themeMediaState{
		"wall": mediaPlanned, "plate": mediaPlanned,
		"huge": mediaOverBudget, "ghost": mediaMissing,
	}
	for _, e := range plan.Entries {
		if want, known := wantState[e.ID]; !known {
			t.Errorf("the plan invented an entry %q", e.ID)
		} else if e.State != want {
			t.Errorf("[media] %q planned as state %d, want %d (%s)", e.ID, e.State, want, e.Why)
		}
	}

	// --- the generators: content-addressed, deduplicated, unknown-safe -------
	//
	// Four `kind = gen` elements are declared. `discbadge` and `disctwin` carry
	// byte-identical params, so they ARE one tile — that is what the hash is for and
	// it is the difference between 12 tiles of budget and 12 elements of budget.
	// `futuretile` names a generator this build has never heard of, which costs no
	// tile at all.
	if len(plan.Gens) != 3 {
		var names []string
		for _, g := range plan.Gens {
			names = append(names, g.Spec.Name)
		}
		t.Fatalf("the plan holds %d generator tiles %v, want 3 (scanlines, checkerdisc, radial) — "+
			"two identical specs must share one content address and an unknown name must cost nothing",
			len(plan.Gens), names)
	}
	twinA := plan.ElemKey(elemIndexByID(sc, "discbadge"))
	twinB := plan.ElemKey(elemIndexByID(sc, "disctwin"))
	if twinA == "" || twinA != twinB {
		t.Errorf("the two identical generator elements resolved to %q and %q — identical params are one tile", twinA, twinB)
	}
	if got := plan.ElemKey(elemIndexByID(sc, "futuretile")); got != "" {
		t.Errorf("the unknown generator took key %q — an unrecognised name degrades to the element's own fill", got)
	}

	// --- the store: the ledger matches what landed --------------------------
	if got := a.d.Store.ThemeMediaCount(); got != 2 {
		t.Errorf("%d imported pages resident, want 2 (wall + plate)", got)
	}
	if got := a.d.Store.ThemeGenCount(); got != 3 {
		t.Errorf("%d generator tiles resident, want 3", got)
	}
	if a.d.Store.ThemeMediaBytes() <= 0 {
		t.Error("the store's art ledger is empty — nothing was admitted through the gate")
	}
	if budget := render.ThemeMediaByteCap(config.TexBudgetDefaultMiB); a.d.Store.ThemeMediaBytes() > budget {
		t.Errorf("resident art is %d bytes, over the %d allowance", a.d.Store.ThemeMediaBytes(), budget)
	}

	// --- the bake: every declared element exists, art or no art -------------
	if a.themeLay.elN != len(sc.Elements) {
		t.Fatalf("the bake produced %d elements for %d declared — an over-budget or missing one was lost, "+
			"and design §Q2 says the author's work survives a refused file", a.themeLay.elN, len(sc.Elements))
	}
	for _, id := range []string{"wallpaper", "paperfield", "scanwash", "chatframe", "sealcontain", "sealcover", "discbadge", "disctwin", "platewash"} {
		if e := bakedByID(t, a, sc, id); e.page == nil {
			t.Errorf("element %q baked with no page — its art never reached the draw path", id)
		}
	}
	for _, id := range []string{"bigbackdrop", "ghostart", "futuretile"} {
		if e := bakedByID(t, a, sc, id); e.page != nil {
			t.Errorf("element %q resolved a page, but its art was refused/unknown — the painter must draw "+
				"nothing rather than show the wrong thing", id)
		}
	}
	// The twins really do share ONE page object, not two copies of one image.
	if x, y := bakedByID(t, a, sc, "discbadge"), bakedByID(t, a, sc, "disctwin"); x.page != y.page {
		t.Error("the two identical generator elements hold different pages — the content address did not dedupe")
	}
	// The tint reached the baked row: it is the one image-only value with no other
	// spelling, so a reader that dropped it would be invisible everywhere else.
	// Its ALPHA is opaque here because the element declares no opacity.
	if e := bakedByID(t, a, sc, "paperfield"); e.tint.R != 0x80 || e.tint.G != 0x60 || e.tint.B != 0x40 || e.tint.A != 0xFF {
		t.Errorf("the tinted element baked tint %v, want #806040 at full alpha", e.tint)
	}
	// `opacity` on an IMAGE or GEN element has exactly one route to the screen —
	// paintElementArt's SetAlphaMod(tint.A) — so the bake has to fold it into the
	// tint. It did not, and every wash, scrim and vignette in the fourteen shipped
	// themes (all of which set opacity on a gen or image element) drew at full
	// strength with nothing anywhere to say why.
	if e := bakedByID(t, a, sc, "scanwash"); e.tint.A != 60 {
		t.Errorf("the `opacity = 60` generator baked tint alpha %d, want 60 — opacity is silently ignored on "+
			"art elements unless it folds into the tint", e.tint.A)
	}
	// R9's LTRB order, as authored. A reader that transposed it would still draw a
	// frame — just the wrong one — which is exactly why this is pinned by value.
	if e := bakedByID(t, a, sc, "chatframe"); e.slice != [4]int32{6, 4, 10, 12} {
		t.Errorf("the 9-slice insets baked as %v, want [6 4 10 12] (left, top, right, bottom)", e.slice)
	}

	// --- the draw: every element is dispatched, and the frame is clean ------
	var seen []*bakedElement
	restore := captureElementPaints(&seen)
	a.themeLay.valid = false
	a.drawCourtroom(1280, 720)
	restore()
	if len(seen) != len(sc.Elements) {
		t.Errorf("%d painter calls for %d elements — every element paints, including the placeholders",
			len(seen), len(sc.Elements))
	}
	// And the real painters run over the real pages without exploding.
	a.drawCourtroom(1280, 720)
}

// TestThemeMediaSwapPrunesTheOutgoingThemesArt is the producer half of the leak the
// store-side gate pins from below: applying a theme with no art after one with art
// must leave nothing resident.
//
// The prune is keyed on what the OUTGOING apply DECLARED (a.themeMediaKeys), not on
// what the store happens to hold, and that is the only reason it can be right — the
// store cannot tell a page the new theme also declares from one only the old one did.
func TestThemeMediaSwapPrunesTheOutgoingThemesArt(t *testing.T) {
	a, _, _, cleanup := stageW4MediaTheme(t)
	defer cleanup()
	if a.d.Store.ThemeMediaBytes() == 0 {
		t.Fatal("the fixture landed no art — the swap below would prove nothing")
	}

	// A stock AO2 theme: no sidecar, no plan, no media.
	var stock themeApply
	stock.loadThemeMediaSet(false)
	a.themeSidecar = nil
	a.landThemeMedia(&stock)

	if got := a.d.Store.ThemeMediaBytes(); got != 0 {
		t.Errorf("%d bytes of the previous theme's art survived a swap to a stock theme — pinned pages are "+
			"eviction-exempt, so the swap prune is the ONLY thing that frees them", got)
	}
	if n, g := a.d.Store.ThemeMediaCount(), a.d.Store.ThemeGenCount(); n != 0 || g != 0 {
		t.Errorf("%d images / %d tiles still resident after the swap", n, g)
	}
	if len(a.themeMediaKeys) != 0 {
		t.Errorf("the declared-key set still holds %d keys — the next swap would not know to free them", len(a.themeMediaKeys))
	}
}

// ---------------------------------------------------------------------------
// Export parity
// ---------------------------------------------------------------------------

// TestExportDrawsTheSameElementBandsAsTheScreen is the GIF/video parity gate.
//
// It exists because the export composes its own frame (drawGifThemedFrame) rather
// than calling drawCourtroomThemed, so every band the themed pass gained had to be
// wired in by hand — and until W4 none of them were. The symptom is not subtle and
// it is not recoverable after the fact: a native theme's wallpaper, chatbox frame
// and masthead are on the user's screen and absent from every render of the same
// session.
func TestExportDrawsTheSameElementBandsAsTheScreen(t *testing.T) {
	a, _, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()

	const w, h int32 = 1280, 720
	var live []*bakedElement
	restore := captureElementPaints(&live)
	a.drawCourtroom(w, h)
	restore()
	if len(live) == 0 {
		t.Fatal("the live pass painted no elements — the comparison below would be vacuous")
	}

	j := &gifExportJob{room: newRoomForTest(t), chatPct: DefaultScalePct}
	var exported []*bakedElement
	restore = captureElementPaints(&exported)
	a.drawExportScene(j, &j.room.Scene, sdl.Rect{X: 0, Y: 0, W: w, H: h})
	restore()

	if len(exported) != len(live) {
		t.Fatalf("the export painted %d elements, the screen painted %d — an exported frame must show the "+
			"same decoration the player is looking at", len(exported), len(live))
	}
	// PAINT ORDER, not just count. The three bands are the format's promise
	// (docs/THEME-FORMAT.md §4) and the baked array is band-sorted, so a correct pass
	// walks it in strictly increasing index order. A single band call in the wrong
	// place — or two of them transposed — shows up here and nowhere else.
	lay := &a.themeLay
	prev := -1
	for _, e := range exported {
		i := indexOfBaked(lay, e)
		if i < 0 {
			t.Fatal("the export painted an element that is not in the baked array")
		}
		if i <= prev {
			t.Fatalf("the export painted baked index %d after %d — the three band calls are out of order", i, prev)
		}
		prev = i
	}
	// All three bands really are represented, or the test would pass with one call
	// wired and two forgotten.
	for b, rg := range lay.band {
		if rg.hi <= rg.lo {
			continue // the fixture declares nothing in this band
		}
		found := false
		for _, e := range exported {
			if i := int16(indexOfBaked(lay, e)); i >= rg.lo && i < rg.hi {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("band %d holds elements [%d,%d) and the export painted none of them", b, rg.lo, rg.hi)
		}
	}
}

// TestExportConditionsFollowTheExportedRoom pins WHICH room a visible_when resolves
// against in an export.
//
// The live courtroom is still behind the progress overlay with its last message on
// it, so reading a.room would decorate a recording with whatever the session
// happened to be doing — an objection glow over a line nobody objected to. The two
// CLIENT-state axes (the local pos dropdown and character) have no recorded
// counterpart and deliberately still come from the client.
func TestExportConditionsFollowTheExportedRoom(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	a.room.Scene.Position = "wit"
	a.sidePref = "pro"
	j := &gifExportJob{room: newRoomForTest(t)}
	j.room.Scene.Position = "def"

	if live := a.liveCondSource(); live.side != "wit" {
		t.Fatalf("the live source reports side %q, want \"wit\" — the fixture never took", live.side)
	}
	got := a.exportCondSource(j)
	if got.side != "def" {
		t.Errorf("the export resolved side %q, want \"def\" — a stage axis must come from the room being "+
			"exported, not from the session behind the progress overlay", got.side)
	}
	if got.pos != "pro" {
		t.Errorf("the export resolved pos %q, want \"pro\" — pos is the local player's own dropdown and has "+
			"no recorded counterpart", got.pos)
	}
	// A job with no room at all (the abort paths) must not panic and must not invent
	// a stage.
	if empty := a.exportCondSource(nil); empty.side != "" || empty.shout != "" {
		t.Errorf("a nil job resolved stage axes %+v", empty)
	}
}

// TestExportBorrowsTheThemeLayoutCacheWithoutThrashing pins the reasoning written at
// drawExportScene: there is ONE themeLayoutCache and the export builds it with a
// different `top` than the window does, so the only thing standing between that and
// a cold rebuild (a 12 KiB clear plus the rect map plus the element bake) every
// frame is that the two never run in the same frame.
//
// That property lives two files away — App.Frame's full-window precedence, written
// down as chrometop.go's screenDispatchPreempted — so it is pinned here rather than
// left as a comment somebody could invalidate from screens.go without ever opening
// this file.
func TestExportBorrowsTheThemeLayoutCacheWithoutThrashing(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	a.gifExporting = true
	if !a.screenDispatchPreempted() {
		t.Fatal("an export does not preempt the screen dispatch — the courtroom would rebuild the shared " +
			"layout cache every frame the export borrowed it")
	}
	a.gifExporting = false

	const w, h int32 = 640, 480
	j := &gifExportJob{room: newRoomForTest(t), chatPct: DefaultScalePct}
	dst := sdl.Rect{X: 0, Y: 0, W: w, H: h}
	a.drawExportScene(j, &j.room.Scene, dst) // the one cold rebuild per job
	if !a.themeLay.valid || a.themeLay.topOff != 0 {
		t.Fatalf("the export's layout is valid=%v topOff=%d — an export frame reserves no chrome band",
			a.themeLay.valid, a.themeLay.topOff)
	}
	// Every capture after the first is a cache HIT: gifFramesPerTick captures a tick,
	// all at the same dst.
	if n := testing.AllocsPerRun(50, func() { a.themeLayout(w, h) }); n != 0 {
		t.Errorf("a repeated export-sized themeLayout allocates %.1f/op — the captures within one tick are "+
			"rebuilding the cache instead of hitting it", n)
	}
}
