package ui

// THE PRESET GALLERY (v1.90.0 W9) — docs/wip/THEME-EDITOR-DESIGN.md §3.1's panel
// list, which has named this surface since W7 ("OPEN to the panels design §3.1
// still owes (preset gallery, …)", editorPanelUp).
//
// ONE concern: choosing a point on the 21 x 14 grid and doing something with it.
// It owns no merging (internal/theme's presetmerge.go), no writing (themepack's
// CreateFromSidecar) and no format knowledge; what is here is the pick, the
// miniature and the three acts.
//
// THE THREE ACTS, and why they are three and not one:
//
//   CREATE  — build a NEW theme from the pair. The primary act, and the only one
//             that is unconditionally safe: it writes a fresh folder whose sidecar
//             has never held anything else, so there is no previous tier to leave
//             a trace of. It is also the flagship's actual use — "give me a theme".
//   APPLY   — replace a tier of the theme already open in the editor. One
//             snapshot, one undo (theme.PresetUndo), and it is how you swap the
//             style of something you are already working on.
//   UNDO    — put the last APPLY back, exactly. Not a ring op: see PresetUndo's
//             own comment for why a bulk structural replace cannot be one.
//
// THE MINIATURE IS ARITHMETIC, NOT A RENDER. A live render of 294 combinations
// would be 294 offscreen frames and 294 textures against a texture budget the
// whole feature is already careful with (risk R2). What the card actually has to
// answer is "where do things sit, and what colour is it", and both come straight
// out of the merged model: the layout's [overrides] normalised to its own design
// canvas, painted in the style's [palette]. It costs one bounded bake per pair,
// cached, and it draws with plain Fill calls that upload nothing.

import (
	"strconv"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// ---------------------------------------------------------------------------
// Named caps and metrics (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// presetThumbCap bounds the baked-miniature cache. The panel shows ONE
	// miniature at a time and hover-scrub walks a column, so the working set is a
	// column's worth; 24 holds the longer axis (21 layouts) plus the pair the
	// pointer left behind, which is what makes scrubbing back up a cache hit.
	//
	// It is a HARD bound with an LRU behind it (hard rule 4), not a hint: a cache
	// keyed on 294 combinations that grew on demand would be 294 entries after one
	// idle minute of scrubbing.
	presetThumbCap = 24
	// presetThumbBoxCap bounds how many slot rects one miniature draws. A layout
	// declares 59-60 [overrides] rows and the card is ~200 px wide, so past this
	// the boxes are smaller than their own borders and the picture stops improving.
	// The bake takes the FIRST presetThumbBoxCap in the layout's own declaration
	// order, which is stage-first in every shipped fragment.
	presetThumbBoxCap = 48
	// presetThumbUnit is the miniature's normalised coordinate space. Integer, so
	// the bake has no floats in it and the draw is one multiply and one divide per
	// edge; 1000 is finer than any card is wide.
	presetThumbUnit = 1000

	// editGalleryPanelW / editGalleryRowH size the panel. Two rails wide, exactly
	// like the font panel, so the two do not need two answers to "how wide is a
	// panel in this editor".
	editGalleryPanelW = editRailW * 2
	editGalleryRowH   = editFieldRowH
	// editGalleryListW is one of the two axis columns.
	editGalleryListW = 150
	// editGalleryVisibleRows bounds how many axis rows are drawn at once. The
	// panel is height-clamped, so this is the ceiling the scroll offset works
	// against rather than a promise about what fits.
	editGalleryVisibleRows = 12

	// presetCreditLineCap bounds the wrapped attribution. The shipped credits run
	// 130-380 runes and the preview column is ~250 px, so four lines carries the
	// short ones whole and the first two sentences of the longest — which is where
	// every one of them says "zero bytes of bundled art". Past four the panel would
	// be a licence page with a picture on it.
	presetCreditLineCap = 4
)

// presetThumbBox is one slot rect in a miniature: normalised into
// [0, presetThumbUnit) and pre-coloured. No strings, no ids — the card never
// labels a box, so carrying the key would be carrying it for nobody.
type presetThumbBox struct {
	x, y, w, h int32
	col        sdl.Color
}

// presetThumb is one baked miniature. A FIXED ARRAY for the same reason the baked
// element list is one: it is the thing the cache holds N of, and a slice per entry
// is N allocations that outlive the frame that made them.
type presetThumb struct {
	layout, style string
	n             int
	box           [presetThumbBoxCap]presetThumbBox
	wash          sdl.Color // the style's panel colour: the card's ground
	ink           sdl.Color // the style's text colour: the card's border
	// stamp is the LRU clock at last use. Monotonic on the cache, so it cannot
	// wrap into a false "most recent" inside one session.
	stamp uint64
}

