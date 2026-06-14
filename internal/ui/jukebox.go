package ui

// Jukebox: the Wardrobe's music-link library. AO DJs/CMs stream music by
// typing "/play <url>" in OOC (YouTube/Discord links etc.); this stores those
// links in named playlists so you click instead of paste, shuffle a set, or
// fire a song from a bare key. The data is GLOBAL (config.Jukebox, its own
// async file) — shared across every server. Render-thread only; the store does
// the disk I/O off-thread.

import (
	"fmt"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/veandco/go-sdl2/sdl"
)

// jukeFilterKey memoizes the song search so a playlist of thousands isn't
// re-filtered every frame (keyed by query + library revision + playlist).
type jukeFilterKey struct {
	q   string
	rev int64
	pl  int
}

// pollJukebox lands the off-thread library load exactly once.
func (a *App) pollJukebox() {
	if a.juke != nil {
		return
	}
	select {
	case j := <-a.jukeRes:
		a.juke = j
	default:
	}
}

// CloseJukebox stops the debounce timer and writes any pending change. Called
// from main on shutdown (no-op until the library has loaded).
func (a *App) CloseJukebox() {
	if a.juke != nil {
		_ = a.juke.Close()
	}
}

// refreshJukeCache re-snapshots the library when its revision changed, so the
// per-frame draw reads a local copy instead of deep-copying under lock.
func (a *App) refreshJukeCache() {
	if a.juke == nil {
		return
	}
	if rev := a.juke.Rev(); rev != a.jukeCacheRev || a.jukeCache == nil {
		a.jukeCache = a.juke.Playlists()
		a.jukeCacheRev = rev
	}
}

// jukePlay streams a link to everyone (DJ/CM only — the server enforces it).
func (a *App) jukePlay(url string) {
	if a.sess == nil {
		a.jukeWarn("Connect to a server to play music (/play needs DJ/CM rights).")
		return
	}
	a.queueOOCLines([]string{"/play " + url})
	a.jukeWarn("▶ /play " + url)
}

// jukeShare fills the OOC input with the raw link (and focuses it) so you can
// post it for others to grab — a deliberate send, not an automatic one.
func (a *App) jukeShare(url string) {
	if a.sess == nil {
		a.jukeWarn("Connect to a server first.")
		return
	}
	a.oocInput = url
	a.ctx.FocusField("ooc")
	a.showIni = false // close the wardrobe so the OOC box is in reach
	a.jukeWarn("Link dropped in your OOC box — press Enter to post it.")
}

func (a *App) jukeWarn(msg string) {
	a.warnLine = clampLine(msg)
	a.warnAt = time.Now()
}

// drawWardrobeJukeboxBody is the Jukebox section: playlists (folders) of music
// links, then a song list inside the opened one.
func (a *App) drawWardrobeJukeboxBody(panel sdl.Rect, w, h int32) {
	c := a.ctx
	if a.juke == nil {
		c.Label(panel.X+pad, panel.Y+50, "Loading jukebox…", ColTextDim)
		return
	}
	a.refreshJukeCache()
	left := panel.X + pad
	top := panel.Y + 44
	bottom := panel.Y + panel.H - pad
	c.Label(left, top, "Your music library — shared across every server. Play sends /play in OOC (DJ/CM only).", ColTextDim)
	top += 22

	if a.jukeOpen < 0 || a.jukeOpen >= len(a.jukeCache) {
		a.jukeOpen = -1
		a.drawJukeboxPlaylists(left, top, panel.W-pad*2, bottom)
		return
	}
	a.drawJukeboxEntries(left, top, panel.W-pad*2, bottom)
}

