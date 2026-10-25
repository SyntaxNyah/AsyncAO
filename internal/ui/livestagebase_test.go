package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestIsLiveStageBase pins item #9's widening: the held-frame bridge's steal
// gate (render/textures.go onEvict -> SetLiveScenery(a.IsLiveStageBase)) now
// covers the DRAWN speaker/pair sprite bases, not just scenery (bg + desk).
func TestIsLiveStageBase(t *testing.T) {
	const (
		bg      = "live://background/court/defenseempty"
		desk    = "live://background/court/defensedesk"
		speaker = "live://characters/witch/(a)talk"
		pair    = "live://characters/phoenix/(a)normal"
		other   = "live://characters/edgeworth/(a)thinking" // a base NOT on stage
	)

	a := &App{env: env{}}
	a.link.room = &courtroom.Courtroom{}
	sc := &a.link.room.Scene
	sc.BackgroundBase = bg
	sc.DeskBase = desk
	sc.Speaker.Active = speaker
	sc.Pair.Active = pair
	// Start with everything visible so the speaker/pair widening is exercised.
	sc.ShowDesk = true
	sc.Speaker.Visible = true
	sc.PairActive = true

	cases := []struct {
		name string
		base string
		want bool
	}{
		// The widening: the drawn speaker/pair are steal-eligible (FALSE pre-fix).
		{"speaker visible", speaker, true},
		{"pair active", pair, true},
		// The original scenery pair.
		{"background", bg, true},
		{"desk shown", desk, true},
		// Non-stage / empty / structural misses.
		{"unrelated base", other, false},
		{"empty base", "", false},
	}
	for _, c := range cases {
		if got := a.IsLiveStageBase(c.base); got != c.want {
			t.Errorf("%s: IsLiveStageBase(%q) = %v, want %v", c.name, c.base, got, c.want)
		}
	}

	// Each gate flag must actually gate: hide the layer and its base falls out,
	// while the always-on background stays in (so this proves the switch cases
	// are wired to the right flags, not that everything is on).
	sc.Speaker.Visible = false
	if a.IsLiveStageBase(speaker) {
		t.Error("a hidden speaker must not be steal-eligible (Speaker.Visible gate)")
	}
	sc.PairActive = false
	if a.IsLiveStageBase(pair) {
		t.Error("an inactive pair must not be steal-eligible (PairActive gate)")
	}
	sc.ShowDesk = false
	if a.IsLiveStageBase(desk) {
		t.Error("a hidden desk must not be steal-eligible (ShowDesk gate)")
	}
	if !a.IsLiveStageBase(bg) {
		t.Error("the background stays steal-eligible regardless of the layer gates")
	}

	// A nil room (not in a courtroom, not replaying) is never a live stage base.
	a.link.room = nil
	if a.IsLiveStageBase(bg) {
		t.Error("with no room the bridge gate must be off")
	}
}
