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
	loadGoldenSHA256     = "c145f46aaafe7687bd3f1eb74e2269477d106318c88c1c735ad888c5481f96b5"
	loadGoldenCompactSHA = "b2d0ea4712c226a4e92700ab0f0cae0f002784c0bee95127e89b2f48acd065d2"
	malformedGoldenSHA   = "5fbecb2ee822db25c0a4fda90277386d31677fc3b05db63fa613081ffde22d46"
	malformedCompactSHA  = "a20792b6c36e1dc8730f83d8949d47d84f3dad3d3ea9d608beda113a3f8a1715"
	defaultPrefsSHA      = "da5c9c299abf84907c5bd0e73f0adc948dee6903e6ee9b412f9310ccfbf1db8d"
	prefsGoldenSHA256    = "d734b3d604eff3d309ecd780f5683ec362564879e4ec5699e95e8add977fae8e"
	settersGoldenSHA     = "298fc4503e894f623e6027f5933a716da522e0bc17c2d44f3cb6601501da715d"
	settersCompactSHA    = "53514adfaddf304650f5810eff6b3be998f4c057b5e564471400ba9c6b5918f9"
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
