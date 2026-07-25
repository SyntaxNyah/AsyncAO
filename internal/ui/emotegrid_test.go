package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// #33 "Stretching the window stretches the emote buttons": the themed emote
// grid used to scale the button cell by the PER-AXIS themeLayoutCache scale,
// which is independent per axis in Stretch fit-mode, so a wide window squashed
// every button flat. AO2 scales emote_button_size by the single
// Options::themeScalingFactor() (text_file_functions.cpp:212-213), i.e. the
// cell always keeps its designed aspect. These tests pin that, plus the two
// invariants a grid must never break: cells stay inside the rect and never
// overlap, and the hit-test rect IS the drawn rect (both come from cellRect).

// aspectSkew is the cross-multiplied aspect error of a cell against its design.
// Exact equality is impossible after int truncation: cellW = floor(s*dw) and
// cellH = floor(s*dh) each lose < 1 px, which bounds
// cellW*dh - cellH*dw to (-dh, +dw]. Anything outside that is real distortion.
func aspectSkew(cellW, cellH int32, designW, designH int) int64 {
	return int64(cellW)*int64(designH) - int64(cellH)*int64(designW)
}

func aspectTolerance(designW, designH int) int64 {
	if designW > designH {
		return int64(designW)
	}
	return int64(designH)
}

// TestEmoteCellScaleIsUniform pins the fix itself: whatever the per-axis
// scales, the cell factor is one number, and it is the SMALLER axis (a cell
// grown by the looser axis would overflow the emotes rect on the tighter one).
func TestEmoteCellScaleIsUniform(t *testing.T) {
	cases := []struct{ sx, sy, want float64 }{
		{1, 1, 1},          // 1:1 window
		{3.2, 1.2, 1.2},    // ultrawide Stretch: height is the tight axis
		{0.6, 2.4, 0.6},    // tall/narrow Stretch: width is the tight axis
		{1.75, 1.75, 1.75}, // Letterbox/Crop/Custom: already uniform, unchanged
		{0.35, 0.35, 0.35}, // ...and when scaling DOWN
	}
	for _, tc := range cases {
		if got := emoteCellScale(tc.sx, tc.sy); got != tc.want {
			t.Fatalf("emoteCellScale(%g,%g) = %g, want %g", tc.sx, tc.sy, got, tc.want)
		}
	}
}

// TestEmoteGridCellKeepsDesignAspect sweeps window widths (including ones that
// divide neither the rect nor the cell evenly, plus extreme wide and narrow
// aspects) and asserts the cell aspect never departs from the theme's design.
func TestEmoteGridCellKeepsDesignAspect(t *testing.T) {
	const (
		designCourtW = 714 // AO2 stock courtroom_design.ini courtroom width
		designCourtH = 668 // ...and height
		winH         = 720 // fixed height: only the width (=> scaleX) sweeps
	)
	cells := [][2]int{
		{40, 40}, // AO2 stock emote_button_size
		{50, 40}, // deliberately non-square theme data
		{35, 61}, // tall, prime-ish: truncation-hostile
	}
	widths := []int32{640, 800, 1024, 1280, 1366, 1440, 1600, 1920, 2560, 3440, 3840, 5120}
	for _, cell := range cells {
		for _, w := range widths {
			sx := float64(w) / designCourtW
			sy := float64(winH) / designCourtH
			// The emotes rect is per-axis scaled (that part is intended: the
			// container stretches, the buttons must not).
			r := sdl.Rect{X: 0, Y: 0, W: int32(464 * sx), H: int32(224 * sy)}
			g := emoteGridLayout(r, cell, [2]int{1, 1}, emoteCellScale(sx, sy))
			skew := aspectSkew(g.cellW, g.cellH, cell[0], cell[1])
			if tol := aspectTolerance(cell[0], cell[1]); skew > tol || skew < -tol {
				t.Fatalf("cell %v at win %dx%d: got %dx%d, aspect skew %d exceeds ±%d",
					cell, w, winH, g.cellW, g.cellH, skew, tol)
			}
		}
	}
}

// TestEmoteGridCellSizeIndependentOfWidth: once the window is wider than the
// design aspect, widening it further must not change the button size at all —
// it may only add columns. That is the user-visible half of #33.
func TestEmoteGridCellSizeIndependentOfWidth(t *testing.T) {
	const designCell = 40
	const designCourtW = 714 // AO2 stock courtroom_design.ini courtroom width
	sy := 1.5                // height fixed => the uniform (tight) axis is constant
	var wantW, wantH, prevCols int32
	// Every width here is past designCourtW*sy, so sx > sy on every step and the
	// uniform factor is pinned to sy — exactly the ultrawide case from #33.
	for i, w := range []int32{1200, 1600, 2000, 2400, 3000, 3840} {
		sx := float64(w) / designCourtW
		r := sdl.Rect{X: 12, Y: 34, W: int32(float64(w) * 0.6), H: 300}
		g := emoteGridLayout(r, [2]int{designCell, designCell}, [2]int{1, 1}, emoteCellScale(sx, sy))
		if i == 0 {
			wantW, wantH = g.cellW, g.cellH
		}
		if g.cellW != wantW || g.cellH != wantH {
			t.Fatalf("width %d: cell %dx%d, want the width-independent %dx%d", w, g.cellW, g.cellH, wantW, wantH)
		}
		if g.cols < prevCols {
			t.Fatalf("width %d: cols dropped %d -> %d (grid must reflow, not shrink)", w, prevCols, g.cols)
		}
		prevCols = g.cols
	}
}

