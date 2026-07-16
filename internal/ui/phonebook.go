package ui

// AsyncAO Server Phone Book: a dedicated lobby page over the user's saved
// (favorite) servers. Manual add + connect, persisted in Favorites — which
// survives "Reset settings" and is cleared only by "Wipe everything" — with a
// shareable clipboard export/import (disk I/O off the render thread, §17.2, so
// the share path is the clipboard, like the jukebox's paste-merge).

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

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

// beginPhoneBookEdit opens the inline editor for a phone-book row, seeding the
// working buffers from the current entry. oldURL is the row's identity key
// (its stored Favorites URL == WebSocketURL()).
func (a *App) beginPhoneBookEdit(oldURL, name, addr string) {
	a.pbEditURL, a.pbEditName, a.pbEditAddr = oldURL, name, addr
	a.connErr = ""
}

// cancelPhoneBookEdit closes the inline editor without saving.
func (a *App) cancelPhoneBookEdit() {
	a.pbEditURL, a.pbEditName, a.pbEditAddr = "", "", ""
}

// savePhoneBookEdit validates the edited address (exactly like the Add form),
// commits the rename/re-address through UpdateFavorite, and — because a
// successful address change alters the row's identity key — clears the edit
// state and re-merges in the same commit so the row list stays consistent.
// desc is passed through unchanged (the entry keeps its description).
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
		// UpdateFavorite returns false only for not-found or an address that
		// already belongs to ANOTHER saved server — surface the latter (the
		// common fat-finger case) rather than silently swallowing the Save.
		a.connErr = "That address is already in your phone book under another entry."
		return
	}
	a.servers = a.mergedFavorites()
	a.cancelPhoneBookEdit()
	a.connErr = ""
	a.lobbyStatus = "Updated " + name + " in your phone book."
}

// --- "Phone Fanat" click-to-quip chip (phone-book page only) ------------------

const (
	fanatShowDuration = 4 * time.Second // how long one quip lingers after a click
	fanatChipW        = 96              // "Phone Fanat" chip width (bottom-right corner)
	fanatChipH        = 22              // chip height
)

// fanatLines are author-supplied in-jokes shown one at a time. Package-level so
// no per-frame allocation happens on the draw path (rule 8).
var fanatLines = []string{"don't forget FantaCrypt!", "SERGEI!!!!"}

// phoneFanatChipRect is the chip's bottom-right rect. Shared by the early
// click-claim and the late draw so the geometry lives in exactly one spot.
func phoneFanatChipRect(w, h int32) sdl.Rect {
	return sdl.Rect{X: w - pad - fanatChipW, Y: h - pad - fanatChipH, W: fanatChipW, H: fanatChipH}
}

// claimPhoneFanatClick pops a fresh quip if the chip was clicked and — like
// classicEditFence — CONSUMES the click so a server row underneath (rows draw
// first and act on c.clicked immediately) can't also select/connect from the
// same press. Called BEFORE the row loop; the chip's visual draws later, on top.
func (a *App) claimPhoneFanatClick(w, h int32) {
	c := a.ctx
	if c.hovering(phoneFanatChipRect(w, h)) && c.clicked {
		a.phoneFanatLine = fanatLines[rand.IntN(len(fanatLines))]
		a.phoneFanatShownAt = a.now()
		c.clicked = false // top-most element wins; the rows below must not also act
	}
}

// drawPhoneFanatChip draws the small, dim, always-visible "Phone Fanat" chip in
// the Phone Book page's bottom-right corner, plus the active quip just above it.
// The CLICK is already handled (and consumed) by claimPhoneFanatClick before the
// rows draw, so this pass is visual only — the button's return is discarded and
// can't double-fire (c.clicked is false by now on a chip click). Called only
// from inside the phone-book branch of drawLobby, so the whole mechanism costs
// nothing anywhere else and can never leak onto the courtroom or the plain
// all-servers lobby. Ephemeral — nothing here persists. Alloc-free: the chip
// label is a fixed string (text-texture cache hit) and the quip only draws while
// active; the lobby 0-alloc gate stages ScreenLobby WITHOUT phoneBookPage, so
// the chip never draws in that gate by construction.
func (a *App) drawPhoneFanatChip(w, h int32) {
	c := a.ctx
	chip := phoneFanatChipRect(w, h)
	// Dim palette: a low-key chip that doesn't compete with the server rows —
	// dim ink, panel body (matches ColTextDim quip styling below). The click was
	// already claimed; discard the return so it can't act a second time.
	_ = c.ButtonCol(chip, "Phone Fanat", ColPanel, ColPanelHi, ColPanelHi, ColTextDim)
	// The zero-value phoneFanatShownAt keeps this false until the first click, so
	// no init flag is needed. The quip draws just ABOVE the chip so it never
	// overlaps it (right-aligned to the chip's right edge).
	if a.phoneFanatLine != "" && a.now().Sub(a.phoneFanatShownAt) < fanatShowDuration {
		x := chip.X + fanatChipW - c.TextWidth(a.phoneFanatLine)
		if x < pad {
			x = pad // a longer line can't run off the left edge
		}
		c.Label(x, chip.Y-18, a.phoneFanatLine, ColTextDim)
	}
}
