package ui

import (
	"reflect"
	"testing"
)

// TestComponentEnvWiring pins that every component embedded in App has its env
// pointer wired to App's own env (non-nil, identical pointer), so a component
// always reaches the shared deps/ctx/notice/navigation through &a.env. It is
// reflection-driven so a new embedded component is covered the moment it gains
// an env field.
func TestComponentEnvWiring(t *testing.T) {
	a := &App{env: env{ctx: &Ctx{}}}
	a.wireComponents()

	want := reflect.ValueOf(&a.env).Pointer()
	av := reflect.ValueOf(a).Elem()
	at := av.Type()
	found := 0
	for i := 0; i < at.NumField(); i++ {
		f := at.Field(i)
		if !f.Anonymous {
			continue
		}
		ef := av.Field(i).FieldByName("env")
		if !ef.IsValid() || ef.Type() != reflect.TypeOf((*env)(nil)) {
			continue // the env embed itself has no env field
		}
		found++
		if ef.IsNil() {
			t.Errorf("embedded component %s has a nil env", f.Name)
			continue
		}
		if got := ef.Pointer(); got != want {
			t.Errorf("embedded component %s: env = %#x, want &a.env = %#x", f.Name, got, want)
		}
	}
	if found == 0 {
		t.Fatal("no embedded component with an env field found — the wiring test is vacuous")
	}
}
