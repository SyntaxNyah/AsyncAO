package ui

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// Chrome SHAPE presets (A5) — a silhouette system parallel to chrome.go's colour
// presets and orthogonal to it. Where chrome.go recolours the flat kit chrome,
// this reshapes it: buttons/chips/panel frames gain rounded or pill corners while
// hit-testing stays the SAME rectangle everywhere (no polygon picking).
//
// Mechanism: procedurally-rastered WHITE-RGB, straight-alpha silhouette MASKS
// (one "fill" mask + one "stroke ring" mask per shape/tier) uploaded ONCE into
// the eviction-exempt pinned texture tier under a shape:// key scheme, then drawn
// 9-slice (corners 1:1, edges/center stretched) and tinted per draw via
// SetColorMod + SetAlphaMod — the exact glyphcache white-texture idiom. So a
// button's fill colour and border colour survive, its translucency survives (the
// toolbox's A:120/A:205), and the whole thing costs zero per-frame allocation
// once the masks are resident (two persistent scratch rects, set-then-Copy).
//
// SHARP IS THE DEFAULT AND IS BYTE-IDENTICAL: when the active preset is "sharp"
// (or the masks are not yet resident), every shaped draw site early-returns into
// the verbatim Fill+Border body it had before A5. The settled-frame 0-alloc gate
// runs at the default prefs, so the default path is untouched by construction.
//
// The mask keys mirror the config package's persisted vocabulary. shapeSharp is
// never rastered (it has no mask — it IS the fall-through).
const (
	shapeSharp   = "sharp"   // flat rect — the byte-identical default (no mask)
	shapeRounded = "rounded" // rounded-rectangle corners (radius by tier)
	shapePillKey = "pill"    // full-radius pill: corner = min(w,h)/2 at draw time
)

// shapeRadiusTiers is the number of corner-radius size classes the "rounded"
// preset offers (0..shapeRadiusTiers-1) — a NAMED cap that bounds the derived
// mask cache (hard rule §17.4) and matches config.shapeRadiusTiers exactly.
const shapeRadiusTiers = 4

// shapeTierRadii maps each tier to its corner radius in device px. Small enough
// that even the largest fits a stock button (~24px tall) without the corners
// meeting, large enough that the top tier reads as a strong round. The pill
// preset ignores this table (its corner is min(w,h)/2, resolved at draw time
// against the LARGEST-tier mask so the quarter-circle source is high-resolution).
var shapeTierRadii = [shapeRadiusTiers]int32{4, 8, 12, 16}

// shapeMaskCenter is the solid center span (px) the 9-slice stretches to fill a
// widget's interior — a few px is plenty since the center is a flat run; keeping
// it tiny keeps each pinned mask small (mask side = 2*radius + center).
const shapeMaskCenter = int32(4)

// shapeStrokePx is the stroke-ring thickness (px) of the border mask — matches
// the 1px look of the flat Border it replaces (a hair thicker so the ring reads
// on a curve). Device px, not scaled: the stroke Copies stretch with the edges.
const shapeStrokePx = int32(2)

// shapeMaskAA is the anti-alias coverage width (px) at the rounded corner edge —
// the soft band over which alpha falls 255→0, so curves don't stair-step.
const shapeMaskAA = float64(1.2)

// shapeLabelInset is the extra horizontal padding (px, EACH side) a shaped
// button's label gets so a glyph never tucks under a corner curve. Applied ONLY
// on the shaped path — the sharp path keeps its original r.W-8 clamp untouched.
const shapeLabelInset = int32(3)

// shapeHealPeriod paces re-rastering the shape masks after a T1 eviction dropped
// the pinned pages (same trick as themeHealPeriod: one rebuild per period).
const shapeHealPeriod = 2 * time.Second

// shapeTexKey builds the pinned-tier key for one shape mask: shape://<kind>/<role>/<tier>.
// The scheme prefix can't collide with asset URLs (http/https/local) or theme://
// keys. role is "fill" or "stroke"; tier indexes shapeTierRadii. Called only on
// (rare) build/heal + the once-per-frame refresh, never per widget, so the small
// strconv/concat cost is off the hot path.
func shapeTexKey(kind, role string, tier int) string {
	return "shape://" + kind + "/" + role + "/" + strconv.Itoa(tier)
}

