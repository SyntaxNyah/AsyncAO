package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// testTabApp builds a headless App with just enough wiring for the tab
// machinery (real prefs for callword checks; no SDL, no network).
func testTabApp(t *testing.T) *App {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatalf("prefs: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{ctx: &Ctx{}, activeTab: -1}
	a.d.Prefs = prefs
	a.resetSessionState()
	return a
}

// TestTabParkActivateRoundTrip pins the core invariant: parking moves the
// WHOLE session out (live state pristine afterwards), activating moves it
// back bit-for-bit (logs, seqs, identity).
func TestTabParkActivateRoundTrip(t *testing.T) {
	a := testTabApp(t)
	if !a.allocateTab() {
		t.Fatal("first allocate must succeed")
	}
	a.serverName = "Skrapegropen"
	a.serverKey = "wss://skra.example:2096"
	// A session must exist or activation treats the tab as dead. The
	// rehearsal constructor gives a real offline session without a conn.
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	a.icInput = "half-typed message"
	a.icLog = append(a.icLog, icEntry{text: "Phoenix: hold it", color: 2})
	a.icLogSeq = 7
	a.oocLog = append(a.oocLog, "mod: welcome")
	a.oocSeq = 3

	a.parkActive()
	if a.activeTab != -1 || a.serverName != "" || len(a.icLog) != 0 || a.icInput != "" {
		t.Fatalf("park must leave a pristine live session: name=%q log=%d input=%q",
			a.serverName, len(a.icLog), a.icInput)
	}
	if a.spriteOv == nil || a.pairWith == 0 {
		t.Fatal("reset state must re-init maps and sentinels")
	}
	parked := &a.tabs[0].state
	if parked.serverName != "Skrapegropen" || parked.icLogSeq != 7 || len(parked.oocLog) != 1 {
		t.Fatalf("parked state lost data: %+v", parked.serverName)
	}

	a.activateTab(0)
	if a.activeTab != 0 || a.serverName != "Skrapegropen" || a.icInput != "half-typed message" {
		t.Fatalf("activate must restore the session: name=%q input=%q", a.serverName, a.icInput)
	}
	if a.icLogSeq != 7 || len(a.icLog) != 1 || a.icLog[0].color != 2 {
		t.Fatal("activate must restore logs and seqs bit-for-bit")
	}
	if a.tabs[0].state.serverName != "" {
		t.Fatal("the active tab's parked slot must be zeroed")
	}
}

// TestTabCap pins the bound: maxTabs sessions, the next connect refuses
// with a visible reason and leaves the active session untouched.
func TestTabCap(t *testing.T) {
	a := testTabApp(t)
	for i := 0; i < maxTabs; i++ {
		if !a.allocateTab() {
			t.Fatalf("allocate %d must succeed", i)
		}
		a.serverName = "srv"
		a.parkActive()
	}
	if a.allocateTab() {
		t.Fatalf("allocate beyond maxTabs=%d must refuse", maxTabs)
	}
	if a.connErr == "" {
		t.Fatal("the refusal must explain itself on connErr")
	}
}

// TestTabBackgroundRouting pins what parked tabs accumulate: chat into
// their own logs with caps and unread counts; disconnects mark dead; and
// the dead slot reaps on the next allocate.
func TestTabBackgroundRouting(t *testing.T) {
	a := testTabApp(t)
	if !a.allocateTab() {
		t.Fatal("allocate")
	}
	a.serverName = "bg"
	a.serverKey = "wss://bg.example"
	a.parkActive()

	tab := a.tabs[0]
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventOOC, Name: "mod", Text: "hi"})
	msg := &protocol.ChatMessage{Message: "objection!", TextColor: 2}
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventMessage, Message: msg})
	if tab.unread != 2 || len(tab.state.oocLog) != 1 || len(tab.state.icLog) != 1 {
		t.Fatalf("background chat must land in the tab: unread=%d ooc=%d ic=%d",
			tab.unread, len(tab.state.oocLog), len(tab.state.icLog))
	}
	if tab.state.icLogSeq == 0 || tab.state.oocSeq == 0 {
		t.Fatal("background appends must bump the cache-key seqs")
	}

	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventDisconnect, Text: "kicked"})
	if !tab.dead {
		t.Fatal("disconnect must mark the tab dead")
	}
	// Dead tabs reap on allocate: the slot frees up.
	if !a.allocateTab() {
		t.Fatal("allocate must reap the dead tab and succeed")
	}
	if len(a.tabs) != 1 {
		t.Fatalf("dead tab must be gone, have %d tabs", len(a.tabs))
	}
}

// TestLoweredCacheMemo pins the per-frame query memo.
func TestLoweredCacheMemo(t *testing.T) {
	var l loweredCache
	if got := l.get("  Phoenix "); got != "phoenix" {
		t.Fatalf("get = %q", got)
	}
	first := l.get("  Phoenix ")
	second := l.get("  Phoenix ")
	if first != second || second != "phoenix" {
		t.Fatal("repeat gets must return the memoized value")
	}
	if got := l.get("EDGEWORTH"); got != "edgeworth" {
		t.Fatalf("changed src must re-lower, got %q", got)
	}
}
