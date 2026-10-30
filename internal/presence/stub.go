//go:build nodiscord

// The nodiscord build: a no-op stub keeps the call sites identical.
package presence

import "time"

// Activity mirrors the real type so call sites compile.
type Activity struct {
	Details string
	State   string
	Start   time.Time
}

// Client is the inert stand-in.
type Client struct{}

func New(string) *Client       { return &Client{} }
func (*Client) Set(Activity)   {}
func (*Client) Clear()         {}
func (*Client) Close()         {}
func (*Client) Status() string { return "compiled out (-tags nodiscord)" }
