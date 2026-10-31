package ui

// Undo stack for the theme editor: one stack over one document, because cross-domain edits are normal and separate
// stacks would make Ctrl+Z ambiguous.
// An op is a value with no pointers or closures, since the ring is written from a per-frame drag on the render thread
// and must not allocate.
// themeDoc.apply reads the current value into op.before before writing op.after, so undo and redo share one code path
// and no field can be edited without an undo entry.

import "time"

const (
	// themeEditUndoCap bounds the single timeline; past it the oldest op is dropped, never the newest.
	themeEditUndoCap = 128

	// editStrCap bounds the per-session string table that keeps editValue fixed size; past it an op carrying a new
	// string is refused (mutateElemField returns opNone) rather than editing to the wrong string.
	editStrCap = 256

	// editCoalesceWindow makes a continuous gesture one undo step, while two deliberate edits stay two steps.
	editCoalesceWindow = 400 * time.Millisecond
)

// editStrID indexes the editor's string table; 0 is always the empty string, so a zero editValue means empty.
type editStrID int16

// editStrNone is the empty string's id, and the zero value.
const editStrNone editStrID = 0

// editValue is one field's value in a fixed size: a rect or scalar in i, an RGBA in b, an interned string in s.
// A union, not an interface, because an interface would box every int and allocate per frame during a drag.
// Only editFieldGet and editFieldSet know which parts a field uses.
type editValue struct {
	i [4]int32
	b [4]uint8
	s editStrID
}

// Named constructors, so a call site reads as the part it fills.
func editValueInt(v int32) editValue           { return editValue{i: [4]int32{v}} }
func editValueRect(x, y, w, h int32) editValue { return editValue{i: [4]int32{x, y, w, h}} }
func editValueRGBA(r, g, b, a uint8) editValue { return editValue{b: [4]uint8{r, g, b, a}} }
func editValueStr(id editStrID) editValue      { return editValue{s: id} }
func editValueBool(on bool) editValue          { return editValueInt(boolToInt32(on)) }
func boolToInt32(on bool) int32 {
	if on {
		return 1
	}
	return 0
}

// editOpKind is what a mutation did; the zero value opNone pushes nothing, so a forgotten or refused op has no effect.
type editOpKind uint8

const (
	// opNone: not a mutation. Never pushed; see themeEditRing.push.
	opNone editOpKind = iota
	// opElemField: one field of one free element, named by editField.
	opElemField
	// opElemAdd: an element appended at index target.elem; after names the graveyard entry holding its body.
	opElemAdd
	// opElemDel: the element at target.elem removed; before names the graveyard entry holding its body for undo.
	opElemDel
	// opSlotRect: one AO2 widget's [overrides] row, with the rect in i plus editSlotRowPresent, so undo can delete a
	// row the edit created.
	opSlotRect
	// opSlotReset: one AO2 widget's [overrides] row removed; after has editSlotRowPresent clear, so redo drops the row
	// again.
	opSlotReset
	// opFontFamily: one [fonts] row; the target index is its position in Sidecar.Fonts, len(Fonts) appends, values are
	// "family<sep>file" as one interned string (fontRowValue), and an empty after removes the row.
	opFontFamily
	// opFontBind: one [fontbind] row, the class's append-only theme.FontElements index bound to a family; "" means
	// none.
	// This is what renders: an element's own font and size are carried but not drawn in format 1.
	opFontBind
	// opMediaRow: one [media] row; same index and empty-after rules as opFontFamily, with the row as one string
	// (mediaRowValue).
	// Always grouped with the element that draws it, or undo would leave an unseen image counting against the art
	// budget.
	// INIDoc can't remove a key, so a removed row stays in the file until the next full rewrite; the reader accepts it.
	opMediaRow
	// editOpKindCount sizes the tables; never a real kind.
	editOpKindCount
)

var editOpKindNames = [editOpKindCount]string{
	"none", "element field", "element add", "element delete", "widget rect", "widget reset",
	"font family", "font binding", "image row",
}

func (k editOpKind) String() string {
	if int(k) >= len(editOpKindNames) {
		return "?"
	}
	return editOpKindNames[k]
}

