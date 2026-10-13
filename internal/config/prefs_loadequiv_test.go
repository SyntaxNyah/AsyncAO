package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// jsonKey returns the json tag key for a struct field ("" for untagged or "-").
func jsonKey(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	return strings.Split(tag, ",")[0]
}

// nonZeroValue returns a value for t that differs from its zero value, so an
// explicit-non-zero file is observably different from absent/zero.
func nonZeroValue(t reflect.Type) any {
	switch t.Kind() {
	case reflect.Bool:
		return true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int64(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return uint64(7)
	case reflect.Float32, reflect.Float64:
		return float64(7)
	case reflect.String:
		return "nv"
	default:
		return nil
	}
}

// TestLoadEquivalence drives the B0 gap question directly: for every field with a
// non-zero default, does a file with the key ABSENT, explicitly ZERO, and explicitly
// NON-ZERO load identically through the old load() and through a plain
// unmarshal-over-defaults? Every difference is a case the snapshot's normalize() must
// reproduce; the test prints them rather than failing, so the report is a list, not a
// binary gate.
func TestLoadEquivalence(t *testing.T) {
	pt := reflect.TypeOf(AssetPreferences{})
	def := defaultPrefs("")

	var diffs []string
	record := func(key, cs string, oldv, newv any) {
		diffs = append(diffs, key+"|"+cs+"|old="+stableV(reflect.ValueOf(oldv))+"|new="+stableV(reflect.ValueOf(newv)))
	}

	dir := t.TempDir()
	for i := 0; i < pt.NumField(); i++ {
		f := pt.Field(i)
		key := jsonKey(f)
		if key == "" {
			continue
		}
		dv := reflect.ValueOf(def).Elem().Field(i)
		if !dv.CanInterface() || reflect.DeepEqual(dv.Interface(), reflect.Zero(f.Type).Interface()) {
			continue // zero default: absent and explicit-zero already agree
		}
		cases := []struct {
			name  string
			value any
			write bool // false = the key is absent from the file
		}{
			{"absent", nil, false},
			{"null", nil, true}, // JSON null — must read as absent, like the old pointer-field load()
			{"zero", reflect.Zero(f.Type).Interface(), true},
		}
		if nv := nonZeroValue(f.Type); nv != nil {
			cases = append(cases, struct {
				name  string
				value any
				write bool
			}{"nonzero", nv, true})
		}
		// below-min / above-max exercise the clamps, not just absent/zero/non-zero.
		switch f.Type.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			cases = append(cases,
				struct {
					name  string
					value any
					write bool
				}{"below-min", int64(-1000), true},
				struct {
					name  string
					value any
					write bool
				}{"above-max", int64(1000000), true},
			)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			cases = append(cases, struct {
				name  string
				value any
				write bool
			}{"above-max", uint64(1000000), true})
		case reflect.Float32, reflect.Float64:
			cases = append(cases,
				struct {
					name  string
					value any
					write bool
				}{"below-min", float64(-1000), true},
				struct {
					name  string
					value any
					write bool
				}{"above-max", float64(1000000), true},
			)
		}
		for _, cs := range cases {
			obj := map[string]any{}
			if cs.write {
				obj[key] = cs.value
			}
			raw, _ := json.Marshal(obj)

			path := filepath.Join(dir, "p.json")
			_ = os.WriteFile(path, raw, 0o644)
			oldp, err := load(path)
			if err != nil {
				t.Fatalf("load(%s): %v", key, err)
			}
			oldv := reflect.ValueOf(oldp).Elem().Field(i).Interface()

			newp, err := snapshotLoad(raw)
			if err != nil {
				t.Fatalf("snapshotLoad(%s): %v", key, err)
			}
			newv := reflect.ValueOf(newp).Elem().Field(i).Interface()
			if !reflect.DeepEqual(oldv, newv) {
				record(key, cs.name, oldv, newv)
			}
		}
	}

	if len(diffs) > 0 {
		// The allowlist is the explicit set of KNOWN load-equivalence differences the
		// snapshot does not yet reproduce (Order 1A-a); each entry is removed by the
		// commit that closes it (Order 1A-b). A difference NOT on the allowlist is a
		// regression and fails. Key is "jsonKey|case".
		var unlisted []string
		for _, d := range diffs {
			if _, ok := equivalenceAllowlist[d[:strings.Index(d, "old=")]]; ok {
				continue
			}
			unlisted = append(unlisted, d)
		}
		for _, d := range unlisted {
			t.Errorf("snapshot load differs from load(): %s", d)
		}
		if len(unlisted) > 0 {
			t.Fatalf("%d load-equivalence differences remain — the snapshot's normalize() must close every one (allowlist must be empty)", len(unlisted))
		}
	}
}

