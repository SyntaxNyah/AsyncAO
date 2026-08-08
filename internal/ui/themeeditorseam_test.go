package ui

// THE EDITOR'S FOUR STRUCTURAL SEAMS, DRIVEN (v1.90.0 W7a).
//
// The sibling files gate the editor's BEHAVIOUR — a drag moves a box, an undo puts
// it back, a save lands a file. This one gates the four claims that are load-bearing
// and yet have no behavioural symptom a fixture would trip over:
//
//  1. the canvas IS the live courtroom pass, not a mockup of it;
//  2. the rails own their own pixels, so one press is never two;
//  3. the peek is an ALPHA change and never a clip change;
//  4. every document mutation goes through the one funnel, and unsaved edits survive
//     a REAL background apply — the frame's own, not a hand-called reclaim.
//
// Each is a deletion-catcher: the mutation it answers to is named at the gate, and
// each one leaves the rest of the suite green when applied.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// editorSeamThemeName is the folder the reapply gate authors its fixture theme in.
// Named because the durability check compares it against the applied preference —
// a document is about ONE theme, and the reclaim is guarded on exactly that.
const editorSeamThemeName = "w7aseamgate"

// ---------------------------------------------------------------------------
// 1 — the canvas is the LIVE courtroom pass, at full bleed
// ---------------------------------------------------------------------------

// TestTheEditorCanvasIsTheLiveCourtroomPassAtFullBleed is the wave's headline
// requirement, asked mechanically.
//
// "Full-bleed LIVE courtroom preview — the real drawCourtroom themed pass as the
// canvas, not a mockup" is the sentence the whole screen exists to satisfy, and it
// is the one thing a screenshot cannot distinguish: a hand-drawn approximation of a
// courtroom looks like a courtroom. So the gate asks the only witness that cannot be
// faked. `toolboxThemeRectOn` is armed by drawCourtroomThemed and by nothing else,
// and drawCourtroom CLEARS it at the top of every frame (screens.go), so its value
// after one editor frame answers "did the real themed pass run inside this screen's
// dispatch" and nothing else.
//
// THE SECOND HALF IS THE "NOT AN INSET PREVIEW" CLAIM, and it is arithmetic rather
// than opinion: if the rails reserved space from the canvas, fitDesignCanvas would
// have been handed a box editRailW*2 narrower and the fitted canvas could not be
// wider than the gap between them. It is wider, so the rails float OVER the frame —
// which is what makes the editor 1:1 with the size the theme will play, with no mode
// hop for pixel work.
//
// Mutations it answers to: replacing `a.drawCourtroom(w, h)` in drawEditorCanvas with
// the geometry arm (or with any mockup); insetting the canvas by the rails.
func TestTheEditorCanvasIsTheLiveCourtroomPassAtFullBleed(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	// A FILLING FIT MODE, named rather than inherited. The shipped default is
	// ThemeFitNative — 1:1, downscale-only — under which the stock 714 px canvas is
	// NARROWER than the gap between the two rails, so the width arithmetic below
	// cannot tell "floated over" from "inset" at all. The geometry decision under test
	// is the same in every mode (drawEditorCanvas hands the canvas the whole window);
	// this picks the one mode in which the decision is observable.
	a.d.Prefs.SetThemeFit(config.ThemeFitLetterbox)
	a.invalidateThemeCanvases()

	driveFrame(a) // a plain courtroom frame first, so the fixture's own state is known
	if !a.toolboxThemeRectOn {
		t.Fatal("the fixture never reached drawCourtroomThemed on a plain courtroom frame — the witness " +
			"below would be measuring nothing")
	}
	if a.themeSidecar == nil {
		a.themeSidecar = theme.NewSidecar()
	}
	if !a.openThemeEditor(ScreenCourtroom) {
		t.Fatal("openThemeEditor refused a theme that has a sidecar")
	}

	a.toolboxThemeRectOn = false
	driveFrame(a)

	if a.screen != ScreenThemeEditor {
		t.Fatalf("the frame left the editor screen (now %d) — nothing below is measuring an editor frame",
			a.screen)
	}
	if !a.toolboxThemeRectOn {
		t.Fatal("an editor frame did not run drawCourtroomThemed — the canvas is a MOCKUP, so every " +
			"pixel the user is editing against is one this client does not actually draw, and the " +
			"1:1 promise the whole screen is built on is gone")
	}
	lay := &a.themeLay
	if !lay.valid {
		t.Fatal("the editor frame left no themed layout — the canvas resolved no geometry to edit")
	}
	if between := frameHarnessW - editRailW*2; lay.area.W <= between {
		t.Fatalf("the canvas is %d px wide inside a %d px window, i.e. no wider than the %d px gap "+
			"between the rails — the canvas has been INSET instead of floated over, which means the "+
			"user is laying a theme out at a size it will never play at", lay.area.W, frameHarnessW, between)
	}
}

