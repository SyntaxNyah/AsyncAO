package ui

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/winexec"
)

// #71 copy-exported-clip-to-clipboard: put the finished FILE on the OS
// clipboard (a real file drop, not its path as text), so a fresh export pastes
// straight into Discord / Explorer. Windows-only for now — PowerShell's
// Set-Clipboard builds the CF_HDROP for us, spawned hidden (winexec) and
// off-thread by the caller's poll path. Elsewhere it reports false and the
// toast simply doesn't claim a copy.

// copyFileToClipboard places path on the clipboard as a file. Returns whether
// the platform supports it (the spawn itself is async fire-and-forget; a
// failure there is harmless — the file is still on disk either way).
func copyFileToClipboard(path string) bool {
	if runtime.GOOS != "windows" || path == "" {
		return false
	}
	// Single-quoted PowerShell literal; embedded quotes double. The path comes
	// from our own recordings writer, but quote defensively anyway.
	lit := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Set-Clipboard -LiteralPath "+lit)
	winexec.Hide(cmd)
	go func() { _ = cmd.Run() }() // fire-and-forget: never block the UI thread
	return true
}
