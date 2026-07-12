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

// serverHelpLegend explains the per-server capability chips below. Worded with
// colours + "tick/cross" rather than ✓/✕ glyphs — the chips draw the marks as
// shapes (drawCheck/drawCrossMark), and the glyphs would render as tofu here.
var serverHelpLegend = "Each server carries three capability chips. WS — a yellow tick (every server here speaks plain WebSockets). WSS — a green tick for native secure WebSockets, or a red cross if it needs a reverse proxy for TLS. Players — a green tick for the modern live player list, a yellow tick if it comes via a plugin, or a red cross for none."

// serverProject is one catalog entry.
type serverProject struct {
	name        string
	lang        string
	parent      string   // "" = base; else the upstream it's a fork of (indented under it)
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
		name: "witches-akashi-party", lang: "C++", parent: "Akashi", wss: true, plist: true,
		desc: []string{
			"A feature-rich community fork of Akashi, with its active development living on the 'tea' branch rather than master.",
			"It is maintained by Ganty1999, Elchi, IDk-2023 and SyntaxNyah, and builds directly on top of Akashi's modern C++/Qt base.",
			"Everything Akashi does, it does too — native WSS and the full modern 2.11 live player list are both included.",
			"On top of that it adds a VIP system, a /radio command, /getmusic, and a dedicated /play DJ role for managing music in a room.",
			"Moderation gains ID-based /kick and /ban, finer-grained mute controls, and OOC text effects (disemvowel / shake / gimp) that also apply to PMs and global chat.",
			"It reworks pairing to sync by client id instead of character id, and it marks webAO users in the area player list so you can tell who's on the web client.",
			"Pick it when you want Akashi's reliability plus a big pile of quality-of-life and roleplay extras for a busy, active community.",
		},
		credits: "Ganty1999, IDk-2023, SyntaxNyah, ElChi, Claude, plus the full upstream Akashi author list",
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
		name: "Nyathena", lang: "Go", parent: "Athena", wss: true, plist: true,
		desc: []string{
			"SyntaxNyah's Go fork of Mangos' Athena, grown into a much larger and more complete feature set.",
			"It adds native WSS support, so you can point it straight at a TLS certificate and serve the secure port without a proxy.",
			"It also implements the modern 2.11 player list, so AsyncAO's real-time roster, spectator counts and pairing data all work against it.",
			"On top of Athena's clean bones it layers a large pile of extra commands and features, some of them deliberately chaotic, that not every server will need.",
			"Despite the bigger feature set it keeps Go's lightweight runtime footprint.",
			"It is the server that AsyncAO itself is most actively developed and tested against day to day.",
			"Pick it when you want Athena's structure with every modern feature switched on and you don't mind the larger surface area.",
		},
		credits: "SyntaxNyah, Claude, MangosArentLiterature, OmniTroid, lambdcalculus, Miles Nottingham, David Skoland",
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
		name: "tsuserver3", lang: "Python", wss: false, plist: false,
		warn: "Deprecated — no longer maintained by the official developers; for a new server, use Akashi instead.",
		desc: []string{
			"The original Python server for Attorney Online 2, and the common ancestor of the entire classic Python server lineage.",
			"For years it was effectively THE AO server, and most long-running communities ran on it at some point.",
			"Modern builds gained WebSocket support, which is what allows browser and streaming clients to reach its descendants at all.",
			"It speaks plain WS only, so any secure WSS port has to be provided by a reverse proxy sitting in front of it.",
			"It has no modern 2.11 player list, so a roster on it is entirely /getarea-driven.",
			"Most importantly it is no longer maintained by the official developers, so although it still runs you should not start a new server on it today.",
			"Its actively-maintained forks below are the living continuation of this lineage; for a fresh modern server, use Akashi.",
		},
		credits: "argoneuscze, oldmud0, OmniTroid, stonedDiscord, Poyoanon, Crystalwarrior, Lewdton, in1tiate, caleb-mabry, collinxchu, Pyraqq, likeawindrammer, cents02, Cerapter, Lernos, shogunator1337, mposs00, Enovale, Fronku, Parazoid",
		links:   []string{"https://github.com/AttorneyOnline/tsuserver3"},
	},
	{
		name: "KFO-Server", lang: "Python", parent: "tsuserver3", wss: true, plist: false,
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
		name: "tsuserverCC", lang: "Python", parent: "tsuserver3", wss: false, plist: false,
		desc: []string{
			"A fork of tsuserver3 originally built for the Case Café community, which is where the \"CC\" in the name comes from.",
			"It keeps the classic tsuserver protocol and command style, with its own additions and tweaks layered on top.",
			"Like the rest of the tsuserver family it is WebSocket-capable but serves plain WS only, so a WSS port needs a reverse proxy.",
			"It does not implement the modern 2.11 player list, so AsyncAO uses /getarea snapshots for the roster against it.",
			"It leans toward casing and roleplay tooling for the communities it grew up serving.",
			"Being Python and tsuserver-based, it is familiar, hackable territory for anyone who has run the classic server before.",
			"Pick it if you specifically want the Case Café flavor of the long-running tsuserver lineage.",
		},
		credits: "RealKaiser, argoneuscze, oldmud0, OmniTroid, Poyoanon, Lewdton, in1tiate, collinxchu, Pyraqq, shogunator1337, likeawindrammer, Cerapter, yuvi18, mposs00, cents02, Crystalwarrior, Enovale, HolyMan-17, Fronku, Parazoid, perplexedMurfy, the-moonwitch",
		links:   []string{"https://github.com/RealKaiser/tsuserverCC"},
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

// Server-catalog card layout.
const (
	shMaxColW    = 960 // reading-column cap (centered); wide enough for chips + text
	shCardPad    = 16  // inner padding of a server card
	shCardGap    = 12  // vertical gap between server cards
	shForkIndent = 26  // a fork card is indented under its upstream (+ a connector)
	shCapsH      = 20  // capability-chip row height
)

// drawServerHelp renders the catalog as a centered column of per-server CARDS
// (scrollable; every link clickable). Every fork link and credit is kept — the
// cards + fork connectors are what make the ecosystem read at a glance.
func (a *App) drawServerHelp(w, h int32) {
	c := a.ctx
	a.drawScreenBackdrop(w, h, "lobbybackground")
	c.Heading(pad, pad, "For server owners — the AsyncAO-ready ecosystem", ColText)
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.screen = ScreenLobby
		return
	}
	top := pad + 44
	c.Fill(sdl.Rect{X: 0, Y: top - 8, W: w, H: 1}, ColPanelHi) // hairline under the header
	view := sdl.Rect{X: 0, Y: top, W: w, H: h - top - pad}

	// Centered reading column.
	colW := w - 2*pad - scrollBarW - 2*pad
	if colW > shMaxColW {
		colW = shMaxColW
	}
	if colW < 320 {
		colW = 320
	}
	x0 := (w - scrollBarW - colW) / 2
	if x0 < pad {
		x0 = pad
	}
	lineH := int32(c.font.Height()) + 4

	// #31: pushClip (not raw SetClipRect) keeps scrolled content out of the
	// header band AND makes hovering() honour the clip — a catalog link
	// scrolled behind the header must not hit-test in its hidden half (a raw
	// clip is draw-only, so the click leaked past the edge). One push spans
	// both the measure and draw passes, exactly like the raw clip did.
	clipPrev, clipHad := c.pushClip(view)
	defer c.popClip(clipPrev, clipHad)

	// Two passes: measure (for the scrollbar), then draw. WrapText is width-memoized,
	// so the doubled calls are cheap; the catalog is a dozen entries.
	draw := func(measure bool) int32 {
		y := top - a.helpScroll
		para := func(text string, col sdl.Color, x, wrapW int32, maxLines int) {
			for _, ln := range c.WrapText(text, wrapW, maxLines) {
				if !measure && y+lineH > top && y < top+view.H {
					c.Label(x, y, ln, col)
				}
				y += lineH
			}
		}
		for _, p := range serverHelpIntro {
			if p == "" {
				y += lineH / 2
				continue
			}
			para(p, ColText, x0, colW, 10)
		}
		y += lineH / 2
		para(serverHelpLegend, ColTextDim, x0, colW, 10)
		y += lineH + 4

		for i := range serverProjects {
			p := &serverProjects[i]
			cardX, cardW := x0, colW
			if p.parent != "" { // a fork: indent the card under its upstream
				cardX, cardW = x0+shForkIndent, colW-shForkIndent
			}
			innerW := cardW - 2*shCardPad
			ch := a.serverCardHeight(p, innerW, lineH)
			if !measure && y+ch > top && y < top+view.H {
				card := sdl.Rect{X: cardX, Y: y, W: cardW, H: ch}
				c.Fill(card, cardColor())
				c.Border(card, ColPanelHi)
				if p.parent != "" {
					a.drawForkConnector(x0, cardX, y)
				}
				ix, cy := cardX+shCardPad, y+shCardPad
				header := p.name + "  ·  " + p.lang
				if p.parent != "" {
					header += "  ·  fork of " + p.parent
				}
				c.Label(ix, cy, header, ColAccent)
				cy += lineH
				a.drawServerCaps(ix, cy, p)
				cy += shCapsH + 8
				for _, s := range p.desc {
					for _, ln := range c.WrapText(s, innerW, 8) {
						c.Label(ix, cy, ln, ColText)
						cy += lineH
					}
				}
				if p.warn != "" {
					for _, ln := range c.WrapText("⚠ "+p.warn, innerW, 6) {
						c.Label(ix, cy, ln, ColDanger)
						cy += lineH
					}
				}
				if p.credits != "" {
					cy += 6
					for _, ln := range c.WrapText("Credits: "+p.credits, innerW, 10) {
						c.Label(ix, cy, ln, ColTextDim)
						cy += lineH
					}
				}
				if len(p.links) > 0 {
					cy += 6
					for _, link := range p.links {
						// #31: the per-link on-screen guard is gone — pushClip now
						// gates linkLabel's hit-test through hovering(), so a link
						// scrolled behind the header no longer clicks in its hidden
						// half. The card cull above still skips off-screen cards
						// entirely (a pure draw optimization).
						a.linkLabel(ix, cy, innerW, link)
						cy += lineH
					}
				}
			}
			y += ch + shCardGap
		}
		y += lineH / 2
		para(serverHelpOutro, ColTextDim, x0, colW, 8)
		return (y + a.helpScroll) - top // content height
	}

	contentH := draw(true)
	if !c.ctrlHeld && c.hovering(view) {
		a.helpScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: w - scrollBarW - 4, Y: view.Y, W: scrollBarW, H: view.H}
	a.helpScroll = c.VScrollbar("helpscroll", track, a.helpScroll, contentH, view.H)
	draw(false)
}

