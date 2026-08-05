package ui

// Themed panel chip strips (#21 label 2).
//
// AO2 switches its right-column panels with buttons the THEME places: ooc_toggle
// for the chatlogs (courtroom.cpp:5197) and switch_area_music for music-vs-areas
// (courtroom.cpp:1060). AsyncAO carries panels AO2 has no rect for at all — Notes,
// and the player roster — so the themed panels draw a compact chip strip across
// the top of their design rect for the tabs AO2's own buttons cannot reach.
//
// The strip's width was a straight line of constants, and a design rect's width is
// theme DATA: aceattorney2x's music_list is 216 px and AO2's own stock default is
// 224, while Music+Areas+"Player List" want 224 px of chips plus the panel's 4 px
// margin. Both overflowed — the reported "the Player List chip sticks out past the
// right edge of its panel". Shaving the constants would only move the cliff, so
// every strip goes through fitThemedChips instead, which is TOTAL: for any panel
// width it returns a layout that fits inside the panel, or no strip at all.

import "github.com/veandco/go-sdl2/sdl"

const (
	// themedChipMinW is the narrowest chip that still says something. Ctx.Button
	// clips its label to r.W-8 (4 px of margin each side), so 24 px leaves 16 px of
	// glyphs — two or three characters at the chrome font, enough to tell three
	// chips apart. Below that a chip is an unlabelled box, and the strip drops to
	// nothing instead: the same judgement minThemedElementPx makes one tier up for
	// a themed widget scaled into a smear.
	themedChipMinW = int32(24)
	// themedChipsMax bounds one strip. The log strip is IC/OOC/Notes and the list
	// strip is Music/Areas/Player List — three each, and the fitter's returned array
	// is sized on it, so the render path never allocates to lay a strip out.
	themedChipsMax = 3
)

// themedChipSpec is one view a panel can show: the tab that selects it, its label,
// and the width that label was measured for. The tables below are package vars so
// a call site can hand the fitter a slice of one without allocating per frame.
type themedChipSpec struct {
	tab   int
	label string
	w     int32
	// toggle is the label to draw while THIS chip's own view is the one on screen and
	// the chip has therefore become the way BACK OUT (see fitThemedChips' `n <
	// len(views)` branch). "" keeps `label` in both states.
	//
	// It exists because a one-chip strip is a TOGGLE wearing a button's clothes: the
	// roster chip says "Player List", you press it, the roster appears — and the chip
	// still says "Player List", so it reads as an inert button rather than the switch
	// back to the music list that it is. Naming it for its destination is what every
	// other toggle in this client does.
	toggle string
}

// themedChip is one chip the fitter PLACED: where it goes, what it says, and what
// clicking it selects.
type themedChip struct {
	r     sdl.Rect
	label string
	tab   int
}

// themedListViews is the music panel's view order — which is also the cycle order
// a collapsed strip walks, so every view stays reachable from every other.
var themedListViews = [themedChipsMax]themedChipSpec{
	{tab: logTabMusic, label: "Music", w: themedListTabW},
	{tab: logTabAreas, label: "Areas", w: themedListTabW},
	// The roster chip is the one that is routinely drawn ALONE (themedRosterStrip), so
	// it is the one that needs a destination label for its pressed state. It swaps to
	// the music list, which is where the strip's cycle sends it. "Music List" is
	// narrower than "Player List", so themedPlayersTabW still fits it.
	{tab: logTabPlayers, label: "Player List", w: themedPlayersTabW, toggle: "Music List"},
}

// themedRosterStrip is the strip the music panel draws when the theme's own
// switch_area_music button is on screen: the ROSTER ALONE.
//
// That button IS AO2's Music/Areas switch, so a Music chip and an Areas chip
// beside it are duplicate controls for a control the theme already placed — which
// is exactly what #21's parity rule forbids inside the design canvas. The roster
// is the one view AO2 has no control for anywhere (its player_list is a separate
// rect and a written deferral, and the Extras box has no roster to move it to), so
// it is the only chip the strip still owes the user. 96 px instead of 224 is why
// the strip fits a stock 216–224 px music_list at last.
var themedRosterStrip = themedListViews[len(themedListViews)-1:]

