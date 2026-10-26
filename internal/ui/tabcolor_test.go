package ui

import "testing"

// TestCycleTabColor pins #22 per-tab colour: Ctrl+click cycles a tab's chip tint through the
// palette and wraps back to none (dropping the map entry), keyed by serverKey; a tint reads back
// via tabChipTint; an unkeyed tab is a no-op.
func TestCycleTabColor(t *testing.T) {
	a := testTabApp(t)
	a.activeTab = 0
	a.serverKey = "ws://x:50001" // tabKey(0) == serverKey for the active tab

	a.cycleTabColor(0)
	if a.tabColors["ws://x:50001"] != 1 {
		t.Fatalf("first cycle = %d, want 1", a.tabColors["ws://x:50001"])
	}
	if _, ok := a.tabChipTint(0); !ok {
		t.Error("tabChipTint should report a tint after colouring")
	}

	// Cycle through the rest of the palette and back to none (entry removed).
	for i := 0; i < len(tabPalette)-1; i++ {
		a.cycleTabColor(0)
	}
	if _, ok := a.tabColors["ws://x:50001"]; ok {
		t.Error("cycling back to 0 must drop the map entry (no tint)")
	}
	if _, ok := a.tabChipTint(0); ok {
		t.Error("tabChipTint must report no tint at palette 0")
	}

	// Unkeyed tab: no-op (never panics, never colours).
	a.serverKey = ""
	a.cycleTabColor(0)
	if len(a.tabColors) != 0 {
		t.Error("an unkeyed tab must not get a colour")
	}
}
