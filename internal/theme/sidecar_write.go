package theme

// The sidecar WRITER. It never rebuilds a file from the model: it edits the
// lossless document the reader kept, so comments, blank lines, key order, key
// casing, unknown keys, entire unknown sections and the author's own formatting
// all survive byte-identical.
//
// THE ONE RULE THAT MAKES THAT TRUE (setKey, below): a key is written only when
// the document does not ALREADY MEAN what the model says. "Means" is decided by
// running the reader's own parser over the current text and formatting the
// result — so a differently-spelled but equivalent value ("1,2,3,4"), an
// out-of-range value the reader clamped, and an UNKNOWN ENUM VALUE the reader
// degraded are all left exactly as the author wrote them.
//
// "The reader's own parser" INCLUDES ITS RANGE CONVERSION. Every field the
// reader narrows has a canonicaliser that narrows identically (canonZ,
// canonTimeMs, canonByte, canonAmp, canonSize, canonSlice); canonInt is for the
// two UNBOUNDED integers alone. A canonicaliser that skipped the conversion
// would report an untouched file as changed and rewrite a line nobody edited —
// which is the whole failure this type exists to prevent.
//
// The same applies to SECTION NAMES: they come from the model's own record of
// what the reader saw (Element.ID, Pane.ID, ClockSpec.ID), never rebuilt, or a
// legal alternative spelling gains a duplicate section beside it.
//
// LIMIT, stated plainly: THE WRITER STILL HAS NO DELETE, and the distinction
// matters now that INIDoc has one. INIDoc.RemoveSection (W9) removes a section
// on request; nothing in this file calls it, because "the model no longer holds
// this" and "the author deleted this" are not the same claim — the reader
// deliberately SKIPS an element whose `kind` a newer build invented and preserves
// its section, so a writer that pruned every element section the model lacks
// would delete exactly the forward-compat data rule 3 exists to protect.
// Deletion is therefore driven by the ACT that meant it: the preset tier replace
// (presetmerge.go's clearReplacedTier) and the editor's element delete. There is
// still no KEY delete at all — an empty model value leaves the author's line
// alone (setKey, below).

import (
	"io"
	"strconv"
	"strings"
)

// elementFieldKeys maps every Element FIELD to the wire key that carries it, in
// the order a freshly written element lists them.
//
// IT IS THE FORMAT. TestSidecarSchemaMatchesTheElementStruct reflects Element
// against this table and fails on any field that is missing, so a field added to
// the struct without a key here cannot silently save as nothing — the
// model-vs-format drift class that TestEverySavedPreferenceIsAlsoLoaded closed
// for preferences, applied to the theme format.
//
// An empty key means the value is carried by the SECTION NAME instead. There is
// no third category.
var elementFieldKeys = []struct{ field, key string }{
	{"ID", ""}, // the [element.<id>] section suffix, never a key
	{"Kind", "kind"},
	{"Band", "band"},
	{"Z", "z"},
	{"Space", "space"},
	{"Anchor", "anchor"},
	{"Rect", "rect"},
	{"Rot", "rot"},
	{"Media", "media"},
	{"Shape", "shape"},
	{"Gen", "generator"},
	{"GenParams", "gen_params"},
	{"Fit", "fit"},
	{"Slice", "slice"},
	{"Fill", "fill"},
	{"Fill2", "fill2"},
	{"GradAngle", "grad_angle"},
	{"GradRadial", "grad_radial"},
	{"Stroke", "stroke"},
	{"StrokePx", "stroke_px"},
	{"Tint", "tint"},
	{"Opacity", "opacity"},
	{"Text", "text"},
	{"Font", "font"},
	{"Size", "size"},
	{"Color", "color"},
	{"Align", "align"},
	{"Clock", "clock"},
	{"PhaseMs", "phase_ms"},
	{"Loop", "loop"},
	{"Effect", "effect"},
	{"EffectPeriodMs", "effect_period_ms"},
	{"EffectAmpPct", "effect_amp_pct"},
	{"NoDecimate", "decimate"},
	{"VisibleWhen", "visible_when"},
	{"Locked", "locked"},
	{"Hidden", "hidden"},
}

