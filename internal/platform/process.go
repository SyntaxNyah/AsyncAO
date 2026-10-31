package platform

import "os/exec"

// Process is a child process, so callers need not import os/exec.
type Process = exec.Cmd

// FindTool returns the path of a program on PATH, "" (a supported state, not an error) when it isn't installed.
func FindTool(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}
