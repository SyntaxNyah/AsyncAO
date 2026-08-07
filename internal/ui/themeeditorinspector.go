package ui

// THE PROPERTY INSPECTOR'S TABLE (v1.90.0 W7a) — docs/wip/THEME-EDITOR-DESIGN.md
// §Q7 ("Declarative property inspector") and §3.1's right rail.
//
// DECLARATIVE, NOT HAND-WRITTEN PER KIND. A per-kind draw function is how an
// inspector rots: the eleventh field added in v1.9x is the one somebody forgets to
// wire into undo, and nothing fails. Here a row is DATA — a label, a widget kind, a
// getter and a mutator — and the mutator RETURNS AN OP rather than performing one,
// so a field physically cannot bypass the undo stack. Adding a row to this table is
// the whole of adding an inspector field, and it is undoable the moment it exists.
//
// TWO GATES HOLD THE SHAPE:
//   - TestEveryElementKindHasInspectorFields — totality over theme.ElementKind, so a
//     kind appended to the format cannot ship with an empty inspector.
//   - TestEveryInspectorFieldIsUndoable — STRUCTURAL: every `mut` in this file names
//     a top-level func whose result type is themeEditOp, and every row's mutation
//     round-trips through the document with the serialised bytes hash-identical
//     before and after the undo.
//
// TOP-LEVEL FUNCS, NEVER METHOD VALUES OR CLOSURES OVER STATE. A method value
// (a.something) allocates a receiver box, and a closure over a field allocates per
// row; themeslots.go:105 already states the rule for the shipped editors' tables.
//
// ONE DOCUMENTED DEVIATION from the design's literal signature. The design writes
// `get func(*App, editTarget) editValue` / `mut func(editTarget, editValue)
// themeEditOp`, which cannot know WHICH field it is reading — so it implies one
// top-level func pair per field, i.e. ~64 near-identical three-line funcs whose only
// difference is an enum. The field is passed as an argument instead. The property
// the design's ban was protecting (no closures, no method values, no per-row
// allocation) is strictly stronger this way: there are exactly FOUR function values
// in the whole table, all package-level, all shared by every row.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// fieldKind is which WIDGET a row draws. It decides the control, never the storage
// — two rows of different widget kinds can address the same editField (a colour
// swatch and a hex entry both write editFieldFill).
type fieldKind uint8

const (
	fkRect    fieldKind = iota // four spinners sharing the drag path's own clamp
	fkInt                      // one spinner
	fkPercent                  // a 0..100 slider
	fkAngle                    // the 360/256 angle byte
	fkColor                    // swatch + wheel + hex
	fkEnum                     // a dropdown over the field's own wire names
	fkText                     // a free-text field
	fkBool                     // a checkbox
	fkMedia                    // a [media] id picker with a thumbnail strip
	fkFont                     // a [fonts] family picker
	// fkPick is a free-text value with a LIVE list of legal answers beside it
	// (v1.90.0 W7b): the anchor keys this theme actually places, the generators this
	// build rasterises, the silhouettes it draws, the faces it can resolve.
	//
	// IT IS NOT fkEnum, and the difference is the format's. An enum row's value IS its
	// index — the wire names are the whole domain and a value outside them cannot be
	// written. Every fkPick field is read as FREE TEXT by the reader (sidecar_read.go
	// says so for `shape` and `generator` in as many words), because a name this build
	// does not know must degrade rather than fail: a theme authored against a later
	// build keeps its `generator = ripples` line and draws a flat fill here. So the
	// picker OFFERS the live list and still lets a name be typed, and the row's `enum`
	// stays nil because the list is not the format's — it is this build's.
	fkPick
	fieldKindCount
)

// pickSource names WHICH live list an fkPick row offers. A small enum on the row
// rather than a func field: a per-row closure over the App is exactly the
// allocation the table's header bans, and there are only four lists.
type pickSource uint8

