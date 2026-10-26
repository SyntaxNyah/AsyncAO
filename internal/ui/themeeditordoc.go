package ui

// THE EDITOR'S DOCUMENT (v1.90.0 W7a) — docs/wip/THEME-EDITOR-DESIGN.md §Q7.
//
// ONE concern: "what the editor is editing, and the only way to change it."
//
// WHY A DOCUMENT LAYER EXISTS AT ALL. W6 left the hazard written down at its own
// site (layoutnudge.go's nudgeThemeElement): a.themeSidecar is REPLACED WHOLESALE
// by pollThemeApply (`a.themeSidecar = res.sidecar`), and applies are not
// user-initiated — healTheme re-kicks one on any T1 eviction of a theme texture,
// the texture-filter row kicks one, boot kicks two. So every unsaved element edit
// silently evaporated, with nothing on screen to say why. The document is what
// makes edits durable: while the editor owns a theme, the document's model is the
// authoritative one, and reclaimThemeDoc re-installs it after every apply.
//
// ONE MUTATION PATH, AND IT IS NOT OPTIONAL. themeDoc.apply is the only function
// in the package that writes a field of a theme.Element belonging to the edited
// document, and it RETURNS the op it performed with `before` filled in from the
// live model. A field therefore cannot be edited without producing its own inverse
// — not by discipline, but because the write and the inverse are the same line.
// TestOnlyTheDocumentUsesTheFieldTables and
// TestEveryDocumentMutationGoesThroughTheApplyFunnel are the censuses that keep
// it that way, and TestEveryInspectorFieldIsUndoable proves the inverse really
// inverts by hashing the serialised document before and after.
//
// SDL-FREE BY SHAPE. Nothing here draws, measures text or touches a texture; the
// screen (themeeditor.go) does all of that. The split is what lets every gate in
// this file run without a renderer.

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Named caps (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// themeElemGraveCap bounds the element graveyard — the bodies a delete removed
	// that an undo must be able to resurrect. It is themeEditUndoCap by argument
	// rather than by coincidence: the ring is the only thing that can ever ask for
	// one back, so a graveyard bigger than the ring holds bodies nobody can reach and
	// a smaller one would make some undos lossy.
	themeElemGraveCap = themeEditUndoCap

	// editSlotRowPresent is the opSlotRect payload's "an [overrides] row for this
	// key already existed" flag, carried in the b[0] lane.
	//
	// ONE BIT, AND IT IS LOAD-BEARING. Without it an undo can only put the NUMBERS
	// back, not the row: a drag that created the theme's first override on a widget
	// and was then undone would leave behind a row restating what the AO2 tier
	// already says — a diff in the author's file for an edit the user took back.
	// editValue is a union whose lanes are the writer's business (themeeditorop.go
	// says so), so the flag lives here, beside the only two functions that read it.
	editSlotRowPresent uint8 = 1
)

// editValueSlotRect / slotRectOf are the ONE encoding of an opSlotRect payload.
// Named, and a pair, for the same reason rgbaValue/valueRGBA are: a rect packed in
// one lane order and unpacked in another compiles and is silently wrong.
func editValueSlotRect(r theme.Rect, hadRow bool) editValue {
	v := editValueRect(int32(r.X), int32(r.Y), int32(r.W), int32(r.H))
	if hadRow {
		v.b[0] = editSlotRowPresent
	}
	return v
}

func slotRectOf(v editValue) (theme.Rect, bool) {
	return theme.Rect{X: int(v.i[0]), Y: int(v.i[1]), W: int(v.i[2]), H: int(v.i[3])},
		v.b[0] == editSlotRowPresent
}

// ---------------------------------------------------------------------------
// editField — the addressable fields of a free element
// ---------------------------------------------------------------------------

// editField names ONE addressable field. It is the third coordinate of an edit
// (space, target, field) and the index into the get/set tables below.
//
// APPEND-ONLY, at the END. Ops are never persisted, so a reorder cannot corrupt a
// saved theme — but the inspector table is indexed by these and a reorder would
// silently re-point every row in it.
type editField uint8

const (
	editFieldNone editField = iota

	// Transform
	editFieldRect
	editFieldRot
	editFieldZ
	editFieldBand
	editFieldSpace
	editFieldAnchor

	// Fill
	editFieldMedia
	editFieldShape
	editFieldGen
	editFieldFit
	editFieldFill
	editFieldFill2
	editFieldGradAngle
	editFieldGradRadial
	editFieldStroke
	editFieldStrokePx
	editFieldTint
	editFieldOpacity

	// Text
	editFieldText
	editFieldFont
	editFieldSize
	editFieldColor
	editFieldAlign

	// Motion
	editFieldClock
	editFieldPhaseMs
	editFieldLoop
	editFieldEffect
	editFieldEffectPeriodMs
	editFieldEffectAmpPct

	// Behaviour
	editFieldVisibleAxis
	editFieldVisibleValue
	editFieldHidden
	editFieldLocked

	// Identity and fill detail (v1.90.0 W7b). APPENDED, at the END, per this enum's
	// own rule — the inspector table is indexed by these and a reorder would silently
	// re-point every row in it.
	editFieldKind
	editFieldGenParams
	editFieldSlice
	editFieldNoDecimate

	// Identity (v1.90.0 W8). APPENDED, at the END, per this enum's own rule.
	//
	// editFieldElemID was deliberately ABSENT through W7b, and the reason is worth
	// keeping now that it is here: an element's id is its SECTION SUFFIX — the writer
	// emits `[element.<id>]` and no key carries it — and INIDoc had no way to move a
	// section, so a rename would have APPENDED a second section for the same element
	// and left the first behind: a file that reads back as TWO elements, on a save the
	// user asked for. W8 is the wave that owns moving bytes around, so it landed
	// theme.INIDoc.RenameSection and the one funnel allowed to drive it
	// (theme.Sidecar.RenameElement, which moves the model and the header as one act).
	// The setter below calls exactly that; nothing else in this package may.
	editFieldElemID

	// editFieldCount sizes the get/set tables — fixed arrays indexed by field, never
	// maps: the tables are read during a drag.
	editFieldCount
)

// editFieldNames is the field's own label in a diagnostic. Not the inspector's
// label (that is authored per row, and two rows may address one field).
var editFieldNames = [editFieldCount]string{
	editFieldNone: "none",

	editFieldRect: "rect", editFieldRot: "rot", editFieldZ: "z",
	editFieldBand: "band", editFieldSpace: "space", editFieldAnchor: "anchor",

	editFieldMedia: "media", editFieldShape: "shape", editFieldGen: "gen",
	editFieldFit: "fit", editFieldFill: "fill", editFieldFill2: "fill2",
	editFieldGradAngle: "grad_angle", editFieldGradRadial: "grad_radial",
	editFieldStroke: "stroke", editFieldStrokePx: "stroke_px",
	editFieldTint: "tint", editFieldOpacity: "opacity",

	editFieldText: "text", editFieldFont: "font", editFieldSize: "size",
	editFieldColor: "color", editFieldAlign: "align",

	editFieldClock: "clock", editFieldPhaseMs: "phase_ms", editFieldLoop: "loop",
	editFieldEffect: "effect", editFieldEffectPeriodMs: "effect_period_ms",
	editFieldEffectAmpPct: "effect_amp_pct",

	editFieldVisibleAxis: "visible_when.axis", editFieldVisibleValue: "visible_when.value",
	editFieldHidden: "hidden", editFieldLocked: "locked",

	editFieldKind: "kind", editFieldGenParams: "gen_params",
	editFieldSlice: "slice", editFieldNoDecimate: "decimate",

	editFieldElemID: "id",
}

func (f editField) String() string {
	if int(f) >= len(editFieldNames) || editFieldNames[f] == "" {
		return "?"
	}
	return editFieldNames[f]
}

