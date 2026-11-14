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
	loadGoldenSHA256     = "2f12d3d0ece77327a0c2e4bc5e5be0f051aaa83b88dd41f79413a39bdcd429bd"
	loadGoldenCompactSHA = "d5fc422f5435bcda0a8dea2066dc7c7016cc9acd84196190592902fef61e1234"
	malformedGoldenSHA   = "71c9bc35d1f51380302fee3455848ad9e26e69531afd358d9d9c7f3bf7f98bf1"
	malformedCompactSHA  = "a20792b6c36e1dc8730f83d8949d47d84f3dad3d3ea9d608beda113a3f8a1715"
	defaultPrefsSHA      = "da4b6333aacfcf333cac15dca529aedf7747242744a4cd9b1c737470aed7405d"
	prefsGoldenSHA256    = "32fc839da97d8dd93fc5555036884d1827b796a736c7d934acf97b4d0114ba30"
	settersGoldenSHA     = "4679f9c7120659feee3b12230c588ae6930c89cffc409d8477a0f726f1b758fc"
	settersCompactSHA    = "24be0149ad243bfb76a27e118ba5049662fd8c8d530938a8ff819455a2552ce1"
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
