//go:build !windows

package host

// Notify does nothing here; the SystemNotifications capability is off and the in-app toast is the fallback.
func (Desktop) Notify(_, _ string) {}
