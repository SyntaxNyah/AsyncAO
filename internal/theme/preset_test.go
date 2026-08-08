package theme

// THE PRESET LIBRARY'S GATES (v1.90.0 W9) — design §6.5 acceptance clauses 1-3
// and 7, plus the encapsulation gates hard rule 11 asks of every new seam.
//
// The 294-combination COHERENCE matrix is not here: it needs the real anchor
// resolver, which lives in internal/ui, and re-deriving the resolve in this
// package would be exactly the mirror-test hard rule 11 rejects. See
// internal/ui/themepresetmatrix_test.go.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SyntaxNyah/AsyncAO/internal/theme/presets"
)

// shippedLibraryFor loads the embedded library, failing the test rather than
// returning an error: every gate below is meaningless against a nil library, and
// a library that does not load is the first thing a reader needs told.
func shippedLibraryFor(t testing.TB) *PresetLibrary {
	t.Helper()
	lib, err := ShippedPresets()
	if err != nil {
		t.Fatalf("the embedded preset library does not load: %v", err)
	}
	return lib
}

// ---------------------------------------------------------------------------
// §6.5 clause 1 — every embedded preset parses
// ---------------------------------------------------------------------------

// TestEveryEmbeddedPresetParses is acceptance clause 1, and it is deliberately
// the loudest test in the file: everything else in this wave is downstream of
// the fragments being readable at all.
//
// It asserts the SHAPE of the library, not just the absence of an error. A
// loader that silently skipped a file it could not classify would return a
// perfectly valid library with 13 styles in it, and the gallery would render a
// grid with a hole nobody could explain.
func TestEveryEmbeddedPresetParses(t *testing.T) {
	lib := shippedLibraryFor(t)

	// The two axis directories, counted from the EMBEDDED FS rather than from the
	// library, so a fragment the loader dropped shows up as a mismatch instead of
	// as a smaller expectation.
	for _, axis := range []struct {
		dir  string
		kind PresetKind
	}{{presets.LayoutDir, PresetLayout}, {presets.StyleDir, PresetStyle}} {
		files, err := presets.FS.ReadDir(axis.dir)
		if err != nil {
			t.Fatalf("read %s: %v", axis.dir, err)
		}
		var want int
		for _, f := range files {
			if strings.HasSuffix(f.Name(), presets.FileExt) {
				want++
			}
		}
		if got := lib.Count(axis.kind); got != want {
			t.Fatalf("%s: %d files on disk, %d in the library — the loader dropped %d fragment(s) silently",
				axis.dir, want, got, want-got)
		}
	}

	if lib.Count(PresetLayout) == 0 || lib.Count(PresetStyle) == 0 {
		t.Fatal("an axis is empty — the gallery would have no grid")
	}
	if n := lib.Combinations(); n != lib.Count(PresetLayout)*lib.Count(PresetStyle) {
		t.Fatalf("Combinations() = %d, want %d", n, lib.Count(PresetLayout)*lib.Count(PresetStyle))
	}

	for _, p := range append(append([]*Preset(nil), lib.Layouts()...), lib.Styles()...) {
		if !validID(p.ID) {
			t.Errorf("%s: id %q is outside the [a-z0-9_-]{1,%d} grammar", p.ID, p.ID, IDRuneCap)
		}
		if p.Name == "" || p.Description == "" {
			t.Errorf("%s: a gallery card needs both a name (%q) and a description (%q)", p.ID, p.Name, p.Description)
		}
		if p.Side == nil {
			t.Fatalf("%s: parsed to a nil sidecar", p.ID)
		}
		// EVERY note is a degrade the reader recorded. The metadata class is the one
		// legal source of them (metatext.go): a shipped fragment may carry an
		// over-long credit, but it may not carry a skipped element or a dropped pane
		// host, because those are content nobody would ever see missing.
		for _, n := range p.Side.Notes() {
			if !strings.Contains(n, "shortened for display") {
				t.Errorf("%s: the reader degraded something structural: %s", p.ID, n)
			}
		}
	}
}

// TestEveryLayoutPresetDeclaresItsDesignCanvas pins the one header key that is
// axis-specific. Geometry in an [overrides] row is design-space, so a layout
// whose canvas nobody declared is a set of numbers with no unit.
func TestEveryLayoutPresetDeclaresItsDesignCanvas(t *testing.T) {
	for _, p := range shippedLibraryFor(t).Layouts() {
		if p.DesignW <= 0 || p.DesignH <= 0 {
			t.Errorf("layout %s declares design %dx%d — an [overrides] row has no unit without one",
				p.ID, p.DesignW, p.DesignH)
		}
	}
	// A style's geometry is anchor-relative by rule, so it has no canvas to
	// declare and declaring one would imply its rects were absolute.
	for _, p := range shippedLibraryFor(t).Styles() {
		if p.DesignW != 0 || p.DesignH != 0 {
			t.Errorf("style %s declares design %dx%d — style geometry is anchor-relative and has no canvas of its own",
				p.ID, p.DesignW, p.DesignH)
		}
	}
}

// ---------------------------------------------------------------------------
// §6.5 clause 2 — the two axes are disjoint
// ---------------------------------------------------------------------------

