package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
)

// The proxy section of the Power-user tab.
//
// The READOUT is the load-bearing part of this, not the controls. The biggest
// risk in honouring a machine's proxy configuration is honouring one that cannot
// carry AO traffic — a proxy that blocks CONNECT to the non-443 ports community
// AO servers use will make every room fail to load, and from the player's side
// that looks exactly like the client being broken. So the user has to be able to
// see WHAT was detected and WHERE it came from, on screen, without a debug log
// and without guessing; and "Never use a proxy" has to be one click away from
// reading that line.

// proxyModeLabel is the cycle button's text: what is IN EFFECT, not what
// clicking would switch to.
func proxyModeLabel(mode int) string {
	switch mode {
	case config.ProxyModeDirect:
		return "Proxy: never use one"
	case config.ProxyModeManual:
		return "Proxy: the address below"
	default:
		return "Proxy: use this computer's setting"
	}
}

// applyProxyPrefs re-resolves the policy from the saved preferences. Called on
// every change and by the re-check button — never per request.
func (a *App) applyProxyPrefs() {
	p := netproxy.Configure(netproxy.Mode(a.d.Prefs.ProxyMode()), a.d.Prefs.ProxyURLValue())
	a.pushDebug("proxy: " + p.Describe())
}

func (a *App) drawProxySettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX

	y = a.settingsSection(y, w, "Proxy")
	mode := a.d.Prefs.ProxyMode()
	if c.Button(sdl.Rect{X: pad, Y: y, W: 300, H: btnH}, proxyModeLabel(mode)) {
		a.d.Prefs.SetProxyMode((mode + 1) % config.ProxyModeCount)
		a.applyProxyPrefs()
	}
	// Re-check sits beside the mode rather than under it: the case it exists for
	// is a user who just connected to a VPN or changed their network, and having
	// to restart to be believed is the thing that makes a detected-proxy feature
	// feel broken.
	if c.Button(sdl.Rect{X: pad + 308, Y: y, W: 110, H: btnH}, "Re-check") {
		a.applyProxyPrefs()
	}
	c.Tooltip(sdl.Rect{X: pad + 308, Y: y, W: 110, H: btnH},
		"Read this computer's proxy configuration again — after connecting to a VPN, or changing network settings, without restarting.")
	y += btnH + 6

	if mode == config.ProxyModeManual {
		c.Label(pad, y+4, "Proxy address:", ColText)
		cur := a.d.Prefs.ProxyURLValue()
		if next, _ := c.TextField("proxyurl", sdl.Rect{X: pad + 160, Y: y, W: 340, H: fieldH}, cur,
			"http://host:8080  ·  socks5://host:1080"); next != cur {
			a.d.Prefs.SetProxyURL(next)
			a.applyProxyPrefs()
		}
		y += 24
		y = a.settingsDesc(pad, y, "http://, https:// and socks5:// are all understood. A bare host:port is read as http://. An address that can't be read blocks connections rather than quietly going direct — the line below will say so.", ColTextDim)
		y += 6
	}

	// The status line. Colour carries the same information as the words, because
	// the two states that matter — "nothing is proxied" and "connections are being
	// blocked" — need to be distinguishable at a glance.
	p := netproxy.Current()
	statusCol := ColTextDim
	if p.AutoScript {
		statusCol = ColDanger
	} else if p.HTTP != nil || p.HTTPS != nil {
		statusCol = ColAccent
	}
	c.Label(pad, y+4, "In effect:", ColText)
	c.LabelClipped(pad+90, y+4, w-90-8, p.Describe(), statusCol)
	y += 26

	if p.AutoScript {
		y = a.settingsDesc(pad, y, "This computer is set to find its proxy from a script, which AsyncAO can't run here. Rather than connect around a proxy you meant to use, connections are being BLOCKED. Two ways out: switch the setting above to \"the address below\" and enter the proxy yourself, or switch it to \"never use one\" if you don't want AsyncAO proxied at all.", ColDanger)
		y += 6
	}
	y = a.settingsDesc(pad, y, "Where AsyncAO's traffic goes: the server connection, all character and background art, music, the server list and updates. It follows this computer's own setting by default. If a proxy is found that can't carry AO traffic — some block the ports community servers use — nothing will load, and \"never use one\" is the fix.", ColTextDim)
	y += 10
	return y
}
