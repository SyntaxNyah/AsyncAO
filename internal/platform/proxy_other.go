//go:build !windows && !darwin

package platform

// osProxySettings is empty here: Linux and the BSDs have only the environment variables, and GNOME's and KDE's settings
// don't reach a non-GLib, non-KIO application.
func osProxySettings() ProxySettings { return ProxySettings{} }
