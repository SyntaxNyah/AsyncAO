package ui

// Procedural generators: arithmetic that turns a theme.GeneratorSpec into a plain *image.RGBA, with no SDL.
// Contract for every generator:
//  1. Pure and deterministic: the param hash is the cache key, so no time.Now, unseeded rand, mutable globals or map
//     order.
//  2. A tile, never a field: w and h are at most theme.GenTileMaxPx, and the element tiles, stretches or 9-slices it.
//  3. Runs on the theme-apply goroutine, never the render thread.
//  4. Allocates exactly one *image.RGBA: each pixel comes from coordinates, params and a seeded hash.
// Wire names are the format: append-only. An unknown name degrades to a flat fill and the element still draws.

import (
	"image"
	"image/color"
	"math"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// genTileMinPx is the smallest tile; below a few pixels a pattern is a solid colour with rounding noise.
const genTileMinPx = int32(4)

// genPctMax is the top of every 0..100 parameter.
const genPctMax = int32(100)

// Bounds on hostile parameters: values come from a file a stranger wrote, and the work depends on the params, not
// the tile size, so the tile cap alone does not bound it.
const (
	// genIntParamMax clamps pitch, size and count at parse time, keeping generator arithmetic inside int32.
	genIntParamMax = int32(4 * theme.GenTileMaxPx)
	// genFigureCap bounds the discrete figures in one tile, such as rays or scattered marks.
	genFigureCap = int32(128)
	// genRepeatCap bounds how many times a figure is packed into one tile.
	genRepeatCap = int32(8)
	// genStampMaxSteps bounds genLine's and genRing's walks, where an absurd length becomes absurd time.
	genStampMaxSteps = 4 * theme.GenTileMaxPx
	// genHalftoneDotPct is halftone's default dot diameter as a percent of its cell.
	genHalftoneDotPct = int32(68)
)

// GenParams is the typed view over a generator's k = v list, resolved once at bake so a generator never parses a
// string.
// Wire keys are per-generator names for a few shared roles; genParamSlots is the one global mapping.
type GenParams struct {
	// Colors are tint, bg, accent, shadow, in that order.
	Colors [4]color.RGBA
	// Ints are pitch, size, count, seed.
	Ints [4]int32
	// Angle is the 360/256 angle byte, so 64 is 90 degrees (the ThemeRectRotations convention).
	Angle uint8
	// Pcts are the two 0..100 knobs, documented at each generator.
	Pcts [2]int16
}

func (p GenParams) tint() color.RGBA   { return p.Colors[0] }
func (p GenParams) bg() color.RGBA     { return p.Colors[1] }
func (p GenParams) accent() color.RGBA { return p.Colors[2] }
func (p GenParams) shadow() color.RGBA { return p.Colors[3] }
func (p GenParams) pitch() int32       { return p.Ints[0] }
func (p GenParams) size() int32        { return p.Ints[1] }
func (p GenParams) count() int32       { return p.Ints[2] }
func (p GenParams) seed() int32        { return p.Ints[3] }

// pitchOr, sizeOr and countOr return the parameter or a default; 0 is the absent marker for every int param.
func (p GenParams) pitchOr(def int32) int32 { return genIntOr(p.pitch(), def) }
func (p GenParams) sizeOr(def int32) int32  { return genIntOr(p.size(), def) }
func (p GenParams) countOr(def int32) int32 { return genIntOr(p.count(), def) }

func genIntOr(v, def int32) int32 {
	if v <= 0 {
		return def
	}
	return v
}

// pctOr returns Pcts[i] or a default; 0 is a legal percent, so absent is genParamAbsent.
func (p GenParams) pctOr(i int, def int32) int32 {
	if p.Pcts[i] < 0 {
		return def // the absent marker; see genParseParams
	}
	return int32(p.Pcts[i])
}

// genParamAbsent is what an unwritten percent holds; 0 is a legitimate authored value such as "no fade".
const genParamAbsent = int16(-1)

// genParamSlot names one destination in GenParams.
type genParamSlot uint8

const (
	genSlotTint genParamSlot = iota
	genSlotBG
	genSlotAccent
	genSlotShadow
	genSlotPitch
	genSlotSize
	genSlotCount
	genSlotSeed
	genSlotAngle
	genSlotPct0
	genSlotPct1
)

// genParamSlots maps every wire key to a destination; append-only, since an unknown key is silently ignored.
var genParamSlots = map[string]genParamSlot{
	// Colours — one role each, in every generator.
	"tint": genSlotTint, "bg": genSlotBG, "accent": genSlotAccent, "shadow": genSlotShadow,
	// Integers. cells and dot are a pitch; radius and ring are a size; bloom is a count.
	"pitch": genSlotPitch, "cells": genSlotPitch, "dot": genSlotPitch,
	"size": genSlotSize, "radius": genSlotSize, "ring": genSlotSize,
	"count": genSlotCount, "bloom": genSlotCount,
	"seed":  genSlotSeed,
	"angle": genSlotAngle,
	// Percents. Which generator reads which is documented at each raster.
	"pct": genSlotPct0, "inner": genSlotPct0, "density": genSlotPct0,
	"gap": genSlotPct0, "soft": genSlotPct0, "base": genSlotPct0, "fade": genSlotPct0,
	"squash": genSlotPct1, "amp": genSlotPct1, "phase": genSlotPct1, "dots": genSlotPct1,
	// full is wingmark's lobe fullness and bow is the bezel's barrel.
	"full": genSlotPct1, "bow": genSlotPct1,
}

// GenFunc rasterises one tile under the contract at the top of this file.
type GenFunc func(w, h int32, p GenParams) *image.RGBA

// genEntry is one registry row; size is separate because the caller allocates first and clause 2 checks the size.
type genEntry struct {
	tile func(p GenParams) (int32, int32)
	draw GenFunc
}

// genRegistry is indexed by wire name and read once per element per theme apply, on the apply goroutine.
var genRegistry = map[string]genEntry{
	"scanlines":   {tileScanlines, drawScanlines},
	"halftone":    {tileHalftone, drawHalftone},
	"grid":        {tileGrid, drawGrid},
	"hex":         {tileHex, drawHex},
	"noise":       {tileNoise, drawNoise},
	"woodgrain":   {tileWoodgrain, drawWoodgrain},
	"gradient":    {tileGradient, drawGradient},
	"glow":        {tileGlow, drawGlow},
	"stripes":     {tileStripes, drawStripes},
	"hatch":       {tileHatch, drawHatch},
	"plate":       {tilePlate, drawPlate},
	"frame":       {tileFrame, drawFrame},
	"radial":      {tileRadial, drawRadial},
	"mottle":      {tileMottle, drawMottle},
	"checkerdisc": {tileCheckerdisc, drawCheckerdisc},
	"skyline":     {tileSkyline, drawSkyline},
	"wingmark":    {tileWingmark, drawWingmark},
}

// GeneratorNames returns the registry's wire names in map order; it allocates, so never use it on a draw path.
func GeneratorNames() []string {
	out := make([]string, 0, len(genRegistry))
	for name := range genRegistry {
		out = append(out, name)
	}
	return out
}

// genLookup is the one registry probe, so every entry point normalises the name the same way.
func genLookup(name string) (genEntry, bool) {
	e, ok := genRegistry[theme.NormalizeGenName(name)]
	return e, ok
}

// GeneratorKnown reports if a wire name rasterises; if not, the element is a flat fill and the import report says so.
func GeneratorKnown(name string) bool {
	_, ok := genLookup(name)
	return ok
}

// genTileSizeOf is one spec's clamped tile size, for budgeting without rasterising.
// It takes the spec so a caller cannot budget one generator's tile and raster another's.
func genTileSizeOf(g theme.GeneratorSpec) (int32, int32) {
	e, ok := genLookup(g.Name)
	if !ok {
		return 0, 0
	}
	w, h := e.tile(genParseParams(g))
	return clampTilePx(w), clampTilePx(h)
}

// clampTilePx enforces clause 2, so no tile function can breach the cap through its own arithmetic.
func clampTilePx(v int32) int32 {
	if v < genTileMinPx {
		return genTileMinPx
	}
	if v > theme.GenTileMaxPx {
		return theme.GenTileMaxPx
	}
	return v
}

// RasterGenerator rasterises a spec on the theme-apply goroutine, or returns nil for an unknown name.
func RasterGenerator(g theme.GeneratorSpec) *image.RGBA {
	e, ok := genLookup(g.Name) // one probe, shared with GeneratorKnown/genTileSizeOf
	if !ok {
		return nil
	}
	p := genParseParams(g)
	w, h := e.tile(p)
	return e.draw(clampTilePx(w), clampTilePx(h), p)
}

// genParseParams resolves the k=v list into the typed view; on a repeated key the last wins, as in the INI reader.
func genParseParams(g theme.GeneratorSpec) GenParams {
	p := GenParams{Pcts: [2]int16{genParamAbsent, genParamAbsent}}
	for i := 0; i < g.NP && i < len(g.Params); i++ {
		kv := g.Params[i]
		slot, known := genParamSlots[kv.Key]
		if !known {
			continue // "a generator ignores what it does not use" (§5)
		}
		switch slot {
		case genSlotTint, genSlotBG, genSlotAccent, genSlotShadow:
			if c, ok := genParseColor(kv.Value); ok {
				p.Colors[slot] = c
			}
		case genSlotSeed:
			// The seed is not clamped: it is a hash input, and every value is legal.
			p.Ints[genSlotSeed-genSlotPitch] = int32(parse.AtoiOr(kv.Value, 0))
		case genSlotPitch, genSlotSize, genSlotCount:
			p.Ints[slot-genSlotPitch] = int32(value.Clamp(parse.AtoiOr(kv.Value, 0), 0, int(genIntParamMax)))
		case genSlotAngle:
			p.Angle = uint8(parse.AtoiOr(kv.Value, 0) % theme.AngleCount)
		case genSlotPct0, genSlotPct1:
			p.Pcts[slot-genSlotPct0] = int16(value.Clamp(parse.AtoiOr(kv.Value, 0), 0, int(genPctMax)))
		}
	}
	return p
}

// genParseColor delegates to internal/theme so a generator param and an element's fill agree on colour grammar.
func genParseColor(v string) (color.RGBA, bool) {
	c, ok := theme.ParseRGBA(v)
	if !ok {
		return color.RGBA{}, false
	}
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}, true
}

