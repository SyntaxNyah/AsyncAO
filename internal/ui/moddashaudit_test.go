package ui

import (
	"strconv"
	"strings"
	"testing"
)

// TestRecordModAudit pins the #13 audit log: it appends newest-last, skips an empty command, keeps
// the stored fields, and stays bounded at modAuditCap (dropping the oldest) so a long mod session
// can never grow it unbounded (hard rule #4).
func TestRecordModAudit(t *testing.T) {
	a := testTabApp(t)
	a.recordModAudit("Kick", "[5] Phoenix", "/kick 5 spam")
	a.recordModAudit("Ban", "[6] Edgeworth", "") // empty command — not recorded
	a.recordModAudit("Ban", "[7] Maya", "/ban -u 7 -d perma trolling")
	if len(a.modAudit) != 2 {
		t.Fatalf("len = %d, want 2 (the empty command must be skipped)", len(a.modAudit))
	}
	if a.modAudit[0].action != "Kick" || a.modAudit[1].target != "[7] Maya" {
		t.Errorf("order/fields wrong: %+v", a.modAudit)
	}
	if a.modAudit[1].cmd != "/ban -u 7 -d perma trolling" {
		t.Errorf("cmd not stored: %q", a.modAudit[1].cmd)
	}

	// Overflow: keep only the newest modAuditCap entries, dropping the oldest.
	for i := 0; i < modAuditCap+25; i++ {
		a.recordModAudit("Kick", "[1] x", "/kick 1 "+strconv.Itoa(i))
	}
	if len(a.modAudit) != modAuditCap {
		t.Fatalf("after overflow len = %d, want %d", len(a.modAudit), modAuditCap)
	}
	if last := a.modAudit[len(a.modAudit)-1].cmd; last != "/kick 1 "+strconv.Itoa(modAuditCap+25-1) {
		t.Errorf("newest entry = %q, want the last command recorded", last)
	}
}

// TestFormatModAudit pins the clipboard export text: a header line (with the server) plus one
// tab-separated row per entry (action, target, command), in record order.
func TestFormatModAudit(t *testing.T) {
	a := testTabApp(t)
	a.serverName = "Test Server"
	a.recordModAudit("Kick", "[5] Phoenix", "/kick 5 spam")
	a.recordModAudit("Ban", "[7] Maya", "/ban -u 7 -d perma trolling")
	out := a.formatModAudit()
	if !strings.Contains(out, "AsyncAO mod audit — Test Server") {
		t.Errorf("missing header: %q", out)
	}
	if !strings.Contains(out, "\tKick\t[5] Phoenix\t/kick 5 spam\n") {
		t.Errorf("missing kick row: %q", out)
	}
	if !strings.Contains(out, "\tBan\t[7] Maya\t/ban -u 7 -d perma trolling\n") {
		t.Errorf("missing ban row: %q", out)
	}
	if n := strings.Count(out, "\n"); n != 3 { // header + 2 entries
		t.Errorf("line count = %d, want 3", n)
	}
}
