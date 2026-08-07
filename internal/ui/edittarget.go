package ui

// The EDIT TARGET — one representation of "the thing being dragged", shared by both
// overlay editors (v1.90.0 W6, docs/wip/THEME-EDITOR-DESIGN.md §W6).
//
// Two editors grew up beside each other with two private answers to the same
// question. The classic slot editor tracked a screen-space slot name
// (classicEditKey); the themed editor tracked a design-space rect key (editKey).
// Neither could say what W7's theme editor has to say every frame — "the thing under
// the cursor is FREE ELEMENT 12" — because an element has no key at all: its identity
// is its INDEX in the sidecar (theme.Element's own doc comment says so, and the whole
// AO2-parity-by-construction argument in design §Q1 rests on it).
//
// THE SPACE IS A FIELD, NEVER A DEDUCTION. The obvious cheap shape — one struct with
// a key AND an index, where "the index is set" means "it is an element" — is the bug
// the design doc is emphatic about, and it is not hypothetical: the two payloads have
// legitimate zero values (an element at index 0; the empty key that both editors use
// for "nothing selected"), so every reader would be guessing, and readers guess
// differently. Here the space is a separate field with no valid zero value, every
// constructor demands it, and the payload accessors answer only for their own space:
// designKey() on an element target is "", elemIdx() on a slot target reports !ok. A
// target therefore cannot be misread even by a caller that never checks.
//
// BEHAVIOUR-NEUTRAL BY CONSTRUCTION. W6 ships no new UI, so nothing constructs an
// element target outside the tests yet; the classic and design arms carry exactly the
// keys their private string fields carried before. What changes is that there is now
// ONE place to add the third case, and W7 adds it once instead of in each editor.

import "github.com/veandco/go-sdl2/sdl"

// editSpace names WHICH editing model a target belongs to — the coordinate system its
// geometry lives in, the store its edits persist to, and the rules its resize obeys.
//
// The zero value is deliberately NOT a real space. "Nothing is selected" is a state
// both editors spend most of their time in, and it has to be distinguishable from
// every payload space without looking at the payload.
type editSpace uint8

const (
	// spaceNone: no selection. The zero value, and the only one whose payload is
	// meaningless.
	spaceNone editSpace = iota
	// spaceClassic: a classic-layout slot (classiclayout.go). SCREEN px, persisted as
	// a window fraction in config.ClassicLayout.
	spaceClassic
	// spaceDesign: an AO2 courtroom_design.ini rect key (themeslots.go). DESIGN px,
	// persisted per theme in prefs.ThemeRectOverrides. The server-tab strip
	// (themeTabBarKey) is the one external:true key in this space and runs its whole
	// gesture in screen px with derived W/H — every arm below that touches design
	// geometry has to keep letting it.
	spaceDesign
	// spaceElement: a FREE ELEMENT, identified by its index in Sidecar.Elements
	// (never by its authoring id — theme.Element). Its rect is design px in the
	// element's own declared Space, and under design §6.6 R1 it is a signed DELTA on
	// its anchor when it has one.
	spaceElement
	// spaceFont: ONE ROW OF THE THEME'S TYPE TABLES (v1.90.0 W7b) — a [fonts] family
	// or a [fontbind] class binding, identified by its INDEX in that table, exactly as
	// an element is identified by its index in Sidecar.Elements.
	//
	// IT CARRIES NO GEOMETRY, and that is a real widening of what a space is, made
	// deliberately rather than by drift. The alternative was a second, parallel
	// "editable thing" type with its own selection array, its own inspector dispatch
	// and its own undo plumbing — i.e. two answers to "what is selected" living beside
	// each other, which is precisely the duplication W6 collapsed this type out of.
	// resizeEdgesFor answers 0 for it (a font row has no edges to drag), which is the
	// same honest answer it gives the server-tab strip, and the payload accessors
	// below refuse it exactly as they refuse every other foreign space. WHICH table
	// the index addresses is the OP KIND's business (opFontFamily / opFontBind), never
	// the target's: the two tables are edited by two different ops and a target that
	// tried to say which one would be a second, weaker copy of that distinction.
	spaceFont
)

// elemTargetNone is the "no element" index. -1 rather than 0 because 0 is a
// legitimate element (identity is the index), which is precisely why the space field
// exists at all.
const elemTargetNone = -1

// editTarget is one thing being dragged, nudged or resized.
//
// A VALUE, not a handle: it is copied freely, compared with ==, and holds no pointer
// into the layout cache — so a target that outlives a rebuild (a selection surviving a
// window resize) is stale in the harmless way rather than the dangling way.
type editTarget struct {
	// space is mandatory. See the file header for why it is not derived.
	space editSpace
	// key is the slot / design key for spaceClassic and spaceDesign; "" otherwise.
	key string
	// elem is the sidecar element index for spaceElement; elemTargetNone otherwise.
	elem int
}

