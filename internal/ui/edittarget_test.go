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
func TestEditTargetIsOnlyBuiltByItsConstructors(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob the package sources: %v (%d files)", err, len(files))
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if filepath.Base(name) == "edittarget.go" {
			continue // the constructors' own home
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "editTarget" {
				t.Errorf("%s builds an editTarget by composite literal at %s — use classicTarget / "+
					"designTarget / elementTarget / noTarget, or the space becomes a default nobody chose.",
					name, fset.Position(lit.Pos()))
			}
			return true
		})
	}
}

// ---------------------------------------------------------------------------
// Eight handles on the design-rect path
// ---------------------------------------------------------------------------

// designHandleRect is a themed widget with room for the full handle set on both axes.
var designHandleRect = sdl.Rect{X: 100, Y: 100, W: 200, H: 200}

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
	var covered uint8
	for i, hnd := range classicHandles(designHandleRect) {
		mx, my := hnd.X+hnd.W/2, hnd.Y+hnd.H/2
		got := handleGripAt(designHandleRect, allowed, mx, my)
		if got != handleEdgeMask[i] {
			t.Errorf("handle %d at (%d,%d) grips %04b, want %04b", i, mx, my, got, handleEdgeMask[i])
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

	for i, mask := range handleEdgeMask {
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
