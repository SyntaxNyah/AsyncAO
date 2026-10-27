package ui

import "testing"

// Connect and disconnect rebuild the per-tab session state. App-wide values that
// are set once — at boot or from Settings — must survive that rebuild.

func TestSpriteCapBaseSurvivesSessionReset(t *testing.T) {
	const displayBase = 1080
	a := testTabApp(t)
	a.SetSpriteCapBase(displayBase)
	a.resetSessionState()
	if a.spriteCapBase != displayBase {
		t.Fatalf("sprite cap base = %d after a connect, want %d: the downscale sliders would compute the cap from 0 (no cap)",
			a.spriteCapBase, displayBase)
	}
}
