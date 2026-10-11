package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestLoadGolden pins the OLD load()'s whole-struct output for a corpus of inputs, so
// deleting load() (and prefsJSON) in the persistence swap does not delete the oracle.
// Each case records the resulting whole-Prefs struct as DECODED JSON, not bytes.
//
// Regenerate with: GOLDEN_REGEN=1 go test ./internal/config -run TestLoadGolden
const loadGoldenName = "load_golden.json"

// loadGoldenCorpus is the corpus: the legacy/migration keys (each written alone so the
// stamp side-effects fire), representative clamped fields at below-min/above-max, and
// the full golden prefs fixture.
func loadGoldenCorpus(t *testing.T) []struct {
	name  string
	input map[string]any
} {
	cases := []struct {
		name  string
		input map[string]any
	}{
		{"legacy_smoothScaling", map[string]any{"smoothScaling": true}},
		{"legacy_spriteScalingMigrated", map[string]any{"spriteScalingMigrated": true, "spriteScaling": 2}},
		{"legacy_autoReconnectDefaultMigrated", map[string]any{"autoReconnectDefaultMigrated": false, "autoReconnect": true}},
		{"legacy_themeFitDefaultMigrated", map[string]any{"themeFitDefaultMigrated": false, "themeFit": 0}},
		{"legacy_blipSpeedDefaultMigrated", map[string]any{"blipSpeedDefaultMigrated": false, "textCrawlMs": 18}},
		{"legacy_deskManifestDefaultMigrated", map[string]any{"deskManifestDefaultMigrated": false, "deskFollowManifest": false}},
		{"legacy_detailedLogDefaultMigrated", map[string]any{"detailedLogDefaultMigrated": false, "detailedLog": false}},
		{"viewportPercent_aboveMax", map[string]any{"viewportPercent": 1000000}},
		{"nameColorVal_belowMin", map[string]any{"nameColorVal": -1000}},
		{"highlightColor_belowMin", map[string]any{"highlightColor": -1000}},
		{"export_partial", map[string]any{"export": map[string]any{"heightPx": 1000000, "fps": 0}}},
		// Multi-field migrations the single-field table cannot see:
		{"layoutProfile_legacyPresets", map[string]any{"layoutPresets": map[string]any{"streamer": map[string]any{"viewport": []any{0, 0, 0.74, 0.66}}}}},
		{"oocScale_inherit", map[string]any{"logScalePct": 170}},
		{"subtheme_sanitize", map[string]any{"subtheme": "../../secret"}},
		{"proxyMode_clamp", map[string]any{"proxyMode": 99}},
	}
	// The full golden prefs fixture, decoded, as one corpus case.
	golden := decodeJSON(t, filepath.Join("testdata", goldenPrefsName))
	cases = append(cases, struct {
		name  string
		input map[string]any
	}{"golden_fixture", golden})
	return cases
}

// loadGoldenOutput runs the OLD load() on an input and returns the whole-Prefs struct
// as decoded JSON.
func loadGoldenOutput(t *testing.T, input map[string]any) map[string]any {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func TestLoadGolden(t *testing.T) {
	cases := loadGoldenCorpus(t)
	dir := filepath.Join("testdata")
	goldenPath := filepath.Join(dir, loadGoldenName)

	// Regenerate under GOLDEN_REGEN=1.
	if os.Getenv("GOLDEN_REGEN") == "1" {
		var out []struct {
			Name   string         `json:"name"`
			Input  map[string]any `json:"input"`
			Output map[string]any `json:"output"`
		}
		for _, c := range cases {
			out = append(out, struct {
				Name   string         `json:"name"`
				Input  map[string]any `json:"input"`
				Output map[string]any `json:"output"`
			}{c.name, c.input, loadGoldenOutput(t, c.input)})
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	var golden []struct {
		Name   string         `json:"name"`
		Input  map[string]any `json:"input"`
		Output map[string]any `json:"output"`
	}
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (regenerate with GOLDEN_REGEN=1)", err)
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	for _, g := range golden {
		got := loadGoldenOutput(t, g.Input)
		if !reflect.DeepEqual(got, g.Output) {
			t.Errorf("load() output changed for %q — the snapshot must reproduce it exactly", g.Name)
		}
	}
}