// genHash is splitmix64's finalizer over (x, y, seed); a rand stream would tie each tile to its pixel visit order.
func genHash(x, y, seed int32) uint64 {
	v := uint64(uint32(x))*0x9E3779B97F4A7C15 ^ uint64(uint32(y))*0xC2B2AE3D27D4EB4F ^ uint64(uint32(seed))*0x165667B19E3779F9
	v ^= v >> 30
	v *= 0xBF58476D1CE4E5B9
	v ^= v >> 27
	v *= 0x94D049BB133111EB
	v ^= v >> 31
	return v
}

// genUnit is genHash folded to [0, 1).
func genUnit(x, y, seed int32) float64 {
	return float64(genHash(x, y, seed)>>11) / float64(uint64(1)<<53)
}

// genValueNoise is smooth value noise at feature size cell, torus-wrapped on period so a tile repeats cleanly.
func genValueNoise(x, y float64, cell, period, seed int32) float64 {
	if cell <= 0 {
		cell = 1
	}
	fx, fy := x/float64(cell), y/float64(cell)
	x0, y0 := int32(math.Floor(fx)), int32(math.Floor(fy))
	tx, ty := fx-float64(x0), fy-float64(y0)
	tx = tx * tx * (3 - 2*tx) // smoothstep
	ty = ty * ty * (3 - 2*ty)
	wrap := period / cell
	if wrap < 1 {
		wrap = 1
	}
	at := func(ix, iy int32) float64 {
		return genUnit(((ix%wrap)+wrap)%wrap, ((iy%wrap)+wrap)%wrap, seed)
	}
	a, b := at(x0, y0), at(x0+1, y0)
	c, d := at(x0, y0+1), at(x0+1, y0+1)
	return (a*(1-tx)+b*tx)*(1-ty) + (c*(1-tx)+d*tx)*ty
}

