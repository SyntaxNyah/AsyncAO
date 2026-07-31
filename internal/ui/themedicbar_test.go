package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// The themed IC bar is AO2's own (#21, the issue's headline complaint).
//
// aceattorney2x declares ao2_ic_chat_message = 216,384,512,23 — a bar of its own,
// the full width of the stage — and places the colour picker, the SFX picker and
// the Pre/Immediate/Flip toggles at their own design rects on the rows below.
// AsyncAO used to pack all of them INTO the message rect, so the input the theme
// sized at 512 px rendered as a ~136 px leftover on the right.

// fixtureLayout builds a themeLayoutCache straight from the checked-in
// aceattorney2x rects at 1:1, which is what ThemeFitNative gives on a window that
// fits the canvas. No renderer needed: every assertion here is geometry.
func fixtureLayout(t *testing.T) *themeLayoutCache {
	t.Helper()
	th := loadAceAttorney2x(t)
	lay := &themeLayoutCache{r: map[string]sdl.Rect{}}
	for _, key := range themeLayoutKeys {
		if r, ok := th.ElementRect(key); ok {
			lay.r[key] = sdl.Rect{X: int32(r.X), Y: int32(r.Y), W: int32(r.W), H: int32(r.H)}
		}
	}
	lay.valid = true
	return lay
}

// TestThemedICInputKeepsItsFullDesignWidth is the headline regression. The input
// must resolve to the whole rect the theme declared — nothing may take a bite out
// of it for a control that has a rect of its own.
func TestThemedICInputKeepsItsFullDesignWidth(t *testing.T) {
	lay := fixtureLayout(t)

	in, ok := lay.rect("ao2_ic_chat_message")
	if !ok {
		t.Fatal("the fixture must declare ao2_ic_chat_message")
	}
	if want := (sdl.Rect{X: 216, Y: 384, W: 512, H: 23}); in != want {
		t.Fatalf("ao2_ic_chat_message = %+v, want %+v", in, want)
	}

	// Every control that used to be crammed into that rect has one of its own, at
	// a DIFFERENT y — i.e. on another row, which is what "the IC bar is a bar"
	// means. If any of these resolved inside the input's row, the cram is back.
	for _, key := range []string{"text_color", "sfx_dropdown", "pre", "pre_no_interrupt", "flip"} {
		r, ok := lay.rect(key)
		if !ok {
			t.Errorf("the fixture declares %q but the layout did not ingest it", key)
			continue
		}
		if r.Y == in.Y {
			t.Errorf("%q sits on the input's own row (y=%d) — that is the cram", key, r.Y)
		}
		if r.X >= in.X && r.X < in.X+in.W && r.Y < in.Y+in.H && r.Y+r.H > in.Y {
			t.Errorf("%q overlaps the input rect %+v — that is the cram", key, in)
		}
	}
}

