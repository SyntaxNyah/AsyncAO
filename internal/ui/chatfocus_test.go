package ui

// Gates for the IC input's focus policy (chatfocus.go) and the bare-key namespaces
// it retired (bareBindKey, qol.go).

import (
	"go/ast"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// focusTestApp is a courtroom App whose IC field drew on the last painted frame —
// the state claimTypingForIC requires before it will take a keystroke.
//
// drawnFieldSeq, not fieldSeq: fieldSeq is the list the CURRENT pass is building
// and it is empty for the whole pre-screen half of App.Frame, which is where the
// claim runs. Seeding the wrong one is how this file used to pass against a claim
// that could not fire — see the frame-driven gate below.
func focusTestApp() *App {
	a := &App{ctx: &Ctx{}, macroBind: -1}
	a.screen = ScreenCourtroom
	a.ctx.drawnFieldSeq = []string{icFieldID}
	return a
}

// TestTypingClaimsTheICInputOnTheFIRSTKeystroke is half (a) of the Discord model,
// and the half that distinguishes it from plain click-to-focus: the character that
// arms the focus is the character that gets typed.
//
// FRAME-DRIVEN, and that is not tidiness. The previous version of this gate seeded
// ctx.fieldSeq by hand and called a.claimTypingForIC() directly, and it was green
// against a feature that could never fire in the shipped client: App.Frame calls
// ctx.BeginDraw() — whose whole body empties the field list — some 280 lines ABOVE
// the claim, and nothing in between draws a TextField, so the on-screen test the
// claim ends on always answered "no". That is precisely the hand-seeded mirror
// CLAUDE.md rule 11 refuses to count. The only honest question is the user's: type
// a letter into a settled courtroom with nothing focused, and see where it lands.
func TestTypingClaimsTheICInputOnTheFIRSTKeystroke(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	// TWO settled frames, because the drawn order is PUBLISHED at the next
	// BeginDraw: after frame 1 the IC input is in the list this pass built, after
	// frame 2 it is in the list a pre-screen reader sees. That one-frame lag is the
	// same one the Tab cycle has always run on.
	driveFrames(a, 2)
	if !a.ctx.fieldDrewLastFrame(icFieldID) {
		t.Fatal("the fixture's courtroom painted no IC input, so nothing below is measuring the claim")
	}
	a.ctx.focusID = "" // nothing focused, which is the whole precondition
	before := a.icInput

	driveTypedText(a, "h")

	if a.ctx.focusID != icFieldID {
		t.Fatalf("focus = %q after typing with nothing focused, want %q — the claim refused a real "+
			"frame's keystroke (it reads the list BeginDraw just emptied)", a.ctx.focusID, icFieldID)
	}
	if want := before + "h"; a.icInput != want {
		t.Fatalf("the IC input holds %q, want %q — the FIRST keystroke was swallowed: the claim has to "+
			"land before the field draws, in the same frame, or this is plain click-to-focus", a.icInput, want)
	}
	if !a.ctx.caretOn {
		t.Error("the caret must land visible, as the Tab cycle does")
	}
}

// TestBeginDrawPublishesTheDrawnFieldOrder pins the seam the claim stands on. The
// two lists are a double buffer: fieldSeq is what THIS pass is building,
// drawnFieldSeq is what is on screen, and every pre-screen reader needs the second
// one. A "tidy-up" that collapsed them back into one list would restore the dead
// claim, so this fails the moment BeginDraw stops publishing.
func TestBeginDrawPublishesTheDrawnFieldOrder(t *testing.T) {
	c := &Ctx{}
	c.BeginDraw()
	c.fieldSeq = append(c.fieldSeq, "showname", icFieldID) // two TextFields painted
	c.BeginDraw()                                          // the next pass opens

	if len(c.fieldSeq) != 0 {
		t.Fatalf("the new pass started with %d fields already registered — Tab order would double up", len(c.fieldSeq))
	}
	if !c.fieldDrewLastFrame(icFieldID) || !c.fieldDrewLastFrame("showname") {
		t.Fatal("the fields painted last pass are not reported as on screen — every pre-screen reader is blind")
	}
	if c.fieldDrewLastFrame("areasearch") {
		t.Error("a field that never drew is reported as on screen")
	}
	// A pass that draws no field publishes an empty list: "on screen" must mean the
	// last pass, not the last pass that happened to have fields.
	c.BeginDraw()
	if c.fieldDrewLastFrame(icFieldID) {
		t.Error("a field-less pass still reports the IC box on screen — a hidden IC bar would take focus it cannot show")
	}
	// The swap allocates nothing: it runs at the top of every drawn frame, inside
	// the whole-screen zero-alloc budget.
	if n := testing.AllocsPerRun(beginDrawAllocRuns, c.BeginDraw); n != 0 {
		t.Errorf("BeginDraw allocated %v per call — the publish must be a swap, never a copy", n)
	}
}

// beginDrawAllocRuns is the sample count for the swap's alloc gate. Small: the
// operation is two assignments, and AllocsPerRun already discards a warm-up run.
const beginDrawAllocRuns = 50

// TestTypingClaimRefusesWhereTheKeyBelongsToSomethingElse walks every refusal in
// claimTypingForIC. Each row is a place a keystroke has an owner that is not the
// chat box; a claim there would swallow it.
func TestTypingClaimRefusesWhereTheKeyBelongsToSomethingElse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mangle func(*App)
	}{
		{"another field is focused", func(a *App) { a.ctx.focusID = "areasearch" }},
		{"no text this frame", func(a *App) { a.ctx.typed = "" }},
		{"not in the courtroom", func(a *App) { a.screen = ScreenSettings }},
		{"a Ctrl chord", func(a *App) { a.ctx.ctrlHeld = true }},
		{"an Alt chord (a relocated bind namespace)", func(a *App) { a.ctx.altHeld = true }},
		{"a modal fence is up", func(a *App) { a.ctx.modalOn = true }},
		{"a key-bind capture is armed", func(a *App) { a.bindingFor = "Judge" }},
		{"the layout editor owns the keyboard", func(a *App) { a.layoutEdit = true }},
		{"the classic editor owns the keyboard", func(a *App) { a.classicEdit = true }},
		{"the IC field is not on screen", func(a *App) { a.ctx.drawnFieldSeq = nil }},
	} {
		a := focusTestApp()
		a.ctx.typed = "h"
		tc.mangle(a)
		before := a.ctx.focusID
		a.claimTypingForIC()
		if a.ctx.focusID != before {
			t.Errorf("%s: focus moved to %q — that keystroke was not the chat box's", tc.name, a.ctx.focusID)
		}
	}
}

