package ui

import (
	"os"
	"path/filepath"
)

// exeDataRoot overrides the directory exe-adjacent data lives in: recordings/,
// logs/, screenshots/, downloads/ and the two export files. Empty in
// production, where that directory is the running binary's own. Tests set it
// through withExeDataRoot because `go test` hands every run of a package the
// SAME cached test binary — two concurrent `go test ./internal/ui` processes
// therefore share one os.Executable() directory and race on the same files.
var exeDataRoot string

// exeDataDir is the base directory for exe-adjacent data: the injected test
// root when one is set, else the running binary's directory.
func exeDataDir() (string, error) {
	if exeDataRoot != "" {
		return exeDataRoot, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}
