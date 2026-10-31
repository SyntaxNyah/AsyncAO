package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The proxy section of the Power-user tab. A proxy blocking CONNECT to the non-443 ports AO servers use looks like a
// broken client, so what was detected and where it came from must be on screen, one click from "Never use a proxy".

// proxyModeLabel is the cycle button's text: what is in effect, not what clicking would switch to.
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

// applyProxyPrefs re-resolves the policy from saved preferences; called on change and re-check, never per request.
func (a *App) applyProxyPrefs() {
	p := netproxy.Configure(netproxy.Mode(a.d.Prefs.Load().ProxyModeVal), a.d.Prefs.Load().ProxyURL)
	a.pushDebug("proxy: " + p.Describe())
}

func (a *App) drawProxySettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX

	y = a.settingsSection(y, w, "Proxy")
	mode := a.d.Prefs.Load().ProxyModeVal
	if c.Button(sdl.Rect{X: pad, Y: y, W: 300, H: btnH}, proxyModeLabel(mode)) {
		{
			pv1 := (mode + 1) % config.ProxyModeCount
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.ProxyModeVal = pv1
			})
		}
		a.applyProxyPrefs()
	}
	// Re-check is for a user who just joined a VPN or changed network: they should not have to restart.
	if c.Button(sdl.Rect{X: pad + 308, Y: y, W: 110, H: btnH}, "Re-check") {
		a.applyProxyPrefs()
	}
	c.Tooltip(sdl.Rect{X: pad + 308, Y: y, W: 110, H: btnH},
		"Read this computer's proxy configuration again — after connecting to a VPN, or changing network settings, without restarting.")
	y += btnH + 6

	if mode == config.ProxyModeManual {
		c.Label(pad, y+4, "Proxy address:", ColText)
		cur := a.d.Prefs.Load().ProxyURL
		if next, _ := c.TextField("proxyurl", sdl.Rect{X: pad + 160, Y: y, W: 340, H: fieldH}, cur,
			"http://host:8080  ·  socks5://host:1080"); next != cur {
			a.d.Prefs.SetProxyURL(next)
			a.applyProxyPrefs()
		}
		y += 24
		y = a.settingsDesc(pad, y, "http://, https:// and socks5:// are all understood. A bare host:port is read as http://. An address that can't be read blocks connections rather than quietly going direct — the line below will say so.", ColTextDim)
		y += 6
	}

	// Colour repeats the words so "nothing is proxied" and "connections are being blocked" differ at a glance.
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
