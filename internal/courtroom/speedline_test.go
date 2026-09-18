package courtroom

import (
	"slices"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestZoomSide pins the position → speedline-side mapping (#126).
//
// `wit` is PROSECUTION: AO2's whole test is
// `side.startsWith("pro") || side == "hlp" || side.startsWith("wit")`, with the
// else-branch taking everything else (courtroom.cpp:3463-3471). v1.98.0 shipped it
// on the defense side, which drew the wrong burst for every witness.
//
// IT IS ALSO NEVER "" NOW. v1.98.0 returned "" for judge/jury/seance as a
// deliberate deviation from AO2's catch-all else, and the playtest killed the
// deviation — an empty side drew NO burst at all for a judge zoom, and AO2's else
// branch already answers the question with defense_speedlines. Crystalwarrior:
// "in no circumstance should ZoomSide return ”".
func TestZoomSide(t *testing.T) {
	prosecution := []string{"pro", "hlp", "wit"}
	// Everything else, including the three positions v1.98.0 blanked and the empty
	// side (AO2's else branch catches all of them).
	defense := []string{"def", "hld", "jud", "jur", "sea", "", "unknown"}

	for _, pos := range prosecution {
		if got := ZoomSide(pos); got != "prosecution" {
			t.Errorf("ZoomSide(%q) = %q, want prosecution", pos, got)
		}
	}
	for _, pos := range defense {
		if got := ZoomSide(pos); got != "defense" {
			t.Errorf("ZoomSide(%q) = %q, want defense (the catch-all)", pos, got)
		}
	}
}

// TestSpeedlinesCandidates pins the ordered char-folder chain (#126 follow-up):
// the position's OWN <pos>_speedlines first, then the AO2 side stem — so a judge,
// jury or seance speaker can ship jud_speedlines / jur_speedlines /
// sea_speedlines, which AO2's hardcoded filename never allowed. Crystalwarrior:
// "Why the hell not. so jud_speedlines, jur_speedlines, etc. will all be valid to
// check for."
//
// The slice is NEVER empty and ALWAYS ends on the side stem, because a zoom must
// resolve to something (the bundled burst is the tier behind the chain's end).
func TestSpeedlinesCandidates(t *testing.T) {
	const origin = "http://x/base/"
	const ch = "judge"
	u := NewURLBuilder(origin)
	url := func(stem string) string { return origin + "characters/judge/" + stem }

	cases := []struct {
		pos  string
		want []string
	}{
		{"jud", []string{url("jud_speedlines"), url("defense_speedlines")}},
		{"jur", []string{url("jur_speedlines"), url("defense_speedlines")}},
		{"sea", []string{url("sea_speedlines"), url("defense_speedlines")}},
		{"def", []string{url("def_speedlines"), url("defense_speedlines")}},
		{"hld", []string{url("hld_speedlines"), url("defense_speedlines")}},
		// The pro/hlp/wit side: its own stem first, then prosecution_speedlines —
		// and NEVER defense_speedlines, which AO2's hardcode has no branch for.
		{"wit", []string{url("wit_speedlines"), url("prosecution_speedlines")}},
		{"hlp", []string{url("hlp_speedlines"), url("prosecution_speedlines")}},
		{"pro", []string{url("pro_speedlines"), url("prosecution_speedlines")}},
		// An empty side has no <pos> stem to try (there is no "_speedlines" name).
		{"", []string{url("defense_speedlines")}},
		{"unknown", []string{url("unknown_speedlines"), url("defense_speedlines")}},
	}
	for _, c := range cases {
		got := u.SpeedlinesCandidates(ch, c.pos)
		if !slices.Equal(got, c.want) {
			t.Errorf("SpeedlinesCandidates(%q, %q) = %q, want %q", ch, c.pos, got, c.want)
		}
		if len(got) == 0 {
			t.Errorf("SpeedlinesCandidates(%q, %q) is empty — a zoom always needs a candidate", ch, c.pos)
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
// speaker's speedline chain + side, and a non-zoom message clears them (#126).
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
		want := room.urls.SpeedlinesCandidates("Phoenix", "def")
		if !slices.Equal(room.Scene.SpeedlinesChain, want) {
			t.Fatalf("emote %d SpeedlinesChain = %q, want %q", mod, room.Scene.SpeedlinesChain, want)
		}
		if room.Scene.SpeedlinesSide != "defense" {
			t.Fatalf("emote %d SpeedlinesSide = %q, want defense", mod, room.Scene.SpeedlinesSide)
		}
		room.SkipToIdle()
	}

	// A JUDGE zoom stages a chain too — the v1.98.0 blank is what the playtest
	// killed. The judge has no side of their own, so the chain falls to defense.
	mj := waitMsg("Judge", "normal", "order")
	mj.CharID = 1
	mj.Side = "jud"
	mj.EmoteMod = protocol.EmoteModZoom
	room.HandleEvent(Event{Kind: EventMessage, Message: mj})
	if want := room.urls.SpeedlinesCandidates("Judge", "jud"); !slices.Equal(room.Scene.SpeedlinesChain, want) {
		t.Fatalf("judge zoom SpeedlinesChain = %q, want %q", room.Scene.SpeedlinesChain, want)
	}
	if room.Scene.SpeedlinesSide != "defense" {
		t.Fatalf("judge zoom SpeedlinesSide = %q, want defense (never blank)", room.Scene.SpeedlinesSide)
	}
	room.SkipToIdle()

	m2 := waitMsg("Phoenix", "normal", "hi2")
	m2.CharID = 1
	m2.Side = "def"
	m2.EmoteMod = protocol.EmoteModIdle
	room.HandleEvent(Event{Kind: EventMessage, Message: m2})
	if room.Scene.SpeedlinesChain != nil || room.Scene.SpeedlinesSide != "" {
		t.Fatalf("non-zoom emote must clear speedlines: chain=%q side=%q", room.Scene.SpeedlinesChain, room.Scene.SpeedlinesSide)
	}
}
