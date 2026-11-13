package ui

import (
	"sort"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// editChipMagnetW / editChipProfileW fit the widest labels of the editor banner's Magnet and Profile chips.
	editChipMagnetW  = int32(94)
	editChipProfileW = int32(150)
)

// sortedLayoutProfileNames returns profile names sorted so the banner chip's cycle position is stable.
func (a *App) sortedLayoutProfileNames() []string {
	names := a.d.Prefs.Load().LayoutProfileNames()
	sort.Strings(names)
	return names
}

// hasLayoutProfiles reports whether a profile exists; the Profile chip only draws when one does.
func (a *App) hasLayoutProfiles() bool {
	return len(a.d.Prefs.Load().LayoutProfileNames()) > 0
}

// currentLayoutProfileLabel is the Profile chip's label, or "" with no profiles.
// The range check covers a profile deleted mid-edit.
func (a *App) currentLayoutProfileLabel() string {
	names := a.sortedLayoutProfileNames()
	if len(names) == 0 {
		return ""
	}
	if a.layoutProfileCursor < 0 || a.layoutProfileCursor >= len(names) {
		return "Profile: pick"
	}
	return "Profile: " + names[a.layoutProfileCursor]
}

// cycleLayoutProfile applies the next saved profile, wrapping; the chip is a shortcut for the Settings list.
func (a *App) cycleLayoutProfile() {
	names := a.sortedLayoutProfileNames()
	if len(names) == 0 {
		return
	}
	a.layoutProfileCursor = (a.layoutProfileCursor + 1) % len(names)
	a.applyProfile(a.d.Prefs.Load().LayoutProfile(names[a.layoutProfileCursor]))
	a.pushDebug("layout: profile applied from editor banner")
}

const (
	// Theater premade: one 4:3 stage centred across the top, computed from the live window size.
	theaterStageHeightFrac   = 0.64 // stage height as a fraction of the window height
	theaterStageMaxWidthFrac = 0.96 // never wider than this fraction (narrow/wide windows)
	theaterStageTopFrac      = 0.04 // top margin above the stage
)

// applyLayoutPreset makes an override map the live and saved layout; a nil or empty map resets every box.
func (a *App) applyLayoutPreset(m map[string][4]float64) {
	// A swap drops the pin of every box whose rect changes, because the pin's saved window described the old override.
	// Untouched boxes keep their pins, so a stage-only premade never unpins the log.
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

// applyProfile makes a full LayoutProfile the live layout and saved prefs, wholesale, including its anchors.
func (a *App) applyProfile(p config.LayoutProfile) {
	a.d.Prefs.SetClassicLayout(p.Classic)
	a.classicOv = cloneClassicOv(p.Classic)
	a.classicOvLoaded = true
	a.d.Prefs.SetClassicAnchors(p.Anchors)
	a.classicAnchor = parseAnchors(p.Anchors)
	// Seed the live map from the saved copy so both read the same sanitized values.
	a.d.Prefs.SetClassicRotations(p.Rotations)
	a.classicRot = a.d.Prefs.Load().ClassicRotationSnapshot()
	// Rebuild a.hidden from HiddenPanels, not p.Hidden, because SetHiddenPanels dedups and caps.
	// seedHiddenFromPrefs also stops a profile hiding both the toolbox grip and the Settings button.
	a.d.Prefs.SetHiddenPanels(p.Hidden)
	a.seedHiddenFromPrefs()
	gridPx := p.GridPx
	a.d.Prefs.Update(func(sn *config.Settings) {
		sn.LayoutGridPx = gridPx
	})

	// Clear placed so open panels re-seed from the profile next frame; floatWins otherwise cache x, y and placed.
	// panelSlotTable covers every panel but msgWin and the Extras box, handled below.
	for i := range panelSlotTable {
		panelSlotTable[i].fw(a).placed = false // cold-path table use (never a draw path)
	}
	a.msgWin.placed = false
	a.extrasPlaced = false
	// SetClassicLayout replaced the whole map, so just drop the live torn-off boxes; the next courtroom frame rebuilds
	// them from the profile with the real w and h, which this function doesn't have.
	a.extrasDetached = a.extrasDetached[:0]
	a.extrasTornRebuilt = false
}

// applyStagePreset overlays a premade stage rect on the current layout, leaving every other box in place.
// ensureClassicOv first so saved overrides aren't dropped before the courtroom has been entered.
func (a *App) applyStagePreset(vp [4]float64) {
	a.ensureClassicOv()
	ov := cloneClassicOv(a.classicOv)
	if ov == nil {
		ov = map[string][4]float64{}
	}
	ov[slotViewport] = vp
	a.applyLayoutPreset(ov)
}

// theaterStageFrac builds the Theater viewport fraction for a window size, or a top-left square when it is unknown.
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
