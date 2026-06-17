package ui

import (
	"log"
	"os/exec"
	"runtime"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// The built-in About page: who made AsyncAO, why, the original developers this
// client owes everything to, and a credits rundown of the AO2 server softwares
// it speaks to (with their WebSocket / secure-WebSocket / live-player-list
// support). Scrolls with the mousewheel; the link buttons live at the bottom.
const (
	aboutRepoURL   = "https://github.com/SyntaxNyah/AsyncAO"
	aboutAO2URL    = "https://github.com/AttorneyOnline/AO2-Client"
	aboutWebAOURL  = "https://github.com/AttorneyOnline/webAO"
	aboutAOSDLURL  = "https://github.com/AttorneyOnline/AO-SDL"
	aboutAOSiteURL = "https://aceattorneyonline.com"
)

// aboutIntroLines: who/why + the original-AO credits, drawn above the server
// rundown.
var aboutIntroLines = []string{
	"AsyncAO " + protocol.Version,
	"",
	"Made by SyntaxNyah.",
	"",
	"Why this exists: I was tired of people having to download 20 gigabytes of",
	"files just to play, of client lookups taking ages, and — let's be honest —",
	"the AO2 client being a bit slow. AsyncAO streams exactly the assets it",
	"needs, learns what formats your server ships, caches everything, and",
	"renders on a zero-allocation hot path. Every millisecond counts.",
	"",
	"All credit to the original Attorney Online developers:",
	"  • FanatSors — creator of the original Attorney Online",
	"  • OmniTroid — original AO2-Client developer, and a huge help on the",
	"    protocol documentation",
	"  • The AttorneyOnline organization and every AO2-Client contributor —",
	"    AsyncAO mirrors their protocol and courtroom semantics",
	"  • The webAO developers — the asset-URL conventions come from their work",
	"  • The AO-SDL developers — the SDL2 rendering model reference",
	"Thank you for two decades of courtroom drama.",
}

// aboutOutroLines: the thank-yous, drawn below the server rundown.
var aboutOutroLines = []string{
	"Closed-source beta testers — thank you for the bug reports, feature",
	"requests and feedback that shaped AsyncAO during development:",
	"  Cocoa Bean · Lala · Peen · Emerald · Extra7 · Poki · Xocfti · Dag · CherriPop",
	"",
	"A special thank-you to Northgate — who backed this project, including",
	"financially, and gave me the inspiration to keep going. Without that",
	"support AsyncAO wouldn't have come this far this fast. Thank you.",
	"",
	"Pull requests, bug fixes and feature requests are welcome!",
}

// aboutServer is one AO2 server software credit + its capabilities. fork=true
// indents it under the base it descends from; warn=true flags a deprecated base.
// desc is pre-wrapped (no per-frame word-wrap on the scroll path). url=="" draws
// no link button.
type aboutServer struct {
	name  string
	lang  string
	ws    bool // WebSocket (every server here has it — AsyncAO is WS-only)
	wss   bool // NATIVE secure WebSocket (server terminates TLS itself)
	plist bool // live player list (PR/PU player-state packets)
	fork  bool
	warn  bool
	url   string
	desc  []string
}

// aboutServers is grouped base → forks. Capabilities are verified from the
// server sources where available; the WSS column is NATIVE support (a ✕ server
// is still reachable over wss:// behind a reverse proxy). First-draft credits —
// corrections welcome.
var aboutServers = []aboutServer{
	{
		name: "tsuserver3", lang: "Python · base", ws: true, wss: false, plist: false, warn: true,
		url: "https://github.com/AttorneyOnline/tsuserver3",
		desc: []string{
			"The original Python server for Attorney Online 2, and the lineage every classic AO",
			"server descends from. Modern builds accept WebSocket clients, which is what lets",
			"browser and streaming clients like AsyncAO connect at all. It has no live player-list",
			"packets, so a roster only appears when you ask with /getarea. DEPRECATED — no longer",
			"maintained by the official devs; it still runs, but use Akashi for modern support.",
		},
	},
	{
		name: "KFO-Server", lang: "Python · tsuserver3 fork", ws: true, wss: false, plist: false, fork: true,
		url: "https://github.com/Crystalwarrior/KFO-Server",
		desc: []string{
			"A community fork of tsuserver3 maintained for the Case Café / KFO scene. It keeps the",
			"classic tsuserver feature set and piles on a large set of moderation and roleplay",
			"commands. Like its parent it serves WebSocket clients but sends no player-state stream,",
			"so AsyncAO falls back to /getarea snapshots; secure WebSocket is a reverse-proxy job.",
		},
	},
	{
		name: "tsuserverCC", lang: "Python · tsuserver3 fork", ws: true, wss: false, plist: false, fork: true,
		desc: []string{
			"Another tsuserver3 descendant, originally built for the Case Café community, with its",
			"own additions and tweaks on the classic protocol and command style. It is WebSocket-",
			"capable but, like the rest of the tsuserver family, emits no PR/PU player-state packets.",
			"Secure connections are handled by a front-end proxy rather than the server itself.",
		},
	},
	{
		name: "Akashi", lang: "C++ · base", ws: true, wss: true, plist: true,
		url: "https://github.com/AttorneyOnline/akashi",
		desc: []string{
			"The modern C++ server for Attorney Online 2, built on Qt and the official successor to",
			"the tsuserver lineage. It terminates TLS itself, so it serves secure wss:// directly,",
			"no reverse proxy required. Crucially it streams a live player list via PR/PU player-",
			"state packets — which is what powers AsyncAO's real-time roster, shownames and pairing.",
		},
	},
	{
		name: "witches-akashi-party", lang: "C++ · Akashi fork (tea)", ws: true, wss: true, plist: true, fork: true,
		url: "https://github.com/Elchi-2023/witches-akashi-party/tree/tea",
		desc: []string{
			"A feature-rich fork of Akashi by Ganty1999, Elchi, IDk-2023 and SyntaxNyah (the active",
			"work lives on the 'tea' branch). It keeps everything Akashi does — native WSS and the",
			"live PR/PU player list — and adds a VIP system, /radio and /getmusic, a /play DJ role,",
			"ID-based /kick and /ban, OOC text effects, webAO-user markers and reworked pair syncing.",
		},
	},
	{
		name: "Athena", lang: "Go · base", ws: true, wss: false, plist: false,
		desc: []string{
			"A Go implementation of an Attorney Online 2 server, aiming for a lightweight, fast",
			"alternative to the C++ and Python servers. It handles WebSocket clients but, in its base",
			"form, relies on a reverse proxy for TLS rather than serving WSS itself. The stock build",
			"emits no live player-state packets, so a roster there is /getarea-driven — see Nyathena.",
		},
	},
	{
		name: "Nyathena", lang: "Go · Athena fork", ws: true, wss: true, plist: true, fork: true,
		url: "https://github.com/SyntaxNyah/Nyathena",
		desc: []string{
			"A fork of Athena that closes the gap with the modern Akashi feature set. It adds native",
			"WSS — point it at a certificate and it terminates TLS directly, no proxy needed. It also",
			"streams the live PR/PU player list, so AsyncAO's real-time roster, spectator counts and",
			"pairing all work against it. A solid modern Go option, and the server AsyncAO is most",
			"actively tested against.",
		},
	},
	{
		name: "Ferris-AO", lang: "Rust · base", ws: true, wss: true, plist: true,
		url: "https://github.com/SyntaxNyah/Ferris-AO",
		desc: []string{
			"A privacy-first Attorney Online 2 server written in Rust, built async-first on Tokio. It",
			"implements the full AO2 protocol over both WebSocket (with native WSS) and legacy TCP. Its",
			"headline is privacy: raw IP addresses and hardware IDs are never stored. It also streams",
			"the live PR/PU player list, so AsyncAO's real-time roster works against it.",
		},
	},
	{
		name: "Whisker", lang: "C3 · base", ws: true, wss: true, plist: false,
		url: "https://github.com/SyntaxNyah/Whisker",
		desc: []string{
			"A super-lightweight Attorney Online 2 server written in C3 — a leaner alternative to Akashi",
			"with far less bloat, benchmarked using even less memory than it. Its headline feature is a",
			"plugin system: the base itself stays tiny and you bolt features on as plugins rather than",
			"carrying them all by default. It serves WebSocket clients with native WSS.",
		},
	},
}

type aboutLink struct {
	label string
	url   string
}

var aboutLinks = []aboutLink{
	{"AsyncAO repository (PRs welcome!)", aboutRepoURL},
	{"AO2-Client — the original client", aboutAO2URL},
	{"webAO — AO in the browser", aboutWebAOURL},
	{"AO-SDL — SDL2 AO client", aboutAOSDLURL},
	{"aceattorneyonline.com", aboutAOSiteURL},
}

// aboutTickX / aboutCapGap position the WS · WSS · Players capability ticks.
const (
	aboutTickX  = 360 // left edge of the capability columns (from pad)
	aboutCapGap = 76  // spacing between the three capability cells
)

func (a *App) drawAbout(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	c.Heading(pad, pad, "About AsyncAO", ColText)
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.screen = a.prevScreen
		return
	}

	lineH := int32(c.font.Height()) + 4
	content := sdl.Rect{X: 0, Y: pad + 44, W: w, H: h - (pad + 44)}
	if c.hovering(content) {
		a.aboutScroll -= c.WheelIn(content) * scrollStepPx
	}
	total := a.aboutContentHeight(lineH)
	track := sdl.Rect{X: w - scrollBarW - 2, Y: content.Y, W: scrollBarW, H: content.H}
	a.aboutScroll = c.VScrollbar("aboutscroll", track, a.aboutScroll, total, content.H)

	clipPrev, clipHad := c.pushClip(content)
	defer c.popClip(clipPrev, clipHad)
	y := content.Y - a.aboutScroll

	for _, line := range aboutIntroLines {
		c.Label(pad, y, line, ColText)
		y += lineH
	}
	y += lineH

	c.Label(pad, y, "Server software AsyncAO speaks to (WS-only — legacy TCP servers aren't supported):", ColAccent)
	y += lineH
	c.Label(pad, y, "WS = WebSocket · WSS = native secure WebSocket (✕ = via a reverse proxy) · Players = live player list", ColTextDim)
	y += lineH + 4
	for i := range aboutServers {
		y = a.drawAboutServer(&aboutServers[i], pad, y, lineH)
		y += 6
	}
	y += lineH - 6

	for _, line := range aboutOutroLines {
		col := ColText
		if line == "Pull requests, bug fixes and feature requests are welcome!" {
			col = ColAccent
		}
		c.Label(pad, y, line, col)
		y += lineH
	}

	y += 10
	for _, link := range aboutLinks {
		bw := c.TextWidth(link.label) + 24
		if c.Button(sdl.Rect{X: pad, Y: y, W: bw, H: btnH}, link.label) {
			openBrowser(link.url)
		}
		y += btnH + 6
	}
}

