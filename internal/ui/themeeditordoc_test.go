package ui

// THE DOCUMENT AND THE TIMELINE (v1.90.0 W7a).
//
// These gates DRIVE the seam rather than mirroring it: every one of them goes
// through themeDoc.apply / revert and the real inspector table, so deleting the
// funnel, the inverse or a table row fails the suite. Rule 11's own words: a
// mirror-test that re-implements the seam's logic does not count.

import (
	"crypto/sha256"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// editProbeElement is an element with EVERY addressable field set to a
// non-default value.
//
// Non-default everywhere on purpose, and it is what makes the hash round-trip
// meaningful. The writer's setKey has no DELETE (sidecar_write.go says so): a key
// the model no longer has anything to say about keeps its existing line. So a field
// that started ABSENT, was edited to a value and then reverted would leave the line
// behind and the bytes would not return — a false failure that says nothing about
// the inverse. Starting with every key PRESENT means a revert rewrites the same
// line back to the same text, and byte equality really does mean "the document is
// where it was".
func editProbeElement() theme.Element {
	return theme.Element{
		ID:    "probe",
		Kind:  theme.ElemShape,
		Band:  theme.BandOverlay,
		Z:     6,
		Space: theme.SpaceChatbox,

		Anchor: "chatbox",
		Rect:   theme.Rect{X: 11, Y: 22, W: 33, H: 44},
		Rot:    5,

		Media: "art",
		Shape: "hex",
		Gen:   "grid",
		// GenParams / Slice / NoDecimate are set for the reason the whole fixture is:
		// the writer's setKey has no DELETE, so a key that started ABSENT and was edited
		// and reverted keeps the line it grew, and the byte round trip below would fail
		// for a reason that says nothing about the inverse. `decimate` is the sharpest
		// case — it is written INVERTED (formatBool(!NoDecimate), default "yes"), so a
		// probe that left it false would emit nothing, gain `decimate = no` on the edit,
		// and come back with a line the original never had.
		GenParams:  [theme.GenParamCap]theme.KV{{Key: "cols", Value: "4"}, {Key: "rows", Value: "2"}},
		Slice:      [4]int16{3, 4, 5, 6},
		NoDecimate: true,
		Fit:        theme.FitContain,
		Fill:       theme.RGBA{R: 1, G: 2, B: 3, A: 4},
		Fill2:      theme.RGBA{R: 5, G: 6, B: 7, A: 8},
		GradAngle:  9,
		GradRadial: true,
		Stroke:     theme.RGBA{R: 9, G: 10, B: 11, A: 12},
		StrokePx:   3,
		Tint:       theme.RGBA{R: 13, G: 14, B: 15, A: 16},
		Opacity:    200,

		Text:  "probe text",
		Font:  "body",
		Size:  12,
		Color: theme.RGBA{R: 17, G: 18, B: 19, A: 20},
		Align: theme.AlignCenter,

		Clock:          2,
		PhaseMs:        33,
		Loop:           true,
		Effect:         theme.FXPulse,
		EffectPeriodMs: 440,
		EffectAmpPct:   55,

		VisibleWhen: theme.Condition{Axis: theme.CondPos, Value: "wit"},
		Hidden:      true,
		Locked:      true,
	}
}

// editDocRig is a document over a sidecar carrying n probe elements, plus the App
// the inspector's getters read through.
func editDocRig(t *testing.T, n int) (*App, *themeDoc) {
	t.Helper()
	sc := theme.NewSidecar()
	for i := 0; i < n; i++ {
		el := editProbeElement()
		el.ID = "probe" + string(rune('a'+i))
		sc.Elements = append(sc.Elements, el)
	}
	doc := newThemeDoc("fixture", sc)
	if doc == nil {
		t.Fatal("newThemeDoc returned nil for a non-nil sidecar")
	}
	a := &App{te: &themeEditor{doc: doc}}
	// The document must serialise cleanly BEFORE anything is edited, or every hash
	// comparison below is comparing two error returns.
	if _, err := doc.bytes(); err != nil {
		t.Fatalf("the probe fixture does not serialise: %v", err)
	}
	return a, doc
}

// editNonDefaultKind is any element kind that is NOT the writer's default, used to
// force the `kind` key into a fixture document before its baseline hash is taken (see
// TestEveryInspectorFieldIsUndoable). ElemImage is the default; index 1 is not, and
// theme.ElementKindCount is 6, so this is a real kind rather than a wish.
const editNonDefaultKind = theme.ElementKind(1)

// editUnlockAll clears the probe element's authored lock.
//
// The probe is LOCKED by construction (every field non-default, see
// editProbeElement), and the lock is a real refusal: the nudge and the delete both
// honour it. A gate about coalescing or about the graveyard must therefore say out
// loud that it is not testing the lock — the one that IS
// (TestLockedElementsRefuseDestructiveEdits) sets it back explicitly.
func editUnlockAll(d *themeDoc) {
	for i := range d.side.Elements {
		d.side.Elements[i].Locked = false
	}
}

// docHash is the serialised document's digest — the "mutation reverts" evidence.
func docHash(t *testing.T, d *themeDoc) [32]byte {
	t.Helper()
	b, err := d.bytes()
	if err != nil {
		t.Fatalf("serialising the document: %v", err)
	}
	return sha256.Sum256(b)
}

// alterValue produces a DIFFERENT value for one inspector row, in the lane that
// row's field actually uses.
func alterValue(t *testing.T, d *themeDoc, f *inspectorField, v editValue) editValue {
	t.Helper()
	out := v
	switch f.kind {
	case fkRect:
		for i := range out.i {
			out.i[i] = v.i[i] + 1
		}
	case fkInt, fkPercent, fkAngle:
		// +1 rather than a large step: every numeric field in the probe is set well
		// inside its own range, so one unit is guaranteed to survive the setter's clamp
		// (a big step could saturate and produce "no change" on the narrow ones).
		out.i[0] = v.i[0] + 1
	case fkBool:
		out.i[0] = 1 - v.i[0]
	case fkEnum:
		if len(f.enum) < 2 {
			t.Fatalf("%s offers %d enum values — a cycler over one value can never change",
				f.label, len(f.enum))
		}
		out.i[0] = (v.i[0] + 1) % int32(len(f.enum))
	case fkColor:
		// Alpha forced opaque so the altered colour is unambiguously "present" and the
		// writer emits it; the revert puts the authored alpha back.
		out.b = [4]uint8{v.b[0] ^ 0x0F, v.b[1], v.b[2], 255}
	case fkText, fkMedia, fkFont, fkPick:
		// fkPick is altered as free text on purpose, and that is the assertion rather
		// than a shortcut: every fkPick field is read as FREE TEXT by the format, so a
		// name outside this build's live list has to survive a write and a read
		// unchanged. Appending a letter produces exactly such a name ("grid" -> "gridz")
		// and the hash round trip below is what proves it round-trips.
		out.s = d.intern(d.str(v.s) + "z")
	}
	return out
}

// ---------------------------------------------------------------------------
// Totality
// ---------------------------------------------------------------------------

// TestEveryElementKindHasInspectorFields is the totality gate over the format's own
// enum: a kind appended to theme.ElementKind cannot ship with a blank right rail.
func TestEveryElementKindHasInspectorFields(t *testing.T) {
	for k := theme.ElementKind(0); k < theme.ElementKindCount; k++ {
		rows := inspectorFields[k]
		if len(rows) == 0 {
			t.Errorf("%s has no inspector rows — selecting one would show an empty rail", k)
			continue
		}
		// Every kind must be able to move, to fade and to be hidden: the three things
		// that are true of a box whatever it paints.
		want := map[editField]bool{editFieldRect: false, editFieldOpacity: false, editFieldHidden: false}
		for i := range rows {
			if _, ok := want[rows[i].field]; ok {
				want[rows[i].field] = true
			}
			if rows[i].get == nil || rows[i].mut == nil {
				t.Errorf("%s row %q has a nil getter or mutator", k, rows[i].label)
			}
			if rows[i].field == editFieldNone {
				t.Errorf("%s row %q addresses no field", k, rows[i].label)
			}
		}
		for f, got := range want {
			if !got {
				t.Errorf("%s has no %s row — every element must be movable, fadeable and hideable", k, f)
			}
		}
	}
}

// TestEveryEditFieldHasAGetterAndASetter pins the two tables against the enum they
// are indexed by. A field appended without a reader would silently read as empty;
// one without a writer would silently refuse every edit.
func TestEveryEditFieldHasAGetterAndASetter(t *testing.T) {
	for f := editFieldNone + 1; f < editFieldCount; f++ {
		if editFieldGet[f] == nil {
			t.Errorf("%s (%d) has no getter — the inspector would draw it as empty forever", f, f)
		}
		if editFieldSet[f] == nil {
			t.Errorf("%s (%d) has no setter — every edit to it would be silently dropped", f, f)
		}
		if editFieldNames[f] == "" {
			t.Errorf("field %d has no name — its widget id and every diagnostic about it would collide", f)
		}
	}
}

// ---------------------------------------------------------------------------
// The undo contract
// ---------------------------------------------------------------------------

// TestEveryInspectorFieldIsUndoable is the wave's headline gate, in both halves.
//
// STRUCTURALLY: a row's `mut` must RETURN an op and must not mutate — so a field
// physically cannot bypass the undo stack, and every field added in v1.9x is
// automatically undoable.
//
// BEHAVIOURALLY, hash-verified: apply the op through the document, prove the model
// really changed, revert it, and prove the SERIALISED DOCUMENT is byte-identical to
// what it was. An inverse that is merely "the setter run again" would pass a
// value-equality check and fail this one the moment a setter clamped asymmetrically.
func TestEveryInspectorFieldIsUndoable(t *testing.T) {
	for k := theme.ElementKind(0); k < theme.ElementKindCount; k++ {
		for i := range inspectorFields[k] {
			f := &inspectorFields[k][i]
			a, doc := editDocRig(t, 1)
			// THE `kind` KEY IS FORCED PRESENT BEFORE THE BASELINE, and the reason is the
			// same one editProbeElement's own comment gives for every other field: setKey
			// omits a key whose value equals the DEFAULT and which is not already in the
			// document, but REWRITES it once it is there. The default kind is `image`, so
			// this loop's image pass would otherwise take its baseline with no `kind` line
			// at all, gain one on the edit, and come back with a line the original never
			// had — a false failure that says nothing about the inverse.
			doc.side.Elements[0].Kind = editNonDefaultKind
			if _, err := doc.bytes(); err != nil {
				t.Fatalf("%s/%s: seeding the kind key failed: %v", k, f.label, err)
			}
			doc.side.Elements[0].Kind = k
			if _, err := doc.bytes(); err != nil {
				t.Fatalf("%s/%s: the fixture does not serialise: %v", k, f.label, err)
			}
			tgt := elementTarget(0)
			before := docHash(t, doc)
			beforeEl := doc.side.Elements[0]

			cur := f.get(a, tgt, f.field)
			alt := alterValue(t, doc, f, cur)
			if alt == cur {
				t.Fatalf("%s/%s: the fixture could not produce a different value; this row is untested", k, f.label)
			}

			// STRUCTURAL: mut returns an op and writes nothing.
			op := f.mut(tgt, f.field, alt)
			if op.kind == opNone {
				t.Fatalf("%s/%s: mut returned no op", k, f.label)
			}
			if doc.side.Elements[0] != beforeEl {
				t.Fatalf("%s/%s: mut MUTATED the model — a row that can write is a row that can "+
					"forget the undo entry", k, f.label)
			}

			done, res := doc.apply(op)
			if res != applyDone {
				t.Fatalf("%s/%s: apply answered %d for a real change (%+v -> %+v), want applyDone",
					k, f.label, res, cur, alt)
			}
			if doc.side.Elements[0] == beforeEl {
				t.Fatalf("%s/%s: apply reported success but the model is unchanged", k, f.label)
			}
			if done.before != cur {
				t.Fatalf("%s/%s: apply recorded before=%+v, want the live value %+v — the op is its own "+
					"inverse only if it read the model", k, f.label, done.before, cur)
			}

			if !doc.revert(done) {
				t.Fatalf("%s/%s: revert refused", k, f.label)
			}
			if doc.side.Elements[0] != beforeEl {
				t.Errorf("%s/%s: revert left the model at %+v, want %+v", k, f.label,
					doc.side.Elements[0], beforeEl)
			}
			if got := docHash(t, doc); got != before {
				t.Errorf("%s/%s: the SERIALISED document did not return after undo — an edit that cannot "+
					"be un-saved is data loss wearing an undo stack as a hat", k, f.label)
			}
		}
	}
}

// TestMutatorsAreTopLevelFuncsThatReturnAnOp is the structural half, at the SOURCE.
//
// The behavioural gate above proves every row that EXISTS is undoable. This one
// proves the SHAPE cannot change: a row wired to a closure, a method value, or a
// function that returns something other than a themeEditOp would let the next wave
// reintroduce a direct writer, and the behavioural gate would keep passing for every
// row that had not yet been converted.
func TestMutatorsAreTopLevelFuncsThatReturnAnOp(t *testing.T) {
	const inspectorFile = "themeeditorinspector.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, inspectorFile, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", inspectorFile, err)
	}
	// Collect the top-level funcs this file declares, with their result types.
	results := map[string]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name == nil || fn.Type == nil || fn.Type.Results == nil {
			continue
		}
		if len(fn.Type.Results.List) != 1 {
			continue
		}
		if id, ok := fn.Type.Results.List[0].Type.(*ast.Ident); ok {
			results[fn.Name.Name] = id.Name
		}
	}
	// Every `mut:` / `get:` value in the file must name one of them.
	seen := 0
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || (key.Name != "mut" && key.Name != "get") {
			return true
		}
		seen++
		id, ok := kv.Value.(*ast.Ident)
		if !ok {
			t.Errorf("%s: %s is wired to %T at %s — it must name a TOP-LEVEL func; a closure or a "+
				"method value allocates per row and can capture state the undo stack never sees",
				inspectorFile, key.Name, kv.Value, fset.Position(kv.Pos()))
			return true
		}
		res, declared := results[id.Name]
		if !declared {
			t.Errorf("%s: %s names %s, which this file does not declare as a top-level func (%s)",
				inspectorFile, key.Name, id.Name, fset.Position(kv.Pos()))
			return true
		}
		if key.Name == "mut" && res != "themeEditOp" {
			t.Errorf("%s: mut names %s, which returns %s — a mutator must RETURN an op, never perform "+
				"one, or a row can bypass the undo stack (%s)",
				inspectorFile, id.Name, res, fset.Position(kv.Pos()))
		}
		if key.Name == "get" && res != "editValue" {
			t.Errorf("%s: get names %s, which returns %s, want editValue (%s)",
				inspectorFile, id.Name, res, fset.Position(kv.Pos()))
		}
		return true
	})
	if seen == 0 {
		t.Fatal("the census found no get/mut wiring at all — it stopped matching the table it is named for")
	}
}

