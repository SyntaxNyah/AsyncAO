//go:build !windows && !darwin

package netproxy

// Linux and the BSDs have no OS proxy setting, only the environment variables envPolicy reads. GNOME's and
// KDE's settings don't reach a non-GLib, non-KIO application, and would be silently wrong on other desktops.

func init() { discoverSystem = func() *Policy { return nil } }
