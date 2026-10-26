package ui

import (
	"strconv"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestModBulkSelection pins #13 bulk targeting: ticking toggles membership and selectedPresentUIDs
// returns only ticked-and-present UIDs in roster order — a ticked player who isn't in the roster
// (left) is dropped, so a bulk command never targets a stale slot.
func TestModBulkSelection(t *testing.T) {
	a := testTabApp(t)
	a.rosterLegacy = true
	a.areaPlayers = []areaPlayer{
		{uid: "1", name: "phoenix"},
		{uid: "2", name: "edgeworth"},
		{uid: "3", name: "maya"},
	}
	a.toggleModSelected("3")
	a.toggleModSelected("1")
	a.toggleModSelected("9") // ticked but NOT present — must be dropped from the present set
	if got := a.selectedPresentUIDs(); len(got) != 2 || got[0] != "1" || got[1] != "3" {
		t.Errorf("selectedPresentUIDs = %v, want [1 3] in roster order", got)
	}
	if a.countSelectedPresent() != 2 {
		t.Errorf("countSelectedPresent = %d, want 2", a.countSelectedPresent())
	}
	a.toggleModSelected("1") // untick removes it
	if a.modDashSelected["1"] {
		t.Error("untick didn't remove the UID")
	}
	a.clearModSelected()
	if len(a.modDashSelected) != 0 {
		t.Errorf("clear left %d entries", len(a.modDashSelected))
	}
}

// TestModBulkSelectionCap pins the modBulkCap guard rail (hard rule #4): the selection can't grow
// past modBulkCap, bounding the set and guarding against a fat-fingered "ban the whole room".
func TestModBulkSelectionCap(t *testing.T) {
	a := testTabApp(t)
	for i := 0; i < modBulkCap+10; i++ {
		a.toggleModSelected("u" + strconv.Itoa(i))
	}
	if len(a.modDashSelected) != modBulkCap {
		t.Errorf("selection grew to %d, want capped at %d", len(a.modDashSelected), modBulkCap)
	}
}

// TestSendBulk pins the bulk send: one paced OOC command per ready frozen target, each audited, the
// selection cleared after. Whisker kicks by UID, so no IPID fetch is needed for the test.
func TestSendBulk(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil) // swallows sends
	a.frameNow = time.Now()
	a.cmSoftwareOverride = courtroom.SoftwareWhisker // "/kick <uid> <reason>" — builds from UID alone
	a.rosterLegacy = true
	a.areaPlayers = []areaPlayer{
		{uid: "1", name: "phoenix"},
		{uid: "2", name: "edgeworth"},
	}
	a.bulkBoxUIDs = []string{"1", "2"}
	a.banBoxReason = "spam"

	if n := a.sendBulk(false); n != 2 { // false = kick
		t.Fatalf("sendBulk queued %d, want 2", n)
	}
	if len(a.oocQueue) != 2 {
		t.Fatalf("oocQueue has %d lines, want 2", len(a.oocQueue))
	}
	if a.oocQueue[0].line != "/kick 1 spam" {
		t.Errorf("first queued = %q, want /kick 1 spam", a.oocQueue[0].line)
	}
	if len(a.modAudit) != 2 {
		t.Errorf("audit has %d entries, want 2", len(a.modAudit))
	}
	if len(a.bulkBoxUIDs) != 0 || len(a.modDashSelected) != 0 {
		t.Error("sendBulk must clear both the frozen list and the live selection")
	}
}
