package ui

// THE EDITOR'S ART SYNC (v1.90.0 W7b) — docs/wip/THEME-EDITOR-DESIGN.md
// §"Live preview, no Apply button".
//
// ONE concern: keeping the ART THE FRAME DRAWS in step with the document being
// edited, without a theme apply.
//
// THE DEFECT IT CLOSES, and it is the shape hard rule 11 names by hand. The bake
// resolves an element's art as `a.themeMediaPage(a.themeMediaPlan.ElemKey(idx))`
// (themeelementbake.go), and the plan was assigned in exactly ONE place —
// landThemeMedia, from pollThemeApply. But planElements derives those keys from each
// element's kind, media id, generator name and generator params, AND from the
// element's POSITION in the array. So every one of the inspector's headline fill rows,
// plus adding or deleting an element (which shifts every later index), left the stale
// plan in place: the element went on drawing the OLD tile, or its neighbour's, or a
// flat fill, until the user switched themes and back. Four rows that edited the file
// and changed nothing on screen — parsed but never applied, one wave after the
// `[overrides]` tier taught us what that costs.
//
// WHY A SIGNATURE AND NOT A CALL AT EVERY MUTATION SITE. editorApply is the funnel for
// ops, but the document also moves under undo, redo, a grouped paste and the
// discard-on-exit baseline restore, and a background theme apply can land a plan built
// from the file ON DISK while the document holds unsaved edits. A "re-plan here" call
// at each of those is five call sites today and a forgotten one in W8. Instead the
// PLAN carries the signature of the elements it was derived from (themeMediaPlan.ArtSig,
// stamped by the one derivation), the frame compares it against the document's own, and
// any way the model can possibly have moved is covered by construction — including the
// ways that do not exist yet.
//
// TWO CALL SITES, NOT ONE, AND THE SECOND IS NOT A HEDGE. "The frame reconciles it"
// holds for every move the editor survives; it says nothing about the move that ENDS the
// editor, because dropping a.te is what stops the frame from running. The discard-on-exit
// restore is exactly that move and the reason this paragraph exists: it was named above
// as covered and was the one site that was not, so Back-Back over an edited generator
// returned the baseline MODEL to a courtroom still drawing from the edited PLAN — with
// that plan's orphaned tile already released by pruneEditorGenTiles, i.e. an element
// drawing nothing at all until a theme switch rebuilt it. So the second site is
// closeThemeEditor, the single function that clears a.te (themeeditor.go), which makes it
// the last instant any exit — this one, the clean one, the session-dropped one, W8's —
// can still reconcile. Two sites, and between them a state where the editor is gone and
// the plan is stale is unreachable.
//
// WHAT IT DELIBERATELY DOES NOT DO: kick a theme apply. An apply re-anchors a.themeAt,
// restarts every clock group and purges the text cache, which is precisely the hitch
// TestSavingGeometryDoesNotRestartTheTheme exists to forbid. The tiles land through
// landThemeMediaPage — the incremental path written for this wave — which re-anchors
// nothing.

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
//
// THE SETTLED PATH IS ONE HASH AND A COMPARE, allocation-free (themeArtSig's own note),
// so the editor's frame budget (editorChromeAllocBudget) is untouched between edits.
func (a *App) editorSyncArt() {
	if a.te == nil || a.te.doc == nil || a.te.doc.side == nil || a.d.Store == nil {
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
	name, _ := a.d.Prefs.Theme()
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
//
// IT IS NOT HOUSEKEEPING, it is the cap (hard rule 4). A generator's key is the content
// address of its params, so every keystroke in the Params row mints a NEW key: without
// this, typing a number would upload a fresh 256 KiB tile per character and never
// release one, and the store would grow without bound for as long as the editor is open.
//
// GENERATOR KEYS ONLY. A [media] page belongs to a declared row that no element edit can
// remove, and an element that stopped pointing at one may point back at it on the next
// keystroke or the next Ctrl+Z — dropping it would mean re-reading the file from the
// render thread to get it back.
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
//
// ON THE RENDER THREAD, which is a DOCUMENTED DEVIATION from design §W4's "tiles ≤ 256²,
// rastered on the apply goroutine" — and it is the deviation the same design's "live
// preview, no Apply button" requires, because the apply goroutine only exists for an
// apply and running one is the hitch this whole file avoids. What §W4's rule protects is
// the frame budget, and the trade is bounded on every axis that matters: rasterising is
// pure CPU over at most theme.GenTileMaxPx² pixels (no disk, no decode pool, nothing hard
// rule 2 names), at most render.ThemeGenCap tiles, and only on the frame an edit lands —
// never on a settled one. The alternative is a job, a poll and a hand-off for work
// loadThemeMediaSet already does inline, in exchange for a preview that arrives a frame
// or two after the keystroke that asked for it.
//
// A REFUSAL IS A CHIP, never a silent absence: the store re-checks the budget at the
// door, and an element whose tile was refused would otherwise just stop drawing with
// nothing anywhere to say why (design §3.3 — name the offender, name the limit).
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
