package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// A zero App is enough here: slotRect touches only classicOv + classicEdit.

func TestSlotRectOverrideAndDefault(t *testing.T) {
	var a App
	def := sdl.Rect{X: 10, Y: 20, W: 100, H: 80}
	const w, h = 1000, 500

	// No override → the exact computed default (the safety invariant).
	if got := a.slotRect(slotOOC, def, w, h); got != def {
		t.Fatalf("no-override slotRect = %+v, want default %+v", got, def)
	}

	// Override stored as window fractions → frac→px.
	a.classicOv = map[string][4]float64{
		slotOOC: {0.5, 0.25, 0.2, 0.4}, // x=500 y=125 w=200 h=200
	}
	want := sdl.Rect{X: 500, Y: 125, W: 200, H: 200}
	if got := a.slotRect(slotOOC, def, w, h); got != want {
		t.Fatalf("override slotRect = %+v, want %+v", got, want)
	}
}

// TestSlotNoAlloc pins the hard rule: the render path (no edit mode) allocates
// nothing, even with an override present (the worst case runs fracToRect).
func TestSlotNoAlloc(t *testing.T) {
	var a App
	def := sdl.Rect{X: 1, Y: 2, W: 300, H: 200}
	const w, h = 800, 600
	a.classicOv = map[string][4]float64{
		slotOOC:      {0.1, 0.1, 0.3, 0.3},
		slotViewport: {0.2, 0.2, 0.5, 0.5},
	}
	if n := testing.AllocsPerRun(200, func() {
		_ = a.slotRect(slotOOC, def, w, h)
		_ = a.slotRect(slotViewport, def, w, h)
	}); n != 0 {
		t.Fatalf("slotRect allocates %v/op on the render path; want 0", n)
	}
}

// TestControlsBlockOrigin pins the control-button block's safety invariant: with no
// override the block draws exactly where it always did (clusterX==pad, dy==0,
// clusterRight==w-pad), so icY = y2 - dy + btnH + 6 reduces to y2 + btnH + 6 and the
// un-edited courtroom is byte-identical. An override translates X/Y only; the constant
// content width keeps the wrap edge at clusterX + (w-2*pad).
func TestControlsBlockOrigin(t *testing.T) {
	const w, h, defY = 1000, 700, 480

	// No override → default origin, zero offset, default wrap edge.
	if cx, by, dy, cr := controlsBlockOrigin([4]float64{}, false, w, h, defY); cx != pad || by != defY || dy != 0 || cr != w-pad {
		t.Fatalf("no-override origin = (x=%d y=%d dy=%d right=%d), want (x=%d y=%d dy=0 right=%d)",
			cx, by, dy, cr, pad, defY, w-pad)
	}

	// Override at frac (0.1, 0.5, …) → translated origin; width/height ignored.
	ov := [4]float64{0.1, 0.5, 0.4, 0.2} // x=100 y=350 (W/H ignored by design)
	wantX, wantY := int32(100), int32(350)
	if cx, by, dy, cr := controlsBlockOrigin(ov, true, w, h, defY); cx != wantX || by != wantY || dy != wantY-defY || cr != wantX+(w-2*pad) {
		t.Fatalf("override origin = (x=%d y=%d dy=%d right=%d), want (x=%d y=%d dy=%d right=%d)",
			cx, by, dy, cr, wantX, wantY, wantY-defY, wantX+(w-2*pad))
	}
}

// TestControlsBlockOriginNoAlloc keeps the block-origin calc off the allocator: it
// runs every courtroom frame on the render path.
func TestControlsBlockOriginNoAlloc(t *testing.T) {
	const w, h, defY = 800, 600, 400
	ov := [4]float64{0.2, 0.3, 0.4, 0.1}
	if n := testing.AllocsPerRun(200, func() {
		_, _, _, _ = controlsBlockOrigin(ov, true, w, h, defY)
	}); n != 0 {
		t.Fatalf("controlsBlockOrigin allocates %v/op on the render path; want 0", n)
	}
}

