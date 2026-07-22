package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// froomApp builds a headless App parked in a courtroom: a rehearsal session
// (Ready, no char picked so loadCharINI/music resume stay inert), a live room, a
// viewport for buildRoom, and the applied theme matched so a Disconnect() in-test
// is a no-op (no async theme reload). One live tab holds the session.
func froomApp(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	name, _ := a.d.Prefs.Theme()
	a.themeAppliedName = name
	a.d.Viewport = render.NewViewport(nil)
	a.serverName, a.serverKey = "Test Server", "ws://test.example"
	a.lastConnName, a.lastConnURL = "Test Server", "ws://test.example"
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	a.room = courtroom.NewCourtroom(courtroom.URLBuilder{}, nil, a.sess, courtroom.NopAudio{})
	a.screen = ScreenCourtroom
	return a
}

// TestFriendlyDisconnectReason pins the raw→friendly mapping table: the known
// causes get a friendly line (raw always preserved underneath), and an unknown
// reason keeps friendly == "" so it shows raw alone — never a guessed label. This
// mapping still feeds the background/parked-tab dialog (see openDisconnectDialog).
func TestFriendlyDisconnectReason(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantFriend bool // a friendly line is expected
	}{
		{"kick", "Kicked: spamming", true},
		{"ban", "Banned: evasion", true},
		{"network unreachable", "dial tcp: network is unreachable", true},
		{"no route", "connect: no route to host", true},
		{"timeout", "protocol: i/o timeout", true},
		{"deadline", "context deadline exceeded", true},
		{"eof close", "protocol: reading: EOF", true},
		{"server going away", "websocket: close 1001 (going away)", true},
		{"reset by peer", "read: connection reset by peer", true},
		{"plain connection closed", "connection closed", true},
		{"connection lost prefix", "connection lost: sending CH failed", true},
		// Deliberately unknown: no friendly guess, raw shown alone.
		{"unknown gibberish", "kwyjibo 0x5f", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		got := friendlyDisconnectReason(tc.raw)
		if got.raw != tc.raw {
			t.Errorf("%s: raw not preserved: got %q, want %q", tc.name, got.raw, tc.raw)
		}
		if (got.friendly != "") != tc.wantFriend {
			t.Errorf("%s: friendly=%q, wantFriend=%v", tc.name, got.friendly, tc.wantFriend)
		}
	}
}

// TestHandleInvoluntaryDropGoesToLobby is the load-bearing table for the restored
// v1.70.0 behaviour: for a connection ending on the ACTIVE tab, handleInvoluntaryDrop
// always runs the plain teardown to the LOBBY with the reason shown — it never
// freezes the courtroom under the dialog. Auto-reconnect arms only for a genuine
// transport drop (not a kick/ban, not a deliberate close).
func TestHandleInvoluntaryDropGoesToLobby(t *testing.T) {
	cases := []struct {
		name        string
		reason      string
		deliberate  bool
		wantAutoArm bool
	}{
		{"transport drop → lobby + arms", "connection closed", false, true},
		{"kick → lobby, no auto-reconnect", "Kicked: rude", false, false},
		{"ban → lobby, no auto-reconnect", "Banned: cheating", false, false},
		{"deliberate close → lobby, no arm", "connection closed", true, false},
	}
	for _, tc := range cases {
		a := froomApp(t)
		a.d.Prefs.SetAutoReconnect(true)
		a.deliberateClose = tc.deliberate
		a.connErr = tc.reason // the caller sets connErr before the shared tail
		a.handleInvoluntaryDrop(tc.reason)
		if a.disconnectDlg.open {
			t.Errorf("%s: an active-tab drop must NEVER freeze under the dialog", tc.name)
		}
		if a.screen != ScreenLobby {
			t.Errorf("%s: a drop must land on the lobby, got %v", tc.name, a.screen)
		}
		if a.connErr != tc.reason {
			t.Errorf("%s: the lobby must show the reason, connErr=%q want %q", tc.name, a.connErr, tc.reason)
		}
		if gotArm := !a.autoReconnectAt.IsZero(); gotArm != tc.wantAutoArm {
			t.Errorf("%s: auto-reconnect armed=%v, want %v", tc.name, gotArm, tc.wantAutoArm)
		}
	}
}

// TestVoluntaryDisconnectGoesToLobby pins that the user's own Disconnect lands on the
// lobby with no dialog — a deliberate close is the plain teardown. Uses the SendErr
// drop path stand-in every deliberate caller shares (they all set deliberateClose).
func TestVoluntaryDisconnectGoesToLobby(t *testing.T) {
	a := froomApp(t)
	a.sess = courtroom.NewSession(func(protocol.Packet) error { return nil }, "")
	a.room = courtroom.NewCourtroom(courtroom.URLBuilder{}, nil, a.sess, courtroom.NopAudio{})
	a.deliberateClose = true // the user chose to leave
	a.handleInvoluntaryDrop("connection closed")
	if a.disconnectDlg.open {
		t.Error("a deliberate close must NEVER open the dialog")
	}
	if a.screen != ScreenLobby {
		t.Errorf("a deliberate close lands on the lobby, got %v", a.screen)
	}
}