const (
	pickNone pickSource = iota
	// pickAnchor: the design keys this theme actually places, plus its [pane.*] ids —
	// theme.Element.Anchor's own documented domain ("a themeSlots key (or a pane id
	// when Space == SpacePane)").
	pickAnchor
	// pickGenerator: internal/ui's own registry (GeneratorNames), which is the ONE
	// list of names that rasterise. genLookup's doc comment already names this picker
	// as the registry's second spec producer.
	pickGenerator
	// pickShape: elemShapeNames, the shipped silhouette vocabulary.
	pickShape
	// pickFace: the class ladder (theme.FontElements) plus every family this theme
	// declares in [fonts] — which is exactly the two things theme.Element.Font may
	// name, quoted from its own comment: "a FontElements id (inherit that element's
	// face) or a [fonts] family".
	//
	// pickFACE, not pickFont, and that is not a preference: `pickFont` is already a
	// top-level FUNCTION in this package (ui.go — the per-run script-coverage picker),
	// so the two cannot coexist and the wave did not compile until this was renamed.
	// "Face" is the word this client already uses for what a family resolves to
	// (internFace, the fonts panel's own copy).
	pickFace
	pickSourceCount
)

// inspectorSection groups rows under one heading in the rail.
type inspectorSection uint8

const (
	secTransform inspectorSection = iota
	secFill
	secText
	secMotion
	secBehaviour
	inspectorSectionCount
)

var inspectorSectionNames = [inspectorSectionCount]string{
	"Transform", "Fill", "Text", "Motion", "Behaviour",
}

// inspectorField is ONE row.
type inspectorField struct {
	label   string
	section inspectorSection
	kind    fieldKind
	field   editField
	// get reads the CURRENT value for the rail to draw. Top-level func (see the
	// file header), never a method value.
	get func(*App, editTarget, editField) editValue
	// mut RETURNS the op that would make the change. It does not perform it — the
	// document's apply is the only writer, and a row that could write directly would
	// be a row that could forget the undo entry.
	mut func(editTarget, editField, editValue) themeEditOp
	// lo / hi bound a numeric row. Advisory for the WIDGET; the document's setter
	// clamps to the FORMAT's range regardless, so a wrong bound here is a cosmetic
	// bug rather than an unsaveable document.
	lo, hi int32
	// enum is the wire-name list an fkEnum row offers. It is the format's own name
	// table, never a second copy: a renamed enum value would be a format change.
	enum []string
	// pick names the LIVE list an fkPick row offers beside its text field. pickNone
	// for every other widget kind.
	pick pickSource
}

// inspectElemField is the ONE getter. See the file header for why the field is an
// argument.
func inspectElemField(a *App, t editTarget, f editField) editValue {
	if a == nil || a.te == nil {
		return editValue{}
	}
	v, _ := a.te.doc.read(t, f)
	return v
}

// mutateElemField is the ONE mutator. It returns an op; it writes nothing.
//
// A target that is not an element yields opNone, which themeEditRing.push drops —
// so a row wired to the wrong space is inert rather than corrupting.
func mutateElemField(t editTarget, f editField, v editValue) themeEditOp {
	if t.space != spaceElement || f == editFieldNone || int(f) >= int(editFieldCount) {
		return themeEditOp{}
	}
	return themeEditOp{kind: opElemField, target: t, field: f, after: v}
}

// mutateSlotRect is the mutator for an AO2 WIDGET's geometry — the [overrides] tier.
//
// It lives beside mutateElemField because they are the same kind of thing (a
// top-level func that returns an op and writes nothing) and because W7b's inspector
// gains a Transform section for slot selections that will wire this exact function:
// "for a fixed SLOT selection the inspector shows Transform + Reset-to-theme only"
// is the AO2-parity rule made visible, and it needs one undoable geometry mutator,
// not a second one that happens to agree with the canvas drag's.
//
// The field is editFieldRect so the coalescing key reads honestly — a widget drag
// and an element drag are the same gesture on the same kind of value.
func mutateSlotRect(t editTarget, r theme.Rect) themeEditOp {
	if t.space != spaceDesign {
		return themeEditOp{}
	}
	return themeEditOp{kind: opSlotRect, target: t, field: editFieldRect, after: editValueSlotRect(r, true)}
}

// ---------------------------------------------------------------------------
// The table
// ---------------------------------------------------------------------------

// inspectorFields is indexed by theme.ElementKind. A fixed array of slices: the
// slices are built once at package init and never mutated, so the rail reads them
// with no allocation and no map probe.
var inspectorFields = buildInspectorFields()

