package ui

import (
	"github.com/veandco/go-sdl2/sdl"
)

// The in-app Help screen: a glossary of Attorney Online terms (and, in its
// Privacy section, a plain-English "what the server can see" explainer) for
// newcomers. Reached from the lobby top bar and the courtroom Extras menu. The
// content is reflowed + cached by column width like drawChangelog / drawAbout, so
// the page stays allocation-free per frame.

// helpSectionNames drives the section tab row (which hides itself when there's
// only one section).
var helpSectionNames = []string{"Glossary", "Privacy"}

// glossaryEntry is one Attorney Online term and its plain-English explanation.
type glossaryEntry struct{ term, def string }

// glossaryEntries is the AO newcomer glossary — deliberately plain language, for
// someone who just opened the client and doesn't yet know what "IC" or "CM" mean.
var glossaryEntries = []glossaryEntry{
	{"IC — In Character", "Talking AS your character in the courtroom: your line shows in the chatbox over your sprite. This is the main roleplay channel."},
	{"OOC — Out Of Character", "Talking as YOURSELF, not your character — for coordinating, joking or asking questions. It shows in the OOC box, never over a sprite."},
	{"CM — Case Maker", "The player running the current room/case. A CM can lock the area, manage evidence, kick from that area and direct the scene. Claim it with /cm where it's allowed."},
	{"Mod — Moderator", "Server staff. Mods can warn, kick and ban across the whole server — broader than a CM, who only controls one area."},
	{"WTCE", "Witness Testimony / Cross Examination: the splash animations a judge plays to begin testimony or cross-examination (and the Guilty / Not Guilty verdicts)."},
	{"Pos — Position", "Where your character stands: wit (witness), def (defense), pro (prosecution), jud (judge), hld/hlp (helpers), jur (jury), sea (seance). Set it with the Pos dropdown or /pos."},
	{"Area", "A room on the server. One server hosts many areas; switch in the Areas tab. Each has its own background, music and players."},
	{"Iniswap", "Borrowing another character's sprites and sounds while keeping your own name — AsyncAO's Wardrobe does this for you without editing any files."},
	{"Pairing", "Sharing the stage with another character so two sprites appear side by side. Set it up in the Pair panel."},
	{"Evidence", "Images with descriptions attached to the case (a knife, a photo…). 'Presenting' one shows it to the whole room."},
	{"Showname", "The name shown over your chatbox. It can differ from the character's real name; many servers let you set it freely."},
	{"Blip", "The little typing sound that ticks as your message types out — usually one per character."},
	{"Shouts (Objection! / Hold It! / Take That!)", "Dramatic interjections with a splash and a sound, fired from the shout buttons."},
	{"HDID — Hardware ID", "A per-device identifier the client sends so a server can ban a device, not just an account. The Privacy section spells out exactly what AsyncAO sends."},
	{"IPID", "A hashed stand-in for your IP that mods see instead of the raw address — used to recognise and ban troublemakers across different names."},
	{"Master list", "The public directory of servers shown in the lobby. Servers advertise there so clients can find them."},
	{"Spectator", "Someone in a room who hasn't chosen a character: they can watch and use OOC, but can't speak IC until they pick one."},
}

// privacySection is one heading + its paragraphs in the Privacy explainer.
type privacySection struct {
	heading string
	body    []string
}