// ---------------------------------------------------------------------------
// 2 — the rails own their pixels
// ---------------------------------------------------------------------------

// TestARailPressNeverReachesTheCanvas pins editorCanvasRect's arithmetic.
//
// The kit is single-pass and the canvas hit-tests with raw pointIn (it must — the
// courtroom underneath is fenced, so hovering() answers false everywhere), which
// means nothing STOPS a press on an inspector button from also selecting whatever
// the theme parked behind it. editorCanvasRect is the whole defence: the canvas
// publishes the rect it is actually pressable in, minus the band and both rails.
// That is overlayfence.go's "publish the rect that is actually painted" rule applied
// by subtraction instead of by a fence, and a gate is the only thing that keeps it
// true, because the symptom — one click doing two things — only appears on a theme
// whose art happens to run under a rail.
//
// The fixture builds exactly that theme: the design canvas is centred, so its left
// edge sits behind the left rail, and the element is parked in that overlap.
//
// Mutation it answers to: `editorCanvasRect` returning the whole window.
func TestARailPressNeverReachesTheCanvas(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	el := tgts[0]
	// A FILLING FIT MODE, for the same reason the full-bleed gate names one: under the
	// shipped ThemeFitNative default the 714 px canvas is centred with 283 px of empty
	// window on each side, so it never reaches under a 232 px rail and there would be
	// no overlap to press in. Forced rather than skipped-on — a gate that quietly opts
	// out when the default moves is a gate that pins nothing.
	a.d.Prefs.SetThemeFit(config.ThemeFitLetterbox)
	a.invalidateThemeCanvases()
	driveFrame(a)
	lay := &a.themeLay
	if lay.area.X >= editRailW-editorSeamRailBitePx {
		t.Fatalf("the canvas starts at x=%d even under a filling fit, clear of the %d px rail — there is "+
			"no overlap to press in, so this gate would pin nothing", lay.area.X, editRailW)
	}
	// A box whose painted right edge lands just inside the rail, so its whole body is
	// under chrome.
	wpx := int(float64(editRailW-editorSeamRailBitePx-lay.area.X) / lay.scaleX)
	a.themeSidecar.Elements[el.elem].Rect = theme.Rect{X: 0, Y: editorSeamBoxTopPx, W: wpx, H: editorSeamBoxTopPx}
	a.invalidateThemeCanvases()
	driveFrame(a)

	box := editorPainted(t, a, el)
	cx, cy := editorCentreOf(box)
	if cx >= editRailW {
		t.Fatalf("the element painted at %+v, clear of the %d px rail — the press below would not be a "+
			"rail press at all", box, editRailW)
	}
	// The PROBE still sees it: the gate is about the canvas rect refusing the press,
	// not about the element having become unfindable.
	if got, _ := a.editorProbe(lay, cx, cy); got != el {
		t.Fatalf("the probe under the rail resolved to %v, want the parked element — this gate would "+
			"pass for the wrong reason", got)
	}

	a.te.selectClear()
	driveMouse(a, cx, cy, true)
	if a.te.drag.active() {
		t.Error("a press on the LEFT RAIL started a canvas gesture — the same click would work an " +
			"inspector control and drag a box behind it at the same time")
	}
	if a.te.selN != 0 {
		t.Errorf("a press on the left rail selected %d canvas target(s)", a.te.selN)
	}
	driveMouse(a, cx, cy, false)

	// NON-VACUITY: the identical press two pixels past the rail's edge DOES work, so
	// the gate above is not passing because presses never do.
	a.themeSidecar.Elements[el.elem].Rect = editorCentreRect(a, 0, 0)
	a.invalidateThemeCanvases()
	driveFrame(a)
	ox, oy := editorCentreOf(editorPainted(t, a, el))
	driveMouse(a, ox, oy, true)
	if !a.te.drag.active() {
		t.Fatal("a press on the OPEN canvas started no gesture either — the rail gate above proves nothing")
	}
	driveMouse(a, ox, oy, false)
}

