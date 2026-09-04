package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsTestServer starts a minimal AO-shaped WebSocket server that accepts and
// then just holds the socket open (idle) until the test closes it. Good
// enough for a successful Dial — nothing here reads the courtroom-decryptor
// greeting, since these tests assert on connectWith's post-dial state, not
// on anything pumpConnection would drain.
func wsTestServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// seedCarryableLog stamps a's live session with distinctive, recognisable
// IC/OOC/area-log content so a test can tell "restored" from "started blank"
// at a glance.
func seedCarryableLog(a *App, tag string) {
	a.icLog = []icEntry{{text: tag + "-ic-line", color: 2}}
	a.icLogSeq = 3
	a.icReadMark = 1
	a.oocLog = []string{tag + "-ooc-line"}
	a.oocSpeakers = []string{tag + "-mod"}
	a.oocSeq = 5
	a.areaLogs = map[string][]icEntry{"Basement": {{text: tag + "-area-line"}}}
	a.areaLogOrder = []string{"Basement"}
}

func logIsEmpty(a *App) bool {
	return len(a.icLog) == 0 && len(a.oocLog) == 0 && len(a.oocSpeakers) == 0 && len(a.areaLogs) == 0 && len(a.areaLogOrder) == 0
}

// TestReconnectFromDialogRestoresLogOnSameServer pins the user-facing ask: a
// frozen session's Reconnect click, redialed to the SAME server, must bring
// the IC/OOC/area log back — not start blank like a fresh Join.
func TestReconnectFromDialogRestoresLogOnSameServer(t *testing.T) {
	a := froomApp(t)
	url := wsTestServer(t)
	a.serverName, a.serverKey = "Test Server", url
	a.lastConnName, a.lastConnURL = "Test Server", url
	seedCarryableLog(a, "before")

	a.handleInvoluntaryDrop("connection closed") // freezes: sess/room/logs all survive
	if !a.disconnectDlg.open {
		t.Fatal("test setup: expected the dialog to freeze open")
	}
	if a.disconnectDlg.url != url {
		t.Fatalf("test setup: dialog url = %q, want %q", a.disconnectDlg.url, url)
	}

	a.reconnectFromDisconnectDialog()

	if a.conn == nil {
		t.Fatal("the redial to a real, reachable server must have succeeded")
	}
	if a.screen != ScreenCharSelect {
		t.Fatalf("a successful redial lands on char select, got %v", a.screen)
	}
	if len(a.icLog) != 1 || a.icLog[0].text != "before-ic-line" {
		t.Errorf("icLog not restored, got %+v", a.icLog)
	}
	if len(a.oocLog) != 1 || a.oocLog[0] != "before-ooc-line" || a.oocSpeakers[0] != "before-mod" {
		t.Errorf("oocLog/oocSpeakers not restored, got %v / %v", a.oocLog, a.oocSpeakers)
	}
	if got := a.areaLogs["Basement"]; len(got) != 1 || got[0].text != "before-area-line" {
		t.Errorf("areaLogs not restored, got %v", a.areaLogs)
	}
	if len(a.areaLogOrder) != 1 || a.areaLogOrder[0] != "Basement" {
		t.Errorf("areaLogOrder not restored, got %v", a.areaLogOrder)
	}
}

// TestDeliberateLeavePathsNeverCarryTheLog pins the other half of the
// contract: every deliberate LEAVE — the Disconnect button and the dialog's
// Back to lobby — must keep today's clean-slate behavior. Neither takes the
// snapshotSessionCarry call, so a later reconnect to the very same server
// starts blank, exactly as before this feature existed.
func TestDeliberateLeavePathsNeverCarryTheLog(t *testing.T) {
	url := wsTestServer(t)

	t.Run("Disconnect button", func(t *testing.T) {
		a := froomApp(t)
		a.serverName, a.serverKey = "Test Server", url
		a.lastConnName, a.lastConnURL = "Test Server", url
		seedCarryableLog(a, "stale")

		a.Disconnect()
		a.Connect("Test Server", url)

		if a.conn == nil {
			t.Fatal("the redial to a real, reachable server must have succeeded")
		}
		if !logIsEmpty(a) {
			t.Errorf("a deliberate Disconnect must never carry a log, got icLog=%v oocLog=%v", a.icLog, a.oocLog)
		}
	})

	t.Run("Back to lobby", func(t *testing.T) {
		a := froomApp(t)
		a.serverName, a.serverKey = "Test Server", url
		a.lastConnName, a.lastConnURL = "Test Server", url
		seedCarryableLog(a, "stale")
		a.handleInvoluntaryDrop("connection closed") // freeze first, as the real path requires
		if !a.disconnectDlg.open {
			t.Fatal("test setup: expected the dialog to freeze open")
		}

		a.closeDisconnectDialogToLobby()
		a.Connect("Test Server", url)

		if a.conn == nil {
			t.Fatal("the redial to a real, reachable server must have succeeded")
		}
		if !logIsEmpty(a) {
			t.Errorf("Back to lobby must never carry a log, got icLog=%v oocLog=%v", a.icLog, a.oocLog)
		}
	})
}

