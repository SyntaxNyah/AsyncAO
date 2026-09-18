package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestZoomSide pins the position → speedline-side mapping (#126).
//
// `wit` is PROSECUTION: AO2's whole test is
// `side.startsWith("pro") || side == "hlp" || side.startsWith("wit")`, with the
// else-branch taking everything else (courtroom.cpp:3463-3471). v1.98.0 shipped it
// on the defense side, which drew the wrong burst for every witness.
func TestZoomSide(t *testing.T) {
	defense := map[string]bool{"def": true, "hld": true}
	prosecution := map[string]bool{"pro": true, "hlp": true, "wit": true}
	none := []string{"jud", "jur", "sea", "", "unknown"}

	for pos := range defense {
		if got := ZoomSide(pos); got != "defense" {
			t.Errorf("ZoomSide(%q) = %q, want defense", pos, got)
		}
	}
	for pos := range prosecution {
		if got := ZoomSide(pos); got != "prosecution" {
			t.Errorf("ZoomSide(%q) = %q, want prosecution", pos, got)
		}
	}
	for _, pos := range none {
		if got := ZoomSide(pos); got != "" {
			t.Errorf("ZoomSide(%q) = %q, want empty", pos, got)
		}
	}
}

// TestSpeedlinesURL pins the char-folder speedline base (#126).
func TestSpeedlinesURL(t *testing.T) {
	const origin = "http://x/base/"
	u := NewURLBuilder(origin)
	if got := u.Speedlines("Phoenix_Wright", "defense"); got != origin+"characters/phoenix_wright/defense_speedlines" {
		t.Errorf("defense speedlines = %q", got)
	}
	if got := u.Speedlines("Phoenix", "prosecution"); got != origin+"characters/phoenix/prosecution_speedlines" {
		t.Errorf("prosecution speedlines = %q", got)
	}
}

// TestZoomEmoteSetsSpeedlines pins that a zoom/preanim-zoom message stages the
// speaker's speedline overlay + side, and a non-zoom message clears them (#126).
//
// It deliberately does NOT pin any magnification: AO2's zoom never rescales the
// character layer, it only hides the desk + pair and plays the speedlines
// (courtroom.cpp:3456-3477). v1.98.0 magnified the speaker 1.5x on top of the
// character's own zoom preanimation art, which the playtest reported as a double
// zoom, so the magnification was removed rather than fixed.
func TestZoomEmoteSetsSpeedlines(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)

	for _, mod := range []int{protocol.EmoteModZoom, protocol.EmoteModPreanimZoom} {
		m := waitMsg("Phoenix", "normal", "hi")
		m.CharID = 1
		m.Side = "def"
		m.EmoteMod = mod
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		want := room.urls.Speedlines("Phoenix", "defense")
		if room.Scene.SpeedlinesBase != want {
			t.Fatalf("emote %d SpeedlinesBase = %q, want %q", mod, room.Scene.SpeedlinesBase, want)
		}
		if room.Scene.SpeedlinesSide != "defense" {
			t.Fatalf("emote %d SpeedlinesSide = %q, want defense", mod, room.Scene.SpeedlinesSide)
		}
		room.SkipToIdle()
	}

	m2 := waitMsg("Phoenix", "normal", "hi2")
	m2.CharID = 1
	m2.Side = "def"
	m2.EmoteMod = protocol.EmoteModIdle
	room.HandleEvent(Event{Kind: EventMessage, Message: m2})
	if room.Scene.SpeedlinesBase != "" || room.Scene.SpeedlinesSide != "" {
		t.Fatalf("non-zoom emote must clear speedlines: base=%q side=%q", room.Scene.SpeedlinesBase, room.Scene.SpeedlinesSide)
	}
}
