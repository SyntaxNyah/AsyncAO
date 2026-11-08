package ui

// The web half of the Asset source list: the picked server's own host, then its mirrors, asked in order when the
// server's own host lacks a file.

import (
	"slices"
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// hostServerKey is the server whose mirrors the list shows, defaulting to the connected one; "" when none is known.
func (a *App) hostServerKey() (key string, names, keys []string) {
	names, keys = a.loginTargets()
	if len(keys) == 0 {
		return "", nil, nil
	}
	if settings.assetHostKey == "" && a.serverKey != "" {
		settings.assetHostKey = a.serverKey
	}
	cur := max(slices.Index(keys, settings.assetHostKey), 0)
	settings.assetHostKey = keys[cur]
	return keys[cur], names, keys
}

// drawHostRows draws the server's own host and its mirrors, numbered after the rows above the divider. A row's
// format-list state shows while it is hovered, or always when it failed.
func (a *App) drawHostRows(y int32, key string, first int, on bool) int32 {
	if key == "" {
		return y
	}
	col := sourceRowColor(on)
	snap := a.d.Prefs.Load()
	own := snap.ServerWarmInfoFor(key).Origin
	hovered := a.hoverRow(y)
	line := sourceLine{pos: first, text: own, kind: "Server", col: col}
	if own == "" {
		line.text, line.col = "Known once you join this server", ColTextDim
	} else {
		line.note, line.noteCol = hostNote(a.ownHostListState(own), hovered)
	}
	a.drawSourceLine(y, line)
	y += mountRowH

	hosts := snap.ServerAssetHosts(key)
	for i, h := range hosts {
		hovered := a.hoverRow(y)
		mirror := sourceLine{pos: first + 1 + i, text: h, kind: "Mirror", col: col}
		mirror.note, mirror.noteCol = hostNote(a.d.Manager.HostListState(h), hovered)
		a.drawSourceLine(y, mirror)
		act := rowKeep
		if hovered {
			act = a.rowControls(y, i, len(hosts))
		}
		switch act {
		case rowUp:
			a.setServerHosts(key, moveItem(hosts, i, i-1))
		case rowDown:
			a.setServerHosts(key, moveItem(hosts, i, i+1))
		case rowRemove:
			a.setServerHosts(key, slices.Delete(slices.Clone(hosts), i, i+1))
			settings.statusLine = "Removed " + h + "."
		default:
			y += mountRowH
			continue
		}
		return y
	}
	return y
}

// hostNote is a host row's format-list note: shown while hovered, and always when reading it failed.
func hostNote(s media.ManifestState, hovered bool) (string, sdl.Color) {
	if !hovered && s != media.ManifestFailed {
		return "", ColTextDim
	}
	return hostListLabel(s)
}

// addHost adds a mirror for the server key; it returns the status line and whether the list changed.
func (a *App) addHost(key, input string) (string, bool) {
	if key == "" {
		return "Refresh the lobby once before adding a mirror.", false
	}
	snap := a.d.Prefs.Load()
	hosts := snap.ServerAssetHosts(key)
	switch added := config.NormalizeAssetHost(input); {
	case added == "":
		return "That isn't an http:// or https:// address.", false
	case added == snap.ServerWarmInfoFor(key).Origin || slices.Contains(hosts, added):
		return added + " is already asked for this server.", false
	case len(hosts) >= config.AssetHostCap:
		return "Only " + strconv.Itoa(config.AssetHostCap) + " mirrors fit per server, so remove one first.", false
	default:
		a.setServerHosts(key, append(slices.Clone(hosts), added))
		return "Added " + added + ".", true
	}
}

// setServerHosts saves a server's mirrors and applies them at once when it is the connected server.
func (a *App) setServerHosts(key string, hosts []string) {
	a.d.Prefs.SetServerAssetHosts(key, hosts)
	if key == a.serverKey && a.sess != nil {
		a.applyAssetHosts()
	}
}

// hostListLabel says in words what reading a host's extensions.json found, and in which color.
func hostListLabel(s media.ManifestState) (string, sdl.Color) {
	switch s {
	case media.ManifestRead:
		return "format list read", ColAccent
	case media.ManifestAbsent:
		return "no format list", ColTextDim
	case media.ManifestFailed:
		return "couldn't read format list", ColDanger
	default:
		return "not checked yet", ColTextDim
	}
}

// ownHostListState is the server's own host's result, which the connect path records per host.
func (a *App) ownHostListState(origin string) media.ManifestState {
	st, ok := a.manifestStatus[hostOfURL(origin)]
	switch {
	case !ok:
		return media.ManifestUnchecked
	case st == seedApplied:
		return media.ManifestRead
	case st == seedAbsent:
		return media.ManifestAbsent
	case st == seedFailed:
		return media.ManifestFailed
	default:
		return media.ManifestUnchecked
	}
}
