// Package netproxy opens the client's outbound HTTP and WebSocket connections. Each configuration is one read-only
// Network: a changed setting applies to the next connection, and an open connection keeps the route it started on.
package netproxy

import (
	"sync/atomic"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

// Factory holds the Network in force. It is safe from any goroutine.
type Factory struct {
	system  func() platform.ProxySettings
	current atomic.Pointer[Network]
}

// New returns a factory with no proxy until Apply; system reads the OS proxy settings and may block.
func New(system func() platform.ProxySettings) *Factory {
	f := &Factory{system: system}
	f.current.Store(newNetwork("not configured yet", nil, nil, nil))
	return f
}

// Direct returns a factory that never proxies, for tests and tools with no proxy setting.
func Direct() *Factory {
	f := New(func() platform.ProxySettings { return platform.ProxySettings{} })
	f.Apply(ModeDirect, "", "")
	return f
}

// Apply resolves a configuration and puts it in force; address and bypass apply to ModeManual only. It may block on the OS, so never call it on the render thread.
func (f *Factory) Apply(mode Mode, address, bypass string) *Network {
	n := resolveNetwork(mode, address, bypass, f.system)
	f.current.Swap(n).closeIdleConnections()
	return n
}

// Current is the Network in force, never nil. Ask for it per connection, so a changed setting applies.
func (f *Factory) Current() *Network { return f.current.Load() }
