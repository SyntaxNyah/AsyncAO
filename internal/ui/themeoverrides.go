package ui

// The AsyncAO tier's [overrides] applier (v1.90.0).
//
// docs/THEME-FORMAT.md §3 has published `[overrides]` since format 1 —
// "design-space edits laid OVER courtroom_design.ini" (:176-190) — and
// docs/wip/THEME-EDITOR-DESIGN.md:689 calls it one of only two sanctioned ways to
// move an AO2 widget. NOTHING APPLIED IT. Theme.ElementRect reads t.design and
// only t.design (theme.go:345-346), t.design is courtroom_design.ini
// (theme.go:286), and the sidecar is never merged into that INI — so the whole
// tier had exactly one reader in the tree, Sidecar.OverrideRect
// (sidecar.go:824), called from exactly one non-test site: bakePanes
// (themeelementbake.go:671), which returns at its `len(sc.Panes) == 0` guard for
// every theme that declares no `[pane.*]` section — that is all fourteen we ship.
// Every [overrides] row in every theme was inert.
//
// The visible damage was not "a widget stayed put". The free elements authored ON
// those rects resolved their anchors against AO2's STOCK geometry instead, so the
// decoration landed somewhere its author never wrote: thh_trial's `rail_mat`
// anchors emote_dropdown at `-6, 0, 592, 0` and documents `-> 2, 306, 710, 22` (a
// 22 px seat under the selector rail), but resolveElementRect added that delta to
// the stock emote_dropdown {5,380,105,20} (ao2defaultrects.go:156) and produced
// {-1,380,697,20} — a 697 px opaque slab lying across the toggle row.
//
// PRECEDENCE, lowest to highest:
//
//  1. the theme's own courtroom_design.ini (Theme.ElementRect).
//  2. AO2's stock per-key defaults, for keys the theme was silent about
//     (applyAO2DefaultRects — #21's backstop).
//  3. THIS TIER: the theme author's sidecar edits.
//  4. the player's own layout-editor drags (applyRectOverrides, layoutedit.go:825).
//
// The author beats the backstop and the player beats the author, which is the
// order every other themed value already resolves in.

import "github.com/SyntaxNyah/AsyncAO/internal/theme"

// foldSidecarOverrides lays a theme's [overrides] over the RESOLVED design map —
// after applyAO2DefaultRects, so "the AO2 design" an override binds to means the
// theme's own file plus the stock backstop, which is what makes a two-key
// courtroom_design.ini (`courtroom` + `viewport`) plus a sidecar a whole layout.
//
// REWRITES EXISTING KEYS ONLY, the same discipline applyRectOverrides uses
// (layoutedit.go:832). A key absent from the map is a widget this build does not
// place at all, and adding one would resurrect a widget from a rect rather than
// from a draw site — the ghost-box failure the themeSlots registry was built to
// end (themeslots.go:12-14).
//
// Runs on the theme-apply goroutine, against a map nothing else can see yet
// (res.layout is published by pollThemeApply afterwards), so it needs no
// synchronisation and touches no SDL — hard rules 1 and 2. Its cost is bounded by
// construction: at most theme.OverrideCap rows, each a map probe.
//
// A DEGENERATE RECT IS NOT REFUSED HERE, deliberately. parseRectValue swallows a
// bad component on purpose (AO2 parity, sidecar_read.go:922-924), and a
// declared-but-degenerate rect hides its widget in AO2 too; themeLayoutIn already
// drops anything under minThemedElementPx (theme_layout.go:346). Refusing it here
// instead would make an override silently fall back to the stock rect, which is
// the confusing answer, not the safe one.
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
//
// Two gates and one alias, in that order:
//
//   - EDITABLE. docs/THEME-FORMAT.md:189 says only keys that exist and are
//     editable are honoured, and themeKeyEditable is that predicate
//     (themeslots.go:307). It refuses the inert rows — the rects themeSlots
//     ingests for audit with nothing painting them — and the four `fixed` ones.
//     Refusing `courtroom` matters most: it is the design canvas itself, and an
//     override that resized it would be silently re-authoring the theme's
//     coordinate system rather than moving a widget. A consequence worth naming:
//     every key that survives this gate is relCourtroom, because the three
//     parent-relative rects (showname, message, music_name) are all `fixed` and
//     the two viewport-relative ones are inert — so no honoured override can land
//     in a coordinate system its consumer will re-base.
//
//   - PRESENT. The map is the resolved design; a key it does not carry is not a
//     widget this build places.
//
//   - THE SECOND SPELLING. AO2 probes two identifiers for exactly one widget —
//     "immediate" first, then "pre_no_interrupt" (courtroom.cpp:1072, mirrored by
//     themedToggleRect, theme_layout.go:444-459) — and AO2's own stock file
//     spells it `pre_no_interrupt`, so that is the only spelling the backstop
//     fills. An author who writes the spelling AO2 reads FIRST would otherwise
//     have written a row that resolves to nothing. Both spellings index the same
//     themeSlots row (themeslots.go:282-284), so the override is written into
//     whichever one the map actually carries, and the draw site's own probe order
//     then finds it. Writing the missing spelling instead would ADD a key, which
//     is the one thing this applier does not do.
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
