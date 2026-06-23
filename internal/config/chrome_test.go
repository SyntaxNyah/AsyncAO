package config

import (
	"path/filepath"
	"testing"
)

// TestChromeThemePref pins the #M3 chrome-theme pref: "dark" default + round-trip.
func TestChromeThemePref(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if got := p.ChromeTheme(); got != "dark" {
		t.Errorf("default chrome theme = %q, want dark", got)
	}
	p.SetChromeTheme("midnight")
	if got := p.ChromeTheme(); got != "midnight" {
		t.Errorf("after set = %q, want midnight", got)
	}
}
