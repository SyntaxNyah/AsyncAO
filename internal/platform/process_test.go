package platform_test

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestFindToolReportsAMissingProgramAsEmpty(t *testing.T) {
	if got := platform.FindTool("asyncao-no-such-tool"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestFindToolFindsAProgramOnPath(t *testing.T) {
	name := "asyncao-test-tool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir := t.TempDir()
	const executable = 0o755
	if err := os.WriteFile(filepath.Join(dir, name), nil, executable); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := platform.FindTool("asyncao-test-tool"); got == "" {
		t.Error("a program on PATH was not found")
	}
}