// genFill paints the whole tile one colour.
func genFill(img *image.RGBA, c color.RGBA) {
	if c.A == 0 {
		return // already zeroed by image.NewRGBA; writing zeros costs a pass for nothing
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.PixOffset(b.Min.X, y)
		for x := 0; x < b.Dx(); x++ {
			i := row + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
		}
	}
}

// genBlend composites src over (x, y) with extra coverage in [0, 1], straight alpha as buildPage uploads and SDL
// expects.
// Out of bounds is a no-op: analytic curves can round one pixel past an edge.
func genBlend(img *image.RGBA, x, y int32, src color.RGBA, cover float64) {
	b := img.Bounds()
	if x < int32(b.Min.X) || y < int32(b.Min.Y) || x >= int32(b.Max.X) || y >= int32(b.Max.Y) {
		return
	}
	if cover <= 0 || src.A == 0 {
		return
	}
	if cover > 1 {
		cover = 1
	}
	sa := float64(src.A) / 255 * cover
	i := img.PixOffset(int(x), int(y))
	da := float64(img.Pix[i+3]) / 255
	out := sa + da*(1-sa)
	if out <= 0 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 0, 0, 0, 0
		return
	}
	mix := func(s, d uint8) uint8 {
		return uint8(math.Round((float64(s)*sa + float64(d)*da*(1-sa)) / out))
	}
	img.Pix[i] = mix(src.R, img.Pix[i])
	img.Pix[i+1] = mix(src.G, img.Pix[i+1])
	img.Pix[i+2] = mix(src.B, img.Pix[i+2])
	img.Pix[i+3] = uint8(math.Round(out * 255))
}

// genAngleRad turns the 360/256 angle byte into radians.
func genAngleRad(a uint8) float64 { return float64(a) * 2 * math.Pi / float64(theme.AngleCount) }

// genNewTile allocates the one image a generator is allowed (clause 4).
func genNewTile(w, h int32) *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
}

// scanlines. pitch: line period px (default 4). size: line thickness px (default 1). Pcts[0] (fade): edge softening.
func tileScanlines(p GenParams) (int32, int32) { return genTileMinPx, p.pitchOr(4) }

func drawScanlines(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	thick := p.sizeOr(1)
	soft := float64(p.pctOr(0, 0)) / float64(genPctMax)
	for y := int32(0); y < h; y++ {
		if y >= thick {
			continue
		}
		cover := 1.0
		if soft > 0 && thick > 1 {
			// Ramp the band toward transparent, so a thick line reads as a bar rather than a hard stripe.
			cover = 1 - soft*float64(y)/float64(thick-1)
		}
		for x := int32(0); x < w; x++ {
			genBlend(img, x, y, p.tint(), cover)
		}
	}
	return img
}

// halftone. pitch or dot: cell px (default 6). Pcts[0] (pct): dot diameter percent of cell (default genHalftoneDotPct).
// pitch and dot alias one slot, so a dot size needs pct.
// Angle staggers rather than rotates: a rotated lattice cannot repeat cleanly in an axis-aligned tile.
func tileHalftone(p GenParams) (int32, int32) { c := p.pitchOr(6); return c * 2, c * 2 }

func drawHalftone(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	// Bounded by the tile: a bigger cell makes the disc rasteriser's whole cost invisible.
	cell := min(p.pitchOr(6), theme.GenTileMaxPx)
	// Diameter as a percent of the cell, so the radius is pct/200 of it.
	radius := float64(cell) * float64(p.pctOr(0, genHalftoneDotPct)) / 200
	// The stagger is the angle's sine: 0 is a square lattice, 64 (90 degrees) the maximum half-cell offset.
	stagger := math.Abs(math.Sin(genAngleRad(p.Angle))) * float64(cell) / 2
	for cy := int32(0); cy*cell < h; cy++ {
		offset := 0.0
		if cy%2 == 1 {
			offset = stagger
		}
		for cx := int32(-1); cx*cell < w+cell; cx++ {
			ox := float64(cx*cell) + float64(cell)/2 + offset
			oy := float64(cy*cell) + float64(cell)/2
			genDisc(img, ox, oy, radius, p.tint())
		}
	}
	return img
}

