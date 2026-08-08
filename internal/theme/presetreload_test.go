package theme

// SWAPPING A TIER ON A THEME THAT IS ALREADY ON DISK (v1.90.0 W9).
//
// ONE concern: the round trip the gallery's Apply button actually performs —
// build, SAVE, reopen, apply another tier, SAVE, reopen — and the property that
// the last reopen holds the theme the model held.
//
// It exists because every other gate in the wave measured a step short of it.
// TestAMergedThemeReadsBackAsItself merges over a NIL base, which is the one case
// with an empty carrier and therefore the one case where a stale section is
// structurally impossible; TestPresetApplyThenUndoRestoresExactly compares the
// bytes before and after an undo, and a section that is stale on BOTH sides
// cancels out of that comparison exactly. Measured before the fix: 35 elements
// written, 70 read back, and the worst shipped re-style reloading at 97 against
// ElementCap = 96 — a theme the reader REFUSES the moment its author reopens it.

import (
	"strings"
	"testing"
)

// saveAndReopen is the step the wave was missing. Serialising and re-parsing is
// what the gallery's Apply → Save → next session actually does, and it is where
// the carrier and the model get to disagree.
func saveAndReopen(t testing.TB, sc *Sidecar, what string) *Sidecar {
	t.Helper()
	raw, err := sc.Bytes()
	if err != nil {
		t.Fatalf("%s: serialise: %v", what, err)
	}
	back, err := ParseSidecar(raw)
	if err != nil {
		t.Fatalf("%s: the file we just wrote does not read back: %v", what, err)
	}
	return back
}

// sidecarShape is what a reload has to agree with the model about. Counts rather
// than a deep compare: a section the writer left behind shows up as a COUNT, and
// the ids say which one it was.
type sidecarShape struct {
	elements, overrides, panes, effects, clocks, fontBind, palette int
	elementIDs                                                     string
}

func shapeOf(s *Sidecar) sidecarShape {
	out := sidecarShape{
		elements:  len(s.Elements),
		overrides: len(s.Overrides),
		panes:     len(s.Panes),
		effects:   len(s.Effects),
		fontBind:  len(s.FontBind),
	}
	for i := range s.Clocks {
		if s.Clocks[i].Declared {
			out.clocks++
		}
	}
	for _, role := range paletteRoleKeys {
		if paletteSlotColor(s.Palette, role.slot) != nil {
			out.palette++
		}
	}
	ids := make([]string, 0, len(s.Elements))
	for i := range s.Elements {
		ids = append(ids, s.Elements[i].ID)
	}
	out.elementIDs = strings.Join(ids, ",")
	return out
}

// TestSwappingATierOnASavedThemeReloadsAsItself walks every shipped fragment
// through both roles — the tier that is replaced and the tier that replaces it —
// as a ring, so no fragment is exercised only as one or the other.
func TestSwappingATierOnASavedThemeReloadsAsItself(t *testing.T) {
	lib := shippedLibraryFor(t)
	styles, layouts := lib.Styles(), lib.Layouts()

	swap := func(t *testing.T, base PresetPair, next *Preset) {
		t.Helper()
		built, err := NewThemeFromPresets("Saved", base)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		onDisk := saveAndReopen(t, built, "first save")
		merged, err := MergePresets(onDisk, PresetPair{Layout: pairAxis(next, PresetLayout), Style: pairAxis(next, PresetStyle)})
		if err != nil {
			t.Fatalf("apply %s: %v", next.ID, err)
		}
		want := shapeOf(merged)
		got := shapeOf(saveAndReopen(t, merged, "second save"))
		if got != want {
			t.Fatalf("%s x %s then %s: the model and the file it saved disagree\n have %+v\n want %+v",
				base.Layout.ID, base.Style.ID, next.ID, got, want)
		}
	}

	for i, s := range styles {
		next := styles[(i+1)%len(styles)]
		swap(t, PresetPair{Layout: layouts[0], Style: s}, next)
	}
	for i, l := range layouts {
		next := layouts[(i+1)%len(layouts)]
		swap(t, PresetPair{Layout: l, Style: styles[0]}, next)
	}
	// The worst measured pair, named so a regression prints the case that hurt:
	// these two are the largest element lists in the set, and their union was 97
	// against ElementCap = 96, so the reload did not merely double — it REFUSED.
	worstFrom, err := lib.Find(PresetStyle, "thh_courtroom")
	if err != nil {
		t.Fatalf("fixture style: %v", err)
	}
	worstTo, err := lib.Find(PresetStyle, "vaporwave")
	if err != nil {
		t.Fatalf("fixture style: %v", err)
	}
	swap(t, PresetPair{Layout: layouts[0], Style: worstFrom}, worstTo)
}

