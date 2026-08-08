package courtroom

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// The emote SFX rides the PREANIM, and nothing else.
//
// AO2 wires play_sfx to exactly one trigger — sfx_delay_timer (courtroom.cpp:432) —
// and starts that timer in exactly one place, play_preanim (:4054), which
// handle_emote_mod reaches only for PREANIM / PREANIM_ZOOM (:2892-2896) or for
// IDLE / ZOOM with immediate ticked (:2897-2910). AsyncAO armed the deadline for
// every message, so a line sent with Pre unchecked still fired the emote's char.ini
// sound: the receive half of the 2026-08-08 field report.

// sfxGateMsg builds a message carrying an audible SFX under emote mod `mod`.
func sfxGateMsg(mod int, immediate bool) *protocol.ChatMessage {
	return &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", PreEmote: "point", Message: "hi", Side: "wit",
		EmoteMod: mod, Immediate: immediate,
		SFXName: "whack", SFXDelay: 0, // fires on the first Update once armed
	}
}

func TestEmoteSFXPlaysOnlyWhereAO2StartsTheDelayTimer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mod       int
		immediate bool
		want      bool
		why       string
	}{
		{"preanim", protocol.EmoteModPreanim, false, true,
			"PREANIM → play_preanim(false) → sfx_delay_timer (courtroom.cpp:2892-2896, :4054)"},
		{"preanim zoom", protocol.EmoteModPreanimZoom, false, true,
			"PREANIM_ZOOM → play_preanim(false) (courtroom.cpp:2893)"},
		{"idle + immediate", protocol.EmoteModIdle, true, true,
			"IDLE with immediate → play_preanim(true) (courtroom.cpp:2900-2910)"},
		{"zoom + immediate", protocol.EmoteModZoom, true, true,
			"ZOOM with immediate → play_preanim(true) (courtroom.cpp:2898-2910)"},
		{"idle", protocol.EmoteModIdle, false, false,
			"IDLE without immediate → handle_ic_speaking, the timer never starts (courtroom.cpp:2900-2903)"},
		{"zoom", protocol.EmoteModZoom, false, false,
			"ZOOM without immediate → handle_ic_speaking (courtroom.cpp:2900-2903)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			room, _, _, audio := newCourtroomRig(t)
			room.HandleEvent(Event{Kind: EventMessage, Message: sfxGateMsg(tc.mod, tc.immediate)})
			// Run well past any shout/preanim phase so an armed deadline certainly fires.
			for i := 0; i < 200 && len(audio.sfx) == 0; i++ {
				room.Update(20 * time.Millisecond)
			}
			if got := len(audio.sfx) > 0; got != tc.want {
				t.Errorf("emote SFX played = %v, want %v — %s", got, tc.want, tc.why)
			}
		})
	}
}

// TestMissingPreanimArtStillPlaysTheEmoteSFX pins WHERE the gate is not: AO2 starts
// the timer at courtroom.cpp:4054, BEFORE the `file_exists(anim_to_find)` check at
// :4056, so a preanim whose art never resolves still plays its sound. The gate is the
// emote MOD, never whether the sprite loaded — which is why preanimSFXPlays
// deliberately does not reuse enterAfterShout's art-aware playPre predicate.
func TestMissingPreanimArtStillPlaysTheEmoteSFX(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	msg := sfxGateMsg(protocol.EmoteModPreanim, false)
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	// Mark the preanim base conclusively missing, exactly as a 404 would.
	room.NotifyAssetMissing(room.Scene.Speaker.PreanimBase)
	for i := 0; i < 200 && len(audio.sfx) == 0; i++ {
		room.Update(20 * time.Millisecond)
	}
	if len(audio.sfx) == 0 {
		t.Error("a PREANIM message whose art is missing must still play its SFX (courtroom.cpp:4054 precedes :4056)")
	}
}
