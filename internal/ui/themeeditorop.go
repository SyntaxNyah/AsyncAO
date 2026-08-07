package ui

// THE UNDO STACK (v1.90.0 W7a) — docs/wip/THEME-EDITOR-DESIGN.md §Q7
// ("ONE undo stack over ONE document") and §3.2's keyboard map.
//
// ONE STACK, ONE DOCUMENT, ONE MUTATION PATH. The user's model is "undo the last
// thing I did", and cross-domain mutations are the norm in an editor of this shape:
// paste-style writes colour AND font AND effect, applying a preset writes
// everything, a marquee drag writes twelve rects. Separate per-domain stacks make
// Ctrl+Z ambiguous, which is the modal misery the brief forbids in as many words.
//
// AN OP IS A VALUE — no pointers, no closures, no method values. That is not
// stylistic: a closure per op ALLOCATES, and this ring is written to on the render
// thread from a drag that fires every frame. themeslots.go:105 states the same rule
// for the shipped editors' tables. Everything an op needs to be undone is packed
// into two fixed-size editValues plus an interned string id, so
// [themeEditUndoCap]themeEditOp is a plain array on App and pushing to it cannot
// heap-allocate. Gate: TestUndoRingIsCappedAndNeverAllocatesAfterWarmup.
//
// EVERY MUTATION RETURNS ITS OWN INVERSE. themeDoc.apply reads the CURRENT value
// into op.before before writing op.after, so undo is "write before" and redo is
// "write after" — one code path, and a field physically cannot be edited without
// producing an undo entry (themeeditordoc.go carries that argument at the funnel).

import "time"

// ---------------------------------------------------------------------------
// Named caps (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// themeEditUndoCap bounds the editor's single timeline. 2x layoutUndoCap (64),
	// because one stack now carries geometry AND colour AND motion AND element
	// add/delete where the legacy editors' two carried rects alone. A fixed array:
	// past the cap the OLDEST op is dropped, never the newest — losing the thing you
	// just did is the one failure an undo stack must not have.
	themeEditUndoCap = 128

	// editStrCap bounds the per-session string interning table that keeps editValue
	// a fixed size. 256 distinct strings is far past what one editing session types
	// into media ids, shape names, anchors and label text; past it an op carrying a
	// NEW string is refused at construction (mutateElemField returns opNone) rather
	// than silently editing to the wrong string.
	editStrCap = 256

	// editCoalesceWindow makes a continuous gesture ONE undo step. A drag fires a
	// mutation every frame and a typed hex fires one per keystroke; 400 ms is long
	// enough that neither ever splits mid-gesture and short enough that two
	// deliberate edits to the same field stay two steps.
	editCoalesceWindow = 400 * time.Millisecond
)

// ---------------------------------------------------------------------------
// editValue — the fixed-size payload
// ---------------------------------------------------------------------------

// editStrID indexes the editor's interning table. 0 is ALWAYS the empty string, so
// the zero editValue means "empty" for every string field rather than "whatever
// happened to be interned first".
type editStrID int16

// editStrNone is the empty string's id, and the zero value.
const editStrNone editStrID = 0

// editValue is one field's value, in a FIXED SIZE, for every field kind.
//
// Four int32s carry a rect (x, y, w, h) or a single scalar in i[0]; four bytes
// carry an RGBA; one interned id carries a string. A union rather than an
// interface, because an interface field would box every int and allocate on a path
// that runs per frame during a drag.
//
// Which lanes a field uses is the FIELD's business, not this type's — editFieldGet
// and editFieldSet are the only two places that know, and they are each other's
// inverse by construction (TestEveryInspectorFieldIsUndoable pins the round trip).
type editValue struct {
	i [4]int32
	b [4]uint8
	s editStrID
}

// editValueInt / editValueRect / editValueRGBA / editValueStr are the named
// constructors. They exist so a call site READS as the lane it means; a bare
// composite literal that filled i[0] for a colour would compile and be silently
// wrong.
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

// ---------------------------------------------------------------------------
// themeEditOp
// ---------------------------------------------------------------------------

// editOpKind is WHAT a mutation did, which decides how the document applies it.
//
// The zero value is opNone — "not a mutation" — so a table row that forgot to fill
// its op, or a refused construction (an unknown field, a full interning table),
// pushes nothing instead of pushing a mutation with an arbitrary meaning.
type editOpKind uint8

const (
	// opNone: not a mutation. Never pushed; see themeEditRing.push.
	opNone editOpKind = iota
	// opElemField: one field of one free element, named by editField.
	opElemField
	// opElemAdd: an element appended at index target.elem. before is empty, after
	// names the graveyard slot holding the element's body.
	opElemAdd
	// opElemDel: the element at index target.elem removed. before names the
	// graveyard slot holding its body so undo can resurrect it byte-for-byte.
	opElemDel
	// opSlotRect: one AO2 widget's geometry, i.e. one [overrides] row. The target
	// is a DESIGN target, so the key rides in the target itself and needs no
	// interning; before/after carry the rect in the int lanes plus the
	// editSlotRowPresent flag (themeeditordoc.go), which is what lets an undo
	// DELETE a row the edit created instead of leaving an override behind that says
	// exactly what the AO2 tier already said.
	opSlotRect
	// editOpKindCount sizes the census tables; never a real kind.
	editOpKindCount
)

