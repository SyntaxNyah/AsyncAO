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
	// applyRefused: a NAMED bound said no — theme.OverrideCap, theme.ElementCap, or a
	// full undo graveyard. The user asked for something the document cannot do, which
	// is exactly the case that must never be a bare "failed".
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
	}
	return op, applyNoChange
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
	case opSlotRect:
		return d.revertSlotRect(op)
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
		// theme.ElementCap, or a body the graveyard no longer holds: a bound said no,
		// and an add that vanished without a word is the worst of both.
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
func (d *themeDoc) removeAt(t editTarget, v editValue) (theme.Element, bool) {
	idx, ok := t.elemIdx()
	if !ok || d.side == nil || idx < 0 || idx >= len(d.side.Elements) {
		return theme.Element{}, false
	}
	el := d.side.Elements[idx]
	d.park(int(v.i[0]), el)
	d.side.Elements = append(d.side.Elements[:idx], d.side.Elements[idx+1:]...)
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
	d.dirty = true
	return true
}

// restoreBaseline puts the document back exactly as it stood when the editor
// opened — the model AND the resolved geometry.
//
// IT IS THE EXIT-WITHOUT-SAVING PATH, and it is deliberately not an undo replay.
// The ring is capped, so replaying it would restore most of a long session and stay
// silent about the rest; a snapshot is all-or-nothing, which is the only honest
// answer to "throw my changes away". It also cannot fail part-way: three
// assignments, no I/O, no validation — the document it produces is one that was
// already live, so it is saveable by construction.
func (d *themeDoc) restoreBaseline() {
	if d == nil || d.side == nil {
		return
	}
	d.side.Elements = append([]theme.Element(nil), d.baseElems...)
	d.side.Overrides = append([]theme.KeyRect(nil), d.baseOv...)
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