// presetThumbCache is the bounded LRU. A fixed array and a linear scan: at
// presetThumbCap entries a map plus a list would allocate on every miss to save
// twenty-four comparisons on a path that runs once per pointer move.
type presetThumbCache struct {
	e     [presetThumbCap]presetThumb
	n     int
	clock uint64
}

// get returns the baked miniature for a pair, baking and interning it on a miss.
// It is the ONLY way a thumb is produced, which is what makes the cap real —
// TestPresetThumbCacheIsBounded drives it with every one of the 294 combinations.
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

// holds reports whether a pair is resident WITHOUT touching the LRU clock. It is
// a test seam and it is here rather than in the test file for the reason every
// other seam in this package is: reading c.e/c.n from outside would be a second
// reader of the cache's internals, and the first change to the layout would leave
// the test asserting over a shape nothing else has.
//
// It must not use get(): asking through the front door would BAKE the miss it was
// sent to detect, and the eviction gate would then measure its own probe.
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

// oldest is the LRU victim. Linear over a fixed array; see the type's comment.
func (c *presetThumbCache) oldest() int {
	victim := 0
	for i := 1; i < c.n; i++ {
		if c.e[i].stamp < c.e[victim].stamp {
			victim = i
		}
	}
	return victim
}

// bakePresetThumb fills one miniature from the two fragments.
//
// IT READS THE FRAGMENTS DIRECTLY rather than merging them, and that is the one
// place in this wave where not going through the merge is right: a merge allocates
// a whole Sidecar plus a document per pair, and the miniature needs two things the
// merge cannot add to — the layout's rows and the style's palette are disjoint by
// the very rule that makes the merge a concatenation. Pinned by
// TestTheThumbnailShowsTheLayoutItIsFor.
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
		// The STAGE is the one box worth telling apart at this size: it is what a
		// person recognises a layout by, and at 200 px wide everything else is texture.
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

// presetPaletteColor resolves an optional palette role to a drawable colour.
func presetPaletteColor(p *theme.RGB, fallback sdl.Color) sdl.Color {
	if p == nil {
		return fallback
	}
	return sdl.Color{R: p.R, G: p.G, B: p.B, A: 255}
}

// draw paints the miniature into r. Plain fills: nothing is uploaded, nothing is
// cached in the texture store, and the whole card costs presetThumbBoxCap
// rectangles at worst.
func (t *presetThumb) draw(c *Ctx, r sdl.Rect) {
	c.Fill(r, t.wash)
	prev, had := c.pushClip(r)
	for i := 0; i < t.n; i++ {
		b := t.box[i]
		c.Fill(sdl.Rect{
			X: r.X + b.x*r.W/presetThumbUnit,
			Y: r.Y + b.y*r.H/presetThumbUnit,
			W: maxInt32(1, b.w*r.W/presetThumbUnit),
			H: maxInt32(1, b.h*r.H/presetThumbUnit),
		}, b.col)
	}
	c.popClip(prev, had)
	c.Border(r, t.ink)
}

// maxInt32 keeps a rounded-away box visible as a hairline. A slot that rounds to
// zero pixels at card size is still a slot the layout has, and a miniature that
// silently drops the narrow ones misrepresents the arrangement it exists to show.
func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// The attribution line
// ---------------------------------------------------------------------------

// galleryCredit is the hovered style's `[preset] credit`, wrapped, baked on
// change.
//
// IT EXISTS BECAUSE OF THE ALLOC GATE, not because wrapping is expensive.
// Ctx.WrapText allocates a slice and its lines, and the panel draws inside the
// editor frame that editorChromeAllocBudget bounds — so wrapping a 380-rune
// attribution every frame is the exact per-frame-string-in-a-loop regression that
// budget exists to catch. This is the bakeToggleLabels pattern: the work happens
// when the INPUT changes (a different style under the pointer, or a resized
// panel), and every other frame reads a fixed array.
//
// FIXED ARRAY, bounded by presetCreditLineCap (hard rule 4), for the same reason
// presetThumb holds one: it is per-panel state that outlives the frame that filled it.
//
// WHY THE CREDIT IS ON SCREEN AT ALL. §6.5 clause 7 is a CONTRACT — "zero bytes of
// bundled art; every [preset] credit line states it" — and a contract nobody can
// read is a comment. TestEveryShippedPresetShipsZeroBytesOfArt proves the corpus
// holds it; this is where the person who is about to build a theme out of one gets
// told. Only the STYLE axis has one: a layout fragment is rects, and rects have no
// provenance to state (TestOnlyTheStyleAxisCarriesACredit pins both halves).
type galleryCredit struct {
	// style / w are what the bake was for. Both, because a narrower panel rewraps
	// the same text differently and a stale wrap overflows the column it was cut to.
	style string
	w     int32
	n     int
	line  [presetCreditLineCap]string
	// bakes counts rewraps. A TEST SEAM, and it is here rather than in the test file
	// for the reason holds() is: the alloc claim is "this wraps on change, not per
	// frame", and nothing observable from outside distinguishes those two.
	bakes int
}

