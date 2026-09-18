package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestZoomEmoteHidesDeskAndPair pins the stage a zoom emote takes over (#126
// playtest — Crystalwarrior quoted AO2's handle_ic_speaking).
//
// AO2's zoom is a SOLO shot and two separate behaviours build it:
//
//   - handle_ic_speaking hides the desk outright (courtroom.cpp:3459). It sits on
//     the TALK side of the phase machine, and play_preanim re-runs set_scene first
//     (:4055-4073), so a PREANIM_ZOOM still shows its desk through the
//     preanimation and loses it when the talking starts — the same talk-only
//     asymmetry the mod 4/5 EX pair already has in applyDeskMods.
//   - handle_ic_message never calls display_pair_character for a zoom (:3160), and
//     that runs BEFORE the phase machine, so the pair is gone in BOTH phases.
func TestZoomEmoteHidesDeskAndPair(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  int
	}{
		{"zoom", protocol.EmoteModZoom},
		{"preanim zoom", protocol.EmoteModPreanimZoom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			room := deskRoomWithBackground(t)
			room.begin(&protocol.ChatMessage{
				CharName: "Phoenix", Emote: "normal", Side: "def",
				DeskMod: protocol.DeskShow, EmoteMod: tc.mod,
				Pair: protocol.ParsePair("4", "Edgeworth", "thinking", "0", "0"),
			})

			// The talk column is what begin() stages: desk gone, pair never staged.
			if room.Scene.ShowDesk {
				t.Error("begin() left the desk up — AO2 hides it for a zoom")
			}
			if room.Scene.PairActive {
				t.Error("begin() staged the pair — AO2 never displays one for a zoom")
			}
			if room.Scene.Pair.Active != "" {
				t.Errorf("a pair layer was built (%q) behind an inactive PairActive", room.Scene.Pair.Active)
			}

			// The preanim column shows the desk again (play_preanim's set_scene)
			// but never the pair.
			room.applyDeskMods(true)
			if !room.Scene.ShowDesk {
				t.Error("the preanim column hid the desk — AO2's set_scene shows it there")
			}
			if room.Scene.PairActive {
				t.Error("the preanim column staged the pair — a zoom hides it in BOTH phases")
			}

			// …and talking hides the desk again.
			room.applyDeskMods(false)
			if room.Scene.ShowDesk {
				t.Error("the talk column left the desk up")
			}
		})
	}
}

// TestNonZoomEmoteKeepsItsDeskAndPair is the regression guard on the two
// conditions above: the same message shape on an IDLE emote must stage exactly
// what the desk_mod table and the wire asked for, or the zoom branch has leaked
// into every message.
func TestNonZoomEmoteKeepsItsDeskAndPair(t *testing.T) {
	room := deskRoomWithBackground(t)
	room.begin(&protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "def",
		DeskMod: protocol.DeskShow, EmoteMod: protocol.EmoteModIdle,
		Pair: protocol.ParsePair("4", "Edgeworth", "thinking", "0", "0"),
	})
	if !room.Scene.ShowDesk {
		t.Error("an idle message with DeskShow must show the desk")
	}
	if !room.Scene.PairActive {
		t.Error("an idle message with a pair must stage it")
	}
	// The phase edges must not disturb either one.
	room.applyDeskMods(true)
	if !room.Scene.ShowDesk || !room.Scene.PairActive {
		t.Error("the preanim column changed an idle message's desk or pair")
	}
}