// privacySections is the plain-English "what a server can see" explainer. Honest
// and specific: it names exactly what AsyncAO sends (see internal/hwid for the
// HDID) so there are no surprises, rather than vaguely reassuring.
var privacySections = []privacySection{
	{"What every server can see", []string{
		"Connecting to an Attorney Online server — like opening any website or joining any game — lets it see a few things about you. AsyncAO can't hide these, so here's exactly what they are.",
	}},
	{"Your IP address", []string{
		"The server sees your IP address; that's just how an internet connection works. From it an operator can roughly tell your region and internet provider — not your name or address.",
		"Moderators normally DON'T see your raw IP: the server shows them an IPID instead — a scrambled stand-in that lets them recognise and ban a troublemaker without exposing the real address. (A server's own operator can still see raw IPs in their logs.)",
	}},
	{"Your device ID (HDID)", []string{
		"AsyncAO sends a Hardware ID so a server can ban a device, not just a name. It builds one from stable, per-install identifiers the OS exposes — your Windows account SID and MachineGuid, Linux's machine-id, or the macOS hardware UUID — then SALTS and SHA-256-hashes them. Only that hash (asyncao- followed by 64 hex characters) ever leaves your machine; the raw identifiers never cross the wire.",
		"It's an exact hash with no fuzzy matching, so swapping real hardware gives a brand-new, unrelated ID — a genuine hardware change can't be mistaken for someone else's ban. It's stable across reinstalls and renames (so ban-evaders can't just rename) and carries no personal information. It's a pseudonym, not anonymity: a server can still ban your device, it just never sees what your device actually is.",
	}},
	{"How this differs from the standard AO2 client", []string{
		"The classic AO2 (Qt) client sends a RAW identifier as its HDID — not a hash. On Windows that's your actual Windows account SID (S-1-5-21-…); on macOS it's your Mac's real hardware serial number; on Linux it's the machine-id. That real value travels over the connection — the server usually hashes it for storage, but the raw value was already transmitted, and on a non-encrypted server anyone on the network path could read it.",
		"AsyncAO never puts a raw identifier on the wire: it hashes on your device first, and mixes several roots so the fingerprint is harder to forge. One trade-off to know: because the schemes differ, your AsyncAO HDID is a different value from the one AO2 would send for the same PC — they aren't interchangeable. You're still just as bannable; the difference is that your real account and hardware IDs stay on your computer.",
	}},
	{"On the server side (IPID & HDID)", []string{
		"IPIDs exist mainly so moderators can act on someone — recognise and ban a troublemaker across names — rather than as a privacy feature. On all of these, the MODERATORS you deal with see an IPID, not your raw IP. Where the raw IP ends up is a separate question, and it varies: Akashi and the tsuserver family (including KFO) write raw IPs into the server's log FILES, which the operator running the box (the VPS owner) can read — whereas Athena and Nyathena don't keep raw IPs in their logs.",
		"As for the HDID: it's stored so a device can be banned. Only Athena and Nyathena hash it AGAIN on their side for extra privacy; the rest keep it as received. With AsyncAO that's a non-issue either way, because what you send is already a salted hash — a server storing it 'as is' is still only holding an opaque token, never your real IDs. (With the classic AO2 client, 'as is' would mean your raw account SID or hardware serial sitting in that database.)",
	}},
	{"The web server in front can still see your IP", []string{
		"This is the catch worth knowing. An encrypted (wss://) server almost always sits behind a reverse proxy — usually nginx or Caddy — that handles the TLS. That proxy is what your computer actually connects to, so it sees your real IP regardless of what the AO server software then does, and by default nginx (like most proxies) writes that IP into an access log on every connection.",
		"So take any 'we don't log IPs' with a grain of salt: even when the AO software genuinely doesn't, the operator's nginx / Caddy logs almost certainly do — and that proxy is their own infrastructure, outside both AsyncAO's and the AO server software's control. The realistic takeaway: assume whoever runs a server can find your IP if they want to, however private the AO software itself claims to be.",
	}},
	{"If your IP is the concern: use a VPN", []string{
		"AsyncAO already keeps your device ID private — it only ever sends a salted hash, never your real hardware or account IDs. Your IP is the one thing a client can't hide, since the connection has to come from somewhere. If that matters to you, run the client through a trustworthy VPN: the server and its proxy then see the VPN's address, not yours.",
		"Choose a provider with an INDEPENDENTLY AUDITED no-logs policy — such as Mullvad, IVPN or ProtonVPN — not a random free one. Favour a privacy-respecting jurisdiction, outside the Five Eyes (and ideally the wider Nine / Fourteen Eyes) intelligence-sharing networks: ProtonVPN is based in Switzerland; Mullvad and IVPN in Sweden are trusted for their audited no-logs record. Do your own research before trusting any of them.",
		"Be wary of FREE VPNs especially. Running servers costs real money, so a free provider has to make it back somehow — by logging and selling your browsing data, by injecting ads, or worst of all by routing OTHER people's traffic out through YOUR connection as a free exit node, which means a stranger's activity (potentially illegal) can look like it came from your IP. If a VPN is free, assume you're the product. The usual exception people trust is ProtonVPN's FREE tier — the same no-logs Swiss company as its paid plan, just slower and with fewer locations — a decent free option if paying isn't on the table.",
		"With a reputable VPN in front and AsyncAO's hashed HDID, there's nothing on the wire that personally identifies you: the server sees a VPN IP and an opaque device hash, and that's it.",
		"One catch: some servers BLOCK known VPN and proxy IP ranges. After years of people using VPNs to evade bans, plenty of staff just refuse the whole range — so a server that's fine normally can simply fail to connect while your VPN is on. If a server won't let you in through a VPN, that's usually why: turn the VPN off for that server (accepting the IP trade-off), or pick a different one.",
	}},
	{"If you can't connect", []string{
		"A failed connection is FAR more often your network or the server than a bug in AsyncAO — so before you open a GitHub issue, rule out the usual causes first. Is your own internet up (does another site or app work)? Is a VPN, firewall or antivirus in the way (some servers block VPNs — see above)? Is the server itself just down, or is it a yellow/black server AsyncAO can't use? The quickest test: try a different known-good server — if THAT one connects, the problem is the first server or your route to it, not the client.",
		"If you've genuinely ruled all of that out and a server that should be reachable still won't connect — or connects but misbehaves — then it's worth a GitHub issue, with what you already tried, so someone can actually reproduce it.",
	}},
	{"Voice chat & your IP", []string{
		"Voice chat is built so it can't leak your IP. The obvious way to do voice — peer-to-peer, your PC talking straight to the others' — would expose your address to everyone in the call: anyone could read it off their own connection with a packet sniffer like Wireshark. So AsyncAO doesn't do that.",
		"WebRTC with TURN relay servers was an option, and it does work, but for an always-on chatroom it's fragile: drop the connection once and you often can't get back in, and it's more setup and headache than it's worth.",
		"Instead, AsyncAO and LemmyAO use the Nyathena voice handshake, which relays your voice over the SAME WebSocket you're already connected on. Your audio goes to the server and back out to the others, so nobody in the call ever sees your address. The only IP anyone can snoop is the public server's — which everyone's already connected to anyway. Private and stable at once.",
	}},
	{"What you type — and that it's logged", []string{
		"Everything you say IC and OOC, your character, showname and OOC name, the music and evidence you present, and which area you're in are visible to the room — that's the game.",
		"On top of that, nearly every AO server LOGS your messages — for moderation and their own record-keeping — so what you type tends to stick around, not vanish when you leave. Treat it like a public, permanent chat: keep your real-life details off the server, don't reveal anything you wouldn't post publicly, and you never know what someone might do with information you hand out. A good habit is a showname/username that ISN'T tied to your real identity.",
		"And assume EVERY server logs your IP, one way or another — whether the AO software itself records it or its nginx / Caddy proxy does, your address ends up on their infrastructure. However loudly a server claims not to log, treat your IP as seen; that's exactly what a VPN (above) is for.",
	}},
	{"If someone crosses a line: report it", []string{
		"If you hit harassment, someone leaking or misusing personal information, or anyone breaking the rules, press the Call Mod button to alert that server's staff. They're the ones who can actually act on it; AsyncAO is just the client and can't moderate other people's servers for you. That's the right channel — and that's it.",
	}},
	{"Green vs yellow servers (WS vs WSS)", []string{
		"In the lobby, a GREEN server speaks WSS and a YELLOW one speaks plain WS. (A server pinned black at the very bottom is a legacy raw-TCP server AsyncAO can't connect to at all.) The difference between green and yellow is encryption, and it's a real one.",
		"WS (yellow) is a plain, UNENCRYPTED connection. Everything — your messages, your showname, and any password if the server uses logins — travels as readable text. Anyone sitting on the network path can read it: whoever runs the public Wi-Fi you're on, others on that same network, and to a degree your ISP. They can also plainly see which server you're talking to and your IP.",
		"WSS (green) is that same connection wrapped in TLS — the exact encryption that puts the padlock on an https:// website. A snooper on the path now only sees THAT you connected to the server; the contents are scrambled and unreadable to them. On public or untrusted Wi-Fi that's the whole difference between 'anyone nearby can read everything you type' and 'they can't'.",
		"WSS also stops a 'man-in-the-middle' attack. That's when someone on the network path — a rogue public Wi-Fi hotspot, a hacked router — secretly slips between you and the server, reading (or even quietly altering) what you send while relaying it on, so to you nothing looks wrong. Plain WS makes that easy. WSS's TLS both scrambles the traffic AND lets your client check it's really the server on the other end — strictly, with 'Validate server certificates' turned on in Settings — so a man-in-the-middle can neither read your messages nor impersonate the server.",
		"So GREEN / WSS is the recommendation, especially away from home — it protects what you say and any login in transit. Be clear on the limits, though: WSS encrypts the link only up to the server (whose proxy decrypts it), so it shields you from network snoopers, NOT from the server operator, who still sees everything once it lands. And AsyncAO accepts self-signed wss certificates by default so small community servers stay reachable — that proves the link is encrypted but not necessarily WHO you're encrypted to; turn on 'Validate server certificates' in Settings if you want that checked strictly.",
		"Bottom line: prefer green (WSS) servers, treat yellow (WS) like a postcard anyone on the way can read, and pair either with a VPN if your IP is also a concern.",
	}},
	{"What AsyncAO does NOT do", []string{
		"AsyncAO has no analytics, ads or tracking. It only talks to the network for things you'd expect: the public server list (to fill the lobby), the assets you load from a server, and an optional update check. Discord Rich Presence and voice chat are both OFF until you turn them on, and Discord-free / voice-free builds exist.",
	}},
}

