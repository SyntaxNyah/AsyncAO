package platform

import (
	"encoding/binary"
	"syscall"
)

// hwMemsizeBytes is the width of the hw.memsize value.
const hwMemsizeBytes = 8

func totalMemory() (uint64, bool) {
	value, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0, false
	}
	// Sysctl returns a string and cuts trailing zero bytes off the number.
	raw := make([]byte, hwMemsizeBytes)
	copy(raw, value)
	return binary.LittleEndian.Uint64(raw), true
}
