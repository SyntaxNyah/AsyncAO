package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestWeatherKindMaxMatchesRender pins the cross-package contract for #124: config can't import
// render, so it hard-codes the highest valid weather index — this proves it still equals
// render.WeatherCount-1, so the Settings cycle and the keybind can reach every weather and a
// hand-edited pref can't select a non-existent one.
func TestWeatherKindMaxMatchesRender(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	last := int(render.WeatherCount) - 1 // Embers
	prefs.SetWeatherType(last)
	if prefs.WeatherType() != last {
		t.Fatalf("the last weather (%d) didn't round-trip (got %d) — config's max is out of sync with render.WeatherCount", last, prefs.WeatherType())
	}
	prefs.SetWeatherType(last + 1) // one past the last valid → must clamp to the last
	if prefs.WeatherType() != last {
		t.Errorf("an out-of-range weather didn't clamp to %d (got %d)", last, prefs.WeatherType())
	}
}
