package ui

import (
	"errors"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// TestLobbyAutoRefreshDue pins the pure cap decision: a zero stamp (no fetch
// ever started) is due immediately, a fetch inside the cap suppresses, and the
// cap boundary itself (>=) fires — opening the lobby after the interval must
// refresh at once, never wait out a timer.
func TestLobbyAutoRefreshDue(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if !lobbyAutoRefreshDue(time.Time{}, now) {
		t.Error("zero last (never fetched) must be due immediately")
	}
	if lobbyAutoRefreshDue(now.Add(-lobbyAutoRefreshMinInterval/2), now) {
		t.Error("a fetch inside the cap must suppress the auto-refresh")
	}
	if !lobbyAutoRefreshDue(now.Add(-lobbyAutoRefreshMinInterval), now) {
		t.Error("exactly the cap elapsed must fire (>= boundary — open-after-a-minute refreshes NOW)")
	}
	if !lobbyAutoRefreshDue(now.Add(-2*lobbyAutoRefreshMinInterval), now) {
		t.Error("well past the cap must fire")
	}
}

// TestLobbyEntryAutoRefresh pins the edge trigger end to end on a headless App:
func TestLobbyEntryAutoRefresh(t *testing.T) {
	a := testTabApp(t)
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	a.budget.frameNow = base // fake clock: a.now() reads the frame snapshot (reconnect.go idiom)
	a.conn.lobby.lobbyResult = make(chan lobbyFetch, 1)
	a.d.MasterURL = "://no-network" // missing scheme: http.NewRequestWithContext errors before any I/O

	// Entry edge with a cold (zero) stamp: fires immediately, arming the cap.
	a.screen, a.frame.drawnScreen = ScreenLobby, ScreenCourtroom
	a.noteScreenTransition()
	if !a.conn.lobby.lobbyFetching {
		t.Fatal("entering the lobby with no prior fetch must refresh immediately")
	}
	if !a.conn.lobby.lastLobbyRefreshAt.Equal(base) {
		t.Errorf("RefreshServers must arm the cap stamp: got %v, want %v", a.conn.lobby.lastLobbyRefreshAt, base)
	}
	<-a.conn.lobby.lobbyResult // reap the (instantly failed) fetch so the goroutine is done
	a.conn.lobby.lobbyFetching = false

	// Sitting on the lobby: however stale the stamp, no transition = no request
	// (the trigger is purely edge — there is no polling timer to fire).
	a.budget.frameNow = base.Add(2 * lobbyAutoRefreshMinInterval)
	a.conn.lobby.lastLobbyRefreshAt = time.Time{} // even maximally due…
	a.noteScreenTransition()
	if a.conn.lobby.lobbyFetching {
		t.Error("no screen transition must never auto-refresh (edge-triggered, not polled)")
	}

	// Re-entry INSIDE the cap: suppressed — a manual refresh (or any fetch)
	// moments ago already armed the stamp.
	a.conn.lobby.lastLobbyRefreshAt = a.budget.frameNow.Add(-lobbyAutoRefreshMinInterval / 2)
	a.frame.drawnScreen = ScreenSettings // last frame drew Settings → this frame is an entry edge
	a.noteScreenTransition()
	if a.conn.lobby.lobbyFetching {
		t.Error("re-entering the lobby within the cap must not refetch")
	}

	// Leaving the lobby: never fires, regardless of staleness.
	a.conn.lobby.lastLobbyRefreshAt = a.budget.frameNow.Add(-2 * lobbyAutoRefreshMinInterval)
	a.screen = ScreenSettings // drawnScreen settled on ScreenLobby above
	a.noteScreenTransition()
	if a.conn.lobby.lobbyFetching {
		t.Error("a transition OUT of the lobby must not refetch")
	}

	// Re-entry PAST the cap: fires again and re-arms the stamp.
	a.screen = ScreenLobby
	a.noteScreenTransition()
	if !a.conn.lobby.lobbyFetching {
		t.Error("re-entering the lobby past the cap must refresh immediately")
	}
	if !a.conn.lobby.lastLobbyRefreshAt.Equal(a.budget.frameNow) {
		t.Errorf("the auto fetch must re-arm the cap stamp: got %v, want %v", a.conn.lobby.lastLobbyRefreshAt, a.budget.frameNow)
	}
	<-a.conn.lobby.lobbyResult
}

// TestRefreshServersDedupKeepsCapStamp pins the stamp's placement BELOW the
// in-flight dedup gate: a coalesced RefreshServers call starts no fetch, so it
// must not push the cap out (only fetch STARTS arm it).
func TestRefreshServersDedupKeepsCapStamp(t *testing.T) {
	a := testTabApp(t)
	a.budget.frameNow = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	a.conn.lobby.lobbyFetching = true // a fetch is already in flight
	armed := a.budget.frameNow.Add(-lobbyAutoRefreshMinInterval / 2)
	a.conn.lobby.lastLobbyRefreshAt = armed
	a.RefreshServers() // dedup'd: must be a pure no-op
	if !a.conn.lobby.lastLobbyRefreshAt.Equal(armed) {
		t.Errorf("a dedup'd RefreshServers must not re-arm the cap: got %v, want %v", a.conn.lobby.lastLobbyRefreshAt, armed)
	}
}

// TestLobbyEntryDrainsParkedFetch pins the parked-result drain at the lobby
// entry edge: leaving the lobby mid-fetch parks the result in the 1-buffer
// channel with lobbyFetching latched true (drawLobby, the only steady-state
// drain, no longer runs). A due re-entry must drain that parked result — apply
// it exactly once, clearing the latch — and then start a genuinely NEW fetch,
// not no-op against RefreshServers' dedup gate and show the stale list for the
// whole visit.
func TestLobbyEntryDrainsParkedFetch(t *testing.T) {
	a := testTabApp(t)
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	a.budget.frameNow = base
	a.conn.lobby.lobbyResult = make(chan lobbyFetch, 1)
	a.d.MasterURL = "://no-network" // missing scheme: the fetch goroutine errors before any I/O

	// Stage "left the lobby mid-fetch": the latch is up and the completed
	// result sits parked, un-drained.
	a.conn.lobby.lobbyFetching = true
	a.conn.lobby.lobbyResult <- lobbyFetch{entries: []network.ServerEntry{
		{IP: "a.example", WSPort: 1001, Name: "Alpha", Players: 3},
	}}

	// Re-entry past the cap: the edge must drain the parked result AND fire a
	// fresh fetch.
	a.conn.lobby.lastLobbyRefreshAt = base.Add(-2 * lobbyAutoRefreshMinInterval)
	a.screen, a.frame.drawnScreen = ScreenLobby, ScreenSettings
	a.noteScreenTransition()
	if len(a.conn.lobby.masterEntries) != 1 || a.conn.lobby.masterEntries[0].Name != "Alpha" {
		t.Errorf("the parked result must be applied on entry, got masterEntries=%v", a.conn.lobby.masterEntries)
	}
	if !a.conn.lobby.lobbyFetching {
		t.Fatal("a new fetch must start after the drain (the stale latch must not dedup it away)")
	}
	if !a.conn.lobby.lastLobbyRefreshAt.Equal(base) {
		t.Errorf("the new fetch must re-arm the cap stamp (proof it passed the dedup gate): got %v, want %v", a.conn.lobby.lastLobbyRefreshAt, base)
	}
	<-a.conn.lobby.lobbyResult // reap the new (instantly failed) fetch so its goroutine is done
}

// TestPollLobbyFetchKeepsSelectionByIdentity pins that a landing refresh
// re-matches the selected row by WebSocketURL in the re-sorted list instead of
// wiping the selection — a background auto-refresh must not cancel the user's
// click-1 or their Enter-to-join target — while a server that VANISHED from
// the list still clears it (index carry-over through a re-sort could join a
// different server, so identity is the only safe key).
func TestPollLobbyFetchKeepsSelectionByIdentity(t *testing.T) {
	a := testTabApp(t)
	a.conn.lobby.lobbyResult = make(chan lobbyFetch, 1)
	alpha := network.ServerEntry{IP: "a.example", WSPort: 1001, Name: "Alpha", Players: 1}
	beta := network.ServerEntry{IP: "b.example", WSPort: 1002, Name: "Beta", Players: 9}
	a.conn.lobby.servers = []network.ServerEntry{alpha, beta}
	a.conn.lobby.selServer = 1 // Beta
	wantURL := beta.WebSocketURL()

	// The new list still contains Beta (extra entry, different sort order).
	gamma := network.ServerEntry{IP: "c.example", WSPort: 1003, Name: "Gamma", Players: 20}
	a.conn.lobby.lobbyResult <- lobbyFetch{entries: []network.ServerEntry{gamma, alpha, beta}}
	a.pollLobbyFetch()
	if a.conn.lobby.selServer < 0 || a.conn.lobby.selServer >= len(a.conn.lobby.servers) {
		t.Fatalf("selection must survive a refresh containing the same server, got selServer=%d", a.conn.lobby.selServer)
	}
	if got := a.conn.lobby.servers[a.conn.lobby.selServer].WebSocketURL(); got != wantURL {
		t.Errorf("selection must follow the server's identity through the re-sort: got %s, want %s", got, wantURL)
	}

	// A new list WITHOUT Beta: the selection must clear — any carried index
	// would now point at a different server (the wrong-join hazard).
	a.conn.lobby.lobbyResult <- lobbyFetch{entries: []network.ServerEntry{gamma, alpha}}
	a.pollLobbyFetch()
	if a.conn.lobby.selServer != -1 {
		t.Errorf("a vanished server must clear the selection, got selServer=%d", a.conn.lobby.selServer)
	}
}

// TestRefreshKeepsPingSortAndPrunes pins that ping state survives a refresh —
// the on-open auto-refresh funnels through RefreshServers, and the old
// wipe-at-fetch-start snapped a ping-sorted lobby back to player order with
// zero user action — and that boundedness moved rather than vanished: a
// successful landing prunes connect times for servers no longer on the list
// (rule §17.4), while an error landing (favorites-only degraded list) keeps
// them so a transient master hiccup can't throw the cache away.
func TestRefreshKeepsPingSortAndPrunes(t *testing.T) {
	a := testTabApp(t)
	a.budget.frameNow = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	a.conn.lobby.lobbyResult = make(chan lobbyFetch, 1)
	a.d.MasterURL = "://no-network"
	alpha := network.ServerEntry{IP: "a.example", WSPort: 1001, Name: "Alpha", Players: 1}
	gone := "ws://gone.example:9999"
	a.conn.lobby.pingMode = true
	a.conn.lobby.pings = map[string]time.Duration{
		alpha.WebSocketURL(): 5 * time.Millisecond,
		gone:                 8 * time.Millisecond,
	}

	a.RefreshServers()
	if !a.conn.lobby.pingMode {
		t.Error("a refresh start must not exit ping-sort mode")
	}
	if len(a.conn.lobby.pings) != 2 {
		t.Errorf("a refresh start must not drop the connect-time cache, got %d entries", len(a.conn.lobby.pings))
	}
	<-a.conn.lobby.lobbyResult // reap the (instantly failed) real fetch so its goroutine is done
	a.conn.lobby.lobbyFetching = false

	// Error landing: no prune (the merged list degraded to favorites-only).
	a.conn.lobby.lobbyResult <- lobbyFetch{err: errors.New("master down")}
	a.pollLobbyFetch()
	if len(a.conn.lobby.pings) != 2 {
		t.Errorf("an error landing must keep the connect-time cache, got %d entries", len(a.conn.lobby.pings))
	}

	// Successful landing: live entries survive, vanished ones are pruned.
	a.conn.lobby.lobbyResult <- lobbyFetch{entries: []network.ServerEntry{alpha}}
	a.pollLobbyFetch()
	if !a.conn.lobby.pingMode {
		t.Error("a landing list must not exit ping-sort mode")
	}
	if _, ok := a.conn.lobby.pings[alpha.WebSocketURL()]; !ok {
		t.Error("a live server's connect time must survive the landing")
	}
	if _, ok := a.conn.lobby.pings[gone]; ok {
		t.Error("a vanished server's connect time must be pruned on a successful landing (boundedness, rule §17.4)")
	}
}

// TestErrorLandingKeepsList pins that a FAILED fetch landing leaves the visible
// list untouched: the on-open auto-refresh fires with zero user action, so a
// transient master hiccup must not erase a stale-but-fully-joinable list (or
// the selection on it) mid-glance. Only the status line changes.
func TestErrorLandingKeepsList(t *testing.T) {
	a := testTabApp(t)
	a.conn.lobby.lobbyResult = make(chan lobbyFetch, 1)
	alpha := network.ServerEntry{IP: "a.example", WSPort: 1001, Name: "Alpha", Players: 1}
	a.conn.lobby.masterEntries = []network.ServerEntry{alpha}
	a.conn.lobby.servers = []network.ServerEntry{alpha}
	a.conn.lobby.selServer = 0

	a.conn.lobby.lobbyResult <- lobbyFetch{err: errors.New("master down")}
	a.pollLobbyFetch()
	if len(a.conn.lobby.servers) != 1 || len(a.conn.lobby.masterEntries) != 1 {
		t.Errorf("an error landing must keep the visible list, got servers=%d master=%d", len(a.conn.lobby.servers), len(a.conn.lobby.masterEntries))
	}
	if a.conn.lobby.selServer != 0 {
		t.Errorf("an error landing must not touch the selection, got selServer=%d", a.conn.lobby.selServer)
	}
}

// TestLeaveLobbyClearsPingLatch pins the leave-edge heal for a wedged sweep:
func TestLeaveLobbyClearsPingLatch(t *testing.T) {
	a := testTabApp(t)
	a.conn.lobby.pinging = true
	gen := a.conn.lobby.pingGen
	a.screen, a.frame.drawnScreen = ScreenSettings, ScreenLobby // leaving the lobby this frame
	a.noteScreenTransition()
	if a.conn.lobby.pinging {
		t.Error("leaving the lobby must clear the pinging latch (dropped-sentinel wedge)")
	}
	if a.conn.lobby.pingGen == gen {
		t.Error("leaving the lobby must supersede the in-flight sweep (gen bump)")
	}
}
