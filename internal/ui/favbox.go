package ui

import "github.com/veandco/go-sdl2/sdl"

// The floating Favourite-Emotes box (#85): a small, movable, on-top panel showing
// just the current character's STARRED emotes as clickable sprite buttons — a
// curated palette so the handful of emotes you actually use are one click away,
// without paging the full grid. Press a button to select that emote (it sends on
// your next message), exactly like the grid. OFF by default (the FavEmoteBox
// pref); opened by its rebindable hotkey (default Ctrl+A) or the Settings tick.
//
// Non-invasive, like the Extras box: the scene/chat/logs stay live underneath,
// and it shares the Extras surface's single per-frame mouse-press edge so exactly
// one box grabs a given press. Drawn only while open, so it's free when closed.
// The render loop (render.Viewport) is untouched.

const (
	favBoxCols   = int32(4)     // max emote buttons per row
	favBoxCell   = emoteBtnCell // 40×40, matching the grid's button art
	favBoxGap    = int32(6)     // spacing between buttons
	favBoxPad    = int32(8)     // inner padding
	favBoxEmptyW = int32(264)   // width when empty, so the "how to fill it" hint fits
)

// favBoxRect is the box's screen rect: sized to its favourites grid (or a hint
// when empty), clamped fully on-screen at its dragged position, else a default
// near the top-right where it's out of the way.
func (a *App) favBoxRect(w, h int32) sdl.Rect {
	n := int32(len(a.favBoxList))
	cols := n
	if cols > favBoxCols {
		cols = favBoxCols
	}
	if cols < 1 {
		cols = 1
	}
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
	return sdl.Rect{X: clampI32(x, 8, maxX), Y: clampI32(y, 8, maxY), W: bw, H: bh}
}

// drawFavEmoteBox paints the favourite-emotes box and handles its clicks. It
// rebuilds the favourites view ITSELF (the emote grid is a hideable panel — if
// it's hidden but this box is open, nothing else would refresh favBoxList).
// pressed is the Extras surface's shared press edge (title-bar drag consumes it).
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
		a.d.Prefs.SetFavEmoteBox(false)
		return
	}
	a.handleFavBoxDrag(sdl.Rect{X: r.X, Y: r.Y, W: r.W - 28, H: extrasTitleH}, w, h, pressed)

	if len(a.favBoxList) == 0 {
		c.LabelClipped(r.X+favBoxPad, r.Y+extrasTitleH+favBoxPad, r.W-2*favBoxPad,
			"No favourites yet — click the ★ on emotes.", ColTextDim)
		return
	}

	me := a.activeCharName()
	useImages := a.d.Prefs.EmoteButtonImagesEnabled()
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
	}
}

// handleFavBoxDrag moves the box by its title bar, mirroring handleExtrasDrag and
// sharing the per-frame press edge (zeroed on grab, so one press moves one box).
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
	}
}