// genDisc paints an anti-aliased filled circle.
func genDisc(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	if r <= 0 {
		return
	}
	// Clamp to the tile before the loop: a hostile radius would otherwise visit billions of coordinates.
	b := img.Bounds()
	x0 := max(int32(math.Floor(cx-r-1)), int32(b.Min.X))
	x1 := min(int32(math.Ceil(cx+r+1)), int32(b.Max.X)-1)
	y0 := max(int32(math.Floor(cy-r-1)), int32(b.Min.Y))
	y1 := min(int32(math.Ceil(cy+r+1)), int32(b.Max.Y)-1)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			genBlend(img, x, y, c, r-d+0.5)
		}
	}
}

// grid. pitch: cell px (default 16). size: line width px (default 1).
// Pcts[0] (pct): perspective strength, bunching rows toward the top. Pcts[1] (dots): above 50 draws intersections only.
func tileGrid(p GenParams) (int32, int32) { c := p.pitchOr(16); return c, c }

func drawGrid(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	line := p.sizeOr(1)
	persp := float64(p.pctOr(0, 0)) / float64(genPctMax)
	dotsOnly := p.pctOr(1, 0) > 50
	// The row at t is placed at t^(1+3*persp), the identity at persp=0.
	rowY := func(t float64) float64 {
		if persp <= 0 {
			return t * float64(h)
		}
		return math.Pow(t, 1+3*persp) * float64(h)
	}
	for x := int32(0); x < w; x++ {
		vertical := x < line
		for y := int32(0); y < h; y++ {
			horizontal := false
			for i := 0; i <= 4; i++ {
				ry := rowY(float64(i) / 4)
				if float64(y) >= ry && float64(y) < ry+float64(line) {
					horizontal = true
					break
				}
			}
			switch {
			case dotsOnly && vertical && horizontal:
				genBlend(img, x, y, p.tint(), 1)
			case !dotsOnly && (vertical || horizontal):
				genBlend(img, x, y, p.tint(), 1)
			}
		}
	}
	return img
}

// hex. pitch: cell width px (default 20). size: line width px (default 1).
func tileHex(p GenParams) (int32, int32) {
	c := p.pitchOr(20)
	// Width 3/2 c and height sqrt(2) c, rounded, because a tile is pixels.
	return c * 3 / 2, int32(math.Round(float64(c) * math.Sqrt2))
}

func drawHex(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	line := float64(p.sizeOr(1))
	// Bounded by the tile: genLine's walk depends on the circumradius.
	r := float64(min(p.pitchOr(20), theme.GenTileMaxPx)) / 2
	// Two lattice centres per tile (the offset row), so the pattern closes.
	for _, c := range [2][2]float64{{0, 0}, {float64(w) * 0.5, float64(h) * 0.5}} {
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				genHexOutline(img, c[0]+float64(int32(dx)*w), c[1]+float64(int32(dy)*h), r, line, p.tint())
			}
		}
	}
	return img
}

// genHexOutline strokes one flat-top hexagon of circumradius r.
func genHexOutline(img *image.RGBA, cx, cy, r, width float64, c color.RGBA) {
	var pts [7][2]float64
	for i := 0; i < 7; i++ {
		a := float64(i) * math.Pi / 3
		pts[i] = [2]float64{cx + r*math.Cos(a), cy + r*math.Sin(a)}
	}
	for i := 0; i < 6; i++ {
		genLine(img, pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1], width, c)
	}
}

// genLine strokes a segment by stamping discs; allocation-free and correct at any angle.
func genLine(img *image.RGBA, x0, y0, x1, y1, width float64, c color.RGBA) {
	dx, dy := x1-x0, y1-y0
	r := width / 2
	if r < 0.5 {
		r = 0.5
	}
	// Spacing scales with the pen: discs stay connected up to the radius, and half-pixel steps go quadratic for a wide
	// pen.
	spacing := math.Max(0.5, r/2)
	steps := int(math.Ceil(math.Hypot(dx, dy) / spacing))
	if steps <= 0 {
		return
	}
	if steps > genStampMaxSteps {
		steps = genStampMaxSteps
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		genDisc(img, x0+dx*t, y0+dy*t, r, c)
	}
}

// noise. size: speck px (default 1). seed: required for a stable look. Pcts[0] (density): percent lit (default 20).
// Pcts[1] (amp): luma swing applied to the speck's alpha (default 100).
func tileNoise(p GenParams) (int32, int32) { return theme.GenTileMaxPx, theme.GenTileMaxPx }

func drawNoise(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	speck := p.sizeOr(1)
	density := p.pctOr(0, 20)
	amp := p.pctOr(1, genPctMax)
	seed := p.seed()
	for y := int32(0); y < h; y += speck {
		for x := int32(0); x < w; x += speck {
			u := genUnit(x/speck, y/speck, seed)
			if u*float64(genPctMax) >= float64(density) {
				continue
			}
			// The same hash decides how lit, so there is no second seed to keep in step.
			cover := 1.0
			if amp < genPctMax {
				cover = float64(amp)/float64(genPctMax) + (1-float64(amp)/float64(genPctMax))*u
			}
			for dy := int32(0); dy < speck; dy++ {
				for dx := int32(0); dx < speck; dx++ {
					genBlend(img, x+dx, y+dy, p.tint(), cover)
				}
			}
		}
	}
	return img
}

// woodgrain. pitch: band period px (default 24). Pcts[0] (pct): contrast (default 60).
func tileWoodgrain(p GenParams) (int32, int32) {
	return theme.GenTileMaxPx, theme.GenTileMaxPx
}

