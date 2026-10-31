package ui

// Jukebox: named playlists of music links that /play through OOC, shared across servers (config.Jukebox).
// Render-thread only; the store does the disk I/O off-thread.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// jukeboxOOCPlaylist is where the "save this shared link" action on an OOC line files into; created on first use.
const jukeboxOOCPlaylist = "Saved from chat"

// jukeFilterKey memoizes the song search so a playlist of thousands isn't re-filtered every frame.
type jukeFilterKey struct {
	q   string
	rev int64
	pl  int
}

// pollJukebox lands the off-thread library load once, then drains export/import results onto the toast.
func (a *App) pollJukebox() {
	if a.juke == nil {
		select {
		case j := <-a.jukeRes:
			a.juke = j
		default:
		}
	}
	for {
		select {
		case line := <-a.jukeIORes:
			a.jukeWarn(line)
		default:
			return
		}
	}
}

// jukeboxExportFileName sits beside the executable, where a friend drops it to click Import.
const jukeboxExportFileName = "jukebox-playlists.json"

func jukeboxExportPath() (string, error) {
	dir, err := exeDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, jukeboxExportFileName), nil
}

// exportJukeboxAsync writes the library off-thread (hard rule 2); the Jukebox is safe to read concurrently.
func (a *App) exportJukeboxAsync() {
	juke, res := a.juke, a.jukeIORes
	if juke == nil {
		return
	}
	go func() {
		var line string
		path, err := jukeboxExportPath()
		if err == nil {
			var data []byte
			if data, err = juke.ExportJSON(); err == nil {
				err = os.WriteFile(path, data, 0o644)
			}
		}
		line = "Playlists exported to " + path + " — share that file; others Import it to merge."
		if err != nil {
			line = "Jukebox export failed: " + err.Error()
		}
		select {
		case res <- line:
		default:
		}
	}()
}

// importJukeboxAsync merges a shared export into the library: additive, dedup by URL, your key binds win.
func (a *App) importJukeboxAsync() {
	juke, res := a.juke, a.jukeIORes
	if juke == nil {
		return
	}
	go func() {
		var line string
		path, err := jukeboxExportPath()
		if err == nil {
			var data []byte
			if data, err = os.ReadFile(path); err == nil {
				var n int
				if n, err = juke.MergeJSON(data); err == nil {
					line = fmt.Sprintf("Merged %d new link(s) from %s", n, path)
				}
			}
		}
		if err != nil {
			line = "Jukebox import failed (put " + jukeboxExportFileName + " beside the exe): " + err.Error()
		}
		select {
		case res <- line:
		default:
		}
	}()
}

// CloseJukebox stops the debounce timer and writes any pending change; a no-op until the library has loaded.
func (a *App) CloseJukebox() {
	if a.juke != nil {
		_ = a.juke.Close()
	}
}

// refreshJukeCache re-snapshots on a revision change so the draw reads a local copy instead of deep-copying under lock.
func (a *App) refreshJukeCache() {
	if a.juke == nil {
		return
	}
	if rev := a.juke.Rev(); rev != a.jukeCacheRev || a.jukeCache == nil {
		a.jukeCache = a.juke.Playlists()
		a.jukeCacheRev = rev
	}
}

// jukePlay streams a link to everyone; the server enforces DJ/CM.
func (a *App) jukePlay(url string) {
	if a.sess == nil {
		a.jukeWarn("Connect to a server to play music (/play needs DJ/CM rights).")
		return
	}
	a.queueOOCLines([]string{"/play " + url})
	a.jukeWarn("Playing — /play " + url)
}

// jukeShare posts the raw link to OOC as a plain line, through the same paced pipeline as jukePlay.
func (a *App) jukeShare(url string) {
	if a.sess == nil {
		a.jukeWarn("Connect to a server first.")
		return
	}
	a.queueOOCLines([]string{url})
	a.jukeWarn("Shared in OOC: " + url)
}

func (a *App) jukeWarn(msg string) {
	a.warnLine = clampLine(msg)
	a.warnAt = time.Now()
}

