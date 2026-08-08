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

	// Frame by frame, not one 30-second step. This is the AUDIO-active census and its
	// blips are budgeted per Update (blipsPerUpdate — AO2 sounds at most one blip per
	// chat_tick delivery, courtroom.cpp:4545), so one giant step would reveal the whole
	// line without ever exercising the per-frame blip path the census is about. Bounded
	// far above the message length, so a regression fails here instead of spinning.
	for i := 0; i < 500 && !room.Typewriter.Done(); i++ {
		room.Update(DefaultCharInterval)
	}
	room.Update(30 * time.Second) // collapse the linger timer → idle
	if room.AudioActive() {
		t.Fatalf("a finished message must not be audio-active (phase %v, done %v)", room.Phase(), room.Typewriter.Done())
	}
}
