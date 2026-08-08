package ui

// Gates for the procedural generators (v1.90.0 W4 — design §6.3).
//
// The generator contract has four clauses and three of them are only checkable by a
// test: purity is invisible until two runs disagree, the tile cap is invisible until
// a theme eats the media budget with one element, and resize-invariance is invisible
// until somebody drags a window and the client re-rasters seventeen tiles per frame.

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// genFixtureSpec builds a spec by hand, the way a sidecar line would.
func genFixtureSpec(name string, kv ...string) theme.GeneratorSpec {
	g := theme.GeneratorSpec{Name: theme.NormalizeGenName(name)}
	for i := 0; i+1 < len(kv) && g.NP < len(g.Params); i += 2 {
		g.Params[g.NP] = theme.KV{Key: kv[i], Value: kv[i+1]}
		g.NP++
	}
	return g
}

// genExerciseParams is one non-default parameter set per generator: every int, both
// percents, an angle and four colours, chosen so no raster can take a "nothing to
// draw" early exit and quietly stop being measured. Eight keys, which is exactly
// theme.GenParamCap — the widest a real element may write.
func genExerciseParams(name string) theme.GeneratorSpec {
	return genFixtureSpec(name,
		"pitch", "9",
		"size", "5",
		"count", "3",
		"seed", "1234",
		"angle", "40",
		"tint", "#EC34E4",
		"bg", "#101010C0",
		"accent", "#0CECEC",
	)
}

// TestEveryGeneratorKindRasterizes is the totality gate: every registry name
// produces a tile, and a name outside it produces nothing rather than a panic.
//
// Totality matters here for the same reason it matters for the element painter
// table — the name comes out of a file a stranger wrote, so "we never tested that
// one" is the same as "that one crashes on somebody else's machine".
func TestEveryGeneratorKindRasterizes(t *testing.T) {
	names := GeneratorNames()
	if len(names) == 0 {
		t.Fatal("the generator registry is empty")
	}
	sort.Strings(names)
	for _, name := range names {
		if !GeneratorKnown(name) {
			t.Errorf("%q is in the registry but GeneratorKnown says no", name)
		}
		img := RasterGenerator(genExerciseParams(name))
		if img == nil {
			t.Errorf("%q rasterised nil", name)
			continue
		}
		b := img.Bounds()
		if b.Dx() <= 0 || b.Dy() <= 0 {
			t.Errorf("%q rasterised an empty tile %v", name, b)
			continue
		}
		// A generator that painted nothing at all is a generator nobody would notice
		// was broken: at least one pixel must have some alpha.
		lit := false
		for i := 3; i < len(img.Pix) && !lit; i += 4 {
			lit = img.Pix[i] != 0
		}
		if !lit {
			t.Errorf("%q produced a fully transparent tile for a fully-specified param set — "+
				"either the raster is a no-op or its defaults cancel out", name)
		}
	}
	// The degrade path: an unknown name is not an error, it just does not rasterise,
	// and the element falls back to a flat fill (format rule 3).
	if GeneratorKnown("definitely_not_a_generator") {
		t.Error("an unknown generator name reported as known")
	}
	if img := RasterGenerator(genFixtureSpec("definitely_not_a_generator")); img != nil {
		t.Error("an unknown generator name rasterised something")
	}
	// And an over-long name is not a name: NormalizeGenName refuses it outright
	// rather than letting a 4 KiB string reach a map probe.
	if theme.NormalizeGenName(strings.Repeat("x", theme.GenNameRuneCap+1)) != "" {
		t.Error("an over-long generator name was not refused")
	}
	t.Logf("%d generators: %s", len(names), strings.Join(names, ", "))
}

