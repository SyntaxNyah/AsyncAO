package ui

// The shared edit target and the 8-handle design-rect resize (v1.90.0 W6).
//
// W6 is a behaviour-neutral refactor with exactly one capability added — the themed
// editor's other seven handles — so everything here pins the NEW surface: that a
// target cannot exist without saying which space it is in, that the design path now
// offers the full handle set (and still offers exactly the historical single grip
// where a box is too small for more), and that every one of those handles reaches the
// magnet. The old behaviour is pinned where it always was, by the tab-strip quartet
// and classiclayout_test.go, which this wave did not have to change.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// The space is a field, not a deduction
// ---------------------------------------------------------------------------

// TestEditTargetSpaceIsExplicitNotInferred is the design's emphatic requirement: a
// target says which space it is in, and it is impossible to build one that does not.
//
// The failure this forbids is not hypothetical arithmetic — it is a reader looking at
// {key: "", elem: 0} and having to GUESS whether that is "no selection" or "element
// zero". Both payloads have legitimate zero values, so the guess is unanswerable, and
// two readers would answer it differently.
func TestEditTargetSpaceIsExplicitNotInferred(t *testing.T) {
	// The zero value is unarmed and answers nothing for any space.
	var zero editTarget
	if zero.armed() {
		t.Error("the zero editTarget must be unarmed")
	}
	if zero.classicKey() != "" || zero.designKey() != "" {
		t.Errorf("the zero target answered a key: classic=%q design=%q", zero.classicKey(), zero.designKey())
	}
	if idx, ok := zero.elemIdx(); ok || idx != elemTargetNone {
		t.Errorf("the zero target answered element %d (ok=%v)", idx, ok)
	}
	if noTarget() != zero {
		t.Errorf("noTarget() = %+v, want the zero value %+v", noTarget(), zero)
	}

	// Each constructor answers for ITS space and for no other, so a caller that never
	// checks the space still cannot read one as the other.
	cls := classicTarget(slotOOC)
	if cls.classicKey() != slotOOC || cls.designKey() != "" {
		t.Errorf("classic target leaked across spaces: %+v", cls)
	}
	des := designTarget(themeTabBarKey)
	if des.designKey() != themeTabBarKey || des.classicKey() != "" {
		t.Errorf("design target leaked across spaces: %+v", des)
	}
	if _, ok := des.elemIdx(); ok {
		t.Error("a design target answered an element index")
	}
	// Element ZERO is the case an inferred space cannot represent at all.
	el := elementTarget(0)
	idx, ok := el.elemIdx()
	if !ok || idx != 0 {
		t.Errorf("elementTarget(0) = (%d, %v), want (0, true) — index 0 is a real element", idx, ok)
	}
	if el.designKey() != "" || el.classicKey() != "" {
		t.Errorf("element target answered a key: %+v", el)
	}
	if !el.armed() || !cls.armed() || !des.armed() {
		t.Error("a constructed target must be armed")
	}

	// And the constructor REFUSES every shape that would leave the space to a guess.
	for _, tc := range []struct {
		name  string
		space editSpace
		key   string
		elem  int
	}{
		{"no space at all", spaceNone, "", elemTargetNone},
		{"no space but a payload", spaceNone, slotOOC, elemTargetNone},
		{"a key space with no key", spaceDesign, "", elemTargetNone},
		{"a key space carrying an element", spaceClassic, slotOOC, 3},
		{"an element carrying a key", spaceElement, slotOOC, 3},
		{"an element with no index", spaceElement, "", elemTargetNone},
		{"a space this build does not define", editSpace(200), "", elemTargetNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("newEditTarget(%d, %q, %d) built a target instead of panicking",
						tc.space, tc.key, tc.elem)
				}
			}()
			_ = newEditTarget(tc.space, tc.key, tc.elem)
		})
	}
}

