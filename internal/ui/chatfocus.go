package ui

// The IC input's FOCUS POLICY — the "Discord model" (user order, 2026-08-09).
// Render thread. No allocation on the settled path: the claim only runs on a
// frame that actually produced text with nothing focused.

// claimTypingForIC is half (a). Called PRE-SCREEN from App.Frame, so the focus is
// already set when the IC field draws later in the same frame and therefore
// consumes this frame's c.typed — that is what makes the FIRST keystroke land.
func (a *App) claimTypingForIC() {
	c := a.ctx
	if c.focusID != "" || c.typed == "" || a.screen != ScreenCourtroom {
		return
	}
	if c.ctrlHeld || c.altHeld || c.modalOn {
		return
	}
	if a.capturingKey() || a.classic.classicEdit || a.layoutEdit {
		return
	}
	if !c.fieldDrewLastFrame(icFieldID) {
		return
	}
	c.focusID = icFieldID
	c.caretOn, c.caretAcc = true, 0 // land visible, exactly as the Tab cycle does
}

// bounceFocusToIC is half (b): queue focus back onto the IC input. Queued rather
// than assigned (FocusField → applied at the next BeginFrame) so it survives the
// same frame's click-away unfocus no matter where the calling widget sits in draw
// order — the same reason every other FocusField caller queues.
// It is a no-op outside the courtroom and while a key-bind capture is armed, so a
// shared widget (the SFX picker also appears in the theme editor's preview) can
// call it unconditionally.
func (a *App) bounceFocusToIC() {
	if a.screen != ScreenCourtroom || a.capturingKey() {
		return
	}
	a.ctx.FocusField(icFieldID)
}

// bounceOnToggle is (b) for a CHECKBOX: pass the value the box just returned and
// the value the field still holds, assign the result back. A toggle that moved is
// a click, and AO2 wires those clicks straight to focus_ic_input
// (courtroom.cpp:502-506) — so:
// It exists because the alternative at seven call sites is seven hand-written
// three-line change tests, and the IC bar has TWO layouts (classic + themed) whose
// controls must not drift. Comparing rather than trusting the click is what keeps
// Immediate is not a caller, on purpose: see the canon table at the top.
func (a *App) bounceOnToggle(next, cur bool) bool {
	if next != cur {
		a.bounceFocusToIC()
	}
	return next
}
