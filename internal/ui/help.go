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
		"What a server DOES with these is up to its software. It turns your IP into an IPID — a short hash — so moderators see a stable token instead of your address; the exact hashing differs between server software (tsuserver, Akashi, Athena and the rest each have their own). It also stores the HDID you send (often hashed again) so it can ban a device across names. None of that changes what AsyncAO puts on the wire — a hashed HDID — only how the server files it.",
	}},
	{"What you type and pick", []string{
		"Everything you say IC and OOC, your character, showname and OOC name, the music and evidence you present, and which area you're in are all visible to the room — that's the game. Treat OOC like a public chat: don't share anything you wouldn't post publicly.",
	}},
	{"Encrypted vs plain connections", []string{
		"Servers on the GREEN lobby tier use WSS (encryption), so your traffic is protected in transit. A plain server sends messages as readable text that someone on the same network could in principle see. Prefer encrypted servers where you can.",
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
	maxScroll := contentH - viewH
	if maxScroll < 0 {
		maxScroll = 0
	}
	a.helpDocScroll -= c.WheelIn(sdl.Rect{X: 0, Y: top, W: w, H: viewH}) * scrollStepPx
	if a.helpDocScroll < 0 {
		a.helpDocScroll = 0
	}
	if a.helpDocScroll > maxScroll {
		a.helpDocScroll = maxScroll
	}

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