// themeEditOp is one undoable mutation; ops with the same nonzero group undo together, 0 means alone.
type themeEditOp struct {
	kind   editOpKind
	target editTarget
	field  editField
	before editValue
	after  editValue
	group  uint32
}

// coalesceKey is what two ops must share to merge. The target is essential: without it, a nudge burst across a
// Tab-cycled selection would merge and undo would write one element's old value onto another.
type coalesceKey struct {
	kind   editOpKind
	target editTarget
	field  editField
	group  uint32
}

func (op themeEditOp) coalesceKey() coalesceKey {
	return coalesceKey{kind: op.kind, target: op.target, field: op.field, group: op.group}
}

// themeEditRing is the single timeline: n ops held, applied of them in effect, so redo is applied < n.
// A push after an undo drops the redo tail, so redo can't replay against a document that has diverged.
type themeEditRing struct {
	ops     [themeEditUndoCap]themeEditOp
	n       int
	applied int
	// group is the group id currently being accumulated; 0 = ops stand alone.
	group uint32
	// nextGroup only increases within a session, so a dropped redo tail can't leave an id a later group collides with.
	nextGroup uint32
	// lastAt / lastKey drive coalescing; the caller supplies the time so the ring has no clock of its own.
	lastAt  time.Time
	lastKey coalesceKey
}

// begin opens a group undone together until end; a second begin keeps the first group, so a helper can't split a
// caller's.
func (r *themeEditRing) begin() uint32 {
	if r.group != 0 {
		return r.group
	}
	r.nextGroup++
	r.group = r.nextGroup
	return r.group
}

// end closes the open group.
func (r *themeEditRing) end() { r.group = 0 }

// push records an applied op, returning false if it merged into its predecessor or was never a mutation.
// The full-ring case shifts the array with copy; a growing slice would allocate on a drag.
func (r *themeEditRing) push(op themeEditOp, now time.Time) bool {
	if op.kind == opNone {
		return false
	}
	op.group = r.group
	// A new mutation invalidates the redo tail.
	r.n = r.applied
	if r.n > 0 && !now.Before(r.lastAt) && now.Sub(r.lastAt) < editCoalesceWindow &&
		r.lastKey == op.coalesceKey() && coalescableKind(op.kind) {
		// Keep the original before and take the newest after, so a hundred-frame drag is one Ctrl+Z.
		r.ops[r.n-1].after = op.after
		r.lastAt = now
		return false
	}
	if r.n == len(r.ops) {
		copy(r.ops[:], r.ops[1:])
		r.n--
	}
	r.ops[r.n] = op
	r.n++
	r.applied = r.n
	r.lastAt = now
	r.lastKey = op.coalesceKey()
	return true
}

// coalescableKind reports whether adjacent ops may merge: only value edits, which a drag rewrites each frame; never
// structural ops.
func coalescableKind(k editOpKind) bool { return k == opElemField || k == opSlotRect }

// canUndo / canRedo drive the header band's two buttons.
func (r *themeEditRing) canUndo() bool { return r.applied > 0 }
func (r *themeEditRing) canRedo() bool { return r.applied < r.n }

// takeUndo returns the next op to invert, newest first, leaving the document to the caller.
func (r *themeEditRing) takeUndo() (themeEditOp, bool) {
	if r.applied == 0 {
		return themeEditOp{}, false
	}
	r.applied--
	// A coalesce partner must not survive an undo: the next push starts a fresh op even inside the window.
	r.lastKey = coalesceKey{}
	return r.ops[r.applied], true
}

// takeRedo is takeUndo's mirror.
func (r *themeEditRing) takeRedo() (themeEditOp, bool) {
	if r.applied >= r.n {
		return themeEditOp{}, false
	}
	op := r.ops[r.applied]
	r.applied++
	r.lastKey = coalesceKey{}
	return op, true
}

// peekUndoGroup / peekRedoGroup report the next step's group, so one Ctrl+Z can drain a grouped edit.
func (r *themeEditRing) peekUndoGroup() (uint32, bool) {
	if r.applied == 0 {
		return 0, false
	}
	return r.ops[r.applied-1].group, true
}

func (r *themeEditRing) peekRedoGroup() (uint32, bool) {
	if r.applied >= r.n {
		return 0, false
	}
	return r.ops[r.applied].group, true
}
