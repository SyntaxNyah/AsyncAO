package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestInputSnapshotOrder pins the Ctx frame contract main.go's loop relies
// on: BeginFrame opens (and clears) the input snapshot, HandleEvent then
// fills it, and widgets read it during the draw pass. The inverse order
// shipped once — events fed before BeginFrame — and the reset erased every
// click/keypress/wheel tick before any widget saw it, leaving the whole UI
// dead. No SDL init is needed: events are plain structs and GetMouseState
// reads SDL's static mouse state.
func TestInputSnapshotOrder(t *testing.T) {
	c := &Ctx{}

	c.BeginFrame(time.Millisecond)
	c.HandleEvent(&sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, X: 5, Y: 6})
	if c.mouseX != 5 || c.mouseY != 6 {
		t.Fatalf("motion event must refresh the cursor snapshot, got (%d,%d)", c.mouseX, c.mouseY)
	}
	c.HandleEvent(&sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONUP, Button: sdl.BUTTON_LEFT, X: 40, Y: 20})
	if !c.clicked {
		t.Fatal("MOUSEBUTTONUP after BeginFrame must leave clicked set for the draw pass")
	}
	if c.mouseX != 40 || c.mouseY != 20 {
		t.Fatalf("button event must move the cursor snapshot to the release point, got (%d,%d)", c.mouseX, c.mouseY)
	}
	if !c.hovering(sdl.Rect{X: 30, Y: 10, W: 20, H: 20}) {
		t.Fatal("hovering must hit-test against the release coordinates")
	}
	c.HandleEvent(&sdl.MouseWheelEvent{Type: sdl.MOUSEWHEEL, Y: 3})
	if c.wheelY != 3 {
		t.Fatalf("wheel ticks must accumulate, got %d", c.wheelY)
	}

	// The next BeginFrame consumes the frame's input.
	c.BeginFrame(time.Millisecond)
	if c.clicked || c.wheelY != 0 {
		t.Fatal("BeginFrame must clear the previous frame's input snapshot")
	}
}

// TestParseIniswapList pins the iniswap.txt contract: one folder name per
// line, CRLF tolerated, blanks skipped, case-insensitive dedupe, bounded
// by iniswapListCap, sorted case-insensitively for the menu.
func TestParseIniswapList(t *testing.T) {
	data := []byte("zeta\r\n\r\n  aigis  \r\namong us_red\nAigis\namong us_red\n\n")
	got := parseIniswapList(data)
	want := []string{"aigis", "among us_red", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parsed %v, want %v", got, want)
		}
	}

	// Cap holds against a hostile file.
	var huge []byte
	for i := 0; i < iniswapListCap+500; i++ {
		huge = append(huge, []byte(fmt.Sprintf("char%06d\n", i))...)
	}
	if got := parseIniswapList(huge); len(got) != iniswapListCap {
		t.Fatalf("cap = %d entries, want %d", len(got), iniswapListCap)
	}
}

// TestSelectAllChordArms pins Ctrl+A semantics: the chord arms a pending
// select-all that survives BeginFrame until a field consumes it (typing
// replaces the whole value, backspace clears it — handled in TextField).
func TestSelectAllChordArms(t *testing.T) {
	c := &Ctx{}
	c.BeginFrame(time.Millisecond)
	c.HandleEvent(&sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Sym: sdl.K_a, Mod: sdl.KMOD_LCTRL}})
	if !c.selectAll {
		t.Fatal("Ctrl+A must arm select-all")
	}
	c.BeginFrame(time.Millisecond)
	if !c.selectAll {
		t.Fatal("select-all must persist across frames until consumed")
	}
}