// TestLayoutAndStylePresetKeySetsAreDisjoint is acceptance clause 2, and it is
// the load-bearing gate of the entire wave: "apply a layout" and "apply a style"
// are only well-defined acts because the sections they own do not overlap, and
// the 294-cell gallery is only orthogonal for the same reason.
//
// It runs in BOTH directions on purpose. Over the TABLE, because the rule lives
// there and a future section added to both partitions must fail here rather than
// in a bug report. Over the DATA, because the table only governs fragments that
// go through the parser, and a gate that trusted the parser to have run would
// pass on an empty directory.
func TestLayoutAndStylePresetKeySetsAreDisjoint(t *testing.T) {
	// --- the table ---------------------------------------------------------
	// [element.*] is the ONE deliberate overlap: both axes declare elements, and
	// the partition continues one level down at the element KIND
	// (presetElementKindOK). Everything else must belong to exactly one axis.
	const sharedPrefix = secElementPrefix
	lay, sty := presetTierSections[PresetLayout], presetTierSections[PresetStyle]
	for _, a := range lay.exact {
		for _, b := range sty.exact {
			if a == b {
				t.Errorf("[%s] is owned by BOTH axes — applying either tier would clobber the other", a)
			}
		}
		for _, b := range sty.prefixes {
			if strings.HasPrefix(a, b) {
				t.Errorf("[%s] is owned by the layout axis and matches the style prefix %q", a, b)
			}
		}
	}
	for _, a := range lay.prefixes {
		if a == sharedPrefix {
			continue
		}
		for _, b := range sty.prefixes {
			if b == sharedPrefix {
				continue
			}
			if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
				t.Errorf("section prefixes %q and %q overlap across the two axes", a, b)
			}
		}
		for _, b := range sty.exact {
			if strings.HasPrefix(b, a) {
				t.Errorf("[%s] is owned by the style axis and matches the layout prefix %q", b, a)
			}
		}
	}
	// The element kinds partition too, and they partition TOTALLY: every kind
	// belongs to exactly one axis, so no element can be ownerless.
	for k := ElementKind(0); k < ElementKindCount; k++ {
		inLayout := presetElementKindOK(PresetLayout, k)
		inStyle := presetElementKindOK(PresetStyle, k)
		if inLayout == inStyle {
			t.Errorf("element kind %s is owned by %s axes, want exactly one",
				k, map[bool]string{true: "both", false: "neither"}[inLayout])
		}
	}

	// --- the data ----------------------------------------------------------
	lib := shippedLibraryFor(t)
	seen := map[string]PresetKind{}
	for _, p := range append(append([]*Preset(nil), lib.Layouts()...), lib.Styles()...) {
		for _, sec := range fragmentSections(t, p) {
			if sec == secPreset || strings.HasPrefix(sec, secElementPrefix) {
				continue
			}
			if prev, ok := seen[sec]; ok && prev != p.Kind {
				t.Errorf("[%s] appears in a %s preset AND in a %s preset (%s)", sec, prev, p.Kind, p.ID)
			}
			seen[sec] = p.Kind
		}
	}
	if len(seen) == 0 {
		t.Fatal("no sections were censused — the walk found nothing, so this proved nothing")
	}
}

// fragmentSections re-reads a fragment's own file and lists its section headers.
// It goes back to the BYTES rather than to the parsed model, because a section
// the model has no field for is still a section the fragment declared — and
// that is precisely the kind a disjointness violation would hide in.
func fragmentSections(t testing.TB, p *Preset) []string {
	t.Helper()
	dir := presets.LayoutDir
	if p.Kind == PresetStyle {
		dir = presets.StyleDir
	}
	raw, err := presets.FS.ReadFile(dir + "/" + p.ID + presets.FileExt)
	if err != nil {
		t.Fatalf("re-read %s: %v", p.ID, err)
	}
	doc, err := ParseINIDoc(raw)
	if err != nil {
		t.Fatalf("re-parse %s: %v", p.ID, err)
	}
	var out []string
	for _, name := range walkSections(doc).names {
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// TestPresetTierViolationIsRefusedNotIgnored is the ENCAPSULATION half of the
// disjointness rule: the partition cannot be bypassed by writing a fragment that
// ignores it. Both directions, and the element-kind rule with them.
//
// It drives ParsePreset — the sanctioned path — with hand-built fragments rather
// than asserting over the table a second time, because a test that re-derived
// the rule would pass even if nothing enforced it.
func TestPresetTierViolationIsRefusedNotIgnored(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want error
	}{
		{
			name: "a style preset writing [overrides]",
			src:  "[preset]\nkind = style\nid = x\nname = X\n\n[overrides]\nviewport = 0, 0, 10, 10\n",
			want: ErrPresetTier,
		},
		{
			name: "a style preset writing [rotations]",
			src:  "[preset]\nkind = style\nid = x\nname = X\n\n[rotations]\nviewport = 64\n",
			want: ErrPresetTier,
		},
		{
			name: "a style preset writing [pane.a]",
			src:  "[preset]\nkind = style\nid = x\nname = X\n\n[pane.a]\nrect = 0, 0, 10, 10\n",
			want: ErrPresetTier,
		},
		{
			name: "a layout preset writing [palette]",
			src:  "[preset]\nkind = layout\nid = x\nname = X\n\n[palette]\ntext = #ffffff\n",
			want: ErrPresetTier,
		},
		{
			name: "a layout preset writing [effect.viewport]",
			src:  "[preset]\nkind = layout\nid = x\nname = X\n\n[effect.viewport]\neffect = pulse\n",
			want: ErrPresetTier,
		},
		{
			name: "either axis writing [theme] — identity is never a contribution",
			src:  "[preset]\nkind = style\nid = x\nname = X\n\n[theme]\nname = Not Yours\n",
			want: ErrPresetTier,
		},
		{
			name: "a style preset declaring a pane element",
			src:  "[preset]\nkind = style\nid = x\nname = X\n\n[element.p]\nkind = pane\nanchor = viewport\nfill = #ffffff\n",
			want: ErrPresetElementKind,
		},
		{
			name: "a layout preset declaring a shape element",
			src:  "[preset]\nkind = layout\nid = x\nname = X\n\n[element.s]\nkind = shape\nshape = pill\nanchor = viewport\nfill = #ffffff\n",
			want: ErrPresetElementKind,
		},
		{
			name: "an unanchored style element nobody declared full_canvas",
			src:  "[preset]\nkind = style\nid = x\nname = X\n\n[element.s]\nkind = shape\nshape = pill\nfill = #ffffff\n",
			want: ErrPresetAnchor,
		},
		{
			name: "a fragment with no kind at all",
			src:  "[preset]\nid = x\nname = X\n",
			want: ErrPresetKind,
		},
		{
			name: "a fragment whose id is not an id",
			src:  "[preset]\nkind = style\nid = Not An Id\nname = X\n",
			want: ErrPresetID,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePreset([]byte(tc.src))
			if err == nil {
				t.Fatalf("ParsePreset accepted it — the partition is not enforced")
			}
			if !strings.Contains(err.Error(), strings.TrimPrefix(tc.want.Error(), "theme: ")) {
				t.Fatalf("got %v, want a %v", err, tc.want)
			}
		})
	}
}