// pairAxis puts a fragment on its own axis of a pair, so the ring above can hand
// a layout and a style to one function.
func pairAxis(p *Preset, k PresetKind) *Preset {
	if p.Kind != k {
		return nil
	}
	return p
}

// TestATierApplyLeavesNoTraceOfTheTierItReplaced is the same failure read at the
// BYTE level, which is where a person debugging it would look. The counts above
// say "something came back"; this says what.
func TestATierApplyLeavesNoTraceOfTheTierItReplaced(t *testing.T) {
	lib := shippedLibraryFor(t)
	layout, err := lib.Find(PresetLayout, "classic_default")
	if err != nil {
		t.Fatalf("fixture layout: %v", err)
	}
	from, err := lib.Find(PresetStyle, "cyberpunk")
	if err != nil {
		t.Fatalf("fixture style: %v", err)
	}
	to, err := lib.Find(PresetStyle, "terminal")
	if err != nil {
		t.Fatalf("fixture style: %v", err)
	}
	built, err := NewThemeFromPresets("Saved", PresetPair{Layout: layout, Style: from})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	onDisk := saveAndReopen(t, built, "first save")
	merged, err := MergePresets(onDisk, PresetPair{Style: to})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, err := merged.Bytes()
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	out := string(raw)
	for _, e := range from.Elements() {
		if _, readopted := to.Side.FindElement(e.ID); readopted {
			continue
		}
		if sec := secElementPrefix + e.ID; strings.Contains(out, "["+sec+"]") {
			t.Fatalf("[%s] is still in the file after its tier was replaced — the reader will hand it "+
				"back, and the theme grows by a whole style every time it is re-styled", sec)
		}
	}
	// The tier that ARRIVED is all there, so the clear did not overreach.
	for _, e := range to.Elements() {
		if !strings.Contains(out, "["+secElementPrefix+e.ID+"]") {
			t.Fatalf("[%s%s] is missing — the clear took the incoming tier with the outgoing one",
				secElementPrefix, e.ID)
		}
	}
}

// TestAnElementBothStylesDeclareReloadsAsTheNewOne is the half of the clear that
// a count can never see, and the one the shipped set actually hits: `chat_frame`
// exists in more than one style fragment.
//
// The writer only writes a key when the MODEL has something to say about it (an
// empty value leaves the author's line exactly as it is — setKey's first clause),
// so an old element's section left standing under a new element of the same name
// keeps every string the newcomer does not set: its `anchor`, its `generator`,
// its `text`. The result reads back as a row that is half of each style, and
// every count in this file agrees with it.
func TestAnElementBothStylesDeclareReloadsAsTheNewOne(t *testing.T) {
	lib := shippedLibraryFor(t)
	layout := lib.Layouts()[0]
	for i, from := range lib.Styles() {
		to := lib.Styles()[(i+1)%len(lib.Styles())]
		shared := sharedElementIDs(from, to)
		if len(shared) == 0 {
			continue
		}
		built, err := NewThemeFromPresets("Saved", PresetPair{Layout: layout, Style: from})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		merged, err := MergePresets(saveAndReopen(t, built, "first save"), PresetPair{Style: to})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		back := saveAndReopen(t, merged, "second save")
		for _, id := range shared {
			want, _ := to.Side.FindElement(id)
			got, ok := back.FindElement(id)
			if !ok {
				t.Fatalf("%s -> %s: element %q vanished across the save", from.ID, to.ID, id)
			}
			if *got != *want {
				t.Fatalf("%s -> %s: element %q reloaded as neither style's row:\n have %+v\n want %+v",
					from.ID, to.ID, id, *got, *want)
			}
		}
	}
}

// sharedElementIDs is the ids two fragments both declare — the case above, and
// the one the fixtures are chosen by rather than hoped for.
func sharedElementIDs(a, b *Preset) []string {
	var out []string
	for _, e := range a.Elements() {
		if _, both := b.Side.FindElement(e.ID); both {
			out = append(out, e.ID)
		}
	}
	return out
}

