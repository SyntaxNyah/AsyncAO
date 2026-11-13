package ui

// Session-only, in-memory list of songs played in the room, captured once per song at EventMusic.
// The display string is precomputed at capture so the row draw does no string work.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// musicHistoryCap bounds the recently-played list (hard rule 4); newest first, a replay moves its entry to the front.
const musicHistoryCap = 30

// jukeboxHistoryPlaylist is the playlist the recently-played Save button files into, apart from "Saved from chat".
const jukeboxHistoryPlaylist = "Music history"

// musicHistEntry is one played song; track is the raw MC text, a full /play URL or a server music-list name.
type musicHistEntry struct {
	track   string // raw wire text — what Play/Share/Save act on
	name    string // cleaned song name (Save uses it as the link title)
	display string // precomputed "<name> — <by>" row label (0 string work at draw)
	isURL   bool   // a real link → Save/Share make sense (a bare name doesn't)
}

// noteMusicHistory records a room music change into the session list.
// Filters like logMusicChange via MusicAction, but keeps system and area-played songs and checks Chars' range.
func (a *App) noteMusicHistory(ev courtroom.Event) {
	if !a.d.Prefs.Load().MusicHistory {
		return // history disabled in Settings (default ON)
	}
	_, song, ok := courtroom.MusicAction(ev.Text)
	if !ok || song == "" {
		return // a ~stop sentinel or an area-name transfer — not a song
	}
	if !a.d.Prefs.Load().MusicURLAllowed(ev.Text) {
		return // server/non-allowlisted song: it still plays, it's just not recorded
	}
	by := ev.Name // the MC showname (field 2); may be empty
	if by == "" && a.sess != nil && ev.Int >= 0 && ev.Int < len(a.sess.Chars) {
		by = a.sess.Chars[ev.Int].Name // fall back to the character — bounds-checked
	}
	display := song
	if by != "" {
		display = song + " — " + by
	}
	e := musicHistEntry{
		track:   ev.Text,
		name:    song,
		display: display,
		isURL:   strings.Contains(ev.Text, "://"),
	}
	// MRU by raw track: drop a prior copy of the same link, then prepend.
	for i := range a.musicHist {
		if a.musicHist[i].track == ev.Text {
			a.musicHist = append(a.musicHist[:i], a.musicHist[i+1:]...)
			break
		}
	}
	a.musicHist = append(a.musicHist, musicHistEntry{})
	copy(a.musicHist[1:], a.musicHist) // shift down; newest at index 0
	a.musicHist[0] = e
	if len(a.musicHist) > musicHistoryCap {
		a.musicHist = a.musicHist[:musicHistoryCap]
	}
	a.refreshRecentLabel()
}

// refreshRecentLabel rebuilds the cached toggle label; called only when the list size changes.
func (a *App) refreshRecentLabel() {
	a.jukeRecentLbl = fmt.Sprintf("Recently played (%d)", len(a.musicHist))
}

