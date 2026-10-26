package ui

// THE PRESET GALLERY'S GATES (v1.90.0 W9).
//
// Three properties, and the third is the one hard rule 11 asks for by name: the
// panel must DRIVE the merge engine rather than re-implement a corner of it. The
// sidecar [overrides] tier shipped parsed-but-never-applied through four waves
// behind a mirror-test, and the model for what counts instead is
// TestThemeApplyRunsTheOverridesTier — a test that performs the real act and then
// looks at what the real act changed.

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// stageGalleryEditor opens a themed courtroom, an editor on a sidecar, and the
// gallery — the state every act below starts from.
func stageGalleryEditor(t testing.TB) (*App, func()) {
	t.Helper()
	a, cleanup := stageThemedCourtroom(t)
	sc := theme.NewSidecar()
	sc.Meta.Name = "Mine"
	sc.Elements = []theme.Element{{
		ID: "mine_badge", Kind: theme.ElemShape, Shape: "pill",
		Anchor: "viewport", Fill: theme.RGBA{R: 9, G: 9, B: 9, A: 255},
	}}
	if err := applyFixtureSidecar(a, sc); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if !a.openThemeEditor(ScreenSettings) {
		cleanup()
		t.Fatal("the editor did not open on a theme that has a sidecar")
	}
	a.openThemeGallery()
	if a.gallery() == nil {
		cleanup()
		t.Fatal("the gallery did not open — the library failed to load, or the editor was not there")
	}
	return a, cleanup
}

// ---------------------------------------------------------------------------
// The bounded miniature cache
// ---------------------------------------------------------------------------

// TestPresetThumbCacheIsBounded is hard rule 4 for this wave's one new cache. The
// gallery is 294 cells and hover-scrub walks them, so a cache that grew on demand
// would hold 294 baked miniatures after a minute of idle mousing — which is the
// shape of every unbounded cache that has ever shipped by accident.
//
// It drives get() with every combination, which is also the only honest way to
// prove the LRU never corrupts an entry: after the flood, a re-ask must return the
// right miniature for the pair, not whichever one happened to be in that slot.
func TestPresetThumbCacheIsBounded(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	var c presetThumbCache
	for _, l := range lib.Layouts() {
		for _, s := range lib.Styles() {
			th := c.get(l, s)
			if th == nil {
				t.Fatalf("%s x %s baked nothing", l.ID, s.ID)
			}
			if th.layout != l.ID || th.style != s.ID {
				t.Fatalf("asked for %s x %s, got %s x %s — the LRU handed back another pair's entry",
					l.ID, s.ID, th.layout, th.style)
			}
			if c.n > presetThumbCap {
				t.Fatalf("the cache holds %d entries, cap is %d", c.n, presetThumbCap)
			}
		}
	}
	if c.n != presetThumbCap {
		t.Fatalf("after %d combinations the cache holds %d entries, want the cap (%d) — "+
			"either it is not filling or it is not reusing", lib.Combinations(), c.n, presetThumbCap)
	}
	// A HIT MUST NOT GROW IT, and must not re-bake: the same pointer back is the
	// cheap proof that the second ask found the first ask's entry.
	l, s := lib.Layouts()[0], lib.Styles()[0]
	first := c.get(l, s)
	before := c.n
	if second := c.get(l, s); second != first || c.n != before {
		t.Fatal("re-asking for a cached pair re-baked it")
	}
}

// TestPresetThumbCacheEvictsTheLeastRecentlyUsed is the OTHER half of the cache's
// claim, and it exists because the boundedness gate above does not imply it: a
// cache that always evicted slot 0 is just as bounded and just as correct, and it
// passes that test unchanged (measured — the mutation was run).
//
// It matters for the one interaction the panel is built around. Hover-scrub walks
// a column of 14 or 21 and comes back; under always-evict-slot-0 the entry you
// started from is the first thing thrown away, so scrubbing back up re-bakes every
// card. LRU is what makes the second pass free.
func TestPresetThumbCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	styles := lib.Styles()
	layouts := lib.Layouts()
	if len(layouts)*len(styles) <= presetThumbCap {
		t.Skip("the library is smaller than the cache, so nothing is ever evicted")
	}
	// Fill it exactly, walking one style down the layout axis.
	var c presetThumbCache
	pairAt := func(i int) (*theme.Preset, *theme.Preset) {
		return layouts[i%len(layouts)], styles[(i/len(layouts))%len(styles)]
	}
	for i := 0; i < presetThumbCap; i++ {
		l, s := pairAt(i)
		c.get(l, s)
	}
	// Touch entry 0 so it becomes the MOST recent, then push one more in. The
	// victim must be entry 1 (now the oldest), never entry 0.
	l0, s0 := pairAt(0)
	l1, s1 := pairAt(1)
	c.get(l0, s0)
	lN, sN := pairAt(presetThumbCap)
	c.get(lN, sN)

	if !c.holds(l0, s0) {
		t.Fatal("the most recently used entry was evicted — the cache is not an LRU, and scrubbing back up re-bakes")
	}
	if c.holds(l1, s1) {
		t.Fatal("the LEAST recently used entry survived an eviction — something else was thrown away instead")
	}
	if c.n != presetThumbCap {
		t.Fatalf("the cache holds %d entries after an eviction, want %d", c.n, presetThumbCap)
	}
}

