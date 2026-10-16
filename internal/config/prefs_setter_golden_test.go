package config

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// setterGoldenPath is the committed characterization of every AssetPreferences
// setter: for each setter and a small set of edge inputs, the fields it changed
// (path=value), recorded by reflection against the CURRENT code. B0 part 2 must
// reproduce this byte-for-byte through the Settings snapshot, so it is the proof
// that "reimplemented over update/clone/normalize" is behaviour-identical to the
// load() the client ships today.
//
// Regenerate: GOLDEN_REGEN=1 go test ./internal/config -run TestSetterGoldenRegenerate
const setterGoldenPath = "testdata/prefs_setters_golden.txt"

// namedArgs is one edge-input scenario for one setter.
type namedArgs struct {
	label string
	args  []reflect.Value
}

func zeroOf(t reflect.Type) reflect.Value { return reflect.Zero(t) }

// validOf returns a small non-zero value for t (one element for slices, a fresh
// pointer for pointers, one entry for maps).
func validOf(t reflect.Type) reflect.Value {
	switch t.Kind() {
	case reflect.Bool:
		return reflect.ValueOf(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if t == reflect.TypeOf(time.Duration(0)) {
			return reflect.ValueOf(time.Second).Convert(t)
		}
		return reflect.ValueOf(int64(1)).Convert(t)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflect.ValueOf(uint64(1)).Convert(t)
	case reflect.Float32, reflect.Float64:
		return reflect.ValueOf(float64(1.5)).Convert(t)
	case reflect.String:
		return reflect.ValueOf("v")
	case reflect.Slice:
		s := reflect.MakeSlice(t, 1, 1)
		s.Index(0).Set(validOf(t.Elem()))
		return s
	case reflect.Map:
		m := reflect.MakeMap(t)
		m.SetMapIndex(validOf(t.Key()), validOf(t.Elem()))
		return m
	case reflect.Ptr:
		p := reflect.New(t.Elem())
		p.Elem().Set(validOf(t.Elem()))
		return p
	default:
		return reflect.Zero(t)
	}
}

// setterScenarios builds the edge-input scenarios for one setter. Every setter gets
// "zero" and "valid"; single-parameter setters with a numeric/string/collection
// parameter also get neg/huge/empty/nil.
func setterScenarios(m reflect.Method) []namedArgs {
	kind := func(f func(reflect.Type) reflect.Value) []reflect.Value {
		args := make([]reflect.Value, m.Type.NumIn()-1)
		for i := 1; i < m.Type.NumIn(); i++ {
			args[i-1] = f(m.Type.In(i))
		}
		return args
	}
	out := []namedArgs{{"zero", kind(zeroOf)}, {"valid", kind(validOf)}}
	if m.Type.NumIn() == 2 {
		t := m.Type.In(1)
		one := func(label string, v reflect.Value) { out = append(out, namedArgs{label, []reflect.Value{v}}) }
		switch t.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if t != reflect.TypeOf(time.Duration(0)) {
				one("neg", reflect.ValueOf(int64(-1)).Convert(t))
				one("huge", reflect.ValueOf(int64(math.MaxInt32)).Convert(t))
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			one("huge", reflect.ValueOf(uint64(math.MaxInt32)).Convert(t))
		case reflect.Float32, reflect.Float64:
			one("neg", reflect.ValueOf(float64(-1)).Convert(t))
			one("huge", reflect.ValueOf(float64(math.MaxFloat32)).Convert(t))
		case reflect.String:
			one("empty", reflect.ValueOf(""))
			one("huge", reflect.ValueOf(strings.Repeat("x", 4096)))
		case reflect.Slice, reflect.Map, reflect.Ptr:
			one("nil", reflect.Zero(t))
		}
	}
	return out
}

// fieldDiff returns the sorted list of changed leaf fields between before and
// after, each "path=value". A leaf is a non-struct field, or a slice/map/ptr whose
// identity or contents changed.
func fieldDiff(before, after *AssetPreferences) []string {
	var out []string
	diffValue(reflect.ValueOf(before).Elem(), reflect.ValueOf(after).Elem(), "", &out)
	sort.Strings(out)
	return out
}

func diffValue(b, a reflect.Value, path string, out *[]string) {
	if !b.IsValid() || !a.IsValid() || !b.CanInterface() || !a.CanInterface() {
		return
	}
	switch b.Kind() {
	case reflect.Struct:
		for i := 0; i < b.NumField(); i++ {
			fb, fa := b.Field(i), a.Field(i)
			if !fb.CanInterface() {
				continue
			}
			diffValue(fb, fa, path+b.Type().Field(i).Name+".", out)
		}
	case reflect.Ptr:
		if b.IsNil() && a.IsNil() {
			return
		}
		if b.IsNil() || a.IsNil() {
			*out = append(*out, trimDot(path)+"="+stableString(a))
			return
		}
		diffValue(b.Elem(), a.Elem(), path, out)
	default:
		if !reflect.DeepEqual(b.Interface(), a.Interface()) {
			*out = append(*out, trimDot(path)+"="+stableString(a))
		}
	}
}

func trimDot(s string) string { return strings.TrimSuffix(s, ".") }

// stableString renders v deterministically: maps sort their keys and structs recurse
// (fmt.Sprint of a nested map iterates in random order, which would make the golden
// flaky).
func stableString(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return stableString(keys[i]) < stableString(keys[j]) })
		var b strings.Builder
		b.WriteString("map[")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(stableString(k))
			b.WriteString(":")
			b.WriteString(stableString(v.MapIndex(k)))
		}
		b.WriteString("]")
		return b.String()
	case reflect.Slice, reflect.Array:
		var b strings.Builder
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(stableString(v.Index(i)))
		}
		b.WriteString("]")
		return b.String()
	case reflect.Struct:
		var b strings.Builder
		b.WriteString("{")
		wrote := false
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanInterface() {
				continue
			}
			if wrote {
				b.WriteString(" ")
			}
			b.WriteString(stableString(f))
			wrote = true
		}
		b.WriteString("}")
		return b.String()
	case reflect.Ptr:
		if v.IsNil() {
			return "<nil>"
		}
		return "&" + stableString(v.Elem())
	default:
		return fmt.Sprint(v.Interface())
	}
}