const (
	// editorSeamRailBitePx is how far inside the rail's right edge the overlap fixture
	// parks its box. Two px: far enough that no rounding puts the box's edge on the
	// boundary, close enough that the fixture works on any canvas that reaches the
	// rail at all.
	editorSeamRailBitePx = 2
	// editorSeamBoxTopPx is the overlap fixture's top edge and size in DESIGN px.
	// Below editBannerH once painted, so the press it takes is a CANVAS press being
	// refused by the rail rather than a header press being refused by the band.
	editorSeamBoxTopPx = 40
)

// ---------------------------------------------------------------------------
// 3 — the peek is alpha, never a clip
// ---------------------------------------------------------------------------

// TestSpacePeekIsAnAlphaChangeNeverAClip pins the one decision on this screen that
// was made to CLOSE a bug class rather than to fix a bug.
//
// The macOS blank-chatbox hunt established that SDL clip timing is backend-specific
// — Metal evaluates the clip at USE time, so a device-exact bracket that measured
// safe under the software renderer collapsed the clip and ate every chat line. A
// Space-peek implemented as "clip the rails away" is that same shape, on a surface
// nobody would test on Metal until a user reported the rails had vanished. Peek is
// therefore an alpha on ONE function, and this gate keeps it there.
//
// Mutations it answers to: editorPlate darkening/tinting instead of fading (the
// peek stops being a peek and the rails just change colour); any editor file
// reaching for the renderer's own clip.
func TestSpacePeekIsAnAlphaChangeNeverAClip(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()

	a.te.peek = false
	normal := a.editorPlate(ColPanel)
	a.te.peek = true
	peeked := a.editorPlate(ColPanel)

	if normal.R != peeked.R || normal.G != peeked.G || normal.B != peeked.B {
		t.Errorf("peek changed the plate's COLOUR (%v -> %v), not just its alpha — a peek that repaints "+
			"the rails is a second theme for the chrome, and the frame underneath is still hidden",
			normal, peeked)
	}
	if normal.A != editRailAlpha || peeked.A != editRailPeekAlpha {
		t.Errorf("plate alpha is %d normally and %d while peeking, want %d and %d",
			normal.A, peeked.A, editRailAlpha, editRailPeekAlpha)
	}
	if peeked.A >= normal.A {
		t.Error("peeking did not make the rails MORE transparent — the whole gesture is 'let me see the " +
			"pure frame for a second'")
	}

	// The source half: no editor file CALLS the renderer's clip.
	//
	// Parsed, not grepped, and the difference is not fastidiousness: this very file's
	// prose names the forbidden call in order to explain why it is forbidden, and a
	// text census would read the warning as the violation. Only call expressions count.
	const clipTrap = "SetClipRect"
	for _, name := range editorSeamFiles(t) {
		for _, callee := range editorSeamCallees(t, name) {
			if callee != clipTrap {
				continue
			}
			t.Errorf("%s calls %s — the raw call clips DRAW only (clicks and hovers leak past the edge, "+
				"the v1.55.8 char-select class) AND it is the surface the Metal use-time trap lives on. "+
				"The kit's pushClip/popClip is the only sanctioned clip on this screen", name, clipTrap)
		}
	}
	// …and the screen DOES clip, through the kit, so the census is not vacuous. Asked
	// the same parsed way, for the same reason.
	clipped := false
	for _, callee := range editorSeamCallees(t, "themeeditor.go") {
		if callee == "pushClip" {
			clipped = true
		}
	}
	if !clipped {
		t.Fatal("the editor no longer clips its rails through the kit's stack — this census would pass " +
			"for a screen that had stopped clipping at all")
	}
}