func drawWoodgrain(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	period := float64(p.pitchOr(24))
	contrast := float64(p.pctOr(0, 60)) / float64(genPctMax)
	seed := p.seed()
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			// Rings: a warped coordinate through a sine, so it reads as wood rather than stripes.
			warp := genValueNoise(float64(x), float64(y), 32, w, seed)*period*0.6 +
				genValueNoise(float64(x), float64(y), 8, w, seed+1)*period*0.15
			v := 0.5 + 0.5*math.Sin((float64(y)+warp)*2*math.Pi/period)
			v = 0.5 + (v-0.5)*contrast
			// The shadow inks the dark half, so a theme picks both ends of the ramp.
			if v > 0.5 {
				genBlend(img, x, y, p.tint(), (v-0.5)*2)
			} else {
				genBlend(img, x, y, p.shadow(), (0.5-v)*2)
			}
		}
	}
	return img
}

// gradient. A ramp from tint to accent at a true arbitrary Angle, since it rasterises off the frame path.
func tileGradient(p GenParams) (int32, int32) { return theme.GenTileMaxPx, theme.GenTileMaxPx }

func drawGradient(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	a := genAngleRad(p.Angle)
	dx, dy := math.Cos(a), math.Sin(a)
	span := math.Abs(dx)*float64(w) + math.Abs(dy)*float64(h)
	if span <= 0 {
		span = 1
	}
	from, to := p.tint(), p.accent()
	if to.A == 0 && from.A != 0 {
		to = color.RGBA{R: from.R, G: from.G, B: from.B} // ramp to transparent
	}
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			t := (float64(x)*dx + float64(y)*dy) / span
			if dx < 0 {
				t += math.Abs(dx) * float64(w) / span
			}
			if dy < 0 {
				t += math.Abs(dy) * float64(h) / span
			}
			t = math.Min(1, math.Max(0, t))
			genBlend(img, x, y, genLerpColor(from, to, t), 1)
		}
	}
	return img
}

// genLerpColor interpolates straight-alpha colours including alpha, for ramps to transparent.
func genLerpColor(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.RGBA{R: m(a.R, b.R), G: m(a.G, b.G), B: m(a.B, b.B), A: m(a.A, b.A)}
}

// glow. size or radius: radius px (default 48). Pcts[0] (soft): falloff, 0 a linear ramp, 100 a long tail (default 60).
func tileGlow(p GenParams) (int32, int32) { r := p.sizeOr(48); return r * 2, r * 2 }

func drawGlow(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	cx, cy := float64(w)/2, float64(h)/2
	r := math.Min(cx, cy)
	soft := float64(p.pctOr(0, 60)) / float64(genPctMax)
	// exponent 1 at soft=0 (linear), rising to 4 at soft=100 (a long tail).
	exp := 1 + 3*soft
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) / r
			if d >= 1 {
				continue
			}
			genBlend(img, x, y, p.tint(), math.Pow(1-d, exp))
		}
	}
	return img
}

// stripes. pitch: period px (default 12). size: stripe width px (default 4). Angle: direction.
// count or bloom: halo width px in accent. Pcts[0] (fade): ends ramp out. Pcts[1] (phase): percent of a period.
func tileStripes(p GenParams) (int32, int32) {
	period := p.pitchOr(12)
	// A whole number of periods on both axes, so a diagonal stripe set repeats.
	n := theme.GenTileMaxPx / period
	if n < 1 {
		n = 1
	}
	return period * n, period * n
}

func drawStripes(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	period := float64(p.pitchOr(12))
	width := float64(p.sizeOr(4))
	bloom := float64(p.countOr(0))
	fade := float64(p.pctOr(0, 0)) / float64(genPctMax)
	phase := float64(p.pctOr(1, 0)) / float64(genPctMax) * period
	a := genAngleRad(p.Angle)
	nx, ny := math.Cos(a), math.Sin(a)
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			// Distance from the nearest stripe centre, along the stripe normal.
			d := math.Mod(float64(x)*nx+float64(y)*ny+phase, period)
			if d < 0 {
				d += period
			}
			if d > period/2 {
				d = period - d
			}
			// Fade is measured across the tile's height, so a fit = nine band can pin a solid edge and ramp the middle.
			cover := 1.0
			if fade > 0 {
				t := math.Abs(float64(y)+0.5-float64(h)/2) / (float64(h) / 2)
				cover = 1 - fade*t
			}
			switch {
			case d <= width/2:
				genBlend(img, x, y, p.tint(), cover)
			case bloom > 0 && d <= width/2+bloom:
				halo := 1 - (d-width/2)/bloom
				genBlend(img, x, y, p.accent(), cover*halo*halo)
			}
		}
	}
	return img
}

// hatch. Two stripe sets at +/-Angle. pitch: period (default 8). size: line width (default 1).
func tileHatch(p GenParams) (int32, int32) { c := p.pitchOr(8); return c * 2, c * 2 }

func drawHatch(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	period := float64(p.pitchOr(8))
	width := float64(p.sizeOr(1))
	a := genAngleRad(p.Angle)
	if p.Angle == 0 {
		a = math.Pi / 4 // the default hatch is 45°, which is what makes it read as hatch
	}
	for _, n := range [2][2]float64{{math.Cos(a), math.Sin(a)}, {math.Cos(-a), math.Sin(-a)}} {
		for y := int32(0); y < h; y++ {
			for x := int32(0); x < w; x++ {
				d := math.Mod(float64(x)*n[0]+float64(y)*n[1], period)
				if d < 0 {
					d += period
				}
				if d > period/2 {
					d = period - d
				}
				if d <= width/2 {
					genBlend(img, x, y, p.tint(), 1)
				}
			}
		}
	}
	return img
}