// setterGolden generates the golden content from the current code.
func setterGolden() (string, error) {
	pt := reflect.TypeOf(&AssetPreferences{})
	var methods []string
	for i := 0; i < pt.NumMethod(); i++ {
		name := pt.Method(i).Name
		if strings.HasPrefix(name, "Set") {
			methods = append(methods, name)
		}
	}
	sort.Strings(methods)

	var lines []string
	for _, name := range methods {
		m, _ := pt.MethodByName(name)
		for _, sc := range setterScenarios(m) {
			base := defaultPrefs("")
			callArgs := append([]reflect.Value{reflect.ValueOf(base)}, sc.args...)
			var panicked string
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = fmt.Sprint(r)
					}
				}()
				m.Func.Call(callArgs)
			}()
			if panicked != "" {
				lines = append(lines, name+"|"+sc.label+"|panic="+panicked)
				continue
			}
			d := fieldDiff(defaultPrefs(""), base)
			lines = append(lines, name+"|"+sc.label+"|"+strings.Join(d, ";"))
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// TestSetterGolden verifies the current code still matches the committed golden. The
// golden is the FROZEN oracle over every setter; setters migrated off AssetPreferences
// (deleted) are covered by TestSetterGoldenViaUpdate via the setterFieldMap, so here we
// only compare the setters that still exist as methods.
func TestSetterGolden(t *testing.T) {
	want, err := os.ReadFile(setterGoldenPath)
	if err != nil {
		t.Fatalf("read golden (regenerate with GOLDEN_REGEN=1): %v", err)
	}
	got, err := setterGolden()
	if err != nil {
		t.Fatal(err)
	}
	if got != filterGoldenToCurrentSetters(string(want)) {
		t.Errorf("setter characterization drifted — regenerate the golden.\n--- want (filtered to current setters) ---\n%s\n--- got ---\n%s", filterGoldenToCurrentSetters(string(want)), got)
	}
}

// filterGoldenToCurrentSetters keeps only the golden lines whose setter still exists as
// a method on AssetPreferences, so the frozen golden can hold the deleted setters'
// cases (checked by TestSetterGoldenViaUpdate) without failing here.
func filterGoldenToCurrentSetters(golden string) string {
	pt := reflect.TypeOf(&AssetPreferences{})
	live := map[string]bool{}
	for i := 0; i < pt.NumMethod(); i++ {
		if name := pt.Method(i).Name; strings.HasPrefix(name, "Set") {
			live[name] = true
		}
	}
	var out []string
	for _, line := range strings.Split(golden, "\n") {
		if line == "" {
			continue
		}
		if i := strings.Index(line, "|"); i >= 0 && live[line[:i]] {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// TestSetterGoldenRegenerate rewrites the golden from the current code.
func TestSetterGoldenRegenerate(t *testing.T) {
	if os.Getenv("GOLDEN_REGEN") != "1" {
		t.Skip("set GOLDEN_REGEN=1 to regenerate")
	}
	got, err := setterGolden()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setterGoldenPath, []byte(got), 0o644); err != nil {
		t.Fatal(err)
	}
}