// jukePasteMerge merges an export from the clipboard; MergeJSON is in-memory and the flush async, so render-thread
// safe.
func (a *App) jukePasteMerge() {
	if a.juke == nil {
		return
	}
	txt, err := sdl.GetClipboardText()
	if err != nil || strings.TrimSpace(txt) == "" {
		a.jukeWarn("Clipboard is empty — copy a jukebox export (JSON) first.")
		return
	}
	n, err := a.juke.MergeJSON([]byte(txt))
	if err != nil {
		a.jukeWarn("Clipboard isn't a valid jukebox export.")
		return
	}
	a.jukeWarn(fmt.Sprintf("Merged %d new link(s) from the clipboard.", n))
}

func (a *App) drawWardrobeJukeboxBody(panel sdl.Rect, w, h int32) {
	c := a.ctx
	if a.juke == nil {
		c.Label(panel.X+pad, panel.Y+50, "Loading jukebox…", ColTextDim)
		return
	}
	a.refreshJukeCache()
	a.refreshJukeFavs() // keep the ★ Favorites count label + list current (memoized, 0-alloc when unchanged)
	left := panel.X + pad
	top := panel.Y + 44
	bottom := panel.Y + panel.H - pad
	wide := panel.W - pad*2
	c.Label(left, top, "Your music library — shared across every server. Play sends /play in OOC (DJ/CM only).", ColTextDim)

	atTop := a.jukeOpen < 0 || a.jukeOpen >= len(a.jukeCache)
	histOn := a.d.Prefs.Load().MusicHistory
	if !histOn {
		a.jukeShowRecent = false // feature disabled in Settings: never show the recent view
	}
	// Hidden inside a playlist and inside either sub-view, which have their own back buttons. Labels are cached: 0
	// allocs.
	if atTop && !a.jukeShowRecent && !a.jukeShowFav {
		bx := left + wide
		if histOn {
			togLbl := a.jukeRecentLbl
			if togLbl == "" {
				togLbl = "Recently played (0)" // before the first song
			}
			tw := c.TextWidth(togLbl) + 24
			bx -= tw
			if c.Button(sdl.Rect{X: bx, Y: top - 4, W: tw, H: btnH}, togLbl) {
				a.jukeShowRecent = true
				a.jukeShowFav = false
				a.jukeHistScroll = 0
			}
			bx -= 8
		}
		favLbl := a.jukeFavLbl
		if favLbl == "" {
			favLbl = "★ Favorites (0)"
		}
		fw := c.TextWidth(favLbl) + 24
		bx -= fw
		if c.Button(sdl.Rect{X: bx, Y: top - 4, W: fw, H: btnH}, favLbl) {
			a.jukeShowFav = true
			a.jukeShowRecent = false
			a.jukeFavScroll = 0
		}
	}
	top += 22

	if atTop {
		a.jukeOpen = -1
		switch {
		case a.jukeShowRecent:
			a.drawJukeHistory(left, top, wide, bottom)
		case a.jukeShowFav:
			a.drawJukeFavorites(left, top, wide, bottom)
		default:
			a.drawJukeboxPlaylists(left, top, wide, bottom)
		}
		return
	}
	a.drawJukeboxEntries(left, top, wide, bottom)
}

