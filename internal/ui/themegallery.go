package ui

// The preset gallery: choosing a point on the layout x style grid and creating, applying or undoing with it.
// Merging, writing and format knowledge live elsewhere. Create is the only unconditionally safe act: a fresh folder.
// The miniature is arithmetic, not a render:
// the layout's [overrides] painted in the style's [palette], with plain fills,
// because 294 live renders would be 294 textures.

import (
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// presetThumbCap is a hard bound on the baked-miniature LRU (hard rule 4):
	// a column's worth, plus the pair just left,
	// so scrubbing back up is a cache hit.
	presetThumbCap = 24
	// presetThumbBoxCap bounds the rects one miniature draws, first in declaration order;
	// smaller ones stop improving it.
	presetThumbBoxCap = 48
	// presetThumbUnit is the miniature's normalised integer coordinate space, finer than any card is wide.
	presetThumbUnit = 1000

	// editGalleryPanelW and editGalleryRowH size the panel, two rails wide like the font panel.
	editGalleryPanelW = editRailW * 2
	editGalleryRowH   = editFieldRowH
	// editGalleryListW is one of the two axis columns.
	editGalleryListW = 150
	// editGalleryVisibleRows is the ceiling for the scroll offset; the panel is height-clamped, so fewer may fit.
	editGalleryVisibleRows = 12

	// presetCreditLineCap bounds the wrapped attribution,
	// enough for the first sentences that say "zero bytes of bundled art".
	presetCreditLineCap = 4
)

// presetThumbBox is one rect in a miniature, normalised to presetThumbUnit and pre-coloured.
type presetThumbBox struct {
	x, y, w, h int32
	col        sdl.Color
}

// presetThumb is one baked miniature; a fixed array, so N cache entries aren't N long-lived slices.
type presetThumb struct {
	layout, style string
	n             int
	box           [presetThumbBoxCap]presetThumbBox
	wash          sdl.Color // the style's panel colour: the card's ground
	ink           sdl.Color // the style's text colour: the card's border
	// stamp is the cache's monotonic LRU clock at last use.
	stamp uint64
}

// presetThumbCache is the bounded LRU; linear scan, because a map plus a list would allocate on every miss.
type presetThumbCache struct {
	e     [presetThumbCap]presetThumb
	n     int
	clock uint64
}

// get returns the baked miniature for a pair, baking on a miss; the only way a thumb is produced, so the cap holds.
func (c *presetThumbCache) get(l, s *theme.Preset) *presetThumb {
	if l == nil || s == nil {
		return nil
	}
	c.clock++
	for i := 0; i < c.n; i++ {
		if c.e[i].layout == l.ID && c.e[i].style == s.ID {
			c.e[i].stamp = c.clock
			return &c.e[i]
		}
	}
	slot := c.n
	if slot >= len(c.e) {
		slot = c.oldest()
	} else {
		c.n++
	}
	c.e[slot] = presetThumb{}
	bakePresetThumb(&c.e[slot], l, s)
	c.e[slot].stamp = c.clock
	return &c.e[slot]
}

// holds reports whether a pair is resident without touching the LRU clock or baking, as get would.
func (c *presetThumbCache) holds(l, s *theme.Preset) bool {
	if l == nil || s == nil {
		return false
	}
	for i := 0; i < c.n; i++ {
		if c.e[i].layout == l.ID && c.e[i].style == s.ID {
			return true
		}
	}
	return false
}

func (c *presetThumbCache) oldest() int {
	victim := 0
	for i := 1; i < c.n; i++ {
		if c.e[i].stamp < c.e[victim].stamp {
			victim = i
		}
	}
	return victim
}

