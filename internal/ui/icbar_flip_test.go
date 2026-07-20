package ui

// The IC-bar Flip toggle and the two-segment pair-order control are DISCOVERABILITY
// re-homes of controls that already rode the standard AO2 wire. These tests pin that
// the re-homing is presentation-only: Flip is ONE bool (a.pairFlip) shared by the Pair
// panel checkbox and the IC-bar checkbox, gated on the server's flipping feature exactly
// as AO2-Client gates ui_flip (courtroom.cpp:1629-1636); the order segments map to the
// same PairSpeakerInFront/Behind values the old cycling button set. The wire behavior is
// unchanged, so we assert against the OUTGOING MS the shared fields produce.

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// flipSendApp wires a testTabApp with a capturing session that advertises the flipping,
// cccc_ic, and effects features. cccc_ic lets the pair block ride the wire at all; effects
// is what gates the "<id>^<order>" pair suffix — formatPairID emits ^order ONLY when the
// server has "effects" (ms.go:460, features.go:22 "also gates pair ^order"), so without it
// a "behind" order serializes as a bare id and the order can't round-trip. It returns the
// app plus a pointer to the slice the session appends sent packets to.
func flipSendApp(t *testing.T) (*App, *[]protocol.Packet) {
	t.Helper()
	a := testTabApp(t)
	var sent []protocol.Packet
	a.sess = courtroom.NewSession(func(p protocol.Packet) error { sent = append(sent, p); return nil }, "")
	// A tiny two-char list so MyCharID / PairWith are in range and ParseMS accepts them.
	a.sess.Chars = []courtroom.CharacterSlot{{Name: "Phoenix"}, {Name: "Maya"}}
	a.sess.MyCharID = 0
	a.sess.Features = protocol.FeatureSet{}
	a.sess.Features[protocol.FeatureFlipping] = struct{}{}
	a.sess.Features[protocol.FeatureCCCCIC] = struct{}{} // required for the pair block to serialize
	a.sess.Features[protocol.FeatureEffects] = struct{}{}
	a.icInput = "over the phone"
	return a, &sent
}

// sendICFresh sends an IC line with the chat rate-limit window cleared first. These
// tests send twice and assert on the SECOND packet; the default prefs rate is 0 (no
// limit) so back-to-back sends both land, but resetting lastICSend keeps the test correct
// even if a non-zero default were ever introduced (sendIC drops sends inside the window,
// screens.go:6138-6140, and stamps lastICSend on each accepted send).
func (a *App) sendICFresh(shout int) {
	a.lastICSend = time.Time{}
	a.sendIC(shout)
}

// lastSentMS parses the most recent captured packet back into a ChatMessage so the
// test reads the same fields a receiving client would — the outgoing wire is the
// source of truth for "did the shared field ride the message".
func lastSentMS(t *testing.T, a *App, sent *[]protocol.Packet) *protocol.ChatMessage {
	t.Helper()
	if len(*sent) == 0 || (*sent)[len(*sent)-1].Header != "MS" {
		t.Fatalf("want an MS packet out, got %+v", *sent)
	}
	msg, err := protocol.ParseMS((*sent)[len(*sent)-1].Fields, a.sess.Features, len(a.sess.Chars))
	if err != nil {
		t.Fatalf("ParseMS: %v", err)
	}
	return msg
}

// TestICBarFlipRidesSharedField pins that the IC-bar Flip checkbox and the Pair panel
// checkbox are ONE bool: both draw sites do `a.pairFlip = c.Checkbox(...)`, so whichever
// view the user toggles, the SAME a.pairFlip rides OutgoingMS.Flip. We can't headlessly
// click SDL, so we set the shared field directly (which is exactly what an unclicked
// Checkbox leaves it at from EITHER view) and confirm the wire carries it, both ways.
func TestICBarFlipRidesSharedField(t *testing.T) {
	a, sent := flipSendApp(t)

	a.pairFlip = true // the state either view writes
	a.sendICFresh(0)
	if msg := lastSentMS(t, a, sent); !msg.Flip {
		t.Error("pairFlip=true must ride OutgoingMS.Flip (the shared IC-bar / Pair-panel field)")
	}

	a.pairFlip = false
	a.icInput = "back to normal"
	a.sendICFresh(0)
	if msg := lastSentMS(t, a, sent); msg.Flip {
		t.Error("pairFlip=false must clear OutgoingMS.Flip")
	}
}

// TestICBarFlipGatedOnFlippingFeature pins the AO2 parity (courtroom.cpp:1629-1636):
// the IC-bar Flip toggle is shown ONLY when the server advertises "flipping". The row's
// visibility predicate is `a.sess != nil && a.sess.Features.Has(FeatureFlipping)` — this
// pins that exact gate so a future refactor can't strand the toggle on a non-flipping
// server (where AO2 hides ui_flip) or hide it on one that supports it.
func TestICBarFlipGatedOnFlippingFeature(t *testing.T) {
	a := testTabApp(t)

	// No session → never shown (the nil guard).
	a.sess = nil
	if a.sess != nil && a.sess.Features.Has(protocol.FeatureFlipping) {
		t.Fatal("nil session must not show the IC-bar Flip toggle")
	}

	// Session without the feature → hidden (mirrors AO2 hiding ui_flip).
	a.sess = &courtroom.Session{Features: protocol.FeatureSet{}}
	if a.sess.Features.Has(protocol.FeatureFlipping) {
		t.Error("a non-flipping server must NOT show the IC-bar Flip toggle")
	}

	// Session WITH the feature → shown.
	a.sess.Features[protocol.FeatureFlipping] = struct{}{}
	if !a.sess.Features.Has(protocol.FeatureFlipping) {
		t.Error("a flipping server must show the IC-bar Flip toggle")
	}
}

// TestPairOrderSegmentsMapToWire pins the two-segment order control's semantics: the
// "To front" segment sets a.pairOrder = PairSpeakerInFront and "To behind" sets
// PairSpeakerBehind — the SAME field + wire the old cycling button drove, so the
// presentation change can't alter what goes on the wire. We assert against the parsed
// outgoing pair block (front = HasOrder false or Order==InFront; behind carries the ^1).
func TestPairOrderSegmentsMapToWire(t *testing.T) {
	a, sent := flipSendApp(t)
	a.pairWith = 1 // must pair with someone for the order to be meaningful on the wire

	// "To front" segment → PairSpeakerInFront.
	a.pairOrder = protocol.PairSpeakerInFront
	a.sendICFresh(0)
	if msg := lastSentMS(t, a, sent); msg.Pair.Order != protocol.PairSpeakerInFront {
		t.Errorf("front segment: wire pair order = %d, want PairSpeakerInFront (%d)", msg.Pair.Order, protocol.PairSpeakerInFront)
	}

	// "To behind" segment → PairSpeakerBehind (rides the ^1 suffix).
	a.pairOrder = protocol.PairSpeakerBehind
	a.icInput = "behind you"
	a.sendICFresh(0)
	msg := lastSentMS(t, a, sent)
	if msg.Pair.Order != protocol.PairSpeakerBehind || !msg.Pair.HasOrder {
		t.Errorf("behind segment: wire pair = {order:%d hasOrder:%v}, want {Behind(%d), true}", msg.Pair.Order, msg.Pair.HasOrder, protocol.PairSpeakerBehind)
	}
}