// themedLogViews is the merged-chatlog panel's view order (a theme that stacks
// ic_chatlog and server_chatlog on one rect meant them as one toggled widget).
var themedLogViews = [themedChipsMax]themedChipSpec{
	{tab: logTabLog, label: "IC", w: themedLogTabW},
	{tab: logTabOOC, label: "OOC", w: themedLogTabW},
	{tab: logTabNotes, label: "Notes", w: themedNotesTabW},
}

// themedSwitchAreaMusic is what AO2's switch_area_music button does: Areas goes
// back to Music, and ANYTHING ELSE goes to Areas (courtroom.cpp:1060-1061 —
// ui_area_list and ui_music_list share the music_list rect, so the button just
// picks which one is up).
//
// "Anything else" includes the roster and the log-side tabs, and that is what
// makes the one-chip strip above provably escapable: A/M always leads back into
// the Music/Areas pair. Extracted from the draw site so the reachability test
// walks the SHIPPING transition instead of a copy of it.
func themedSwitchAreaMusic(view int) int {
	if view == logTabAreas {
		return logTabMusic
	}
	return logTabAreas
}

// themedViewIndex is where view sits in a panel's cycle, or 0 — the panel's own
// default — for anything else.
//
// "Anything else" is routine, not defensive: logTab is ONE field shared by both
// themed panels, so while the user is on Notes the list panel is showing Music
// (its switch's default branch) and while the user is on Players the log panel is
// showing IC (likewise). Index 0 is the truth in both cases.
func themedViewIndex(views []themedChipSpec, view int) int {
	for i := range views {
		if views[i].tab == view {
			return i
		}
	}
	return 0
}

// fitThemedChips lays `want` across the top of `body` (an already-inset themed
// panel body) and returns the chips plus how many to draw. `views` is the panel's
// full cycle, which `want` is normally all of.
//
// The array comes back BY VALUE so the render path allocates nothing to lay a
// strip out — the same idiom, for the same reason, as dockedLogTabs (torntabs.go).
//
// It is total, and it degrades in this order:
//
//  1. natural widths, when the strip fits;
//  2. otherwise an equal share of the panel each — a segmented strip — while that
//     share is still at least themedChipMinW;
//  3. otherwise ONE chip labelled with the view you are on, which advances to the
//     next one, so a panel too narrow for a strip still reaches every view;
//  4. and no strip at all when even that will not fit, or when the panel is too
//     short to give the strip its height and still leave a body. The caller then
//     hands the whole panel to the list, which beats drawing a mystery box.
//
// A chip for the CURRENT view selects the NEXT one whenever the strip is showing
// fewer chips than the panel has views — cases 3 above, and the roster-only strip,
// where it is the way back out. With a chip per view (the ordinary case) it stays
// the plain no-op re-select it looks like.
func fitThemedChips(body sdl.Rect, want, views []themedChipSpec, view int) ([themedChipsMax]themedChip, int32) {
	var out [themedChipsMax]themedChip
	n := len(want)
	if n > themedChipsMax {
		n = themedChipsMax
	}
	// themedStripH is the strip PLUS its gap to the list; minThemedElementPx is the
	// least body worth having under it. A panel shorter than the two together has
	// no room for a strip, and the caller's `inner` would go negative.
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

// themedListStrip is which views the music panel's chip strip carries: the roster
// alone when the theme placed AO2's own switch_area_music button (see
// themedRosterStrip), all three when it did not, because then the chips are the
// only route to any of them.
//
// Shared by the draw site and the geometry census so the two cannot drift — a
// census that transcribed this rule would go on reporting a strip the courtroom
// had stopped drawing. A map probe and a slice of a package array: no allocation.
func themedListStrip(lay *themeLayoutCache) []themedChipSpec {
	if _, ok := lay.rect("switch_area_music"); ok {
		return themedRosterStrip
	}
	return themedListViews[:]
}

// themedStripBody is the panel body left for the list UNDER a strip of n chips.
// n == 0 (a panel too small for even one chip) hands the whole body back rather
// than reserving room for a strip that did not draw — and keeps `inner.H` from
// going negative on a panel barely taller than the strip.
func themedStripBody(body sdl.Rect, n int32) sdl.Rect {
	if n <= 0 {
		return body
	}
	body.Y += themedStripH
	body.H -= themedStripH
	return body
}

// fitThemedCycleChip is degrade step 3: one chip, named for the view on screen,
// clicking it advances to the next. Never wider than the panel.
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
