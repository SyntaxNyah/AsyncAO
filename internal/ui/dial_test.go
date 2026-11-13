package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// awaitDial finishes the dial in progress, as a frame does once its result has arrived.
func awaitDial(t *testing.T, a *App) {
	t.Helper()
	d := a.dial
	if d == nil {
		t.Fatal("no dial in progress")
	}
	a.finishDial(d, <-d.res)
}

// stalledServer answers no WebSocket handshake until the test ends.
func stalledServer(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func lobbyApp(t *testing.T) *App {
	t.Helper()
	a := froomApp(t)
	a.Disconnect()
	return a
}

func TestConnectDoesNotWaitForTheServer(t *testing.T) {
	a := lobbyApp(t)
	a.Connect("Stalled", stalledServer(t))
	if a.dial == nil {
		t.Fatal("Connect must leave a dial in progress")
	}
	if a.conn != nil || a.sess != nil || a.screen != ScreenLobby {
		t.Fatalf("nothing may change until the dial finishes: conn=%v sess=%v screen=%v", a.conn, a.sess, a.screen)
	}
	a.pollDial()
	if a.dial == nil {
		t.Fatal("a poll with no result must keep waiting")
	}
}

func TestDialResultOpensTheSession(t *testing.T) {
	a := lobbyApp(t)
	url := wsTestServer(t)
	a.Connect("Test Server", url)
	awaitDial(t, a)
	if a.conn == nil || a.sess == nil || a.screen != ScreenCharSelect || a.serverKey != url {
		t.Fatalf("conn=%v sess=%v screen=%v key=%q", a.conn, a.sess, a.screen, a.serverKey)
	}
	if a.dial != nil {
		t.Error("the finished dial must be cleared")
	}
}

func TestCancelDialLeavesTheLobbyAlone(t *testing.T) {
	a := lobbyApp(t)
	a.Connect("Stalled", stalledServer(t))
	a.cancelDial()
	a.pollDial()
	if a.dial != nil || a.conn != nil || a.screen != ScreenLobby || a.connErr != "" {
		t.Fatalf("dial=%v conn=%v screen=%v err=%q", a.dial, a.conn, a.screen, a.connErr)
	}
}

func TestCancellingARetryStopsAutoReconnect(t *testing.T) {
	a := lobbyApp(t)
	a.lastConnName, a.lastConnURL = "Stalled", stalledServer(t)
	a.autoReconnectAt = a.now()
	a.pollAutoReconnect()
	if a.dial == nil {
		t.Fatal("setup: the due retry must start a dial")
	}
	a.cancelDial()
	if !a.autoReconnectAt.IsZero() {
		t.Error("Cancel must stop the retries, or the next one starts straight away")
	}
}

func TestDialingAnotherServerReplacesTheDial(t *testing.T) {
	a := lobbyApp(t)
	a.Connect("Stalled", stalledServer(t))
	url := wsTestServer(t)
	a.Connect("Test Server", url)
	awaitDial(t, a)
	if a.serverKey != url {
		t.Errorf("connected to %q, want the later choice %q", a.serverKey, url)
	}
}

func TestDialingTheSameServerKeepsTheDial(t *testing.T) {
	a := lobbyApp(t)
	url := stalledServer(t)
	a.Connect("Stalled", url)
	first := a.dial
	a.Connect("Stalled", url)
	if a.dial != first {
		t.Error("a second click must not restart the handshake")
	}
}

func TestTabRestoreDialsOneAtATime(t *testing.T) {
	a := lobbyApp(t)
	a.restoreQueue = []config.OpenTab{{Name: "A", URL: stalledServer(t)}, {Name: "B", URL: stalledServer(t)}}
	a.pumpTabRestore()
	first := a.dial
	a.pumpTabRestore()
	if a.dial != first || len(a.restoreQueue) != 1 {
		t.Errorf("the second tab must wait for the first: queue=%d", len(a.restoreQueue))
	}
}
