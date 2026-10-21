package ui

import (
	"os"
	"path/filepath"
	"sync"
)

// exeDataMu guards exeDataRoot and downloadsRootMemo. Tests set the root while
// goroutines spawned by the code under test (the async .aorec write, for one)
// are still reading it, so a plain package var raced under -race: the CI race
// job caught TestStopRecordingPrefOffNoBundle on exactly that overlap.
var (
	exeDataMu         sync.RWMutex
	exeDataRoot       string
	downloadsRootMemo string
)

// exeDataDir is the base directory for exe-adjacent data: the injected test
// root when one is set, else the running binary's directory.
func exeDataDir() (string, error) {
	exeDataMu.RLock()
	root := exeDataRoot
	exeDataMu.RUnlock()
	if root != "" {
		return root, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// setExeDataRoot points every exe-adjacent path at root and drops the downloads
// memo built from the previous root. Test-only (datadir_test.go), so it lives in
// the test file: the production path only ever reads the root.
