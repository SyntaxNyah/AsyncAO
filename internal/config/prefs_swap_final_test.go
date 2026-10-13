package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestAssetPrefsJSONKeysUnique pins the post-swap invariant that replaced the old
// "prefsJSON twin" gate: with a single struct, a duplicate json key across two fields
// (or a json:"-" that should serialize) would silently drop data on the direct unmarshal
// load() now performs. Every exported, json-tagged field must have a unique key.
func TestAssetPrefsJSONKeysUnique(t *testing.T) {
	seen := map[string]string{}
	var walk func(typ reflect.Type)
	walk = func(typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.PkgPath != "" { // unexported: encoding/json ignores it
				continue
			}
			tag := f.Tag.Get("json")
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
				walk(f.Type)
				continue
			}
			if name == "" {
				name = f.Name
			}
			if prev, ok := seen[name]; ok {
				t.Errorf("duplicate json key %q on %s and %s", name, prev, f.Name)
			}
			seen[name] = f.Name
		}
	}
	walk(reflect.TypeOf(AssetPreferences{}))
}

// TestUnknownKeysIgnored proves a file with unknown keys at the root and inside nested
// objects loads identically to the same file without them — encoding/json drops unknown
// keys, so an unrecognised hand-edited key can never change a preference.
func TestUnknownKeysIgnored(t *testing.T) {
	base := map[string]any{
		"smoothScaling": true,
		"export":        map[string]any{"heightPx": 360, "loop": true},
	}
	withUnknown := map[string]any{
		"smoothScaling": true,
		"export":        map[string]any{"heightPx": 360, "loop": true, "zzzNested": "x"},
		"zzzUnknown":    1,
		"zzzNestedObj":  map[string]any{"a": 1},
	}
	baseBytes, _ := json.Marshal(base)
	unknownBytes, _ := json.Marshal(withUnknown)

	bp, berr := snapshotLoad(baseBytes)
	if berr != nil {
		t.Fatalf("base: %v", berr)
	}
	up, uerr := snapshotLoad(unknownBytes)
	if uerr != nil {
		t.Fatalf("unknown: %v", uerr)
	}
	if diffs := diffJSON(dumpPrefs(bp), dumpPrefs(up), ""); len(diffs) > 0 {
		t.Errorf("unknown keys changed the load: %v", diffs)
	}
}

// TestNullReadsAsAbsent pins the presence pre-pass's null-as-absent rule: an explicit
// JSON null for a key must read exactly like the key absent (the old pointer-field load()
// treated a nil pointer as "key not written"). Dropping the rule would flip
// presence["export"] (and friends) to true on `{"export": null}` and change the result.
func TestNullReadsAsAbsent(t *testing.T) {
	withNull := map[string]any{"export": nil, "subtheme": nil, "proxyMode": nil, "discord": nil}
	absent := map[string]any{}

	nb, _ := json.Marshal(withNull)
	ab, _ := json.Marshal(absent)

	np, nerr := snapshotLoad(nb)
	if nerr != nil {
		t.Fatalf("null: %v", nerr)
	}
	ap, aerr := snapshotLoad(ab)
	if aerr != nil {
		t.Fatalf("absent: %v", aerr)
	}
	if diffs := diffJSON(dumpPrefs(np), dumpPrefs(ap), ""); len(diffs) > 0 {
		t.Errorf("explicit null differs from absent: %v", diffs)
	}
}
