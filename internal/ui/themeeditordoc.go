package ui

// What the editor is editing and the only way to change it; nothing here draws, so it all runs without a renderer.
// pollThemeApply replaces a.themeSidecar wholesale and applies are not user-initiated, so reclaimThemeDoc re-installs
// this model after every apply or unsaved edits would vanish.
// themeDoc.apply is the only writer of an edited element's fields, and it returns the op with before filled in,
// so a field cannot be edited without producing its inverse.

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/text"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// themeElemGraveCap bounds the deleted-element bodies undo can bring back; it equals the ring size because only
	// the ring can ask for one, so larger is unreachable and smaller makes undo lossy.
	themeElemGraveCap = themeEditUndoCap

	// editSlotRowPresent flags, in the b[0] lane, that an [overrides] row already existed for this key:
	// without it an undo restores the numbers but leaves a row the user took back.
	editSlotRowPresent uint8 = 1
)

// editValueSlotRect and slotRectOf are the one encoding of an opSlotRect payload, so the lane order cannot diverge.
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

// editField names one addressable field, indexing the get/set tables; append only, because the inspector table
// is indexed by these and a reorder would re-point its rows.
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

	// Identity and fill detail; appended per this enum's rule.
	editFieldKind
	editFieldGenParams
	editFieldSlice
	editFieldNoDecimate

	// An element's id is its section suffix ([element.<id>]), so renaming must move the section or a save
	// writes two elements; the setter goes through theme.Sidecar.RenameElement, and nothing else in this package may.
	editFieldElemID

	// editFieldCount sizes the get/set tables: fixed arrays, not maps, because they are read during a drag.
	editFieldCount
)

// editFieldNames is the field's label in diagnostics, not the inspector's (two rows may address one field).
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

// editFieldGet reads one field into the fixed-size payload; a table, so a field with no reader is a visible nil entry.
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
	// Read through the format's own writer, so a save that changed nothing cannot rewrite the author's line.
	editFieldGenParams: func(d *themeDoc, e *theme.Element) editValue {
		return editValueStr(d.intern(theme.WriteGenParams(e)))
	},
	editFieldSlice: func(_ *themeDoc, e *theme.Element) editValue {
		return editValueRect(int32(e.Slice[0]), int32(e.Slice[1]), int32(e.Slice[2]), int32(e.Slice[3]))
	},
	// Stored inverted so the zero value is version 1's behaviour; the row reads the way the file spells it.
	editFieldNoDecimate: func(_ *themeDoc, e *theme.Element) editValue { return editValueBool(!e.NoDecimate) },

	editFieldElemID: func(d *themeDoc, e *theme.Element) editValue { return editValueStr(d.intern(e.ID)) },
}

