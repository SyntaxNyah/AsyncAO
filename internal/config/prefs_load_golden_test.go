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

// loadGoldenCompactCase is the on-disk (compact) form: Output is stored as a delta
// against the default prefs (prefs_default.json) so the frozen corpus stays small.
type loadGoldenCompactCase struct {
	Name        string         `json:"name"`
	Input       map[string]any `json:"input"`
	OutputDelta map[string]any `json:"output_delta"`
}

// loadGoldenFullCase is the EXPANDED form the old load_golden.json stored on disk.
type loadGoldenFullCase struct {
	Name   string         `json:"name"`
	Input  map[string]any `json:"input"`
	Output map[string]any `json:"output"`
}

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
	cases = append(cases, sanitizeGarbageCases()...)
	// The full golden prefs fixture, decoded, as one corpus case.
	golden := decodeJSON(t, filepath.Join("testdata", goldenPrefsName))
	cases = append(cases, struct {
		name  string
		input map[string]any
	}{"golden_fixture", golden})
	return cases
}

// sanitizeGarbageCases are hand-editable-file garbage inputs for the load-side
// sanitize helpers: over-cap maps/slices, blank names/ids, and out-of-range values.
// Each helper's clean-up must reproduce load()'s output exactly, not just pass
// absent/zero — this is the golden for rows 5-15 of the persistence-swap migration
// list (the sanitize tail the single-field normalize() does not yet cover).
func sanitizeGarbageCases() []struct {
	name  string
	input map[string]any
} {
	return []struct {
		name  string
		input map[string]any
	}{
		{"panelFonts_garbage", map[string]any{"panelFonts": map[string]any{
			"  ID1  ": map[string]any{"family": "  Arial  ", "sizePx": 999999},
			"id2":     map[string]any{"family": "very-long-family-name-over-max-len", "sizePx": -5},
			"":        map[string]any{"family": "x"},
		}}},
		{"macros_garbage", map[string]any{"macros": []any{
			map[string]any{"name": "  m1  ", "key": "  KEY1  ", "lines": []any{"  l1  ", "", "  l2  "}},
		}}},
		{"stylePresets_garbage", map[string]any{"stylePresets": []any{
			map[string]any{"name": "  ", "key": "K"},
			map[string]any{"name": "ok", "key": "K2", "style": map[string]any{"opacity": 255}},
		}}},
		{"chromeShape_garbage", map[string]any{"chromeShape": "garbage"}},
		{"hiddenPanels_garbage", map[string]any{"hiddenPanels": []any{"a", "a", "", "b"}}},
		{"classicLayout_garbage", map[string]any{"classicLayout": map[string]any{
			"a": []any{0, 0, 0.5, 0.5},
			"b": []any{0, 0, -1, 1},
			"c": []any{5, 0, 0.5, 0.5},
		}}},
		{"layoutGridPx_garbage", map[string]any{"layoutGridPx": 100000}},
		{"classicAnchors_garbage", map[string]any{"classicAnchors": map[string]any{
			"a": map[string]any{"mode": "garbage", "winW": 999999, "winH": -5},
		}}},
		{"classicRotations_garbage", map[string]any{"classicRotations": map[string]any{
			"a": 90, "b": 180, "": 45,
		}}},
		{"themeRectRotations_garbage", map[string]any{"themeRectRotations": map[string]any{
			"t1": map[string]any{"k1": 90, "": 45},
			"":   map[string]any{"k": 1},
		}}},
		{"layoutProfiles_garbage", map[string]any{"layoutProfiles": map[string]any{
			"p1": map[string]any{"classic": map[string]any{"a": []any{0, 0, -1, 1}}, "gridPx": 99999},
			"":   map[string]any{"hidden": []any{"x"}},
		}}},
	}
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

	// Regenerate under GOLDEN_REGEN=1 (compact delta form).
	if os.Getenv("GOLDEN_REGEN") == "1" {
		defaults := prefsDefaults(t)
		out := make([]loadGoldenCompactCase, 0, len(cases))
		for _, c := range cases {
			out = append(out, loadGoldenCompactCase{c.name, c.input, prefsDelta(defaults, loadGoldenOutput(t, c.input))})
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

	defaults := prefsDefaults(t)
	var golden []loadGoldenCompactCase
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (regenerate with GOLDEN_REGEN=1)", err)
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	for _, g := range golden {
		want := applyPrefsDelta(defaults, g.OutputDelta)
		got := loadGoldenOutput(t, g.Input)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("load() output changed for %q — the snapshot must reproduce it exactly", g.Name)
		}
	}
}