// TestOneRegistryProbeAnswersKnownSizedAndRastered pins the ledger's L23: the three
// public entry points must agree about a name, whatever its case.
//
// They did not. GeneratorKnown and GenTileSize normalised the wire name while
// RasterGenerator indexed the registry RAW, so a spec named "Scanlines" was known,
// sized, planned, and booked one of the twelve ThemeGenCap slots — then rasterised
// nil. The element drew blank with nothing anywhere to say why, and the whole
// admission arm above it had spent a slot on it.
//
// Latent only because theme.GeneratorSpecOf (which lower-cases) is today's sole spec
// producer, which is precisely why no existing gate could see it: not one test in the
// tree builds a mixed-case spec, and no theme in the shipped corpus spells one.
// W7's editor picker is the second producer.
func TestOneRegistryProbeAnswersKnownSizedAndRastered(t *testing.T) {
	for _, name := range GeneratorNames() {
		// The name as a HAND-WRITTEN spec might spell it, bypassing NormalizeGenName —
		// which is exactly what a spec built anywhere other than GeneratorSpecOf does.
		shouty := strings.ToUpper(name[:1]) + name[1:]
		spec := theme.GeneratorSpec{Name: shouty}
		spec.Params[0] = theme.KV{Key: "pitch", Value: "9"}
		spec.NP = 1

		known := GeneratorKnown(spec.Name)
		w, h := genTileSizeOf(spec)
		img := RasterGenerator(spec)
		switch {
		case !known && (w != 0 || h != 0 || img != nil):
			t.Errorf("%q: GeneratorKnown says no but it sized %dx%d / rastered %v", shouty, w, h, img != nil)
		case known && img == nil:
			t.Errorf("%q: known and sized %dx%d, but rasterised NIL — the three entry points do not "+
				"share one registry probe, so this spec books a ThemeGenCap slot and then draws "+
				"nothing, silently", shouty, w, h)
		case known && img != nil:
			if b := img.Bounds(); int32(b.Dx()) != w || int32(b.Dy()) != h {
				t.Errorf("%q: sized %dx%d but rasterised %v", shouty, w, h, b)
			}
		}
	}
}

// TestEveryGeneratorIsDeterministic is clause 1, and it is the one that makes the
// content-addressed cache key VALID.
//
// The key is the hash of the params. If the same params could produce two different
// rasters, the cache would serve whichever one happened to land first — so the tile
// a theme gets would depend on which element the apply reached first, which is not a
// cosmetic bug but a theme that renders differently on consecutive launches.
func TestEveryGeneratorIsDeterministic(t *testing.T) {
	for _, name := range GeneratorNames() {
		spec := genExerciseParams(name)
		first := RasterGenerator(spec)
		if first == nil {
			t.Fatalf("%q rasterised nil", name)
		}
		for run := 0; run < 3; run++ {
			again := RasterGenerator(spec)
			if again == nil {
				t.Fatalf("%q rasterised nil on run %d", name, run)
			}
			if again.Bounds() != first.Bounds() {
				t.Fatalf("%q run %d produced %v, first run produced %v", name, run, again.Bounds(), first.Bounds())
			}
			if !bytes.Equal(again.Pix, first.Pix) {
				t.Fatalf("%q run %d is not byte-identical to run 0 — the param hash is its cache key, so a "+
					"non-deterministic generator makes a theme's appearance depend on apply order "+
					"(no time.Now, no unseeded math/rand, no map iteration inside a raster)", name, run)
			}
		}
		// Determinism must be a property of the PARAMS, not a constant: a different
		// seed has to change the picture, or the seed is decoration.
		seeded := genFixtureSpec(name, "seed", "1", "pitch", "9", "size", "5", "count", "3", "tint", "#ffffff")
		other := genFixtureSpec(name, "seed", "999", "pitch", "9", "size", "5", "count", "3", "tint", "#ffffff")
		if a, b := RasterGenerator(seeded), RasterGenerator(other); a != nil && b != nil {
			if bytes.Equal(a.Pix, b.Pix) {
				t.Logf("%q ignores `seed` (it is not a seeded generator)", name)
			}
		}
	}
}

