package ui

import "testing"

// TestRequestEvidenceSelectionAsksWhileEditing pins the Stop-editing? gate: a
// selection swap (or Add new) while the editor is open must ask first and leave
// the current selection untouched; a swap while not editing applies directly.
func TestRequestEvidenceSelectionAsksWhileEditing(t *testing.T) {
	a := testTabApp(t)
	a.evid.evidIdx = 0
	a.evid.evidEditing = true
	a.requestEvidenceSelection(2)
	if !a.evid.evidDiscardConfirm || a.evid.evidDiscardTarget != 2 {
		t.Fatalf("swap while editing must ask: confirm=%v target=%d", a.evid.evidDiscardConfirm, a.evid.evidDiscardTarget)
	}
	if a.evid.evidIdx != 0 {
		t.Fatalf("selection must not change while the confirm is pending, got %d", a.evid.evidIdx)
	}

	// Add new also routes through the confirm when editing.
	a.evid.evidDiscardConfirm = false
	a.startEvidenceAdd()
	if !a.evid.evidDiscardConfirm || a.evid.evidDiscardTarget != -1 {
		t.Fatalf("Add new while editing must ask: confirm=%v target=%d", a.evid.evidDiscardConfirm, a.evid.evidDiscardTarget)
	}

	// Not editing: swaps (and Add new) apply directly.
	a.evid.evidEditing = false
	a.evid.evidDiscardConfirm = false
	a.requestEvidenceSelection(3)
	if a.evid.evidDiscardConfirm || a.evid.evidIdx != 3 {
		t.Fatalf("swap while not editing must apply directly: confirm=%v idx=%d", a.evid.evidDiscardConfirm, a.evid.evidIdx)
	}
}