// drawJukeHistory is the jukebox's Recently played view: back button, clear, and a scrollable list of session songs.
func (a *App) drawJukeHistory(x, y, wide, bottom int32) {
	c := a.ctx
	if c.Button(sdl.Rect{X: x, Y: y, W: 140, H: btnH}, "‹ Playlists") {
		a.jukeShowRecent = false
		a.jukeHistScroll = 0
		return
	}
	c.Label(x+150, y+5, "Songs played here this session — Save a link to keep it in your library.", ColTextDim)
	if len(a.musicHist) > 0 {
		if c.Button(sdl.Rect{X: x + wide - 100, Y: y, W: 100, H: btnH}, "Clear list") {
			a.musicHist = a.musicHist[:0]
			a.jukeHistScroll = 0
			a.refreshRecentLabel()
			return
		}
	}
	y += btnH + 10

	lineH := int32(28)
	listTop := y
	listH := bottom - listTop
	if c.hovering(sdl.Rect{X: x, Y: listTop, W: wide, H: listH}) {
		a.jukeHistScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: x + wide - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
	a.jukeHistScroll = c.VScrollbar("jukehistscroll", track, a.jukeHistScroll, int32(len(a.musicHist))*lineH, listH)
	rowY := listTop - a.jukeHistScroll
	rowW := wide - scrollBarW - 6
	for i := range a.musicHist {
		if rowY > listTop+listH-lineH {
			break
		}
		if rowY >= listTop-lineH {
			a.drawJukeHistRow(a.musicHist[i], sdl.Rect{X: x, Y: rowY, W: rowW, H: lineH - 3})
		}
		rowY += lineH
	}
	if len(a.musicHist) == 0 {
		c.Label(x, listTop+6, "Nothing yet — when someone plays a song here, it shows up so you can save the link.", ColTextDim)
	}
}

// drawJukeHistRow draws one song: Play always, plus Save and Share when it is a real link, not a bare server-song name.
func (a *App) drawJukeHistRow(e musicHistEntry, r sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanel)
	bx := r.X + r.W - 56
	if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 54, H: r.H}, "Play") {
		a.jukePlay(e.track)
	}
	if e.isURL {
		bx -= 62
		if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 58, H: r.H}, "Share") {
			a.jukeShare(e.track)
		}
		bx -= 58
		if c.Button(sdl.Rect{X: bx, Y: r.Y, W: 54, H: r.H}, "Save") {
			if a.juke != nil && a.juke.QuickAdd(jukeboxHistoryPlaylist, e.name, e.track) {
				a.jukeWarn(fmt.Sprintf("Saved to %q: %s", jukeboxHistoryPlaylist, e.name))
			} else {
				a.jukeWarn("Already saved (or at the link cap).")
			}
		}
	}
	c.LabelClipped(r.X+8, r.Y+5, bx-r.X-12, e.display, ColText)
}

// jukeGroupRow is one row of the grouped Music history view: a domain header (domain != "") or an entry index.
type jukeGroupRow struct {
	domain string
	entry  int
}

// jukeGroupCacheKey rebuilds the grouped layout only when the open playlist or library revision changes.
type jukeGroupCacheKey struct {
	pl  int
	rev int64
}

// jukeHistoryDomainOther labels entries with no recognizable host, such as a manually added bare name.
const jukeHistoryDomainOther = "other"

// refreshJukeGroups rebuilds the domain-grouped rows for the Music history playlist, memoized on playlist and revision.
func (a *App) refreshJukeGroups(pl int, entries []config.JukeboxEntry) {
	key := jukeGroupCacheKey{pl: pl, rev: a.jukeCacheRev}
	if key == a.jukeGroupKey && a.jukeGroupRows != nil {
		return
	}
	a.jukeGroupKey = key
	a.jukeGroupRows = a.jukeGroupRows[:0]

	type de struct {
		domain string
		idx    int
	}
	pairs := make([]de, len(entries))
	for i, e := range entries {
		d := a.d.Prefs.Load().MusicURLDomain(e.URL)
		if d == "" {
			d = jukeHistoryDomainOther
		}
		pairs[i] = de{domain: d, idx: i}
	}
	// Stable sort keeps each domain's songs in their original (recency) order.
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].domain < pairs[j].domain })

	cur := ""
	for i, p := range pairs {
		if i == 0 || p.domain != cur {
			cur = p.domain
			a.jukeGroupRows = append(a.jukeGroupRows, jukeGroupRow{domain: cur})
		}
		a.jukeGroupRows = append(a.jukeGroupRows, jukeGroupRow{entry: p.idx})
	}
}

// drawJukeGroupHeader draws a domain section header in the grouped history view.
func (a *App) drawJukeGroupHeader(domain string, r sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanelHi)
	c.LabelClipped(r.X+6, r.Y+5, r.W-12, domain, ColAccent)
}
