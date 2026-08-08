package theme

// THE SHIPPED PRESET'S PROSE IS DOCUMENTATION, NOT A WORK LOG (v1.90.0 W9).
//
// Every `;` comment in internal/theme/presets/ reaches a THEME AUTHOR: the
// fragments are the worked examples the format ships, they are what a `.aotheme`
// starts life as, and the editor's "open the preset" path puts them on screen
// verbatim. So a comment there may only say things the reader can act on with
// the file in front of them.
//
// The class this file closes is the opposite: prose that survived the authoring
// pass still pointing at the wave's own paperwork — a planning dossier that was
// never in the tree, a tier letter off a triage table nobody outside the wave
// has, an internal Go enum name (`FXShake`) instead of the value an author
// actually types (`shake`), a wave/round id. Three such lines shipped past a
// hand review and a post-pass lint that measured line length, trailing
// whitespace, block length and quote balance — all four are SHAPE rules, and
// none of them can see a word. This is the vocabulary rule they were missing,
// and finding four MORE offenders the hand grep had walked past is the argument
// for having it be a test instead of a grep.
//
// The rule is deliberately absolute where a clever rule would be fragile: the
// whole "tier" family is banned rather than only "tier (b)", because the next
// author writing "tier 1 of 2" is one edit away from "tier (b)" and a reviewer
// cannot tell the two apart at a glance. Words the corpus legitimately needs
// (`refs`, `[sampled]`, `CORRECTED`) are NOT banned, and cleanCommentSamples
// pins them so a later widening of a pattern fails here rather than forcing a
// corpus-wide rewrite.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme/presets"
)

// presetProseMinCommentsPerFragment is the floor the scan must clear IN EVERY
// SHIPPED FRAGMENT before its silence means anything.
//
// PER FRAGMENT, not per corpus, and that is the whole lesson of this constant.
// A corpus-wide floor is a number that cannot see WHICH files were read: 200
// against a ~1500-line comment corpus stayed green with the entire layout axis
// dropped from the walk, because the style axis alone clears it seven times
// over. The file set is now the library's own (eachPresetLine), so coverage is
// exact and this floor only has to catch the remaining hole — a fragment that
// was opened and yielded nothing. The leanest shipped fragment carries eight
// comment lines, so five is below any fragment anyone would write and far above
// zero.
const presetProseMinCommentsPerFragment = 5

// repoRootFromTheme is this package's path back to the checkout root, used to
// resolve the document references a comment makes.
const repoRootFromTheme = "../.."

// presetProseRule is one banned turn of phrase plus the reason a theme author
// cannot act on it, and a sample that MUST trip it. The sample lives in the
// table so that adding a rule without a proof it fires is not possible.
type presetProseRule struct {
	name   string
	re     *regexp.Regexp
	why    string
	sample string
}

// presetProseBan is the vocabulary rule. Every entry names something that exists
// only inside the project's own process: a planning document, a triage axis, a
// Go identifier, or a work-item id.
var presetProseBan = []presetProseRule{
	{
		name:   "dossier",
		re:     regexp.MustCompile(`(?i)\bdossiers?\b`),
		why:    "names a planning document that is not in the tree, so an author cannot read it",
		sample: "closest shipped kind to the dossier's ease-out-back slide-in",
	},
	{
		name: "tier",
		// The whole family: "tier (b)", "tiered (d1)", "tiering row 3", "Tier-1".
		// An author has no tier list, and a rule that allowed the innocent-looking
		// members would be a rule a reviewer cannot apply by eye.
		re:     regexp.MustCompile(`(?i)\btier(s|ed|ing)?\b`),
		why:    "the wave's private triage axis; a theme author has no tier table to look it up in",
		sample: "the hard square-wave version is tiered (b) in the tiering file",
	},
	{
		name:   "work-item id",
		re:     regexp.MustCompile(`\b[WR][0-9]+\b`),
		why:    "a wave or round id; it dates the file and means nothing to a reader",
		sample: "`pct = 0` keeps R6's new perspective knob off",
	},
	{
		name: "process noun",
		// Spelled-out schedule slices ("Wave 9", "round 2", "lane A"). `phase` is
		// deliberately NOT here: it is a shipped generator parameter and a band
		// name in this corpus, so banning it would outlaw the file's own subject.
		re:     regexp.MustCompile(`(?i)\b(wave|round|lane)\s+[0-9A-Za-z]\b`),
		why:    "names a slice of the project's schedule rather than anything in the file",
		sample: "DRAFT for Wave 9",
	},
	{
		name:   "planning vocabulary",
		re:     regexp.MustCompile(`(?i)\b(escalation|handoff|the brief|proposals?|dossiers?|docs/wip)\b`),
		why:    "process paperwork; the file must describe what it does, not how it was decided",
		sample: "change this one word if the FXTilt proposal lands",
	},
	{
		name: "internal identifier",
		// `FXShake` is the Go enum; `shake` is what an author types. Quoting the
		// former also smuggles in names that never shipped at all (FXGlitch,
		// FXBlink, FXTilt were three of the five found here).
		re:     regexp.MustCompile(`\bFX[A-Z][A-Za-z]*`),
		why:    "a Go identifier, not the value an author writes; use the authored word (`shake`, not `FXShake`)",
		sample: "all four are FXFade/FXShake, decaying and self-clearing",
	},
}