// TestAutoReconnectRetriesFromLobbyNeverGivesUp pins the restored v1.70.0 retry: a
// due retry fires from the LOBBY (screen == lobby), and a genuine transport drop
// keeps retrying indefinitely — no give-up cutoff — so an AFK session self-heals
// when the server returns. With no reachable server the dial fails and backs off,
// staying armed the whole way through.
func TestAutoReconnectRetriesFromLobbyNeverGivesUp(t *testing.T) {
	a := froomApp(t)
	a.d.Prefs.SetAutoReconnect(true)
	a.screen = ScreenLobby // a drop already tore down to the lobby
	a.connErr = "connection closed"
	a.autoReconnectTries = 0
	a.autoReconnectAt = a.now().Add(-1 * time.Second) // due now

	const attempts = 10 // well past the 8-try cutoff a prior version enforced
	for i := 1; i <= attempts; i++ {
		a.pollAutoReconnect()
		if a.autoReconnectAt.IsZero() {
			t.Fatalf("attempt %d: auto-reconnect stopped retrying — it must never give up", i)
		}
		a.autoReconnectAt = a.now().Add(-1 * time.Second) // force the next retry due now
	}
	if a.autoReconnectTries != attempts {
		t.Errorf("expected %d attempts to have run, got %d", attempts, a.autoReconnectTries)
	}
}

// TestAutoReconnectSuppressedOffLobby pins that the foreground retry only fires from
// the lobby: while the user is anywhere else (a live courtroom, settings), a due
// retry does NOT fire — the restored Frame-only-from-lobby contract.
func TestAutoReconnectSuppressedOffLobby(t *testing.T) {
	a := froomApp(t) // screen == Courtroom
	a.autoReconnectTries = 0
	a.autoReconnectAt = a.now().Add(-1 * time.Second) // due now
	a.pollAutoReconnect()
	if a.autoReconnectTries != 0 {
		t.Error("a retry must NOT fire off the lobby (courtroom): it stays armed until the user returns")
	}
	if a.autoReconnectAt.IsZero() {
		t.Error("the armed retry must remain armed while off the lobby")
	}
}

// TestBackToLobbyLandsInPostDisconnectState pins the (background-tab) dialog's Back
// to lobby: it runs the real teardown, landing on the lobby with conn/sess/room
// gone, the dialog cleared (fence released), and any pending countdown cancelled.
func TestBackToLobbyLandsInPostDisconnectState(t *testing.T) {
	a := froomApp(t)
	a.openDisconnectDialog("Test Server", "ws://test.example", "connection closed", time.Time{})
	a.autoReconnectAt = a.now().Add(2 * time.Second) // a countdown was running
	a.closeDisconnectDialogToLobby()

	if a.disconnectDlg.open {
		t.Error("Back to lobby must clear the dialog (release its fence)")
	}
	if a.screen != ScreenLobby {
		t.Errorf("Back to lobby must land on the lobby, got %v", a.screen)
	}
	if a.sess != nil || a.room != nil || a.conn != nil {
		t.Error("Back to lobby runs the full teardown — session/room/conn must all be gone")
	}
	if !a.autoReconnectAt.IsZero() {
		t.Error("Back to lobby cancels any pending auto-reconnect (the user opted out)")
	}
}

// TestReconnectFromDialogRedials pins the (background-tab) dialog's Reconnect: it
// tears the frozen session fully down and redials the SAME server captured in the
// dialog through the normal Connect path — not whatever lastConn* held globally.
func TestReconnectFromDialogRedials(t *testing.T) {
	a := froomApp(t)
	a.openDisconnectDialog("Test Server", "ws://test.example", "connection closed", time.Time{})
	// A DIFFERENT server later became the global lastConn* (a second tab): Reconnect
	// must still target the server captured in the dialog, not this one.
	a.lastConnName, a.lastConnURL = "Other", "ws://other.example"

	a.reconnectFromDisconnectDialog()

	if a.disconnectDlg.open {
		t.Error("Reconnect must clear the dialog first (fence release)")
	}
	// Connect seeds lastConn* to the redial target; with no reachable server the dial
	// fails and lands on the lobby, but lastConn* proves which server was dialed.
	if a.lastConnURL != "ws://test.example" {
		t.Errorf("Reconnect must redial the dialog's server, got lastConnURL=%q", a.lastConnURL)
	}
	if a.screen != ScreenLobby {
		t.Errorf("a failed redial lands on the lobby, got %v", a.screen)
	}
}

