package courtroom

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// waitMsg builds a synthetic no-preanim message for the wait-gate tests.
func waitMsg(char, emote, text string) *protocol.ChatMessage {
	return &protocol.ChatMessage{
		CharName: char, Emote: emote, Message: text, Side: "wit",
		EmoteMod: protocol.EmoteModIdle,
	}
}

// TestSpriteWaitGate pins cold-load mode 3 ("wait"): with the gate on, a message
// whose speaker idle sprite hasn't decoded is HELD in the queue (nothing begins);
// it begins the moment the sprite lands; and the timeout releases it anyway so a
// 404/decode failure can only ever delay a message, never hang the room.
func TestSpriteWaitGate(t *testing.T) {
	newRig := func(t *testing.T) (*Courtroom, map[string]bool) {
		room, _, _, _ := newCourtroomRig(t)
		ready := map[string]bool{}
		room.SpriteWait = true
		room.SpriteWaitTimeout = 500 * time.Millisecond
		room.SpriteReady = func(base string) bool { return ready[base] }
		return room, ready
	}

	t.Run("holds until ready", func(t *testing.T) {
		room, ready := newRig(t)
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if room.Phase() != PhaseIdle || room.QueueLen() != 1 || room.Scene.Speaker.Visible {
			t.Fatalf("cold sprite must hold: phase=%v queue=%d visible=%v", room.Phase(), room.QueueLen(), room.Scene.Speaker.Visible)
		}
		room.Update(100 * time.Millisecond) // still cold, still held
		if room.Phase() != PhaseIdle || room.QueueLen() != 1 {
			t.Fatal("hold must persist while the sprite is cold and the timeout hasn't expired")
		}
		ready[room.urls.Emote("Phoenix", "normal", EmoteIdle)] = true
		room.Update(16 * time.Millisecond) // sprite landed → begins
		if room.QueueLen() != 0 || !room.Scene.Speaker.Visible || room.Scene.Speaker.Name != "Phoenix" {
			t.Fatalf("ready sprite must begin the held message: queue=%d visible=%v", room.QueueLen(), room.Scene.Speaker.Visible)
		}
	})

	t.Run("timeout releases", func(t *testing.T) {
		room, _ := newRig(t)
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if room.QueueLen() != 1 {
			t.Fatal("setup: message should be held")
		}
		room.Update(600 * time.Millisecond) // one tick past the 500 ms cap
		if room.QueueLen() != 0 || !room.Scene.Speaker.Visible {
			t.Fatal("an expired hold must play the message anyway (a 404 can only delay, never hang)")
		}
	})

	t.Run("shout bypasses", func(t *testing.T) {
		room, _ := newRig(t)
		m := waitMsg("Phoenix", "normal", "OBJECTION!")
		m.Objection = protocol.ShoutObjection
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.Phase() != PhaseShout {
			t.Fatalf("a shout must play NOW (AO2 parity), got phase %v", room.Phase())
		}
	})

	t.Run("catch-up wins", func(t *testing.T) {
		room, ready := newRig(t)
		room.CatchUp, room.CatchUpThreshold = true, 1
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "one")})
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Edgeworth", "normal", "two")})
		if room.QueueLen() != 2 {
			t.Fatalf("setup: both messages should be queued (head held), got %d", room.QueueLen())
		}
		room.Update(16 * time.Millisecond) // backlog ≥ threshold → the head must NOT wait
		if room.QueueLen() != 1 {
			t.Fatalf("a backlog at the catch-up threshold must never wait, queue=%d", room.QueueLen())
		}
		_ = ready // never marked ready — catch-up alone must release the head
	})

	t.Run("pair strictness", func(t *testing.T) {
		room, ready := newRig(t)
		room.SpriteWaitPair = true
		m := waitMsg("Phoenix", "normal", "hi")
		m.Pair = protocol.PairInfo{CharID: 1, Name: "Edgeworth", Emote: "normal"} // Active(): valid id + folder
		ready[room.urls.Emote("Phoenix", "normal", EmoteIdle)] = true             // speaker ready, pair cold
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("pair strictness on: a cold pair sprite must hold the message")
		}
		ready[room.urls.Emote("Edgeworth", "normal", EmoteIdle)] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 || !room.Scene.PairActive {
			t.Fatal("both sprites ready must begin the paired message")
		}
	})

	t.Run("preanim strictness", func(t *testing.T) {
		room, ready := newRig(t)
		room.SpriteWaitPreanim = true
		m := waitMsg("Phoenix", "normal", "hi")
		m.PreEmote, m.EmoteMod = "flourish", protocol.EmoteModPreanim
		ready[room.urls.Emote("Phoenix", "normal", EmoteIdle)] = true // idle ready, preanim cold
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("preanim strictness on: a cold preanim must hold the message")
		}
		ready[room.urls.Emote("Phoenix", "flourish", EmotePreanim)] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("a ready preanim must release the hold")
		}
	})

	t.Run("confirmed-missing preanim releases on the miss signal, not the timeout", func(t *testing.T) {
		room, ready := newRig(t)
		room.SpriteWaitPreanim = true
		m := waitMsg("Phoenix", "normal", "hi")
		// A dummy preanim name (live packs ship "-<n>" on every emote); idle emote-mod
		// upgrades to preanim on the wire when a preanim name is present, so the gate
		// arms on a preanim sprite that will never resolve.
		m.PreEmote, m.EmoteMod = "-1", protocol.EmoteModPreanim
		ready[room.urls.Emote("Phoenix", "normal", EmoteIdle)] = true // idle ready; the preanim never will be
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("setup: a cold preanim must hold the message")
		}
		room.Update(50 * time.Millisecond) // far under the 500 ms cap, no miss learned yet → still held
		if room.QueueLen() != 1 {
			t.Fatal("without the miss signal the hold must persist (nothing has said the preanim is gone)")
		}
		// The manager conclusively 404s the dummy preanim — the App's warning relay
		// calls exactly this on the game thread.
		room.NotifyAssetMissing(room.urls.Emote("Phoenix", "-1", EmotePreanim))
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 || !room.Scene.Speaker.Visible {
			t.Fatal("a conclusively-missing preanim must release the hold on the miss signal, not burn the full timeout")
		}
	})

	t.Run("gate off is unchanged", func(t *testing.T) {
		room, _, _, _ := newCourtroomRig(t)
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if !room.Scene.Speaker.Visible || room.QueueLen() != 0 {
			t.Fatal("with the gate off a message must begin immediately (default behaviour pinned)")
		}
	})
}
