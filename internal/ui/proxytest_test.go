package ui

import "testing"

func TestProxyTestTargetPrefersTheServerInUse(t *testing.T) {
	const master = "https://servers.example/list"
	cases := []struct{ name, server, master, want string }{
		{"ws keeps its port", "ws://ao.example:27016", master, "ao.example:27016"},
		{"wss default port", "wss://ao.example", master, "ao.example:443"},
		{"ws default port", "ws://ao.example", master, "ao.example:80"},
		{"no server uses the list", "", master, "servers.example:443"},
		{"unusable server uses the list", "::bad", master, "servers.example:443"},
		{"nothing usable", "", "", ""},
	}
	for _, tc := range cases {
		if got := proxyTestTarget(tc.server, tc.master); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
