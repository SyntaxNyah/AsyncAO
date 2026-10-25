package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
)

// writeDecodeCrash best-effort appends a decode-worker panic + full stack to
// asyncao-crash.log next to the executable. A panic on a background decode
// goroutine is invisible to the render thread's frameCrashLog (which only
// guards the frame loop), so without this the crash leaves no trace behind.
// It does NOT mask the bug: the caller re-panics so the process still dies.
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
