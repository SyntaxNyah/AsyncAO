package ui

// Gates for the IC bar's one-shot arms (icarms.go). The bug these exist for is
// "the Realization button does nothing": the flag went out on the wire while the
// VISIBLE half — the effects row AO2 selects at the same moment
// (courtroom.cpp:6163-6166) — was never touched, so on any modern server the
// button looked inert.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// armsTestApp is an App with an effects roster that CONTAINS a realization row —
// the case where canon's find-and-set has something to find.
func armsTestApp(t *testing.T, names ...string) *App {
	t.Helper()
	// macroBind:-1 mirrors NewApp: the zero value reads as "a macro bind capture is
	// armed" (capturingKey), which would make every focus bounce a no-op.
	a := &App{ctx: &Ctx{}, macroBind: -1}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey(""): {set: theme.MergeOverlayFX(names)},
	}
	return a
}

// TestRealizationArmsTheEffectsRowNotJustTheFlag is the whole point of icarms.go.
// courtroom.cpp:6162-6166 sets realization_state AND points the effects dropdown at
// the "realization" row; :6172-6175 undoes both. A toggle that moved only the flag
// is the reported "does nothing".
func TestRealizationArmsTheEffectsRowNotJustTheFlag(t *testing.T) {
	a := armsTestApp(t, "flash", "realization", "shake")
	want := 0
	for i, name := range a.overlayChoices() {
		if name == overlayRealizationEffect {
			want = i
		}
	}
	if want == 0 {
		t.Fatal("the fixture roster has no realization row — this gate would pass vacuously")
	}

	a.toggleRealization()
	if !a.icRealize {
		t.Error("arming must set the wire flag (realization_state = 1, courtroom.cpp:6162)")
	}
	if a.overlayChoiceIdx != want {
		t.Errorf("effects pick = %d, want the realization row %d — the VISIBLE half of the arm "+
			"(effects_dropdown_find_and_set, courtroom.cpp:6163-6166) did not happen", a.overlayChoiceIdx, want)
	}

	// Disarm is canon's exact inverse: setCurrentIndex(0) unconditionally (:6173).
	a.toggleRealization()
	if a.icRealize {
		t.Error("disarming must clear the wire flag")
	}
	if a.overlayChoiceIdx != 0 {
		t.Errorf("effects pick = %d after disarm, want 0 (courtroom.cpp:6173 sets index 0 unconditionally)", a.overlayChoiceIdx)
	}
}

// TestRealizationStillArmsWithoutARealizationEffect pins the bool AO2 checks
// before it sets the row (courtroom.cpp:5659-5671): a character or theme whose
// effects pack has no realization entry must still be able to arm the wire flag,
// and must not have an unrelated row selected for it.
func TestRealizationStillArmsWithoutARealizationEffect(t *testing.T) {
	a := armsTestApp(t, "flash", "shake")
	a.toggleRealization()
	if !a.icRealize {
		t.Error("the wire flag must arm even when the pack declares no realization effect")
	}
	if a.overlayChoiceIdx != 0 {
		t.Errorf("effects pick = %d, want 0 — find-and-set must leave the pick alone when the row is absent", a.overlayChoiceIdx)
	}
}

// TestSelectOverlayEffectByNameIsCaseInsensitive pins the deliberate deviation
// from canon's exact compare, and why it is the right one: the picker's own
// realization special-case already uses EqualFold because effects.ini
// capitalisation varies in the wild, and one half of the pair being stricter would
// mean the button armed a row the sound lookup then failed to recognise.
func TestSelectOverlayEffectByNameIsCaseInsensitive(t *testing.T) {
	a := armsTestApp(t, "Realization")
	a.selectOverlayEffectByName(overlayRealizationEffect)
	if a.overlayChoiceIdx == 0 {
		t.Fatalf("a differently-cased %q row was not found — the arm and the sound lookup would disagree", overlayRealizationEffect)
	}
}

// TestScreenshakeIsTheFlagAlone pins the sibling's DIFFERENCE. AO2's
// on_screenshake_clicked (courtroom.cpp:6181-6196) does not touch the effects
// dropdown — the shake is a viewport motion the receiver derives from the wire
// flag — so copying realization's body onto it would silently clear a picked
// effect every time someone armed a shake.
func TestScreenshakeIsTheFlagAlone(t *testing.T) {
	a := armsTestApp(t, "flash", "realization")
	a.overlayChoiceIdx = 1 // the user picked "flash"
	a.toggleScreenshake()
	if !a.icShake {
		t.Error("arming must set the screenshake flag")
	}
	if a.overlayChoiceIdx != 1 {
		t.Errorf("effects pick = %d, want the user's own 1 — screenshake must not touch the dropdown", a.overlayChoiceIdx)
	}
}

// TestBothArmsBounceFocusToTheICInput pins courtroom.cpp:6178 / :6195: every arm
// ends in focus_ic_input, so the keyboard is never stranded on the button
// mid-sentence. Queued (FocusField), which is what survives the same frame's
// click-away unfocus.
func TestBothArmsBounceFocusToTheICInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		arm  func(*App)
	}{
		{"realization", (*App).toggleRealization},
		{"screenshake", (*App).toggleScreenshake},
	} {
		a := armsTestApp(t, "realization")
		a.screen = ScreenCourtroom
		tc.arm(a)
		if a.ctx.focusNext != icFieldID {
			t.Errorf("%s: focus queued onto %q, want %q (focus_ic_input, courtroom.cpp:6178/:6195)",
				tc.name, a.ctx.focusNext, icFieldID)
		}
	}
}

// TestArmedOverlayIsWiredIntoBothButtons is the encapsulation gate for the arm's
// VISUAL. AO2 swaps in a "*_pressed" sprite (courtroom.cpp:6168/:6186); AsyncAO
// cannot (those stems are not in the theme image set it loads), so drawArmedOverlay
// is the substitute — and a button that stopped calling it would look exactly as
// inert as the bug this file fixed, with every gate above still green.
func TestArmedOverlayIsWiredIntoBothButtons(t *testing.T) {
	body := funcBodySource(t, "theme_layout.go", "drawCourtroomThemed")
	for _, call := range []string{"toggleRealization", "toggleScreenshake", "drawArmedOverlay"} {
		if !containsCall(body, call) {
			t.Errorf("the themed IC bar no longer calls %s — the arm is unreachable or invisible", call)
		}
	}
}