// Bytes serialises the sidecar back through its lossless document.
//
// IT VALIDATES FIRST (Sidecar.Validate, sidecar_validate.go) and refuses BEFORE
// it edits anything, so an editor holding a live model can never write a file
// the reader would refuse or silently truncate. That check is the writer's
// precondition, not an option: there is no exported path around it, which is
// the whole point — a caller that could skip it would skip it.
//
// THAT CLAIM IS ENFORCED, NOT ASSERTED (ledger L32).
// TestNoExportedPathWritesASidecarWithoutTheGuard censuses this package's whole
// API for a symbol that hands out a writable *INIDoc or emits sidecar bytes
// without going through the guard. The claim was FALSE when it was first
// written — Sidecar.Doc() returned the live document and INIDoc.Set/Bytes/Write
// are all exported — and it survived because nothing executed it.
//
// W7 reopens the door deliberately and narrowly: its undo ring and autosave need
// the document, so the wave that owns those CALL SITES adds the one path they
// need, guarded, and extends the census's allow-list with the reason. That is
// the open–closed shape — open to a wave that names its callers, closed against
// a general-purpose accessor nobody can arbitrate.
//
// COLD PATH ONLY (hard rule 2). On error the document may hold a partial edit —
// the caller discards it rather than writing it, which is why the atomic
// temp+fsync+rename dance belongs to whoever owns the destination directory.
// A VALIDATION error leaves the document untouched.
func (s *Sidecar) Bytes() ([]byte, error) {
	if s == nil {
		return nil, nil
	}
	// THE METADATA DEGRADE runs BEFORE the guard, in the same order the reader runs
	// it: a model carrying a 300-rune credit (a preset, a copy-for-editing stamp, an
	// inspector row) shortens with a note rather than making the whole save fail.
	// Validate then only ever sees values inside the cap, which is why its length
	// walk skips exactly these keys (metatext.go).
	s.degradeMeta()
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if s.doc == nil {
		d, err := ParseINIDoc(nil)
		if err != nil {
			return nil, err
		}
		s.doc = d
	}
	// THE DEFERRED DELETIONS, before a single key is written: a section the model
	// dropped must not be resurrected by the very write that was supposed to
	// remove it. After Validate, so a refused save leaves the carrier untouched.
	s.flushRetiredSections()
	if err := s.writeInto(s.doc); err != nil {
		return nil, err
	}
	return s.doc.Bytes(), nil
}

