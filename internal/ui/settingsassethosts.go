package ui

import (
	"slices"
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Layout of the Extra asset hosts section.
const (
	assetHostIndent   = 12
	assetHostLabelDY  = 4
	assetHostLabelW   = 80
	assetHostPickW    = 280
	assetHostFieldW   = 420
	assetHostAddW     = 70
	assetHostMoveW    = 56
	assetHostRemoveW  = 80
	assetHostGap      = 8
	assetHostRowH     = 30
	assetHostLineH    = 22
	assetHostSectionB = 10
)

// drawAssetHostSettings edits a server's extra asset hosts, asked in order when the server's own host lacks a file.
func (a *App) drawAssetHostSettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsDesc(pad, y, "When a server's own asset host doesn't have a file, AsyncAO asks these hosts, top to bottom. "+
		"The server's host is always asked first. Each host's extensions.json decides which formats are asked there. "+
		"Use mirrors of the same content: a file is taken from the first host that has it.", ColTextDim)
	y += assetHostGap

	names, keys := a.loginTargets()
	if len(names) == 0 {
		c.Label(pad+assetHostIndent, y, "No servers known yet — Refresh the lobby once, then add hosts here ahead of time.", ColTextDim)
		return y + assetHostRowH
	}
	if settings.assetHostKey == "" && a.serverKey != "" {
		settings.assetHostKey = a.serverKey
	}
	cur := max(slices.Index(keys, settings.assetHostKey), 0)
	settings.assetHostKey = keys[cur]
	key := settings.assetHostKey
	c.Label(pad+assetHostIndent, y+assetHostLabelDY, "Server:", ColText)
	if next, changed := c.Dropdown("assethostsrv", sdl.Rect{X: pad + assetHostLabelW, Y: y, W: assetHostPickW, H: btnH}, names, cur); changed {
		settings.assetHostKey = keys[next]
		key = keys[next]
	}
	y += btnH + assetHostGap

	snap := a.d.Prefs.Load()
	own := snap.ServerWarmInfoFor(key).Origin
	if own == "" {
		own = "not known until you join this server once"
	}
	c.LabelClipped(pad+assetHostIndent, y, w-pad-assetHostIndent-scrollBarW, "1. "+own+"  (the server's own host)", ColTextDim)
	y += assetHostLineH

	hosts := snap.ServerAssetHosts(key)
	changed := false
	buttonsX := pad + assetHostIndent + assetHostFieldW + assetHostGap
	for i, h := range hosts {
		c.LabelClipped(pad+assetHostIndent, y+assetHostLabelDY, assetHostFieldW, strconv.Itoa(i+2)+". "+h, ColText)
		x := buttonsX
		if i > 0 && c.Button(sdl.Rect{X: x, Y: y, W: assetHostMoveW, H: btnH}, "Up") {
			hosts[i-1], hosts[i] = hosts[i], hosts[i-1]
			changed = true
		}
		x += assetHostMoveW + assetHostGap
		if i < len(hosts)-1 && c.Button(sdl.Rect{X: x, Y: y, W: assetHostMoveW, H: btnH}, "Down") {
			hosts[i], hosts[i+1] = hosts[i+1], hosts[i]
			changed = true
		}
		x += assetHostMoveW + assetHostGap
		if c.Button(sdl.Rect{X: x, Y: y, W: assetHostRemoveW, H: btnH}, "Remove") {
			hosts = slices.Delete(hosts, i, i+1)
			changed = true
			settings.statusLine = "Removed " + h + "."
		}
		y += assetHostRowH
		if changed {
			break
		}
	}

	var commit bool
	settings.assetHostInput, commit = c.TextField("assethostadd", sdl.Rect{X: pad + assetHostIndent, Y: y, W: assetHostFieldW, H: fieldH},
		settings.assetHostInput, "https://mirror.example/base/")
	if c.Button(sdl.Rect{X: buttonsX, Y: y, W: assetHostAddW, H: btnH}, "Add") || commit {
		switch added := config.NormalizeAssetHost(settings.assetHostInput); {
		case added == "":
			settings.statusLine = "That isn't an http:// or https:// address."
		case added == snap.ServerWarmInfoFor(key).Origin || slices.Contains(hosts, added):
			settings.statusLine = added + " is already asked for this server."
		case len(hosts) >= config.AssetHostCap:
			settings.statusLine = "Up to " + strconv.Itoa(config.AssetHostCap) + " extra hosts per server. Remove one first."
		default:
			hosts = append(hosts, added)
			changed = true
			settings.assetHostInput = ""
			settings.statusLine = "Added " + added + "."
		}
	}
	y += assetHostRowH

	if changed {
		a.d.Prefs.SetServerAssetHosts(key, hosts)
		if key == a.serverKey && a.sess != nil {
			a.applyAssetHosts()
		}
	}
	return y + assetHostSectionB
}
