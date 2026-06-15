package ui

// AsyncAO Server Phone Book: a dedicated lobby page over the user's saved
// (favorite) servers. Manual add + connect, persisted in Favorites — which
// survives "Reset settings" and is cleared only by "Wipe everything" — with a
// shareable clipboard export/import (disk I/O off the render thread, §17.2, so
// the share path is the clipboard, like the jukebox's paste-merge).

import (
	"fmt"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// drawPhoneBookBar draws the Phone Book page's controls (add a server + clipboard
// export/import), in place of the all-servers direct-connect row.
func (a *App) drawPhoneBookBar(w, dcY int32) {
	c := a.ctx
	c.Label(pad, dcY+4, "Add server:", ColText)
	x := pad + 92
	a.pbName, _ = c.TextField("pbname", sdl.Rect{X: x, Y: dcY, W: 150, H: fieldH}, a.pbName, "name (optional)")
	a.pbURL, _ = c.TextField("pburl", sdl.Rect{X: x + 158, Y: dcY, W: 250, H: fieldH}, a.pbURL, "host:port, ws:// or wss://")
	if c.Button(sdl.Rect{X: x + 416, Y: dcY, W: 80, H: btnH}, "+ Add") {
		a.addPhoneBookServer()
	}
	if c.Button(sdl.Rect{X: x + 504, Y: dcY, W: 112, H: btnH}, "Copy (export)") {
		a.exportPhoneBook()
	}
	if c.Button(sdl.Rect{X: x + 622, Y: dcY, W: 124, H: btnH}, "Paste (import)") {
		a.importPhoneBook()
	}
}

// addPhoneBookServer saves the add-form server into the phone book (Favorites)
// and re-merges so it shows immediately. Click the row to connect; the ★ on the
// row removes it.
func (a *App) addPhoneBookServer() {
	in := strings.TrimSpace(a.pbURL)
	if in == "" {
		a.connErr = "Enter a server address (host:port, ws:// or wss://)."
		return
	}
	url, err := network.ParseDirectAddress(in, strings.Contains(strings.ToLower(in), "wss"))
	if err != nil {
		a.connErr = err.Error()
		return
	}
	name := strings.TrimSpace(a.pbName)
	if name == "" {
		name = in
	}
	a.d.Prefs.AddFavorite(name, url, "")
	a.servers = a.mergedFavorites()
	a.pbName, a.pbURL, a.connErr = "", "", ""
	a.lobbyStatus = "Added " + name + " to your phone book."
}

// exportPhoneBook copies the phone book to the clipboard as JSON — render-thread
// safe (an SDL call, no disk I/O), and shareable: a friend pastes it to merge.
func (a *App) exportPhoneBook() {
	data, err := a.d.Prefs.ExportFavoritesJSON()
	if err != nil {
		a.connErr = "Export failed: " + err.Error()
		return
	}
	if err := sdl.SetClipboardText(string(data)); err != nil {
		a.connErr = "Export failed: " + err.Error()
		return
	}
	a.connErr = ""
	a.lobbyStatus = "Phone book copied to the clipboard — paste it to a friend to share."
}

// importPhoneBook merges a phone-book export from the clipboard (additive, dedup
// by URL).
func (a *App) importPhoneBook() {
	txt, err := sdl.GetClipboardText()
	if err != nil || strings.TrimSpace(txt) == "" {
		a.connErr = "Clipboard is empty — copy a phone-book export (JSON) first."
		return
	}
	n, err := a.d.Prefs.MergeFavoritesJSON([]byte(txt))
	if err != nil {
		a.connErr = "Clipboard isn't a valid phone-book export."
		return
	}
	a.servers = a.mergedFavorites()
	a.connErr = ""
	a.lobbyStatus = fmt.Sprintf("Imported %d new server(s) into your phone book.", n)
}
