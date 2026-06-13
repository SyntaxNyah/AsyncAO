package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestBuildColorSpans pins the courtroom-style-run → render-color-span
// resolution: default uses the message color, a palette index maps to that
// color, and rainbow expands into per-rune spans (pure; no SDL).
func TestBuildColorSpans(t *testing.T) {
	def := sdl.Color{R: 1, G: 2, B: 3, A: 255}

	got := buildColorSpans([]courtroom.StyleRun{
		{Len: 3, Color: courtroom.ColorDefault},
		{Len: 2, Color: 2},
	}, def)
	if len(got) != 2 {
		t.Fatalf("spans = %d, want 2", len(got))
	}
	if got[0].Len != 3 || got[0].Color != def {
		t.Errorf("default span = %+v, want Len 3 color %v", got[0], def)
	}
	if got[1].Len != 2 || got[1].Color != render.TextColor(2) {
		t.Errorf("palette span = %+v, want Len 2 color %v", got[1], render.TextColor(2))
	}

	// Rainbow expands into per-rune spans (each Len 1), covering every rune.
	rb := buildColorSpans([]courtroom.StyleRun{{Len: 4, Color: courtroom.ColorRainbow}}, def)
	if len(rb) != 4 {
		t.Fatalf("rainbow spans = %d, want 4 per-rune", len(rb))
	}
	total := 0
	for _, s := range rb {
		if s.Len != 1 {
			t.Errorf("rainbow span Len = %d, want 1", s.Len)
		}
		total += s.Len
	}
	if total != 4 {
		t.Errorf("rainbow covers %d runes, want 4", total)
	}
}

// TestSceneHasInlineColor: only non-default runs route to the styled raster.
func TestSceneHasInlineColor(t *testing.T) {
	if sceneHasInlineColor([]courtroom.StyleRun{{Len: 5, Color: courtroom.ColorDefault}}) {
		t.Error("a single default run must use the plain raster")
	}
	if sceneHasInlineColor(nil) {
		t.Error("no styles must use the plain raster")
	}
	if !sceneHasInlineColor([]courtroom.StyleRun{{Len: 2, Color: courtroom.ColorDefault}, {Len: 2, Color: 4}}) {
		t.Error("a palette run must route to the styled raster")
	}
	if !sceneHasInlineColor([]courtroom.StyleRun{{Len: 2, Color: courtroom.ColorRainbow}}) {
		t.Error("a rainbow run must route to the styled raster")
	}
}
