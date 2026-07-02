package ui

// Keep-until-echo (the send race): tsuserver-family servers silently swallow an
// MS that lands inside another message's area-wide delay window
// (area.can_send_message → plain return, no OOC notice), so sending "at the
// same time" as someone else loses. AO2-Client survives it because
// handle_chatmessage clears the input only when the server echoes YOUR message
// back (CHAR_ID == m_cid) — these tests pin that parity in AsyncAO: a send
// snapshots the line as pending, and only the own-echo consumes it.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestSendICKeepsInputUntilEcho pins that a send never clears the box — it only
// snapshots the pending line — so a server-swallowed message costs a re-Enter,
// not a retype.
func TestSendICKeepsInputUntilEcho(t *testing.T) {
	a := testTabApp(t)
	var sent []protocol.Packet
	a.sess = courtroom.NewSession(func(p protocol.Packet) error { sent = append(sent, p); return nil }, "")
	a.sess.MyCharID = 7
	a.icInput = "gotcha — the clock was stopped"

	a.sendIC(0)

	if len(sent) != 1 || sent[0].Header != "MS" {
		t.Fatalf("want exactly one MS out, got %+v", sent)
	}
	if a.icInput != "gotcha — the clock was stopped" {
		t.Errorf("input must be KEPT until the echo, got %q", a.icInput)
	}
	if a.icPendingSent != a.icInput {
		t.Errorf("pending snapshot = %q, want the typed line", a.icPendingSent)
	}
}

// TestOwnEchoClearsUnchangedInput pins noteOwnICEcho: the echo consumes the
// pending line and the one-shot evidence present, clearing the box because it
// still holds exactly what was sent.
func TestOwnEchoClearsUnchangedInput(t *testing.T) {
	a := testTabApp(t)
	a.icInput, a.icPendingSent, a.evidPresent = "same line", "same line", true

	a.noteOwnICEcho()

	if a.icInput != "" || a.icPendingSent != "" || a.evidPresent {
		t.Errorf("own echo must clear box+pending+present, got input=%q pending=%q present=%v",
			a.icInput, a.icPendingSent, a.evidPresent)
	}
}

// TestOwnEchoKeepsEditedInput pins the in-flight-typing guard: text edited
// between send and echo survives (AO2-Client wipes it; we only clear an
// UNCHANGED box), while the pending snapshot is still consumed.
func TestOwnEchoKeepsEditedInput(t *testing.T) {
	a := testTabApp(t)
	a.icInput, a.icPendingSent = "already retyping the next line", "the sent line"

	a.noteOwnICEcho()

	if a.icInput != "already retyping the next line" {
		t.Errorf("edited input must survive the echo, got %q", a.icInput)
	}
	if a.icPendingSent != "" {
		t.Errorf("pending must still be consumed, got %q", a.icPendingSent)
	}
}

// TestBackgroundEchoClearsParkedInput pins the parked/pinned-tab wiring:
// routeBackgroundEvent clears a tab's pending line only on ITS OWN char id —
// a foreign speaker (the race winner) must never touch it.
func TestBackgroundEchoClearsParkedInput(t *testing.T) {
	a := testTabApp(t)
	sess := courtroom.NewRehearsalSession("", []string{"Phoenix"})
	sess.MyCharID = 0
	tab := &courtTab{state: sessionState{
		sess:          sess,
		icInput:       "parked line",
		icPendingSent: "parked line",
	}}
	a.tabs = append(a.tabs, tab)

	foreign := &protocol.ChatMessage{CharID: 3, CharName: "Edgeworth", Message: "hold it"}
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventMessage, Message: foreign})
	if tab.state.icInput != "parked line" || tab.state.icPendingSent != "parked line" {
		t.Fatalf("a foreign message must not clear the parked input, got input=%q pending=%q",
			tab.state.icInput, tab.state.icPendingSent)
	}

	own := &protocol.ChatMessage{CharID: 0, CharName: "Phoenix", Message: "parked line"}
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventMessage, Message: own})
	if tab.state.icInput != "" || tab.state.icPendingSent != "" {
		t.Errorf("own echo must clear the parked input, got input=%q pending=%q",
			tab.state.icInput, tab.state.icPendingSent)
	}
}
