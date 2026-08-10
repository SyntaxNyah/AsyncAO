package ui

// REGRESSION: HOLD IT / OBJECTION / TAKE THAT fired on the CLICK.
//
// Live report, 2026-08-10: "clicking a shout button now plays the interjection
// immediately". Canon does not: on_hold_it_clicked and its three siblings
// (courtroom.cpp:6048-6127) only toggle objection_state and bounce the keyboard
// back to the message. The interjection rides the NEXT message — the MS
// objection field is assembled from objection_state at :2136-2147 and cleared in
// reset_ui at :2327.
//
// AsyncAO's three surfaces (the classic bar, the themed bar, the four hotkeys)
// each called sendIC(mod) instead, and sendIC's blankpost guard deliberately
// lets a shout through with NO text — so one click sent a whole empty message
// and played the shout on every client in the room.
//
// These gates drive the real state and the real packet builder.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestShoutClickArmsAndSendsNothing is the report, inverted: a click must leave
// the wire silent and the arm visible.
func TestShoutClickArmsAndSendsNothing(t *testing.T) {
	a, sent := flipSendApp(t)
	for _, mod := range []int{protocol.ShoutHoldIt, protocol.ShoutObjection, protocol.ShoutTakeThat, protocol.ShoutCustom} {
		a.icShout = 0
		a.toggleShout(mod)
		if len(*sent) != 0 {
			t.Fatalf("shout %d put %d packet(s) on the wire — clicking a shout must not SEND", mod, len(*sent))
		}
		if a.icShout != mod {
			t.Fatalf("shout %d did not arm: objection_state = %d", mod, a.icShout)
		}
	}
}

// TestShoutRidesTheNextMessage pins the other half of the contract: the armed
// interjection is what the NEXT message carries, and it carries the typed text
// with it rather than a blank line.
func TestShoutRidesTheNextMessage(t *testing.T) {
	a, sent := flipSendApp(t)
	a.toggleShout(protocol.ShoutObjection)
	if len(*sent) != 0 {
		t.Fatal("arming sent something")
	}
	a.sendICFresh() // sendIC reads the ARMED a.icShout — no shout argument exists any more
	msg := lastSentMS(t, a, sent)
	if msg.Objection != protocol.ShoutObjection {
		t.Fatalf("MS objection field = %d, want %d — the armed shout did not ride the message",
			msg.Objection, protocol.ShoutObjection)
	}
	if msg.Message != "over the phone" {
		t.Fatalf("MS text = %q, want the typed line", msg.Message)
	}
}

// TestShoutDisarmsAfterTheSend is courtroom.cpp:2327 (reset_ui clears
// objection_state UNCONDITIONALLY — it is not one of the three sticky options).
// A stuck arm shouts on every following line, which is the failure everybody
// notices.
func TestShoutDisarmsAfterTheSend(t *testing.T) {
	a, sent := flipSendApp(t)
	a.toggleShout(protocol.ShoutTakeThat)
	a.sendICFresh()
	if a.icShout != 0 {
		t.Fatalf("objection_state = %d after the send, want 0 (courtroom.cpp:2327)", a.icShout)
	}
	a.icInput = "a second line"
	a.sendICFresh()
	if msg := lastSentMS(t, a, sent); msg.Objection != 0 {
		t.Fatalf("the NEXT message still carries shout %d — the arm is stuck", msg.Objection)
	}
}

// TestShoutsAreMutuallyExclusiveAndToggleOff pins the shape of canon's four
// handlers: each else-arm resets the other three sprites before selecting its
// own (:6056-6063 and siblings), and clicking the armed one zeroes the state
// (:6050-6054). One int expresses both, which is why there is no bookkeeping to
// get wrong.
func TestShoutsAreMutuallyExclusiveAndToggleOff(t *testing.T) {
	a, _ := flipSendApp(t)
	a.toggleShout(protocol.ShoutHoldIt)
	a.toggleShout(protocol.ShoutObjection)
	if a.icShout != protocol.ShoutObjection {
		t.Fatalf("objection_state = %d, want the newly clicked shout to replace the old one", a.icShout)
	}
	a.toggleShout(protocol.ShoutObjection)
	if a.icShout != 0 {
		t.Fatalf("clicking the ARMED shout must disarm it, got %d", a.icShout)
	}
}

// TestRealizationStillArmsAlongsideTheShout guards the fix that shipped in the
// same lane as this defect (the realization button, 0fc8f38 item 8): it must
// keep arming — and now that its neighbour in the same bar arms too, the two
// have to ride ONE message and clear together.
//
// The effects-row half of the realization arm keeps its own gate
// (TestRealizationArmsTheEffectsRowNotJustTheFlag, icarms_test.go, which owns
// the roster fixture); this one is about the wire and the reset.
func TestRealizationStillArmsAlongsideTheShout(t *testing.T) {
	a, sent := flipSendApp(t)
	a.toggleRealization()
	if !a.icRealize {
		t.Fatal("realization no longer sets its wire flag")
	}
	if len(*sent) != 0 {
		t.Fatal("realization sent a message — it arms, like the shouts now do")
	}
	// Both arms ride the SAME message and both clear with it.
	a.toggleShout(protocol.ShoutHoldIt)
	a.sendICFresh()
	msg := lastSentMS(t, a, sent)
	if !msg.Realization || msg.Objection != protocol.ShoutHoldIt {
		t.Fatalf("realization=%v objection=%d — the two arms must ride one message together",
			msg.Realization, msg.Objection)
	}
	if a.icRealize || a.icShout != 0 {
		t.Fatalf("post-send arms: realize=%v shout=%d, want both cleared (courtroom.cpp:2327-2329)",
			a.icRealize, a.icShout)
	}
}

// TestNoShoutSurfaceCallsSendIC is the deletion catcher, and it is a SOURCE
// census because the failure has no return value: a surface that goes back to
// sending is a surface whose unit tests all still pass.
//
// It covers all three call sites that had the defect — the classic bar's shout
// row, the themed bar, and the hotkey switch — and it holds them to calling
// toggleShout, so a fourth surface added tomorrow cannot quietly copy the old
// spelling from one of them.
func TestNoShoutSurfaceCallsSendIC(t *testing.T) {
	for _, site := range []struct{ file, fn string }{
		{"screens.go", "drawICShoutRow"},
		{"theme_layout.go", "drawCourtroomThemed"},
		{"qol.go", "handleHotkeys"},
	} {
		body := funcBodySource(t, site.file, site.fn)
		if !containsCall(body, "toggleShout") {
			t.Errorf("%s no longer arms through toggleShout — the shout surfaces have drifted apart again", site.fn)
		}
	}
	// The classic shout row must not send at all: it used to hand a modifier
	// straight back to a sendIC call.
	if containsCall(funcBodySource(t, "screens.go", "drawICShoutRow"), "sendIC") {
		t.Error("drawICShoutRow sends — a shout BUTTON is not a send button (courtroom.cpp:6048-6127)")
	}
	// And the armed state has to be VISIBLE, which is the other half of what the
	// button does upstream (the "*_selected" sprite swap, :6062).
	for _, site := range []struct{ file, fn string }{
		{"screens.go", "drawICShoutRow"},
		{"theme_layout.go", "drawCourtroomThemed"},
	} {
		if !containsCall(funcBodySource(t, site.file, site.fn), "drawArmedOverlay") {
			t.Errorf("%s draws no armed marker — an arm nobody can see reads as a button that does nothing", site.fn)
		}
	}
}
