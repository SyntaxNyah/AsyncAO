package ui

// The "For server owners" screen: reached from the button beside the
// legacy-servers notice in the lobby. Explains how to get a raw-TCP-era
// server speaking WebSockets (and WSS for the green tier), then catalogs
// the modern server software with clickable repo links. Every project
// that supports WSS also speaks plain WS.

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// serverHelpOutro closes the catalog: nobody's contribution goes
// uncredited just because this page was written before it.
var serverHelpOutro = "Credit lists were taken from each project's git history at the time of writing. If you contributed to any of these projects and your name isn't here — or you contribute later on — all credit to you as well."

// serverHelpIntro is the upgrade guidance shown before the catalog.
var serverHelpIntro = []string{
	"Your server is pinned to the bottom of the lobby because it only speaks the legacy raw-TCP protocol. AsyncAO — like webAO and the AO master list's web tier — connects over WebSockets only.",
	"",
	"How to support WebSockets: run any of the software below. All of it speaks plain WS, and everything marked WSS can also serve the encrypted port that earns the GREEN lobby tier.",
	"For WSS you need a TLS certificate on the websocket port: use the software's built-in TLS where offered, or terminate TLS with a reverse proxy (Caddy or nginx) in front of the plain WS port — a free Let's Encrypt certificate is enough.",
	"Then advertise the ws:// (and wss://) ports on the master server, and web + desktop clients can finally reach you.",
}

// serverProject is one catalog entry.
type serverProject struct {
	name    string
	lang    string
	wss     bool     // WSS ⇒ WS too; false = plain WS only
	desc    []string // three sentences
	warn    string   // "" = none
	credits string   // every author from the project's git history
	links   []string // first = main repo
}

// serverProjects: descriptions per the project owners' own positioning;
// support notes current as of 2026-06.
var serverProjects = []serverProject{
	{
		name: "Akashi", lang: "C++", wss: true,
		desc: []string{
			"The main server software for modern-day Attorney Online, maintained under the AttorneyOnline organization.",
			"Written in C++, it tracks the current protocol closely and is the de-facto reference for new server features.",
			"Speaks WS and WSS out of the box, including the modern 2.11 player list.",
		},
		credits: "Salanto, scatterflower, in1tiate, MangosArentLiterature, Rosemary Witchaven, stonedDiscord, Cerapter, Marisa P, AwesomeAim, Denton Poss, Leifa, Wiso, likeawindrammer, Rebecca, Doz1l, Pyraqq, cow-face, Adam Swanson, HolyMan, Jun-pei, Scott Brenner, SyntaxNyah, cancer, oldmud0, t-h-i-s-u-s-e-r-n-a-m-e-i-s-c-a-n-c-e-r",
		links:   []string{"https://github.com/AttorneyOnline/akashi"},
	},
	{
		name: "Athena", lang: "Go", wss: false,
		desc: []string{
			"A server written in Go by MangosArentLiterature, focused on clean concurrency and carrying less bloat than the bigger forks.",
			"The codebase is small and readable, which makes it a pleasant base to study or build on.",
			"It speaks plain WS only.",
		},
		warn:    "No WSS and no modern 2.11 player-list support.",
		credits: "MangosArentLiterature, lambdcalculus, Miles Nottingham",
		links:   []string{"https://github.com/MangosArentLiterature/Athena"},
	},
	{
		name: "Nyathena", lang: "Go", wss: true,
		desc: []string{
			"SyntaxNyah's Go fork of Mangos' Athena work, grown into a much larger feature set.",
			"It adds WSS and DOES support the modern player list, on top of a pile of chaotic extra features that not every AO server will need.",
			"Pick it when you want Athena's bones with full modern protocol coverage and don't mind the size.",
		},
		credits: "SyntaxNyah, Claude, MangosArentLiterature, David Skoland, lambdcalculus, Miles Nottingham",
		links:   []string{"https://github.com/SyntaxNyah/Nyathena"},
	},
	{
		name: "Whisker", lang: "C3", wss: true,
		desc: []string{
			"Written in C3 and built to be the lightest possible core: every command — even the CM casing commands — is a plugin on top of its own documented API.",
			"The build system is deliberately minimal (no CMake), keeping the server as slim as possible to build and deploy.",
			"It ships premade plugins you can drop straight in, and speaks WS and WSS.",
		},
		credits: "SyntaxNyah, ElChi",
		links:   []string{"https://github.com/SyntaxNyah/Whisker"},
	},
	{
		name: "KFO-Server", lang: "Python", wss: true,
		desc: []string{
			"CrystalWarrior's Python server, forked from the official — now dead — tsuserver3, the original AO Python server.",
			"It carries a huge focus on roleplaying commands and extra features for RP-heavy communities.",
			"Speaks WS and WSS.",
		},
		warn:    "No modern 2.11 player-list tab support.",
		credits: "Alex Noir, Crystalwarrior, argoneus, oldmud0, stonedDiscord, sD, OmniTroid, David Skoland, ghostfeesh, Dev, Lewdton, Jumbowl, BazettFraga, UnDeviato, Pyraq, Parazoid, SymphonyVR, cents02, in1tiate, mastyra, Cerapter, EstatoDeviato, Satoru;1816, windrammer, Mariomagistr, Trey, Denton, Elijah Bansley, Somebody Somebodious, SyntaxNyah, likeawindrammer, scatterflower, AwesomeAim, Chrezm, ElijahZAwesome, Jumblr, Paradox, Rosemary Witchaven, Salanto, deadlestrade, perplexedMurfy, shogun, slavfox, yemt",
		links:   []string{"https://github.com/Crystalwarrior/KFO-Server"},
	},
	{
		name: "Ferris-AO", lang: "Rust", wss: true,
		desc: []string{
			"A privacy-focused server written in Rust as an alternative to the Python and C++ servers, with privacy as the entire design philosophy.",
			"Raw IPs are hashed immediately on receipt and discarded (never logged or stored), hardware IDs get a permanent keyed hash so bans survive reconnects without keeping the original identifier, sensitive database records are encrypted at rest with AES-256-GCM, and passwords are hashed with Argon2id.",
			"Speaks WS and WSS.",
		},
		credits: "SyntaxNyah, Claude",
		links:   []string{"https://github.com/SyntaxNyah/Ferris-AO"},
	},
	{
		name: "Alibi", lang: "C#", wss: true,
		desc: []string{
			"A C# server written with a plugin system in mind from the start.",
			"Extending it means writing .NET plugins rather than patching the core.",
			"WSS support landed very recently, alongside plain WS.",
		},
		credits: "Enovale",
		links:   []string{"https://github.com/Enovale/Alibi"},
	},
	{
		name: "Kagami", lang: "C++", wss: true,
		desc: []string{
			"Scatterflower's server, developed in the AttorneyOnline/AO-SDL repository and shipped as the kagami container image.",
			"It comes from the same from-base-principles effort as the AO-SDL client, with correctness and performance as the explicit design goals.",
			"Speaks WS and WSS.",
		},
		credits: "scatterflower, Salanto, stonedDiscord, in1tiate, SyntaxNyah",
		links: []string{
			"https://github.com/AttorneyOnline/AO-SDL",
			"https://github.com/AttorneyOnline/AO-SDL/pkgs/container/kagami",
		},
	},
}

