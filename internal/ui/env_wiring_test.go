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

// TestOnlyEnvIsEmbeddedInApp pins the end state of the decomposition: App embeds
// its state blobs and nothing else — env (cross-cutting) and sessionState (the
// per-session blob that parks/restores as one value, see app.go). Every
// COMPONENT is a NAMED field reached as a.<name>.<field>; embedding one would
// silently re-promote its whole namespace — re-creating exactly the flat App
// this work is deleting — so it is a test failure, not a style nit.
func TestOnlyEnvIsEmbeddedInApp(t *testing.T) {
	stateBlobs := map[reflect.Type]bool{
		reflect.TypeOf(env{}):          true,
		reflect.TypeOf(sessionState{}): true,
	}
	at := reflect.TypeOf(App{})
	embeds := 0
	for i := 0; i < at.NumField(); i++ {
		f := at.Field(i)
		if !f.Anonymous {
			continue
		}
		embeds++
		if !stateBlobs[f.Type] {
			t.Errorf("App embeds %s; only the state blobs (env, sessionState) may be embedded — components are NAMED fields (a.<name>.<field>)", f.Type)
		}
	}
	if embeds != len(stateBlobs) {
		t.Fatalf("App embeds %d types, want %d (env + sessionState)", embeds, len(stateBlobs))
	}
}
