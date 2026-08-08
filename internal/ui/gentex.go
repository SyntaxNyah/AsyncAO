package ui

// Procedural generators — the RASTERISER half (v1.90.0 W4).
// docs/wip/THEME-EDITOR-DESIGN.md §6.3, docs/THEME-FORMAT.md §5.
//
// A `kind = gen` element names one of these and hands it an ordered `k = v` list.
// The SPEC (name, params, content-address hash) is internal/theme/generator.go, and
// it is SDL-free by hard rule 1; this file is the arithmetic that turns it into
// pixels. Nothing here touches SDL either — the output is a plain *image.RGBA that
// pollThemeApply uploads exactly like a decoded asset.
//
// THE CONTRACT, all four clauses gated:
//
//  1. PURE AND DETERMINISTIC. The same (w, h, params) produce a BYTE-IDENTICAL
//     image. This is not a nicety: the param hash IS the cache key, so a
//     non-deterministic generator has the cache serve whichever raster happened to
//     land first, forever, and the theme looks different depending on which element
//     the apply reached first. No time.Now, no math/rand without an explicit seed
//     param, no package-level mutable state, no map iteration inside a raster.
//     (TestEveryGeneratorIsDeterministic.)
//  2. A TILE, NEVER A FIELD. w and h are at most theme.GenTileMaxPx. The ELEMENT
//     tiles, stretches or 9-slices it. That single decision is what makes generators
//     affordable: a full-screen scanline field is 3.7 MiB and re-rasters on every
//     resize; the same look as a 256² tile is 256 KiB and survives every resize
//     untouched. (TestGeneratorTileNeverExceedsMaxPx.)
//  3. IT RUNS ON THE THEME-APPLY GOROUTINE, never the render thread. Contrast
//     shapemask.go, whose masks are tiny and chrome-preset-driven; a generator tile
//     is up to 256² and parameterised, so it is cold-path work by construction —
//     which is also why frame-path allocation is impossible here rather than merely
//     avoided.
//  4. IT ALLOCATES EXACTLY ONE *image.RGBA. No scratch buffers, no per-column
//     slices: every generator below computes each pixel from its coordinates, its
//     params and a seeded integer hash, so the only allocation is the tile.
//
// WIRE NAMES ARE THE FORMAT: append-only, never renamed, never repurposed. An
// unknown name degrades to a flat fill and the element still draws (format rule 3).
// TestEveryGeneratorKindRasterizes is the totality gate.

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// genTileMinPx is the smallest tile any generator may return. Below a few pixels a
// "pattern" is a solid colour with rounding noise in it, and a 1px tile stretched
// over a canvas is a fill wearing a generator's name.
const genTileMinPx = int32(4)

// genPctMax is the top of every 0..100 parameter. Named because eleven generators
// divide by it.
const genPctMax = int32(100)

// THE HOSTILE-PARAMETER BOUNDS. Every number a generator reads arrives from a file
// a stranger wrote, and a tile is at most theme.GenTileMaxPx on a side — so
// `pitch = 100000` is not a big pattern, it is a denial of service on the
// theme-apply goroutine.
//
// It was exactly that, and the hostile row of TestGeneratorTileNeverExceedsMaxPx is
// what found it: halftone's dot radius reached 34 000 px and genDisc walked a
// 68 000² coordinate box clipping every pixel, to paint at most 65 536 of them. The
// TILE cap does not bound the work, because the work is a function of the PARAMS
// rather than of the output size. These four are what bound it.
const (
	// genIntParamMax clamps pitch / size / count at parse time. Four tiles' worth:
	// past it a measurement cannot describe anything inside a 256 px tile, and every
	// generator's arithmetic stays well inside int32.
	genIntParamMax = int32(4 * theme.GenTileMaxPx)
	// genFigureCap bounds how many discrete FIGURES one tile may hold — rays,
	// scattered marks. A tile with more figures than it has pixel columns is a solid
	// fill drawn the expensive way.
	genFigureCap = int32(128)
	// genRepeatCap bounds how many times a figure is packed into one tile. 8×8 is 64
	// cells, past the point where a repeat still reads as a repeat.
	genRepeatCap = int32(8)
	// genStampMaxSteps bounds genLine's and genRing's walks. A segment longer than a
	// few tile diagonals is mostly outside the tile whatever it does, and the walk is
	// what turns an absurd length into absurd time.
	genStampMaxSteps = 4 * theme.GenTileMaxPx
	// genHalftoneDotPct is halftone's dot diameter as a percent of its cell when the
	// element does not say. 68 is exactly the 0.34 radius the generator drew before
	// the knob existed, so adding the parameter changed no shipped pixel by itself.
	genHalftoneDotPct = int32(68)
)

