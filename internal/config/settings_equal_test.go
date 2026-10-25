package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestEqualMatchesDeepEqual pins the generated equal() against reflect.DeepEqual over a
// fully-filled struct, flipping every exported field ONE AT A TIME, so a field equal()
func TestEqualMatchesDeepEqual(t *testing.T) {
	a := filledSettings()

	check := func(label string, b *Settings) {
		if got, want := a.equal(b), reflect.DeepEqual(a, b); got != want {
			t.Errorf("%s: equal()=%v, DeepEqual=%v", label, got, want)
		}
	}

	check("identical", a.Clone())
	check("fresh-identical", filledSettings())

	rt := reflect.TypeOf(Settings{})
	for i := 0; i < rt.NumField(); i++ {
		ft := rt.Field(i)
		if ft.PkgPath != "" { // unexported
			continue
		}
		b := a.Clone()
		invertField(reflect.ValueOf(b).Elem().Field(i))
		check("flip-"+ft.Name, b)
	}
}

// invertField flips a single value to a different value, recursing into structs/arrays so
// a nested field flip still changes exactly one leaf.
func invertField(v reflect.Value) {
	if !v.CanSet() {
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		v.SetUint(v.Uint() + 1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 1)
	case reflect.String:
		v.SetString(v.String() + "x")
	case reflect.Slice, reflect.Map, reflect.Ptr:
		v.Set(reflect.Zero(v.Type()))
	case reflect.Array:
		for j := 0; j < v.Len(); j++ {
			invertField(v.Index(j))
		}
	case reflect.Struct:
		for j := 0; j < v.NumField(); j++ {
			invertField(v.Field(j))
		}
	}
}

// TestCollectionSettersReachSave proves the B2 write-back reaches Save for collections: a
// slice setter, a map-element setter and a multi-field setter through Update are all
// persisted to disk and reload to the same values.
func TestCollectionSettersReachSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prefs.json")

	p := defaultPrefs(path)
	p.Update(func(s *Settings) { s.HiddenPanelIDs = []string{"a", "b"} })
	p.Update(func(s *Settings) {
		if s.HotkeyMap == nil {
			s.HotkeyMap = map[string]string{}
		}
		s.HotkeyMap["F1"] = "chat"
	})
	p.Update(func(s *Settings) {
		s.SmoothScaling = true
		s.MasterVol = 80
	})
	if err := p.SaveNow(); err != nil {
		t.Fatalf("SaveNow: %v", err)
	}

	q, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(q.Load().HiddenPanelIDs, []string{"a", "b"}) {
		t.Errorf("slice setter lost: HiddenPanelIDs = %v", q.Load().HiddenPanelIDs)
	}
	if q.Load().HotkeyMap["F1"] != "chat" {
		t.Errorf("map-element setter lost: HotkeyMap[F1] = %q", q.Load().HotkeyMap["F1"])
	}
	if !q.Load().SmoothScaling || q.Load().MasterVol != 80 {
		t.Errorf("multi-field setter lost: SmoothScaling=%v MasterVol=%d", q.Load().SmoothScaling, q.Load().MasterVol)
	}
}
