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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

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

// publishedShapeFence is the line in docs/THEME-FORMAT.md §3 that publishes the
// silhouette vocabulary, as a pipe-separated list inside a fenced block.
const publishedShapeHeading = "**`shape` names a silhouette.**"

// TestPublishedShapeVocabularyMatchesTheConstants census-pins the shape names the
// FORMAT DOCUMENT publishes against elemShapeNames + elemShapeAliases.
//
// It is the internal/ui sibling of theme.TestPublishedCapsMatchTheConstants, and it
// exists for the same reason that one does: docs/THEME-FORMAT.md is the contract a
// third-party writer implements against, so a name in the code and not the document
// is undiscoverable, and a name in the document and not the code degrades a
// conforming file to a flat box with no note (shape is free text — sidecar_read.go
// never sees it as an enum, so nothing reports the mismatch at runtime).
//
// The vocabulary lives in internal/ui rather than internal/theme, which is why the
// census cannot ride the theme package's own doc gate.
func TestPublishedShapeVocabularyMatchesTheConstants(t *testing.T) {
	doc := readFormatDoc(t)
	head := strings.Index(doc, publishedShapeHeading)
	if head < 0 {
		t.Fatalf("docs/THEME-FORMAT.md no longer contains %q — the census is reading nothing; "+
			"repoint publishedShapeHeading rather than deleting the gate", publishedShapeHeading)
	}
	rest := doc[head:]
	open := strings.Index(rest, "```")
	if open < 0 {
		t.Fatal("no fenced block follows the shape heading in docs/THEME-FORMAT.md")
	}
	body := rest[open+3:]
	close := strings.Index(body, "```")
	if close < 0 {
		t.Fatal("the shape vocabulary's fenced block is never closed")
	}
	published := map[string]bool{}
	for _, name := range strings.Split(body[:close], "|") {
		if n := strings.TrimSpace(name); n != "" {
			published[n] = true
		}
	}
	if len(published) == 0 {
		t.Fatal("the published shape list parsed empty — the census is vacuous")
	}
	// Every canonical name is published, and nothing is published that does not
	// resolve. Both directions, because each failure mode is silent in its own way.
	for _, name := range elemShapeNames {
		if !published[name] {
			t.Errorf("shape %q resolves in the client but docs/THEME-FORMAT.md does not publish it — "+
				"a name nobody can discover is a name nobody writes", name)
		}
		delete(published, name)
	}
	for name := range published {
		id, ok := elemShapeIDOK(name)
		if !ok {
			t.Errorf("docs/THEME-FORMAT.md publishes shape %q and the client does not resolve it — a "+
				"conforming theme written against the document degrades to %q, silently",
				name, elemShapeNames[id])
		}
	}
	// The aliases are published as prose, not in the fence — each one has to be
	// named somewhere in the document or it is an undocumented spelling we can
	// never remove.
	for _, al := range elemShapeAliases {
		if !strings.Contains(doc, "`"+al.name+"`") {
			t.Errorf("shape alias %q (→ %q) is accepted by the client but appears nowhere in "+
				"docs/THEME-FORMAT.md — aliases are append-only, so an undocumented one is a "+
				"permanent obligation nobody wrote down", al.name, elemShapeNames[al.id])
		}
		if int(al.id) >= len(elemShapeNames) {
			t.Errorf("shape alias %q maps to id %d, outside the vocabulary", al.name, al.id)
		}
	}
}

// readFormatDoc loads docs/THEME-FORMAT.md relative to the repository root.
func readFormatDoc(t *testing.T) string {
	t.Helper()
	p := filepath.Join(uiRepoRoot(t), "docs", "THEME-FORMAT.md")
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v — the published format is part of the contract; repoint the path rather "+
			"than dropping the census", p, err)
	}
	return string(src)
}

// publishedGenParamHeading is the §5 subsection that publishes the generator
// parameter vocabulary as a slot table.
const publishedGenParamHeading = "### The parameter vocabulary"