// GenParams is the fixed, TYPED view over a generator's `k = v` list. A generator
// never parses a string: the whole list is resolved once, at bake, into these
// eleven slots, and a key nobody wrote keeps the generator's documented default.
//
// ELEVEN SLOTS FOR TWENTY-FIVE WIRE KEYS, because the keys are per-generator names
// for the same few roles — `cells` and `pitch` are both "how often", `radius` and
// `size` are both "how big". genParamSlots is the whole mapping and it is GLOBAL
// rather than per-generator, which is checkable (TestGeneratorParamKeysDoNotCollide
// proves no generator's own vocabulary lands two keys in one slot) where a
// per-generator table would be twenty-five more things to keep in step.
type GenParams struct {
	// Colors are tint, bg, accent, shadow — in that order, always. A generator that
	// wants "the ink" reads Colors[0] whatever the preset called it.
	Colors [4]color.RGBA
	// Ints are pitch, size, count, seed.
	Ints [4]int32
	// Angle is the 360/256 angle byte — the ThemeRectRotations convention, so 64 is
	// 90 degrees.
	Angle uint8
	// Pcts are the two 0..100 knobs. Which is which is the generator's own business
	// and is documented at each one.
	Pcts [2]int16
}

// Named accessors, so a raster reads as what it means rather than as an index.
func (p GenParams) tint() color.RGBA   { return p.Colors[0] }
func (p GenParams) bg() color.RGBA     { return p.Colors[1] }
func (p GenParams) accent() color.RGBA { return p.Colors[2] }
func (p GenParams) shadow() color.RGBA { return p.Colors[3] }
func (p GenParams) pitch() int32       { return p.Ints[0] }
func (p GenParams) size() int32        { return p.Ints[1] }
func (p GenParams) count() int32       { return p.Ints[2] }
func (p GenParams) seed() int32        { return p.Ints[3] }

// pitchOr / sizeOr / countOr return the parameter or the generator's documented
// default when the author wrote nothing (0 is the absent marker for every int
// param, exactly as it is for opacity in the format).
func (p GenParams) pitchOr(def int32) int32 { return genIntOr(p.pitch(), def) }
func (p GenParams) sizeOr(def int32) int32  { return genIntOr(p.size(), def) }
func (p GenParams) countOr(def int32) int32 { return genIntOr(p.count(), def) }

func genIntOr(v, def int32) int32 {
	if v <= 0 {
		return def
	}
	return v
}

// pctOr returns Pcts[i] or a default when the author wrote nothing. 0 is a LEGAL
// value for every percent, so "absent" has to be distinguishable from "zero" —
// genParamSet records which slots were actually written.
func (p GenParams) pctOr(i int, def int32) int32 {
	if p.Pcts[i] < 0 {
		return def // the absent marker; see genParseParams
	}
	return int32(p.Pcts[i])
}

// genParamAbsent is what an unwritten percent holds. -1 rather than 0 because 0 is
// a legitimate authored value ("no fade", "no perspective") and the two must not
// mean the same thing.
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

// genParamSlots maps every wire key any generator accepts onto its slot.
//
// APPEND-ONLY, like every other wire-name table in this format: a key that once
// resolved has to keep resolving, or a theme authored against that build loses the
// parameter silently (an unknown key is ignored — there is no note, because a
// generator "ignores what it does not use" by design).
//
// The aliases are not synonyms invented here; every one of them is a spelling the
// fourteen shipped themes and the preset drafts already write, and the merge that
// produced them is docs/wip/preset-drafts/GENERATOR-AND-EFFECT-PROPOSALS.md §1.
var genParamSlots = map[string]genParamSlot{
	// Colours — one role each, in every generator.
	"tint": genSlotTint, "bg": genSlotBG, "accent": genSlotAccent, "shadow": genSlotShadow,
	// Integers. `cells` (checkerdisc) and `dot` (halftone) are that generator's own
	// word for a pitch; `radius` (glow) and `ring` (checkerdisc) for a size;
	// `bloom` (stripes) for a count.
	"pitch": genSlotPitch, "cells": genSlotPitch, "dot": genSlotPitch,
	"size": genSlotSize, "radius": genSlotSize, "ring": genSlotSize,
	"count": genSlotCount, "bloom": genSlotCount,
	"seed":  genSlotSeed,
	"angle": genSlotAngle,
	// Percents. Which generator reads which is documented at each raster.
	"pct": genSlotPct0, "inner": genSlotPct0, "density": genSlotPct0,
	"gap": genSlotPct0, "soft": genSlotPct0, "base": genSlotPct0, "fade": genSlotPct0,
	"squash": genSlotPct1, "amp": genSlotPct1, "phase": genSlotPct1, "dots": genSlotPct1,
	// `full` (wingmark's lobe fullness) and `bow` (the bezel's barrel) are the two
	// keys the shipped preset fragments write that this table did not carry. `full`
	// was already NAMED in drawWingmark's own doc comment as the spelling of
	// Pcts[1] — the raster implemented it and the parser threw the key away, so
	// `full = 0` rendered as `full = 100` on all three of umineko's marks. An
	// unrecognised key is ignored with no note anywhere, which is how a wiring gap
	// this size survived a whole wave; TestEveryShippedGenParamResolvesToASlot is
	// the gate that makes the next one impossible.
	"full": genSlotPct1, "bow": genSlotPct1,
}