var editOpKindNames = [editOpKindCount]string{
	"none", "element field", "element add", "element delete", "widget rect",
}

func (k editOpKind) String() string {
	if int(k) >= len(editOpKindNames) {
		return "?"
	}
	return editOpKindNames[k]
}

// themeEditOp is one undoable mutation.
//
// group ties ops that must undo TOGETHER: a preset apply writes everything and is
// one Ctrl+Z, a duplicate is an add plus a selection change, a multi-drag is one
// delta over N elements. 0 means "alone".
type themeEditOp struct {
	kind   editOpKind
	target editTarget
	field  editField
	before editValue
	after  editValue
	group  uint32
}

// coalesceKey is the identity two ops must share to merge into one undo step: the
// same kind, on the same target, in the same field, inside the same group.
//
// The TARGET is part of it and that is the load-bearing half: without it, dragging
// element 3 and then element 7 inside the same 400 ms would merge into one op whose
// `before` belonged to element 3 and whose `after` belonged to element 7 — an undo
// that writes one element's old value onto another. A drag cannot produce that, a
// nudge burst across a Tab-cycled selection can.
type coalesceKey struct {
	kind   editOpKind
	target editTarget
	field  editField
	group  uint32
}

func (op themeEditOp) coalesceKey() coalesceKey {
	return coalesceKey{kind: op.kind, target: op.target, field: op.field, group: op.group}
}

// ---------------------------------------------------------------------------
// The ring
// ---------------------------------------------------------------------------

// themeEditRing is the editor's single timeline.
//
// A FIXED ARRAY WITH TWO CURSORS, not a slice: `n` is how many ops the timeline
// holds and `applied` is how many of them are currently in effect, so redo is
// simply applied < n. A new push after an undo TRUNCATES the redo tail (n =
// applied), which is what every editor of this shape does and what stops a redo
// from replaying a mutation against a document that has since diverged.
type themeEditRing struct {
	ops     [themeEditUndoCap]themeEditOp
	n       int
	applied int
	// group is the group id currently being accumulated; 0 = ops stand alone.
	group uint32
	// nextGroup hands out group ids. It only ever increases within a session, so a
	// truncated redo tail can never leave a stale id that a later group collides
	// with (which would make two unrelated edits undo together).
	nextGroup uint32
	// lastAt / lastKey drive coalescing. lastAt is a wall-clock stamp taken by the
	// caller (the draw site already has one) rather than read here, so the ring has
	// no clock of its own and a test can drive a whole gesture deterministically.
	lastAt  time.Time
	lastKey coalesceKey
}

// begin opens a group: every op pushed until end() undoes together. Nested calls
// are NOT supported and do not silently nest — a second begin while one is open
// keeps the first group, so a helper that groups internally cannot split a caller's
// larger group in half.
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

// push records an applied op. Returns false for an op that was merged into its
// predecessor or was never a mutation, so a caller can tell "nothing new happened".
//
// ZERO ALLOCATION. The full-ring case shifts the array down by one with copy(),
// which moves 127 values on the stack-free array that already lives on App. Growing
// a slice instead would allocate on a drag.
func (r *themeEditRing) push(op themeEditOp, now time.Time) bool {
	if op.kind == opNone {
		return false
	}
	op.group = r.group
	// A new mutation invalidates the redo tail.
	r.n = r.applied
	if r.n > 0 && !now.Before(r.lastAt) && now.Sub(r.lastAt) < editCoalesceWindow &&
		r.lastKey == op.coalesceKey() && coalescableKind(op.kind) {
		// MERGE: keep the ORIGINAL before (the state the gesture started from) and take
		// the newest after. That is what makes a hundred-frame drag one Ctrl+Z.
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

// coalescableKind reports whether two adjacent ops of this kind may merge.
//
// STRUCTURAL ops never merge, and the reason is that merging them is meaningless
// rather than merely unwanted: two adds are two different elements, and an add
// followed by a delete inside the window is two facts about the document, not one
// value that moved twice. Only a VALUE carries a "same thing, new value" reading —
// an element field, or an AO2 widget's rect, both of which a drag rewrites every
// frame for as long as the button is down.
func coalescableKind(k editOpKind) bool { return k == opElemField || k == opSlotRect }

// canUndo / canRedo drive the header band's two buttons.
func (r *themeEditRing) canUndo() bool { return r.applied > 0 }
func (r *themeEditRing) canRedo() bool { return r.applied < r.n }

// takeUndo pops the next op to invert, newest first, and reports whether the caller
// should keep going for the same group. Returning the op rather than performing it
// keeps this type free of any knowledge of the document — the ring is a timeline,
// not an editor.
func (r *themeEditRing) takeUndo() (themeEditOp, bool) {
	if r.applied == 0 {
		return themeEditOp{}, false
	}
	r.applied--
	// A coalesce partner must never survive an undo: after stepping back, the next
	// push starts a fresh op even if it lands inside the window.
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

// peekUndoGroup / peekRedoGroup report the group of the op the next step would
// take, so the caller can drain a whole grouped edit in one Ctrl+Z.
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

// (There is no reset(). The ring is born with the editor and dies with it — the
// editor is one allocation whose lifetime IS the document's, so "empty the timeline
// because the theme changed" is spelled `a.te = nil`. A reset method would be a
// second way to reach a state that already has one, and the two would drift.)