// presetProseFormatKeys are keys of the FORMAT whose names collide with the
// banned vocabulary. `shape_tier` is a published chrome key an author sets, so a
// comment explaining it must stay legal even though "tier" is banned as a word.
// They are blanked out of the comment before the table runs, which is narrower
// than weakening the patterns and keeps the collision visible in one place.
var presetProseFormatKeys = []string{"shape_tier"}

// presetProseRequiredRules is the deletion catcher. A rule may be renamed only
// by editing this list too, which is a visible act; silently dropping one so a
// new comment can pass is not.
var presetProseRequiredRules = []string{
	"dossier", "tier", "work-item id", "process noun", "planning vocabulary", "internal identifier",
}

// cleanCommentSamples are real phrases from the corpus that must stay legal.
// They are the guard on the other side: every pattern above is broad on purpose,
// and this is where "broad" stops being "wrong".
var cleanCommentSamples = []string{
	"the matte inner bezel, PIXEL-sampled from both VT100 refs",
	"[sampled] the top exact colour in 4 of 7 refs. Neutral R=G=B, not blue-tinted.",
	"newsprint. CORRECTED from #F5F2E8, the biggest single fix",
	"Format: docs/THEME-FORMAT.md.",
	"a hard square-wave blink is not one of the shipped effect kinds",
	"shape = rounded, and the corner rounds with the panel",
	"one stretched radial glow tile does that for the cost of a single blit",
	"the shout beat: 0.6 -> 1.15 -> 1.0 ease-out-back overshoot",
	"`shape_tier = 0` keeps every corner sharp",
	"MID — frames over the stage phase. Transparent fills ONLY.",
	"the rule around it is #000000, never the other way round.",
	"sky tone P = 1/147 of frame width -> 714/147 = 4.9, rounded to 6",
}

// presetDocReference matches a document a comment points the author at. Only
// markdown is checked: those are the references the corpus actually makes, and
// an .ini path in a comment is a preset id the library already gates.
var presetDocReference = regexp.MustCompile(`[A-Za-z0-9_./-]+\.md\b`)

// presetCommentOf returns the comment on one preset line, using the READER's own
// comment rule (iniCommentStart) rather than a second copy of it: if the parser
// ever stopped treating ';' this way, this lint would follow it instead of
// quietly scanning text that is no longer a comment.
func presetCommentOf(line string) string {
	i := strings.IndexByte(line, iniCommentStart)
	if i < 0 {
		return ""
	}
	return line[i+1:]
}

// eachPresetLine visits every line of every shipped fragment on every axis. One
// walk, four callers: the vocabulary gate, the citation gate, the allow-list
// guard and the coverage gate all have to agree about what "the corpus" is.
//
// THE FILE SET IS THE LIBRARY'S, NOT A DIRECTORY LISTING'S. It used to be a
// literal `[]string{presets.LayoutDir, presets.StyleDir}`, and editing that list
// down to one entry left all three gates passing while 21 fragments went unread
// — the corpus quietly became half a corpus and nothing said so. So the axes are
// enumerated over the ENUM's full range and the ids come from ShippedPresets():
// there is no list here to shorten, and presetCorpusCoverage re-derives the
// expectation independently afterwards, so a walk that stops enumerating still
// has to answer for what it skipped.
func eachPresetLine(t *testing.T, visit func(path string, n int, line string)) {
	t.Helper()
	lib := shippedLibraryFor(t)
	var visited [PresetKindCount]map[string]bool
	for i := range visited {
		visited[i] = map[string]bool{}
	}
	for kind := PresetKind(0); kind < PresetKindCount; kind++ {
		for _, p := range lib.axis(kind) {
			path := presetFragmentPath(kind, p.ID)
			raw, err := presets.FS.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			visited[kind][p.ID] = true
			for n, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
				visit(path, n+1, line)
			}
		}
	}
	for _, gap := range presetCorpusCoverage(lib, visited) {
		t.Errorf("the walk was not the corpus: %s", gap)
	}
}

