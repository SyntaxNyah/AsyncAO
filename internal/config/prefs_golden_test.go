package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Golden preferences fixtures live in testdata/. They are SYNTHETIC — built by
// fillGoldenPrefs, which gives every exported field a distinct non-zero value so an
// omitempty field actually appears and every map/slice is non-empty — so the file
// never leaks a real username, home path or server address.
//
// TestGoldenPrefsRoundTrip loads the golden through the real New()/SaveNow() path and
// compares the result as DECODED JSON (map[string]any), not bytes: a later restructure
// that regroups fields into embedded structs may reorder keys and must not change the
// key set or any value, and a duplicate-key collision from embedding must not silently
// drop a field.
//
// Regenerate with: GOLDEN_REGEN=1 go test ./internal/config -run TestGoldenFixtureRegenerate

const goldenPrefsName = "prefs_golden.json"
const defaultPrefsName = "prefs_default.json"

// fillDistinctValue writes a distinct, non-zero value into v, recursing into structs,
// slices, arrays, maps and pointers. c is a shared counter so every scalar across the
// whole tree is different — that is what makes a duplicated-key drop visible: two
// embedded fields that fold onto the same JSON key would otherwise carry the same
// value and hide the collision.
func fillDistinctValue(v reflect.Value, c *int) {
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.String:
		*c++
		v.SetString("v" + itoa(*c))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		*c++
		// Small values so enum-coded prefs (sprite scaling, char casing, texture
		// compression, …) stay inside their valid range; the counter only picks 1 or 2.
		v.SetInt(int64(1 + (*c % 2)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		*c++
		v.SetUint(uint64(1 + (*c % 2)))
	case reflect.Float32, reflect.Float64:
		*c++
		v.SetFloat(0.5)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillDistinctValue(v.Index(0), c)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fillDistinctValue(v.Index(i), c)
		}
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		k := reflect.New(v.Type().Key()).Elem()
		el := reflect.New(v.Type().Elem()).Elem()
		fillDistinctValue(k, c)
		fillDistinctValue(el, c)
		v.SetMapIndex(k, el)
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		fillDistinctValue(v.Elem(), c)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanSet() {
				continue
			}
			// time.Duration is an int64 we fill as a duration, not a wall clock.
			if f.Type().PkgPath() == "time" {
				if f.Kind() == reflect.Int64 {
					*c++
					f.SetInt(int64(*c))
				}
				continue
			}
			fillDistinctValue(f, c)
		}
	case reflect.Interface:
		// No AssetPreferences field is an untyped interface; leave nil.
	default: // chan, func, complex, unsafe.Pointer: not present in prefs
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n%10)) + itoa(n/10)
}

// fillGoldenPrefs returns an AssetPreferences with every exported field populated
// distinctly, ready to Save.
func fillGoldenPrefs() *AssetPreferences {
	p := &AssetPreferences{}
	c := 0
	v := reflect.ValueOf(p).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanSet() {
			continue
		}
		fillDistinctValue(f, &c)
	}
	// Pin the two fields whose valid range the generic fill undershoots: the
	// animated-sprite cap has a 240px floor (a value below it reads as "off"), and
	// an anchor needs a two-letter mode code plus a positive window size.
	p.AnimatedSpriteCapPxVal = AnimatedSpriteCapMinPx
	p.ClassicAnchors = map[string]ClassicAnchor{"cc": {Mode: "cc", WinW: 800, WinH: 600}}
	return p
}

// TestGoldenFixtureRegenerate rebuilds the committed golden fixtures. One-shot: it
// runs only under GOLDEN_REGEN=1 so a normal test run never rewrites checked-in bytes.
func TestGoldenFixtureRegenerate(t *testing.T) {
	if os.Getenv("GOLDEN_REGEN") != "1" {
		t.Skip("set GOLDEN_REGEN=1 to regenerate testdata/*.json")
	}
	dir := filepath.Join("testdata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	full := fillGoldenPrefs()
	full.path = filepath.Join(dir, goldenPrefsName)
	if err := full.SaveNow(); err != nil {
		t.Fatal(err)
	}
	// Load back and save once more so the committed golden is the STABLE (post-clamp)
	// state: several numeric prefs clamp to a valid floor/ceiling on load, so the raw
	// filled values do not round-trip. The stable golden does, and the round-trip test
	// then asserts idempotence — the property a restructure must preserve.
	reloaded, err := New(full.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.SaveNow(); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Close(); err != nil {
		t.Fatal(err)
	}
	defPath := filepath.Join(dir, defaultPrefsName)
	def, err := New(defPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.SaveNow(); err != nil {
		t.Fatal(err)
	}
	if err := def.Close(); err != nil {
		t.Fatal(err)
	}
}

// decodeJSON reads a file and decodes it into map[string]any, so ordering and
// indentation never matter.
func decodeJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return m
}

// assertSameKeysAndValues reports key-set and value differences between golden and
// saved (both decoded).
func assertSameKeysAndValues(t *testing.T, golden, saved map[string]any) {
	t.Helper()
	for k := range golden {
		if _, ok := saved[k]; !ok {
			t.Errorf("key %q present in golden but missing after round-trip", k)
		}
	}
	for k := range saved {
		if _, ok := golden[k]; !ok {
			t.Errorf("key %q appeared after round-trip but is not in the golden", k)
		}
	}
	if len(golden) != len(saved) {
		t.Errorf("key-set size differs: golden %d, saved %d", len(golden), len(saved))
	}
	if !reflect.DeepEqual(golden, saved) {
		for k, gv := range golden {
			if sv, ok := saved[k]; ok && !reflect.DeepEqual(gv, sv) {
				t.Errorf("value for %q differs: golden %v, saved %v", k, gv, sv)
			}
		}
	}
}

// TestGoldenPrefsRoundTrip loads the golden through the real New()/SaveNow() path and
// asserts the result is identical (as decoded JSON) to the committed golden: same key
// set, same values. This is the guard the AssetPreferences→subpackage restructure must
// keep green.
func TestGoldenPrefsRoundTrip(t *testing.T) {
	goldenPath := filepath.Join("testdata", goldenPrefsName)
	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	golden := decodeJSON(t, goldenPath)

	out := filepath.Join(t.TempDir(), "prefs.json")
	if err := os.WriteFile(out, goldenBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(out)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if err := p.SaveNow(); err != nil {
		t.Fatalf("SaveNow: %v", err)
	}
	saved := decodeJSON(t, out)
	assertSameKeysAndValues(t, golden, saved)
}

// TestGoldenDefaultPrefsRoundTrip is the same gate for the default (New()) output: a
// fresh, default prefs file must load and re-save to the same key set and values.
func TestGoldenDefaultPrefsRoundTrip(t *testing.T) {
	golden := decodeJSON(t, filepath.Join("testdata", defaultPrefsName))
	out := filepath.Join(t.TempDir(), "prefs.json")
	p, err := New(out)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if err := p.SaveNow(); err != nil {
		t.Fatalf("SaveNow: %v", err)
	}
	saved := decodeJSON(t, out)
	assertSameKeysAndValues(t, golden, saved)
}
