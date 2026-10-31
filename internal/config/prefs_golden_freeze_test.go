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
	loadGoldenSHA256     = "a93b556349cd45717a6513e71ab6d7467dc1b18886f86c295c6ca0ba66ac8d52"
	loadGoldenCompactSHA = "34831b7251c14aeb655cc51c9d113687b492d95db18de2c8daf691216ff79fe7"
	malformedGoldenSHA   = "6e84ee57a6067d352d2cb999350a723c8eb29f7fc244e908a85b4d4f975a1f8f"
	malformedCompactSHA  = "a20792b6c36e1dc8730f83d8949d47d84f3dad3d3ea9d608beda113a3f8a1715"
	defaultPrefsSHA      = "26b0208bc578c646ae3272bdca4f4d35e49ac198959a4ed6cf22574dbcb81b2b"
	prefsGoldenSHA256    = "faee03086f03dbf15d3fc4e65a0e4289b5b8168abd22e740798de03765cfa548"
	settersGoldenSHA     = "ab77b56f47727cd043545008a780d9dea4ba5c1a65d57e96a1e11bb23bfe2730"
	settersCompactSHA    = "0aa4fcb9e5fc5a5ff4823e9b99a32ed9a3bcc79a3aa09e02ee4529e91e17d65e"
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
