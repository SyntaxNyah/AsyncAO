package ui

// Editor art sync: keeps the art the frame draws in step with the document being edited, without a theme apply.
//
// The bake resolves art via a.themeMediaPage(a.themeMediaPlan.ElemKey(idx)), but planElements derives those keys
// from each element's kind, media id, generator and params, and its position in the array, so a fill edit or an
// element add/delete would leave a stale plan drawing the old tile. The plan therefore carries a signature of the
// elements it was derived from (themeMediaPlan.ArtSig) and the frame compares it with the document's, which covers
// undo, redo, paste, baseline restore and a background apply landing a plan built from the file on disk.
//
// There are two call sites: the frame, and closeThemeEditor, the function that clears a.te. Once a.te is gone the
// frame stops, so the discard-on-exit restore would otherwise leave the courtroom drawing from the edited plan
// with its tile already released by pruneEditorGenTiles.
//
// It does not kick a theme apply: that re-anchors a.themeAt, restarts every clock group and purges the text cache.
// Tiles land through landThemeMediaPage, which re-anchors nothing.

import (
	"image"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// editorSyncArt re-derives the media plan when the document's art has moved, and lands the tiles it needs.
// Render thread, once per editor frame before the canvas draws, and once more from closeThemeEditor.
// The settled path is one hash and a compare, allocation-free (themeArtSig), so editorChromeAllocBudget holds.
func (a *App) editorSyncArt() {
	if a.te == nil || a.te.doc == nil || a.te.doc.side == nil || a.d.Store == nil {
		return
	}
	// Live preview off freezes the picture. This is before the signature hash on purpose: a paused editor should do
	// what an idle one does. Turning it on pays one reconcile (setEditorLive), and closeThemeEditor un-pauses first so
	// the last frame reconciles.
	if !a.te.editorLiveOn() {
		return
	}
	if a.themeMediaPlan.ArtSig == themeArtSig(a.te.doc.side) {
		return
	}
	// The plan is namespaced by the applied theme's id, so a document of a theme that is no longer applied must not
	// re-key the plan the screen draws from. reclaimThemeDoc guards its re-install on the same comparison.
	name, _ := a.d.Prefs.Load().Theme()
	if name != a.te.doc.name {
		return
	}
	// The [media] entries survive the copy untouched: they carry the per-row budget verdicts, were resolved with an
	// os.Stat per row that may not happen on a frame (hard rule 2), and no op can move them.
	plan := a.themeMediaPlan
	plan.planElements(a.te.doc.side, themeSanitizeID(name))
	// Prune first, then admit (landThemeMedia's order): orphaned tiles are not part of the allowance for the new ones.
	a.pruneEditorGenTiles(&plan)
	a.themeMediaPlan = plan
	a.landEditorGenTiles()
	// The bake caches the resolved page per element, so the keys only mean anything once the layout is rebuilt.
	a.invalidateThemeCanvases()
	a.uiDirty = true
}

// pruneEditorGenTiles drops every generator tile the new plan no longer wants.
//
// This is the cap (hard rule 4): a generator's key is the content address of its params, so each keystroke in
// the Params row mints a new key and would otherwise upload a fresh tile per character forever.
// Generator keys only: a [media] page belongs to a declared row, and an element may point back at it next
// keystroke or Ctrl+Z, which would mean re-reading the file from the render thread.
func (a *App) pruneEditorGenTiles(plan *themeMediaPlan) {
	for key := range a.themeMediaKeys {
		if !strings.HasPrefix(key, render.ThemeGenKeyPrefix) || themeGenPlanned(plan.Gens, key) {
			continue
		}
		a.d.Store.Remove(key)
		delete(a.themeMediaKeys, key)
	}
}

// landEditorGenTiles rasterises and uploads every planned tile that is not in the store already.
//
// It runs on the render thread because the apply goroutine only exists for an apply, and running one is the hitch
// this file avoids. The cost is bounded: pure CPU over at most theme.GenTileMaxPx^2 pixels (no disk, no decode
// pool), at most render.ThemeGenCap tiles, and only on the frame an edit lands.
//
// A refusal is a chip, not silence: the store re-checks the budget, and a refused tile would otherwise just stop
// drawing with nothing saying why.
func (a *App) landEditorGenTiles() {
	for i := range a.themeMediaPlan.Gens {
		g := &a.themeMediaPlan.Gens[i]
		if a.themeMediaKeys[g.Key] {
			continue
		}
		img := RasterGenerator(g.Spec)
		if img == nil {
			// planElements admits only names GeneratorKnown accepts; the element degrades to its own flat fill.
			continue
		}
		b := img.Bounds()
		d := &assets.Decoded{Frames: []*image.RGBA{img}, Width: b.Dx(), Height: b.Dy()}
		if err := a.landThemeMediaPage(g.Key, d); err != nil {
			a.te.note("the " + g.Spec.Name + " tile was refused — " + err.Error())
		}
	}
}