// TestGeneratorTileNeverExceedsMaxPx is clause 2 — the affordability decision.
//
// It is driven with HOSTILE parameters, because the cap has to hold against a file a
// stranger wrote: a generator whose tile size is a function of `pitch` would, without
// the shared clamp, turn `pitch = 100000` into a 40 GB allocation on the theme-apply
// goroutine.
func TestGeneratorTileNeverExceedsMaxPx(t *testing.T) {
	hostile := [][]string{
		{"pitch", "100000", "size", "100000", "count", "100000"},
		{"pitch", "-5", "size", "-5", "count", "-5"},
		{"pitch", "0", "size", "0", "count", "0"},
		{"pitch", "255", "size", "255", "count", "255", "seed", "-2147483648"},
		{"radius", "99999", "cells", "99999", "ring", "99999", "bloom", "99999"},
	}
	// EVERY row is rasterised, not merely sized. The size check alone would pass a
	// generator whose output is a correctly clamped 256² tile and whose RASTERISER
	// walks a 68 000² coordinate box to fill it — which is exactly what two of these
	// did before the parse-time int clamp, the pen-scaled stamp spacing in genLine
	// and genDisc's bounds clamp landed. Bounded OUTPUT and bounded WORK are two
	// different claims and only the raster checks the second.
	for _, name := range GeneratorNames() {
		for _, kv := range hostile {
			spec := genFixtureSpec(name, kv...)
			w, h := genTileSizeOf(spec)
			if w < genTileMinPx || h < genTileMinPx {
				t.Errorf("%q with %v sized %dx%d, below the %d floor", name, kv, w, h, genTileMinPx)
			}
			if w > theme.GenTileMaxPx || h > theme.GenTileMaxPx {
				t.Errorf("%q with %v sized %dx%d, over the %d cap — one element would eat the media budget",
					name, kv, w, h, theme.GenTileMaxPx)
			}
			img := RasterGenerator(spec)
			if img == nil {
				t.Fatalf("%q rasterised nil for %v", name, kv)
			}
			if got := img.Bounds(); int32(got.Dx()) != w || int32(got.Dy()) != h {
				t.Errorf("%q rasterised %v but genTileSizeOf said %dx%d — the planner would budget the wrong number",
					name, got, w, h)
			}
		}
	}
	// An unknown name has no size, which is how the planner knows not to budget for it.
	if w, h := genTileSizeOf(genFixtureSpec("nope")); w != 0 || h != 0 {
		t.Errorf("an unknown generator reported a %dx%d tile", w, h)
	}
	// The worst legal tile is small enough that the whole generator cap fits inside
	// the media allowance with room — which is what makes tiles "effectively free".
	worst := int64(theme.GenTileMaxPx) * int64(theme.GenTileMaxPx) * 4 * int64(render.ThemeGenCap)
	if budget := render.ThemeMediaByteCap(config.TexBudgetDefaultMiB); worst > budget/2 {
		t.Errorf("the full generator cap is %d bytes against a %d-byte media allowance — tiles are no "+
			"longer the cheap tier the design leans on", worst, budget)
	}
}

// TestGeneratorKeyIsResizeInvariant pins the property that makes a window drag free.
//
// The tile carries no width, no height and no theme in its key, so the SAME
// generator at any canvas size is the same cache entry: nothing re-rasters, nothing
// re-pins, nothing re-uploads. The element stretches or repeats it. A key that
// carried `<w>x<h>` would make every resize a full re-raster of every generator
// element, on the theme-apply goroutine, while the user is still dragging.
func TestGeneratorKeyIsResizeInvariant(t *testing.T) {
	spec := genExerciseParams("stripes")
	key := render.ThemeGenKey(spec.Hash())
	if !strings.Contains(key, "theme://g/") {
		t.Fatalf("generator key %q is not under theme://g/", key)
	}
	// Nothing in the key mentions a size, and the SPEC has no size to mention.
	for _, dim := range []string{"1280", "720", "1512", "648", "640", "480", "x"} {
		if strings.Contains(strings.TrimPrefix(key, "theme://g/"), dim) && dim == "x" {
			t.Errorf("generator key %q looks dimensioned", key)
		}
	}
	// Two elements with identical params share one key — across elements and across
	// themes, which is what makes a fourteen-element scanline set cost one tile.
	if other := render.ThemeGenKey(genExerciseParams("stripes").Hash()); other != key {
		t.Errorf("the same params produced two keys: %q and %q", key, other)
	}
	// And the hash is a real function of the params: change one and the key moves.
	changed := genFixtureSpec("stripes", "pitch", "10")
	if render.ThemeGenKey(changed.Hash()) == render.ThemeGenKey(genFixtureSpec("stripes", "pitch", "11").Hash()) {
		t.Error("two different param sets produced one key — elements would share a tile they never asked to share")
	}
	// Order-sensitivity is DOCUMENTED and therefore tested: the same pairs written in
	// a different order are a different key. Two tiles instead of one, no visual
	// difference — the cheap direction of the trade, and the one that avoids needing
	// a canonicalisation this format has never published.
	ab := genFixtureSpec("stripes", "pitch", "9", "size", "5")
	ba := genFixtureSpec("stripes", "size", "5", "pitch", "9")
	if ab.Hash() == ba.Hash() {
		t.Error("the generator hash is order-INsensitive — either make it so deliberately and document it, " +
			"or this test is stale")
	}
	// Different generators with identical params never collide.
	if genExerciseParams("noise").Hash() == genExerciseParams("mottle").Hash() {
		t.Error("two generators with the same params hashed alike — the name is not in the digest")
	}
}

