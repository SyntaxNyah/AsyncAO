package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Character profile (#101): the local model, this Settings editor, a reusable card
// renderer (with a live preview), and the cross-client wire (slice 2). Configurable:
// Enable is the master switch, "Show on player list" controls visibility. The card is
// standard-client-safe; the cross-client slice (pronouns + tagline) rides the same
// invisible zero-width IC channel as the sprite style (courtroom.WireProfile), so other
// AsyncAO players see it AFTER you speak, while AO2/webAO are unaffected. The bigger
// fields (bio, theme song, art) stay LOCAL — too large for the message channel.

// drawProfileSettings draws the "Your profile" Settings section and returns the y
// below it. Settings-only; never a hot path.
func (a *App) drawProfileSettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // rebase into the settings content card
	_ = w          // laid out by formX/formW; w param kept for the call signature
	pr := a.d.Prefs.Profile()
	if next := c.Checkbox(pad, y, "Enable my character profile — a small card other AsyncAO players can see", pr.Enabled); next != pr.Enabled {
		pr.Enabled = next
		a.d.Prefs.SetProfile(pr)
	}
	y += 24
	c.Label(pad, y, "Standard clients (AO2 / webAO) are unaffected — they just see the normal player list.", ColTextDim)
	y += 24
	if !pr.Enabled {
		return y
	}
	if next := c.Checkbox(pad, y, "Show my profile on the player list", pr.ShowOnList); next != pr.ShowOnList {
		pr.ShowOnList = next
		a.d.Prefs.SetProfile(pr)
	}
	y += 30

	changed := false
	field := func(id, label, val, hint string) string {
		c.Label(pad, y+5, label, ColText)
		n, _ := c.TextField(id, sdl.Rect{X: pad + 110, Y: y, W: 280, H: fieldH}, val, hint)
		c.Label(pad+400, y+5, hint, ColTextDim)
		y += 30
		if n != val {
			changed = true
		}
		return n
	}
	pr.Name = field("prof_name", "Name", pr.Name, "card title (blank = your showname)")
	pr.Pronouns = field("prof_pronouns", "Pronouns", pr.Pronouns, "e.g. they/them")
	pr.Tag = field("prof_tag", "Tagline", pr.Tag, "one-line status")
	pr.Bio = field("prof_bio", "Bio", pr.Bio, "a short line about your character")
	pr.ThemeSong = field("prof_song", "Theme song", pr.ThemeSong, "URL (mp3 / opus / ogg)")
	pr.ArtURL = field("prof_art", "Art URL", pr.ArtURL, "URL to a profile picture")
	if changed {
		a.d.Prefs.SetProfile(pr)
	}

	y += 6
	c.Label(pad, y, "Other AsyncAO players see your name, pronouns and tagline after you", ColTextDim)
	y += 18
	c.Label(pad, y, "speak; bio, theme song and art stay on this device. Standard clients see nothing.", ColTextDim)
	y += 22
	return a.drawProfileCard(pad, y, pr, a.effectiveShowname())
}

// profileFor returns the profile to show for a roster row, if any. For our own row
// it's the LOCAL profile (when Enabled + ShowOnList); for other players it's the
// cross-client slice (#101 slice 2) received over the zero-width IC channel — pronouns
// + tagline only, keyed by the bare character name. The lookup is a plain map read so
// it stays allocation-free per row per frame.
func (a *App) profileFor(p *areaPlayer, isMe bool) (config.ProfilePref, bool) {
	if isMe {
		if pr := a.d.Prefs.Profile(); pr.Enabled && pr.ShowOnList {
			return pr, true
		}
		return config.ProfilePref{}, false
	}
	if a.room != nil && p != nil && p.name != "" {
		if wp, ok := a.room.RemoteProfile(p.name); ok {
			return config.ProfilePref{Enabled: true, ShowOnList: true, Pronouns: wp.Pronouns, Tag: wp.Tag}, true
		}
	}
	return config.ProfilePref{}, false
}

// myWireProfile is the cross-client slice of our profile to transmit (#101 slice 2):
// empty unless the profile is enabled AND set to show on the list (the same gate the
// local card uses), so disabling either stops transmitting (and sends a clear). Only
// pronouns + tagline travel — the receiver's card title uses our live showname.
func (a *App) myWireProfile() courtroom.WireProfile {
	pr := a.d.Prefs.Profile()
	if !pr.Enabled || !pr.ShowOnList {
		return courtroom.WireProfile{}
	}
	return courtroom.WireProfile{Pronouns: pr.Pronouns, Tag: pr.Tag}
}

// openProfileCard opens the player-list profile popover for pr/name.
func (a *App) openProfileCard(pr config.ProfilePref, name string) {
	a.profileCardShow = true
	a.profileCardPr = pr
	a.profileCardName = name
}

// profileCardTitleH is the popover's title band above the card body.
const profileCardTitleH = int32(30)

// profileCardRect is the popover's panel, centred in area (the player list).
// Shared by the draw and drawPlayerList's pointer fence, so the fenced region
// can never drift from the pixels.
func profileCardRect(area sdl.Rect) sdl.Rect {
	cw, ch := profileCardW+20, profileCardH+profileCardTitleH+18
	return sdl.Rect{X: area.X + (area.W-cw)/2, Y: area.Y + (area.H-ch)/2, W: cw, H: ch}
}

// drawProfileCardOverlay paints the profile popover centred in area (the player
// list), if open. Closed by its X. Called last in drawPlayerList so it sits on
// top — the rows beneath it are pointer-fenced while it's hovered.
func (a *App) drawProfileCardOverlay(area sdl.Rect) {
	if !a.profileCardShow {
		return
	}
	c := a.ctx
	panel := profileCardRect(area)
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)
	c.Label(panel.X+10, panel.Y+8, a.profileCardName+" — profile", ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 26, Y: panel.Y + 5, W: 20, H: 20}, "x") {
		a.profileCardShow = false
		return
	}
	a.drawProfileCard(panel.X+10, panel.Y+profileCardTitleH+4, a.profileCardPr, a.profileCardName)
}

// profileCardW / profileCardH size the profile card; the bio clips inside it.
const (
	profileCardW = int32(330)
	profileCardH = int32(98)
)

// drawProfileCard paints a profile card at (x, y) and returns the y below it.
// fallbackName fills the title when the profile sets no Name. Reused by the
// Settings preview now and (slice 2) the player-list popover. Pure drawing.
func (a *App) drawProfileCard(x, y int32, pr config.ProfilePref, fallbackName string) int32 {
	c := a.ctx
	card := sdl.Rect{X: x, Y: y, W: profileCardW, H: profileCardH}
	c.Fill(card, ColPanel)
	c.Border(card, ColAccent)

	name := pr.Name
	if name == "" {
		name = fallbackName
	}
	if name == "" {
		name = "(no name)"
	}
	c.Label(card.X+10, card.Y+8, name, ColText)
	if pr.Pronouns != "" {
		c.Label(card.X+12+c.TextWidth(name), card.Y+8, "· "+pr.Pronouns, ColTextDim)
	}
	if pr.Tag != "" {
		c.LabelClipped(card.X+10, card.Y+30, card.W-20, pr.Tag, ColAccent)
	}
	if pr.Bio != "" {
		c.LabelClipped(card.X+10, card.Y+52, card.W-20, pr.Bio, ColText)
	}
	if pr.ThemeSong != "" {
		c.LabelClipped(card.X+10, card.Y+74, card.W-20, "♪ theme song set", ColTextDim)
	}
	return card.Y + card.H + 8
}
