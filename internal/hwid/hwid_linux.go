//go:build linux

package hwid

import (
	"os"
	"strings"
)

// roots returns /etc/machine-id, falling back to the D-Bus machine id; per-install and stable across renames.
func roots() []string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(b)); id != "" {
				return []string{"machineid=" + id}
			}
		}
	}
	return nil
}
