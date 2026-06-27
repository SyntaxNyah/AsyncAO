//go:build darwin

package hwid

import (
	"os/exec"
	"strings"
)

// roots returns the macOS hardware UUID (IOPlatformUUID), read via ioreg — the
// stable per-machine identifier. Best-effort; a failure drops this root and
// compute() falls back to the hostname.
func roots() []string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return nil
	}
	// A matching line looks like: `    "IOPlatformUUID" = "XXXXXXXX-...."`.
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
