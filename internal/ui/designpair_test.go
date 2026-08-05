package ui

// designPair / emote-grid spacing parity with AO2.
//
// AO2's get_button_spacing (AO2-Client text_file_functions.cpp:193-216) parses
// "x, y" with QString::toInt() and applies NO positivity test, so a spacing of
// 0 means "flush buttons" — a dozen shipped themes (the CC* family, AAI,
// GrayGarden, "alter ego (mobi)") declare exactly that. Treating 0 as "absent"
// substituted a gap the theme never asked for and walked the grid off the
// artwork painted behind it. These tests pin the accepted values, the ones that
// still fall back, and the consumer floor that keeps AO2's unguarded division
// from trapping here.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/veandco/go-sdl2/sdl"
)

// writeThemeDesign fabricates a theme dir holding only a courtroom_design.ini.
func writeThemeDesign(t *testing.T, design string) *theme.Theme {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, theme.ThemesDirName, "spacing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, theme.DesignFileName), []byte(design), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := theme.Load("spacing", "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func TestDesignPairAcceptsZero(t *testing.T) {
	const (
		defX = 7 // stand-in caller defaults, distinct from every value under test
		defY = 5
	)
	for _, tc := range []struct {
		name string
		raw  string // the courtroom_design.ini line, "" = key absent entirely
		want [2]int
		why  string
	}{
		{name: "flush", raw: "emote_button_spacing = 0, 0", want: [2]int{0, 0},
			why: "the CC*/AAI/GrayGarden case: 0 is a declared value, not a missing one"},
		{name: "one-axis-zero", raw: "emote_button_spacing = 0, 4", want: [2]int{0, 4},
			why: "CCSmol: flush horizontally, spaced vertically"},
		{name: "stock", raw: "emote_button_spacing = 9, 9", want: [2]int{9, 9},
			why: "AO2 default theme"},
		{name: "absent", raw: "", want: [2]int{defX, defY},
			why: "no key at all: AO2 would fall back to the default theme on disk; we have none"},
		{name: "one-component", raw: "emote_button_spacing = 8", want: [2]int{defX, defY},
			why: "AO2 bails when the split yields fewer than two components"},
		{name: "junk-y", raw: "emote_button_spacing = 8, 8SS", want: [2]int{8, defY},
			why: "HDF-Standard: the parseable component survives, the junk one takes the default"},
		{name: "junk-x", raw: "emote_button_spacing = SS8, 3", want: [2]int{defX, 3},
			why: "the fallback is per component, not all-or-nothing"},
		{name: "negative", raw: "emote_button_spacing = -4, -4", want: [2]int{-4, -4},
			why: "AO2 passes negatives straight through; the consumer floors them"},
		{name: "spaces", raw: "emote_button_spacing =   0 ,  0  ", want: [2]int{0, 0}},
		{name: "inline-comment", raw: "emote_button_spacing = 0, 0 ; flush", want: [2]int{0, 0},
			why: "the QSettings comment rule and the zero rule have to compose"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := writeThemeDesign(t, tc.raw+"\n")
			got := designPair(th, "emote_button_spacing", defX, defY)
			if got != tc.want {
				t.Fatalf("designPair(%q) = %v, want %v (%s)", tc.raw, got, tc.want, tc.why)
			}
		})
	}
}

// TestDesignPairSizeStillDefaults: emote_button_size shares designPair, and a
// theme declaring a 0-size button is degenerate rather than expressive. The
// value is still accepted here (AO2 accepts it too) — the grid's own
// minEmoteCellPx fallback is what keeps it drawable, which is the layer that
// can tell a cell from a gutter.
func TestDesignPairSizeStillDefaults(t *testing.T) {
	th := writeThemeDesign(t, "emote_button_size = 0, 0\n")
	if got := designPair(th, "emote_button_size", defaultEmoteCellPx, defaultEmoteCellPx); got != [2]int{0, 0} {
		t.Fatalf("designPair(emote_button_size 0,0) = %v, want the declared 0,0", got)
	}
	g := emoteGridLayout(sdl.Rect{W: 464, H: 224}, [2]int{0, 0}, [2]int{0, 0}, 1)
	if g.cellW != emoteBtnCell || g.cellH != emoteBtnCell {
		t.Fatalf("degenerate cell %dx%d, want the %d×%d stock fallback", g.cellW, g.cellH, emoteBtnCell, emoteBtnCell)
	}
	if g.cols < 1 || g.rows < 1 {
		t.Fatalf("degenerate grid %+v", g)
	}
}

// TestEmoteGridZeroSpacingIsFlush is the user-visible half of the fix: with
// emote_button_spacing = 0, 0 the grid pitch must equal the cell size exactly,
// so the buttons land on the slot frames the theme painted into its artwork.
func TestEmoteGridZeroSpacingIsFlush(t *testing.T) {
	r := sdl.Rect{X: 10, Y: 20, W: 464, H: 224}
	g := emoteGridLayout(r, [2]int{40, 40}, [2]int{0, 0}, 1)
	if g.gapX != 0 || g.gapY != 0 {
		t.Fatalf("gap = %d,%d, want 0,0 — a zero spacing must not become a gutter", g.gapX, g.gapY)
	}
	if g.cols != r.W/g.cellW || g.rows != r.H/g.cellH {
		t.Fatalf("grid %dx%d, want %dx%d whole cells", g.cols, g.rows, r.W/g.cellW, r.H/g.cellH)
	}
	// Adjacent cells touch and never overlap.
	a := g.cellRect(r, 0)
	b := g.cellRect(r, 1)
	if b.X != a.X+a.W {
		t.Fatalf("cell 1 starts at %d, want flush against cell 0 end %d", b.X, a.X+a.W)
	}
	if _, hit := a.Intersect(&b); hit {
		t.Fatalf("flush cells %+v and %+v overlap", a, b)
	}
	// ...and a spaced grid is still spaced (the fix must not flatten everything).
	sp := emoteGridLayout(r, [2]int{40, 40}, [2]int{9, 9}, 1)
	if sp.gapX != 9 || sp.gapY != 9 {
		t.Fatalf("stock gap = %d,%d, want 9,9", sp.gapX, sp.gapY)
	}
}

// TestEmoteGridNegativeSpacingFloored: AO2 divides by (spacing + button size)
// with no guard (emotes.cpp:70-71, charselect.cpp:160-161), so a theme shipping
// a spacing that cancels the cell divides by zero there. designPair now passes
// negatives through for AO2 parity, which makes this floor load-bearing.
func TestEmoteGridNegativeSpacingFloored(t *testing.T) {
	r := sdl.Rect{X: 0, Y: 0, W: 464, H: 224}
	for _, gap := range [][2]int{{-1, -1}, {-40, -40}, {-4096, -4096}, {-40, 9}} {
		g := emoteGridLayout(r, [2]int{40, 40}, gap, 1)
		if g.gapX < minEmoteGapPx || g.gapY < minEmoteGapPx {
			t.Fatalf("gap %v → %d,%d, want ≥ %d", gap, g.gapX, g.gapY, minEmoteGapPx)
		}
		if g.cellW+g.gapX <= 0 || g.cellH+g.gapY <= 0 {
			t.Fatalf("gap %v → pitch %d,%d: the column/row division would trap",
				gap, g.cellW+g.gapX, g.cellH+g.gapY)
		}
		if g.cols < 1 || g.rows < 1 || g.perPage() < 1 {
			t.Fatalf("gap %v → empty grid %+v", gap, g)
		}
	}
}