// TestDragIsOneUndoStep pins coalescing on the shape a drag has: many mutations to
// ONE field of ONE target inside the window.
func TestDragIsOneUndoStep(t *testing.T) {
	a, doc := editDocRig(t, 1)
	tgt := elementTarget(0)
	base := time.Now()
	const frames = 60
	for i := 0; i < frames; i++ {
		v, _ := doc.read(tgt, editFieldRect)
		v.i[0] += 3
		op := mutateElemField(tgt, editFieldRect, v)
		done, res := doc.apply(op)
		if res != applyDone {
			t.Fatalf("frame %d: apply answered %d, want applyDone", i, res)
		}
		a.te.ring.push(done, base.Add(time.Duration(i)*16*time.Millisecond))
	}
	if a.te.ring.n != 1 {
		t.Fatalf("a %d-frame drag produced %d undo entries, want 1 — Ctrl+Z would step one pixel at a "+
			"time through a gesture the user made once", frames, a.te.ring.n)
	}
	if !a.editorUndo() {
		t.Fatal("undo refused after a drag")
	}
	if got := doc.side.Elements[0].Rect.X; got != editProbeElement().Rect.X {
		t.Errorf("one Ctrl+Z left X at %d, want the pre-drag %d — the merged op kept the wrong `before`",
			got, editProbeElement().Rect.X)
	}
}