// Write serialises to w. It does not touch the disk itself — the atomic
// temp+fsync+rename dance belongs to whoever owns the destination directory.
//
// Named Write, not WriteTo, to mirror INIDoc.Write: io.WriterTo's WriteTo has a
// fixed (int64, error) signature, and go vet's stdmethods check is right that a
// method wearing that name should be that interface.
func (s *Sidecar) Write(w io.Writer) error {
	b, err := s.Bytes()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// writeInto emits every section in canonical order. For a document that was
// READ, the order is irrelevant (each key lands on its existing line); for one
// authored from scratch it is the file's layout.
func (s *Sidecar) writeInto(d *INIDoc) error {
	for _, emit := range []func(*INIDoc) error{
		s.writeTheme,
		s.writeOverrides,
		s.writeRotations,
		s.writePalette,
		s.writeChrome,
		s.writeFonts,
		s.writeFontBind,
		s.writeMedia,
		s.writeClocks,
		s.writePanes,
		s.writeElements,
		s.writeEffects,
		s.writeSounds,
		s.writeImport,
	} {
		if err := emit(d); err != nil {
			return err
		}
	}
	return nil
}

// canonFunc re-derives what the document's CURRENT text means, in the writer's
// own spelling. It is always format(parse(raw)) using the reader's parser, so
// "unchanged" is decided by meaning and never by bytes.
type canonFunc func(string) string

// setKey is the whole preservation contract in nine lines.
//
//   - want == "": the model has nothing to say. An existing line is left alone
//     (there is no KEY delete — see the file header) and no line is created.
//   - the key is ABSENT: written only when it differs from the format default,
//     so a fresh file lists what the author chose and nothing else.
//   - the key is PRESENT: written only when the document does not already mean
//     it. That single comparison is what preserves comments, spellings,
//     clamped values and unknown enum values through a save.
func setKey(d *INIDoc, section, key, want, def string, canon canonFunc) error {
	if want == "" {
		return nil
	}
	cur, ok := d.Get(section, key)
	if !ok {
		if want == def {
			return nil
		}
		return d.Set(section, key, want)
	}
	if canon(cur) == want {
		return nil
	}
	return d.Set(section, key, want)
}

func (s *Sidecar) writeTheme(d *INIDoc) error {
	format := s.Format
	if format <= 0 {
		format = ThemeFormatVersion
	}
	// Written with no default, so every file states its own grammar: a sidecar
	// with no format key is the one shape a third-party reader has to guess at.
	rows := []struct {
		key, want, def string
		canon          canonFunc
	}{
		{"format", formatInt(format), "", canonInt},
		{"revision", formatInt(s.Revision), "0", canonInt},
		// THE METADATA ROWS CANONICALISE THROUGH THE TRUNCATION (canonMetaFree /
		// canonMetaPlain). That is what keeps the degrade out of the FILE: the
		// author's 300-rune credit on disk canonicalises to the same 240-rune string
		// the model holds, so setKey decides the file already means it and writes
		// nothing — §7's own writer rule, applied to the one cap that degrades.
		{"name", encodeFreeText(s.Meta.Name), "", canonMetaFree},
		{"author", encodeFreeText(s.Meta.Author), "", canonMetaFree},
		{"license", encodeFreeText(s.Meta.License), "", canonMetaFree},
		{"credit", encodeFreeText(s.Meta.Credit), "", canonMetaFree},
		{"description", encodeFreeText(s.Meta.Description), "", canonMetaFree},
		{"min_client", canonPlain(s.Meta.MinClient), "", canonMetaPlain},
		{"base", canonPlain(s.Meta.Base), "", canonPlain},
		{"subtheme", canonPlain(s.Meta.Subtheme), "", canonPlain},
		{"layout_preset", canonPlain(s.Meta.LayoutPreset), "", canonMetaPlain},
		{"style_preset", canonPlain(s.Meta.StylePreset), "", canonMetaPlain},
		{"generated_by", canonPlain(s.Meta.GeneratedBy), "", canonMetaPlain},
	}
	for _, r := range rows {
		if err := setKey(d, secTheme, r.key, r.want, r.def, r.canon); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeOverrides(d *INIDoc) error {
	for _, o := range s.Overrides {
		if err := setKey(d, secOverrides, o.Key, formatRect(o.Rect), "", canonRect); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeRotations(d *INIDoc) error {
	for _, r := range s.Rotations {
		// An angle of 0 is a real, meaningful entry (it says "this key is not
		// rotated" over a preset that rotates it), so it has no default.
		if err := setKey(d, secRotations, r.Key, formatInt(int(r.Angle)), "", canonAngle); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writePalette(d *INIDoc) error {
	for _, role := range paletteRoleKeys {
		c := paletteSlotColor(s.Palette, role.slot)
		if c == nil {
			continue // absent roles inherit the stylesheet; absence is the value
		}
		want := formatRGB(*c)
		if err := setKey(d, secPalette, role.key, want, "", canonRGB); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeChrome(d *INIDoc) error {
	if err := setKey(d, secChrome, "shape", canonPlain(s.Chrome.Shape), "", canonPlain); err != nil {
		return err
	}
	if err := setKey(d, secChrome, "shape_tier", formatInt(s.Chrome.ShapeTier), "0", canonInt); err != nil {
		return err
	}
	return setKey(d, secChrome, "tabbar_hex", formatRGBA(s.Chrome.TabbarHex), formatRGBA(RGBA{}), canonColor)
}

func (s *Sidecar) writeFonts(d *INIDoc) error {
	for _, f := range s.Fonts {
		if err := setKey(d, secFonts, f.Family, canonPlain(f.File), "", canonPlain); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeFontBind(d *INIDoc) error {
	for _, b := range s.FontBind {
		if err := setKey(d, secFontBind, b.Key, canonPlain(b.Value), "", canonPlain); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeMedia(d *INIDoc) error {
	for _, m := range s.Media {
		if err := setKey(d, secMedia, m.ID, formatMedia(m), "", canonMedia); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeClocks(d *INIDoc) error {
	// Clock 0 is the shared theme anchor and is never declared, so the loop
	// starts at 1 — writing [clock.0] would invent a group that cannot exist.
	for i := 1; i < ClockCap; i++ {
		c := s.Clocks[i]
		if !c.Declared && c.SpeedPct == 0 {
			continue
		}
		// The section name is the one the READER SAW (ClockSpec.ID), never one
		// re-derived from the index: "01" is a legal id that reads as clock 1,
		// so rebuilding the name would miss the author's section and append a
		// second one beside it saying the same thing. A group the editor
		// declared carries no source spelling and gets the canonical one.
		id := c.ID
		if id == "" {
			id = strconv.Itoa(i)
		}
		sec := secClockPrefix + id
		if err := setKey(d, sec, "speed_pct", formatInt(int(c.SpeedPct)), "0", canonSpeed); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writePanes(d *INIDoc) error {
	for i := range s.Panes {
		p := &s.Panes[i]
		sec := secPanePrefix + p.ID
		// An all-zero rect is the DEFAULT here, not a statement: a pane with no
		// area cannot host anything, so a file that omits the key must not gain
		// one. (An [overrides] entry is the opposite case — it exists only
		// because the author wrote it — so there the default is "no value".)
		if err := setKey(d, sec, "rect", formatRect(p.Rect), formatRect(Rect{}), canonRect); err != nil {
			return err
		}
		if err := setKey(d, sec, "space", p.Space.String(), spaceNames[SpaceCourtroom], canonSpace); err != nil {
			return err
		}
		if err := setKey(d, sec, "hosts", formatList(p.Hosts[:p.NHosts]), "", canonList); err != nil {
			return err
		}
		if err := setKey(d, sec, "divider", formatRGBA(p.Divider), formatRGBA(RGBA{}), canonColor); err != nil {
			return err
		}
		if err := setKey(d, sec, "divider_px", formatInt(int(p.DividerPx)), "0", canonByte); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeEffects(d *INIDoc) error {
	for _, e := range s.Effects {
		sec := secEffectPrefix + e.Target
		if err := setKey(d, sec, "effect", e.Effect.String(), effectNames[FXNone], canonEffect); err != nil {
			return err
		}
		if err := setKey(d, sec, "color", formatRGBA(e.Color), formatRGBA(RGBA{}), canonColor); err != nil {
			return err
		}
		if err := setKey(d, sec, "period_ms", formatInt(int(e.PeriodMs)), "0", canonTimeMs); err != nil {
			return err
		}
		if err := setKey(d, sec, "amp_pct", formatInt(int(e.AmpPct)), "0", canonAmp); err != nil {
			return err
		}
		if err := setKey(d, sec, "clock", formatInt(int(e.Clock)), "0", canonClockIndex); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeSounds(d *INIDoc) error {
	for _, kv := range s.Sounds {
		if err := setKey(d, secSounds, kv.Key, canonPlain(kv.Value), "", canonPlain); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeImport(d *INIDoc) error {
	rows := []struct {
		key, want, def string
		canon          canonFunc
	}{
		// Metadata, so the same truncating canonicaliser [theme]'s rows use.
		{"derived_from", canonPlain(s.Import.DerivedFrom), "", canonMetaPlain},
		{"derived_at", canonPlain(s.Import.DerivedAt), "", canonMetaPlain},
		{"derived_hash", canonPlain(s.Import.DerivedHash), "", canonMetaPlain},
		{"subthemes", formatList(s.Import.Subthemes), "", canonList},
		{"per_misc", formatBool(s.Import.PerMisc), formatBool(false), canonBool},
		{"lobby_design", formatBool(s.Import.LobbyDesign), formatBool(false), canonBool},
	}
	for _, r := range rows {
		if err := setKey(d, secImport, r.key, r.want, r.def, r.canon); err != nil {
			return err
		}
	}
	return nil
}

// writeElements emits one [element.<id>] per model element, in the model's
// order — which for a document that was read is its declaration order, so a
// save never re-orders the author's file.
func (s *Sidecar) writeElements(d *INIDoc) error {
	for i := range s.Elements {
		if err := s.writeElement(d, &s.Elements[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sidecar) writeElement(d *INIDoc, e *Element) error {
	sec := secElementPrefix + e.ID
	// The rows are in elementFieldKeys order, and the schema gate asserts the
	// two lists hold the same keys — so this is the one place a new field has
	// to be wired, not two.
	rows := []struct {
		key, want, def string
		canon          canonFunc
	}{
		{"kind", e.Kind.String(), elementKindNames[ElemImage], canonKind},
		{"band", e.Band.String(), elementBandNames[BandBackdrop], canonBand},
		{"z", formatInt(int(e.Z)), "0", canonZ},
		{"space", e.Space.String(), spaceNames[SpaceCourtroom], canonSpace},
		{"anchor", canonPlain(e.Anchor), "", canonPlain},
		{"rect", formatRect(e.Rect), formatRect(Rect{}), canonRect},
		{"rot", formatInt(int(e.Rot)), "0", canonAngle},
		{"media", canonPlain(e.Media), "", canonPlain},
		{"shape", canonPlain(e.Shape), "", canonPlain},
		{"generator", canonPlain(e.Gen), "", canonPlain},
		{"gen_params", formatGenParams(e), "", canonGenParams},
		{"fit", e.Fit.String(), fitNames[FitStretch], canonFit},
		{"slice", formatSlice(e.Slice), formatSlice([4]int16{}), canonSlice},
		{"fill", formatRGBA(e.Fill), formatRGBA(RGBA{}), canonColor},
		{"fill2", formatRGBA(e.Fill2), formatRGBA(RGBA{}), canonColor},
		{"grad_angle", formatInt(int(e.GradAngle)), "0", canonAngle},
		{"grad_radial", formatBool(e.GradRadial), formatBool(false), canonBool},
		{"stroke", formatRGBA(e.Stroke), formatRGBA(RGBA{}), canonColor},
		{"stroke_px", formatInt(int(e.StrokePx)), "0", canonByte},
		{"tint", formatRGBA(e.Tint), formatRGBA(RGBA{}), canonColor},
		{"opacity", formatInt(int(e.Opacity)), "0", canonByte},
		{"text", encodeFreeText(e.Text), "", canonFree},
		{"font", canonPlain(e.Font), "", canonPlain},
		{"size", formatInt(e.Size), "0", canonSize},
		{"color", formatRGBA(e.Color), formatRGBA(RGBA{}), canonColor},
		{"align", e.Align.String(), alignNames[AlignLeft], canonAlign},
		{"clock", formatInt(int(e.Clock)), "0", canonClockIndex},
		{"phase_ms", formatInt(int(e.PhaseMs)), "0", canonTimeMs},
		{"loop", formatBool(e.Loop), formatBool(false), canonBool},
		{"effect", e.Effect.String(), effectNames[FXNone], canonEffect},
		{"effect_period_ms", formatInt(int(e.EffectPeriodMs)), "0", canonTimeMs},
		{"effect_amp_pct", formatInt(int(e.EffectAmpPct)), "0", canonAmp},
		// Inverted on the wire: the key says "decimate", the field says "no".
		{"decimate", formatBool(!e.NoDecimate), formatBool(true), canonDecimate},
		{"visible_when", formatCondition(e.VisibleWhen), "", canonCondition},
		{"locked", formatBool(e.Locked), formatBool(false), canonBool},
		{"hidden", formatBool(e.Hidden), formatBool(false), canonBool},
	}
	skipSlice := sliceIsHeldByItsAlias(d, sec, e)
	for _, r := range rows {
		if r.key == "slice" && skipSlice {
			continue
		}
		if err := setKey(d, sec, r.key, r.want, r.def, r.canon); err != nil {
			return err
		}
	}
	return nil
}

// sliceIsHeldByItsAlias reports that the author spelled the 9-slice inset with
// the scalar alias ("nine_slice = 12") and still means it. Writing the
// canonical four-tuple beside it would be a diff nobody asked for — and two
// keys saying the same thing, which is worse than either spelling.
//
// It is deliberately narrow: the moment the model's inset stops matching what
// the alias says, the canonical key IS written, so an edit is never lost.
func sliceIsHeldByItsAlias(d *INIDoc, section string, e *Element) bool {
	raw, ok := d.Get(section, "nine_slice")
	if !ok {
		return false
	}
	if _, canonical := d.Get(section, "slice"); canonical {
		return false // both spellings present: the canonical one is the one we keep
	}
	n := int16(clampInt(atoiTrim(raw), ZMin, ZMax))
	return e.Slice == [4]int16{n, n, n, n}
}

// ---------------------------------------------------------------------------
// Formatters. AO2's exact shapes: "x, y, w, h" with ", ", plain decimal ints.
// atoiTrim is deliberately lenient (see its comment) and NOTHING here may
// depend on that leniency — TestWriterNeverEmitsAValueThatReadsAsZero holds it.
// ---------------------------------------------------------------------------

func formatInt(v int) string { return strconv.Itoa(v) }

func formatRect(r Rect) string {
	return strconv.Itoa(r.X) + tupleJoin + strconv.Itoa(r.Y) + tupleJoin +
		strconv.Itoa(r.W) + tupleJoin + strconv.Itoa(r.H)
}

func formatSlice(s [4]int16) string {
	var b strings.Builder
	for i, v := range s {
		if i > 0 {
			b.WriteString(tupleJoin)
		}
		b.WriteString(strconv.Itoa(int(v)))
	}
	return b.String()
}

// tupleJoin is how AO2 spells a tuple separator in every theme in the corpus.
// Written as ", " and never as "," so a diff against a hand-authored file stays
// quiet.
const tupleJoin = ", "

// formatRGBA writes "#rrggbb" when the colour is opaque and "#rrggbbaa"
// otherwise, so the common case reads like every other hex colour in a theme.
func formatRGBA(c RGBA) string {
	const hexDigits = "0123456789abcdef"
	buf := make([]byte, 0, 9)
	buf = append(buf, '#')
	for _, v := range [3]uint8{c.R, c.G, c.B} {
		buf = append(buf, hexDigits[v>>4], hexDigits[v&0xf])
	}
	if c.A != 255 {
		buf = append(buf, hexDigits[c.A>>4], hexDigits[c.A&0xf])
	}
	return string(buf)
}

// formatRGB is the palette's spelling: alpha has no meaning for a QSS role.
func formatRGB(c RGB) string { return formatRGBA(RGBA{c.R, c.G, c.B, 255}) }

// formatBool writes the spelling the published examples use. Every other legal
// spelling ("1", "true") still READS, and canonBool folds them, so an author's
// own choice survives a save untouched.
func formatBool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func formatList(items []string) string { return strings.Join(items, tupleJoin) }

func formatGenParams(e *Element) string {
	n := e.GenParamCount()
	if n == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(tupleJoin)
		}
		b.WriteString(e.GenParams[i].Key)
		b.WriteByte('=')
		b.WriteString(e.GenParams[i].Value)
	}
	return b.String()
}

// formatMedia writes "file, bytes, hash", dropping trailing components that
// carry nothing — both because a bare file name is what a hand-authored theme
// looks like, and because a trailing ", " would be a value INIDoc refuses (the
// reader trims, so it could not read back as itself).
func formatMedia(m MediaEntry) string {
	switch {
	case m.File == "":
		return ""
	case m.Hash != "":
		return m.File + tupleJoin + strconv.FormatInt(m.Bytes, 10) + tupleJoin + m.Hash
	case m.Bytes != 0:
		return m.File + tupleJoin + strconv.FormatInt(m.Bytes, 10)
	}
	return m.File
}

func formatCondition(c Condition) string {
	if c.Axis == CondAlways {
		return "" // the default; absent says it more clearly than "always"
	}
	name := c.Axis.String()
	if !conditionAxisTakesValue[c.Axis] || c.Value == "" {
		return name
	}
	return name + ":" + c.Value
}

// ---------------------------------------------------------------------------
// Canonicalisers: format(parse(raw)) with the READER's parser, one per value
// type. A missing or unparseable value must canonicalise to whatever the reader
// STORED for it, or an untouched save would rewrite a line nobody edited.
// ---------------------------------------------------------------------------

func canonPlain(s string) string { return strings.TrimSpace(s) }

func canonFree(s string) string { return encodeFreeText(decodeFreeText(s)) }

// canonMetaFree / canonMetaPlain are canonFree / canonPlain THROUGH the metadata
// truncation, and they are the reason the degrade never reaches the author's file.
//
// A canonicaliser answers "what does the text on disk mean to THIS reader?", and
// what a 300-rune credit means to this reader is its first 240 runes (metatext.go).
// Saying so here makes setKey's comparison come out equal, so the line is left
// exactly as the author wrote it — and a newer build with a bigger cap reads their
// whole sentence back. Canonicalising WITHOUT the truncation would rewrite that
// line to the short version on the first save of an unrelated rect nudge, which is
// the silent-truncate-then-save data loss §7's caps block exists to prevent.
func canonMetaFree(s string) string {
	v, _ := truncMetaRunes(decodeFreeText(s))
	return encodeFreeText(v)
}

func canonMetaPlain(s string) string {
	v, _ := truncMetaRunes(strings.TrimSpace(s))
	return v
}

// canonInt is for the integers the reader does NOT narrow: [theme] format and
// revision, and [chrome] shape_tier, each stored exactly as atoiTrim returned
// it. (format is additionally the one key a save always stamps — §7's writer
// rules — so a file that spells it 0, or spells it nothing an integer, is the
// one line the writer does rewrite, into the grammar it can be read by.)
//
// Every integer the reader NARROWS has its own canonicaliser below, because a
// canonicaliser that skips the reader's conversion reports an untouched file as
// changed and rewrites the author's line.
func canonInt(s string) string { return formatInt(atoiTrim(s)) }

func canonByte(s string) string { return formatInt(clampInt(atoiTrim(s), 0, 255)) }

// canonZ mirrors the reader's int16 clamp on z and on a scalar 9-slice inset.
func canonZ(s string) string { return formatInt(clampInt(atoiTrim(s), ZMin, ZMax)) }

// canonTimeMs mirrors the reader's int32 clamp on phase_ms, effect_period_ms
// and [effect.*] period_ms. Without it a legal "phase_ms = 3000000000" saves
// back as a NEGATIVE number — not merely a rewrite of a line nobody edited, but
// a rewrite into a value that means something else.
func canonTimeMs(s string) string { return formatInt(clampInt(atoiTrim(s), TimeMsMin, TimeMsMax)) }

func canonAmp(s string) string { return formatInt(clampInt(atoiTrim(s), 0, AmpMaxPct)) }

func canonSize(s string) string { return formatInt(clampInt(atoiTrim(s), 0, TextSizeMaxPt)) }

func canonAngle(s string) string { return formatInt(int(parseAngle(s))) }

func canonClockIndex(s string) string { return formatInt(int(parseClockIndex(s))) }

func canonSpeed(s string) string { return formatInt(int(parseSpeedPct(s))) }

func canonRect(s string) string { return formatRect(parseRectValue(s)) }

func canonSlice(s string) string { return formatSlice(parseSliceValue(s)) }

// canonColor folds an unparseable colour to the ZERO colour, because that is
// exactly what the reader left in the model when the parse failed.
func canonColor(s string) string {
	c, _ := parseRGBAValue(s)
	return formatRGBA(c)
}

func canonRGB(s string) string {
	c, _ := parseRGBAValue(s)
	return formatRGB(c.RGB())
}

func canonBool(s string) string { return formatBool(parseBoolValue(s)) }

// canonDecimate mirrors the reader's guarded negation: an ABSENT key leaves
// NoDecimate false, so an empty string canonicalises to "yes".
func canonDecimate(s string) string {
	if strings.TrimSpace(s) == "" {
		return formatBool(true)
	}
	return formatBool(parseBoolValue(s))
}

func canonList(s string) string {
	// The cap is the reader's problem; here a longer list simply canonicalises
	// as far as it parses, and the reader already refused the file if it was
	// over.
	items, _ := parseTextList(s, PaneHostCap+SubthemeCap)
	return formatList(items)
}

func canonGenParams(s string) string {
	params, err := genParamsOf(s)
	if err != nil {
		return canonPlain(s) // unreachable: the reader refused such a file
	}
	e := Element{GenParams: params}
	return formatGenParams(&e)
}

func canonCondition(s string) string {
	c, _, err := conditionOf(s)
	if err != nil {
		return canonPlain(s) // unreachable: the reader refused such a file
	}
	return formatCondition(c)
}

func canonMedia(s string) string {
	parts := strings.Split(s, tupleSeparator)
	var m MediaEntry
	if len(parts) > 0 {
		m.File = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		m.Bytes = int64(atoiTrim(parts[1]))
	}
	if len(parts) > 2 {
		m.Hash = strings.ToLower(strings.TrimSpace(parts[2]))
	}
	return formatMedia(m)
}

// canonEnum builds the canonicaliser for one wire-name table: an unknown value
// folds to the SAME fallback the reader degraded it to, which is what makes an
// unknown enum value survive a save (rule 3's second half).
func canonEnum(names []string, fallback uint8) canonFunc {
	return func(s string) string {
		if v, ok := enumValue(names, s); ok {
			return enumName(names, v)
		}
		return enumName(names, fallback)
	}
}

var (
	canonKind   = canonEnum(elementKindNames[:], uint8(ElemImage))
	canonBand   = canonEnum(elementBandNames[:], uint8(BandBackdrop))
	canonSpace  = canonEnum(spaceNames[:], uint8(SpaceCourtroom))
	canonFit    = canonEnum(fitNames[:], uint8(FitStretch))
	canonEffect = canonEnum(effectNames[:], uint8(FXNone))
	canonAlign  = canonEnum(alignNames[:], uint8(AlignLeft))
)
