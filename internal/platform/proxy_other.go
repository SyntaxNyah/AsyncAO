//go:build !windows && !darwin

package platform

// SystemProxySettings is empty here: Linux and the BSDs have only the environment variables netproxy reads, and GNOME's
// and KDE's settings don't reach a non-GLib, non-KIO application.
func SystemProxySettings() ProxySettings { return ProxySettings{} }
