package ui

// The pane bake's [overrides] reading (L18).
//
// bakePanes used to ask the SIDECAR for a host's override rect, while the layout
// tier asked sidecarOverrideTarget. Two spellings of one policy, and they
// disagreed for one file in three ways at once:
//
//   - an inert key (player_list) is refused by themeKeyEditable, so the tier
//     leaves the widget where the theme put it — and the pane applied the row
//     anyway;
//   - a row written `immediate` gets AO2's second-spelling fold in the tier and
//     in absSlotRect, and got none in the pane;
//   - a player's own layout-editor drag on a pane host was silently overruled by
//     the theme author, inverting the documented precedence (rung 4 beats rung
//     3, themeoverrides.go).
//
// The fix deletes the second reading outright: lay.r already carries the tier's
// answer, so the pane re-bases what it is given and never learns that
// [overrides] exists. Both gates below fail the moment that raw read comes back.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// paneHostFixture stages a themed courtroom with ONE pane hosting one key, and
// one [overrides] row spelled however the caller asks. It returns the pane's
// absolute rect and the host's baked rect.
func paneHostFixture(t *testing.T, host, rowKey string, row theme.Rect, seed map[string]theme.Rect) (a *App, pane, got sdl.Rect) {
	t.Helper()
	a, cleanup := stageThemedCourtroom(t)
	t.Cleanup(cleanup)
	for k, r := range seed {
		a.themeRects[k] = r
		a.themeRectsOrig[k] = r
	}
	sc := theme.NewSidecar()
	sc.Panes = []theme.Pane{{
		ID:     "docket",
		Rect:   theme.Rect{X: 380, Y: 60, W: 300, H: 200},
		Space:  theme.SpaceCourtroom,
		Hosts:  [theme.PaneHostCap]string{host},
		NHosts: 1,
	}}
	sc.Overrides = []theme.KeyRect{{Key: rowKey, Rect: row}}
	if err := applyFixtureSidecar(a, sc); err != nil {
		t.Fatal(err)
	}
	lay := &a.themeLay
	pane, ok := a.paneRect(lay, &sc.Panes[0])
	if !ok {
		t.Fatal("the pane did not resolve — its space is missing from the fixture layout")
	}
	got, present := lay.r[host]
	if !present {
		t.Fatalf("host %q vanished from the layout — a reparent must MOVE a host, never drop it", host)
	}
	return a, pane, got
}

// TestPaneRefusesTheOverrideTheLayoutTierRefused: an [overrides] row addressing a
// key the client will not move must not move it inside a pane either.
//
// player_list is slotStateInert — nothing paints there, so themeKeyEditable
// refuses the row and the layout tier leaves the theme's own rect alone. Under
// the old raw read the pane honoured it, so ONE file produced two different
// answers for one widget depending on whether it happened to be hosted.
func TestPaneRefusesTheOverrideTheLayoutTierRefused(t *testing.T) {
	const host = "player_list"
	// The theme declares the key (the stock AO2 backstop does not), so the pane
	// has something to re-home; the row then tries to move it somewhere else.
	design := theme.Rect{X: 20, Y: 30, W: 120, H: 90}
	row := theme.Rect{X: 200, Y: 150, W: 60, H: 40}
	a, pane, got := paneHostFixture(t, host, host, row, map[string]theme.Rect{host: design})
	lay := &a.themeLay

	// The tier refused the row, so the host keeps its DESIGN rect, re-based onto
	// the pane's origin. That is what a reparent is.
	wantX := pane.X + int32(float64(design.X)*lay.scaleX)
	wantY := pane.Y + int32(float64(design.Y)*lay.scaleY)
	if got.X != wantX || got.Y != wantY {
		t.Errorf("%s baked at (%d,%d), want (%d,%d) — the pane applied an [overrides] row the layout tier "+
			"refuses (themeKeyEditable says this key is inert). One row, two answers.",
			host, got.X, got.Y, wantX, wantY)
	}
	if got.W == int32(float64(row.W)*lay.scaleX) && got.H == int32(float64(row.H)*lay.scaleY) {
		t.Errorf("%s took the refused row's SIZE (%dx%d) — the raw sidecar read is back", host, got.W, got.H)
	}
}

// TestPaneHonoursAO2sOtherSpellingForAPaneHost is the same seam from the other
// side: a row written with the identifier AO2 reads FIRST must reach the widget.
//
// AO2 probes "immediate" then "pre_no_interrupt" for one button
// (courtroom.cpp:1072), and its stock file spells the second — so the backstop
// only ever fills that one. The layout tier folds the alias; the pane's raw read
// did not, so an author who wrote `immediate` moved the button everywhere except
// inside their own pane.
func TestPaneHonoursAO2sOtherSpellingForAPaneHost(t *testing.T) {
	const host = "pre_no_interrupt"
	if _, ok := themeSlotOtherSpelling(host); !ok {
		t.Fatalf("%s no longer has a second spelling — this gate names the wrong pair", host)
	}
	row := theme.Rect{X: 12, Y: 140, W: 80, H: 24}
	a, pane, got := paneHostFixture(t, host, "immediate", row, nil)
	lay := &a.themeLay

	wantX := pane.X + int32(float64(row.X)*lay.scaleX)
	wantY := pane.Y + int32(float64(row.Y)*lay.scaleY)
	if got.X != wantX || got.Y != wantY {
		t.Errorf("%s baked at (%d,%d), want (%d,%d) — a row spelled `immediate` addresses this widget in "+
			"every other tier (sidecarOverrideTarget, absSlotRect) and must here too",
			host, got.X, got.Y, wantX, wantY)
	}
}
