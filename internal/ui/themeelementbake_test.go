package ui

// The free-element BAKE's gates (v1.90.0 W3).
//
// The draw path's gates live in themeelements_test.go and were written first,
// against a stub painter table, because that is what the wave plan asked for. These
// are the other half: what the bake must PRODUCE for those loops to be drawing the
// right thing — anchor deltas, skip-if-absent, pane reparenting, band order,
// condition interning — measured against sidecars a human wrote by hand under
// testdata/sidecars, before any UI exists that could author them.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// w3SidecarDir is where the hand-authored W3 fixtures live.
const w3SidecarDir = "testdata/sidecars"

// loadW3Sidecar parses one hand-authored fixture. A parse FAILURE is fatal here
// even though the client itself never refuses a sidecar (format rule 1): these
// files are ours, and one that stopped parsing would silently turn every test
// below into a test of the empty case.
func loadW3Sidecar(t testing.TB, name string) *theme.Sidecar {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(w3SidecarDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	sc, err := theme.ParseSidecar(src)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	if sc == nil {
		t.Fatalf("fixture %s parsed to a nil sidecar", name)
	}
	return sc
}

// stageThemedWithSidecar is stageThemedCourtroom plus an applied sidecar, with one
// frame drawn so the cold rebuild has baked it. The layout is invalidated BY HAND
// because a sidecar swap is not part of the cache key — in the live client the
// theme apply invalidates both canvases when it lands the sidecar (pollThemeApply),
// and this is that step.
func stageThemedWithSidecar(t testing.TB, name string) (*App, *themeLayoutCache, func()) {
	t.Helper()
	a, cleanup := stageThemedCourtroom(t)
	a.themeSidecar = loadW3Sidecar(t, name)
	a.themeLay.valid = false
	a.drawCourtroom(1280, 720)
	if !a.themeLay.valid || !a.toolboxThemeRectOn {
		cleanup()
		t.Fatal("the fixture did not reach the themed branch — nothing was baked")
	}
	return a, &a.themeLay, cleanup
}

// ---------------------------------------------------------------------------
// The zero case
// ---------------------------------------------------------------------------

// TestStockAO2ThemeGetsZeroElements is the property every install that never opens
// the theme editor depends on: an AO2 theme has no sidecar, so it declares no
// elements, so the three band loops are three empty loops.
//
// It asserts the BAND RANGES too, not just the count, because a count of zero with
// a stale non-empty range would still walk the array — and the array is not cleared
// between rebuilds, so those entries are a previous theme's.
func TestStockAO2ThemeGetsZeroElements(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	a.drawCourtroom(1280, 720)
	lay := &a.themeLay
	if a.themeSidecar != nil {
		t.Fatal("the stock fixture carries a sidecar — it is meant to be a plain AO2 theme")
	}
	if lay.elN != 0 {
		t.Fatalf("a theme with no sidecar baked %d elements, want 0", lay.elN)
	}
	if lay.condN != 0 {
		t.Fatalf("a theme with no sidecar interned %d conditions, want 0", lay.condN)
	}
	for b, rg := range lay.band {
		if rg.lo != 0 || rg.hi != 0 {
			t.Fatalf("band %d is %+v with no sidecar, want {0 0}", b, rg)
		}
	}
}

// TestSidecarSwapClearsThePreviousThemesElements pins the theme-swap prune for this
// tier: applying a theme with no sidecar after one with elements must leave nothing
// behind.
//
// themeLayoutIn's cold path DOES zero the baked array — `*lay = themeLayoutCache{…}`
// (theme_layout.go:274) replaces the whole struct, fixed array included — so the
// stale rows cannot survive a rebuild. The gate is still exactly as load-bearing:
// what it pins is that the SWAP reaches that rebuild at all and that elN and the
// three band ranges come back zero, which is the state the draw loop reads. Nothing
// here relies on the array's contents, so a future cache that recycled its rows
// (to skip re-baking unchanged elements, say) would keep passing for the right
// reason rather than by accident.
func TestSidecarSwapClearsThePreviousThemesElements(t *testing.T) {
	a, lay, cleanup := stageThemedWithSidecar(t, "w3_order.ini")
	defer cleanup()
	if lay.elN == 0 {
		t.Fatal("the ordering fixture baked nothing — the rest of this test proves nothing")
	}

	a.themeSidecar = nil
	a.themeLay.valid = false
	a.drawCourtroom(1280, 720)
	if lay.elN != 0 {
		t.Fatalf("after swapping to a theme with no sidecar, elN is %d — the previous theme's elements survived", lay.elN)
	}
	for b, rg := range lay.band {
		if rg.lo != 0 || rg.hi != 0 {
			t.Fatalf("band %d is %+v after the swap, want {0 0}", b, rg)
		}
	}
}

// TestElementsAreNotInThemeLayoutKeys is the AO2-parity gate stated as a namespace
// rule: an element id is NEVER a design key.
//
// That is what makes parity structural rather than careful. The census oracle over
// courtroom_design.ini's root keys (themecensus_test.go) walks themeSlots,
// themeSlotDeferred and charSelectDefaultRects; applyRectOverrides and
// themeKeyEditable walk a.themeRects. If an element id could reach any of them it
// would become a draggable, persistable, override-able box — and the whole "free
// elements cannot move an AO2 widget" contract would be a convention instead of a
// consequence.
func TestElementsAreNotInThemeLayoutKeys(t *testing.T) {
	a, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()
	if lay.elN == 0 {
		t.Fatal("the render fixture baked no elements — this gate would be vacuous")
	}
	sc := a.themeSidecar
	for i := range sc.Elements {
		id := sc.Elements[i].ID
		if id == "" {
			t.Fatalf("element %d has no id — the fixture is malformed", i)
		}
		if _, present := lay.r[id]; present {
			t.Errorf("element id %q reached the SCALED rect cache — elements must never enter the slot-key namespace", id)
		}
		if _, present := a.themeRects[id]; present {
			t.Errorf("element id %q reached a.themeRects — it would become a draggable, persistable box", id)
		}
		if themeSlotFor(id) != nil {
			t.Errorf("element id %q collides with a themeSlots row", id)
		}
		if themeKeyEditable(id) {
			t.Errorf("element id %q is editable as a design key", id)
		}
		for _, k := range themeLayoutKeys {
			if k == id {
				t.Errorf("element id %q is in themeLayoutKeys", id)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Geometry
// ---------------------------------------------------------------------------

// bakedByFill finds the baked element whose fill is exactly col. The baked row
// carries no id on purpose (identity is the INDEX — design §Q1), so the fixtures
// give every element a unique colour and the tests name them by it.
func bakedByFill(lay *themeLayoutCache, col sdl.Color) (*bakedElement, int) {
	for i := 0; i < lay.elN; i++ {
		if lay.el[i].col == col {
			return &lay.el[i], i
		}
	}
	return nil, -1
}

// TestAnchoredRectIsASignedDelta is design §6.6 R1, measured: an anchored element's
// rect is the ANCHOR'S box plus the rect, component by component, in DESIGN pixels.
//
// The delta scales with the canvas, which is the half that is easy to get wrong: a
// six-pixel inflate authored against a 714-wide canvas must still be six DESIGN
// pixels at 1280x720, or a frame that hugs its chatbox at one window size stands off
// it at another.
func TestAnchoredRectIsASignedDelta(t *testing.T) {
	_, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()

	box, ok := lay.rect("ao2_chatbox")
	if !ok {
		t.Fatal("the fixture layout has no ao2_chatbox — nothing to anchor to")
	}
	// chatframe = ao2_chatbox at -6, -6, +12, +12 design px.
	frame, _ := bakedByFill(lay, sdl.Color{R: 0, G: 0, B: 0, A: 0x80})
	if frame == nil {
		t.Fatal("the chatframe element did not bake")
	}
	dx := int32(-6 * lay.scaleX)
	dy := int32(-6 * lay.scaleY)
	dw := int32(12 * lay.scaleX)
	dh := int32(12 * lay.scaleY)
	want := sdl.Rect{X: box.X + dx, Y: box.Y + dy, W: box.W + dw, H: box.H + dh}
	if frame.r != want {
		t.Fatalf("anchored rect = %+v, want %+v (anchor %+v, delta -6,-6,12,12 at scale %.3f/%.3f) — "+
			"an anchored rect is a SIGNED DELTA on the anchor's box, not an absolute rect",
			frame.r, want, box, lay.scaleX, lay.scaleY)
	}
}

// TestElementWithAnAbsentAnchorIsInert is design §6.6 R10: an element whose anchor
// key no layout declares is INERT — no widget, no decoration — and is NOT an error.
//
// Skip-if-absent rather than "fall back to the canvas": a decoration placed
// relative to a widget that is not there has no defined position, and putting it
// somewhere anyway is how a theme ends up with a badge floating in the middle of
// the stage on exactly the layouts its author never tried.
func TestElementWithAnAbsentAnchorIsInert(t *testing.T) {
	_, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()

	// The ghost element is the only magenta in the fixture.
	if e, i := bakedByFill(lay, sdl.Color{R: 255, G: 0, B: 255, A: 255}); e != nil {
		t.Fatalf("the element anchored to a widget nothing declares baked at index %d, rect %+v — "+
			"an absent anchor must skip the element entirely", i, e.r)
	}
}

// TestFullCanvasElementSnapsToTheCanvas is design §6.6 R7: coverage is judged AFTER
// the uniform fit, against the ACTIVE canvas, and an element at or above
// elemFullCanvasPct IS the backdrop — so it is snapped flush rather than left a
// rounding error short of one edge.
func TestFullCanvasElementSnapsToTheCanvas(t *testing.T) {
	_, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()

	// The wallpaper is the only element whose stop A is #101018.
	wall, _ := bakedByFill(lay, sdl.Color{R: 0x10, G: 0x10, B: 0x18, A: 255})
	if wall == nil {
		t.Fatal("the wallpaper element did not bake")
	}
	if wall.r != lay.area {
		t.Fatalf("the full-canvas backdrop baked at %+v, canvas is %+v — a >= %d%% covering element "+
			"is snapped flush post-fit (§6.6 R7), or a seam of client colour shows down one edge",
			wall.r, lay.area, elemFullCanvasPct)
	}
}

// TestWindowSpaceIsNotTransformedByTheCanvas pins the one space AO2 has no
// analogue for: `space = window` is WINDOW pixels, outside the design canvas
// entirely. It is the AO2-full-parity rule ("extras go in chrome OUTSIDE the
// canvas") expressed as a coordinate space, so a canvas scale must not touch it.
func TestWindowSpaceIsNotTransformedByTheCanvas(t *testing.T) {
	_, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()

	tape, _ := bakedByFill(lay, sdl.Color{R: 0xc8, G: 0xa3, B: 0x4a, A: 0xcc})
	if tape == nil {
		t.Fatal("the window-space tape element did not bake")
	}
	want := sdl.Rect{X: 16, Y: 16, W: 120, H: 18}
	if tape.r != want {
		t.Fatalf("window-space rect baked at %+v, want %+v verbatim (canvas scale %.3f/%.3f, origin %+v) — "+
			"window space takes no scale and no canvas offset",
			tape.r, want, lay.scaleX, lay.scaleY, lay.area)
	}
}

// TestElementOpacityFoldsIntoTheBakedColours pins where opacity is applied: at
// BAKE, into every colour's alpha, so no painter multiplies anything per frame and
// a transparent fill stays transparent.
func TestElementOpacityFoldsIntoTheBakedColours(t *testing.T) {
	const half = 128
	got := fadeColor(theme.RGBA{R: 200, G: 100, B: 50, A: 200}, half)
	want := sdl.Color{R: 200, G: 100, B: 50, A: uint8(200 * half / theme.OpacityOpaque)}
	if got != want {
		t.Fatalf("fadeColor at opacity %d = %+v, want %+v", half, got, want)
	}
	if solid := fadeColor(theme.RGBA{R: 1, G: 2, B: 3, A: 4}, theme.OpacityOpaque); solid.A != 4 {
		t.Fatalf("an opaque element changed its colours' alpha: %+v", solid)
	}
	if clear := fadeColor(theme.RGBA{}, theme.OpacityOpaque); clear.A != 0 {
		t.Fatalf("a transparent fill gained alpha: %+v", clear)
	}
}

// ---------------------------------------------------------------------------
// Panes
// ---------------------------------------------------------------------------

// TestPaneReparentsExactlyOnce is the split-screen contract, and the failure it
// prevents is cumulative: a second reparent composes the pane's origin onto a rect
// that already carries it, so the widget walks further off screen at every rebuild
// — a window resize away from a bug nobody can reproduce from a screenshot.
//
// "Exactly once" is checked as an ARITHMETIC identity rather than a call count: the
// host lands at pane origin + its pane-relative override, and a second application
// could not produce that rect.
func TestPaneReparentsExactlyOnce(t *testing.T) {
	a, lay, cleanup := stageThemedWithSidecar(t, "w3_panes.ini")
	defer cleanup()

	pane, ok := a.paneRect(lay, &a.themeSidecar.Panes[0])
	if !ok {
		t.Fatal("the pane did not resolve — its space is missing from the fixture layout")
	}
	for _, tc := range []struct {
		key      string
		ovX, ovY int
	}{
		{key: "ic_chatlog", ovX: 0, ovY: 0},
		{key: "ms_chatlog", ovX: 0, ovY: 100},
	} {
		got, present := lay.r[tc.key]
		if !present {
			t.Fatalf("%s vanished from the layout — a reparent must MOVE a host, never drop it", tc.key)
		}
		wantX := pane.X + int32(float64(tc.ovX)*lay.scaleX)
		wantY := pane.Y + int32(float64(tc.ovY)*lay.scaleY)
		if got.X != wantX || got.Y != wantY {
			t.Errorf("%s reparented to (%d,%d), want (%d,%d) — pane origin %+v plus its PANE-RELATIVE "+
				"override (§6.6 R8). Twice the pane origin means the reparent ran twice.",
				tc.key, got.X, got.Y, wantX, wantY, pane)
		}
		if _, inside := got.Intersect(&pane); !inside {
			t.Errorf("%s landed at %+v, entirely outside its pane %+v", tc.key, got, pane)
		}
	}

	// Idempotence over rebuilds: the same theme, re-baked, must land in the same
	// place. This is the cumulative-drift failure stated directly.
	before := lay.r["ic_chatlog"]
	a.themeLay.valid = false
	a.drawCourtroom(1280, 720)
	if after := lay.r["ic_chatlog"]; after != before {
		t.Fatalf("a second bake moved the host from %+v to %+v — the reparent is compounding", before, after)
	}
}

// TestPaneHostKeepsExactlyOneDrawSite states the other half of §Q1 in the terms
// that made the alternative impossible: a pane changes WHERE a widget draws, never
// how many times. The registry is the oracle — a host is still one themeSlots row
// with one phase and one draw state after being re-homed.
func TestPaneHostKeepsExactlyOneDrawSite(t *testing.T) {
	a, _, cleanup := stageThemedWithSidecar(t, "w3_panes.ini")
	defer cleanup()

	p := &a.themeSidecar.Panes[0]
	for h := 0; h < p.NHosts; h++ {
		key := p.Hosts[h]
		slot := themeSlotFor(key)
		if slot == nil {
			t.Fatalf("pane host %q has no themeSlots row — a pane can only re-home a widget that exists", key)
		}
		if slot.state == slotStateInert {
			t.Errorf("pane host %q is inert: nothing paints there, so re-homing it moves nothing", key)
		}
	}
}

// TestPaneDoesNotRehomeAParentRelativeChild pins the guard that keeps the reparent
// arithmetic honest. showname/message ride the chatbox, music_name rides the plate
// and the evidence icons ride the stage: their entries in the scaled cache are
// PARENT-relative and their draw sites re-base them. Re-homing one would compose
// the pane's origin on top of a re-basing that still happens, and the widget would
// land at pane + parent. A child moves by moving its parent.
func TestPaneDoesNotRehomeAParentRelativeChild(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	sc := theme.NewSidecar()
	sc.Panes = []theme.Pane{{
		ID:     "side",
		Rect:   theme.Rect{X: 300, Y: 40, W: 200, H: 200},
		Space:  theme.SpaceCourtroom,
		Hosts:  [theme.PaneHostCap]string{"showname"},
		NHosts: 1,
	}}
	a.themeSidecar = sc
	a.themeLay.valid = false
	a.drawCourtroom(1280, 720)

	// showname's design rect is chatbox-relative and small; if it had been
	// re-homed it would now carry the pane's absolute origin.
	got, ok := a.themeLay.r["showname"]
	if !ok {
		t.Skip("this fixture layout does not place showname")
	}
	if got.X >= a.themeLay.area.X+int32(300*a.themeLay.scaleX) {
		t.Fatalf("showname baked at %+v — a chatbox child was re-homed into a pane, so its draw site "+
			"will add the chatbox origin on top of the pane's", got)
	}
}

// ---------------------------------------------------------------------------
// Order
// ---------------------------------------------------------------------------

// TestElementDrawOrderIsBakedNotSorted pins BOTH halves of the design's
// "bake, don't sort":
//
//  1. the ORDER is (band, then z, then declaration) — including the tie, which is
//     what an unstable sort silently gets wrong;
//  2. it is produced ONCE, at bake, and the draw path re-derives nothing — proved
//     by dispatching the bands and comparing what the painters received against the
//     baked array, index for index.
func TestElementDrawOrderIsBakedNotSorted(t *testing.T) {
	a, lay, cleanup := stageThemedWithSidecar(t, "w3_order.ini")
	defer cleanup()

	const want = 5
	if lay.elN != want {
		t.Fatalf("the ordering fixture baked %d elements, want %d", lay.elN, want)
	}
	// The fixture's greys are numbered in the order the elements must land in.
	for i := 0; i < lay.elN; i++ {
		grey := uint8(i + 1)
		if got := lay.el[i].col; got.R != grey || got.G != grey || got.B != grey {
			t.Fatalf("baked position %d holds fill %+v, want the #%02x%02x%02x element — order is "+
				"(band, then z, then DECLARATION order); an unstable sort breaks the z tie",
				i, got, grey, grey, grey)
		}
	}
	// Bands are contiguous and cover exactly [0, elN).
	//
	// EMPTY BANDS ARE SKIPPED, and that is not a loosening. bakeThemeElements leaves
	// a band nothing sorted into at its zero value {0, 0} (it only ever writes lo/hi
	// for a band it actually saw), which is correct — the draw loop runs `for i := 0;
	// i < 0` and paints nothing. Compared against `next` it would read as a band
	// starting back at 0, so a fixture whose middle band happened to be empty would
	// fail this assertion for a theme that renders perfectly. The property that
	// actually matters is that the NON-EMPTY bands tile [0, elN) in band order, and
	// that is what this now says.
	next := int16(0)
	for b, rg := range lay.band {
		if rg.lo == 0 && rg.hi == 0 {
			continue // no element sorted into this band
		}
		if rg.lo != next {
			t.Fatalf("band %d starts at %d, want %d — the band ranges must tile [0, elN) with no gap", b, rg.lo, next)
		}
		if rg.hi < rg.lo {
			t.Fatalf("band %d is inverted: %+v", b, rg)
		}
		next = rg.hi
	}
	if int(next) != lay.elN {
		t.Fatalf("the band ranges cover [0,%d), want [0,%d)", next, lay.elN)
	}

	// And the draw path hands the painters exactly that array, in that order.
	var seen []*bakedElement
	restore := captureElementPaints(&seen)
	a.drawCourtroom(1280, 720)
	restore()
	if len(seen) != lay.elN {
		t.Fatalf("the three band loops dispatched %d painters, want %d", len(seen), lay.elN)
	}
	for i, e := range seen {
		if got := indexOfBaked(lay, e); got != i {
			t.Fatalf("painter call %d received baked element %d — the draw path must walk the baked "+
				"array in place and never re-order it", i, got)
		}
	}
}

// ---------------------------------------------------------------------------
// visible_when
// ---------------------------------------------------------------------------

// TestVisibleWhenIsInternedAndGatesTheDraw pins the condition pipeline end to end:
// the bake interns each distinct value once, and the draw path gates on an integer
// compare against the index the frame resolved.
//
// The gate must be able to say NO. A condition that is always true is not a
// condition, and "the element drew" is not evidence that anything was evaluated —
// so this drives the live state both ways and demands the dispatch count move.
func TestVisibleWhenIsInternedAndGatesTheDraw(t *testing.T) {
	a, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()

	if lay.condN == 0 {
		t.Fatal("the render fixture interned no conditions — it declares pos: and shout: elements")
	}
	// Interning is by VALUE and deduplicated: no value may appear twice.
	for i := 0; i < lay.condN; i++ {
		for j := i + 1; j < lay.condN; j++ {
			if lay.condVal[i] == lay.condVal[j] {
				t.Fatalf("condition value %q interned twice (slots %d and %d)", lay.condVal[i], i, j)
			}
		}
		if lay.condVal[i] == "" {
			t.Fatalf("interned slot %d is empty — the value-less axes must intern nothing", i)
		}
	}

	count := func() int {
		var seen []*bakedElement
		restore := captureElementPaints(&seen)
		a.drawCourtroom(1280, 720)
		restore()
		return len(seen)
	}
	a.sidePref = "pro"
	base := count()
	a.sidePref = "def" // the fixture's defencebadge is gated on pos:def
	withDef := count()
	if withDef <= base {
		t.Fatalf("switching pos to def dispatched %d painters, was %d at pos:pro — a visible_when gate "+
			"that never changes the frame is not being evaluated", withDef, base)
	}
	a.sidePref = "pro"
	if again := count(); again != base {
		t.Fatalf("switching pos back dispatched %d painters, want %d — the per-axis memo is not "+
			"re-resolving on a change", again, base)
	}
}

// TestVisibleWhenSurvivesARebake is the generation guard: a cold rebuild re-interns
// every value, so an index the App memoised against the PREVIOUS table would point
// at the wrong string. Without the guard the symptom is a decoration that latches
// on (or off) after a window resize and never recovers.
func TestVisibleWhenSurvivesARebake(t *testing.T) {
	a, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()

	a.sidePref = "def"
	a.drawCourtroom(1280, 720)
	genBefore := lay.condGen
	liveBefore := a.condLive[theme.CondPos]
	if liveBefore < 0 {
		t.Fatalf("pos:def did not resolve to an interned index (condN=%d)", lay.condN)
	}

	a.themeLay.valid = false // a window resize, a fit change, a theme apply
	a.drawCourtroom(1280, 720)
	if lay.condGen == genBefore {
		t.Fatal("a rebuild did not bump the intern generation — the memo cannot know its indices are stale")
	}
	if a.condLive[theme.CondPos] < 0 {
		t.Fatal("pos:def stopped resolving after a rebuild — the memo did not re-resolve against the new table")
	}
	if lay.condVal[a.condLive[theme.CondPos]] != "def" {
		t.Fatalf("pos resolved to slot %d, which holds %q, want \"def\"",
			a.condLive[theme.CondPos], lay.condVal[a.condLive[theme.CondPos]])
	}
}

// TestUnknownConditionAxisLeavesTheElementVisible is the format's own rule
// (docs/THEME-FORMAT.md §3): "an unknown axis leaves the element visible — an
// element a future condition would have hidden is better shown than lost".
//
// It is checked against an axis OUT of the enum, which is exactly what a theme from
// a newer client produces, and it is the one case where the safe default is to
// paint rather than to skip.
func TestUnknownConditionAxisLeavesTheElementVisible(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()
	e := bakedElement{condAxis: theme.ConditionAxisCount + 3, cond: 7}
	if !a.elementVisible(&e) {
		t.Fatal("an element gated on an axis this build does not define was hidden — it must stay visible")
	}
}

// ---------------------------------------------------------------------------
// The bake's own cost
// ---------------------------------------------------------------------------

// elemBakeAllocCeil bounds how many objects ONE full bake may allocate, and it is
// deliberately a CONSTANT rather than a per-element budget: the bake writes fixed
// arrays and copies strings the sidecar already owns, so its allocation count must
// not be a function of how many elements a theme declares. A preset swap re-bakes,
// and a swap that allocated per element would hitch the frame it landed on.
//
// The headroom that is here covers label truncation (truncateLabelTo allocates when
// it actually shortens a string, which is the one legitimate allocator on this path)
// and the font-width memo behind it.
const elemBakeAllocCeil = 8

// TestElementBandBakeAllocationCeiling is BenchmarkElementBandBake's assertion half:
// the benchmark reports the cost, this fails when it grows.
func TestElementBandBakeAllocationCeiling(t *testing.T) {
	a, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()
	if lay.elN == 0 {
		t.Fatal("the render fixture baked nothing — the ceiling would be measuring an empty bake")
	}
	// Warm every string the bake touches (the truncation memo, the width memo) so
	// the measurement is of a STEADY bake — a preset swap on a running client, not
	// the first one after boot.
	for i := 0; i < 4; i++ {
		a.bakeThemeElements(lay)
	}
	n := testing.AllocsPerRun(50, func() { a.bakeThemeElements(lay) })
	if n > elemBakeAllocCeil {
		t.Fatalf("one bake of %d elements allocates %.1f objects, ceiling %d — the bake must be O(1) in "+
			"allocations, not O(elements): a preset swap re-bakes and must not hitch", lay.elN, n, elemBakeAllocCeil)
	}
	t.Logf("bake of %d elements: %.1f allocs/op", lay.elN, n)
}

// BenchmarkElementBandBake measures a full re-bake — what a preset swap costs, and
// what W9's two preset axes will pay on every pick. Reported with allocations, and
// gated by TestElementBandBakeAllocationCeiling above.
func BenchmarkElementBandBake(b *testing.B) {
	a, lay, cleanup := stageThemedWithSidecar(b, "w3_render.ini")
	defer cleanup()
	if lay.elN == 0 {
		b.Fatal("the render fixture baked nothing")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.bakeThemeElements(lay)
	}
	b.StopTimer()
	// Non-vacuity: a bake that stopped producing elements would benchmark nothing.
	if lay.elN == 0 {
		b.Fatal("the bake produced no elements during the run")
	}
	runtime.KeepAlive(a)
}

// ---------------------------------------------------------------------------
// Painters, exercised
// ---------------------------------------------------------------------------

// TestHandAuthoredSidecarRendersEveryW3Painter is the wave's ship criterion stated
// as a test: the hand-authored fixture reaches shape, gradient, text AND pane, on a
// real themed frame, through the real band loops.
//
// It counts dispatches per KIND rather than inspecting pixels: the software
// renderer these tests run on is not a pixel oracle, and what W3 owes is that the
// model resolves and dispatches — the pixels are what a human verifies on a screen.
func TestHandAuthoredSidecarRendersEveryW3Painter(t *testing.T) {
	a, _, cleanupA := stageThemedWithSidecar(t, "w3_render.ini")
	seen := map[theme.ElementKind]int{}
	collect := func(app *App) {
		var got []*bakedElement
		restore := captureElementPaints(&got)
		app.drawCourtroom(1280, 720)
		restore()
		for _, e := range got {
			seen[e.kind]++
		}
	}
	// pos:def so the conditional element is on screen too.
	a.sidePref = "def"
	collect(a)
	cleanupA()

	b, _, cleanupB := stageThemedWithSidecar(t, "w3_panes.ini")
	collect(b)
	cleanupB()

	for _, kind := range []theme.ElementKind{theme.ElemShape, theme.ElemGradient, theme.ElemText, theme.ElemPane} {
		if seen[kind] == 0 {
			t.Errorf("no %s element was dispatched by the hand-authored fixtures — W3 ships a painter for "+
				"it, so a fixture must exercise it", kind)
		}
	}
	if seen[theme.ElemImage] != 0 || seen[theme.ElemGen] != 0 {
		t.Errorf("a W4 kind (image/gen) was dispatched: %d image, %d gen — the fixtures must not depend on "+
			"the media tier this wave does not build", seen[theme.ElemImage], seen[theme.ElemGen])
	}
}

// TestEveryShapeNameResolves pins the silhouette name table total over the enum:
// the bake maps a wire name to an integer id once, and the draw path switches on
// the integer. A name that fell out of the table would degrade every theme using it
// to a plain box with no warning anywhere.
func TestEveryShapeNameResolves(t *testing.T) {
	for id := uint8(0); id < elemShapeCount; id++ {
		name := elemShapeNames[id]
		if name == "" {
			t.Fatalf("shape id %d has no wire name", id)
		}
		if got := elemShapeID(name); got != id {
			t.Errorf("shape %q resolved to id %d, want %d", name, got, id)
		}
	}
	if got, ok := elemShapeIDOK("no-such-silhouette"); got != elemShapeSharp || ok {
		t.Errorf("an unknown shape name resolved to (%d, %v), want (elemShapeSharp %d, false) — format rule 3 "+
			"says an unknown enum value degrades to the nearest safe primitive and keeps drawing, and the "+
			"resolver has to be able to SAY it degraded", got, ok, elemShapeSharp)
	}
	// The aliases resolve too, and to the id they name. An alias is append-only for
	// the same reason a wire name is: a theme authored against the build that
	// accepted it must not lose its silhouette on an upgrade.
	for _, al := range elemShapeAliases {
		got, ok := elemShapeIDOK(al.name)
		if !ok || got != al.id {
			t.Errorf("alias %q resolved to (%d, %v), want (%d, true)", al.name, got, ok, al.id)
		}
	}
	// Case and surrounding space are the author's business, not the format's.
	for _, spelling := range []string{"Rounded", "  pill  ", "SHARP"} {
		if _, ok := elemShapeIDOK(spelling); !ok {
			t.Errorf("shape %q did not resolve — `shape` is read as free text, so case and padding reach "+
				"the bake verbatim and must not degrade a file that says exactly what it means", spelling)
		}
	}
	// Every id must produce a sane inset for every band, at every size, including
	// the degenerate ones a clamped rect can hand it.
	for id := uint8(0); id < elemShapeCount; id++ {
		for _, dim := range [][2]int32{{1, 1}, {8, 4}, {200, 60}, {60, 200}} {
			for i := int32(0); i < 8; i++ {
				l, r := elemShapeInset(id, i, 8, dim[0], dim[1])
				if l < 0 || r < 0 {
					t.Fatalf("shape %d band %d of 8 at %dx%d gave insets (%d, %d) — a negative inset "+
						"widens the silhouette past its own box", id, i, dim[0], dim[1], l, r)
				}
				if l+r > dim[0] {
					t.Fatalf("shape %d band %d of 8 at %dx%d gave insets (%d, %d), which exceed the width",
						id, i, dim[0], dim[1], l, r)
				}
			}
		}
	}
}
