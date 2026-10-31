package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Emote favourites: star the emotes you use and optionally filter the grid to them. Keyed per character by the emote's
// index in its list, since labels and talking sprites duplicate within a character (Apollo has three "normal" emotes
// sharing one anim) and a name key would merge distinct emotes. See config.ToggleEmoteFav.
//
// A click rebuilds emoteFavSet (O(1) membership) and emoteVisible once; every steady-state frame is a single guard-key
// compare, reusing the backing arrays.

const emoteFavStarPx = 15 // star badge size in an emote cell's top-right corner

// refreshEmoteView rebuilds the favourite set and visible-index list for the active character only when the guard key
// changed (character, favs-only toggle, emote count, favourite edit). It is idempotent, so every consumer (both grids,
// number keys, random) can call it without depending on call order.
func (a *App) refreshEmoteView() {
	char := a.activeCharName()
	favOnly := a.d.Prefs.Load().EmoteFavOnly
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
	for _, idx := range a.d.Prefs.Load().EmoteFavsFor(char) {
		a.emoteFavSet[idx] = struct{}{}
	}

	// favBoxList is always the favourites in emote order (the floating box reads it regardless of the grid filter);
	// built here so it and the box stay allocation-free.
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

// drawEmoteFavStar draws the favourite badge in an emote cell's top-right corner and reports whether this frame's click
// toggled it (so the caller skips emote-select). The star is always shown, dim when not a favourite and gold when it
// is, so the feature is discoverable; the backing appears only when favourited or hovered. Draw it after the cell
// button; its result must win over the button's pick.
func (a *App) drawEmoteFavStar(cell sdl.Rect, realIdx int) bool {
	if !a.d.Prefs.Load().EmoteFavStars {
		return false // favourites are opt-in (default off): no badge, no tofu on every cell
	}
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
	c.IconLabel(sr, "★", col)
	if c.ClickedIn(sr) {
		a.toggleEmoteFav(realIdx)
		return true
	}
	return false
}

// drawEmoteFavToggle draws the favourites-only filter button at btn and flips the persisted pref, invalidating the
// view. Shared by both emote grids; highlighted while on.
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

		a.emoteFavRev++ // force the visible list to rebuild for the new filter state
	}
	c.Tooltip(btn, "Show only your favourite emotes — click the ★ on an emote to add it")
}