// TestTheThumbnailShowsTheLayoutItIsFor pins that the miniature is derived from
// the fragments rather than being decorative furniture. Two different layouts must
// produce different box sets, and the style must decide the colours — otherwise
// every card in the gallery looks the same and the panel is a list with a picture
// of nothing beside it.
func TestTheThumbnailShowsTheLayoutItIsFor(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	classic, err := lib.Find(theme.PresetLayout, "classic_default")
	if err != nil {
		t.Fatalf("fixture layout: %v", err)
	}
	wide, err := lib.Find(theme.PresetLayout, "widescreen_21_9")
	if err != nil {
		t.Fatalf("fixture layout: %v", err)
	}
	var c presetThumbCache
	a, b := *c.get(classic, lib.Styles()[0]), *c.get(wide, lib.Styles()[0])
	if a.n == 0 || b.n == 0 {
		t.Fatalf("a miniature has no boxes (%d, %d) — the layout's [overrides] never reached it", a.n, b.n)
	}
	same := a.n == b.n
	for i := 0; same && i < a.n; i++ {
		same = a.box[i] == b.box[i]
	}
	if same {
		t.Fatal("two different layouts baked the same miniature — the card cannot tell them apart")
	}

	// The style decides the colour. Two styles over one layout must differ in the
	// ground or the ink, or the whole right-hand axis is invisible in the preview.
	var c2 presetThumbCache
	x := *c2.get(classic, mustFindStyle(t, lib, "terminal"))
	y := *c2.get(classic, mustFindStyle(t, lib, "vaporwave"))
	if x.wash == y.wash && x.ink == y.ink {
		t.Fatalf("terminal and vaporwave bake the same colours (%v/%v) — the [palette] never reached the card",
			x.wash, x.ink)
	}
	// And the BOXES must be identical, because the layout is: a style that moved a
	// miniature's boxes would mean the axes are not disjoint after all.
	if x.n != y.n {
		t.Fatalf("one layout baked %d boxes under one style and %d under another", x.n, y.n)
	}
	for i := 0; i < x.n; i++ {
		if x.box[i].x != y.box[i].x || x.box[i].y != y.box[i].y ||
			x.box[i].w != y.box[i].w || x.box[i].h != y.box[i].h {
			t.Fatalf("box %d moved when only the STYLE changed — the two axes are not disjoint", i)
		}
	}
}

// ---------------------------------------------------------------------------
// The attribution (§6.5 clause 7's user-visible half)
// ---------------------------------------------------------------------------

// TestTheGallerySurfacesEveryShippedCredit is clause 7 read as a requirement on
// the CLIENT rather than on the data. internal/theme already proves every style
// fragment states a credit; a claim that nothing here is copied is worth nothing
// if the only reader is a test, and this preset library is a redistributable
// artefact under AGPLv3 whose attribution has to reach the person redistributing
// it — which is exactly the person standing in front of the gallery.
//
// It drives galleryCredit.lines, the one producer the panel draws from, over all
// fourteen shipped styles at the real panel width, and it looks for the fragment's
// own opening words in what came back.
func TestTheGallerySurfacesEveryShippedCredit(t *testing.T) {
	a, cleanup := stageGalleryEditor(t)
	defer cleanup()

	const previewW = editGalleryPanelW - editGalleryListW*2 - editRowPad*4
	for _, style := range a.gallery().lib.Styles() {
		var gc galleryCredit
		lines := gc.lines(a.ctx, style, previewW)
		if len(lines) == 0 {
			t.Errorf("style %s puts no credit on screen — clause 7's statement reaches nobody", style.ID)
			continue
		}
		// The FIRST WORD of the fragment's own credit must be the first word drawn,
		// which is what distinguishes "the credit is displayed" from "some text is".
		want := strings.Fields(style.Credit)
		got := strings.Fields(strings.Join(lines, " "))
		if len(got) == 0 || got[0] != want[0] {
			t.Errorf("style %s draws %q, its credit starts %q", style.ID, strings.Join(lines, " "), want[0])
		}
		if len(lines) > presetCreditLineCap {
			t.Errorf("style %s wrapped to %d lines, cap is %d", style.ID, len(lines), presetCreditLineCap)
		}
	}

	// AND THE PANEL'S OWN DRAW REACHES IT. Everything above drives the producer,
	// which proves the text exists and says nothing about whether anyone paints it —
	// deleting the loop in drawGalleryPreview passes all of it (measured; the
	// mutation was run). The panel's baked credit carries WHOSE credit it is, so one
	// real draw with a known style picked is the non-mirror evidence: after it, the
	// panel's own state names that style, and it can only have got there through the
	// draw calling lines().
	g := a.gallery()
	g.styleIdx = presetIndexOf(g.lib.Styles(), "terminal")
	g.layoutIdx = presetIndexOf(g.lib.Layouts(), "classic_default")
	g.credit = galleryCredit{}
	a.drawThemeGallery(frameHarnessW, frameHarnessH)
	if g.credit.style != "terminal" {
		t.Fatalf("after a real gallery draw the panel's baked credit is for %q, want \"terminal\" — "+
			"drawGalleryPreview never asks for the attribution, so §6.5 clause 7's statement is "+
			"in the data and on nobody's screen", g.credit.style)
	}
}

