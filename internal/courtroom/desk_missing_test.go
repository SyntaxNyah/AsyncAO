package courtroom

import (
	"fmt"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestDeskDrawnTable pins #44's decision function across the full cross product
// of desk_mod × phase × what we know about the desk image.
//
// The DeskUnresolved/DeskResolved columns must reproduce the existing deskVisible
// table VERBATIM (a regression guard on TestDeskVisiblePhaseMatrix): a desk we have
// not been told is missing behaves exactly as it did before the fix, so a real desk
// that is merely still streaming never blinks.
//
// The DeskAbsent column must be false for EVERY mod — including DeskShow, and
// including the unknown-mod default that deskVisible answers "true" to. That is
// AO2-Client's set_scene: it computes show_desk from desk_mod and then clears it
// unconditionally when the position's desk file does not exist
// (../AO2-Client/src/courtroom.cpp:4628-4634), hiding the layer (:4656-4663). The
// desk arm has NO fallback, unlike the background arm right above it (:4613-4626).
func TestDeskDrawnTable(t *testing.T) {
	// {mod, preanim-visible, talk-visible} — the pre-#44 deskVisible truth table.
	cases := []struct {
		mod           int
		preanim, talk bool
	}{
		{protocol.DeskHide, false, false},
		{protocol.DeskShow, true, true},
		{protocol.DeskEmoteOnly, false, true},   // desk only while talking
		{protocol.DeskPreOnly, true, false},     // desk only during the preanim
		{protocol.DeskEmoteOnlyEx, false, true}, // EX mirrors its base for desk visibility
		{protocol.DeskPreOnlyEx, true, false},
		{-1, true, true}, // out-of-range mods fall to AO2's "show" default
		{99, true, true},
	}
	for _, tc := range cases {
		for _, res := range []DeskResolution{DeskUnresolved, DeskResolved} {
			if got := DeskDrawn(tc.mod, true, res); got != tc.preanim {
				t.Errorf("DeskDrawn(mod=%d, preanim, res=%d) = %v, want %v (= deskVisible)", tc.mod, res, got, tc.preanim)
			}
			if got := DeskDrawn(tc.mod, false, res); got != tc.talk {
				t.Errorf("DeskDrawn(mod=%d, talk, res=%d) = %v, want %v (= deskVisible)", tc.mod, res, got, tc.talk)
			}
		}
		for _, preanim := range []bool{true, false} {
			if DeskDrawn(tc.mod, preanim, DeskAbsent) {
				t.Errorf("DeskDrawn(mod=%d, preanim=%v, DeskAbsent) = true; a background that ships no "+
					"desk for this position must draw NO desk (set_scene forces show_desk=false)", tc.mod, preanim)
			}
		}
	}
}

// deskRoomWithBackground builds a rig whose session already has a background, so
// begin() derives Scene.DeskBase (the guard at begin's scene block needs it).
func deskRoomWithBackground(t *testing.T) *Courtroom {
	t.Helper()
	room, sess, _, _ := newCourtroomRig(t)
	sess.Background = "gs4"
	return room
}

// TestNotifyDeskMissingHidesLiveDesk pins the live half of #44: the conclusive 404
// for the CURRENT desk must hide it immediately — not at the next phase edge, which
// a settled message never reaches — and the phase machine must not be able to
// resurrect it afterwards.
func TestNotifyDeskMissingHidesLiveDesk(t *testing.T) {
	room := deskRoomWithBackground(t)
	room.begin(&protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "def", DeskMod: protocol.DeskShow,
	})
	if !room.Scene.ShowDesk {
		t.Fatal("test setup: a DeskShow message must start with the desk visible")
	}
	if room.Scene.DeskBase == "" {
		t.Fatal("test setup: begin must derive a desk base from the session background")
	}

	room.NotifyDeskMissing(room.Scene.DeskBase)
	if room.Scene.ShowDesk {
		t.Error("a conclusively-404'd desk must hide the desk on the miss signal, with no phase transition")
	}
	// Neither phase edge may bring it back: applyDeskMods runs the desk_mod table
	// through DeskDrawn, which the absence overrides in both columns.
	room.applyDeskMods(true)
	if room.Scene.ShowDesk {
		t.Error("the preanim phase edge resurrected a conclusively-missing desk")
	}
	room.applyDeskMods(false)
	if room.Scene.ShowDesk {
		t.Error("the talk phase edge resurrected a conclusively-missing desk")
	}
	// A NEW message on the same background must stay hidden too (begin re-derives
	// the same base, which is still in the missing set).
	room.begin(&protocol.ChatMessage{
		CharName: "Edgeworth", Emote: "normal", Side: "def", DeskMod: protocol.DeskShow,
	})
	if room.Scene.ShowDesk {
		t.Error("a later message on the same deskless background must still draw no desk")
	}
}

// TestNotifyDeskMissingWrongBaseIgnored pins the wrong-room contract: one Manager
// serves every room, so bases belonging to another room's scene arrive here too and
// must be a string-compare no-op (mirroring NotifyAssetMissing).
func TestNotifyDeskMissingWrongBaseIgnored(t *testing.T) {
	room := deskRoomWithBackground(t)
	room.begin(&protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "def", DeskMod: protocol.DeskShow,
	})
	room.NotifyDeskMissing(room.Scene.DeskBase + "-some-other-room")
	if !room.Scene.ShowDesk {
		t.Error("a miss for ANOTHER room's desk must not hide this room's desk")
	}
	room.NotifyDeskMissing("")
	if !room.Scene.ShowDesk {
		t.Error("an empty base must be ignored outright")
	}
}

// TestMissingDesksBounded pins rule §17.4: the conclusively-missing desk set is
// capped, and overflow simply stops recording (never grows, never panics).
func TestMissingDesksBounded(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	const overflow = 50
	for i := 0; i < missingDesksCap+overflow; i++ {
		room.NotifyDeskMissing(fmt.Sprintf("http://example.invalid/background/bg%d/defensedesk", i))
	}
	if got := len(room.missingDesks); got != missingDesksCap {
		t.Errorf("missingDesks grew to %d entries, want the %d cap", got, missingDesksCap)
	}
}
