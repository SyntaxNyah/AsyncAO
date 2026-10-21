package ui

// THE EDITOR'S FOUR STRUCTURAL SEAMS, DRIVEN (v1.90.0 W7a).
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
// The kit is single-pass and the canvas hit-tests with raw pointIn (it must — the
// courtroom underneath is fenced, so hovering() answers false everywhere), which
// means nothing STOPS a press on an inspector button from also selecting whatever
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
// Mutations it answers to: editorPlate darkening/tinting instead of fading (the
// peek stops being a peek and the rails just change colour); any editor file
// reaching for the renderer's own clip.
func TestSpacePeekIsAnAlphaChangeNeverAClip(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()

	a.te.peek = false
	normal := a.te.editorPlate(ColPanel)
	a.te.peek = true
	peeked := a.te.editorPlate(ColPanel)

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

// TestUnsavedEditsSurviveARealBackgroundThemeApply is the durability seam driven by
// the FRAME rather than by a hand-called reclaim.
// Its sibling TestEditsSurviveAThemeReapply calls a.reclaimThemeDoc() itself, which
// means it stays green if the call is DELETED from pollThemeApply — and greener still
// if the call is merely MOVED above `a.themeSidecar = res.sidecar`, where it would be
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
