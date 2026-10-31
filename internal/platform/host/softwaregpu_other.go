//go:build !windows

package host

import "os"

// lavapipeManifests are Mesa's install locations; macOS has none, so its software tier is SDL's software renderer.
var lavapipeManifests = []string{
	"/usr/share/vulkan/icd.d/lvp_icd.x86_64.json",
	"/usr/share/vulkan/icd.d/lvp_icd.aarch64.json",
	"/usr/local/share/vulkan/icd.d/lvp_icd.x86_64.json",
}

// SoftwareVulkanDriverFile is the system's lavapipe manifest, or "" when not installed.
func SoftwareVulkanDriverFile() string {
	for _, p := range lavapipeManifests {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