// publishedGenTableHeading is the §5 subsection that publishes the generator names
// themselves. Deliberately NOT spelled with the count in it — a heading a new
// generator has to renumber is a heading somebody forgets to renumber.
const publishedGenTableHeading = "| Name | Serves | Key parameters |"

// docBacktickedWord pulls every `word` out of a table cell.
var docBacktickedWord = regexp.MustCompile("`([a-z0-9_]+)`")

// TestPublishedGeneratorVocabularyMatchesTheTables is the generator sibling of the
// shape census above, and it exists because the generator surface is the ONE place
// in this format where the same slot has several legal spellings.
//
// The aliases are not a convenience: `cells` is what a checkerdisc author calls its
// pitch and `radius` is what a glow author calls its size, and every one of them is
// a word the shipped themes and the preset drafts already write. That makes them a
// PERMANENT obligation — an unrecognised parameter key is *ignored*, silently, so
// removing a spelling deletes the parameter from every theme that used it with
// nothing anywhere to say so. A permanent obligation nobody wrote down is the worst
// kind, so the document has to carry the whole list, and this is what keeps it
// carrying the whole list.
//
// Both directions, and both tables: a key in the code and not the document is
// undiscoverable, a key in the document and not the code degrades a conforming
// theme silently, and a generator NAME missing from the document is a feature
// nobody can reach.
func TestPublishedGeneratorVocabularyMatchesTheTables(t *testing.T) {
	doc := readFormatDoc(t)
	head := strings.Index(doc, publishedGenParamHeading)
	if head < 0 {
		t.Fatalf("docs/THEME-FORMAT.md no longer contains %q — the census is reading nothing; repoint the "+
			"heading rather than deleting the gate", publishedGenParamHeading)
	}
	published := map[string]bool{}
	for _, line := range strings.Split(doc[head:], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			if len(published) > 0 {
				break // the table ended
			}
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 3 || strings.HasPrefix(strings.TrimSpace(cells[0]), "---") {
			continue // the separator row, or a table that is not this one
		}
		for _, m := range docBacktickedWord.FindAllStringSubmatch(cells[1], -1) {
			published[m[1]] = true
		}
	}
	if len(published) == 0 {
		t.Fatal("the published parameter table parsed empty — the census is vacuous")
	}
	// The counts in the prose above the table are part of the claim: a document that
	// says "25 accepted keys" over a table of 26 is worse than one that says nothing,
	// because a reader trusts the sentence and stops counting.
	for _, phrase := range []string{
		fmt.Sprintf("**%d accepted keys**", len(genParamSlots)),
		fmt.Sprintf("**%d slots**", int(genSlotPct1)+1),
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("docs/THEME-FORMAT.md no longer states %q", phrase)
		}
	}
	for key := range genParamSlots {
		if !published[key] {
			t.Errorf("generator parameter %q resolves in the client and docs/THEME-FORMAT.md does not "+
				"publish it — an alias nobody can discover is one nobody writes, and one we can never remove", key)
		}
		delete(published, key)
	}
	for key := range published {
		t.Errorf("docs/THEME-FORMAT.md publishes generator parameter %q and the client ignores it — a theme "+
			"written against the document loses that parameter with no degrade note (an unknown key is "+
			"skipped by design)", key)
	}

	// The GENERATOR table, parsed on its own heading — both directions again.
	listed := map[string]bool{}
	if h := strings.Index(doc, publishedGenTableHeading); h < 0 {
		t.Errorf("docs/THEME-FORMAT.md no longer contains %q", publishedGenTableHeading)
	} else {
		for _, line := range strings.Split(doc[h:], "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "|") {
				if len(listed) > 0 {
					break
				}
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			if len(cells) != 3 {
				continue
			}
			if m := docBacktickedWord.FindStringSubmatch(cells[0]); m != nil {
				listed[m[1]] = true
			}
		}
	}
	for _, name := range GeneratorNames() {
		if !listed[name] {
			t.Errorf("generator %q rasterises and is not in docs/THEME-FORMAT.md's generator table — the "+
				"table is the only place an author finds out it exists", name)
		}
		delete(listed, name)
	}
	for name := range listed {
		t.Errorf("docs/THEME-FORMAT.md lists a generator %q the client does not rasterise — a theme written "+
			"against the document degrades to a flat fill", name)
	}
}

