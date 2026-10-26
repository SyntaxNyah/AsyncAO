package ui

import "testing"

// TestCloseTopOverlay pins Esc's "back out one layer" behaviour: it closes the
// single topmost overlay (dropdown → modal → floating panel), reports whether it
// closed anything, and leaves the rest for the next press.
func TestCloseTopOverlay(t *testing.T) {
	a := testTabApp(t)

	if a.closeTopOverlay() {
		t.Fatal("nothing open: closeTopOverlay should return false")
	}

	// A floating panel closes and reports true.
	a.showVoice = true
	if !a.closeTopOverlay() || a.showVoice {
		t.Errorf("showVoice should close (closed=%v)", !a.showVoice)
	}

	// Priority: a confirm modal closes before a floating panel.
	a.showMessages = true
	a.banBoxKind = 1
	if !a.closeTopOverlay() {
		t.Fatal("should close the modal")
	}
	if a.banBoxKind != 0 {
		t.Error("ban confirm should close before the panel")
	}
	if !a.showMessages {
		t.Error("the panel must survive until the modal is gone")
	}
	if !a.closeTopOverlay() || a.showMessages {
		t.Error("the next press should close the panel")
	}

	// A dropdown wins over everything.
	a.showVoice = true
	a.ctx.ddOpen = "swatch"
	a.closeTopOverlay()
	if a.ctx.ddOpen != "" {
		t.Error("an open dropdown should close first")
	}
	if !a.showVoice {
		t.Error("the panel must survive while a dropdown was open")
	}
}

// TestCapturingKey pins that the Esc handler stands down while a key-bind capture
// is armed (those use Esc to cancel).
func TestCapturingKey(t *testing.T) {
	a := testTabApp(t)
	a.macroBind = -1 // mirror the real App default (NewApp sets -1 = no macro armed)
	if a.capturingKey() {
		t.Fatal("fresh app should not be capturing a key (macroBind must default to -1)")
	}
	a.bindingFor = "modcall"
	if !a.capturingKey() {
		t.Error("an armed hotkey capture should report capturingKey")
	}
	a.bindingFor = ""
	a.macroBind = 0 // 0 = armed (slot 0)
	if !a.capturingKey() {
		t.Error("an armed macro capture should report capturingKey")
	}
}
