package host_test

import (
	"errors"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
	"github.com/SyntaxNyah/AsyncAO/internal/platform/host"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	testWindowWidth  = 320
	testWindowHeight = 240
)

var testApp = host.App{Name: "AsyncAO test", Version: "test", ID: "io.github.SyntaxNyah.AsyncAO.test"}

func runMain(t *testing.T, client func(*host.Runtime) error) error {
	t.Helper()
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	return host.Main(testApp, platform.SystemProbe{}, client)
}

func TestMainRunsTheClientWithSDLStarted(t *testing.T) {
	var ran bool
	err := runMain(t, func(rt *host.Runtime) error {
		ran = true
		if rt.Platform.Environment.Arch == "" {
			t.Error("the client got no detected platform")
		}
		window, err := rt.OpenMainWindow(host.WindowSpec{Title: "test", Width: testWindowWidth, Height: testWindowHeight})
		if err != nil {
			return err
		}
		w, h := window.GetSize()
		if w != testWindowWidth || h != testWindowHeight {
			t.Errorf("window %dx%d, want %dx%d", w, h, testWindowWidth, testWindowHeight)
		}
		return nil
	})
	if err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	if !ran {
		t.Fatal("the client never ran")
	}
}

func TestMainReturnsTheClientError(t *testing.T) {
	want := errors.New("client failed")
	if got := runMain(t, func(*host.Runtime) error { return want }); !errors.Is(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Main releases SDL on the way out, so a process can start it again; a leak would fail this second run.
func TestMainReleasesWhatItAcquired(t *testing.T) {
	for range 2 {
		err := runMain(t, func(rt *host.Runtime) error {
			_, err := rt.OpenMainWindow(host.WindowSpec{Title: "test", Width: testWindowWidth, Height: testWindowHeight})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if sdl.WasInit(sdl.INIT_VIDEO) != 0 {
		t.Error("video still initialised after Main returned")
	}
}
