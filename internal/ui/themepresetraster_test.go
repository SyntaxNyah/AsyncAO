package ui

// THE SHIPPED PRESETS' GENERATOR CENSUS (v1.90.0 W9).
//
// ONE concern: every `kind = gen` element in the shipped preset library must
// rasterise SOMETHING. It is deliberately not part of the coherence matrix —
// that gate answers "where does this decoration land", this one answers "is
// there any decoration at all", and an element can pass one while failing the
// other. A fully transparent tile lands perfectly, every time, in exactly the
// right place, and is invisible.
//
// WHY IT HAS TO DRIVE THE REAL RASTER. The tile a generator produces is a
// function of gen_params AFTER the whole resolution chain: the wire-key alias
// table (`cells` is a pitch, `full` is Pcts[1]), the absent-vs-zero percent
// marker, the int clamp, the tile-size clamp and each raster's own documented
// defaults. Nothing short of calling RasterGenerator knows what a fragment's
// parameter list actually paints, which is precisely how seventeen elements of
// one style shipped invisible through a static audit that read every line of
// them: `frame` mode 0 inks with `tint`, and a fragment that names its colour
// in the element's own `tint =` colour-mod rather than inside `gen_params` has
// a generator whose ink is the zero colour — transparent — with an element-level
// multiply over it that cannot rescue a texture with no pixels.
//
// It is a census over the LIBRARY rather than over a list of known offenders,
// so a fragment authored next wave is covered the day it is added.

import (
	"image"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// presetGenElements walks the shipped library and yields every generator
// element with the preset that declares it. Both axes: a layout fragment may
// declare structural generator elements too (ruling R3's `panel` tier), and a
// blank one there is the same defect.
func presetGenElements(t *testing.T, visit func(p *theme.Preset, el theme.Element)) {
	t.Helper()
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	for _, set := range [][]*theme.Preset{lib.Layouts(), lib.Styles()} {
		for _, p := range set {
			for _, el := range p.Elements() {
				if el.Kind != theme.ElemGen {
					continue
				}
				visit(p, el)
			}
		}
	}
}

// tileMaxAlpha is the census's measurement: the most opaque pixel in the tile.
// Alpha rather than colour because a generator paints with genBlend, whose
// whole output is premultiplied-free RGBA — a tile that painted black-on-black
// still has alpha, and a tile with none painted nothing at all.
func tileMaxAlpha(img *image.RGBA) uint8 {
	if img == nil {
		return 0
	}
	var max uint8
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] > max {
			max = img.Pix[i]
		}
	}
	return max
}

// TestNoShippedPresetGeneratorElementRasterisesAnEmptyTile is the gate.
//
// THE DEFECT IT CLOSES, measured before the fix: 20 of the 183 shipped preset
// generator elements produced a fully transparent tile, 17 of them in
// `cyberpunk` — its entire bracket-and-chip vocabulary, invisible in all 21
// layouts. Two causes, one per generator:
//
//   - `frame` in mode 0/1/2 inks with GenParams.tint(), and seven of cyberpunk's
//     bracket elements (plus three of vhs_crt's) named their colour ONLY in the
//     element's `tint =` colour-mod. An element-level tint MULTIPLIES the
//     texture; there was no texture.
//   - `plate` in mode 1 (chamfer) paints only GenParams.accent(), and ten of
//     cyberpunk's chips wrote `accent=#00000000` with the colour they meant in
//     `tint`/`shadow` — which mode 1 never reads.
//
// Both fixes are in the FRAGMENTS (the drafts were audited statically; this is
// the first wave that runs them through real code), and both use the colour the
// author had already written elsewhere in the same element rather than a new one.
func TestNoShippedPresetGeneratorElementRasterisesAnEmptyTile(t *testing.T) {
	var seen, blank int
	presetGenElements(t, func(p *theme.Preset, el theme.Element) {
		seen++
		spec := theme.GeneratorSpecOf(&el)
		img := RasterGenerator(spec)
		if img == nil {
			t.Errorf("%s/%s: element %q names generator %q, which this build does not rasterise",
				p.Kind, p.ID, el.ID, el.Gen)
			return
		}
		if tileMaxAlpha(img) == 0 {
			blank++
			t.Errorf("%s/%s: element %q (generator %q, gen_params %q) rasterises a FULLY "+
				"TRANSPARENT tile — it ships invisible in every layout. An element-level "+
				"`tint`/`fill` cannot rescue it: those modulate a texture, and there is none",
				p.Kind, p.ID, el.ID, el.Gen, theme.WriteGenParams(&el))
		}
	})
	if seen == 0 {
		t.Fatal("the census found no generator elements at all — it is measuring nothing")
	}
	if blank > 0 {
		t.Logf("%d of %d shipped preset generator elements rasterise nothing", blank, seen)
	}
}

// TestTheEmptyTileCensusWouldCatchABlankedGenerator is the gate's own gate: it
// proves the measurement is real rather than vacuously green, by taking a
// shipped element that DOES paint and blanking the one parameter its raster
// inks with.
//
// Without this arm, tileMaxAlpha could return 255 for everything (say, because
// a future genNewTile pre-filled its tile opaque) and the census above would
// stay green for ever while the class it guards came straight back.
func TestTheEmptyTileCensusWouldCatchABlankedGenerator(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	style, err := lib.Find(theme.PresetStyle, "cyberpunk")
	if err != nil {
		t.Fatalf("finding the cyberpunk style: %v", err)
	}
	var probe *theme.Element
	for _, el := range style.Elements() {
		if el.Kind == theme.ElemGen && theme.NormalizeGenName(el.Gen) == "frame" {
			cp := el
			probe = &cp
			break
		}
	}
	if probe == nil {
		t.Skip("cyberpunk no longer declares a `frame` element; the arm has nothing to blank")
	}
	if got := tileMaxAlpha(RasterGenerator(theme.GeneratorSpecOf(probe))); got == 0 {
		t.Fatalf("the probe element %q is ALREADY blank — the census above should have "+
			"failed first, and this arm cannot prove anything against it", probe.ID)
	}

	// Blank every colour the tile could possibly be inked with. A `frame` reads
	// tint (modes 0-2), bg (mode 3) and shadow; zeroing all four alphas is the
	// hostile version of the authoring mistake the census exists for.
	blanked := *probe
	for i := 0; i < blanked.GenParamCount() && i < len(blanked.GenParams); i++ {
		switch blanked.GenParams[i].Key {
		case "tint", "bg", "accent", "shadow":
			blanked.GenParams[i].Value = "#00000000"
		}
	}
	if got := tileMaxAlpha(RasterGenerator(theme.GeneratorSpecOf(&blanked))); got != 0 {
		t.Errorf("blanking every colour of %q still rasterised alpha %d — tileMaxAlpha is not "+
			"measuring what the census claims it measures", probe.ID, got)
	}
}
