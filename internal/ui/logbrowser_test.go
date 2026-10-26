package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/logbrowser"
)

func TestBuildModcallClip(t *testing.T) {
	now := time.Date(2026, 6, 29, 14, 32, 0, 0, time.UTC)
	log := []icEntry{
		{text: "Phoenix: one", stamp: "14:30"},
		{text: "Maya: two", stamp: "14:31"},
		{text: "Edgeworth: three", stamp: "14:32"},
	}
	// n caps to the last 2 lines; header is 5 lines.
	got := buildModcallClip("miku.pizza", "Phoenix called a mod: spam", log, 2, now)
	if len(got) != 7 {
		t.Fatalf("clip lines = %d, want 7 (5 header + 2 IC)\n%q", len(got), got)
	}
	if got[0] != "AsyncAO modcall clip" || got[1] != "Server : miku.pizza" {
		t.Errorf("header = %q", got[:2])
	}
	if got[3] != "Notice : Phoenix called a mod: spam" {
		t.Errorf("notice line = %q", got[3])
	}
	// Only the last 2 IC lines, with timestamps, in order.
	if got[5] != "[14:31] Maya: two" || got[6] != "[14:32] Edgeworth: three" {
		t.Errorf("IC tail = %q", got[5:])
	}
	// n larger than the log keeps every line (no panic / negative slice).
	if all := buildModcallClip("s", "x", log, 99, now); len(all) != 5+3 {
		t.Errorf("oversized n: lines = %d, want 8", len(all))
	}
}

// TestPollLogBrowserDropsStaleGeneration pins the latest-wins guard on the
// scope-load funnel: an off-thread result whose generation was superseded by a
// newer kick must be dropped, never overwriting the newer scope's state.
func TestPollLogBrowserDropsStaleGeneration(t *testing.T) {
	a := &App{
		logBrowserRes: make(chan logbrowser.Result, 1),
		logBrowser:    logBrowserState{gen: 5, loading: true},
	}
	a.logBrowserRes <- logbrowser.Result{Gen: 3, Servers: []string{"stale"}}
	a.pollLogBrowser()
	if a.logBrowser.loading != true || len(a.logBrowser.servers) != 0 {
		t.Fatalf("stale result landed: loading=%v servers=%v", a.logBrowser.loading, a.logBrowser.servers)
	}

	// A matching generation lands and clears loading.
	a.logBrowserRes <- logbrowser.Result{Gen: 5, Servers: []string{"miku.pizza"}}
	a.pollLogBrowser()
	if a.logBrowser.loading || len(a.logBrowser.servers) != 1 || a.logBrowser.servers[0] != "miku.pizza" {
		t.Fatalf("fresh result did not land: loading=%v servers=%v", a.logBrowser.loading, a.logBrowser.servers)
	}
}
