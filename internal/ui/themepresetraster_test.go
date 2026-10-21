package ui

// THE SHIPPED PRESETS' GENERATOR CENSUS (v1.90.0 W9).
// ONE concern: every `kind = gen` element in the shipped preset library must
// rasterise SOMETHING. It is deliberately not part of the coherence matrix —
// that gate answers "where does this decoration land", this one answers "is
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