// buildHelpFlat reflows the active Help section to colW (cached by width via
// a.helpFlat / a.helpFlatW). The aboutFlatLine model + the draw loop mirror
// drawChangelog, so the wrap runs only on a resize, never per frame.
func (a *App) buildHelpFlat(c *Ctx, colW int32) []aboutFlatLine {
	out := make([]aboutFlatLine, 0, 128)
	switch a.helpTab {
	case 1: // Privacy — what a server can see
		for si, s := range privacySections {
			gap := int32(16)
			if si == 0 {
				gap = 0
			}
			out = append(out, aboutFlatLine{text: s.heading, col: ColAccent, gap: gap})
			for pi, p := range s.body {
				for i, ln := range c.WrapText(p, colW, 0) {
					fl := aboutFlatLine{text: ln, col: ColText}
					if i == 0 && pi > 0 {
						fl.gap = 8 // space between paragraphs in a section
					}
					out = append(out, fl)
				}
			}
		}
	default: // Glossary
		for i, e := range glossaryEntries {
			gap := int32(12)
			if i == 0 {
				gap = 0
			}
			out = append(out, aboutFlatLine{text: e.term, col: ColAccent, gap: gap})
			for _, ln := range c.WrapText(e.def, colW, 0) {
				out = append(out, aboutFlatLine{text: ln, col: ColText})
			}
		}
	}
	return out
}