// TestKickAndBanNeverCarryTheLog pins that a server kick/ban — which never
// auto-reconnects (shouldAutoReconnect) — also never snapshots a carry, so a
// LATER manual reconnect to the same server still starts blank: retrying
// after a kick/ban must look exactly like a fresh join, not a resume.
func TestKickAndBanNeverCarryTheLog(t *testing.T) {
	for _, reason := range []string{"Kicked: rude", "Banned: cheating"} {
		t.Run(reason, func(t *testing.T) {
			a := froomApp(t)
			url := wsTestServer(t)
			a.serverName, a.serverKey = "Test Server", url
			a.lastConnName, a.lastConnURL = "Test Server", url
			seedCarryableLog(a, "stale")
			a.room = nil // no courtroom to freeze — the plain-teardown branch

			a.handleInvoluntaryDrop(reason)
			if a.screen != ScreenLobby {
				t.Fatalf("test setup: expected the plain teardown to lobby, got %v", a.screen)
			}

			a.Connect("Test Server", url)
			if a.conn == nil {
				t.Fatal("the redial to a real, reachable server must have succeeded")
			}
			if !logIsEmpty(a) {
				t.Errorf("%s must never carry a log, got icLog=%v oocLog=%v", reason, a.icLog, a.oocLog)
			}
		})
	}
}

// TestAutoReconnectCarriesLogAcrossAFailedThenSuccessfulRetry pins the
// trickiest part of the one-shot design: a transport drop with no courtroom
// to freeze (a roomless drop, or one caught off the courtroom screen) still
// snapshots the log because it's about to auto-retry — but a FAILED first
// attempt must not burn that one-shot slot, or the very next, successful
// retry would silently lose the log it earned.
func TestAutoReconnectCarriesLogAcrossAFailedThenSuccessfulRetry(t *testing.T) {
	a := froomApp(t)
	goodURL := wsTestServer(t)
	badURL := "ws://127.0.0.1:1" // nothing listens here: an immediate refused-connection failure
	a.serverName, a.serverKey = "Test Server", goodURL
	a.lastConnName, a.lastConnURL = "Test Server", goodURL
	seedCarryableLog(a, "before")
	a.room = nil // roomless drop: takes handleInvoluntaryDrop's plain-teardown branch
	a.d.Prefs.SetAutoReconnect(true)

	a.handleInvoluntaryDrop("connection closed")
	if a.screen != ScreenLobby {
		t.Fatalf("test setup: expected the plain teardown to lobby, got %v", a.screen)
	}
	if a.autoReconnectAt.IsZero() {
		t.Fatal("test setup: a genuine transport drop must arm auto-reconnect")
	}
	if !logIsEmpty(a) {
		t.Fatal("test setup: Disconnect must have wiped the live log before the retry")
	}
	if a.pendingSessionCarry == nil {
		t.Fatal("a roomless transport drop about to auto-retry must snapshot a carry")
	}

	// First retry: point it at an unreachable address so the dial fails. The
	// pending carry must survive this — it's the mistake test (d) in this
	// class of fix is built to catch.
	a.lastConnURL = badURL
	a.autoReconnectAt = a.now().Add(-1 * time.Second) // due now
	a.pollAutoReconnect()
	if a.conn != nil {
		t.Fatal("test setup: the bad address must fail to dial")
	}
	if a.pendingSessionCarry == nil {
		t.Fatal("a failed dial attempt must NOT consume the pending carry")
	}

	// Second retry: back to the real server. The carry must land now.
	a.lastConnURL = goodURL
	a.autoReconnectAt = a.now().Add(-1 * time.Second) // due now
	a.pollAutoReconnect()

	if a.conn == nil {
		t.Fatal("the retry against a real, reachable server must have succeeded")
	}
	if len(a.icLog) != 1 || a.icLog[0].text != "before-ic-line" {
		t.Errorf("icLog not restored on the successful retry, got %+v", a.icLog)
	}
	if len(a.oocLog) != 1 || a.oocLog[0] != "before-ooc-line" {
		t.Errorf("oocLog not restored on the successful retry, got %v", a.oocLog)
	}
	if a.pendingSessionCarry != nil {
		t.Error("the carry must be consumed (one-shot) after a successful apply")
	}
}

