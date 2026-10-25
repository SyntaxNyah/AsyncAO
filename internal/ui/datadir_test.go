package ui

import "testing"

// withExeDataRoot points every exe-adjacent data path (recordings, logs,
// screenshots, downloads and the two export files) at a per-test t.TempDir().
// `go test` reuses one cached test binary, so two concurrent
// `go test ./internal/ui` runs would otherwise share os.Executable()'s
// directory and race on the same files (TestStopRecordingSkipsWhenJobBusy was
// the observed casualty). No test in this package calls t.Parallel, so a
// package-level override is race-free within one process; separate processes
// have separate variables.
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