// GenFunc rasterises one tile. See the contract at the top of the file.
type GenFunc func(w, h int32, p GenParams) *image.RGBA

// genEntry is one registry row: how big the tile is, and how to fill it. The size
// is a separate function because the caller has to allocate before it draws, and
// because clause 2 is checked on the SIZE rather than on the pixels.
type genEntry struct {
	tile func(p GenParams) (int32, int32)
	draw GenFunc
}

// genRegistry is indexed by WIRE NAME. Seventeen entries: the ten the design
// planned, plus the seven the preset workflow's merge produced (`plate`, `frame`,
// `radial`, `mottle`, `checkerdisc`, `skyline`, `wingmark`) — each of which
// replaces between two and six independently-invented names, so the count is a
// merge result rather than a wish list.
//
// A map rather than an array because the key is a string from a file; it is read
// once per element per theme apply, on the apply goroutine, never on a frame.
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

// GeneratorNames returns the registry's wire names. Diagnostics, docs census and
// the editor's picker; never a draw path (it allocates and the order is a map's).
func GeneratorNames() []string {
	out := make([]string, 0, len(genRegistry))
	for name := range genRegistry {
		out = append(out, name)
	}
	return out
}

// genLookup is THE registry probe: one spelling of "does this wire name rasterise,
// and with what". Every one of the three public entry points goes through it.
//
// It exists because they did not, and the divergence was live: GeneratorKnown and
// GenTileSize normalised the name while RasterGenerator indexed the map RAW. A spec
// named "Scanlines" was therefore known, sized, planned and given one of the twelve
// ThemeGenCap slots — and then rasterised nil, so the element drew blank with
// nothing anywhere to say why. Latent only because theme.GeneratorSpecOf (which
// lower-cases) is today's sole spec producer; W7's editor picker is the second.
func genLookup(name string) (genEntry, bool) {
	e, ok := genRegistry[theme.NormalizeGenName(name)]
	return e, ok
}

// GeneratorKnown reports whether a wire name rasterises. An unknown one is not an
// error — the element degrades to a flat fill of its own `fill` colour — but the
// import report says so, which is the difference between a theme that looks
// slightly plainer on an older client and one nobody can debug.
func GeneratorKnown(name string) bool {
	_, ok := genLookup(name)
	return ok
}

// genTileSizeOf is the tile ONE SPEC would rasterise to, already clamped into
// [genTileMinPx, theme.GenTileMaxPx]. The admission planner budgets a generator
// with it without rasterising anything.
//
// It takes the SPEC, not (name, params), because the two-argument form permitted
// GenTileSize(nameA, paramsOfB) — budget one tile, raster another — and the budget
// is the only thing standing between a stranger's theme and the texture store. The
// name and the params of one generator now cannot be separated by a caller.
func genTileSizeOf(g theme.GeneratorSpec) (int32, int32) {
	e, ok := genLookup(g.Name)
	if !ok {
		return 0, 0
	}
	w, h := e.tile(genParseParams(g))
	return clampTilePx(w), clampTilePx(h)
}

// clampTilePx enforces clause 2 for every generator at once, so no individual
// `tile` function can breach the cap by getting its own arithmetic wrong.
func clampTilePx(v int32) int32 {
	if v < genTileMinPx {
		return genTileMinPx
	}
	if v > theme.GenTileMaxPx {
		return theme.GenTileMaxPx
	}
	return v
}

// RasterGenerator rasterises a spec, or returns nil for a name this build does not
// know. Runs on the theme-apply goroutine (clause 3).
func RasterGenerator(g theme.GeneratorSpec) *image.RGBA {
	e, ok := genLookup(g.Name) // one probe, shared with GeneratorKnown/genTileSizeOf
	if !ok {
		return nil
	}
	p := genParseParams(g)
	w, h := e.tile(p)
	return e.draw(clampTilePx(w), clampTilePx(h), p)
}