// bakePresetThumb fills one miniature directly from the fragments:
// a merge allocates a Sidecar per pair and adds nothing,
// since the layout's rows and the style's palette are disjoint.
func bakePresetThumb(t *presetThumb, l, s *theme.Preset) {
	t.layout, t.style = l.ID, s.ID
	t.wash, t.ink = presetPaletteColor(s.Side.Palette.Panel, ColPanel), presetPaletteColor(s.Side.Palette.Text, ColText)
	fill := presetPaletteColor(s.Side.Palette.PanelHi, ColPanelHi)
	accent := presetPaletteColor(s.Side.Palette.Accent, ColAccent)
	cw, ch := int32(l.DesignW), int32(l.DesignH)
	if cw <= 0 || ch <= 0 {
		return
	}
	for _, o := range l.Side.Overrides {
		if t.n >= len(t.box) {
			return
		}
		if o.Rect.W <= 0 || o.Rect.H <= 0 {
			continue // an off-design or degenerate row paints nothing in the real canvas either
		}
		col := fill
		// The stage is what a person recognises a layout by; at this size everything else is texture.
		if o.Key == "viewport" {
			col = accent
		}
		t.box[t.n] = presetThumbBox{
			x:   int32(o.Rect.X) * presetThumbUnit / cw,
			y:   int32(o.Rect.Y) * presetThumbUnit / ch,
			w:   int32(o.Rect.W) * presetThumbUnit / cw,
			h:   int32(o.Rect.H) * presetThumbUnit / ch,
			col: col,
		}
		t.n++
	}
}

func presetPaletteColor(p *theme.RGB, fallback sdl.Color) sdl.Color {
	if p == nil {
		return fallback
	}
	return sdl.Color{R: p.R, G: p.G, B: p.B, A: 255}
}

// draw paints the miniature into r with plain fills; nothing is uploaded.
func (t *presetThumb) draw(c *Ctx, r sdl.Rect) {
	c.Fill(r, t.wash)
	prev, had := c.pushClip(r)
	for i := 0; i < t.n; i++ {
		b := t.box[i]
		c.Fill(sdl.Rect{
			X: r.X + b.x*r.W/presetThumbUnit,
			Y: r.Y + b.y*r.H/presetThumbUnit,
			W: max(1, b.w*r.W/presetThumbUnit),
			H: max(1, b.h*r.H/presetThumbUnit),
		}, b.col)
	}
	c.popClip(prev, had)
	c.Border(r, t.ink)
}

// galleryCredit is the hovered style's wrapped credit, baked on change: WrapText allocates, and this draws per frame.
// Only the style axis has one; zero bundled art is a stated contract, so it is shown where a theme gets built.
type galleryCredit struct {
	// style and w are what the bake was for: a narrower panel rewraps the same text differently.
	style string
	w     int32
	n     int
	line  [presetCreditLineCap]string
	// bakes counts rewraps, the only outside evidence that wrapping happens on change and not per frame.
	bakes int
}

// lines returns the wrapped credit, a view into the fixed array, rewrapping only on a style or width change.
func (gc *galleryCredit) lines(c *Ctx, p *theme.Preset, w int32) []string {
	if p == nil || p.Credit == "" || w <= 0 {
		gc.style, gc.n = "", 0
		return nil
	}
	if gc.style == p.ID && gc.w == w {
		return gc.line[:gc.n]
	}
	gc.style, gc.w, gc.n = p.ID, w, 0
	gc.bakes++
	for _, l := range c.WrapText(p.Credit, w, presetCreditLineCap) {
		if gc.n >= len(gc.line) {
			break
		}
		gc.line[gc.n] = l
		gc.n++
	}
	return gc.line[:gc.n]
}

// themeGallery is the panel's state; it lives on the editor so it dies with the screen.
type themeGallery struct {
	lib *theme.PresetLibrary
	// layoutIdx and styleIdx are the pick the three acts operate on.
	layoutIdx, styleIdx int
	// hoverLayout and hoverStyle are what the miniature shows, re-armed each frame;
	// scrubbing must not change what a click does.
	hoverLayout, hoverStyle int
	scrollLayout            int
	scrollStyle             int
	thumbs                  presetThumbCache
	// credit is the hovered style's attribution.
	credit galleryCredit
	// undo is the one-deep way back from apply (see theme.PresetUndo).
	undo *theme.PresetUndo
}