// presetCorpusCoverage is the verdict on a corpus walk: it compares what the
// walk visited against the SHIPPED LIBRARY and returns one sentence per way the
// two disagree.
//
// IT ENUMERATES THE AXES ITSELF, over the enum's whole range, and that is the
// point rather than an implementation detail — the walk's own loop is the thing
// under suspicion, so a verdict that asked the walk which axes to check would
// agree with a walk that reads one of them. A missing axis, a missing fragment
// and a visit to something the library does not hold get three different
// sentences because they have three different causes: a shortened loop, a file
// the loader dropped, and a walk reading files that do not ship.
//
// A library with an EMPTY axis is itself a complaint, so passing nil (or a
// library that failed to load) cannot make this return "all clear".
func presetCorpusCoverage(lib *PresetLibrary, visited [PresetKindCount]map[string]bool) []string {
	var out []string
	for k := PresetKind(0); k < PresetKindCount; k++ {
		want, seen := lib.axis(k), visited[k]
		if len(want) == 0 {
			out = append(out, fmt.Sprintf("the library holds no %s fragments at all, so walking that axis proves nothing", k))
			continue
		}
		if len(seen) == 0 {
			out = append(out, fmt.Sprintf("the %s axis was never opened: all %d shipped fragments under %s/ went unread, "+
				"and every rule in this file is silent about them", k, len(want), presetAxisDir(k)))
			continue
		}
		for _, p := range want {
			if !seen[p.ID] {
				out = append(out, fmt.Sprintf("%s ships but the walk never read it", presetFragmentPath(k, p.ID)))
			}
		}
		var extra []string
		for id := range seen {
			if p, err := lib.Find(k, id); err != nil || p == nil {
				extra = append(extra, id)
			}
		}
		sort.Strings(extra) // map order is not a failure message
		for _, id := range extra {
			out = append(out, fmt.Sprintf("the walk read %s, which the library does not hold — it is linting a file that does not ship",
				presetFragmentPath(k, id)))
		}
	}
	return out
}

// TestShippedPresetProseIsAuthorFacing walks every comment in the shipped corpus
// and fails on the vocabulary a theme author cannot act on.
func TestShippedPresetProseIsAuthorFacing(t *testing.T) {
	lines := map[string]int{}
	comments := map[string]int{}
	eachPresetLine(t, func(path string, n int, line string) {
		lines[path]++
		c := presetCommentOf(line)
		if strings.TrimSpace(c) == "" {
			return
		}
		comments[path]++
		for _, hit := range presetProseHits(c) {
			t.Errorf("%s:%d: %q — %s\n    %s", path, n,
				hit.match, presetProseWhy(hit.rule), strings.TrimSpace(line))
		}
	})
	// Which fragments were read is eachPresetLine's promise (presetCorpusCoverage);
	// what was read INSIDE each one is this floor's, per file, so a fragment that
	// opened and yielded nothing cannot hide behind its noisier neighbours.
	paths := make([]string, 0, len(lines))
	for p := range lines {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if comments[p] < presetProseMinCommentsPerFragment {
			t.Errorf("%s: the walk found %d comment line(s) in %d lines, expected at least %d — "+
				"a lint that read nothing passes everything",
				p, comments[p], lines[p], presetProseMinCommentsPerFragment)
		}
	}
}

// TestShippedPresetProseOnlyCitesDocumentsInTheTree is the other half of the same
// promise: a comment may cite a document, but only one the reader can open. Every
// citation in the corpus today is docs/THEME-FORMAT.md, which is the format's
// published reference; a citation of a draft, a design note or a proposal file
// fails here whether or not its name trips the vocabulary table.
func TestShippedPresetProseOnlyCitesDocumentsInTheTree(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRootFromTheme, "go.mod")); err != nil {
		t.Skipf("no checkout root above this package: %v", err)
	}
	var cited int
	eachPresetLine(t, func(path string, n int, line string) {
		for _, ref := range presetDocReference.FindAllString(presetCommentOf(line), -1) {
			cited++
			if _, err := os.Stat(filepath.Join(repoRootFromTheme, filepath.FromSlash(ref))); err != nil {
				t.Errorf("%s:%d: cites %q, which is not in the checkout — a shipped preset "+
					"may only point an author at a file they have", path, n, ref)
			}
		}
	})
	if cited == 0 {
		t.Error("no preset comment cites the format reference at all — every fragment used to " +
			"end its header with docs/THEME-FORMAT.md, and losing that is losing the one pointer " +
			"an author needs")
	}
}