// TestShippedThemesEmitNoDegradeNotesAtAll widens the note check that used to live
// inside the condition gate above — where it only ever looked for the substring
// "visible_when" — to EVERY note the reader can produce.
//
// The narrow version was blind by construction. A degrade note is the reader saying
// "this line meant something and I could not use it", and there is no reason the
// only interesting ones would mention conditions: the defect this widening actually
// caught was four of themes/thh_trial's five [palette] roles being refused, because
// parseRGBAValue reached hexNibble with the author's UPPER-CASE hex and hexNibble
// only knew a-f. `panel = #101010` parsed (all digits), `panel_hi = #1C1C1C` did
// not, and the theme shipped with the stylesheet's colours where its own were
// meant to be. Nothing on screen said so — which is precisely why the sidecar's
// visibility surface (the refusal chip) and this gate landed in the same wave.
//
// A note is fine in a STRANGER's theme; it is the format's rule 3 working. In one
// of ours it is a bug in the theme or a gap in the reader, and either way somebody
// has to look at it.
func TestShippedThemesEmitNoDegradeNotesAtAll(t *testing.T) {
	scs := loadShippedSidecars(t)
	for name, sc := range scs {
		for _, note := range sc.Notes() {
			t.Errorf("themes/%s: %s — a theme WE ship must not degrade anything. Either the theme is "+
				"writing something this build cannot read, or the reader is refusing something it should "+
				"accept; both are defects and neither is visible on screen.", name, note)
		}
	}
	t.Logf("%d shipped sidecars, 0 degrade notes", len(scs))
}

