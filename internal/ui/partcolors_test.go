package ui

// Per-part layout colours (v1.52.0): the pref hexes parse once into the draw
// cache; blank slots stay off (the chrome default draws); the per-frame read
// is allocation-free.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

func TestPartColorsCache(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetLayoutPartColor(partLog, "ff0080")
	a.d.Prefs.SetLayoutPartColor(partChatbox, "102030")
	a.d.Prefs.SetLayoutPartColor(partOOC, "zz-bad") // unparseable = off, never a panic
	a.refreshPartColors()

	if col, ok := a.partPanel(partLog); !ok || col != (sdl.Color{R: 0xff, G: 0, B: 0x80, A: 255}) {
		t.Fatalf("log tint = %v/%v, want ff0080/on", col, ok)
	}
	if _, ok := a.partPanel(partEmotes); ok {
		t.Fatal("unset part must stay off (the grid keeps no backing)")
	}
	if _, ok := a.partPanel(partOOC); ok {
		t.Fatal("an unparseable hex must read as off")
	}
	def := sdl.Color{R: 1, G: 2, B: 3, A: 255}
	if got := a.partPanelOr(partEmotes, def); got != def {
		t.Fatalf("fallback = %v, want the chrome default", got)
	}

	// Clearing returns the part to the default.
	a.d.Prefs.SetLayoutPartColor(partLog, "")
	a.refreshPartColors()
	if _, ok := a.partPanel(partLog); ok {
		t.Fatal("a cleared part must fall back to the chrome colour")
	}

	// The draw-site read runs every courtroom frame: keep it off the allocator.
	if n := testing.AllocsPerRun(200, func() {
		_ = a.partPanelOr(partLog, def)
		_, _ = a.partPanel(partChatbox)
	}); n != 0 {
		t.Fatalf("partPanel allocates %v/op on the render path; want 0", n)
	}
}
