//go:build linux

package platform

import (
	"os"
	"strings"
)

// MachineIdentity returns the machine's stable identity values, strongest first; nil when none is readable. They are
// private: hwid hashes them, and nothing may send or log them.
// Linux: /etc/machine-id, falling back to the D-Bus machine id; per-install and stable across renames.
func MachineIdentity() []string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(b)); id != "" {
				return []string{"machineid=" + id}
			}
		}
	}
	return nil
}
