//go:build !windows && !linux && !darwin

package hwid

// roots has no platform identity source on other GOOSes, so compute() falls back
// to the hostname. AsyncAO ships windows/linux/darwin; this keeps the package
// buildable everywhere.
func roots() []string { return nil }
