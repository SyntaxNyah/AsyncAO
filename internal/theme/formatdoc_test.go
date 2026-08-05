package theme

// The SHIPPED SPEC is a test input.
//
// docs/THEME-FORMAT.md is written so a third party can implement a reader
// without ever reading this source, which makes every number in it a promise.
// A promise nothing checks drifts: the constant moves, the document keeps
// saying the old number, and the next third-party writer emits files we refuse.
//
// So the census trick internal/theme already runs over INIDoc (see
// inidoccensus_test.go) is pointed at the document itself. The parser below is
// TOLERANT OF PROSE — it reads markdown tables and matches substrings, so the
// document stays readable English — and STRICT ON NUMBERS: every published
// figure is compared against the Go constant it claims to describe, and §7's
// caps table is checked for TOTALITY IN BOTH DIRECTIONS, so a row added to the
// document without an expectation here fails as loudly as a missing one.

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// themeFormatDocPath is the published spec, relative to this package. It is
// checked in; a missing file is a failure, never a skip.
const themeFormatDocPath = "../../docs/THEME-FORMAT.md"

// capsTableHeading opens §7's caps table. Matched as a PREFIX so the prose
// after it ("— refuse, never truncate") can be reworded freely.
const capsTableHeading = "### Caps"

// docMinusSign is U+2212, which the document uses for negative numbers because
// it is typographically correct. It is folded to ASCII '-' before matching so
// an expectation can be built with a plain fmt.Sprintf.
const docMinusSign = "−"

// docInteger matches one signed integer inside a table cell.
var docInteger = regexp.MustCompile(`-?\d+`)

// mibNumeral is the "1" in the caps table's "1 MiB" — the one published cap
// stated as a unit rather than as a count. The byte value behind it is asserted
// separately, so both halves of that row are pinned.
const mibNumeral = 1