// serverCardHeight is one card's pixel height — it MUST track drawServerHelp's
// vertical advances exactly so the card background fits its content.
func (a *App) serverCardHeight(p *serverProject, innerW, lineH int32) int32 {
	c := a.ctx
	hgt := int32(shCardPad) // top padding
	hgt += lineH            // header (name · lang · fork-of)
	hgt += shCapsH + 8      // capability chips + gap
	for _, s := range p.desc {
		hgt += int32(len(c.WrapText(s, innerW, 8))) * lineH
	}
	if p.warn != "" {
		hgt += int32(len(c.WrapText("⚠ "+p.warn, innerW, 6))) * lineH
	}
	if p.credits != "" {
		hgt += 6 + int32(len(c.WrapText("Credits: "+p.credits, innerW, 10)))*lineH
	}
	if len(p.links) > 0 {
		hgt += 6 + int32(len(p.links))*lineH
	}
	return hgt + shCardPad // bottom padding
}

// drawForkConnector links a fork CARD to the upstream above it: a vertical drop
// in the indent gutter from the gap above down to the card's header, plus a stub
// into the card. Drawn as lines, not glyphs, so it never needs box-drawing chars.
func (a *App) drawForkConnector(x0, cardX, y int32) {
	c := a.ctx
	railX := x0 + 9
	headMid := y + shCardPad + 7 // ≈ the header text's vertical middle
	c.Fill(sdl.Rect{X: railX, Y: y - shCardGap, W: 2, H: headMid - (y - shCardGap)}, ColAccent)
	c.Fill(sdl.Rect{X: railX, Y: headMid, W: cardX - railX, H: 2}, ColAccent)
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
	const icon = 13
	// cell: label, then a DRAWN tick/cross (not a glyph — see drawCheck), then an
	// optional suffix word ("plugin"). ok picks tick vs cross; okCol tints the tick.
	cell := func(bx int32, label string, ok bool, okCol sdl.Color, suffix string) int32 {
		labelW := c.TextWidth(label + " ")
		sufW := int32(0)
		if suffix != "" {
			sufW = c.TextWidth(" " + suffix)
		}
		cw := labelW + icon + sufW + 16
		c.Fill(sdl.Rect{X: bx, Y: y, W: cw, H: 20}, serverCapBox)
		c.Label(bx+8, y+2, label, ColText)
		ix := bx + 8 + labelW
		if ok {
			c.drawCheck(ix, y+3, icon, okCol)
		} else {
			c.drawCrossMark(ix, y+3, icon, ColDanger)
		}
		if suffix != "" {
			c.Label(ix+icon+2, y+2, suffix, okCol)
		}
		return bx + cw + 8
	}
	bx := cell(x, "WS", true, ColTierYellow, "") // every catalog server speaks WS
	bx = cell(bx, "WSS", p.wss, ColTierGreen, "")
	switch {
	case p.plist:
		cell(bx, "Players", true, ColTierGreen, "")
	case p.plistPlugin:
		cell(bx, "Players", true, ColTierYellow, "plugin")
	default:
		cell(bx, "Players", false, ColDanger, "")
	}
}

