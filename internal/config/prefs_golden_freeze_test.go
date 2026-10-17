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
	loadGoldenSHA256      = "2f52b461d8280db12f391575f1a00d5a2243cf26edf1b4640fe355e78ff75b5d"
	loadGoldenCompactSHA  = "68eef8c2df62d93fdb054d83f6365acf69475c0ce782123d83a3c05ba6fac5e2"
	malformedGoldenSHA    = "8211cdc9008e4e5da754d54de3590c50a6e7dfab7c4cef353ab6cfafc14fec67"
	malformedCompactSHA   = "2280ef31818a2e4123f6febb1f93107d37760255e24faf4d2734f57f4568821f"
	defaultPrefsSHA       = "a279d6b85a783121c6aa8238569fd532918652a7a15ad58d492e44f7a3919105"
	prefsGoldenSHA256     = "64f4503932cbd365f97b016a6a6c8c968a7a5d890242e808335f881c97730c50"
	settersGoldenSHA      = "e55d1fea40fe72ca84796e0c964a3d5f2fc0bda27865c6b7c9fc24d3dd792f8b"
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
		{"prefs_setters_golden.txt", setterGoldenPath, settersGoldenSHA},
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
