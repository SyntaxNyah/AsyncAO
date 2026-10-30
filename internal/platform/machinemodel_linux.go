package platform

import (
	"os"
	"strings"
)

func machineModel() string {
	var parts []string
	for _, path := range []string{"/sys/class/dmi/id/sys_vendor", "/sys/class/dmi/id/product_name"} {
		// Unreadable on Android and some containers; the model is then just unknown.
		if value, err := os.ReadFile(path); err == nil {
			parts = append(parts, strings.TrimSpace(string(value)))
		}
	}
	return strings.Join(parts, " ")
}