// drawJukeboxPlaylists is the top level: create playlists, shuffle all, and a
// scrollable list of playlist rows (open / shuffle / delete).
func (a *App) drawJukeboxPlaylists(x, y, wide, bottom int32) {
	c := a.ctx

	// New-playlist row + Shuffle all.
	a.jukeNewName, _ = c.TextField("jukenew", sdl.Rect{X: x, Y: y, W: 240, H: fieldH}, a.jukeNewName, "New playlist name…")
	if c.Button(sdl.Rect{X: x + 246, Y: y, W: 90, H: btnH}, "+ Add") {
		if a.juke.AddPlaylist(a.jukeNewName) {
			a.jukeNewName = ""
		} else {
			a.jukeWarn("Name it first (or you're at the playlist cap).")
		}
	}
	if c.Button(sdl.Rect{X: x + wide - 130, Y: y, W: 130, H: btnH}, "▶ Shuffle all") {
		if url, ok := a.juke.ShuffleAll(); ok {
			a.jukePlay(url)
		} else {
			a.jukeWarn("No songs yet — open a playlist and add some links.")
		}
	}
	y += fieldH + 8

	// Search (filters playlists by name here).
	a.jukeSearch, _ = c.TextField("jukesearch", sdl.Rect{X: x, Y: y, W: 240, H: fieldH}, a.jukeSearch, "Search playlists…")
	c.Label(x+250, y+5, fmt.Sprintf("%d playlists · %d links total", len(a.jukeCache), a.juke.TotalEntries()), ColTextDim)
	y += fieldH + 8

	query := strings.ToLower(strings.TrimSpace(a.jukeSearch))
	lineH := int32(30)
	listTop := y
	listH := bottom - listTop
	// Count matches for the scrollbar.
	matches := 0
	for _, pl := range a.jukeCache {
		if query == "" || strings.Contains(strings.ToLower(pl.Name), query) {
			matches++
		}
	}
	if c.hovering(sdl.Rect{X: x, Y: listTop, W: wide, H: listH}) {
		a.jukeScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: x + wide - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
	a.jukeScroll = c.VScrollbar("jukescroll", track, a.jukeScroll, int32(matches)*lineH, listH)
	rowY := listTop - a.jukeScroll
	rowW := wide - scrollBarW - 6
	for i := range a.jukeCache {
		pl := &a.jukeCache[i]
		if query != "" && !strings.Contains(strings.ToLower(pl.Name), query) {
			continue
		}
		if rowY > listTop+listH-lineH {
			break
		}
		if rowY >= listTop-lineH {
			a.drawJukePlaylistRow(*pl, i, sdl.Rect{X: x, Y: rowY, W: rowW, H: lineH - 4})
		}
		rowY += lineH
	}
	if matches == 0 {
		c.Label(x, listTop+6, "No playlists yet. Name one above and hit Add.", ColTextDim)
	}
}

// drawJukePlaylistRow draws one playlist: click to open, plus shuffle/delete.
func (a *App) drawJukePlaylistRow(pl config.Playlist, idx int, r sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanel)
	if c.hovering(r) {
		c.Border(r, ColAccent)
	}
	// Right-aligned controls.
	bx := r.X + r.W - 30
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 28, H: r.H}, "×") {
		a.jukeDelPlaylist = idx
	}
	bx -= 116
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 110, H: r.H}, "▶ Shuffle") {
		if url, ok := a.juke.Shuffle(idx); ok {
			a.jukePlay(url)
		} else {
			a.jukeWarn("That playlist is empty.")
		}
	}
	bx -= 76
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 70, H: r.H}, "Open") {
		a.openJukePlaylist(idx)
	}
	// Name + count fill the left; clicking the name area also opens it.
	nameHit := sdl.Rect{X: r.X, Y: r.Y, W: bx - r.X - 6, H: r.H}
	c.LabelClipped(r.X+8, r.Y+6, nameHit.W-12, fmt.Sprintf("%s  (%d)", pl.Name, len(pl.Entries)), ColText)
	if c.hovering(nameHit) && c.clicked {
		a.openJukePlaylist(idx)
	}

	// Inline delete confirmation.
	if a.jukeDelPlaylist == idx {
		cf := sdl.Rect{X: r.X + r.W - 240, Y: r.Y, W: 240, H: r.H}
		c.Fill(cf, ColPanelHi)
		c.Border(cf, ColDanger)
		c.Label(cf.X+6, cf.Y+6, "Delete this playlist?", ColText)
		if c.Button(sdl.Rect{X: cf.X + 150, Y: cf.Y, W: 40, H: r.H}, "Yes") {
			a.juke.RemovePlaylist(idx)
			a.jukeDelPlaylist = -1
		}
		if c.Button(sdl.Rect{X: cf.X + 194, Y: cf.Y, W: 42, H: r.H}, "No") {
			a.jukeDelPlaylist = -1
		}
	}
}

func (a *App) openJukePlaylist(idx int) {
	a.jukeOpen = idx
	a.jukeScroll = 0
	a.jukeSearch = ""
	a.jukeDelPlaylist = -1
}

