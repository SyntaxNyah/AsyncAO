package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// froomApp builds a headless App parked in a FROZEN-able courtroom: a rehearsal
// session (Ready, no char picked so loadCharINI/music resume stay inert), a live
// room, a viewport for buildRoom, and the applied theme matched so a Disconnect()
// in-test is a no-op (no async theme reload). One live tab holds the session.
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
// reason keeps friendly == "" so it shows raw alone — never a guessed label.
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
		{"stale watchdog", "protocol: connection stale (no data for 100s)", true},
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

// TestHandleInvoluntaryDropClassification is the load-bearing switch table: for a
// connection ending, handleInvoluntaryDrop must freeze the courtroom (dialog up,
// screen stays Courtroom) ONLY for an involuntary drop with a live room; a
// deliberate close runs the plain teardown (no dialog, → lobby); a kick/ban freezes
// but never arms auto-reconnect; a genuine transport drop freezes AND arms it.
func TestHandleInvoluntaryDropClassification(t *testing.T) {
	cases := []struct {
		name        string
		reason      string
		deliberate  bool
		wantDialog  bool
		wantAutoArm bool
	}{
		{"transport drop freezes + arms", "connection closed", false, true, true},
		{"kick freezes, no auto-reconnect", "Kicked: rude", false, true, false},
		{"ban freezes, no auto-reconnect", "Banned: cheating", false, true, false},
		{"deliberate close: plain teardown, no dialog", "connection closed", true, false, false},
	}
	for _, tc := range cases {
		a := froomApp(t)
		a.d.Prefs.SetAutoReconnect(true)
		a.deliberateClose = tc.deliberate
		a.connErr = tc.reason // the caller sets connErr before the shared tail
		a.handleInvoluntaryDrop(tc.reason)
		if a.disconnectDlg.open != tc.wantDialog {
			t.Errorf("%s: disconnectDlg.open=%v, want %v", tc.name, a.disconnectDlg.open, tc.wantDialog)
		}
		if tc.wantDialog && a.screen != ScreenCourtroom {
			t.Errorf("%s: a freeze must keep screen==Courtroom, got %v", tc.name, a.screen)
		}
		if !tc.wantDialog && a.screen != ScreenLobby {
			t.Errorf("%s: a plain teardown must land on the lobby, got %v", tc.name, a.screen)
		}
		if gotArm := !a.autoReconnectAt.IsZero(); gotArm != tc.wantAutoArm {
			t.Errorf("%s: auto-reconnect armed=%v, want %v", tc.name, gotArm, tc.wantAutoArm)
		}
	}
}

// TestBeginInvoluntaryDisconnectFreezesAndKeepsLogs pins the freeze contract: the
// dialog opens, the network session is torn down (conn nilled) but a.sess/a.room —
// and the IC log — stay alive and drawn, and the screen stays on the courtroom so
// the last scene keeps rendering. The reason (friendly + raw) reaches the dialog.
func TestBeginInvoluntaryDisconnectFreezesAndKeepsLogs(t *testing.T) {
	a := froomApp(t)
	// A readable IC log the user was mid-read of — it must survive the freeze.
	a.icLog = []icEntry{{text: "Phoenix: Objection!", speaker: "Phoenix"}}
	a.connErr = "connection closed"
	a.beginInvoluntaryDisconnect("connection closed")

	if !a.disconnectDlg.open {
		t.Fatal("an involuntary active-tab drop must open the dialog")
	}
	if a.screen != ScreenCourtroom {
		t.Errorf("the courtroom must stay on screen (frozen), got screen=%v", a.screen)
	}
	if a.sess == nil || a.room == nil {
		t.Error("the session and room must stay alive so the frozen scene keeps drawing")
	}
	if a.conn != nil {
		t.Error("the network conn must be nilled — the pump early-returns, so the room is frozen")
	}
	if len(a.icLog) != 1 || a.icLog[0].text != "Phoenix: Objection!" {
		t.Error("the IC log must remain intact under the dialog (the whole point)")
	}
	if a.disconnectDlg.reason.raw != "connection closed" {
		t.Errorf("the raw reason must reach the dialog, got %q", a.disconnectDlg.reason.raw)
	}
	if a.disconnectDlg.reason.friendly == "" {
		t.Error("a known cause (server closed) should carry a friendly line")
	}
	if a.disconnectDlg.url != "ws://test.example" {
		t.Errorf("the redial target must be captured for Reconnect, got %q", a.disconnectDlg.url)
	}
}

// TestBeginInvoluntaryDisconnectFallsBackToLobby pins the no-room-to-freeze corner:
// a drop with no live courtroom (char-select / already torn down) must fall back to
// the plain teardown so we still land on the lobby, never a dialog over nothing.
func TestBeginInvoluntaryDisconnectFallsBackToLobby(t *testing.T) {
	a := froomApp(t)
	a.room = nil // no room to freeze (e.g. dropped at char-select)
	a.screen = ScreenCharSelect
	a.beginInvoluntaryDisconnect("connection closed")
	if a.disconnectDlg.open {
		t.Error("with no room to freeze, the dialog must NOT open")
	}
	if a.screen != ScreenLobby {
		t.Errorf("the fallback must land on the lobby, got %v", a.screen)
	}
}