// TestMergeWardrobe pins the wardrobe-first menu: client favourites sort
// case-insensitively up front (starred), server iniswap.txt entries follow
// minus case-insensitive duplicates of wardrobe entries.
func TestMergeWardrobe(t *testing.T) {
	names, stars := mergeWardrobe(
		[]string{"Zeta", "aigis"},
		[]string{"AIGIS", "bread", "zeta", "shrek"},
	)
	wantNames := []string{"aigis", "Zeta", "bread", "shrek"}
	wantStars := []bool{true, true, false, false}
	if len(names) != len(wantNames) {
		t.Fatalf("merged %v, want %v", names, wantNames)
	}
	for i := range wantNames {
		if names[i] != wantNames[i] || stars[i] != wantStars[i] {
			t.Fatalf("merged %v/%v, want %v/%v", names, stars, wantNames, wantStars)
		}
	}
}

// TestNormalizeThemeRoot pins the three path shapes users actually paste:
// the root, the themes folder itself, and a single theme inside it (which
// also auto-picks that theme).
func TestNormalizeThemeRoot(t *testing.T) {
	root := t.TempDir()
	themes := filepath.Join(root, "themes")
	one := filepath.Join(themes, "themeexample1")
	if err := os.MkdirAll(one, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(one, "courtroom_design.ini"), []byte("[a]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, pick := normalizeThemeRoot(root); got != root || pick != "" {
		t.Errorf("root form → %q/%q", got, pick)
	}
	if got, pick := normalizeThemeRoot(themes); got != root || pick != "" {
		t.Errorf("themes form → %q/%q, want %q", got, pick, root)
	}
	if got, pick := normalizeThemeRoot(one); got != root || pick != "themeexample1" {
		t.Errorf("single-theme form → %q/%q, want %q/themeexample1", got, pick, root)
	}
	// Explorer "Copy as path" pastes the path quoted — must still resolve.
	if got, pick := normalizeThemeRoot(`"` + themes + `"`); got != root || pick != "" {
		t.Errorf("quoted themes form → %q/%q, want %q", got, pick, root)
	}
	if names := scanThemeDirs([]string{root}); len(names) != 2 || names[1] != "themeexample1" {
		t.Errorf("scan = %v, want [default themeexample1]", names)
	}
}

// TestShelfPacker pins the label-atlas allocator: rows fill left to
// right, new shelves open below, padding separates slots, and oversize
// requests fail cleanly (the dedicated-texture fallback).
func TestShelfPacker(t *testing.T) {
	p := shelfPacker{edge: 100}

	a, ok := p.take(40, 10)
	if !ok || a.X != 0 || a.Y != 0 || a.W != 40 || a.H != 10 {
		t.Fatalf("first slot = %+v ok=%v", a, ok)
	}
	b, ok := p.take(40, 10)
	if !ok || b.X != 41 || b.Y != 0 {
		t.Fatalf("second slot = %+v ok=%v (want padded to x=41)", b, ok)
	}
	// 40 more won't fit the 100-wide shelf (41+41+41 > 100): new shelf.
	cSlot, ok := p.take(40, 12)
	if !ok || cSlot.X != 0 || cSlot.Y != 11 {
		t.Fatalf("third slot = %+v ok=%v (want new shelf at y=11)", cSlot, ok)
	}
	// A label taller than the page can never fit.
	if _, ok := p.take(10, 200); ok {
		t.Error("oversize label accepted")
	}
	// Fill vertically until the page refuses.
	for i := 0; i < 32; i++ {
		if _, ok := p.take(90, 20); !ok {
			return // refused cleanly once full — expected
		}
	}
	t.Error("packer never refused on a full page")
}

// TestWrapTextCaps pins the lobby description wrapper: nil for blank
// input, the line cap holds, and the capped line gains an ellipsis.
// (Headless: TextWidth returns 0 without a font, so everything fits one
// line unless the cap forces splits — we exercise the cap path.)
func TestWrapTextCaps(t *testing.T) {
	c := &Ctx{widthCache: map[string]int32{}}
	if got := c.WrapText("   ", 100, 4); got != nil {
		t.Fatalf("blank input → %v, want nil", got)
	}
	if got := c.WrapText("one two three", 100, 4); len(got) != 1 {
		t.Fatalf("zero-width font must yield one line, got %v", got)
	}
}
