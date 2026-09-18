package ui

import "testing"

// TestRequestEvidenceSelectionAsksWhileEditing pins the Stop-editing? gate: a
// selection swap (or Add new) while the editor is open must ask first and leave
// the current selection untouched; a swap while not editing applies directly.
func TestRequestEvidenceSelectionAsksWhileEditing(t *testing.T) {
	a := testTabApp(t)
	a.evidIdx = 0
	a.evidEditing = true
	a.requestEvidenceSelection(2)
	if !a.evidDiscardConfirm || a.evidDiscardTarget != 2 {
		t.Fatalf("swap while editing must ask: confirm=%v target=%d", a.evidDiscardConfirm, a.evidDiscardTarget)
	}
	if a.evidIdx != 0 {
		t.Fatalf("selection must not change while the confirm is pending, got %d", a.evidIdx)
	}

	// Add new also routes through the confirm when editing.
	a.evidDiscardConfirm = false
	a.startEvidenceAdd()
	if !a.evidDiscardConfirm || a.evidDiscardTarget != -1 {
		t.Fatalf("Add new while editing must ask: confirm=%v target=%d", a.evidDiscardConfirm, a.evidDiscardTarget)
	}

	// Not editing: swaps (and Add new) apply directly.
	a.evidEditing = false
	a.evidDiscardConfirm = false
	a.requestEvidenceSelection(3)
	if a.evidDiscardConfirm || a.evidIdx != 3 {
		t.Fatalf("swap while not editing must apply directly: confirm=%v idx=%d", a.evidDiscardConfirm, a.evidIdx)
	}
}
