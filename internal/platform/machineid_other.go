//go:build !windows && !linux && !darwin

package platform

// MachineIdentity has no source on other systems.
func MachineIdentity() []string { return nil }
