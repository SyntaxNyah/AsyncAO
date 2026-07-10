package render

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestAmbientAnimating pins the viewport's ambient-FX census — the draw-site
// replacement for wantsFullRate's old pref-knob checks (the "knob not state"
// anti-pattern that held uncapped full rate on every screen, lobby and
// Settings included — the idle-CPU-burn report). Knobs alone with NO sprite on
// stage report nothing; a drawn sprite plus a clock-driven wash / transmitted
// style / weather reports animating; static styles (a plain tint) do not.
func TestAmbientAnimating(t *testing.T) {
	v := NewViewport(nil)
	scene := &courtroom.Scene{}

	// A wash knob with no sprite drawn: quiet — this exact case burned CPU in
	// the lobby/Settings under the old knob checks.
	v.SetSpriteFX(SpriteFX{Rainbow: true})
	if v.AmbientAnimating(scene) {
		t.Fatal("a wash knob with NO sprite on stage must not report animating (knob-not-state)")
	}
	scene.Speaker.Visible = true
	if !v.AmbientAnimating(scene) {
		t.Fatal("rainbow wash over a drawn speaker must report animating")
	}
	v.SetSpriteFX(SpriteFX{})
	if v.AmbientAnimating(scene) {
		t.Fatal("no wash, no style, no weather: a plain speaker is not ambient-animating")
	}

	// Breathing needs the master AND a visible component (both off = no motion).
	v.SetSpriteFX(SpriteFX{IdleBreath: true})
	if v.AmbientAnimating(scene) {
		t.Fatal("breathing with both components off has no visible motion")
	}
	v.SetSpriteFX(SpriteFX{IdleBreath: true, BreathBob: true})
	if !v.AmbientAnimating(scene) {
		t.Fatal("breathing (bob) over a drawn speaker must report animating")
	}
	v.SetSpriteFX(SpriteFX{})

	// A transmitted style: static recolours are not motion; the clock-driven
	// ones are — including the #34 path, which the old wantsFullRate style
	// check missed entirely (a path-styled settled sprite froze at idle=0).
	scene.Speaker.Style = courtroom.SpriteStyle{Tint: true, R: 255}
	if v.AmbientAnimating(scene) {
		t.Fatal("a static tint style must not report animating")
	}
	scene.Speaker.Style = courtroom.SpriteStyle{HueCycle: true}
	if !v.AmbientAnimating(scene) {
		t.Fatal("a hue-cycle style over a drawn speaker must report animating")
	}
	scene.Speaker.Style = courtroom.SpriteStyle{PathLen: 2}
	if !v.AmbientAnimating(scene) {
		t.Fatal("a path-motion style must report animating")
	}
	scene.Speaker.Style = courtroom.SpriteStyle{}

	// The pair layer censuses like the speaker.
	scene.Speaker.Visible = false
	scene.PairActive = true
	scene.Pair.Style = courtroom.SpriteStyle{Wobble: true}
	if !v.AmbientAnimating(scene) {
		t.Fatal("a wobble style on the pair layer must report animating")
	}
	scene.PairActive = false
	scene.Pair.Style = courtroom.SpriteStyle{}

	// Weather animates the whole stage, sprites or not; off goes quiet.
	v.SetWeather(WeatherSnow, 50)
	if !v.AmbientAnimating(scene) {
		t.Fatal("live weather must report animating")
	}
	v.SetWeather(WeatherNone, 0)
	if v.AmbientAnimating(scene) {
		t.Fatal("weather off must go quiet")
	}
}