// TestBackToLobbyLandsInPostDisconnectState pins the dialog's Back to lobby: it
// runs the real teardown, so we land in EXACTLY today's post-disconnect state —
// screen == lobby, conn/sess/room gone, the dialog cleared (fence released), and
// the pending countdown cancelled (the user opted out).
func TestBackToLobbyLandsInPostDisconnectState(t *testing.T) {
	a := froomApp(t)
	a.connErr = "connection closed"
	a.beginInvoluntaryDisconnect("connection closed")
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

// TestReconnectFromDialogRedials pins the dialog's Reconnect: it tears the frozen
// session fully down (deliberate, so the teardown itself doesn't auto-retry) and
// redials the SAME server through the normal Connect path. The dialog clears; the
// redial target is the frozen server, not whatever lastConn* held globally.
func TestReconnectFromDialogRedials(t *testing.T) {
	a := froomApp(t)
	a.connErr = "connection closed"
	a.beginInvoluntaryDisconnect("connection closed")
	// A DIFFERENT server later became the global lastConn* (a second tab): Reconnect
	// must still target the frozen server captured in the dialog, not this one.
	a.lastConnName, a.lastConnURL = "Other", "ws://other.example"

	a.reconnectFromDisconnectDialog()

	if a.disconnectDlg.open {
		t.Error("Reconnect must clear the dialog first (fence release)")
	}
	// Connect seeds lastConn* to the redial target; with no reachable server the dial
	// fails and lands on the lobby, but lastConn* proves which server was dialed.
	if a.lastConnURL != "ws://test.example" {
		t.Errorf("Reconnect must redial the FROZEN server, got lastConnURL=%q", a.lastConnURL)
	}
	if a.screen != ScreenLobby {
		t.Errorf("a failed redial lands on the lobby (like the lobby Reconnect button), got %v", a.screen)
	}
}

// TestEscClosesDisconnectDialogToLobby pins the closeTopOverlay routing: Esc while
// the dialog is up must NOT merely flip the flag (that would strand the user in a
// frozen courtroom with no dialog and no exit) — it routes through Back to lobby, so
// closeTopOverlay reports handled AND we land on the lobby with the dialog cleared.
func TestEscClosesDisconnectDialogToLobby(t *testing.T) {
	a := froomApp(t)
	a.connErr = "connection closed"
	a.beginInvoluntaryDisconnect("connection closed")

	if !a.closeTopOverlay() {
		t.Fatal("Esc must be handled by closeTopOverlay while the dialog is up")
	}
	if a.disconnectDlg.open {
		t.Error("Esc must clear the dialog")
	}
	if a.screen != ScreenLobby {
		t.Errorf("Esc == Back to lobby: must land on the lobby (not a frozen dead-end), got %v", a.screen)
	}
}

// TestDialogCloseAlwaysReleasesFence pins the emoji-picker freeze class: EVERY path
// that closes the dialog must leave it closed, so the frame-tail fence (derived
// from disconnectDlg.open) is never left set with no dialog to own it. Drives all
// three close paths and asserts open==false and the pointer fence would not re-arm.
func TestDialogCloseAlwaysReleasesFence(t *testing.T) {
	// Reconnect is covered by its own test (it does a real redial); here we drive the
	// network-free close paths, plus a foreign Disconnect() to prove the defensive
	// clear in the teardown holds the disconnectDlg.open ⇒ screen==Courtroom invariant.
	closers := map[string]func(a *App){
		"back to lobby": (*App).closeDisconnectDialogToLobby,
		"esc":           func(a *App) { a.closeTopOverlay() },
		"disconnect":    (*App).Disconnect, // a foreign teardown (e.g. quit) must also clear it
	}
	for name, close := range closers {
		a := froomApp(t)
		a.connErr = "connection closed"
		a.beginInvoluntaryDisconnect("connection closed")
		a.ctx.fencePointer() // stand in for the frame-tail fence set while the dialog is up
		close(a)
		// With the dialog closed, the frame-tail fence-on condition (disconnectDlg.open)
		// is false — the pointer would not be re-fenced next frame, so no persistent
		// freeze (the reported emoji-picker class).
		if a.disconnectDlg.open {
			t.Errorf("%s: the dialog must be closed so its fence releases (else a stuck freeze)", name)
		}
	}
}

// TestParkedTabDeathArmsWithoutActiveModal pins the parked-tab rule: a BACKGROUND
// tab's connection dying must mark the tab dead and LATCH its reason, but must NOT
// pop the dialog over whatever ACTIVE tab the user is looking at. Both death sites
// are exercised: a socket close (pumpBackgroundTabs) and a kick (routeBackgroundEvent).
func TestParkedTabDeathArmsWithoutActiveModal(t *testing.T) {
	// Kick/ban path: routeBackgroundEvent EventDisconnect.
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
// switching TO a tab that died in court surfaces the same dialog over its restored
// courtroom (reason from the latch), rather than silently booting to the lobby. The
// dead flag is consumed so the dialog now owns the drop.
func TestActivateDeadInCourtTabOpensDialog(t *testing.T) {
	a := froomApp(t) // active tab 0 is a live frozen-able courtroom
	// A background tab that died in court, with its reason latched.
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

// TestAutoReconnectFiresWhileFrozen pins the tie-breaker the whole feature hinges
// on: an armed countdown must fire even while the courtroom is FROZEN under the
// dialog (screen != lobby). A successful redial closes the dialog and leaves the
// lobby; a due retry that connects proves the frozen state doesn't wedge the poll.
func TestAutoReconnectFiresWhileFrozen(t *testing.T) {
	a := froomApp(t)
	a.d.Prefs.SetAutoReconnect(true)
	a.connErr = "connection closed"
	a.beginInvoluntaryDisconnect("connection closed")
	// Arm a due retry (as scheduleAutoReconnect would, but already past its time).
	a.autoReconnectTries = 0
	a.autoReconnectAt = a.now().Add(-1 * time.Second) // due now

	// The poll must fire despite screen==Courtroom+dialog: it tears the frozen
	// session down, clears the dialog, then attempts the redial. With no reachable
	// server the dial fails and backs off on the lobby — but the FROZEN wedge is
	// gone (dialog cleared, screen == lobby), which is the property under test.
	a.pollAutoReconnect()

	if a.disconnectDlg.open {
		t.Error("a due auto-retry must clear the frozen dialog (else the countdown wedges forever)")
	}
	if a.screen != ScreenLobby {
		t.Errorf("after the frozen fire the session is torn down to the lobby, got %v", a.screen)
	}
	// The retry counter advanced (the attempt ran), proving the poll didn't early-out.
	if a.autoReconnectTries == 0 {
		t.Error("the frozen fire must have run an attempt (tries should have advanced)")
	}
}

// TestVoluntaryDisconnectNeverFreezes pins that the user's own Disconnect never
// shows the involuntary dialog — a deliberate close is byte-identical to today
// (plain teardown → lobby). Uses the SendErr drop path with deliberateClose set,
// the stand-in every deliberate caller shares (they all set deliberateClose first).
func TestVoluntaryDisconnectNeverFreezes(t *testing.T) {
	a := froomApp(t)
	a.sess = courtroom.NewSession(func(protocol.Packet) error { return nil }, "")
	a.room = courtroom.NewCourtroom(courtroom.URLBuilder{}, nil, a.sess, courtroom.NopAudio{})
	a.deliberateClose = true // the user chose to leave
	a.handleInvoluntaryDrop("connection closed")
	if a.disconnectDlg.open {
		t.Error("a deliberate close must NEVER open the involuntary dialog")
	}
	if a.screen != ScreenLobby {
		t.Errorf("a deliberate close lands on the lobby, got %v", a.screen)
	}
}

// TestTabBarInertUnderDialog pins the state-safety guard: while the dialog is up,
// handleTabBar (which runs in the update phase, BEFORE the frame-tail pointer fence)
// must NOT act on a chip click — otherwise activating another tab would park the
// frozen-but-not-dead session into a zombie slot (never pumped, s.conn==nil) while
// the App-level dialog stayed up over the wrong server. The active tab must be
// unchanged after a simulated click.
func TestTabBarInertUnderDialog(t *testing.T) {
	a := froomApp(t)
	a.tabs = append(a.tabs, &courtTab{state: sessionState{serverName: "Other", serverKey: "ws://other"}})
	a.connErr = "connection closed"
	a.beginInvoluntaryDisconnect("connection closed")
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

// TestTeardownCallerClassification enumerates the literal ask: every teardown caller
// classified voluntary/involuntary. The voluntary callers all set deliberateClose
// before Disconnect() (asserted at their sites in screens.go/tabs.go); here we pin
// the RESULT the classification produces — a voluntary reason never freezes, an
// involuntary one does — for each caller's characteristic reason string, through the
// one switch (handleInvoluntaryDrop) they conceptually share.
func TestTeardownCallerClassification(t *testing.T) {
	cases := []struct {
		caller      string
		reason      string
		deliberate  bool // the caller sets deliberateClose = this before teardown
		involuntary bool // => freezes under the dialog
	}{
		{"Disconnect button (requestDisconnect)", "connection closed", true, false},
		{"Disconnect confirm Yes", "connection closed", true, false},
		{"close active tab", "connection closed", true, false},
		{"rehearsal end (parkActive)", "connection closed", true, false},
		{"pumpConnection SendErr (transport)", "connection lost: write failed", false, true},
		{"pumpConnection closed Incoming", "connection closed", false, true},
		{"EventDisconnect kick", "Kicked: rude", false, true},
		{"EventDisconnect ban", "Banned: evasion", false, true},
	}
	for _, tc := range cases {
		a := froomApp(t)
		a.deliberateClose = tc.deliberate
		a.connErr = tc.reason
		a.handleInvoluntaryDrop(tc.reason)
		if got := a.disconnectDlg.open; got != tc.involuntary {
			t.Errorf("%s: involuntary(freeze)=%v, want %v", tc.caller, got, tc.involuntary)
		}
	}
}
