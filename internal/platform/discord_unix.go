//go:build !windows

package platform

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// DialDiscord opens the local Discord IPC unix socket.
func DialDiscord() (io.ReadWriteCloser, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	for i := range discordPipeCandidates {
		conn, err := net.Dial("unix", filepath.Join(base, fmt.Sprintf("discord-ipc-%d", i)))
		if err == nil {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("presence: no discord-ipc socket found")
}
