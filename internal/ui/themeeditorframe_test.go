package ui

// THE EDITOR THROUGH THE REAL FRAME (v1.90.0 W7a).
//
// frameharness_test.go names "W7's editor pass" as one of its concrete future call
// sites; this is that call site. Every gate here goes through App.Frame, so the
// screen's WIRING is what is under test — the skippability arm, the draw dispatch,
// the Esc ladder and the poll cluster all live inside Frame and are reached by
// nothing that calls drawThemeEditor directly.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// stageEditorApp is a frame-driven App with the theme editor open over a sidecar
// carrying one probe element.
func stageEditorApp(t *testing.T) (*App, func()) {
	t.Helper()
	a, cleanup := stageFrameDrivenApp(t)
	if a.themeSidecar == nil {
		a.themeSidecar = theme.NewSidecar()
	}
	el := editProbeElement()
	el.Locked, el.Hidden = false, false
	a.themeSidecar.Elements = append(a.themeSidecar.Elements, el)
	if !a.openThemeEditor(ScreenCourtroom) {
		cleanup()
		t.Fatal("openThemeEditor refused a theme that has a sidecar")
	}
	return a, cleanup
}

// TestThemeEditorScreenSurvivesRealFrames is the wiring gate. An unwired draw
// dispatch shows the previous screen's pixels; an editor that bounces out on frame
// one is the shape drawCourtroom's own no-session arm would produce.
func TestThemeEditorScreenSurvivesRealFrames(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()

	driveFrames(a, 3)
	if a.screen != ScreenThemeEditor {
		t.Fatalf("three real frames left the app on screen %d, want ScreenThemeEditor (%d)",
			a.screen, ScreenThemeEditor)
	}
	if a.te == nil {
		t.Fatal("the editor released itself while its own screen was up")
	}
	// The two legacy overlay editors are DISARMED: two tools, two scopes, never armed
	// simultaneously (layouteditlock_test.go enforces the exclusion from the other
	// side).
	if a.layoutEdit || a.classicEdit {
		t.Errorf("a legacy overlay editor is still armed under the theme editor "+
			"(layoutEdit=%v classicEdit=%v) — the courtroom would be fenced by an editor nobody can see",
			a.layoutEdit, a.classicEdit)
	}
}

// TestThemeEditorFrameStaysInsideItsChromeBudget is the alloc gate the design asks
// for: the CANVAS is the courtroom frame (already gated at zero by the existing
// alloc suite), and the rails get a named, non-zero budget.
func TestThemeEditorFrameStaysInsideItsChromeBudget(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	a.te.selectOnly(elementTarget(len(a.themeSidecar.Elements) - 1))

	settle(func() { driveFrame(a) })
	if n := testing.AllocsPerRun(30, func() { driveFrame(a) }); n > editorChromeAllocBudget {
		t.Fatalf("one editor frame allocates %.0f/op, past the named budget of %d — the usual cause is a "+
			"string built inside the element rail's ROW LOOP, which scales with the theme",
			n, editorChromeAllocBudget)
	}
	if a.screen != ScreenThemeEditor {
		t.Fatal("the measured frames were not editor frames")
	}
}

// TestBackDoesNotOutliveTheRailsThatDrawAfterIt is a crash gate, and it is one a
// function-level test could not have found.
//
// drawThemeEditor's order is header, then both rails. The header's own Back button calls
// requestEditorExit, which on a CLEAN document releases a.te on the spot — and every
// rail below dereferences a.te.doc. Pressing Back was therefore a nil dereference on the
// render thread, i.e. a client crash, on the most ordinary exit there is.
//
// It drives the real button through the real frame, so it also fails if the band is
// re-spaced out from under it (a miss leaves the editor open, which is the assertion).
func TestBackDoesNotOutliveTheRailsThatDrawAfterIt(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	if a.te.doc.dirty {
		t.Fatal("fixture: the document must be clean, or Back arms a confirmation instead of leaving")
	}
	driveClickAt(a, frameHarnessW-editRowPad-editorHeaderBtnW/2, editBannerH/2)
	if a.te != nil {
		t.Fatal("the Back button did not leave the editor — the press missed the header band")
	}
	if a.screen != ScreenCourtroom {
		t.Errorf("Back landed on screen %d, want the screen the editor was opened FROM (%d)",
			a.screen, ScreenCourtroom)
	}
	// And the frame after the exit is an ordinary one: the editor released cleanly
	// rather than half-way through its own draw.
	driveFrame(a)
}

