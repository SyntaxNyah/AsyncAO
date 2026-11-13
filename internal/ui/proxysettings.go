package ui

import (
	"context"
	"log"
	"net"
	"net/url"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The proxy section of the Power-user tab. A proxy blocking CONNECT to the non-443 ports AO servers use looks like a
// broken client, so what is in effect and where it came from must be on screen.

// applyProxyPrefs re-resolves the configuration from saved preferences, off the render thread because it may read the registry
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
			p := sockets.Apply(netproxy.Mode(prefs.Load().ProxyModeVal), prefs.Load().ProxyURL, prefs.Load().ProxyBypass)
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

// applyProxyFields puts typed manual settings in force once editing has ended; applying per keystroke would send new
// requests to a half-typed address.
func (a *App) applyProxyFields() {
	s := a.d.Prefs.Load()
	if s.ProxyURL != a.proxyURLApplied || s.ProxyBypass != a.proxyBypassApplied {
		a.proxyURLApplied, a.proxyBypassApplied = s.ProxyURL, s.ProxyBypass
		a.applyProxyPrefs()
	}
}

// proxyTestTimeout bounds the test button, longer than a working connection takes to set up.
const proxyTestTimeout = 20 * time.Second

// proxyTestTarget picks what to test against: the server in use, else the server list's host.
func proxyTestTarget(serverURL, masterURL string) string {
	for _, raw := range []string{serverURL, masterURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			continue
		}
		port := u.Port()
		switch {
		case port != "":
		case u.Scheme == "wss" || u.Scheme == "https":
			port = "443"
		default:
			port = "80"
		}
		return net.JoinHostPort(u.Hostname(), port)
	}
	return ""
}

// startProxyTest runs one connection test off the render thread; the readout polls proxyTestResult.
func (a *App) startProxyTest() {
	master := ""
	if urls := a.masterListURLs(); len(urls) > 0 {
		master = urls[0]
	}
	target := proxyTestTarget(a.lastConnURL, master)
	if target == "" || a.proxyTestBusy.Swap(true) {
		return
	}
	a.proxyTestResult.Store(nil)
	sockets := a.sockets()
	go func() {
		defer PushWake()
		defer a.proxyTestBusy.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), proxyTestTimeout)
		defer cancel()
		result := sockets.Current().Check(ctx, target)
		log.Printf("proxy test: %s (%s)", result.Message, result.Elapsed)
		a.proxyTestResult.Store(&result)
	}()
}

// drawProxyTest is the button and its result, naming the step that failed.
func (a *App) drawProxyTest(pad, y, w int32) int32 {
	c := a.ctx
	const testBtnW = 150
	busy := a.proxyTestBusy.Load()
	label := "Test connection"
	if busy {
		label = "Testing…"
	}
	if c.Button(sdl.Rect{X: pad, Y: y, W: testBtnW, H: btnH}, label) && !busy {
		a.startProxyTest()
	}
	c.Tooltip(sdl.Rect{X: pad, Y: y, W: testBtnW, H: btnH},
		"Opens one connection using the setting above, to the server you last joined or else the server list, and says which step failed.")
	a.noteSearchRow("Test connection", y)
	if result := a.proxyTestResult.Load(); result != nil && !busy {
		col := ColDanger
		if result.OK() {
			col = ColTierGreen
		}
		c.LabelClipped(pad+testBtnW+10, y+4, w-pad-testBtnW-18, result.Message+" ("+result.Elapsed.String()+")", col)
	}
	return y + btnH + 8
}

func (a *App) drawProxySettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX

	y = a.settingsSection(y, w, "Proxy")
	mode := a.d.Prefs.Load().ProxyModeVal
	for _, choice := range []struct {
		mode  int
		label string
	}{
		{config.ProxyModeSystem, "Use this computer's proxy settings"},
		{config.ProxyModeDirect, "Don't use a proxy"},
		{config.ProxyModeManual, "Configure the proxy manually"},
	} {
		if c.Checkbox(pad, y, choice.label, mode == choice.mode) && mode != choice.mode {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.ProxyModeVal = choice.mode })
			a.applyProxyPrefs()
		}
		a.noteSearchRow(choice.label, y)
		y += 24
	}
	y += 6

	if mode == config.ProxyModeManual {
		y = a.drawManualProxy(pad, y)
	}

	// Colour repeats the words so "nothing is proxied" and "connections are being blocked" differ at a glance.
	p := a.sockets().Current()
	statusCol := ColTextDim
	if p.Blocked() {
		statusCol = ColDanger
	} else if p.UsesProxy() {
		statusCol = ColAccent
	}
	c.Label(pad, y+4, "In effect:", ColText)
	c.LabelClipped(pad+90, y+4, w-90-8, p.Describe(), statusCol)
	y += 26
	y = a.drawProxyTest(pad, y, w)

	if mode == config.ProxyModeSystem {
		// Re-check is for a user who just joined a VPN or changed network: they should not have to restart.
		if c.Button(sdl.Rect{X: pad, Y: y, W: 110, H: btnH}, "Re-check") {
			a.applyProxyPrefs()
		}
		c.Tooltip(sdl.Rect{X: pad, Y: y, W: 110, H: btnH},
			"Read this computer's proxy configuration again — after connecting to a VPN, or changing network settings, without restarting.")
		y += btnH + 6
	}
	y = a.settingsDesc(pad, y, "Everything AsyncAO connects to follows this: the server connection, all character and background art, music, the server list, and updates. A change applies to new connections, so reconnect to move a server you're already on. If a proxy can't carry AO traffic — some block the ports community servers use — nothing will load, and \"Don't use a proxy\" is the fix.", ColTextDim)
	return y + 10
}

// drawManualProxy is the address and bypass fields, which exist only in manual mode.
func (a *App) drawManualProxy(pad, y int32) int32 {
	c := a.ctx
	c.Label(pad, y+4, "Proxy address:", ColText)
	cur := a.d.Prefs.Load().ProxyURL
	next, addressEntered := c.TextField("proxyurl", sdl.Rect{X: pad + 160, Y: y, W: 340, H: fieldH}, cur, "http://host:8080")
	if next != cur {
		a.d.Prefs.SetProxyURL(next)
	}
	y += 24
	c.Label(pad, y+4, "Skip the proxy for:", ColText)
	curBypass := a.d.Prefs.Load().ProxyBypass
	nextBypass, bypassEntered := c.TextField("proxybypass", sdl.Rect{X: pad + 160, Y: y, W: 340, H: fieldH}, curBypass, "lan.example;192.168.*")
	if nextBypass != curBypass {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ProxyBypass = nextBypass })
	}
	if addressEntered || bypassEntered || (c.focusID != "proxyurl" && c.focusID != "proxybypass") {
		a.applyProxyFields()
	}
	y += 24
	y = a.settingsDesc(pad, y, "This computer's proxy settings are ignored. http:// and https:// are understood, and a bare host:port is read as http://. A missing or unreadable address blocks connections, and the line below will say so. Skipped hosts are split by ; or a comma: names cover their subdomains, and 192.168.*, 10.0.0.0/8 and <local> also work.", ColTextDim)
	return y + 6
}
