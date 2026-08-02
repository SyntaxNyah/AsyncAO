package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// newEffectsRig is the minimal App an FX-builder test needs: prefs on a temp file
// and nothing else. spriteFX/postFX read only prefs, so no renderer is required —
// which keeps this test running on a headless box with no SDL at all.
func newEffectsRig(t *testing.T) (*App, *config.AssetPreferences) {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	return &App{d: Deps{Prefs: prefs}}, prefs
}

// TestDisableEffectsSuppressesEveryLocalFX pins the master off-switch at the two
// render-side choke points that carry the LOCAL eye-candy. Every knob is turned
// ON first, so a regression that forgets to consult the master shows up as a
// non-zero field rather than as a default that happened to already be false.
//
// UserScaling is deliberately EXCLUDED from the suppression: it decides how big a
// sprite draws (char.ini / the enlarging rule), not whether it sparkles, so the
// test asserts it SURVIVES. Zeroing it would silently resize every character the
// moment someone asked for a calmer screen.
func TestDisableEffectsSuppressesEveryLocalFX(t *testing.T) {
	a, prefs := newEffectsRig(t)

	prefs.SetRainbowSprites(true)
	prefs.SetSpriteSolidTint(true)
	prefs.SetRainbowSpriteGlow(true)
	prefs.SetSpriteWobble(true)
	prefs.SetSpriteSpin(true)
	prefs.SetShoutPunch(true)
	prefs.SetAnimateEntrances(true)
	prefs.SetDepthOfField(true)
	prefs.SetSpotlight(true)
	prefs.SetReflection(true)
	prefs.SetPostCRT(true)
	prefs.SetPostVignette(true)
	prefs.SetPostScanlines(true)
	prefs.SetPostGrain(true)

	// Sanity: without the master these really are on, so the assertions below
	// prove suppression rather than an accidentally-empty struct.
	if got := a.spriteFX(); !got.Rainbow || !got.Spin || !got.Entrance {
		t.Fatalf("rig failed to enable the sprite FX: %+v", got)
	}
	if got := a.postFX(); !got.CRT || !got.Grain {
		t.Fatalf("rig failed to enable the post FX: %+v", got)
	}

	prefs.SetDisableEffects(true)

	wantScaling := courtroom.ScalingMode(prefs.SpriteScalingMode())
	if got, want := a.spriteFX(), (render.SpriteFX{UserScaling: wantScaling}); got != want {
		t.Errorf("spriteFX under the master off-switch = %+v, want everything but UserScaling zeroed (%+v)", got, want)
	}
	if got := a.postFX(); got != (render.PostFX{}) {
		t.Errorf("postFX under the master off-switch = %+v, want the zero value", got)
	}

	// Turning it back off restores the player's own choices untouched — the master
	// overrules, it must never rewrite the individual preferences.
	prefs.SetDisableEffects(false)
	if got := a.spriteFX(); !got.Rainbow || !got.Spin || !got.Entrance {
		t.Errorf("unticking the master didn't restore the user's FX: %+v", got)
	}
	if got := a.postFX(); !got.CRT || !got.Grain {
		t.Errorf("unticking the master didn't restore the user's post FX: %+v", got)
	}
}

// TestDisableEffectsSuppressesIncomingReactions pins the transmitted half: another
// player's emoji reaction must not float when the viewer asked for no effects, even
// though the dedicated HideReactions opt-out is untouched.
func TestDisableEffectsSuppressesIncomingReactions(t *testing.T) {
	a, prefs := newEffectsRig(t)
	a.icLog = []icEntry{{speaker: "Phoenix", ref: 4242}}

	a.onIncomingReaction(courtroom.WireReaction{Ref: 4242, Index: 2})
	if len(a.reactionFloats) != 1 {
		t.Fatalf("baseline reaction didn't float (got %d)", len(a.reactionFloats))
	}

	a.reactionFloats = a.reactionFloats[:0]
	prefs.SetDisableEffects(true)
	a.onIncomingReaction(courtroom.WireReaction{Ref: 4242, Index: 2})
	if len(a.reactionFloats) != 0 {
		t.Error("the master off-switch didn't suppress an incoming reaction float")
	}
	if prefs.HideReactionsOn() {
		t.Error("the master off-switch rewrote HideReactions; it must only overrule it")
	}
}

// TestDisableEffectsRoundTrips pins the save/load half — the bug class where a new
// preference persists but is never read back (see TestEverySavedPreferenceIsAlsoLoaded).
func TestDisableEffectsRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), config.PrefsFileName)
	prefs, err := config.New(path)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.EffectsDisabled() {
		t.Error("the master off-switch must default to OFF (effects render)")
	}
	prefs.SetDisableEffects(true)
	if err := prefs.SaveNow(); err != nil {
		t.Fatal(err)
	}
	_ = prefs.Close()

	reloaded, err := config.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	if !reloaded.EffectsDisabled() {
		t.Error("the master off-switch did not survive a save/load round trip")
	}
}