// openThemeGallery arms the panel; the library load blocks once behind a sync.Once, on a press, and failure is a chip.
func (a *App) openThemeGallery() {
	if a.te == nil {
		return
	}
	lib, err := theme.ShippedPresets()
	if err != nil {
		a.te.note("the built-in preset library could not be read: " + err.Error())
		return
	}
	g := &themeGallery{lib: lib}
	// Start on what the theme declares, so a preset theme shows where it is.
	if sc := a.te.doc.side; sc != nil {
		g.layoutIdx = presetIndexOf(lib.Layouts(), sc.Meta.LayoutPreset)
		g.styleIdx = presetIndexOf(lib.Styles(), sc.Meta.StylePreset)
	}
	g.hoverLayout, g.hoverStyle = g.layoutIdx, g.styleIdx
	a.te.gallery = g
}

// presetIndexOf finds an id in an ordered axis, or 0, since every consumer indexes a non-empty list.
func presetIndexOf(list []*theme.Preset, id string) int {
	for i, p := range list {
		if p.ID == id {
			return i
		}
	}
	return 0
}

func (g *themeGallery) pair() theme.PresetPair {
	return theme.PresetPair{Layout: g.at(g.lib.Layouts(), g.layoutIdx), Style: g.at(g.lib.Styles(), g.styleIdx)}
}

func (g *themeGallery) hovered() theme.PresetPair {
	return theme.PresetPair{Layout: g.at(g.lib.Layouts(), g.hoverLayout), Style: g.at(g.lib.Styles(), g.hoverStyle)}
}

// at is a bounds-checked index into an axis; nil, because a shrunken library must not crash the render thread.
func (g *themeGallery) at(list []*theme.Preset, i int) *theme.Preset { return presetAt(list, i) }

// presetAt is the bounds rule, shared with the settings tab's axis dropdowns.
func presetAt(list []*theme.Preset, i int) *theme.Preset {
	if i < 0 || i >= len(list) {
		return nil
	}
	return list[i]
}

// applyGalleryPick replaces both tiers of the open theme with the pair as one undoable act.
// Both go through one MergePresets, adopted only once it exists, so a half-applied pair can't happen.
// The undo ring resets: its ops identify elements by index, and this replaces the element list.
func (a *App) applyGalleryPick() {
	g, sc := a.gallery(), a.editorSidecar()
	if g == nil || sc == nil {
		return
	}
	pair := g.pair()
	if pair.Empty() {
		a.te.note("pick a layout and a style first")
		return
	}
	snap, err := sc.SnapshotForPreset()
	if err != nil {
		a.te.note("this theme could not be snapshotted, so the preset was not applied: " + err.Error())
		return
	}
	merged, err := theme.MergePresets(sc, pair)
	if err != nil {
		a.te.note("this preset does not fit this theme: " + err.Error())
		return
	}
	sc.AdoptPresetMerge(merged)
	a.themeSidecar = sc
	g.undo = snap
	a.te.ring = themeEditRing{}
	a.te.doc.dirty = true
	a.invalidateThemeCanvases()
	a.te.note("applied " + pair.Layout.Name + " × " + pair.Style.Name + " — Undo puts it back")
}

func (a *App) undoGalleryPick() {
	g, sc := a.gallery(), a.editorSidecar()
	if g == nil || sc == nil || !g.undo.Valid() {
		return
	}
	if !sc.RestorePreset(g.undo) {
		a.te.note("there is nothing to put back")
		return
	}
	g.undo = nil
	a.te.ring = themeEditRing{}
	a.te.doc.dirty = true
	a.invalidateThemeCanvases()
	a.te.note("the preset was undone")
}

// createThemeFromGalleryPick writes a new theme through startThemeCreate; the editor may hold unsaved work,
// so the new one is named rather than applied and opened.
func (a *App) createThemeFromGalleryPick() {
	g := a.gallery()
	if g == nil {
		return
	}
	pair := g.pair()
	name := galleryThemeName(pair)
	sc, err := newThemeSidecarFrom(templatePreset, name, pair)
	if err != nil {
		a.te.note(err.Error())
		return
	}
	a.startThemeCreate(name, sc, themeCreateThenNote)
}