// drawJukeboxEntries is the inside-a-playlist view: add songs and a scrollable
// song list (play / open / share / remove).
func (a *App) drawJukeboxEntries(x, y, wide, bottom int32) {
	c := a.ctx
	pl := &a.jukeCache[a.jukeOpen]

	// Header: back + name + shuffle.
	if c.Button(sdl.Rect{X: x, Y: y, W: 110, H: btnH}, "‹ Playlists") {
		a.jukeOpen = -1
		a.jukeScroll = 0
		a.jukeSearch = ""
		return
	}
	c.LabelClipped(x+120, y+5, wide-360, fmt.Sprintf("%s  (%d songs)", pl.Name, len(pl.Entries)), ColAccent)
	if c.Button(sdl.Rect{X: x + wide - 130, Y: y, W: 130, H: btnH}, "▶ Shuffle") {
		if url, ok := a.juke.Shuffle(a.jukeOpen); ok {
			a.jukePlay(url)
		} else {
			a.jukeWarn("This playlist is empty — add a link below.")
		}
	}
	y += btnH + 8

	// Add-song row: URL + optional title.
	a.jukeAddURL, _ = c.TextField("jukeurl", sdl.Rect{X: x, Y: y, W: wide - 360, H: fieldH}, a.jukeAddURL, "Paste a music URL (/play link)…")
	a.jukeAddTitle, _ = c.TextField("juketitle", sdl.Rect{X: x + wide - 354, Y: y, W: 200, H: fieldH}, a.jukeAddTitle, "Title (optional)")
	if c.Button(sdl.Rect{X: x + wide - 148, Y: y, W: 148, H: btnH}, "+ Add song") {
		if a.juke.AddEntry(a.jukeOpen, a.jukeAddTitle, a.jukeAddURL) {
			a.jukeAddURL, a.jukeAddTitle = "", ""
		} else {
			a.jukeWarn("Paste a URL first (or you're at the link cap).")
		}
	}
	y += fieldH + 8

	// Search within this playlist.
	a.jukeSearch, _ = c.TextField("jukesearch", sdl.Rect{X: x, Y: y, W: 260, H: fieldH}, a.jukeSearch, "Search songs in this playlist…")
	y += fieldH + 8

	query := strings.ToLower(strings.TrimSpace(a.jukeSearch))
	if query != "" {
		a.refreshJukeFilter(a.jukeOpen, pl.Entries, query)
	}
	rows := len(pl.Entries)
	if query != "" {
		rows = len(a.jukeFiltered)
	}

	lineH := int32(28)
	listTop := y
	listH := bottom - listTop
	if c.hovering(sdl.Rect{X: x, Y: listTop, W: wide, H: listH}) {
		a.jukeScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: x + wide - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
	a.jukeScroll = c.VScrollbar("jukeentscroll", track, a.jukeScroll, int32(rows)*lineH, listH)
	rowY := listTop - a.jukeScroll
	rowW := wide - scrollBarW - 6
	for i := 0; i < rows; i++ {
		ri := i
		if query != "" {
			ri = a.jukeFiltered[i]
		}
		if rowY > listTop+listH-lineH {
			break
		}
		if rowY >= listTop-lineH && ri >= 0 && ri < len(pl.Entries) {
			a.drawJukeEntryRow(pl.Entries[ri], ri, sdl.Rect{X: x, Y: rowY, W: rowW, H: lineH - 3})
		}
		rowY += lineH
	}
	if rows == 0 {
		hint := "No songs yet — paste a link above."
		if query != "" {
			hint = "No songs match your search."
		}
		c.Label(x, listTop+6, hint, ColTextDim)
	}
}

// drawJukeEntryRow draws one song: play (button or row click), open, share, ×.
func (a *App) drawJukeEntryRow(e config.JukeboxEntry, idx int, r sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanel)
	bx := r.X + r.W - 28
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 26, H: r.H}, "×") {
		a.juke.RemoveEntry(a.jukeOpen, idx)
		return
	}
	bx -= 70
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 66, H: r.H}, "Share") {
		a.jukeShare(e.URL)
	}
	bx -= 70
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 66, H: r.H}, "Open ↗") {
		openBrowser(e.URL)
	}
	bx -= 34
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 30, H: r.H}, "▶") {
		a.jukePlay(e.URL)
	}
	// Title (or URL) fills the left; clicking it also plays.
	label := e.Title
	if label == "" {
		label = e.URL
	}
	titleHit := sdl.Rect{X: r.X, Y: r.Y, W: bx - r.X - 6, H: r.H}
	if c.hovering(titleHit) {
		c.Border(titleHit, ColPanelHi)
	}
	c.LabelClipped(r.X+8, r.Y+5, titleHit.W-12, label, ColText)
	if c.hovering(titleHit) && c.clicked {
		a.jukePlay(e.URL)
	}
}

// refreshJukeFilter recomputes the matching song indices for a non-empty query
// (memoized against query + library revision + playlist). Only called when a
// search is active, so the key's q is never "" and can't collide with the
// zero-value key.
func (a *App) refreshJukeFilter(pl int, entries []config.JukeboxEntry, query string) {
	key := jukeFilterKey{q: query, rev: a.jukeCacheRev, pl: pl}
	if key == a.jukeFilteredKey {
		return
	}
	a.jukeFilteredKey = key
	a.jukeFiltered = a.jukeFiltered[:0]
	for i, e := range entries {
		if strings.Contains(strings.ToLower(e.Title), query) || strings.Contains(strings.ToLower(e.URL), query) {
			a.jukeFiltered = append(a.jukeFiltered, i)
		}
	}
}
