package ui

// W7b — THE INSPECTORS (v1.90.0).
//
// Every gate here DRIVES the shipping seam: the real inspector table, the real
// document funnel, the real pick lists, the real type tables. Nothing below
// re-implements a rule it is checking (rule 11's mirror-test clause), and the
// mutation-reverts evidence is the SERIALISED document's hash, exactly as W7a's is.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// editorGateApplyWait bounds a gate's wait on the theme-apply goroutine. Generous with
// a hard stop, like editorGateSaveWait: the apply is off-thread, and a gate that never
// waited for it would leave a load in flight when the harness tears the renderer down.
const editorGateApplyWait = 10 * time.Second

// ---------------------------------------------------------------------------
// The AO2 WIDGET arm
// ---------------------------------------------------------------------------

// editSlotRig is a document over a live design map with one editable widget.
func editSlotRig(t *testing.T) (*App, *themeDoc, editTarget) {
	t.Helper()
	const key = "emotes"
	if !editorSlotKeyEditable(key) {
		t.Fatalf("fixture: %q must be an editable design key", key)
	}
	a, doc := editDocRig(t, 1)
	live := map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: 714, H: 579},
		key:         {X: 100, Y: 400, W: 200, H: 120},
	}
	doc.bindLive(live)
	return a, doc, designTarget(key)
}

// TestSlotInspectorOffersGeometryAndNothingElse is the AO2 full-parity rule, made
// structural.
//
// W7a stated the rule in a label and offered no control at all, so a widget could be
// dragged but its rect could not be TYPED. W7b gives the arm a real row — and the gate
// is that it is the ONLY row, addressing the ONLY field [overrides] can carry. A
// future AsyncAO-side extra for an AO2 widget cannot be smuggled in as a second row
// without failing here.
func TestSlotInspectorOffersGeometryAndNothingElse(t *testing.T) {
	a, _, tgt := editSlotRig(t)
	rows := a.inspectorRowsFor(tgt)
	if len(rows) != 1 {
		t.Fatalf("the AO2 widget rail offers %d rows, want exactly 1 — a theme may restate an AO2 "+
			"widget's rect and nothing else about it", len(rows))
	}
	if rows[0].field != editFieldRect || rows[0].kind != fkRect {
		t.Errorf("the widget row addresses %s as %d, want the rect as fkRect", rows[0].field, rows[0].kind)
	}
	// It must produce a real op, and the SAME op a drag produces — one mutation, two
	// gestures, or the typed number and the dragged one could diverge.
	cur := rows[0].get(a, tgt, editFieldRect)
	next := cur
	next.i[0] += 7
	op := rows[0].mut(tgt, editFieldRect, next)
	if op.kind != opSlotRect {
		t.Fatalf("the widget row produced op kind %v, want opSlotRect (what a drag produces)", op.kind)
	}
	// And an element target gets nothing from this table, so a row cannot be reached
	// through the wrong space.
	if got := inspectSlotField(a, elementTarget(0), editFieldRect); got != (editValue{}) {
		t.Errorf("the widget getter answered %+v for an ELEMENT target", got)
	}
	if got := mutateSlotField(tgt, editFieldOpacity, cur); got.kind != opNone {
		t.Error("the widget mutator answered a non-rect field — [overrides] is a rect table and the " +
			"arm must refuse everything else")
	}
}

// TestResetToThemeDropsTheRowAndUndoRestoresIt pins the one ACTION the widget rail
// carries, in the half that matters: a reset must REMOVE the [overrides] row, not
// write the resolved rect back into it.
//
// Writing it back would leave a row in the author's file restating exactly what the
// AO2 tier already says — a diff for an edit the user just took back, which is the
// same defect editSlotRowPresent exists to keep an undo from causing.
func TestResetToThemeDropsTheRowAndUndoRestoresIt(t *testing.T) {
	a, doc, tgt := editSlotRig(t)
	base := doc.live[tgt.designKey()]

	// Nothing authored yet: Reset is a NO-OP, not a refusal, and must not dirty the
	// document (pressing Reset on an untouched widget is not an edit).
	if op, res := doc.apply(mutateSlotReset(tgt)); res != applyNoChange {
		t.Fatalf("Reset on an unauthored widget answered %d (op %v), want applyNoChange", res, op.kind)
	}
	if doc.dirty {
		t.Fatal("Reset on an unauthored widget marked the theme edited")
	}

	moved := theme.Rect{X: base.X + 40, Y: base.Y + 8, W: base.W, H: base.H}
	if !a.editorApply(mutateSlotRect(tgt, moved)) {
		t.Fatal("the fixture drag wrote nothing")
	}
	if _, ok := doc.side.OverrideRect(tgt.designKey()); !ok {
		t.Fatal("the drag wrote no [overrides] row; every assertion below is vacuous")
	}

	if !a.editorApply(mutateSlotReset(tgt)) {
		t.Fatal("Reset refused an authored widget")
	}
	if _, ok := doc.side.OverrideRect(tgt.designKey()); ok {
		t.Error("Reset left the [overrides] row behind — a reset that writes the baseline into a row " +
			"is a diff for an edit the user took back")
	}
	if got := doc.live[tgt.designKey()]; got != base {
		t.Errorf("Reset left the live geometry at %+v, want the theme's own %+v", got, base)
	}

	// Undo puts the row back, numbers and all.
	if !a.editorUndo() {
		t.Fatal("undo of a reset did nothing")
	}
	got, ok := doc.side.OverrideRect(tgt.designKey())
	if !ok || got != moved {
		t.Errorf("undo of a reset restored %+v (present=%v), want the authored %+v", got, ok, moved)
	}
	// Redo drops it again rather than re-creating it from the baseline.
	if !a.editorRedo() {
		t.Fatal("redo of a reset did nothing")
	}
	if _, ok := doc.side.OverrideRect(tgt.designKey()); ok {
		t.Error("redo of a reset re-created the row it had removed")
	}
}

// ---------------------------------------------------------------------------
// The new element fields
// ---------------------------------------------------------------------------

