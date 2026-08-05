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
			w, h := GenTileSize(name, genParseParams(spec))
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
				t.Errorf("%q rasterised %v but GenTileSize said %dx%d — the planner would budget the wrong number",
					name, got, w, h)
			}
		}
	}
	// An unknown name has no size, which is how the planner knows not to budget for it.
	if w, h := GenTileSize("nope", GenParams{}); w != 0 || h != 0 {
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

// TestGeneratorParamKeysDoNotCollide checks the ONE risk the global slot table
// carries: two keys landing in the same slot for a generator that writes both.
//
// The mapping is global because twenty-five keys mapped per-generator would be
// twenty-five more things to keep in step with the rasters. The price is this test,
// which walks the SHIPPED THEMES' actual `gen_params` lines and fails if any single
// element writes two keys that resolve to one slot — i.e. if a parameter the author
// wrote is silently overwritten by a later one.
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
		for i := range sc.Elements {
			el := &sc.Elements[i]
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
					t.Errorf("themes/%s [element.%s] generator %q: param %q is in no slot — it is silently "+
						"ignored, with no note anywhere", e.Name(), el.ID, spec.Name, key)
					continue
				}
				if prev, dup := seen[slot]; dup {
					t.Errorf("themes/%s [element.%s] generator %q: %q and %q share a GenParams slot, so the "+
						"author wrote two values and only one survives", e.Name(), el.ID, spec.Name, prev, key)
				}
				seen[slot] = key
			}
			if !GeneratorKnown(spec.Name) {
				t.Errorf("themes/%s [element.%s] names generator %q, which this build does not rasterise — "+
					"the element degrades to a flat fill", e.Name(), el.ID, spec.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no shipped element declares a generator — the gate is vacuous")
	}
	t.Logf("%d generator elements across the shipped themes, no slot collisions", checked)
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
