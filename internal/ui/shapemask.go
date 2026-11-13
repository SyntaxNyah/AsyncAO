package ui

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Chrome shape presets: chrome.go recolours the kit, this reshapes it. Hit-testing stays the same rectangle everywhere.
// White-RGB, straight-alpha masks (fill and stroke, per shape and tier) are uploaded once into the eviction-exempt
// pinned tier under shape:// keys, then drawn 9-slice and tinted per draw, so colour and translucency survive at zero
// per-frame allocation.
// Sharp is the default: with it, or before masks are resident, every shaped draw falls through to plain Fill and
// Border. It has no mask.
const (
	shapeSharp   = "sharp"   // flat rect — the byte-identical default (no mask)
	shapeRounded = "rounded" // rounded-rectangle corners (radius by tier)
	shapePillKey = "pill"    // full-radius pill: corner = min(w,h)/2 at draw time
)

// shapeRadiusTiers is the number of "rounded" corner-radius classes, a named cap on the mask cache (hard rule 4). It
// must match config.shapeRadiusTiers.
const shapeRadiusTiers = 4

// shapeTierRadii maps each tier to its corner radius in device px; even the largest fits a stock button without the
// corners meeting. Pill ignores it.
var shapeTierRadii = [shapeRadiusTiers]int32{4, 8, 12, 16}

// shapeMaskCenter is the solid center span (px) the 9-slice stretches; tiny, to keep each pinned mask small (side =
// 2*radius + center).
const shapeMaskCenter = int32(4)

// shapeStrokePx is the border mask's ring thickness, a hair over the flat Border's 1px so it reads on a curve. Device
// px: the Copies stretch with the edges.
const shapeStrokePx = int32(2)

// shapeMaskAA is the band (px) at the rounded edge over which alpha falls from 255 to 0.
const shapeMaskAA = float64(1.2)

// shapeLabelInset is the extra padding (px, each side) a shaped button's label gets so a glyph never tucks under a
// corner.
const shapeLabelInset = int32(3)

// shapeHealPeriod paces re-rastering the masks after a T1 eviction dropped the pinned pages.
const shapeHealPeriod = 2 * time.Second

// shapeTexKey builds the pinned-tier key shape://<kind>/<role>/<tier>; the scheme can't collide with asset URLs or
// theme:// keys. Called only on build, heal and the per-frame refresh, never per widget.
func shapeTexKey(kind, role string, tier int) string {
	return "shape://" + kind + "/" + role + "/" + strconv.Itoa(tier)
}

// shapeMaskKinds is the shapes that have masks. It bounds the cache at 16 pinned masks; only the active shape's are
// built.
var shapeMaskKinds = []string{shapeRounded, shapePillKey}

// rasterFillMask rasters a square rounded-rect silhouette, anti-aliased at the corner arc. RGB stays 255 and only alpha
// carries the shape, so SetColorMod tints it under BLENDMODE_BLEND.
func rasterFillMask(radius int32) *image.RGBA {
	dim := 2*radius + shapeMaskCenter
	img := image.NewRGBA(image.Rect(0, 0, int(dim), int(dim)))
	for y := int32(0); y < dim; y++ {
		for x := int32(0); x < dim; x++ {
			cov := roundedRectCoverage(x, y, dim, radius)
			img.SetRGBA(int(x), int(y), color.RGBA{R: 255, G: 255, B: 255, A: uint8(cov * 255)})
		}
	}
	return img
}

// rasterStrokeMask rasters a ring on the same outline as the fill: alpha peaks on the outline and falls to 0 either
// side.
func rasterStrokeMask(radius int32) *image.RGBA {
	dim := 2*radius + shapeMaskCenter
	img := image.NewRGBA(image.Rect(0, 0, int(dim), int(dim)))
	half := float64(shapeStrokePx) / 2
	for y := int32(0); y < dim; y++ {
		for x := int32(0); x < dim; x++ {
			// 0 is on the outline, positive outside; a ring is |sd| within half the stroke width.
			sd := roundedRectSignedDist(x, y, dim, radius)
			cov := 1 - value.Clamp((math.Abs(sd)-half)/shapeMaskAA, 0, 1)
			img.SetRGBA(int(x), int(y), color.RGBA{R: 255, G: 255, B: 255, A: uint8(cov * 255)})
		}
	}
	return img
}