// TestBounceFocusToICQueuesRatherThanAssigns pins half (b) and HOW it is done.
// AO2 calls focus_ic_input after an emote pick (emotes.cpp:225), a colour pick
// (courtroom.cpp:6402) and the additive space insert (:6572). Queuing
// (FocusField) is what survives the same frame's click-away unfocus regardless of
// where the calling widget sits in draw order.
func TestBounceFocusToICQueuesRatherThanAssigns(t *testing.T) {
	a := focusTestApp()
	a.bounceFocusToIC()
	if a.ctx.focusNext != icFieldID {
		t.Errorf("focus queued onto %q, want %q", a.ctx.focusNext, icFieldID)
	}
	if a.ctx.focusID == icFieldID {
		t.Error("the bounce must QUEUE focus, not assign it — an assignment loses to the same frame's click-away")
	}
	// Shared widgets (the SFX picker also appears in the theme editor's preview)
	// call it unconditionally, so it must be inert off the courtroom.
	b := focusTestApp()
	b.screen = ScreenSettings
	b.bounceFocusToIC()
	if b.ctx.focusNext != "" {
		t.Error("the bounce must be a no-op outside the courtroom")
	}
	// …and while a bind capture owns the next key.
	d := focusTestApp()
	d.bindingFor = "Judge"
	d.bounceFocusToIC()
	if d.ctx.focusNext != "" {
		t.Error("the bounce must be a no-op while a key-bind capture is armed")
	}
}

