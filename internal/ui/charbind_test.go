package ui

import "testing"

// TestPollCharBindCancel pins that an armed wardrobe key-capture is dismissable:
// Esc cancels it, and (the accidental-arm escape) so does any mouse click — so a
// stray arm can never trap you into pressing a key. An idle capture is a no-op.
//
// Fields are ASSIGNED, not set in a keyed &App{} literal: bindingFor is a real
// field but keyed App literals hit the known phantom "unknown field" glitch.
func TestPollCharBindCancel(t *testing.T) {
	// Esc cancels.
	a := &App{ctx: &Ctx{escPressed: true}}
	a.bindingFor = "Apollo"
	a.pollCharBind()
	if a.bindingFor != "" {
		t.Error("Esc must cancel the armed bind")
	}

	// A click cancels (the "oops, didn't mean to" gesture).
	a = &App{ctx: &Ctx{clicked: true}}
	a.bindingFor = "Apollo"
	a.pollCharBind()
	if a.bindingFor != "" {
		t.Error("a click must cancel the armed bind")
	}

	// No key, no click, no Esc: the capture stays armed (waiting for a key).
	a = &App{ctx: &Ctx{}}
	a.bindingFor = "Apollo"
	a.pollCharBind()
	if a.bindingFor != "Apollo" {
		t.Error("an armed capture with no input must stay armed")
	}

	// Idle (nothing armed): a no-op even with a click queued.
	a = &App{ctx: &Ctx{clicked: true}}
	a.pollCharBind() // must not panic / touch state
	if a.bindingFor != "" {
		t.Error("idle pollCharBind must stay idle")
	}
}
