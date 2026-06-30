package ui

import "testing"

// TestHideablePanelsHaveShortLabels pins the editor-toolbox requirement (#27): every
// hideable chrome panel carries a SHORT chip label, so no toolbox chip renders blank.
func TestHideablePanelsHaveShortLabels(t *testing.T) {
	for _, p := range hideablePanels {
		if p.short == "" {
			t.Errorf("hideablePanel %q (%q) has no short chip label", p.id, p.label)
		}
		if p.label == "" {
			t.Errorf("hideablePanel %q has no dialog label", p.id)
		}
	}
}

// TestHideableForSlot pins the drag show/hide mapping (#27 slice 2): a slot key
// resolves to its hideable element id, and non-mapped slots return "".
func TestHideableForSlot(t *testing.T) {
	if got := hideableForSlot(slotEmotes); got != panelEmotes {
		t.Errorf("hideableForSlot(emotes) = %q, want %q", got, panelEmotes)
	}
	if got := hideableForSlot(slotRightCol); got != panelLog {
		t.Errorf("hideableForSlot(rightcol) = %q, want %q", got, panelLog)
	}
	if got := hideableForSlot("ctrl.mods"); got != "ctrl.mods" {
		t.Errorf("hideableForSlot(ctrl.mods) = %q, want ctrl.mods", got)
	}
	// The viewport and the IC bar are not hide targets; toggle-only pieces (hp) have
	// no slot. Both must resolve to "" so a drag-release there never hides anything.
	if got := hideableForSlot(slotViewport); got != "" {
		t.Errorf("hideableForSlot(viewport) = %q, want empty", got)
	}
	if got := hideableForSlot(panelHP); got != "" {
		t.Errorf("hideableForSlot(hp) = %q, want empty (no slot)", got)
	}
}

// TestHideableSlotKeysKnown guards the map's keys against drift: every mapped id must
// be a real hideable panel or button.
func TestHideableSlotKeysKnown(t *testing.T) {
	known := make(map[string]bool)
	for _, p := range hideablePanels {
		known[p.id] = true
	}
	for _, b := range hideableButtons {
		known[b.id] = true
	}
	for id := range hideableSlot {
		if !known[id] {
			t.Errorf("hideableSlot maps unknown id %q", id)
		}
	}
}

// TestToolboxIDsUnique guards against a duplicate id across the panel + button sets,
// which would make two toolbox chips toggle the same hidden-state key.
func TestToolboxIDsUnique(t *testing.T) {
	seen := make(map[string]string)
	for _, p := range hideablePanels {
		if prev, dup := seen[p.id]; dup {
			t.Errorf("duplicate hideable id %q (panel %q and %q)", p.id, p.short, prev)
		}
		seen[p.id] = p.short
	}
	for _, b := range hideableButtons {
		if prev, dup := seen[b.id]; dup {
			t.Errorf("duplicate hideable id %q (button %q and %q)", b.id, b.label, prev)
		}
		seen[b.id] = b.label
	}
}
