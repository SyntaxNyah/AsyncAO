package ui

// The free-element render model's gates (v1.90.0 W3).
//
// WRITTEN BEFORE THE PAINTERS, ON PURPOSE. The wave plan says so in as many
// words — "write the two zero-alloc gates FIRST, against a stub draw body,
// before any real drawing exists" (docs/wip/THEME-EDITOR-DESIGN.md §4 W3) —
// because this is the one wave that puts a NEW draw surface inside a frame that
// is gated at exactly zero allocations. A gate written afterwards is written
// against whatever shipped; a gate written first is a constraint every painter
// that follows has to satisfy.
//
// So these tests pass against a table of no-op painters, and they are expected to
// keep passing, unchanged, as each real painter lands. If one of them has to be
// loosened to make a painter fit, the painter is wrong.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

// seedElemW / seedElemH / seedElemGap lay the seeded elements out as a grid
// inside the design canvas: small enough that 96 of them fit the stock 714x579
// canvas, big enough that a real painter has something to fill.
const (
	seedElemW    = int32(24)
	seedElemH    = int32(16)
	seedElemGap  = int32(2)
	seedElemCols = 8
)

// seedElemLabel is the ElemText fixture string. ONE shared ASCII string for every
// seeded element, deliberately: distinct strings would each need their own text
// texture and the cache growth would read as an allocation for the first frames
// (settle absorbs one-offs, but 96 distinct rasters is not a settled frame). ASCII
// so it cannot latch the CJK/emoji font chain mid-gate, the same rule the
// whole-screen gates' IC lines follow.
const seedElemLabel = "AsyncAO"

// seedBakedElements fills the layout cache's baked array by hand, exactly as the
// cold-rebuild bake will.
//
// BY HAND, because the gates exist BEFORE the bake does — that is the whole point
// of writing them first. When the real bake lands these tests do not change: they
// seed the same array the bake writes, and the alloc contract they pin is a
// property of the DRAW path, not of how the array got filled.
//
// Elements are spread evenly over the three bands and cycle through every
// theme.ElementKind, so the gate covers every painter in the table rather than the
// one kind a fixture happened to pick.
func seedBakedElements(lay *themeLayoutCache, n int) {
	if n > len(lay.el) {
		n = len(lay.el)
	}
	per := n / int(theme.ElementBandCount)
	for i := 0; i < n; i++ {
		col := int32(i % seedElemCols)
		row := int32(i / seedElemCols)
		x := lay.area.X + col*(seedElemW+seedElemGap)
		y := lay.area.Y + row*(seedElemH+seedElemGap)
		lay.el[i] = bakedElement{
			r:     sdl.Rect{X: x, Y: y, W: seedElemW, H: seedElemH},
			kind:  theme.ElementKind(i % int(theme.ElementKindCount)),
			label: seedElemLabel,
			// Opaque colours on every slot so no painter can early-out on "nothing
			// to draw" and quietly stop measuring itself.
			col:      sdl.Color{R: 200, G: 40, B: 40, A: 255},
			col2:     sdl.Color{R: 40, G: 40, B: 200, A: 255},
			stroke:   sdl.Color{R: 255, G: 255, B: 255, A: 255},
			tint:     sdl.Color{R: 255, G: 255, B: 255, A: 255},
			strokePx: 1,
			opacity:  255,
			fit:      theme.FitStretch,
			cond:     condAlwaysVisible,
			condAxis: theme.CondAlways,
		}
	}
	lay.elN = n
	// Contiguous, ascending band ranges — the shape the bake produces after it
	// sorts by (band, z, declaration order). The last band absorbs the remainder so
	// the ranges always cover exactly [0, n).
	lo := int16(0)
	for b := 0; b < int(theme.ElementBandCount); b++ {
		hi := lo + int16(per)
		if b == int(theme.ElementBandCount)-1 {
			hi = int16(n)
		}
		lay.band[b] = elemBandRange{lo: lo, hi: hi}
		lo = hi
	}
}

// captureElementPaints swaps the painter table for recorders that log the element
// each call received, and returns the restore func.
//
// A test-local table swap rather than a counter inside the production painters:
// the shipped table must stay a plain array of top-level funcs with nothing in it
// for a test's benefit, and this way the recorder sees exactly what the band loop
// dispatched — including the ORDER, which is what proves all three hook positions
// fire and that the bands paint back-to-front.
func captureElementPaints(seen *[]*bakedElement) func() {
	saved := elemPainters
	for k := range elemPainters {
		elemPainters[k] = func(_ *App, e *bakedElement) { *seen = append(*seen, e) }
	}
	return func() { elemPainters = saved }
}