// TestTheCreditWrapsOnChangeAndNotPerFrame is the alloc-discipline half. The panel
// draws inside the frame editorChromeAllocBudget bounds, and Ctx.WrapText
// allocates — so a credit rewrapped every frame is a per-frame string built in a
// loop, the regression that budget is named for. The seam is the bake counter
// (galleryCredit.bakes); the property is that redrawing does not move it and
// changing the input does.
func TestTheCreditWrapsOnChangeAndNotPerFrame(t *testing.T) {
	a, cleanup := stageGalleryEditor(t)
	defer cleanup()

	lib := a.gallery().lib
	one, two := mustFindStyle(t, lib, "terminal"), mustFindStyle(t, lib, "vaporwave")
	var gc galleryCredit
	gc.lines(a.ctx, one, 240)
	after := gc.bakes
	for i := 0; i < 8; i++ {
		gc.lines(a.ctx, one, 240)
	}
	if gc.bakes != after {
		t.Fatalf("eight redraws of one credit cost %d rewraps — the panel wraps per frame",
			gc.bakes-after+1)
	}
	if gc.lines(a.ctx, two, 240); gc.bakes != after+1 {
		t.Fatal("hovering a different style did not rewrap — the panel would show the previous style's credit")
	}
	if gc.lines(a.ctx, two, 200); gc.bakes != after+2 {
		t.Fatal("a narrower panel did not rewrap — the lines would overflow the column they were cut to")
	}
}

// TestOnlyTheStyleAxisCarriesACredit pins the asymmetry the panel's draw assumes.
// A layout fragment is rects, and rects have no provenance to state; if a layout
// ever grew one, the preview would be silently dropping it.
func TestOnlyTheStyleAxisCarriesACredit(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	for _, l := range lib.Layouts() {
		if l.Credit != "" {
			t.Errorf("layout %s carries a credit the gallery never draws: %q", l.ID, l.Credit)
		}
	}
	for _, s := range lib.Styles() {
		if s.Credit == "" {
			t.Errorf("style %s carries no credit — there is nothing for the panel to show", s.ID)
		}
	}
}

func mustFindStyle(t testing.TB, lib *theme.PresetLibrary, id string) *theme.Preset {
	t.Helper()
	p, err := lib.Find(theme.PresetStyle, id)
	if err != nil {
		t.Fatalf("fixture style %s: %v", id, err)
	}
	return p
}

// ---------------------------------------------------------------------------
// Apply and undo
// ---------------------------------------------------------------------------