// TestEditTargetIsOnlyBuiltByItsConstructors is the other half of the same guarantee:
// the refusals above are worth nothing if any file can sidestep them with a composite
// literal. Every editTarget outside edittarget.go must come from one of the named
// constructors (or noTarget), so the space can never be defaulted in by accident.
//
// THE ELIDED-TYPE HOLE, which the first version of this matcher had. Go lets the
// element type be omitted inside an array, slice or map literal:
//
//	[]editTarget{{key: "x"}}                 // the inner literal's Type is nil
//	map[string]editTarget{"a": {elem: 0}}    // ditto
//	[2]editTarget{{}, {}}                    // ditto
//
// Every one of those builds a target with a defaulted space and NONE of them has an
// `editTarget` Ident to match on. So the matcher now resolves the element type of the
// enclosing ArrayType / MapType as well, which is the shape a table-driven caller
// would reach for first.
func TestEditTargetIsOnlyBuiltByItsConstructors(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob the package sources: %v (%d files)", err, len(files))
	}
	fset := token.NewFileSet()
	flagged := 0
	for _, name := range files {
		if filepath.Base(name) == "edittarget.go" {
			continue // the constructors' own home
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		// elemTypeIsEditTarget answers for the CONTAINER's element type, so an inner
		// literal that elided its own type is still attributed correctly.
		report := func(pos token.Pos, how string) {
			flagged++
			t.Errorf("%s builds an editTarget %s at %s — use classicTarget / designTarget / "+
				"elementTarget / noTarget, or the space becomes a default nobody chose.",
				name, how, fset.Position(pos))
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if isEditTargetType(lit.Type) {
				report(lit.Pos(), "by composite literal")
				return true
			}
			// A container OF targets: every element literal inside it is a target,
			// written with its type elided.
			elem, container := editTargetElemType(lit.Type)
			if !elem {
				return true
			}
			for _, e := range lit.Elts {
				inner := e
				if kv, isKV := e.(*ast.KeyValueExpr); isKV {
					inner = kv.Value
				}
				if il, isLit := inner.(*ast.CompositeLit); isLit && il.Type == nil {
					report(il.Pos(), "inside a "+container+" literal with the element type elided")
				}
			}
			return true
		})
	}
	if flagged > 0 {
		t.Logf("%d offending literal(s); the constructors are the only sanctioned path", flagged)
	}
}

// isEditTargetType reports whether an AST type expression names editTarget directly.
func isEditTargetType(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "editTarget"
}

// editTargetElemType reports whether e is an array/slice/map whose ELEMENT type is
// editTarget, and what kind of container it is (for the message).
func editTargetElemType(e ast.Expr) (bool, string) {
	switch t := e.(type) {
	case *ast.ArrayType: // covers []editTarget and [N]editTarget alike
		return isEditTargetType(t.Elt), "array/slice"
	case *ast.MapType:
		return isEditTargetType(t.Value), "map"
	}
	return false, ""
}