// editFieldGet reads one field out of an element into the fixed-size payload.
//
// A TABLE, NOT A SWITCH, and indexed by the enum: a field appended to editField
// without a reader here is a nil entry that
// TestEveryEditFieldHasAGetterAndASetter names, where a switch's default would
// have silently returned the zero value and made the field look like it was
// always empty.
var editFieldGet = [editFieldCount]func(*themeDoc, *theme.Element) editValue{
	editFieldRect: func(_ *themeDoc, e *theme.Element) editValue {
		return editValueRect(int32(e.Rect.X), int32(e.Rect.Y), int32(e.Rect.W), int32(e.Rect.H))
	},
	editFieldRot:   func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Rot)) },
	editFieldZ:     func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Z)) },
	editFieldBand:  func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Band)) },
	editFieldSpace: func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Space)) },
	editFieldAnchor: func(d *themeDoc, e *theme.Element) editValue {
		return editValueStr(d.intern(e.Anchor))
	},

	editFieldMedia: func(d *themeDoc, e *theme.Element) editValue { return editValueStr(d.intern(e.Media)) },
	editFieldShape: func(d *themeDoc, e *theme.Element) editValue { return editValueStr(d.intern(e.Shape)) },
	editFieldGen:   func(d *themeDoc, e *theme.Element) editValue { return editValueStr(d.intern(e.Gen)) },
	editFieldFit:   func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Fit)) },
	editFieldFill:  func(_ *themeDoc, e *theme.Element) editValue { return rgbaValue(e.Fill) },
	editFieldFill2: func(_ *themeDoc, e *theme.Element) editValue { return rgbaValue(e.Fill2) },
	editFieldGradAngle: func(_ *themeDoc, e *theme.Element) editValue {
		return editValueInt(int32(e.GradAngle))
	},
	editFieldGradRadial: func(_ *themeDoc, e *theme.Element) editValue { return editValueBool(e.GradRadial) },
	editFieldStroke:     func(_ *themeDoc, e *theme.Element) editValue { return rgbaValue(e.Stroke) },
	editFieldStrokePx: func(_ *themeDoc, e *theme.Element) editValue {
		return editValueInt(int32(e.StrokePx))
	},
	editFieldTint:    func(_ *themeDoc, e *theme.Element) editValue { return rgbaValue(e.Tint) },
	editFieldOpacity: func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Opacity)) },

	editFieldText:  func(d *themeDoc, e *theme.Element) editValue { return editValueStr(d.intern(e.Text)) },
	editFieldFont:  func(d *themeDoc, e *theme.Element) editValue { return editValueStr(d.intern(e.Font)) },
	editFieldSize:  func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Size)) },
	editFieldColor: func(_ *themeDoc, e *theme.Element) editValue { return rgbaValue(e.Color) },
	editFieldAlign: func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Align)) },

	editFieldClock:   func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Clock)) },
	editFieldPhaseMs: func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(e.PhaseMs) },
	editFieldLoop:    func(_ *themeDoc, e *theme.Element) editValue { return editValueBool(e.Loop) },
	editFieldEffect:  func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Effect)) },
	editFieldEffectPeriodMs: func(_ *themeDoc, e *theme.Element) editValue {
		return editValueInt(e.EffectPeriodMs)
	},
	editFieldEffectAmpPct: func(_ *themeDoc, e *theme.Element) editValue {
		return editValueInt(int32(e.EffectAmpPct))
	},

	editFieldVisibleAxis: func(_ *themeDoc, e *theme.Element) editValue {
		return editValueInt(int32(e.VisibleWhen.Axis))
	},
	editFieldVisibleValue: func(d *themeDoc, e *theme.Element) editValue {
		return editValueStr(d.intern(e.VisibleWhen.Value))
	},
	editFieldHidden: func(_ *themeDoc, e *theme.Element) editValue { return editValueBool(e.Hidden) },
	editFieldLocked: func(_ *themeDoc, e *theme.Element) editValue { return editValueBool(e.Locked) },

	editFieldKind: func(_ *themeDoc, e *theme.Element) editValue { return editValueInt(int32(e.Kind)) },
	// The params are read back through the FORMAT'S OWN writer, so the string the
	// inspector shows is the string the file holds — never a second spelling of the
	// same list that would rewrite somebody's line on a save that changed nothing.
	editFieldGenParams: func(d *themeDoc, e *theme.Element) editValue {
		return editValueStr(d.intern(theme.WriteGenParams(e)))
	},
	editFieldSlice: func(_ *themeDoc, e *theme.Element) editValue {
		return editValueRect(int32(e.Slice[0]), int32(e.Slice[1]), int32(e.Slice[2]), int32(e.Slice[3]))
	},
	// STORED INVERTED, and the row reads the way the FILE spells it. theme.Element's
	// NoDecimate is "decimate = no" held inverted so the zero value is version 1's
	// behaviour; an inspector row labelled "Decimate" that showed the raw bool would
	// be checked when the key says no. One negation, here, beside the setter's.
	editFieldNoDecimate: func(_ *themeDoc, e *theme.Element) editValue { return editValueBool(!e.NoDecimate) },

	editFieldElemID: func(d *themeDoc, e *theme.Element) editValue { return editValueStr(d.intern(e.ID)) },
}

