package ui

import (
	"os"
	"path/filepath"
	"sync"
)

// exeDataMu guards exeDataRoot and downloadsRootMemo. Tests set the root while goroutines under test (such as the
// async .aorec write) are still reading it, so an unguarded package var races under -race.
var (
	exeDataMu         sync.RWMutex
	exeDataRoot       string
	downloadsRootMemo string
)

// exeDataDir is the base directory for exe-adjacent data: the injected test root when set, else the binary's
// directory.
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

// setExeDataRoot points every exe-adjacent path at root and drops the downloads memo built from the previous root.
// Tests use it through withExeDataRoot.
func setExeDataRoot(root string) {
	exeDataMu.Lock()
	exeDataRoot = root
	downloadsRootMemo = ""
	exeDataMu.Unlock()
}
