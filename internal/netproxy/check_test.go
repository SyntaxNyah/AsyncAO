package netproxy_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy/netproxytest"
)

// closedAddress returns a local address nothing listens on.
func closedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}

func TestCheckNamesTheStepThatFailed(t *testing.T) {
	_, target := targetServer(t)
	working := netproxytest.Start(t, target).URL
	refusing := netproxytest.Start(t, "").URL
	cases := []struct {
		name    string
		mode    netproxy.Mode
		address string
		addr    string
		want    netproxy.CheckStage
		says    string
	}{
		{"works through a proxy", netproxy.ModeManual, working, unreachable, netproxy.CheckConnected, "through"},
		{"proxy refuses the tunnel", netproxy.ModeManual, refusing, unreachable, netproxy.CheckTunnelFailed, "403"},
		{"nothing at the proxy address", netproxy.ModeManual, "http://" + closedAddress(t), unreachable, netproxy.CheckProxyUnreachable, "reach the proxy"},
		{"unreadable address", netproxy.ModeManual, "ftp://x", unreachable, netproxy.CheckBlocked, "Blocked"},
		{"direct works", netproxy.ModeDirect, "", target, netproxy.CheckConnected, "directly"},
		{"direct fails", netproxy.ModeDirect, "", closedAddress(t), netproxy.CheckDirectFailed, "No proxy"},
		{"bad target", netproxy.ModeDirect, "", "no-port", netproxy.CheckDirectFailed, "host and port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := netproxy.New(noSystem, noEnv)
			f.Apply(tc.mode, tc.address)
			got := f.Check(context.Background(), tc.addr)
			if got.Stage != tc.want {
				t.Errorf("stage = %d, want %d (%s)", got.Stage, tc.want, got.Message)
			}
			if got.OK() != (tc.want == netproxy.CheckConnected) {
				t.Errorf("OK = %v at stage %d", got.OK(), got.Stage)
			}
			if !strings.Contains(got.Message, tc.says) {
				t.Errorf("message %q lacks %q", got.Message, tc.says)
			}
		})
	}
}

func TestCheckMessageHidesTheProxyPassword(t *testing.T) {
	refusing := netproxytest.Start(t, "").URL
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, strings.Replace(refusing, "http://", "http://me:secret@", 1))
	if got := f.Check(context.Background(), unreachable).Message; strings.Contains(got, "secret") {
		t.Errorf("message shows the password: %q", got)
	}
}
