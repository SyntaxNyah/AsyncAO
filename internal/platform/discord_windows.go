package platform

import (
	"fmt"
	"io"
	"os"
)

// DialDiscord uses plain os.OpenFile, which works for duplex byte-mode pipes, so no winio dependency is needed.
func DialDiscord() (io.ReadWriteCloser, error) {
	for i := range discordPipeCandidates {
		f, err := os.OpenFile(fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i), os.O_RDWR, 0)
		if err == nil {
			return f, nil
		}
	}
	return nil, fmt.Errorf("presence: no discord-ipc pipe found")
}