// assertGenParamsAllResolve is the whole rule, applied to one element list:
// every `gen_params` key must resolve to a slot, and no two keys in one element
// may land on the same one.
//
// It is a function taking a corpus because there are TWO of them, and the second
// one arrived with the gate pointed at the first. An unrecognised key is ignored
// with no note anywhere ("a generator ignores what it does not use"), and aliases
// like `pitch`/`dot` or `pct`/`fade` share a slot under a later-wins rule, so a
// dead parameter is invisible from every direction except this one.
func assertGenParamsAllResolve(t *testing.T, where string, elements []theme.Element) int {
	t.Helper()
	checked := 0
	for i := range elements {
		el := &elements[i]
		if el.Gen == "" {
			continue
		}
		checked++
		spec := theme.GeneratorSpecOf(el)
		seen := map[genParamSlot]string{}
		for j := 0; j < spec.NP; j++ {
			key := spec.Params[j].Key
			slot, known := genParamSlots[key]
			if !known {
				t.Errorf("%s [element.%s] generator %q: param %q is in no slot — it is silently "+
					"ignored, with no note anywhere", where, el.ID, spec.Name, key)
				continue
			}
			if prev, dup := seen[slot]; dup {
				t.Errorf("%s [element.%s] generator %q: %q and %q share a GenParams slot, so the "+
					"author wrote two values and only one survives", where, el.ID, spec.Name, prev, key)
			}
			seen[slot] = key
		}
		if !GeneratorKnown(spec.Name) {
			t.Errorf("%s [element.%s] names generator %q, which this build does not rasterise — "+
				"the element degrades to a flat fill", where, el.ID, spec.Name)
		}
	}
	return checked
}

// TestGeneratorParamKeysDoNotCollide checks the ONE risk the global slot table
// carries, over the SHIPPED THEMES: two keys landing in the same slot for a
// generator that writes both.
//
// The mapping is global because twenty-five keys mapped per-generator would be
// twenty-five more things to keep in step with the rasters. This is the price.
func TestGeneratorParamKeysDoNotCollide(t *testing.T) {
	root := filepath.Join(uiRepoRoot(t), shippedThemeDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	checked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), theme.SidecarFileName)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		sc, err := theme.LoadSidecar(path)
		if err != nil || sc == nil {
			t.Fatalf("themes/%s: %v", e.Name(), err)
		}
		checked += assertGenParamsAllResolve(t, "themes/"+e.Name(), sc.Elements)
	}
	if checked == 0 {
		t.Fatal("no shipped element declares a generator — the gate is vacuous")
	}
	t.Logf("%d generator elements across the shipped themes, no slot collisions", checked)
}

// genInkedPixels counts the pixels a tile actually paints. Coverage rather than a
// pixel-by-pixel expectation: what these knobs claim is "more ink" or "less ink",
// and a hand-written bitmap would pin the anti-aliasing instead of the parameter.
//
// EVERY FIXTURE BELOW USES A TRANSPARENT `bg`, and that is not cosmetic: a tile
// with an opaque background inks every pixel it has, so this counter reads the
// tile SIZE and no parameter can move it. Both of the first two arms passed
// vacuously that way before the fixtures were fixed.
func genInkedPixels(t *testing.T, g theme.GeneratorSpec) int {
	t.Helper()
	img := RasterGenerator(g)
	if img == nil {
		t.Fatalf("%s rasterised nil", g.Name)
	}
	n := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 0 {
			n++
		}
	}
	return n
}

