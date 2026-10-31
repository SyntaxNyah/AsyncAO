package host_test

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform/host"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
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
	const (
		sampleRate = 48000
		channels   = 1
	)
	t.Setenv("SDL_AUDIO_DRIVER", "dummy")
	err := runMain(t, func(rt *host.Runtime) error {
		mixer, err := rt.Audio.OpenMixer()
		if err != nil {
			return err
		}
		mixer.Close()
		spec := sdl.AudioSpec{Freq: sampleRate, Format: sdl.AUDIO_S16SYS, Channels: channels}
		stream, err := rt.Audio.OpenPlayback(&spec)
		if err != nil {
			return err
		}
		stream.Destroy()
		return nil
	})
	if err != nil {
		t.Skipf("audio unavailable: %v", err)
	}
}
