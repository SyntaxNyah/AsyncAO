package config

import (
	"reflect"
	"sync"
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

// TestSnapshotMirror pins the republish: a setter (markDirty) republishes the snapshot
// from the inline fields, so Load() reflects the write; a no-op setter does not.
func TestSnapshotMirror(t *testing.T) {
	p := defaultPrefs("")
	if p.Load() == nil {
		t.Fatal("Load() returned nil snapshot")
	}
	before := p.Load().SmoothScaling
	p.SetSmoothScaling(!before)
	if p.Load().SmoothScaling != !before {
		t.Error("snapshot did not reflect the setter (republish)")
	}
	p.SetSmoothScaling(!before) // same value: early-return, no republish
	if p.Load().SmoothScaling != !before {
		t.Error("snapshot changed unexpectedly after a no-op setter")
	}
}

// TestSnapshotRaceHammer runs one writer republishing the snapshot through a real setter
// and eight readers reading every field, under -race, to prove the atomic swap plus
// immutable snapshot carries no data race.
func TestSnapshotRaceHammer(t *testing.T) {
	p := defaultPrefs("")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				readAll(reflect.ValueOf(p.Load()).Elem())
			}
		}()
	}
	for i := 0; i < 200; i++ {
		p.SetSmoothScaling(i%2 == 0)
	}
	close(stop)
	wg.Wait()
}

func readAll(v reflect.Value) {
	switch v.Kind() {
	case reflect.Bool:
		_ = v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		_ = v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		_ = v.Uint()
	case reflect.Float32, reflect.Float64:
		_ = v.Float()
	case reflect.String:
		_ = v.String()
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			readAll(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			readAll(v.MapIndex(k))
		}
	case reflect.Ptr:
		if !v.IsNil() {
			readAll(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			readAll(v.Field(i))
		}
	}
}