// genParseParams resolves the spec's ordered k=v list into the typed view. Cold
// path: it parses strings, which is exactly what the generators must never do.
//
// LATER WINS on a repeated key, which is the same rule the INI reader applies to a
// repeated key in a section — one file, one convention.
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
			// The seed is the ONE int that is not clamped: it is a hash input, not a
			// measurement, and every value of it is equally legal.
			p.Ints[genSlotSeed-genSlotPitch] = int32(genAtoi(kv.Value))
		case genSlotPitch, genSlotSize, genSlotCount:
			p.Ints[slot-genSlotPitch] = int32(clampInt(genAtoi(kv.Value), 0, int(genIntParamMax)))
		case genSlotAngle:
			p.Angle = uint8(genAtoi(kv.Value) % theme.AngleCount)
		case genSlotPct0, genSlotPct1:
			p.Pcts[slot-genSlotPct0] = int16(clampInt(genAtoi(kv.Value), 0, int(genPctMax)))
		}
	}
	return p
}

// genParseColor accepts the format's own colour grammar. It delegates to
// internal/theme so a generator param and an element's `fill` can never disagree
// about what "#1C1C1C" means — the upper-case-hex defect that silently dropped four
// of themes/thh_trial's palette roles was exactly that kind of divergence.
func genParseColor(v string) (color.RGBA, bool) {
	c, ok := theme.ParseRGBA(v)
	if !ok {
		return color.RGBA{}, false
	}
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}, true
}

// genAtoi is a tolerant integer parse: anything unreadable is 0, which every
// generator then replaces with its documented default. Matches how AO2 reads its
// own numeric INI values (QString::toInt yields 0 and the caller decides).
func genAtoi(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return n
}

// ---------------------------------------------------------------------------
// Shared arithmetic
// ---------------------------------------------------------------------------

// genHash is the deterministic value source every seeded generator draws from: an
// integer avalanche over (x, y, seed). Chosen over math/rand for the reason clause 1
// gives — a generator seeded from a stream would depend on the ORDER its pixels were
// visited, so a future loop reorder would silently change every seeded tile.
// Splitmix64's finalizer, which is the standard well-mixed 64-bit avalanche.
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

// genValueNoise is smooth value noise at feature size `cell`: bilinear interpolation
// between lattice values, with a smoothstep so the result has no visible grid. It
// TORUS-WRAPS on `period` so a tile made of it repeats seamlessly, which is what
// `fit = tile` requires and what a naive noise field cannot give.
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

// genFill paints the whole tile one colour — every generator's first act, so the
// transparent case (a bg nobody declared) is uniform.
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

// genBlend composites src OVER the pixel at (x, y) with an extra coverage factor in
// [0, 1]. Straight (non-premultiplied) alpha, because that is what
// render.TextureStore.buildPage uploads and what SDL's BLENDMODE_BLEND expects.
//
// Out of bounds is a no-op rather than a panic: the shape generators below walk
// analytic curves whose rounding can step one pixel past an edge, and clamping at
// the writer is one check instead of one at every caller.
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

// ---------------------------------------------------------------------------
// The seventeen
// ---------------------------------------------------------------------------

// scanlines — VHS/CRT, Steins;Gate, Cyberpunk.
// pitch: line period px (default 4). size: line thickness px (default 1).
// Pcts[0] (`fade`): 0..100, how far the line's edge softens.
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
			// Ramp the last row of the band toward transparent, so a thick line reads
			// as a phosphor bar rather than as a hard stripe.
			cover = 1 - soft*float64(y)/float64(thick-1)
		}
		for x := int32(0); x < w; x++ {
			genBlend(img, x, y, p.tint(), cover)
		}
	}
	return img
}

// halftone — Newspaper, Manga screentone.
// pitch/`dot`: cell size px (default 6). Angle staggers the lattice.
// Pcts[0] (`pct`): DOT DIAMETER as a percent of the cell (default
// genHalftoneDotPct). Coverage is pi/4 * (pct/100)^2, which is the number every
// screentone reference is quoted in.
//
// THE SECOND KNOB EXISTS BECAUSE FIVE SHIPPED ELEMENTS NEED IT. `pitch` and `dot`
// are aliases for ONE slot, so `pitch = 6, dot = 2` resolved to pitch 2 and the
// authored lattice was silently thrown away — in three presets whose comments
// derive their measured coverage from the RATIO of the two. The ratio is what
// they were actually specifying, so it gets a slot of its own rather than a
// second integer; the default is the 0.34 radius the generator always drew, so
// an element that says nothing renders byte-identically to before.
//
// The angle STAGGERS rather than rotates, and that is a deliberate limit rather than
// an approximation: a rotated dot lattice cannot repeat seamlessly inside an
// axis-aligned tile, so a true rotation would need a field, which is the one thing
// clause 2 forbids. A staggered lattice is what a screentone reads as at tile size.
func tileHalftone(p GenParams) (int32, int32) { c := p.pitchOr(6); return c * 2, c * 2 }

