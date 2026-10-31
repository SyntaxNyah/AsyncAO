package ui

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/winexec"
)

// Copies the finished file to the OS clipboard as a real file drop. Windows only: PowerShell's Set-Clipboard builds
// CF_HDROP.

// copyFileToClipboard reports whether the platform supports it; the spawn is fire-and-forget.
func copyFileToClipboard(path string) bool {
	if runtime.GOOS != "windows" || path == "" {
		return false
	}
	// Single-quoted PowerShell literal; embedded quotes double.
	lit := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Set-Clipboard -LiteralPath "+lit)
	winexec.Hide(cmd)
	go func() { _ = cmd.Run() }() // fire-and-forget: never block the UI thread
	return true
}
