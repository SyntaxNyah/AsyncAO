package ui

// Music history (M12 slice): a session-only "recently played" list of the songs
// that played in the room, captured at the existing EventMusic hook (once per
// song — never per frame) so you can grab a link someone /played and save it to
// the jukebox. In-memory only: it's a "what just played" scratchpad, not a
// persisted library (the jukebox playlists are the persisted half). The display
// string is precomputed at capture so the per-frame row draw does zero string
// work — the render/UI hot path stays allocation-free.

import (
	"fmt"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

// musicHistoryCap bounds the recently-played ring (rule §17.4: no unbounded
// slices). Newest first; a replay moves its entry to the front.
const musicHistoryCap = 30

// musicHistEntry is one played song. track is the raw MC text (a full /play URL
// or a server music-list name); the rest are precomputed for the draw.
type musicHistEntry struct {
	track   string // raw wire text — what Play/Share/Save act on
	name    string // cleaned song name (Save uses it as the link title)
	display string // precomputed "<name> — <by>" row label (0 string work at draw)
	isURL   bool   // a real link → Save/Share make sense (a bare name doesn't)
}

// noteMusicHistory records a room music change into the session ring. Called
// from the EventMusic handler (rare — once per song), so it never touches a
// per-frame path. Mirrors logMusicChange's filtering via MusicAction, but —
// unlike the IC log line — it keeps system/area-played songs too (attribution
// is best-effort), and it never indexes Chars out of range.
func (a *App) noteMusicHistory(ev courtroom.Event) {
	_, song, ok := courtroom.MusicAction(ev.Text)
	if !ok || song == "" {
		return // a ~stop sentinel or an area-name transfer — not a song
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

// refreshRecentLabel rebuilds the cached toggle label. Called only when the ring
// size changes (capture / clear) — never per frame — so the jukebox draw reads a
// ready string instead of formatting one every frame.
func (a *App) refreshRecentLabel() {
	a.jukeRecentLbl = fmt.Sprintf("Recently played (%d)", len(a.musicHist))
}

// drawJukeHistory is the jukebox's "Recently played" top-level view: a back
// button, a clear, and a scrollable list of session songs (Save/Play/Share).
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

// drawJukeHistRow draws one recently-played song: Play always, plus Save/Share
// when it's a real link (a bare server-song name can only be re-played).
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
			if a.juke != nil && a.juke.QuickAdd(jukeboxOOCPlaylist, e.name, e.track) {
				a.jukeWarn(fmt.Sprintf("Saved to %q: %s", jukeboxOOCPlaylist, e.name))
			} else {
				a.jukeWarn("Already saved (or at the link cap).")
			}
		}
	}
	c.LabelClipped(r.X+8, r.Y+5, bx-r.X-12, e.display, ColText)
}