// galleryThemeName is a created theme's name, from both axes so one style on two layouts doesn't collide; nil-safe,
// because newThemeSidecarFrom's refusal comes after this runs.
func galleryThemeName(pair theme.PresetPair) string {
	switch {
	case pair.Layout == nil && pair.Style == nil:
		return ""
	case pair.Style == nil:
		return pair.Layout.Name
	case pair.Layout == nil:
		return pair.Style.Name
	}
	return pair.Style.Name + " — " + pair.Layout.Name
}

// gallery is the nil-safe accessor every act opens with.
func (a *App) gallery() *themeGallery {
	if a.te == nil {
		return nil
	}
	return a.te.gallery
}

func (a *App) editorSidecar() *theme.Sidecar {
	if a.te == nil || a.te.doc == nil {
		return nil
	}
	return a.te.doc.side
}

// editorGalleryPanelRect is the panel's box, the font panel's geometry.
func editorGalleryPanelRect(w, h int32) sdl.Rect {
	pw := int32(editGalleryPanelW)
	if pw > w-editRowPad*2 {
		pw = w - editRowPad*2
	}
	ph := editGalleryRowH*int32(editGalleryVisibleRows+5) + editRowPad*2
	if ph > h-editBannerH-editRowPad*2 {
		ph = h - editBannerH - editRowPad*2
	}
	return sdl.Rect{X: (w - pw) / 2, Y: editBannerH + editRowPad, W: pw, H: ph}
}

// drawThemeGallery paints the panel; not a modal, so the canvas behind keeps animating while you scrub.
func (a *App) drawThemeGallery(w, h int32) {
	g := a.gallery()
	if g == nil {
		return
	}
	c := a.ctx
	r := editorGalleryPanelRect(w, h)
	c.Fill(r, ColBackground)
	c.Border(r, ColAccent)
	c.Label(r.X+editRowPad, r.Y+4, "Gallery — "+strconv.Itoa(g.lib.Combinations())+" combinations", ColText)
	if c.Button(sdl.Rect{X: r.X + r.W - 60 - editRowPad, Y: r.Y + 2, W: 60, H: editGalleryRowH - 2}, "Close") {
		a.te.gallery = nil
		return
	}

	// Re-armed every frame and falls back to the pick, so moving off the lists shows what a click would do.
	g.hoverLayout, g.hoverStyle = g.layoutIdx, g.styleIdx

	body := sdl.Rect{X: r.X + editRowPad, Y: r.Y + editGalleryRowH + 2,
		W: r.W - editRowPad*2, H: r.H - editGalleryRowH*3 - editRowPad*2}
	prev, had := c.pushClip(body)
	a.drawGalleryAxis(g, sdl.Rect{X: body.X, Y: body.Y, W: editGalleryListW, H: body.H},
		"Layout", g.lib.Layouts(), &g.layoutIdx, &g.scrollLayout, &g.hoverLayout)
	a.drawGalleryAxis(g, sdl.Rect{X: body.X + editGalleryListW + editRowPad, Y: body.Y, W: editGalleryListW, H: body.H},
		"Style", g.lib.Styles(), &g.styleIdx, &g.scrollStyle, &g.hoverStyle)
	c.popClip(prev, had)

	a.drawGalleryPreview(g, sdl.Rect{
		X: body.X + (editGalleryListW+editRowPad)*2,
		Y: body.Y,
		W: body.W - (editGalleryListW+editRowPad)*2,
		H: body.H,
	})
	a.drawGalleryActions(g, r)
}

