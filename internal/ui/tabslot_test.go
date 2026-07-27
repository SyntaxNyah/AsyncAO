package ui

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestTabStripDefaultXCentresInTheWindow replaces TestTabStripDefaultXClearsDockTabs.
//
// WHAT CHANGED AND WHY. The old test pinned issue #2's workaround: the strip FLOATED
// over the stage, so its default X was centred in the gap LEFT of the docked
// Log/Music/Areas column and the test asserted `x + total <= dockLeftX`. Issue #14
// reported the consequence — the strip sits well off to one side ("it doesn't even
// center itself") and, under a theme, on top of the IC log. The strip now DOCKS in a
// reserved band of its own (chrometop.go) and every screen, dock tabs included,
// starts below that band. Non-overlap is therefore structural (a Y property, pinned
// by TestTabStripDocksInsideTheChromeBand below) and the X arithmetic is free to do
// the thing the reporter asked for: centre in the window. The old expectation is not
// loosened, it is superseded — a strip centred in the window would FAIL it, and that
// is now the correct behaviour.
func TestTabStripDefaultXCentresInTheWindow(t *testing.T) {
	cases := []struct {
		name     string
		total, w int32
	}{
		{"default 1152 layout, one tab", 150, 1152},
		{"default layout, several tabs", 460, 1152},
		{"wide window, long name", 250, 1600},
		{"small window", 120, 1024},
		{"minimum window", 120, config.MinWindowW},
	}
	for _, tc := range cases {
		x := tabStripDefaultX(tc.total, tc.w)
		if want := (tc.w - tc.total) / 2; x != want {
			t.Errorf("%s: tabStripDefaultX = %d, want the window centre %d", tc.name, x, want)
		}
		if x < 0 {
			t.Errorf("%s: tabStripDefaultX = %d, must be >= 0", tc.name, x)
		}
	}
	// A strip wider than the window still starts on-screen rather than half off the
	// left edge (the clamp), which is the only case centring alone cannot handle.
	if got := tabStripDefaultX(2000, config.MinWindowW); got != 0 {
		t.Errorf("an over-wide strip = %d, want 0 (clamped to the left edge)", got)
	}
}

// TestTabStripDocksInsideTheChromeBand pins the structural half of the #14 fix: with
// no override the strip's rects land in the band between the menu bar and the screen
// content, so nothing a screen draws can be underneath it. This is the invariant that
// replaces the old "clears the dock tabs" X check.
func TestTabStripDocksInsideTheChromeBand(t *testing.T) {
	a := testTabApp(t)
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	const w, h = 1152, 864
	rects, add := a.tabBarRects(w, h)
	if len(rects) != 1 {
		t.Fatalf("tabBarRects returned %d chip rects, want 1", len(rects))
	}
	band := a.topChromeH()
	if band != menuBarH+tabBarH {
		t.Fatalf("topChromeH = %d, want menu bar + strip (%d)", band, menuBarH+tabBarH)
	}
	for _, r := range append(rects, add) {
		if r.W == 0 { // the "+" chip is absent at the tab cap
			continue
		}
		if r.Y != a.menuBarHeight() {
			t.Errorf("chip Y = %d, want it docked under the menu bar at %d", r.Y, a.menuBarHeight())
		}
		if r.Y+r.H > band {
			t.Errorf("chip bottom %d escapes the reserved band %d — it would cover screen content", r.Y+r.H, band)
		}
	}
	// A layout-editor override lifts the strip out of the band, and the band goes
	// with it: reserving space for a strip that is no longer there would leave a dead
	// gap at the top of every screen.
	a.classicOv = map[string][4]float64{slotTabBar: {0.1, 0.5, 0.16, 0.03}}
	if got := a.tabStripBandH(); got != 0 {
		t.Errorf("with a slotTabBar override the reserved band = %d, want 0", got)
	}
	// No sessions at all ⇒ no strip ⇒ no band (a first-run lobby has no dead gap).
	a.classicOv = nil
	a.tabs = nil
	if got := a.tabStripBandH(); got != 0 {
		t.Errorf("with no tabs the reserved band = %d, want 0", got)
	}
}

// TestTabBarSlotOverrideRepositions pins that the strip follows a layout-editor drag:
// with a "tabbar" override present, its rect comes from the stored window-fraction, not
// the computed default — the mechanism that lets users move it themselves (issue #2).
func TestTabBarSlotOverrideRepositions(t *testing.T) {
	a := testTabApp(t)
	def := sdl.Rect{X: 500, Y: 0, W: 160, H: tabBarH}
	if got := a.slotRect(slotTabBar, def, 1000, 800); got != def {
		t.Errorf("no override: slotRect = %+v, want the default %+v", got, def)
	}
	a.classicOv = map[string][4]float64{slotTabBar: {0.1, 0.5, 0.16, 0.03}}
	want := sdl.Rect{X: 100, Y: 400, W: 160, H: 24}
	if got := a.slotRect(slotTabBar, def, 1000, 800); got != want {
		t.Errorf("override: slotRect = %+v, want the moved spot %+v", got, want)
	}
}

// TestTabBarSlotIsMoveOnlyAndRegisters pins the editor integration: the strip is
// move-only (its width comes from the chips, so resize is meaningless) and registers
// itself while editing so drawClassicEditor can grab it.
func TestTabBarSlotIsMoveOnlyAndRegisters(t *testing.T) {
	if slotResizable(slotTabBar) {
		t.Error("the server-tab strip must be move-only (width is chip-driven)")
	}
	if got := classicSlotLabel(slotTabBar); !strings.Contains(got, "Server tabs") {
		t.Errorf("classicSlotLabel(slotTabBar) = %q, want it to name the Server tabs", got)
	}
	a := testTabApp(t)
	a.classicEdit = true
	a.slotRect(slotTabBar, sdl.Rect{X: 10, Y: 0, W: 100, H: tabBarH}, 1000, 800)
	if _, ok := a.slotReg[slotTabBar]; !ok {
		t.Error("while editing, the strip must register its slot so the editor hands it drag handles")
	}
}