// editorSeamFiles is the editor's own production sources. A helper rather than a
// literal at each census, so a file added to the screen joins every source gate at
// once — and it FAILS on an empty result, because a glob that stopped matching is
// the way a source census quietly retires itself.
func editorSeamFiles(t *testing.T) []string {
	t.Helper()
	all, err := filepath.Glob("themeeditor*.go")
	if err != nil {
		t.Fatalf("globbing the editor's sources: %v", err)
	}
	out := make([]string, 0, len(all))
	for _, name := range all {
		if !strings.HasSuffix(name, "_test.go") {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		t.Fatal("no themeeditor*.go production sources — every source census over the editor is vacuous")
	}
	return out
}

// editorSeamCallees names every function one source actually CALLS, by the callee's
// final identifier (so `c.Ren.SetClipRect(&r)` reports "SetClipRect").
//
// It exists so a "never call X" census cannot be tripped by the comment that explains
// why X is never called — the failure mode the first draft of the peek gate had, and
// the one that makes source censuses get deleted instead of fixed.
func editorSeamCallees(t *testing.T, name string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, nil, 0) // no ParseComments: prose is not code
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			out = append(out, fn.Name)
		case *ast.SelectorExpr:
			out = append(out, fn.Sel.Name)
		}
		return true
	})
	return out
}

// ---------------------------------------------------------------------------
// 4 — the one funnel, and a REAL apply
// ---------------------------------------------------------------------------

// TestEveryDocumentMutationGoesThroughTheApplyFunnel is the anti-second-driver
// census for the screen side of the seam.
//
// themeDoc.apply is the only writer (TestOnlyTheDocumentUsesTheFieldTables and
// TestOnlyTheDocumentWritesTheOverridesTier pin that from the document's side), but a
// writer with two DRIVERS is the same defect one level up: editorApply's three steps
// are inseparable — apply fills in the inverse, push makes it undoable, invalidate
// re-bakes the geometry the edit is expressed in — and a call site that did two of the
// three would produce an edit that either cannot be undone or cannot be seen.
//
// Mutations it answers to: dropping `a.invalidateThemeCanvases()` from editorApply (the
// edit lands in the model and the screen keeps drawing last frame's geometry, which
// reads as an editor that ignores you); dropping `a.te.ring.push` (every edit becomes
// permanent); calling doc.apply / doc.revert / doc.redo from anywhere else.
func TestEveryDocumentMutationGoesThroughTheApplyFunnel(t *testing.T) {
	const funnel = "func (a *App) editorApply(op themeEditOp) bool {"
	body := funcBodyInPackage(t, funnel)
	for _, step := range []struct{ call, why string }{
		{"a.te.doc.apply(op)", "the mutation itself, which is also what fills in the inverse"},
		{"a.te.ring.push(", "what makes the mutation undoable"},
		// v1.90.0 W10: the SPELLING changed, the step did not. editorApply now calls
		// editorInvalidateLive — which is invalidateThemeCanvases behind the live-preview
		// toggle, and the ONE gated spelling of it, so a future edit path cannot re-bake
		// past a frozen canvas by calling the raw function. The gate asks for the funnel's
		// own step by its current name; TestFrozenLivePreviewStillEditsTheDocument drives
		// the toggle's behaviour so the indirection cannot become a no-op unnoticed.
		{"a.editorInvalidateLive()", "what makes the mutation visible on the next frame"},
	} {
		if !strings.Contains(body, step.call) {
			t.Errorf("editorApply no longer calls %s — %s, and the three steps are inseparable",
				step.call, step.why)
		}
	}

	const owner = "themeeditor.go"
	drivers := []string{".doc.apply(", ".doc.revert(", ".doc.redo("}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") || name == owner {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, d := range drivers {
			if strings.Contains(string(src), d) {
				t.Errorf("%s drives the document directly (%q) — every mutation belongs in editorApply "+
					"/ editorUndo / editorRedo, which are the only three that keep the timeline and the "+
					"baked geometry in step with the model", name, d)
			}
		}
	}
	own, err := os.ReadFile(owner)
	if err != nil {
		t.Fatalf("reading %s: %v", owner, err)
	}
	for _, d := range drivers {
		if !strings.Contains(string(own), d) {
			t.Fatalf("the census no longer matches %s (%q is gone) — it would pass vacuously", owner, d)
		}
	}
}

