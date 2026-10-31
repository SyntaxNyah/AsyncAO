//go:build !windows

package host

// CopyFileToClipboard does nothing here; the CopyFilesToClipboard capability is off.
func (Desktop) CopyFileToClipboard(string) bool { return false }