// editFieldSet writes one field back.
//
// EVERY SETTER CLAMPS TO THE FORMAT'S OWN RANGE, in the format's own constants.
// Defence in depth rather than duplication: the reader clamps what it parses, but
// the editor writes the model directly, and Sidecar.Bytes() REFUSES a model outside
// the grammar — so an unclamped spinner would turn a save into an error dialog
// long after the edit that caused it. Clamping here means the document is always
// saveable, which is the property autosave rests on.
var editFieldSet = [editFieldCount]func(*themeDoc, *theme.Element, editValue){
	editFieldRect: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Rect = theme.Rect{X: int(v.i[0]), Y: int(v.i[1]), W: int(v.i[2]), H: int(v.i[3])}
	},
	editFieldRot: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Rot = uint8(v.i[0] & (theme.AngleCount - 1))
	},
	editFieldZ: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Z = int16(clampInt32(v.i[0], theme.ZMin, theme.ZMax))
	},
	editFieldBand: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Band = theme.ElementBand(clampInt32(v.i[0], 0, int32(theme.ElementBandCount)-1))
	},
	editFieldSpace: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Space = theme.Space(clampInt32(v.i[0], 0, int32(theme.SpaceCount)-1))
	},
	editFieldAnchor: func(d *themeDoc, e *theme.Element, v editValue) { e.Anchor = d.str(v.s) },

	editFieldMedia: func(d *themeDoc, e *theme.Element, v editValue) { e.Media = d.str(v.s) },
	editFieldShape: func(d *themeDoc, e *theme.Element, v editValue) { e.Shape = d.str(v.s) },
	editFieldGen:   func(d *themeDoc, e *theme.Element, v editValue) { e.Gen = d.str(v.s) },
	editFieldFit: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Fit = theme.ElementFit(clampInt32(v.i[0], 0, int32(theme.FitCount)-1))
	},
	editFieldFill:  func(_ *themeDoc, e *theme.Element, v editValue) { e.Fill = valueRGBA(v) },
	editFieldFill2: func(_ *themeDoc, e *theme.Element, v editValue) { e.Fill2 = valueRGBA(v) },
	editFieldGradAngle: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.GradAngle = uint8(v.i[0] & (theme.AngleCount - 1))
	},
	editFieldGradRadial: func(_ *themeDoc, e *theme.Element, v editValue) { e.GradRadial = v.i[0] != 0 },
	editFieldStroke:     func(_ *themeDoc, e *theme.Element, v editValue) { e.Stroke = valueRGBA(v) },
	editFieldStrokePx: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.StrokePx = uint8(clampInt32(v.i[0], 0, 255))
	},
	editFieldTint: func(_ *themeDoc, e *theme.Element, v editValue) { e.Tint = valueRGBA(v) },
	editFieldOpacity: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Opacity = uint8(clampInt32(v.i[0], 0, 255))
	},

	editFieldText: func(d *themeDoc, e *theme.Element, v editValue) {
		e.Text = truncRunes(d.str(v.s), theme.TextRuneCap)
	},
	editFieldFont: func(d *themeDoc, e *theme.Element, v editValue) { e.Font = d.str(v.s) },
	editFieldSize: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Size = int(clampInt32(v.i[0], 0, theme.TextSizeMaxPt))
	},
	editFieldColor: func(_ *themeDoc, e *theme.Element, v editValue) { e.Color = valueRGBA(v) },
	editFieldAlign: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Align = theme.TextAlign(clampInt32(v.i[0], 0, int32(theme.AlignCount)-1))
	},

	editFieldClock: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Clock = uint8(clampInt32(v.i[0], 0, int32(theme.ClockCap)-1))
	},
	editFieldPhaseMs: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.PhaseMs = clampInt32(v.i[0], theme.TimeMsMin, theme.TimeMsMax)
	},
	editFieldLoop: func(_ *themeDoc, e *theme.Element, v editValue) { e.Loop = v.i[0] != 0 },
	editFieldEffect: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Effect = theme.EffectKind(clampInt32(v.i[0], 0, int32(theme.EffectKindCount)-1))
	},
	editFieldEffectPeriodMs: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.EffectPeriodMs = clampInt32(v.i[0], theme.TimeMsMin, theme.TimeMsMax)
	},
	editFieldEffectAmpPct: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.EffectAmpPct = int16(clampInt32(v.i[0], 0, theme.AmpMaxPct))
	},

	editFieldVisibleAxis: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.VisibleWhen.Axis = theme.ConditionAxis(clampInt32(v.i[0], 0, int32(theme.ConditionAxisCount)-1))
	},
	editFieldVisibleValue: func(d *themeDoc, e *theme.Element, v editValue) {
		e.VisibleWhen.Value = truncRunes(d.str(v.s), theme.ConditionValueRuneCap)
	},
	editFieldHidden: func(_ *themeDoc, e *theme.Element, v editValue) { e.Hidden = v.i[0] != 0 },
	editFieldLocked: func(_ *themeDoc, e *theme.Element, v editValue) { e.Locked = v.i[0] != 0 },

	editFieldKind: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Kind = theme.ElementKind(clampInt32(v.i[0], 0, int32(theme.ElementKindCount)-1))
	},
	editFieldGenParams: func(d *themeDoc, e *theme.Element, v editValue) {
		// Parsed by the FORMAT'S OWN parser (theme.ParseGenParams == the reader's
		// genParamsOf). A string past GenParamCap comes back with the entries that DID
		// fit plus an error; the draw site refuses it before it ever gets here, so the
		// error is unreachable for a validated row — and taking the partial array is
		// still the honest fallback for an op replayed against a changed document.
		p, _ := theme.ParseGenParams(d.str(v.s))
		e.GenParams = p
	},
	// theme.ZMin/ZMax is the READER's own bound on a slice inset (parseSliceValue
	// clamps to exactly those), reused rather than restated: a second pair of numbers
	// here would let the editor author a value the reader then folds, and the file
	// would stop round-tripping.
	editFieldSlice: func(_ *themeDoc, e *theme.Element, v editValue) {
		for i := range e.Slice {
			e.Slice[i] = int16(clampInt32(v.i[i], theme.ZMin, theme.ZMax))
		}
	},
	editFieldNoDecimate: func(_ *themeDoc, e *theme.Element, v editValue) { e.NoDecimate = v.i[0] == 0 },

	// THE ID GOES THROUGH THE FORMAT'S OWN FUNNEL and never through e.ID directly.
	// An element's id lives in two places — this struct field and the
	// `[element.<id>]` header in the lossless carrier — and theme.RenameElement is
	// the only thing that can move both as one act. Assigning e.ID here would leave
	// the carrier's header on the old name, and the next save would APPEND a second
	// section: a file that reads back as two elements (see editFieldElemID).
	//
	// The error is dropped HERE and refused THERE: editorWriteStringField asks the
	// same funnel first and shows the reason as a chip, because a setter cannot say
	// no — the op is already recorded by the time it runs. What is left at this point
	// is an id that has already been accepted once, so the only way this fails is a
	// document that changed underneath a replayed op, and the honest answer to that
	// is to leave the model alone.
	editFieldElemID: func(d *themeDoc, e *theme.Element, v editValue) {
		_ = d.side.RenameElement(e.ID, d.str(v.s))
	},
}

// rgbaValue / valueRGBA are the ONE conversion between the format's colour and the
// payload's byte lane, so a field cannot pack its channels in one order and unpack
// them in another.
func rgbaValue(c theme.RGBA) editValue { return editValueRGBA(c.R, c.G, c.B, c.A) }
func valueRGBA(v editValue) theme.RGBA {
	return theme.RGBA{R: v.b[0], G: v.b[1], B: v.b[2], A: v.b[3]}
}