// shapeMaskKinds is the set of shapes that HAVE masks (sharp excluded). The
// mask cache is bounded by len(shapeMaskKinds) × shapeRadiusTiers × 2 roles —
// enumerate: {rounded,pill} × 4 tiers × {fill,stroke} = 16 pinned masks worst
// case, though only the active shape's masks are ever built at once.
var shapeMaskKinds = []string{shapeRounded, shapePillKey}

// rasterFillMask rasters a rounded-rect fill silhouette: WHITE RGB, straight
// alpha = coverage of the rounded rectangle. The mask is square (side =
// 2*radius + shapeMaskCenter); the rounded corners live in the four radius×radius
// quadrants, the straight edges/center are fully opaque. Anti-aliased at the
// corner arc via signed-distance coverage. RGB stays 255 everywhere (only alpha
// carries the shape) so SetColorMod tints it to any colour under BLENDMODE_BLEND.
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

// rasterStrokeMask rasters a 1..shapeStrokePx-wide ring following the same
// rounded-rect outline as the fill: alpha peaks on the outline and falls to 0
// inward and outward, so tinting it with the border colour draws a curved 1px
// border that hugs the fill. Same square dims + RGB=255 convention as the fill.
func rasterStrokeMask(radius int32) *image.RGBA {
	dim := 2*radius + shapeMaskCenter
	img := image.NewRGBA(image.Rect(0, 0, int(dim), int(dim)))
	half := float64(shapeStrokePx) / 2
	for y := int32(0); y < dim; y++ {
		for x := int32(0); x < dim; x++ {
			// Signed distance to the rounded outline (0 = on the outline,
			// positive = outside). A ring is |sd| within half the stroke width.
			sd := roundedRectSignedDist(x, y, dim, radius)
			cov := 1 - clampF64((math.Abs(sd)-half)/shapeMaskAA, 0, 1)
			img.SetRGBA(int(x), int(y), color.RGBA{R: 255, G: 255, B: 255, A: uint8(cov * 255)})
		}
	}
	return img
}

// roundedRectSignedDist is the signed distance from pixel (x,y)'s CENTRE to the
// rounded-rect outline inscribed in the dim×dim square with corner radius. <0
// inside, >0 outside. Standard SDF: distance to the inset box's corner circles.
func roundedRectSignedDist(x, y, dim, radius int32) float64 {
	// Pixel centre coordinates relative to the square's centre.
	cx := float64(x) + 0.5 - float64(dim)/2
	cy := float64(y) + 0.5 - float64(dim)/2
	half := float64(dim) / 2
	r := float64(radius)
	// Distance in the "core box" (square shrunk by r on each side): a point's
	// offset beyond the core box, then subtract r for the corner rounding.
	qx := math.Abs(cx) - (half - r)
	qy := math.Abs(cy) - (half - r)
	ox := math.Max(qx, 0)
	oy := math.Max(qy, 0)
	outside := math.Hypot(ox, oy)
	inside := math.Min(math.Max(qx, qy), 0)
	return outside + inside - r
}

// roundedRectCoverage returns the fill coverage [0,1] of pixel (x,y) for the
// rounded-rect silhouette: 1 well inside, 0 well outside, anti-aliased across
// the corner arc.
func roundedRectCoverage(x, y, dim, radius int32) float64 {
	sd := roundedRectSignedDist(x, y, dim, radius)
	// sd<0 inside → coverage 1; sd>0 outside → 0; soft band of width shapeMaskAA.
	return clampF64(0.5-sd/shapeMaskAA, 0, 1)
}

