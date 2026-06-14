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

// TestCollectOpenTabs pins the restore-on-launch snapshot (M7): the live active
// tab comes from the session fields, parked tabs from their stored state, in
// order, and dead / rehearsal / blank-URL / duplicate-URL slots are skipped.
func TestCollectOpenTabs(t *testing.T) {
	a := &App{activeTab: 1}
	a.serverName = "Active"
	a.serverKey = "wss://active:2096"
	a.tabs = []*courtTab{
		{state: sessionState{serverName: "Parked", serverKey: "wss://parked:2096"}},
		{}, // index 1 = the active tab (its session lives in a.sessionState)
		{state: sessionState{serverName: "Dead", serverKey: "wss://dead:2096"}, dead: true},
		{state: sessionState{serverName: "Reh", serverKey: "wss://reh:2096", rehearsal: true}},
		{state: sessionState{serverName: "NoURL"}},                               // blank URL → skipped
		{state: sessionState{serverName: "Dup", serverKey: "wss://active:2096"}}, // dup of active
	}
	got := a.collectOpenTabs()
	want := []config.OpenTab{
		{Name: "Parked", URL: "wss://parked:2096"},
		{Name: "Active", URL: "wss://active:2096"},
	}
	if len(got) != len(want) {
		t.Fatalf("collected %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tab %d = %v, want %v", i, got[i], want[i])
		}
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

// TestMoveTab pins the drag-reorder slice math: tabs land in the right order
// and activeTab keeps pointing at whatever session was active across the move
// (the two-step remove-then-insert index shift). Slots are identified by
// pointer, mirroring how the live strip carries each parked session.
func TestMoveTab(t *testing.T) {
	cases := []struct {
		name             string
		from, to, active int
		wantOrder        []int // positions expressed as original indices
		wantActive       int
	}{
		{"forward, active follows", 0, 2, 2, []int{1, 2, 0, 3}, 1},
		{"forward, moved is active", 0, 2, 0, []int{1, 2, 0, 3}, 2},
		{"forward, active past range", 1, 2, 3, []int{0, 2, 1, 3}, 3},
		{"backward to front", 3, 0, 1, []int{3, 0, 1, 2}, 2},
		{"no-op", 2, 2, 1, []int{0, 1, 2, 3}, 1},
		{"to end, lobby (no active)", 0, 3, -1, []int{1, 2, 3, 0}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := []*courtTab{{}, {}, {}, {}}
			a := &App{tabs: append([]*courtTab(nil), orig...), activeTab: tc.active}
			a.moveTab(tc.from, tc.to)
			if a.activeTab != tc.wantActive {
				t.Errorf("activeTab = %d, want %d", a.activeTab, tc.wantActive)
			}
			if len(a.tabs) != len(orig) {
				t.Fatalf("len = %d, want %d", len(a.tabs), len(orig))
			}
			for pos, want := range tc.wantOrder {
				if a.tabs[pos] != orig[want] {
					t.Errorf("position %d holds the wrong tab (want original index %d)", pos, want)
				}
			}
		})
	}
}