// TestFullCanvasIsADeclarationNotAnInference pins the other half of the anchor
// rule: an unanchored element is legal ONLY when the fragment listed it, so a
// decoration cannot opt out of the rule by merely being large.
func TestFullCanvasIsADeclarationNotAnInference(t *testing.T) {
	const body = "[preset]\nkind = style\nid = x\nname = X\nfull_canvas = %s\n\n" +
		"[element.wash]\nkind = shape\nshape = sharp\nspace = courtroom\nrect = 0, 0, 9999, 9999\nfill = #ffffff\n"
	if _, err := ParsePreset([]byte(fmt.Sprintf(body, "wash"))); err != nil {
		t.Fatalf("a DECLARED full-canvas element was refused: %v", err)
	}
	if _, err := ParsePreset([]byte(fmt.Sprintf(body, "somethingelse"))); err == nil {
		t.Fatal("an element covering the whole canvas was accepted without being declared — " +
			"size is not a declaration, or the gate cannot tell the two apart")
	}
}

// ---------------------------------------------------------------------------
// §6.5 clause 3 — every style element anchors
// ---------------------------------------------------------------------------

// TestStylePresetElementsAlwaysAnchor is acceptance clause 3 over the SHIPPED
// data. The parser refuses a violation (above), so this cannot fail while that
// passes — which is the point: it is the gate that proves the rule was applied
// to the 461 elements that actually ship, not only to the two in a fixture.
//
// It also asserts the converse, which the parser does NOT check: every id in a
// `full_canvas` list must name an element that exists. A stale entry there is an
// anchor rule quietly switched off for an element that was renamed.
func TestStylePresetElementsAlwaysAnchor(t *testing.T) {
	var anchored, full int
	for _, p := range shippedLibraryFor(t).Styles() {
		ids := map[string]bool{}
		for i := range p.Side.Elements {
			e := &p.Side.Elements[i]
			ids[e.ID] = true
			switch {
			case e.Anchor != "":
				anchored++
			case p.IsFullCanvas(e.ID):
				full++
				// R7: coverage is judged post-fit against the ACTIVE canvas, so the only
				// thing decidable here is the space. A full-canvas overlay must live in a
				// space that HAS a canvas — `pane` does not (it needs an anchor to name
				// one), so an unanchored pane-space element can never resolve.
				if e.Space == SpacePane {
					t.Errorf("style %s: full_canvas element %q declares space = pane with no anchor — it can never resolve",
						p.ID, e.ID)
				}
			default:
				t.Errorf("style %s: element %q is neither anchored nor declared full_canvas", p.ID, e.ID)
			}
		}
		for _, id := range p.FullCanvas {
			if !ids[id] {
				t.Errorf("style %s: full_canvas names %q, which is not an element in this fragment — "+
					"a stale entry is the anchor rule switched off for nothing", p.ID, id)
			}
		}
	}
	if anchored == 0 || full == 0 {
		t.Fatalf("censused %d anchored and %d declared elements — one arm never ran", anchored, full)
	}
	// THE EXEMPTION IS SMALL AND IT HAS TO STAY SMALL. It is the one way a style
	// element escapes the orthogonality rule, and W9 moved elements in BOTH
	// directions across it, for opposite reasons:
	//
	//   * 40 canvas-wide overlays went the other way, from an absolute
	//     `0, 0, 714, 668` to `anchor = courtroom` with a `0, 0, 0, 0` delta,
	//     because the absolute rect covers only 47% of a 1512-wide layout
	//     (README F2) and the >= 90% snap cannot reach it from there;
	//   * 13 fixed-size ORNAMENTS came in, because their w/h had been written as
	//     "canvas minus a constant" and ruling R1 turned that into a delta that
	//     inverts on every canvas but the one they were drawn on.
	//
	// Anchor first, always; declare only what genuinely references no widget.
	// PresetFullCanvasCap is the format's bound and is far above this; the number
	// here is a REVIEW threshold, so a wave that doubles the exemptions has to
	// change a line that says out loud what it is doing.
	const shippedFullCanvasReviewCap = 24
	if full > shippedFullCanvasReviewCap {
		t.Errorf("%d shipped style elements take the full_canvas exemption (review threshold %d) — "+
			"anchoring is the rule and this is the escape hatch, not a second idiom", full, shippedFullCanvasReviewCap)
	}
	t.Logf("%d anchored style elements, %d declared canvas-space ornaments", anchored, full)
}

// ---------------------------------------------------------------------------
// §6.5 clause 7 — zero bytes of bundled art
// ---------------------------------------------------------------------------