// plate. A filled tile with a decorated edge, 9-sliced so the edge keeps a fixed size.
// pitch: groove period px (0 is flat).
// size: edge px (default 6). count: 0 bevel, 1 chamfer, 2 capsule, 3 notch. Angle: light. Pcts[0] (pct): default 60.
func tilePlate(p GenParams) (int32, int32) {
	e := 2 * (p.sizeOr(6) + p.pitchOr(0))
	return clampTilePx(e), clampTilePx(e)
}

func drawPlate(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	edge := float64(p.sizeOr(6))
	mode := p.countOr(0) % 4
	strength := float64(p.pctOr(0, 60)) / float64(genPctMax)
	groove := p.pitchOr(0)
	// The face grooves first, so the edge treatment lands on top of them.
	if groove > 0 {
		for y := int32(0); y < h; y += groove {
			for x := int32(0); x < w; x++ {
				genBlend(img, x, y, p.accent(), strength*0.4)
			}
		}
	}
	lit := math.Cos(genAngleRad(p.Angle))
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			// Distance to the nearest tile edge, which is what every mode shapes.
			d := math.Min(math.Min(float64(x), float64(w-1-x)), math.Min(float64(y), float64(h-1-y)))
			if d >= edge {
				continue
			}
			t := 1 - d/edge
			switch mode {
			case 1: // chamfer: a 45° corner cut, so the CORNERS are removed
				if float64(x)+float64(y) < edge || float64(w-1-x)+float64(h-1-y) < edge {
					genBlend(img, x, y, color.RGBA{}, 1)
					continue
				}
				genBlend(img, x, y, p.accent(), t*strength)
			case 2: // capsule: rounded ends, so the corner radius IS the edge extent
				cx := math.Min(float64(x), float64(w-1-x))
				cy := math.Min(float64(y), float64(h-1-y))
				if cx < edge && cy < edge && math.Hypot(edge-cx, edge-cy) > edge {
					genBlend(img, x, y, color.RGBA{}, 1)
					continue
				}
				genBlend(img, x, y, p.accent(), t*strength)
			case 3: // notch: a rail, so only the top and bottom edges are decorated
				if float64(y) < edge || float64(h-1-y) < edge {
					genBlend(img, x, y, p.shadow(), t*strength)
				}
			default: // bevel: highlight on the lit side, shadow on the other
				top := float64(y) < edge || float64(x) < edge
				ink := p.shadow()
				if (top && lit >= 0) || (!top && lit < 0) {
					ink = p.tint()
				}
				genBlend(img, x, y, ink, t*strength)
			}
		}
	}
	return img
}

// frame. A hollow tile whose band keeps a fixed size under fit = nine. pitch: band px (default 2).
// size: corner px (default 12).
// count: 0 corner brackets, 1 box, 2 rail, 3 bezel. Pcts[1] (bow): mode 3 only, how much the moulding thins at
// mid-edge.
// Pcts[0] (gap): percent of each half-edge left open from the middle; 100 leaves nothing, not corners only.
// Mode 0 with no gap draws corner brackets from its size-derived default.
func tileFrame(p GenParams) (int32, int32) {
	e := 2 * (p.pitchOr(2) + p.sizeOr(12))
	return clampTilePx(e), clampTilePx(e)
}

func drawFrame(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	band := float64(p.pitchOr(2))
	corner := float64(p.sizeOr(12))
	mode := p.countOr(0) % 4
	gap := float64(p.pctOr(0, 0)) / float64(genPctMax)
	bow := 0.0
	if mode == 3 {
		bow = float64(p.pctOr(1, 0)) / float64(genPctMax)
		// Only the bezel has a case fill; the others are hollow.
		genFill(img, p.bg())
	}
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			dx := math.Min(float64(x), float64(w-1-x))
			dy := math.Min(float64(y), float64(h-1-y))
			d := math.Min(dx, dy)
			// The moulding thins toward mid-edge; squaring along makes the opening a curve rather than a chamfer.
			thick := band
			if bow > 0 {
				a := frameAlong(x, y, w, h, dx, dy)
				thick = band * (1 - bow*(1-a*a))
			}
			if d >= thick {
				continue
			}
			// The gap opens each edge from its middle, and open is the fraction of the half-edge: 100 empties the band.
			// Mode 0 with no gap derives open from size; at size >= 128 it reaches 0 and the brackets close into a box.
			if mode == 0 || gap > 0 {
				along := frameAlong(x, y, w, h, dx, dy)
				open := gap
				if mode == 0 && open <= 0 {
					open = 1 - corner/math.Min(float64(w), float64(h))*2
				}
				if along < open {
					continue
				}
			}
			if mode == 2 && dy >= dx {
				continue // a rail decorates the horizontal edges only
			}
			genBlend(img, x, y, p.tint(), 1)
		}
	}
	// The secondary rule, one band inside the first.
	if p.accent().A > 0 && band >= 1 {
		inset := int32(band) + 1
		for y := inset; y < h-inset; y++ {
			genBlend(img, inset, y, p.accent(), 1)
			genBlend(img, w-1-inset, y, p.accent(), 1)
		}
		for x := inset; x < w-inset; x++ {
			genBlend(img, x, inset, p.accent(), 1)
			genBlend(img, x, h-1-inset, p.accent(), 1)
		}
	}
	return img
}

// frameAlong is how far along its nearest edge a pixel sits: 0 at mid-edge, 1 at a corner.
// dy < dx means the top or bottom is nearest, and position along a horizontal edge is the column.
func frameAlong(x, y, w, h int32, dx, dy float64) float64 {
	if dy < dx {
		return math.Abs(float64(x)+0.5-float64(w)/2) / (float64(w) / 2)
	}
	return math.Abs(float64(y)+0.5-float64(h)/2) / (float64(h) / 2)
}

