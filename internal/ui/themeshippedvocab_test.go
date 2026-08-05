package ui

// The SHIPPED-CONTENT gates for the free-element vocabulary (v1.90.0 W3).
//
// Every other element test in this package measures the model against a fixture
// this package wrote. These two measure it against the 14 themes the repository
// SHIPS (themes/*/asyncao_theme.ini, commit 269633a) — because both defects these
// gates exist to catch were invisible to a self-consistent test:
//
//   - `shape` is read as FREE TEXT (sidecar_read.go), not as an enum, so a name the
//     bake does not know degrades to the flat box with no note, no report line and
//     no log. TestEveryShapeNameResolves only ever checked W3's table against
//     itself, so renaming sharp→rect and dropping pill passed it while silently
//     flattening ~200 authored lines across 8 themes.
//   - `visible_when = shout:<name>` compares an authored string against a live one.
//     Three of AO2's four shouts are spelled one way in a theme file (the design
//     key, `hold_it`) and another on the wire (the asset stem, `holdit`), so seven
//     conditions in the shipped themes could never fire — and the one spelling that
//     did work, `shout:objection`, was the one the fixture happened to use.
//
// A vocabulary is only as real as the content written in it. These read the content.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// shippedThemeDir is the repository's own theme folder, relative to the repo root.
const shippedThemeDir = "themes"

// shippedThemeMin is the number of themes that must be found before either gate
// believes it measured anything. 14 shipped at 269633a; the floor is there so a
// path change turns into a failure instead of a vacuous pass.
const shippedThemeMin = 14

// uiRepoRoot walks up from this package to the directory holding go.mod.
func uiRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test's working directory")
		}
		dir = parent
	}
}

// loadShippedSidecars parses every themes/<name>/asyncao_theme.ini through the real
// reader. A parse error is FATAL: the client never refuses a stranger's sidecar
// (format rule 1), but these files are ours, and one that stopped parsing would turn
// both gates below into tests of the empty set.
func loadShippedSidecars(t *testing.T) map[string]*theme.Sidecar {
	t.Helper()
	root := filepath.Join(uiRepoRoot(t), shippedThemeDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	out := map[string]*theme.Sidecar{}
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), theme.SidecarFileName)
		if _, err := os.Stat(path); err != nil {
			continue // an AO2-only theme folder: nothing of ours to check
		}
		found++
		sc, err := theme.LoadSidecar(path)
		if err != nil {
			// EVERY broken theme, not just the first: a cap violation is a whole-file
			// refusal (ErrSidecarCap), so a theme that trips one ships with no AsyncAO
			// tier at all — no elements, no overrides, no palette — and one Fatal would
			// hide the other thirteen.
			t.Errorf("themes/%s: %v — a theme WE ship must parse, or it loads as a bare AO2 theme with "+
				"its entire AsyncAO tier silently dropped", e.Name(), err)
			continue
		}
		if sc == nil {
			t.Errorf("themes/%s parsed to a nil sidecar", e.Name())
			continue
		}
		out[e.Name()] = sc
	}
	if found < shippedThemeMin {
		t.Fatalf("found %d shipped sidecars under %s, want at least %d — the gate is measuring nothing",
			found, root, shippedThemeMin)
	}
	return out
}

// TestShippedThemeShapesAllResolve is the first gate: every `shape =` value written
// in a theme we ship must be IN the vocabulary, not merely survive it.
//
// "Resolves" is checked with elemShapeIDOK, whose second return is the whole point —
// `sharp` degrades to the flat box and looks correct by luck, so a test that only
// compared ids could not tell a match from a fall-through. The failure this prevents
// is the exact one that shipped: `pill` (17 lines, 8 themes) collapsing to a hard
// rectangle, including the register plate whose own comment says the capsule IS its
// identity (themes/thh_trial/asyncao_theme.ini:1200-1209).
func TestShippedThemeShapesAllResolve(t *testing.T) {
	scs := loadShippedSidecars(t)
	used := map[string]int{}
	total := 0
	for name, sc := range scs {
		for i := range sc.Elements {
			el := &sc.Elements[i]
			raw := strings.TrimSpace(el.Shape)
			if raw == "" {
				continue // not a shape element (or one the reader already called inert)
			}
			total++
			used[strings.ToLower(raw)]++
			if _, ok := elemShapeIDOK(raw); !ok {
				t.Errorf("themes/%s [element.%s] shape = %q is not in the element vocabulary — it degrades "+
					"to a flat box SILENTLY (shape is free text, so no degrade note reaches the import "+
					"report). Add the name to elemShapeNames/elemShapeAliases, or fix the theme.",
					name, el.ID, raw)
			}
		}
	}
	if total == 0 {
		t.Fatal("no shipped theme declares a shape — the gate is vacuous")
	}
	// Non-vacuity with teeth: the three names shapemask.go already persists are the
	// ones the shipped content is written in, so each must actually appear. A future
	// rename that "passed" by deleting the content it broke fails here instead.
	for _, want := range []string{shapeSharp, shapeRounded, shapePillKey} {
		if used[want] == 0 {
			t.Errorf("no shipped theme uses shape %q — either the corpus changed or the census is reading "+
				"the wrong files; this gate exists because that vocabulary is the one authors write in", want)
		}
	}
	names := make([]string, 0, len(used))
	for n := range used {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		t.Logf("shipped shape %q: %d elements", n, used[n])
	}
}

