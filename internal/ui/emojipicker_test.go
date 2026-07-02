package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

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

// TestEmojiPickerKeepsPanels pins the playtest fix "the emoji button makes all
// the areas disappear": the picker still FENCES the pointer (courtModalOpen)
// but no longer counts as a blocking popup, so the Extras surface — the box and
// every torn-off tab panel (Areas!) — keeps drawing while it's open. Truly
// blocking popups still hide them.
func TestEmojiPickerKeepsPanels(t *testing.T) {
	a := &App{ctx: &Ctx{}, screen: ScreenCourtroom}
	a.room = &courtroom.Courtroom{}
	a.sess = &courtroom.Session{}

	if !a.extrasSurfaceLive() {
		t.Fatal("a live court with nothing open must show the Extras surface")
	}
	a.showEmojiPicker = true
	if !a.courtModalOpen() {
		t.Error("the open picker must still fence the pointer (courtModalOpen)")
	}
	if !a.extrasSurfaceLive() {
		t.Error("the open picker must NOT hide torn-off panels / the Extras box")
	}
	a.showEmojiPicker = false
	a.showIni = true // a genuinely blocking popup still hides them
	if a.extrasSurfaceLive() {
		t.Error("a blocking popup must hide the Extras surface")
	}
}
