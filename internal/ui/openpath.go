package ui

import (
	"log"
	"path/filepath"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// openInFileManager opens a folder in the file manager, or a file in its default program.
func (a *App) openInFileManager(path string) {
	if err := a.d.Desktop.OpenPath(path); err != nil {
		log.Printf("ui: opening %s: %v", path, err)
	}
}

// openConfigFolder opens the AsyncAO config directory (preferences, notebooks, jukebox library) in the file manager.
func (a *App) openConfigFolder() {
	if d := configDir(); d != "" {
		a.openInFileManager(d)
	}
}

// openSettingsFile opens asset_preferences.json itself with the OS default handler.
func (a *App) openSettingsFile() {
	if p, err := config.DefaultPath(); err == nil {
		a.openInFileManager(p)
	}
}

// configDir returns the config directory path for display, or "" if it can't be resolved.
func configDir() string {
	if p, err := config.DefaultPath(); err == nil {
		return filepath.Dir(p)
	}
	return ""
}
