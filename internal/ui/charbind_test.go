package ui

import "testing"

// TestPollCharBindCancel pins that an armed wardrobe key-capture is dismissable:
// Fields are ASSIGNED, not set in a keyed &App{} literal: bindingFor is a real
// field but keyed App literals hit the known phantom "unknown field" glitch.
func TestPollCharBindCancel(t *testing.T) {
	// Esc cancels.
	a := &App{env: env{ctx: &Ctx{escPressed: true}}}
	a.courtkeys.bindingFor = "Apollo"
	a.pollCharBind()
	if a.courtkeys.bindingFor != "" {
		t.Error("Esc must cancel the armed bind")
	}

	// A click cancels (the "oops, didn't mean to" gesture).
	a = &App{env: env{ctx: &Ctx{clicked: true}}}
	a.courtkeys.bindingFor = "Apollo"
	a.pollCharBind()
	if a.courtkeys.bindingFor != "" {
		t.Error("a click must cancel the armed bind")
	}

	// No key, no click, no Esc: the capture stays armed (waiting for a key).
	a = &App{env: env{ctx: &Ctx{}}}
	a.courtkeys.bindingFor = "Apollo"
	a.pollCharBind()
	if a.courtkeys.bindingFor != "Apollo" {
		t.Error("an armed capture with no input must stay armed")
	}

	// Idle (nothing armed): a no-op even with a click queued.
	a = &App{env: env{ctx: &Ctx{clicked: true}}}
	a.pollCharBind() // must not panic / touch state
	if a.courtkeys.bindingFor != "" {
		t.Error("idle pollCharBind must stay idle")
	}
}