// TestEveryShippedPresetShipsZeroBytesOfArt is acceptance clause 7, and it is an
// AGPLv3 gate before it is an engineering one: "everything is arithmetic, so
// everything is original by construction" is only true while it stays true.
//
// It checks the FS as well as the model. A preset cannot reference art without a
// [media] entry, but it could still smuggle a .png into the axis directory and a
// later wave could start reading it.
func TestEveryShippedPresetShipsZeroBytesOfArt(t *testing.T) {
	for _, p := range append(append([]*Preset(nil), shippedLibraryFor(t).Layouts()...), shippedLibraryFor(t).Styles()...) {
		if n := len(p.Side.Media); n != 0 {
			t.Errorf("%s declares %d [media] entries — presets ship zero bytes of art (§6.4)", p.ID, n)
		}
		// [fonts] maps a family to a FILE inside the theme, and a bundled face is
		// bundled art. A row whose value is a generic CLASS (`serif`, `mono`) is
		// not: it ships no bytes, it resolves to the client's own face, and it is
		// the only way §6.4 leaves a preset to express typography at all. The test
		// is therefore on the EXTENSION, not on the row count.
		for _, f := range p.Side.Fonts {
			if fontFileExt(f.File) {
				t.Errorf("%s [fonts] %q = %q names a font FILE — presets ship zero bundled faces (§6.4)",
					p.ID, f.Family, f.File)
			}
		}
		if n := len(p.Side.Sounds); n != 0 {
			t.Errorf("%s declares %d [sounds] entries — a sampled sound is bundled art", p.ID, n)
		}
		for i := range p.Side.Elements {
			if e := &p.Side.Elements[i]; e.Kind == ElemImage {
				t.Errorf("%s element %q is kind = image — there is no tier that bundles a raster (§6.2)", p.ID, e.ID)
			}
		}
		if p.Kind == PresetStyle && p.Credit == "" {
			t.Errorf("style %s states no credit — clause 7 asks every preset to say it ships no art", p.ID)
		}
	}
	for _, dir := range []string{presets.LayoutDir, presets.StyleDir} {
		files, err := presets.FS.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), presets.FileExt) {
				t.Errorf("%s/%s is not a %s fragment — the axis directories hold text and nothing else",
					dir, f.Name(), presets.FileExt)
			}
		}
	}
}

// fontFileExt reports a [fonts] value that names an actual face file. The list
// is every extension internFace could open; anything else is a class name.
func fontFileExt(file string) bool {
	lower := strings.ToLower(strings.TrimSpace(file))
	for _, ext := range []string{".ttf", ".otf", ".ttc", ".otc", ".woff", ".woff2", ".pfb", ".fon"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// TestNoShippedPresetExceedsTheRecipientsBudget is §6.4's third bullet: "the
// budget is the recipient's, not the author's". Every cap a preset can spend is
// counted here, against the constant rather than against a number typed into the
// assertion.
//
// The generator TILE count is the interesting one, and it is counted the way the
// texture cache counts it — by DEDUPLICATING (name, params) pairs, because the
// param hash is the cache key, so twenty elements sharing one recipe cost one
// tile. Counting elements instead would fail a preset that is doing exactly the
// right thing.
func TestNoShippedPresetExceedsTheRecipientsBudget(t *testing.T) {
	// themeGenTileCap mirrors render.ThemeGenCap, which internal/theme may not
	// import (it is an SDL package). NAMED and asserted against the one live
	// consumer by internal/ui's own budget gates; here it is the number the
	// authoring guide published, and a preset over it would be refused at apply.
	const themeGenTileCap = 12
	lib := shippedLibraryFor(t)
	for _, p := range append(append([]*Preset(nil), lib.Layouts()...), lib.Styles()...) {
		if n := len(p.Side.Elements); n > ElementCap {
			t.Errorf("%s declares %d elements, cap is %d", p.ID, n, ElementCap)
		}
		if n := len(p.Side.Panes); n > PaneCap {
			t.Errorf("%s declares %d panes, cap is %d", p.ID, n, PaneCap)
		}
		if n := len(p.Side.Effects); n > EffectBindCap {
			t.Errorf("%s declares %d effect bindings, cap is %d", p.ID, n, EffectBindCap)
		}
		if n := len(p.Side.Overrides); n > OverrideCap {
			t.Errorf("%s declares %d overrides, cap is %d", p.ID, n, OverrideCap)
		}
		tiles := map[uint64]struct{}{}
		for i := range p.Side.Elements {
			if e := &p.Side.Elements[i]; e.Kind == ElemGen && e.Gen != "" {
				tiles[GeneratorSpecOf(e).Hash()] = struct{}{}
			}
		}
		if n := len(tiles); n > themeGenTileCap {
			t.Errorf("%s needs %d distinct generator tiles, cap is %d", p.ID, n, themeGenTileCap)
		}
	}
}

// ---------------------------------------------------------------------------
// The library loader's own refusals
// ---------------------------------------------------------------------------

// fakeAxisFS builds a two-directory tree the loader can read. It exists because
// none of the failures below may ever exist in the shipped tree, and a rule that
// only the shipped tree could violate is a rule with no test.
func fakeAxisFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: []byte(body)}
	}
	// fs.ReadDir needs both directories to exist even when one is empty.
	for _, d := range []string{presets.LayoutDir, presets.StyleDir} {
		if _, ok := out[d+"/.keep"]; !ok {
			out[d+"/.keep"] = &fstest.MapFile{Data: []byte("")}
		}
	}
	return out
}

func styleFragment(id string) string {
	return "[preset]\nkind = style\nid = " + id + "\nname = " + id + "\ndescription = d\n"
}

