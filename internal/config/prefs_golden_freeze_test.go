package config

// Frozen golden data. The persistence swap's oracle reads these data files. Their
// SHA-256 is pinned here — computed on CR-STRIPPED bytes, so a CRLF vs LF checkout
// cannot drift them — making any change a deliberate, reviewed edit to these
// constants, never a silent regeneration drift.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

const (
	loadGoldenSHA256     = "3db15fe064a5ec56633931b09ca5d75d52c35887190b7b3d0ddc1d83241d44cf"
	loadGoldenCompactSHA = "22e0cb397d2c5b66e5cefe4ff8308b9c1ffe10ad88b61a177fd211370ade6f12"
	malformedGoldenSHA   = "238bea9dc510686b35e0c62930bddbd16d4812668108d99f44e869e867c0f803"
	malformedCompactSHA  = "a20792b6c36e1dc8730f83d8949d47d84f3dad3d3ea9d608beda113a3f8a1715"
	defaultPrefsSHA      = "0641337698646339fb7f69692257a2cc8d959897d649209b9c2090bf6a9f63eb"
	prefsGoldenSHA256    = "ed34a7b79c93b1c1fbd7359493d738a938c6c97201c5f3af284a25e0e4058619"
	settersGoldenSHA     = "ac8d84e5f22e5d3436f4bf74b560612362326c300c0acde6254d8467de6b1cd3"
	settersCompactSHA    = "c0f7f337733f794fd31f4bf02bc0494b2876daf8bfb9683863a3ed2c684d0fd1"
)

// TestGoldenFilesFrozen pins the four data files the persistence-swap oracle depends on
// to byte-identical SHA-256 constants (CR stripped, so a CRLF/LF checkout cannot drift
// them). Regenerating (GOLDEN_REGEN=1) is refused under CI, and skipped locally (the
// constants are then updated by hand, surfacing in review).
func TestGoldenFilesFrozen(t *testing.T) {
	if os.Getenv("GOLDEN_REGEN") == "1" {
		t.Fatal("GOLDEN_REGEN=1 refuses the freeze check — regenerate by name only (go test ./internal/config -run <RegenerateTest>), hand-edit the frozen constants, then re-run without GOLDEN_REGEN")
	}
	for _, c := range []struct {
		name, path, want string
	}{
		{"load_golden.json", filepath.Join("testdata", loadGoldenName), loadGoldenCompactSHA},
		{"malformed_golden.json", filepath.Join("testdata", malformedGoldenName), malformedCompactSHA},
		{"prefs_default.json", filepath.Join("testdata", defaultPrefsName), defaultPrefsSHA},
		{"prefs_golden.json", filepath.Join("testdata", goldenPrefsName), prefsGoldenSHA256},
		{"prefs_setters_golden.txt", setterGoldenPath, settersCompactSHA},
	} {
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("read %s: %v", c.name, err)
		}
		b = bytes.ReplaceAll(b, []byte("\r"), nil)
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != c.want {
			t.Errorf("%s SHA-256 changed: got %s, want %s — edit the frozen constant deliberately", c.name, got, c.want)
		}
	}
}