// indexOfBaked maps a painted element back to its slot in the baked array. A linear
// scan over 96 entries, in a test — the draw path never does this.
func indexOfBaked(lay *themeLayoutCache, e *bakedElement) int {
	for i := range lay.el {
		if &lay.el[i] == e {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Gate 1 — the 0-base-cost case
// ---------------------------------------------------------------------------

// TestDrawElementBandsZeroAllocWithNoElements is the gate that matters most,
// because it covers every install that never touches the theme editor: a theme
// with NO elements must add exactly nothing to a settled themed frame.
//
// It is the alloc half of the v1.89.0 "0 base = 0 cost" rule (the ns/op half is
// BenchmarkThemedFrameNoElements below). A stock AO2 theme leaves the baked array
// zeroed and every band range at {0, 0}, so the three band loops must not even
// reach a painter — which this also asserts directly, so the test cannot pass
// vacuously on a fixture that quietly acquired elements.
func TestDrawElementBandsZeroAllocWithNoElements(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	draw()
	if !a.themeLay.valid || !a.toolboxThemeRectOn {
		t.Fatal("the fixture did not reach the themed branch — the element bands never ran")
	}
	if a.themeLay.elN != 0 {
		t.Fatalf("the stock fixture baked %d elements, want 0 — this gate measures the NO-element case", a.themeLay.elN)
	}
	for b, rg := range a.themeLay.band {
		if rg.lo != 0 || rg.hi != 0 {
			t.Fatalf("band %d is %+v on a theme with no elements, want {0 0}", b, rg)
		}
	}
	// Nothing may be dispatched at all: three empty loops, no painter calls.
	var seen []*bakedElement
	restore := captureElementPaints(&seen)
	draw()
	restore()
	if len(seen) != 0 {
		t.Fatalf("a theme with no elements dispatched %d painter calls, want 0", len(seen))
	}

	settle(draw)
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled themed drawCourtroom with NO elements allocates %.1f/op, want 0 — "+
			"the element model is charging every install for a feature it does not use "+
			"(fix the alloc, don't loosen the gate)", n)
	}
}

// ---------------------------------------------------------------------------
// Gate 2 — the cap-full case
// ---------------------------------------------------------------------------

// TestDrawElementBandsZeroAllocWith96Elements is the same gate at theme.ElementCap:
// the densest theme the format allows, every kind represented, drawn on every
// frame, still allocating nothing.
//
// This is the constitution every future painter is bound by. A painter that
// formats a string, clones a slice, probes a map with a built key, takes &localRect
// into cgo (the classic themed-frame leak — see the cgoRect contract in ui.go) or
// reads prefs per element fails HERE, at 96x amplification, instead of shipping.
func TestDrawElementBandsZeroAllocWith96Elements(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	// One frame FIRST: themeLayoutIn rebuilds the whole cache (`*lay = themeLayoutCache{…}`)
	// on its cold path, which would wipe anything seeded before it.
	draw()
	if !a.themeLay.valid || !a.toolboxThemeRectOn {
		t.Fatal("the fixture did not reach the themed branch — the element bands never ran")
	}
	lay := &a.themeLay
	seedBakedElements(lay, theme.ElementCap)
	if lay.elN != theme.ElementCap {
		t.Fatalf("seeded %d elements, want %d", lay.elN, theme.ElementCap)
	}

	// Every seeded element is dispatched exactly once, in baked order, across all
	// THREE band positions. This is what proves the hooks are wired into
	// drawCourtroomThemed at all — an alloc gate over a loop that never runs would
	// pass forever.
	var seen []*bakedElement
	restore := captureElementPaints(&seen)
	draw()
	restore()
	if len(seen) != theme.ElementCap {
		t.Fatalf("the three band loops dispatched %d painter calls, want %d — "+
			"a band hook is missing from drawCourtroomThemed, or a band range is wrong",
			len(seen), theme.ElementCap)
	}
	for i, e := range seen {
		if got := indexOfBaked(lay, e); got != i {
			t.Fatalf("painter call %d received baked element %d — elements must paint in baked order "+
				"(band, then z, then declaration order), and the painter must be handed a POINTER into "+
				"the cache, never a copy", i, got)
		}
	}

	settle(draw)
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled themed drawCourtroom with %d elements allocates %.1f/op, want 0 — "+
			"a painter is allocating per frame (fix the painter, don't shrink the fixture)", theme.ElementCap, n)
	}
	// Non-vacuity: if anything invalidated the layout cache during the measured
	// frames, the array was zeroed and the run above measured the empty case.
	if lay.elN != theme.ElementCap {
		t.Fatalf("the layout cache was rebuilt mid-gate (elN=%d) — the measured frames drew fewer than %d elements",
			lay.elN, theme.ElementCap)
	}
}

// ---------------------------------------------------------------------------
// Gate 3 — the ns/op half of "0 base = 0 cost"
// ---------------------------------------------------------------------------

