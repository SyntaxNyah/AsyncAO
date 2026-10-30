//go:build darwin

package hwid

import (
	"os/exec"
	"strings"
)

// roots returns the hardware UUID (IOPlatformUUID) from ioreg; on failure the hostname is used.
func roots() []string {
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
