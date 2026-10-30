//go:build !windows && !linux && !darwin

package hwid

// roots has no identity source on other GOOSes, so the hostname is used.
func roots() []string { return nil }