// BenchmarkThemedFrameNoElements is the v1.89.0 "0 base = 0 cost" rule applied to
// the element model: a theme that declares NO elements must pay for the feature in
// three empty loops and nothing else.
//
// BENCHMARKED, NOT ASSERTED — the same rule layered assets shipped under: a nil
// layer is one atomic load, and the way that was settled was by measuring it, not
// by claiming it. The target is "within noise of the pre-feature baseline", so the
// baseline is measured on this exact benchmark before the band loops exist and
// recorded in the wave's notes; a later run that drifts past noise means the empty
// case grew a cost that nobody meant to add.
func BenchmarkThemedFrameNoElements(b *testing.B) {
	a, cleanup := stageThemedCourtroom(b)
	defer cleanup()

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	// Prove the themed branch really ran: a silently-classic frame would measure a
	// screen the element model never touches.
	draw()
	if !a.themeLay.valid || !a.toolboxThemeRectOn {
		b.Fatal("the fixture did not reach the themed branch — the benchmark would measure the classic path")
	}
	if a.themeLay.elN != 0 {
		b.Fatalf("the fixture baked %d elements — this benchmark measures the NO-element case", a.themeLay.elN)
	}
	// Settle first (text atlas, width memos, the demand pump's negative caching) so
	// b.N measures steady-state frames rather than the app warming up.
	settle(draw)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.drawCourtroom(w, h)
	}
}

// ---------------------------------------------------------------------------
// Gate 4 — no string building anywhere on the element draw path
// ---------------------------------------------------------------------------

// elementDrawFiles are the sources the element draw path lives in. A source scan,
// not a runtime check, because the failure it prevents is a painter that allocates
// only for the theme that exercises it — a `fmt.Sprintf` on the ElemText path fires
// for a themed frame with text elements and for nobody else, so no fixture can be
// trusted to catch it.
//
// APPEND EVERY NEW PAINTER FILE HERE. The gate below fails if a listed file has
// vanished, so a rename can never silently drop the scan.
var elementDrawFiles = []string{"themeelements.go"}

// elementDrawColdFuncs are functions in those files that do NOT run on a frame —
// the bake, the editor's own helpers — and may therefore build strings freely.
// EMPTY today because themeelements.go is draw-path only; an entry here is a claim
// that a reviewer should check, so it carries its reason.
var elementDrawColdFuncs = map[string]string{}

// elementDrawBannedPkgs are the packages whose every useful call allocates. The
// draw path resolves text at BAKE time (bakedElement.label) exactly so it never
// needs one of these.
var elementDrawBannedPkgs = map[string]string{
	"fmt":     "formats and allocates — bake the string into bakedElement.label instead",
	"strconv": "allocates a new string — bake the number's text at rebuild time",
	"strings": "Builder/Join/Repeat/Replace all allocate — bake it",
	"bytes":   "Buffer allocates — bake it",
}

// TestElementDrawUsesNoStringBuilding scans the element draw path for the one class
// of allocation the two AllocsPerRun gates cannot be relied on to catch: work that
// only runs for a theme the fixture does not happen to declare.
//
// It matches the mechanism the package already uses for a source-level rule
// (TestUserTextSurfacesUseACoveringFace, usertextlabel_test.go): parse the package,
// walk the FuncDecls, fail on the call shape rather than on a measured symptom.
func TestElementDrawUsesNoStringBuilding(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range elementDrawFiles {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v — if the file was renamed, update elementDrawFiles so the scan keeps covering it", name, err)
		}
		funcs := 0
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Name == nil || fn.Body == nil {
				return true
			}
			if why, cold := elementDrawColdFuncs[fn.Name.Name]; cold {
				t.Logf("%s: skipped as a cold path (%s)", fn.Name.Name, why)
				return true
			}
			funcs++
			ast.Inspect(fn.Body, func(in ast.Node) bool {
				switch e := in.(type) {
				case *ast.CallExpr:
					sel, ok := e.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkg, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					if why, banned := elementDrawBannedPkgs[pkg.Name]; banned {
						t.Errorf("%s calls %s.%s at %s — %s. The element draw path is gated at zero "+
							"allocations; resolve text at bake time.",
							fn.Name.Name, pkg.Name, sel.Sel.Name, fset.Position(e.Pos()), why)
					}
				case *ast.BinaryExpr:
					if e.Op == token.ADD && (isStringLit(e.X) || isStringLit(e.Y)) {
						t.Errorf("%s concatenates strings at %s — that allocates on every frame it runs. "+
							"Bake the finished string into bakedElement.label.",
							fn.Name.Name, fset.Position(e.Pos()))
					}
				case *ast.AssignStmt:
					if e.Tok == token.ADD_ASSIGN {
						for _, rhs := range e.Rhs {
							if isStringLit(rhs) {
								t.Errorf("%s appends to a string at %s — that allocates on every frame it runs.",
									fn.Name.Name, fset.Position(e.Pos()))
							}
						}
					}
				}
				return true
			})
			return true
		})
		if funcs == 0 {
			t.Errorf("%s declares no draw-path functions — the scan is vacuous; "+
				"update elementDrawFiles/elementDrawColdFuncs to point at where the painters actually live", name)
		}
	}
}