// editFieldSet writes one field, clamped to the format's range: Sidecar.Bytes refuses an out-of-grammar model,
// and autosave relies on the document always being saveable.
var editFieldSet = [editFieldCount]func(*themeDoc, *theme.Element, editValue){
	editFieldRect: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Rect = theme.Rect{X: int(v.i[0]), Y: int(v.i[1]), W: int(v.i[2]), H: int(v.i[3])}
	},
	editFieldRot: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Rot = uint8(v.i[0] & (theme.AngleCount - 1))
	},
	editFieldZ: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Z = int16(value.Clamp(v.i[0], theme.ZMin, theme.ZMax))
	},
	editFieldBand: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Band = theme.ElementBand(value.Clamp(v.i[0], 0, int32(theme.ElementBandCount)-1))
	},
	editFieldSpace: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Space = theme.Space(value.Clamp(v.i[0], 0, int32(theme.SpaceCount)-1))
	},
	editFieldAnchor: func(d *themeDoc, e *theme.Element, v editValue) { e.Anchor = d.str(v.s) },

	editFieldMedia: func(d *themeDoc, e *theme.Element, v editValue) { e.Media = d.str(v.s) },
	editFieldShape: func(d *themeDoc, e *theme.Element, v editValue) { e.Shape = d.str(v.s) },
	editFieldGen:   func(d *themeDoc, e *theme.Element, v editValue) { e.Gen = d.str(v.s) },
	editFieldFit: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Fit = theme.ElementFit(value.Clamp(v.i[0], 0, int32(theme.FitCount)-1))
	},
	editFieldFill:  func(_ *themeDoc, e *theme.Element, v editValue) { e.Fill = valueRGBA(v) },
	editFieldFill2: func(_ *themeDoc, e *theme.Element, v editValue) { e.Fill2 = valueRGBA(v) },
	editFieldGradAngle: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.GradAngle = uint8(v.i[0] & (theme.AngleCount - 1))
	},
	editFieldGradRadial: func(_ *themeDoc, e *theme.Element, v editValue) { e.GradRadial = v.i[0] != 0 },
	editFieldStroke:     func(_ *themeDoc, e *theme.Element, v editValue) { e.Stroke = valueRGBA(v) },
	editFieldStrokePx: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.StrokePx = uint8(value.Clamp(v.i[0], 0, 255))
	},
	editFieldTint: func(_ *themeDoc, e *theme.Element, v editValue) { e.Tint = valueRGBA(v) },
	editFieldOpacity: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Opacity = uint8(value.Clamp(v.i[0], 0, 255))
	},

	editFieldText: func(d *themeDoc, e *theme.Element, v editValue) {
		e.Text = text.Truncate(d.str(v.s), theme.TextRuneCap)
	},
	editFieldFont: func(d *themeDoc, e *theme.Element, v editValue) { e.Font = d.str(v.s) },
	editFieldSize: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Size = int(value.Clamp(v.i[0], 0, theme.TextSizeMaxPt))
	},
	editFieldColor: func(_ *themeDoc, e *theme.Element, v editValue) { e.Color = valueRGBA(v) },
	editFieldAlign: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Align = theme.TextAlign(value.Clamp(v.i[0], 0, int32(theme.AlignCount)-1))
	},

	editFieldClock: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Clock = uint8(value.Clamp(v.i[0], 0, int32(theme.ClockCap)-1))
	},
	editFieldPhaseMs: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.PhaseMs = value.Clamp(v.i[0], theme.TimeMsMin, theme.TimeMsMax)
	},
	editFieldLoop: func(_ *themeDoc, e *theme.Element, v editValue) { e.Loop = v.i[0] != 0 },
	editFieldEffect: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Effect = theme.EffectKind(value.Clamp(v.i[0], 0, int32(theme.EffectKindCount)-1))
	},
	editFieldEffectPeriodMs: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.EffectPeriodMs = value.Clamp(v.i[0], theme.TimeMsMin, theme.TimeMsMax)
	},
	editFieldEffectAmpPct: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.EffectAmpPct = int16(value.Clamp(v.i[0], 0, theme.AmpMaxPct))
	},

	editFieldVisibleAxis: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.VisibleWhen.Axis = theme.ConditionAxis(value.Clamp(v.i[0], 0, int32(theme.ConditionAxisCount)-1))
	},
	editFieldVisibleValue: func(d *themeDoc, e *theme.Element, v editValue) {
		e.VisibleWhen.Value = text.Truncate(d.str(v.s), theme.ConditionValueRuneCap)
	},
	editFieldHidden: func(_ *themeDoc, e *theme.Element, v editValue) { e.Hidden = v.i[0] != 0 },
	editFieldLocked: func(_ *themeDoc, e *theme.Element, v editValue) { e.Locked = v.i[0] != 0 },

	editFieldKind: func(_ *themeDoc, e *theme.Element, v editValue) {
		e.Kind = theme.ElementKind(value.Clamp(v.i[0], 0, int32(theme.ElementKindCount)-1))
	},
	editFieldGenParams: func(d *themeDoc, e *theme.Element, v editValue) {
		// Uses the format's own parser; the draw site refuses a string past GenParamCap, so the error only arises
		// for an op replayed against a changed document, where the partial array is the honest fallback.
		p, _ := theme.ParseGenParams(d.str(v.s))
		e.GenParams = p
	},
	// theme.ZMin/ZMax is the reader's own bound on a slice inset; a different pair would stop the file round-tripping.
	editFieldSlice: func(_ *themeDoc, e *theme.Element, v editValue) {
		for i := range e.Slice {
			e.Slice[i] = int16(value.Clamp(v.i[i], theme.ZMin, theme.ZMax))
		}
	},
	editFieldNoDecimate: func(_ *themeDoc, e *theme.Element, v editValue) { e.NoDecimate = v.i[0] == 0 },

	// Never assign e.ID: the id also lives in the carrier's [element.<id>] header, and only RenameElement moves both.
	// The error is dropped because editorWriteStringField asks first and shows the reason; a setter cannot refuse,
	// so what remains is a document changed under a replayed op, where leaving the model alone is right.
	editFieldElemID: func(d *themeDoc, e *theme.Element, v editValue) {
		_ = d.side.RenameElement(e.ID, d.str(v.s))
	},
}

