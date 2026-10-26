package ui

import "testing"

// TestNoteFocusLostClearsFocus pins the window-focus-loss fix: clicking away
// drops the focused text field (caret stops blinking, typing goes elsewhere)
// and a nil-ctx App (headless) is safe.
func TestNoteFocusLostClearsFocus(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.ctx.focusID = icFieldID
	a.ctx.selAnchor = 5

	a.NoteFocusLost()

	if a.ctx.focusID != "" {
		t.Errorf("focusID = %q after focus loss, want cleared", a.ctx.focusID)
	}
	if a.ctx.selAnchor != -1 {
		t.Errorf("selAnchor = %d after focus loss, want -1", a.ctx.selAnchor)
	}

	// A headless/partial App must not panic.
	var bare App
	bare.NoteFocusLost()
}
