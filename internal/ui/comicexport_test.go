package ui

import (
	"image"
	"image/color"
	"testing"
)

// solidPanel builds a w×h RGBA filled with one colour — a synthetic comic panel for
// the layout test (no SDL capture needed).
func solidPanel(w, h int, c color.RGBA) *image.RGBA {
	p := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(p.Pix); i += 4 {
		p.Pix[i], p.Pix[i+1], p.Pix[i+2], p.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return p
}

// TestComposeComicPageLayout pins the storyboard grid: page dimensions derived from
// the panel count, and each panel composited into its OWN cell — panel i's unique
// colour must land at cell i's centre, proving row/column placement and that a later
// panel's border frame never overwrites an earlier panel.
func TestComposeComicPageLayout(t *testing.T) {
	const pw, ph, cols, gutter, margin, border = 10, 8, 3, 4, 5, 1
	// 5 panels → 3 cols × 2 rows; each a distinct opaque colour.
	want := []color.RGBA{
		{R: 200, A: 255}, {G: 200, A: 255}, {B: 200, A: 255},
		{R: 200, G: 200, A: 255}, {R: 200, B: 200, A: 255},
	}
	var panels []*image.RGBA
	for _, c := range want {
		panels = append(panels, solidPanel(pw, ph, c))
	}
	page := composeComicPage(panels, cols, pw, ph, gutter, margin, border)
	if page == nil {
		t.Fatal("composeComicPage returned nil for 5 panels")
	}
	rows := (len(panels) + cols - 1) / cols
	wantW := margin*2 + cols*pw + (cols-1)*gutter
	wantH := margin*2 + rows*ph + (rows-1)*gutter
	if b := page.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
		t.Fatalf("page size = %dx%d, want %dx%d", b.Dx(), b.Dy(), wantW, wantH)
	}
	for i, c := range want {
		col, row := i%cols, i/cols
		cx := margin + col*(pw+gutter) + pw/2
		cy := margin + row*(ph+gutter) + ph/2
		if got := page.RGBAAt(cx, cy); got != c {
			t.Errorf("panel %d centre (%d,%d) = %+v, want %+v", i, cx, cy, got, c)
		}
	}
	// The page background shows through the top-left corner (paper colour, no frame).
	if got := page.RGBAAt(0, 0); got != comicPageColor {
		t.Errorf("page corner = %+v, want page colour %+v", got, comicPageColor)
	}
}

// TestComposeComicPageEmpty: no panels → nil (nothing to write).
func TestComposeComicPageEmpty(t *testing.T) {
	if composeComicPage(nil, comicCols, comicPanelW, comicPanelH, comicGutter, comicMargin, comicBorder) != nil {
		t.Error("composeComicPage(nil) should be nil")
	}
}

// TestComposeComicPageSingleRow: fewer panels than cols collapses to one row exactly
// that wide (a 2-line scene isn't a 3-wide page with a hole).
func TestComposeComicPageSingleRow(t *testing.T) {
	panels := []*image.RGBA{
		solidPanel(6, 6, color.RGBA{A: 255}),
		solidPanel(6, 6, color.RGBA{A: 255}),
	}
	page := composeComicPage(panels, 4, 6, 6, 2, 3, 1) // cols=4 but only 2 panels
	wantW := 3*2 + 2*6 + (2-1)*2                       // margin*2 + 2 panels + 1 gutter
	wantH := 3*2 + 1*6                                 // a single row
	if b := page.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
		t.Fatalf("single-row page = %dx%d, want %dx%d", b.Dx(), b.Dy(), wantW, wantH)
	}
}
