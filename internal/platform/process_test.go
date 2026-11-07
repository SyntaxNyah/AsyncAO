package platform_test

import (
	"os"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

func TestCommandRunsTheProgram(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The test binary lists its tests and exits, with no window on any OS.
	if err := platform.Command(exe, "-test.list=^$").Run(); err != nil {
		t.Errorf("run: %v", err)
	}
}
