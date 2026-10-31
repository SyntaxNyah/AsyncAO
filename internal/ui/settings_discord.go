//go:build !nodiscord

package ui

import "github.com/SyntaxNyah/AsyncAO/internal/config"

// Discord Rich Presence settings live in their own build-tagged file; a -tags nodiscord build compiles it out and
// the no-op drawDiscordSection in settings_discord_nodiscord.go takes over.

// drawDiscordSection renders the Settings → Discord section: its header plus the Rich
// Presence row. Returns the next y.
func (a *App) drawDiscordSection(y, w int32) int32 {
	y = a.settingsSection(y, w, "Discord")
	return a.drawDiscordRow(y, w)
}

// drawDiscordRow renders the Rich Presence section: a master toggle (default on), one checkbox per displayed field
// (showname, character and server by default; the area stays private unless chosen) and the live status.
// Returns the next y.
func (a *App) drawDiscordRow(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	dp := a.d.Prefs.Load().DiscordRPC
	changed := false
	if next := c.Checkbox(pad, y, "Discord Rich Presence (\"Playing AsyncAO\" on your profile while Discord runs; fully optional)", dp.Enabled); next != dp.Enabled {
		dp.Enabled = next
		changed = true
	}
	y += 26
	if dp.Enabled {
		c.Label(pad+20, y+2, "Show:", ColTextDim)
		x := pad + 70
		fields := []struct {
			label string
			v     *bool
		}{
			{"server", &dp.ShowServer},
			{"character", &dp.ShowChar},
			{"showname", &dp.ShowName},
			{"area", &dp.ShowArea},
		}
		for _, f := range fields {
			if next := c.Checkbox(x, y, f.label, *f.v); next != *f.v {
				*f.v = next
				changed = true
			}
			x += c.TextWidth(f.label) + 52
		}
		y += 28
		// The app identity is baked in (DefaultDiscordAppID); show only the live presence status.
		status := "(shows \"Playing AsyncAO\" on your Discord profile while Discord is running)"
		if a.d.Presence != nil {
			status = "status: " + a.d.Presence.Status()
		}
		c.LabelClipped(pad+20, y+4, w-pad-30, status, ColTextDim)
		y += 28
	}
	if changed {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.DiscordRPC = dp })
		a.updatePresence()
	}
	return y + 4
}