// TestEscClosesDisconnectDialogToLobby pins the closeTopOverlay routing: Esc while
// the (background-tab) dialog is up must route through Back to lobby, so it reports
// handled AND we land on the lobby with the dialog cleared — never a frozen dead end.
func TestEscClosesDisconnectDialogToLobby(t *testing.T) {
	a := froomApp(t)
	a.openDisconnectDialog("Test Server", "ws://test.example", "connection closed", time.Time{})

	if !a.closeTopOverlay() {
		t.Fatal("Esc must be handled by closeTopOverlay while the dialog is up")
	}
	if a.disconnectDlg.open {
		t.Error("Esc must clear the dialog")
	}
	if a.screen != ScreenLobby {
		t.Errorf("Esc == Back to lobby: must land on the lobby, got %v", a.screen)
	}
}

// TestDialogCloseAlwaysReleasesFence pins the emoji-picker freeze class: EVERY path
// that closes the dialog must leave it closed, so the frame-tail fence (derived from
// disconnectDlg.open) is never left set with no dialog to own it.
func TestDialogCloseAlwaysReleasesFence(t *testing.T) {
	closers := map[string]func(a *App){
		"back to lobby": (*App).closeDisconnectDialogToLobby,
		"esc":           func(a *App) { a.closeTopOverlay() },
		"disconnect":    (*App).Disconnect, // a foreign teardown (e.g. quit) must also clear it
	}
	for name, close := range closers {
		a := froomApp(t)
		a.openDisconnectDialog("Test Server", "ws://test.example", "connection closed", time.Time{})
		a.ctx.fencePointer() // stand in for the frame-tail fence set while the dialog is up
		close(a)
		if a.disconnectDlg.open {
			t.Errorf("%s: the dialog must be closed so its fence releases (else a stuck freeze)", name)
		}
	}
}

// TestParkedTabDeathArmsWithoutActiveModal pins the parked-tab rule: a BACKGROUND
// tab's connection dying must mark the tab dead and LATCH its reason, but must NOT
// pop the dialog over whatever ACTIVE tab the user is looking at.
func TestParkedTabDeathArmsWithoutActiveModal(t *testing.T) {
	a := testTabApp(t)
	bg := &courtTab{state: sessionState{serverName: "Background", serverKey: "ws://bg"}, inCourt: true}
	a.tabs = []*courtTab{bg, {}}
	a.activeTab = 1
	a.routeBackgroundEvent(bg, courtroom.Event{Kind: courtroom.EventDisconnect, Text: "Kicked: afk"})
	if !bg.dead {
		t.Error("a background kick must mark the tab dead")
	}
	if bg.deadReason != "Kicked: afk" {
		t.Errorf("the reason must latch on the tab, got %q", bg.deadReason)
	}
	if a.disconnectDlg.open {
		t.Error("a PARKED tab's death must NOT open a modal over the active tab")
	}
}

// TestActivateDeadInCourtTabOpensDialog pins the other half of the parked rule:
// switching TO a tab that died in court surfaces the dialog over its restored
// courtroom (reason from the latch), rather than silently booting to the lobby.
func TestActivateDeadInCourtTabOpensDialog(t *testing.T) {
	a := froomApp(t) // active tab 0 is a live courtroom
	dead := &courtTab{
		state:   sessionState{serverName: "Dropped", serverKey: "ws://dropped"},
		dead:    true,
		inCourt: true,
	}
	dead.state.sess = courtroom.NewRehearsalSession("", []string{"Edgeworth"})
	dead.deadReason = "Banned: naughty"
	a.tabs = append(a.tabs, dead) // index 1

	a.activateTab(1)

	if !a.disconnectDlg.open {
		t.Fatal("activating a dead-in-court tab must open the disconnect dialog")
	}
	if a.screen != ScreenCourtroom {
		t.Errorf("the dialog sits over the tab's restored courtroom, screen=%v", a.screen)
	}
	if a.disconnectDlg.reason.raw != "Banned: naughty" {
		t.Errorf("the dialog must show the latched reason, got %q", a.disconnectDlg.reason.raw)
	}
	if a.tabs[a.activeTab].dead {
		t.Error("the dead flag must be consumed — the dialog now owns this drop")
	}
}

// TestTabBarInertUnderDialog pins the state-safety guard: while the (background-tab)
// dialog is up, handleTabBar must NOT act on a chip click — otherwise activating
// another tab would park a frozen session into a zombie slot while the dialog stayed
// up over the wrong server. The active tab must be unchanged after a simulated click.
func TestTabBarInertUnderDialog(t *testing.T) {
	a := froomApp(t)
	a.tabs = append(a.tabs, &courtTab{state: sessionState{serverName: "Other", serverKey: "ws://other"}})
	a.openDisconnectDialog("Test Server", "ws://test.example", "connection closed", time.Time{})
	before := a.activeTab
	a.ctx.mouseDown = true // a press this frame, as a chip click would present
	a.handleTabBar(1000, 40)
	if a.activeTab != before {
		t.Errorf("the strip must be inert under the dialog (active tab changed %d→%d)", before, a.activeTab)
	}
	if !a.disconnectDlg.open {
		t.Error("the dialog must still be up (the click must not have resolved it)")
	}
}
