package ui

// THE IMPORT REPORT PANEL'S GATES (v1.90.0 W9).
//
// The property that matters is the one hard rule 11 names: App.themeReport was a
// bounded slice written on every theme apply, kept "for the editor", and read by
// nothing but a test — the sidecar-[overrides] shape exactly. These gates fail if
// it goes back to that, and they do it by driving the REAL rail chip and the REAL
// panel rather than by asserting over the slice a second time.

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// reportFixtureNotes is what the fixture theme's apply "noticed". Two notes, one
// long enough to wrap, so the bake has something to do and the panel has something
// to scroll.
var reportFixtureNotes = []string{
	"element \"gold_wing\" was skipped: kind \"lantern\" is not a kind this build knows, " +
		"and the section was preserved unchanged so a newer client can still read it",
	"fonts this theme names but could not find: Court Serif",
}

// stageReportEditor opens the frame-driven editor over a theme whose apply
// produced notes.
//
// The report is INSTALLED THE WAY THE APPLY INSTALLS IT — through noteThemeReport,
// the one landing site the poll uses — so the fixture cannot pass by writing a
// slice somewhere the real path never touches.
func stageReportEditor(t *testing.T) (*App, func()) {
	t.Helper()
	a, cleanup, _ := stageEditorCanvas(t)
	a.noteThemeReport("Noted", reportFixtureNotes)
	driveFrame(a)
	return a, cleanup
}

// reportChipCentre is the rail-footer chip's hit point, in drawEditorElementRail's
// own arithmetic. Duplicated rather than exported (the editorEyeToggleAt idiom),
// and the gate below PROVES it lands by first checking the chip is drawn at all —
// a stale coordinate would otherwise make this pass vacuously by missing.
func reportChipCentre() (x, y int32) {
	rail := sdl.Rect{X: 0, Y: editBannerH, W: editRailW, H: frameHarnessH - editBannerH}
	foot := rail.Y + rail.H - editRowH
	return rail.X + rail.W/2, foot - editRowH + editRowH/2
}

// TestTheImportReportReachesAUserSurface is the rule-11 gate for this wave's other
// new seam, and it is driven through App.Frame rather than by calling the panel's
// own predicate — a gate that asserted on themeReportPanelUp() would be a mirror
// of the fix and would stay green with the rail's call site deleted.
//
// It looks for a note's OWN WORDS in what the panel bakes: text the panel has no
// way to invent, because it came out of the sidecar reader. A surface that had
// re-implemented "say something about the theme" (a count, a canned sentence)
// passes every other test in this file and fails this one.
func TestTheImportReportReachesAUserSurface(t *testing.T) {
	a, cleanup := stageReportEditor(t)
	defer cleanup()

	if a.themeReportPanelUp() {
		t.Fatal("the panel is up before anything opened it")
	}
	x, y := reportChipCentre()
	driveClickAt(a, x, y)
	if !a.themeReportPanelUp() {
		t.Fatalf("a real click at the rail chip (%d,%d) opened nothing — App.themeReport still "+
			"reaches no user surface, which is the hole this file exists to close", x, y)
	}
	p := a.te.report
	// The frame above already DREW it, so what follows is what the real draw baked,
	// not what a test asked bake() for.
	if p.n == 0 {
		t.Fatal("the panel baked no lines out of a report with notes — the draw never reached bake()")
	}
	joined := strings.Join(p.line[:p.n], " ")
	for _, want := range []string{"gold_wing", "Court Serif"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the panel never shows %q. It has:\n  %s", want, joined)
		}
	}
	if p.notes != len(reportFixtureNotes) {
		t.Errorf("the panel says %d notes, the report has %d", p.notes, len(reportFixtureNotes))
	}

	// AND THERE IS A WAY OUT, pressed for real. The rail chip cannot be it — the
	// rail is under the panel fence while a panel is up, so its Button is blank by
	// construction — which leaves the panel's own Close, and a panel with no live
	// way out is a modal that forgot to say so.
	r := editorReportPanelRect(frameHarnessW, frameHarnessH)
	driveClickAt(a, r.X+r.W-editRowPad-editReportCloseW/2, r.Y+2+editReportRowH/2)
	if a.themeReportPanelUp() {
		t.Fatal("the panel's own Close did not close it — the only way out is Esc")
	}
}