// lines returns the wrapped credit, rewrapping only when the style or the width
// changed. The returned slice is a view into the fixed array — no allocation on
// the steady-state frame.
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

// ---------------------------------------------------------------------------
// The panel's state
// ---------------------------------------------------------------------------

// themeGallery is the panel's whole state. It lives on the EDITOR, not on App,
// so it dies with the screen — the closed-editor cost is one nil pointer, which
// is the same shape pickCache and the font rail already have.
type themeGallery struct {
	lib *theme.PresetLibrary
	// layoutIdx / styleIdx are the PICK: what the three acts operate on.
	layoutIdx, styleIdx int
	// hoverLayout / hoverStyle are the SCRUB: what the miniature shows this frame,
	// reset every frame and re-armed by the row under the pointer. Separate from
	// the pick because scrubbing must not change what a click would do — a preview
	// that commits by hovering is the least reversible affordance there is.
	hoverLayout, hoverStyle int
	scrollLayout            int
	scrollStyle             int
	thumbs                  presetThumbCache
	// credit is the hovered style's attribution, wrapped on change (galleryCredit).
	credit galleryCredit
	// undo is the one-deep way back from APPLY. See theme.PresetUndo.
	undo *theme.PresetUndo
	// create is the in-flight folder write. The pointer IS the single-flight flag,
	// exactly as themeEditor.save and .export are.
	create *galleryCreateJob
}

// galleryCreateJob is one "make me a theme" write, off the render thread.
type galleryCreateJob struct {
	name string
	done chan galleryCreateResult
}

type galleryCreateResult struct {
	manifest *themepack.Manifest
	err      error
}

// openThemeGallery arms the panel, loading the library on first use.
//
// THE LIBRARY LOAD IS THE ONE BLOCKING THING HERE and it is 35 INI parses behind a
// sync.Once, on a button press rather than on a frame — the same class of cold work
// the font rail's directory walk already does at its own open. A failure is a chip,
// never a refusal to open: an empty gallery that says why beats a button that does
// nothing.
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
	// Start on whatever the theme already declares, so opening the gallery on a
	// preset theme shows you where you are rather than the top of the list.
	if sc := a.te.doc.side; sc != nil {
		g.layoutIdx = presetIndexOf(lib.Layouts(), sc.Meta.LayoutPreset)
		g.styleIdx = presetIndexOf(lib.Styles(), sc.Meta.StylePreset)
	}
	g.hoverLayout, g.hoverStyle = g.layoutIdx, g.styleIdx
	a.te.gallery = g
}

// presetIndexOf finds an id in an ordered axis, or 0. Zero rather than -1: every
// consumer here is an index into a non-empty list, and "the first one" is a
// better answer than a branch at each of them.
func presetIndexOf(list []*theme.Preset, id string) int {
	for i, p := range list {
		if p.ID == id {
			return i
		}
	}
	return 0
}

// pair is the currently PICKED combination.
func (g *themeGallery) pair() theme.PresetPair {
	return theme.PresetPair{Layout: g.at(g.lib.Layouts(), g.layoutIdx), Style: g.at(g.lib.Styles(), g.styleIdx)}
}

// hovered is the combination the miniature shows.
func (g *themeGallery) hovered() theme.PresetPair {
	return theme.PresetPair{Layout: g.at(g.lib.Layouts(), g.hoverLayout), Style: g.at(g.lib.Styles(), g.hoverStyle)}
}

// at is a bounds-checked index into an axis. Nil rather than a panic: the indices
// are UI state and a library that shrank under one (a future user-preset axis) is
// a card that does not draw, not a crash on the render thread.
func (g *themeGallery) at(list []*theme.Preset, i int) *theme.Preset {
	if i < 0 || i >= len(list) {
		return nil
	}
	return list[i]
}

// ---------------------------------------------------------------------------
// The three acts
// ---------------------------------------------------------------------------

// applyGalleryPick replaces both tiers of the OPEN theme with the picked pair, as
// one undoable act.
//
// THE SNAPSHOT IS TAKEN FIRST AND THE APPLY IS ALL-OR-NOTHING. ApplyPresetTier
// refuses before it moves a field, so a layout that lands and a style that does
// not leaves a model that is half of two themes — hence both tiers go through one
// MergePresets, whose result is only adopted once it exists.
//
// THE UNDO RING IS RESET, deliberately. Its ops identify an element BY INDEX
// (theme.Element's own doc comment), and this replaces the element list wholesale,
// so every op already in the ring points at a row that is no longer the row it
// meant — the same reason the ring is reset on a theme change.
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

// undoGalleryPick puts the last apply back, exactly.
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