// TestNudgeBurstIsOneUndoStep drives the REAL keyboard path (editorNudgeSelection),
// not a hand-built op stream — so a future change that groups every tap (and thereby
// defeats the merge, because the group is part of the coalescing identity) fails
// here rather than in a playtest.
func TestNudgeBurstIsOneUndoStep(t *testing.T) {
	a, doc := editDocRig(t, 1)
	editUnlockAll(doc)
	a.te.selectOnly(elementTarget(0))
	const taps = 20
	for i := 0; i < taps; i++ {
		a.editorNudgeSelection(1, 0, false)
	}
	if a.te.ring.n != 1 {
		t.Fatalf("%d arrow taps produced %d undo entries, want 1", taps, a.te.ring.n)
	}
	if got, want := doc.side.Elements[0].Rect.X, editProbeElement().Rect.X+taps; got != want {
		t.Fatalf("the burst moved X to %d, want %d", got, want)
	}
	a.editorUndo()
	if got, want := doc.side.Elements[0].Rect.X, editProbeElement().Rect.X; got != want {
		t.Errorf("one Ctrl+Z left X at %d, want %d", got, want)
	}
}

// TestTwoTargetsNeverCoalesce is the trap coalesceKey's target field exists for: two
// elements nudged inside the same window must not merge into one op whose `before`
// belongs to the first and whose `after` belongs to the second.
func TestTwoTargetsNeverCoalesce(t *testing.T) {
	a, doc := editDocRig(t, 2)
	base := time.Now()
	for i := 0; i < 2; i++ {
		tgt := elementTarget(i)
		v, _ := doc.read(tgt, editFieldRect)
		v.i[0] += 5
		done, res := doc.apply(mutateElemField(tgt, editFieldRect, v))
		if res != applyDone {
			t.Fatalf("element %d: apply answered %d, want applyDone", i, res)
		}
		a.te.ring.push(done, base.Add(time.Duration(i)*10*time.Millisecond))
	}
	if a.te.ring.n != 2 {
		t.Fatalf("two elements edited inside the coalescing window produced %d entries, want 2 — a merge "+
			"across targets writes one element's old value onto another", a.te.ring.n)
	}
}

