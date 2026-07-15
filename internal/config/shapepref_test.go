package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestChromeShapePref pins the A5 chrome-shape pref: "sharp" default, set/get,
// and a disk round-trip through the save side (AssetPreferences json tags) AND
// the load side (prefsJSON DTO + sanitised overlay).
func TestChromeShapePref(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if got := p.ChromeShape(); got != defaultChromeShape {
		t.Fatalf("default chrome shape = %q, want %q", got, defaultChromeShape)
	}
	if got := p.ChromeShapeTier(); got != 0 {
		t.Fatalf("default chrome shape tier = %d, want 0", got)
	}
	p.SetChromeShape(chromeShapePill)
	p.SetChromeShapeTier(2)
	if got := p.ChromeShape(); got != chromeShapePill {
		t.Fatalf("after set = %q, want %q", got, chromeShapePill)
	}
	if got := p.ChromeShapeTier(); got != 2 {
		t.Fatalf("after set tier = %d, want 2", got)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := q.ChromeShape(); got != chromeShapePill {
		t.Fatalf("shape lost across disk: %q, want %q", got, chromeShapePill)
	}
	if got := q.ChromeShapeTier(); got != 2 {
		t.Fatalf("tier lost across disk: %d, want 2", got)
	}
}

// TestChromeShapeSanitize pins the "unknown key -> sharp" ruling on both the
// setter and the load overlay, plus the tier clamp.
func TestChromeShapeSanitize(t *testing.T) {
	// Setter path: a bogus key is stored as the byte-identical default.
	p := &AssetPreferences{}
	p.SetChromeShape("hexagon")
	if got := p.ChromeShape(); got != defaultChromeShape {
		t.Fatalf("SetChromeShape(bogus) = %q, want %q", got, defaultChromeShape)
	}
	p.SetChromeShape(chromeShapeRounded)
	if got := p.ChromeShape(); got != chromeShapeRounded {
		t.Fatalf("SetChromeShape(rounded) = %q, want %q", got, chromeShapeRounded)
	}

	// A zero-value / blank stored key must also resolve to the default (so a
	// pref file that predates A5 renders exactly like today).
	blank := &AssetPreferences{}
	if got := blank.ChromeShape(); got != defaultChromeShape {
		t.Fatalf("blank ChromeShapeKey = %q, want %q", got, defaultChromeShape)
	}

	// Tier clamp on the setter.
	p.SetChromeShapeTier(-5)
	if got := p.ChromeShapeTier(); got != 0 {
		t.Fatalf("SetChromeShapeTier(-5) = %d, want 0", got)
	}
	p.SetChromeShapeTier(9999)
	if got := p.ChromeShapeTier(); got != shapeRadiusTiers-1 {
		t.Fatalf("SetChromeShapeTier(9999) = %d, want %d", got, shapeRadiusTiers-1)
	}

	// Load overlay path: a garbage key + out-of-range tier written straight to
	// disk must sanitise when re-read through load() (sanitizeChromeShape /
	// clampChromeShapeTier in the load overlay), so a hand-edited prefs file
	// can't smuggle an undrawable shape or tier into memory.
	path := filepath.Join(t.TempDir(), PrefsFileName)
	if err := os.WriteFile(path, []byte(`{"chromeShape":"hexagon","chromeShapeTier":42}`), 0o644); err != nil {
		t.Fatalf("write prefs: %v", err)
	}
	out, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := out.ChromeShape(); got != defaultChromeShape {
		t.Fatalf("overlay unknown shape = %q, want %q", got, defaultChromeShape)
	}
	if got := out.ChromeShapeTier(); got != shapeRadiusTiers-1 {
		t.Fatalf("overlay huge tier = %d, want %d", got, shapeRadiusTiers-1)
	}
}
