package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestPumpConnectionSurvivesServerKick pins two coupled fixes around a server
// kick/ban packet (KK/KB/BD → courtroom.EventDisconnect):
func TestPumpConnectionSurvivesServerKick(t *testing.T) {
	// A minimal AO server: accept, immediately kick, then hold the socket open
	// (so the KK frame — not a socket close — is what drives the disconnect).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		_ = ws.Write(ctx, websocket.MessageText, []byte("KK#you are kicked#%"))
		<-ctx.Done() // keep the connection up until the test tears the server down
	}))
	defer srv.Close()

	conn, err := protocol.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	a := testTabApp(t)
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			AutoReconnect =

			true

	})

	// on, to prove a kick STILL doesn't rearm (asserted below)
	// Neutralize the async theme reload Disconnect()→ensureThemeForSession would
	// otherwise kick off: with the applied name already matching the pref, it's a
	// no-op (themeAppliedName lives on App, so resetSessionState won't clear it).
	name, _ := a.d.Prefs.Load().Theme()
	a.toolbox.themeAppliedName = name
	// Simulate a live connection to a named server, as connectWith would leave it,
	// so Disconnect captures the reconnect target.
	a.srvMeta.serverName, a.srvMeta.serverKey = "Test Server", "ws://test.example"
	a.link.conn = conn
	a.link.sess = courtroom.NewSession(func(protocol.Packet) error { return nil }, "test-hdid")

	// Drain until the kick lands. The first pumps may see an empty queue (the
	// read loop hasn't delivered KK yet) and return via the select default with
	// conn still live; once KK arrives the drain disconnects and nils a.conn.
	deadline := time.Now().Add(5 * time.Second)
	for a.link.conn != nil && time.Now().Before(deadline) {
		a.pumpConnection() // must not panic when the session is freed mid-drain
		time.Sleep(2 * time.Millisecond)
	}
	// The disconnect must have actually run (so the loop's re-check was the
	// thing under test, not a timeout): conn AND sess are torn down.
	if a.link.conn != nil {
		t.Fatal("server kick (KK) should have disconnected the session")
	}
	if a.link.sess != nil {
		t.Error("Disconnect should have torn the session down (sess != nil)")
	}
	// Fix #2: the kick reason and reconnect state must SURVIVE Disconnect.
	if a.conn.connErr != "Kicked: you are kicked" {
		t.Errorf("connErr = %q, want %q (reason must outlive Disconnect)", a.conn.connErr, "Kicked: you are kicked")
	}
	if a.conn.lastConnURL != "ws://test.example" {
		t.Errorf("lastConnURL = %q, want %q (Reconnect target must survive)", a.conn.lastConnURL, "ws://test.example")
	}
	// #1: a kick must NOT arm auto-reconnect (retrying reads as ban evasion / bad
	// optics). The manual Reconnect button still works — that's the lastConnURL above.
	if !a.conn.autoReconnectAt.IsZero() {
		t.Error("a server kick must NOT arm auto-reconnect (autoReconnectAt should stay zero)")
	}
}
