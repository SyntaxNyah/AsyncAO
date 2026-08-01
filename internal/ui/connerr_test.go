package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
)

// TestFriendlyConnError pins #9: raw dial errors map to plain guidance, a wss:// TLS
// failure suggests ws://, and an unrecognised error falls back to the raw text.
func TestFriendlyConnError(t *testing.T) {
	cases := []struct {
		name, url, errText, wantContains string
	}{
		{"dns", "wss://x", "dial tcp: lookup x: no such host", "Couldn't find"},
		{"refused", "ws://x", "dial tcp 1.2.3.4:80: connect: connection refused", "refused"},
		{"timeout", "ws://x", "context deadline exceeded", "Timed out"},
		{"cert", "wss://x", "x509: certificate has expired", "certificate"},
		{"tls-on-wss", "wss://x", "tls: handshake failure", "try ws://"},
		{"not-websocket", "ws://x", "websocket: bad handshake", "WebSocket"},
		{"unknown", "ws://x", "something totally unexplained here", "Couldn't connect: something totally unexplained here"},
	}
	for _, tc := range cases {
		got := friendlyConnError(tc.url, errors.New(tc.errText))
		if !strings.Contains(got, tc.wantContains) {
			t.Errorf("%s: friendlyConnError(%q) = %q, want it to contain %q", tc.name, tc.errText, got, tc.wantContains)
		}
		if strings.HasPrefix(got, "dial tcp") || got == tc.errText {
			t.Errorf("%s: returned the raw error verbatim: %q", tc.name, got)
		}
	}
}

// TestFriendlyConnErrorNamesTheProxy pins the ORDER of the switch, which is the
// whole reason the proxy cases sit at the top of it.
//
// The websocket library wraps every dial failure with its own name, so a proxy
// error arrives carrying the word "websocket" and lands in the "not a WebSocket
// server" arm — telling the user to blame a server that is working perfectly,
// and sending them to look at the one thing that is not wrong. That is worse
// than an unhelpful message: it is a confident wrong answer.
func TestFriendlyConnErrorNamesTheProxy(t *testing.T) {
	// The shape the error really arrives in: our dial wrapper, then the
	// library's, then the transport's, then ours at the bottom.
	wrapped := fmt.Errorf("protocol: dialing wss://x: failed to WebSocket dial: Get %q: %w",
		"https://x", netproxy.ErrProxyUnresolvable)
	got := friendlyConnError("wss://x", wrapped)
	if !strings.Contains(got, "proxy") {
		t.Errorf("an unresolvable-proxy failure reported %q — it must name the proxy", got)
	}
	if strings.Contains(got, "WebSocket connection") {
		t.Errorf("a proxy failure was blamed on the server: %q", got)
	}
	if !strings.Contains(got, "Power user") {
		t.Errorf("the message must say where the fix is, got %q", got)
	}

	// A proxy that is configured and simply unreachable — Go's own wording.
	got = friendlyConnError("wss://x", errors.New(`failed to WebSocket dial: Get "https://x": proxyconnect tcp: dial tcp 10.0.0.1:8080: connect: connection refused`))
	if !strings.Contains(got, "proxy") {
		t.Errorf("a proxyconnect failure reported %q — it must name the proxy", got)
	}
	// ...and specifically must NOT be read as the server refusing us, which is
	// the arm it would otherwise fall into on the word "refused".
	if strings.Contains(got, "server may be offline") {
		t.Errorf("an unreachable PROXY was reported as an offline server: %q", got)
	}

	// The ordinary cases must still be untouched by the new arms — "proxy" is a
	// broad substring and the risk runs both ways.
	if got := friendlyConnError("ws://x", errors.New("websocket: bad handshake")); !strings.Contains(got, "WebSocket") {
		t.Errorf("a genuine handshake failure now reports %q", got)
	}
}