func (a *App) drawJukeboxPlaylists(x, y, wide, bottom int32) {
	c := a.ctx

	a.jukeNewName, _ = c.TextField("jukenew", sdl.Rect{X: x, Y: y, W: 240, H: fieldH}, a.jukeNewName, "New playlist name…")
	if c.Button(sdl.Rect{X: x + 246, Y: y, W: 90, H: btnH}, "+ Add") {
		if a.juke.AddPlaylist(a.jukeNewName) {
			a.jukeNewName = ""
		} else {
			a.jukeWarn("Name it first (or you're at the playlist cap).")
		}
	}
	if c.Button(sdl.Rect{X: x + wide - 110, Y: y, W: 110, H: btnH}, "Shuffle all") {
		if url, ok := a.juke.ShuffleAll(); ok {
			a.jukePlay(url)
		} else {
			a.jukeWarn("No songs yet — open a playlist and add some links.")
		}
	}
	y += fieldH + 8

	// Export writes the library beside the exe; Import merges one in.
	a.jukeSearch, _ = c.TextField("jukesearch", sdl.Rect{X: x, Y: y, W: 240, H: fieldH}, a.jukeSearch, "Search playlists…")
	c.Label(x+250, y+5, fmt.Sprintf("%d playlists · %d links total", len(a.jukeCache), a.juke.TotalEntries()), ColTextDim)
	if c.Button(sdl.Rect{X: x + wide - 350, Y: y, W: 104, H: btnH}, "Export…") {
		a.exportJukeboxAsync()
	}
	if c.Button(sdl.Rect{X: x + wide - 240, Y: y, W: 110, H: btnH}, "Import file") {
		a.importJukeboxAsync()
	}
	if c.Button(sdl.Rect{X: x + wide - 124, Y: y, W: 124, H: btnH}, "Paste & merge") {
		a.jukePasteMerge()
	}
	y += fieldH + 8

	query := strings.ToLower(strings.TrimSpace(a.jukeSearch))
	lineH := int32(30)
	listTop := y
	listH := bottom - listTop
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

func (a *App) drawJukePlaylistRow(pl config.Playlist, idx int, r sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanel)
	if c.hovering(r) {
		c.Border(r, ColAccent)
	}
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
	bx -= 50
	a.drawJukeKeyBadge(sdl.Rect{X: bx, Y: r.Y, W: 46, H: r.H}, pl.Key, fmt.Sprintf("p:%d", idx))
	nameHit := sdl.Rect{X: r.X, Y: r.Y, W: bx - r.X - 6, H: r.H}
	c.LabelClipped(r.X+8, r.Y+6, nameHit.W-12, fmt.Sprintf("%s  (%d)", pl.Name, len(pl.Entries)), ColText)
	if c.hovering(nameHit) && c.clicked {
		a.openJukePlaylist(idx)
	}

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

func (a *App) drawJukeboxEntries(x, y, wide, bottom int32) {
	c := a.ctx
	pl := &a.jukeCache[a.jukeOpen]

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

	a.jukeSearch, _ = c.TextField("jukesearch", sdl.Rect{X: x, Y: y, W: 260, H: fieldH}, a.jukeSearch, "Search songs in this playlist…")
	y += fieldH + 8

	query := strings.ToLower(strings.TrimSpace(a.jukeSearch))
	if query != "" {
		a.refreshJukeFilter(a.jukeOpen, pl.Entries, query)
	}
	// "Music history" groups its songs under domain headers unless searching.
	grouped := query == "" && pl.Name == jukeboxHistoryPlaylist && len(pl.Entries) > 0
	if grouped {
		a.refreshJukeGroups(a.jukeOpen, pl.Entries)
	}
	var rows int
	switch {
	case grouped:
		rows = len(a.jukeGroupRows)
	case query != "":
		rows = len(a.jukeFiltered)
	default:
		rows = len(pl.Entries)
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
		if rowY > listTop+listH-lineH {
			break
		}
		if rowY >= listTop-lineH {
			r := sdl.Rect{X: x, Y: rowY, W: rowW, H: lineH - 3}
			switch {
			case grouped:
				if gr := a.jukeGroupRows[i]; gr.domain != "" {
					a.drawJukeGroupHeader(gr.domain, r)
				} else if gr.entry >= 0 && gr.entry < len(pl.Entries) {
					a.drawJukeEntryRow(pl.Entries[gr.entry], gr.entry, r)
				}
			case query != "":
				if ri := a.jukeFiltered[i]; ri >= 0 && ri < len(pl.Entries) {
					a.drawJukeEntryRow(pl.Entries[ri], ri, r)
				}
			default:
				a.drawJukeEntryRow(pl.Entries[i], i, r)
			}
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

// drawJukeEntryRow draws one song: play (button or row click), open, share, remove.
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
	bx -= 64
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 60, H: r.H}, "Open") {
		a.openBrowser(schemeForOpen(e.URL)) // a bare "www." link saved from a log opens with https://
	}
	bx -= 50
	a.drawJukeKeyBadge(sdl.Rect{X: bx, Y: r.Y, W: 46, H: r.H}, e.Key, fmt.Sprintf("e:%d:%d", a.jukeOpen, idx))
	// A labelled button because a play glyph is missing from fonts without Geometric Shapes.
	bx -= 60
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 56, H: r.H}, "Play") {
		a.jukePlay(e.URL)
	}
	bx -= 28
	sr := sdl.Rect{X: bx, Y: r.Y, W: 26, H: r.H}
	starCol := ColTextDim
	if e.Fav {
		starCol = ColStar
	}
	c.Fill(sr, ColPanelHi)
	c.IconLabel(sr, "★", starCol)
	c.Tooltip(sr, "Favorite this song (★) — find it fast in the Favorites view")
	if c.hovering(sr) && c.clicked {
		a.juke.SetEntryFav(a.jukeOpen, idx, !e.Fav)
	}
	label := e.Title
	if label == "" {
		label = e.URL
	}
	titleHit := sdl.Rect{X: r.X, Y: r.Y, W: bx - r.X - 6, H: r.H}
	if c.hovering(titleHit) {
		c.Border(titleHit, ColPanelHi)
		c.Tooltip(titleHit, "Click to /play this song (autoplays for everyone)")
	}
	c.LabelClipped(r.X+8, r.Y+5, titleHit.W-12, label, ColText)
	if c.hovering(titleHit) && c.clicked {
		a.jukePlay(e.URL)
	}
}