// TestACleanThemeDrawsNoReportChip is the other half, and it is what keeps the
// surface from becoming a nag. An editor over a theme this client understood
// completely must say nothing at all — a chip reading "0 notes" is a permanent
// piece of furniture everybody learns to ignore.
func TestACleanThemeDrawsNoReportChip(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t)
	defer cleanup()
	a.noteThemeReport("Clean", nil)
	driveFrame(a)

	rail := sdl.Rect{X: 0, Y: editBannerH, W: editRailW, H: 400}
	if got := a.drawEditorReportChip(rail, rail.Y+300); got != 0 {
		t.Fatalf("a theme with nothing to report drew a %d px chip", got)
	}
	// And the real chip's hit point does nothing, because there is nothing there.
	x, y := reportChipCentre()
	driveClickAt(a, x, y)
	if a.themeReportPanelUp() {
		t.Fatal("a click where the chip would be opened an empty report — it would say nothing, at length")
	}
	a.openThemeReport()
	if a.themeReportPanelUp() {
		t.Fatal("openThemeReport armed a panel over no notes")
	}
}

// TestTheReportPanelIsAPanelAndNotAModal pins the two halves of the editor's
// fence, the pair of holes the Fonts panel fell into once and the gallery had to
// join in this same wave.
func TestTheReportPanelIsAPanelAndNotAModal(t *testing.T) {
	a, cleanup := stageReportEditor(t)
	defer cleanup()

	a.openThemeReport()
	if !a.themeReportPanelUp() {
		t.Fatal("the panel did not open over a report with notes")
	}
	if !a.editorPanelUp() {
		t.Fatal("editorPanelUp does not see the report panel — the rails behind it stay live")
	}
	a.closeEditorPanels()
	if a.themeReportPanelUp() {
		t.Fatal("closeEditorPanels left the report panel up — Esc walks past it")
	}
	if a.editorPanelUp() {
		t.Fatal("the fence is still raised with no panel on screen")
	}
}

// TestTheReportWrapsOnChangeAndNotPerFrame is the alloc-discipline gate.
// themeReportCap is 69 notes; Ctx.WrapText allocates; the editor frame is bounded
// by editorChromeAllocBudget. Wrapping on the draw would be that budget's named
// regression, at sixty-nine strings a frame.
func TestTheReportWrapsOnChangeAndNotPerFrame(t *testing.T) {
	a, cleanup := stageReportEditor(t)
	report := reportFixtureNotes
	defer cleanup()

	var p themeReportPanel
	p.bake(a.ctx, report, 400)
	after := p.bakes
	for i := 0; i < 8; i++ {
		p.bake(a.ctx, report, 400)
	}
	if p.bakes != after {
		t.Fatalf("eight redraws cost %d rewraps — the panel wraps every frame", p.bakes-after+1)
	}
	if p.bake(a.ctx, report, 300); p.bakes != after+1 {
		t.Fatal("a resized panel did not rewrap — the lines overflow the column they were cut to")
	}
	if p.bake(a.ctx, report[:1], 300); p.bakes != after+2 {
		t.Fatal("a different report did not rewrap — the panel would show the previous theme's notes")
	}
	// A DIFFERENT REPORT OF THE SAME LENGTH must also rewrap, and this is the arm
	// that a count-keyed bake fails. Two themes producing two notes each is not a
	// contrived case: it is a font this machine lacks plus an unbound key, which is
	// most imported themes. Keyed on the count, switching between them leaves the
	// first theme's diagnostics on screen under the second theme's name.
	swapped := []string{"a wholly different first note", "and a different second"}
	if len(swapped) != len(report) {
		t.Fatalf("the fixture reports %d notes and the swap has %d — this arm proves nothing",
			len(report), len(swapped))
	}
	p.bake(a.ctx, report, 300)
	before := p.bakes
	p.bake(a.ctx, swapped, 300)
	if p.bakes != before+1 {
		t.Fatal("a DIFFERENT report with the SAME note count did not rewrap — the panel is keyed on the count")
	}
	if !strings.Contains(strings.Join(p.line[:p.n], " "), "wholly different") {
		t.Fatal("the panel still shows the previous report's text")
	}
}