// drawServerHelp renders the catalog (scrollable; every link clickable).
func (a *App) drawServerHelp(w, h int32) {
	c := a.ctx
	a.drawScreenBackdrop(w, h, "lobbybackground")
	c.Heading(pad, pad, "For server owners — supporting modern clients", ColText)
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.screen = ScreenLobby
		return
	}

	const lineH = 18
	view := sdl.Rect{X: 0, Y: pad + 40, W: w, H: h - pad - 44}
	wrapW := w - 2*pad - scrollBarW - 12

	// Measure pass for the scrollbar, then draw. Wrapping is cached by
	// the kit's width memo; the catalog is a dozen entries — cheap.
	draw := func(measure bool) int32 {
		y := view.Y - a.helpScroll
		put := func(line string, col sdl.Color) {
			if !measure && line != "" && y > view.Y-lineH && y < view.Y+view.H {
				c.LabelClipped(pad, y, wrapW, line, col)
			}
			y += lineH
		}
		for _, para := range serverHelpIntro {
			if para == "" {
				y += lineH / 2
				continue
			}
			for _, line := range c.WrapText(para, wrapW, 8) {
				put(line, ColText)
			}
		}
		y += lineH
		for i := range serverProjects {
			p := &serverProjects[i]
			tier := "WS only"
			if p.wss {
				tier = "WS + WSS"
			}
			put(p.name+"  ·  "+p.lang+"  ·  "+tier, ColAccent)
			for _, sentence := range p.desc {
				for _, line := range c.WrapText(sentence, wrapW, 6) {
					put(line, ColText)
				}
			}
			if p.warn != "" {
				put("⚠ "+p.warn, ColDanger)
			}
			if p.credits != "" {
				for _, line := range c.WrapText("Credits: "+p.credits, wrapW, 8) {
					put(line, ColTextDim)
				}
			}
			for _, link := range p.links {
				if !measure {
					a.linkLabel(pad+12, y, wrapW-12, link)
				}
				y += lineH
			}
			y += lineH // entry gap
		}
		for _, line := range c.WrapText(serverHelpOutro, wrapW, 6) {
			put(line, ColTextDim)
		}
		return y + a.helpScroll - view.Y // content height
	}

	contentH := draw(true)
	if !c.ctrlHeld && c.hovering(view) {
		a.helpScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: w - scrollBarW - 4, Y: view.Y, W: scrollBarW, H: view.H}
	a.helpScroll = c.VScrollbar("helpscroll", track, a.helpScroll, contentH, view.H)
	draw(false)
}

// linkLabel draws one clickable URL: accent text, hover underline, click
// opens the system browser.
func (a *App) linkLabel(x, y, maxW int32, url string) {
	c := a.ctx
	tw := c.TextWidth(url)
	if tw > maxW {
		tw = maxW
	}
	hit := sdl.Rect{X: x, Y: y, W: tw, H: 18}
	c.LabelClipped(x, y, maxW, url, ColAccent)
	if c.hovering(hit) {
		c.Fill(sdl.Rect{X: x, Y: y + 15, W: tw, H: 1}, ColAccent) // underline
		if c.clicked {
			openBrowser(url)
		}
	}
}

// extractURLs pulls http(s):// links out of free text (lobby descriptions),
// trimming trailing punctuation; capped so a hostile description can't
// flood the box.
func extractURLs(s string, max int) []string {
	var out []string
	for _, tok := range strings.Fields(s) {
		tok = strings.TrimRight(strings.Trim(tok, "()[]<>\"'"), ".,;!?")
		if !strings.HasPrefix(tok, "http://") && !strings.HasPrefix(tok, "https://") {
			continue
		}
		out = append(out, tok)
		if len(out) >= max {
			break
		}
	}
	return out
}
