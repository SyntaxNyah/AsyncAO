//go:build !windows && !linux && !darwin

package platform

func totalMemory() (uint64, bool) { return 0, false }
