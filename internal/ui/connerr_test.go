package ui

import (
	"errors"
	"strings"
	"testing"
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
