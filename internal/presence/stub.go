//go:build nodiscord

// The -tags nodiscord build: the entire presence implementation compiles
// out and this no-op stub keeps the call sites identical. Discord is
// never a build requirement either way — the real implementation is
// stdlib-only local IPC (see BUILDING.md).
package presence

import "time"

// Activity mirrors the real type so call sites compile unchanged.
type Activity struct {
	Details string
	State   string
	Start   time.Time
}

// Compiled is false in the -tags nodiscord build, so the Settings UI hides the Discord
// section entirely (nothing to configure when the integration is compiled out).
const Compiled = false

// Client is the inert stand-in.
type Client struct{}

func New(string) *Client       { return &Client{} }
func (*Client) Set(Activity)   {}
func (*Client) Clear()         {}
func (*Client) Close()         {}
func (*Client) Status() string { return "compiled out (-tags nodiscord)" }