// roundedRectSignedDist is the signed distance from pixel (x,y)'s centre to the rounded-rect outline in the dim×dim
// square; <0 inside, >0 outside.
func roundedRectSignedDist(x, y, dim, radius int32) float64 {
	cx := float64(x) + 0.5 - float64(dim)/2
	cy := float64(y) + 0.5 - float64(dim)/2
	half := float64(dim) / 2
	r := float64(radius)
	// A point's offset beyond the square shrunk by r on each side, minus r for the corner rounding.
	qx := math.Abs(cx) - (half - r)
	qy := math.Abs(cy) - (half - r)
	ox := math.Max(qx, 0)
	oy := math.Max(qy, 0)
	outside := math.Hypot(ox, oy)
	inside := math.Min(math.Max(qx, qy), 0)
	return outside + inside - r
}

// roundedRectCoverage returns the fill coverage [0,1] of pixel (x,y), anti-aliased across the corner arc.
func roundedRectCoverage(x, y, dim, radius int32) float64 {
	sd := roundedRectSignedDist(x, y, dim, radius)
	return value.Clamp(0.5-sd/shapeMaskAA, 0, 1)
}

// buildShapeMasks rasters the active shape's masks across every tier and uploads them to the pinned tier. Render thread
// only. Idempotent: UploadPinned destroys any page it replaces.
func (a *App) buildShapeMasks() {
	shape := a.d.Prefs.ChromeShape()
	a.shapeMaskShape = shape
	a.shapeMaskAt = a.now()
	if a.d.Store != nil {
		// A preset switch abandons the old shape's masks, so sweep them rather than leave dead eviction-exempt bytes.
		// Callers re-resolve the Ctx mask pointers immediately after, so nothing dangles on the queued destroys.
		for _, kind := range shapeMaskKinds {
			if kind == shape {
				continue
			}
			for tier := 0; tier < shapeRadiusTiers; tier++ {
				a.d.Store.Remove(shapeTexKey(kind, shapeRoleFill, tier))
				a.d.Store.Remove(shapeTexKey(kind, shapeRoleStroke, tier))
			}
		}
	}
	if shape == shapeSharp || a.d.Store == nil {
		// Nothing to build; mark built so we don't spin. A Settings change to a real shape rebuilds explicitly.
		a.shapeMasksBuilt = true
		a.shapeMasksGen = a.shapeStoreGen()
		return
	}
	ok := true
	for tier := 0; tier < shapeRadiusTiers; tier++ {
		radius := shapeTierRadii[tier]
		fill := &assets.Decoded{
			Frames: []*image.RGBA{rasterFillMask(radius)},
			Delays: []time.Duration{0},
			Width:  int(2*radius + shapeMaskCenter), Height: int(2*radius + shapeMaskCenter),
		}
		stroke := &assets.Decoded{
			Frames: []*image.RGBA{rasterStrokeMask(radius)},
			Delays: []time.Duration{0},
			Width:  int(2*radius + shapeMaskCenter), Height: int(2*radius + shapeMaskCenter),
		}
		if err := a.d.Store.UploadPinned(shapeTexKey(shape, shapeRoleFill, tier), fill); err != nil {
			ok = false
		}
		if err := a.d.Store.UploadPinned(shapeTexKey(shape, shapeRoleStroke, tier), stroke); err != nil {
			ok = false
		}
	}
	a.shapeMasksBuilt = ok
	a.shapeMasksGen = a.shapeStoreGen()
}

// shapeStoreGen reads the texture store generation, 0 when the store is absent.
func (a *App) shapeStoreGen() uint64 {
	if a.d.Store == nil {
		return 0
	}
	return a.d.Store.Generation()
}

const (
	shapeRoleFill   = "fill"
	shapeRoleStroke = "stroke"
)

