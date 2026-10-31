package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The floating Favourite-Emotes box: a small movable on-top panel of the current character's starred emotes as sprite
// buttons; pressing one selects it like the grid. Off by default (FavEmoteBox pref), opened by its hotkey or Settings.
// It shares the Extras surface's per-frame press edge so exactly one box grabs a given press.

const (
	favBoxCols   = int32(4)     // max emote buttons per row
	favBoxCell   = emoteBtnCell // 40×40, matching the grid's button art
	favBoxGap    = int32(6)     // spacing between buttons
	favBoxPad    = int32(8)     // inner padding
	favBoxEmptyW = int32(264)   // width when empty, so the "how to fill it" hint fits
)

// favBoxRect is the box's screen rect: sized to its grid (or a hint when empty), clamped on-screen, default top-right.
func (a *App) favBoxRect(w, h int32) sdl.Rect {
	n := int32(len(a.favBoxList))
	cols := n
	cols = value.Clamp(cols, 1, favBoxCols)
	rows := (n + cols - 1) / cols
	if rows < 1 {
		rows = 1
	}
	var bw, bh int32
	if n == 0 {
		bw = favBoxEmptyW
		bh = extrasTitleH + favBoxPad*2 + 22 // one hint line
	} else {
		bw = favBoxPad*2 + cols*favBoxCell + (cols-1)*favBoxGap
		bh = extrasTitleH + favBoxPad*2 + rows*favBoxCell + (rows-1)*favBoxGap
	}
	if bw > w-16 {
		bw = w - 16
	}
	if bh > h-16 {
		bh = h - 16
	}
	x, y := a.favBoxX, a.favBoxY
	if !a.favBoxPlaced {
		x, y = w-bw-24, 96 // default: tucked top-right, clear of the viewport centre
	}
	maxX, maxY := w-bw-8, h-bh-8
	if maxX < 8 {
		maxX = 8
	}
	if maxY < 8 {
		maxY = 8
	}
	return sdl.Rect{X: value.Clamp(x, 8, maxX), Y: value.Clamp(y, 8, maxY), W: bw, H: bh}
}

// drawFavEmoteBox paints the box and handles its clicks. It rebuilds the favourites view itself, since the emote grid
// is hideable and nothing else would refresh favBoxList. pressed is the Extras surface's shared press edge.
func (a *App) drawFavEmoteBox(w, h int32, pressed *bool) {
	c := a.ctx
	a.refreshEmoteView() // owns its data; never assume the grid ran this frame
	r := a.favBoxRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColStar)

	// Title bar / drag handle + close.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: extrasTitleH}, ColPanelHi)
	c.Label(r.X+8, r.Y+6, "★ Favourite Emotes", ColStar)
	if c.Button(sdl.Rect{X: r.X + r.W - 24, Y: r.Y + 4, W: 18, H: extrasTitleH - 8}, "x") {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FavEmoteBox =

				false

		})

		return
	}
	a.handleFavBoxDrag(sdl.Rect{X: r.X, Y: r.Y, W: r.W - 28, H: extrasTitleH}, w, h, pressed)

	if len(a.favBoxList) == 0 {
		c.LabelClipped(r.X+favBoxPad, r.Y+extrasTitleH+favBoxPad, r.W-2*favBoxPad,
			"No favourites yet — click the ★ on emotes.", ColTextDim)
		return
	}

	me := a.activeCharName()
	useImages := a.d.Prefs.Load().EmoteButtonImages
	// Emote-name hover tooltip config, read once (RLock) outside the per-cell loop.
	hoverNamesOn := a.d.Prefs.Load().EmoteHoverNamesOn
	hoverNamesDelay := a.d.Prefs.EmoteHoverNamesDelay()
	cols := int32(len(a.favBoxList))
	if cols > favBoxCols {
		cols = favBoxCols
	}
	gx, gy := r.X+favBoxPad, r.Y+extrasTitleH+favBoxPad
	for k, i := range a.favBoxList {
		col := int32(k) % cols
		row := int32(k) / cols
		btn := sdl.Rect{X: gx + col*(favBoxCell+favBoxGap), Y: gy + row*(favBoxCell+favBoxGap), W: favBoxCell, H: favBoxCell}
		selected := i == a.emoteIdx
		if selected {
			c.Fill(sdl.Rect{X: btn.X - 2, Y: btn.Y - 2, W: btn.W + 4, H: btn.H + 4}, ColAccent)
		}
		label := a.emotes[i].Comment
		if label == "" {
			label = a.emotes[i].Anim
		}
		var picked bool
		if useImages {
			picked = a.drawEmoteImageButton(btn, me, i, selected, label)
		} else {
			picked = c.Button(btn, label)
		}
		if picked {
			a.selectEmote(i) // same as a grid click: warms art + focuses IC
		}
		if c.HoverPreview("favemote:"+a.emotes[i].Anim, btn) {
			a.previewEmote(me, &a.emotes[i])
		}
		// Emote-name tooltip as in the grid; gated on hovering(btn) so the id concat runs only for the hovered cell.
		if hoverNamesOn && c.hovering(btn) {
			c.TooltipAfterDelay("favemotename:"+a.emotes[i].Anim, btn, label, hoverNamesDelay)
		}
	}
}

// handleFavBoxDrag moves the box by its title bar, like handleExtrasDrag; the shared press edge is zeroed on grab.
func (a *App) handleFavBoxDrag(handle sdl.Rect, w, h int32, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, handle) {
		*pressed = false
		r := a.favBoxRect(w, h)
		a.favBoxDragging = true
		a.favBoxGrabDX, a.favBoxGrabDY = c.mouseX-r.X, c.mouseY-r.Y
	}
	if !c.mouseDown {
		a.favBoxDragging = false
	}
	if a.favBoxDragging {
		a.favBoxX, a.favBoxY = c.mouseX-a.favBoxGrabDX, c.mouseY-a.favBoxGrabDY
		a.favBoxPlaced = true
		if !a.siblingMagnetOff() && len(a.panelMagnetRects) > 0 { // M3: piece-to-piece magnet
			r := a.favBoxRect(w, h)
			a.favBoxX, a.favBoxY, a.alignGuides = snapRectToSiblings(r, a.panelMagnetRects, w, h, a.alignGuides)
		}
	}
}