// rgbaValue and valueRGBA are the one conversion between the format's colour and the payload's byte lane.
func rgbaValue(c theme.RGBA) editValue { return editValueRGBA(c.R, c.G, c.B, c.A) }
func valueRGBA(v editValue) theme.RGBA {
	return theme.RGBA{R: v.b[0], G: v.b[1], B: v.b[2], A: v.b[3]}
}

// themeDoc is everything the editor edits for one theme; side is the authoritative model while the editor is open.
// It holds no *theme.INIDoc because that would be a second writer beside the guarded Sidecar.Bytes().
type themeDoc struct {
	// name is the theme folder; the reclaim check keeps a document off a different theme switched to mid-session.
	name string
	side *theme.Sidecar
	// live is the applied design map, a.themeRects itself: an [overrides] edit must land in the row and the geometry,
	// or no pixel moves until a theme switch. Rebound by bindLive because pollThemeApply replaces the map.
	live map[string]theme.Rect
	// The baseline for exit-without-saving: a snapshot, because a replay of the capped undo ring could be partly right.
	// baseLive also answers what a widget's rect resolved to before a row existed, for undoing a row's creation.
	baseLive  map[string]theme.Rect
	baseElems []theme.Element
	baseOv    []theme.KeyRect
	// Type tables at open, so a discard cannot keep a session's font bindings while dropping its geometry.
	baseFonts []theme.FontFamily
	baseBind  []theme.KV
	// typeSigApplied is typeSig() at the last theme apply this document caused: a save asks whether the type changed
	// because only an apply resolves [fontbind] into faces, and an apply restarts every animated element.
	// Set only by newThemeDoc and applyThemeAfterSaveIfTypeChanged, each of which has just made it true.
	typeSigApplied uint64
	// dirty means there are unsaved edits; set by apply, cleared by a save.
	dirty bool

	// grave holds deleted elements' bodies for undo; an op is fixed-size, so it carries the index.
	grave  [themeElemGraveCap]theme.Element
	graveN int

	// strs is the session's interning table (see editStrCap); strs[0] is "", so the zero editValue means empty.
	strs [editStrCap]string
	strN int
}

// newThemeDoc opens a document over a live sidecar;
// nil for a stock AO2 theme, since adding a native tier is a user act.
func newThemeDoc(name string, sc *theme.Sidecar) *themeDoc {
	if sc == nil {
		return nil
	}
	d := &themeDoc{name: name, side: sc}
	d.strN = 1 // strs[0] is the empty string
	// The baseline, taken once at open; no frame path touches these copies.
	d.baseElems = append([]theme.Element(nil), sc.Elements...)
	d.baseOv = append([]theme.KeyRect(nil), sc.Overrides...)
	d.baseFonts = append([]theme.FontFamily(nil), sc.Fonts...)
	d.baseBind = append([]theme.KV(nil), sc.FontBind...)
	// The editor opens over the applied theme, so a session that never touches the type kicks no applies.
	d.typeSigApplied = d.typeSig()
	return d
}

