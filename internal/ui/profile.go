package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Character profile (#101) — SLICE 1: the local model, this Settings editor, and
// a reusable card renderer (with a live preview). Configurable: Enable is the
// master switch, "Show on player list" controls visibility. Entirely local and
// standard-client-safe; the cross-client WIRE (other players' cards on the
// roster, via a tiny zero-width IC fingerprint) is slice 2, gated on confirming
// the zero-width channel survives the live server.

// drawProfileSettings draws the "Your profile" Settings section and returns the y
// below it. Settings-only; never a hot path.
func (a *App) drawProfileSettings(y, w int32) int32 {
	c := a.ctx
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
	c.Label(pad, y, "Preview (what other AsyncAO players see):", ColTextDim)
	y += 22
	return a.drawProfileCard(pad, y, pr, a.effectiveShowname())
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