// TestTheThreeReWiredGenParamsActuallyChangeTheirTile is the anti-regression half
// of the dead-param pass. `full`, `bow` and `pct` were all written by shipped
// fragments and all resolved to nothing; wiring them into the slot table is only
// half a fix, because a key that resolves to a slot NO RASTER READS is dead in
// exactly the same way and looks identical from
// TestEveryShippedGenParamResolvesToASlot.
//
// So each one is driven to two values and the tile has to differ. That is also
// what makes the halftone default claim checkable: the knob's default is stated
// to be the constant the raster used before it existed, so an element that says
// nothing must rasterise byte-identically.
func TestTheThreeReWiredGenParamsActuallyChangeTheirTile(t *testing.T) {
	// wingmark `full`: 0 is a thin gull arc, 100 two full lobes. umineko's `gulls`
	// asked for 0 and got the default, so it was drawing butterflies.
	base := []string{"size", "40", "count", "4", "seed", "5", "tint", "#FFFFFF"}
	gull := genInkedPixels(t, genFixtureSpec("wingmark", append(append([]string{}, base...), "full", "0")...))
	fly := genInkedPixels(t, genFixtureSpec("wingmark", append(append([]string{}, base...), "full", "100")...))
	if gull == fly {
		t.Errorf("wingmark ignores `full` (%d inked pixels either way) — the key resolves to a slot "+
			"the raster does not read, which is the same dead parameter under a new name", gull)
	}

	// frame `bow`: mode 3 only, and it must NOT disturb the other modes.
	bezel := []string{"pitch", "6", "size", "14", "count", "3", "gap", "0",
		"tint", "#2F2A26", "bg", "#00000000"}
	straight := genInkedPixels(t, genFixtureSpec("frame", append(append([]string{}, bezel...), "bow", "0")...))
	bowed := genInkedPixels(t, genFixtureSpec("frame", append(append([]string{}, bezel...), "bow", "35")...))
	if bowed >= straight {
		t.Errorf("frame's bezel does not bow: %d inked pixels at bow=35 against %d at bow=0, and the "+
			"barrel thins the moulding, so it must paint less", bowed, straight)
	}
	box := []string{"pitch", "6", "size", "14", "count", "1", "gap", "0", "tint", "#2F2A26"}
	if a, b := genInkedPixels(t, genFixtureSpec("frame", append(append([]string{}, box...), "bow", "0")...)),
		genInkedPixels(t, genFixtureSpec("frame", append(append([]string{}, box...), "bow", "90")...)); a != b {
		t.Errorf("`bow` changed a mode-1 box frame (%d -> %d) — it is reserved for the bezel, and a rule "+
			"that changed thickness along its length reads as a mistake", a, b)
	}

	// halftone `pct`: the dot diameter, and a default that reproduces the constant
	// the raster carried before the knob existed.
	tone := []string{"pitch", "12", "angle", "0", "tint", "#000000", "bg", "#00000000"}
	small := genInkedPixels(t, genFixtureSpec("halftone", append(append([]string{}, tone...), "pct", "20")...))
	big := genInkedPixels(t, genFixtureSpec("halftone", append(append([]string{}, tone...), "pct", "90")...))
	if small >= big {
		t.Errorf("halftone ignores `pct`: %d inked pixels at 20%% against %d at 90%%", small, big)
	}
	if silent, stated := genInkedPixels(t, genFixtureSpec("halftone", tone...)),
		genInkedPixels(t, genFixtureSpec("halftone", append(append([]string{}, tone...), "pct", "68")...)); silent != stated {
		t.Errorf("halftone's default dot size is not genHalftoneDotPct (%d): saying nothing inks %d "+
			"pixels and saying 68 inks %d, so adding the knob moved every tile that never asked for it",
			genHalftoneDotPct, silent, stated)
	}
}