func drawHalftone(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	// Bounded by the TILE for the same reason the hex lattice is: the dot radius is
	// a third of the cell, and a cell larger than the tile makes it the disc
	// rasteriser's whole cost with none of it visible.
	cell := min32(p.pitchOr(6), theme.GenTileMaxPx)
	// Diameter as a percent of the cell, so the radius is pct/200 of it.
	radius := float64(cell) * float64(p.pctOr(0, genHalftoneDotPct)) / 200
	// The stagger is the angle's sine, so 0 is a square lattice and 64 (90°) is the
	// maximum half-cell offset.
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

// genDisc paints an anti-aliased filled circle. Shared by four generators, so the
// coverage rule (distance to the edge, clamped to one pixel) is written once.
func genDisc(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	if r <= 0 {
		return
	}
	// CLAMPED TO THE TILE before the loop, not inside genBlend. Clipping per pixel is
	// correct and useless: it still visits every coordinate in the disc's bounding
	// box, which for a hostile radius is billions of them for a 256² tile.
	b := img.Bounds()
	x0 := max32(int32(math.Floor(cx-r-1)), int32(b.Min.X))
	x1 := min32(int32(math.Ceil(cx+r+1)), int32(b.Max.X)-1)
	y0 := max32(int32(math.Floor(cy-r-1)), int32(b.Min.Y))
	y1 := min32(int32(math.Ceil(cy+r+1)), int32(b.Max.Y)-1)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			genBlend(img, x, y, c, r-d+0.5)
		}
	}
}

// grid — Vaporwave floor, Cyberpunk wireframe, perfboard.
// pitch: cell px (default 16). size: line width px (default 1).
// Pcts[0] (`pct`): perspective strength — rows bunch toward the top (design §6.6 R6:
// perspective FOLDS INTO grid; `pgrid` does not exist).
// Pcts[1] (`dots`): 0..100; above 50 only the intersections are drawn.
func tileGrid(p GenParams) (int32, int32) { c := p.pitchOr(16); return c, c }

func drawGrid(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	line := p.sizeOr(1)
	persp := float64(p.pctOr(0, 0)) / float64(genPctMax)
	dotsOnly := p.pctOr(1, 0) > 50
	// Perspective bunches the horizontal rules toward the top of the tile: the row
	// at t is placed at t^(1+3*persp), which is the identity at persp=0.
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

// hex — Danganronpa chatbox, Cyberpunk.
// pitch: cell width px (default 20). size: line width px (default 1).
func tileHex(p GenParams) (int32, int32) {
	c := p.pitchOr(20)
	// A flat-top hex lattice repeats over (3/2 w, sqrt(3) w) — rounded to integers,
	// because a tile is pixels.
	return c * 3 / 2, int32(math.Round(float64(c) * math.Sqrt2))
}

func drawHex(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	line := float64(p.sizeOr(1))
	// Bounded by the TILE: a hexagon larger than the tile it lives in is a straight
	// line through it, and its circumradius is what genLine's walk is a function of.
	r := float64(min32(p.pitchOr(20), theme.GenTileMaxPx)) / 2
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

// genLine strokes a segment by walking it at half-pixel steps and stamping a disc.
// Simple, allocation-free and correct at any angle — which matters because six of
// the generators here draw lines that are not axis-aligned.
func genLine(img *image.RGBA, x0, y0, x1, y1, width float64, c color.RGBA) {
	dx, dy := x1-x0, y1-y0
	r := width / 2
	if r < 0.5 {
		r = 0.5
	}
	// SPACING SCALES WITH THE PEN. Overlapping discs stay connected as long as the
	// spacing is at most the radius; stepping every half pixel regardless is correct
	// and quadratic — a 1024 px pen over a 128 px edge was 256 stamps of a
	// tile-filling disc, which is how `hex` alone took 58 seconds of the wave's test
	// budget on one hostile parameter row.
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

// noise — VHS grain, Higurashi.
// size: speck px (default 1). seed: REQUIRED for a stable look across runs.
// Pcts[0] (`density`): 0..100 percent of specks lit (default 20).
// Pcts[1] (`amp`): 0..100 luma swing applied to the speck's alpha (default 100).
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
			// The second draw from the same lattice point decides HOW lit — one hash,
			// two independent-enough bits, no second seed to keep in step.
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

// woodgrain — Classic Courtroom (banded fBm).
// pitch: band period px (default 24). Pcts[0] (`pct`): contrast (default 60).
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
			// Rings: a warped coordinate run through a sine, which is what makes wood
			// read as wood rather than as stripes.
			warp := genValueNoise(float64(x), float64(y), 32, w, seed)*period*0.6 +
				genValueNoise(float64(x), float64(y), 8, w, seed+1)*period*0.15
			v := 0.5 + 0.5*math.Sin((float64(y)+warp)*2*math.Pi/period)
			v = 0.5 + (v-0.5)*contrast
			// Two inks: the grain (tint) over the ground, with the shadow used for the
			// dark half so a theme can pick both ends of the ramp.
			if v > 0.5 {
				genBlend(img, x, y, p.tint(), (v-0.5)*2)
			} else {
				genBlend(img, x, y, p.shadow(), (0.5-v)*2)
			}
		}
	}
	return img
}

