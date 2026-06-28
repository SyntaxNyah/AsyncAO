package ui

import "testing"

// TestSnapToEdges pins #21: a dragged window snaps to the screen edges and centre when within
// floatSnapPx, and is left alone when it's clearly away from them.
func TestSnapToEdges(t *testing.T) {
	const winW, winH = int32(1000), int32(800)
	const w, h = int32(100), int32(80)
	newWin := func(x, y int32) *floatWin {
		return &floatWin{x: x, y: y, lastW: w, lastH: h, lastWinW: winW, lastWinH: winH}
	}

	// Left / top edges.
	fw := newWin(floatWinMargin+3, floatWinMargin+2)
	fw.snapToEdges()
	if fw.x != floatWinMargin || fw.y != floatWinMargin {
		t.Errorf("left/top snap = (%d,%d), want (%d,%d)", fw.x, fw.y, floatWinMargin, floatWinMargin)
	}

	// Right / bottom edges (the window's far edge near the screen's far edge).
	fw = newWin(winW-floatWinMargin-w-4, winH-floatWinMargin-h-5)
	fw.snapToEdges()
	if fw.x != winW-floatWinMargin-w || fw.y != winH-floatWinMargin-h {
		t.Errorf("right/bottom snap = (%d,%d), want (%d,%d)", fw.x, fw.y, winW-floatWinMargin-w, winH-floatWinMargin-h)
	}

	// Centre.
	fw = newWin((winW-w)/2+5, (winH-h)/2-6)
	fw.snapToEdges()
	if fw.x != (winW-w)/2 || fw.y != (winH-h)/2 {
		t.Errorf("centre snap = (%d,%d), want (%d,%d)", fw.x, fw.y, (winW-w)/2, (winH-h)/2)
	}

	// Clearly away from any edge / centre → untouched.
	fw = newWin(300, 250)
	fw.snapToEdges()
	if fw.x != 300 || fw.y != 250 {
		t.Errorf("away-from-edges moved to (%d,%d), want (300,250)", fw.x, fw.y)
	}

	// No-op before rect() has stamped the dims (lastWinW/H == 0).
	fw = &floatWin{x: floatWinMargin + 1, y: floatWinMargin + 1}
	fw.snapToEdges()
	if fw.x != floatWinMargin+1 {
		t.Error("snap must be a no-op until rect() has run (lastWinW/H unset)")
	}
}
