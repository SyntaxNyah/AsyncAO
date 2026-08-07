package ui

// F3 — the performance overlay painted OVER an open dropdown menu.
//
// The kit is single-pass: the last thing painted is the thing on top. drawMenuBar
// used to paint the strip AND the open pane, and App.Frame calls it before the two
// diagnostics overlays — so the F3 graph landed on the menu the user had just
// opened. The kit's OWN dropdown lists never had this problem: they paint in
// Ctx.FinishFrame, which runs last.
//
// The fix is a z-layer split, so these gates are about ORDER and PLACEMENT. That is
// not observable from a return value in a single-pass immediate-mode kit, so they
// read App.Frame's source (see srcgate_test.go for why that is a deletion-catcher
// rather than a mirror).

import "testing"

// TestMenuPanesPaintAboveTheDiagnosticsOverlays pins the layer order inside the one
// function that owns it: strip → diagnostics → pane → the kit's deferred overlays.
func TestMenuPanesPaintAboveTheDiagnosticsOverlays(t *testing.T) {
	body := funcBodySource(t, "app.go", "Frame")
	order := callOrder(body, "drawMenuBar", "drawPerfHUD", "drawDebugOverlay", "drawMenuPanes", "FinishFrame")

	idx := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}
	strip, hud, panes, finish := idx("drawMenuBar"), idx("drawPerfHUD"), idx("drawMenuPanes"), idx("FinishFrame")
	for name, i := range map[string]int{"drawMenuBar": strip, "drawPerfHUD": hud, "drawMenuPanes": panes, "FinishFrame": finish} {
		if i < 0 {
			t.Fatalf("App.Frame no longer calls %s — the z-order it enforces is gone", name)
		}
	}
	if !(strip < hud) {
		t.Error("the menu-bar STRIP must paint before the performance overlay: it is flat client chrome, and the overlay is meant to sit on top of the client")
	}
	if !(hud < panes) {
		t.Error("the open menu PANE must paint AFTER the performance overlay — this is the reported defect: a dropdown covered by the F3 graph (F3)")
	}
	if !(panes < finish) {
		t.Error("the kit's deferred dropdown lists (FinishFrame) must stay the topmost layer — a menu-bar pane must not cover an open combo list")
	}
	if d := idx("drawDebugOverlay"); d >= 0 && !(d < panes) {
		t.Error("the debug overlay is the performance overlay's sibling and must sit under an open pane for the same reason")
	}
}

// TestDrawMenuBarNoLongerPaintsItsPane is the single-responsibility catcher: the
// strip painter must not paint the pane, or merging them back would silently restore
// the bug while the order gate above still passed (drawMenuPanes would just draw
// nothing new).
func TestDrawMenuBarNoLongerPaintsItsPane(t *testing.T) {
	strip := funcBodySource(t, "menubar.go", "drawMenuBar")
	if containsCall(strip, "drawMenuPane") {
		t.Error("drawMenuBar paints a pane again — the strip and the pane are two z-LAYERS, and painting them together puts the pane under the diagnostics overlays (F3)")
	}
	panes := funcBodySource(t, "menubar.go", "drawMenuPanes")
	if !containsCall(panes, "drawMenuPane") {
		t.Error("drawMenuPanes paints nothing — the split dropped the pane instead of moving it")
	}
	// Substitutability: the split must REPLAY phase 1's latch, not re-decide. A pane
	// that consulted live state could paint after the strip had already declined to.
	if !containsCall(panes, "menuBarPaintsNow") {
		t.Error("drawMenuPanes must replay menuBarPaintsNow like drawMenuBar does — otherwise the strip and its pane can disagree about whether the bar is up")
	}
}
