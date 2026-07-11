package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestShouldAutoReconnect pins the pure drop-vs-ban-vs-user-close decision (#1) —
// the single source of truth the pumpConnection drop paths and the EventDisconnect
// handler all consult. Deliberate closes and server kicks/bans never reconnect;
// every other end (a genuine transport drop) does.
func TestShouldAutoReconnect(t *testing.T) {
	cases := []struct {
		name       string
		reason     string
		deliberate bool
		want       bool
	}{
		{"transport drop", "connection closed", false, true},
		{"read error drop", "protocol: reading: EOF", false, true},
		{"stale watchdog drop", "protocol: connection stale (no data for 100s, ping failed: ...)", false, true},
		{"write failure drop", "connection lost: protocol: sending CH: ...", false, true},
		{"server ban", "Banned: you are banned", false, false},
		{"server kick", "Kicked: you are kicked", false, false},
		{"user disconnect (drop path)", "connection closed", true, false},
		{"user disconnect wins over benign reason", "connection closed", true, false},
	}
	for _, tc := range cases {
		if got := shouldAutoReconnect(tc.reason, tc.deliberate); got != tc.want {
			t.Errorf("%s: shouldAutoReconnect(%q, %v) = %v, want %v",
				tc.name, tc.reason, tc.deliberate, got, tc.want)
		}
	}
}

// TestPumpConnectionReconnectsOnTransportDrop pins the #1 fix from the other side
// of the kick test: when the SOCKET closes (a genuine drop — server yanks the
// connection, no kick/ban packet), pumpConnection must schedule an auto-reconnect,
// not merely Disconnect. This is the exact case the feature was built for and used
// to miss entirely.
func TestPumpConnectionReconnectsOnTransportDrop(t *testing.T) {
	// A server that greets, waits for the client's first packet, then abruptly
	// drops the socket (CloseNow, no close frame) — a transport drop, not a kick.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = ws.Write(r.Context(), websocket.MessageText, []byte("decryptor#34#%"))
		_, _, _ = ws.Read(r.Context()) // wait for the client's HI
		_ = ws.CloseNow()              // yank the socket — surfaces as Incoming closing with an error
	}))
	defer srv.Close()

	conn, err := protocol.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	a := testTabApp(t)
	a.d.Prefs.SetAutoReconnect(true)
	// Match the applied theme so Disconnect()→ensureThemeForSession is a no-op
	// (no async theme reload in a headless test), as the kick test does.
	name, _ := a.d.Prefs.Theme()
	a.themeAppliedName = name
	a.serverName, a.serverKey = "Test Server", "ws://test.example"
	a.lastConnName, a.lastConnURL = "Test Server", "ws://test.example"
	a.conn = conn
	a.sess = courtroom.NewSession(func(p protocol.Packet) error {
		return conn.Send(context.Background(), p)
	}, "test-hdid")

	// Prod the server into dropping: send one packet, then pump until the drop
	// lands. The keepalive Ping in pumpConnection also writes, but sending HI
	// explicitly makes the server's Read return promptly.
	if err := conn.Send(context.Background(), protocol.NewPacket("HI", "x")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for a.conn != nil && time.Now().Before(deadline) {
		a.pumpConnection()
		time.Sleep(5 * time.Millisecond)
	}
	if a.conn != nil {
		t.Fatal("a dropped socket should have disconnected the session")
	}
	// The #1 fix: a genuine transport drop arms auto-reconnect (the old code only
	// Disconnected and never rescheduled).
	if a.autoReconnectAt.IsZero() {
		t.Error("a transport drop must arm auto-reconnect (autoReconnectAt is zero)")
	}
	if a.connErr == "" {
		t.Error("the drop reason should be shown in the lobby (connErr is empty)")
	}
}

// TestPumpConnectionSurfacesHalfDeadWrite pins #7a: once Session.SendErr is
// non-nil (the write side detected a dead socket), pumpConnection surfaces it —
// disconnecting and arming a reconnect — instead of silently swallowing every
// later outgoing packet. Driven with a session whose send func always fails, so
// the first keepalive/reply records sendErr.
func TestPumpConnectionSurfacesHalfDeadWrite(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetAutoReconnect(true)
	name, _ := a.d.Prefs.Theme()
	a.themeAppliedName = name
	a.serverName, a.serverKey = "Test Server", "ws://test.example"
	a.lastConnName, a.lastConnURL = "Test Server", "ws://test.example"

	// A real, live conn (server holds the socket open) so a.conn != nil — but we
	// force the WRITE side to fail via a failing send func, so pumpConnection
	// takes the SendErr branch before the read side ever closes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		<-r.Context().Done()
	}))
	defer srv.Close()
	conn, err := protocol.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	a.conn = conn

	sendFails := func(protocol.Packet) error { return context.DeadlineExceeded }
	a.sess = courtroom.NewSession(sendFails, "test-hdid")
	// Record a write failure into the session (as a failed keepalive/reply would).
	a.sess.Ping()
	if a.sess.SendErr() == nil {
		t.Fatal("test setup: the failing send should have recorded SendErr")
	}

	a.pumpConnection() // must notice SendErr, disconnect, and arm a reconnect
	if a.conn != nil {
		t.Fatal("a half-dead write (SendErr) should have disconnected the session")
	}
	if a.autoReconnectAt.IsZero() {
		t.Error("a half-dead write must arm auto-reconnect (it's a transport failure, not a ban)")
	}
	if a.connErr == "" {
		t.Error("the half-dead reason should be shown in the lobby (connErr is empty)")
	}
}

// TestDeliberateDisconnectDoesNotReconnect pins the anti-regression trap: a
// user-initiated Disconnect (deliberateClose set) must NOT arm a reconnect even
// though it flows through the same teardown. Uses the SendErr path as the
// close-trigger stand-in (both drop paths gate on shouldAutoReconnect the same way).
func TestDeliberateDisconnectDoesNotReconnect(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetAutoReconnect(true)
	name, _ := a.d.Prefs.Theme()
	a.themeAppliedName = name
	a.serverName, a.serverKey = "Test Server", "ws://test.example"
	a.lastConnName, a.lastConnURL = "Test Server", "ws://test.example"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		<-r.Context().Done()
	}))
	defer srv.Close()
	conn, err := protocol.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	a.conn = conn
	a.sess = courtroom.NewSession(func(protocol.Packet) error { return context.DeadlineExceeded }, "test-hdid")
	a.sess.Ping() // record a write failure so pumpConnection takes the SendErr branch

	a.deliberateClose = true // the user meant to leave
	a.pumpConnection()
	if a.conn != nil {
		t.Fatal("the teardown should still have run")
	}
	if !a.autoReconnectAt.IsZero() {
		t.Error("a deliberate close must NOT arm auto-reconnect (autoReconnectAt should stay zero)")
	}
}
