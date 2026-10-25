//go:build !windows && !darwin

package netproxy

// Linux and the BSDs have no operating-system proxy setting to read, and that is
// a fact about the platform rather than a gap in AsyncAO. There is only a
// convention — the http_proxy / https_proxy / no_proxy environment variables —
// which envPolicy already reads on every platform, before this is consulted.
// So the honest behaviour is: the environment, and nothing else. A Linux user
// whose desktop proxy settings are not reaching AsyncAO can export the standard
// variables, or enter the proxy in Settings — and the readout says which of
// those is in effect, so they are never guessing.

func init() { discoverSystem = func() *Policy { return nil } }
