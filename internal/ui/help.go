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
var helpSectionNames = []string{"Glossary"}

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

// buildHelpFlat reflows the active Help section to colW (cached by width via
// a.helpFlat / a.helpFlatW). The aboutFlatLine model + the draw loop mirror
// drawChangelog, so the wrap runs only on a resize, never per frame.
func (a *App) buildHelpFlat(c *Ctx, colW int32) []aboutFlatLine {
	out := make([]aboutFlatLine, 0, 128)
	switch a.helpTab {
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

func (a *App) drawHelp(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	c.Heading(pad, pad, "Help", ColText)
	c.Label(pad, pad+30, "New to Attorney Online? Here's what the words mean.", ColTextDim)
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