// drawCheck / drawCrossMark render a tick / cross as short thick strokes instead
// of the ✓ / ✕ glyphs — the embedded font lacks them, so the glyphs rendered as
// tofu (□). s is the icon box size. Same rationale as drawForkConnector.
func (c *Ctx) drawCheck(x, y, s int32, col sdl.Color) {
	_ = c.Ren.SetDrawColor(col.R, col.G, col.B, col.A)
	ax, ay := x+s*15/100, y+s*55/100
	bx, by := x+s*40/100, y+s*80/100
	cx, cy := x+s*85/100, y+s*20/100
	for d := int32(0); d < 3; d++ { // a few offset passes = a readable stroke width
		_ = c.Ren.DrawLine(ax, ay+d, bx, by+d)
		_ = c.Ren.DrawLine(bx, by+d, cx, cy+d)
	}
}

func (c *Ctx) drawCrossMark(x, y, s int32, col sdl.Color) {
	_ = c.Ren.SetDrawColor(col.R, col.G, col.B, col.A)
	for d := int32(0); d < 3; d++ {
		_ = c.Ren.DrawLine(x+s*2/10, y+s*2/10+d, x+s*8/10, y+s*8/10+d)
		_ = c.Ren.DrawLine(x+s*8/10, y+s*2/10+d, x+s*2/10, y+s*8/10+d)
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
