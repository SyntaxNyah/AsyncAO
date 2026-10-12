package config

// Frozen golden data. The persistence swap's oracle reads load_golden.json (the
// whole-struct output of the OLD load()) and prefs_default.json (the default fixture).
// Their SHA-256 is pinned here so a change to either must be a deliberate, reviewed edit
// to these constants — never a silent regeneration drift.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

const (
	loadGoldenSHA256  = "2f52b461d8280db12f391575f1a00d5a2243cf26edf1b4640fe355e78ff75b5d"
	defaultPrefsSHA   = "a279d6b85a783121c6aa8238569fd532918652a7a15ad58d492e44f7a3919105"
)

// TestGoldenFilesFrozen pins the two data files the persistence-swap oracle depends on
// to byte-identical SHA-256 constants. Regenerating (GOLDEN_REGEN=1) is refused under CI,
// and skipped locally (the constants are then updated by hand, surfacing in review).
func TestGoldenFilesFrozen(t *testing.T) {
	if os.Getenv("GOLDEN_REGEN") == "1" {
		if os.Getenv("CI") != "" {
			t.Fatal("GOLDEN_REGEN=1 refused under CI — regenerate locally, then hand-edit the frozen constants")
		}
		t.Skip("regenerating golden locally; update the frozen constants by hand afterward")
	}
	for _, c := range []struct {
		name, path, want string
	}{
		{"load_golden.json", filepath.Join("testdata", loadGoldenName), loadGoldenSHA256},
		{"prefs_default.json", filepath.Join("testdata", "prefs_default.json"), defaultPrefsSHA},
	} {
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("read %s: %v", c.name, err)
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != c.want {
			t.Errorf("%s SHA-256 changed: got %s, want %s — edit the frozen constant deliberately", c.name, got, c.want)
		}
	}
}