// TestBounceOnToggleFiresOnTheCHANGEOnly pins the checkbox half of (b). A
// checkbox is drawn every frame and returns its value every frame, so a bounce on
// the VALUE rather than on the change would re-focus the IC box on every single
// frame — stealing focus from the OOC box, the search fields and the layout editor
// for as long as the bar is on screen. Only a click bounces.
func TestBounceOnToggleFiresOnTheCHANGEOnly(t *testing.T) {
	a := focusTestApp()
	if got := a.bounceOnToggle(true, false); !got {
		t.Error("the toggle's new value must pass straight through")
	}
	if a.ctx.focusNext != icFieldID {
		t.Fatalf("a flipped toggle queued focus onto %q, want %q", a.ctx.focusNext, icFieldID)
	}
	// Unchanged: no bounce. This is also what keeps Flip's two views (the IC bar and
	// the Pair panel) from bouncing each other — the second draw carries the value
	// the first already wrote.
	b := focusTestApp()
	if got := b.bounceOnToggle(true, true); !got {
		t.Error("an unchanged toggle must still pass its value through")
	}
	if b.ctx.focusNext != "" {
		t.Errorf("an unchanged toggle queued focus onto %q — every frame would steal the keyboard", b.ctx.focusNext)
	}
}

// TestSFXPickBouncesFocusAndAuditions pins the dropdown half. on_sfx_dropdown_changed
// is `custom_sfx = ""; focus_ic_input();` (courtroom.cpp:5498-5502) — picking a sound
// is a step in composing the line, so the keyboard goes back to the line.
func TestSFXPickBouncesFocusAndAuditions(t *testing.T) {
	a := focusTestApp()
	a.applySFXPick(0) // row 0 is "auto": no audition, so no audio device is needed
	if a.sfxChoiceIdx != 0 {
		t.Errorf("the pick did not land (idx %d)", a.sfxChoiceIdx)
	}
	if a.ctx.focusNext != icFieldID {
		t.Errorf("an SFX pick queued focus onto %q, want %q (courtroom.cpp:5501)", a.ctx.focusNext, icFieldID)
	}
}