// TestPresetProseLintCatchesTheVocabularyItBans is the encapsulation gate (hard
// rule 11): a lint nobody has watched fail is a lint nobody knows works. Each
// rule must fire on its own sample, no rule may fire on the corpus's legitimate
// phrasing, and the required-rule list must still be intact — so gutting the
// table to make a new comment pass fails HERE, loudly, instead of turning the
// corpus walk into a no-op that stays green forever.
func TestPresetProseLintCatchesTheVocabularyItBans(t *testing.T) {
	byName := map[string]presetProseRule{}
	for _, r := range presetProseBan {
		if _, dup := byName[r.name]; dup {
			t.Errorf("two rules are both named %q — the failure message could not tell them apart", r.name)
		}
		byName[r.name] = r
	}
	for _, want := range presetProseRequiredRules {
		if _, ok := byName[want]; !ok {
			t.Errorf("the %q rule is gone; deleting a rule is a decision, not a cleanup", want)
		}
	}

	for _, r := range presetProseBan {
		if r.sample == "" || r.why == "" {
			t.Errorf("rule %q ships without a %s", r.name, map[bool]string{true: "sample", false: "reason"}[r.sample == ""])
			continue
		}
		hits := presetProseHits(r.sample)
		var fired bool
		for _, h := range hits {
			if h.rule == r.name {
				fired = true
			}
		}
		if !fired {
			t.Errorf("rule %q does not catch its own sample %q — the pattern is dead", r.name, r.sample)
		}
	}

	for _, clean := range cleanCommentSamples {
		for _, h := range presetProseHits(clean) {
			t.Errorf("rule %q fires on legitimate prose %q (matched %q) — the pattern is too broad, "+
				"and widening it silently would force a corpus rewrite", h.rule, clean, h.match)
		}
	}

	// The allow list is the one hole in the table, so it is nailed shut: an entry
	// only earns its exemption by being a KEY the corpus actually writes. Adding a
	// word here to make a comment pass fails unless that word is also a real key,
	// which no banned term is.
	written := map[string]bool{}
	eachPresetLine(t, func(_ string, _ int, line string) {
		body := line
		if i := strings.IndexByte(body, iniCommentStart); i >= 0 {
			body = body[:i]
		}
		if k, _, ok := strings.Cut(body, "="); ok {
			written[strings.TrimSpace(k)] = true
		}
	})
	for _, k := range presetProseFormatKeys {
		if !written[k] {
			t.Errorf("%q is exempted from the vocabulary rule but no preset writes it as a key — "+
				"the allow list holds format keys, not excuses", k)
		}
	}
}

// TestPresetCorpusWalkReadsEveryShippedFragment is the axis-loss gate, and it
// drives the real walk rather than the verdict function so that it survives the
// verdict being unhooked from eachPresetLine.
//
// THE REGRESSION IT PINS: replacing the walk's axis source with a single axis
// left all three prose tests green while a whole axis — 21 fragments, several
// hundred comment lines — stopped being linted. Nothing failed, because the
// rules only ever speak about text they were handed. Here the expectation comes
// from the library instead of from the walk, so "what did you read" and "what
// ships" are two independent answers that have to match.
func TestPresetCorpusWalkReadsEveryShippedFragment(t *testing.T) {
	lib := shippedLibraryFor(t)
	type fragment struct {
		kind PresetKind
		id   string
	}
	ships := map[string]fragment{}
	for k := PresetKind(0); k < PresetKindCount; k++ {
		for _, p := range lib.axis(k) {
			ships[presetFragmentPath(k, p.ID)] = fragment{k, p.ID}
		}
	}
	if len(ships) == 0 {
		t.Fatal("the shipped library is empty, so this gate would pass on a walk that read nothing")
	}

	var visited [PresetKindCount]map[string]bool
	for i := range visited {
		visited[i] = map[string]bool{}
	}
	strangers := map[string]bool{}
	eachPresetLine(t, func(path string, _ int, _ string) {
		f, ok := ships[path]
		if !ok {
			strangers[path] = true
			return
		}
		visited[f.kind][f.id] = true
	})

	for _, gap := range presetCorpusCoverage(lib, visited) {
		t.Error(gap)
	}
	odd := make([]string, 0, len(strangers))
	for p := range strangers {
		odd = append(odd, p)
	}
	sort.Strings(odd)
	for _, p := range odd {
		t.Errorf("the walk yielded lines from %q, which is not a fragment the library holds", p)
	}
}

