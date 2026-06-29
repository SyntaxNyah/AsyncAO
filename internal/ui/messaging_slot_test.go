package ui

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestMessagesSlotSeedsPanel pins that the Group Chat panel adopts its persisted
// layout-slot geometry on first open: a stored override makes msgPanelRect place
// the live floatWin exactly there (the "I arranged it in Edit Layout" path).
func TestMessagesSlotSeedsPanel(t *testing.T) {
	a := testTabApp(t)
	const w, h = int32(1000), int32(800)
	a.classicOv = map[string][4]float64{slotMessages: {0.1, 0.1, 0.5, 0.5}}

	got := a.msgPanelRect(w, h)
	want := sdl.Rect{X: 100, Y: 80, W: 500, H: 400}
	if got != want {
		t.Errorf("seeded rect = %+v, want %+v", got, want)
	}
	if !a.msgWin.placed {
		t.Error("seeding from the slot must mark the floatWin placed so the panel stays put")
	}
}

// TestMessagesSlotPersistsOnMove pins the write-back: a moved/resized panel records
// its rect to the layout slot (live snapshot AND durable pref) so it survives a
// relaunch. Visibility is untouched — the slot is geometry only.
func TestMessagesSlotPersistsOnMove(t *testing.T) {
	a := testTabApp(t)
	const w, h = int32(1000), int32(800)
	a.msgWin.x, a.msgWin.y, a.msgWin.w, a.msgWin.h, a.msgWin.placed = 200, 100, 480, 360, true

	a.persistMsgSlot(w, h)

	want := [4]float64{0.2, 0.125, 0.48, 0.45}
	if got := a.classicOv[slotMessages]; got != want {
		t.Errorf("live override = %v, want %v", got, want)
	}
	if got := a.d.Prefs.ClassicLayoutOverrides()[slotMessages]; got != want {
		t.Errorf("persisted override = %v, want %v", got, want)
	}
	if a.showMessages {
		t.Error("persisting geometry must not change visibility (showMessages)")
	}
}

// TestMessagesSlotEditorMetadata pins the editor integration: the panel is a
// resizable slot and carries a human label naming Group Chat.
func TestMessagesSlotEditorMetadata(t *testing.T) {
	if !slotResizable(slotMessages) {
		t.Error("the Group Chat panel must be resizable in the layout editor")
	}
	if got := classicSlotLabel(slotMessages); !strings.Contains(got, "Group Chat") {
		t.Errorf("classicSlotLabel(slotMessages) = %q, want it to name Group Chat", got)
	}
}
