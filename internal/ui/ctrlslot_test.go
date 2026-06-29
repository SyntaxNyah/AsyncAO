package ui

import "testing"

// TestCtrlSlotCompaction pins the ctrlSlot HELPER's contract (not screens.go's
// own advance constants — those are a synthetic row here, verified against the
// real call sites by manual diff): with nothing hidden each button sits at the
// running cursor and the cursor steps by the exact per-button advance, so the
// row is pixel-identical to the old inline one; hiding a button removes it AND
// shifts every later button left by precisely that button's advance — the row
// compacts with no gap, no overlap.
func TestCtrlSlotCompaction(t *testing.T) {
	a := testTabApp(t)
	a.hidden = map[string]bool{}

	// A miniature of the real toolbar row: (key, width, advance) in draw order.
	// The advances mirror screens.go (restyle's is 84+gGap = 100).
	row := []struct {
		key      string
		wdt, adv int32
	}{
		{"ctrl.character", 100, 106},
		{"ctrl.wardrobe", 90, 96},
		{"ctrl.restyle", 84, 100},
		{"ctrl.background", 100, 106},
	}
	const startX, y2 int32 = 40, 8

	// run plays the row through ctrlSlot (no layout override, so slotRect is a
	// pass-through) and returns each VISIBLE button's drawn X plus the final cursor.
	run := func() (map[string]int32, int32) {
		x := startX
		at := map[string]int32{}
		for _, b := range row {
			if r, ok := a.ctrlSlot(&x, y2, b.wdt, b.adv, 1000, 800, b.key); ok {
				at[b.key] = r.X
			}
		}
		return at, x
	}

	// 1) Nothing hidden: every button is visible at the running sum, and the
	//    cursor ends past the last advance — identical to the inline row.
	at, end := run()
	if len(at) != len(row) {
		t.Fatalf("nothing hidden: %d visible, want all %d", len(at), len(row))
	}
	wantX := startX
	for _, b := range row {
		if at[b.key] != wantX {
			t.Errorf("%s drawn at X=%d, want %d", b.key, at[b.key], wantX)
		}
		wantX += b.adv
	}
	if end != wantX {
		t.Errorf("final cursor = %d, want %d", end, wantX)
	}

	// 2) Hide a MIDDLE button: it vanishes and every later button shifts LEFT by
	//    exactly that button's advance (96) — no gap, and the button before it
	//    is untouched.
	const wardrobeAdv int32 = 96
	a.setPanelHidden("ctrl.wardrobe", true)
	at2, end2 := run()
	if _, shown := at2["ctrl.wardrobe"]; shown {
		t.Error("a hidden button must not be drawn")
	}
	if at2["ctrl.character"] != startX {
		t.Errorf("the button before the hidden one moved: X=%d, want %d", at2["ctrl.character"], startX)
	}
	if got, want := at2["ctrl.restyle"], at["ctrl.restyle"]-wardrobeAdv; got != want {
		t.Errorf("restyle did not compact: X=%d, want %d", got, want)
	}
	if got, want := at2["ctrl.background"], at["ctrl.background"]-wardrobeAdv; got != want {
		t.Errorf("background did not compact: X=%d, want %d", got, want)
	}
	if end2 != end-wardrobeAdv {
		t.Errorf("cursor after hide = %d, want %d (one advance shorter)", end2, end-wardrobeAdv)
	}
}

// TestCtrlSlotKeysAreHideable pins that every key in the customizable-button
// list is a real ctrl.* slot key, so a tick in the UI popup actually hides a
// button (a typo'd key would silently toggle nothing).
func TestCtrlSlotKeysAreHideable(t *testing.T) {
	if len(hideableButtons) == 0 {
		t.Fatal("no hideable buttons registered")
	}
	for _, b := range hideableButtons {
		if len(b.id) < 5 || b.id[:5] != "ctrl." {
			t.Errorf("hideable button %q must use a ctrl.* slot key", b.id)
		}
		if b.label == "" {
			t.Errorf("hideable button %q has no label", b.id)
		}
	}
}