// TestFrameGapOpensTheMiddleOfEachEdge pins the axis the bezel's barrel and the
// frame's gap both measure, and it is here because that axis was SWAPPED.
//
// `gap` is "percent of each edge left open", and it measured the distance from
// the tile centre PERPENDICULAR to the nearest edge — which is ~1 everywhere
// inside the band, so nothing ever opened: gap = 25 and gap = 50 painted the
// whole band, and mode 0's "corner brackets" drew a continuous box. Six shipped
// preset elements write `gap = 22`.
//
// The orientation is pinned by two PIXELS rather than by a count, because a
// count is what let the swap through: a tile can ink the right number of pixels
// in the wrong places.
func TestFrameGapOpensTheMiddleOfEachEdge(t *testing.T) {
	img := RasterGenerator(genFixtureSpec("frame",
		"pitch", "6", "size", "14", "count", "1", "gap", "50", "tint", "#FFFFFF"))
	if img == nil {
		t.Fatal("frame rasterised nil")
	}
	b := img.Bounds()
	alpha := func(x, y int) uint8 { return img.Pix[img.PixOffset(x, y)+3] }
	mid, edge := b.Dx()/2, 1
	for _, c := range []struct {
		x, y int
		ink  bool
		what string
	}{
		{mid, edge, false, "the middle of the TOP edge must be open at gap = 50"},
		{mid, b.Dy() - 1 - edge, false, "so must the middle of the bottom edge"},
		{edge, b.Dy() / 2, false, "and the middle of the left edge"},
		{b.Dx() - 1 - edge, b.Dy() / 2, false, "and the middle of the right edge"},
		{edge, edge, true, "the top-left CORNER must survive — a gap leaves the corners"},
		{b.Dx() - 1 - edge, b.Dy() - 1 - edge, true, "so must the bottom-right corner"},
	} {
		if got := alpha(c.x, c.y) > 0; got != c.ink {
			t.Errorf("pixel (%d,%d) inked=%v, want %v — %s", c.x, c.y, got, c.ink, c.what)
		}
	}
	// And the two modes really are different pictures: mode 0 is corner brackets,
	// so it must paint LESS than the continuous box mode 1 draws at gap = 0.
	box := genInkedPixels(t, genFixtureSpec("frame", "pitch", "6", "size", "14", "count", "1", "gap", "0", "tint", "#FFFFFF"))
	brackets := genInkedPixels(t, genFixtureSpec("frame", "pitch", "6", "size", "14", "count", "0", "gap", "0", "tint", "#FFFFFF"))
	if brackets >= box {
		t.Errorf("mode 0 inked %d pixels against mode 1's %d — corner brackets are drawing a continuous box",
			brackets, box)
	}
}

// TestEveryShippedGenParamResolvesToASlot is the same rule over the OTHER shipped
// corpus — the embedded preset fragments — and it is the gate that was missing.
//
// The rule was already written and already enforced; it was pointed at
// `themes/`, and W9 shipped a second body of generator data that nothing checked.
// The census the day it was written: 13 keys in no slot (`bow`, `full`, `glow`,
// `ratio`, `rough`, `width`, `rgb`) and 8 collisions where the EARLIER value was
// the dead one (`pitch` under `dot` on seven halftones, `pct` under `fade` on the
// one element the file calls THE preset for ruling R6 — so the perspective it
// documents at 100 was rendering at 45). Twenty-one parameters, five presets,
// none of them visible from anywhere: an unknown key is dropped in silence and an
// aliased one is overwritten in silence.
func TestEveryShippedGenParamResolvesToASlot(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	checked := 0
	for _, axis := range [][]*theme.Preset{lib.Layouts(), lib.Styles()} {
		for _, p := range axis {
			checked += assertGenParamsAllResolve(t, p.Kind.String()+"/"+p.ID, p.Elements())
		}
	}
	if checked == 0 {
		t.Fatal("no preset element declares a generator — the gate is vacuous")
	}
	t.Logf("%d generator elements across the embedded presets, every param in a slot of its own", checked)
}

// TestCheckerdiscCornersAreTransparent pins the ADOPTED CLAUSE from the preset
// workflow's escalation ESC-2.
//
// `bg` is the dark checker CELL; the tile outside the disc is transparent by
// construction. One consumer draws its medallion over a near-black void and cannot
// tell the difference; the other draws it over a gold floor, where an opaque
// background square erases the floor. The clause is invisible to the preset that
// proposed it, which is exactly why it needs a gate rather than a comment.
func TestCheckerdiscCornersAreTransparent(t *testing.T) {
	img := RasterGenerator(genFixtureSpec("checkerdisc",
		"cells", "6", "ring", "8", "squash", "45",
		"tint", "#EDEDED", "bg", "#101010", "accent", "#B98F3C"))
	if img == nil {
		t.Fatal("checkerdisc rasterised nil")
	}
	b := img.Bounds()
	for _, c := range [4][2]int{{b.Min.X, b.Min.Y}, {b.Max.X - 1, b.Min.Y}, {b.Min.X, b.Max.Y - 1}, {b.Max.X - 1, b.Max.Y - 1}} {
		if a := img.Pix[img.PixOffset(c[0], c[1])+3]; a != 0 {
			t.Errorf("corner (%d,%d) has alpha %d, want 0 — an opaque tile corner erases whatever the "+
				"disc is inlaid into", c[0], c[1], a)
		}
	}
	// Non-vacuity: the middle really is drawn.
	if a := img.Pix[img.PixOffset(b.Dx()/2, b.Dy()/2)+3]; a == 0 {
		t.Error("the centre of the disc is transparent — the raster drew nothing")
	}
}