// buildShapeMasks rasters the active shape's fill+stroke masks across every tier
// and uploads them into the pinned (eviction-exempt) texture tier, mirroring
// applyChromePreset's live-apply. Render thread only (it touches the store); a
// no-op when the store is absent (early startup / headless) or the shape is
// sharp. Idempotent: UploadPinned replaces an existing page (destroying the old
// one), so re-running never leaks. Latches shapeMasksBuilt on success so the
// per-frame refresh stops rebuilding.
func (a *App) buildShapeMasks() {
	shape := a.d.Prefs.ChromeShape()
	a.shapeMaskShape = shape
	a.shapeMaskAt = a.now()
	if a.d.Store != nil {
		// A preset switch abandons the departing shape's masks: sweep every
		// OTHER kind's pages out of the pinned tier (sharp sweeps them all)
		// rather than letting dead eviction-exempt bytes ride until a Purge.
		// Bounded by the enumerated key set; Remove on an absent key is a
		// no-op. Callers re-resolve the Ctx mask pointers immediately after
		// (refreshShapeMasks), so nothing dangles on the queued destroys.
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
		// Nothing to build; the draw sites fall through to sharp. Mark built so
		// we don't spin (a Settings change to a real shape rebuilds explicitly).
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

// shapeStoreGen reads the texture store generation, 0 when the store is absent
// (headless / early startup) so the shape mask cache logic has a stable baseline.
func (a *App) shapeStoreGen() uint64 {
	if a.d.Store == nil {
		return 0
	}
	return a.d.Store.Generation()
}

// shape mask roles.
const (
	shapeRoleFill   = "fill"
	shapeRoleStroke = "stroke"
)

// refreshShapeMasks is the frame-start hook: it resolves the active shape and
// its mask textures into the Ctx fields the per-widget shaped draws read, and
// (lazily, paced) (re)builds the pinned masks when they are absent — on first
// use, after a Settings change, or after an eviction dropped them. Called once
// per frame from Frame(), and from tests via the same path, so the shaped draw
// path is exercised identically live and headless. Zero alloc on the steady
// state (all field reads); the build path runs only on change/eviction.
func (a *App) refreshShapeMasks() {
	c := a.ctx
	shape := a.d.Prefs.ChromeShape()
	tier := a.d.Prefs.ChromeShapeTier()
	c.activeShape = shape
	// Sharp: disable the shaped path entirely and skip all store work — this is
	// the default and MUST stay a cheap early exit (byte-identical draws below).
	if shape == shapeSharp {
		c.shapeMaskReady = false
		c.shapeFillTex, c.shapeStrokeTex = nil, nil
		a.shapeResolvedShape = "" // drop the memo; a later non-sharp pick re-resolves
		return
	}
	// A shape changed since we built, or the store generation moved past what we
	// built against (an eviction may have dropped the pinned pages): rebuild,
	// paced so a burst of streaming generation bumps can't rebuild every frame.
	if a.d.Store != nil {
		gen := a.shapeStoreGen()
		stale := a.shapeMaskShape != shape || !a.shapeMasksBuilt
		if stale || gen != a.shapeMasksGen {
			// Verify residency cheaply: if the fill mask for the active tier is
			// gone, heal (paced); a same-generation shape change rebuilds now.
			if stale || !a.shapeMaskResident(shape) {
				if stale || a.now().Sub(a.shapeMaskAt) > shapeHealPeriod {
					a.buildShapeMasks()
				}
			} else {
				// Masks still resident, only the generation counter moved — adopt it.
				a.shapeMasksGen = gen
			}
		}
	}
	// Resolve this frame's tier mask pointers. Pill always uses the largest tier
	// (highest-resolution corner quadrant) and flags the min(w,h)/2 draw rule.
	c.shapePill = shape == shapePillKey
	if c.shapePill {
		tier = shapeRadiusTiers - 1
	}
	if tier < 0 || tier >= shapeRadiusTiers {
		tier = 0
	}
	// Steady-state fast path: the same shape+tier already resolved against the
	// SAME store generation — the pointers must still be valid, because every
	// store mutation that could free them (eviction, our own build/sweep,
	// UploadPinned replacing a key, Purge) bumps the generation. Skipping the
	// shapeTexKey concat + map Gets keeps this always-on frame hook zero-alloc
	// on settled frames (pinned by TestRefreshShapeMasksZeroAlloc). The gen is
	// read AFTER the (possible) rebuild above so a fresh build re-resolves
	// against its own post-upload generation.
	gen := a.shapeStoreGen()
	if c.shapeMaskReady && shape == a.shapeResolvedShape && tier == a.shapeResolvedTier && gen == a.shapeResolvedGen {
		return
	}
	radius := shapeTierRadii[tier]
	fillPage, fok := a.shapePage(shape, shapeRoleFill, tier)
	strokePage, sok := a.shapePage(shape, shapeRoleStroke, tier)
	if !fok || !sok || len(fillPage.Frames) == 0 || len(strokePage.Frames) == 0 {
		// Not resident yet (build pending / evicted mid-heal): fall through to
		// sharp this frame — no flash, and the next frame retries.
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

// invalidateShapeMasks drops the frame-resolved mask pointers (Ctx) and the
// built latch, forcing the rest of THIS frame onto the sharp fall-through and
// an immediate rebuild at the next refreshShapeMasks. It MUST be called right
// after any MID-FRAME store invalidation — today the Settings "smooth texture
// scaling" toggle's Store.Purge() — because refreshShapeMasks resolved the
// pointers at frame START and Purge destroys pinned pages SYNCHRONOUSLY
// (textures.go Purge → page.destroy()), so a shaped draw later in the same
// frame would Copy a freed texture (the stale-handle/black-flash class). Any
// future mid-frame Store.Purge()/invalidation site must make this same call.
func (a *App) invalidateShapeMasks() {
	a.shapeMasksBuilt = false
	a.shapeResolvedShape = "" // drop the resolve memo with the pointers
	c := a.ctx
	c.shapeMaskReady = false
	c.shapeFillTex, c.shapeStrokeTex = nil, nil
}

// shapeMaskResident reports whether the active shape's tier-0 fill mask is still
// in the store (a cheap residency probe used to decide whether an eviction
// dropped the pinned pages).
func (a *App) shapeMaskResident(shape string) bool {
	if a.d.Store == nil {
		return false
	}
	page, ok := a.d.Store.Get(shapeTexKey(shape, shapeRoleFill, 0))
	return ok && page != nil && len(page.Frames) > 0
}

// shapePage fetches a resident shape-mask page from the pinned tier. Unlike
// themePage there is no generation-keyed map cache: the mask pointers are already
// resolved into Ctx fields once per frame by refreshShapeMasks, so this is called
// at most a handful of times per frame (2 roles × 1 tier) — a direct pinned-map
// Get, no per-widget lookups.
func (a *App) shapePage(kind, role string, tier int) (*render.TexturePage, bool) {
	if a.d.Store == nil {
		return nil, false
	}
	return a.d.Store.Get(shapeTexKey(kind, role, tier))
}

// FillShaped fills r with col, honouring the active chrome SHAPE. On the sharp
// preset (or before masks are resident) it is EXACTLY c.Fill(r,col) — the
// byte-identical default. Otherwise it draws the fill mask 9-slice, tinted to
// col. Self-contained container backgrounds call this instead of Fill (v1: the
// toolbox flyout chips; ButtonCol routes here for every kit button). The base
// Fill stays rectangular for gradients/swatches/checkbox inners/clip strips —
// and for panel bodies that carry a flat title bar over their top edge, whose
// top-rounded corner treatment is deferred (a title bar drawn as a sharp rect
// over a rounded body would poke square nubs past the rounded corners).
func (c *Ctx) FillShaped(r sdl.Rect, col sdl.Color) {
	if c.activeShape == shapeSharp || c.activeShape == "" || !c.shapeMaskReady || c.shapeFillTex == nil {
		c.Fill(r, col)
		return
	}
	c.draw9Slice(c.shapeFillTex, r, col)
}

// borderShaped outlines r in col, honouring the active chrome SHAPE. Sharp (or
// not-ready) is EXACTLY c.Border(r,col). Otherwise it draws the stroke-ring mask
// 9-slice, tinted to col, so the border hugs the rounded fill.
func (c *Ctx) borderShaped(r sdl.Rect, col sdl.Color) {
	if c.activeShape == shapeSharp || c.activeShape == "" || !c.shapeMaskReady || c.shapeStrokeTex == nil {
		c.Border(r, col)
		return
	}
	c.draw9Slice(c.shapeStrokeTex, r, col)
}

// draw9Slice paints tex (a WHITE straight-alpha mask, side c.shapeMaskDim, corner
// quadrant c.shapeMaskR) into dst as a 9-slice tinted to col: the four corner
// quadrants Copy from the mask corners, the four edges stretch the mask edges,
// and the center stretches the mask center. Corners are copied at cornerPx (=
// mask radius, or min(w,h)/2 for pill) so the curve size is correct at any widget
// size. Everything uses the two persistent scratch rects (set-then-Copy) so no
// &local escapes through cgo — the hot path is 0-alloc once masks are resident.
//
// SetColorMod + SetAlphaMod tint the mask to col before the passes. NO restore
// to 255 afterwards — a DELIBERATE divergence from the shared-texture rule
// (viewport.go restores because sprite textures are drawn by many sites): these
// mask textures are shape-dedicated, never blitted by any other code path, and
// every draw9Slice re-sets both mods first. Do not copy this un-restored
// pattern onto any texture something else also draws.
func (c *Ctx) draw9Slice(tex *sdl.Texture, dst sdl.Rect, col sdl.Color) {
	if dst.W <= 0 || dst.H <= 0 {
		return
	}
	_ = tex.SetColorMod(col.R, col.G, col.B)
	_ = tex.SetAlphaMod(col.A)

	src := c.shapeMaskR // mask corner-quadrant side in source px
	dim := c.shapeMaskDim
	// Destination corner size: clamp to half the smaller dimension so the four
	// corners never overlap (advisor: buttons are ~24px tall). Pill wants the
	// full half so the ends are true semicircles; rounded uses the mask radius
	// but still clamped to fit.
	corner := src
	if c.shapePill {
		corner = min32(dst.W, dst.H) / 2
	}
	if maxC := min32(dst.W, dst.H) / 2; corner > maxC {
		corner = maxC
	}
	if corner < 1 {
		// Degenerate (1px-tall clip strip): a plain tinted stretch reads as the
		// flat fill it approximates — cheaper than nine 1px Copies.
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

	// Corners (source: the four src×src quadrants; dest: corner×corner).
	c.copy9(tex, 0, 0, src, src, lx, ty, corner, corner)             // TL
	c.copy9(tex, dim-src, 0, src, src, rx, ty, corner, corner)       // TR
	c.copy9(tex, 0, dim-src, src, src, lx, by, corner, corner)       // BL
	c.copy9(tex, dim-src, dim-src, src, src, rx, by, corner, corner) // BR
	// Edges (stretch the mask's edge strips along the free axis).
	if dstMidW > 0 {
		c.copy9(tex, src, 0, srcCenter, src, mx, ty, dstMidW, corner)       // top
		c.copy9(tex, src, dim-src, srcCenter, src, mx, by, dstMidW, corner) // bottom
	}
	if dstMidH > 0 {
		c.copy9(tex, 0, src, src, srcCenter, lx, my, corner, dstMidH)       // left
		c.copy9(tex, dim-src, src, src, srcCenter, rx, my, corner, dstMidH) // right
	}
	// Center (stretch the mask center over the interior).
	if dstMidW > 0 && dstMidH > 0 {
		c.copy9(tex, src, src, srcCenter, srcCenter, mx, my, dstMidW, dstMidH)
	}
}

// copy9 blits one 9-slice cell from source (sx,sy,sw,sh) to dest (dx,dy,dw,dh)
// using the persistent scratch rects — the cgoRect discipline for the shaped
// path (a &sdl.Rect local would heap-allocate per Copy through cgo).
func (c *Ctx) copy9(tex *sdl.Texture, sx, sy, sw, sh, dx, dy, dw, dh int32) {
	if dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0 {
		return
	}
	c.shapeSrcScratch = sdl.Rect{X: sx, Y: sy, W: sw, H: sh}
	c.shapeDstScratch = sdl.Rect{X: dx, Y: dy, W: dw, H: dh}
	_ = c.Ren.Copy(tex, &c.shapeSrcScratch, &c.shapeDstScratch)
}

// min32 returns the smaller of two int32s (no generics dependency in this hot
// path; a plain helper keeps the 9-slice math obvious).
func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
