package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/veandco/go-sdl2/sdl"
)

// Layout premades + full-state profiles (#34 → A6): flip the whole courtroom
// between named setups (a big stage for watching, a wide log for moderating, …).
// applyLayoutPreset swaps just the ClassicLayout override map (used by the anchorless
// theater premades); applyProfile (A6) restores a full LayoutProfile — classic slots,
// anchors, hidden set, grid — at once. The Settings → Theme "Layout profiles" section
// owns the save/load UI; these helpers apply them and define the built-in premades.

const (
	// Theater premade geometry (#34) — the stage's share of the window. One big 4:3
	// stage centered across the top; computed from the live window size so it stays a
	// true 4:3 at any resolution.
	theaterStageHeightFrac   = 0.64 // stage height as a fraction of the window height
	theaterStageMaxWidthFrac = 0.96 // never wider than this fraction (narrow/wide windows)
	theaterStageTopFrac      = 0.04 // top margin above the stage
)

// applyLayoutPreset makes an override map the live default-courtroom layout AND the
// durable pref. It takes effect the same frame whether or not the editor is open —
// slotRect reads a.classicOv, which this replaces and marks loaded. A nil/empty map
// is the stock reset (every box back to its computed default).
func (a *App) applyLayoutPreset(m map[string][4]float64) {
	// A wholesale swap invalidates the window PIN of every slot whose rect it
	// changes: the pin's saved-window context described the OLD override (a
	// preset may have been saved months ago at another resolution — re-basing
	// its fresh rect against that stale window would misplace it). Untouched
	// slots keep their pins, so a stage-only premade never unpins the log.
	for k := range a.classicAnchor {
		if m[k] != a.classicOv[k] {
			delete(a.classicAnchor, k)
			a.d.Prefs.ClearClassicAnchor(k)
		}
	}
	a.d.Prefs.SetClassicLayout(m)
	a.classicOv = cloneClassicOv(m)
	a.classicOvLoaded = true
}

// applyProfile makes a full-state LayoutProfile (A6) the live layout AND the
// durable prefs, restoring all four axes at once: classic slots, their window
// anchors, the hidden-chrome set, and the editor snap-grid step. Unlike
// applyLayoutPreset it does NOT selectively unpin — a profile carries its Anchors
// map explicitly (with each anchor's saved window), so the whole snapshot is
// applied wholesale. Live and durable copies are fed from the SAME profile map so
// they can't diverge (SetClassicAnchors sanitize-drops, but parseAnchors reads p
// directly). applyLayoutPreset / applyStagePreset stay for the anchorless theater
// premades.
func (a *App) applyProfile(p config.LayoutProfile) {
	// Classic slots.
	a.d.Prefs.SetClassicLayout(p.Classic)
	a.classicOv = cloneClassicOv(p.Classic)
	a.classicOvLoaded = true
	// Window anchors (wholesale; the profile carries WinW/WinH per anchor).
	a.d.Prefs.SetClassicAnchors(p.Anchors)
	a.classicAnchor = parseAnchors(p.Anchors)
	// Rotations (A4, wholesale like the anchors). Seed the live App map from the
	// durable snapshot so live and persisted read the same sanitized source.
	a.d.Prefs.SetClassicRotations(p.Rotations)
	a.classicRot = a.d.Prefs.ClassicRotationSnapshot()
	// Hidden-chrome set — durable + the live map (mirror applyPrefsToState). Rebuild
	// the live map from HiddenPanels() (NOT the raw p.Hidden slice) so live and
	// durable read the SAME sanitized source — SetHiddenPanels dedups/caps, and
	// seeding a.hidden from the unsanitized slice would diverge from what persisted.
	a.d.Prefs.SetHiddenPanels(p.Hidden)
	a.hidden = map[string]bool{}
	for _, id := range a.d.Prefs.HiddenPanels() {
		a.hidden[id] = true
	}
	// Editor snap grid.
	a.d.Prefs.SetLayoutGridSize(p.GridPx)
	// THE CATCH (architect ruling 6): reset .placed on every persistable floatWin
	// so an already-OPEN panel re-seeds from the applied profile's slots next frame
	// (classic slots update immediately; floatWins otherwise cache x/y/placed). The
	// panelSlotTable covers the 10 non-msgWin panels; msgWin + the Extras box are
	// handled explicitly — 11 floatWins + extrasPlaced.
	for i := range panelSlotTable {
		panelSlotTable[i].fw(a).placed = false // cold-path table use (never a draw path)
	}
	a.msgWin.placed = false
	a.extrasPlaced = false
	// Torn-off Extras widgets: a profile may carry its own torn:widget:* slots, so
	// rebuild the set from the newly-applied classicOv. SetClassicLayout above already
	// replaced the WHOLE override map, so torn slots the profile lacks are gone — we
	// need only drop the live boxes and re-arm the one-shot latch; the next courtroom
	// frame reconstructs from the profile's slots (with a real w/h, which applyProfile
	// doesn't have — hence the deferred rebuild rather than an inline one here).
	a.extrasDetached = a.extrasDetached[:0]
	a.extrasTornRebuilt = false
}

// applyStagePreset overlays a premade stage rect onto the CURRENT layout, leaving every
// other box where the user put it — so a premade is a quick stage resize, not a full
// layout swap. ensureClassicOv first so persisted other-slot overrides aren't dropped
// when the courtroom hasn't been entered yet this session.
func (a *App) applyStagePreset(vp [4]float64) {
	a.ensureClassicOv()
	ov := cloneClassicOv(a.classicOv)
	if ov == nil {
		ov = map[string][4]float64{}
	}
	ov[slotViewport] = vp
	a.applyLayoutPreset(ov)
}

// theaterStageFrac builds the "Theater" premade's viewport fraction: one big 4:3 stage
// centered across the top of the window. w/h are the REAL window dimensions (fractions
// are window-relative). Returns the zero rect's fraction if the window size is unknown.
func (a *App) theaterStageFrac(w, h int32) [4]float64 {
	if w <= 0 || h <= 0 {
		return [4]float64{0, 0, theaterStageHeightFrac, theaterStageHeightFrac}
	}
	vh := int32(float64(h) * theaterStageHeightFrac)
	vw := vh * 4 / 3
	if maxW := int32(float64(w) * theaterStageMaxWidthFrac); vw > maxW {
		vw, vh = maxW, maxW*3/4
	}
	r := sdl.Rect{X: (w - vw) / 2, Y: int32(float64(h) * theaterStageTopFrac), W: vw, H: vh}
	return rectToFrac(r, w, h)
}