// gradient — everything.
// A two-stop ramp from tint to accent across the tile at Angle. Unlike the ELEMENT's
// own two-stop plate (paintElementGradient), this one is a genuine arbitrary angle:
// it is rasterised once, off the frame path, so there is no zero-allocation budget
// to respect and no rotated geometry to submit.
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

// genLerpColor interpolates two straight-alpha colours, ALPHA INCLUDED — a ramp to
// transparent is the vignette idiom and the reason theme.RGBA carries alpha.
func genLerpColor(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.RGBA{R: m(a.R, b.R), G: m(a.G, b.G), B: m(a.B, b.B), A: m(a.A, b.A)}
}

// glow — Cyberpunk neon, Umineko gold (radial falloff).
// size/`radius`: radius px (default 48). Pcts[0] (`soft`): falloff exponent
// 0 = hard disc, 100 = a long soft tail (default 60).
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

// stripes — Persona 5, warning tape, hairlines, phosphor rules.
// pitch: period px (default 12). size: stripe width px (default 4).
// Angle: stripe direction. count/`bloom`: halo width px, painted in `accent`.
// Pcts[0] (`fade`): 0..100, ends ramp to transparent.
// Pcts[1] (`phase`): 0..100 of one period, so two stripe elements can interleave.
func tileStripes(p GenParams) (int32, int32) {
	period := p.pitchOr(12)
	// The tile has to be a whole number of periods on BOTH axes for a diagonal
	// stripe set to repeat, so it is the period squared up, clamped by the cap.
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
			// The ends-fade is measured across the tile's own height, so a `fit = nine`
			// band can pin a solid edge and let the middle ramp out.
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

// hatch — the over-budget placeholder, and Manga tone.
// Two stripe sets at ±Angle. pitch: period (default 8). size: line width (default 1).
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

// plate — the merge of bevelplate / slabcut / capsule / notchband.
// A filled rectangle tile with a decorated edge, drawn 9-sliced so the edge keeps a
// fixed device size at any anchor size.
// pitch: groove period px (0 = flat face). size: edge extent px (default 6).
// count: edge mode — 0 bevel, 1 chamfer, 2 capsule, 3 notch. Angle: light direction.
// Pcts[0] (`pct`): bevel/keyline strength (default 60).
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

// frame — the merge of bracket / boxframe / deckframe / bezel.
// A hollow frame tile whose band keeps a fixed device size under `fit = nine`.
// pitch: band thickness px (default 2). size: corner extent px (default 12).
// count: mode — 0 corner brackets, 1 continuous box, 2 rail, 3 bezel.
// Pcts[0] (`gap`): percent of each edge left OPEN, measured over the whole
// half-edge from its middle out to the corner.
// Pcts[1] (`bow`): MODE 3 ONLY — how much the bezel's opening barrels, as the
// percent by which the moulding thins at the middle of each edge relative to its
// corners. 0 is a straight-sided bezel, which is what every other mode draws.
//
// `gap = 100` OPENS EVERYTHING, and the doc line above used to say it meant
// "corners only". It does not and cannot: the fraction is measured over the
// whole half-edge, so at 100 there is no corner left over and the band comes out
// EMPTY. Ten shipped preset elements were authored against the old sentence and
// rasterised nothing (see the W9 INK notes in presets/style/cyberpunk.ini and
// vhs_crt.ini). Corner brackets are what MODE 0 draws with no gap at all — its
// default open fraction is derived from `size`, the leg length, three lines
// down. The wording is corrected rather than the arithmetic because six other
// shipped elements write `gap = 22` and get exactly the caption-sized hole they
// ask for; re-scaling the fraction to make 100 mean something else would move
// all six to rescue ten that had a working spelling already.
// TestNoShippedPresetGeneratorElementRasterisesAnEmptyTile is what catches the
// next author who reaches for it.
//
// It is mode-3-only because that is the only mode with a case to bow: the other
// three are hollow rules and a rule that changed thickness along its length would
// read as a mistake. The reservation is the preset fragment's own (terminal's
// `crt_bezel`), and until W9 the key resolved to nothing at all.
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
		// Mode 3 (bezel) is the only one with a case fill: the others are hollow, which
		// is the whole point of a frame tile.
		genFill(img, p.bg())
	}
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			dx := math.Min(float64(x), float64(w-1-x))
			dy := math.Min(float64(y), float64(h-1-y))
			d := math.Min(dx, dy)
			// The barrel: the moulding keeps its full thickness at the corners and thins
			// toward the middle of each edge, which is the shape a tube's bezel has. The
			// square of `along` is what makes the opening a curve rather than a chamfer.
			thick := band
			if bow > 0 {
				a := frameAlong(x, y, w, h, dx, dy)
				thick = band * (1 - bow*(1-a*a))
			}
			if d >= thick {
				continue
			}
			// The gap opens each edge from its MIDDLE outward, and `open` is the
			// fraction of the WHOLE half-edge it eats: gap = 100 eats all of it and
			// leaves the band empty — it is not "corners only" (see the header).
			// Modes 1-3 draw a continuous box at gap = 0; mode 0 never does, because
			// with no gap it falls through to the `size`-derived leg default in the
			// branch below, and that default is what makes mode 0 corner brackets.
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
	// The secondary rule, one band inside the first — the inner keyline every one of
	// the four merged names drew in some form.
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

