package host

import (
	"errors"
	"slices"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

var relaunchTestApp = App{Name: "AsyncAO test", Version: "test", ID: "io.github.SyntaxNyah.AsyncAO.test"}

// The new client must start only after this one has let go of SDL and saved its state.
func TestRelaunchHappensAfterSDLQuits(t *testing.T) {
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	want := []string{"-gpu-recovery=1"}
	var got []string
	started := false
	start := func(args []string) error {
		started = true
		got = args
		if sdl.WasInit(sdl.INIT_VIDEO) != 0 {
			t.Error("relaunched with SDL still running")
		}
		return nil
	}
	err := mainWith(relaunchTestApp, platform.SystemProbe{}, start, func(rt *Runtime) error {
		rt.RelaunchAfterExit(want)
		return nil
	})
	if err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	if !started || !slices.Equal(got, want) {
		t.Errorf("started %v with %v, want %v", started, got, want)
	}
}

func TestNoRelaunchUnlessAskedOrAfterAFailure(t *testing.T) {
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	start := func([]string) error {
		t.Error("relaunched")
		return nil
	}
	if err := mainWith(relaunchTestApp, platform.SystemProbe{}, start, func(*Runtime) error { return nil }); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	failure := errors.New("client failed")
	err := mainWith(relaunchTestApp, platform.SystemProbe{}, start, func(rt *Runtime) error {
		rt.RelaunchAfterExit(nil)
		return failure
	})
	if !errors.Is(err, failure) {
		t.Errorf("got %v, want %v", err, failure)
	}
}
