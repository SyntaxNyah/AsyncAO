package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

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

func TestPartColorsSurviveSessionReset(t *testing.T) {
	const logTint = "ff0080"
	want := sdl.Color{R: 0xff, G: 0, B: 0x80, A: 0xff}
	a := testTabApp(t)
	a.d.Prefs.SetLayoutPartColor(partLog, logTint)
	a.refreshPartColors()
	a.resetSessionState()
	if col, ok := a.partPanel(partLog); !ok || col != want {
		t.Fatalf("log tint after a connect = %v/%v, want %v/on", col, ok, want)
	}
}