// TestUnsavedEditsSurviveARealBackgroundThemeApply is the durability seam driven by
// the FRAME rather than by a hand-called reclaim.
//
// This is the defect the whole document layer was built for, and it is worth the
// expensive fixture: applies are NOT user-initiated. healTheme re-kicks one on any T1
// eviction of a theme texture, the texture-filter row kicks one, boot kicks two — and
// each one does `a.themeSidecar = res.sidecar`. Before the reclaim, a user could nudge
// a decoration into place, take their hand off the keyboard, and watch it jump back
// with nothing on screen to say why.
//
// Its sibling TestEditsSurviveAThemeReapply calls a.reclaimThemeDoc() itself, which
// means it stays green if the call is DELETED from pollThemeApply — and greener still
// if the call is merely MOVED above `a.themeSidecar = res.sidecar`, where it would be
// overwritten a line later and do nothing at all. Only a real apply, landed by
// App.Frame's own poll, can tell. That is exactly the gap the frame harness exists to
// close, and this is the editor's instance of it.
func TestUnsavedEditsSurviveARealBackgroundThemeApply(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	// A real theme on disk, written by the sidecar's own writer so the file that lands
	// is one the real reader accepts — a hand-typed fixture would be a second opinion
	// about the format.
	root := t.TempDir()
	dir := filepath.Join(root, theme.ThemesDirName, editorSeamThemeName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, theme.DesignFileName),
		[]byte("courtroom = 0, 0, 714, 579\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sc := theme.NewSidecar()
	el := editProbeElement()
	el.Hidden, el.Locked = false, false
	sc.Elements = append(sc.Elements, el)
	b, err := sc.Bytes()
	if err != nil {
		t.Fatalf("the fixture theme does not serialise: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, theme.SidecarFileName), b, 0o644); err != nil {
		t.Fatal(err)
	}

	a.d.Prefs.SetTheme(editorSeamThemeName, root)
	driveUntilThemeApplied(t, a)
	if a.themeSidecar == nil || len(a.themeSidecar.Elements) == 0 {
		t.Fatal("the fixture theme landed no elements — there is nothing for an apply to discard")
	}
	if !a.openThemeEditor(ScreenCourtroom) {
		t.Fatal("openThemeEditor refused the fixture theme")
	}
	driveFrame(a)

	a.te.selectOnly(elementTarget(0))
	authored := a.themeSidecar.Elements[0].Rect
	a.editorNudgeSelection(1, 0, false)
	edited := a.themeSidecar.Elements[0].Rect
	if edited == authored {
		t.Fatal("the nudge changed nothing; every assertion below would pass vacuously")
	}

	// The background apply — the shape a T1 eviction heal takes — landed by App.Frame's
	// own pollThemeApply and by nothing this test calls.
	driveUntilThemeApplied(t, a)

	if a.te == nil {
		t.Fatal("the apply closed the editor")
	}
	if got := a.themeSidecar.Elements[0].Rect; got != edited {
		t.Fatalf("a real background theme apply discarded the unsaved edit (%+v, want %+v) — applies are "+
			"not user-initiated, so this is a user's work vanishing between keystrokes with nothing on "+
			"screen to explain it", got, edited)
	}
	if a.themeSidecar != a.te.doc.side {
		t.Error("the applied sidecar is no longer the document's — the editor is now authoring a model " +
			"nothing draws from")
	}
	// The GEOMETRY tier travels with it: the document must still be bound to the map the
	// frame resolves from, or the next [overrides] edit writes a row into a dead object
	// and moves no pixel. Go cannot compare two maps, so the question is asked the only
	// way it can be — a write to one must be visible through the other.
	if a.te.doc.live == nil {
		t.Fatal("the document lost its binding to the applied design map")
	}
	const probeKey = "asyncao_seam_probe"
	probeRect := theme.Rect{X: 1, Y: 2, W: 3, H: 4}
	a.themeRects[probeKey] = probeRect
	if got, ok := a.te.doc.live[probeKey]; !ok || got != probeRect {
		t.Error("the document is bound to a map the frame no longer resolves from — pollThemeApply " +
			"replaces a.themeRects wholesale, so a reclaim that re-installs the sidecar without " +
			"re-binding leaves every widget edit writing into a dead object")
	}
	delete(a.themeRects, probeKey)
}
