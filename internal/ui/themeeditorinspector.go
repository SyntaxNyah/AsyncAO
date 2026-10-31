package ui

// The property inspector's table: each row is data (label, widget kind, getter, mutator), and the mutator returns
// an op rather than performing one, so no field can bypass undo. Adding a row adds an undoable field.
//
// get and mut are top-level funcs, never method values or closures: those allocate per row (see themeslots.go).
// The field is passed as an argument, so the whole table needs four function values shared by every row instead
// of a func pair per field.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// fieldKind is which widget a row draws, never the storage: a swatch and a hex entry both write editFieldFill.
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
	// fkPick is free text with a live list of legal answers beside it: anchor keys, generators, silhouettes, faces.
	//
	// It is not fkEnum: an enum row's value is its index, while the reader takes fkPick fields as free text so a name
	// from a later build degrades to a flat fill rather than failing. The row's enum stays nil because the list is
	// this build's, not the format's.
	fkPick
	fieldKindCount
)

// pickSource names which live list an fkPick row offers; an enum, since a per-row closure would allocate.
type pickSource uint8

const (
	pickNone pickSource = iota
	// pickAnchor: the design keys this theme places, plus its [pane.*] ids (theme.Element.Anchor's domain).
	pickAnchor
	// pickGenerator: internal/ui's registry (GeneratorNames), the one list of names that rasterise.
	pickGenerator
	// pickShape: elemShapeNames, the shipped silhouette vocabulary.
	pickShape
	// pickFace: the class ladder (theme.FontElements) plus every family this theme declares in [fonts], the two
	// things theme.Element.Font may name.
	//
	// Named pickFace because pickFont is already a function in ui.go.
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

// inspectorField is one row.
type inspectorField struct {
	label   string
	section inspectorSection
	kind    fieldKind
	field   editField
	// get reads the current value for the rail to draw.
	get func(*App, editTarget, editField) editValue
	// mut returns the op that would make the change; the document's apply is the only writer.
	mut func(editTarget, editField, editValue) themeEditOp
	// lo / hi bound a numeric row for the widget; the document's setter clamps to the format's range regardless.
	lo, hi int32
	// enum is the wire-name list an fkEnum row offers: the format's own name table.
	enum []string
	// pick names the live list an fkPick row offers beside its text field; pickNone otherwise.
	pick pickSource
}

// inspectElemField is the one getter; the field is an argument.
func inspectElemField(a *App, t editTarget, f editField) editValue {
	if a == nil || a.te == nil {
		return editValue{}
	}
	v, _ := a.te.doc.read(t, f)
	return v
}

// mutateElemField is the one mutator. It returns an op and writes nothing.
// A target that is not an element yields opNone, which themeEditRing.push drops.
func mutateElemField(t editTarget, f editField, v editValue) themeEditOp {
	if t.space != spaceElement || f == editFieldNone || int(f) >= int(editFieldCount) {
		return themeEditOp{}
	}
	return themeEditOp{kind: opElemField, target: t, field: f, after: v}
}

// mutateSlotRect is the mutator for an AO2 widget's geometry (the [overrides] tier).
// The field is editFieldRect so the coalescing key matches a widget drag and an element drag.
func mutateSlotRect(t editTarget, r theme.Rect) themeEditOp {
	if t.space != spaceDesign {
		return themeEditOp{}
	}
	return themeEditOp{kind: opSlotRect, target: t, field: editFieldRect, after: editValueSlotRect(r, true)}
}

// inspectorFields is indexed by theme.ElementKind; built once at init and never mutated, so reads allocate nothing.
var inspectorFields = buildInspectorFields()

// buildInspectorFields assembles the per-kind rows from the shared sections (transform, motion, behaviour);
// only the fill section differs per kind (Element: "exactly one is meaningful per Kind").
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

// row is the constructor every entry goes through, so no row lacks a getter and a mutator.
func row(label string, sec inspectorSection, kind fieldKind, f editField, lo, hi int32, enum []string) inspectorField {
	return inspectorField{
		label: label, section: sec, kind: kind, field: f,
		get: inspectElemField, mut: mutateElemField,
		lo: lo, hi: hi, enum: enum,
	}
}

// pickRow is row's fkPick twin, adding the live list; separate so non-picker rows need not write pickNone.
func pickRow(label string, sec inspectorSection, f editField, src pickSource) inspectorField {
	r := row(label, sec, fkPick, f, 0, 0, nil)
	r.pick = src
	return r
}

// editRectSpanPx bounds the inspector's four rect spinners. An anchored element's rect is a signed delta, so
// negative values are normal, and a SpaceWindow element has no canvas; this is a sanity rail on a typed digit.
const editRectSpanPx = 8192

// identityFields open every rail: what this box is and what it is called.
//
// Kind leads because it changes which other rows exist (the fill arm is chosen by it). The id row is second:
// an element's id is its [element.<id>] header, and renaming goes through theme.INIDoc.RenameSection and
// theme.Sidecar.RenameElement so the file does not read back as two elements.
var identityFields = []inspectorField{
	row("Kind", secTransform, fkEnum, editFieldKind, 0, int32(theme.ElementKindCount)-1, theme.ElementKindNames()),
	row("Id", secTransform, fkText, editFieldElemID, 0, 0, nil),
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
	// decimate sits in behaviour, not motion: it is a budget instruction, honoured only while the undecimated page
	// fits the per-item media budget (theme.Element's comment).
	row("Decimate", secBehaviour, fkBool, editFieldNoDecimate, 0, 1, nil),
	row("Hidden", secBehaviour, fkBool, editFieldHidden, 0, 1, nil),
	row("Locked", secBehaviour, fkBool, editFieldLocked, 0, 1, nil),
}

var textFields = []inspectorField{
	row("Text", secText, fkText, editFieldText, 0, 0, nil),
	// The font row offers the AO2 class ids this client honours and every family in [fonts], from the live document.
	pickRow("Font", secText, editFieldFont, pickFace),
	// Size's bound is the format's (theme.TextSizeMaxPt); the widget is advisory and the setter is the contract.
	row("Size (pt)", secText, fkInt, editFieldSize, 0, theme.TextSizeMaxPt, nil),
	row("Colour", secText, fkColor, editFieldColor, 0, 0, nil),
	row("Align", secText, fkEnum, editFieldAlign, 0, int32(theme.AlignCount)-1, theme.AlignNames()),
}

// opacityRow and strokeRows are shared by every fill arm.
var opacityRow = row("Opacity", secFill, fkInt, editFieldOpacity, 0, 255, nil)

var strokeRows = []inspectorField{
	row("Stroke", secFill, fkColor, editFieldStroke, 0, 0, nil),
	row("Stroke px", secFill, fkInt, editFieldStrokePx, 0, 255, nil),
}

// fillFieldsFor is the per-kind arm. An unknown kind gets only the shared opacity row, so a future kind shows a
// minimal inspector rather than a blank rail.
func fillFieldsFor(k theme.ElementKind) []inspectorField {
	switch k {
	case theme.ElemImage:
		return []inspectorField{
			row("Image", secFill, fkMedia, editFieldMedia, 0, 0, nil),
			row("Fit", secFill, fkEnum, editFieldFit, 0, int32(theme.FitCount)-1, theme.FitNames()),
			// The 9-slice insets are not gated on fit = nine: the format carries them either way.
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
			// The params use the file's k = v, k = v spelling, validated by the format's parser before the op is built,
			// so a string past theme.GenParamCap is refused with the cap named rather than truncated.
			row("Params", secFill, fkText, editFieldGenParams, 0, 0, nil),
			row("Fit", secFill, fkEnum, editFieldFit, 0, int32(theme.FitCount)-1, theme.FitNames()),
			row("Tint", secFill, fkColor, editFieldTint, 0, 0, nil),
			opacityRow,
		}
	case theme.ElemText:
		// A text element's plate, distinct from its ink (the Text section).
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

// inspectSlotField / mutateSlotField are the widget arm's getter and mutator, shaped like the element pair.
// There is one field: [overrides] is a rect table, so geometry is all a theme may restate about an AO2 widget.
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

// mutateSlotField turns a slot rect row's four spinners into the same op a drag produces, via mutateSlotRect.
func mutateSlotField(t editTarget, f editField, v editValue) themeEditOp {
	if f != editFieldRect {
		return themeEditOp{}
	}
	r, _ := slotRectOf(v)
	return mutateSlotRect(t, r)
}

// mutateSlotReset puts a widget back where the theme's courtroom_design.ini put it by dropping its [overrides] row;
// writing the resolved rect back would leave a row restating what the AO2 tier already says.
func mutateSlotReset(t editTarget) themeEditOp {
	if t.space != spaceDesign {
		return themeEditOp{}
	}
	return themeEditOp{kind: opSlotReset, target: t, field: editFieldRect}
}

// slotFields is the whole AO2-widget rail: geometry only.
var slotFields = []inspectorField{{
	label: "Rect", section: secTransform, kind: fkRect, field: editFieldRect,
	get: inspectSlotField, mut: mutateSlotField,
	lo: -editRectSpanPx,
	hi: editRectSpanPx,
}}

// inspectorRowsFor returns the rows for the selected target, or nil. A slot selection (spaceDesign) gets the one
// geometry row (slotFields), so a widget's rect can be typed as well as dragged.
func (te *themeEditor) inspectorRowsFor(t editTarget) []inspectorField {
	if te == nil {
		return nil
	}
	switch t.space {
	case spaceDesign:
		if !te.doc.slotEditable(t.designKey()) {
			return nil
		}
		return slotFields
	case spaceElement:
		el := te.doc.element(t)
		if el == nil || int(el.Kind) >= len(inspectorFields) {
			return nil
		}
		return inspectorFields[el.Kind]
	}
	return nil
}