func TestClassicEdgeAt(t *testing.T) {
	r := sdl.Rect{X: 100, Y: 100, W: 200, H: 120}
	const m = 12
	cases := []struct {
		name string
		x, y int32
		want uint8
	}{
		{"interior", 200, 160, 0},
		{"right edge", 300, 160, edgeR},
		{"left edge", 100, 160, edgeL},
		{"top edge", 200, 100, edgeT},
		{"bottom edge", 200, 220, edgeB},
		{"top-right corner", 300, 100, edgeR | edgeT},
		{"bottom-left corner", 100, 220, edgeL | edgeB},
		{"outside", 400, 160, 0},
	}
	for _, tc := range cases {
		if got := classicEdgeAt(tc.x, tc.y, r, m); got != tc.want {
			t.Errorf("%s: classicEdgeAt = %04b, want %04b", tc.name, got, tc.want)
		}
	}
}

// TestCloneClassicOvNoAlias pins the undo-history landmine: a snapshot must be an
// independent copy, so a later edit can't reach back and mutate it.
func TestCloneClassicOvNoAlias(t *testing.T) {
	orig := map[string][4]float64{"a": {1, 2, 3, 4}}
	cp := cloneClassicOv(orig)
	orig["a"] = [4]float64{9, 9, 9, 9}
	orig["b"] = [4]float64{5, 5, 5, 5}
	if cp["a"] != ([4]float64{1, 2, 3, 4}) {
		t.Fatal("clone aliased the original: a value mutation leaked into the snapshot")
	}
	if _, ok := cp["b"]; ok {
		t.Fatal("clone aliased the original: a new key leaked into the snapshot")
	}
	if cloneClassicOv(nil) != nil || cloneClassicOv(map[string][4]float64{}) != nil {
		t.Fatal("empty/nil must clone to nil (the no-overrides state)")
	}
}

// TestPushClassicUndoCap pins the bounded history (hard rule 4).
func TestPushClassicUndoCap(t *testing.T) {
	var a App
	for i := 0; i < layoutUndoCap+10; i++ {
		a.pushClassicUndo()
	}
	if len(a.classicUndo) != layoutUndoCap {
		t.Fatalf("undo stack = %d, want capped at %d", len(a.classicUndo), layoutUndoCap)
	}
}

// TestClassicEditUndoReSyncsPref drives two edits then two undos, asserting each undo
// restores BOTH the live overrides AND the durable pref (so undo survives a relog).
func TestClassicEditUndoReSyncsPref(t *testing.T) {
	a := testTabApp(t)
	undo := func() { // mirror the editor's Ctrl+Z
		a.classicRedo = append(a.classicRedo, cloneClassicOv(a.classicOv))
		snap := a.classicUndo[len(a.classicUndo)-1]
		a.classicUndo = a.classicUndo[:len(a.classicUndo)-1]
		a.restoreClassicOv(snap)
	}

	a.pushClassicUndo() // snapshot the empty state
	a.classicOv = map[string][4]float64{slotOOC: {0.1, 0.1, 0.2, 0.2}}
	a.d.Prefs.SetClassicSlot(slotOOC, a.classicOv[slotOOC])

	a.pushClassicUndo() // snapshot OOC-at-0.1
	a.classicOv[slotOOC] = [4]float64{0.5, 0.5, 0.2, 0.2}
	a.d.Prefs.SetClassicSlot(slotOOC, a.classicOv[slotOOC])

	undo() // back to OOC-at-0.1
	if got := a.classicOv[slotOOC]; got != ([4]float64{0.1, 0.1, 0.2, 0.2}) {
		t.Fatalf("after undo, live OOC = %v, want the 0.1 spot", got)
	}
	if got := a.d.Prefs.ClassicLayoutOverrides()[slotOOC]; got != ([4]float64{0.1, 0.1, 0.2, 0.2}) {
		t.Fatalf("after undo, durable OOC = %v, want the 0.1 spot (undo must re-sync the pref)", got)
	}

	undo() // back to empty
	if len(a.classicOv) != 0 {
		t.Fatalf("after undoing the first edit, live overrides = %v, want none", a.classicOv)
	}
	if len(a.d.Prefs.ClassicLayoutOverrides()) != 0 {
		t.Fatal("after undoing the first edit, the durable pref must be empty too")
	}
}

func TestFracRectRoundTrip(t *testing.T) {
	const w, h = 1600, 900
	r := sdl.Rect{X: 240, Y: 90, W: 800, H: 450}
	got := fracToRect(rectToFrac(r, w, h), w, h)
	if got != r {
		t.Fatalf("round-trip = %+v, want %+v", got, r)
	}
}
