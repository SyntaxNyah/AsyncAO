package host_test

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform/host"
)

// A missing window, or the dummy driver's window with no native handle, reports failure and must not crash.
func TestLinkWindowOwnerDegradesSafely(t *testing.T) {
	if host.LinkWindowOwner(nil, nil) {
		t.Error("linked two missing windows")
	}
	err := runMain(t, func(rt *host.Runtime) error {
		window, err := rt.OpenMainWindow(host.WindowSpec{Title: "test", Width: testWindowWidth, Height: testWindowHeight})
		if err != nil {
			return err
		}
		if host.LinkWindowOwner(window, nil) {
			t.Error("linked to a missing owner")
		}
		return nil
	})
	if err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
}