// TestGroupedOpsUndoTogether pins the property "try a preset, hate it, Ctrl+Z"
// rests on, using the shipped grouped path (a multi-select toggle).
func TestGroupedOpsUndoTogether(t *testing.T) {
	a, doc := editDocRig(t, 3)
	for i := 0; i < 3; i++ {
		a.te.selectAdd(elementTarget(i))
	}
	a.editorToggleField(editFieldHidden)
	for i := 0; i < 3; i++ {
		if doc.side.Elements[i].Hidden {
			t.Fatalf("element %d was not un-hidden by the grouped toggle", i)
		}
	}
	if a.te.ring.n != 3 {
		t.Fatalf("the grouped toggle recorded %d ops, want 3", a.te.ring.n)
	}
	if !a.editorUndo() {
		t.Fatal("undo refused")
	}
	for i := 0; i < 3; i++ {
		if !doc.side.Elements[i].Hidden {
			t.Errorf("element %d survived ONE Ctrl+Z un-hidden — a grouped edit must undo together, or "+
				"the user has to press Ctrl+Z once per element they never edited individually", i)
		}
	}
	if a.te.ring.applied != 0 {
		t.Errorf("the timeline is at %d after undoing a 3-op group, want 0", a.te.ring.applied)
	}
}

// TestUndoRingIsCappedAndNeverAllocatesAfterWarmup is the hard-rule-4 gate plus the
// zero-alloc one: the ring is a fixed array, so pushing past its cap drops the
// OLDEST entry and costs nothing.
func TestUndoRingIsCappedAndNeverAllocatesAfterWarmup(t *testing.T) {
	a, doc := editDocRig(t, 1)
	tgt := elementTarget(0)
	base := time.Now()
	// Well past the cap, each op outside the coalescing window so none of them merge.
	const pushes = themeEditUndoCap * 3
	for i := 0; i < pushes; i++ {
		v, _ := doc.read(tgt, editFieldRect)
		v.i[0]++
		done, _ := doc.apply(mutateElemField(tgt, editFieldRect, v))
		a.te.ring.push(done, base.Add(time.Duration(i)*time.Second))
	}
	if a.te.ring.n != themeEditUndoCap {
		t.Fatalf("the ring holds %d ops after %d pushes, want the cap %d", a.te.ring.n, pushes, themeEditUndoCap)
	}

	ring := &a.te.ring
	op := ring.ops[0]
	now := base
	push := func() {
		now = now.Add(time.Second)
		ring.push(op, now)
	}
	push()
	if n := testing.AllocsPerRun(200, push); n != 0 {
		t.Fatalf("pushing to a FULL undo ring allocates %.1f/op, want 0 — this runs every frame of a "+
			"drag on the render thread", n)
	}
}

