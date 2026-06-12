//go:build !nodiscord && !windows

package presence

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// dialDiscord opens the local Discord IPC unix socket.
func dialDiscord() (io.ReadWriteCloser, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	for i := 0; i < pipeCandidates; i++ {
		conn, err := net.Dial("unix", filepath.Join(base, fmt.Sprintf("discord-ipc-%d", i)))
		if err == nil {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("presence: no discord-ipc socket found")
}
