package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestZoomSide pins the position → speedline-side mapping (#126).
func TestZoomSide(t *testing.T) {
	defense := map[string]bool{"def": true, "wit": true, "hld": true}
	prosecution := map[string]bool{"pro": true, "hlp": true}
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
// speaker's speedline overlay + side + magnification, and a non-zoom message
// clears them (#126).
func TestZoomEmoteSetsSpeedlines(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)

	m := waitMsg("Phoenix", "normal", "hi")
	m.CharID = 1
	m.Side = "def"
	m.EmoteMod = protocol.EmoteModZoom
	room.HandleEvent(Event{Kind: EventMessage, Message: m})
	want := room.urls.Speedlines("Phoenix", "defense")
	if room.Scene.SpeedlinesBase != want {
		t.Fatalf("zoom emote SpeedlinesBase = %q, want %q", room.Scene.SpeedlinesBase, want)
	}
	if room.Scene.SpeedlinesSide != "defense" {
		t.Fatalf("zoom emote SpeedlinesSide = %q, want defense", room.Scene.SpeedlinesSide)
	}
	if room.Scene.Speaker.ZoomPct != ZoomScalePct {
		t.Fatalf("zoom emote ZoomPct = %d, want %d", room.Scene.Speaker.ZoomPct, ZoomScalePct)
	}

	room.SkipToIdle()

	m2 := waitMsg("Phoenix", "normal", "hi2")
	m2.CharID = 1
	m2.Side = "def"
	m2.EmoteMod = protocol.EmoteModIdle
	room.HandleEvent(Event{Kind: EventMessage, Message: m2})
	if room.Scene.SpeedlinesBase != "" || room.Scene.SpeedlinesSide != "" {
		t.Fatalf("non-zoom emote must clear speedlines: base=%q side=%q", room.Scene.SpeedlinesBase, room.Scene.SpeedlinesSide)
	}
	if room.Scene.Speaker.ZoomPct != 0 {
		t.Fatalf("non-zoom emote must clear ZoomPct, got %d", room.Scene.Speaker.ZoomPct)
	}
}