// TestReconnectToADifferentServerNeverCarriesTheLog is the encapsulation
// test for the carry seam: it drives the REAL production path end to end
// (handleInvoluntaryDrop captures a carry for server A; a normal Connect to
// a DIFFERENT server B follows) rather than re-implementing the key
// comparison — proving applySessionCarryIfPending's URL match, reached only
// from connectWith, is what actually gates every restoration. It also pins
// that the stale carry is discarded on the mismatch, not merely skipped: a
// LATER reconnect back to A must not resurrect it either.
func TestReconnectToADifferentServerNeverCarriesTheLog(t *testing.T) {
	a := froomApp(t)
	urlA := wsTestServer(t)
	urlB := wsTestServer(t)
	a.serverName, a.serverKey = "Server A", urlA
	a.lastConnName, a.lastConnURL = "Server A", urlA
	seedCarryableLog(a, "server-a")
	a.room = nil // roomless drop: takes the plain-teardown+carry branch
	a.d.Prefs.SetAutoReconnect(true)

	a.handleInvoluntaryDrop("connection closed")
	if a.pendingSessionCarry == nil {
		t.Fatal("test setup: expected server A's drop to snapshot a carry")
	}

	// The user doesn't wait for the auto-retry — they manually join a
	// DIFFERENT server (Connect cancels the pending auto-retry but must NOT
	// let A's stale carry follow them onto B).
	a.Connect("Server B", urlB)
	if a.conn == nil {
		t.Fatal("the join to server B must have succeeded")
	}
	if !logIsEmpty(a) {
		t.Errorf("server B's fresh session must start blank, not carry A's log, got icLog=%v oocLog=%v", a.icLog, a.oocLog)
	}
	if a.pendingSessionCarry != nil {
		t.Error("the mismatched carry must be discarded (one-shot), not left pending")
	}

	// Prove it can't resurface later either: leave B, rejoin A. If the carry
	// had merely been "skipped" instead of discarded, this redial would wake
	// it back up under the wrong session entirely.
	a.Disconnect()
	a.Connect("Server A", urlA)
	if a.conn == nil {
		t.Fatal("the rejoin to server A must have succeeded")
	}
	if !logIsEmpty(a) {
		t.Errorf("A's discarded carry must not resurface on a later rejoin, got icLog=%v oocLog=%v", a.icLog, a.oocLog)
	}
}

// TestSessionCarryDoesNotLeakIntoAParkedTab pins the multi-tab isolation
// rule: the carry is a single App-level slot, but it must only ever touch
// the ACTIVE session being redialed — a DIFFERENT, backgrounded tab's own
// parked log must be untouched by another tab's drop-and-reconnect.
func TestSessionCarryDoesNotLeakIntoAParkedTab(t *testing.T) {
	a := froomApp(t) // tab 0: active, in court
	urlA := wsTestServer(t)
	a.serverName, a.serverKey = "Server A", urlA
	a.lastConnName, a.lastConnURL = "Server A", urlA
	seedCarryableLog(a, "server-a")

	// Tab 1: a different, parked server with its OWN distinct log.
	parked := &courtTab{state: sessionState{
		serverName: "Server B",
		serverKey:  "ws://parked.example",
		icLog:      []icEntry{{text: "server-b-ic-line"}},
		oocLog:     []string{"server-b-ooc-line"},
	}}
	a.tabs = append(a.tabs, parked)

	a.handleInvoluntaryDrop("connection closed") // freezes tab 0 in place
	if !a.disconnectDlg.open {
		t.Fatal("test setup: expected the dialog to freeze open")
	}
	a.reconnectFromDisconnectDialog()

	if a.conn == nil {
		t.Fatal("the redial to a real, reachable server must have succeeded")
	}
	if len(a.icLog) != 1 || a.icLog[0].text != "server-a-ic-line" {
		t.Errorf("tab A's own log must be restored, got %+v", a.icLog)
	}
	// The parked tab must be byte-for-byte what it was — never overwritten by
	// tab A's carry, and never itself donated into tab A's restored session.
	if len(parked.state.icLog) != 1 || parked.state.icLog[0].text != "server-b-ic-line" {
		t.Errorf("parked tab B's log was disturbed, got %+v", parked.state.icLog)
	}
	if len(parked.state.oocLog) != 1 || parked.state.oocLog[0] != "server-b-ooc-line" {
		t.Errorf("parked tab B's OOC log was disturbed, got %v", parked.state.oocLog)
	}
}