// TestEveryTranscribedCanonFocusBounceIsWired is the CENSUS for half (b), and it
// is written against AO2's own connect block rather than against our taste:
// courtroom.cpp:502-506 wires ui_pre, ui_flip, ui_slide_enable straight to
// focus_ic_input and ui_additive through on_additive_clicked (:6572), and
// pointedly does NOT wire ui_immediate.
//
// TRANSCRIBED, not "every": canon has about two dozen focus_ic_input sites and
// AsyncAO wires eleven of them. The name says so because the previous name did
// not, and a census called "Every" that checks a subset is how on_text_color_changed
// stayed unwired while the header swore the list was complete. chatfocus.go's four
// tables are the full accounting; this gate holds the two that can rot silently —
// TRANSCRIBED (must bounce) and the DEFERRED rows (must still NOT bounce, so
// wiring one forces the table to be updated in the same change).
//
// It reads the source because the two IC bars draw the same controls twice and the
// failure this closes is exactly a control that has the wiring in one bar and not
// the other — chatfocus.go claimed all of them and had none of them.
func TestEveryTranscribedCanonFocusBounceIsWired(t *testing.T) {
	for _, bar := range []struct {
		file, fn string
		bounce   []string // canon wires these to focus_ic_input, and so do we
		inert    []string // …canon deliberately does not wire these
		deferred []string // …and canon wires these but AsyncAO does not yet
	}{
		{"screens.go", "drawICInputRow", []string{"Pre", "Additive", "Flip"}, []string{"Immediate"}, nil},
		{"theme_layout.go", "drawCourtroomThemed", []string{"Pre", "Additive", "Flip", "Slide"}, []string{"Immediate"}, []string{"Guard"}},
	} {
		wrapped := icToggleBounces(t, bar.file, bar.fn)
		for _, label := range bar.bounce {
			got, drawn := wrapped[label]
			if !drawn {
				t.Errorf("%s draws no %q checkbox — the control moved and this gate stopped checking it", bar.fn, label)
				continue
			}
			if !got {
				t.Errorf("%s: the %q checkbox does not go through bounceOnToggle — the keyboard is "+
					"stranded on it mid-sentence (courtroom.cpp:502-506)", bar.fn, label)
			}
		}
		for _, label := range bar.inert {
			if got, drawn := wrapped[label]; drawn && got {
				t.Errorf("%s: the %q checkbox bounces focus, but AO2 leaves ui_immediate unconnected "+
					"(constructed at courtroom.cpp:331, absent from the :502-506 connect block) — "+
					"canon owns the membership list", bar.fn, label)
			}
		}
		// The DEFERRED rows are pinned in BOTH directions. Still drawn: the control
		// is real, so chatfocus.go's "not transcribed" line is about live code and not
		// about a widget that has since been deleted. Still un-wrapped: the day one is
		// wired, this fails and the header's table has to move with it — which is the
		// only thing standing between an honest deferral and the overstatement that
		// hid on_text_color_changed.
		for _, label := range bar.deferred {
			got, drawn := wrapped[label]
			if !drawn {
				t.Errorf("%s draws no %q checkbox, but chatfocus.go still lists it as a DEFERRED canon "+
					"bounce — delete the row instead of leaving a claim about a control that is gone", bar.fn, label)
				continue
			}
			if got {
				t.Errorf("%s: the %q checkbox now bounces focus — move it out of chatfocus.go's "+
					"NOT TRANSCRIBED table into the TRANSCRIBED one (ui_guard is courtroom.cpp:505)", bar.fn, label)
			}
		}
	}
	// The dropdowns, whose pick is a CALL rather than a value: every bar that draws
	// one must route through the single applier, and the applier is where the bounce
	// lives — that is what stops the two IC bars drifting a control apart.
	for _, h := range []struct{ file, fn, applier, cite string }{
		{"screens.go", "drawICInputRow", "applySFXPick", "on_sfx_dropdown_changed, courtroom.cpp:5498-5502"},
		{"theme_layout.go", "drawThemedSFXPicker", "applySFXPick", "on_sfx_dropdown_changed, courtroom.cpp:5498-5502"},
		{"screens.go", "drawICColorStrip", "applyICColorPick", "on_text_color_changed, courtroom.cpp:6364-6403"},
		{"theme_layout.go", "drawCourtroomThemed", "applyICColorPick", "on_text_color_changed, courtroom.cpp:6364-6403"},
	} {
		if !containsCall(funcBodySource(t, h.file, h.fn), h.applier) {
			t.Errorf("%s sets the pick itself instead of through %s — the focus bounce (and, for SFX, the "+
				"audition) drifts between the two bars (%s)", h.fn, h.applier, h.cite)
		}
	}
	if !containsCall(funcBodySource(t, "effectspicker.go", "drawEffectsPicker"), "bounceFocusToIC") {
		t.Error("drawEffectsPicker does not bounce focus — on_effects_dropdown_changed's whole body is " +
			"the pick plus focus_ic_input (courtroom.cpp:5653-5657)")
	}
}

