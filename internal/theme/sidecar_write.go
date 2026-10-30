package theme

// The sidecar writer edits the reader's lossless document rather than rebuilding, so every line survives.

import (
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

// elementFieldKeys maps Element fields to wire keys in written order; empty means the section name.
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

// Bytes serialises through the document; cold path (hard rule 2); on error, discard the partial edit.
func (s *Sidecar) Bytes() ([]byte, error) {
	if s == nil {
		return nil, nil
	}
	// Before the guard, as in the reader: a long credit shortens rather than failing the save.
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
	// Before any key, so a write cannot resurrect a dropped section; after Validate, so a refusal is clean.
	s.flushRetiredSections()
	if err := s.writeInto(s.doc); err != nil {
		return nil, err
	}
	return s.doc.Bytes(), nil
}

// Write serialises to w; named Write, not WriteTo, because go vet's stdmethods expects io.WriterTo's signature there.
func (s *Sidecar) Write(w io.Writer) error {
	b, err := s.Bytes()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// writeInto emits every section in canonical order, which is the layout of a file authored from scratch.
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

// canonFunc is format(parse(raw)) with the reader's parser, so "unchanged" is decided by meaning, not bytes.
type canonFunc func(string) string

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
	// No default, so every file states its grammar.
	rows := []struct {
		key, want, def string
		canon          canonFunc
	}{
		{"format", formatInt(format), "", canonInt},
		{"revision", formatInt(s.Revision), "0", canonInt},
		// Metadata rows canonicalise through truncation, so a long credit on disk writes nothing.
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
		// An angle of 0 says "not rotated" over a preset that rotates, so it has no default.
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
	// From 1: clock 0 is the shared anchor and [clock.0] cannot exist.
	for i := 1; i < ClockCap; i++ {
		c := s.Clocks[i]
		if !c.Declared && c.SpeedPct == 0 {
			continue
		}
		// The name the reader saw: rebuilding "01" from the index would append a second section.
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
		// An all-zero rect is the default, so a file omitting the key must not gain one.
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
		// Metadata: the same truncating canonicaliser as [theme].
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

// writeElements emits sections in model order, so a save never re-orders the file.
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
		// Inverted on the wire.
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

// sliceIsHeldByItsAlias reports that "nine_slice = N" still matches, so no four-tuple is written.
func sliceIsHeldByItsAlias(d *INIDoc, section string, e *Element) bool {
	raw, ok := d.Get(section, "nine_slice")
	if !ok {
		return false
	}
	if _, canonical := d.Get(section, "slice"); canonical {
		return false // both spellings present: the canonical one is the one we keep
	}
	n := int16(value.Clamp(parse.AtoiOr(raw, 0), ZMin, ZMax))
	return e.Slice == [4]int16{n, n, n, n}
}

// Formatters, in AO2's shapes; nothing here may depend on parse.AtoiOr's leniency.

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

// tupleJoin is how AO2 themes spell a tuple separator, keeping diffs against hand-authored files quiet.
const tupleJoin = ", "

func formatRGBA(c RGBA) string {
	const hexDigits = "0123456789abcdef"
	buf := make([]byte, 0, len("#rrggbbaa"))
	buf = append(buf, '#')
	for _, v := range [3]uint8{c.R, c.G, c.B} {
		buf = append(buf, hexDigits[v>>4], hexDigits[v&0xf])
	}
	if c.A != OpacityOpaque {
		buf = append(buf, hexDigits[c.A>>4], hexDigits[c.A&0xf])
	}
	return string(buf)
}

// formatRGB is the palette's spelling: alpha has no meaning for a QSS role.
func formatRGB(c RGB) string { return formatRGBA(RGBA{c.R, c.G, c.B, 255}) }

// formatBool writes the published spelling; canonBool folds others, so an author's choice survives.
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

// formatMedia drops empty trailing components, because a trailing ", " would not read back and INIDoc refuses it.
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

// Canonicalisers: a bad value must give what the reader stored, or a save rewrites the line.

func canonPlain(s string) string { return strings.TrimSpace(s) }

func canonFree(s string) string { return encodeFreeText(decodeFreeText(s)) }

// canonMetaFree and canonMetaPlain go through the metadata truncation, keeping it out of the file.
func canonMetaFree(s string) string {
	v, _ := truncMetaRunes(decodeFreeText(s))
	return encodeFreeText(v)
}

func canonMetaPlain(s string) string {
	v, _ := truncMetaRunes(strings.TrimSpace(s))
	return v
}

// canonInt is for integers the reader does not narrow; narrowed ones need their own canonicaliser,
// or an untouched line gets rewritten.
func canonInt(s string) string { return formatInt(parse.AtoiOr(s, 0)) }

func canonByte(s string) string { return formatInt(value.Clamp(parse.AtoiOr(s, 0), 0, math.MaxUint8)) }

func canonZ(s string) string { return formatInt(value.Clamp(parse.AtoiOr(s, 0), ZMin, ZMax)) }

// canonTimeMs mirrors the reader's int32 clamp; without it a legal "phase_ms = 3000000000" saves back negative.
func canonTimeMs(s string) string {
	return formatInt(value.Clamp(parse.AtoiOr(s, 0), TimeMsMin, TimeMsMax))
}

func canonAmp(s string) string { return formatInt(value.Clamp(parse.AtoiOr(s, 0), 0, AmpMaxPct)) }

func canonSize(s string) string { return formatInt(value.Clamp(parse.AtoiOr(s, 0), 0, TextSizeMaxPt)) }

func canonAngle(s string) string { return formatInt(int(parseAngle(s))) }

func canonClockIndex(s string) string { return formatInt(int(parseClockIndex(s))) }

func canonSpeed(s string) string { return formatInt(int(parseSpeedPct(s))) }

func canonRect(s string) string { return formatRect(parseRectValue(s)) }

func canonSlice(s string) string { return formatSlice(parseSliceValue(s)) }

// canonColor folds a bad colour to zero, what the reader left in the model.
func canonColor(s string) string {
	c, _ := parseRGBAValue(s)
	return formatRGBA(c)
}

func canonRGB(s string) string {
	c, _ := parseRGBAValue(s)
	return formatRGB(c.RGB())
}

func canonBool(s string) string { return formatBool(parseBoolValue(s)) }

// canonDecimate mirrors the reader's guarded negation: empty canonicalises to "yes".
func canonDecimate(s string) string {
	if strings.TrimSpace(s) == "" {
		return formatBool(true)
	}
	return formatBool(parseBoolValue(s))
}

func canonList(s string) string {
	// The reader already refused an over-cap file; here a list canonicalises as far as it parses.
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
		m.Bytes = int64(parse.AtoiOr(parts[1], 0))
	}
	if len(parts) > 2 {
		m.Hash = strings.ToLower(strings.TrimSpace(parts[2]))
	}
	return formatMedia(m)
}

// canonEnum folds an unknown value to the reader's fallback, so it survives a save.
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
