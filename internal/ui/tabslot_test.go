package ui

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestTabStripDefaultXClearsDockTabs pins issue #2's core invariant: the server-tab
// strip's DEFAULT position sits entirely LEFT of the dock tabs (dockLeftX = the docked
// Log/Music/Areas strip's left edge), so it can never overlap them. This is the
// mechanical check that catches "I moved it but it still covers a dock tab".
func TestTabStripDefaultXClearsDockTabs(t *testing.T) {
	cases := []struct {
		name                string
		total, dockLeftX, w int32
	}{
		// The real default layout: 1152-wide window, vpPct=66 ⇒ stage W=760, so the
		// dock strip's left edge dockLeftX = pad+760+pad = 776. A 1-tab chip is ~150px.
		// This is the exact case the CHANGELOG claims ("defaults clear of them").
		{"default 1152 layout (vpPct=66)", 150, 776, 1152},
		{"default layout, several tabs", 460, 776, 1152},
		{"narrow custom stage", 150, 316, 1152}, // user-shrunk viewport still clears
		{"wide window, long name", 250, 800, 1600},
		{"small window", 120, 300, 1024},
	}
	for _, tc := range cases {
		x := tabStripDefaultX(tc.total, tc.dockLeftX, tc.w)
		if x < 0 {
			t.Errorf("%s: tabStripDefaultX = %d, must be >= 0", tc.name, x)
		}
		if right := x + tc.total; right > tc.dockLeftX {
			t.Errorf("%s: strip right edge %d overlaps the dock tabs at %d (issue #2)", tc.name, right, tc.dockLeftX)
		}
	}
	// Hidden log (dockLeftX >= w) and pre-courtroom (dockLeftX == 0) both fall back to
	// the original window-centre — there are no dock tabs to clear.
	wantCentre := (int32(1152) - 160) / 2
	if got := tabStripDefaultX(160, 0, 1152); got != wantCentre {
		t.Errorf("pre-courtroom fallback = %d, want window-centre %d", got, wantCentre)
	}
	if got := tabStripDefaultX(160, 5000, 1152); got != wantCentre {
		t.Errorf("hidden-log fallback = %d, want window-centre %d", got, wantCentre)
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