// bindLive points the document at the applied design map; only the first bind takes the baseline,
// because a later one means the map was rebuilt under the session.
func (d *themeDoc) bindLive(rects map[string]theme.Rect) {
	if d == nil {
		return
	}
	d.live = rects
	if d.baseLive == nil && rects != nil {
		d.baseLive = cloneRects(rects)
	}
}

// intern returns the id of s, adding it if there is room; a full table returns editStrNone, which the op layer refuses.
// Linear scan: inspector edits only, and a map would allocate on every miss.
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

// str resolves an interned id; out of range is "" because a stale integer must not panic the render thread.
func (d *themeDoc) str(id editStrID) string {
	if id <= 0 || int(id) >= d.strN {
		return ""
	}
	return d.strs[id]
}

func (d *themeDoc) element(t editTarget) *theme.Element {
	idx, ok := t.elemIdx()
	if !ok || d == nil || d.side == nil || idx < 0 || idx >= len(d.side.Elements) {
		return nil
	}
	return &d.side.Elements[idx]
}

func (d *themeDoc) elementCount() int {
	if d == nil || d.side == nil {
		return 0
	}
	return len(d.side.Elements)
}

// read returns a field's current value; the inspector draws only from this.
func (d *themeDoc) read(t editTarget, f editField) (editValue, bool) {
	el := d.element(t)
	if el == nil || int(f) >= len(editFieldGet) || editFieldGet[f] == nil {
		return editValue{}, false
	}
	return editFieldGet[f](d, el), true
}

// editApplyResult separates "already that value" from "refused": a drag holding still must not chip a refusal.
// It is decided at the mutation, so a prior check cannot disagree with it about equality.
type editApplyResult uint8

const (
	// applyNoChange: nothing to do, or nothing at that target; silent, because a held pointer does this every frame.
	applyNoChange editApplyResult = iota
	// applyRefused: the document refused and the caller must say so. Usually a named cap, but insertAt also
	// fails on a stale index, so the cap is stated by the arms that raise it.
	applyRefused
	// applyDone: the model moved and op carries its own inverse.
	applyDone
)

// apply performs one mutation and returns its own inverse; anything but applyDone must not reach the undo ring.
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

// mediaRowValue and mediaRowOf are the one encoding of a [media] row; fontRowSep cannot occur in a value,
// and an empty id encodes to "", the table's "no row" value.
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

// applyMediaRow is applyFontRow over Sidecar.Media.
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

// mediaRowAt reads one row's encoded value, "" if it does not exist.
func (d *themeDoc) mediaRowAt(idx int) string {
	if d.side == nil || idx < 0 || idx >= len(d.side.Media) {
		return ""
	}
	return mediaRowValue(d.side.Media[idx])
}

// writeMediaRow is the one writer of [media]: empty removes, an end index appends, and the cap refuses rather
// than evicts, since dropping another picture would leave elements pointing at nothing.
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

// fontRowSep joins family and file in one interned string; INIDoc refuses control characters in a value,
// so U+001F cannot be ambiguous.
const fontRowSep = "\x1f"

// fontRowValue and fontRowOf are the one encoding of a [fonts] row.
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

// typeSigOffset64 and typeSigPrime64 are FNV-1a's published parameters; the hash is only compared within one session.
const (
	typeSigOffset64 uint64 = 14695981039346656037
	typeSigPrime64  uint64 = 1099511628211
)

// typeSig hashes only [fonts] and [fontbind], the tables applySidecarFonts reads, so other saves don't restart motion.
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

// typeSigFold folds a string plus a terminator; without it {"ab","c"} and {"a","bc"} hash alike.
func typeSigFold(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * typeSigPrime64
	}
	return (h ^ uint64(fontRowSep[0])) * typeSigPrime64
}