// TestICColorPickBouncesFocusExceptTheColourWheel drives the applier both IC bars
// call, because the source census above only proves they REACH it.
//
// on_text_color_changed's focus_ic_input is the handler's last statement, outside
// its if/else (courtroom.cpp:6402), so it runs on both the wrap branch and the
// whole-message branch — this walks both. The one arm that must not bounce is
// Custom…, which has no canon counterpart at all (AO2's dropdown holds only the
// palette names, set_text_color_dropdown :6302-6341) and which opens a float that
// owns the keyboard.
func TestICColorPickBouncesFocusExceptTheColourWheel(t *testing.T) {
	// Whole-message branch: no frozen selection, a standard palette pick.
	a := colorPickTestApp(t)
	a.applyICColorPick(2)
	if a.icColor != 2 {
		t.Fatalf("the pick did not land (icColor %d) — nothing below is measuring the bounce", a.icColor)
	}
	if a.ctx.focusNext != icFieldID {
		t.Errorf("a colour pick queued focus onto %q, want %q (courtroom.cpp:6402)", a.ctx.focusNext, icFieldID)
	}

	// Wrap branch: a selection frozen through captureICColorSel (the real path —
	// the strip snapshots while the field is still focused) and wrapped in AO2
	// markup. Canon bounces here too: the focus call sits below the if/else, not
	// inside the else.
	b := colorPickTestApp(t)
	b.icInput = "hello world"
	b.ctx.focusID, b.ctx.caretField = icFieldID, icFieldID
	b.ctx.selAnchor, b.ctx.caret = 6, 11 // "world"
	b.captureICColorSel()
	b.ctx.focusID = "" // the dropdown's open-click unfocuses the field
	b.applyICColorPick(1)
	if b.icInput != "hello `world`" {
		t.Fatalf("the wrap did not land (%q) — nothing below is measuring the bounce", b.icInput)
	}
	if b.ctx.focusNext != icFieldID {
		t.Errorf("a selection-wrapping pick queued focus onto %q, want %q — focus_ic_input is the last "+
			"statement of on_text_color_changed, not the tail of its else branch", b.ctx.focusNext, icFieldID)
	}

	// Custom…: opens the free-hex wheel, which carries its own text field and whose
	// Enter must not reach the IC input's send. The ONE documented divergence.
	d := colorPickTestApp(t)
	d.applyICColorPick(icColorCustomIdx)
	if !d.showICColorWheel {
		t.Fatal("Custom… did not open the colour wheel — this case is no longer the one being excepted")
	}
	if d.ctx.focusNext != "" {
		t.Errorf("Custom… queued focus onto %q — the wheel's own hex field and the IC box's "+
			"Enter-to-send would be fighting over the keyboard", d.ctx.focusNext)
	}
	if !icColorPickOpensWheel(icColorCustomIdx) || icColorPickOpensWheel(icColorRainbowIdx) {
		t.Error("icColorPickOpensWheel no longer names exactly the Custom… row — the exception has drifted " +
			"off the arm that sets showICColorWheel")
	}
}

// colorPickTestApp is a courtroom App with real preferences — applyICColorChoice
// writes the rainbow/random flags through them — and no bind capture armed.
//
// macroBind is the one App field whose ZERO value means "armed" (slot 0), and
// bounceFocusToIC is a no-op while a capture is armed, so without the sentinel
// every bounce assertion below would pass or fail for the wrong reason.
func colorPickTestApp(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	a.screen = ScreenCourtroom
	a.macroBind = macroBindIdle
	return a
}

// icToggleBounces maps each checkbox drawn inside fn to whether its call is nested
// inside a bounceOnToggle(...). The key is a string literal from the call — the
// classic row's label, or the themed row's design.ini key / fallback label — which
// is what makes one entry per control readable at the call site.
//
// A control drawn twice must be wrapped BOTH times (the answers are AND-ed), so a
// second copy added later cannot pass on its sibling's wiring.
func icToggleBounces(t *testing.T, file, fn string) map[string]bool {
	t.Helper()
	body := funcBodySource(t, file, fn)
	inBounce := map[token.Pos]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || callName(call) != "bounceOnToggle" {
			return true
		}
		ast.Inspect(call, func(inner ast.Node) bool {
			if c2, ok := inner.(*ast.CallExpr); ok && isCheckboxCall(c2) {
				inBounce[c2.Pos()] = true
			}
			return true
		})
		return true
	})
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isCheckboxCall(call) {
			return true
		}
		for _, lit := range callStringLits(call) {
			if prev, seen := out[lit]; seen {
				out[lit] = prev && inBounce[call.Pos()]
				continue
			}
			out[lit] = inBounce[call.Pos()]
		}
		return true
	})
	return out
}

// isCheckboxCall reports whether call is one of the kit's two checkbox widgets.
func isCheckboxCall(call *ast.CallExpr) bool {
	switch callName(call) {
	case "Checkbox", "CheckboxIn":
		return true
	}
	return false
}

// callStringLits is every string literal appearing anywhere inside call's
// arguments — including the ones nested in a label helper such as
// themedToggleLabel("pre", "Pre").
func callStringLits(call *ast.CallExpr) []string {
	var out []string
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && len(lit.Value) >= 2 {
				out = append(out, lit.Value[1:len(lit.Value)-1])
			}
			return true
		})
	}
	return out
}

