package netproxy

import (
	"context"
	"fmt"
	"net"
	"time"
)

// CheckStage is how far a connection test got.
type CheckStage int

// Stages, in the order a connection passes them.
const (
	// CheckBlocked means the setting itself refuses connections.
	CheckBlocked CheckStage = iota
	// CheckProxyUnreachable means nothing answered at the proxy's address.
	CheckProxyUnreachable
	// CheckTunnelFailed means the proxy answered but couldn't or wouldn't reach the destination, or rejected the login.
	CheckTunnelFailed
	// CheckDirectFailed means the route is direct and the destination didn't answer.
	CheckDirectFailed
	// CheckConnected means a connection to the destination opened.
	CheckConnected
)

// CheckResult is one connection test, for the Settings readout.
type CheckResult struct {
	Stage CheckStage
	// Message is one line for the user, naming the step that failed.
	Message string
	Elapsed time.Duration
}

// OK reports whether the connection opened.
func (r CheckResult) OK() bool { return r.Stage == CheckConnected }

// Check opens one connection to addr by the current policy and reports which step failed. It blocks on the network, so
// never call it on the render thread.
func (f *Factory) Check(ctx context.Context, addr string) CheckResult {
	start := time.Now()
	finish := func(stage CheckStage, format string, args ...any) CheckResult {
		return CheckResult{Stage: stage, Message: fmt.Sprintf(format, args...), Elapsed: time.Since(start).Round(time.Millisecond)}
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return finish(CheckDirectFailed, "%s isn't a host and port: %v", addr, err)
	}
	via, err := f.policy.Load().route(host)
	if err != nil {
		return finish(CheckBlocked, "Blocked by the setting: %v", err)
	}
	if via == nil {
		conn, err := dialDirect(ctx, "tcp", addr)
		if err != nil {
			return finish(CheckDirectFailed, "No proxy in use, and %s didn't answer: %v", addr, err)
		}
		_ = conn.Close()
		return finish(CheckConnected, "Connected to %s directly", addr)
	}
	reach, err := dialDirect(ctx, "tcp", proxyAddress(via))
	if err != nil {
		return finish(CheckProxyUnreachable, "Couldn't reach the proxy at %s: %v", via.Host, err)
	}
	_ = reach.Close()
	conn, err := dialTunnel(ctx, via, addr)
	if err != nil {
		return finish(CheckTunnelFailed, "Reached the proxy, but it couldn't connect to %s: %v", addr, err)
	}
	_ = conn.Close()
	return finish(CheckConnected, "Connected to %s through %s", addr, via.Host)
}
