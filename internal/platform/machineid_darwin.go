//go:build darwin

package platform

import (
	"os/exec"
	"strings"
)

// MachineIdentity returns the machine's stable identity values, strongest first; nil when none is readable. They are
// private: hwid hashes them, and nothing may send or log them.
// macOS: the hardware UUID (IOPlatformUUID) from ioreg.
func MachineIdentity() []string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return nil
	}
	// Matching line: `    "IOPlatformUUID" = "XXXXXXXX-...."`.
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		if i := strings.Index(line, "="); i >= 0 {
			if v := strings.Trim(strings.TrimSpace(line[i+1:]), `"`); v != "" {
				return []string{"platformuuid=" + v}
			}
		}
	}
	return nil
}