// TestPresetApplyThenUndoRestoresExactly is the wave plan's own gate name, and
// the property it pins is the one that makes a gallery safe to touch: pressing
// Apply on a theme you have been editing is REVERSIBLE, exactly, including the
// parts of the model the panel never mentions.
//
// It compares the SERIALISED theme before and after, not a field list. A field
// list is a second place that has to know what a preset can change, and it would
// have gone stale the first time the merge learned to touch a new section.
func TestPresetApplyThenUndoRestoresExactly(t *testing.T) {
	a, cleanup := stageGalleryEditor(t)
	defer cleanup()

	sc := a.editorSidecar()
	before, err := sc.Bytes()
	if err != nil {
		t.Fatalf("serialise the pristine theme: %v", err)
	}
	beforeStr := string(before)

	g := a.gallery()
	g.layoutIdx = presetIndexOf(g.lib.Layouts(), "split_triptych")
	g.styleIdx = presetIndexOf(g.lib.Styles(), "vaporwave")
	a.applyGalleryPick()

	if !g.undo.Valid() {
		t.Fatal("Apply left nothing to undo")
	}
	if sc.Meta.StylePreset != "vaporwave" || sc.Meta.LayoutPreset != "split_triptych" {
		t.Fatalf("Apply did not stamp the tiers: layout=%q style=%q", sc.Meta.LayoutPreset, sc.Meta.StylePreset)
	}
	after, err := sc.Bytes()
	if err != nil {
		t.Fatalf("serialise the applied theme: %v", err)
	}
	if string(after) == beforeStr {
		t.Fatal("Apply changed nothing — the gate below would pass for the wrong reason")
	}

	a.undoGalleryPick()
	back, err := sc.Bytes()
	if err != nil {
		t.Fatalf("serialise the undone theme: %v", err)
	}
	if string(back) != beforeStr {
		t.Fatalf("undo did not restore the theme exactly.\n--- before ---\n%s\n--- after undo ---\n%s",
			beforeStr, string(back))
	}
	if g.undo.Valid() {
		t.Error("the snapshot survived its own restore — undoing twice would take back an edit made after it")
	}
}

// TestApplyingAPresetSurvivesTheSaveThatFollowsIt is the arm every other apply
// gate in this file stops one step short of.
//
// The button path is Apply → Save → next session, and the SAVE is where the model
// and the lossless carrier get to disagree: the writer edits the document the
// reader kept, so the tier a preset REPLACED stayed in the file as a set of
// orphan [element.*] sections and came back on the next read. Measured through
// exactly this fixture before the fix: 35 elements out, 36 back, with
// `mine_badge` — the element the apply had just dropped — still in the bytes.
// TestGalleryPickRunsTheMergeEngine asserts it is gone from the MODEL and never
// serialises, and TestPresetApplyThenUndoRestoresExactly compares two byte
// strings that both carried it, so it cancelled out of both.
func TestApplyingAPresetSurvivesTheSaveThatFollowsIt(t *testing.T) {
	a, cleanup := stageGalleryEditor(t)
	defer cleanup()

	sc := a.editorSidecar()
	// The theme has been SAVED once, which is what puts the author's own element
	// into the carrier. Applying to a theme whose document is still empty is the one
	// case where a stale section cannot exist.
	if _, err := sc.Bytes(); err != nil {
		t.Fatalf("the pristine theme does not serialise: %v", err)
	}

	g := a.gallery()
	g.layoutIdx = presetIndexOf(g.lib.Layouts(), "classic_default")
	g.styleIdx = presetIndexOf(g.lib.Styles(), "cyberpunk")
	a.applyGalleryPick()

	raw, err := sc.Bytes()
	if err != nil {
		t.Fatalf("serialise the applied theme: %v", err)
	}
	if strings.Contains(string(raw), "[element.mine_badge]") {
		t.Error("the theme's own element is still a section in the saved file after its tier was " +
			"replaced — the next read hands it back")
	}
	back, err := theme.ParseSidecar(raw)
	if err != nil {
		t.Fatalf("the theme the gallery just saved does not read back: %v", err)
	}
	if len(back.Elements) != len(sc.Elements) {
		t.Fatalf("the model holds %d elements and the file it saved reads back %d — every re-style "+
			"grows the theme by a whole style, and two of them put it past theme.ElementCap (%d)",
			len(sc.Elements), len(back.Elements), theme.ElementCap)
	}
	for i := range sc.Elements {
		if back.Elements[i].ID != sc.Elements[i].ID {
			t.Fatalf("element %d reloaded as %q, the model says %q", i, back.Elements[i].ID, sc.Elements[i].ID)
		}
	}
	// The sections a tier owns beyond the elements leak the same way, and the counts
	// are the only place a person would ever see it.
	if len(back.Effects) != len(sc.Effects) {
		t.Errorf("the model binds %d effects, the file reads back %d", len(sc.Effects), len(back.Effects))
	}
	if len(back.Overrides) != len(sc.Overrides) {
		t.Errorf("the model holds %d overrides, the file reads back %d", len(sc.Overrides), len(back.Overrides))
	}

	// AND A SECOND RE-STYLE ON TOP, which is where the leak stopped being cosmetic:
	// the union of two styles was 97 elements against a cap of 96, so the reader
	// REFUSED the theme its own author had just saved.
	a.themeSidecar = sc
	g.styleIdx = presetIndexOf(g.lib.Styles(), "thh_courtroom")
	a.applyGalleryPick()
	raw, err = sc.Bytes()
	if err != nil {
		t.Fatalf("serialise the re-styled theme: %v", err)
	}
	if _, err := theme.ParseSidecar(raw); err != nil {
		t.Fatalf("a second re-style produced a theme the reader refuses: %v", err)
	}
}

