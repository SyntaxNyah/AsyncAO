package ui

import (
	"path/filepath"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestSnapViewportTo43 pins the editor's "4:3" toggle doing something the moment
// it's clicked (the playtest report was "the 4:3 button does nothing"): the
// stage's height re-derives from its width, the override persists, the change is
// undoable, and an already-4:3 stage is a no-op (no phantom undo entry).
func TestSnapViewportTo43(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}}
	const w, h = 1000, 800

	// A stretched stage: 400×150 at (100,100) → snapping makes it 400×300.
	a.regSlot(slotViewport, sdl.Rect{X: 100, Y: 100, W: 400, H: 150}, sdl.Rect{})
	a.snapViewportTo43(w, h)
	got := fracToRect(a.classicOv[slotViewport], w, h)
	if got.W != 400 || got.H != 300 {
		t.Fatalf("snapped stage = %+v, want 400×300", got)
	}
	if len(a.classicUndo) != 1 {
		t.Fatalf("snap must push exactly one undo entry, got %d", len(a.classicUndo))
	}

	// Already 4:3 → nothing changes, no extra undo entry.
	a.regSlot(slotViewport, got, sdl.Rect{})
	a.snapViewportTo43(w, h)
	if len(a.classicUndo) != 1 {
		t.Fatalf("a no-op snap must not push undo, got %d entries", len(a.classicUndo))
	}

	// A stage whose derived height would leave the window shrinks to fit instead.
	a.regSlot(slotViewport, sdl.Rect{X: 0, Y: 700, W: 800, H: 50}, sdl.Rect{})
	a.snapViewportTo43(w, h)
	got = fracToRect(a.classicOv[slotViewport], w, h)
	if got.Y+got.H > h {
		t.Fatalf("snapped stage %+v leaves the %dx%d window", got, w, h)
	}
	if got.W != got.H*4/3 {
		t.Fatalf("shrunk stage %+v is not 4:3", got)
	}
}
