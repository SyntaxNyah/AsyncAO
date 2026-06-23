package config

import (
	"path/filepath"
	"testing"
)

// TestCallwordOSToastPref pins the #M4 callword desktop-toast pref: OFF by default and a
// round-trip through the setter (covers the load-merge).
func TestCallwordOSToastPref(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if p.CallwordOSToastOn() {
		t.Error("callword OS toast should default OFF")
	}
	p.SetCallwordOSToast(true)
	if !p.CallwordOSToastOn() {
		t.Error("SetCallwordOSToast(true) didn't take")
	}
}