// createThemeFromGalleryPick writes a NEW theme from the picked pair.
//
// The write is off the render thread and lands through pollThemeGalleryCreate, the
// same job/poll/land shape copyThemeForEditing uses — and for the same reason: a
// result that arrives between frames must not touch UI state from another
// goroutine.
func (a *App) createThemeFromGalleryPick() {
	g := a.gallery()
	if g == nil {
		return
	}
	if g.create != nil {
		a.te.note("a theme is already being created — let it finish first")
		return
	}
	pair := g.pair()
	if pair.Empty() {
		a.te.note("pick a layout and a style first")
		return
	}
	cat := a.themeCatalogSnapshot()
	if cat == nil || cat.WriteRoot == "" {
		a.requestThemeCatalog(themeScanExplicit)
		a.te.note("this install has nowhere to write themes yet. Set a theme folder in Settings ▸ Theme.")
		return
	}
	name := galleryThemeName(pair)
	sc, err := theme.NewThemeFromPresets(name, pair)
	if err != nil {
		a.te.note("that combination could not be built: " + err.Error())
		return
	}
	root := cat.WriteRoot
	job := &galleryCreateJob{name: name, done: make(chan galleryCreateResult, 1)}
	g.create = job
	go func() {
		m, err := themepack.CreateFromSidecar(root, name, sc)
		job.done <- galleryCreateResult{manifest: m, err: err}
	}()
	a.te.note("creating " + name + "…")
}

// galleryThemeName is what a created theme is called. Both axes, because a name
// that said only "Vaporwave" would collide with itself the moment the same style
// is built on a second layout — and themepack's collision suffix would then be
// the only thing telling them apart.
func galleryThemeName(pair theme.PresetPair) string {
	switch {
	case pair.Style == nil:
		return pair.Layout.Name
	case pair.Layout == nil:
		return pair.Style.Name
	}
	return pair.Style.Name + " — " + pair.Layout.Name
}

// pollThemeGalleryCreate lands a finished write. Called once per frame from the
// editor's draw, beside the save/font/export polls.
func (a *App) pollThemeGalleryCreate() {
	g := a.gallery()
	if g == nil || g.create == nil {
		return
	}
	select {
	case res := <-g.create.done:
		name := g.create.name
		g.create = nil
		if res.err != nil {
			a.te.note("could not create " + name + ": " + res.err.Error())
			return
		}
		a.requestThemeCatalog(themeScanExplicit)
		a.te.note("created " + res.manifest.Root + " — it is in Settings ▸ Theme")
	default:
	}
}

// gallery is the nil-safe accessor every act above opens with. One spelling of
// "is the panel there", so a future act cannot invent a second.
func (a *App) gallery() *themeGallery {
	if a.te == nil {
		return nil
	}
	return a.te.gallery
}

// editorSidecar is the model the editor is editing, or nil.
func (a *App) editorSidecar() *theme.Sidecar {
	if a.te == nil || a.te.doc == nil {
		return nil
	}
	return a.te.doc.side
}

// ---------------------------------------------------------------------------
// Drawing
// ---------------------------------------------------------------------------

// editorGalleryPanelRect is the panel's box — the font panel's geometry, so the
// editor has one answer to "where does a panel go".
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

// drawThemeGallery paints the panel. A PANEL, not a modal (design §3.1): the
// canvas behind it keeps drawing and the theme keeps animating while you scrub.
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

	// The scrub is re-armed from scratch every frame and falls back to the pick, so
	// moving the pointer OFF the lists shows what a click would actually do.
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

// drawGalleryAxis is one column of the grid: a heading and a scrolling list of
// fragment names, with the picked one lit.
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
	// The scroll follows the PICK rather than the pointer: a list that scrolled
	// under a hover would move the row out from under the click that was aiming at it.
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

// drawGalleryPreview is the miniature plus the hovered fragment's own words: what
// the pair is for (both descriptions) and where its art came from (the style's
// credit). The credit is LAST and dimmest because it is a statement of provenance
// rather than a selling point — but it is on the same surface as the button that
// builds a theme out of it, which is the whole of §6.5 clause 7's point.
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
	// 4:3-ish, which is every shipped design canvas' neighbourhood, and clamped so
	// a tall panel does not stretch the picture into something the theme is not.
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
	// The attribution, clipped to what is left of the column rather than pushed off
	// the panel: the lists and the buttons keep working on a short window, and the
	// wrap is baked so a resize costs one rewrap and not one per frame.
	for _, line := range g.credit.lines(c, pair.Style, r.W) {
		if y+editGalleryRowH > r.Y+r.H {
			break
		}
		c.LabelClipped(r.X, y, r.W, line, ColTextDim)
		y += editGalleryRowH
	}
}

// drawGalleryActions is the footer: the three acts, and the one line that says
// which of them touches the theme that is open.
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
