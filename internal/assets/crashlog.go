package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
)

// writeDecodeCrash appends a decode-worker panic to asyncao-crash.log; frameCrashLog only guards the frame loop.
func writeDecodeCrash(r any) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	path := filepath.Join(filepath.Dir(exe), "asyncao-crash.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "\n=== decode worker panic ===\n%v\n%s\n", r, debug.Stack())
}