// TestThemedToggleRectPrecedence pins rule (d): the asyncao_* author override WINS
// over the AO2 key, rather than being a fallback for when the AO2 key is absent.
// That inversion is what made a stock AO2 theme render wrong, because a stock theme
// never declares an asyncao_* key at all.
func TestThemedToggleRectPrecedence(t *testing.T) {
	const (
		ao2Rect      = 1
		altRect      = 2
		overrideRect = 3
	)
	mk := func(id int32) sdl.Rect { return sdl.Rect{X: id, Y: id, W: 50, H: 19} }

	for _, tc := range []struct {
		name    string
		declare map[string]sdl.Rect
		want    int32
		wantOK  bool
	}{
		{"AO2 key only", map[string]sdl.Rect{"immediate": mk(ao2Rect)}, ao2Rect, true},
		{"alt spelling only (what aceattorney2x ships)", map[string]sdl.Rect{"pre_no_interrupt": mk(altRect)}, altRect, true},
		{"AO2 key beats the alt", map[string]sdl.Rect{"immediate": mk(ao2Rect), "pre_no_interrupt": mk(altRect)}, ao2Rect, true},
		{"override BEATS the AO2 key", map[string]sdl.Rect{
			"asyncao_ic_immediate": mk(overrideRect), "immediate": mk(ao2Rect),
		}, overrideRect, true},
		{"override beats the alt too", map[string]sdl.Rect{
			"asyncao_ic_immediate": mk(overrideRect), "pre_no_interrupt": mk(altRect),
		}, overrideRect, true},
		// Rule (e): a theme that declares none of the three draws NOTHING there. It
		// does not fall back to AsyncAO's own arrangement.
		{"none declared draws nothing", map[string]sdl.Rect{}, 0, false},
	} {
		lay := &themeLayoutCache{valid: true, r: tc.declare}
		got, ok := themedToggleRect(lay, "immediate", "pre_no_interrupt", "asyncao_ic_immediate")
		if ok != tc.wantOK {
			t.Errorf("%s: ok=%v, want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if ok && got.X != tc.want {
			t.Errorf("%s: resolved rect id %d, want %d", tc.name, got.X, tc.want)
		}
	}
}

// TestAceAttorney2xUsesTheAltImmediateSpelling pins why the alt probe exists at
// all: this theme declares pre_no_interrupt and NOT immediate, so a resolver that
// only knew the primary spelling would silently drop the toggle.
func TestAceAttorney2xUsesTheAltImmediateSpelling(t *testing.T) {
	th := loadAceAttorney2x(t)
	if _, ok := th.ElementRect("immediate"); ok {
		t.Error("fixture changed: it now declares `immediate`, so the alt probe is no longer exercised here")
	}
	if _, ok := th.ElementRect("pre_no_interrupt"); !ok {
		t.Fatal("fixture must declare pre_no_interrupt")
	}
	lay := fixtureLayout(t)
	if _, ok := themedToggleRect(lay, "immediate", "pre_no_interrupt", "asyncao_ic_immediate"); !ok {
		t.Error("the Immediate toggle resolved nothing — AO2 falls back to pre_no_interrupt (courtroom.cpp:1072-1084)")
	}
}

// TestAceAttorney2xDeclaresTheWholeToggleRow pins that every toggle this commit
// binds has a rect in the real theme, on its own row — the "completely missing
// toggles here" complaint. If any of these stopped resolving, the toggle would
// silently vanish under rule (e) with no test noticing.
func TestAceAttorney2xDeclaresTheWholeToggleRow(t *testing.T) {
	lay := fixtureLayout(t)
	for _, tc := range themedToggles {
		if _, ok := themedToggleRect(lay, tc.key, tc.alt, tc.override); !ok {
			t.Errorf("%q resolves no rect — the toggle would not draw at all", tc.key)
		}
	}
}

// TestOneShotArmsClearOnSend is the regression that matters most about
// realization and screenshake. AO2 clears realization_state and screenshake_state
// the instant the packet is built, beside objection_state
// (AO2-Client courtroom.cpp:2327-2329). Miss that and the arm is STICKY: every
// following line flashes and shakes, which is both obnoxious and hard to trace
// back to its cause.
func TestOneShotArmsClearOnSend(t *testing.T) {
	src := readSourceFile(t, "screens.go")
	// Matched loosely on purpose: gofmt realigns struct literals and multi-assign
	// statements, so pinning exact spacing makes the test fail on formatting rather
	// than on behaviour. (It did, once.)
	if !strings.Contains(src, "a.icSlide, a.icRealize, a.icShake = false, false, false") {
		t.Error("the one-shot arms are not all cleared after a send — a stuck arm rides every following message")
	}
	// All three must reach the wire too, or they are decorative toggles.
	for _, field := range []string{"Slide:", "Realization:", "Screenshake:"} {
		if !strings.Contains(src, field) {
			t.Errorf("the OutgoingMS literal does not set %s", field)
		}
	}
	for _, backing := range []string{"a.icSlide", "a.icRealize", "a.icShake"} {
		if !strings.Contains(src, backing) {
			t.Errorf("%s never reaches the send path", backing)
		}
	}
}

// TestArmsAreClearedOptimistically pins the deliberate asymmetry with the input
// field. The IC text is cleared only on the server's own echo, because a
// tsuserver-family server silently swallows a message that lands inside another's
// delay window and an optimistic clear would lose the typed line. The one-shot
// arms take the opposite trade on purpose: losing one flash to a swallowed send is
// invisible, a stuck arm is not.
func TestArmsAreClearedOptimistically(t *testing.T) {
	src := readSourceFile(t, "screens.go")
	arm := strings.Index(src, "a.icSlide, a.icRealize, a.icShake = false, false, false")
	if arm < 0 {
		t.Fatal("the one-shot clear moved — re-check it is still on the send path")
	}
	echo := strings.Index(src, "func (s *sessionState) noteOwnICEcho")
	if echo < 0 {
		t.Fatal("noteOwnICEcho moved")
	}
	if arm > echo {
		t.Error("the arms are cleared in the ECHO handler — a swallowed send would leave them armed forever")
	}
}

// TestGuardSilencesOnlyTheAlert pins the half of ui_guard that is easy to get
// wrong. AO2 gates the SOUND and the window flash on the checkbox
// (courtroom.cpp:4997-5001); the OOC line and the auto-clip are not gated, so a
// guarded moderator loses the interruption but never the report itself.
func TestGuardSilencesOnlyTheAlert(t *testing.T) {
	arm, ok := sourceBlockAfter(readSourceFile(t, "app.go"), "case courtroom.EventModcall:", "case courtroom.")
	if !ok {
		t.Fatal("the modcall handler moved")
	}
	// The guard's body is the run of lines indented one level deeper than the `if`,
	// which is what distinguishes "inside the guard" from "after it" — an index
	// comparison cannot tell those apart.
	var guarded []string
	inGuard := false
	for _, line := range strings.Split(arm, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "if !a.modcallGuard {":
			inGuard = true
		case inGuard && trimmed == "}":
			inGuard = false
		case inGuard && trimmed != "":
			guarded = append(guarded, trimmed)
		}
	}
	if len(guarded) == 0 {
		t.Fatal("the modcall alert is not gated on modcallGuard")
	}
	body := strings.Join(guarded, "\n")
	// Only the two ALERT calls may be silenced.
	for _, want := range []string{"a.playThemeSFX(", "a.ctx.FlashWindow()"} {
		if !strings.Contains(body, want) {
			t.Errorf("the guard does not silence %s", want)
		}
	}
	// The report itself must never be. AO2 gates only the sound and the flash
	// (courtroom.cpp:4997-5001).
	for _, mustSurvive := range []string{"a.pushOOC(", "a.autoClipModcall(", "a.signalModcall("} {
		if strings.Contains(body, mustSurvive) {
			t.Errorf("%s is INSIDE the guard — a guarded mod would lose the report, not just the noise", mustSurvive)
		}
		if !strings.Contains(arm, mustSurvive) {
			t.Errorf("the modcall handler no longer calls %s at all", mustSurvive)
		}
	}
}