// TestBareBindKeyRequiresAlt pins the gate every plain-key bind namespace moved
// onto. The bare-letter space now belongs to the IC input, so a bare key must
// return 0; AltGr (Ctrl+Alt on Windows) produces real text and must return 0 too,
// or every accented character on a European layout would fire a bind while being
// typed.
func TestBareBindKeyRequiresAlt(t *testing.T) {
	for _, tc := range []struct {
		name           string
		alt, ctrl, arm bool
		key            sdl.Keycode
		want           sdl.Keycode
	}{
		{"bare key", false, false, false, sdl.K_m, 0},
		{"alt chord", true, false, false, sdl.K_m, sdl.K_m},
		{"AltGr (ctrl+alt)", true, true, false, sdl.K_m, 0},
		{"ctrl chord", false, true, false, sdl.K_m, 0},
		{"a capture owns the key", true, false, true, sdl.K_m, 0},
		{"no key this frame", true, false, false, 0, 0},
	} {
		a := &App{ctx: &Ctx{}, macroBind: -1}
		a.ctx.keyPressed, a.ctx.altHeld, a.ctx.ctrlHeld = tc.key, tc.alt, tc.ctrl
		if tc.arm {
			a.bindingFor = "Judge"
		}
		if got := a.bareBindKey(); got != tc.want {
			t.Errorf("%s: bareBindKey = %v, want %v", tc.name, got, tc.want)
		}
	}
	// Focus is deliberately NOT a fence any more: an Alt chord is never text, so a
	// bind must still fire while the user is in the chat box.
	a := &App{ctx: &Ctx{}, macroBind: -1}
	a.ctx.keyPressed, a.ctx.altHeld, a.ctx.focusID = sdl.K_m, true, icFieldID
	if a.bareBindKey() != sdl.K_m {
		t.Error("an Alt chord must fire even with the IC box focused — it can never be mistaken for typing")
	}
}

// TestNoBareLetterNamespaceSurvives is the CENSUS the user asked for: every
// plain-key bind namespace has to read the shared Alt gate, because a single one
// still reading c.keyPressed directly would race the first-keystroke IC claim for
// the same letter — the exact competition the move exists to end.
func TestNoBareLetterNamespaceSurvives(t *testing.T) {
	for _, h := range []struct{ file, fn string }{
		{"qol.go", "handleCharKeys"},
		{"qol.go", "handleEmoteKeys"},
		{"macros.go", "handleMacroKeys"},
		{"showname_bind.go", "handleShownameKeys"},
		{"ic_phrase.go", "handleICPhraseKeys"},
		{"jukebox.go", "handleJukeboxKeys"},
		{"stylepreset.go", "handleStylePresetKeys"},
		{"floatbox.go", "pollVoicePTT"},
	} {
		if !containsCall(funcBodySource(t, h.file, h.fn), "bareBindKey") {
			t.Errorf("%s (%s) does not go through bareBindKey — it is still claiming plain letters from the IC input", h.fn, h.file)
		}
	}
}

