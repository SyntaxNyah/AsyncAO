package ui

import (
	"image"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestCycleField pins Tab/Shift+Tab focus order: draw order forward,
// reversed with shift, wrapping both ways, first field when none focused.
func TestCycleField(t *testing.T) {
	seq := []string{"ic", "ooc", "search"}
	cases := []struct {
		cur  string
		back bool
		want string
	}{
		{"ic", false, "ooc"},
		{"ooc", false, "search"},
		{"search", false, "ic"}, // wrap forward
		{"ooc", true, "ic"},
		{"ic", true, "search"}, // wrap backward
		{"", false, "ic"},      // nothing focused → first
		{"gone", false, "ic"},  // stale focus → first
	}
	for _, tc := range cases {
		if got := cycleField(seq, tc.cur, tc.back); got != tc.want {
			t.Errorf("cycleField(%q, back=%v) = %q, want %q", tc.cur, tc.back, got, tc.want)
		}
	}
}

// TestInkReadabilityGuard pins the theme ink-vs-skin contrast math: dark
// ink on a dark skin falls below the floor (dropped), white ink clears it.
func TestInkReadabilityGuard(t *testing.T) {
	skin := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for i := 0; i < len(skin.Pix); i += 4 {
		skin.Pix[i+0] = 24 // a dark chatbox
		skin.Pix[i+1] = 24
		skin.Pix[i+2] = 30
		skin.Pix[i+3] = 255
	}
	lum := avgSkinLuma(skin, lumaSampleStep)
	if lum < 18 || lum > 32 {
		t.Fatalf("avgSkinLuma = %d, want ≈24 for the flat dark skin", lum)
	}
	black := colLuma(sdl.Color{R: 16, G: 16, B: 16, A: 255})
	white := colLuma(sdl.Color{R: 235, G: 235, B: 235, A: 255})
	if absInt(black-lum) >= minInkSkinContrast {
		t.Error("near-black ink on a dark skin must fall below the contrast floor")
	}
	if absInt(white-lum) < minInkSkinContrast {
		t.Error("white ink on a dark skin must clear the contrast floor")
	}

	// Fully transparent skins read as the dark backdrop, never as black=0.
	clear := image.NewRGBA(image.Rect(0, 0, 8, 8))
	if got := avgSkinLuma(clear, 1); got != transparentSkinLuma {
		t.Errorf("transparent skin luma = %d, want %d", got, transparentSkinLuma)
	}
}

// TestApplyThemePaletteReadabilityFloor pins the kit-wide ink guard: a
// stylesheet whose text has no contrast against its own panels (playtest:
// GrayGarden = black on black settings) gets readable ink re-derived from
// the panel's lightness; switching back restores the stock palette.
func TestApplyThemePaletteReadabilityFloor(t *testing.T) {
	defer applyThemePalette(theme.Palette{}) // restore stock for other tests
	rgb := func(r, g, b uint8) *theme.RGB { return &theme.RGB{R: r, G: g, B: b} }

	// Black-on-black sheet → light ink wins.
	applyThemePalette(theme.Palette{Text: rgb(10, 10, 10), Panel: rgb(18, 18, 20)})
	if absInt(colLuma(ColText)-colLuma(ColPanel)) < minInkSkinContrast {
		t.Errorf("dark-on-dark sheet kept unreadable ink: text %v panel %v", ColText, ColPanel)
	}

	// White-on-white sheet → dark ink wins.
	applyThemePalette(theme.Palette{Text: rgb(245, 245, 245), Panel: rgb(230, 230, 235)})
	if absInt(colLuma(ColText)-colLuma(ColPanel)) < minInkSkinContrast {
		t.Errorf("light-on-light sheet kept unreadable ink: text %v panel %v", ColText, ColPanel)
	}
	if colLuma(ColText) >= paletteLightPanelLuma {
		t.Error("light panels must take dark ink, not light")
	}

	// A readable sheet passes through untouched.
	applyThemePalette(theme.Palette{Text: rgb(220, 230, 240), Panel: rgb(27, 39, 53)})
	if (ColText != sdl.Color{R: 220, G: 230, B: 240, A: 255}) {
		t.Errorf("readable sheet text must apply verbatim, got %v", ColText)
	}

	// Empty palette restores stock exactly.
	applyThemePalette(theme.Palette{})
	if ColText != defaultKitColors[4] || ColPanel != defaultKitColors[1] {
		t.Error("empty palette must restore the stock kit colors")
	}
}

// TestICWrapped pins the IC log wrap cache: rows split to width, inherit
// their entry index (color source), and the cache returns the same backing
// until log/width/query move.
func TestICWrapped(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.logPct = DefaultScalePct // share the (nil) chrome font: 8 px/char path
	a.icLog = []icEntry{
		{text: strings.Repeat("word ", 40), color: 2},
		{text: "short", color: 0},
	}
	a.icLogSeq = 1

	rows := a.icWrapped(160) // 20 chars per row at the 8 px fallback
	if len(rows) < 3 {
		t.Fatalf("long entry must wrap to multiple rows, got %d", len(rows))
	}
	last := rows[len(rows)-1]
	if last.text != "short" || last.entry != 1 {
		t.Fatalf("tail row = %+v, want the second entry verbatim", last)
	}
	for _, r := range rows[:len(rows)-1] {
		if r.entry != 0 {
			t.Fatalf("wrapped row %+v must keep its source entry index", r)
		}
	}

	again := a.icWrapped(160)
	if &again[0] != &rows[0] || len(again) != len(rows) {
		t.Error("unchanged log/width/query must hit the cache (same backing)")
	}
	if narrower := a.icWrapped(80); len(narrower) <= len(rows) {
		t.Error("narrower width must produce more wrapped rows")
	}
}

// TestNextRandomEmote pins auto-random emote selection: with >1 emote it always
// returns a DIFFERENT, in-range index than the current one (so the sprite
// visibly changes every send); with 0 or 1 it can't vary and returns cur.
func TestNextRandomEmote(t *testing.T) {
	// Degenerate counts can't vary — return cur, never panic.
	for _, n := range []int{0, 1} {
		if got := nextRandomEmote(n, 0); got != 0 {
			t.Errorf("nextRandomEmote(%d, 0) = %d, want 0 (no variation possible)", n, got)
		}
	}
	// With many emotes, every roll differs from cur and stays in [0, n).
	const n = 10
	for cur := 0; cur < n; cur++ {
		for trial := 0; trial < 200; trial++ {
			got := nextRandomEmote(n, cur)
			if got == cur {
				t.Fatalf("nextRandomEmote(%d, %d) returned the same index", n, cur)
			}
			if got < 0 || got >= n {
				t.Fatalf("nextRandomEmote(%d, %d) = %d, out of range", n, cur, got)
			}
		}
	}
	// An out-of-range current selection (-1 = nothing picked) still yields a
	// valid index without panicking.
	for trial := 0; trial < 200; trial++ {
		if got := nextRandomEmote(n, -1); got < 0 || got >= n {
			t.Fatalf("nextRandomEmote(%d, -1) = %d, out of range", n, got)
		}
	}
}