// TestPresetCorpusCoverageCatchesADroppedAxis is the encapsulation half (hard
// rule 11): the coverage verdict cannot fire on the shipped corpus, so a verdict
// nobody has watched fail is a verdict nobody knows works.
//
// It drives presetCorpusCoverage — the same function eachPresetLine calls, not a
// copy of its reasoning — with censuses that are wrong in each of the ways a
// broken walk is wrong, and with a nil library, which is the case where "no
// complaints" would be the most dangerous answer.
func TestPresetCorpusCoverageCatchesADroppedAxis(t *testing.T) {
	lib := shippedLibraryFor(t)
	full := func() [PresetKindCount]map[string]bool {
		var v [PresetKindCount]map[string]bool
		for k := PresetKind(0); k < PresetKindCount; k++ {
			v[k] = map[string]bool{}
			for _, p := range lib.axis(k) {
				v[k][p.ID] = true
			}
		}
		return v
	}
	if gaps := presetCorpusCoverage(lib, full()); len(gaps) != 0 {
		t.Fatalf("a census of every shipped fragment is reported as incomplete: %s", strings.Join(gaps, "; "))
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*[PresetKindCount]map[string]bool)
		mustSay string
	}{
		{
			// The mutation this whole gate exists for, in both directions: one axis
			// walked, the other never opened.
			name:    "layout axis never opened",
			mutate:  func(v *[PresetKindCount]map[string]bool) { v[PresetLayout] = map[string]bool{} },
			mustSay: presetAxisDir(PresetLayout),
		},
		{
			name:    "style axis never opened",
			mutate:  func(v *[PresetKindCount]map[string]bool) { v[PresetStyle] = map[string]bool{} },
			mustSay: presetAxisDir(PresetStyle),
		},
		{
			// One file skipped inside an axis that WAS walked: the same class one
			// fragment at a time, which a per-axis count alone would miss if the walk
			// also read something extra.
			name: "one fragment skipped",
			mutate: func(v *[PresetKindCount]map[string]bool) {
				delete(v[PresetStyle], lib.axis(PresetStyle)[0].ID)
			},
			mustSay: presetFragmentPath(PresetStyle, lib.axis(PresetStyle)[0].ID),
		},
		{
			// A walk reading files the library does not hold is linting something the
			// user will never receive, which is the mirror image of the same fault.
			name:    "a file that does not ship",
			mutate:  func(v *[PresetKindCount]map[string]bool) { v[PresetLayout]["not_a_shipped_preset"] = true },
			mustSay: "not_a_shipped_preset",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := full()
			tc.mutate(&v)
			gaps := presetCorpusCoverage(lib, v)
			if len(gaps) == 0 {
				t.Fatalf("%s went unreported — the coverage check is dead", tc.name)
			}
			if joined := strings.Join(gaps, "; "); !strings.Contains(joined, tc.mustSay) {
				t.Errorf("the complaint never names %q, so nobody could act on it: %s", tc.mustSay, joined)
			}
		})
	}

	// A library that failed to load has empty axes, and a coverage check that
	// stayed quiet about that would bless every walk over it.
	if gaps := presetCorpusCoverage(nil, full()); len(gaps) != int(PresetKindCount) {
		t.Errorf("coverage against a nil library reported %d complaint(s), want one per axis (%d) — "+
			"an empty expectation must never read as 'all clear'", len(gaps), int(PresetKindCount))
	}
}

// presetProseHit is one banned phrase found in one comment.
type presetProseHit struct {
	rule  string
	match string
}

// presetProseHits applies the whole table to a single comment, after removing
// the format's own colliding key names (presetProseFormatKeys).
func presetProseHits(comment string) []presetProseHit {
	for _, k := range presetProseFormatKeys {
		comment = strings.ReplaceAll(comment, k, "")
	}
	var out []presetProseHit
	for _, r := range presetProseBan {
		if m := r.re.FindString(comment); m != "" {
			out = append(out, presetProseHit{rule: r.name, match: m})
		}
	}
	return out
}

// presetProseWhy resolves a rule name to its reason, so the corpus failure says
// what to do about the word it found rather than only naming it.
func presetProseWhy(rule string) string {
	for _, r := range presetProseBan {
		if r.name == rule {
			return r.why
		}
	}
	return "banned vocabulary"
}
