package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestPreanimPlaysOncePerEmote pins #52: AO2 plays a preanimation only when the
// emote CHANGES. Re-sending the SAME emote (same character) must skip the
// preanim and go straight to the talk loop, not replay the (possibly long)
// one-shot every time.
func TestPreanimPlaysOncePerEmote(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)

	msg := func(text string) *protocol.ChatMessage {
		m := waitMsg("Phoenix", "angry", text)
		m.CharID = 1
		m.PreEmote = "flourish"
		m.EmoteMod = protocol.EmoteModPreanim
		return m
	}

	// First message: the preanim plays (blocking preanim phase).
	room.HandleEvent(Event{Kind: EventMessage, Message: msg("first")})
	mustPhase(t, room, PhasePreanim)

	// Settle it fully so the next message can take the stage.
	room.SkipToIdle()

	// Second message, same character + same emote: the preanim must be skipped.
	room.HandleEvent(Event{Kind: EventMessage, Message: msg("second")})
	if room.Phase() == PhasePreanim {
		t.Fatalf("identical emote replayed the preanim; want the talk loop")
	}
	if room.Phase() != PhaseTalking {
		t.Fatalf("phase = %v, want PhaseTalking (preanim skipped)", room.Phase())
	}

	// A DIFFERENT emote must play the preanim again (the memory is per-emote).
	room.SkipToIdle()
	changed := waitMsg("Phoenix", "sad", "third")
	changed.CharID = 1
	changed.PreEmote = "flourish"
	changed.EmoteMod = protocol.EmoteModPreanim
	room.HandleEvent(Event{Kind: EventMessage, Message: changed})
	mustPhase(t, room, PhasePreanim)
}