// radial. One polar function. pitch: ray count (default 12). size: outer radius px. count: repeats (default 1).
// seed: jitter.
// Pcts[0] (inner): inner radius percent of outer; 100 hairlines, 0 full wedges.
// Pcts[1] (squash): 100 circle, 45 perspective.
func tileRadial(p GenParams) (int32, int32) {
	r := p.sizeOr(64)
	n := int32(value.Clamp(int(p.countOr(1)), 1, int(genRepeatCap)))
	return clampTilePx(r * 2 * n), clampTilePx(r * 2 * n)
}

func drawRadial(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	// Both are clamped: they decide how many figures get drawn, so a hostile file would reach for them.
	reps := int32(value.Clamp(int(p.countOr(1)), 1, int(genRepeatCap)))
	rays := int32(value.Clamp(int(p.pitchOr(12)), 1, int(genFigureCap)))
	inner := float64(p.pctOr(0, 0)) / float64(genPctMax)
	squash := float64(p.pctOr(1, genPctMax)) / float64(genPctMax)
	if squash <= 0 {
		squash = 1
	}
	seed := p.seed()
	cellW, cellH := float64(w)/float64(reps), float64(h)/float64(reps)
	rOut := math.Min(cellW, cellH) / 2
	for ry := int32(0); ry < reps; ry++ {
		for rx := int32(0); rx < reps; rx++ {
			cx := cellW*float64(rx) + cellW/2
			cy := cellH*float64(ry) + cellH/2
			// The disc behind the rays, when the theme declared an accent for it.
			if p.accent().A > 0 {
				genDisc(img, cx, cy, rOut*squash, p.accent())
			}
			jitter := genUnit(rx, ry, seed) * 2 * math.Pi / float64(max(rays, 1))
			for i := int32(0); i < rays; i++ {
				a := float64(i)*2*math.Pi/float64(rays) + genAngleRad(p.Angle) + jitter
				x0 := cx + rOut*inner*math.Cos(a)
				y0 := cy + rOut*inner*math.Sin(a)*squash
				x1 := cx + rOut*math.Cos(a)
				y1 := cy + rOut*math.Sin(a)*squash
				// A hairline at inner=100, a wedge as inner falls: a true wedge polygon would need a scanline fill for
				// no visible gain.
				width := 1 + (1-inner)*rOut*math.Pi/float64(max(rays, 1))
				genLine(img, x0, y0, x1, y1, width, p.tint())
			}
			if p.shadow().A > 0 {
				genRing(img, cx, cy, rOut*squash, 1, p.shadow())
			}
		}
	}
	return img
}

// genRing strokes a circle (or a squashed one, via the caller's radius).
func genRing(img *image.RGBA, cx, cy, r, width float64, c color.RGBA) {
	if r <= 0 {
		return
	}
	steps := int(math.Ceil(2 * math.Pi * r * 2))
	if steps > genStampMaxSteps {
		steps = genStampMaxSteps
	}
	for i := 0; i < steps; i++ {
		a := float64(i) * 2 * math.Pi / float64(steps)
		genDisc(img, cx+r*math.Cos(a), cy+r*math.Sin(a), width/2, c)
	}
}

// mottle. Smooth low-frequency value noise, unlike per-pixel noise. pitch: feature px. size: octaves 1..4 (default 2).
// count: speck density 0..100. seed: required. Pcts[0] (fade): vertical ramp out.
// Pcts[1] (amp): luma swing (default 100).
func tileMottle(p GenParams) (int32, int32) { return theme.GenTileMaxPx, theme.GenTileMaxPx }

func drawMottle(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	base := p.pitchOr(32)
	octaves := value.Clamp(int(p.sizeOr(2)), 1, 4)
	specks := p.countOr(0)
	fade := float64(p.pctOr(0, 0)) / float64(genPctMax)
	amp := float64(p.pctOr(1, genPctMax)) / float64(genPctMax)
	seed := p.seed()
	for y := int32(0); y < h; y++ {
		ramp := 1.0
		if fade > 0 {
			ramp = 1 - fade*float64(y)/float64(h)
		}
		for x := int32(0); x < w; x++ {
			v, weight, cell := 0.0, 0.0, base
			for o := 0; o < octaves; o++ {
				gain := 1 / math.Pow(2, float64(o))
				v += gain * genValueNoise(float64(x), float64(y), cell, w, seed+int32(o))
				weight += gain
				cell = max(cell/2, 1)
			}
			v /= weight
			ink := p.tint()
			if v < 0.5 && p.shadow().A > 0 {
				ink = p.shadow()
				v = 1 - v
			}
			genBlend(img, x, y, ink, (v-0.5)*2*amp*ramp)
			if specks > 0 && genUnit(x, y, seed+9)*float64(genPctMax) < float64(specks) {
				genBlend(img, x, y, p.accent(), ramp)
			}
		}
	}
	return img
}

// checkerdisc. A checkered disc in perspective. pitch or cells: cells across (default 6).
// size or ring: ring band px (default 8).
// Angle: rotation. Pcts[1] (squash): 100 circle, 45 perspective. bg is the dark cell; outside the disc is transparent.
func tileCheckerdisc(p GenParams) (int32, int32) {
	return theme.GenTileMaxPx, theme.GenTileMaxPx
}