// sourceBlockAfter returns the source between start and the next occurrence of
// end, which is enough to isolate one arm of a switch.
func sourceBlockAfter(src, start, end string) (string, bool) {
	i := strings.Index(src, start)
	if i < 0 {
		return "", false
	}
	arm := src[i+len(start):]
	if j := strings.Index(arm, end); j > 0 {
		arm = arm[:j]
	}
	return arm, true
}

// readSourceFile is the source probe the structural pins above share.
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestThemedToggleLabelFallsBackToTheConstant pins the memo's contract: an absent
// key means "it fits", so a theme with roomy rects takes the byte-identical path
// through the plain constant and allocates nothing.
func TestThemedToggleLabelFallsBackToTheConstant(t *testing.T) {
	lay := &themeLayoutCache{valid: true, r: map[string]sdl.Rect{}}
	if got := lay.themedToggleLabel("flip", "Flip"); got != "Flip" {
		t.Errorf("empty memo → %q, want the constant %q", got, "Flip")
	}
	lay.toggleLabel = map[string]string{"flip": "Fl…"}
	if got := lay.themedToggleLabel("flip", "Flip"); got != "Fl…" {
		t.Errorf("memoised label ignored: got %q", got)
	}
	if got := lay.themedToggleLabel("pre", "Pre"); got != "Pre" {
		t.Errorf("unrelated key picked up a memo: got %q", got)
	}
}

// TestThemedFlipRectIsTightEnoughToTruncate documents WHY the label memo exists.
// If this theme's flip rect ever grows past its label, the memo stops being
// exercised by the fixture and the truncation path needs another witness.
func TestThemedFlipRectIsTightEnoughToTruncate(t *testing.T) {
	lay := fixtureLayout(t)
	r, ok := lay.rect("flip")
	if !ok {
		t.Fatal("fixture must declare flip")
	}
	c := &Ctx{}
	if avail := c.CheckboxLabelAvail(r); avail > 40 {
		t.Errorf("flip leaves %d px for its label — roomy enough that the fixture no longer exercises truncation", avail)
	}
}
