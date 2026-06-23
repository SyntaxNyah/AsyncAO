package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAutoStatusPref pins the #M1 auto-status config: OFF by default with words
// pre-filled, and a round-trip that clamps oversize word fields.
func TestAutoStatusPref(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	def := p.AutoStatus()
	if def.Enabled {
		t.Error("auto-status should default OFF")
	}
	if def.AFKWords == "" {
		t.Error("default AFK words should be pre-filled so enabling just works")
	}

	long := strings.Repeat("z", 500)
	p.SetAutoStatus(AutoStatusPref{Enabled: true, AFKWords: "brb", BusyWords: long})
	got := p.AutoStatus()
	if !got.Enabled || got.AFKWords != "brb" {
		t.Errorf("round trip = %+v", got)
	}
	if len(got.BusyWords) > autoStatusWordsMax {
		t.Errorf("busy words not clamped: %d > %d", len(got.BusyWords), autoStatusWordsMax)
	}
}
