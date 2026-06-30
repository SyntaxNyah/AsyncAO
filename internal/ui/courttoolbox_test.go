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
