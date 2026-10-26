package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStrictLoadClampsTheThreeApprovedFields pins the approved strict-load behaviour:
// a prefs file holding 1 for these three fields reads back clamped at LOAD (not only
// after a setter runs), with no dirty flag. The three values are the ones approved with
// the strict-load decision: 1 -> 3 (bgSlideshowSecs), 1 -> 10 (instantReplaySeconds and
// spotlightStrength).
func TestStrictLoadClampsTheThreeApprovedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prefs.json")
	body := `{"bgSlideshowSecs":1,"instantReplaySeconds":1,"spotlightStrength":1}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Load()
	if s.BgSlideshowSecs != 3 {
		t.Errorf("bgSlideshowSecs: want 3 (clamped at load), got %d", s.BgSlideshowSecs)
	}
	if s.InstantReplaySeconds != 10 {
		t.Errorf("instantReplaySeconds: want 10 (clamped at load), got %d", s.InstantReplaySeconds)
	}
	if s.SpotlightStrength != 10 {
		t.Errorf("spotlightStrength: want 10 (clamped at load), got %d", s.SpotlightStrength)
	}
}
