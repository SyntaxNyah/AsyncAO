package ui

// Player-row "…" menu pins: which actions a row offers (and how the UI…
// hide-prefs and the spectator rule trim them), plus the modal-fence
// lifecycle — including the forced close on a tab switch, so a menu opened
// on one server can never act on another tab's session.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/veandco/go-sdl2/sdl"
)

// menuKinds flattens the open menu's item kinds for assertion.
func menuKinds(a *App) map[rosterMenuKind]bool {
	out := map[rosterMenuKind]bool{}
	for _, it := range a.rosterMenuItems {
		out[it.kind] = true
	}
	return out
}

// TestRosterMenuItems pins the full item set for a rich row (live UID + IPID +
// session), the spectator trims, and the UI…-popup hide prefs.
func TestRosterMenuItems(t *testing.T) {
	a := testTabApp(t)
	a.screen = ScreenCourtroom
	a.serverKey = "ws://test"
	a.sess = courtroom.NewSession(func(p protocol.Packet) error { return nil }, "")
	p := areaPlayer{uid: "7", name: "Phoenix", ipid: "AbCd1234"}

	a.openRosterMenu(&p, false, false, sdl.Point{X: 10, Y: 10})
	if !a.rosterMenuOpen {
		t.Fatal("a rich row must open the menu")
	}
	got := menuKinds(a)
	for _, want := range []rosterMenuKind{rosterActMessage, rosterActPair, rosterActCopyUID, rosterActCopyIPID, rosterActFriend, rosterActIgnore} {
		if !got[want] {
			t.Errorf("menu missing kind %d (items=%v)", want, a.rosterMenuItems)
		}
	}
	if got[rosterActPairManual] {
		t.Error("a live-UID row must offer the direct Pair, not the manual popup")
	}
	if got[rosterActFollow] {
		t.Error("Follow must stay out of the menu while the header toggle is off")
	}

	// Spectators: no Message (DM threads key on the character name — ambiguous),
	// no Pair-manual; the UID copy still works when a live UID exists.
	a.rosterMenuOpen = false
	spec := areaPlayer{uid: "9", name: "Spectator"}
	a.openRosterMenu(&spec, false, true, sdl.Point{})
	got = menuKinds(a)
	if got[rosterActMessage] || got[rosterActPairManual] {
		t.Errorf("spectator rows must not offer Message / manual Pair (items=%v)", a.rosterMenuItems)
	}
	if !got[rosterActCopyUID] {
		t.Error("a spectator with a live UID keeps Copy UID")
	}

	// UI… hide prefs trim menu entries (same ids that hid the old buttons).
	a.rosterMenuOpen = false
	a.hidden = map[string]bool{rosterBtnPair: true, rosterBtnUID: true, rosterBtnIPID: true, rosterBtnIgnore: true}
	a.openRosterMenu(&p, false, false, sdl.Point{})
	got = menuKinds(a)
	for _, hidden := range []rosterMenuKind{rosterActPair, rosterActCopyUID, rosterActCopyIPID, rosterActIgnore} {
		if got[hidden] {
			t.Errorf("hidden action kind %d still in the menu (items=%v)", hidden, a.rosterMenuItems)
		}
	}
	if !got[rosterActMessage] {
		t.Error("Message has no hide pref and must survive")
	}
}

// TestRosterMenuFence pins the modal-fence latch: held while open on the
// courtroom, force-closed (and released) when the active tab changes or the
// screen leaves the courtroom — the snapshot must never act cross-session.
func TestRosterMenuFence(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	a.screen = ScreenCourtroom
	a.serverKey = "ws://test"
	p := areaPlayer{uid: "3", name: "Edgeworth"}
	a.openRosterMenu(&p, false, false, sdl.Point{X: 5, Y: 5})
	if !a.rosterMenuOpen {
		t.Fatal("open failed")
	}

	a.rosterMenuFence(c)
	if !c.modalOn {
		t.Fatal("open menu must hold the modal fence")
	}

	a.activeTab = a.rosterMenuTab + 1 // tab switch under an open menu
	a.rosterMenuFence(c)
	if a.rosterMenuOpen {
		t.Fatal("a tab switch must force-close the menu")
	}
	if c.modalOn {
		t.Fatal("the forced close must release the fence")
	}

	// Same for leaving the courtroom.
	a.activeTab = 0
	a.rosterMenuTab = 0
	a.rosterMenuOpen = true
	a.rosterMenuFence(c)
	if !c.modalOn {
		t.Fatal("re-open must re-hold the fence")
	}
	a.screen = ScreenLobby
	a.rosterMenuFence(c)
	if a.rosterMenuOpen || c.modalOn {
		t.Fatal("leaving the courtroom must close the menu and release the fence")
	}
}

// TestRosterMenuEscCloses pins Esc's closeTopOverlay rung for the menu.
func TestRosterMenuEscCloses(t *testing.T) {
	a := testTabApp(t)
	a.rosterMenuOpen = true
	if !a.closeTopOverlay() {
		t.Fatal("closeTopOverlay must claim the open roster menu")
	}
	if a.rosterMenuOpen {
		t.Fatal("Esc must close the roster menu")
	}
}
