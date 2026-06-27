//go:build linux

package hwid

import (
	"os"
	"strings"
)

// roots returns the Linux machine identity: /etc/machine-id (set once at install
// by systemd), falling back to the D-Bus machine id. Per-OS-install and stable
// across renames; best-effort.
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