// TestPublishedCapsMatchTheConstants pins docs/THEME-FORMAT.md against the
// constants it publishes: §7's caps table row by row, plus the numeric bounds
// stated in prose elsewhere. When this fails, the DOCUMENT is usually the thing
// that is wrong — but exactly one of the two is, and the diff says which.
func TestPublishedCapsMatchTheConstants(t *testing.T) {
	doc := readFormatDoc(t)
	rows := parseCapsTable(t, doc)
	if len(rows) == 0 {
		t.Fatalf("no caps table found under %q in %s", capsTableHeading, themeFormatDocPath)
	}

	// Every row of §7's caps table, keyed by the label EXACTLY as the document
	// spells it, against every integer that must appear in its value cell.
	want := []struct {
		label string
		nums  []int
	}{
		{"file size", []int{mibNumeral}},
		{"lines per file", []int{IniDocLineCap}},
		{"elements", []int{ElementCap}},
		{"panes / hosts per pane", []int{PaneCap, PaneHostCap}},
		{"media entries", []int{MediaCap}},
		{"font families / bindings", []int{FontCap, FontBindCap}},
		{"overrides / rotations", []int{OverrideCap, OverrideCap}},
		// Clock 0 is the shared theme anchor and is never declared, so the
		// published number is one less than the array bound.
		{"clock groups", []int{ClockCap - 1}},
		{"widget effect bindings", []int{EffectBindCap}},
		{"sound overrides", []int{SoundCap}},
		{"element text", []int{TextRuneCap}},
		{"generator parameters", []int{GenParamCap, GenParamRuneCap}},
		{"unknown entries carried", []int{PreservedCap}},
		// A REPORT bound, not a load bound — the sentence that says so is
		// asserted below, because it is the normative half of this row.
		{"degrade notes reported", []int{NoteCap}},
		{"ids (element, pane, media, clock, `media`/`shape`/`generator` names)", []int{IDRuneCap}},
		{"`anchor`", []int{AnchorRuneCap}},
		{"`visible_when` value", []int{ConditionValueRuneCap}},
		{"every other text value (names, paths, families)", []int{NameRuneCap}},
		{"`[import] subthemes`", []int{SubthemeCap}},
	}

	checked := make(map[string]bool, len(want))
	for _, w := range want {
		checked[w.label] = true
		cell, ok := rows[w.label]
		if !ok {
			t.Errorf("caps table row %q is gone from %s — either a published cap was dropped "+
				"or the row was reworded; this gate matches the label verbatim", w.label, themeFormatDocPath)
			continue
		}
		if got := docIntegers(cell); !equalInts(got, w.nums) {
			t.Errorf("caps table row %q reads %v (%q), the constants say %v", w.label, got, cell, w.nums)
		}
	}
	for label, cell := range rows {
		if !checked[label] {
			t.Errorf("%s publishes a cap this gate does not check: %q = %q.\n"+
				"Add it to the table in this test — an unchecked published number is one that drifts.",
				themeFormatDocPath, label, cell)
		}
	}

	// The one row whose cell states a unit instead of a count.
	if IniDocByteCap != mibNumeral<<20 {
		t.Errorf("IniDocByteCap = %d, but the caps table publishes it as %d MiB", IniDocByteCap, mibNumeral)
	}
	// NoteCap is published as a REPORT bound, and that sentence is normative:
	// it is the only thing telling a third-party reader not to refuse a legal
	// file for having too many problems (§7.1 and §7.4).
	for _, phrase := range []string{"not a load bound", "keeps loading"} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("%s no longer says %q — that sentence is what stops a conforming reader "+
				"from refusing a legal file over its diagnostics", themeFormatDocPath, phrase)
		}
	}

	// The numeric bounds stated in prose. Each phrase is BUILT from the
	// constant, so the assertion is that the document says the number the code
	// means, wherever it says it.
	for _, c := range []struct{ why, phrase string }{
		{"the format version stamp", fmt.Sprintf("format version %d.", ThemeFormatVersion)},
		{"the minimal example's format key", fmt.Sprintf("format = %d", ThemeFormatVersion)},
		{"the sidecar file name", SidecarFileName},
		{"the media directory", SidecarMediaDir},
		{"the vendor section prefix", "`[x.<vendor>.*]`"},
		{"the id grammar", fmt.Sprintf("`[a-z0-9_-]{1,%d}`", IDRuneCap)},
		{"§2.1's percent escape", freeTextPercent},
		{"§2.1's semicolon escape", freeTextSemi},
		{"§2.1's LF escape", freeTextLF},
		{"§2.1's CR escape", freeTextCR},
		{"§2.3's z/slice width", fmt.Sprintf("signed 16-bit, %d..%d", ZMin, ZMax)},
		{"§2.3's millisecond width", fmt.Sprintf("signed 32-bit, ±%d (%d low)", TimeMsMax, TimeMsMin)},
		{"§2.3's point size", fmt.Sprintf("| `size` | 0..%d |", TextSizeMaxPt)},
		{"§2.3's amplitude", fmt.Sprintf("| `amp_pct`, `effect_amp_pct` | 0..%d |", AmpMaxPct)},
		{"§2.3's clock speed", fmt.Sprintf("| `speed_pct` | %d..%d (`0` = absent) |", SpeedMinPct, SpeedMaxPct)},
		{"§2.3's clock index", fmt.Sprintf("| `clock` | 0..%d |", ClockCap-1)},
		{"§2.3's angle wrap", fmt.Sprintf("| `rot`, `grad_angle` | 0..%d |", AngleCount-1)},
		{"§3's overrides/rotations bound", fmt.Sprintf("Up to %d entries each.", OverrideCap)},
		{"§3's font bounds", fmt.Sprintf("Up to %d families and %d bindings.", FontCap, FontBindCap)},
		{"§3's media bound", fmt.Sprintf("Up to %d entries.", MediaCap)},
		{"§3's clock group range", fmt.Sprintf("`N` in `1..%d`", ClockCap-1)},
		{"§3's nominal clock speed", fmt.Sprintf("Absent = %d.", SpeedNominalPct)},
		{"§3's pane bounds", fmt.Sprintf("Up to %d panes, %d hosts each.", PaneCap, PaneHostCap)},
		{"§3's element bound", fmt.Sprintf("Up to %d per theme.", ElementCap)},
		// Ruling R1 (design §6.6): the anchored-rect reading is the one sentence
		// that keeps a layout preset and a style preset orthogonal, so both halves
		// of it — the delta formula and the empty-anchor case — are pinned here.
		// A reader that took an anchored rect as absolute would place every
		// decoration in a style preset at the top-left of its space.
		{"§3's anchored-rect delta rule", "`X = anchor.X + rect.x`"},
		{"§3's empty-anchor absolute rule", "`rect` is **absolute in its declared space**"},
		{"§3's element text bound", fmt.Sprintf("up to %d runes", TextRuneCap)},
		{"§3's generator parameter bound", fmt.Sprintf("ordered; up to %d", GenParamCap)},
		{"§3's effect binding bound", fmt.Sprintf("Up to %d bindings.", EffectBindCap)},
		{"§3's [effect.<key>] widget-key bound", fmt.Sprintf("at most **%d runes**", AnchorRuneCap)},
		{"§3's sound bound", fmt.Sprintf("Up to %d entries.", SoundCap)},
		{"§7's note-cap paragraph", fmt.Sprintf("%d degrade notes stops recording", NoteCap)},
		{"§7's worked forward-compat case", fmt.Sprintf("so %d elements from a newer client", ElementCap)},
		{"§7's one-past-the-cap sentence", fmt.Sprintf("the %dth problem into a refusal", NoteCap+1)},
	} {
		if !strings.Contains(doc, c.phrase) {
			t.Errorf("%s no longer states %s: %q is not in the document", themeFormatDocPath, c.why, c.phrase)
		}
	}
}

// readFormatDoc returns the spec with U+2212 folded to ASCII '-'.
func readFormatDoc(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(themeFormatDocPath)
	if err != nil {
		t.Fatalf("the published spec is unreadable (%v). It is checked in at %s; "+
			"if it moved, repoint the constant rather than skipping the gate.", err, themeFormatDocPath)
	}
	return strings.ReplaceAll(string(src), docMinusSign, "-")
}

// parseCapsTable returns the caps table as label -> value cell. It is
// deliberately forgiving about everything but the two cells: any number of
// header, separator and prose lines may sit between the heading and the table,
// and the table ends at the first non-table line after it.
func parseCapsTable(t *testing.T, doc string) map[string]string {
	t.Helper()
	out := map[string]string{}
	started := false
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(line)
		if !started {
			started = strings.HasPrefix(line, capsTableHeading)
			continue
		}
		if !strings.HasPrefix(line, "|") {
			if len(out) > 0 {
				break
			}
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 2 {
			continue
		}
		label, value := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1])
		if label == "" || label == "Limit" || strings.HasPrefix(label, "---") {
			continue // the header row and the |---|---| separator
		}
		if prev, dup := out[label]; dup {
			t.Errorf("the caps table lists %q twice (%q, then %q)", label, prev, value)
		}
		out[label] = value
	}
	return out
}

// docIntegers pulls every signed integer out of a table cell, in order, so the
// surrounding prose ("15 (plus the shared anchor)") costs nothing.
func docIntegers(cell string) []int {
	var out []int
	for _, m := range docInteger.FindAllString(cell, -1) {
		v, err := strconv.Atoi(m)
		if err != nil {
			continue // a number too wide for int is not a cap this format has
		}
		out = append(out, v)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
