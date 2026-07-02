package ui

import "testing"

// TestGroupUnreadFlow pins the chat-list badge: a line landing while the group
// isn't on screen counts as unread; one landing while it's selected AND the
// panel is open doesn't; opening the group clears the count (drawGroupView).
func TestGroupUnreadFlow(t *testing.T) {
	a := &App{}
	g := a.ensureGroup(7)
	g.name = "case prep"
	g.ownerUID = 1

	a.showMessages = false
	a.applyGroupText(7, 2, "Edgeworth", "hello")
	a.applyGroupText(7, 2, "Edgeworth", "anyone?")
	if g.unread != 2 {
		t.Fatalf("unread = %d, want 2 (panel closed)", g.unread)
	}
	a.showMessages = true
	a.msgSelGroup = 7
	a.applyGroupText(7, 2, "Edgeworth", "oh hi")
	if g.unread != 2 {
		t.Fatalf("a line landing while the group is on screen must not bump unread (got %d)", g.unread)
	}
	if got := groupUnreadLabel(g.name, g.unread); got != "# case prep (2)" {
		t.Errorf("unread label = %q", got)
	}
	if got := groupUnreadLabel(g.name, 0); got != "# case prep" {
		t.Errorf("read label = %q", got)
	}

	// Timestamps + sender chars ride each line (the bubble icon/stamp inputs).
	if ln := g.lines[len(g.lines)-1]; ln.at.IsZero() {
		t.Error("appendLine must stamp the receipt time")
	}
}

// TestGroupChipColorStable pins the per-group accent: deterministic per id,
// different for different ids (the two-groups-look-identical fix).
func TestGroupChipColorStable(t *testing.T) {
	if groupChipColor(42) != groupChipColor(42) {
		t.Error("chip colour must be stable per group id")
	}
	if groupChipColor(41) == groupChipColor(140) {
		t.Error("distinct group ids should get distinct hues (41%360 != 140%360)")
	}
}
