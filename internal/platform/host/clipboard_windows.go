package host

import (
	"os/exec"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/winexec"
)

// CopyFileToClipboard puts path on the clipboard as a real file drop and reports whether it tried; PowerShell's
// Set-Clipboard builds CF_HDROP, which SDL's clipboard can't, and the spawn never blocks the caller.
func (Desktop) CopyFileToClipboard(path string) bool {
	if path == "" {
		return false
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Set-Clipboard -LiteralPath "+powerShellLiteral(path))
	winexec.Hide(cmd)
	go func() { _ = cmd.Run() }()
	return true
}

// powerShellLiteral quotes s as a single-quoted string, in which only an embedded quote needs doubling.
func powerShellLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
