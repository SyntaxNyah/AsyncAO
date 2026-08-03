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

// TestBackgroundChangeReDerivesDesk pins the reported "the background is missing
// its desk on purpose, but the default desk appears anyway".
//
// ShowDesk is derived from the DESK BASE's residency. setBackground rewrites that
// base and used to leave ShowDesk alone, so a room whose desk 404s inherited the
// PREVIOUS room's answer and kept drawing its desk — for the rest of the message,
// because a settled message has no further phase edge to correct it.
//
// It also pins the hazard: applyDeskMods recomputes PairActive and the speaker
// offsets from c.current too, so re-deriving at the WRONG phase would flip pair
// state mid-preanim. Passing the current phase must leave both untouched.
func TestBackgroundChangeReDerivesDesk(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)

	// A settled message with a desk, a live pair and a speaker offset.
	room.current = &protocol.ChatMessage{
		DeskMod:     protocol.DeskShow,
		SelfOffsetX: 37,
		SelfOffsetY: 11,
		Pair:        protocol.PairInfo{CharID: 4, Name: "partner"},
	}
	room.phase = PhaseTalking
	room.Scene.Position = "wit"
	room.setBackground("hasdesk", "", false)
	if !room.Scene.ShowDesk {
		t.Fatal("fixture: a DeskShown message on an unknown-but-not-missing desk should draw one")
	}
	pairBefore, offXBefore, offYBefore := room.Scene.PairActive, room.Scene.Speaker.OffsetX, room.Scene.Speaker.OffsetY

	// Now move to a room whose desk is conclusively missing, exactly as the
	// warning relay reports it.
	room.Scene.Position = "wit"
	nextDesk := room.urls.Background("nodesk", func() string { _, d := PositionScene("wit"); return d }())
	room.recordMissingDesk(nextDesk)
	room.setBackground("nodesk", "", false)

	if room.Scene.ShowDesk {
		t.Error("the new room's desk is conclusively missing, but the desk still draws — the previous room's answer survived")
	}
	// The pair and the offsets are NOT the desk's business.
	if room.Scene.PairActive != pairBefore {
		t.Errorf("PairActive flipped to %v on a background change", room.Scene.PairActive)
	}
	if room.Scene.Speaker.OffsetX != offXBefore || room.Scene.Speaker.OffsetY != offYBefore {
		t.Errorf("speaker offset moved to (%d,%d), want (%d,%d) — the phase argument is wrong",
			room.Scene.Speaker.OffsetX, room.Scene.Speaker.OffsetY, offXBefore, offYBefore)
	}
}

// TestBackgroundChangeWithNoMessageIsSafe pins the guard: joining a room before
// any message has landed must not dereference a nil c.current.
func TestBackgroundChangeWithNoMessageIsSafe(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.Scene.Position = "wit"
	room.setBackground("somewhere", "", false) // must not panic
	if room.Scene.BackgroundBase == "" {
		t.Error("setBackground did not resolve a background base")
	}
}

// TestLateDeskUploadHealsTheMissingSet pins the other half of the desk state.
//
// missingDesks was insert-only, so a desk that 404'd once stayed absent for the
// life of the Courtroom — even after it actually arrived (a server repack, a
// local mount added, a format learned). The RENDER side already self-heals
// (TextureStore.clearMissing on upload), so the two disagreed: the texture was
// resident and ShowDesk still said no, for the rest of the session.
//
// It also makes DeskResolved a real state. It was declared and unreachable.
func TestLateDeskUploadHealsTheMissingSet(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.current = &protocol.ChatMessage{DeskMod: protocol.DeskShow}
	room.phase = PhaseTalking
	room.Scene.Position = "wit"
	room.setBackground("repacked", "", false)

	desk := room.Scene.DeskBase
	room.NotifyDeskMissing(desk)
	if room.Scene.ShowDesk {
		t.Fatal("a conclusively-missing desk must stop drawing")
	}
	if got := room.deskResolution(); got != DeskAbsent {
		t.Fatalf("deskResolution = %v, want DeskAbsent", got)
	}

	// The desk lands after all. The residency probe is the same one the sprite
	// wait-gate uses; an unwired room keeps the old two-state answer.
	resident := map[string]bool{}
	room.SpriteReady = func(base string) bool { return resident[base] }
	if got := room.deskResolution(); got != DeskAbsent {
		t.Error("a probe that reports NOT resident must leave the miss standing")
	}
	resident[desk] = true

	if got := room.deskResolution(); got != DeskResolved {
		t.Errorf("deskResolution = %v after the desk uploaded, want DeskResolved", got)
	}
	if _, stale := room.missingDesks[desk]; stale {
		t.Error("the miss survived the upload — it would re-assert on the next re-derive")
	}
	room.applyDeskMods(false)
	if !room.Scene.ShowDesk {
		t.Error("the desk is resident but still hidden — the render and courtroom sides disagree")
	}
}