// isStringLit reports a string literal operand — the cheap, false-negative-free
// half of "is this a string concatenation": a numeric `a + b` is fine, and a
// concatenation of two string VARIABLES is caught by the AllocsPerRun gates the
// moment it runs.
func isStringLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// ---------------------------------------------------------------------------
// Gate 5 — the painter table is total
// ---------------------------------------------------------------------------

// elemPainterPending names the kinds whose painter is still paintElementStub, with
// the wave that lands it. It is a CENSUS, not a permission list: the gate fails both
// ways, so a kind that gains a painter must lose its line here, and a kind that
// loses one cannot sneak back in.
//
// All six were pending at the moment the gates were written — that is what "write
// the gates first, against a stub draw body" means (design §4 W3). W3's own four
// painters (shape, gradient, text, pane) have since landed and left this census;
// the two that need the theme-media texture tier stay until W4 brings it.
var elemPainterPending = map[theme.ElementKind]string{
	theme.ElemImage: "W4 — needs theme://m/<themeid>/<id> media pages",
	theme.ElemGen:   "W4 — generator tiles (gentex.go)",
}

// TestEveryElementKindHasAPainter pins the table TOTAL over the enum.
//
// Totality is not a nicety here: drawElementBands indexes elemPainters and calls
// through it with no nil check, so a kind with no entry is a nil dereference on the
// render thread — triggered by a file a stranger wrote, the first time somebody
// shares a theme using a kind this build declared but never wired.
//
// It is the free-element twin of TestEveryHandDrawnKeyHasAPainter, which pins the
// same property for the AO2 slot registry.
func TestEveryElementKindHasAPainter(t *testing.T) {
	if len(elemPainters) != int(theme.ElementKindCount) {
		t.Fatalf("the painter table holds %d entries for %d kinds", len(elemPainters), theme.ElementKindCount)
	}
	stub := reflect.ValueOf(paintElementStub).Pointer()
	for k := theme.ElementKind(0); k < theme.ElementKindCount; k++ {
		p := elemPainters[k]
		if p == nil {
			t.Errorf("kind %q (%d) has NO painter — drawElementBands calls through this table without a nil "+
				"check, so this is a render-thread panic waiting for a theme that uses it. Point it at "+
				"paintElementStub at minimum.", k, k)
			continue
		}
		_, pending := elemPainterPending[k]
		isStub := reflect.ValueOf(p).Pointer() == stub
		switch {
		case isStub && !pending:
			t.Errorf("kind %q paints nothing (still paintElementStub) and is not listed in elemPainterPending — "+
				"either wire its painter or say out loud which wave does", k)
		case !isStub && pending:
			t.Errorf("kind %q has a real painter now — delete its elemPainterPending entry so the census "+
				"keeps telling the truth", k)
		}
	}
	for k := range elemPainterPending {
		if k >= theme.ElementKindCount {
			t.Errorf("elemPainterPending names kind %d, which is outside the enum (%d kinds)", k, theme.ElementKindCount)
		}
	}
	// The enum's own wire names must resolve, or the messages above name nothing.
	for k := theme.ElementKind(0); k < theme.ElementKindCount; k++ {
		if strings.TrimSpace(k.String()) == "" {
			t.Errorf("kind %d has no wire name", k)
		}
	}
}

// ---------------------------------------------------------------------------
// Gate 6 — the baked row stays small
// ---------------------------------------------------------------------------

// TestBakedElementStaysSmall pins the memory the model costs every install.
//
// The baked array is INLINE in themeLayoutCache, which is a field on App, so its
// size is paid once at startup by every user whether they own a themed install or
// not — and it is re-zeroed on every cold rebuild. bakedElementByteCeil is what
// keeps "a fixed array that never grows" an actual bound instead of a slogan (hard
// rule 4: everything has a named cap).
func TestBakedElementStaysSmall(t *testing.T) {
	got := unsafe.Sizeof(bakedElement{})
	if got > bakedElementByteCeil {
		t.Fatalf("bakedElement is %d bytes, ceiling %d — %d elements make that %d KiB inline in App. "+
			"Bake less per element (resolve to an index or a pointer), or raise the ceiling deliberately "+
			"with the new number written down.",
			got, bakedElementByteCeil, theme.ElementCap, uintptr(theme.ElementCap)*got/1024)
	}
	t.Logf("bakedElement = %d bytes; %d of them = %d bytes inline in themeLayoutCache",
		got, theme.ElementCap, uintptr(theme.ElementCap)*got)
}
