package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/veandco/go-sdl2/sdl"
)

// Emote favourites (#77): star the handful of emotes you actually use on a
// character (many ship dozens), and optionally filter the grid to just those.
// Favourites are keyed per character by the emote's INDEX in its list — emote
// labels and talking sprites duplicate within a character (Apollo has three
// "normal" emotes sharing one anim), so a name key would merge distinct emotes
// into one star. See config.ToggleEmoteFav.
// Performance: the grid draw is per-frame, so the lookups must be free. A click
// rebuilds emoteFavSet (O(1) membership) and emoteVisible (the indices to show)

const emoteFavStarPx = 15 // star badge size in an emote cell's top-right corner

// refreshEmoteView rebuilds the favourite set + visible-index list for the
// active character, but only when the guard key changed (character, favs-only
// toggle, emote count, or a favourite edit). Cheap and idempotent — safe to call
// from every consumer (both grids, the number keys, random) so none depends on
// call order.
func (a *App) refreshEmoteView() {
	char := a.activeCharName()
	favOnly := a.d.Prefs.Load().EmoteFavOnly
	if a.favstyle.emoteVisible != nil && a.favstyle.emoteViewChar == char && a.favstyle.emoteViewFavOnly == favOnly &&
		a.favstyle.emoteViewLen == len(a.emotepick.emotes) && a.favstyle.emoteViewRev == a.favstyle.emoteFavRev {
		return // nothing that affects the view changed
	}
	a.favstyle.emoteViewChar, a.favstyle.emoteViewFavOnly = char, favOnly
	a.favstyle.emoteViewLen, a.favstyle.emoteViewRev = len(a.emotepick.emotes), a.favstyle.emoteFavRev

	if a.favstyle.emoteFavSet == nil {
		a.favstyle.emoteFavSet = make(map[int]struct{}, 16)
	} else {
		clear(a.favstyle.emoteFavSet)
	}
	for _, idx := range a.d.Prefs.Load().EmoteFavsFor(char) {
		a.favstyle.emoteFavSet[idx] = struct{}{}
	}

	// favBoxList is always the favourites in emote order (the floating box reads
	// it regardless of the grid filter). Built here so both it and the box stay
	// allocation-free at steady state — only this rebuild touches them.
	a.favstyle.favBoxList = a.favstyle.favBoxList[:0]
	for i := range a.emotepick.emotes {
		if _, ok := a.favstyle.emoteFavSet[i]; ok {
			a.favstyle.favBoxList = append(a.favstyle.favBoxList, i)
		}
	}

	a.favstyle.emoteVisible = a.favstyle.emoteVisible[:0]
	if favOnly {
		a.favstyle.emoteVisible = append(a.favstyle.emoteVisible, a.favstyle.favBoxList...) // favs-only == the fav list
	} else {
		for i := range a.emotepick.emotes {
			a.favstyle.emoteVisible = append(a.favstyle.emoteVisible, i)
		}
	}
}

// toggleEmoteFav flips emote index realIdx's favourite for the active character
// and invalidates the cached view so the next refresh rebuilds it.
func (a *App) toggleEmoteFav(realIdx int) {
	a.d.Prefs.ToggleEmoteFav(a.activeCharName(), realIdx)
	a.favstyle.emoteFavRev++
}

// drawEmoteFavStar draws the favourite badge in an emote cell's top-right corner
// and reports whether THIS frame's click toggled it (so the caller skips the
// cell's emote-select). The ★ is ALWAYS shown — dim grey when not yet a
// favourite, gold when it is — so favouriting is discoverable (a hover-only star
// is invisible until you happen to hover; the whole feature then goes unnoticed).
func (a *App) drawEmoteFavStar(cell sdl.Rect, realIdx int) bool {
	if !a.d.Prefs.Load().EmoteFavStars {
		return false // favourites are opt-in (default off): no badge, no tofu on every cell
	}
	c := a.ctx
	_, fav := a.favstyle.emoteFavSet[realIdx]
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
	if !a.d.Prefs.Load().EmoteFavStars {
		return // feature off: no "★ Favs" filter button either
	}
	c := a.ctx
	on := a.d.Prefs.Load().EmoteFavOnly
	if on { // accent ring so it's clearly engaged
		c.Fill(sdl.Rect{X: btn.X - 2, Y: btn.Y - 2, W: btn.W + 4, H: btn.H + 4}, ColStar)
	}
	if c.Button(btn, "★ Favs") {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				EmoteFavOnly =

				!on

		})

		a.favstyle.emoteFavRev++ // force the visible list to rebuild for the new filter state
	}
	c.Tooltip(btn, "Show only your favourite emotes — click the ★ on an emote to add it")
}
