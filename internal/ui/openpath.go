package ui

import (
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// openInFileManager opens a folder in the OS file manager (Windows Explorer /
// macOS Finder / Linux xdg-open), fire-and-forget so it never blocks the frame.
func openInFileManager(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}

// openConfigFolder opens the AsyncAO config directory — where asset_preferences.json
// (every setting: layout, favourites, hotkeys, colours…), the per-server case
// notebooks and the jukebox library live — in the OS file manager.
func openConfigFolder() {
	if d := configDir(); d != "" {
		openInFileManager(d)
	}
}

// openSettingsFile opens asset_preferences.json itself with the OS default handler.
func openSettingsFile() {
	if p, err := config.DefaultPath(); err == nil {
		openInFileManager(p)
	}
}

// configDir returns the AsyncAO config directory's path for display, or "" if it
// can't be resolved.
func configDir() string {
	if p, err := config.DefaultPath(); err == nil {
		return filepath.Dir(p)
	}
	return ""
}
