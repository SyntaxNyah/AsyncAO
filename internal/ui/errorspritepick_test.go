package ui

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestErrorSpritePickSwitchesTheCustomImageOn: a picked file becomes the custom image, switched on and loading; a
// cancelled or failed dialog changes nothing.
func TestErrorSpritePickSwitchesTheCustomImageOn(t *testing.T) {
	const loadWait = 5 * time.Second
	a := expApp(t)
	a.errorSpriteRes = make(chan errorSpriteLoad, 1)
	t.Cleanup(func() { settings.errorSpritePickBusy = false })

	for _, pick := range []dialogPick{{}, {err: errors.New("no portal")}} {
		settings.errorSpritePickRes <- pick
		a.pollErrorSpritePick()
		if got := a.d.Prefs.Load().CustomErrorSprite(); got != "" {
			t.Errorf("%+v switched the custom image on with %q", pick, got)
		}
	}

	picked := filepath.Join(t.TempDir(), "missing.png")
	settings.errorSpritePickBusy = true
	settings.errorSpritePickRes <- dialogPick{paths: []string{picked}}
	a.pollErrorSpritePick()
	if settings.errorSpritePickBusy {
		t.Error("the picker still counts as open")
	}
	if got := a.d.Prefs.Load().CustomErrorSprite(); got != picked {
		t.Errorf("custom image = %q, want the picked %q", got, picked)
	}
	select {
	case res := <-a.errorSpriteRes:
		if res.err == nil {
			t.Error("a file that doesn't exist loaded")
		}
	case <-time.After(loadWait):
		t.Fatal("picking a file did not start loading it")
	}
	a.d.Prefs.Update(func(sn *config.Settings) { sn.ErrorSpriteCustom = false })
}
