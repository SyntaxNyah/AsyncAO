package ui

import "testing"

// withExeDataRoot points every exe-adjacent data path (recordings, logs,
// screenshots, downloads and the two export files) at a per-test t.TempDir().
func withExeDataRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exeDataMu.RLock()
	old := exeDataRoot
	exeDataMu.RUnlock()
	setExeDataRoot(dir)
	t.Cleanup(func() { setExeDataRoot(old) })
	return dir
}

// setExeDataRoot is the test-only writer for the seam; production only reads it.
func setExeDataRoot(root string) {
	exeDataMu.Lock()
	exeDataRoot = root
	downloadsRootMemo = ""
	exeDataMu.Unlock()
}
