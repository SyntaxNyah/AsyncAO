package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestChromePresetApplies pins #M3: a non-dark preset changes the kit colours; dark
// restores stock; an unknown key falls back to Dark. Restores stock after so the other
// tests in the package (which assert the stock palette) stay unaffected.
func TestChromePresetApplies(t *testing.T) {
	defer func() {
		activeKitColors = defaultKitColors
		applyThemePalette(theme.Palette{})
	}()
	a := &App{ctx: &Ctx{}}

	a.applyChromePreset("light")
	if colLuma(ColPanel) < paletteLightPanelLuma {
		t.Errorf("light preset should give a light panel, luma=%d", colLuma(ColPanel))
	}

	a.applyChromePreset("dark")
	if ColPanel != defaultKitColors[1] {
		t.Errorf("dark preset should restore the stock panel, got %+v", ColPanel)
	}

	a.applyChromePreset("nonsense")
	if ColPanel != defaultKitColors[1] {
		t.Error("an unknown chrome key should fall back to Dark")
	}
}