// TestTheReportChipAllocatesNothingPerFrame is the alloc gate for the two strings
// this wave added to a surface that draws sixty times a second.
//
// IT MEASURES THE SEAM, NOT THE FRAME, and that is deliberate — it was measured
// the other way first. editorChromeAllocBudget is 512, so a frame-level assertion
// swallows the two allocations an unbaked chip label costs: the mutation (drop the
// change-check, rebuild "Imported · 4 notes" every draw) PASSES a frame gate and
// fails this one. A budget that cannot see the regression it was cited for is a
// budget, not a gate.
//
// ZERO, not a budget, because both producers are supposed to be pure reads once
// baked: the chip hands Button an interned string, and the panel walks a fixed
// array. Anything above zero here is work that moved onto the draw.
func TestTheReportChipAllocatesNothingPerFrame(t *testing.T) {
	a, cleanup := stageReportEditor(t)
	defer cleanup()
	if len(a.themeReport) == 0 {
		t.Fatal("the fixture reports nothing, so no chip is drawn and this gate measures nothing")
	}

	rail := sdl.Rect{X: 0, Y: editBannerH, W: editRailW, H: frameHarnessH - editBannerH}
	y := rail.Y + rail.H - editRowH*2
	a.drawEditorReportChip(rail, y) // one warm draw: the label bakes, the face memoises
	if n := testing.AllocsPerRun(50, func() { a.drawEditorReportChip(rail, y) }); n > 0 {
		t.Errorf("the rail chip allocates %.0f/op — its label is being rebuilt at the draw", n)
	}

	// The PANEL's own draw, which is where the wrap lives: up to themeReportCap
	// notes rewrapped per frame would be the same regression an order of magnitude
	// larger.
	a.openThemeReport()
	a.drawThemeReportPanel(frameHarnessW, frameHarnessH)
	if n := testing.AllocsPerRun(50, func() {
		a.drawThemeReportPanel(frameHarnessW, frameHarnessH)
	}); n > 0 {
		t.Errorf("the report panel allocates %.0f/op — the wrap is running on the draw instead of on change", n)
	}

	// And the whole frame still fits the editor's own named budget, with both up.
	if n := allocsPerFrame(editorAllocFrames, editorChromeAllocBudget, func() { driveFrame(a) }); n > editorChromeAllocBudget {
		t.Fatalf("an editor frame with the report panel up allocates %.0f/op, past the named budget of %d",
			n, editorChromeAllocBudget)
	}
}

// TestTheReportPanelIsBounded is hard rule 4 for the baked line array. A report is
// up to themeReportCap notes of arbitrary length, and a pathological one must
// stop at the cap and SAY it stopped, never grow past it and never end silently.
func TestTheReportPanelIsBounded(t *testing.T) {
	a, cleanup := stageReportEditor(t)
	defer cleanup()

	// One long note per slot the report can hold, each of which wraps to several
	// lines in a narrow column: comfortably more lines than the cap.
	long := strings.Repeat("a diagnostic sentence that will not fit on one line. ", 6)
	flood := make([]string, themeReportCap)
	for i := range flood {
		flood[i] = long
	}
	var p themeReportPanel
	p.bake(a.ctx, flood, 200)
	if p.n > editReportLineCap {
		t.Fatalf("the panel baked %d lines, cap is %d", p.n, editReportLineCap)
	}
	if p.dropped == 0 {
		t.Fatalf("%d notes of %d wrapped lines fitted in %d lines with nothing dropped — "+
			"either the flood is too small to prove anything or the cap is not being applied",
			len(flood), p.n, editReportLineCap)
	}
	if p.dropped+notesShown(&p) != len(flood) {
		t.Errorf("the panel shows some of %d notes and reports %d dropped — the two do not add up",
			len(flood), p.dropped)
	}
}

// notesShown counts the baked lines that open a note (the bullet ones).
func notesShown(p *themeReportPanel) int {
	n := 0
	for i := 0; i < p.n; i++ {
		if strings.HasPrefix(p.line[i], editReportBullet) {
			n++
		}
	}
	return n
}
