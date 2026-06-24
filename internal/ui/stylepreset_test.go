package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

func presetTestApp(t *testing.T) *App {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	return &App{d: Deps{Prefs: prefs}, ctx: &Ctx{}, macroBind: -1} // -1 = no macro bind armed (real App default)
}

// TestStylePresetSaveApply pins #126's round-trip: saving captures the current style + colour +
// the selected emote's NAME, and applying restores all three (the emote resolved by name into
// the current character's emote list).
func TestStylePresetSaveApply(t *testing.T) {
	a := presetTestApp(t)
	a.emotes = []courtroom.Emote{{Anim: "happy"}, {Anim: "angry"}}
	a.emoteIdx = 1 // "angry"
	a.icColor = 3
	a.d.Prefs.SetSpriteStyle(config.SpriteStylePref{Tint: true, R: 200, Glow: true})

	a.saveStylePreset("Rage")
	ps := a.d.Prefs.StylePresets()
	if len(ps) != 1 || ps[0].Name != "Rage" || ps[0].Color != 3 || ps[0].Emote != "angry" {
		t.Fatalf("saved preset = %+v", ps)
	}
	if !ps[0].Style.Tint || ps[0].Style.R != 200 || !ps[0].Style.Glow {
		t.Errorf("saved style fields wrong: %+v", ps[0].Style)
	}

	// Change everything, then apply the preset back.
	a.icColor, a.emoteIdx = 0, 0
	a.d.Prefs.SetSpriteStyle(config.SpriteStylePref{})
	a.applyStylePreset(ps[0])
	if a.icColor != 3 {
		t.Errorf("colour not applied: %d", a.icColor)
	}
	if a.emoteIdx != 1 {
		t.Errorf("emote-by-name not applied: emoteIdx=%d, want 1 (angry)", a.emoteIdx)
	}
	if s := a.d.Prefs.SpriteStyle(); !s.Tint || s.R != 200 || !s.Glow {
		t.Errorf("style not applied: %+v", s)
	}
}

// TestStylePresetKeyApply pins the keybind path: a key-bound preset is applied on a bare
// keypress (with no field focused / no chord), and consumes the key.
func TestStylePresetKeyApply(t *testing.T) {
	a := presetTestApp(t)
	// Bind whatever name SDL gives this keycode (headless GetKeyName may differ), so the bind
	// and the lookup agree; skip only if key names aren't resolvable at all.
	key := strings.ToLower(sdl.GetKeyName(sdl.K_F2))
	if key == "" {
		t.Skip("SDL key names unavailable headless")
	}
	a.d.Prefs.AddStylePreset(config.StylePreset{Name: "Calm", Color: 5})
	a.d.Prefs.SetStylePresetKey(0, key)

	a.ctx.keyPressed = sdl.K_F2
	if !a.handleStylePresetKeys() {
		t.Fatal("a bound key didn't apply its preset")
	}
	if a.icColor != 5 {
		t.Errorf("preset colour not applied via keybind: %d", a.icColor)
	}

	// A focused field suppresses it (so typing the key never swaps styles).
	a.icColor = 0
	a.ctx.focusID = "ic"
	if a.handleStylePresetKeys() {
		t.Error("style keybind fired while a text field was focused")
	}
}