// TestSelectionIsAFixedArray pins the selection's bound and its refusal: past the
// cap it stops accepting rather than growing.
func TestSelectionIsAFixedArray(t *testing.T) {
	e := &themeEditor{}
	for i := 0; i < themeSelCap*2; i++ {
		e.selectAdd(elementTarget(i))
	}
	if e.selN != themeSelCap {
		t.Fatalf("the selection holds %d after %d adds, want the cap %d", e.selN, themeSelCap*2, themeSelCap)
	}
	// Adding the same target twice is not two selections.
	e.selectClear()
	e.selectAdd(elementTarget(4))
	e.selectAdd(elementTarget(4))
	if e.selN != 1 {
		t.Errorf("selecting the same element twice gave %d entries, want 1", e.selN)
	}
	if !e.selectedIs(elementTarget(4)) || e.selectedIs(elementTarget(5)) {
		t.Error("selectedIs answered for the wrong target")
	}
	e.selectToggle(elementTarget(4))
	if e.selN != 0 {
		t.Errorf("Ctrl+click on a selected element left %d entries, want 0", e.selN)
	}
}

// TestUndoOfAnElementDeleteRestoresItExactly pins the graveyard: a delete cannot be
// approximately undone, because "approximately" means the user's authored values are
// gone.
func TestUndoOfAnElementDeleteRestoresItExactly(t *testing.T) {
	a, doc := editDocRig(t, 3)
	editUnlockAll(doc)
	before := docHash(t, doc)
	want := doc.side.Elements[1]

	a.te.selectOnly(elementTarget(1))
	a.editorDeleteSelection()
	if len(doc.side.Elements) != 2 {
		t.Fatalf("the delete left %d elements, want 2", len(doc.side.Elements))
	}
	if !a.editorUndo() {
		t.Fatal("undo refused after a delete")
	}
	if len(doc.side.Elements) != 3 {
		t.Fatalf("undo left %d elements, want 3", len(doc.side.Elements))
	}
	if got := doc.side.Elements[1]; got != want {
		t.Errorf("the resurrected element is %+v, want %+v — a delete that cannot be undone exactly is "+
			"data loss", got, want)
	}
	if got := docHash(t, doc); got != before {
		t.Error("the serialised document did not return after undoing a delete")
	}
}

// TestLockedElementsRefuseDestructiveEdits pins the editor-only lock across the two
// paths that can destroy work: the nudge and the delete.
func TestLockedElementsRefuseDestructiveEdits(t *testing.T) {
	a, doc := editDocRig(t, 1)
	doc.side.Elements[0].Locked = true
	a.te.selectOnly(elementTarget(0))

	a.editorNudgeSelection(1, 1, false)
	if got := doc.side.Elements[0].Rect; got != editProbeElement().Rect {
		t.Errorf("a locked element moved to %+v — the lock is the one promise the rail's padlock makes", got)
	}
	a.editorDeleteSelection()
	if len(doc.side.Elements) != 1 {
		t.Error("a locked element was deleted")
	}
}

// TestElementInsertRefusesPastTheCap pins the format's own ruling (design §3.3,
// reconciled 2026-08-04): caps REFUSE, they do not truncate — an add that succeeded
// into an over-cap model would produce a document Sidecar.Bytes() then refuses to
// save, which turns an editing mistake into an unsaveable session.
func TestElementInsertRefusesPastTheCap(t *testing.T) {
	_, doc := editDocRig(t, 1)
	for len(doc.side.Elements) < theme.ElementCap {
		el := editProbeElement()
		el.ID = "e" + string(rune('a'+len(doc.side.Elements)%26)) + string(rune('a'+len(doc.side.Elements)/26))
		doc.side.Elements = append(doc.side.Elements, el)
	}
	slot, ok := doc.graveSlot()
	if !ok {
		t.Fatal("the graveyard refused a slot on an empty graveyard")
	}
	v := doc.park(slot, editProbeElement())
	if doc.insertAt(elementTarget(len(doc.side.Elements)), v) {
		t.Fatalf("inserting past ElementCap (%d) succeeded — the model would then be unsaveable",
			theme.ElementCap)
	}
}