// openHelp switches to the Help screen on a given section tab (0 = Glossary,
// 1 = Privacy), re-reflowing for that section. Callers set prevScreen first.
func (a *App) openHelp(tab int) {
	a.helpTab, a.helpFlat, a.helpDocScroll = tab, nil, 0
	a.screen = ScreenHelp
}

func (a *App) drawHelp(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	c.Heading(pad, pad, "Help", ColText)
	c.Label(pad, pad+30, "What the words mean — and what a server can see about you.", ColTextDim)
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.screen = a.prevScreen
		return
	}

	top := pad + 56
	// Section tabs — shown only once there's more than one section.
	if len(helpSectionNames) > 1 {
		tx := pad
		for i, name := range helpSectionNames {
			bw := c.TextWidth(name) + 24
			r := sdl.Rect{X: tx, Y: top, W: bw, H: btnH}
			if c.Button(r, name) && a.helpTab != i {
				a.helpTab, a.helpFlat, a.helpDocScroll = i, nil, 0
			}
			if a.helpTab == i {
				c.Border(r, ColAccent)
			}
			tx += bw + 6
		}
		top += btnH + 10
	}
	c.Fill(sdl.Rect{X: 0, Y: top - 10, W: w, H: 1}, ColPanelHi) // hairline under the header
	viewH := h - top - pad

	// Centered reading column (mirrors drawChangelog).
	colW := w - 2*pad - scrollBarW - 2*pad
	if colW > aboutMaxColW {
		colW = aboutMaxColW
	}
	if colW < 200 {
		colW = 200
	}
	x0 := (w - scrollBarW - colW) / 2
	if x0 < pad {
		x0 = pad
	}
	if a.helpFlat == nil || a.helpFlatW != colW {
		a.helpFlat = a.buildHelpFlat(c, colW)
		a.helpFlatW = colW
	}

	lineH := int32(c.font.Height()) + 4
	contentH := int32(0)
	for _, fl := range a.helpFlat {
		contentH += fl.gap + lineH
	}
	contentH += pad
	a.helpDocScroll -= c.WheelIn(sdl.Rect{X: 0, Y: top, W: w, H: viewH}) * scrollStepPx
	// A draggable scrollbar on the right for fast up/down navigation.
	track := sdl.Rect{X: w - scrollBarW - pad, Y: top, W: scrollBarW, H: viewH}
	a.helpDocScroll = c.VScrollbar("helpdocbar", track, a.helpDocScroll, contentH, viewH)

	clip := sdl.Rect{X: 0, Y: top, W: w, H: viewH}
	_ = c.Ren.SetClipRect(&clip)
	defer func() { _ = c.Ren.SetClipRect(nil) }()
	y := top - a.helpDocScroll
	for _, fl := range a.helpFlat {
		y += fl.gap
		if y+lineH > top && y < top+viewH {
			c.Label(x0+fl.indent, y, fl.text, fl.col)
		}
		y += lineH
	}
}
