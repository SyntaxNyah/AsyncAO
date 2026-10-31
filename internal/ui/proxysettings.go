package ui

import (
	"errors"
	"log"

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

// applyProxyPrefs re-resolves the policy from saved preferences, off the render thread because it may read the registry
// or start a process. One goroutine at a time (hard rule 4): a change made meanwhile runs right after it.
func (a *App) applyProxyPrefs() {
	if a.proxyApplyBusy.Swap(true) {
		a.proxyApplyAgain.Store(true)
		return
	}
	sockets, prefs := a.sockets(), a.d.Prefs
	go func() {
		defer PushWake()
		for {
			a.proxyApplyAgain.Store(false)
			p := sockets.Apply(netproxy.Mode(prefs.Load().ProxyModeVal), prefs.Load().ProxyURL)
			log.Printf("proxy: %s", p.Describe())
			if !a.proxyApplyAgain.Load() {
				break
			}
		}
		a.proxyApplyBusy.Store(false)
		// A change between the last check and clearing busy would otherwise wait for the next one.
		if a.proxyApplyAgain.Swap(false) {
			a.applyProxyPrefs()
		}
	}()
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
	p := a.sockets().Current()
	statusCol := ColTextDim
	if p.Blocked() {
		statusCol = ColDanger
	} else if p.Via != nil {
		statusCol = ColAccent
	}
	c.Label(pad, y+4, "In effect:", ColText)
	c.LabelClipped(pad+90, y+4, w-90-8, p.Describe(), statusCol)
	y += 26

	if errors.Is(p.Block, netproxy.ErrProxyUnresolvable) {
		y = a.settingsDesc(pad, y, "This computer is set to find its proxy from a script, which AsyncAO can't run here. Rather than connect around a proxy you meant to use, connections are being BLOCKED. Two ways out: switch the setting above to \"the address below\" and enter the proxy yourself, or switch it to \"never use one\" if you don't want AsyncAO proxied at all.", ColDanger)
		y += 6
	}
	y = a.settingsDesc(pad, y, "Everything AsyncAO connects to follows this: the server connection, all character and background art, music, the server list and its pings, and updates. A change applies to new connections, so reconnect to move a server you're already on. If a proxy can't carry AO traffic — some block the ports community servers use — nothing will load, and \"never use one\" is the fix.", ColTextDim)
	y += 10
	return y
}