// typeSigFoldByte folds one byte through the same fold, for themeArtSig.
func typeSigFoldByte(h uint64, b uint8) uint64 {
	return (h ^ uint64(b)) * typeSigPrime64
}

// boolByte is a flag as one hashable byte; an inline if inside a hash loop is where a polarity inversion hides.
func boolByte(on bool) uint8 {
	if on {
		return 1
	}
	return 0
}

// applyFontRow writes one row of a type table and returns its inverse; one arm so add and replace can't diverge.
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

// fontRowAt reads one row's encoded value, "" if it does not exist.
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

// writeFontRow is the one writer of both tables: empty removes, an end index appends, false refuses.
// The cap refuses rather than evicts: dropping another family would leave bindings pointing at nothing.
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

// The editor writes [overrides] rows while the Ctrl+F2 overlay writes a preference: a tweak of a theme you use
// is yours alone, while editing one you own is authoring, and courtroom_design.ini stays byte-identical.

// slotEditable reports that a design key is editable and placed in the live map; the tab strip is refused by
// editorSlotKeyEditable.
func (d *themeDoc) slotEditable(key string) bool {
	if d == nil || d.live == nil || key == "" || !themeKeyEditable(key) {
		return false
	}
	_, placed := d.live[key]
	return placed
}

// slotRect is a widget's authored geometry: the document's row if it has one, else the resolved rect.
func (d *themeDoc) slotRect(key string) (theme.Rect, bool) {
	if !d.slotEditable(key) {
		return theme.Rect{}, false
	}
	if r, ok := d.side.OverrideRect(key); ok {
		return r, true
	}
	return d.live[key], true
}

// applySlotRect writes one widget's rect to the row and the live map, and returns the inverse.
func (d *themeDoc) applySlotRect(op themeEditOp) (themeEditOp, editApplyResult) {
	key := op.target.designKey()
	if !d.slotEditable(key) {
		return op, applyNoChange // not a widget this document may author: nothing was prevented
	}
	cur, _ := d.slotRect(key)
	_, hadRow := d.side.OverrideRect(key)
	want, _ := slotRectOf(op.after)
	if want == cur {
		// No change is neither an edit nor a refusal:
		// a plain click must not write a row, and a drag holding still must not chip.
		return op, applyNoChange
	}
	op.before = editValueSlotRect(cur, hadRow)
	if !d.writeSlotRect(key, want) {
		return op, applyRefused // theme.OverrideCap: refuse loudly rather than drop the edit
	}
	return op, applyDone
}

// applySlotReset removes this document's row for a widget, back to the rect resolved at open; no row is applyNoChange.
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

// revertSlotRect puts a widget back; a row that did not exist is removed and the opening geometry restored.
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

// writeSlotRect is the one writer of a widget's geometry, row and live map together.
func (d *themeDoc) writeSlotRect(key string, r theme.Rect) bool {
	if !d.setOverrideRow(key, r) {
		return false
	}
	d.live[key] = r
	d.dirty = true
	return true
}

// setOverrideRow upserts an [overrides] row; the cap refuses rather than evicts another widget's row.
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

func (d *themeDoc) dropOverrideRow(key string) {
	for i := range d.side.Overrides {
		if d.side.Overrides[i].Key == key {
			d.side.Overrides = append(d.side.Overrides[:i], d.side.Overrides[i+1:]...)
			return
		}
	}
}

// revert writes an op's before back through apply's writer.
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
		return d.insertAt(op.target, op.before)
	case opElemAdd:
		// The body must survive: a redo has to put back the same element, not a blank one.
		_, ok := d.removeAt(op.target, op.after)
		return ok
	case opSlotRect, opSlotReset:
		// One inverse for both: a reset's before always carries the row it removed.
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

// redo re-applies an undone op; op.before is already correct.
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
		// Drops the row again; writing after into one would re-create what the reset removed.
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

