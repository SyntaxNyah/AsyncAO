package ui

// The AsyncAO tier's [overrides] applier (v1.90.0).
// only t.design (theme.go:345-346), t.design is courtroom_design.ini
// (theme.go:286), and the sidecar is never merged into that INI — so the whole
// (sidecar.go:824), called from exactly one non-test site: bakePanes
// (themeelementbake.go:671), which returns at its `len(sc.Panes) == 0` guard for
// the stock emote_dropdown {5,380,105,20} (ao2defaultrects.go:156) and produced
// The author beats the backstop and the player beats the author, which is the
// order every other themed value already resolves in.

import "github.com/SyntaxNyah/AsyncAO/internal/theme"

// foldSidecarOverrides lays a theme's [overrides] over the RESOLVED design map —
// after applyAO2DefaultRects, so "the AO2 design" an override binds to means the
// theme's own file plus the stock backstop, which is what makes a two-key
// courtroom_design.ini (`courtroom` + `viewport`) plus a sidecar a whole layout.
// (layoutedit.go:832). A key absent from the map is a widget this build does not
// end (themeslots.go:12-14).
// bad component on purpose (AO2 parity, sidecar_read.go:922-924), and a
// declared-but-degenerate rect hides its widget in AO2 too; themeLayoutIn already
// drops anything under minThemedElementPx (theme_layout.go:346). Refusing it here
func foldSidecarOverrides(layout map[string]theme.Rect, sc *theme.Sidecar) {
	// Field access, not a method: the nil-safe accessor discipline covers
	// Sidecar's methods, and a nil *Sidecar is the stock-AO2-theme case.
	if sc == nil {
		return
	}
	for _, o := range sc.Overrides {
		if key, ok := sidecarOverrideTarget(layout, o.Key); ok {
			layout[key] = o.Rect
		}
	}
}

// sidecarOverrideTarget resolves the layout entry one [overrides] row addresses,
// or reports that the row can never do anything.
// Two gates and one alias, in that order:
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
