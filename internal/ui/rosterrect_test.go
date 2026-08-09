package ui

// Gates for THE ROSTER'S RULING under an AO2 theme (theme_layout.go states it):
// the player list is drawn at AO2's own `player_list` rect (courtroom.cpp:879)
// when the theme declares one, and shares music_list — AO2's own reuse, since
// ui_area_list (:864) and ui_music_list (:867) already share it — when it does not.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// rosterLayout is a themed layout declaring exactly the named rects.
func rosterLayout(keys ...string) *themeLayoutCache {
	l := &themeLayoutCache{valid: true, r: make(map[string]sdl.Rect, len(keys))}
	for _, k := range keys {
		l.r[k] = sdl.Rect{X: 0, Y: 0, W: 220, H: 240}
	}
	return l
}

// chipTabs is the tab list a strip carries, for readable failure messages.
func chipTabs(specs []themedChipSpec) []int {
	out := make([]int, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.tab)
	}
	return out
}

// TestThemedListStripDropsWhatTheThemePlaced pins the parity rule the strip
// implements: every chip the strip carries is a view the THEME left unreachable,
// and every control the theme did place removes its chip. The both-placed row is
// the one this ruling adds — an empty strip is correct there, not a bug.
func TestThemedListStripDropsWhatTheThemePlaced(t *testing.T) {
	for _, tc := range []struct {
		name string
		lay  *themeLayoutCache
		want []int
		why  string
	}{
		{
			"bare theme", rosterLayout("music_list"),
			[]int{logTabMusic, logTabAreas, logTabPlayers},
			"nothing is reachable any other way, so the strip owes all three",
		},
		{
			"switch_area_music placed", rosterLayout("music_list", "switch_area_music"),
			[]int{logTabPlayers},
			"AO2's own Music/Areas switch is on screen; only the roster is still owed",
		},
		{
			"player_list placed", rosterLayout("music_list", "player_list"),
			[]int{logTabMusic, logTabAreas},
			"the roster has its own AO2 rect, so its chip would point at a panel it no longer uses",
		},
		{
			"both placed", rosterLayout("music_list", "player_list", "switch_area_music"),
			nil,
			"the theme placed a control for every view — a chip strip here would be pure duplication",
		},
	} {
		got := chipTabs(themedListStrip(tc.lay))
		if len(got) != len(tc.want) {
			t.Errorf("%s: strip = %v, want %v (%s)", tc.name, got, tc.want, tc.why)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: strip = %v, want %v (%s)", tc.name, got, tc.want, tc.why)
				break
			}
		}
	}
}

// TestEmptyStripHandsTheWholePanelToTheList pins the consequence of the
// both-placed row: fitThemedChips must answer zero chips (not one mystery chip,
// not a negative body) and the caller's body must be the whole panel.
func TestEmptyStripHandsTheWholePanelToTheList(t *testing.T) {
	body := sdl.Rect{X: 10, Y: 20, W: 220, H: 240}
	_, n := fitThemedChips(body, themedListStrip(rosterLayout("music_list", "player_list", "switch_area_music")),
		themedListViews[:], logTabMusic)
	if n != 0 {
		t.Fatalf("an empty strip laid out %d chip(s), want 0", n)
	}
	if got := themedStripBody(body, n); got != body {
		t.Errorf("with no strip the list must get the whole body: got %+v, want %+v", got, body)
	}
}

// TestPlayerListRectIsDrawnNotMerelyIngested is the encapsulation gate. The
// `player_list` slot sat at slotStateInert — ingested for audit, painted by
// nothing — which is exactly the parsed-but-never-applied shape CLAUDE.md names:
// the key showed up in the theme editor and in the census while the roster went on
// squatting music_list. Both halves are pinned, because either alone is still the
// bug: the slot must claim to be drawn, and the courtroom must actually draw it.
func TestPlayerListRectIsDrawnNotMerelyIngested(t *testing.T) {
	found := false
	for _, s := range themeSlots {
		if s.key != "player_list" {
			continue
		}
		found = true
		if s.state == slotStateInert {
			t.Error("player_list is back to slotStateInert — the roster ruling says AsyncAO paints there")
		}
	}
	if !found {
		t.Fatal("no player_list slot — AO2 declares it at courtroom.cpp:879 and the census must carry it")
	}
	body := funcBodySource(t, "theme_layout.go", "drawCourtroomThemed")
	if !containsCall(body, "drawPlayerList") {
		t.Fatal("the themed courtroom never draws the roster at all")
	}
	// The strip is what stops the roster being painted TWICE when it has its own
	// rect; a draw site that stopped consulting it would run both.
	if !containsCall(body, "themedListStrip") {
		t.Fatal("drawCourtroomThemed no longer asks themedListStrip which views the panel owes")
	}
}