// TestEmoteGridCellsTileWithoutOverlap: every drawn/hit-tested cell of a full
// page stays inside the rect and touches no other cell. Sweeps rect sizes that
// do NOT divide evenly by cell+gap.
func TestEmoteGridCellsTileWithoutOverlap(t *testing.T) {
	rects := []sdl.Rect{
		{X: 0, Y: 0, W: 464, H: 224},     // design-size
		{X: 17, Y: 29, W: 401, H: 187},   // prime-ish, offset origin
		{X: 5, Y: 5, W: 1997, H: 61},     // extreme wide, one row
		{X: 5, Y: 5, W: 47, H: 1201},     // extreme narrow, one column
		{X: 0, Y: 0, W: 1, H: 1},         // degenerate rect: still ≥ 1 cell
		{X: -30, Y: -12, W: 300, H: 300}, // negative origin (Crop overflow)
	}
	scales := []float64{0.25, 1, 1.37, 2.5}
	for _, r := range rects {
		for _, s := range scales {
			g := emoteGridLayout(r, [2]int{40, 40}, [2]int{1, 1}, s)
			if g.cols < 1 || g.rows < 1 || g.perPage() < 1 {
				t.Fatalf("rect %+v scale %g: empty grid %+v", r, s, g)
			}
			cells := make([]sdl.Rect, 0, g.perPage())
			for n := int32(0); n < int32(g.perPage()); n++ {
				cells = append(cells, g.cellRect(r, n))
			}
			for i, ci := range cells {
				if ci.W != g.cellW || ci.H != g.cellH {
					t.Fatalf("rect %+v scale %g cell %d: size %dx%d, want %dx%d", r, s, i, ci.W, ci.H, g.cellW, g.cellH)
				}
				// Inside the rect, unless the rect itself is smaller than one
				// cell (the ≥1 clamp deliberately wins there so the grid is
				// never empty — same as the classic path's cols<1 clamp).
				if g.cellW <= r.W && g.cellH <= r.H {
					if ci.X < r.X || ci.Y < r.Y || ci.X+ci.W > r.X+r.W || ci.Y+ci.H > r.Y+r.H {
						t.Fatalf("rect %+v scale %g cell %d %+v escapes the rect", r, s, i, ci)
					}
				}
				for j := i + 1; j < len(cells); j++ {
					cj := cells[j]
					if _, hit := ci.Intersect(&cj); hit {
						t.Fatalf("rect %+v scale %g: cells %d %+v and %d %+v overlap", r, s, i, ci, j, cj)
					}
				}
			}
		}
	}
}

// TestEmoteGridDegenerateMetrics: sloppy/absent emote_button_size (and a scale
// that shrinks a sane one below minEmoteCellPx) falls back to AO2's stock
// square instead of drawing invisible buttons.
func TestEmoteGridDegenerateMetrics(t *testing.T) {
	r := sdl.Rect{X: 0, Y: 0, W: 464, H: 224}
	for _, tc := range []struct {
		name  string
		cell  [2]int
		scale float64
	}{
		{"zero", [2]int{0, 0}, 1},
		{"three-px", [2]int{3, 3}, 1},
		{"scaled-to-nothing", [2]int{40, 40}, 0.05},
		{"one-axis-degenerate", [2]int{40, 2}, 1},
	} {
		g := emoteGridLayout(r, tc.cell, [2]int{1, 1}, tc.scale)
		if g.cellW != emoteBtnCell || g.cellH != emoteBtnCell {
			t.Fatalf("%s: cell %dx%d, want the %d×%d stock fallback", tc.name, g.cellW, g.cellH, emoteBtnCell, emoteBtnCell)
		}
	}
	// Just above the floor must NOT trip the fallback.
	g := emoteGridLayout(r, [2]int{int(minEmoteCellPx), int(minEmoteCellPx)}, [2]int{1, 1}, 1)
	if g.cellW != minEmoteCellPx || g.cellH != minEmoteCellPx {
		t.Fatalf("at the floor: cell %dx%d, want %d×%d", g.cellW, g.cellH, minEmoteCellPx, minEmoteCellPx)
	}
}

// TestEmoteGridLayoutZeroAlloc: the grid geometry runs every frame the themed
// courtroom draws — it must not allocate (hard rule: zero-allocation render).
func TestEmoteGridLayoutZeroAlloc(t *testing.T) {
	r := sdl.Rect{X: 0, Y: 0, W: 464, H: 224}
	if n := testing.AllocsPerRun(200, func() {
		g := emoteGridLayout(r, [2]int{40, 40}, [2]int{1, 1}, emoteCellScale(2.4, 1.1))
		_ = g.cellRect(r, 3)
	}); n != 0 {
		t.Fatalf("emoteGridLayout allocs = %v, want 0", n)
	}
}
