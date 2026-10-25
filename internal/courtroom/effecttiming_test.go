package courtroom

import (
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// WHEN the 2.8 EFFECTS field fires.
//
// AO2 runs do_effect from start_chat_ticking (courtroom.cpp:4154-4172) — the function
// that begins the TEXT — and a blocking preanim only reaches start_chat_ticking through
// preanim_done → handle_ic_speaking (:4108-4118 → :3506). So on a preanim message the
// overlay, its sound and the legacy realization flash all land when the crawl begins,
// one whole preanim later than AsyncAO used to put them (fireMessageEffects was called
// from enterAfterShout, before the preanim phase).
//
// The emote SFX is the OPPOSITE case and stays where it was: AO2 arms sfx_delay_timer
// inside play_preanim (:4054), i.e. at preanim START. Both halves are pinned here so a
// future tidy-up cannot "simplify" them back into one call site.

func effectTimingMsg(fx string) *protocol.ChatMessage {
	return &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", PreEmote: "point", Message: "hello there", Side: "wit",
		EmoteMod: protocol.EmoteModPreanim,
		Effects:  fx,
		SFXName:  "whack", SFXDelay: 0,
	}
}

func TestTheEffectFieldFiresWhenTheTextStartsNotAtMessageStart(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"wash": {}})
	room.HandleEvent(Event{Kind: EventMessage, Message: effectTimingMsg("wash|packpack|boom")})

	if room.Phase() != PhasePreanim {
		t.Fatalf("fixture: a PREANIM message should hold the stage, got phase %v", room.Phase())
	}
	if room.Scene.Overlay.Active() {
		t.Error("the overlay must NOT be up during the preanim — do_effect lives in start_chat_ticking (courtroom.cpp:4154)")
	}
	for _, s := range audio.sfx {
		if strings.Contains(strings.ToLower(s), "boom") {
			t.Errorf("the effect SOUND played during the preanim (%q) — canon plays it from do_effect, at text start", s)
		}
	}

	// Release the preanim: the text starts, and the whole effect field lands with it.
	room.preanimDone = true
	room.Update(time.Millisecond)
	if room.Phase() != PhaseTalking {
		t.Fatalf("the preanim released should start the text, got phase %v", room.Phase())
	}
	if !room.Scene.Overlay.Active() {
		t.Error("the overlay must be on stage the moment the text starts (start_chat_ticking → do_effect)")
	}
	found := false
	for _, s := range audio.sfx {
		if strings.Contains(strings.ToLower(s), "boom") {
			found = true
		}
	}
	if !found {
		t.Errorf("the effect sound must play when the text starts, got %v", audio.sfx)
	}
}

// TestTheEmoteSFXStillArmsAtPreanimStart is the other half: moving the EFFECTS field
// must not drag the emote SFX with it. AO2 starts sfx_delay_timer in play_preanim
// (courtroom.cpp:4054), so a SFX_DELAY of 0 sounds while the preanim is still on stage.
func TestTheEmoteSFXStillArmsAtPreanimStart(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"wash": {}})
	room.HandleEvent(Event{Kind: EventMessage, Message: effectTimingMsg("wash|packpack|boom")})
	if room.Phase() != PhasePreanim {
		t.Fatalf("fixture: expected PhasePreanim, got %v", room.Phase())
	}
	room.Update(time.Millisecond) // still mid-preanim: only the armed emote SFX may fire
	found := false
	for _, s := range audio.sfx {
		if strings.Contains(strings.ToLower(s), "whack") {
			found = true
		}
	}
	if !found {
		t.Errorf("the emote SFX must fire at its delay DURING the preanim (courtroom.cpp:4054), got %v", audio.sfx)
	}
}

// TestAnIdleMessagesEffectStillFiresAtOnce guards the other direction: for a message
// with no preanim, enterAfterShout falls straight through to startTalking, so the move
// must be invisible — the effect is up on the very first tick, as it always was.
func TestAnIdleMessagesEffectStillFiresAtOnce(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"wash": {}})
	msg := effectTimingMsg("wash|packpack|boom")
	msg.EmoteMod, msg.PreEmote = protocol.EmoteModIdle, ""
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if !room.Scene.Overlay.Active() {
		t.Error("an idle message's overlay must be up immediately")
	}
	if len(audio.sfx) == 0 {
		t.Error("an idle message's effect sound must play immediately")
	}
}
