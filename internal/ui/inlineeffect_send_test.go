package ui

// The send half of the bare-inline-effect fix. sendIC's two "nothing to say"
// guards both test the RAW typed line (they only substitute the AO single-space
// blankpost when the line is empty or whitespace), so a line that is nothing but
// `\f` / `\s` already reaches the wire intact — the same rule AO2 applies, whose
// own emptiness test reads the raw MESSAGE field (start_chat_ticking,
// ../AO2-Client/src/courtroom.cpp:4183). These pin that, so a future "strip the
// markup first, then decide if it's empty" refactor can't quietly turn a
// bare-effect send into a blankpost and re-break the feature from this end.

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// sendICOnce wires a recording session onto a headless App, types line, sends it
// and returns every packet that went out.
func sendICOnce(t *testing.T, a *App, line string) []protocol.Packet {
	t.Helper()
	var sent []protocol.Packet
	a.sess = courtroom.NewSession(func(p protocol.Packet) error { sent = append(sent, p); return nil }, "")
	a.sess.MyCharID = 7
	a.icInput = line
	a.sendIC()
	return sent
}

// TestSendBareInlineEffectReachesWire pins that a message consisting only of
// screen-effect codes is transmitted verbatim rather than being replaced by the
// blankpost single space — and that a genuinely empty line still becomes that
// blankpost.
func TestSendBareInlineEffectReachesWire(t *testing.T) {
	for _, tc := range []struct {
		name, typed, wantWire string
	}{
		{"bare flash", `\f`, `\f`},
		{"bare shake", `\s`, `\s`},
		{"two bare effects", `\s\f`, `\s\f`},
		{"effect plus text still works", `\fboom`, `\fboom`},
		{"real blankpost stays a blankpost", "", " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := sendICOnce(t, testTabApp(t), tc.typed)
			if len(sent) != 1 || sent[0].Header != "MS" {
				t.Fatalf("want exactly one MS out, got %+v", sent)
			}
			if got := sent[0].Fields[protocol.MSMessage]; got != tc.wantWire {
				t.Errorf("wire MESSAGE = %q, want %q", got, tc.wantWire)
			}
		})
	}
}

// TestSendBareInlineEffectObeysRateLimit pins that bare effects are not a
// rate-limit bypass: they go through the same chat_ratelimit gate as any other
// IC line, so a second one inside the window is dropped rather than becoming a
// free screenshake channel.
func TestSendBareInlineEffectObeysRateLimit(t *testing.T) {
	a := testTabApp(t)
	crawl, stay, _ := a.d.Prefs.Timing()
	const rateLimitMs = 3000 // comfortably longer than this test takes to run
	a.d.Prefs.SetTiming(crawl, stay, rateLimitMs)

	var sent []protocol.Packet
	a.sess = courtroom.NewSession(func(p protocol.Packet) error { sent = append(sent, p); return nil }, "")
	a.sess.MyCharID = 7
	a.lastICSend = time.Time{} // no prior send: the first one must go through

	a.icInput = `\s`
	a.sendIC()
	if len(sent) != 1 {
		t.Fatalf("the first bare effect must send, got %d packets", len(sent))
	}
	a.icInput = `\s`
	a.sendIC() // immediately again — inside the window
	if len(sent) != 1 {
		t.Errorf("a bare effect inside the rate-limit window must be dropped, got %d packets", len(sent))
	}
}

// TestBareInlineEffectFromAnotherClient is the receive half end-to-end through
// the App's own room: a foreign speaker's glyph-less `\f` fires the flash. This
// is the case a stock AO2 peer produces — AO2 keeps the escape codes in the
// transmitted text, so its bare-effect line arrives here as a literal "\f".
func TestBareInlineEffectFromAnotherClient(t *testing.T) {
	a := &App{}
	a.room = newRoomForTest(t)
	a.room.ScreenEffects, a.room.ReduceMotion = true, false

	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(3, "Edgeworth", `\f`)})
	a.room.Update(20 * time.Millisecond)

	if a.room.Scene.FlashLeft != courtroom.RealizationFlashDuration {
		t.Errorf(`a foreign bare "\f" must flash, FlashLeft = %v`, a.room.Scene.FlashLeft)
	}
	if a.room.Scene.ShakeLeft != 0 {
		t.Errorf(`a bare "\f" must not shake, ShakeLeft = %v`, a.room.Scene.ShakeLeft)
	}
}