// applyDelete removes an element, parking its body so undo restores it exactly.
func (d *themeDoc) applyDelete(op themeEditOp) (themeEditOp, editApplyResult) {
	slot, ok := d.graveSlot()
	if !ok {
		// themeElemGraveCap reached: refuse rather than lose the body.
		return op, applyRefused
	}
	op.before = editValueInt(int32(slot))
	if _, ok := d.removeAt(op.target, op.before); !ok {
		return op, applyNoChange // no element at that index any more: nothing was prevented
	}
	return op, applyDone
}

// applyAdd appends a new element; the body comes through the graveyard index in op.after,
// so add and undo-of-delete match.
func (d *themeDoc) applyAdd(op themeEditOp) (themeEditOp, editApplyResult) {
	if !d.insertAt(op.target, op.after) {
		// Refused for theme.ElementCap, a body no longer held, or a stale index, not only for a cap.
		return op, applyRefused
	}
	return op, applyDone
}

// graveSlot claims the next graveyard index; it refuses at the cap so a delete is never lossy (hard rule 4).
func (d *themeDoc) graveSlot() (int, bool) {
	if d.graveN >= len(d.grave) {
		return 0, false
	}
	d.graveN++
	return d.graveN - 1, true
}

func (d *themeDoc) park(slot int, el theme.Element) editValue {
	if slot >= 0 && slot < len(d.grave) {
		d.grave[slot] = el
	}
	return editValueInt(int32(slot))
}

func (d *themeDoc) buried(v editValue) (theme.Element, bool) {
	slot := int(v.i[0])
	if slot < 0 || slot >= len(d.grave) {
		return theme.Element{}, false
	}
	return d.grave[slot], true
}

// removeAt deletes the element at the target's index, parking its body; identity is the index, so ops don't survive it.
// The carrier's section is retired too, or the next save re-emits it;
// insertAt cancels that, so undo stays byte-identical.
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

// insertAt puts a parked body back; past theme.ElementCap it refuses so the UI can name the cap,
// rather than build a model Sidecar.Bytes() would refuse.
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
	// Taking off the retirement mark keeps the author's key order, spacing and comments through the round trip.
	d.side.UnretireElementSection(el.ID)
	d.dirty = true
	return true
}

// rebaseline re-takes the discard snapshot; only landEditorSave may call it, so a discard means back to the last save.
// It must not touch typeSigApplied, which would claim an apply that never happened.
func (d *themeDoc) rebaseline() {
	if d == nil || d.side == nil {
		return
	}
	d.baseElems = append([]theme.Element(nil), d.side.Elements...)
	d.baseOv = append([]theme.KeyRect(nil), d.side.Overrides...)
	d.baseFonts = append([]theme.FontFamily(nil), d.side.Fonts...)
	d.baseBind = append([]theme.KV(nil), d.side.FontBind...)
	// The resolved geometry moves with the model, or the pixels would disagree with the rows.
	if d.live != nil {
		d.baseLive = cloneRects(d.live)
	}
}

// restoreBaseline puts the model and resolved geometry back at the baseline;
// a snapshot because the undo ring is capped.
func (d *themeDoc) restoreBaseline() {
	if d == nil || d.side == nil {
		return
	}
	d.side.Elements = append([]theme.Element(nil), d.baseElems...)
	d.side.Overrides = append([]theme.KeyRect(nil), d.baseOv...)
	d.side.Fonts = append([]theme.FontFamily(nil), d.baseFonts...)
	d.side.FontBind = append([]theme.KV(nil), d.baseBind...)
	// Every id back in the snapshot must keep its section;
	// otherwise a discard reorders the theme file on the next write.
	// An id the baseline lacks keeps its retirement.
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

// bytes serialises the document through Sidecar.Bytes, which validates before touching the lossless carrier.
func (d *themeDoc) bytes() ([]byte, error) {
	if d == nil || d.side == nil {
		return nil, nil
	}
	return d.side.Bytes()
}
