package ui

// The PUBLISHED-VOCABULARY gates for the free-element vocabulary (v1.90.0 W3),
// plus the two shout-condition gates that ride the same tables.
//
// These began as a pair: the document census here, and a SHIPPED-CONTENT census
// over the fourteen themes the repository carried in themes/. The content half was
// the half that mattered, because both defects it existed to catch were invisible
// to a self-consistent test:
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
// A vocabulary is only as real as the content written in it. The themes/ corpus has
// since been retired from the repository, so the content half went with it and what
// remains is the part that needs no corpus: the format document against the live
// tables. Re-derive a census over content you actually ship before trusting a
// vocabulary change again — this file can no longer see that class of defect.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

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
