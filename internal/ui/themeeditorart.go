package ui

// THE EDITOR'S ART SYNC (v1.90.0 W7b) — docs/wip/THEME-EDITOR-DESIGN.md
// §"Live preview, no Apply button".

import (
	"image"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// editorSyncArt re-derives the media plan when the document's art has moved, and lands
// whatever tiles the new plan needs. Render thread, once per editor frame BEFORE the
// canvas draws (the bake it feeds runs inside that draw), and once more from
// closeThemeEditor, which is the last frame there will be.
func (a *App) editorSyncArt() {
	if a.te == nil || a.te.doc == nil || a.te.doc.side == nil || a.d.Store == nil {
		return
	}
	// LIVE PREVIEW OFF FREEZES THE PICTURE (v1.90.0 W10), and this gate is BEFORE
	// the signature hash on purpose: the whole value of the toggle is that a paused
	// editor does what an idle one does, and a hash over every element's fill fields
	// per frame is exactly the "cheap enough not to bother" cost that adds up on the
	// theme this is for. Turning it back on pays one reconcile (setEditorLive), and
	// closeThemeEditor un-pauses before its own call so the last frame always
	// reconciles — a plan that outlived the editor stale is the defect this file's
	// second call site exists to prevent.
	if !a.te.editorLiveOn() {
		return
	}
	if a.themeMediaPlan.ArtSig == themeArtSig(a.te.doc.side) {
		return
	}
	// THE PLAN IS NAMESPACED BY THE APPLIED THEME'S ID, so a document belonging to a
	// theme that is no longer applied must not re-key the plan the screen is drawing
	// from — it would point every element at art under the wrong namespace and prune the
	// applied theme's tiles on the way. reclaimThemeDoc guards its own re-install on the
	// same comparison and for the same reason. (A cold path either way: the sigs differ
	// every frame in that state, and this returns before it allocates anything.)
	name, _ := a.d.Prefs.Load().Theme()
	if name != a.te.doc.name {
		return
	}
	// The [media] ENTRIES survive the copy untouched: they carry the per-row budget
	// verdicts, they were resolved with an os.Stat per row that may not happen on a
	// frame (hard rule 2), and no op can move them — the inspector edits which id an
	// ELEMENT names, never the [media] table.
	plan := a.themeMediaPlan
	plan.planElements(a.te.doc.side, themeSanitizeID(name))
	// PRUNE FIRST, THEN ADMIT, which is landThemeMedia's order and load-bearing for the
	// same reason: the tiles the edit orphaned are not part of the allowance the new
	// ones are measured against.
	a.pruneEditorGenTiles(&plan)
	a.themeMediaPlan = plan
	a.landEditorGenTiles()
	// The bake caches the resolved page per element, so the keys only mean anything
	// once the layout is rebuilt.
	a.invalidateThemeCanvases()
	a.uiDirty = true
}

// pruneEditorGenTiles drops every generator tile the new plan no longer wants.
func (a *App) pruneEditorGenTiles(plan *themeMediaPlan) {
	for key := range a.themeMediaKeys {
		if !strings.HasPrefix(key, render.ThemeGenKeyPrefix) || themeGenPlanned(plan.Gens, key) {
			continue
		}
		a.d.Store.Remove(key)
		delete(a.themeMediaKeys, key)
	}
}

// landEditorGenTiles rasterises and uploads every planned tile that is not in the store
// already.
func (a *App) landEditorGenTiles() {
	for i := range a.themeMediaPlan.Gens {
		g := &a.themeMediaPlan.Gens[i]
		if a.themeMediaKeys[g.Key] {
			continue
		}
		img := RasterGenerator(g.Spec)
		if img == nil {
			// planElements admits only names GeneratorKnown accepts, so this is the belt
			// to genLookup's brace; the element degrades to its own flat fill.
			continue
		}
		b := img.Bounds()
		d := &assets.Decoded{Frames: []*image.RGBA{img}, Width: b.Dx(), Height: b.Dy()}
		if err := a.landThemeMediaPage(g.Key, d); err != nil {
			a.te.note("the " + g.Spec.Name + " tile was refused — " + err.Error())
		}
	}
}