// TestATierApplyOnlyClearsWhatItsAxisOwns is the ENCAPSULATION gate for the clear
// (hard rule 11). "Replace the tier" is a wholesale act, so the fence around it is
// the only thing between a style apply and someone's layout.
//
// It drives the real merge over a HAND-AUTHORED theme, because everything the
// fence protects — an author's own pane, their comment, a manifest entry — is
// exactly what a preset-built theme has none of.
func TestATierApplyOnlyClearsWhatItsAxisOwns(t *testing.T) {
	const authored = `[theme]
format = 1
name = Mine

[overrides]
; the author's own note about this rect
viewport = 10, 20, 300, 200

[fonts]
serif = mine.ttf

[element.my_pane]
; a comment on the author's own element
kind = pane
rect = 0, 0, 100, 100

[pane.left]
rect = 0, 0, 200, 400
`
	lib := shippedLibraryFor(t)
	style, err := lib.Find(PresetStyle, "terminal")
	if err != nil {
		t.Fatalf("fixture style: %v", err)
	}
	base, err := ParseSidecar([]byte(authored))
	if err != nil {
		t.Fatalf("parse the authored theme: %v", err)
	}
	merged, err := MergePresets(base, PresetPair{Style: style})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, err := merged.Bytes()
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	out := string(raw)
	for _, kept := range []string{
		"; the author's own note about this rect",
		"viewport = 10, 20, 300, 200", // [overrides] is the LAYOUT axis
		"[pane.left]",                 // so are the panes
		"serif = mine.ttf",            // [fonts] is a manifest: merged, never pruned
		"[element.my_pane]",           // a pane element belongs to the other axis
		"; a comment on the author's own element",
	} {
		if !strings.Contains(out, kept) {
			t.Errorf("a style apply removed %q — it belongs to the layout axis or to a manifest:\n%s", kept, out)
		}
	}
	// And the style axis's own sections really were replaced rather than merged,
	// or the gate above would pass for a clear that never runs.
	if len(merged.Effects) != len(style.Side.Effects) {
		t.Errorf("the theme holds %d effect binds, the style declares %d", len(merged.Effects), len(style.Side.Effects))
	}
}

// TestClearingATierIsDrivenByThePartitionTable pins the rule the clear is
// expressed in, so a section added to one axis cannot quietly become a section
// the other axis deletes. The two exceptions are the ones presetmerge.go's header
// already states: the manifests are merged, and [element.*] is per element.
func TestClearingATierIsDrivenByThePartitionTable(t *testing.T) {
	for _, c := range []struct {
		kind    PresetKind
		section string
		want    bool
		why     string
	}{
		{PresetLayout, secOverrides, true, "a layout replaces every rect it places"},
		{PresetLayout, secRotations, true, "same tier, same act"},
		{PresetLayout, secPanePrefix + "left", true, "panes are the layout's own sections"},
		{PresetLayout, secPalette, false, "a layout apply must not touch the style's colours"},
		{PresetStyle, secPalette, true, "a style replaces the palette wholesale"},
		{PresetStyle, secChrome, true, "and the chrome"},
		{PresetStyle, secFontBind, true, "and the bindings"},
		{PresetStyle, secClockPrefix + "1", true, "and the clocks it declared"},
		{PresetStyle, secEffectPrefix + "viewport", true, "and the effects it bound"},
		{PresetStyle, secOverrides, false, "a style apply must not move a single widget"},
		{PresetStyle, secFonts, false, "a manifest names files in the theme folder: merged, never pruned"},
		{PresetStyle, secMedia, false, "same"},
		{PresetStyle, secSounds, false, "same"},
		{PresetStyle, secTheme, false, "identity is nobody's tier"},
		{PresetStyle, secImport, false, "nor is provenance"},
		{PresetStyle, secElementPrefix + "x", false, "elements are decided per element, by kind"},
		{PresetLayout, secElementPrefix + "x", false, "same"},
		{PresetStyle, secPreset, false, "the fragment header is not part of a theme at all"},
	} {
		if got := presetTierClears(c.kind, c.section); got != c.want {
			t.Errorf("presetTierClears(%s, %q) = %v, want %v — %s", c.kind, c.section, got, c.want, c.why)
		}
	}
}
