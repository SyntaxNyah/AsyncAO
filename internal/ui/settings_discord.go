//go:build !nodiscord

package ui

// Discord Rich Presence settings live in their own build-tagged file. A Discord-free
// build (-tags nodiscord) compiles this file out entirely and the no-op
// drawDiscordSection in settings_discord_nodiscord.go takes over, so that binary
// carries NO Discord settings code at all — not a hidden section, genuinely absent.

// drawDiscordSection renders the Settings → Discord section: its header plus the Rich
// Presence row. Returns the next y.
func (a *App) drawDiscordSection(y, w int32) int32 {
	y = a.settingsSection(y, w, "Discord")
	return a.drawDiscordRow(y, w)
}

// drawDiscordRow renders the optional Rich Presence section: a master
// toggle (default ON), one checkbox per displayed field (the tick-on
// defaults show showname + character + server; the area stays private
// unless chosen), and the live presence status. The app identity is baked
// in (DefaultDiscordAppID), so there is no user-editable ID box. Returns the next y.
func (a *App) drawDiscordRow(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	dp := a.d.Prefs.Discord()
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
		// The app identity is baked in (the official AsyncAO app) — no user-editable
		// ID box. Show only the live presence status so they can see it connect.
		status := "(shows \"Playing AsyncAO\" on your Discord profile while Discord is running)"
		if a.d.Presence != nil {
			status = "status: " + a.d.Presence.Status()
		}
		c.LabelClipped(pad+20, y+4, w-pad-30, status, ColTextDim)
		y += 28
	}
	if changed {
		a.d.Prefs.SetDiscord(dp)
		a.updatePresence()
	}
	return y + 4
}