// newEditTarget is the ONE constructor. It PANICS on a target that does not say what
// it is, or that carries the other space's payload.
//
// A panic rather than a silent zero value, and at the construction site rather than at
// the first confused read: this runs on the render thread, so the only acceptable
// outcomes are "a correct target" and "a stack trace naming the line that built the
// wrong one". Every input here is a compile-time-known space plus a local payload, so
// a panic is unreachable for correct code — it is an assertion, not error handling.
//
// TestEditTargetSpaceIsExplicitNotInferred pins every refusal below, and
// TestEditTargetIsOnlyBuiltByItsConstructors pins that no other file sidesteps them
// with a composite literal.
func newEditTarget(space editSpace, key string, elem int) editTarget {
	switch space {
	case spaceClassic, spaceDesign:
		if key == "" {
			panic("ui: editTarget in a key space needs a key (use noTarget for an empty selection)")
		}
		if elem != elemTargetNone {
			panic("ui: editTarget in a key space must not carry an element index")
		}
	case spaceElement, spaceFont:
		if key != "" {
			panic("ui: an index editTarget has no key — identity is the index")
		}
		if elem < 0 {
			panic("ui: an index editTarget needs a non-negative index")
		}
	default:
		panic("ui: editTarget needs an explicit space — it is never inferred from the payload")
	}
	return editTarget{space: space, key: key, elem: elem}
}

// noTarget is the empty selection.
//
// It is EXACTLY the zero value, which is what lets an App field that nobody has
// assigned yet mean "nothing selected" without an initializer — and what lets a
// cleared selection compare equal to a fresh one. It exists as a function so no caller
// outside this file has to write a composite literal, which is what
// TestEditTargetIsOnlyBuiltByItsConstructors forbids.
func noTarget() editTarget { return editTarget{} }

// classicTarget / designTarget / elementTarget are the three named constructors. They
// exist so a call site READS as the space it means; each is one line over
// newEditTarget and inlines to nothing.
func classicTarget(key string) editTarget { return newEditTarget(spaceClassic, key, elemTargetNone) }
func designTarget(key string) editTarget  { return newEditTarget(spaceDesign, key, elemTargetNone) }
func elementTarget(idx int) editTarget    { return newEditTarget(spaceElement, "", idx) }

// fontTarget is W7b's one addition: a row of the theme's type tables, by index.
func fontTarget(idx int) editTarget { return newEditTarget(spaceFont, "", idx) }

// armed reports that something is selected. The zero target is the only unarmed one.
func (t editTarget) armed() bool { return t.space != spaceNone }

// classicKey / designKey answer ONLY for their own space, so a caller that forgets to
// check the space still cannot mistake one for the other. "" is the honest answer for
// every other space, and it is the same "" both editors already treat as "nothing".
func (t editTarget) classicKey() string {
	if t.space == spaceClassic {
		return t.key
	}
	return ""
}

func (t editTarget) designKey() string {
	if t.space == spaceDesign {
		return t.key
	}
	return ""
}

// elemIdx is the element index, with ok=false for every non-element target. Two
// results rather than a sentinel because the sentinel is what the caller would have to
// remember to compare against — the bug class this whole type exists to close.
func (t editTarget) elemIdx() (int, bool) {
	if t.space == spaceElement {
		return t.elem, true
	}
	return elemTargetNone, false
}

// fontIdx is the type-table row index, with ok=false for every non-font target.
// WHICH table is the op kind's business — see spaceFont.
func (t editTarget) fontIdx() (int, bool) {
	if t.space == spaceFont {
		return t.elem, true
	}
	return elemTargetNone, false
}

// ---------------------------------------------------------------------------
// Resize handles, generalized across the spaces
// ---------------------------------------------------------------------------

// resizeEdgesFor is the ONE entry point for "which sides of this thing may a drag
// move" — the generalization of slotResizeEdges (classiclayout.go:396) across the
// three spaces. Each space keeps its own rule, and the rules stay where their reasons
// are: slotResizeEdges knows why the control block is width-only, themedKeyResizable
// knows why the server-tab strip has no authored size at all.
//
// The two must agree about the SAME widget where both can see it — the strip is
// move-only in both (slotResizeEdges(slotTabBar) == 0, themedKeyResizable
// (themeTabBarKey) == false), and TestTabBarSlotIsMoveOnlyAndRegisters pins one half.
func resizeEdgesFor(t editTarget) uint8 {
	switch t.space {
	case spaceClassic:
		return slotResizeEdges(t.key)
	case spaceDesign:
		return themedResizeEdges(t.key)
	case spaceElement:
		// A free element carries an authored W and H in its own rect, so every side
		// is real. Nothing derives an element's size from its content the way the
		// control block and the tab strip derive theirs.
		return edgeL | edgeR | edgeT | edgeB
	case spaceFont:
		// NO EDGE TO DRAG, said here rather than left to the default so that adding a
		// space is a decision taken at this table: a [fonts] or [fontbind] row has no
		// rect at all, which is the same honest 0 the server-tab strip gets for having
		// no authored size.
		return 0
	}
	return 0
}