// TestGraveyardIsBoundedAndRefusesRatherThanLosingABody pins hard rule 4 with a
// BEHAVIOUR: at the cap a delete is refused, so no body is ever dropped on the floor
// while an op that claims to resurrect it stays on the timeline.
func TestGraveyardIsBoundedAndRefusesRatherThanLosingABody(t *testing.T) {
	_, doc := editDocRig(t, 1)
	for i := 0; i < themeElemGraveCap; i++ {
		if _, ok := doc.graveSlot(); !ok {
			t.Fatalf("the graveyard refused slot %d, before its cap of %d", i, themeElemGraveCap)
		}
	}
	if _, ok := doc.graveSlot(); ok {
		t.Fatalf("the graveyard handed out slot %d, past its cap of %d", themeElemGraveCap, themeElemGraveCap)
	}
	op, res := doc.applyDelete(themeEditOp{kind: opElemDel, target: elementTarget(0)})
	if res == applyDone {
		t.Fatal("a delete succeeded with a full graveyard — its body would be unrecoverable while the " +
			"undo entry claimed otherwise")
	}
	// And it is a REFUSAL, not a no-op: a named bound (themeElemGraveCap) stood in the
	// way, so the rail must say so. applyNoChange here would make the delete key look
	// broken — the element stays put and nothing on screen explains why.
	if res != applyRefused {
		t.Errorf("a graveyard-full delete answered %d, want applyRefused — a bound that refuses "+
			"silently is a feature that appears to be broken", res)
	}
	_ = op
	if len(doc.side.Elements) != 1 {
		t.Error("the refused delete removed the element anyway")
	}
}

// TestDeletingAnElementPersistsThroughTheSave closes the gap the preset wave's
// own carrier bug pointed at: NO element deletion had ever reached the file.
//
// The writer edits the lossless document the reader kept, so an element taken out
// of `side.Elements` and nowhere else is re-emitted from its own surviving
// [element.<id>] section on the next save and read straight back in — a delete
// that looks like it worked for the whole session and is undone by reopening the
// theme. Every existing delete gate stops at the MODEL, which is exactly why this
// survived: the model is right in all of them.
func TestDeletingAnElementPersistsThroughTheSave(t *testing.T) {
	_, doc := editDocRig(t, 2)
	editUnlockAll(doc)
	gone := doc.side.Elements[0].ID

	op, res := doc.applyDelete(themeEditOp{kind: opElemDel, target: elementTarget(0)})
	if res != applyDone {
		t.Fatalf("the delete did not apply (%d) — the gate cannot run", res)
	}
	raw := mustDocBytes(t, doc)
	if s := "[element." + gone + "]"; strings.Contains(string(raw), s) {
		t.Fatalf("%s is still in the saved file after the element was deleted — reopening the theme "+
			"brings it back:\n%s", s, raw)
	}
	back, err := theme.ParseSidecar(raw)
	if err != nil {
		t.Fatalf("the file we just wrote does not read back: %v", err)
	}
	if _, resurrected := back.FindElement(gone); resurrected {
		t.Fatalf("element %q came back out of the file the delete had already been applied to", gone)
	}
	if len(back.Elements) != len(doc.side.Elements) {
		t.Fatalf("the file holds %d elements, the model holds %d", len(back.Elements), len(doc.side.Elements))
	}
	// And UNDO still restores it — the carrier loses the section, the graveyard
	// keeps the body (theme.Sidecar.RetireElementSection's own note).
	if !doc.revert(op) {
		t.Fatal("undoing the delete failed")
	}
	restored, err := theme.ParseSidecar(mustDocBytes(t, doc))
	if err != nil {
		t.Fatalf("re-read after undo: %v", err)
	}
	if _, ok := restored.FindElement(gone); !ok {
		t.Fatalf("element %q did not come back after undo — a delete that cannot be taken back is not "+
			"an editor operation", gone)
	}
}

