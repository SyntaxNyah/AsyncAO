//go:build !windows

package main

import "os"

// lavapipeManifests are where distributions install Mesa's lavapipe manifest.
// macOS has none, so the software tier there is SDL's software renderer.
var lavapipeManifests = []string{
	"/usr/share/vulkan/icd.d/lvp_icd.x86_64.json",
	"/usr/share/vulkan/icd.d/lvp_icd.aarch64.json",
	"/usr/local/share/vulkan/icd.d/lvp_icd.x86_64.json",
}

// softwareVulkanDriverFile is the system's lavapipe manifest, or "" when Mesa's
// lavapipe isn't installed.
func softwareVulkanDriverFile() string {
	for _, p := range lavapipeManifests {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