// TestEveryBindDisplaySurfaceAdvertisesTheChord is the DISPLAY half of the census
// above, and it exists because the first pass shipped without it: bareBindLabel /
// bareBindChordPrefix were created so "the cheat sheet and the Settings rows cannot
// drift apart", and then only the cheat sheet used them. Four in-app surfaces went
// on printing the bare letter that no longer fires anything (a showname preset's
// "Key: M", the wardrobe cell's key badge, the jukebox bind chip, the IC
// quick-phrase list) plus two the first sweep never even listed (the macro list +
// editor, the style-preset chip) and the voice push-to-talk readout.
//
// Source, because these are painters: they need a live Ctx, a font and a texture
// store to return anything, and what is being pinned is which LABEL HELPER they
// call — exactly the deletion-catcher shape srcgate_test.go documents.
//
// COUNTED, not merely present. A "does this function mention the helper anywhere"
// gate is not a census: drawSettingsGeneral is 200 lines and paints one chip beside
// a tooltip that names the chord, so reverting the chip alone left it green (proved
// by mutation). chips is the number of bind labels the painter renders; add a chip,
// update the number, and the reviewer sees the surface in the diff.
func TestEveryBindDisplaySurfaceAdvertisesTheChord(t *testing.T) {
	for _, s := range []struct {
		file, fn string
		chips    int
	}{
		{"hotkeysheet.go", "appendKeyMapSection", 1},  // the cheat sheet's three namespaces
		{"hotkeysheet.go", "hotkeyCheatEntries", 1},   // the macro rows
		{"settings.go", "drawSettingsGeneral", 1},     // showname presets: the per-preset bind button
		{"screens.go", "drawIniswapCell", 1},          // the wardrobe / char-select key badge
		{"jukebox.go", "drawJukeKeyBadge", 1},         // playlist + song bind chips (one painter, two callers)
		{"ic_phrase.go", "drawICPhraseSettings", 1},   // the IC quick-phrase list row
		{"macros.go", "drawMacroSettings", 2},         // the macro list row AND the editor's key button
		{"stylepreset.go", "drawStylePresets", 1},     // the saved-mood bind chip
		{"settings_voice.go", "drawSettingsVoice", 1}, // the push-to-talk readout
	} {
		body := funcBodySource(t, s.file, s.fn)
		got := countCalls(body, "bindChipLabel") + countCalls(body, "bareBindLabel")
		if got != s.chips {
			t.Errorf("%s (%s) renders %d bind labels through the sanctioned helpers, want %d — a chip is printing the retired bare key (or a new one was added without listing it here)",
				s.fn, s.file, got, s.chips)
		}
	}
}

// TestBindChipLabelIsTheOnlyWayToSpellAChip is the seam's encapsulation gate
// (CLAUDE.md rule 11). It drives bindChipLabel rather than restating its switch,
// and the load-bearing assertion is the last one: whatever a caller passes, a
// non-empty bind can only come back as the CHORD. A surface cannot opt out of the
// Alt+ prefix through this function, which is what makes the census above worth
// counting.
func TestBindChipLabelIsTheOnlyWayToSpellAChip(t *testing.T) {
	const (
		arming  = "press…"
		unbound = "+key"
	)
	if got := bindChipLabel("m", true, arming, unbound); got != arming {
		t.Errorf("an armed chip reads %q, want %q — the capture is in flight, the old bind is not the answer", got, arming)
	}
	if got := bindChipLabel("", true, arming, unbound); got != arming {
		t.Errorf("an armed unbound chip reads %q, want %q", got, arming)
	}
	if got := bindChipLabel("", false, arming, unbound); got != unbound {
		t.Errorf("an unbound chip reads %q, want the surface's own wording %q", got, unbound)
	}
	// Every bound spelling, whatever case the capture stored, comes back as the
	// chord — never the bare letter.
	for _, key := range []string{"m", "M", "f5", "space", "1"} {
		got := bindChipLabel(key, false, arming, unbound)
		if !strings.HasPrefix(got, bareBindChordPrefix) {
			t.Errorf("bindChipLabel(%q) = %q — a bound chip must advertise the chord that fires it", key, got)
		}
		if got == strings.ToUpper(key) {
			t.Errorf("bindChipLabel(%q) = %q, the bare key: that press only types into the IC box now", key, got)
		}
	}
	// The wardrobe grid paints one of these per bound cell on every frame the
	// char-select screen is up, so the single-character case — every real bind —
	// must not allocate. `"Alt+" + s` does; the table lookup does not.
	if n := testing.AllocsPerRun(bindChipAllocRuns, func() {
		_ = bindChipLabel("m", false, arming, unbound)
	}); n != 0 {
		t.Errorf("a one-key bind chip allocates %v per paint — the memo in bindlabel.go is being bypassed", n)
	}
	if n := testing.AllocsPerRun(bindChipAllocRuns, func() {
		_ = bindChipLabel("", false, arming, unbound)
	}); n != 0 {
		t.Errorf("an unbound chip allocates %v per paint", n)
	}
}

