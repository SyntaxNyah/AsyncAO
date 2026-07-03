package ui

import "github.com/veandco/go-sdl2/sdl"

// Layout presets (#34): save the whole default-courtroom slot arrangement under a
// name and flip between setups (a big stage for watching, a wide log for moderating,
// …). A preset is just a ClassicLayout override map (window fractions), so it travels
// across window sizes. The Settings → Theme "Layout presets" section owns the UI;
// these helpers apply the presets and define the built-in premades.

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