// stableV is a tiny stable renderer for the diff report (maps already handled by
// stableString in the setter golden; keep this for the scalar summary).
func stableV(v reflect.Value) string {
	if v.IsValid() && v.Kind() == reflect.Map {
		return "map(len=" + itoa(v.Len()) + ")"
	}
	return stableString(v)
}

// dumpPrefs renders an AssetPreferences as a comparable map: the exported fields via
// JSON, plus the unexported DATA fields (themeFitMovedToNative / themeFitStampPending)
// that json.Marshal skips. The mutex, the dirty/stop/done channels and any timer are
// runtime state, not persisted data, so they are deliberately excluded. This is the
// comparator the swap-equivalence test uses to prove new == old INCLUDING the one-shot
// stamp side-effects.
func dumpPrefs(p *AssetPreferences) map[string]any {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["__themeFitMovedToNative"] = p.themeFitMovedToNative
	m["__themeFitStampPending"] = p.themeFitStampPending
	return m
}

// themeFitFlagCases are the unexported one-shot side-effect cases: a file still on the
// legacy Stretch default arms the move-to-native (movedToNative) and always stamps
// (stampPending); a deliberate non-Stretch pick keeps the value but still stamps; an
// already-migrated file does neither. The JSON golden cannot see these, so the swap
// equivalence test reads them via dumpPrefs.
func themeFitFlagCases() []struct {
	name  string
	input map[string]any
} {
	return []struct {
		name  string
		input map[string]any
	}{
		{"themeFit_stretch_moves", map[string]any{"themeFit": 0}},
		{"themeFit_letterbox_keeps", map[string]any{"themeFit": 1}},
		{"themeFit_migrated_honours", map[string]any{"themeFit": 2, "themeFitDefaultMigrated": true}},
	}
}

// TestThemeFitFlagsCharacterized pins the OLD load()'s unexported one-shot side-effects
// via dumpPrefs, giving the swap's normalize() a target for the
// themeFitMovedToNative/themeFitStampPending flags the JSON golden cannot see.
func TestThemeFitFlagsCharacterized(t *testing.T) {
	want := map[string][2]bool{
		"themeFit_stretch_moves":    {true, true},   // Stretch arms the move, always stamps
		"themeFit_letterbox_keeps":  {false, true},  // non-Stretch keeps value, still stamps
		"themeFit_migrated_honours": {false, false}, // already migrated: neither
	}
	for _, c := range themeFitFlagCases() {
		raw, err := json.Marshal(c.input)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "p.json")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := load(path)
		if err != nil {
			t.Fatalf("%s: load: %v", c.name, err)
		}
		d := dumpPrefs(p)
		moved := d["__themeFitMovedToNative"].(bool)
		stamp := d["__themeFitStampPending"].(bool)
		w := want[c.name]
		if moved != w[0] || stamp != w[1] {
			t.Errorf("%s: moved=%v stamp=%v, want moved=%v stamp=%v", c.name, moved, stamp, w[0], w[1])
		}
	}
}

// equivalenceAllowlist is the explicit set of KNOWN load-equivalence differences the
// snapshot does not yet reproduce (Order 1A-a). Key is "case.field"; value is
// "category: reason". Each per-migration commit (1A-b) deletes its entries; the final
// commit (1A-c) asserts empty.
var equivalenceAllowlist = map[string]string{
}