// TestDiscardingADeleteLeavesTheFileByteIdentical is the retire seam's OTHER exit,
// and the only gate in the wave that measures the discard against BYTES.
//
// A delete has two halves: the model's, and the lossless carrier's. The second is
// DEFERRED to the next write (theme.Sidecar.RetireElementSection's own note),
// because the file has to come back byte-identical when the user changes their
// mind. insertAt cancels the retirement, so the per-element undo was covered; the
// EDITOR'S DISCARD — "Back, Back", restoreBaseline — restored the model and left
// the retirement standing, so the very next save still stripped the author's
// [element.<id>] lines from where they were written and re-emitted the section
// canonically at the end of the file.
//
// WHY BYTES AND NOT THE MODEL. Every model-level assertion passes either way: the
// element is present, its fields are right, the count is right, and it reads back.
// What changes is the section's POSITION, and element declaration order is reload
// order — so the silent damage is a reordered theme plus the loss of that one
// section's authored key order and comments. Nothing but a byte comparison can see
// it, which is how it survived the wave that introduced the seam.
//
// It also outlives the editor: d.side and a.themeSidecar are the same object and
// closeThemeEditor drops only the editor, so the stale retirement reaches disk
// through internal/themepack's copy and create as readily as through a save.
func TestDiscardingADeleteLeavesTheFileByteIdentical(t *testing.T) {
	_, doc := editDocRig(t, 2)
	editUnlockAll(doc) // the probe is authored LOCKED, and a locked element refuses a delete
	// RE-OPEN over the unlocked model, because the baseline is taken at construction
	// and a discard restores it wholesale — including the lock. Without this the gate
	// would be measuring "the discard threw away my unlock" (which it should, and
	// TestExitWithoutSavingThrowsAwayTypeEditsToo is where that belongs) instead of
	// the carrier question it is about.
	doc = newThemeDoc(doc.name, doc.side)

	// The BASELINE is now this document's own open state, so the pre-delete bytes are
	// the baseline's bytes. Serialising here also materialises the [element.*]
	// sections in the carrier, which is what gives the retirement below anything to
	// take.
	before := mustDocBytes(t, doc)
	gone := doc.side.Elements[0].ID
	if s := "[element." + gone + "]"; !strings.Contains(string(before), s) {
		t.Fatalf("the fixture file does not carry %s, so there is no section for a retirement to "+
			"take and this gate would prove nothing:\n%s", s, before)
	}

	if _, res := doc.applyDelete(themeEditOp{kind: opElemDel, target: elementTarget(0)}); res != applyDone {
		t.Fatalf("the delete did not apply (%d) — the gate cannot run", res)
	}
	if len(doc.side.Elements) != 1 {
		t.Fatalf("the delete left %d elements in the model, want 1", len(doc.side.Elements))
	}
	// NOTHING SERIALISES BETWEEN HERE AND THE DISCARD, deliberately. A write FLUSHES
	// the pending retirement, and a delete-save-undo is the seam's own stated
	// residual (RetireElementSection's last paragraph) — the section is gone from
	// disk by then and holding its lines for the rest of the session would be a
	// second graveyard for one act. The gesture this gate is about is "delete, then
	// leave without saving", which never writes. That the carrier half of the delete
	// is armed at all is TestDeletingAnElementPersistsThroughTheSave's job.

	// ...and the user backs out without saving.
	doc.restoreBaseline()
	if len(doc.side.Elements) != 2 {
		t.Fatalf("the discard restored %d elements, want 2", len(doc.side.Elements))
	}
	after := mustDocBytes(t, doc)
	if string(after) != string(before) {
		t.Fatalf("delete + discard + save is not byte-identical to the pre-delete file — the "+
			"discarded element's section was moved to the end of the file, which reorders the theme "+
			"on the next load and loses that section's authored key order and comments:\n"+
			"--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// mustDocBytes is docHash's other half: the bytes rather than their digest, for
// the gates that have to read what was written.
func mustDocBytes(t *testing.T, d *themeDoc) []byte {
	t.Helper()
	b, err := d.bytes()
	if err != nil {
		t.Fatalf("serialising the document: %v", err)
	}
	return b
}

// TestStringInterningIsBoundedAndRefusesRatherThanAliasing pins editStrCap: a full
// table returns editStrNone, which reads as "" — and the drawing path treats that as
// a refusal with a named chip rather than writing an empty string over the user's
// value.
func TestStringInterningIsBoundedAndRefusesRatherThanAliasing(t *testing.T) {
	_, doc := editDocRig(t, 1)
	ids := map[editStrID]bool{}
	for i := 0; i < editStrCap*2; i++ {
		id := doc.intern("s" + string(rune('a'+i%26)) + string(rune('a'+i/26)))
		if id == editStrNone {
			continue
		}
		if ids[id] {
			t.Fatalf("intern reused id %d for a different string — every op holding it would now mean "+
				"something else", id)
		}
		ids[id] = true
	}
	if len(ids) != editStrCap-1 {
		t.Fatalf("interned %d distinct strings, want %d (the table minus its reserved empty slot)",
			len(ids), editStrCap-1)
	}
	if doc.str(editStrNone) != "" {
		t.Error("id 0 must always be the empty string")
	}
	if doc.str(editStrID(editStrCap+5)) != "" {
		t.Error("an out-of-range id must read as empty, never panic")
	}
}

// ---------------------------------------------------------------------------
// Encapsulation
// ---------------------------------------------------------------------------

// TestOnlyTheDocumentUsesTheFieldTables is the encapsulation census.
//
// editFieldGet and editFieldSet ARE the write path. If any other file reaches into
// them, that file is a second writer with no undo entry — exactly the failure mode
// the whole document layer exists to prevent, and exactly the shape the sidecar's
// own deleted Doc() accessor had. The allow-list is one file, by name.
func TestOnlyTheDocumentUsesTheFieldTables(t *testing.T) {
	const owner = "themeeditordoc.go"
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	hits := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		n := strings.Count(string(src), "editFieldSet[") + strings.Count(string(src), "editFieldGet[")
		if n == 0 {
			continue
		}
		hits += n
		if name != owner {
			t.Errorf("%s indexes the element field tables (%d times) — every write must go through "+
				"themeDoc.apply, which is what fills in the op's inverse. A second writer produces edits "+
				"that cannot be undone, and nothing on screen says so", name, n)
		}
	}
	if hits == 0 {
		t.Fatalf("the census found no use of the field tables at all — it stopped matching %s", owner)
	}
}

// TestThemeEditorScreenIsWiredAtEverySwitchSite is the anti-half-wiring census.
//
// A new Screen has FIVE obligations in app.go and four of them fail SILENTLY: an
// unwired expScreenSkippable burns full CPU forever with nothing on screen to
// explain it, an unwired Esc arm strands the user on a screen with no way out, an
// unwired chord claim leaves Ctrl+Z dead on the entry point that has no courtroom
// pass to ride, and an unwired draw dispatch shows the previous screen's pixels.
// Only the draw one is obvious, and only after you look.
func TestThemeEditorScreenIsWiredAtEverySwitchSite(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("reading app.go: %v", err)
	}
	body := string(src)
	for _, want := range []struct{ site, needle string }{
		{"the frame-rate skippability switch (expScreenSkippable)", "case ScreenThemeEditor:\n\t\t// ITS OWN CASE"},
		{"the draw dispatch", "case ScreenThemeEditor:\n\t\t\ta.drawThemeEditor(winW, winH)"},
		{"the Esc ladder", "case ScreenThemeEditor:\n\t\t\t\t// The editor's own ladder"},
		{"pollThemeApply's document reclaim", "a.reclaimThemeDoc()"},
		// The pre-screen Ctrl+Z / Ctrl+Y claim. editorUndoChord's other caller is
		// handleHotkeys, which runs only from the COURTROOM passes — so without this line
		// the chord reaches the editor only while a live room paints behind its canvas,
		// and the Settings entry point (no server attached) has no undo at all.
		// TestEditorUndoChordReachesTheEditorThroughAFrameWithNoRoom is the behavioural
		// half; this is the one that fails the moment the line is deleted.
		{"the pre-screen chord claim", "if a.themeEditorOpen() {\n\t\ta.editorUndoChord()\n\t}"},
	} {
		if !strings.Contains(body, want.needle) {
			t.Errorf("app.go no longer wires ScreenThemeEditor into %s — four of this screen's five "+
				"obligations fail silently (full CPU forever, no way out with Esc, no keyboard undo, "+
				"the previous screen's pixels), so the wiring is censused rather than trusted", want.site)
		}
	}
}

// TestEditsSurviveAThemeReapply drives the seam the whole document layer was built
// for: a background theme apply (a texture eviction heal, the texture-filter row,
// boot) must not discard unsaved edits.
func TestEditsSurviveAThemeReapply(t *testing.T) {
	a, prefs := newEffectsRig(t)
	prefs.SetTheme("fixture", "")
	sc := theme.NewSidecar()
	sc.Elements = append(sc.Elements, editProbeElement())
	a.themeSidecar = sc
	if !a.openThemeEditorForTest(ScreenSettings) {
		t.Fatal("the editor refused to open over a sidecar")
	}
	editUnlockAll(a.te.doc)
	a.te.selectOnly(elementTarget(0))
	a.editorNudgeSelection(1, 0, false)
	edited := a.themeSidecar.Elements[0].Rect

	// The apply, exactly as pollThemeApply does it: a freshly parsed sidecar lands
	// over the live one…
	a.themeSidecar = theme.NewSidecar()
	a.themeSidecar.Elements = append(a.themeSidecar.Elements, editProbeElement())
	// …and the reclaim puts the edited document back.
	a.reclaimThemeDoc()

	if got := a.themeSidecar.Elements[0].Rect; got != edited {
		t.Fatalf("a background theme apply discarded the unsaved edit (%+v, want %+v) — applies are not "+
			"user-initiated, so this is work vanishing with nothing on screen to say why", got, edited)
	}

	// …and a DIFFERENT theme correctly loses it: the document is about one theme.
	prefs.SetTheme("somethingelse", "")
	fresh := theme.NewSidecar()
	a.themeSidecar = fresh
	a.reclaimThemeDoc()
	if a.themeSidecar != fresh {
		t.Error("the reclaim pasted the old theme's document over a DIFFERENT theme")
	}
}

// openThemeEditorForTest is openThemeEditor without the overlay teardown, which
// needs a renderer. It exists so this file's gates run headlessly.
//
// It calls the PRODUCTION arm (armThemeEditor) rather than restating it: a helper
// that built the editor its own way would be a second construction path, and the
// half it forgot — the document's binding to the applied design map — is exactly
// the kind of thing that then only fails on a real screen.
func (a *App) openThemeEditorForTest(from Screen) bool {
	if a.themeSidecar == nil {
		return false
	}
	return a.armThemeEditor(from)
}

// TestClosedThemeEditorCostsNothing is the v1.89.0 "0 base = 0 cost" rule applied to
// this wave: with the editor shut, the client carries ONE nil pointer and nothing
// else — no allocation, no goroutine, no per-frame work. Benchmarked, not asserted.
func TestClosedThemeEditorCostsNothing(t *testing.T) {
	a := &App{}
	probe := func() {
		_ = a.themeEditorOpen()
		a.reclaimThemeDoc()
	}
	probe()
	if n := testing.AllocsPerRun(1000, probe); n != 0 {
		t.Fatalf("a CLOSED theme editor allocates %.1f/op, want 0", n)
	}
	if a.te != nil {
		t.Error("probing a closed editor created one")
	}
}
