package ui

// Chip strips across the top of themed panels, for the tabs AO2's own buttons cannot reach:
// ooc_toggle (courtroom.cpp:5197) and switch_area_music (courtroom.cpp:1060).
// A design rect's width is theme data, so every strip goes through fitThemedChips.

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

const (
	// themedChipMinW is the narrowest chip that still says something; Ctx.Button clips its label to r.W-8.
	themedChipMinW = int32(24)
	// themedChipsMax bounds one strip and sizes the fitter's returned array, so laying out a strip does not allocate.
	themedChipsMax = 3
)

// themedChipSpec is one view a panel can show; the tables are package vars so slicing one does not allocate.
type themedChipSpec struct {
	tab   int
	label string
	w     int32
	// toggle is the label while this chip's view is on screen and the chip is the way back out; "" keeps label.
	toggle string
}

// themedChip is one placed chip: where it goes, what it says, and what clicking it selects.
type themedChip struct {
	r     sdl.Rect
	label string
	tab   int
}

// themedListViews is the music panel's view order, also the cycle a collapsed strip walks.
var themedListViews = [themedChipsMax]themedChipSpec{
	{tab: logTabMusic, label: "Music", w: themedListTabW},
	{tab: logTabAreas, label: "Areas", w: themedListTabW},
	// Routinely drawn alone (themedRosterStrip), so it needs a label for its pressed state; it swaps to the music list.
	{tab: logTabPlayers, label: "Player List", w: themedPlayersTabW, toggle: "Music List"},
}

// themedRosterStrip is the roster alone, drawn when the theme's switch_area_music button is on screen:
// that button is AO2's Music/Areas switch, so chips for those would duplicate it.
var themedRosterStrip = themedListViews[len(themedListViews)-1:]

// themedLogViews is the merged-chatlog panel's view order, for a theme stacking ic_chatlog and server_chatlog.
var themedLogViews = [themedChipsMax]themedChipSpec{
	{tab: logTabLog, label: "IC", w: themedLogTabW},
	{tab: logTabOOC, label: "OOC", w: themedLogTabW},
	{tab: logTabNotes, label: "Notes", w: themedNotesTabW},
}

// themedSwitchAreaMusic is AO2's switch_area_music: Areas to Music, anything else to Areas (courtroom.cpp:1060-1061).
func themedSwitchAreaMusic(view int) int {
	if view == logTabAreas {
		return logTabMusic
	}
	return logTabAreas
}

// themedViewIndex is where view sits in a panel's cycle, or 0, the panel's default, for anything else.
// logTab is shared by both themed panels, so a view from the other panel is routine here.
func themedViewIndex(views []themedChipSpec, view int) int {
	for i := range views {
		if views[i].tab == view {
			return i
		}
	}
	return 0
}

// fitThemedChips lays want across the top of body and returns the chips by value, plus how many to draw.
// It degrades from natural widths, to equal shares down to themedChipMinW, to one chip that cycles the views, to none.
// With fewer chips than views, the chip for the current view selects the next one, so it is the way back out.
func fitThemedChips(body sdl.Rect, want, views []themedChipSpec, view int) ([themedChipsMax]themedChip, int32) {
	var out [themedChipsMax]themedChip
	n := len(want)
	if n > themedChipsMax {
		n = themedChipsMax
	}
	// A panel shorter than the strip plus minThemedElementPx has no room, and the caller's inner would go negative.
	if n == 0 || len(views) == 0 || body.W < themedChipMinW || body.H < themedStripH+minThemedElementPx {
		return out, 0
	}
	natural := themedChipGap * int32(n-1)
	for i := 0; i < n; i++ {
		natural += want[i].w
	}
	share := int32(0) // 0 = each chip keeps its own measured width
	if natural > body.W {
		share = (body.W - themedChipGap*int32(n-1)) / int32(n)
		if share < themedChipMinW {
			return fitThemedCycleChip(body, views, view)
		}
	}
	cur := themedViewIndex(views, view)
	x := body.X
	for i := 0; i < n; i++ {
		w := want[i].w
		if share > 0 {
			w = share
		}
		tab, label := want[i].tab, want[i].label
		if n < len(views) && tab == views[cur].tab {
			tab = views[(cur+1)%len(views)].tab
			if want[i].toggle != "" {
				label = want[i].toggle // pressed: name it for where it goes, not where you are
			}
		}
		out[i] = themedChip{
			r:     sdl.Rect{X: x, Y: body.Y, W: w, H: themedChipH},
			label: label,
			tab:   tab,
		}
		x += w + themedChipGap
	}
	return out, int32(n)
}

// themedMusicOnlyStrip is Music + Areas, for a theme whose player_list rect gives the roster its own home.
var themedMusicOnlyStrip = themedListViews[:2]

// themedListStrip is which views the music panel's strip carries, dropping any the theme already gives a control:
// switch_area_music drops Music and Areas, player_list drops the roster; with both, the list gets the whole body.
func themedListStrip(lay *themeLayoutCache) []themedChipSpec {
	_, ownRoster := lay.rect("player_list")
	_, hasSwitch := lay.rect("switch_area_music")
	switch {
	case hasSwitch && ownRoster:
		return nil
	case hasSwitch:
		return themedRosterStrip
	case ownRoster:
		return themedMusicOnlyStrip
	}
	return themedListViews[:]
}

// themedStripBody is the panel body left under a strip of n chips; n == 0 hands back the whole body.
func themedStripBody(body sdl.Rect, n int32) sdl.Rect {
	if n <= 0 {
		return body
	}
	body.Y += themedStripH
	body.H -= themedStripH
	return body
}

// fitThemedCycleChip places one chip named for the view on screen; clicking advances to the next.
func fitThemedCycleChip(body sdl.Rect, views []themedChipSpec, view int) ([themedChipsMax]themedChip, int32) {
	var out [themedChipsMax]themedChip
	cur := themedViewIndex(views, view)
	w := views[cur].w
	if w > body.W {
		w = body.W
	}
	if w < themedChipMinW {
		return out, 0 // unreachable through fitThemedChips' own guard; kept total
	}
	out[0] = themedChip{
		r:     sdl.Rect{X: body.X, Y: body.Y, W: w, H: themedChipH},
		label: views[cur].label,
		tab:   views[(cur+1)%len(views)].tab,
	}
	return out, 1
}