// refreshShapeMasks is the frame-start hook: it resolves the active shape's masks into Ctx and lazily rebuilds them
// when absent. Zero alloc in the steady state.
func (a *App) refreshShapeMasks() {
	c := a.ctx
	shape := a.d.Prefs.ChromeShape()
	tier := a.d.Prefs.ChromeShapeTier()
	c.activeShape = shape
	// The default, and it must stay a cheap early exit.
	if shape == shapeSharp {
		c.shapeMaskReady = false
		c.shapeFillTex, c.shapeStrokeTex = nil, nil
		a.shapeResolvedShape = "" // drop the memo; a later non-sharp pick re-resolves
		return
	}
	// Rebuild if the shape changed or the generation moved (an eviction may have dropped the pages), paced so streaming
	// bumps can't rebuild every frame.
	if a.d.Store != nil {
		gen := a.shapeStoreGen()
		stale := a.shapeMaskShape != shape || !a.shapeMasksBuilt
		if stale || gen != a.shapeMasksGen {
			// If the fill mask is gone, heal (paced); a shape change rebuilds now.
			if stale || !a.shapeMaskResident(shape) {
				if stale || a.now().Sub(a.shapeMaskAt) > shapeHealPeriod {
					a.buildShapeMasks()
				}
			} else {
				// Masks still resident; adopt the new generation.
				a.shapeMasksGen = gen
			}
		}
	}
	// Pill always uses the largest tier (highest-resolution corner) and the min(w,h)/2 draw rule.
	c.shapePill = shape == shapePillKey
	if c.shapePill {
		tier = shapeRadiusTiers - 1
	}
	if tier < 0 || tier >= shapeRadiusTiers {
		tier = 0
	}
	// Same shape and tier at the same generation: the pointers are still valid, because every store mutation that could
	// free them bumps it. Skipping the key concat keeps this hook zero-alloc. Read after any rebuild above, so a fresh
	// build re-resolves.
	gen := a.shapeStoreGen()
	if c.shapeMaskReady && shape == a.shapeResolvedShape && tier == a.shapeResolvedTier && gen == a.shapeResolvedGen {
		return
	}
	radius := shapeTierRadii[tier]
	fillPage, fok := a.shapePage(shape, shapeRoleFill, tier)
	strokePage, sok := a.shapePage(shape, shapeRoleStroke, tier)
	if !fok || !sok || len(fillPage.Frames) == 0 || len(strokePage.Frames) == 0 {
		// Not resident yet: fall through to sharp this frame; the next frame retries.
		c.shapeMaskReady = false
		c.shapeFillTex, c.shapeStrokeTex = nil, nil
		a.shapeResolvedShape = "" // no memo — retry the resolve next frame
		return
	}
	c.shapeFillTex = fillPage.Frames[0]
	c.shapeStrokeTex = strokePage.Frames[0]
	c.shapeMaskDim = 2*radius + shapeMaskCenter
	c.shapeMaskR = radius
	c.shapeMaskReady = true
	a.shapeResolvedShape, a.shapeResolvedTier, a.shapeResolvedGen = shape, tier, gen
}

// invalidateShapeMasks forces the rest of this frame to sharp and rebuilds at the next refreshShapeMasks. Must be
// called after any mid-frame store invalidation: Purge destroys pinned pages synchronously (textures.go), so a later
// shaped draw would Copy a freed texture.
func (a *App) invalidateShapeMasks() {
	a.shapeMasksBuilt = false
	a.shapeResolvedShape = "" // drop the resolve memo with the pointers
	c := a.ctx
	c.shapeMaskReady = false
	c.shapeFillTex, c.shapeStrokeTex = nil, nil
}

// shapeMaskResident reports whether the shape's tier-0 fill mask is still in the store.
func (a *App) shapeMaskResident(shape string) bool {
	if a.d.Store == nil {
		return false
	}
	page, ok := a.d.Store.Get(shapeTexKey(shape, shapeRoleFill, 0))
	return ok && page != nil && len(page.Frames) > 0
}

// shapePage fetches a resident mask from the pinned tier. No cache needed: refreshShapeMasks resolves pointers once per
// frame, so this runs a handful of times at most.
func (a *App) shapePage(kind, role string, tier int) (*render.TexturePage, bool) {
	if a.d.Store == nil {
		return nil, false
	}
	return a.d.Store.Get(shapeTexKey(kind, role, tier))
}

