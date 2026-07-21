package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// wireBackgroundDeps fills in the render/assets deps that App.Background touches
// (drainWarnings/drainMusicFailures → Manager, Audio.Frame, Pump.Frame,
// Store.DrainDestroyQueue) so the FULL Background loop can be driven headlessly.
// Mirrors exportstall_test.go's manager wiring; skips when SDL/render targets are
// unavailable (the no-SDL CI path). Without this, Background nil-derefs the unset
// deps the moment it reaches the first drain.
func wireBackgroundDeps(t *testing.T, a *App) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	t.Cleanup(cleanup)
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store

	resolver := assets.NewResolver(a.d.Prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(1)
	t.Cleanup(decoder.Close)
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver: resolver,
		Prefs:    a.d.Prefs,
		T2:       t2,
		Disk:     disk,
		Source:   network.NewClient(),
		Pool:     pool,
		Decoder:  decoder,
	})
	a.d.Pump = render.NewPump(store, a.d.Manager, nil)
	// NewAudio (not a bare &render.Audio{}) so Audio.Frame's `<-a.mgr.Audio()` has a
	// non-nil manager to drain — a bare Audio nil-derefs there. The device init is
	// best-effort and disables gracefully with no audio subsystem (headless).
	a.d.Audio = render.NewAudio(a.d.Manager)
}

// TestAutoReconnectFiresFromBackground pins FIX 1: a due auto-reconnect must fire
// from a BACKGROUND pass (the minimized / unfocused-with-bg-off loop), not only
// from Frame. Before the fix pollAutoReconnect was called from exactly one site
// inside Frame, so a drop taken while popped out counted down to zero and sat
// there until the window was refocused. The observable is the same one the frozen
// -state test asserts: the redial path engaged (tries advanced) and — the dial
// failing against an unreachable server — it collapsed back to the lobby.
func TestAutoReconnectFiresFromBackground(t *testing.T) {
	a := froomApp(t)
	wireBackgroundDeps(t, a) // Background needs these or it nil-derefs before the poll
	a.d.Prefs.SetAutoReconnect(true)
	// A minimized-style drop: the courtroom is FROZEN under the involuntary-disconnect
	// dialog (the exact state a popped-out drop leaves, and the proven-safe redial
	// path), with a due retry armed. Background stamps frameNow itself, so a.now()
	// advances even though we never draw a Frame — this is the whole point of Fix 1.
	a.connErr = "connection closed"
	a.beginInvoluntaryDisconnect("connection closed", false)
	a.autoReconnectTries = 0
	a.autoReconnectAt = a.now().Add(-1 * time.Second) // due now

	a.Background(16 * time.Millisecond) // the popped-out loop — must fire the poll

	// The redial engaged: the attempt ran (tries advanced), and — with no reachable
	// server — it failed and re-surfaced the dialog (not a silent bare lobby; see
	// TestAutoReconnectFiresWhileFrozen for why a failure must re-open it) while the
	// screen itself collapsed to the lobby underneath. This proves the Background
	// call site drives the poll just like Frame does.
	if a.autoReconnectTries == 0 {
		t.Error("a due retry must FIRE from a Background pass (tries should have advanced)")
	}
	if !a.disconnectDlg.open {
		t.Error("a failed redial must re-open the dialog, not strand the user on a silent lobby")
	}
	if a.screen != ScreenLobby {
		t.Errorf("a failed background redial stays on the lobby, got %v", a.screen)
	}
}

// TestAutoReconnectDoublePollFiresOnce pins the idempotency guard FIX 1 relies on:
// polling from BOTH loops in one tick (Frame then Background, or vice versa) must
// dial at most once per due-time. There is no explicit "already dialing" flag —
// the guard is that the FIRST poll mutates autoReconnectAt (a failed dial pushes it
// ≥autoReconnectBase into the future), so the second poll early-returns on
// now().Before(autoReconnectAt). A regression that dialled unconditionally would
// advance tries twice here.
func TestAutoReconnectDoublePollFiresOnce(t *testing.T) {
	// Drive the redial through the proven frozen path (the same setup
	// TestAutoReconnectFiresWhileFrozen uses): the first poll tears the frozen
	// session down, dials, fails, and collapses to the lobby with the retry backed
	// off — a known-safe fire. The idempotency claim is about what the SECOND poll
	// (the other loop, same tick) does.
	a := froomApp(t)
	a.d.Prefs.SetAutoReconnect(true)
	a.connErr = "connection closed"
	a.beginInvoluntaryDisconnect("connection closed", false)
	a.autoReconnectTries = 0
	a.autoReconnectAt = a.now().Add(-1 * time.Second) // due now

	a.pollAutoReconnect() // Frame's call
	firstTries := a.autoReconnectTries
	if firstTries == 0 {
		t.Fatal("the first poll must fire the due retry")
	}
	// The dial failed and backed off: autoReconnectAt is now in the future, so the
	// immediately-following Background poll (same tick) sees an un-due retry.
	if a.autoReconnectAt.IsZero() || !a.now().Before(a.autoReconnectAt) {
		t.Fatalf("after a failed dial the retry must be rescheduled into the future, got %v (now %v)", a.autoReconnectAt, a.now())
	}
	a.pollAutoReconnect() // Background's call, same due window
	if a.autoReconnectTries != firstTries {
		t.Errorf("double-polling must not double-dial: tries went %d→%d in one due window", firstTries, a.autoReconnectTries)
	}
}

// TestPinnedPaneDeathSurfacesWarning pins FIX 2: when the PINNED float pane's
// server dies in the background, the pane vanishes (clearSplit) but its death must
// be ANNOUNCED — the server name + close reason on the warn line — so a torn-out
// server's disappearance is never silent. The existing parked-death latch behavior
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
		lastPing:   time.Now(), // skip the keepalive-ping branch (conn is closing)
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
	// FIX 2: the vanished pane's death is announced with the server name.
	if !strings.Contains(a.warnLine, "PinnedServer") {
		t.Errorf("the pinned-pane death must surface a warning naming the server, got warnLine=%q", a.warnLine)
	}
}
