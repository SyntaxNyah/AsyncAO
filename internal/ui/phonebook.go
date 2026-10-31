package ui

// AsyncAO Server Phone Book: a lobby page over the user's saved (favorite) servers, with manual add and connect.
// It is persisted in Favorites, which survives "Reset settings" and is cleared only by "Wipe everything".
// Sharing is by clipboard export/import, since disk I/O stays off the render thread (hard rule 2).

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// setPhoneBookPage switches the lobby between the full server list and the Phone Book page, for the header button
// and the Servers menu row (menubar.go). Swapping the page also drops the row selection and its cached description
// (the list is re-filtered), resets the scroll and abandons any half-finished row edit.
// It is idempotent: leaving from the all-servers page must not clear a selection the user is reading.
func (a *App) setPhoneBookPage(on bool) {
	if a.phoneBookPage == on {
		return
	}
	a.phoneBookPage = on
	a.selServer, a.descLines, a.descLinks, a.lobbyScroll = -1, nil, nil, 0
	a.cancelPhoneBookEdit()
}

// drawPhoneBookBar draws the Phone Book page's controls (add a server, clipboard export/import).
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

// addPhoneBookServer saves the add-form server into Favorites and re-merges so it shows immediately.
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

// exportPhoneBook copies the phone book to the clipboard as JSON (an SDL call, no disk I/O) for a friend to
// paste and merge.
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

// importPhoneBook merges a phone-book export from the clipboard (additive, deduplicated by URL).
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

// beginPhoneBookEdit opens the inline editor for a row, seeding the buffers from the entry. oldURL is the row's
// identity key (its stored Favorites URL == WebSocketURL()).
func (a *App) beginPhoneBookEdit(oldURL, name, addr string) {
	a.pbEditURL, a.pbEditName, a.pbEditAddr = oldURL, name, addr
	a.connErr = ""
}

// cancelPhoneBookEdit closes the inline editor without saving.
func (a *App) cancelPhoneBookEdit() {
	a.pbEditURL, a.pbEditName, a.pbEditAddr = "", "", ""
}

// savePhoneBookEdit validates the edited address like the Add form and commits through UpdateFavorite. An address
// change alters the row's identity key, so it clears the edit state and re-merges in the same commit.
// desc is passed through unchanged.
func (a *App) savePhoneBookEdit(oldURL, desc string) {
	in := strings.TrimSpace(a.pbEditAddr)
	if in == "" {
		a.connErr = "Enter a server address (host:port, ws:// or wss://)."
		return
	}
	url, err := network.ParseDirectAddress(in, strings.Contains(strings.ToLower(in), "wss"))
	if err != nil {
		a.connErr = err.Error()
		return
	}
	name := strings.TrimSpace(a.pbEditName)
	if name == "" {
		name = in
	}
	if !a.d.Prefs.UpdateFavorite(oldURL, name, url, desc) {
		// UpdateFavorite returns false only for not-found or an address already used by another saved server;
		// surface the latter.
		a.connErr = "That address is already in your phone book under another entry."
		return
	}
	a.servers = a.mergedFavorites()
	a.cancelPhoneBookEdit()
	a.connErr = ""
	a.lobbyStatus = "Updated " + name + " in your phone book."
}

// Phone Fanat click-to-quip chip (phone-book page only)

const (
	fanatShowDuration = 4 * time.Second // how long one quip lingers after a click
	fanatChipW        = 96              // "Phone Fanat" chip width (bottom-right corner)
	fanatChipH        = 22              // chip height
)

// fanatLines are author-supplied in-jokes shown one at a time; package-level so the draw path allocates nothing.
var fanatLines = []string{"don't forget FantaCrypt!", "SERGEI!!!!"}

// phoneFanatChipRect is the chip's bottom-right rect, shared by the early click-claim and the late draw.
func phoneFanatChipRect(w, h int32) sdl.Rect {
	return sdl.Rect{X: w - pad - fanatChipW, Y: h - pad - fanatChipH, W: fanatChipW, H: fanatChipH}
}

// claimPhoneFanatClick pops a fresh quip if the chip was clicked and consumes the click, so a server row underneath
// (rows act on c.clicked immediately) cannot also select or connect. Called before the row loop.
func (a *App) claimPhoneFanatClick(w, h int32) {
	c := a.ctx
	if c.hovering(phoneFanatChipRect(w, h)) && c.clicked {
		a.phoneFanatLine = fanatLines[rand.IntN(len(fanatLines))]
		a.phoneFanatShownAt = a.now()
		c.clicked = false // top-most element wins; the rows below must not also act
	}
}

// drawPhoneFanatChip draws the dim "Phone Fanat" chip in the Phone Book page's bottom-right corner, plus the active
// quip above it. The click was already consumed by claimPhoneFanatClick, so this pass is visual only. Called only
// from the phone-book branch of drawLobby, so nothing leaks onto other screens. Ephemeral and allocation-free.
func (a *App) drawPhoneFanatChip(w, h int32) {
	c := a.ctx
	chip := phoneFanatChipRect(w, h)
	// Dim palette: a low-key chip that does not compete with the server rows.
	// The click was already claimed, so discard the return.
	_ = c.ButtonCol(chip, "Phone Fanat", ColPanel, ColPanelHi, ColPanelHi, ColTextDim)
	// The zero-value phoneFanatShownAt keeps this false until the first click. The quip draws above the chip,
	// right-aligned to its right edge, so it never overlaps it.
	if a.phoneFanatLine != "" && a.now().Sub(a.phoneFanatShownAt) < fanatShowDuration {
		x := chip.X + fanatChipW - c.TextWidth(a.phoneFanatLine)
		if x < pad {
			x = pad // a longer line can't run off the left edge
		}
		c.Label(x, chip.Y-18, a.phoneFanatLine, ColTextDim)
	}
}
