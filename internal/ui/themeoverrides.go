package ui

// Applies the sidecar's [overrides] tier. Precedence, lowest to highest: the theme's courtroom_design.ini;
// AO2's stock per-key defaults (applyAO2DefaultRects); these author edits; the player's drags (applyRectOverrides).

import "github.com/SyntaxNyah/AsyncAO/internal/theme"

// foldSidecarOverrides lays [overrides] over the resolved design map, after applyAO2DefaultRects. It rewrites existing
// keys only: adding one would resurrect a widget from a rect rather than a draw site. Runs on the theme-apply goroutine
// against an unpublished map, so no locks. A degenerate rect is not refused: it hides its widget in AO2 too, and
// themeLayoutIn drops it, where refusing would silently fall back to the stock rect.
func foldSidecarOverrides(layout map[string]theme.Rect, sc *theme.Sidecar) {
	// A nil *Sidecar is the stock-AO2-theme case; this is field access, so nil-safe methods don't cover it.
	if sc == nil {
		return
	}
	for _, o := range sc.Overrides {
		if key, ok := sidecarOverrideTarget(layout, o.Key); ok {
			layout[key] = o.Rect
		}
	}
}

// sidecarOverrideTarget resolves the layout entry one [overrides] row addresses, or reports it can do nothing. Must be
// editable: themeKeyEditable refuses inert and fixed rows, notably `courtroom`, the coordinate system itself. All
// surviving keys are relCourtroom, so no override lands in a system its consumer re-bases. Must be in the resolved map.
// AO2 probes "immediate", then "pre_no_interrupt" (courtroom.cpp:1072), and its stock file uses the latter, so the
// override goes to whichever spelling the map carries; writing the missing one would add a key.
func sidecarOverrideTarget(layout map[string]theme.Rect, key string) (string, bool) {
	if !themeKeyEditable(key) {
		return "", false
	}
	if _, ok := layout[key]; ok {
		return key, true
	}
	other, hasOther := themeSlotOtherSpelling(key)
	if !hasOther {
		return "", false
	}
	if _, ok := layout[other]; ok {
		return other, true
	}
	return "", false
}