// TestNewInspectorFieldsAreAddressableAndNamed is the census growing with the wave:
// every field W7b added must have a getter, a setter, a name and a row that reaches
// it. Without this a field could be declared, wired into the tables, and reachable
// from no rail at all — which is the "parsed but never applied" shape one tier down.
func TestNewInspectorFieldsAreAddressableAndNamed(t *testing.T) {
	added := map[editField]string{
		editFieldKind:       "an element that cannot change kind has to be deleted and re-made",
		editFieldGenParams:  "a generator with no reachable parameters is one preset",
		editFieldSlice:      "9-slice insets are unreachable from any other surface",
		editFieldNoDecimate: "design §3.3 requires the inspector to explain decimation",
	}
	reached := map[editField]bool{}
	for k := theme.ElementKind(0); k < theme.ElementKindCount; k++ {
		for _, f := range inspectorFields[k] {
			reached[f.field] = true
		}
	}
	for f, why := range added {
		if editFieldGet[f] == nil || editFieldSet[f] == nil || editFieldNames[f] == "" {
			t.Errorf("%s has no getter/setter/name", f)
		}
		if !reached[f] {
			t.Errorf("%s is addressable but no inspector row reaches it — %s", f, why)
		}
	}
}

// TestElementIdRowRenamesTheSectionRatherThanDuplicatingIt is the W7b negative gate
// turned into its post-condition (v1.90.0 W8).
//
// THE PRECONDITION IT GUARDED IS GONE, and the property it protected is not. Through
// W7b the gate said "there must be no Id row", because an element's id is its SECTION
// SUFFIX — the writer emits `[element.<id>]` and no key carries it (sidecar_write.go's
// elementFieldKeys names ID as "never a key") — and INIDoc had no way to move a
// section, so a rename would have APPENDED a second one and left the first: a saved
// theme carrying the element twice. W8 landed theme.INIDoc.RenameSection and the one
// funnel that drives it, so the row exists — and the duplication this test was written
// about is now checked DIRECTLY, by saving and re-reading, which is a stronger claim
// than the absence of a row ever was.
func TestElementIdRowRenamesTheSectionRatherThanDuplicatingIt(t *testing.T) {
	// The row exists and is reachable for every element kind: an id you cannot see is
	// an id you cannot fix when two themes' elements collide.
	for k := theme.ElementKind(0); k < theme.ElementKindCount; k++ {
		if findInspectorRowOrNil(k, editFieldElemID) == nil {
			t.Fatalf("%s has no Id row — the rename is unreachable from the rail", k)
		}
	}

	a, doc := editDocRig(t, 1)
	// The rig's own id, and its own section: editDocRig serialises once, so the
	// carrier really holds `[element.<id>]` and the rename really has a header to
	// move. Assigning a fresh id here instead would leave the ORIGINAL section in
	// the carrier and the gate would fail for a reason about the fixture.
	old := doc.side.Elements[0].ID
	tgt := elementTarget(0)
	row := findInspectorRow(t, doc.side.Elements[0].Kind, editFieldElemID)
	a.editorWriteStringField(row, tgt, "band")

	if got := doc.side.Elements[0].ID; got != "band" {
		t.Fatalf("the model kept the id %q", got)
	}
	// THE FILE IS THE CLAIM. Save the document and read it back: exactly one element,
	// under exactly the new name.
	b, err := doc.side.Bytes()
	if err != nil {
		t.Fatalf("Bytes after a rename: %v", err)
	}
	back, err := theme.ParseSidecar(b)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(back.Elements) != 1 || back.Elements[0].ID != "band" {
		t.Fatalf("a renamed element read back as %d element(s) %v — the old section survived",
			len(back.Elements), elementIDsIn(back))
	}
	if strings.Contains(string(b), "[element."+old+"]") {
		t.Errorf("the old section is still in the file:\n%s", b)
	}

	// AND IT UNDOES. A rename that could not be taken back would be the one edit in
	// the editor with no inverse.
	if !a.editorUndo() {
		t.Fatal("undo of a rename did nothing")
	}
	if got := doc.side.Elements[0].ID; got != old {
		t.Errorf("undo left the id at %q, want the original %q", got, old)
	}
	if !a.editorRedo() {
		t.Fatal("redo of a rename did nothing")
	}
	if got := doc.side.Elements[0].ID; got != "band" {
		t.Errorf("redo left the id at %q", got)
	}
}

// TestARefusedRenameChangesNothingAndSaysWhy pins the other half: the funnel's
// refusals reach the user as a chip, and the model does not move.
func TestARefusedRenameChangesNothingAndSaysWhy(t *testing.T) {
	a, doc := editDocRig(t, 2)
	mine, sibling := doc.side.Elements[0].ID, doc.side.Elements[1].ID
	row := findInspectorRow(t, doc.side.Elements[0].Kind, editFieldElemID)

	for _, tc := range []struct{ name, to, want string }{
		{"a taken id", sibling, "already has that id"},
		{"a capital letter", "Box", "a-z0-9_-"},
		{"an empty id", "", "a-z0-9_-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.te.status = ""
			a.editorWriteStringField(row, elementTarget(0), tc.to)
			if got := doc.side.Elements[0].ID; got != mine {
				t.Fatalf("a refused rename still moved the model to %q", got)
			}
			if !strings.Contains(a.te.status, tc.want) {
				t.Errorf("the chip reads %q — it must name the reason (%q)", a.te.status, tc.want)
			}
		})
	}
}

// elementIDsIn lists a parsed sidecar's ids, for a failure message that names the
// duplicate rather than merely counting it.
func elementIDsIn(s *theme.Sidecar) []string {
	out := make([]string, 0, len(s.Elements))
	for i := range s.Elements {
		out = append(out, s.Elements[i].ID)
	}
	return out
}

// TestGenParamsRowUsesTheFormatsOwnParser is the anti-second-grammar gate.
//
// The row's validation must be theme.ParseGenParams — the very function the READER
// uses (genParamsOf) — so a string the editor accepts is a string the file means. A
// hand-rolled comma split in internal/ui would pass a happy-path test and diverge the
// day the reader's cap or its empty-key rule changed.
func TestGenParamsRowUsesTheFormatsOwnParser(t *testing.T) {
	a, doc := editDocRig(t, 1)
	doc.side.Elements[0].Kind = theme.ElemGen
	tgt := elementTarget(0)
	row := findInspectorRow(t, theme.ElemGen, editFieldGenParams)

	// A legal list lands, in the file's own spelling.
	a.editorWriteStringField(row, tgt, "cols = 8, rows = 3")
	if got := theme.WriteGenParams(&doc.side.Elements[0]); got != "cols=8, rows=3" {
		t.Errorf("the params row wrote %q, want the writer's own spelling %q", got, "cols=8, rows=3")
	}

	// PAST THE CAP: refused, with the cap NAMED, and the model untouched — never
	// truncated into a value the author did not ask for.
	before := doc.side.Elements[0].GenParams
	a.te.status = ""
	var over strings.Builder
	for i := 0; i < theme.GenParamCap+3; i++ {
		if i > 0 {
			over.WriteString(", ")
		}
		over.WriteString("k")
		over.WriteString(itoa(i))
		over.WriteString("=v")
	}
	a.editorWriteStringField(row, tgt, over.String())
	if doc.side.Elements[0].GenParams != before {
		t.Error("a gen_params string past the cap changed the model — the cap must refuse, not truncate")
	}
	if !strings.Contains(a.te.status, itoa(theme.GenParamCap)) {
		t.Errorf("the refusal chip is %q — design §3.3 requires it to name the limit (%d)",
			a.te.status, theme.GenParamCap)
	}
}

