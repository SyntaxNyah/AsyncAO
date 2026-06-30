package ui

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestBanActionSummary pins the human-readable ban/kick summary (the "ban someone 6 hours for
// disrespect" feedback): a ban names the duration + reason, a kick omits the duration, and an
// empty reason reads "no reason given".
func TestBanActionSummary(t *testing.T) {
	ban := banActionSummary(true, "[12] Phoenix", courtroom.Ban1Day, "trolling")
	for _, want := range []string{"Ban", "Phoenix", "trolling", " for "} {
		if !strings.Contains(ban, want) {
			t.Errorf("ban summary %q missing %q", ban, want)
		}
	}
	kick := banActionSummary(false, "[12] Phoenix", courtroom.Ban1Day, "")
	if !strings.Contains(kick, "Kick") {
		t.Errorf("kick summary should say Kick: %q", kick)
	}
	if strings.Contains(kick, " for ") {
		t.Errorf("kick summary must not mention a duration: %q", kick)
	}
	if !strings.Contains(kick, "no reason") {
		t.Errorf("empty reason should read 'no reason given': %q", kick)
	}
}

// TestBanBoxNonBlocking pins that the ban / kick box is a NON-BLOCKING floating box (the user
// asked for a draggable box that doesn't make chat unusable): opening it does NOT raise a blocking
// modal, only the cursor being OVER it fences the courtroom, and a closed box never fences. Mirrors
// TestEvidencePanelNonBlocking (#5) for the moderation box.
func TestBanBoxNonBlocking(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	const w, h = int32(1000), int32(800)

	a.banBoxKind = 1 // a single ban box is open
	if a.courtModalOpen() {
		t.Error("the ban/kick box must NOT be a blocking modal — the courtroom stays live behind it")
	}
	r := a.banBoxRect(w, h)
	a.ctx.mouseX, a.ctx.mouseY = r.X+5, r.Y+5
	if !a.boxFencesPointer(w, h) {
		t.Error("cursor over the ban box must fence the courtroom (clicks can't leak through)")
	}
	a.ctx.mouseX, a.ctx.mouseY = r.X+r.W+50, r.Y+r.H+50
	if a.boxFencesPointer(w, h) {
		t.Error("cursor clear of the ban box must NOT fence — the courtroom stays live")
	}
	a.banBoxKind = 0
	a.ctx.mouseX, a.ctx.mouseY = r.X+5, r.Y+5
	if a.boxFencesPointer(w, h) {
		t.Error("a closed ban box must never fence")
	}
}

// TestBanBoxHidesDashboard pins that while the ban/kick box is open the dashboard's own pointer
// fence stands down (it isn't drawn then — the box is drawn in its place), so the only thing
// fencing the courtroom in that region is the box itself. Guards the banBoxKind==0 gate on the
// dashboard fence from silently fencing an invisible panel.
func TestBanBoxHidesDashboard(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	const w, h = int32(1000), int32(800)

	a.showModDash = true
	a.banBoxKind = 2 // a kick box is open over the dashboard
	// A point inside the dashboard rect but clear of the (smaller/elsewhere) ban box must not be
	// fenced by the now-hidden dashboard.
	dr := a.modDashRect(w, h)
	br := a.banBoxRect(w, h)
	mx, my := dr.X+4, dr.Y+4
	if pointIn(mx, my, br) {
		t.Skip("ban box overlaps the probed dashboard corner on this geometry; fence test is ambiguous")
	}
	a.ctx.mouseX, a.ctx.mouseY = mx, my
	if a.boxFencesPointer(w, h) {
		t.Error("the dashboard is hidden while its ban box is open — its rect must not fence")
	}
}
