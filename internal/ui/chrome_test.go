package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/veandco/go-sdl2/sdl"
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

// TestCustomChromeApplies pins the user "Custom" scheme: filled hex slots apply,
// blank slots stay stock dark, and the readability floor forces dark text on a
// light custom panel so a user can't paint the UI into an unreadable corner.
func TestCustomChromeApplies(t *testing.T) {
	defer func() {
		activeKitColors = defaultKitColors
		applyThemePalette(theme.Palette{})
	}()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{ctx: &Ctx{}, d: Deps{Prefs: prefs}}

	// All-blank custom = stock dark in every slot.
	a.applyChromePreset(chromeCustomKey)
	if ColPanel != defaultKitColors[1] || ColAccent != defaultKitColors[3] {
		t.Fatalf("blank custom should be stock dark: panel=%+v accent=%+v", ColPanel, ColAccent)
	}

	// Filled panel + accent apply; the blank slots stay stock.
	prefs.SetCustomChrome([7]string{"", "203040", "", "ff8800", "", "", ""})
	a.applyChromePreset(chromeCustomKey)
	if want := (sdl.Color{R: 0x20, G: 0x30, B: 0x40, A: 255}); ColPanel != want {
		t.Errorf("custom panel = %+v, want %+v", ColPanel, want)
	}
	if want := (sdl.Color{R: 0xff, G: 0x88, B: 0x00, A: 255}); ColAccent != want {
		t.Errorf("custom accent = %+v, want %+v", ColAccent, want)
	}

	// Readability floor: a light panel with the (unset → stock light) text would be
	// illegible, so Text is forced dark — the user can't get stuck on invisible text.
	prefs.SetCustomChrome([7]string{"", "f0f0f0", "", "", "", "", ""})
	a.applyChromePreset(chromeCustomKey)
	if colLuma(ColText) >= paletteLightPanelLuma {
		t.Errorf("light custom panel should force dark text, got text luma %d", colLuma(ColText))
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