// TestGalleryApplyKeepsTheLiveSidecarPointer is the evaporation guard. The
// courtroom's renderer and the editor's document are the SAME *theme.Sidecar; an
// apply that swapped the pointer would leave one of them on the old theme, and
// the symptom is an editor that saves something the screen never showed.
func TestGalleryApplyKeepsTheLiveSidecarPointer(t *testing.T) {
	a, cleanup := stageGalleryEditor(t)
	defer cleanup()

	live := a.editorSidecar()
	if a.themeSidecar != live {
		t.Fatal("the fixture already has two sidecars — this proves nothing")
	}
	a.gallery().styleIdx = presetIndexOf(a.gallery().lib.Styles(), "terminal")
	a.applyGalleryPick()
	if a.editorSidecar() != live || a.themeSidecar != live {
		t.Fatal("Apply replaced the live sidecar pointer — the editor and the courtroom now hold different themes")
	}
	a.undoGalleryPick()
	if a.editorSidecar() != live || a.themeSidecar != live {
		t.Fatal("Undo replaced the live sidecar pointer")
	}
}

// TestGalleryPickRunsTheMergeEngine is the ENCAPSULATION gate (hard rule 11), and
// it is written in the shape TestThemeApplyRunsTheOverridesTier established:
// perform the real act through the real button path, then assert on something only
// the real engine could have produced.
//
// What it looks for is a preset's OWN element, by id, in the live model — a thing
// the gallery has no way to invent, because the ids come out of the embedded
// fragment. A panel that had re-implemented "apply a style" as, say, copying the
// palette across would pass every other test in this file and fail this one.
func TestGalleryPickRunsTheMergeEngine(t *testing.T) {
	a, cleanup := stageGalleryEditor(t)
	defer cleanup()

	g := a.gallery()
	style := mustFindStyle(t, g.lib, "terminal")
	if len(style.Side.Elements) == 0 {
		t.Fatal("the fixture style declares no elements — this would prove nothing")
	}
	g.styleIdx = presetIndexOf(g.lib.Styles(), style.ID)
	g.layoutIdx = presetIndexOf(g.lib.Layouts(), "classic_default")
	a.applyGalleryPick()

	sc := a.editorSidecar()
	for i := range style.Side.Elements {
		want := style.Side.Elements[i]
		got, ok := sc.FindElement(want.ID)
		if !ok {
			t.Fatalf("element %q is in the fragment and not in the theme — the apply did not run the merge", want.ID)
		}
		if *got != want {
			t.Fatalf("element %q differs from the fragment's own row:\n have %+v\n want %+v", want.ID, *got, want)
		}
	}
	// The LAYOUT half, through the same act: an [overrides] row only the layout
	// fragment could have supplied.
	layout, err := g.lib.Find(theme.PresetLayout, "classic_default")
	if err != nil {
		t.Fatalf("fixture layout: %v", err)
	}
	for _, o := range layout.Side.Overrides {
		got, ok := sc.OverrideRect(o.Key)
		if !ok || got != o.Rect {
			t.Fatalf("override %q is %v (present=%v), the fragment says %v", o.Key, got, ok, o.Rect)
		}
	}
	// And the theme's OWN element is gone, because the style tier was replaced —
	// which is the half of the contract a copy-the-palette shortcut would also miss.
	if _, ok := sc.FindElement("mine_badge"); ok {
		t.Error("the theme's own shape element survived a style apply — the tier was not replaced")
	}
}

// TestTheGalleryIsAPanelAndNotAModal pins the two halves of the editor's fence
// that a new panel has to join, both of which were holes the Fonts panel already
// fell into once: editorPanelUp must see it (so the rails behind it go
// click-dead) and closeEditorPanels must close it (so Esc does not walk past it).
func TestTheGalleryIsAPanelAndNotAModal(t *testing.T) {
	a, cleanup := stageGalleryEditor(t)
	defer cleanup()

	if !a.editorPanelUp() {
		t.Fatal("editorPanelUp does not see the gallery — the rails behind it stay live and a click hits both")
	}
	a.closeEditorPanels()
	if a.gallery() != nil {
		t.Fatal("closeEditorPanels left the gallery up — Esc walks past it")
	}
	if a.editorPanelUp() {
		t.Fatal("the fence is still raised with no panel on screen")
	}
}