// TestTheEditTargetMatcherSeesElidedLiterals is the matcher's own gate: it feeds the
// three elided shapes to the same predicates the census uses and requires each to be
// recognised. Without it, widening the matcher is a change nobody can tell landed —
// the census passes either way, because the package contains no offending literal.
func TestTheEditTargetMatcherSeesElidedLiterals(t *testing.T) {
	const src = `package p
var a = []editTarget{{}}
var b = map[string]editTarget{"k": {}}
var c = [2]editTarget{{}, {}}
var d = editTarget{}
var e = []string{"not a target"}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "matcher.go", src, 0)
	if err != nil {
		t.Fatalf("parse the probe: %v", err)
	}
	direct, elided, ignored := 0, 0, 0
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if isEditTargetType(lit.Type) {
			direct++
			return true
		}
		if ok, _ := editTargetElemType(lit.Type); ok {
			for _, el := range lit.Elts {
				inner := el
				if kv, isKV := el.(*ast.KeyValueExpr); isKV {
					inner = kv.Value
				}
				if il, isLit := inner.(*ast.CompositeLit); isLit && il.Type == nil {
					elided++
				}
			}
			return true
		}
		if lit.Type != nil {
			ignored++
		}
		return true
	})
	if direct != 1 {
		t.Errorf("the matcher saw %d direct editTarget literals, want 1", direct)
	}
	if elided != 4 {
		t.Errorf("the matcher saw %d elided element literals, want 4 (one slice, one map, two array) — "+
			"an elided literal is the shape a table-driven caller writes first", elided)
	}
	if ignored != 1 {
		t.Errorf("the matcher flagged %d unrelated literals, want exactly the one []string", ignored)
	}
}

// ---------------------------------------------------------------------------
// Eight handles on the design-rect path
// ---------------------------------------------------------------------------

// designHandleRect is a themed widget with room for the full handle set on both axes.
var designHandleRect = sdl.Rect{X: 100, Y: 100, W: 200, H: 200}

// wantHandleEdges is the LITERAL, per-position expectation for handleEdgeMask, in
// classicHandles' order: four corners (TL, TR, BL, BR), then the four edge midpoints
// (top, bottom, left, right).
//
// It exists because the gate below used to compare handleGripAt's answer against
// handleEdgeMask itself, which is a tautology: swap any two rows of the production
// table and every assertion still held, while the editor grew a top-left grip that
// resized from the bottom-right. A test that reads its expectation out of the thing
// under test is not measuring it. These eight values are the contract — corners grip
// two edges, midpoints grip one, and between them they cover each side exactly twice.
var wantHandleEdges = [handleCount]uint8{
	edgeL | edgeT, // 0 top-left
	edgeR | edgeT, // 1 top-right
	edgeL | edgeB, // 2 bottom-left
	edgeR | edgeB, // 3 bottom-right — handleBottomRight, the historical single grip
	edgeT,         // 4 top midpoint
	edgeB,         // 5 bottom midpoint
	edgeL,         // 6 left midpoint
	edgeR,         // 7 right midpoint
}

// TestHandleEdgeMaskIsTheDocumentedTable is the literal-table gate. It is separate
// from the behaviour gate below on purpose: this one says WHAT each position means,
// that one says the editor honours it, and only the pair together survive a mutation
// of the table.
func TestHandleEdgeMaskIsTheDocumentedTable(t *testing.T) {
	if handleEdgeMask != wantHandleEdges {
		t.Fatalf("handleEdgeMask = %04b, want %04b — classicHandles' order is corners "+
			"(TL, TR, BL, BR) then midpoints (top, bottom, left, right), and both editors' "+
			"grips, the magnet's mask and the affordance filter all read this table by INDEX",
			handleEdgeMask, wantHandleEdges)
	}
	// handleBottomRight must name the corner the cramped fallback keeps, or a small
	// widget's one grip resizes from a corner nobody is looking at.
	if handleEdgeMask[handleBottomRight] != (edgeR | edgeB) {
		t.Fatalf("handleBottomRight (%d) grips %04b, want edgeR|edgeB", handleBottomRight, handleEdgeMask[handleBottomRight])
	}
	// Each side is reachable from exactly two positions (one corner pair member and
	// one midpoint), which is what makes "resize from any side" true rather than
	// "resize from four corners that happen to add up".
	for _, e := range []uint8{edgeL, edgeR, edgeT, edgeB} {
		n := 0
		for _, m := range wantHandleEdges {
			if m&e != 0 {
				n++
			}
		}
		if n != 3 {
			t.Errorf("edge %04b is gripped by %d handles, want 3 (two corners + one midpoint)", e, n)
		}
	}
}

// TestCrampedBoxOffersNoLyingHandle pins handleGripMask's filter ORDER, which is the
// trap its own doc comment now names: the allowed-edge filter runs BEFORE the cramped
// fallback, so a target that does not honour edgeR|edgeB gets no grip at all once it
// is too small for the full set — rather than being offered a bottom-right corner
// whose drag would move an edge it refuses.
//
// No production call site reaches this combination today (both design spaces answer
// all-four-or-none), which is exactly why it needs a test: the next caller is where
// it would ship.
func TestCrampedBoxOffersNoLyingHandle(t *testing.T) {
	cramped := sdl.Rect{X: 10, Y: 10, W: editHandleRoomPx - 1, H: 24}
	// A width-only target — slotResizeEdges' shape for the classic control block.
	const widthOnly = edgeL | edgeR
	if m := handleGripMask(cramped, widthOnly); m != 0 {
		t.Fatalf("a cramped width-only box offered handles %08b, want none — the only handle the "+
			"cramped fallback keeps grips edgeR|edgeB, which this target does not honour, and an "+
			"affordance that moves an edge the target refuses is worse than no affordance", m)
	}
	if e := handleGripAt(cramped, widthOnly, cramped.X+cramped.W-1, cramped.Y+cramped.H-1); e != 0 {
		t.Errorf("a press on the cramped width-only box's corner gripped %04b, want 0 (a move)", e)
	}
	// Roomy, the same target: the two side midpoints and nothing else, so the rule is
	// "drop what lies", never "drop everything".
	roomy := sdl.Rect{X: 10, Y: 10, W: 200, H: 200}
	want := uint8(1<<6 | 1<<7) // left + right midpoints
	if m := handleGripMask(roomy, widthOnly); m != want {
		t.Errorf("a roomy width-only box offered %08b, want %08b (the side midpoints only)", m, want)
	}
}

// TestThemedEditorHasAllEightHandles closes the recon's PARTIAL at the machinery
// level: a theme's own widget offers the same four corners and four edge midpoints the
// classic editor has had since v1.52.0, each gripping exactly the edges its
// handleEdgeMask row names.
//
// It also pins the two rules that keep the generalization from taking anything away:
// a move-only key still offers nothing, and a box too small to hold the full set keeps
// exactly the bottom-right corner it had before this wave.
func TestThemedEditorHasAllEightHandles(t *testing.T) {
	const editable = "emotes" // a painted, non-fixed themeSlots row
	if !themeKeyEditable(editable) {
		t.Fatalf("fixture: %q must be an editable design key", editable)
	}
	allowed := resizeEdgesFor(designTarget(editable))
	if allowed != (edgeL | edgeR | edgeT | edgeB) {
		t.Fatalf("a theme's own widget honours %04b, want all four edges", allowed)
	}

	m := handleGripMask(designHandleRect, allowed)
	if want := uint8(1<<handleCount - 1); m != want {
		t.Fatalf("handleGripMask = %08b, want all %d handles (%08b)", m, handleCount, want)
	}
	// Every handle grips its own edges, and the eight between them cover all four
	// sides — the property that makes "resize from any side" true rather than "resize
	// from four corners that happen to add up".
	// Against the LITERAL table, never against handleEdgeMask itself: reading the
	// expectation out of the production table made this loop a tautology that survived
	// any permutation of it (see wantHandleEdges).
	var covered uint8
	for i, hnd := range classicHandles(designHandleRect) {
		mx, my := hnd.X+hnd.W/2, hnd.Y+hnd.H/2
		got := handleGripAt(designHandleRect, allowed, mx, my)
		if got != wantHandleEdges[i] {
			t.Errorf("handle %d at (%d,%d) grips %04b, want %04b", i, mx, my, got, wantHandleEdges[i])
		}
		covered |= got
	}
	if covered != (edgeL | edgeR | edgeT | edgeB) {
		t.Errorf("the eight handles between them grip %04b, want all four edges", covered)
	}
	// The interior is still a MOVE. A grip that swallowed the middle of the box would
	// be the regression this feature could most easily have shipped.
	if e := handleGripAt(designHandleRect, allowed, 200, 200); e != 0 {
		t.Errorf("the centre of the box grips %04b, want 0 (a move)", e)
	}

	// The server-tab strip: no authored size, so no grip anywhere — the same answer
	// the classic slot gives (slotResizeEdges(slotTabBar) == 0), which is what keeps
	// the two editors agreeing about one widget.
	if e := resizeEdgesFor(designTarget(themeTabBarKey)); e != 0 {
		t.Errorf("the server-tab strip honours %04b, want 0 (move-only)", e)
	}
	if m := handleGripMask(designHandleRect, resizeEdgesFor(designTarget(themeTabBarKey))); m != 0 {
		t.Errorf("the move-only strip offered handles %08b, want none", m)
	}

	// A box with no room for the full set keeps EXACTLY the historical grip: the
	// bottom-right corner, gripping edgeR|edgeB — bit-identical to the single corner
	// the themed editor offered before W6, so no small widget lost anything and none
	// became unmovable.
	cramped := sdl.Rect{X: 10, Y: 10, W: editHandleRoomPx - 1, H: 24}
	if m := handleGripMask(cramped, allowed); m != 1<<handleBottomRight {
		t.Fatalf("a cramped box offered handles %08b, want only the bottom-right corner (%08b)",
			m, uint8(1<<handleBottomRight))
	}
	grip := classicHandles(cramped)[handleBottomRight]
	if e := handleGripAt(cramped, allowed, grip.X+1, grip.Y+1); e != (edgeR | edgeB) {
		t.Errorf("the cramped box's corner grips %04b, want edgeR|edgeB", e)
	}
	if e := handleGripAt(cramped, allowed, cramped.X+cramped.W/2, cramped.Y+cramped.H/2); e != 0 {
		t.Errorf("the cramped box's centre grips %04b, want 0 — it must still be movable", e)
	}
}

// TestEveryHandleFeedsTheMagnet is the design's own sentence: "a handle that does not
// snap is a handle nobody can align with."
//
// The themed editor used to hand the magnet the constant edgeR|edgeB, which was the
// whole truth while the bottom-right corner was the only grip. Wiring the other seven
// without widening that mask would have shipped six handles that round to the grid and
// then align to nothing. This drives alignRect with each handle's real mask and
// requires the gripped edge to land flush on a sibling's.
func TestEveryHandleFeedsTheMagnet(t *testing.T) {
	// Two siblings, one on each side, three px outside the dragged box — inside
	// alignSnapPx (6) and far enough apart that neither can answer for the other.
	const (
		lowEdge  = int32(97)  // attracts a LEFT or TOP grip at 100
		highEdge = int32(303) // attracts a RIGHT or BOTTOM grip at 300
		extent   = int32(1000)
	)
	lowSib := sdl.Rect{X: lowEdge, Y: lowEdge, W: 500, H: 500}
	highSib := sdl.Rect{X: highEdge, Y: highEdge, W: 500, H: 500}

	for i, mask := range wantHandleEdges {
		r := designHandleRect
		others := make([]sdl.Rect, 0, 2)
		if mask&(edgeL|edgeT) != 0 {
			others = append(others, lowSib)
		}
		if mask&(edgeR|edgeB) != 0 {
			others = append(others, highSib)
		}
		got, guides := alignRect(r, others, extent, extent, false, mask, nil)
		if len(guides) == 0 {
			t.Errorf("handle %d (%04b): the magnet produced no guide — nothing snapped", i, mask)
		}
		if mask&edgeL != 0 && got.X != lowEdge {
			t.Errorf("handle %d (%04b): left edge landed at %d, want flush at %d", i, mask, got.X, lowEdge)
		}
		if mask&edgeR != 0 && got.X+got.W != highEdge {
			t.Errorf("handle %d (%04b): right edge landed at %d, want flush at %d", i, mask, got.X+got.W, highEdge)
		}
		if mask&edgeT != 0 && got.Y != lowEdge {
			t.Errorf("handle %d (%04b): top edge landed at %d, want flush at %d", i, mask, got.Y, lowEdge)
		}
		if mask&edgeB != 0 && got.Y+got.H != highEdge {
			t.Errorf("handle %d (%04b): bottom edge landed at %d, want flush at %d", i, mask, got.Y+got.H, highEdge)
		}
		// The anchored edge never moves: that is what "resize from this side" means.
		if mask&edgeL == 0 && mask&edgeR != 0 && got.X != r.X {
			t.Errorf("handle %d (%04b): a right-edge snap moved the left edge %d → %d", i, mask, r.X, got.X)
		}
		if mask&edgeT == 0 && mask&edgeB != 0 && got.Y != r.Y {
			t.Errorf("handle %d (%04b): a bottom-edge snap moved the top edge %d → %d", i, mask, r.Y, got.Y)
		}
	}
}

// ---------------------------------------------------------------------------
// End to end, through the real themed editor
// ---------------------------------------------------------------------------

// designResizeLayout is the stock canvas plus one widget big enough for the full
// handle set, and one small sibling parked so that its RIGHT edge (60+37 = 97) sits
// three design px — inside alignSnapPx — from the big widget's left edge.
func designResizeLayout() map[string]theme.Rect {
	return map[string]theme.Rect{
		"courtroom":  {X: 0, Y: 0, W: 714, H: 579},
		"viewport":   {X: 0, Y: 0, W: 714, H: 382},
		"ic_chatlog": {X: 60, Y: 100, W: 37, H: 50},
		"emotes":     {X: 100, Y: 400, W: 200, H: 120},
	}
}

// stageDesignResize is the themed editor over designResizeLayout at a window whose
// canvas scale is exactly 1, so a screen delta IS a design delta and a one-pixel
// assertion is a one-pixel assertion.
func stageDesignResize(t *testing.T) (*App, *Ctx, *themeLayoutCache, sdl.Rect, func()) {
	t.Helper()
	a, ctx, cleanup := stripEditFixture(t)
	applyThemeGeometryForTest(a, designResizeLayout())
	lay := a.themeWindowLayout(stripEditW, stripEditH)
	if lay.scaleX != 1 || lay.scaleY != 1 {
		cleanup()
		t.Fatalf("fixture: the canvas scale is %.4fx%.4f, want 1 — the deltas below assume it",
			lay.scaleX, lay.scaleY)
	}
	box, ok := lay.rect("emotes")
	if !ok {
		cleanup()
		t.Fatal("fixture: the editor must be handed a box for the widget under test")
	}
	return a, ctx, lay, box, cleanup
}

// TestThemedEditorResizesFromTheTopLeftHandle is the wave's one capability, driven
// through the real editor: press a handle that did not exist before W6 and the widget
// grows from THAT corner, with the opposite one nailed down.
//
// Before this wave the same press was a MOVE — there was no grip anywhere but the
// bottom-right corner — so a theme's widget could only ever be resized down and right.
func TestThemedEditorResizesFromTheTopLeftHandle(t *testing.T) {
	a, ctx, lay, box, cleanup := stageDesignResize(t)
	defer cleanup()

	hnd := classicHandles(box)[0] // top-left
	ctx.mouseX, ctx.mouseY = hnd.X+hnd.W/2, hnd.Y+hnd.H/2
	ctx.mouseDown, a.editPrev = true, false
	a.drawLayoutEditor(stripEditW, stripEditH, lay)
	if a.editDrag != 2 {
		t.Fatalf("a press on the top-left handle started drag=%d, want 2 (resize)", a.editDrag)
	}
	if a.editTgt.designKey() != "emotes" {
		t.Fatalf("the press grabbed %+v, want the design key emotes", a.editTgt)
	}
	if a.editEdges != (edgeL | edgeT) {
		t.Fatalf("the top-left handle armed edges %04b, want edgeL|edgeT — the mask the magnet is fed", a.editEdges)
	}

	const dx, dy = int32(20), int32(10)
	ctx.mouseX, ctx.mouseY = ctx.mouseX+dx, ctx.mouseY+dy
	a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))
	ctx.mouseDown = false
	a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))

	want := theme.Rect{X: 120, Y: 410, W: 180, H: 110}
	if got := a.themeRects["emotes"]; got != want {
		t.Fatalf("a (+%d,+%d) drag on the top-left handle gave %+v, want %+v (the far corner stays at 300,520)",
			dx, dy, got, want)
	}
	if _, ov := a.d.Prefs.ThemeRectOverrides(stripEditTheme)["emotes"]; !ov {
		t.Error("the release persisted nothing — a resize must survive like a move does")
	}
}

// TestThemedResizeHandleReachesTheMagnet is TestEveryHandleFeedsTheMagnet's other
// half: not that alignRect CAN snap a left edge, but that the editor actually hands it
// the gripped mask. With the pre-W6 constant (edgeR|edgeB) this drag rounds to the
// grid and then aligns to nothing, leaving the widget at x=100.
func TestThemedResizeHandleReachesTheMagnet(t *testing.T) {
	a, ctx, lay, box, cleanup := stageDesignResize(t)
	defer cleanup()
	a.layoutSnap = true // the shipped default; the magnet rides the same gate

	hnd := classicHandles(box)[6] // left edge midpoint: one axis, one gripped edge
	ctx.mouseX, ctx.mouseY = hnd.X+hnd.W/2, hnd.Y+hnd.H/2
	ctx.mouseDown, a.editPrev = true, false
	a.drawLayoutEditor(stripEditW, stripEditH, lay)
	if a.editDrag != 2 || a.editEdges != edgeL {
		t.Fatalf("a press on the left handle gave drag=%d edges=%04b, want 2 / edgeL", a.editDrag, a.editEdges)
	}

	ctx.mouseX++
	a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))
	ctx.mouseDown = false
	a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))

	// Flush with ic_chatlog's right edge, with the widget's own right edge unmoved.
	want := theme.Rect{X: 97, Y: 400, W: 203, H: 120}
	if got := a.themeRects["emotes"]; got != want {
		t.Fatalf("a snapped left-handle drag gave %+v, want %+v — the left edge must snap flush to ic_chatlog (x=97)",
			got, want)
	}
}

// ---------------------------------------------------------------------------
// The design-space resize arithmetic
// ---------------------------------------------------------------------------

// TestResizeDesignRectIsTheOldCornerGripPlusThreeSides pins the behaviour-neutrality
// claim directly: with the bottom-right mask the new per-edge arithmetic is the old
// `W += dx; H += dy`, and the three new arms move their own edge while the anchored
// one stays put.
func TestResizeDesignRectIsTheOldCornerGripPlusThreeSides(t *testing.T) {
	base := theme.Rect{X: 100, Y: 100, W: 200, H: 200}
	const dx, dy = 30, -20

	// The historical grip, spelled the way it used to be.
	old := base
	old.W += dx
	old.H += dy
	if got := resizeDesignRect(base, handleEdgeMask[handleBottomRight], dx, dy); got != old {
		t.Fatalf("the bottom-right grip resized %+v → %+v, want the pre-W6 %+v", base, got, old)
	}

	for _, tc := range []struct {
		name  string
		edges uint8
		want  theme.Rect
	}{
		{"left", edgeL, theme.Rect{X: 130, Y: 100, W: 170, H: 200}},
		{"right", edgeR, theme.Rect{X: 100, Y: 100, W: 230, H: 200}},
		{"top", edgeT, theme.Rect{X: 100, Y: 80, W: 200, H: 220}},
		{"bottom", edgeB, theme.Rect{X: 100, Y: 100, W: 200, H: 180}},
		{"top-left", edgeL | edgeT, theme.Rect{X: 130, Y: 80, W: 170, H: 220}},
	} {
		if got := resizeDesignRect(base, tc.edges, dx, dy); got != tc.want {
			t.Errorf("%s grip: %+v → %+v, want %+v", tc.name, base, got, tc.want)
		}
	}

	// The floor holds the ANCHORED edge still rather than inverting the box: a left
	// grip dragged past the minimum parks with its right edge where it was.
	got := resizeDesignRect(base, edgeL, 1000, 0)
	if got.W != layoutMinDesignPx || got.X+got.W != base.X+base.W {
		t.Errorf("a left grip past the floor gave %+v, want width %d with the right edge still at %d",
			got, layoutMinDesignPx, base.X+base.W)
	}
	got = resizeDesignRect(base, edgeT, 0, 1000)
	if got.H != layoutMinDesignPx || got.Y+got.H != base.Y+base.H {
		t.Errorf("a top grip past the floor gave %+v, want height %d with the bottom edge still at %d",
			got, layoutMinDesignPx, base.Y+base.H)
	}
}

// TestSnapDesignResizeLeavesTheUngrippedAxisAlone pins the grid half of the same
// claim. Snapping the SIZE (not the moving edge's coordinate) is what the single-grip
// path did, and an axis nobody gripped must come back untouched — before W6 both were
// snapped unconditionally, which was invisible only because the one grip gripped both.
func TestSnapDesignResizeLeavesTheUngrippedAxisAlone(t *testing.T) {
	r := theme.Rect{X: 101, Y: 403, W: 203, H: 91} // off-grid on every field
	if got := snapDesignResize(r, edgeR); got.H != r.H || got.Y != r.Y {
		t.Errorf("a right-edge snap touched the vertical axis: %+v → %+v", r, got)
	}
	if got := snapDesignResize(r, edgeR); got.W != snapDesign(r.W) || got.X != r.X {
		t.Errorf("a right-edge snap gave %+v, want width %d at the same origin", got, snapDesign(r.W))
	}
	// A left grip snaps the width too, and spends the difference on the origin so the
	// anchored (right) edge does not move.
	got := snapDesignResize(r, edgeL)
	if got.W != snapDesign(r.W) || got.X+got.W != r.X+r.W {
		t.Errorf("a left-edge snap gave %+v, want width %d with the right edge still at %d",
			got, snapDesign(r.W), r.X+r.W)
	}
}