// TestShippedThemeClocksAndEffectsAllResolve is the W5 sibling of the two gates
// above, and it exists for the same reason they do: the motion vocabulary is
// already WRITTEN — all fourteen shipped themes carry `[effect.*]` sections, and
// all fourteen carry `[clock.N]` groups as well (29 groups across the corpus) —
// so a resolver that grew no arm for a kind, or a bind pointed at a widget key
// that does not exist, is content that silently does nothing. There is no note,
// no fallback and no misplaced box; the theme just "looks a bit plain", which is
// the class of bug nobody reports.
//
// Four failure modes, each of them invisible on screen:
//
//  1. a bind whose target is not a themeSlots key — absSlotRect can never find it,
//     so §6.6 R10's skip-if-absent makes the whole section inert forever;
//  2. a bind that bakes inert anyway (no effect, no amplitude, no colour) — the
//     author wrote a section that draws nothing;
//  3. an effect kind the resolver never moves anything for;
//  4. a clock group whose speed left the format's range, which would put a
//     divisor the pool has to defend against into elementElapsed.
func TestShippedThemeClocksAndEffectsAllResolve(t *testing.T) {
	scs := loadShippedSidecars(t)
	binds, moving, clocks := 0, 0, 0
	kinds := map[string]int{}
	for name, sc := range scs {
		for i := range sc.Effects {
			b := &sc.Effects[i]
			binds++
			kinds[b.Effect.String()]++
			if themeSlotFor(b.Target) == nil {
				t.Errorf("themes/%s [effect.%s] names no AO2 widget — absSlotRect can never resolve it, so "+
					"the binding is skip-if-absent FOREVER (§6.6 R10) with nothing on screen to say so",
					name, b.Target)
			}
			if b.Effect == theme.FXNone || b.AmpPct <= 0 || !b.Color.Opaque() {
				t.Errorf("themes/%s [effect.%s] bakes INERT (effect=%s amp_pct=%d colour opaque=%v) — a "+
					"binding needs all three, and one that is missing simply does not draw",
					name, b.Target, b.Effect, b.AmpPct, b.Color.Opaque())
			}
			if effectMovesSomething(bakedEffect{kind: b.Effect, periodMs: b.PeriodMs, ampPct: b.AmpPct}) {
				moving++
			} else if b.AmpPct > 0 {
				t.Errorf("themes/%s [effect.%s] names effect %q and the resolver never leaves the neutral "+
					"transform for it across a whole cycle — the section is decoration that cannot animate",
					name, b.Target, b.Effect)
			}
		}
		for i := range sc.Elements {
			el := &sc.Elements[i]
			if el.Effect == theme.FXNone {
				continue
			}
			kinds[el.Effect.String()]++
			fx := bakedEffect{kind: el.Effect, periodMs: el.EffectPeriodMs, ampPct: el.EffectAmpPct}
			if el.EffectAmpPct > 0 && !effectMovesSomething(fx) {
				t.Errorf("themes/%s [element.%s] names effect %q at amp_pct %d and the resolver never "+
					"leaves the neutral transform for it — the element is authored to move and does not",
					name, el.ID, el.Effect, el.EffectAmpPct)
			}
			if int(el.Clock) >= theme.ClockCap {
				t.Errorf("themes/%s [element.%s] clock = %d is outside the pool", name, el.ID, el.Clock)
			}
		}
		// Elements and bindings share ONE array (theme.ElementCap), and the bake fills
		// it with the elements first — so a theme that crowds the array loses its
		// bindings, silently and last-declared-first. The count is the only place that
		// is visible before somebody notices a glow missing. thh_trial is the tight one
		// today at 78 + 9 = 87 of 96.
		if n := len(sc.Elements) + len(sc.Effects); n > theme.ElementCap {
			t.Errorf("themes/%s declares %d elements + %d bindings = %d, past theme.ElementCap (%d) — the "+
				"bake fills the array with elements first, so this theme's LAST bindings never draw",
				name, len(sc.Elements), len(sc.Effects), n, theme.ElementCap)
		}
		for i := range sc.Clocks {
			c := sc.Clocks[i]
			if !c.Declared {
				continue
			}
			clocks++
			if got := clampClockSpeed(c.SpeedPct); got != c.EffectivePct() {
				t.Errorf("themes/%s [clock.%d] speed_pct = %d loads as %d but the pool clamps it to %d — "+
					"the reader and the pool disagree about the same file",
					name, i, c.SpeedPct, c.EffectivePct(), got)
			}
		}
	}
	// Non-vacuity, in the direction that actually goes wrong: the corpus is the
	// reason this gate exists, so a path change that stopped finding the sections
	// must fail rather than pass silently.
	if binds == 0 || clocks == 0 {
		t.Fatalf("found %d effect bindings and %d declared clock groups across %d shipped themes — the "+
			"gate is reading nothing", binds, clocks, len(scs))
	}
	if moving != binds {
		t.Errorf("%d of %d shipped bindings resolve to motion", moving, binds)
	}
	for _, n := range sortedCountKeys(kinds) {
		t.Logf("shipped effect %q: %d declarations", n, kinds[n])
	}
}

// effectMovesSomething reports whether the resolver leaves the neutral transform
// anywhere in one cycle of an effect.
//
// SAMPLED ACROSS THE PERIOD, never at one point: every periodic kind rides a raised
// cosine that is neutral at its own start, so a single sample would be a coin flip
// and this gate would fail (or pass) at random.
func effectMovesSomething(fx bakedEffect) bool {
	period := time.Duration(effectPeriodMs(fx.periodMs)) * time.Millisecond
	for step := 1; step <= 16; step++ {
		if !fxNeutralTerms(resolveElementEffect(fx, period*time.Duration(step)/16, &fxState{})) {
			return true
		}
	}
	return false
}

// sortedCountKeys renders a census map in a stable order.
func sortedCountKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
