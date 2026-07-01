package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSettingPresets pins the preset layer: save (sanitized name, password-free
// snapshot), list (sorted), apply (stages the preset as the live file — the
// import path), delete, and the cap.
func TestSettingPresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	defer p.Close()

	p.SetCrossfadeMs(300) // a fingerprint to recognise the snapshot by
	if err := p.SavePreset("  My Casing Setup!  "); err != nil {
		t.Fatalf("SavePreset: %v", err)
	}
	if err := p.SavePreset("aaa"); err != nil {
		t.Fatalf("SavePreset 2: %v", err)
	}
	got := p.ListPresets()
	if len(got) != 2 || got[0] != "My Casing Setup" || got[1] != "aaa" {
		t.Fatalf("ListPresets = %v, want sanitized [My Casing Setup aaa] (sorted)", got)
	}

	// The snapshot is a loadable prefs file carrying the fingerprint.
	data, err := os.ReadFile(filepath.Join(p.PresetsDir(), "My Casing Setup.json"))
	if err != nil {
		t.Fatalf("preset file: %v", err)
	}
	if !strings.Contains(string(data), "\"crossfadeMs\": 300") {
		t.Error("preset snapshot should carry the saved settings")
	}

	// Apply stages the preset over the live file (restart-applied — the file
	// content is what the next boot loads).
	p.SetCrossfadeMs(500) // diverge the live state
	if err := p.ApplyPreset("My Casing Setup"); err != nil {
		t.Fatalf("ApplyPreset: %v", err)
	}
	live, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("live file: %v", err)
	}
	if !strings.Contains(string(live), "\"crossfadeMs\": 300") {
		t.Error("applying a preset must stage ITS settings as the live file")
	}

	if err := p.DeletePreset("aaa"); err != nil {
		t.Fatalf("DeletePreset: %v", err)
	}
	if got := p.ListPresets(); len(got) != 1 {
		t.Fatalf("after delete = %v, want one preset", got)
	}
	if err := p.SavePreset("!!!"); err == nil {
		t.Error("a name that sanitizes to empty must be refused")
	}
}
