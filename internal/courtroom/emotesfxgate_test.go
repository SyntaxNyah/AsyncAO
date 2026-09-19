package courtroom

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// The emote SFX rides the PREANIM — plus any hand-picked sound.
//
// AO2 wires play_sfx to exactly one trigger — sfx_delay_timer (courtroom.cpp:432) —
// and starts that timer in exactly one place, play_preanim (:4054), which
// handle_emote_mod reaches only for PREANIM / PREANIM_ZOOM (:2892-2896) or for
// IDLE / ZOOM with immediate ticked (:2897-2910). A plain IDLE/ZOOM line with an
// AUTO SFX never starts that timer — AsyncAO armed the deadline for every message,
// so a "SFX: auto" line sent with Pre unchecked still fired the emote's char.ini
// sound: the receive half of the 2026-08-08 field report.
//
// A hand-PICKED sound is different: outgoingSFXName ships it whenever the dropdown
// row is > 0, regardless of Pre (courtroom.cpp:2102-2104), so an IDLE/ZOOM line
// carrying a real name is a sender that explicitly asked for a sound, and it must
// play. The silent sentinels ("", "0", "1") stay silent.

// sfxGateMsg builds a message carrying `sfxName` under emote mod `mod`.
func sfxGateMsg(mod int, immediate bool, sfxName string) *protocol.ChatMessage {
	return &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", PreEmote: "point", Message: "hi", Side: "wit",
		EmoteMod: mod, Immediate: immediate,
		SFXName: sfxName, SFXDelay: 0, // fires on the first Update once armed
	}
}

func TestEmoteSFXGate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mod       int
		immediate bool
		sfxName   string
		want      bool
		why       string
	}{
		{"preanim", protocol.EmoteModPreanim, false, "whack", true,
			"PREANIM → play_preanim(false) → sfx_delay_timer (courtroom.cpp:2892-2896, :4054)"},
		{"preanim zoom", protocol.EmoteModPreanimZoom, false, "whack", true,
			"PREANIM_ZOOM → play_preanim(false) (courtroom.cpp:2893)"},
		{"idle + immediate", protocol.EmoteModIdle, true, "whack", true,
			"IDLE with immediate → play_preanim(true) (courtroom.cpp:2900-2910)"},
		{"zoom + immediate", protocol.EmoteModZoom, true, "whack", true,
			"ZOOM with immediate → play_preanim(true) (courtroom.cpp:2898-2910)"},
		{"idle + hand-picked SFX", protocol.EmoteModIdle, false, "whack", true,
			"a non-silent name is a picked sound shipped regardless of Pre (courtroom.cpp:2102-2104)"},
		{"zoom + hand-picked SFX", protocol.EmoteModZoom, false, "whack", true,
			"a non-silent name is a picked sound shipped regardless of Pre (courtroom.cpp:2102-2104)"},
		{"idle + auto SFX", protocol.EmoteModIdle, false, "1", false,
			"auto SFX with Pre off transmits the silent sentinel, never heard"},
		{"zoom + auto SFX", protocol.EmoteModZoom, false, "1", false,
			"auto SFX with Pre off transmits the silent sentinel, never heard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			room, _, _, audio := newCourtroomRig(t)
			room.HandleEvent(Event{Kind: EventMessage, Message: sfxGateMsg(tc.mod, tc.immediate, tc.sfxName)})
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
	msg := sfxGateMsg(protocol.EmoteModPreanim, false, "whack")
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

// TestPreanimSFXPlaysPinsTheGate is the pure-function truth table behind the
// end-to-end test above: the MOD arm (preanim/immediate) plus the hand-picked
// name arm both open the gate, and the silent sentinels stay silent.
func TestPreanimSFXPlaysPinsTheGate(t *testing.T) {
	cases := []struct {
		name string
		msg  *protocol.ChatMessage
		want bool
	}{
		{"preanim mod", &protocol.ChatMessage{EmoteMod: protocol.EmoteModPreanim, SFXName: "1"}, true},
		{"preanim zoom mod", &protocol.ChatMessage{EmoteMod: protocol.EmoteModPreanimZoom, SFXName: "1"}, true},
		{"idle + immediate", &protocol.ChatMessage{EmoteMod: protocol.EmoteModIdle, Immediate: true, SFXName: "1"}, true},
		{"idle + non-silent", &protocol.ChatMessage{EmoteMod: protocol.EmoteModIdle, SFXName: "boom"}, true},
		{"zoom + non-silent", &protocol.ChatMessage{EmoteMod: protocol.EmoteModZoom, SFXName: "boom"}, true},
		{"idle + empty", &protocol.ChatMessage{EmoteMod: protocol.EmoteModIdle, SFXName: ""}, false},
		{"idle + 0", &protocol.ChatMessage{EmoteMod: protocol.EmoteModIdle, SFXName: "0"}, false},
		{"idle + 1", &protocol.ChatMessage{EmoteMod: protocol.EmoteModIdle, SFXName: "1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := preanimSFXPlays(tc.msg); got != tc.want {
				t.Errorf("preanimSFXPlays = %v, want %v", got, tc.want)
			}
		})
	}
}
