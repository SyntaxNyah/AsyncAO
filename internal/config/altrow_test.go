package config

import (
	"path/filepath"
	"testing"
)

// TestDisableAltEmoteRowPref pins the Alt+1..9 emote number row kill-switch:
// OFF by default (the row works) and a setter round-trip that also survives a
// save/reload (covering the prefsJSON DTO + load overlay, not just the live
// setter).
func TestDisableAltEmoteRowPref(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := New(path)
	if err != nil {
		t.Fatal(err)
	}

	if p.DisableAltEmoteRowOn() {
		t.Error("the Alt+1..9 number row should default to WORKING (disable flag OFF)")
	}
	p.SetDisableAltEmoteRow(true)
	if !p.DisableAltEmoteRowOn() {
		t.Error("SetDisableAltEmoteRow(true) didn't take")
	}

	if err := p.SaveNow(); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = p.Close()

	re, err := New(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	defer re.Close()
	if !re.DisableAltEmoteRowOn() {
		t.Error("the disabled state did not persist across a save/reload")
	}
}