// drawAboutServer renders one server row (name + capability ticks + warning) and
// its wrapped description, returning the new y cursor.
func (a *App) drawAboutServer(sv *aboutServer, x, y, lineH int32) int32 {
	c := a.ctx
	nameX := x
	if sv.fork {
		nameX += 22 // indent under its base (the "· … fork" label names the parent)
	}
	// Clickable name (opens the repo) when a URL is known; otherwise plain.
	nameCol := ColText
	nameW := c.TextWidth(sv.name)
	nameHit := sdl.Rect{X: nameX, Y: y, W: nameW + 8, H: lineH}
	if sv.url != "" {
		nameCol = ColAccent
		if c.hovering(nameHit) {
			if c.clicked {
				openBrowser(sv.url)
			}
		}
	}
	c.Label(nameX, y, sv.name, nameCol)
	c.Label(nameX+nameW+12, y, sv.lang, ColTextDim)
	if sv.warn {
		c.Label(nameX+nameW+12+c.TextWidth(sv.lang)+10, y, "⚠ deprecated", ColTierYellow)
	}

	// Capability ticks: WS (yellow), WSS (green/red), Players (green/red).
	capX := x + aboutTickX
	drawTick := func(col int32, label string, ok bool, okCol sdl.Color) int32 {
		c.Label(col, y, label, ColTextDim)
		gx := col + c.TextWidth(label) + 4
		if ok {
			c.Label(gx, y, "✓", okCol)
		} else {
			c.Label(gx, y, "✕", ColDanger)
		}
		return col + aboutCapGap
	}
	cx := capX
	cx = drawTick(cx, "WS", sv.ws, ColTierYellow)
	cx = drawTick(cx, "WSS", sv.wss, ColTierGreen)
	drawTick(cx, "Players", sv.plist, ColTierGreen)
	y += lineH

	for _, line := range sv.desc {
		c.Label(nameX+8, y, line, ColTextDim)
		y += lineH
	}
	return y
}

// aboutContentHeight sums the scrollable About height so the scrollbar tracks it.
func (a *App) aboutContentHeight(lineH int32) int32 {
	total := int32(len(aboutIntroLines)) * lineH
	total += lineH                // gap after intro
	total += 2*lineH + 4          // section title + legend
	for i := range aboutServers { // each: name row + desc rows + gap
		total += lineH + int32(len(aboutServers[i].desc))*lineH + 6
	}
	total += lineH - 6                            // gap before outro
	total += int32(len(aboutOutroLines)) * lineH  // outro
	total += 10 + int32(len(aboutLinks))*(btnH+6) // links
	return total
}

// openBrowser launches the system browser (go-sdl2 has no SDL_OpenURL
// binding; per-OS shellout is the portable fallback).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("ui: opening %s: %v", url, err)
	}
}