// buildInspectorFields assembles the per-kind rows out of the shared sections.
//
// Assembled rather than written out six times because the SHARED sections really are
// shared: every kind has a transform, a motion block and a behaviour block, and six
// hand-copied transcriptions of them is six places for the seventh to be forgotten.
// Only the FILL section differs per kind, which is the format's own shape (Element's
// comment: "exactly one is meaningful per Kind").
func buildInspectorFields() [theme.ElementKindCount][]inspectorField {
	var out [theme.ElementKindCount][]inspectorField
	for k := theme.ElementKind(0); k < theme.ElementKindCount; k++ {
		rows := make([]inspectorField, 0, 32)
		rows = append(rows, identityFields...)
		rows = append(rows, transformFields...)
		rows = append(rows, fillFieldsFor(k)...)
		if k == theme.ElemText {
			rows = append(rows, textFields...)
		}
		rows = append(rows, motionFields...)
		rows = append(rows, behaviourFields...)
		out[k] = rows
	}
	return out
}

// row is the constructor every entry below goes through, so no row can be written
// without a getter and a mutator.
func row(label string, sec inspectorSection, kind fieldKind, f editField, lo, hi int32, enum []string) inspectorField {
	return inspectorField{
		label: label, section: sec, kind: kind, field: f,
		get: inspectElemField, mut: mutateElemField,
		lo: lo, hi: hi, enum: enum,
	}
}

// pickRow is row's fkPick twin: same constructor discipline, plus the live list the
// row offers. Separate rather than a seventh argument on row() so the ~30 rows that
// are not pickers do not each have to write pickNone.
func pickRow(label string, sec inspectorSection, f editField, src pickSource) inspectorField {
	r := row(label, sec, fkPick, f, 0, 0, nil)
	r.pick = src
	return r
}

// editRectSpanPx bounds the inspector's four rect spinners. Generous on purpose:
// an anchored element's rect is a signed DELTA (design §6.6 R1), so negative values
// are the NORMAL case for decoration that overhangs its widget, and a
// SpaceWindow element is measured against no canvas at all. The number is a
// sanity rail on a typed digit, not a layout rule.
const editRectSpanPx = 8192

// identityFields open every rail: WHAT this box is and what it is called.
//
// Kind leads because it is the one field that changes which OTHER rows exist — the
// fill arm is chosen by it — so a rail whose first row rewrote the rows below it
// would be a rail the user has to hunt through. The design's §3.1 list starts at
// Transform; the wave brief's does not ("for a free ELEMENT: kind, rect, …"), and the
// brief wins because an element whose kind cannot be changed is an element that has
// to be deleted and re-made to become a gradient.
// (There is no Id row. An element's id is its SECTION SUFFIX and INIDoc has no
// DELETE, so a rename would append a second [element.<id>] and leave the first —
// a file that reads back as two elements. See editField's own note.)
var identityFields = []inspectorField{
	row("Kind", secTransform, fkEnum, editFieldKind, 0, int32(theme.ElementKindCount)-1, theme.ElementKindNames()),
}

var transformFields = []inspectorField{
	row("Rect", secTransform, fkRect, editFieldRect, -editRectSpanPx, editRectSpanPx, nil),
	row("Rotation", secTransform, fkAngle, editFieldRot, 0, theme.AngleCount-1, nil),
	row("Z", secTransform, fkInt, editFieldZ, theme.ZMin, theme.ZMax, nil),
	row("Band", secTransform, fkEnum, editFieldBand, 0, int32(theme.ElementBandCount)-1, theme.ElementBandNames()),
	row("Space", secTransform, fkEnum, editFieldSpace, 0, int32(theme.SpaceCount)-1, theme.SpaceNames()),
	pickRow("Anchor", secTransform, editFieldAnchor, pickAnchor),
}

var motionFields = []inspectorField{
	row("Clock", secMotion, fkInt, editFieldClock, 0, int32(theme.ClockCap)-1, nil),
	row("Phase (ms)", secMotion, fkInt, editFieldPhaseMs, theme.TimeMsMin, theme.TimeMsMax, nil),
	row("Loop", secMotion, fkBool, editFieldLoop, 0, 1, nil),
	row("Effect", secMotion, fkEnum, editFieldEffect, 0, int32(theme.EffectKindCount)-1, theme.EffectNames()),
	row("Period (ms)", secMotion, fkInt, editFieldEffectPeriodMs, 0, theme.TimeMsMax, nil),
	row("Amplitude", secMotion, fkPercent, editFieldEffectAmpPct, 0, theme.AmpMaxPct, nil),
}