// TestShippedThemeConditionsCanAllResolve is the second gate: a `visible_when` in a
// theme we ship must be able to MATCH something.
//
// A condition that can never fire is the worst class of theme bug, because the
// element simply never appears and there is nothing at all to see — no note, no
// fallback, no misplaced box. The shout axis is the only closed vocabulary among the
// valued axes (pos, char and side name whatever a server calls them), so it is the
// only one that can be checked this way, and it is exactly where the defect was.
func TestShippedThemeConditionsCanAllResolve(t *testing.T) {
	scs := loadShippedSidecars(t)
	live := liveShoutStems(t)
	shouts, designKeys := 0, 0
	for name, sc := range scs {
		for i := range sc.Elements {
			el := &sc.Elements[i]
			if el.VisibleWhen.Axis != theme.CondShout {
				continue
			}
			shouts++
			raw := strings.TrimSpace(el.VisibleWhen.Value)
			if raw != elemShoutStem(raw) {
				designKeys++ // written in the design-key spelling and normalised at bake
			}
			if !live[elemConditionValue(el.VisibleWhen)] {
				t.Errorf("themes/%s [element.%s] visible_when = shout:%s can NEVER match: it interns as %q, "+
					"and Courtroom.CurrentShout only ever reports %v. The element is invisible forever, "+
					"with nothing on screen to say so.",
					name, el.ID, raw, elemConditionValue(el.VisibleWhen), sortedKeys(live))
			}
		}
		// An axis this build does not know degrades to always-visible and says so in a
		// note. Ours must not be producing one.
		for _, note := range sc.Notes() {
			if strings.Contains(note, "visible_when") {
				t.Errorf("themes/%s: %s — a theme WE ship must not be writing conditions this build "+
					"cannot parse", name, note)
			}
		}
	}
	if shouts == 0 {
		t.Fatal("no shipped theme gates an element on a shout — the gate is vacuous")
	}
	if designKeys == 0 {
		t.Fatal("no shipped shout condition uses the DESIGN-KEY spelling (hold_it / take_that / " +
			"custom_objection) — that is the spelling the defect broke, so a corpus without it cannot " +
			"prove the normalisation is doing anything")
	}
	t.Logf("%d shout conditions across %d shipped themes, %d of them in the design-key spelling",
		shouts, len(scs), designKeys)
}

// TestShoutConditionTableMatchesCourtroom pins elemShoutConditions against the ONE
// function that decides what a shout is called. The table maps the design key that
// theme files (and themeslots.go:177-180) use onto an objection modifier; the stem
// comes from courtroom.ShoutName, so the two can only drift if a modifier appears or
// disappears — which is what this notices.
func TestShoutConditionTableMatchesCourtroom(t *testing.T) {
	live := liveShoutStems(t)
	if len(elemShoutConditions) != len(live) {
		t.Fatalf("elemShoutConditions holds %d shouts, courtroom.ShoutName produces %d (%v) — a shout "+
			"gained or lost a modifier and the condition vocabulary did not follow",
			len(elemShoutConditions), len(live), sortedKeys(live))
	}
	seen := map[string]string{}
	for _, s := range elemShoutConditions {
		stem := courtroom.ShoutName(s.objection)
		if stem == "" {
			t.Fatalf("design key %q maps to objection %d, which is not a shout", s.key, s.objection)
		}
		if prev, dup := seen[stem]; dup {
			t.Fatalf("design keys %q and %q both map to stem %q — the interned values would collide",
				prev, s.key, stem)
		}
		seen[stem] = s.key
		if themeSlotFor(s.key) == nil {
			t.Errorf("shout design key %q is not a themeSlots row — the condition vocabulary must be the "+
				"same spelling `anchor = %s` uses, or one line of a theme means two different things",
				s.key, s.key)
		}
		// Both spellings, in any case, fold onto the live stem.
		for _, spelling := range []string{s.key, stem, strings.ToUpper(s.key), " " + stem + " "} {
			if got := elemShoutStem(spelling); got != stem {
				t.Errorf("elemShoutStem(%q) = %q, want %q", spelling, got, stem)
			}
		}
	}
	// A value naming no shout is returned verbatim: it interns, it never matches, and
	// it is not silently turned into some other shout.
	if got := elemShoutStem("not_a_shout"); got != "not_a_shout" {
		t.Errorf("elemShoutStem(%q) = %q, want it returned verbatim", "not_a_shout", got)
	}
	// And a non-shout axis is never touched: `pos:hold_it` is a position called
	// hold_it, however unlikely, not a shout.
	cond := theme.Condition{Axis: theme.CondPos, Value: "hold_it"}
	if got := elemConditionValue(cond); got != "hold_it" {
		t.Errorf("elemConditionValue on a pos axis rewrote %q to %q — only the shout axis normalises",
			cond.Value, got)
	}
}

