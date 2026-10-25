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
func TestComponentEnvWiring(t *testing.T) {
	a := &App{env: env{ctx: &Ctx{}}}
	a.wireComponents()
	if got, ok := a.env.h.(*App); !ok || got != a {
		t.Fatalf("env.h = %T, want the App itself (redraw + chrome capabilities)", a.env.h)
	}

	want := reflect.ValueOf(&a.env).Pointer()
	found := 0
	envPtr := reflect.TypeOf((*env)(nil))

	// checkComponent pins one component's env: non-nil and identical to &a.env.
	checkComponent := func(v reflect.Value, path string) {
		for v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return
		}
		ef := v.FieldByName("env")
		if !ef.IsValid() || ef.Type() != envPtr {
			return // not a component (no env), or the env field itself
		}
		found++
		switch {
		case ef.IsNil():
			t.Errorf("component %s has a nil env", path)
		case ef.Pointer() != want:
			t.Errorf("component %s: env = %#x, want &a.env = %#x", path, ef.Pointer(), want)
		}
	}

	// walkBlob visits a state blob (App, or an embedded blob such as sessionState):
	var walkBlob func(v reflect.Value, path string)
	walkBlob = func(v reflect.Value, path string) {
		for v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return
		}
		st := v.Type()
		for i := 0; i < st.NumField(); i++ {
			f := st.Field(i)
			child := v.Field(i)
			if f.Anonymous {
				walkBlob(child, path+"."+f.Name)
				continue
			}
			checkComponent(child, path+"."+f.Name)
		}
	}
	walkBlob(reflect.ValueOf(a), "App")
	if found == 0 {
		t.Fatal("no component with an env field found — the wiring test is vacuous")
	}
}

// TestEggsOwnsItsState pins the component contract for the first sessionState
// component: its state lives on eggs (not on App or sessionState), it owns the
// compare-and-store method, and App no longer owns that method.
func TestEggsOwnsItsState(t *testing.T) {
	eggsType := reflect.TypeOf(eggs{})
	for _, f := range []string{"msg", "kind"} {
		if _, ok := eggsType.FieldByName(f); !ok {
			t.Errorf("eggs has no %s field — the component must own its state", f)
		}
	}
	for _, owner := range []reflect.Type{reflect.TypeOf(App{}), reflect.TypeOf(sessionState{})} {
		for _, f := range []string{"eggMsg", "eggKind"} {
			if _, ok := owner.FieldByName(f); ok {
				t.Errorf("%s still declares %s — it must live on eggs", owner, f)
			}
		}
	}
	// The component owns the compare-and-store: drive it directly. reflect cannot
	// see unexported methods, so behaviour is the only honest ownership pin.
	var e eggs
	if !e.refresh("hello fanatsors") {
		t.Error("eggs.refresh did not scan a new text")
	}
	if e.refresh("hello fanatsors") {
		t.Error("eggs.refresh rescanned the same text — the compare-and-store is gone")
	}
	if got := e.kindOf(); got != eggFanat {
		t.Errorf("eggs.kindOf() = %d, want eggFanat", got)
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