// TestEditorEscLadderClearsThenLeaves pins the three-step Esc contract through the
// real key path. Esc must ALWAYS have somewhere to go — a screen you cannot leave
// with the keyboard is the hard lock app.go's own Esc comment is about.
func TestEditorEscLadderClearsThenLeaves(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	a.te.selectOnly(elementTarget(len(a.themeSidecar.Elements) - 1))

	pressEsc := func() {
		a.ctx.BeginFrame(frameHarnessDt)
		a.ctx.escPressed = true
		a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
	}

	// Esc resolves the TOPMOST thing first, and closeTopOverlay's table is long — a
	// themed courtroom fixture legitimately has panels of its own open under the
	// editor. What this gate pins is the ORDER of the editor's own ladder: the
	// selection must clear on a strictly earlier press than the one that leaves, and
	// both must happen inside a bounded number of presses (a screen that eats Esc
	// forever is the hard lock app.go's Esc comment is about).
	const escBudget = 12
	clearedAt, closedAt := -1, -1
	for i := 1; i <= escBudget && closedAt < 0; i++ {
		pressEsc()
		if clearedAt < 0 && a.te != nil && a.te.selN == 0 {
			clearedAt = i
		}
		if a.te == nil {
			closedAt = i
		}
	}
	if closedAt < 0 {
		t.Fatalf("%d Esc presses never left the editor — a screen with no keyboard exit is a hard lock",
			escBudget)
	}
	if clearedAt < 0 {
		t.Fatal("the editor closed without the selection ever clearing first — Esc must clear before it leaves")
	}
	if clearedAt >= closedAt {
		t.Errorf("the selection cleared on press %d and the editor left on press %d — clearing must come "+
			"strictly first, or Esc throws away the screen when the user meant to deselect",
			clearedAt, closedAt)
	}
	if a.screen != ScreenCourtroom {
		t.Errorf("leaving the editor landed on screen %d, want the screen it was opened FROM (%d) — "+
			"prevScreen belongs to the menu screens and would send a courtroom-opened editor to "+
			"whatever menu was last visited", a.screen, ScreenCourtroom)
	}
}

// TestEditorOwnsCtrlZThroughTheRealChord drives the chord the way HandleEvent
// delivers it — through c.hotkey, never c.keyPressed — because that is the exact
// trap editorUndoChord exists for: an in-draw c.keyPressed check for Ctrl+Z is dead
// code, and Ctrl+Y would fire its default bind mid-edit.
func TestEditorOwnsCtrlZThroughTheRealChord(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	idx := len(a.themeSidecar.Elements) - 1
	a.te.selectOnly(elementTarget(idx))
	before := a.themeSidecar.Elements[idx].Rect

	a.editorNudgeSelection(1, 0, false)
	if a.themeSidecar.Elements[idx].Rect == before {
		t.Fatal("the nudge did not move the element")
	}

	a.ctx.hotkey = sdl.K_z
	if !a.editorUndoChord() {
		t.Fatal("Ctrl+Z on the editor screen must be consumed by the editor")
	}
	if a.ctx.hotkey != 0 {
		t.Error("a consumed chord must zero c.hotkey, or it also fires whatever it is bound to")
	}
	if got := a.themeSidecar.Elements[idx].Rect; got != before {
		t.Errorf("Ctrl+Z left the rect at %+v, want %+v", got, before)
	}

	a.ctx.hotkey = sdl.K_y
	if !a.editorUndoChord() {
		t.Fatal("Ctrl+Y on the editor screen must be consumed by the editor")
	}
	if got := a.themeSidecar.Elements[idx].Rect; got == before {
		t.Error("Ctrl+Y did not redo the nudge")
	}
}

