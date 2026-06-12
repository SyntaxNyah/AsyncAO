//go:build !nodiscord && windows

package presence

import (
	"fmt"
	"io"
	"os"
)

// dialDiscord opens the local Discord IPC named pipe. Plain os.OpenFile
// works for duplex byte-mode pipes — no winio dependency needed.
func dialDiscord() (io.ReadWriteCloser, error) {
	for i := 0; i < pipeCandidates; i++ {
		f, err := os.OpenFile(fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i), os.O_RDWR, 0)
		if err == nil {
			return f, nil
		}
	}
	return nil, fmt.Errorf("presence: no discord-ipc pipe found")
}
