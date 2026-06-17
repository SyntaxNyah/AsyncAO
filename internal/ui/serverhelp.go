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

// serverHelpLegend explains the per-server capability ticks below.
var serverHelpLegend = "Each server is tagged:  WS (yellow ✓ — every server here speaks it)  ·  WSS (green ✓ = native secure WebSocket, red ✕ = needs a reverse proxy)  ·  Players (green ✓ = modern live player list, yellow ✓ = via a plugin, red ✕ = none)."

// serverProject is one catalog entry.
type serverProject struct {
	name        string
	lang        string
	wss         bool     // WSS ⇒ WS too; false = plain WS only
	plist       bool     // modern 2.11 live player list in the BASE server
	plistPlugin bool     // player list available only via a plugin (not in base)
	desc        []string // three sentences
	warn        string   // "" = none (extra note beyond the WS/WSS/Players ticks)
	credits     string   // every author from the project's git history
	links       []string // first = main repo
}

// serverProjects: descriptions per the project owners' own positioning;
// support notes current as of 2026-06.
var serverProjects = []serverProject{
	{
		name: "Akashi", lang: "C++", wss: true, plist: true,
		desc: []string{
			"The main, officially-maintained server software for modern-day Attorney Online, developed under the AttorneyOnline organization.",
			"It is written in C++ on the Qt framework, which makes it fast and gives it a mature, well-structured codebase.",
			"Of all the servers it tracks the current protocol most closely, and it is the de-facto reference that new features are designed against.",
			"It terminates TLS itself, so it serves both plain WS and encrypted WSS out of the box without strictly needing a reverse proxy.",
			"It implements the modern 2.11 live player list, which is what powers AsyncAO's real-time roster, shownames, spectator counts and pairing data.",
			"Feature-wise it is the most complete option: areas, evidence, casing, moderation and music are all first-class, and its documentation and support are the best of any AO server.",
			"If you are starting a new server today and want the safest, best-supported, most future-proof choice, this is the one to pick.",
		},
		credits: "Salanto, scatterflower, in1tiate, MangosArentLiterature, Rosemary Witchaven, stonedDiscord, Cerapter, Marisa P, AwesomeAim, Denton Poss, Leifa, Wiso, likeawindrammer, Rebecca, Doz1l, Pyraqq, cow-face, Adam Swanson, HolyMan, Jun-pei, Scott Brenner, SyntaxNyah, cancer, oldmud0, t-h-i-s-u-s-e-r-n-a-m-e-i-s-c-a-n-c-e-r",
		links:   []string{"https://github.com/AttorneyOnline/akashi"},
	},
	{
		name: "witches-akashi-party", lang: "C++", wss: true, plist: true,
		desc: []string{
			"A feature-rich community fork of Akashi, with its active development living on the 'tea' branch rather than master.",
			"It is maintained by Ganty1999, Elchi, IDk-2023 and SyntaxNyah, and builds directly on top of Akashi's modern C++/Qt base.",
			"Everything Akashi does, it does too — native WSS and the full modern 2.11 live player list are both included.",
			"On top of that it adds a VIP system, a /radio command, /getmusic, and a dedicated /play DJ role for managing music in a room.",
			"Moderation gains ID-based /kick and /ban, finer-grained mute controls, and OOC text effects (disemvowel / shake / gimp) that also apply to PMs and global chat.",
			"It reworks pairing to sync by client id instead of character id, and it marks webAO users in the area player list so you can tell who's on the web client.",
			"Pick it when you want Akashi's reliability plus a big pile of quality-of-life and roleplay extras for a busy, active community.",
		},
		credits: "Ganty1999, ElChi, IDk-2023, SyntaxNyah, and the upstream Akashi authors",
		links:   []string{"https://github.com/Elchi-2023/witches-akashi-party/tree/tea"},
	},
	{
		name: "Athena", lang: "Go", wss: false, plist: false,
		desc: []string{
			"A server written in Go by MangosArentLiterature, focused on clean concurrency and carrying far less bloat than the bigger forks.",
			"The codebase is deliberately small and readable, which makes it a genuinely pleasant base to study or build on top of.",
			"It implements the core AO2 experience — areas, IC and OOC chat, music, evidence and moderation — without piling on kitchen-sink extras.",
			"It speaks plain WS only, so to offer an encrypted WSS port you will need to terminate TLS with a reverse proxy such as Caddy or nginx.",
			"The stock build does not implement the modern 2.11 player list, so AsyncAO falls back to /getarea snapshots for its roster.",
			"Its real strength is being a lean, well-structured foundation rather than a batteries-included server.",
			"If you want a minimal Go base to extend yourself it is an excellent starting point — and see Nyathena just below for a much fuller fork.",
		},
		credits: "MangosArentLiterature, lambdcalculus, Miles Nottingham",
		links:   []string{"https://github.com/MangosArentLiterature/Athena"},
	},
	{
		name: "Nyathena", lang: "Go", wss: true, plist: true,
		desc: []string{
			"SyntaxNyah's Go fork of Mangos' Athena, grown into a much larger and more complete feature set.",
			"It adds native WSS support, so you can point it straight at a TLS certificate and serve the secure port without a proxy.",
			"It also implements the modern 2.11 player list, so AsyncAO's real-time roster, spectator counts and pairing data all work against it.",
			"On top of Athena's clean bones it layers a large pile of extra commands and features, some of them deliberately chaotic, that not every server will need.",
			"Despite the bigger feature set it keeps Go's lightweight runtime footprint.",
			"It is the server that AsyncAO itself is most actively developed and tested against day to day.",
			"Pick it when you want Athena's structure with every modern feature switched on and you don't mind the larger surface area.",
		},
		credits: "SyntaxNyah, Claude, MangosArentLiterature, David Skoland, lambdcalculus, Miles Nottingham",
		links:   []string{"https://github.com/SyntaxNyah/Nyathena"},
	},
	{
		name: "Whisker", lang: "C3", wss: true, plistPlugin: true,
		desc: []string{
			"A super-lightweight server written in C3, built from the ground up to be the smallest possible core.",
			"Its entire philosophy is the plugin system: even core features like the CM casing commands are plugins layered on top of a documented API.",
			"The base itself stays tiny — it has been benchmarked using even less memory than Akashi — and you add only the features you actually want.",
			"The build system is deliberately minimal, with no CMake, so the server is quick to compile and to deploy.",
			"It ships with over four premade plugins you can drop straight in, including a live player-list plugin (the base has no player list of its own).",
			"It speaks both plain WS and native WSS.",
			"Choose it when you want maximum control over your server's footprint and features and you are comfortable composing it from plugins.",
		},
		credits: "SyntaxNyah, ElChi",
		links:   []string{"https://github.com/SyntaxNyah/Whisker"},
	},
	{
		name: "KFO-Server", lang: "Python", wss: true, plist: false,
		desc: []string{
			"CrystalWarrior's Python server, forked from the official — now discontinued — tsuserver3, the original AO Python server.",
			"It carries a huge focus on roleplaying commands and extra features tailored to RP-heavy communities.",
			"As a long-lived community project it has one of the largest command sets of any AO server.",
			"Being Python and battle-tested, it is approachable to read and modify and is widely deployed across the casing scene.",
			"It speaks both plain WS and WSS.",
			"It does not implement the modern 2.11 player-list tab, so AsyncAO uses /getarea snapshots for the roster there.",
			"Pick it if your community lives on its rich roleplay command set and you don't need the modern live player list.",
		},
		credits: "Alex Noir, Crystalwarrior, argoneus, oldmud0, stonedDiscord, sD, OmniTroid, David Skoland, ghostfeesh, Dev, Lewdton, Jumbowl, BazettFraga, UnDeviato, Pyraq, Parazoid, SymphonyVR, cents02, in1tiate, mastyra, Cerapter, EstatoDeviato, Satoru;1816, windrammer, Mariomagistr, Trey, Denton, Elijah Bansley, Somebody Somebodious, SyntaxNyah, likeawindrammer, scatterflower, AwesomeAim, Chrezm, ElijahZAwesome, Jumblr, Paradox, Rosemary Witchaven, Salanto, deadlestrade, perplexedMurfy, shogun, slavfox, yemt",
		links:   []string{"https://github.com/Crystalwarrior/KFO-Server"},
	},
	{
		name: "Ferris-AO", lang: "Rust", wss: true, plist: true,
		desc: []string{
			"A privacy-first server written in Rust as an alternative to the Python and C++ servers, with privacy as its entire design philosophy.",
			"Raw IP addresses are hashed immediately on receipt and then discarded — never logged or stored anywhere.",
			"Hardware IDs are given a permanent keyed hash, so bans survive reconnects without the server ever keeping the original identifier.",
			"Sensitive database records are encrypted at rest with AES-256-GCM, and account passwords are hashed with Argon2id.",
			"It is built async-first on Tokio and implements the AO2 protocol over both WebSocket (including native WSS) and legacy TCP.",
			"It also streams the modern live player list, so AsyncAO's real-time roster works against it.",
			"Choose it when player privacy and data protection are non-negotiable requirements for your community.",
		},
		credits: "SyntaxNyah, Claude",
		links:   []string{"https://github.com/SyntaxNyah/Ferris-AO"},
	},
	{
		name: "Alibi", lang: "C#", wss: true, plist: false,
		desc: []string{
			"A C# server on .NET Core, written with a plugin system in mind from the very start.",
			"Extending it means writing .NET plugins rather than patching the core, which keeps your customizations clean and upgrade-safe.",
			"It is cross-platform, running anywhere .NET does, including Windows and Linux.",
			"It was created out of frustration with maintaining existing servers, aiming for a more pleasant day-to-day operator experience.",
			"It includes case alerts, commands and logging, and its WebSocket transport was recently overhauled to behave properly.",
			"Both plain WS and native WSS are supported, with the WSS support having landed fairly recently.",
			"The modern 2.11 live player list is still on its to-do list, so AsyncAO uses /getarea snapshots for the roster for now.",
		},
		credits: "Enovale",
		links:   []string{"https://github.com/Enovale/Alibi"},
	},
	{
		name: "Kagami", lang: "C++", wss: true, plist: false,
		desc: []string{
			"Scatterflower's server, developed inside the AttorneyOnline/AO-SDL repository and shipped as the 'kagami' container image.",
			"It comes from the same from-first-principles effort as the AO-SDL client, with correctness and performance as explicit design goals.",
			"Written in C++, it aims to be a clean, modern implementation rather than an accretion of legacy code.",
			"Being built right alongside a client gives it an unusually precise read on exactly what the protocol expects.",
			"It is distributed primarily as a container image, which makes deployment straightforward and reproducible.",
			"It speaks both plain WS and native WSS.",
			"It is a newer, smaller project than Akashi, so double-check its current feature coverage (including the live player list) against your needs before committing.",
		},
		credits: "scatterflower, Salanto, stonedDiscord, in1tiate",
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
		y += lineH / 2
		for _, line := range c.WrapText(serverHelpLegend, wrapW, 8) {
			put(line, ColTextDim)
		}
		y += lineH
		for i := range serverProjects {
			p := &serverProjects[i]
			put(p.name+"  ·  "+p.lang, ColAccent)
			if !measure && y > view.Y-20 && y < view.Y+view.H {
				a.drawServerCaps(pad, y, p)
			}
			y += lineH + 4
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

// serverCapBox is the slightly-brighter cell behind each WS/WSS/Players tick, so
// the indicators stand out against the dark catalog backdrop (drawn straight on
// the background they were too dim to read).
var serverCapBox = sdl.Color{R: 78, G: 82, B: 96, A: 255}

// drawServerCaps draws the WS / WSS / Players capability chips for one project:
// WS is always a yellow ✓ (every catalog server speaks WS); WSS is a green ✓
// (native) or red ✕; Players is a green ✓ (native live list), a yellow ✓ marked
// "plugin", or a red ✕.
func (a *App) drawServerCaps(x, y int32, p *serverProject) {
	c := a.ctx
	cell := func(bx int32, label, glyph string, glyphCol sdl.Color) int32 {
		w := c.TextWidth(label+" "+glyph) + 16
		c.Fill(sdl.Rect{X: bx, Y: y, W: w, H: 20}, serverCapBox)
		c.Label(bx+8, y+2, label+" ", ColText)
		c.Label(bx+8+c.TextWidth(label+" "), y+2, glyph, glyphCol)
		return bx + w + 8
	}
	bx := cell(x, "WS", "✓", ColTierYellow)
	if p.wss {
		bx = cell(bx, "WSS", "✓", ColTierGreen)
	} else {
		bx = cell(bx, "WSS", "✕", ColDanger)
	}
	switch {
	case p.plist:
		cell(bx, "Players", "✓", ColTierGreen)
	case p.plistPlugin:
		cell(bx, "Players", "✓ plugin", ColTierYellow)
	default:
		cell(bx, "Players", "✕", ColDanger)
	}
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