// frameAlong is how far along its NEAREST edge a pixel sits: 0 at the middle of
// that edge, 1 at a corner. The gap and the bezel's barrel both measure it, and
// one spelling is what keeps the two from disagreeing about which edge is
// nearest.
//
// THE AXES WERE SWAPPED, and `gap` did not work at all. dy is the distance to the
// nearest HORIZONTAL edge, so `dy < dx` means the top or the bottom is nearest —
// and the position along a horizontal edge is the COLUMN. The expression this was
// factored out of read the ROW there and the column on a vertical edge, which
// measures distance from the centre PERPENDICULAR to the edge: ~1 everywhere
// inside the band, on every edge. Measured on a 40 px mode-1 tile: gap = 25 and
// gap = 50 both painted all 816 band pixels (no gap at all), gap = 100 painted 0
// (it must leave the corners), and mode 0 — "corner brackets" — drew a
// continuous box. Six shipped preset elements write `gap = 22` and three write
// `gap = 100` with mode 0, so the corpus was carrying three blank elements and
// six frames that quietly ignored their own signature.
//
// THE `gap = 100` HALF OF THAT WAS NOT A BUG IN THIS FUNCTION, and the fix above
// did not cure it: with the axes correct, 100 still opens the whole half-edge and
// still paints nothing, because the fraction has no corner left over to keep —
// see the `gap` note on drawFrame. The ten shipped elements that wrote it (three
// distinct param strings) have dropped the key and use mode 0's own leg-derived
// default instead; no fragment writes `gap = 100` any more.
func frameAlong(x, y, w, h int32, dx, dy float64) float64 {
	if dy < dx {
		return math.Abs(float64(x)+0.5-float64(w)/2) / (float64(w) / 2)
	}
	return math.Abs(float64(y)+0.5-float64(h)/2) / (float64(h) / 2)
}

// radial — the merge of starburst / spikeburst / spokes / ellipsering / speedlines /
// sundisc. One polar function with six presets' worth of knobs.
// pitch: ray count (default 12). size: outer radius px (0 = fill the tile).
// count: repeats packed into the tile (default 1). seed: jitter.
// Pcts[0] (`inner`): inner radius as a percent of outer — 100 is hairlines,
// 0 is full wedges (default 0). Pcts[1] (`squash`): 100 is a circle, 45 the
// perspective ellipse (default 100).
func tileRadial(p GenParams) (int32, int32) {
	r := p.sizeOr(64)
	n := int32(clampInt(int(p.countOr(1)), 1, int(genRepeatCap)))
	return clampTilePx(r * 2 * n), clampTilePx(r * 2 * n)
}