// themedResizeEdges is the DESIGN-space twin of slotResizeEdges: all four sides for a
// theme's own widget, none for the server-tab strip.
//
// Derived from themedKeyResizable rather than restating it, so the strip's exemption
// (and the argument behind it) lives in exactly one place.
func themedResizeEdges(key string) uint8 {
	if !themedKeyResizable(key) {
		return 0
	}
	return edgeL | edgeR | edgeT | edgeB
}

// handleCount is the number of grips classicHandles returns (4 corners + 4 edge
// midpoints). Named because handleGripMask packs one BIT per handle into a uint8 and
// that packing is only sound while the two numbers agree.
const handleCount = 8

// handleBottomRight is classicHandles' bottom-right index — the corner grip the themed
// editor has always had, and the one handle a box is never too small to offer (see
// handleGripMask).
const handleBottomRight = 3

// editHandleRoomPx is the smallest box that is offered its FULL handle set, per axis.
//
// The grips are layoutHandlePx squares laid ON the box, so a small box is entirely
// covered by its own handles and becomes impossible to MOVE. Five handle widths is the
// smallest span that fits two corner grips, the midpoint grip between them, and a
// grabbable gap on each side of that midpoint — i.e. the smallest box that can offer
// the full set and still be picked up by its own interior.
//
// The classic editor does not need this rule and does not use it: its probe is a
// margin BAND around every edge (classicEdgeAt), which covers a small slot completely
// — and the escape hatch it grew for that is Alt-forces-a-move, documented at its
// press site. The themed editor has no Alt arm, so its grips have to leave the
// interior alone instead.
const editHandleRoomPx = 5 * layoutHandlePx

// handleGripMask is the set of classicHandles indices a box at r offers, as a bitmask
// (bit i = handle i), given the edges its target honours.
//
// TWO FILTERS, and the second is the one that keeps this refactor honest:
//
//  1. a handle whose mask includes an edge the target does not honour is dropped —
//     verbatim drawSlotHandles' rule ("the affordance never lies");
//  2. a box with less than editHandleRoomPx on either axis keeps ONLY its bottom-right
//     corner. That is exactly the single grip the themed editor offered before this
//     wave, so no small widget loses anything, and none of them becomes unmovable —
//     which is what 8 grips on a 24-px-tall AO2 button would have meant.
//
// THE ORDER OF THE TWO FILTERS IS LOAD-BEARING, and the trap is worth naming because
// the "obvious" reorder is wrong in a way nothing on screen would show. Filter 1 runs
// FIRST, so the cramped fallback is not "keep one grip" but "keep the bottom-right
// grip IF the target honours edgeR|edgeB". A target that honours only some other pair
// — slotResizeEdges' width-only control block is the live example, edgeL|edgeR —
// therefore gets NO handle at all once it is cramped, which is correct: every handle
// it could offer grips an edge it does not honour, and "the affordance never lies"
// outranks "offer something". Reordering (fallback first, then filter) would offer the
// bottom-right corner on a width-only widget and a drag on it would silently move an
// edge the target refuses. No caller reaches that combination today — the two design
// spaces answer all-four-or-none and elements answer all four — so this is a guard
// against the next caller, and TestCrampedBoxOffersNoLyingHandle pins it.
func handleGripMask(r sdl.Rect, allowed uint8) uint8 {
	if allowed == 0 {
		return 0
	}
	cramped := r.W < editHandleRoomPx || r.H < editHandleRoomPx
	var m uint8
	for i, e := range handleEdgeMask {
		if e&^allowed != 0 {
			continue // grips an edge this target does not honour
		}
		if cramped && i != handleBottomRight {
			continue
		}
		m |= 1 << uint(i)
	}
	return m
}

// handleGripAt reports which edges a press at (mx,my) grips on a box at r, or 0 for
// "not on a handle" (= the caller moves it instead).
//
// It hit-tests the handle SQUARES — the very rects the editor paints — rather than
// classicEdgeAt's margin band. That is deliberate and it is what makes the 8-handle
// wiring a strict superset of the single corner grip it replaces: handle
// handleBottomRight is bit-identical to the themed editor's old grip rect and carries
// the same edge mask, so a press on it behaves exactly as it always did.
//
// Corners are probed before midpoints (classicHandles' own order), so an overlap on a
// box near the room floor resolves to the corner.
func handleGripAt(r sdl.Rect, allowed uint8, mx, my int32) uint8 {
	m := handleGripMask(r, allowed)
	if m == 0 {
		return 0
	}
	for i, hnd := range classicHandles(r) {
		if m&(1<<uint(i)) == 0 {
			continue
		}
		if pointIn(mx, my, hnd) {
			return handleEdgeMask[i]
		}
	}
	return 0
}
