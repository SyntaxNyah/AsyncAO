package ui

// Jukebox favorites (M12): star a song (★ on its row) and it shows up in a
// dedicated "★ Favorites" view that spans every playlist, so your go-to tracks
// are one click away wherever they live. The list is memoized against the
// library revision (the same self-invalidating snapshot the search and the
// domain-grouping use), so the per-frame draw never re-scans — it walks a cached
// slice. The star itself reads off the by-value playlist snapshot (no lock).

import (
	"fmt"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// favRef points at a starred song by (playlist, entry) index into jukeCache.
type favRef struct {
	pl int
	e  int
}

// refreshJukeFavs rebuilds the cross-playlist favorites list (and its cached
// count label) when the library revision changed — never per frame. Reads the
// jukeCache snapshot refreshJukeCache already maintains.
func (a *App) refreshJukeFavs() {
	if a.jukeFavRev == a.jukeCacheRev && a.jukeFavs != nil {
		return
	}
	a.jukeFavRev = a.jukeCacheRev
	a.jukeFavs = a.jukeFavs[:0]
	for pl := range a.jukeCache {
		for e := range a.jukeCache[pl].Entries {
			if a.jukeCache[pl].Entries[e].Fav {
				a.jukeFavs = append(a.jukeFavs, favRef{pl: pl, e: e})
			}
		}
	}
	a.jukeFavLbl = fmt.Sprintf("★ Favorites (%d)", len(a.jukeFavs))
}

// drawJukeFavorites is the top-level "★ Favorites" view: a back button and a
// scrollable list of every starred song, with its source playlist shown.
func (a *App) drawJukeFavorites(x, y, wide, bottom int32) {
	c := a.ctx
	if c.Button(sdl.Rect{X: x, Y: y, W: 140, H: btnH}, "‹ Playlists") {
		a.jukeShowFav = false
		a.jukeFavScroll = 0
		return
	}
	c.Label(x+150, y+5, "Your starred songs from every playlist — ★ a song to add it here.", ColTextDim)
	y += btnH + 10

	lineH := int32(28)
	listTop := y
	listH := bottom - listTop
	if c.hovering(sdl.Rect{X: x, Y: listTop, W: wide, H: listH}) {
		a.jukeFavScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: x + wide - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
	a.jukeFavScroll = c.VScrollbar("jukefavscroll", track, a.jukeFavScroll, int32(len(a.jukeFavs))*lineH, listH)
	rowY := listTop - a.jukeFavScroll
	rowW := wide - scrollBarW - 6
	for _, ref := range a.jukeFavs {
		if rowY > listTop+listH-lineH {
			break
		}
		if rowY >= listTop-lineH {
			a.drawFavSongRow(ref, sdl.Rect{X: x, Y: rowY, W: rowW, H: lineH - 3})
		}
		rowY += lineH
	}
	if len(a.jukeFavs) == 0 {
		c.Label(x, listTop+6, "No favorites yet — open a playlist and click a song's ★.", ColTextDim)
	}
}

// drawFavSongRow draws one starred song in the Favorites view: ★ (unstar), Play,
// and — for real links — Share/Open, plus the title and its source playlist.
// Uses the song's own (pl, e), so unstarring hits the right entry.
func (a *App) drawFavSongRow(ref favRef, r sdl.Rect) {
	c := a.ctx
	if ref.pl < 0 || ref.pl >= len(a.jukeCache) {
		return
	}
	es := a.jukeCache[ref.pl].Entries
	if ref.e < 0 || ref.e >= len(es) {
		return
	}
	e := es[ref.e]
	c.Fill(r, ColPanel)
	isURL := strings.Contains(e.URL, "://")

	bx := r.X + r.W - 60
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 56, H: r.H}, "Play") {
		a.jukePlay(e.URL)
	}
	if isURL {
		bx -= 66
		if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 62, H: r.H}, "Share") {
			a.jukeShare(e.URL)
		}
		bx -= 56
		if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 52, H: r.H}, "Open") {
			openBrowser(schemeForOpen(e.URL)) // a bare "www." link saved from a log opens with https://
		}
	}
	// ★ unstar (leftmost control, next to the title).
	bx -= 28
	sr := sdl.Rect{X: bx, Y: r.Y, W: 26, H: r.H}
	c.Fill(sr, ColPanelHi)
	c.Label(sr.X+5, r.Y+5, "★", ColStar)
	c.Tooltip(sr, "Remove from favorites")
	if c.hovering(sr) && c.clicked {
		a.juke.SetEntryFav(ref.pl, ref.e, false)
	}

	label := e.Title
	if label == "" {
		label = e.URL
	}
	label += "   ·   " + a.jukeCache[ref.pl].Name // which playlist it lives in
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
