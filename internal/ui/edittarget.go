package ui

// editTarget is one representation of the thing being dragged, shared by the overlay editors.
// The space is a separate field, never deduced from the payload: index 0 and the empty key are both legitimate,
// so a reader could not tell them apart. Payload accessors answer only for their own space.

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// editSpace names a target's coordinate system, store and resize rules; the zero value means nothing is selected.
type editSpace uint8

const (
	// spaceNone: no selection; the only space whose payload is meaningless.
	spaceNone editSpace = iota
	// spaceClassic: a classic-layout slot. Screen px, persisted as a window fraction in config.ClassicLayout.
	spaceClassic
	// spaceDesign: an AO2 courtroom_design.ini rect key. Design px, persisted in prefs.ThemeRectOverrides.
	// The server-tab strip (themeTabBarKey) is its one external:true key, using screen px with derived W/H.
	spaceDesign
	// spaceElement: a free element by index in Sidecar.Elements, never its id. Design px in the element's own Space,
	// and a signed delta on its anchor when it has one.
	spaceElement
	// spaceFont: a row of a [fonts] or [fontbind] table, by index. It has no geometry. Which table is the op kind's
	// business (opFontFamily, opFontBind): a target saying so would be a second, weaker copy of that distinction.
	spaceFont
	// spaceMedia: a row of the [media] table in Sidecar.Media, by index. Separate from spaceFont because an index
	// meaning either table could not be validated without its op. No geometry: the rect belongs to the drawing element.
	spaceMedia
)

// elemTargetNone is the "no element" index; -1 because 0 is a legitimate element.
const elemTargetNone = -1

// editTarget is a value holding no pointer into the layout cache, so one that outlives a rebuild is harmlessly stale.
type editTarget struct {
	// space is mandatory.
	space editSpace
	// key is the design key for spaceClassic and spaceDesign; "" otherwise.
	key string
	// elem is the sidecar element index for spaceElement; elemTargetNone otherwise.
	elem int
}

// newEditTarget panics on a target that does not say what it is or carries another space's payload.
// An assertion, unreachable for correct code: on the render thread the wrong target must name the line that built it.
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

// noTarget is the empty selection, exactly the zero value, so an unset App field means nothing selected.
func noTarget() editTarget { return editTarget{} }

// classicTarget, designTarget and elementTarget are named constructors over newEditTarget.
func classicTarget(key string) editTarget { return newEditTarget(spaceClassic, key, elemTargetNone) }
func designTarget(key string) editTarget  { return newEditTarget(spaceDesign, key, elemTargetNone) }
func elementTarget(idx int) editTarget    { return newEditTarget(spaceElement, "", idx) }

// fontTarget is a row of the theme's type tables, by index.
func fontTarget(idx int) editTarget { return newEditTarget(spaceFont, "", idx) }

// mediaTarget is a row of the theme's [media] table, by index.
func mediaTarget(idx int) editTarget { return newEditTarget(spaceMedia, "", idx) }

func (t editTarget) armed() bool { return t.space != spaceNone }

// classicKey and designKey answer only for their own space, and "" otherwise.
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

// elemIdx is the element index, with ok=false for every non-element target.
func (t editTarget) elemIdx() (int, bool) {
	if t.space == spaceElement {
		return t.elem, true
	}
	return elemTargetNone, false
}

// fontIdx is the type-table row index, with ok=false for every non-font target.
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

// resizeEdgesFor reports which sides of a target a drag may move, delegating to each space's own rule.
func resizeEdgesFor(t editTarget) uint8 {
	switch t.space {
	case spaceClassic:
		return slotResizeEdges(t.key)
	case spaceDesign:
		return themedResizeEdges(t.key)
	case spaceElement:
		// A free element has an authored W and H, so every side is real.
		return edgeL | edgeR | edgeT | edgeB
	case spaceFont, spaceMedia:
		// No edge to drag: a font or media row has no rect. A media row's picture belongs to its spaceElement.
		return 0
	}
	return 0
}

// themedResizeEdges gives all four sides for a theme's widget, none for the server-tab strip (themedKeyResizable).
func themedResizeEdges(key string) uint8 {
	if !themedKeyResizable(key) {
		return 0
	}
	return edgeL | edgeR | edgeT | edgeB
}

// handleCount is classicHandles' grip count; handleGripMask packs one bit per handle into a uint8, so they must agree.
const handleCount = 8

// handleBottomRight is classicHandles' bottom-right index, the one handle a box is never too small to offer.
const handleBottomRight = 3

// editHandleRoomPx is the smallest box offered every handle, per axis: grips are laid on the box, so a small box is
// covered by them and cannot be moved. Five widths fit two corners, a midpoint and a gap each side.
const editHandleRoomPx = 5 * layoutHandlePx

// handleGripMask is the bitmask of classicHandles indices a box at r offers: drop handles gripping a disallowed edge,
// then a box under editHandleRoomPx keeps only its bottom-right corner, so it stays movable.
// The order matters: reversed, a cramped width-only widget offers a corner that moves an edge it refuses.
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

// handleGripAt reports which edges a press grips on a box at r, or 0 for a move; it tests the painted handle squares.
// Corners are probed first, so an overlap near the room floor resolves to the corner.
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
