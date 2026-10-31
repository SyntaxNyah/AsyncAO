package platform

import (
	"fmt"
	"os"
	"runtime"
)

// CurrentOS is the OS this process runs on.
func CurrentOS() OS { return detectOS(runtime.GOOS) }

// UpdateLayout is how a release of the client is laid out on this OS.
type UpdateLayout struct {
	// Bundle means the release is an archive of the binary and its libraries, updated together.
	Bundle bool
	// LibSubdir is the folder of bundled libraries beside the binary, "" when they sit next to it.
	LibSubdir string
}

// UpdateLayoutFor returns the layout on osKind: Windows ships DLLs beside the exe, macOS a binary and lib/ so a SONAME
// bump can't strand a stale library, and Linux a single-file AppImage.
func UpdateLayoutFor(osKind OS) UpdateLayout {
	switch osKind {
	case Windows:
		return UpdateLayout{Bundle: true}
	case MacOS:
		return UpdateLayout{Bundle: true, LibSubdir: "lib"}
	}
	return UpdateLayout{}
}

// RestartProgram starts this program again with args; the caller exits right after.
func RestartProgram(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("relaunch: %w", err)
	}
	return Command(exe, args...).Start()
}
