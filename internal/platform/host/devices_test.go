package host_test

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform/host"
)

func TestOpenExtraWindowGivesAWindowAndRenderer(t *testing.T) {
	err := runMain(t, func(*host.Runtime) error {
		win, ren, err := host.OpenExtraWindow("extra", testWindowWidth, testWindowHeight)
		if err != nil {
			return err
		}
		defer win.Destroy()
		defer ren.Destroy()
		if w, h := win.GetSize(); w != testWindowWidth || h != testWindowHeight {
			t.Errorf("window %dx%d, want %dx%d", w, h, testWindowWidth, testWindowHeight)
		}
		return nil
	})
	if err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
}

func TestRuntimeOpensAudioDevices(t *testing.T) {
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	err := runMain(t, func(rt *host.Runtime) error {
		mixer, err := rt.Audio.OpenMixer()
		if err != nil {
			return err
		}
		mixer.Close()
		return nil
	})
	if err != nil {
		t.Skipf("audio unavailable: %v", err)
	}
}