func drawCheckerdisc(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h) // NO genFill: outside the disc stays transparent
	cells := float64(p.pitchOr(6))
	ring := float64(p.sizeOr(8))
	squash := float64(p.pctOr(1, genPctMax)) / float64(genPctMax)
	if squash <= 0 {
		squash = 1
	}
	cx, cy := float64(w)/2, float64(h)/2
	r := math.Min(cx, cy) - 1
	rot := genAngleRad(p.Angle)
	cos, sin := math.Cos(rot), math.Sin(rot)
	cell := 2 * r / cells
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			px, py := float64(x)+0.5-cx, (float64(y)+0.5-cy)/squash
			d := math.Hypot(px, py)
			if d > r {
				continue // outside the disc: transparent, by construction
			}
			if d > r-ring {
				genBlend(img, x, y, p.accent(), 1) // the annulus
				continue
			}
			// Rotate into the checker's frame, so angle turns the pattern rather than the disc.
			rx, ry := px*cos-py*sin, px*sin+py*cos
			ix := int32(math.Floor((rx + r) / cell))
			iy := int32(math.Floor((ry + r) / cell))
			ink := p.tint()
			if (ix+iy)&1 == 1 {
				ink = p.bg()
			}
			genBlend(img, x, y, ink, 1)
		}
	}
	if p.shadow().A > 0 {
		genRing(img, cx, cy, r, 1, p.shadow())
	}
	return img
}

// skyline. Mirrored ridge silhouettes or column bars. pitch: segment px (default 16). size: peak px (default 48).
// count: ranges 1..3. seed: required. Pcts[0] (base): baseline percent; 100 makes columns. Wraps cleanly at the edges.
func tileSkyline(p GenParams) (int32, int32) {
	return theme.GenTileMaxPx, clampTilePx(p.sizeOr(48) * 2)
}

func drawSkyline(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	seg := p.pitchOr(16)
	peak := float64(p.sizeOr(48))
	ranges := value.Clamp(int(p.countOr(1)), 1, 3)
	baseline := float64(p.pctOr(0, 0)) / float64(genPctMax)
	seed := p.seed()
	// Back to front, so the nearer range occludes the further one.
	for r := ranges - 1; r >= 0; r-- {
		ink := p.tint()
		if r > 0 && p.shadow().A > 0 {
			ink = p.shadow()
		}
		scale := 1 - 0.3*float64(r)
		for x := int32(0); x < w; x++ {
			// Mirrored about the centre column, which makes the tile symmetric.
			mx := x
			if mx >= w/2 {
				mx = w - 1 - mx
			}
			i := mx / seg
			frac := float64(mx%seg) / float64(seg)
			a := genUnit(i, int32(r), seed)
			b := genUnit(i+1, int32(r), seed)
			// Columns (baseline high) step; peaks interpolate.
			var top float64
			if baseline >= 1 {
				top = float64(h) - a*peak*scale
			} else {
				v := a*(1-frac) + b*frac
				top = float64(h) - (baseline*float64(h) + v*peak*scale)
			}
			for y := int32(math.Max(0, top)); y < h; y++ {
				genBlend(img, x, y, ink, 1)
			}
			if p.accent().A > 0 && top >= 0 {
				genBlend(img, x, int32(top), p.accent(), 1) // rim light
			}
		}
	}
	return img
}

// wingmark. Scattered two-lobe marks. size: mark px (default 12). count: marks per tile (default 6). seed: required.
// Angle: heading. Pcts[1] (amp or full): 100 is a butterfly, 0 thin arcs.
// Each mark is stamped nine times to wrap at the edges.
func tileWingmark(p GenParams) (int32, int32) {
	return theme.GenTileMaxPx, theme.GenTileMaxPx
}

func drawWingmark(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	// Bounded by half the tile: a bigger mark cannot read as a mark, and its arcs drive genLine's walk.
	size := float64(min(p.sizeOr(12), theme.GenTileMaxPx/2))
	marks := int32(value.Clamp(int(p.countOr(6)), 1, int(genFigureCap)))
	full := float64(p.pctOr(1, genPctMax)) / float64(genPctMax)
	seed := p.seed()
	for i := int32(0); i < marks; i++ {
		mx := genUnit(i, 0, seed) * float64(w)
		my := genUnit(i, 1, seed) * float64(h)
		heading := genAngleRad(p.Angle) + (genUnit(i, 2, seed)-0.5)*0.8
		scale := 0.6 + 0.8*genUnit(i, 3, seed)
		reach := size * scale
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				cx, cy := mx+float64(int32(dx)*w), my+float64(int32(dy)*h)
				// Skip wrap copies that cannot touch the tile; usually eight of the nine.
				if cx+reach < 0 || cx-reach > float64(w) || cy+reach < 0 || cy-reach > float64(h) {
					continue
				}
				genWing(img, cx, cy, reach, heading, full, p)
			}
		}
	}
	return img
}

// genWing stamps one two-lobe mark: two arcs sweeping out from a shared body.
func genWing(img *image.RGBA, cx, cy, size, heading, full float64, p GenParams) {
	const arcSteps = 10
	for _, side := range [2]float64{1, -1} {
		var px, py float64
		for i := 0; i <= arcSteps; i++ {
			t := float64(i) / arcSteps
			// A lobe at full=1, a thin arc at full=0: the lobe's width is what changes.
			spread := (0.35 + 0.65*full) * math.Pi / 2
			a := heading + side*spread*t
			r := size * math.Sin(math.Pi*t) * (0.4 + 0.6*full)
			if full < 0.2 {
				r = size * t // a gull's arc is a straight taper, not a lobe
			}
			x := cx + r*math.Cos(a)
			y := cy + r*math.Sin(a)
			if i > 0 {
				genLine(img, px, py, x, y, 1+full*2, p.tint())
			}
			px, py = x, y
		}
	}
	if p.accent().A > 0 {
		genDisc(img, cx, cy, 1+full, p.accent())
	}
}