// TestPresetLoaderRefusesEveryWayAnAxisCanLie drives LoadPresetLibrary — the
// sanctioned path — through every refusal it owns. Each of these is a silent
// gallery defect if it degrades instead: a duplicate id makes a saved theme mean
// two things, a stem/id mismatch makes a diff lie, a misfiled fragment breaks the
// partition, an over-cap directory grows a bound nobody named, and an over-size
// fragment grows the binary the same way.
func TestPresetLoaderRefusesEveryWayAnAxisCanLie(t *testing.T) {
	t.Run("duplicate id", func(t *testing.T) {
		// Two FILES cannot share a name, so the duplicate arrives via a stem that
		// matches while the ids collide — which is why the loader checks both.
		src := fakeAxisFS(map[string]string{
			presets.StyleDir + "/a" + presets.FileExt: styleFragment("a"),
			presets.StyleDir + "/b" + presets.FileExt: styleFragment("a"),
		})
		if _, err := LoadPresetLibrary(src); err == nil {
			t.Fatal("two fragments claiming one id were accepted")
		}
	})
	t.Run("stem does not match id", func(t *testing.T) {
		src := fakeAxisFS(map[string]string{
			presets.StyleDir + "/wrong" + presets.FileExt: styleFragment("right"),
		})
		_, err := LoadPresetLibrary(src)
		if err == nil || !strings.Contains(err.Error(), "does not match its file name") {
			t.Fatalf("got %v, want ErrPresetIDMismatch", err)
		}
	})
	t.Run("fragment filed under the wrong axis", func(t *testing.T) {
		src := fakeAxisFS(map[string]string{
			presets.LayoutDir + "/a" + presets.FileExt: styleFragment("a"),
		})
		_, err := LoadPresetLibrary(src)
		if err == nil || !strings.Contains(err.Error(), "does not match its directory") {
			t.Fatalf("got %v, want ErrPresetAxisDir", err)
		}
	})
	t.Run("axis over its cap", func(t *testing.T) {
		files := map[string]string{}
		for i := 0; i <= PresetStyleCap; i++ {
			id := fmt.Sprintf("s%02d", i)
			files[presets.StyleDir+"/"+id+presets.FileExt] = styleFragment(id)
		}
		_, err := LoadPresetLibrary(fakeAxisFS(files))
		if err == nil || !strings.Contains(err.Error(), "exceeds a named cap") {
			t.Fatalf("got %v, want ErrPresetCap", err)
		}
	})
	t.Run("fragment over the per-file byte cap", func(t *testing.T) {
		// The BODY is padded with comment lines, so what trips is the size check and
		// not some other bound the padding happened to reach first.
		body := styleFragment("big") + strings.Repeat("; padding\n", PresetFragmentByteCap/10+1)
		src := fakeAxisFS(map[string]string{presets.StyleDir + "/big" + presets.FileExt: body})
		_, err := LoadPresetLibrary(src)
		if err == nil || !errors.Is(err, ErrPresetFragmentSize) {
			t.Fatalf("got %v, want ErrPresetFragmentSize", err)
		}
	})
}

// TestEveryShippedFragmentFitsTheFragmentCap is the corpus half of the bound
// above: the refusal only protects the tree if the tree is actually inside it,
// and a cap nothing is measured against is a cap that gets discovered the day a
// fragment crosses it.
//
// It also reports the total, because "how many bytes does the preset library add
// to the binary" is the number the zero-bundled-art claim is really about.
func TestEveryShippedFragmentFitsTheFragmentCap(t *testing.T) {
	total, worst, worstName := 0, 0, ""
	for _, dir := range []string{presets.LayoutDir, presets.StyleDir} {
		files, err := presets.FS.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, f := range files {
			raw, err := presets.FS.ReadFile(dir + "/" + f.Name())
			if err != nil {
				t.Fatalf("read %s/%s: %v", dir, f.Name(), err)
			}
			total += len(raw)
			if len(raw) > worst {
				worst, worstName = len(raw), dir+"/"+f.Name()
			}
			if len(raw) > PresetFragmentByteCap {
				t.Errorf("%s/%s is %d bytes, cap is %d", dir, f.Name(), len(raw), PresetFragmentByteCap)
			}
		}
	}
	t.Logf("preset library: %d bytes embedded, largest fragment %s at %d (cap %d)",
		total, worstName, worst, PresetFragmentByteCap)
}

// TestFindTreatsAnUnknownPresetAsAMissingTierNotAFailure pins format rule 1 at
// the library's edge: a theme naming a preset from a newer build must render
// everything else. The distinction the API has to keep is between "" (this theme
// declares no style) and "umineko2" (it declares one this build never shipped) —
// the first is silence, the second is a report line.
func TestFindTreatsAnUnknownPresetAsAMissingTierNotAFailure(t *testing.T) {
	lib := shippedLibraryFor(t)
	if p, err := lib.Find(PresetStyle, ""); p != nil || err != nil {
		t.Fatalf(`Find(style, "") = (%v, %v), want (nil, nil) — an absent tier is not an error`, p, err)
	}
	if _, err := lib.Find(PresetStyle, "no-such-preset-here"); err == nil {
		t.Fatal("an unknown id resolved to something")
	}

	sc := NewSidecar()
	sc.Meta.StylePreset = "no-such-preset-here"
	sc.Meta.LayoutPreset = lib.Layouts()[0].ID
	pair, missing := lib.ResolvePresets(sc)
	if pair.Layout == nil {
		t.Fatal("the KNOWN axis did not resolve — one unknown id took the other tier with it")
	}
	if pair.Style != nil {
		t.Fatal("an unknown id resolved to a preset")
	}
	if len(missing) != 1 || !strings.Contains(missing[0], "no-such-preset-here") {
		t.Fatalf("missing = %v, want the one unresolved id named", missing)
	}
}

// ---------------------------------------------------------------------------
// The merge engine
// ---------------------------------------------------------------------------

