package config

import (
	"reflect"
	"testing"
)

// TestCloneSettingsDeepCopies fills every field, clones, then mutates every map and
// slice of the clone and asserts the original is unchanged.
func TestCloneSettingsDeepCopies(t *testing.T) {
	s := &Settings{}
	fillValue(reflect.ValueOf(s).Elem())
	c := cloneSettings(s)
	if !reflect.DeepEqual(s, c) {
		t.Error("clone != original immediately after cloning")
	}
	mutateValue(reflect.ValueOf(c).Elem())
	if reflect.DeepEqual(s, c) {
		t.Error("mutating the clone's maps/slices changed the original")
	}
	if !reflect.DeepEqual(s, cloneSettings(s)) {
		t.Error("the original's containers were mutated in place")
	}
}

// TestCloneCopiesUnexportedFields proves the clone copies fields the reflect export
// check would normally hide, using a local probe with an unexported field.
func TestCloneCopiesUnexportedFields(t *testing.T) {
	type probe struct {
		Exported string
		hidden   int
		M        map[string]int
	}
	src := probe{Exported: "x", hidden: 42, M: map[string]int{"a": 1}}
	dst := &probe{}
	cloneValue(reflect.ValueOf(&src).Elem(), reflect.ValueOf(dst).Elem())
	if dst.hidden != 42 || dst.Exported != "x" || dst.M["a"] != 1 {
		t.Errorf("unexported field not copied: %+v", dst)
	}
}

func fillValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int:
		v.SetInt(1)
	case reflect.Uint8:
		v.SetUint(1)
	case reflect.Float64:
		v.SetFloat(1)
	case reflect.String:
		v.SetString("x")
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fillValue(v.Index(i))
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillValue(v.Index(0))
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		k := reflect.New(v.Type().Key()).Elem()
		fillValue(k)
		e := reflect.New(v.Type().Elem()).Elem()
		fillValue(e)
		v.SetMapIndex(k, e)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				fillValue(v.Field(i))
			}
		}
	}
}

func mutateValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			mutateValue(v.Index(i))
		}
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				mutateValue(v.Field(i))
			}
		}
	}
}

// TestLoadUpdateSwap pins the update path: Update clones, applies, and swaps the
// snapshot, and marks dirty only on a real change.
func TestLoadUpdateSwap(t *testing.T) {
	p := defaultPrefs("")
	if p.Load() == nil {
		t.Fatal("Load() returned nil snapshot")
	}
	before := p.Load().SmoothScaling
	p.Update(func(s *Settings) { s.SmoothScaling = !before })
	if p.Load().SmoothScaling != !before {
		t.Error("Update did not swap the snapshot")
	}
	if !p.pending.Load() {
		t.Error("a real change did not mark dirty")
	}

	p.pending.Store(false)
	p.Update(func(s *Settings) {}) // no change
	if p.pending.Load() {
		t.Error("a no-op Update marked dirty")
	}
}
