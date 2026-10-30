package main

import (
	"os"
	"path/filepath"
)

// lavapipeManifest is the software Vulkan driver's manifest, staged next to the executable (scripts/build.ps1).
const lavapipeManifest = "lvp_icd.x86_64.json"

// softwareVulkanDriverFile is the staged manifest, or "" when the build has none (a plain `go build`).
func softwareVulkanDriverFile() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(exe), lavapipeManifest)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