// drawGalleryAxis is one column of the grid, with the picked row lit.
func (a *App) drawGalleryAxis(g *themeGallery, r sdl.Rect, title string, list []*theme.Preset, pick, scroll, hover *int) {
	c := a.ctx
	c.Label(r.X, r.Y+3, title+" ("+strconv.Itoa(len(list))+")", ColAccent)
	rows := int(r.H/editGalleryRowH) - 1
	if rows > editGalleryVisibleRows {
		rows = editGalleryVisibleRows
	}
	if rows < 1 {
		return
	}
	// Follows the pick, not the pointer: a list that scrolled under a hover would move the row out from under the
	// click.
	if *pick < *scroll {
		*scroll = *pick
	}
	if *pick >= *scroll+rows {
		*scroll = *pick - rows + 1
	}
	if *scroll > len(list)-rows {
		*scroll = len(list) - rows
	}
	if *scroll < 0 {
		*scroll = 0
	}
	y := r.Y + editGalleryRowH
	for i := *scroll; i < len(list) && i < *scroll+rows; i++ {
		row := sdl.Rect{X: r.X, Y: y, W: r.W, H: editGalleryRowH - 2}
		if i == *pick {
			c.Fill(row, ColPanelHi)
		}
		if c.hovering(row) {
			*hover = i
			if i != *pick {
				c.Fill(row, ColPanel)
			}
		}
		if c.ClickedIn(row) {
			*pick = i
		}
		c.LabelClipped(row.X+3, row.Y+3, row.W-6, list[i].Name, ColText)
		y += editGalleryRowH
	}
}

// drawGalleryPreview is the miniature, both descriptions and the style's credit, last and dimmest.
func (a *App) drawGalleryPreview(g *themeGallery, r sdl.Rect) {
	c := a.ctx
	if r.W < editGalleryRowH*4 {
		return // the panel is too narrow for a picture; the lists still work
	}
	pair := g.hovered()
	if pair.Layout == nil || pair.Style == nil {
		return
	}
	thumb := g.thumbs.get(pair.Layout, pair.Style)
	if thumb == nil {
		return
	}
	// 4:3-ish, like every shipped canvas; clamped so a tall panel doesn't stretch the picture.
	tw := r.W
	th := tw * 3 / 4
	if th > r.H-editGalleryRowH*3 {
		th = r.H - editGalleryRowH*3
		tw = th * 4 / 3
	}
	thumb.draw(c, sdl.Rect{X: r.X, Y: r.Y + editGalleryRowH, W: tw, H: th})
	c.LabelClipped(r.X, r.Y+3, r.W, pair.Style.Name+" × "+pair.Layout.Name, ColText)
	y := r.Y + editGalleryRowH + th + 4
	c.LabelClipped(r.X, y, r.W, pair.Style.Description, ColTextDim)
	y += editGalleryRowH
	c.LabelClipped(r.X, y, r.W, pair.Layout.Description, ColTextDim)
	y += editGalleryRowH
	// Clipped to what is left of the column, so the lists and buttons keep working on a short window.
	for _, line := range g.credit.lines(c, pair.Style, r.W) {
		if y+editGalleryRowH > r.Y+r.H {
			break
		}
		c.LabelClipped(r.X, y, r.W, line, ColTextDim)
		y += editGalleryRowH
	}
}

// drawGalleryActions is the footer: the three acts and which of them touches the open theme.
func (a *App) drawGalleryActions(g *themeGallery, r sdl.Rect) {
	c := a.ctx
	y := r.Y + r.H - editGalleryRowH*2
	x := r.X + editRowPad
	const btnW = 130
	if c.Button(sdl.Rect{X: x, Y: y, W: btnW, H: editGalleryRowH - 2}, "Create theme") {
		a.createThemeFromGalleryPick()
	}
	x += btnW + editRowPad
	if c.Button(sdl.Rect{X: x, Y: y, W: btnW, H: editGalleryRowH - 2}, "Apply to this theme") {
		a.applyGalleryPick()
	}
	x += btnW + editRowPad
	if g.undo.Valid() {
		if c.Button(sdl.Rect{X: x, Y: y, W: btnW, H: editGalleryRowH - 2}, "Undo the preset") {
			a.undoGalleryPick()
		}
	}
	c.LabelClipped(r.X+editRowPad, y+editGalleryRowH+1, r.W-editRowPad*2,
		"Create makes a new theme and leaves this one alone. Apply replaces this theme's layout and style tiers.",
		ColTextDim)
}
