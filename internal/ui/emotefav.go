package ui

import (
	"github.com/veandco/go-sdl2/sdl"
)

// Emote favourites (#77): star the handful of emotes you actually use on a
// character (many ship dozens), and optionally filter the grid to just those.
// Favourites are keyed per character by the emote's INDEX in its list — emote
// labels and talking sprites duplicate within a character (Apollo has three
// "normal" emotes sharing one anim), so a name key would merge distinct emotes
// into one star. See config.ToggleEmoteFav.
//
// Performance: the grid draw is per-frame, so the lookups must be free. A click
// rebuilds emoteFavSet (O(1) membership) and emoteVisible (the indices to show)
// once, then every steady-state frame is a single guard-key compare. The
// backing arrays are reused; nothing here touches the render loop, which keeps
// its 0-alloc gate (render.Viewport).

const emoteFavStarPx = 15 // star badge size in an emote cell's top-right corner

// refreshEmoteView rebuilds the favourite set + visible-index list for the
// active character, but only when the guard key changed (character, favs-only
// toggle, emote count, or a favourite edit). Cheap and idempotent — safe to call
// from every consumer (both grids, the number keys, random) so none depends on
// call order.
func (a *App) refreshEmoteView() {
	char := a.activeCharName()
	favOnly := a.d.Prefs.EmoteFavOnlyOn()
	if a.emoteVisible != nil && a.emoteViewChar == char && a.emoteViewFavOnly == favOnly &&
		a.emoteViewLen == len(a.emotes) && a.emoteViewRev == a.emoteFavRev {
		return // nothing that affects the view changed
	}
	a.emoteViewChar, a.emoteViewFavOnly = char, favOnly
	a.emoteViewLen, a.emoteViewRev = len(a.emotes), a.emoteFavRev

	if a.emoteFavSet == nil {
		a.emoteFavSet = make(map[int]struct{}, 16)
	} else {
		clear(a.emoteFavSet)
	}
	for _, idx := range a.d.Prefs.EmoteFavsFor(char) {
		a.emoteFavSet[idx] = struct{}{}
	}

	// favBoxList is always the favourites in emote order (the floating box reads
	// it regardless of the grid filter). Built here so both it and the box stay
	// allocation-free at steady state — only this rebuild touches them.
	a.favBoxList = a.favBoxList[:0]
	for i := range a.emotes {
		if _, ok := a.emoteFavSet[i]; ok {
			a.favBoxList = append(a.favBoxList, i)
		}
	}

	a.emoteVisible = a.emoteVisible[:0]
	if favOnly {
		a.emoteVisible = append(a.emoteVisible, a.favBoxList...) // favs-only == the fav list
	} else {
		for i := range a.emotes {
			a.emoteVisible = append(a.emoteVisible, i)
		}
	}
}

// toggleEmoteFav flips emote index realIdx's favourite for the active character
// and invalidates the cached view so the next refresh rebuilds it.
func (a *App) toggleEmoteFav(realIdx int) {
	a.d.Prefs.ToggleEmoteFav(a.activeCharName(), realIdx)
	a.emoteFavRev++
}

// drawEmoteFavStar draws the favourite badge in an emote cell's top-right corner
// and reports whether THIS frame's click toggled it (so the caller skips the
// cell's emote-select). The ★ is ALWAYS shown — dim grey when not yet a
// favourite, gold when it is — so favouriting is discoverable (a hover-only star
// is invisible until you happen to hover; the whole feature then goes unnoticed).
// A faint backing only appears when it's favourited or the cell is hovered, so an
// idle grid stays subtle, not noisy. Must be drawn AFTER the cell button (sits on
// top) and its result must win over the button's pick — see the call sites.
func (a *App) drawEmoteFavStar(cell sdl.Rect, realIdx int) bool {
	c := a.ctx
	_, fav := a.emoteFavSet[realIdx]
	sr := sdl.Rect{X: cell.X + cell.W - emoteFavStarPx, Y: cell.Y, W: emoteFavStarPx, H: emoteFavStarPx}
	col := ColTextDim
	if fav {
		col = ColStar
	}
	if fav || c.hovering(cell) {
		c.Fill(sr, ColPanelHi) // backing for contrast only when it matters
	}
	c.Label(sr.X+2, sr.Y, "★", col)
	if c.ClickedIn(sr) {
		a.toggleEmoteFav(realIdx)
		return true
	}
	return false
}

// drawEmoteFavToggle draws the favourites-only filter button at btn and flips
// the (persisted) pref when clicked, invalidating the view. Shared by both
// emote grids. Highlighted while the filter is on.
func (a *App) drawEmoteFavToggle(btn sdl.Rect) {
	c := a.ctx
	on := a.d.Prefs.EmoteFavOnlyOn()
	if on { // accent ring so it's clearly engaged
		c.Fill(sdl.Rect{X: btn.X - 2, Y: btn.Y - 2, W: btn.W + 4, H: btn.H + 4}, ColStar)
	}
	if c.Button(btn, "★ Favs") {
		a.d.Prefs.SetEmoteFavOnly(!on)
		a.emoteFavRev++ // force the visible list to rebuild for the new filter state
	}
	c.Tooltip(btn, "Show only your favourite emotes — click the ★ on an emote to add it")
}

// emotePageOf returns the page (0-based) holding real emote index ri within the
// current visible list, or -1 if ri isn't visible (e.g. a non-favourite picked
// while the favs-only filter is on) — callers then leave the page unchanged.
func (a *App) emotePageOf(ri int) int {
	if a.emotePerPage <= 0 {
		return -1
	}
	for k, idx := range a.emoteVisible {
		if idx == ri {
			return k / a.emotePerPage
		}
	}
	return -1
}
