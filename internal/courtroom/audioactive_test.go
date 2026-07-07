package courtroom

import (
	"testing"
	"time"
)

// TestAudioActive pins the audio-pace predicate: the courtroom is "audio active"
// exactly while a message is typing (blips streaming), not before it begins and not
// once the text finishes. The main loop reads it to advance the room — and play its
// blips — at a fine cadence independent of the (possibly low) present rate, so audio
// never batches to the frame rate.
func TestAudioActive(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	if room.AudioActive() {
		t.Fatal("an idle courtroom (no message) must not be audio-active")
	}

	room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hello there")})
	if room.Phase() != PhaseTalking {
		t.Fatalf("a plain message should begin talking, got phase %v", room.Phase())
	}
	if room.Typewriter.Done() {
		t.Fatal("setup: the message should still be revealing text")
	}
	if !room.AudioActive() {
		t.Fatal("a typing message must be audio-active (blips streaming)")
	}

	room.Update(30 * time.Second) // reveal all text → the typewriter finishes → linger/idle
	if room.AudioActive() {
		t.Fatalf("a finished message must not be audio-active (phase %v, done %v)", room.Phase(), room.Typewriter.Done())
	}
}