// TestShoutConditionInternsAsTheLiveStem is the pipeline end to end, minus the room:
// a sidecar written in the DESIGN-KEY spelling interns the stem, and the per-axis
// resolver — handed exactly what refreshElementConditions hands it — lights the
// element up.
func TestShoutConditionInternsAsTheLiveStem(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	sc := theme.NewSidecar()
	for _, s := range elemShoutConditions {
		sc.Elements = append(sc.Elements, theme.Element{
			ID:          "glow_" + s.key,
			Kind:        theme.ElemGradient,
			Band:        theme.BandMid,
			Space:       theme.SpaceCourtroom,
			Rect:        theme.Rect{X: 10, Y: 10, W: 40, H: 40},
			Fill:        theme.RGBA{R: 255, A: 255},
			VisibleWhen: theme.Condition{Axis: theme.CondShout, Value: s.key},
		})
	}
	a.themeSidecar = sc
	a.themeLay.valid = false
	a.drawCourtroom(1280, 720)
	lay := &a.themeLay
	if !lay.valid || !a.toolboxThemeRectOn {
		t.Fatal("the fixture did not reach the themed branch — nothing was baked")
	}
	if lay.elN != len(elemShoutConditions) {
		t.Fatalf("baked %d elements, want %d", lay.elN, len(elemShoutConditions))
	}
	for i, s := range elemShoutConditions {
		stem := courtroom.ShoutName(s.objection)
		e := &lay.el[i]
		if e.cond < 0 || int(e.cond) >= lay.condN {
			t.Fatalf("the element gated on shout:%s interned no condition (cond=%d)", s.key, e.cond)
		}
		if got := lay.condVal[e.cond]; got != stem {
			t.Fatalf("shout:%s interned as %q, want the LIVE stem %q — the design key is what themes "+
				"write and the stem is what Courtroom.CurrentShout reports", s.key, got, stem)
		}
		// The live half: resolve the axis to exactly what stageShout would have said.
		a.resolveConditionAxis(lay, theme.CondShout, stem)
		if !a.elementVisible(e) {
			t.Fatalf("with the stage showing %q, the element gated on shout:%s stayed hidden", stem, s.key)
		}
		// And the gate can still say no: another shout must not light this one up.
		other := courtroom.ShoutName(otherObjection(s.objection))
		a.resolveConditionAxis(lay, theme.CondShout, other)
		if a.elementVisible(e) {
			t.Fatalf("with the stage showing %q, the element gated on shout:%s painted anyway — the "+
				"condition is not gating", other, s.key)
		}
	}
}

// liveShoutStems is the set of values Courtroom.CurrentShout can ever report, taken
// from courtroom.ShoutName itself over the modifier range AO2 defines
// (datatypes.h / urlbuilder.go:543 — 1..4, everything else is "no shout").
func liveShoutStems(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	// Deliberately scanned past the known range: a fifth shout added upstream shows
	// up here as a table mismatch rather than as a theme that never paints.
	for mod := 0; mod <= 8; mod++ {
		if s := courtroom.ShoutName(mod); s != "" {
			out[s] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("courtroom.ShoutName produced no stems at all")
	}
	return out
}

// otherObjection picks a DIFFERENT shout modifier, so a gate can be shown to say no.
func otherObjection(mod int) int {
	if mod == elemShoutConditions[0].objection {
		return elemShoutConditions[1].objection
	}
	return elemShoutConditions[0].objection
}

// sortedKeys renders a set for a failure message in a stable order (a map's own
// order would make the same failure read differently on every run).
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
