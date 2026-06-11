package ui

import (
	"fmt"
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