// sidecarFingerprint hashes everything about a sidecar that a caller could
// observe: its serialised bytes AND its model. Bytes alone would miss a mutation
// the writer has no key for; the model alone would miss a document edit.
func sidecarFingerprint(t testing.TB, s *Sidecar) string {
	t.Helper()
	b, err := s.Bytes()
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	h := sha256.New()
	h.Write(b)
	fmt.Fprintf(h, "\x00model %+v", *s)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// TestMergePresetsNeverMutatesItsBase is the property the gallery's live preview
// rests on: scrubbing a row merges 294 candidate themes and throws every one of
// them away, and not one of them may leave a mark on the theme the user has open.
//
// It hashes the base BEFORE and AFTER, which is the same shape
// TestCopyForEditingLeavesTheOriginalUntouched uses for the folder on disk. The
// aliasing bug this catches is the plausible one: a struct copy shares every
// slice header, so the merge's first append into Elements writes through.
func TestMergePresetsNeverMutatesItsBase(t *testing.T) {
	lib := shippedLibraryFor(t)
	base := NewSidecar()
	base.Meta.Name = "Mine"
	base.Elements = []Element{
		{ID: "mine_badge", Kind: ElemShape, Shape: "pill", Anchor: "viewport", Fill: RGBA{R: 9, G: 9, B: 9, A: 255}},
		{ID: "mine_pane", Kind: ElemPane, Anchor: "left", Space: SpacePane, Fill: RGBA{R: 1, G: 2, B: 3, A: 255}},
	}
	base.Media = []MediaEntry{{ID: "mine_art", File: "asyncao/media/a.png", Bytes: 10, Hash: "abc"}}
	base.Overrides = []KeyRect{{Key: "viewport", Rect: Rect{X: 1, Y: 2, W: 3, H: 4}}}

	before := sidecarFingerprint(t, base)
	pair := PresetPair{Layout: lib.Layouts()[0], Style: lib.Styles()[0]}
	merged, err := MergePresets(base, pair)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	// Touch the RESULT everywhere a shared header would show through.
	merged.Elements = append(merged.Elements, Element{ID: "zz", Kind: ElemText, Text: "x", Anchor: "viewport"})
	merged.Overrides = append(merged.Overrides, KeyRect{Key: "flip", Rect: Rect{W: 1, H: 1}})
	merged.Media = append(merged.Media, MediaEntry{ID: "zz", File: "z"})
	merged.Meta.Name = "Not Mine"
	if _, err := merged.Bytes(); err != nil {
		t.Fatalf("the merged model does not serialise: %v", err)
	}

	if after := sidecarFingerprint(t, base); after != before {
		t.Fatal("the merge mutated its base — a slice header or the document is shared")
	}
}

// TestApplyPresetTierReplacesConfigurationAndMergesManifests pins the ONE
// sentence the merge is built on (presetmerge.go's header), in both halves.
//
// Configuration replaced: a layout apply must leave nothing of the previous
// layout, INCLUDING a pane element the user hand-authored — a tier you cannot
// replace is a tier you cannot swap.
// Manifests merged: the user's imported art survives a style apply, because
// dropping a [media] row orphans bytes on disk that nothing can reach again.
func TestApplyPresetTierReplacesConfigurationAndMergesManifests(t *testing.T) {
	lib := shippedLibraryFor(t)
	layout, err := lib.Find(PresetLayout, "split_triptych")
	if err != nil {
		t.Fatalf("fixture layout: %v", err)
	}
	style := lib.Styles()[0]

	sc := NewSidecar()
	sc.Elements = []Element{
		{ID: "mine_badge", Kind: ElemShape, Shape: "pill", Anchor: "viewport", Fill: RGBA{R: 9, G: 9, B: 9, A: 255}},
		{ID: "mine_pane", Kind: ElemPane, Anchor: "left", Space: SpacePane, Fill: RGBA{R: 1, G: 2, B: 3, A: 255}},
	}
	sc.Media = []MediaEntry{{ID: "mine_art", File: "asyncao/media/a.png", Bytes: 10, Hash: "abc"}}
	sc.Fonts = []FontFamily{{Family: "Mine", File: "mine.ttf"}}
	sc.Sounds = []KV{{Key: "sfx-mine", Value: "mine.opus"}}
	sc.Overrides = []KeyRect{{Key: "flip", Rect: Rect{X: 7, Y: 7, W: 7, H: 7}}}

	if err := sc.ApplyPresetTier(layout); err != nil {
		t.Fatalf("apply layout: %v", err)
	}
	if _, ok := sc.FindElement("mine_pane"); ok {
		t.Error("a hand-authored PANE element survived a layout apply — the layout tier was not replaced")
	}
	if _, ok := sc.FindElement("mine_badge"); !ok {
		t.Error("a shape element was dropped by a LAYOUT apply — the layout tier took the style tier with it")
	}
	if _, ok := sc.OverrideRect("flip"); !ok {
		t.Log("note: the layout preset does not declare `flip`, so the row is simply gone with the rest of the tier")
	}
	if sc.Meta.LayoutPreset != layout.ID {
		t.Errorf("layout_preset = %q, want %q — reset-to-preset has nothing to resolve from", sc.Meta.LayoutPreset, layout.ID)
	}
	if len(sc.Panes) != len(layout.Side.Panes) {
		t.Errorf("panes = %d, want the preset's %d", len(sc.Panes), len(layout.Side.Panes))
	}

	if err := sc.ApplyPresetTier(style); err != nil {
		t.Fatalf("apply style: %v", err)
	}
	if _, ok := sc.FindElement("mine_badge"); ok {
		t.Error("a hand-authored SHAPE element survived a style apply — the style tier was not replaced")
	}
	if len(sc.Panes) != len(layout.Side.Panes) {
		t.Error("a STYLE apply changed the panes — the axes are not disjoint in the apply")
	}
	if sc.Meta.LayoutPreset != layout.ID {
		t.Error("a style apply cleared layout_preset")
	}
	// The manifests, all three of them.
	if _, ok := sc.FindMedia("mine_art"); !ok {
		t.Error("imported art was dropped by a preset apply — the bytes are now orphaned on disk")
	}
	if _, ok := sc.FontFamilyFile("Mine"); !ok {
		t.Error("a bundled font family was dropped by a preset apply")
	}
	if _, ok := sc.SoundFile("sfx-mine"); !ok {
		t.Error("a sound override was dropped by a preset apply")
	}
	if err := sc.Validate(); err != nil {
		t.Fatalf("the merged model does not validate: %v", err)
	}
}

// TestApplyPresetTierIsAllOrNothing pins the refusal contract: a merge that
// cannot fit refuses BEFORE it moves a field, so a failed apply never leaves a
// model half-way between two themes.
func TestApplyPresetTierIsAllOrNothing(t *testing.T) {
	style := shippedLibraryFor(t).Styles()[0]
	sc := NewSidecar()
	// Fill the element list with PANE elements, which the style tier keeps, so its
	// own contribution cannot fit beside them.
	for i := 0; i < ElementCap; i++ {
		sc.Elements = append(sc.Elements, Element{
			ID: fmt.Sprintf("p%02d", i), Kind: ElemPane, Space: SpacePane, Anchor: "a", Fill: RGBA{R: 1, G: 1, B: 1, A: 255},
		})
	}
	before := len(sc.Elements)
	err := sc.ApplyPresetTier(style)
	if err == nil {
		t.Fatal("an over-cap apply succeeded — the cap truncated instead of refusing")
	}
	if len(sc.Elements) != before || sc.Meta.StylePreset != "" {
		t.Fatalf("the refused apply left a mark: %d elements (was %d), style_preset = %q",
			len(sc.Elements), before, sc.Meta.StylePreset)
	}
}

// TestEveryCombinationMergesAndValidates runs all 294 through the engine. It is
// the cheap half of acceptance clause 4 — the expensive half (do the rects land
// on their anchors) needs the real resolver and lives in internal/ui — and it
// catches the failures that are not about geometry at all: an id collision
// between the axes, a cap the two tiers only breach together, and a merged model
// the writer would refuse to save.
//
// THE CAPS ARE ASSERTED EXPLICITLY, not merely by Bytes() refusing. Bytes()
// enforces them, so a breach fails either way — but "combination does not
// serialise: element cap" and "cyberpunk x split_triptych needs 97 elements, cap
// is 96" are not the same message to the person who has to fix it, and the
// generator TILE budget is not the writer's business at all: it belongs to the
// texture store, so nothing in the serialise path would ever have looked at it.
//
// PER COMBINATION, which is the whole point. TestNoShippedPresetExceedsThe-
// RecipientsBudget measures one FRAGMENT; a cap the two axes only breach
// TOGETHER — 52 style elements over 48 layout panes — is invisible there and is
// exactly what a two-axis gallery makes possible.
func TestEveryCombinationMergesAndValidates(t *testing.T) {
	// themeGenTileCap mirrors render.ThemeGenCap; see the fragment-level gate's
	// note on why the number is spelled here rather than imported.
	const themeGenTileCap = 12
	lib := shippedLibraryFor(t)
	n, worstEl, worstTiles := 0, 0, 0
	for _, l := range lib.Layouts() {
		for _, s := range lib.Styles() {
			merged, err := NewThemeFromPresets("Gate", PresetPair{Layout: l, Style: s})
			if err != nil {
				t.Fatalf("%s x %s: %v", l.ID, s.ID, err)
			}
			if _, err := merged.Bytes(); err != nil {
				t.Fatalf("%s x %s does not serialise: %v", l.ID, s.ID, err)
			}
			if merged.Meta.LayoutPreset != l.ID || merged.Meta.StylePreset != s.ID {
				t.Fatalf("%s x %s: the result does not name its own tiers (%q x %q)",
					l.ID, s.ID, merged.Meta.LayoutPreset, merged.Meta.StylePreset)
			}
			// THE RECIPIENT'S BUDGET, on the merged model (§6.4: "the budget is the
			// recipient's, not the author's").
			if got := len(merged.Elements); got > ElementCap {
				t.Fatalf("%s x %s needs %d elements, cap is %d", l.ID, s.ID, got, ElementCap)
			} else if got > worstEl {
				worstEl = got
			}
			if got := len(merged.Panes); got > PaneCap {
				t.Fatalf("%s x %s needs %d panes, cap is %d", l.ID, s.ID, got, PaneCap)
			}
			if got := len(merged.Overrides); got > OverrideCap {
				t.Fatalf("%s x %s needs %d overrides, cap is %d", l.ID, s.ID, got, OverrideCap)
			}
			if got := len(merged.Media); got != 0 {
				t.Fatalf("%s x %s carries %d media entries — a combination of two zero-art "+
					"presets is a zero-art theme", l.ID, s.ID, got)
			}
			// Deduplicated by param hash, the way the texture cache counts a tile.
			tiles := map[uint64]struct{}{}
			for i := range merged.Elements {
				if e := &merged.Elements[i]; e.Kind == ElemGen && e.Gen != "" {
					tiles[GeneratorSpecOf(e).Hash()] = struct{}{}
				}
			}
			if got := len(tiles); got > themeGenTileCap {
				t.Fatalf("%s x %s needs %d distinct generator tiles, cap is %d — this combination "+
					"only works on a machine with a raised TexBudgetMiB, which makes it a broken preset",
					l.ID, s.ID, got, themeGenTileCap)
			} else if got > worstTiles {
				worstTiles = got
			}
			// ZERO NOTES. Every note is a degrade the reader had to apply, and a
			// SHIPPED combination that degrades is a fragment we got wrong: the whole
			// asymmetry in ParsePreset's doc comment (refuse where the reader degrades)
			// is worth nothing if the merge then quietly shortens something.
			if notes := merged.Notes(); len(notes) != 0 {
				t.Fatalf("%s x %s merged with %d note(s), want none — the first is: %s",
					l.ID, s.ID, len(notes), notes[0])
			}
			n++
		}
	}
	if n != lib.Combinations() {
		t.Fatalf("ran %d combinations, the library says %d", n, lib.Combinations())
	}
	t.Logf("%d combinations merged, serialised and validated; worst combination %d/%d elements, %d/%d generator tiles",
		n, worstEl, ElementCap, worstTiles, themeGenTileCap)
}

// TestAMergedThemeReadsBackAsItself is the round trip the gallery's create
// action depends on: write the merged model, read the file, get the same theme.
// A preset whose values the writer emits and the reader then degrades would
// produce a theme that changes the first time it is opened.
func TestAMergedThemeReadsBackAsItself(t *testing.T) {
	lib := shippedLibraryFor(t)
	for _, s := range lib.Styles() {
		l := lib.Layouts()[0]
		built, err := NewThemeFromPresets("Round Trip", PresetPair{Layout: l, Style: s})
		if err != nil {
			t.Fatalf("%s: %v", s.ID, err)
		}
		raw, err := built.Bytes()
		if err != nil {
			t.Fatalf("%s: serialise: %v", s.ID, err)
		}
		back, err := ParseSidecar(raw)
		if err != nil {
			t.Fatalf("%s: the file we just wrote does not parse: %v", s.ID, err)
		}
		if len(back.Elements) != len(built.Elements) {
			t.Fatalf("%s: wrote %d elements, read back %d", s.ID, len(built.Elements), len(back.Elements))
		}
		for i := range built.Elements {
			a, b := built.Elements[i], back.Elements[i]
			if a.ID != b.ID || a.Kind != b.Kind || a.Anchor != b.Anchor || a.Rect != b.Rect || a.Band != b.Band {
				t.Fatalf("%s: element %d changed across the round trip:\n have %+v\n want %+v", s.ID, i, b, a)
			}
		}
		if len(back.Overrides) != len(built.Overrides) {
			t.Fatalf("%s: wrote %d overrides, read back %d", s.ID, len(built.Overrides), len(back.Overrides))
		}
	}
}

// TestACreatedThemeCarriesItsStylesAttribution is the licensing contract's last
// leg, and it is the one that leaves the process.
//
// §6.5 clause 7 says every preset states that it ships no art. The gallery shows
// that line before you build, but the artefact the user then hands to someone
// else is a FOLDER — and if the attribution stops at our panel, the theme that
// gets redistributed under AGPLv3 says nothing about where its art came from.
// So the style's credit becomes the created theme's `[theme] credit`, and this
// asserts it survives the whole way: merge, serialise, and read back off a file.
func TestACreatedThemeCarriesItsStylesAttribution(t *testing.T) {
	lib := shippedLibraryFor(t)
	layout := lib.Layouts()[0]
	for _, s := range lib.Styles() {
		built, err := NewThemeFromPresets("Handed On", PresetPair{Layout: layout, Style: s})
		if err != nil {
			t.Fatalf("%s: %v", s.ID, err)
		}
		raw, err := built.Bytes()
		if err != nil {
			t.Fatalf("%s: serialise: %v", s.ID, err)
		}
		back, err := ParseSidecar(raw)
		if err != nil {
			t.Fatalf("%s: re-read: %v", s.ID, err)
		}
		if back.Meta.Credit != s.Credit {
			t.Errorf("style %s: a theme built from it credits\n  %q\nits fragment says\n  %q",
				s.ID, back.Meta.Credit, s.Credit)
		}
		// And the theme names the tiers it was built from, so the attribution can be
		// traced back to a fragment rather than being an unsourced sentence.
		if back.Meta.StylePreset != s.ID || back.Meta.LayoutPreset != layout.ID {
			t.Errorf("style %s: the built theme names tiers %q x %q",
				s.ID, back.Meta.LayoutPreset, back.Meta.StylePreset)
		}
	}
}

// TestPresetLibraryIsImmutableAcrossCallers is the encapsulation gate for the
// shared library: two independent applies of one preset must be independent.
// The library is a package-level singleton, so a merge that handed out the
// library's own Element values would let the first theme edited in a session
// rewrite what every later theme gets.
func TestPresetLibraryIsImmutableAcrossCallers(t *testing.T) {
	lib := shippedLibraryFor(t)
	style := lib.Styles()[0]
	want := len(style.Side.Elements)
	if want == 0 {
		t.Fatal("fixture style has no elements — this would prove nothing")
	}
	wantFirst := style.Side.Elements[0]

	first := NewSidecar()
	if err := first.ApplyPresetTier(style); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Vandalise the result as hard as an editing session would.
	for i := range first.Elements {
		first.Elements[i].Fill = RGBA{255, 0, 255, 255}
		first.Elements[i].Hidden = true
		first.Elements[i].Rect = Rect{X: -999, Y: -999}
	}
	first.Elements = first.Elements[:1]

	if got := len(style.Side.Elements); got != want {
		t.Fatalf("the library's own element list shrank to %d (was %d)", got, want)
	}
	if style.Side.Elements[0] != wantFirst {
		t.Fatal("editing one merge result rewrote the library — every later apply in this session is corrupt")
	}
	second := NewSidecar()
	if err := second.ApplyPresetTier(style); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(second.Elements) != want || second.Elements[0] != wantFirst {
		t.Fatal("the second apply of the same preset produced a different theme")
	}
}

// ---------------------------------------------------------------------------
// The data package's own encapsulation
// ---------------------------------------------------------------------------

// TestPresetDataPackageShipsDataOnly is the gate on the DEPENDENCY DIRECTION
// (hard rule 11): internal/theme/presets is data, it imports "embed" and nothing
// else, and it declares no function of any kind.
//
// It matters because the cycle it prevents is invisible until it exists. The
// moment that package imports internal/theme — to validate a fragment, say, or
// to expose a typed accessor — the parser can no longer import the data, and the
// fix at that point is a third package nobody wanted. Here it is one assertion.
func TestPresetDataPackageShipsDataOnly(t *testing.T) {
	dir := filepath.Join("presets")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var goFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			goFiles = append(goFiles, e.Name())
		}
	}
	sort.Strings(goFiles)
	if len(goFiles) != 1 || goFiles[0] != "embed.go" {
		t.Fatalf("the data package holds %v — it is data, and one file is the whole of it", goFiles)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(dir, "embed.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, imp := range f.Imports {
		if imp.Path.Value != `"embed"` {
			t.Errorf("the data package imports %s — data never imports code, or the cycle is one commit away", imp.Path.Value)
		}
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			t.Errorf("the data package declares func %s — behaviour belongs in internal/theme", fn.Name.Name)
		}
	}
}

// TestShippedPresetsIsParsedExactlyOnce pins the sync.Once: the library is 35
// INI parses, and a caller that got a fresh one per call would pay for it on
// every gallery frame — and would break TestPresetLibraryIsImmutableAcrossCallers
// by accident rather than by design.
func TestShippedPresetsIsParsedExactlyOnce(t *testing.T) {
	a, err := ShippedPresets()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := ShippedPresets()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a != b {
		t.Fatal("ShippedPresets re-parsed — the singleton is not one")
	}
}
