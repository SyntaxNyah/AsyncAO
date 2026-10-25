package ui

// The EDIT TARGET — one representation of "the thing being dragged", shared by both
// overlay editors (v1.90.0 W6, docs/wip/THEME-EDITOR-DESIGN.md §W6).

import "github.com/veandco/go-sdl2/sdl"

// editSpace names WHICH editing model a target belongs to — the coordinate system its
// geometry lives in, the store its edits persist to, and the rules its resize obeys.
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
	spaceFont
	// spaceMedia: ONE ROW OF THE THEME'S [media] TABLE (v1.90.0 W10) — a declared
	// image, identified by its INDEX in Sidecar.Media, exactly as a font row is
	// identified by its index in Sidecar.Fonts.
	spaceMedia
)

// elemTargetNone is the "no element" index. -1 rather than 0 because 0 is a
// legitimate element (identity is the index), which is precisely why the space field
// exists at all.
const elemTargetNone = -1

// editTarget is one thing being dragged, nudged or resized.
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
func newEditTarget(space editSpace, key string, elem int) editTarget {
	switch space {
	case spaceClassic, spaceDesign:
		if key == "" {
			panic("ui: editTarget in a key space needs a key (use noTarget for an empty selection)")
		}
		if elem != elemTargetNone {
			panic("ui: editTarget in a key space must not carry an element index")
		}
	case spaceElement, spaceFont, spaceMedia:
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
func noTarget() editTarget { return editTarget{} }

// classicTarget / designTarget / elementTarget are the three named constructors. They
// exist so a call site READS as the space it means; each is one line over
// newEditTarget and inlines to nothing.
func classicTarget(key string) editTarget { return newEditTarget(spaceClassic, key, elemTargetNone) }
func designTarget(key string) editTarget  { return newEditTarget(spaceDesign, key, elemTargetNone) }
func elementTarget(idx int) editTarget    { return newEditTarget(spaceElement, "", idx) }

// fontTarget is W7b's one addition: a row of the theme's type tables, by index.
func fontTarget(idx int) editTarget { return newEditTarget(spaceFont, "", idx) }

// mediaTarget is W10's: a row of the theme's [media] table, by index.
func mediaTarget(idx int) editTarget { return newEditTarget(spaceMedia, "", idx) }

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

// mediaIdx is the [media] row index, with ok=false for every non-media target.
func (t editTarget) mediaIdx() (int, bool) {
	if t.space == spaceMedia {
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
	case spaceFont, spaceMedia:
		// NO EDGE TO DRAG, said here rather than left to the default so that adding a
		// space is a decision taken at this table: a [fonts], [fontbind] or [media]
		// row has no rect at all, which is the same honest 0 the server-tab strip gets
		// for having no authored size. A media row's picture DOES have a rect — it
		// belongs to the element that draws it, and that element is a spaceElement
		// target with all four edges.
		return 0
	}
	return 0
}

// themedResizeEdges is the DESIGN-space twin of slotResizeEdges: all four sides for a
// theme's own widget, none for the server-tab strip.
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
const editHandleRoomPx = 5 * layoutHandlePx

// handleGripMask is the set of classicHandles indices a box at r offers, as a bitmask
// (bit i = handle i), given the edges its target honours.
// THE ORDER OF THE TWO FILTERS IS LOAD-BEARING, and the trap is worth naming because
// the "obvious" reorder is wrong in a way nothing on screen would show. Filter 1 runs
// FIRST, so the cramped fallback is not "keep one grip" but "keep the bottom-right
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
