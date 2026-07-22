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

// TestAutoReconnectIdempotentInDueWindow pins that pollAutoReconnect dials at most
// once per due-time: there is no explicit "already dialing" flag — the guard is that
// the FIRST poll mutates autoReconnectAt (a failed dial pushes it ≥autoReconnectBase
// into the future), so a second poll in the same tick early-returns on
// now().Before(autoReconnectAt). A regression that dialled unconditionally would
// advance tries twice here.
func TestAutoReconnectIdempotentInDueWindow(t *testing.T) {
	a := froomApp(t)
	a.d.Prefs.SetAutoReconnect(true)
	a.screen = ScreenLobby // the retry fires only from the lobby (v1.70.0 behaviour)
	a.connErr = "connection closed"
	a.autoReconnectTries = 0
	a.autoReconnectAt = a.now().Add(-1 * time.Second) // due now

	a.pollAutoReconnect()
	firstTries := a.autoReconnectTries
	if firstTries == 0 {
		t.Fatal("the first poll must fire the due retry")
	}
	// The dial failed and backed off: autoReconnectAt is now in the future, so an
	// immediately-following poll (same tick) sees an un-due retry.
	if a.autoReconnectAt.IsZero() || !a.now().Before(a.autoReconnectAt) {
		t.Fatalf("after a failed dial the retry must be rescheduled into the future, got %v (now %v)", a.autoReconnectAt, a.now())
	}
	a.pollAutoReconnect() // same due window
	if a.autoReconnectTries != firstTries {
		t.Errorf("double-polling must not double-dial: tries went %d→%d in one due window", firstTries, a.autoReconnectTries)
	}
}

// TestPinnedPaneDeathSurfacesWarning pins the pinned-pane recovery: when the PINNED
// float pane's server dies in the background, the pane vanishes (clearSplit) but its
// death must be ANNOUNCED — the server name + close reason on the warn line — so a
// torn-out server's disappearance is never silent. The parked-death latch behavior
// (tab marked dead, reason latched, no modal over the active tab) is unchanged.
func TestPinnedPaneDeathSurfacesWarning(t *testing.T) {
	// A minimal AO server that accepts the websocket then immediately closes it, so
	// the parked tab's Incoming() channel closes and pumpBackgroundTabs hits its
	// dead (!ok) branch — the socket-close death, not a kick.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = ws.Close(websocket.StatusNormalClosure, "server going away")
	}))
	defer srv.Close()

	conn, err := protocol.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	a := testTabApp(t)
	// A background tab pinned as the float pane, on the live-then-closing conn.
	pinned := &courtTab{state: sessionState{
		serverName: "PinnedServer",
		serverKey:  "ws://pinned",
		conn:       conn,
		sess:       courtroom.NewSession(func(protocol.Packet) error { return nil }, ""),
	}}
	a.tabs = []*courtTab{pinned, {}}
	a.activeTab = 1 // the user is looking at a DIFFERENT tab
	a.splitTab = pinned

	// Drain until the closed socket is observed and the tab dies. The first passes
	// may see an empty queue before the close frame arrives.
	deadline := time.Now().Add(5 * time.Second)
	for !pinned.dead && time.Now().Before(deadline) {
		a.pumpBackgroundTabs()
		time.Sleep(2 * time.Millisecond)
	}

	if !pinned.dead {
		t.Fatal("the pinned tab's closed socket should have marked it dead")
	}
	if pinned.deadReason == "" {
		t.Error("the death reason must latch on the tab (existing parked-death behavior)")
	}
	if a.splitTab != nil {
		t.Error("the pinned pane must be cleared when its server dies (existing behavior)")
	}
	if a.disconnectDlg.open {
		t.Error("a parked pinned-tab death must NOT pop a modal over the active tab (unchanged)")
	}
	// The vanished pane's death is announced with the server name.
	if !strings.Contains(a.warnLine, "PinnedServer") {
		t.Errorf("the pinned-pane death must surface a warning naming the server, got warnLine=%q", a.warnLine)
	}
}
