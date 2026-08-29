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
	"image"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
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

// seedCondDecoys are interned condition values that match NOTHING live. They exist
// so lay.condVal is not a one-entry table: resolveConditionAxis walks it linearly,
// and a scan that finds its answer at index 0 would never exercise the walk.
var seedCondDecoys = [...]string{"__seed_decoy_a", "__seed_decoy_b"}

// seedGatedEvery is how often a seeded row carries a REAL condition rather than
// theme.CondAlways. Every third row puts elementVisible's integer-compare arm
// (CondPos) inside the gates while leaving the row visible, so the dispatch-count
// assertions still see all theme.ElementCap painters fire.
const seedGatedEvery = 3

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
// one kind a fixture happened to pick. They also cycle through every elemShape
// silhouette, every theme.TextAlign and both gradient modes, because each of those
// is a SEPARATE arm of a painter: elemShapeInset's five branches, paintElementText's
// width probe (which only runs off the left-aligned default) and paintElementRadial
// are all invisible to a fixture that leaves those fields at their zero values, and
// an allocation in one of them would ship un-measured.
//
// It takes the App so the condition table can be interned against the app's OWN live
// pos (a.mySide(), which is never empty — screens.go returns defaultSide). That is
// what puts refreshElementConditions' resolution pass inside the gates: with
// lay.condN == 0 the function returns after one field write and the whole per-axis
// memo — four string compares on a settled frame, a linear scan on the frame the
// session moves — never runs at all.
// seedElementArt uploads one small theme-media page and returns it, so the seeded
// ElemImage / ElemGen rows have real art to blit.
//
// WITHOUT IT THE ART PAINTER IS NOT MEASURED AT ALL. paintElementArt returns on the
// first line when page is nil, so a fixture of pageless elements would have run the
// two AllocsPerRun gates over an early return and reported zero for a painter that
// had never executed — which is how a deferred colour-mod restore (a heap-allocated
// closure per element per frame) would have shipped straight through both of them.
func seedElementArt(t testing.TB, a *App) *render.TexturePage {
	t.Helper()
	const side = 8
	d := &assets.Decoded{Width: side, Height: side}
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xFF
	}
	d.Frames = append(d.Frames, img)
	key := render.ThemeMediaKey("fixture", "art")
	if err := a.d.Store.UploadThemeMedia(key, d, config.TexBudgetDefaultMiB); err != nil {
		t.Fatalf("seeding theme media: %v", err)
	}
	page, ok := a.d.Store.Get(key)
	if !ok {
		t.Fatal("the seeded theme-media page did not land")
	}
	return page
}