// bindChipAllocRuns is the sample count for the chip's alloc gate. Small: the
// operation is a bounds check and an array read, and AllocsPerRun discards a
// warm-up run of its own.
const bindChipAllocRuns = 100

// TestNoSurfaceStillPromisesTheBareKeyFires is the second half: a surface can call
// bareBindLabel for the chip and still tell the user, in prose one line below, that
// the bind "fires with no text box focused". That sentence was flatly false the
// moment bareBindKey stopped consulting focus at all (qol.go) — an Alt chord is
// never text, so a bind fires WHILE you type. Scans string literals only, so the
// package's comments may keep saying "bare key" about the history.
func TestNoSurfaceStillPromisesTheBareKeyFires(t *testing.T) {
	// The retired promises, lower-cased. Substrings, so a reworded but still-wrong
	// line ("fires when no text box is focused") is caught by the first entry.
	retired := []string{"text box focused", "no text box", "nothing focused"}
	var checked int
	for _, name := range packageGoFiles(t) {
		_, f := parsedFile(t, name)
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := stringLitValue(lit.Value)
			if err != nil {
				return true
			}
			checked++
			low := strings.ToLower(s)
			for _, bad := range retired {
				if strings.Contains(low, bad) {
					t.Errorf("%s draws %q — the focus fence is gone; a bind fires on %s<key> even mid-sentence", name, s, bareBindChordPrefix)
				}
			}
			return true
		})
	}
	if checked < bindProseMinLiterals {
		t.Fatalf("only %d string literals scanned — the corpus collapsed and this census now proves nothing", checked)
	}
}

// bindProseMinLiterals is the anti-vacuity floor for the prose census: the package
// draws many thousands of literals, so a walk that finds fewer than this has lost
// its corpus (a glob that stopped matching) rather than found a clean tree.
const bindProseMinLiterals = 2000

// packageGoFiles lists this package's non-test sources, so a prose census cannot be
// defeated by moving the sentence into a file the list forgot.
func packageGoFiles(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		t.Fatal("no package sources found")
	}
	return out
}

// countCalls counts the calls in n whose function name is fn. containsCall answers
// "at least one", which is too weak for a per-surface census: a painter with two
// chips can lose one and still contain the other.
func countCalls(n ast.Node, fn string) int {
	var count int
	ast.Inspect(n, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && callName(call) == fn {
			count++
		}
		return true
	})
	return count
}

// TestTheClaimRunsAfterEveryBindCapture pins the ORDER in App.Frame. An armed
// capture owns the next keypress; if the IC claim ran first it would focus the box
// and the capture would bind nothing. Source, because ordering inside a
// single-pass frame has no observable return value.
func TestTheClaimRunsAfterEveryBindCapture(t *testing.T) {
	body := funcBodySource(t, "app.go", "Frame")
	order := callOrder(body, "pollCharBind", "pollShownameBind", "pollICPhraseBind", "pollVoicePTT",
		"pollStylePresetBind", "claimTypingForIC")
	if len(order) == 0 || order[len(order)-1] != "claimTypingForIC" {
		t.Fatalf("Frame's poll order is %v — claimTypingForIC must come LAST, after every bind capture", order)
	}
}

// TestHotkeySheetAdvertisesTheChordThatFires closes the loop the cheat sheet used
// to leave open: it printed a bare letter (and, for macros, a Ctrl chord that
// never fired at all) for binds that are Alt chords.
func TestHotkeySheetAdvertisesTheChordThatFires(t *testing.T) {
	if got := bareBindLabel("m"); got != bareBindChordPrefix+"M" {
		t.Errorf("bareBindLabel(m) = %q, want %q", got, bareBindChordPrefix+"M")
	}
	if got := bareBindLabel(""); got != "(no key)" {
		t.Errorf("an unbound row reads %q, want %q", got, "(no key)")
	}
	if !strings.HasPrefix(bareBindChordPrefix, "Alt") {
		t.Errorf("the advertised prefix is %q but bareBindKey gates on Alt — the sheet would lie", bareBindChordPrefix)
	}
}
