package ui

// Keep-until-echo (the send race): tsuserver-family servers silently swallow an
// MS that lands inside another message's area-wide delay window
// (area.can_send_message → plain return, no OOC notice), so sending "at the
// same time" as someone else loses. AO2-Client survives it because
// handle_chatmessage clears the input only when the server echoes YOUR message
// back (CHAR_ID == m_cid) — these tests pin that parity in AsyncAO: a send
// snapshots the line as pending, and only the own-echo consumes it.
//
// Issue #56: AO2-Client's own-echo clears the box unconditionally, but a plain
// exact-match gate here discarded the pending snapshot on ANY mismatch — most
// commonly just typing the start of the next line during the round trip —
// and never cleared afterwards. noteOwnICEcho is prefix-aware: an unchanged
// box clears (matches AO2), a box that still starts with the sent line has
// only that prefix stripped (the continuation typed on top survives), and a
// genuinely different edit survives whole, same as before.

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

	a.sendIC()

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

// TestOwnEchoStripsAppendedContinuation pins the third outcome added for issue
// #56: the box holds the sent line PLUS more the user typed on top of it
// during the round trip (a prefix match, not an exact one). Only the sent
// portion is consumed; the continuation the user was already typing survives
// verbatim, so it is never lost and never has to be retyped.
func TestOwnEchoStripsAppendedContinuation(t *testing.T) {
	a := testTabApp(t)
	a.icInput, a.icPendingSent = "gotcha and here's more", "gotcha"

	a.noteOwnICEcho()

	if a.icInput != " and here's more" {
		t.Errorf("a continuation typed on top of the sent line must survive, got %q", a.icInput)
	}
	if a.icPendingSent != "" {
		t.Errorf("pending must still be consumed, got %q", a.icPendingSent)
	}
}

// TestOwnEchoStripsAppendedContinuation_EmptyPending pins the boundary case of
// the same rule: an empty pending snapshot (nothing was ever sent this round,
// or a prior echo already consumed it) is a prefix of everything, but
// stripping a zero-length prefix must leave the draft untouched rather than
// being special-cased into an unconditional clear (a plausible slip: reading
// "nothing pending" as "a blankpost was sent, so clear anyway").
func TestOwnEchoStripsAppendedContinuation_EmptyPending(t *testing.T) {
	a := testTabApp(t)
	a.icInput, a.icPendingSent = "fresh draft", ""

	a.noteOwnICEcho()

	if a.icInput != "fresh draft" {
		t.Errorf("an empty pending snapshot must not touch the draft, got %q", a.icInput)
	}
}

// TestActiveTabOwnEchoStripsAppendedContinuation drives the SAME fix through
// the active tab's real wiring (handleSessionEvents' EventMessage branch in
// app.go) instead of calling noteOwnICEcho directly, so a future call site
// that stops routing through it — or diverges in what it passes — breaks this
// test instead of silently reverting to the old exact-match behavior.
func TestActiveTabOwnEchoStripsAppendedContinuation(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	a.sess.MyCharID = 0
	a.icInput, a.icPendingSent = "gotcha and here's more", "gotcha"

	own := &protocol.ChatMessage{CharID: 0, CharName: "Phoenix", Message: "gotcha"}
	a.handleSessionEvents([]courtroom.Event{{Kind: courtroom.EventMessage, Message: own}})

	if a.icInput != " and here's more" {
		t.Errorf("active-tab own echo must strip only the sent prefix, got %q", a.icInput)
	}
	if a.icPendingSent != "" {
		t.Errorf("pending must still be consumed, got %q", a.icPendingSent)
	}
}

// TestBackgroundEchoStripsAppendedContinuation is
// TestActiveTabOwnEchoStripsAppendedContinuation's parked-tab counterpart: it
// drives routeBackgroundEvent, the OTHER real call site, proving the same
// prefix-strip lands for a background/pinned tab too — the dossier's claim
// that a single shared method covers both tabs, pinned rather than assumed.
func TestBackgroundEchoStripsAppendedContinuation(t *testing.T) {
	a := testTabApp(t)
	sess := courtroom.NewRehearsalSession("", []string{"Phoenix"})
	sess.MyCharID = 0
	tab := &courtTab{state: sessionState{
		sess:          sess,
		icInput:       "gotcha and here's more",
		icPendingSent: "gotcha",
	}}
	a.tabs = append(a.tabs, tab)

	own := &protocol.ChatMessage{CharID: 0, CharName: "Phoenix", Message: "gotcha"}
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventMessage, Message: own})

	if tab.state.icInput != " and here's more" {
		t.Errorf("parked-tab own echo must strip only the sent prefix, got %q", tab.state.icInput)
	}
	if tab.state.icPendingSent != "" {
		t.Errorf("pending must still be consumed, got %q", tab.state.icPendingSent)
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
