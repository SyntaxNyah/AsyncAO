package ui

// THE EDITOR'S ART SYNC, DRIVEN (v1.90.0 W7b) — themeeditorart.go.
//
// Every behavioural gate here goes through App.Frame and asserts on the page the BAKE
// resolved, never on the plan alone: the defect these close was a plan that was correct
// the moment it was built and stale for the rest of the session, so a gate that read
// back what it had just written would have passed against the broken build.

import (
	"go/ast"
	"os"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

// artRigGenerator / artRigParam are the fixture's generator and the parameter the gates
// edit. Named because two gates compare against them and a typo in one copy would be a
// gate that passes by measuring the wrong element.
const (
	artRigGenerator = "grid"
	artRigParamKey  = "size"
	artRigParamOne  = "12"
	artRigParamTwo  = "31"
)

// stageEditorArt opens the editor over the canvas fixture with ONE extra element: a
// generator box, authored before the first editor frame exactly as the shape boxes are.
//
// It returns the shape's target (index 0 of the pair) and the generator's index.
func stageEditorArt(t *testing.T) (a *App, cleanup func(), shape editTarget, genIdx int) {
	t.Helper()
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	el := editorCanvasElement("genbox", editorCentreRect(a, 0, 0))
	el.Kind = theme.ElemGen
	el.Gen = artRigGenerator
	el.GenParams[0] = theme.KV{Key: artRigParamKey, Value: artRigParamOne}
	a.themeSidecar.Elements = append(a.themeSidecar.Elements, el)
	genIdx = len(a.themeSidecar.Elements) - 1
	driveFrame(a)
	return a, cleanup, tgts[0], genIdx
}

// artKeyOf is the key the element at idx SHOULD resolve to, computed the way the
// planner computes it — through theme.GeneratorSpecOf and render.ThemeGenKey, the two
// published functions, never a second spelling of the hash.
func artKeyOf(a *App, idx int) string {
	return render.ThemeGenKey(theme.GeneratorSpecOf(&a.themeSidecar.Elements[idx]).Hash())
}

// drawnPages is the set of pages the last bake actually put in front of the painter.
// The DRAWN answer, which is the one the defect was about.
func drawnPages(a *App) map[*render.TexturePage]bool {
	out := map[*render.TexturePage]bool{}
	for i := 0; i < a.themeLay.elN; i++ {
		if p := a.themeLay.el[i].page; p != nil {
			out[p] = true
		}
	}
	return out
}

// genTileCount is how many generator tiles the store is holding for this theme.
func genTileCount(a *App) int {
	n := 0
	for key := range a.themeMediaKeys {
		if strings.HasPrefix(key, render.ThemeGenKeyPrefix) {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// The headline: an edited generator changes the picture
// ---------------------------------------------------------------------------

// TestAGeneratorEditMovesTheDrawnPage is the gate the wave's fill rows were missing.
//
// THE DEFECT IT CLOSES. themeelementbake.go resolves art as
// themeMediaPage(themeMediaPlan.ElemKey(idx)), and the plan was written in exactly one
// place — landThemeMedia, from a theme apply. planElements derives those keys from the
// element's kind, media, generator and params, so editing any of them left the OLD key
// in the plan: the Generator and Params rows wrote the file and the box on screen went
// on drawing the previous tile until the user switched themes and back.
//
// It asserts on THREE independent things, because the hole has three shapes: the plan's
// key (what the bake will ask for), the store (whether the new tile exists at all) and
// the set of pages the bake handed the painter (whether anything on screen moved).
func TestAGeneratorEditMovesTheDrawnPage(t *testing.T) {
	a, cleanup, _, genIdx := stageEditorArt(t)
	defer cleanup()

	before := a.themeMediaPlan.ElemKey(genIdx)
	if before == "" || before != artKeyOf(a, genIdx) {
		t.Fatalf("the fixture generator resolved to %q, want its own content address %q — the plan never "+
			"caught up with the document, so the edit below would prove nothing",
			before, artKeyOf(a, genIdx))
	}
	if !a.themeMediaKeys[before] {
		t.Fatalf("the tile for %q was never landed — the editor's own incremental path (landThemeMediaPage) "+
			"is not being driven", before)
	}
	pagesBefore := drawnPages(a)
	if len(pagesBefore) == 0 {
		t.Fatal("no element drew a page at all; this gate cannot see a page move")
	}

	// The EDIT, through the funnel the inspector's Params row uses.
	v := editValueStr(a.te.doc.intern(artRigParamKey + " = " + artRigParamTwo))
	if !a.editorApply(mutateElemField(elementTarget(genIdx), editFieldGenParams, v)) {
		t.Fatal("the document refused the params edit")
	}
	driveFrame(a)

	after := a.themeMediaPlan.ElemKey(genIdx)
	if after == before {
		t.Fatalf("editing gen_params left the drawn key at %q — a generator's key IS its params' content "+
			"address, so an unchanged key means the element is still drawing the tile it had before the "+
			"edit: the row edits the file and nothing else", before)
	}
	if want := artKeyOf(a, genIdx); after != want {
		t.Errorf("the plan points the element at %q, want %q — the re-plan and theme.GeneratorSpecOf "+
			"disagree about the same element", after, want)
	}
	if !a.themeMediaKeys[after] {
		t.Errorf("the new tile %q was never uploaded — the key moved but there is nothing behind it, so "+
			"the element now draws NOTHING, which is worse than the stale tile it replaced", after)
	}
	page, ok := a.d.Store.Get(after)
	if !ok {
		t.Fatalf("the store does not hold %q", after)
	}
	if !drawnPages(a)[page] {
		t.Error("the new tile exists and the plan names it, but the bake never handed it to the painter — " +
			"the re-plan must invalidate the layout, or the picture only changes on the next resize")
	}
}

// TestDeletingAnElementKeepsItsNeighboursArt covers the OTHER half of the same defect,
// and it is the half no field edit can reach.
//
// An element's identity is its INDEX (theme.Element), and the plan's ElemKeys are
// indexed by it. So deleting element 3 renumbers every element after it, and a stale
// plan hands each survivor its PREDECESSOR's art — here, the deleted shape's "no art at
// all", which reads as the generator box going blank for no reason.
func TestDeletingAnElementKeepsItsNeighboursArt(t *testing.T) {
	a, cleanup, shape, genIdx := stageEditorArt(t)
	defer cleanup()

	key := a.themeMediaPlan.ElemKey(genIdx)
	if key == "" {
		t.Fatal("the fixture generator has no art before the delete; there is nothing for it to lose")
	}
	shapeIdx, ok := shape.elemIdx()
	if !ok || shapeIdx >= genIdx {
		t.Fatalf("fixture: the shape must sit BEFORE the generator for the delete to renumber it "+
			"(shape %v, generator %d)", shape, genIdx)
	}

	a.te.selectOnly(shape)
	a.editorDeleteSelection()
	driveFrame(a)

	moved := genIdx - 1
	if got := a.themeMediaPlan.ElemKey(moved); got != key {
		t.Fatalf("after deleting the element in front of it, the generator (now index %d) resolves to %q, "+
			"want its own tile %q — every element after a delete is being handed its predecessor's art",
			moved, got, key)
	}
	page, ok := a.d.Store.Get(key)
	if !ok {
		t.Fatalf("the surviving element's tile %q was pruned by the delete", key)
	}
	if !drawnPages(a)[page] {
		t.Error("the surviving generator stopped drawing its tile after its neighbour was deleted")
	}
}

// TestEditingParamsDoesNotLeakTiles is the cap (hard rule 4) rather than the picture.
//
// A generator's key is the content address of its params, so every keystroke in the
// Params row mints a NEW key. Without the prune, typing a three-digit number would
// upload three 256 KiB tiles and release none, for as long as the editor stays open.
func TestEditingParamsDoesNotLeakTiles(t *testing.T) {
	a, cleanup, _, genIdx := stageEditorArt(t)
	defer cleanup()

	settled := genTileCount(a)
	if settled == 0 {
		t.Fatal("the fixture landed no generator tile at all; a leak gate over zero tiles proves nothing")
	}
	for i := 0; i < 6; i++ {
		v := editValueStr(a.te.doc.intern(artRigParamKey + " = " + string(rune('1'+i)) + "7"))
		if !a.editorApply(mutateElemField(elementTarget(genIdx), editFieldGenParams, v)) {
			t.Fatalf("edit %d was refused", i)
		}
		driveFrame(a)
	}
	if got := genTileCount(a); got != settled {
		t.Errorf("six parameter edits left %d generator tiles in the store, want %d — a tile whose params "+
			"nobody names any more is unreachable art holding texture budget, and the count grows with "+
			"every keystroke", got, settled)
	}
	// …and the tile that is there is the one that is WANTED: a count that holds steady
	// because nothing is ever landed passes the assertion above and draws nothing.
	if want := artKeyOf(a, genIdx); !a.themeMediaKeys[want] {
		t.Errorf("the store does not hold the CURRENT parameters' tile %q — either the prune took it, or "+
			"the last edit never landed one at all", want)
	}
}

// ---------------------------------------------------------------------------
// Leaving: the exit is a move the frame cannot see
// ---------------------------------------------------------------------------

// reopenEditorAtBaseline closes and re-opens the editor so the CURRENT model becomes the
// document's baseline.
//
// The art fixture authors its generator after stageEditorCanvas has already opened the
// editor (which is what makes it an element the gates can edit), so it is not in that
// document's baseline — and a discard gate over an element the baseline never had would
// be measuring a DELETE, not a revert. Two production doors, no second construction path.
func reopenEditorAtBaseline(t *testing.T, a *App) {
	t.Helper()
	from := a.editorReturn
	a.closeThemeEditor()
	if !a.openThemeEditor(from) {
		t.Fatal("re-opening the editor over the same theme was refused")
	}
	driveFrame(a)
}

// TestDiscardingOnExitPutsTheBaselineArtBack is the exit direction of the wave's defect,
// and it is the one site themeeditorart.go's header named as covered and was not.
//
// THE DEFECT IT CLOSES. requestEditorExit's second Back restores the baseline MODEL and
// invalidates the geometry caches — but a.te = nil ends the editor's frame, and the frame
// was the only thing that ever compared the plan's signature against the document. So the
// courtroom came back holding the EDITED plan: the generator drew the tile the discarded
// edit had asked for, and because pruneEditorGenTiles had already released the baseline's
// orphaned tile, the element could draw NOTHING until a theme switch rebuilt it. Discard
// is the one action whose whole promise is "put it back", so this is the worst place in
// the editor for the model and the picture to disagree.
//
// It asserts through the BAKE after real frames, not on the plan alone: the plan is what
// was wrong, so a gate that only read the plan back would be arguing with itself.
func TestDiscardingOnExitPutsTheBaselineArtBack(t *testing.T) {
	a, cleanup, _, genIdx := stageEditorArt(t)
	defer cleanup()
	reopenEditorAtBaseline(t, a)

	baseKey := a.themeMediaPlan.ElemKey(genIdx)
	if baseKey == "" || baseKey != artKeyOf(a, genIdx) {
		t.Fatalf("the fixture generator resolved to %q, want its own content address %q — there is no "+
			"baseline art for the discard to put back", baseKey, artKeyOf(a, genIdx))
	}

	// The EDIT, through the funnel the inspector's Params row uses…
	v := editValueStr(a.te.doc.intern(artRigParamKey + " = " + artRigParamTwo))
	if !a.editorApply(mutateElemField(elementTarget(genIdx), editFieldGenParams, v)) {
		t.Fatal("the document refused the params edit")
	}
	driveFrame(a)
	edited := a.themeMediaPlan.ElemKey(genIdx)
	if edited == baseKey {
		t.Fatal("the edit never moved the drawn key, so a discard cannot move it back — this gate would " +
			"pass against a build that does nothing at all")
	}

	// …and the exit that throws it away. The arming press first (work is never discarded
	// by one keystroke), then the press inside its window that discards.
	a.requestEditorExit()
	if a.te == nil {
		t.Fatal("the arming press left the editor")
	}
	a.requestEditorExit()
	if a.te != nil {
		t.Fatal("the second Back press did not leave")
	}
	driveFrame(a)

	if want := artKeyOf(a, genIdx); want != baseKey {
		t.Fatalf("the discard left the MODEL at %q rather than the baseline %q — the exit contract itself "+
			"is broken and the art below is the smaller half of it", want, baseKey)
	}
	if got := a.themeMediaPlan.ElemKey(genIdx); got != baseKey {
		t.Fatalf("after Back-Back the model is the baseline but the plan still points the element at %q, "+
			"want %q — the courtroom is drawing the art of an edit the user just threw away", got, baseKey)
	}
	page, ok := a.d.Store.Get(baseKey)
	if !ok {
		t.Fatalf("the baseline tile %q is not in the store — the edit's re-plan pruned it as an orphan and "+
			"the exit never landed it again, so the restored element draws nothing at all until the user "+
			"switches themes and back", baseKey)
	}
	if !drawnPages(a)[page] {
		t.Error("the baseline tile exists and the plan names it, but the bake never handed it to the " +
			"painter — the exit must invalidate the layout as well as re-plan")
	}
	if a.themeMediaKeys[edited] {
		t.Errorf("the discarded edit's tile %q is still in the store — nothing names it any more, so it is "+
			"unreachable art holding texture budget for the rest of the session", edited)
	}
}

// TestDiscardingADeleteOnExitPutsTheNeighboursArtBack is the same exit through the
// index-shift half of the defect, which no field edit can reach.
//
// An element's identity is its INDEX, so deleting the shape in front of the generator
// renumbers it — and undoing that by discarding renumbers it BACK. A plan left over from
// the deleted state hands the restored shape the generator's tile and the generator
// nothing, which is two elements drawing each other's art after an exit that promised to
// change nothing.
func TestDiscardingADeleteOnExitPutsTheNeighboursArtBack(t *testing.T) {
	a, cleanup, shape, genIdx := stageEditorArt(t)
	defer cleanup()
	reopenEditorAtBaseline(t, a)

	key := a.themeMediaPlan.ElemKey(genIdx)
	shapeIdx, ok := shape.elemIdx()
	if key == "" || !ok || shapeIdx >= genIdx {
		t.Fatalf("fixture: want a generator with art (%q) sitting after the shape (shape %v, generator %d)",
			key, shape, genIdx)
	}
	n := len(a.themeSidecar.Elements)

	a.te.selectOnly(shape)
	a.editorDeleteSelection()
	driveFrame(a)
	if len(a.themeSidecar.Elements) != n-1 {
		t.Fatalf("the delete did not remove an element (%d, want %d)", len(a.themeSidecar.Elements), n-1)
	}

	a.requestEditorExit()
	a.requestEditorExit()
	if a.te != nil {
		t.Fatal("Back-Back did not leave the editor")
	}
	driveFrame(a)

	if len(a.themeSidecar.Elements) != n {
		t.Fatalf("the discard restored %d elements, want the %d the editor opened over",
			len(a.themeSidecar.Elements), n)
	}
	if got := a.themeMediaPlan.ElemKey(genIdx); got != key {
		t.Fatalf("the restored generator at index %d resolves to %q, want its own tile %q — the plan is "+
			"still numbered for the deleted state, so every element after the deletion draws its "+
			"neighbour's art", genIdx, got, key)
	}
	page, ok := a.d.Store.Get(key)
	if !ok {
		t.Fatalf("the generator's tile %q is gone after a discarded delete", key)
	}
	if !drawnPages(a)[page] {
		t.Error("the generator stopped drawing its tile after the delete that removed its neighbour was " +
			"discarded")
	}
}

// TestTheOnlyPlaceTheEditorIsDroppedSyncsTheArt is the deletion-catcher behind the two
// gates above (hard rule 11).
//
// The claim themeeditorart.go now makes is structural: the per-frame sync covers every
// move the editor SURVIVES, and closeThemeEditor covers the move that ends it — which is
// only true while closeThemeEditor is the one and only place a.te is cleared. A second
// `a.te = nil` anywhere in W8 would restore the hole silently and in a shape no
// behavioural gate here would notice, because it would be a NEW exit with no test of its
// own. So the census is on the assignment, not on the call graph.
//
// It reads the AST, deliberately: themeeditorop.go's note on the ring writes the same
// three tokens in prose, and a text scan would count the comment as an exit.
func TestTheOnlyPlaceTheEditorIsDroppedSyncsTheArt(t *testing.T) {
	sites := editorReleaseSites(t)
	if len(sites) != 1 || sites[0] != "closeThemeEditor" {
		t.Fatalf("a.te is cleared in %v, want closeThemeEditor alone — every other release is an editor "+
			"exit that skips editorSyncArt, and the model it leaves behind goes on being drawn from the "+
			"plan of whatever was on screen a moment ago", sites)
	}
	if !containsCall(funcBodySource(t, "themeeditor.go", "closeThemeEditor"), "editorSyncArt") {
		t.Error("closeThemeEditor no longer calls editorSyncArt — a.te = nil is what stops the per-frame " +
			"sync from ever running again, so this is the last instant the media plan can be brought into " +
			"line with the model the app keeps drawing (themeeditorart.go)")
	}
}

// editorReleaseSites names every function in this package that assigns nil to a `.te`
// field. AST, so prose about the assignment does not count as one.
func editorReleaseSites(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		_, f := parsedFile(t, name)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
					return true
				}
				sel, ok := as.Lhs[0].(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "te" {
					return true
				}
				if id, ok := as.Rhs[0].(*ast.Ident); !ok || id.Name != "nil" {
					return true
				}
				out = append(out, fd.Name.Name)
				return true
			})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The signature, and the one derivation behind it
// ---------------------------------------------------------------------------

// TestTheArtSignatureCoversWhatThePlannerReads drives themeArtSig against every field
// planElements actually reads. A signature that misses one is a field whose edit is
// invisible until something else moves — the original defect, wearing a hash.
//
// It MUTATES and compares rather than restating the fold, so it fails when a field is
// dropped from the signature and stays green when the hash's parameters change.
func TestTheArtSignatureCoversWhatThePlannerReads(t *testing.T) {
	base := func() *theme.Sidecar {
		sc := theme.NewSidecar()
		el := editProbeElement()
		el.Kind = theme.ElemGen
		el.Gen = artRigGenerator
		el.Hidden = false
		el.GenParams[0] = theme.KV{Key: artRigParamKey, Value: artRigParamOne}
		sc.Elements = append(sc.Elements, el)
		return sc
	}
	for _, tc := range []struct {
		what string
		edit func(sc *theme.Sidecar)
	}{
		{"kind", func(sc *theme.Sidecar) { sc.Elements[0].Kind = theme.ElemImage }},
		{"media", func(sc *theme.Sidecar) { sc.Elements[0].Media = "other" }},
		{"generator", func(sc *theme.Sidecar) { sc.Elements[0].Gen = "noise" }},
		{"gen_params key", func(sc *theme.Sidecar) { sc.Elements[0].GenParams[0].Key = "pitch" }},
		{"gen_params value", func(sc *theme.Sidecar) { sc.Elements[0].GenParams[0].Value = artRigParamTwo }},
		{"hidden", func(sc *theme.Sidecar) { sc.Elements[0].Hidden = true }},
		{"element count", func(sc *theme.Sidecar) { sc.Elements = append(sc.Elements, editProbeElement()) }},
		{"element order", func(sc *theme.Sidecar) {
			sc.Elements = append([]theme.Element{editProbeElement()}, sc.Elements...)
		}},
	} {
		sc := base()
		was := themeArtSig(sc)
		tc.edit(sc)
		if themeArtSig(sc) == was {
			t.Errorf("changing %s did not move the art signature — planElements reads it, so an edit to it "+
				"changes which page an element draws, and an unmoved signature means the editor never "+
				"re-plans for it", tc.what)
		}
	}
	// The other direction, which is what stops "return a fresh number every call": a
	// field the planner does NOT read must not churn the plan, or every rect nudge
	// re-rasterises the theme's tiles.
	sc := base()
	was := themeArtSig(sc)
	sc.Elements[0].Rect = theme.Rect{X: 1, Y: 2, W: 3, H: 4}
	sc.Elements[0].Fill = theme.RGBA{R: 9, A: 255}
	if themeArtSig(sc) != was {
		t.Error("a rect and a colour moved the ART signature — the plan would be re-derived and every " +
			"generator tile re-rasterised on a drag, which is the hitch this signature exists to avoid")
	}
}

// TestOneDerivationOfTheElementKeys is the anti-mirror census (hard rule 11).
//
// The editor's live re-plan and the theme apply's cold plan must be the SAME
// derivation. A second copy in the editor would agree on the day it was written and
// drift the first time the planner learns a new rule — and the drift would be silent,
// because both sides would still produce plausible keys.
func TestOneDerivationOfTheElementKeys(t *testing.T) {
	for _, decl := range []string{
		"func planThemeMedia(",
		"func (a *App) editorSyncArt(",
	} {
		if body := funcBodyInPackage(t, decl); !strings.Contains(body, "planElements(") {
			t.Errorf("%q no longer goes through planElements — the cold plan and the editor's live re-plan "+
				"must be one derivation, or an element's key means two things", decl)
		}
	}
	// And the derivation stamps the provenance in the same breath, so a plan can never
	// claim to be current for a model it was not built from.
	if body := funcBodyInPackage(t, "func (plan *themeMediaPlan) planElements("); !strings.Contains(body, "plan.ArtSig = themeArtSig(") {
		t.Error("planElements no longer stamps plan.ArtSig — the editor compares that signature against " +
			"the document every frame, and a plan whose signature was set anywhere else could say it is " +
			"current while its keys are stale")
	}
}

// ---------------------------------------------------------------------------
// The Esc ladder's panel rung
// ---------------------------------------------------------------------------

// TestEscClosesThePanelBeforeItClearsTheSelection pins the rung design §Q7 specifies
// ("close top panel → deselect → leave") and app.go was missing.
//
// THE DEFECT IT CLOSES. While the Fonts panel was cosmetic this was invisible; W7b made
// it a real input mode that fences BOTH rails and the canvas (editorPanelUp), so Esc
// walked straight past the only surface on screen the user could interact with, cleared
// their selection underneath it and then armed the discard chip — with the panel still
// up.
//
// Driven through App.Frame, because the ladder lives inside Frame's key handling and
// nothing that calls drawThemeEditor directly reaches it.
func TestEscClosesThePanelBeforeItClearsTheSelection(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	a.te.selectOnly(tgts[0])
	a.te.fontsOpen = true
	driveFrame(a)

	pressEsc := func() {
		a.ctx.BeginFrame(frameHarnessDt)
		a.ctx.escPressed = true
		a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
	}

	// Esc resolves the topmost thing first, and closeTopOverlay's table is long — a
	// themed courtroom fixture legitimately has panels of its own open under the editor.
	// So the gate pins the ORDER: the panel must be down on a strictly earlier press than
	// the one that clears the selection.
	const escBudget = 12
	panelAt, clearedAt := -1, -1
	for i := 1; i <= escBudget && clearedAt < 0 && a.te != nil; i++ {
		pressEsc()
		if a.te == nil {
			break
		}
		if panelAt < 0 && !a.editorPanelUp() {
			panelAt = i
		}
		if clearedAt < 0 && a.te.selN == 0 {
			clearedAt = i
		}
	}
	if panelAt < 0 {
		t.Fatalf("%d Esc presses never closed the panel — Esc leaves the editor with a panel still up, "+
			"which is the one surface the user was actually looking at", escBudget)
	}
	if clearedAt < 0 {
		t.Fatal("the selection never cleared; the ordering below cannot be judged")
	}
	if panelAt >= clearedAt {
		t.Errorf("the panel closed on press %d and the selection cleared on press %d — the panel is the "+
			"topmost surface and fences the rails, so it must come strictly first", panelAt, clearedAt)
	}
	if a.te == nil {
		t.Error("the editor left before the ladder finished — a panel and a selection are two rungs, " +
			"not one")
	}
}

// TestEveryPanelFlagThePredicateReadsIsClosed is the seam census for the pair.
//
// editorPanelUp is the ONE predicate both halves of the input fence ask
// (TestBothHalvesOfTheFenceAskOnePredicate). closeEditorPanels is its inverse, and the
// day W8 adds the preset gallery to the OR without adding it here, Esc silently stops
// being able to close it — the exact hole the Fonts panel had.
func TestEveryPanelFlagThePredicateReadsIsClosed(t *testing.T) {
	up := funcBodyInPackage(t, "func (a *App) editorPanelUp(")
	closer := funcBodyInPackage(t, "func (a *App) closeEditorPanels(")
	flags := panelFlagsIn(up)
	if len(flags) == 0 {
		t.Fatal("editorPanelUp reads no a.te flag at all — this census has lost its subject")
	}
	for _, f := range flags {
		if !strings.Contains(closer, "a.te."+f) {
			t.Errorf("editorPanelUp reads a.te.%s but closeEditorPanels never writes it — Esc cannot close "+
				"a panel the predicate can see, so that panel becomes a surface with no keyboard exit", f)
		}
	}
}

// panelFlagsIn lists the `a.te.<field>` names a body reads. A tiny scanner rather than
// go/ast: the census needs the field NAMES out of one small function body, and the ast
// walk to get them would be more code than the property is worth.
func panelFlagsIn(body string) []string {
	const marker = "a.te."
	var out []string
	for i := strings.Index(body, marker); i >= 0; i = strings.Index(body, marker) {
		body = body[i+len(marker):]
		j := 0
		for j < len(body) && (body[j] == '_' ||
			body[j] >= 'a' && body[j] <= 'z' || body[j] >= 'A' && body[j] <= 'Z' ||
			body[j] >= '0' && body[j] <= '9') {
			j++
		}
		if name := body[:j]; name != "" && !strings.Contains(strings.Join(out, " "), name) {
			out = append(out, name)
		}
	}
	return out
}
