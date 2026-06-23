package ui

import "testing"

// TestEmojiPickerFenceReleases pins the frozen-UI fix: the modal fence is ON while the
// picker is open and RELEASED the frame it closes (modalOn persists across frames, so an
// un-released fence freezes the whole UI). It also force-closes off the courtroom so the
// fence can never be stranded on another screen.
func TestEmojiPickerFenceReleases(t *testing.T) {
	a := &App{ctx: &Ctx{}, screen: ScreenCourtroom}
	c := a.ctx

	a.showEmojiPicker = true
	a.emojiPickerFence(c)
	if !c.modalOn || !a.emojiFenceOn {
		t.Fatal("fence should be ON while the picker is open")
	}

	// Picker closes → the fence MUST release this frame, or the UI freezes.
	a.showEmojiPicker = false
	a.emojiPickerFence(c)
	if c.modalOn {
		t.Error("fence must RELEASE the frame after the picker closes (frozen-UI bug)")
	}
	if a.emojiFenceOn {
		t.Error("emojiFenceOn should clear after release")
	}

	// Open it, then leave the courtroom: it force-closes and the fence releases.
	a.showEmojiPicker = true
	a.emojiPickerFence(c)
	a.screen = ScreenSettings
	a.emojiPickerFence(c)
	if a.showEmojiPicker {
		t.Error("the picker should force-close off the courtroom screen")
	}
	if c.modalOn {
		t.Error("the fence must not be stranded on another screen")
	}
}