// findInspectorRow is the test's accessor into the SHIPPING table; it never builds a
// row of its own, so a row deleted from the table fails the gate that uses it.
func findInspectorRow(t *testing.T, k theme.ElementKind, f editField) *inspectorField {
	t.Helper()
	if r := findInspectorRowOrNil(k, f); r != nil {
		return r
	}
	t.Fatalf("%s has no inspector row for %s", k, f)
	return nil
}

// findInspectorRowOrNil is the same probe for a caller that wants to phrase the
// failure itself (a per-kind census reads better naming the kind that is missing).
func findInspectorRowOrNil(k theme.ElementKind, f editField) *inspectorField {
	for i := range inspectorFields[k] {
		if inspectorFields[k][i].field == f {
			return &inspectorFields[k][i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// The live pick lists
// ---------------------------------------------------------------------------

// TestPickListsComeFromTheLiveDocument is the seam that makes an fkPick row a PICKER
// rather than a second hard-coded table: the anchors are the keys THIS theme places,
// the fonts are the class ladder plus THIS theme's declared families.
func TestPickListsComeFromTheLiveDocument(t *testing.T) {
	a, doc := editDocRig(t, 1)
	doc.bindLive(map[string]theme.Rect{
		"courtroom":  {X: 0, Y: 0, W: 714, H: 579},
		"emotes":     {X: 0, Y: 0, W: 10, H: 10},
		"ic_chatlog": {X: 0, Y: 0, W: 10, H: 10},
	})
	doc.side.Panes = []theme.Pane{{ID: "sidebar"}}
	doc.side.Fonts = []theme.FontFamily{{Family: "Court Serif", File: "fonts/CourtSerif.otf"}}

	anchors := a.editorPickNames(pickAnchor)
	if anchors[0] != pickAnchorNone {
		t.Errorf("the anchor picker starts at %q, want %q first", anchors[0], pickAnchorNone)
	}
	for _, want := range []string{"emotes", "ic_chatlog", "sidebar"} {
		if pickIndexOf(anchors, want) < 0 {
			t.Errorf("the anchor picker does not offer %q — it must offer what this theme PLACES "+
				"(an anchor that resolves to nothing is an element parked at the origin)", want)
		}
	}
	// A key the theme does NOT place must not be offered: that is the difference
	// between the registry and the document.
	if pickIndexOf(anchors, "music_list") >= 0 {
		t.Error("the anchor picker offered music_list, which this theme does not place")
	}

	fonts := a.editorPickNames(pickFace)
	if pickIndexOf(fonts, "Court Serif") < 0 {
		t.Error("the font picker does not offer the theme's own [fonts] family")
	}
	if pickIndexOf(fonts, theme.FontElements[0]) < 0 {
		t.Errorf("the font picker does not offer the class id %q — `font` may name a FontElements id "+
			"to inherit that element's face", theme.FontElements[0])
	}

	// The CACHE must follow the document: adding a family has to show up.
	doc.side.Fonts = append(doc.side.Fonts, theme.FontFamily{Family: "Loose", File: "Loose.otf"})
	if pickIndexOf(a.editorPickNames(pickFace), "Loose") < 0 {
		t.Error("the font picker's cache did not notice a new family — a cached list that cannot go " +
			"stale is a list that is never right")
	}

	// The static lists are the BUILD's own, sorted, and never the registry's map order.
	gens := a.editorPickNames(pickGenerator)
	if len(gens) != len(GeneratorNames()) {
		t.Errorf("the generator picker offers %d names, the registry has %d", len(gens), len(GeneratorNames()))
	}
	for i := 1; i < len(gens); i++ {
		if gens[i-1] > gens[i] {
			t.Fatalf("the generator picker is unsorted (%q before %q) — a map's iteration order would "+
				"shuffle the control between runs", gens[i-1], gens[i])
		}
	}
	// Sorting the shape list must not have sorted the REAL one: elemShapeNames is
	// indexed BY ID everywhere else (elemShapeSharp == 0).
	if elemShapeNames[0] != "sharp" {
		t.Fatalf("elemShapeNames[0] is %q — the picker sorted the production array in place, which "+
			"silently re-maps every shape a theme names", elemShapeNames[0])
	}
}

// TestOnlyPickerRowsNameAPickSource pins pickRow's own claim: the ~30 rows that are
// not pickers carry pickNone because they never write one, and every row that IS a
// picker names a live list.
//
// Both halves are silent failures. An fkPick row left at pickNone draws a picker with
// nothing to pick — two step buttons the author can press forever — and a non-picker
// row that acquired a source would be asking the cache to build a list nothing draws.
// The zero value is the one the table relies on and the one nobody writes down, which
// is exactly the kind that drifts.
func TestOnlyPickerRowsNameAPickSource(t *testing.T) {
	a, _ := editDocRig(t, 1)
	// THE NO-SOURCE ANSWER IS PINNED BY THE CACHE SLOT, NOT BY THE RETURN VALUE. A nil
	// return is true of any implementation: with editorPickNames' pickNone guard deleted,
	// pickStatic[pickNone] is nil, buildPickNames matches neither case and hands back
	// names[pickNone][:0] — a nil-backed empty slice that compares == nil. The observable
	// only the guard produces is the one its doc comment promises ("it must not take a
	// cache slot to be told so"): the slot stays unbuilt.
	if got := a.editorPickNames(pickNone); got != nil {
		t.Errorf("the no-source list is %v, want nil", got)
	}
	if a.te.picks.built[pickNone] {
		t.Error("asking for the no-source list built and cached one — pickNone must be answered " +
			"ahead of the cache, or every non-picker row that reaches this table burns a slot on " +
			"a list nothing draws")
	}
	// ANTI-VACUITY for the check above: `built` has to be a flag that actually moves, or
	// a slot left false proves nothing. A real dynamic source must set its own.
	a.editorPickNames(pickAnchor)
	if !a.te.picks.built[pickAnchor] {
		t.Fatal("a real dynamic list did not mark its cache slot built — the pickNone check above " +
			"would then hold against any implementation")
	}
	pickers := 0
	for k := theme.ElementKind(0); k < theme.ElementKindCount; k++ {
		for i := range inspectorFields[k] {
			f := &inspectorFields[k][i]
			if f.kind == fkPick {
				pickers++
				if f.pick == pickNone {
					t.Errorf("%s row %q is an fkPick with no source — its two step buttons would walk "+
						"an empty list forever", k, f.label)
				}
				continue
			}
			if f.pick != pickNone {
				t.Errorf("%s row %q is a %v row that names pick source %d — only fkPick rows offer a "+
					"list, so nothing would ever draw it", k, f.label, f.kind, f.pick)
			}
		}
	}
	if pickers == 0 {
		t.Fatal("the census found no fkPick rows at all — it would pass vacuously")
	}
}

// TestPickValueRoundTripsThroughTheNoneEntry pins the one mapping an fkPick row makes
// that is not the identity: "" is displayed as "(none)" and "(none)" is written as "".
// Two functions, each other's inverse, beside each other — the rgbaValue/valueRGBA
// discipline.
func TestPickValueRoundTripsThroughTheNoneEntry(t *testing.T) {
	names := []string{pickAnchorNone, "emotes", "sidebar"}
	if i := pickIndexOf(names, ""); i != 0 {
		t.Errorf("an empty value indexes to %d, want the leading (none) entry", i)
	}
	if v := pickValueAt(names, 0); v != "" {
		t.Errorf("the (none) entry writes %q, want the empty string", v)
	}
	for i := 1; i < len(names); i++ {
		if v := pickValueAt(names, i); v != names[i] {
			t.Errorf("entry %d writes %q, want %q", i, v, names[i])
		}
		if pickIndexOf(names, names[i]) != i {
			t.Errorf("%q does not index back to %d", names[i], i)
		}
	}
	// A value the list does not offer is NOT found, which is what keeps the cycler from
	// jumping to a random entry when a theme names a generator this build lacks.
	if pickIndexOf(names, "ripples") != -1 {
		t.Error("an unoffered value was found in the list")
	}
}

// ---------------------------------------------------------------------------
// The type tables
// ---------------------------------------------------------------------------

// TestFontBindIsUndoableAndHashReverts is the [fontbind] tier's own instance of the
// wave's headline contract: a binding is an op, it inverts, and the SERIALISED
// document returns byte-for-byte.
func TestFontBindIsUndoableAndHashReverts(t *testing.T) {
	a, doc := editDocRig(t, 1)
	doc.side.Fonts = []theme.FontFamily{{Family: "Court Serif", File: "fonts/CourtSerif.otf"}}
	doc.side.FontBind = []theme.KV{{Key: theme.FontElements[0], Value: "Court Serif"}}
	before := docHash(t, doc)

	// Rebind class 0 to nothing, then back.
	if !a.editorApply(a.mutateFontBind(0, "")) {
		t.Fatal("clearing a binding did nothing")
	}
	if _, ok := doc.side.BoundFamily(theme.FontElements[0]); ok {
		t.Error("clearing a binding left the row behind")
	}
	if !a.editorUndo() {
		t.Fatal("undo of a binding did nothing")
	}
	if got, ok := doc.side.BoundFamily(theme.FontElements[0]); !ok || got != "Court Serif" {
		t.Errorf("undo restored %q (present=%v), want Court Serif", got, ok)
	}
	if got := docHash(t, doc); got != before {
		t.Error("the SERIALISED document did not return after undoing a [fontbind] edit")
	}

	// A binding to a family that already holds is not an edit.
	if _, res := doc.apply(a.mutateFontBind(0, "Court Serif")); res != applyNoChange {
		t.Errorf("re-binding to the same family answered %d, want applyNoChange", res)
	}
}

// TestFontFamilyAddRemoveAndCapRefusal pins the [fonts] tier: an append, a removal,
// and the cap REFUSING rather than evicting somebody else's family.
func TestFontFamilyAddRemoveAndCapRefusal(t *testing.T) {
	a, doc := editDocRig(t, 1)

	// THE EVIDENCE HERE IS THE MODEL, NOT THE HASH, and that is a property of the
	// writer rather than a weaker assertion. setKey has no DELETE (sidecar_write.go),
	// so a key the model no longer has anything to say about keeps its existing line —
	// undoing an ADD therefore leaves the added line behind in the lossless carrier by
	// design, exactly as editProbeElement's comment describes for element keys. The
	// hash round trip is the right evidence for a VALUE edit (see
	// TestFontBindIsUndoableAndHashReverts, which clears an existing row and restores
	// it) and is the wrong evidence for a structural one.
	if !a.editorApply(a.mutateFontFamily(0, "Court Serif", "fonts/CourtSerif.otf")) {
		t.Fatal("adding the first family did nothing")
	}
	if len(doc.side.Fonts) != 1 || doc.side.Fonts[0].Family != "Court Serif" ||
		doc.side.Fonts[0].File != "fonts/CourtSerif.otf" {
		t.Fatalf("the add produced %+v", doc.side.Fonts)
	}
	if !a.editorUndo() {
		t.Fatal("undo of an add did nothing")
	}
	if len(doc.side.Fonts) != 0 {
		t.Errorf("undo of an add left %d families", len(doc.side.Fonts))
	}
	if _, err := doc.bytes(); err != nil {
		t.Fatalf("the document no longer serialises after an add and an undo: %v", err)
	}

	// Fill to the cap, then refuse.
	for i := 0; i < theme.FontCap; i++ {
		if !a.editorApply(a.mutateFontFamily(i, "fam"+itoa(i), "f"+itoa(i)+".otf")) {
			t.Fatalf("family %d refused below the cap", i)
		}
	}
	a.te.status = ""
	if a.editorApply(a.mutateFontFamily(theme.FontCap, "one too many", "x.otf")) {
		t.Fatalf("the %dth family was accepted past theme.FontCap (%d)", theme.FontCap+1, theme.FontCap)
	}
	if len(doc.side.Fonts) != theme.FontCap {
		t.Errorf("the cap evicted rather than refused: %d families remain", len(doc.side.Fonts))
	}
	if a.te.status == "" {
		t.Error("a cap refusal said nothing — design §3.3 forbids a silent one")
	}
}

// TestExitWithoutSavingThrowsAwayTypeEditsToo is the snapshot's coverage growing with
// the wave. restoreBaseline is "all or nothing"; a snapshot that covered elements and
// overrides but not the type tables would let "Back, Back" discard a session's
// geometry and silently KEEP its font bindings.
//
// UNCHANGED BY W8's re-baselining, deliberately: this session never saves, so the
// baseline never moves and "discard" still means "back to when I opened it". That it
// still passes verbatim is the open–closed evidence for rebaseline — the semantics
// change only where a SAVE happened, which is TestDiscardAfterASaveGoesBackToTheSave.
func TestExitWithoutSavingThrowsAwayTypeEditsToo(t *testing.T) {
	a, doc := editDocRig(t, 1)
	doc.side.Fonts = []theme.FontFamily{{Family: "Original", File: "o.otf"}}
	doc.side.FontBind = []theme.KV{{Key: theme.FontElements[0], Value: "Original"}}
	// The baseline is taken at OPEN, so re-open over the seeded tables.
	doc = newThemeDoc(doc.name, doc.side)
	a.te.doc = doc

	a.editorApply(a.mutateFontFamily(1, "Added", "a.otf"))
	a.editorApply(a.mutateFontBind(0, "Added"))
	doc.restoreBaseline()

	if len(doc.side.Fonts) != 1 || doc.side.Fonts[0].Family != "Original" {
		t.Errorf("exit-without-saving left %+v, want the one family the editor opened over", doc.side.Fonts)
	}
	if got, _ := doc.side.BoundFamily(theme.FontElements[0]); got != "Original" {
		t.Errorf("exit-without-saving left the binding at %q, want Original", got)
	}
	if doc.dirty {
		t.Error("restoreBaseline left the document dirty")
	}
}

// ---------------------------------------------------------------------------
// What a save is allowed to disturb
// ---------------------------------------------------------------------------

// drainThemeApply waits for the apply goroutine stamped gen to publish its result, so
// no off-thread load is still running when the harness tears the renderer down. It does
// NOT land the result: pollThemeApply replaces a.themeSidecar wholesale, and a gate that
// consumed one here would be measuring the reclaim rather than the save.
func drainThemeApply(t *testing.T, a *App, gen uint64) {
	t.Helper()
	deadline := time.Now().Add(editorGateApplyWait)
	for time.Now().Before(deadline) {
		if res := a.themeRes.Load(); res != nil && res.gen >= gen {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the theme apply at gen %d never published", gen)
}

// TestSavingGeometryDoesNotRestartTheTheme is design §"Live preview, no Apply button"
// made mechanical: applyThemeAsync runs for media/font import and theme switch, and for
// nothing else.
//
// THE DEFECT IT CLOSES. landEditorSave kicked an apply on EVERY successful save. An
// apply re-anchors a.themeAt and re-runs applyThemeClocks, so every clock group in the
// theme restarts from zero, and it calls purgeTextCache() — meaning saving a one-pixel
// rect nudge visibly restarted all motion and hitched the text cache. With the design's
// 2 s debounced autosave that is a motion restart every two seconds while authoring.
//
// The evidence is a.themeGen, which applyThemeAsync bumps SYNCHRONOUSLY before it starts
// its goroutine — so the gate cannot pass by racing it.
func TestSavingGeometryDoesNotRestartTheTheme(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	editorFixtureCatalog(t, a, t.TempDir(), themeOriginYours)
	a.te.selectOnly(tgts[0])
	a.editorNudgeSelection(1, 0, false)
	if !a.te.doc.dirty {
		t.Fatal("the fixture nudge did not dirty the document; the save below would prove nothing")
	}

	before := a.themeGen.Load()
	a.editorSave()
	if !a.editorAwaitSave(editorGateSaveWait) {
		t.Fatalf("the save did not land: %s", a.te.status)
	}
	if got := a.themeGen.Load(); got != before {
		t.Errorf("saving a rect nudge kicked %d theme apply(s) — pollThemeApply re-anchors a.themeAt "+
			"and purges the text cache, so every animated element in the theme restarts and the text "+
			"cache hitches, once per save", got-before)
	}
}

// TestSavingATypeEditAppliesTheThemeOnce is the other half, and it is what stops the fix
// above from being "delete the call".
//
// A [fontbind] row is resolved into real SDL_ttf faces by applySidecarFonts, on the
// theme-apply goroutine, against the sidecar ON DISK — so without an apply after the
// write the binding is saved and still drawn in the old face. The second save proves the
// comparison is against the last APPLY rather than against "the tables were ever
// touched": nothing new to resolve, no second restart.
func TestSavingATypeEditAppliesTheThemeOnce(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	editorFixtureCatalog(t, a, t.TempDir(), themeOriginYours)

	if !a.editorApply(a.mutateFontFamily(len(a.te.doc.side.Fonts), "Court Serif", "fonts/CourtSerif.otf")) {
		t.Fatal("the fixture family was refused")
	}
	if !a.editorApply(a.mutateFontBind(0, "Court Serif")) {
		t.Fatal("the fixture binding was refused")
	}

	before := a.themeGen.Load()
	a.editorSave()
	if !a.editorAwaitSave(editorGateSaveWait) {
		t.Fatalf("the save did not land: %s", a.te.status)
	}
	kicked := a.themeGen.Load()
	if kicked <= before {
		t.Fatalf("saving a [fontbind] edit kicked no apply (gen %d → %d) — the row would be on disk and "+
			"the courtroom would still be drawn in the old face until the user switched themes and back",
			before, kicked)
	}

	// A GEOMETRY save on top of it must be silent again: the type tables have not moved
	// since the apply above.
	a.te.selectOnly(tgts[0])
	a.editorNudgeSelection(0, 1, false)
	a.editorSave()
	if !a.editorAwaitSave(editorGateSaveWait) {
		t.Fatalf("the second save did not land: %s", a.te.status)
	}
	if got := a.themeGen.Load(); got != kicked {
		t.Errorf("a second save re-applied unchanged type tables (gen %d → %d) — the gate must compare "+
			"against the last APPLY, not against whether the tables were ever edited", kicked, got)
	}
	drainThemeApply(t, a, kicked)
}

// TestDiscardAfterASaveGoesBackToTheSave is the W8 exit-contract change, stated as
// the thing that used to be wrong.
//
// THE DEFECT. The baseline was taken once, at open, and nothing moved it. So
// edit → Save → edit → Back → Back left the MODEL at the pre-save document while
// DISK held the save — and closeThemeEditor drops the document, not the model, so
// the courtroom went on drawing a theme that no longer existed anywhere. The two
// states only reconciled the next time something happened to re-read the file.
//
// A DELIBERATE SEMANTICS CHANGE, and this is its evidence: "discard" now means
// "back to my last save". The gate that pins the OTHER meaning
// (TestExitWithoutSavingThrowsAwayTypeEditsToo) is unchanged and still passes,
// because a session with no save has no second meaning to choose between.
func TestDiscardAfterASaveGoesBackToTheSave(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	editorFixtureCatalog(t, a, t.TempDir(), themeOriginYours)
	doc := a.te.doc

	// EDIT ONE — saved. This is the state a discard must return to.
	a.te.selectOnly(tgts[0])
	a.editorNudgeSelection(3, 0, false)
	a.editorSave()
	if !a.editorAwaitSave(editorGateSaveWait) {
		t.Fatalf("the save did not land: %s", a.te.status)
	}
	saved, err := doc.bytes()
	if err != nil {
		t.Fatalf("serialising the saved document: %v", err)
	}

	// EDIT TWO — not saved. This is what the discard throws away.
	a.editorNudgeSelection(0, 5, false)
	if !doc.dirty {
		t.Fatal("the second nudge did not dirty the document; the discard below would prove nothing")
	}

	doc.restoreBaseline()
	got, err := doc.bytes()
	if err != nil {
		t.Fatalf("serialising the restored document: %v", err)
	}
	if string(got) != string(saved) {
		t.Errorf("a discard after a save did not return to the SAVE.\n"+
			"The file on disk is the theme, and closeThemeEditor keeps whatever the model holds — so a "+
			"baseline stuck at open time leaves memory and disk holding two different themes with nothing "+
			"on screen to say so.\n--- want (the save) ---\n%s\n--- got ---\n%s", saved, got)
	}
	if doc.dirty {
		t.Error("restoreBaseline left the document dirty")
	}
}

// TestASaveDoesNotStaleTheTypeSignature. rebaseline moves the tables the type
// signature is computed over, so a save that shifts the baseline and leaves
// typeSigApplied behind would make the NEXT save kick an apply for a change nobody
// made — or, in the other direction, skip one that was needed.
func TestASaveDoesNotStaleTheTypeSignature(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	editorFixtureCatalog(t, a, t.TempDir(), themeOriginYours)
	doc := a.te.doc

	if !a.editorApply(a.mutateFontFamily(len(doc.side.Fonts), "Court Serif", "fonts/CourtSerif.otf")) {
		t.Fatal("the fixture family was refused")
	}
	a.editorSave()
	if !a.editorAwaitSave(editorGateSaveWait) {
		t.Fatalf("the save did not land: %s", a.te.status)
	}
	kicked := a.themeGen.Load()
	if doc.typeSigApplied != doc.typeSig() {
		t.Fatal("typeSigApplied does not match the tables that were just saved and applied")
	}
	// And the baseline the discard would restore signs the SAME tables, so a
	// discard cannot silently put the faces on screen out of step with the model.
	doc.restoreBaseline()
	if doc.typeSigApplied != doc.typeSig() {
		t.Errorf("after a save and a discard the model's type tables no longer match the faces that " +
			"were resolved — the baseline and the applied signature moved apart")
	}
	drainThemeApply(t, a, kicked)
}

// TestFontRowEncodingIsOnePair is the packing gate: a [fonts] row rides ONE interned
// string inside the fixed-size editValue, and the two halves must survive it — a pair
// packed in one order and unpacked in another compiles and is silently wrong.
func TestFontRowEncodingIsOnePair(t *testing.T) {
	for _, tc := range []struct{ family, file string }{
		{"Court Serif", "fonts/Court Serif.otf"},
		{"A", ""},
		{"family=with=equals", "file, with, commas.ttf"},
	} {
		fam, file := fontRowOf(fontRowValue(tc.family, tc.file))
		if fam != tc.family || file != tc.file {
			t.Errorf("%q/%q round-tripped as %q/%q", tc.family, tc.file, fam, file)
		}
	}
	if fontRowValue("", "orphan.otf") != "" {
		t.Error("a row with no family must encode as the empty value, which is what REMOVES the row")
	}
}

// TestEditTargetFontSpaceIsExplicit extends W6's constructor census to W7b's space.
// A font target is index-identified exactly as an element is, and it answers for
// neither key space.
func TestEditTargetFontSpaceIsExplicit(t *testing.T) {
	ft := fontTarget(3)
	if idx, ok := ft.fontIdx(); !ok || idx != 3 {
		t.Errorf("fontTarget(3).fontIdx() = (%d, %v)", idx, ok)
	}
	if _, ok := ft.elemIdx(); ok {
		t.Error("a font target answered an ELEMENT index — the two spaces share a payload field and " +
			"must not share an accessor")
	}
	if ft.designKey() != "" || ft.classicKey() != "" {
		t.Errorf("a font target answered a key: %+v", ft)
	}
	if resizeEdgesFor(ft) != 0 {
		t.Error("a font row offered resize edges — it has no rect at all")
	}
	if _, ok := elementTarget(3).fontIdx(); ok {
		t.Error("an element target answered a FONT index")
	}
	// And the constructor still refuses the shapes that would leave the space guessed.
	for _, tc := range []struct {
		name string
		key  string
		elem int
	}{
		{"a font target carrying a key", "emotes", 3},
		{"a font target with no index", "", elemTargetNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("newEditTarget(spaceFont, %q, %d) built a target instead of panicking", tc.key, tc.elem)
				}
			}()
			_ = newEditTarget(spaceFont, tc.key, tc.elem)
		})
	}
}

// TestOnlyTheDocumentWritesTheTypeTables is the anti-second-writer census for the
// [fonts] / [fontbind] tier — the twin of TestOnlyTheDocumentWritesTheOverridesTier,
// and the seam gate rule 11 requires for every new abstraction.
//
// themeDoc.writeFontRow is the ONE function that may change a type table in a document
// under edit. Anything else assigning to Sidecar.Fonts or Sidecar.FontBind is a writer
// with no undo entry: an edit the user cannot take back and the baseline snapshot
// cannot restore — half an edit, silently.
func TestOnlyTheDocumentWritesTheTypeTables(t *testing.T) {
	const owner = "themeeditordoc.go"
	needles := []string{
		".Fonts = append", ".Fonts[i] =", ".Fonts[idx] =",
		".FontBind = append", ".FontBind[i].Value =",
	}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("globbing the package: %v (%d files)", err, len(files))
	}
	seenInOwner := 0
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		body := string(src)
		for _, n := range needles {
			if !strings.Contains(body, n) {
				continue
			}
			if filepath.Base(name) == owner {
				seenInOwner++
				continue
			}
			if strings.HasSuffix(name, "_test.go") {
				continue // fixtures seed tables directly; they are not the shipping writer
			}
			t.Errorf("%s writes a type table directly (%q) — every change belongs in "+
				"themeDoc.writeFontRow, which is what keeps the undo entry and the baseline "+
				"snapshot in step with the model", name, n)
		}
	}
	if seenInOwner == 0 {
		t.Fatalf("the census found none of its needles in %s — it would pass vacuously", owner)
	}
}

// TestTheMusicHeaderCannotStopUsingTheReflowGuard is the chrome-row seam's own
// encapsulation gate.
//
// The behavioural sweeps prove the CURRENT placement never overlaps. This one proves
// the placement cannot quietly go back to a strip of its own: planMusicHeader must
// call planChromeRow, and it must not open a second cursor beside it. Without this, a
// later wave could restore newPlStrip here and every overlap gate would keep passing
// right up until a theme squeezed the panel.
func TestTheMusicHeaderCannotStopUsingTheReflowGuard(t *testing.T) {
	body := funcBodyInPackage(t, "func planMusicHeader(")
	if !strings.Contains(body, "planChromeRow(") {
		t.Error("planMusicHeader no longer calls planChromeRow — the guarantee that two controls " +
			"cannot land on each other is the ONE cursor inside that planner, not arithmetic here")
	}
	if strings.Contains(body, "newPlStrip(") {
		t.Error("planMusicHeader opened a plStrip of its own beside the reflow planner — two " +
			"independent anchors is exactly the shape that painted Expand all on top of Stop music")
	}
	// And the priorities must stay a named ranking rather than bare integers, or the
	// reachability argument behind the drop order becomes unreadable at the call site.
	for _, want := range []string{"musicDropOnlyRoute", "musicDropShortcut", "chromeCtlNeverDrop"} {
		if !strings.Contains(body, want) {
			t.Errorf("planMusicHeader no longer names %s — the drop order is a reachability "+
				"argument (hard rule 9), not a taste ranking", want)
		}
	}
}

// ---------------------------------------------------------------------------
// The intake
// ---------------------------------------------------------------------------

// TestDroppedFaceInstallsThroughTheFunnel lands W4's written deferral: with the editor
// open, a dropped .ttf is copied into the theme and becomes ONE undoable [fonts] row.
//
// It drives HandleFileDrop, not editorTakeFontDrop, because the routing is half the
// feature — W4's own dropclaim.go gate exists because a missing arm there let a
// dropped font repoint the user's theme root.
func TestDroppedFaceInstallsThroughTheFunnel(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	dir := t.TempDir()
	// The REAL destination authority: editorThemeDir is editorSidecarPath's directory,
	// so a theme we may not write is refused by the same function that refuses the save
	// (TestSaveRefusesAThemeThatIsNotYours keeps that half).
	editorFixtureCatalog(t, a, dir, themeOriginYours)

	src := filepath.Join(t.TempDir(), "Court Serif.otf")
	if err := os.WriteFile(src, []byte("not really a face; the COPY is what is under test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if claim := claimDroppedFile(src, false); claim != dropClaimThemeFont {
		t.Fatalf("a dropped .otf is claimed as %d, want dropClaimThemeFont", claim)
	}
	before := len(a.te.doc.side.Fonts)
	if !a.editorTakeFontDrop(src) {
		t.Fatal("the open editor did not claim a dropped face")
	}
	if a.te.font == nil {
		t.Fatal("no copy was started")
	}
	// Drain the copy THROUGH THE SHIPPING LANDING, so the gate cannot pass while
	// pollEditorFont is deleted.
	if err := <-a.te.font.done; err != nil {
		t.Fatalf("the face copy failed: %v", err)
	}
	a.te.font.done <- nil
	a.pollEditorFont()

	fonts := a.te.doc.side.Fonts
	if len(fonts) != before+1 || fonts[len(fonts)-1].Family != "Court Serif" {
		t.Fatalf("the drop produced %+v, want one more family named for the file", fonts)
	}
	if got := fonts[len(fonts)-1].File; got != editFontDirName+"/Court Serif.otf" {
		t.Errorf("the row points at %q, want the copy under %s/", got, editFontDirName)
	}
	copied := filepath.Join(dir, editFontDirName, "Court Serif.otf")
	if _, err := os.Stat(copied); err != nil {
		t.Errorf("the face was not copied into the theme: %v", err)
	}
	// ONE undo step, and the FILE survives it — an undo that deleted a file the user
	// dropped would destroy data outside the document it owns.
	if !a.editorUndo() {
		t.Fatal("the installed row was not undoable")
	}
	if len(a.te.doc.side.Fonts) != before {
		t.Errorf("undo left %d families, want the %d it opened with", len(a.te.doc.side.Fonts), before)
	}
	if _, err := os.Stat(copied); err != nil {
		t.Error("undo deleted the dropped file — the ROW is the undoable thing, the file is not")
	}
}

// TestFontFamilyFromFileIsInsideTheFormatsBounds pins the derived name: it is what the
// [fonts] row will carry, so a name past theme.NameRuneCap would make the document
// unsaveable the moment it is dropped.
func TestFontFamilyFromFileIsInsideTheFormatsBounds(t *testing.T) {
	for _, tc := range []struct{ file, want string }{
		{"Court Serif.otf", "Court Serif"},
		{"court_serif-bold.ttf", "court serif bold"},
		{"  spaced   out .ttf", "spaced out"},
	} {
		if got := fontFamilyFromFile(tc.file); got != tc.want {
			t.Errorf("fontFamilyFromFile(%q) = %q, want %q", tc.file, got, tc.want)
		}
	}
	long := strings.Repeat("n", 400) + ".ttf"
	if n := len([]rune(fontFamilyFromFile(long))); n > theme.NameRuneCap {
		t.Errorf("a %d-rune family survived, past the format's %d", n, theme.NameRuneCap)
	}
}

// ---------------------------------------------------------------------------
// W7a follow-ups
// ---------------------------------------------------------------------------

// TestDeleteRefusalNamesTheOffenderAndTheFix splits W7a's one-sentence chip.
//
// "some elements were locked or the undo graveyard is full" named neither offender and
// offered no fix, and the two have OPPOSITE remedies — a locked element wants L, a full
// graveyard wants a save or a reopen.
func TestDeleteRefusalNamesTheOffenderAndTheFix(t *testing.T) {
	if got := editorDeleteRefusal(0, 0); got != "" {
		t.Errorf("a delete that refused nothing chipped %q", got)
	}
	locked := editorDeleteRefusal(3, 0)
	if !strings.Contains(locked, "locked") || !strings.Contains(strings.ToUpper(locked), "L") {
		t.Errorf("the locked chip is %q — it must name the lock and the key that clears it", locked)
	}
	if strings.Contains(locked, "graveyard") {
		t.Errorf("the locked chip mentions the graveyard: %q", locked)
	}
	full := editorDeleteRefusal(0, 2)
	if !strings.Contains(full, "graveyard") || !strings.Contains(full, itoa(themeElemGraveCap)) {
		t.Errorf("the graveyard chip is %q — it must name the limit (%d) and the fix", full, themeElemGraveCap)
	}
	if strings.Contains(full, "locked") {
		t.Errorf("the graveyard chip mentions the lock: %q", full)
	}
	both := editorDeleteRefusal(1, 1)
	if !strings.Contains(both, "locked") || !strings.Contains(both, "graveyard") {
		t.Errorf("the combined chip is %q — it must name both", both)
	}
	// Singular / plural, because "1 elements" reads as a bug in the editor.
	if got := editorPluralN(1, "locked element"); got != "1 locked element" {
		t.Errorf("editorPluralN(1) = %q", got)
	}
	if got := editorPluralN(2, "locked element"); got != "2 locked elements" {
		t.Errorf("editorPluralN(2) = %q", got)
	}
}

// TestEditorRedoRidesCtrlShiftZ pins the second redo spelling THROUGH THE REAL CHORD
// dispatcher, which is the only place it can be pinned: HandleEvent folds every
// non-clipboard Ctrl combination into one c.hotkey keysym, so an in-draw keyPressed
// check for Ctrl+Shift+Z would be dead code (editorUndoChord's own argument).
func TestEditorRedoRidesCtrlShiftZ(t *testing.T) {
	a, doc := editDocRig(t, 1)
	a.screen = ScreenThemeEditor
	a.ctx = &Ctx{}
	tgt := elementTarget(0)

	base, _ := doc.read(tgt, editFieldZ)
	if !a.editorApply(mutateElemField(tgt, editFieldZ, editValueInt(base.i[0]+3))) {
		t.Fatal("the fixture edit did nothing")
	}
	edited, _ := doc.read(tgt, editFieldZ)

	a.ctx.hotkey, a.ctx.shiftHeld = sdl.K_z, false
	if !a.editorUndoChord() {
		t.Fatal("Ctrl+Z did not reach the editor")
	}
	if got, _ := doc.read(tgt, editFieldZ); got != base {
		t.Fatalf("Ctrl+Z left z at %d, want the pre-edit %d", got.i[0], base.i[0])
	}

	a.ctx.hotkey, a.ctx.shiftHeld = sdl.K_z, true
	if !a.editorUndoChord() {
		t.Fatal("Ctrl+Shift+Z did not reach the editor")
	}
	if got, _ := doc.read(tgt, editFieldZ); got != edited {
		t.Fatalf("Ctrl+Shift+Z left z at %d, want the redone %d — Shift+Z is redo on every other "+
			"surface in this app, including its own focused-field arm", got.i[0], edited.i[0])
	}
	// Ctrl+Y is unchanged, and the chord is CONSUMED either way so a user hotkey bound
	// to z or y cannot double-fire mid-edit.
	a.ctx.hotkey, a.ctx.shiftHeld = sdl.K_y, false
	if !a.editorUndoChord() || a.ctx.hotkey != 0 {
		t.Error("Ctrl+Y no longer reaches the editor, or the chord was not consumed")
	}
}

// ---------------------------------------------------------------------------
// The offline pack's blip ladder
// ---------------------------------------------------------------------------

// TestOfflinePackMirrorsTheLiveBlipLadder is the downloader gap, stated as the
// property that was broken: the pack must be able to hold what the live ladder PLAYS.
//
// The download used to fetch rung 1 of six (sounds/blips/<lowercased name>), so a
// legacy base that serves only sounds/general/sfx-blip<name> produced a pack whose
// blips are silent while the same base blips fine online — an offline pack that works
// until you unplug.
func TestOfflinePackMirrorsTheLiveBlipLadder(t *testing.T) {
	const origin = "https://example.test/base/"
	for _, tc := range []struct{ cand, want string }{
		{origin + "sounds/blips/male", "sounds/blips/male"},
		{origin + "sounds/general/sfx-blipmale", "sounds/general/sfx-blipmale"},
		{origin + "sounds/blips/deep%20voice", "sounds/blips/deep voice"},
	} {
		got, ok := blipPackRel(origin, tc.cand)
		if !ok || got != tc.want {
			t.Errorf("blipPackRel(%q) = %q (ok=%v), want %q — the pack path must be what the LOCAL "+
				"resolver probes, un-escaped", tc.cand, got, ok, tc.want)
		}
	}
	// Refusals: a URL that is not under the origin, and any escape that would decode
	// into a path separator or a traversal.
	for _, bad := range []string{
		"https://elsewhere.test/sounds/blips/male",
		origin + "sounds/blips/%2e%2e",
		origin + "sounds/blips/a%2Fb",
		origin,
	} {
		if rel, ok := blipPackRel(origin, bad); ok {
			t.Errorf("blipPackRel accepted %q as %q — a hostile candidate must not steer a write", bad, rel)
		}
	}
}
