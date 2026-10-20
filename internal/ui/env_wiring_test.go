package ui

import (
	"reflect"
	"testing"
)

// TestComponentEnvWiring pins that every component App carries has its env
// pointer wired to App's own env (non-nil, identical pointer), so a component
// always reaches the shared deps/ctx/notice/navigation through &a.env. It is
// reflection-driven over App's fields, so a new component is covered the moment
// it gains an env field — whether it is embedded or (as the end state requires)
// a named field like App.updates.
func TestComponentEnvWiring(t *testing.T) {
	a := &App{env: env{ctx: &Ctx{}}}
	a.wireComponents()
	if got, ok := a.env.h.(*App); !ok || got != a {
		t.Fatalf("env.h = %T, want the App itself (redraw + chrome capabilities)", a.env.h)
	}

	want := reflect.ValueOf(&a.env).Pointer()
	av := reflect.ValueOf(a).Elem()
	at := av.Type()
	found := 0
	for i := 0; i < at.NumField(); i++ {
		f := at.Field(i)
		fv := av.Field(i)
		for fv.Kind() == reflect.Ptr {
			if fv.IsNil() {
				fv = reflect.Value{}
				break
			}
			fv = fv.Elem()
		}
		if !fv.IsValid() || fv.Kind() != reflect.Struct {
			continue
		}
		ef := fv.FieldByName("env")
		if !ef.IsValid() || ef.Type() != reflect.TypeOf((*env)(nil)) {
			continue // the env field itself has no env field
		}
		found++
		if ef.IsNil() {
			t.Errorf("component field %s has a nil env", f.Name)
			continue
		}
		if got := ef.Pointer(); got != want {
			t.Errorf("component field %s: env = %#x, want &a.env = %#x", f.Name, got, want)
		}
	}
	if found == 0 {
		t.Fatal("no component with an env field found — the wiring test is vacuous")
	}
}
