//go:build !windows && !darwin

package netproxy

// Linux and the BSDs have no operating-system proxy setting to read, and that is
// a fact about the platform rather than a gap in AsyncAO. There is only a
// convention — the http_proxy / https_proxy / no_proxy environment variables —
// which envPolicy already reads on every platform, before this is consulted.
//
// GNOME's org.gnome.system.proxy and KDE's kioslaverc exist, but neither
// propagates to a non-GLib / non-KIO application: a browser reads them because
// it opted in, and every other program on the system does not. Shelling out to
// `gsettings` would need the schemas installed and a live session bus, would
// return quoted GVariant text to unpick, and would still be silently wrong on
// every non-GNOME desktop. Reading dconf's binary database directly is a gvdb
// parse for the same unreliable answer.
//
// So the honest behaviour is: the environment, and nothing else. A Linux user
// whose desktop proxy settings are not reaching AsyncAO can export the standard
// variables, or enter the proxy in Settings — and the readout says which of
// those is in effect, so they are never guessing.

func init() { discoverSystem = func() *Policy { return nil } }