var behaviourFields = []inspectorField{
	row("Visible when", secBehaviour, fkEnum, editFieldVisibleAxis, 0,
		int32(theme.ConditionAxisCount)-1, theme.ConditionAxisNames()),
	row("Condition value", secBehaviour, fkText, editFieldVisibleValue, 0, 0, nil),
	// `decimate` sits in BEHAVIOUR rather than Motion because it is not a motion
	// parameter — it is a budget instruction about how the art is admitted, and it is
	// honoured only while the undecimated page fits the per-item media budget
	// (theme.Element's own comment). Design §3.3 requires the inspector to say when
	// decimation happened; this is the switch that request is about.
	row("Decimate", secBehaviour, fkBool, editFieldNoDecimate, 0, 1, nil),
	row("Hidden", secBehaviour, fkBool, editFieldHidden, 0, 1, nil),
	row("Locked", secBehaviour, fkBool, editFieldLocked, 0, 1, nil),
}

var textFields = []inspectorField{
	row("Text", secText, fkText, editFieldText, 0, 0, nil),
	// THE FONT ROW OFFERS THE REAL LADDER (W7b): the AO2 class ids this client honours
	// and every family this theme declares in [fonts] — the two things the format says
	// `font` may name, taken from the live document rather than typed from memory.
	pickRow("Font", secText, editFieldFont, pickFace),
	// Size's bound is the FORMAT's (theme.TextSizeMaxPt), and the spinner's clamp and
	// the setter's are the same number for the same reason every other numeric row's
	// are: the widget is advisory, the setter is the contract, and Validate is what
	// would otherwise refuse the save long after the keystroke.
	row("Size (pt)", secText, fkInt, editFieldSize, 0, theme.TextSizeMaxPt, nil),
	row("Colour", secText, fkColor, editFieldColor, 0, 0, nil),
	row("Align", secText, fkEnum, editFieldAlign, 0, int32(theme.AlignCount)-1, theme.AlignNames()),
}

// opacityRow and strokeRows are shared by every fill arm — every kind can be faded
// and every kind can carry an outline.
var opacityRow = row("Opacity", secFill, fkInt, editFieldOpacity, 0, 255, nil)

var strokeRows = []inspectorField{
	row("Stroke", secFill, fkColor, editFieldStroke, 0, 0, nil),
	row("Stroke px", secFill, fkInt, editFieldStrokePx, 0, 255, nil),
}

// fillFieldsFor is the per-kind arm — the format's "exactly one is meaningful per
// Kind", made visible in the UI.
//
// A SWITCH WITH NO DEFAULT ARM THAT INVENTS ROWS: an unknown kind gets the shared
// opacity row only, so a future kind shows a working (if minimal) inspector rather
// than a blank rail, and the totality gate still fails to remind us to fill it in.
func fillFieldsFor(k theme.ElementKind) []inspectorField {
	switch k {
	case theme.ElemImage:
		return []inspectorField{
			row("Image", secFill, fkMedia, editFieldMedia, 0, 0, nil),
			row("Fit", secFill, fkEnum, editFieldFit, 0, int32(theme.FitCount)-1, theme.FitNames()),
			// The 9-slice insets, offered on the kind that can use them. NOT gated on
			// `fit = nine`: the format carries the insets independently of the fit (they
			// are read and written either way), so hiding the row until the fit is right
			// would mean an author has to discover the ordering to author the thing.
			row("Slice l t r b", secFill, fkRect, editFieldSlice, theme.ZMin, theme.ZMax, nil),
			row("Tint", secFill, fkColor, editFieldTint, 0, 0, nil),
			opacityRow,
		}
	case theme.ElemShape:
		rows := []inspectorField{
			pickRow("Shape", secFill, editFieldShape, pickShape),
			row("Fill", secFill, fkColor, editFieldFill, 0, 0, nil),
		}
		return append(append(rows, strokeRows...), opacityRow)
	case theme.ElemGradient:
		rows := []inspectorField{
			row("From", secFill, fkColor, editFieldFill, 0, 0, nil),
			row("To", secFill, fkColor, editFieldFill2, 0, 0, nil),
			row("Angle", secFill, fkAngle, editFieldGradAngle, 0, theme.AngleCount-1, nil),
			row("Radial", secFill, fkBool, editFieldGradRadial, 0, 1, nil),
		}
		return append(append(rows, strokeRows...), opacityRow)
	case theme.ElemGen:
		return []inspectorField{
			pickRow("Generator", secFill, editFieldGen, pickGenerator),
			// The params, in the FILE's own `k = v, k = v` spelling, validated against the
			// format's own parser before the op is built (drawInspectorRow) so a string
			// past theme.GenParamCap is refused with the cap named rather than silently
			// truncated into the model.
			row("Params", secFill, fkText, editFieldGenParams, 0, 0, nil),
			row("Fit", secFill, fkEnum, editFieldFit, 0, int32(theme.FitCount)-1, theme.FitNames()),
			row("Tint", secFill, fkColor, editFieldTint, 0, 0, nil),
			opacityRow,
		}
	case theme.ElemText:
		// A text element's PLATE, distinct from its ink (which is the Text section).
		return []inspectorField{
			row("Plate", secFill, fkColor, editFieldFill, 0, 0, nil),
			opacityRow,
		}
	case theme.ElemPane:
		rows := []inspectorField{
			row("Plate", secFill, fkColor, editFieldFill, 0, 0, nil),
			row("Plate 2", secFill, fkColor, editFieldFill2, 0, 0, nil),
		}
		return append(append(rows, strokeRows...), opacityRow)
	}
	return []inspectorField{opacityRow}
}

