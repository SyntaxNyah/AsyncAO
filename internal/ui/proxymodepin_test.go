package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
)

// The proxy mode is stored as an int in internal/config and consumed as a
// netproxy.Mode, and the two enums are spelled out separately because
// internal/config sits BELOW internal/netproxy and a preference package that
// imported a network package would invert the layering. main.go converts one to
// the other with a plain cast.
//
// So a divergence is silent and total: adding a mode to one enum and not the
// other would make a user's chosen setting resolve to a different one — most
// dangerously, "never use a proxy" resolving to "use the system's", which is the
// exact escape hatch a user reaches for when the detected proxy cannot carry AO
// traffic. internal/ui imports both, so the pin lives here.
func TestProxyModeEnumsAgree(t *testing.T) {
	for _, tc := range []struct {
		name string
		pref int
		mode netproxy.Mode
	}{
		{"system", config.ProxyModeSystem, netproxy.ModeSystem},
		{"direct", config.ProxyModeDirect, netproxy.ModeDirect},
		{"manual", config.ProxyModeManual, netproxy.ModeManual},
	} {
		if netproxy.Mode(tc.pref) != tc.mode {
			t.Errorf("config.ProxyMode%s = %d, but netproxy has it as %d — a saved setting would resolve to the wrong mode",
				tc.name, tc.pref, tc.mode)
		}
	}
	if config.ProxyModeCount != int(netproxy.ModeCount) {
		t.Errorf("config.ProxyModeCount = %d, netproxy.ModeCount = %d — one enum gained a mode the other does not have",
			config.ProxyModeCount, netproxy.ModeCount)
	}
	// The default has to be System specifically. Defaulting to Direct would ship
	// the client ignoring the machine's proxy, which is the defect the setting
	// exists to fix.
	if config.ProxyModeSystem != 0 {
		t.Error("ProxyModeSystem must be the zero value — it is the shipped default")
	}
}