// FillShaped fills r with col in the active chrome shape, or exactly c.Fill on sharp or before masks are resident.
// Panel bodies under a flat title bar stay on Fill: the sharp bar would poke square nubs past rounded corners.
func (c *Ctx) FillShaped(r sdl.Rect, col sdl.Color) {
	if c.activeShape == shapeSharp || c.activeShape == "" || !c.shapeMaskReady || c.shapeFillTex == nil {
		c.Fill(r, col)
		return
	}
	c.draw9Slice(c.shapeFillTex, r, col)
}

// borderShaped outlines r in col in the active chrome shape, or exactly c.Border on sharp or before masks are resident.
func (c *Ctx) borderShaped(r sdl.Rect, col sdl.Color) {
	if c.activeShape == shapeSharp || c.activeShape == "" || !c.shapeMaskReady || c.shapeStrokeTex == nil {
		c.Border(r, col)
		return
	}
	c.draw9Slice(c.shapeStrokeTex, r, col)
}

// draw9Slice paints a mask into dst as a 9-slice tinted to col, with corners at the mask radius (min(w,h)/2 for pill).
// The scratch rects keep &local from escaping through cgo.
// The mods are deliberately not restored to 255: these textures are shape-dedicated and every draw re-sets both. Don't
// copy this onto a texture anything else draws.
func (c *Ctx) draw9Slice(tex *sdl.Texture, dst sdl.Rect, col sdl.Color) {
	if dst.W <= 0 || dst.H <= 0 {
		return
	}
	_ = tex.SetColorMod(col.R, col.G, col.B)
	_ = tex.SetAlphaMod(col.A)

	src := c.shapeMaskR // mask corner-quadrant side in source px
	dim := c.shapeMaskDim
	// Clamp to half the smaller dimension so the corners never overlap. Pill wants the full half for true semicircles.
	corner := src
	if c.shapePill {
		corner = min(dst.W, dst.H) / 2
	}
	if maxC := min(dst.W, dst.H) / 2; corner > maxC {
		corner = maxC
	}
	if corner < 1 {
		// Degenerate (a 1px clip strip): a plain tinted stretch is cheaper than nine Copies.
		c.copy9(tex, 0, 0, dim, dim, dst.X, dst.Y, dst.W, dst.H)
		return
	}
	srcCenter := dim - 2*src // source center/edge span
	if srcCenter < 1 {
		srcCenter = 1
	}
	dstMidW := dst.W - 2*corner
	dstMidH := dst.H - 2*corner
	lx, mx, rx := dst.X, dst.X+corner, dst.X+dst.W-corner
	ty, my, by := dst.Y, dst.Y+corner, dst.Y+dst.H-corner

	c.copy9(tex, 0, 0, src, src, lx, ty, corner, corner)             // TL
	c.copy9(tex, dim-src, 0, src, src, rx, ty, corner, corner)       // TR
	c.copy9(tex, 0, dim-src, src, src, lx, by, corner, corner)       // BL
	c.copy9(tex, dim-src, dim-src, src, src, rx, by, corner, corner) // BR
	if dstMidW > 0 {
		c.copy9(tex, src, 0, srcCenter, src, mx, ty, dstMidW, corner)       // top
		c.copy9(tex, src, dim-src, srcCenter, src, mx, by, dstMidW, corner) // bottom
	}
	if dstMidH > 0 {
		c.copy9(tex, 0, src, src, srcCenter, lx, my, corner, dstMidH)       // left
		c.copy9(tex, dim-src, src, src, srcCenter, rx, my, corner, dstMidH) // right
	}
	if dstMidW > 0 && dstMidH > 0 {
		c.copy9(tex, src, src, srcCenter, srcCenter, mx, my, dstMidW, dstMidH)
	}
}

// copy9 blits one 9-slice cell through the scratch rects: a &sdl.Rect local would heap-allocate per Copy through cgo.
func (c *Ctx) copy9(tex *sdl.Texture, sx, sy, sw, sh, dx, dy, dw, dh int32) {
	if dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0 {
		return
	}
	c.shapeSrcScratch = sdl.Rect{X: sx, Y: sy, W: sw, H: sh}
	c.shapeDstScratch = sdl.Rect{X: dx, Y: dy, W: dw, H: dh}
	_ = c.Ren.Copy(tex, &c.shapeSrcScratch, &c.shapeDstScratch)
}