// clampInt32 bounds a payload scalar. Named because every setter above uses it and
// an inline min/max pair repeated thirty times is thirty chances to invert one.
func clampInt32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// truncRunes bounds a free-text field by RUNES (the format's own unit — a byte
// truncation would cut a multi-byte rune in half and write invalid UTF-8 into a
// file the reader then has to cope with).
func truncRunes(s string, max int) string {
	n := 0
	for i := range s {
		n++
		if n > max {
			return s[:i]
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// themeDoc
// ---------------------------------------------------------------------------

// themeDoc is everything the editor edits, for ONE theme.
//
// side is the AUTHORITATIVE model while the editor is open: a.themeSidecar points
// at the same *theme.Sidecar, and reclaimThemeDoc re-installs it after every theme
// apply (see the file header). The lossless carrier and the AO2-compat export live
// inside the Sidecar itself and are reached only through its guarded Bytes() — this
// type deliberately holds no *theme.INIDoc, because holding one would be the
// second writer the theme package's API census exists to forbid.
type themeDoc struct {
	// name is the theme folder this document belongs to. The reclaim check compares
	// it against the applied theme, so a document is never re-installed over a
	// DIFFERENT theme the user switched to mid-session.
	name string
	side *theme.Sidecar
	// live is the APPLIED design map — a.themeRects itself, not a copy.
	//
	// It is here because an [overrides] edit has to land in TWO places to mean
	// anything: the row (which is what gets saved) and the resolved geometry (which
	// is what the next frame draws). foldSidecarOverrides runs on the theme-apply
	// GOROUTINE, over a map nothing else can see yet, so an edit that wrote only the
	// row would not move a pixel until the user switched themes and back — the
	// "live preview, no Apply button" requirement, failed silently.
	//
	// It is REBOUND rather than captured: pollThemeApply replaces a.themeRects
	// wholesale, so the map a document opened over is a dead object after any
	// background apply. bindLive is called from the two places that know — the open
	// path and the reclaim.
	live map[string]theme.Rect
	// baseLive is the resolved geometry as it stood when the editor OPENED, and
	// baseElems / baseOv are the model as it stood then.
	//
	// They are what "exit without saving puts it back" is made of, and they are a
	// snapshot rather than a replay of the undo ring on purpose: the ring is capped
	// (themeEditUndoCap), so past 128 ops a replay would restore MOST of the
	// document and say nothing about the rest. A snapshot cannot be partly right.
	// baseLive doubles as the answer to "what did this widget's rect resolve to
	// before we wrote a row for it", which is what undoing a row's CREATION needs.
	baseLive  map[string]theme.Rect
	baseElems []theme.Element
	baseOv    []theme.KeyRect
	// baseFonts / baseBind are the TYPE tables as they stood at open, for the same
	// all-or-nothing reason: W7b made [fonts] and [fontbind] editable, so a snapshot
	// that covered only the elements and the overrides would let "Back, Back" throw
	// away a session's geometry and silently keep its font bindings.
	baseFonts []theme.FontFamily
	baseBind  []theme.KV
	// typeSigApplied is typeSig() as of the last theme apply this document caused —
	// i.e. the tables the SDL_ttf faces on screen were resolved from.
	//
	// It exists because a save must be able to answer "did the TYPE change?" without
	// asking "did anything change?". Geometry and colour are re-baked live by
	// editorApply's invalidate and the art is re-planned live by editorSyncArt
	// (themeeditorart.go), but a [fontbind] row is only resolved into real faces by a
	// theme apply — and an apply re-anchors a.themeAt and purges the text cache, so
	// kicking one for a rect nudge restarts every animated element in the theme.
	//
	// TWO WRITERS, both of which have just made it true: newThemeDoc (the editor opens
	// over the APPLIED theme) and applyThemeAfterSaveIfTypeChanged (which kicks the
	// apply in the same breath). It is never set anywhere an apply did not happen.
	typeSigApplied uint64
	// dirty is "there are unsaved edits". Set by apply, cleared by a save.
	//
	// (There is no savedMtime yet. The external-edit guard belongs with the AUTOSAVE
	// that needs it — W7b — and a field carried in advance of its only reader is
	// state nothing keeps honest.)
	dirty bool

	// grave holds the BODIES of deleted elements so an undo can resurrect them
	// exactly. A delete cannot keep the element in place, and the op is a fixed-size
	// value that cannot carry a theme.Element — so the body parks here and the op
	// carries the slot index.
	grave  [themeElemGraveCap]theme.Element
	graveN int

	// strs is the session's string interning table (see editStrCap). strs[0] is
	// always "", which is what makes the zero editValue mean "empty".
	strs [editStrCap]string
	strN int
}

// newThemeDoc opens a document over a live sidecar. A nil sidecar is a STOCK AO2
// theme, which has no native tier yet — the caller creates one first
// (theme.NewSidecar); this returns nil rather than inventing one, because deciding
// to add a native tier to somebody's theme is a user act, not a constructor's.
func newThemeDoc(name string, sc *theme.Sidecar) *themeDoc {
	if sc == nil {
		return nil
	}
	d := &themeDoc{name: name, side: sc}
	d.strN = 1 // strs[0] is the empty string
	// The baseline, taken ONCE at open: three cold copies bounded by
	// theme.ElementCap / theme.OverrideCap / the themeSlots registry. Nothing on any
	// frame path touches them again until an exit-without-saving asks for them back.
	d.baseElems = append([]theme.Element(nil), sc.Elements...)
	d.baseOv = append([]theme.KeyRect(nil), sc.Overrides...)
	d.baseFonts = append([]theme.FontFamily(nil), sc.Fonts...)
	d.baseBind = append([]theme.KV(nil), sc.FontBind...)
	// The faces on screen were resolved from THESE tables: the editor opens over the
	// APPLIED theme, so the document starts in step with the last apply by definition.
	// Seeding it here rather than at the first save is what makes a session that never
	// touches the type kick no applies at all.
	d.typeSigApplied = d.typeSig()
	return d
}

// bindLive points the document at the applied design map.
//
// TWO NAMED CALL SITES, both in themeeditor.go: armThemeEditor (open) and
// reclaimThemeDoc (after a background theme apply replaced the map). It is not a
// setter anyone else may call — a document bound to a map some other subsystem owns
// would be the second-writer shape the sidecar's own deleted Doc() accessor had.
//
// The FIRST bind takes the baseline; later ones do not, because a re-bind means the
// applied map was rebuilt underneath an editing session, not that the session
// started again.
func (d *themeDoc) bindLive(rects map[string]theme.Rect) {
	if d == nil {
		return
	}
	d.live = rects
	if d.baseLive == nil && rects != nil {
		d.baseLive = cloneRects(rects)
	}
}

// intern returns the id of s, adding it if there is room. A FULL TABLE returns
// editStrNone — i.e. "", which the op layer treats as a refused construction rather
// than as a silent edit to the empty string (mutateElemField checks the round trip).
//
// A linear scan of at most editStrCap: this runs on an inspector edit, never per
// frame, and a map would allocate on every miss.
func (d *themeDoc) intern(s string) editStrID {
	if s == "" {
		return editStrNone
	}
	for i := 1; i < d.strN; i++ {
		if d.strs[i] == s {
			return editStrID(i)
		}
	}
	if d.strN >= len(d.strs) {
		return editStrNone
	}
	d.strs[d.strN] = s
	d.strN++
	return editStrID(d.strN - 1)
}

// str resolves an interned id. An out-of-range id is "" rather than a panic: ids
// come from ops, ops outlive nothing, and a render-thread panic is never the right
// answer to a stale integer.
func (d *themeDoc) str(id editStrID) string {
	if id <= 0 || int(id) >= d.strN {
		return ""
	}
	return d.strs[id]
}

// element resolves a target to the live row, or nil.
func (d *themeDoc) element(t editTarget) *theme.Element {
	idx, ok := t.elemIdx()
	if !ok || d == nil || d.side == nil || idx < 0 || idx >= len(d.side.Elements) {
		return nil
	}
	return &d.side.Elements[idx]
}

// elementCount is how many rows the element rail draws.
func (d *themeDoc) elementCount() int {
	if d == nil || d.side == nil {
		return 0
	}
	return len(d.side.Elements)
}

// read returns a field's CURRENT value. It is editFieldGet with the bounds checks,
// and it is what the inspector draws from — never a second reader.
func (d *themeDoc) read(t editTarget, f editField) (editValue, bool) {
	el := d.element(t)
	if el == nil || int(f) >= len(editFieldGet) || editFieldGet[f] == nil {
		return editValue{}, false
	}
	return editFieldGet[f](d, el), true
}

// editApplyResult is what themeDoc.apply says about a request.
//
// THREE ANSWERS, NOT TWO, and the third one is a shipped defect written down. A bare
// ok=false conflated "the model already says that" with "a cap or a lock stopped
// me", and the screen has to tell them apart: editorApply chips a refusal on the
// rail for editStatusMs, so under one bit EVERY frame of a real human drag that
// holds still — and every frame a drag sits against clampDesignRectToCanvas — posted
// "a cap or a lock is in the way" on the wave's headline interaction, naming a limit
// that was not there. Design §3.3 says a refusal must name the offender; it says
// nothing that licenses inventing one.
//
// The distinction lives HERE, at the mutation, rather than in a second predicate the
// screen could ask beforehand: "would this change anything" and "did this change
// anything" answered by two functions is two chances to disagree about equality, and
// the disagreement would be silent.
//
// ONE PRODUCER AND ONE CONSUMER, and both are already fenced: apply is the only thing
// that returns one, and TestEveryDocumentMutationGoesThroughTheApplyFunnel forbids any
// file but themeeditor.go from calling apply at all — so this cannot become a status
// code half the package branches on. The two behaviours it exists for are pinned from
// opposite sides (TestHoldingAWidgetStillNeverCriesRefused that a no-op is silent,
// TestAGenuineCapRefusalStillChips that a bound is not), which is what collapsing it
// back to a bool has to break.
type editApplyResult uint8

const (
	// applyNoChange: nothing was prevented and nothing happened — the model already
	// holds the requested value, or there is nothing at that target to hold it (a
	// selection that outlived a delete). SILENT: it is the ordinary shape of a held
	// pointer, not an event.
	applyNoChange editApplyResult = iota
	// applyRefused: the document could not perform the request. In every case the
	// code actually produces it, that is a NAMED bound — theme.OverrideCap
	// (applySlotRect), theme.ElementCap (insertAt) or the full undo graveyard
	// (graveSlot, insertAt's missing body) — which is why the chip names a cap.
	//
	// NARROWED DELIBERATELY (W7b). This used to read as though refusal MEANT "a named
	// bound said no", and applyAdd's own arm quietly disagreed: insertAt also fails for
	// an out-of-range target index, which is a stale op rather than a limit. The
	// promise this constant makes is therefore the weaker, true one — "the document
	// refused, and the caller must say so out loud" — and the CAP is a property of the
	// three arms that raise it, stated where they raise it. A status code whose doc
	// comment claims more than its producers deliver is how a chip ends up naming a
	// limit that was not there, which is the exact defect editApplyResult exists for.
	applyRefused
	// applyDone: the model moved, and op carries its own inverse.
	applyDone
)

// apply performs one mutation and RETURNS ITS OWN INVERSE.
//
// THE ONE MUTATION PATH. It reads the current value into op.before before writing
// op.after, so the completed op is simultaneously "what happened" and "how to undo
// it", and there is exactly one code path for both directions (revert calls write
// with op.before). A caller cannot edit a field and forget to record it, because
// the edit IS the record.
//
// Anything but applyDone means nothing changed, and a no-op must not reach the ring:
// an undo step that does nothing is a keypress the user spends and sees no result
// from, which reads as a broken undo. Which KIND of nothing is editApplyResult's
// whole reason for existing.
func (d *themeDoc) apply(op themeEditOp) (themeEditOp, editApplyResult) {
	switch op.kind {
	case opElemField:
		el := d.element(op.target)
		if el == nil || int(op.field) >= len(editFieldGet) ||
			editFieldGet[op.field] == nil || editFieldSet[op.field] == nil {
			return op, applyNoChange // nothing there to edit: no cap, no lock, no event
		}
		op.before = editFieldGet[op.field](d, el)
		if op.before == op.after {
			return op, applyNoChange
		}
		editFieldSet[op.field](d, el, op.after)
		d.dirty = true
		return op, applyDone
	case opElemDel:
		return d.applyDelete(op)
	case opElemAdd:
		return d.applyAdd(op)
	case opSlotRect:
		return d.applySlotRect(op)
	case opSlotReset:
		return d.applySlotReset(op)
	case opFontFamily, opFontBind:
		return d.applyFontRow(op)
	case opMediaRow:
		return d.applyMediaRow(op)
	}
	return op, applyNoChange
}

// ---------------------------------------------------------------------------
// The [media] table — one declared image per row
// ---------------------------------------------------------------------------

// mediaRowValue / mediaRowOf are the ONE encoding of a [media] row, a pair for the
// same reason fontRowValue / fontRowOf are: four fields packed in one order and
// unpacked in another compiles and is silently wrong.
//
// It rides fontRowSep (U+001F) for the reason that constant documents — INIDoc
// refuses control characters in a value, so no field can contain the separator and
// the encoding cannot be ambiguous for any row the reader would accept.
//
// AN EMPTY ID ENCODES TO "", which is the table's own "no row" value: it is what a
// removal writes and what an append undoes to, so the two need no extra flag.
func mediaRowValue(m theme.MediaEntry) string {
	if m.ID == "" {
		return ""
	}
	return m.ID + fontRowSep + m.File + fontRowSep + strconv.FormatInt(m.Bytes, 10) + fontRowSep + m.Hash
}

func mediaRowOf(s string) theme.MediaEntry {
	if s == "" {
		return theme.MediaEntry{}
	}
	f := strings.SplitN(s, fontRowSep, 4)
	m := theme.MediaEntry{ID: f[0]}
	if len(f) > 1 {
		m.File = f[1]
	}
	if len(f) > 2 {
		m.Bytes, _ = strconv.ParseInt(f[2], 10, 64)
	}
	if len(f) > 3 {
		m.Hash = f[3]
	}
	return m
}

// applyMediaRow is applyFontRow's twin, over Sidecar.Media.
func (d *themeDoc) applyMediaRow(op themeEditOp) (themeEditOp, editApplyResult) {
	idx, ok := op.target.mediaIdx()
	if !ok || d.side == nil {
		return op, applyNoChange
	}
	cur := d.mediaRowAt(idx)
	want := d.str(op.after.s)
	if cur == want {
		return op, applyNoChange // the table already says that: not an edit, not a refusal
	}
	op.before = editValueStr(d.intern(cur))
	if !d.writeMediaRow(idx, want) {
		return op, applyRefused // theme.MediaCap, or an index past the append slot
	}
	return op, applyDone
}

// mediaRowAt reads one row's encoded value, "" for a row that does not exist.
func (d *themeDoc) mediaRowAt(idx int) string {
	if d.side == nil || idx < 0 || idx >= len(d.side.Media) {
		return ""
	}
	return mediaRowValue(d.side.Media[idx])
}

// writeMediaRow is the ONE writer of the [media] table. An empty value REMOVES the
// row; an index at the end APPENDS.
//
// THE CAP REFUSES, IT DOES NOT EVICT, exactly as writeFontRow, setOverrideRow and
// insertAt do: a document that dropped somebody else's picture to make room for this
// one would save a theme whose elements point at nothing.
func (d *themeDoc) writeMediaRow(idx int, v string) bool {
	m := mediaRowOf(v)
	switch {
	case idx >= 0 && idx < len(d.side.Media):
		if m.ID == "" {
			d.side.Media = append(d.side.Media[:idx], d.side.Media[idx+1:]...)
		} else {
			d.side.Media[idx] = m
		}
	case idx == len(d.side.Media) && m.ID != "":
		if len(d.side.Media) >= theme.MediaCap {
			return false
		}
		d.side.Media = append(d.side.Media, m)
	default:
		return false
	}
	d.dirty = true
	return true
}

// ---------------------------------------------------------------------------
// The TYPE tables — [fonts] and [fontbind]
// ---------------------------------------------------------------------------

// fontRowSep joins a family and its file inside ONE interned string, so a [fonts] row
// fits the fixed-size editValue that makes the ring allocation-free.
//
// U+001F (unit separator) because INIDoc refuses control characters in a value, so
// neither half can ever contain it — the encoding cannot be ambiguous for any string
// the reader would accept, which is the property a hand-picked printable separator
// (':', '|') could not promise.
const fontRowSep = "\x1f"

// fontRowValue / fontRowOf are the ONE encoding of a [fonts] row, a pair for the same
// reason editValueSlotRect / slotRectOf are: two halves packed in one order and
// unpacked in another compiles and is silently wrong.
func fontRowValue(family, file string) string {
	if family == "" {
		return ""
	}
	return family + fontRowSep + file
}

func fontRowOf(s string) (family, file string) {
	if i := strings.Index(s, fontRowSep); i >= 0 {
		return s[:i], s[i+len(fontRowSep):]
	}
	return s, ""
}

// typeSigOffset64 / typeSigPrime64 are FNV-1a's published parameters (hard rule 9:
// named, because nobody may tune them). A 64-bit non-cryptographic hash is the right
// tool here and the choice is bounded by its use: the value is compared only against
// another run of this same function inside one editing session, it is never stored,
// never written to a theme and never a trust boundary.
const (
	typeSigOffset64 uint64 = 14695981039346656037
	typeSigPrime64  uint64 = 1099511628211
)

// typeSig signs the TYPE tables — [fonts] and [fontbind] — and nothing else.
//
// THE NARROWNESS IS THE POINT. Its one consumer asks "would a theme apply resolve
// different FACES than the ones on screen?", and applySidecarFonts reads exactly these
// two tables. Signing the whole document instead would answer "did anything change",
// which is the question that made every save restart the theme's motion.
func (d *themeDoc) typeSig() uint64 {
	h := typeSigOffset64
	if d == nil || d.side == nil {
		return h
	}
	for i := range d.side.Fonts {
		h = typeSigFold(h, d.side.Fonts[i].Family)
		h = typeSigFold(h, d.side.Fonts[i].File)
	}
	for i := range d.side.FontBind {
		h = typeSigFold(h, d.side.FontBind[i].Key)
		h = typeSigFold(h, d.side.FontBind[i].Value)
	}
	return h
}

// typeSigFold folds one string plus a TERMINATOR into the running hash.
//
// The terminator is load-bearing: without it {"ab","c"} and {"a","bc"} sign alike, so
// moving one character between a family and its file would read as no change at all.
// It is fontRowSep's byte for the reason that constant already argues — INIDoc refuses
// control characters in a value, so it cannot occur inside either half.
func typeSigFold(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * typeSigPrime64
	}
	return (h ^ uint64(fontRowSep[0])) * typeSigPrime64
}

// typeSigFoldByte folds ONE byte — an enum member, a flag — into the running hash,
// through the same fold so a signature can mix scalars and strings without two
// disciplines. Its caller is themeArtSig (thememedia.go), which signs `kind` and
// `hidden` beside four strings.
func typeSigFoldByte(h uint64, b uint8) uint64 {
	return (h ^ uint64(b)) * typeSigPrime64
}

// boolByte is a flag as one hashable byte. Named beside boolToInt32 (themeeditorop.go)
// rather than inlined at the fold, because an inline `if` inside a hash loop is where a
// polarity inversion hides.
func boolByte(on bool) uint8 {
	if on {
		return 1
	}
	return 0
}

// applyFontRow writes one row of a type table and returns its inverse.
//
// ONE ARM FOR BOTH TABLES, because they are the same operation over two []KV-shaped
// stores: read the current value at an index, refuse if a cap says no, write, and hand
// back what was there. Two arms would be two chances to get the add-versus-replace
// boundary wrong, and that boundary is the whole of undoing an add.
func (d *themeDoc) applyFontRow(op themeEditOp) (themeEditOp, editApplyResult) {
	idx, ok := op.target.fontIdx()
	if !ok || d.side == nil {
		return op, applyNoChange
	}
	cur := d.fontRowAt(op.kind, idx)
	want := d.str(op.after.s)
	if cur == want {
		return op, applyNoChange // the table already says that: not an edit, not a refusal
	}
	op.before = editValueStr(d.intern(cur))
	if !d.writeFontRow(op.kind, idx, want) {
		return op, applyRefused // theme.FontCap / FontBindCap, or an index past the end
	}
	return op, applyDone
}

// fontRowAt reads one row's encoded value, "" for a row that does not exist.
func (d *themeDoc) fontRowAt(kind editOpKind, idx int) string {
	if kind == opFontBind {
		if idx < 0 || idx >= len(theme.FontElements) {
			return ""
		}
		fam, _ := d.side.BoundFamily(theme.FontElements[idx])
		return fam
	}
	if idx < 0 || idx >= len(d.side.Fonts) {
		return ""
	}
	return fontRowValue(d.side.Fonts[idx].Family, d.side.Fonts[idx].File)
}

// writeFontRow is the ONE writer of both tables. An empty value REMOVES the row; an
// index at the end APPENDS. Returns false for a refusal — a cap, or an index that is
// neither an existing row nor the append slot.
//
// THE CAP REFUSES, IT DOES NOT EVICT, exactly as setOverrideRow and insertAt do: a
// document that dropped somebody else's family to make room for this one would save a
// theme whose bindings point at nothing.
func (d *themeDoc) writeFontRow(kind editOpKind, idx int, v string) bool {
	if kind == opFontBind {
		if idx < 0 || idx >= len(theme.FontElements) {
			return false
		}
		return d.writeFontBind(theme.FontElements[idx], v)
	}
	fam, file := fontRowOf(v)
	switch {
	case idx >= 0 && idx < len(d.side.Fonts):
		if fam == "" {
			d.side.Fonts = append(d.side.Fonts[:idx], d.side.Fonts[idx+1:]...)
		} else {
			d.side.Fonts[idx] = theme.FontFamily{Family: fam, File: file}
		}
	case idx == len(d.side.Fonts) && fam != "":
		if len(d.side.Fonts) >= theme.FontCap {
			return false
		}
		d.side.Fonts = append(d.side.Fonts, theme.FontFamily{Family: fam, File: file})
	default:
		return false
	}
	d.dirty = true
	return true
}

// writeFontBind upserts (or removes) one [fontbind] row.
func (d *themeDoc) writeFontBind(class, family string) bool {
	for i := range d.side.FontBind {
		if d.side.FontBind[i].Key != class {
			continue
		}
		if family == "" {
			d.side.FontBind = append(d.side.FontBind[:i], d.side.FontBind[i+1:]...)
		} else {
			d.side.FontBind[i].Value = family
		}
		d.dirty = true
		return true
	}
	if family == "" {
		return false // nothing to remove: the caller's no-op check should have caught it
	}
	if len(d.side.FontBind) >= theme.FontBindCap {
		return false
	}
	d.side.FontBind = append(d.side.FontBind, theme.KV{Key: class, Value: family})
	d.dirty = true
	return true
}

// ---------------------------------------------------------------------------
// The AO2 tier's geometry — [overrides], the OTHER sanctioned way to move a widget
// ---------------------------------------------------------------------------
//
// WHY THE EDITOR WRITES A ROW AND THE Ctrl+F2 OVERLAY WRITES A PREFERENCE. Design
// §"Where geometry is written" splits them by OWNERSHIP: a live tweak of a theme you
// merely use is yours alone and belongs in ThemeRectOv; editing a theme you own is
// authoring, and belongs in the theme's own [overrides] so the author's
// courtroom_design.ini stays byte-identical to what they shipped. Two tools, two
// stores, and the editor never writes a preference — so nothing it does can change
// how a theme behaves for somebody who only uses it.

// slotEditable reports that a design key is one this document may author.
//
// It is sidecarOverrideTarget's first two gates (themeoverrides.go), asked of the
// LIVE map rather than the apply goroutine's: editable, and actually placed by this
// build. The tab strip is refused on top of them — see editorSlotKeyEditable
// (themeeditorcanvas.go) for that argument, which is about ownership rather than
// about geometry and therefore lives with the probe that enforces it.
func (d *themeDoc) slotEditable(key string) bool {
	if d == nil || d.live == nil || key == "" || !themeKeyEditable(key) {
		return false
	}
	_, placed := d.live[key]
	return placed
}

// slotRect is a widget's CURRENT authored geometry: the document's own row when it
// has one, else the resolved rect. The row wins because while the editor is open the
// document is the authoritative model (see reclaimThemeDoc).
func (d *themeDoc) slotRect(key string) (theme.Rect, bool) {
	if !d.slotEditable(key) {
		return theme.Rect{}, false
	}
	if r, ok := d.side.OverrideRect(key); ok {
		return r, true
	}
	return d.live[key], true
}

// applySlotRect writes one widget's rect to BOTH places (see themeDoc.live) and
// returns the inverse, including whether a row existed at all.
func (d *themeDoc) applySlotRect(op themeEditOp) (themeEditOp, editApplyResult) {
	key := op.target.designKey()
	if !d.slotEditable(key) {
		return op, applyNoChange // not a widget this document may author: nothing was prevented
	}
	cur, _ := d.slotRect(key)
	_, hadRow := d.side.OverrideRect(key)
	want, _ := slotRectOf(op.after)
	if want == cur {
		// A REQUEST THAT CHANGES NOTHING IS NOT AN EDIT — the same rule the element
		// field arm follows, and here it also means a plain CLICK on a widget (press,
		// no movement, release) never writes a row. Marking a theme edited because
		// somebody selected a box would be a diff nobody asked for.
		//
		// AND IT IS NOT A REFUSAL EITHER, which is the half that shipped wrong: every
		// frame of a drag that holds still lands here, as does every frame a drag sits
		// against clampDesignRectToCanvas, so answering "refused" put a four-second
		// "a cap or a lock is in the way" chip on the rail for the most ordinary
		// gesture in the editor. See editApplyResult.
		return op, applyNoChange
	}
	op.before = editValueSlotRect(cur, hadRow)
	if !d.writeSlotRect(key, want) {
		return op, applyRefused // theme.OverrideCap: refuse loudly rather than drop the edit
	}
	return op, applyDone
}

// applySlotReset removes this document's [overrides] row for a widget, putting the
// geometry back to what the tiers below resolved when the editor opened.
//
// A ROW THAT WAS NEVER THERE IS NOT A RESET, and answering applyNoChange for it is
// the same ruling applySlotRect's equal-rect arm makes: pressing Reset on an
// untouched widget must not mark the theme edited, and must not chip a refusal for a
// limit that is not there.
func (d *themeDoc) applySlotReset(op themeEditOp) (themeEditOp, editApplyResult) {
	key := op.target.designKey()
	if !d.slotEditable(key) {
		return op, applyNoChange
	}
	cur, _ := d.slotRect(key)
	if _, hadRow := d.side.OverrideRect(key); !hadRow {
		return op, applyNoChange
	}
	base, ok := d.baseLive[key]
	if !ok {
		base = cur // no baseline for a key that arrived mid-session: keep the pixels put
	}
	op.before = editValueSlotRect(cur, true)
	op.after = editValueSlotRect(base, false)
	d.dropOverrideRow(key)
	d.live[key] = base
	d.dirty = true
	return op, applyDone
}

// revertSlotRect puts a widget back. Restoring a row that did not exist is the case
// the flag exists for: the row is REMOVED and the geometry goes back to what the
// tiers below resolved when the editor opened.
func (d *themeDoc) revertSlotRect(op themeEditOp) bool {
	key := op.target.designKey()
	if !d.slotEditable(key) {
		return false
	}
	r, hadRow := slotRectOf(op.before)
	if hadRow {
		return d.writeSlotRect(key, r)
	}
	d.dropOverrideRow(key)
	if base, ok := d.baseLive[key]; ok {
		d.live[key] = base
	}
	d.dirty = true
	return true
}

// writeSlotRect is the ONE writer of a widget's geometry: the row and the live map,
// in one call, so the two cannot be updated apart.
func (d *themeDoc) writeSlotRect(key string, r theme.Rect) bool {
	if !d.setOverrideRow(key, r) {
		return false
	}
	d.live[key] = r
	d.dirty = true
	return true
}

// setOverrideRow upserts an [overrides] row. THE CAP REFUSES, IT DOES NOT EVICT —
// the same ruling the reader follows (TestSidecarCapsRefuseNotTruncate): a document
// that silently dropped somebody else's row to make space for this drag would save a
// theme missing a widget nobody touched.
func (d *themeDoc) setOverrideRow(key string, r theme.Rect) bool {
	for i := range d.side.Overrides {
		if d.side.Overrides[i].Key == key {
			d.side.Overrides[i].Rect = r
			return true
		}
	}
	if len(d.side.Overrides) >= theme.OverrideCap {
		return false
	}
	d.side.Overrides = append(d.side.Overrides, theme.KeyRect{Key: key, Rect: r})
	return true
}

// dropOverrideRow removes a row the document added.
func (d *themeDoc) dropOverrideRow(key string) {
	for i := range d.side.Overrides {
		if d.side.Overrides[i].Key == key {
			d.side.Overrides = append(d.side.Overrides[:i], d.side.Overrides[i+1:]...)
			return
		}
	}
}

// revert writes an op's `before` back. It is apply's mirror and shares its writer,
// so the two can never disagree about what a field means.
func (d *themeDoc) revert(op themeEditOp) bool {
	switch op.kind {
	case opElemField:
		el := d.element(op.target)
		if el == nil || int(op.field) >= len(editFieldSet) || editFieldSet[op.field] == nil {
			return false
		}
		editFieldSet[op.field](d, el, op.before)
		d.dirty = true
		return true
	case opElemDel:
		// Undoing a DELETE is an insert of the parked body.
		return d.insertAt(op.target, op.before)
	case opElemAdd:
		// Undoing an ADD is a delete, and the body must survive it — a redo has to
		// put back the same element, not a fresh blank one.
		_, ok := d.removeAt(op.target, op.after)
		return ok
	case opSlotRect, opSlotReset:
		// ONE INVERSE FOR BOTH, and it is not a coincidence: revertSlotRect is
		// "restore the row named in `before`, or drop it when there was none", which is
		// exactly what undoing a reset needs (the reset's `before` always carries the
		// row it removed).
		return d.revertSlotRect(op)
	case opFontFamily, opFontBind:
		idx, ok := op.target.fontIdx()
		return ok && d.writeFontRow(op.kind, idx, d.str(op.before.s))
	case opMediaRow:
		idx, ok := op.target.mediaIdx()
		return ok && d.writeMediaRow(idx, d.str(op.before.s))
	}
	return false
}

// redo re-applies an op that was undone. For a field it is apply's write half
// without the read (op.before is already correct); for the structural kinds it is
// the same insert/remove pair the other way round.
func (d *themeDoc) redo(op themeEditOp) bool {
	switch op.kind {
	case opElemField:
		el := d.element(op.target)
		if el == nil || int(op.field) >= len(editFieldSet) || editFieldSet[op.field] == nil {
			return false
		}
		editFieldSet[op.field](d, el, op.after)
		d.dirty = true
		return true
	case opElemDel:
		_, ok := d.removeAt(op.target, op.before)
		return ok
	case opElemAdd:
		return d.insertAt(op.target, op.after)
	case opSlotRect:
		r, _ := slotRectOf(op.after)
		return d.writeSlotRect(op.target.designKey(), r)
	case opSlotReset:
		// Redoing a reset DROPS the row again. Writing `after` into one instead would
		// re-create the very row the reset removed, so a redo would leave the file
		// different from the state it claims to restore.
		key := op.target.designKey()
		if !d.slotEditable(key) {
			return false
		}
		r, _ := slotRectOf(op.after)
		d.dropOverrideRow(key)
		d.live[key] = r
		d.dirty = true
		return true
	case opFontFamily, opFontBind:
		idx, ok := op.target.fontIdx()
		return ok && d.writeFontRow(op.kind, idx, d.str(op.after.s))
	case opMediaRow:
		idx, ok := op.target.mediaIdx()
		return ok && d.writeMediaRow(idx, d.str(op.after.s))
	}
	return false
}

// applyDelete removes an element, parking its body in the graveyard so undo can
// resurrect it byte-for-byte rather than approximately.
func (d *themeDoc) applyDelete(op themeEditOp) (themeEditOp, editApplyResult) {
	slot, ok := d.graveSlot()
	if !ok {
		// A NAMED BOUND (themeElemGraveCap): refuse the delete rather than lose the body,
		// and say so — this is the case the chip exists for.
		return op, applyRefused
	}
	op.before = editValueInt(int32(slot))
	if _, ok := d.removeAt(op.target, op.before); !ok {
		return op, applyNoChange // no element at that index any more: nothing was prevented
	}
	return op, applyDone
}

// applyAdd appends a new element. The BODY is handed in through the graveyard slot
// named by op.after, so add and undo-of-delete are one insert.
func (d *themeDoc) applyAdd(op themeEditOp) (themeEditOp, editApplyResult) {
	if !d.insertAt(op.target, op.after) {
		// THREE CAUSES, and only two of them are bounds: theme.ElementCap, a body the
		// graveyard no longer holds, or a target index that no longer exists. All three
		// are refusals rather than no-ops because an add that vanished without a word is
		// the worst of both — but see editApplyResult for why applyRefused does not
		// promise that a CAP was the reason.
		return op, applyRefused
	}
	return op, applyDone
}

// graveSlot claims the next graveyard slot. The graveyard is drained on save and on
// editor close; inside a session it fills to its cap and then refuses, which is
// what keeps a delete from ever being lossy (hard rule 4: bounded, and the bound
// has a behaviour).
func (d *themeDoc) graveSlot() (int, bool) {
	if d.graveN >= len(d.grave) {
		return 0, false
	}
	d.graveN++
	return d.graveN - 1, true
}

// park stores a body and returns the value naming it.
func (d *themeDoc) park(slot int, el theme.Element) editValue {
	if slot >= 0 && slot < len(d.grave) {
		d.grave[slot] = el
	}
	return editValueInt(int32(slot))
}

// buried reads a parked body back.
func (d *themeDoc) buried(v editValue) (theme.Element, bool) {
	slot := int(v.i[0])
	if slot < 0 || slot >= len(d.grave) {
		return theme.Element{}, false
	}
	return d.grave[slot], true
}

// removeAt deletes the element at the target's index, parking its body in the slot
// v names. IDENTITY IS THE INDEX (theme.Element's own doc comment), so a removal
// renumbers every element after it — which is exactly why an op that outlives a
// structural change is not replayable, and why the ring is reset on theme change.
//
// THE CARRIER LOSES THE SECTION TOO, and it did not until W9. The writer edits the
// lossless document the reader kept, so an element taken out of the model and
// nowhere else is re-emitted from its own surviving [element.<id>] section on the
// next save and read straight back in — a delete that looked like it worked until
// the theme was reopened. theme.Sidecar.RetireElementSection is the narrow act
// (one named id, never a prune); the removal happens at WRITE time, and insertAt
// cancels it, so undoing a delete still restores the file byte-identically.
func (d *themeDoc) removeAt(t editTarget, v editValue) (theme.Element, bool) {
	idx, ok := t.elemIdx()
	if !ok || d.side == nil || idx < 0 || idx >= len(d.side.Elements) {
		return theme.Element{}, false
	}
	el := d.side.Elements[idx]
	d.park(int(v.i[0]), el)
	d.side.Elements = append(d.side.Elements[:idx], d.side.Elements[idx+1:]...)
	d.side.RetireElementSection(el.ID)
	d.dirty = true
	return el, true
}

// insertAt puts a parked body back at the target's index.
//
// THE CAP REFUSES, IT DOES NOT TRUNCATE — the same ruling the reader follows
// (design §3.3, TestSidecarCapsRefuseNotTruncate): an insert past theme.ElementCap
// fails loudly here so the UI can name the cap, rather than succeeding into a model
// that Sidecar.Bytes() would then refuse to save.
func (d *themeDoc) insertAt(t editTarget, v editValue) bool {
	idx, ok := t.elemIdx()
	if !ok || d.side == nil || idx < 0 || idx > len(d.side.Elements) {
		return false
	}
	if len(d.side.Elements) >= theme.ElementCap {
		return false
	}
	el, ok := d.buried(v)
	if !ok {
		return false
	}
	d.side.Elements = append(d.side.Elements, theme.Element{})
	copy(d.side.Elements[idx+1:], d.side.Elements[idx:])
	d.side.Elements[idx] = el
	// The section was marked for deletion but not yet deleted (removeAt's note), so
	// putting the element back takes the mark off and the author's own lines — key
	// order, spacing, comments — survive the round trip untouched.
	d.side.UnretireElementSection(el.ID)
	d.dirty = true
	return true
}

// rebaseline re-takes the snapshot restoreBaseline restores to. ONE CALLER, and it
// is the only event that may move the baseline: landEditorSave, after a write landed
// (themeeditorsave.go).
//
// WHY "DISCARD" MEANS "BACK TO THE LAST SAVE" AND NOT "BACK TO WHEN I OPENED THIS".
// The baseline used to be taken once, at open, and nothing moved it — so
// edit → Save → edit → Back → Back left MEMORY at the pre-save document while DISK
// held the save. Two states for one theme: the file on disk is the theme, the model
// in memory is what the courtroom keeps drawing (closeThemeEditor drops the document,
// not the model), and they disagreed until the next theme apply happened to read the
// file back. The user's own words for the gesture are "throw away what I have not
// saved", and that is what this makes true.
//
// typeSigApplied is not touched here and must not be: its one writer after open is
// applyThemeAfterSaveIfTypeChanged, which runs in the same breath as this and sets it
// only when it actually kicks an apply. Re-taking it here would claim an apply that
// never happened. The tables it signs are the same tables this just snapshotted, so
// after a save the baseline, the model, the file and the signature all agree — which
// is the property the old shape lost.
//
// ALL OF IT OR NONE OF IT, exactly like restoreBaseline: a baseline where the
// elements had moved on but the type tables had not is a discard that half-works.
func (d *themeDoc) rebaseline() {
	if d == nil || d.side == nil {
		return
	}
	d.baseElems = append([]theme.Element(nil), d.side.Elements...)
	d.baseOv = append([]theme.KeyRect(nil), d.side.Overrides...)
	d.baseFonts = append([]theme.FontFamily(nil), d.side.Fonts...)
	d.baseBind = append([]theme.KV(nil), d.side.FontBind...)
	// The RESOLVED geometry moves with the model. baseLive is what a discard puts on
	// screen and what applySlotReset falls back to, so leaving it at the open-time
	// rects while the rows advance to the saved ones would put the pixels and the
	// table that produced them into disagreement — the same two-states defect one
	// layer down.
	if d.live != nil {
		d.baseLive = cloneRects(d.live)
	}
}

// restoreBaseline puts the document back exactly as it stood at the BASELINE — the
// model AND the resolved geometry. The baseline is the editor's open state until a
// save lands, and the last save after that (see rebaseline).
//
// IT IS THE EXIT-WITHOUT-SAVING PATH, and it is deliberately not an undo replay.
// The ring is capped, so replaying it would restore most of a long session and stay
// silent about the rest; a snapshot is all-or-nothing, which is the only honest
// answer to "throw my changes away". It also cannot fail part-way: three
// assignments, no I/O, no validation — the document it produces is one that was
// already live, so it is saveable by construction.
//
// IT IS THE RETIRE SEAM'S SECOND EXIT, and for one wave it was an uncovered one.
// A delete has two halves — the model's and the CARRIER'S (removeAt's note) — and
// the second is deferred to the next write as a pending retirement. insertAt
// cancels it, because the per-element undo is one of the two ways an element comes
// back; this is the other, and it restored the model while leaving the retirement
// standing. The consequence outlives the session: delete, then "Back, Back", and
// the very next Sidecar.Bytes still strips the author's [element.<id>] lines from
// where they were written and re-emits the section canonically at the end of the
// file. Element declaration order is reload order, so a DISCARD silently reordered
// the theme and dropped that one section's key order and comments — and it reaches
// disk from three places (the editor's own save, internal/themepack's copy and its
// create), because a.themeSidecar and d.side are the same object and
// closeThemeEditor drops only the editor.
func (d *themeDoc) restoreBaseline() {
	if d == nil || d.side == nil {
		return
	}
	d.side.Elements = append([]theme.Element(nil), d.baseElems...)
	d.side.Overrides = append([]theme.KeyRect(nil), d.baseOv...)
	d.side.Fonts = append([]theme.FontFamily(nil), d.baseFonts...)
	d.side.FontBind = append([]theme.KV(nil), d.baseBind...)
	// Over the RESTORED list rather than over a remembered set of deletions: the
	// snapshot is the whole truth about which elements the theme has, so "every id
	// that is back" is exactly the set whose sections must stay. An id the baseline
	// does not carry keeps its retirement, which is right — a delete of an element
	// that was authored AND saved this session is a delete the baseline agrees with.
	for i := range d.side.Elements {
		d.side.UnretireElementSection(d.side.Elements[i].ID)
	}
	for k, v := range d.baseLive {
		if _, placed := d.live[k]; placed {
			d.live[k] = v
		}
	}
	d.graveN = 0
	d.dirty = false
}

// bytes serialises the document THROUGH THE GUARD. It is one line, and it exists so
// that the editor has exactly one spelling of "save this" — Sidecar.Bytes validates
// first and refuses before touching the lossless carrier, which is the property
// that makes the save path safe by construction (internal/theme's own API census
// keeps every other route closed).
func (d *themeDoc) bytes() ([]byte, error) {
	if d == nil || d.side == nil {
		return nil, nil
	}
	return d.side.Bytes()
}