// ---------------------------------------------------------------------------
// The AO2 WIDGET arm — the [overrides] tier, and NOTHING else
// ---------------------------------------------------------------------------

// inspectSlotField / mutateSlotField are the widget arm's getter and mutator, the
// same shape (and under the same census) as the element pair above.
//
// ONE FIELD, and that is the AO2 full-parity ruling made structural rather than
// remembered: `[overrides]` is a rect table, so a widget's geometry is the only thing
// a theme may restate about an AO2 widget, and the only thing this table can address.
// A future AsyncAO-side extra for a widget cannot be smuggled in as a row here,
// because there is no field for it and editFieldSet would refuse one.
func inspectSlotField(a *App, t editTarget, f editField) editValue {
	if a == nil || a.te == nil || f != editFieldRect {
		return editValue{}
	}
	r, ok := a.te.doc.slotRect(t.designKey())
	if !ok {
		return editValue{}
	}
	_, hadRow := a.te.doc.side.OverrideRect(t.designKey())
	return editValueSlotRect(r, hadRow)
}

// mutateSlotField turns a slot rect row's four spinners into the same op a drag
// produces. It goes through mutateSlotRect rather than building the op itself, so the
// typed number and the dragged one are literally one mutation.
func mutateSlotField(t editTarget, f editField, v editValue) themeEditOp {
	if f != editFieldRect {
		return themeEditOp{}
	}
	r, _ := slotRectOf(v)
	return mutateSlotRect(t, r)
}

// mutateSlotReset is "put this widget back where the theme's own
// courtroom_design.ini put it": DROP the [overrides] row rather than write the
// baseline into it.
//
// Dropping is the whole point. Writing the resolved rect back would leave a row in
// the author's file restating exactly what the AO2 tier already says — a diff for an
// edit the user just took back, which is the same defect editSlotRowPresent exists to
// stop an undo from causing.
func mutateSlotReset(t editTarget) themeEditOp {
	if t.space != spaceDesign {
		return themeEditOp{}
	}
	return themeEditOp{kind: opSlotReset, target: t, field: editFieldRect}
}

// slotFields is the whole AO2-widget rail: geometry, and nothing else.
var slotFields = []inspectorField{{
	label: "Rect", section: secTransform, kind: fkRect, field: editFieldRect,
	get: inspectSlotField, mut: mutateSlotField,
	lo: -editRectSpanPx,
	hi: editRectSpanPx,
}}

// inspectorRowsFor returns the rows for the selected target, or nil.
//
// For a SLOT selection (spaceDesign) it returns the ONE geometry row (slotFields) —
// W7a returned nil here and the rail drew a bare "AO2 widget — geometry only" line,
// which stated the parity rule and then offered no way to act on it: a widget could
// be dragged but its rect could not be TYPED, so the one thing an author most often
// wants from an imported theme (nudge this box to an exact number) was mouse-only.
// The parity rule is unchanged and is now enforced by the table's shape rather than
// by the absence of one.
func (a *App) inspectorRowsFor(t editTarget) []inspectorField {
	if a.te == nil {
		return nil
	}
	switch t.space {
	case spaceDesign:
		if !a.te.doc.slotEditable(t.designKey()) {
			return nil
		}
		return slotFields
	case spaceElement:
		el := a.te.doc.element(t)
		if el == nil || int(el.Kind) >= len(inspectorFields) {
			return nil
		}
		return inspectorFields[el.Kind]
	}
	return nil
}