// TestEditorUndoChordReachesTheEditorThroughAFrameWithNoRoom is the gate the chord's
// own dispatch was missing, and it is the difference between "the function works" and
// "the key works".
//
// THE DEFECT IT CLOSES. editorUndoChord had exactly one caller — handleHotkeys — and
// handleHotkeys is called from the three COURTROOM draw passes. drawEditorCanvas
// reaches drawCourtroom only with a live room, so on the editor's HEADLINE entry point
// (the Settings row, reachable with no server attached) the offline arm
// drawEditorGeometryCanvas drew instead, no dispatcher ran, and Ctrl+Z did nothing at
// all while c.hotkey survived the frame to fire whatever it was bound to. Every gate
// that covered the chord called a.editorUndoChord() by hand, so the suite could not
// see it: the same "present but unreachable" shape
// TestUnsavedEditsSurviveARealBackgroundThemeApply was written to close for the
// reclaim.
//
// So this one nils the room — the state the Settings entry point is in — and drives
// the chord through App.Frame. The mutation it answers to is deleting
// `if a.themeEditorOpen() { a.editorUndoChord() }` from App.Frame.
func TestEditorUndoChordReachesTheEditorThroughAFrameWithNoRoom(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	idx := len(a.themeSidecar.Elements) - 1
	a.te.selectOnly(elementTarget(idx))

	// NO ROOM: drawEditorCanvas takes its offline arm, which is the arm that reaches no
	// courtroom pass and therefore no handleHotkeys.
	a.room = nil
	driveFrame(a)
	if a.screen != ScreenThemeEditor || a.te == nil {
		t.Fatalf("the editor did not survive a frame with no room (screen %d, te %v) — the session-free "+
			"arm is the one the Settings entry point uses", a.screen, a.te != nil)
	}

	before := a.themeSidecar.Elements[idx].Rect
	a.editorNudgeSelection(1, 0, false)
	edited := a.themeSidecar.Elements[idx].Rect
	if edited == before {
		t.Fatal("the nudge did not move the element; there is nothing for the chord to undo")
	}

	driveHotkey(a, sdl.K_z)
	if got := a.themeSidecar.Elements[idx].Rect; got != before {
		t.Fatalf("Ctrl+Z driven through a REAL frame with no room left the rect at %+v, want %+v — the "+
			"chord is dispatched only from the courtroom passes, so the editor's own screen never "+
			"claims it when it is opened from Settings with no server attached", got, before)
	}
	if a.ctx.hotkey != 0 {
		t.Errorf("the frame left c.hotkey = %d — an unconsumed chord also fires whatever it is bound to, "+
			"mid-edit", a.ctx.hotkey)
	}

	driveHotkey(a, sdl.K_y)
	if got := a.themeSidecar.Elements[idx].Rect; got != edited {
		t.Errorf("Ctrl+Y through a real frame left the rect at %+v, want the redone %+v", got, edited)
	}
}

// TestEditorScreenIsFrameRateSkippable pins the arm whose absence is invisible: an
// unwired expScreenSkippable burns full CPU on a still screen forever, and the
// tempting join (onto the courtroom pair) would make the editor non-skippable in
// exactly the Settings-with-no-server case it is most often opened in.
func TestEditorScreenIsFrameRateSkippable(t *testing.T) {
	a := &App{screen: ScreenThemeEditor}
	if !a.expScreenSkippable() {
		t.Fatal("ScreenThemeEditor is not frame-rate skippable — it would render at full rate forever, " +
			"with a still theme on screen and nothing to explain why the fans are on")
	}
	// …and with no session, which is the case the courtroom pair would have failed.
	a.sess = nil
	if !a.expScreenSkippable() {
		t.Fatal("the editor stopped being skippable with no session — that is the Settings entry point, " +
			"i.e. the common case")
	}
}
