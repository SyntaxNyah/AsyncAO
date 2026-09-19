package ui

import (
	"path/filepath"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestDisableAltEmoteRowGatesNumberRow pins the kill-switch: with the pref on, a
// valid Alt+1 press over a laid-out grid must NOT select an emote. The selection
// path refocuses the IC input (selectEmote → FocusField), so a still-empty
// focusNext proves handleEmoteKeys returned before doing anything.
func TestDisableAltEmoteRowGatesNumberRow(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	a := &App{d: Deps{Prefs: prefs}, ctx: &Ctx{}}
	a.emotePerPage = 9
	a.emotes = []courtroom.Emote{{Comment: "normal"}}
	a.emoteVisible = []int{0}
	a.ctx.keyPressed, a.ctx.altHeld = sdl.K_1, true

	prefs.SetDisableAltEmoteRow(true)
	a.handleEmoteKeys()

	if a.ctx.focusNext != "" {
		t.Errorf("a disabled number row still picked an emote: focusNext = %q", a.ctx.focusNext)
	}
	if a.emoteIdx != 0 {
		t.Errorf("a disabled number row changed the selection to %d", a.emoteIdx)
	}
}
