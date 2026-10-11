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

// simpleUnmarshalOverDefaults is the B0 candidate load: start from the defaults and
// unmarshal the file OVER them (absent keys keep the default), with no normalize.
func simpleUnmarshalOverDefaults(data []byte) *AssetPreferences {
	p := defaultPrefs("")
	_ = json.Unmarshal(data, p)
	return p
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
		}{
			{"absent", nil},
			{"zero", reflect.Zero(f.Type).Interface()},
		}
		if nv := nonZeroValue(f.Type); nv != nil {
			cases = append(cases, struct {
				name  string
				value any
			}{"nonzero", nv})
		}
		for _, cs := range cases {
			obj := map[string]any{}
			if cs.value != nil {
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

			newv := reflect.ValueOf(simpleUnmarshalOverDefaults(raw)).Elem().Field(i).Interface()
			if !reflect.DeepEqual(oldv, newv) {
				record(key, cs.name, oldv, newv)
			}
		}
	}

	if len(diffs) > 0 {
		t.Logf("load-equivalence differences (%d) — each is a case the snapshot's normalize() must reproduce before B0 part 2 proceeds:", len(diffs))
		for _, d := range diffs {
			t.Logf("  %s", d)
		}
		return // report, not a gate: the snapshot does not exist yet, so differences are expected
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
