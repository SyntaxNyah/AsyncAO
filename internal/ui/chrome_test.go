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

// TestChromePresetsLegible guards every built-in preset (the eye-friendly ones were
// picked without a live view): the body text must stay clearly readable against both the
// panel and the background — comfortably above the kit's minInkSkinContrast floor (48).
func TestChromePresetsLegible(t *testing.T) {
	const idxAccent, idxText = 3, 4
	const idxBg, idxPanel = 0, 1
	const floor = 90 // well above minInkSkinContrast; catches a dark-on-dark blunder
	for i := range chromePresets {
		p := &chromePresets[i]
		tl := colLuma(p.colors[idxText])
		if d := absInt(tl - colLuma(p.colors[idxPanel])); d < floor {
			t.Errorf("%s: text-vs-panel contrast %d < %d (illegible)", p.key, d, floor)
		}
		if d := absInt(tl - colLuma(p.colors[idxBg])); d < floor {
			t.Errorf("%s: text-vs-background contrast %d < %d (illegible)", p.key, d, floor)
		}
		// The accent must read on the panel too (it rings buttons / draws borders).
		if d := absInt(colLuma(p.colors[idxAccent]) - colLuma(p.colors[idxPanel])); d < 40 {
			t.Errorf("%s: accent-vs-panel contrast %d < 40 (accent vanishes)", p.key, d)
		}
	}
}

// TestChromeSoftWarmPresent pins that the eye-friendly presets exist and resolve by key.
func TestChromeSoftWarmPresent(t *testing.T) {
	for _, key := range []string{"soft", "warm"} {
		if chromePresets[chromePresetIndex(key)].key != key {
			t.Errorf("preset %q missing from chromePresets", key)
		}
	}
}
