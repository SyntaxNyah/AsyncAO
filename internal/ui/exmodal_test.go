package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Issue #31, "bringing up the Extras → Timer hides all the other UI": the Timer,
// the auto-login dialog and the click-to-pair popup were rows in courtroomModals,
// and a row there ends the courtroom pass outright — so opening a countdown blanked
// the scene, the logs, the music list and every button behind it. They are
// non-blocking floatWin panels now.
//
// These tests pin the three halves of that: they no longer take the pass, they no
// longer suppress the Extras surface, and they still fence their own footprint so
// a click on one cannot ALSO land on the courtroom underneath (which is the
// click-through the pass-taking used to prevent for free).

// exModalPanels is the set #31 moved, with the flag that opens each and the rect fn
// the fence and the draw share.
var exModalPanels = []struct {
	name string
	open func(*App)
	rect func(*App, int32, int32) sdl.Rect
}{
	{"timer", func(a *App) { a.showTimer = true }, (*App).timerPanelRect},
	{"login", func(a *App) { a.showLogin = true }, (*App).loginPanelRect},
	{"pair popup", func(a *App) { a.pairPopupOpen = true }, (*App).pairPopPanelRect},
}

// TestExModalsNoLongerTakeTheScreen is the issue as reported: with one of them open
// the courtroom pass must run to completion, and the Extras box must stay up.
func TestExModalsNoLongerTakeTheScreen(t *testing.T) {
	for _, p := range exModalPanels {
		t.Run(p.name, func(t *testing.T) {
			a := testTabApp(t)
			a.sess = courtroom.NewRehearsalSession("", nil)
			a.room = &courtroom.Courtroom{}
			a.showWidgets = true
			p.open(a)

			if a.courtroomModalUp() {
				t.Error("this panel must not end the courtroom pass — that is what hid all the other UI (#31)")
			}
			if a.blockingCourtPopup() {
				t.Error("a non-blocking panel must not count as a blocking popup, or the Extras box and every torn-off tab hide for it")
			}
			if !a.extrasSurfaceLive() {
				t.Error("the Extras surface must stay live behind it")
			}
			if !a.extrasBoxVisible() {
				t.Error("the Extras box must stay visible — you open the Timer FROM it")
			}
		})
	}
}

// TestExModalsFenceTheirFootprint is the other side of the trade. Ending the pass
// used to stop click-through for free; a floating panel has to fence its own rect
// instead, or a press on the Timer would also hit the music list behind it.
func TestExModalsFenceTheirFootprint(t *testing.T) {
	const w, h = int32(1280), int32(720)
	for _, p := range exModalPanels {
		t.Run(p.name, func(t *testing.T) {
			a := testTabApp(t)
			a.sess = courtroom.NewRehearsalSession("", nil)
			a.room = &courtroom.Courtroom{}
			p.open(a)
			r := p.rect(a, w, h)

			a.ctx.mouseX, a.ctx.mouseY = r.X+r.W/2, r.Y+r.H/2
			if !a.boxFencesPointer(w, h) {
				t.Error("the pointer over the panel must fence the courtroom pass — else the click lands on the scene behind it too")
			}
			a.ctx.mouseX, a.ctx.mouseY = r.X-30, r.Y-30
			if a.boxFencesPointer(w, h) {
				t.Error("off the panel the courtroom must stay live — that is the whole point of not being a modal")
			}
		})
	}
}

// TestExModalsAreClosedByEsc pins that each still answers Esc. closeTopOverlay is
// the app-level handler, and a popup that does not appear there lets Esc fall
// through to the courtroom's leave-the-server confirm.
func TestExModalsAreClosedByEsc(t *testing.T) {
	for _, p := range exModalPanels {
		t.Run(p.name, func(t *testing.T) {
			a := testTabApp(t)
			p.open(a)
			if !a.closeTopOverlay() {
				t.Fatal("Esc must resolve this panel, not fall through to the leave-the-server shortcut")
			}
			if a.showTimer || a.showLogin || a.pairPopupOpen {
				t.Error("Esc left the panel open")
			}
		})
	}
}

// TestExModalsPersistTheirPosition pins that each joined panelSlotTable, which is
// what gives a floatWin its position persistence, its de-overlap cascade and its
// magnetism. A panel missing from the table silently loses all three.
func TestExModalsPersistTheirPosition(t *testing.T) {
	const w, h = int32(1280), int32(720)
	for _, slot := range []string{slotPanelTimer, slotPanelLogin, slotPanelPairPop} {
		found := false
		for i := range panelSlotTable {
			if panelSlotTable[i].slot == slot {
				found = true
				a := testTabApp(t)
				fw := panelSlotTable[i].fw(a)
				if panelSlotTable[i].open(a) {
					t.Errorf("%s: the open predicate must read false on a fresh App", slot)
				}
				// The nominal size in the table feeds the magnetism census; a zero
				// there would snap siblings to a degenerate rect.
				if panelSlotTable[i].defW <= 0 || panelSlotTable[i].defH <= 0 {
					t.Errorf("%s: nominal default size must be non-zero", slot)
				}
				if r := fw.rect(panelSlotTable[i].defW, panelSlotTable[i].defH, panelSlotTable[i].minW, panelSlotTable[i].minH, w, h); r.W <= 0 || r.H <= 0 {
					t.Errorf("%s: rect() produced an empty panel %+v", slot, r)
				}
			}
		}
		if !found {
			t.Errorf("%s is not in panelSlotTable — it loses position persistence, de-overlap and magnetism", slot)
		}
	}
}

// TestExModalPanelsFitTheirMinimumSize is the resize floor these panels gained.
// Every one of them lays widgets out from fixed offsets, so a minimum that is
// smaller than the content lets a button escape the panel — the same class of bug
// as #30, which is being fixed in this release for the Extras box.
func TestExModalPanelsFitTheirMinimumSize(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		defW, defH, minW, minH int32
		// contentW / contentH are the furthest right/bottom edge the draw reaches,
		// measured from the panel origin.
		contentW, contentH int32
	}{
		// Timer: the preset row runs pad + 4*62 wide; vertically the readout (+44),
		// two slider rows (+86, +114), the presets (+142) and then Repeat + the
		// transport row, which sit relative to the BOTTOM edge.
		{"timer", timerPanelDefW, timerPanelDefH, timerPanelMinW, timerPanelMinH, pad + 4*62, 142 + btnH + 24 + btnH + 18},
		// Login: three buttons across the bottom, fields full width.
		{"login", loginPanelDefW, loginPanelDefH, loginPanelMinW, loginPanelMinH, pad + 130 + 10 + 90 + 10 + 90 + pad, floatTitleH + 8 + 20 + 22 + 2*(fieldH+6) + 24 + 20 + btnH + 18},
		// Pair popup: the UID row (label + field + Send) is the widest fixed run.
		{"pair popup", pairPopPanelDefW, pairPopPanelDefH, pairPopPanelMinW, pairPopPanelMinH, 18 + 200 + 120 + 18, floatTitleH + 10 + 22 + fieldH + 10 + btnH + 10 + 20 + 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.minW < tc.contentW {
				t.Errorf("minW %d is narrower than the %d px of fixed-offset content — a button escapes the panel when it is dragged to its floor", tc.minW, tc.contentW)
			}
			if tc.minH < tc.contentH {
				t.Errorf("minH %d is shorter than the %d px of content", tc.minH, tc.contentH)
			}
			if tc.defW < tc.minW || tc.defH < tc.minH {
				t.Errorf("default %dx%d is smaller than the minimum %dx%d", tc.defW, tc.defH, tc.minW, tc.minH)
			}
		})
	}
}