// refreshJukeFilter recomputes matching song indices; only called with a query, so q never collides with the zero key.
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

// handleJukeboxKeys fires a bind on Alt+<key> via bareBindKey: an entry key /plays that song, a playlist key a random
// one. Ordered between macros and char/emote keys, so it shadows those on purpose. Returns true if it fired.
func (a *App) handleJukeboxKeys() bool {
	k := a.bareBindKey()
	if a.juke == nil || k == 0 {
		return false
	}
	if url, ok := a.juke.ResolveKey(strings.ToLower(sdl.GetKeyName(k))); ok {
		a.jukePlay(url)
		return true
	}
	return false
}

// pollJukeBind completes an armed key capture; it runs before handleHotkeys and consumes the key, so binding a song
// never also plays it the same frame.
func (a *App) pollJukeBind() {
	if a.jukeBindFor == "" || a.juke == nil {
		return
	}
	c := a.ctx
	if c.escPressed {
		a.jukeBindFor = ""
		return
	}
	if c.keyPressed == 0 {
		return
	}
	a.applyJukeKey(a.jukeBindFor, strings.ToLower(sdl.GetKeyName(c.keyPressed)))
	a.jukeBindFor = ""
	c.keyPressed = 0 // consume: don't let the just-bound key also fire this frame
}

// applyJukeKey sets (or clears, with "") the bind named by target: "p:<i>" is a playlist, "e:<pl>:<i>" a song.
func (a *App) applyJukeKey(target, key string) {
	parts := strings.Split(target, ":")
	switch {
	case len(parts) == 2 && parts[0] == "p":
		if i, err := strconv.Atoi(parts[1]); err == nil {
			a.juke.SetPlaylistKey(i, key)
		}
	case len(parts) == 3 && parts[0] == "e":
		pl, e1 := strconv.Atoi(parts[1])
		e, e2 := strconv.Atoi(parts[2])
		if e1 == nil && e2 == nil {
			a.juke.SetEntryKey(pl, e, key)
		}
	}
}

// drawJukeKeyBadge draws a bind chip: click arms a capture, right-click clears. It prints bareBindLabel because a bare
// letter would advertise a press that only types into the IC box.
func (a *App) drawJukeKeyBadge(r sdl.Rect, bound, target string) {
	c := a.ctx
	armed := a.jukeBindFor == target
	label, col := bindChipLabel(bound, armed, "press…", "key"), ColAccent
	if bound == "" && !armed {
		col = ColTextDim // an empty chip is a hint, not a value
	}
	c.Fill(r, ColPanelHi)
	c.LabelClipped(r.X+4, r.Y+(r.H-14)/2, r.W-6, label, col)
	c.Tooltip(r, "Bind a key (click, then press a key) — it fires on "+bareBindChordPrefix+"that key. Right-click clears.")
	if c.hovering(r) {
		if c.clicked {
			a.bindingFor = "" // don't also arm a character keybind
			a.jukeBindFor = target
			c.focusID = "" // the capture owns the next keypress
		} else if c.rightClicked && bound != "" {
			a.applyJukeKey(target, "")
		}
	}
}
