//go:build !windows && !darwin

package host

import (
	"log"
	"path/filepath"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// CopyFileToClipboard offers path as a file for as long as this process runs, unless a clipboard manager keeps it.
func (Desktop) CopyFileToClipboard(_ *sdl.Window, path string) bool {
	if path == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if err := sdl.SetClipboardData(fileClipboardData(abs)); err != nil {
		log.Printf("clipboard: %v", err)
		return false
	}
	return true
}