func drawRadial(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	// Both counts are clamped, not merely defaulted: `count` and `pitch` are what
	// decide how many figures get drawn, so they are the two knobs a hostile file
	// would reach for.
	reps := int32(clampInt(int(p.countOr(1)), 1, int(genRepeatCap)))
	rays := int32(clampInt(int(p.pitchOr(12)), 1, int(genFigureCap)))
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
			jitter := genUnit(rx, ry, seed) * 2 * math.Pi / float64(max32(rays, 1))
			for i := int32(0); i < rays; i++ {
				a := float64(i)*2*math.Pi/float64(rays) + genAngleRad(p.Angle) + jitter
				x0 := cx + rOut*inner*math.Cos(a)
				y0 := cy + rOut*inner*math.Sin(a)*squash
				x1 := cx + rOut*math.Cos(a)
				y1 := cy + rOut*math.Sin(a)*squash
				// A hairline at inner=100, a wedge as inner falls: the STROKE width is
				// what carries the difference, because a true wedge polygon would need a
				// scanline fill for a shape that reads identically at tile size.
				width := 1 + (1-inner)*rOut*math.Pi/float64(max32(rays, 1))
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

// mottle — the merge of cork / paper / clouds. Smooth low-frequency value noise,
// which is genuinely a different function from `noise` (per-pixel) and not a
// parameterisation of it — all three authors said so independently.
// pitch: base feature size px (16 = newsprint, 96 = cloud, 2 = cork granule).
// size: octaves 1..4 (default 2). count: speck density 0..100 (0 = smooth).
// seed: REQUIRED. Pcts[0] (`fade`): vertical ramp to transparent.
// Pcts[1] (`amp`): luma swing (default 100).
func tileMottle(p GenParams) (int32, int32) { return theme.GenTileMaxPx, theme.GenTileMaxPx }

func drawMottle(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	base := p.pitchOr(32)
	octaves := clampInt(int(p.sizeOr(2)), 1, 4)
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
				cell = max32(cell/2, 1)
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

// checkerdisc — a checkered disc inlay in perspective. Proposed independently and
// identically by two presets, both of which call it a signature.
// pitch/`cells`: cells across the diameter (default 6). size/`ring`: ring band px
// (default 8). Angle: rotation. Pcts[1] (`squash`): 100 circle, 45 the graded
// perspective value (default 100).
//
// THE ADOPTED CLAUSE (thh_courtroom ESC-2): `bg` is the DARK CHECKER CELL, and the
// tile OUTSIDE the disc is transparent by construction. One consumer draws its disc
// over a near-black void and cannot tell the difference; the other draws it over a
// gold floor, where an opaque square would erase the floor. Gated by
// TestCheckerdiscCornersAreTransparent.
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
			// Rotate into the checker's own frame, so `angle` turns the pattern rather
			// than the disc.
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

// skyline — mirrored ridge silhouettes / column bars. One consumer, but it carries
// two of that preset's roles (mountain ranges and equalizer columns) with one
// algorithm, and nothing else in the set draws a horizon.
// pitch: ridge segment width px (default 16). size: peak height px (default 48).
// count: ranges 1..3 (default 1). seed: REQUIRED.
// Pcts[0] (`base`): baseline height as a percent of the tile — 100 makes columns
// rather than peaks (default 0).
//
// HORIZONTALLY WRAPPING and bilaterally symmetric about the tile centre, so a
// tiled range has no seam and no repeated silhouette.
func tileSkyline(p GenParams) (int32, int32) {
	return theme.GenTileMaxPx, clampTilePx(p.sizeOr(48) * 2)
}

func drawSkyline(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	seg := p.pitchOr(16)
	peak := float64(p.sizeOr(48))
	ranges := clampInt(int(p.countOr(1)), 1, 3)
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
			// Mirrored about the centre column: the silhouette of the right half is the
			// left half reversed, which is what makes the tile symmetric.
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

// wingmark — a two-lobe wing / arc mark, scattered. One consumer, kept because it
// is the design document's own worked example: §2.2's `[element.butterflies]` IS
// this element, so cutting it would mean the spec's headline example does not ship.
// size: mark size px (default 12). count: marks per tile (default 6).
// seed: REQUIRED. Angle: base heading.
// Pcts[0] (`fade`): unused. Pcts[1] (`amp` / the proposal's `full`): 100 = two full
// lobes (a butterfly), 0 = thin arcs (a gull). Default 100.
//
// TORUS-WRAPPED: every mark is stamped nine times, once per neighbouring tile, so a
// mark crossing an edge appears on both sides and `fit = tile` has no seam.
func tileWingmark(p GenParams) (int32, int32) {
	return theme.GenTileMaxPx, theme.GenTileMaxPx
}

func drawWingmark(w, h int32, p GenParams) *image.RGBA {
	img := genNewTile(w, h)
	genFill(img, p.bg())
	// Bounded by HALF the tile: a mark bigger than the tile it scatters into cannot
	// read as a mark, and its arcs are what genLine's walk is a function of.
	size := float64(min32(p.sizeOr(12), theme.GenTileMaxPx/2))
	marks := int32(clampInt(int(p.countOr(6)), 1, int(genFigureCap)))
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
				// CULL the wrap copies that cannot touch the tile. Eight of the nine
				// stamps are usually off-tile, and stamping them anyway is eight ninths
				// of this generator's cost paid for pixels nobody can see.
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