func seedBakedElements(a *App, lay *themeLayoutCache, n int, art *render.TexturePage) {
	if n > len(lay.el) {
		n = len(lay.el)
	}
	// The decoys first, the live value last, so the memo's linear scan walks the
	// whole table before it matches.
	lay.condN = 0
	for _, decoy := range seedCondDecoys {
		lay.condVal[lay.condN] = decoy
		lay.condN++
	}
	livePos := int16(lay.condN)
	lay.condVal[lay.condN] = a.mySide()
	lay.condN++

	per := n / int(theme.ElementBandCount)
	for i := 0; i < n; i++ {
		col := int32(i % seedElemCols)
		row := int32(i / seedElemCols)
		x := lay.area.X + col*(seedElemW+seedElemGap)
		y := lay.area.Y + row*(seedElemH+seedElemGap)
		axis, cond := theme.CondAlways, condAlwaysVisible
		if i%seedGatedEvery == 0 {
			axis, cond = theme.CondPos, livePos
		}
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
			// Every fit mode in rotation, and real art behind the two kinds that use
			// one: stretch, tile, contain, cover and 9-slice are five different bodies
			// in paintElementArt, and a fixture that only ever stretched would leave
			// four of them — including the nine-blit slice path and the two clipped
			// ones — outside the gate entirely.
			fit:      theme.ElementFit(i % int(theme.FitCount)),
			slice:    [4]int32{2, 2, 2, 2},
			page:     art,
			shape:    uint8(i % int(elemShapeCount)),
			align:    uint8(i % int(theme.AlignCount)),
			grad:     uint8(i * theme.AngleCount / n), // sweeps all four cardinal quadrants
			radial:   i%2 == 1,
			cond:     cond,
			condAxis: axis,
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
// THE RESTORE IS REGISTERED WITH t.Cleanup, not merely returned. elemPainters is a
// package-level var and this swaps the WHOLE table: a t.Fatal (or a panic, or an
// early return) between the swap and an inline restore would leave every test that
// runs after it in this package drawing through no-op recorders — and every one of
// them would PASS, vacuously, against painters that do nothing. The returned func is
// still there for the sites that legitimately restore mid-test (a live-vs-export
// comparison captures twice); calling it twice is harmless because it only ever
// writes back the table that was live at capture time.
func captureElementPaints(t testing.TB, seen *[]*bakedElement) func() {
	t.Helper()
	saved := elemPainters
	for k := range elemPainters {
		elemPainters[k] = func(_ *App, e *bakedElement) { *seen = append(*seen, e) }
	}
	restore := func() { elemPainters = saved }
	t.Cleanup(restore)
	return restore
}

// captureElementPaintValues is captureElementPaints' twin, and the difference is the
// whole point: it records a COPY of the element as the painter received it, not the
// pointer.
//
// The pointer recorder cannot answer any question about the per-frame effect terms.
// drawElement applies them, calls the painter, and restores the baked row — so every
// post-paint read through a *bakedElement sees the RESTORED values, which is why the
// wash arm, the offset arm, the scale arm and the alpha arm of applyElementFX were
// all deletable with the suite green, and why the one gate that looked relevant had
// to recompute its own expected alpha three times instead of reading what the painter
// got. A value snapshot is the only honest oracle for "the terms reached the pixel".
//
// Two recorders rather than one wider one: the pointer form pins IDENTITY and ORDER
// (the alloc gates assert the painter is handed &lay.el[i] and never a copy), the
// value form pins CONTENT. Folding them into one struct would make every existing
// call site carry a field it does not use.
func captureElementPaintValues(t testing.TB, seen *[]bakedElement) func() {
	t.Helper()
	saved := elemPainters
	for k := range elemPainters {
		elemPainters[k] = func(_ *App, e *bakedElement) { *seen = append(*seen, *e) }
	}
	restore := func() { elemPainters = saved }
	t.Cleanup(restore)
	return restore
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
	restore := captureElementPaints(t, &seen)
	draw()
	restore()
	if len(seen) != 0 {
		t.Fatalf("a theme with no elements dispatched %d painter calls, want 0", len(seen))
	}

	if n := allocsPerFrame(allocGateFrames, 0, draw); n != 0 {
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
	seedBakedElements(a, lay, theme.ElementCap, seedElementArt(t, a))
	if lay.elN != theme.ElementCap {
		t.Fatalf("seeded %d elements, want %d", lay.elN, theme.ElementCap)
	}
	// Non-vacuity for the condition half: with condN == 0 refreshElementConditions
	// returns immediately and the whole per-axis memo would sit OUTSIDE this gate.
	if lay.condN == 0 {
		t.Fatal("the fixture interned no conditions — refreshElementConditions' resolution pass is not being measured")
	}

	// Every seeded element is dispatched exactly once, in baked order, across all
	// THREE band positions. This is what proves the hooks are wired into
	// drawCourtroomThemed at all — an alloc gate over a loop that never runs would
	// pass forever.
	var seen []*bakedElement
	restore := captureElementPaints(t, &seen)
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

	if n := allocsPerFrame(allocGateFrames, 0, draw); n != 0 {
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
	// b.N measures steady-state frames rather than the app warming up. The gates'
	// own helper does the settling; its reading is what THEY assert on, and here
	// only the warmed state it leaves behind matters.
	allocsPerFrame(allocGateFrames, 0, draw)

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
var elementDrawFiles = []string{"themeelements.go", "themeclock.go"}

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
// EMPTY since W4: every kind paints. ElemImage and ElemGen share paintElementArt,
// because after the bake they are the same thing — a resolved *render.TexturePage
// plus a fit, a tint and a rotation — and the only difference (where the page came
// from) was settled at bake time.
var elemPainterPending = map[theme.ElementKind]string{}

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
