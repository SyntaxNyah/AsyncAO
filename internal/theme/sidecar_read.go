package theme

// The sidecar reader walks the lossless document, never a second parser.
// Note errors are absorbed at degrade sites, as a theme is never refused for too many notes.

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

// Known section names and prefixes; anything else is preserved verbatim, counted and ignored.
const (
	secTheme     = "theme"
	secOverrides = "overrides"
	secRotations = "rotations"
	secPalette   = "palette"
	secChrome    = "chrome"
	secFonts     = "fonts"
	secFontBind  = "fontbind"
	secMedia     = "media"
	secSounds    = "sounds"
	secImport    = "import"

	secElementPrefix = "element."
	secPanePrefix    = "pane."
	secClockPrefix   = "clock."
	secEffectPrefix  = "effect."

	// elementTextKey is named because the writer's schema, the reader's cap message and Validate all spell it.
	elementTextKey = "text"
)

// ParseSidecar reads asyncao_theme.ini content, keeping the lossless document; cold path only (hard rule 2).
func ParseSidecar(src []byte) (*Sidecar, error) {
	doc, err := ParseINIDoc(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSidecarUnreadable, err)
	}
	s := &Sidecar{Format: ThemeFormatVersion, doc: doc}

	// The version comes first so a migration runs against the document the parse then walks.
	if raw, ok := doc.Get(secTheme, "format"); ok {
		if v := parse.AtoiOr(raw, 0); v > 0 {
			s.Format = v
		}
	}
	if err := migrateSidecar(s.Format, doc); err != nil {
		return nil, err
	}

	secs := walkSections(doc)
	if err := s.readSections(doc, secs); err != nil {
		return nil, err
	}
	// An over-long credit shortens with a note rather than refusing the theme (metatext.go).
	s.degradeMeta()
	return s, nil
}

// LoadSidecar reads one file; a missing one returns (nil, nil), the stock AO2 theme; goroutine only (hard rule 2).
func LoadSidecar(path string) (*Sidecar, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	// The stream, not a stat, enforces the cap, so a growing file cannot pass; one byte over lets the cap refuse.
	src, err := io.ReadAll(io.LimitReader(f, IniDocByteCap+1))
	if err != nil {
		return nil, err
	}
	return ParseSidecar(src)
}

// LoadSidecarIn returns the first directory's sidecar and that directory; goroutine only (hard rule 2).
func LoadSidecarIn(dirs []string) (*Sidecar, string, error) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		s, err := LoadSidecar(filepath.Join(dir, SidecarFileName))
		if err != nil {
			return nil, dir, err
		}
		if s != nil {
			return s, dir, nil
		}
	}
	return nil, "", nil
}

// docSections keeps declaration order: element z-ties break on it, and map order varies per launch.
type docSections struct {
	names []string
	keys  map[string][]string
}

// walkSections reads the document's own lines, so no second parse can disagree.
func walkSections(d *INIDoc) docSections {
	out := docSections{keys: map[string][]string{}}
	seenSec := map[string]bool{}
	seenKey := map[string]bool{}
	for _, l := range d.lines {
		if !seenSec[l.section] {
			// A section is declared by its header or first key: a root key needs no header.
			if l.isKey || l.section != "" {
				seenSec[l.section] = true
				out.names = append(out.names, l.section)
			}
		}
		if !l.isKey {
			continue
		}
		ck := l.section + iniSectionSep + l.key
		if seenKey[ck] {
			continue // a repeated key is one key; Get resolves which line wins
		}
		seenKey[ck] = true
		out.keys[l.section] = append(out.keys[l.section], l.key)
	}
	return out
}

func (s *Sidecar) readSections(d *INIDoc, secs docSections) error {
	for _, name := range secs.names {
		keys := secs.keys[name]
		var err error
		switch {
		case name == secTheme:
			err = s.readTheme(d, keys)
		case name == secOverrides:
			err = s.readOverrides(d, keys)
		case name == secRotations:
			err = s.readRotations(d, keys)
		case name == secPalette:
			err = s.readPalette(d, keys)
		case name == secChrome:
			err = s.readChrome(d, keys)
		case name == secFonts:
			err = s.readFonts(d, keys)
		case name == secFontBind:
			err = s.readFontBind(d, keys)
		case name == secMedia:
			err = s.readMedia(d, keys)
		case name == secSounds:
			err = s.readSounds(d, keys)
		case name == secImport:
			err = s.readImport(d, keys)
		case strings.HasPrefix(name, secElementPrefix):
			err = s.readElement(d, name, keys)
		case strings.HasPrefix(name, secPanePrefix):
			err = s.readPane(d, name, keys)
		case strings.HasPrefix(name, secClockPrefix):
			err = s.readClock(d, name, keys)
		case strings.HasPrefix(name, secEffectPrefix):
			err = s.readEffectBind(d, name, keys)
		default:
			err = s.markUnknownSection(name, keys)
		}
		if err != nil {
			return err
		}
	}
	return s.resolvePaneHosts()
}

// note records a degrade for the import report; NoteCap bounds the report, never the load.
func (s *Sidecar) note(format string, args ...any) error {
	if len(s.notes) >= NoteCap {
		return fmt.Errorf("%w: more than %d problems in one sidecar", ErrSidecarCap, NoteCap)
	}
	s.notes = append(s.notes, fmt.Sprintf(format, args...))
	return nil
}

// markUnknown records one preserved entry for the editor's count; INIDoc preserves the lines either way.
func (s *Sidecar) markUnknown(entry string) error {
	if len(s.unknown) >= PreservedCap {
		return fmt.Errorf("%w: more than %d unknown entries in one sidecar", ErrSidecarCap, PreservedCap)
	}
	s.unknown = append(s.unknown, entry)
	return nil
}

func (s *Sidecar) markUnknownSection(name string, keys []string) error {
	if err := s.markUnknown(name + iniSectionSep); err != nil {
		return err
	}
	for _, k := range keys {
		if err := s.markUnknown(name + iniSectionSep + k); err != nil {
			return err
		}
	}
	return nil
}

// markUnknownKeys records unknown keys of a known section, so a newer build's key survives a save.
func (s *Sidecar) markUnknownKeys(section string, keys []string, known func(string) bool) error {
	for _, k := range keys {
		if known(k) {
			continue
		}
		if err := s.markUnknown(section + iniSectionSep + k); err != nil {
			return err
		}
	}
	return nil
}

func keySet(keys ...string) func(string) bool {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return func(k string) bool {
		_, ok := set[k]
		return ok
	}
}

var themeKeyKnown = keySet(
	"format", "revision", "name", "author", "license", "credit", "description",
	"min_client", "base", "subtheme", "layout_preset", "style_preset", "generated_by",
)

// readTheme fills [theme]; all but `base` and `subtheme` degrade, while those name folders and refuse.
func (s *Sidecar) readTheme(d *INIDoc, keys []string) error {
	get := sectionGetter(d, secTheme)
	s.Revision = parse.AtoiOr(get("revision"), 0)
	s.Meta.Name = metaFree(get("name"))
	s.Meta.Author = metaFree(get("author"))
	s.Meta.License = metaFree(get("license"))
	s.Meta.Credit = metaFree(get("credit"))
	s.Meta.Description = metaFree(get("description"))
	// min_client is informational: enforcing it would break themes on downgrade.
	s.Meta.MinClient = metaPlain(get("min_client"))
	s.Meta.LayoutPreset = metaPlain(get("layout_preset"))
	s.Meta.StylePreset = metaPlain(get("style_preset"))
	s.Meta.GeneratedBy = metaPlain(get("generated_by"))
	var err error
	if s.Meta.Base, err = s.plainText(secTheme, "base", get("base")); err != nil {
		return err
	}
	if s.Meta.Subtheme, err = s.plainText(secTheme, "subtheme", get("subtheme")); err != nil {
		return err
	}
	return s.markUnknownKeys(secTheme, keys, themeKeyKnown)
}

func (s *Sidecar) readOverrides(d *INIDoc, keys []string) error {
	if len(keys) > OverrideCap {
		return capErr(secOverrides, len(keys), OverrideCap)
	}
	get := sectionGetter(d, secOverrides)
	for _, k := range keys {
		s.Overrides = append(s.Overrides, KeyRect{Key: k, Rect: parseRectValue(get(k))})
	}
	return nil
}

func (s *Sidecar) readRotations(d *INIDoc, keys []string) error {
	if len(keys) > OverrideCap {
		return capErr(secRotations, len(keys), OverrideCap)
	}
	get := sectionGetter(d, secRotations)
	for _, k := range keys {
		s.Rotations = append(s.Rotations, KeyAngle{Key: k, Angle: parseAngle(get(k))})
	}
	return nil
}

func (s *Sidecar) readPalette(d *INIDoc, keys []string) error {
	get := sectionGetter(d, secPalette)
	for _, role := range paletteRoleKeys {
		raw := get(role.key)
		if raw == "" {
			continue
		}
		c, ok := parseRGBAValue(raw)
		if !ok {
			_ = s.note("[palette] %s is not a colour (%q) — the stylesheet's own value is kept", role.key, raw)
			continue
		}
		assignPaletteSlot(&s.Palette, role.slot, c.RGB())
	}
	return s.markUnknownKeys(secPalette, keys, paletteKeyKnown)
}

var paletteKeyKnown = func() func(string) bool {
	names := make([]string, 0, len(paletteRoleKeys))
	for _, r := range paletteRoleKeys {
		names = append(names, r.key)
	}
	return keySet(names...)
}()

var chromeKeyKnown = keySet("shape", "shape_tier", "tabbar_hex")

func (s *Sidecar) readChrome(d *INIDoc, keys []string) error {
	get := sectionGetter(d, secChrome)
	var err error
	if s.Chrome.Shape, err = s.plainText(secChrome, "shape", get("shape")); err != nil {
		return err
	}
	s.Chrome.ShapeTier = parse.AtoiOr(get("shape_tier"), 0)
	if c, ok := parseRGBAValue(get("tabbar_hex")); ok {
		s.Chrome.TabbarHex = c
	}
	return s.markUnknownKeys(secChrome, keys, chromeKeyKnown)
}

func (s *Sidecar) readFonts(d *INIDoc, keys []string) error {
	if len(keys) > FontCap {
		return capErr(secFonts, len(keys), FontCap)
	}
	get := sectionGetter(d, secFonts)
	for _, k := range keys {
		file, err := s.plainText(secFonts, k, get(k))
		if err != nil {
			return err
		}
		s.Fonts = append(s.Fonts, FontFamily{Family: k, File: file})
	}
	return nil
}

func (s *Sidecar) readFontBind(d *INIDoc, keys []string) error {
	if len(keys) > FontBindCap {
		return capErr(secFontBind, len(keys), FontBindCap)
	}
	get := sectionGetter(d, secFontBind)
	for _, k := range keys {
		fam, err := s.plainText(secFontBind, k, get(k))
		if err != nil {
			return err
		}
		s.FontBind = append(s.FontBind, KV{Key: k, Value: fam})
	}
	return nil
}

func (s *Sidecar) readMedia(d *INIDoc, keys []string) error {
	if len(keys) > MediaCap {
		return capErr(secMedia, len(keys), MediaCap)
	}
	get := sectionGetter(d, secMedia)
	for _, k := range keys {
		if !validID(k) {
			_ = s.note("[media] %q is not a valid id ([a-z0-9_-], %d max) — the row is kept in the file and ignored", k, IDRuneCap)
			continue
		}
		// "file, bytes, hash": the importer checks size and hash before decoding a stranger's file.
		parts := strings.Split(get(k), tupleSeparator)
		e := MediaEntry{ID: k}
		var err error
		if len(parts) > 0 {
			if e.File, err = s.plainText(secMedia, k, parts[0]); err != nil {
				return err
			}
		}
		if len(parts) > 1 {
			e.Bytes = int64(parse.AtoiOr(parts[1], 0))
		}
		if len(parts) > 2 {
			e.Hash = strings.ToLower(strings.TrimSpace(parts[2]))
		}
		s.Media = append(s.Media, e)
	}
	return nil
}

func (s *Sidecar) readSounds(d *INIDoc, keys []string) error {
	if len(keys) > SoundCap {
		return capErr(secSounds, len(keys), SoundCap)
	}
	get := sectionGetter(d, secSounds)
	for _, k := range keys {
		v, err := s.plainText(secSounds, k, get(k))
		if err != nil {
			return err
		}
		s.Sounds = append(s.Sounds, KV{Key: k, Value: v})
	}
	return nil
}

var importKeyKnown = keySet(
	"derived_from", "derived_at", "derived_hash", "subthemes", "per_misc", "lobby_design",
)

func (s *Sidecar) readImport(d *INIDoc, keys []string) error {
	get := sectionGetter(d, secImport)
	// [import] is all metadata: read unbounded, degraded at the end of the parse (metatext.go).
	s.Import.DerivedFrom = metaPlain(get("derived_from"))
	s.Import.DerivedAt = metaPlain(get("derived_at"))
	s.Import.DerivedHash = metaPlain(get("derived_hash"))
	list, err := parseTextList(get("subthemes"), SubthemeCap)
	if err != nil {
		return fmt.Errorf("%w: [import] subthemes holds more than %d entries", ErrSidecarCap, SubthemeCap)
	}
	s.Import.Subthemes = list
	s.Import.PerMisc = parseBoolValue(get("per_misc"))
	s.Import.LobbyDesign = parseBoolValue(get("lobby_design"))
	return s.markUnknownKeys(secImport, keys, importKeyKnown)
}

func (s *Sidecar) readClock(d *INIDoc, name string, keys []string) error {
	idx := strings.TrimPrefix(name, secClockPrefix)
	n := parse.AtoiOr(idx, 0)
	// Clock 0 is the shared anchor; an index past the pool is preserved and reported, not folded.
	if !validID(idx) || n <= 0 || n >= ClockCap {
		_ = s.note("[%s] is not a clock group (1..%d) — preserved and ignored", name, ClockCap-1)
		return s.markUnknownSection(name, keys)
	}
	get := sectionGetter(d, name)
	// The id is kept as spelled: "01" reads as clock 1 and must be written back to [clock.01].
	s.Clocks[n] = ClockSpec{ID: idx, SpeedPct: parseSpeedPct(get("speed_pct")), Declared: true}
	return s.markUnknownKeys(name, keys, clockKeyKnown)
}

var clockKeyKnown = keySet("speed_pct")

var paneKeyKnown = keySet("rect", "space", "hosts", "divider", "divider_px")

func (s *Sidecar) readPane(d *INIDoc, name string, keys []string) error {
	id := strings.TrimPrefix(name, secPanePrefix)
	if !validID(id) {
		_ = s.note("[%s] is not a valid pane id ([a-z0-9_-], %d max) — preserved and ignored", name, IDRuneCap)
		return s.markUnknownSection(name, keys)
	}
	if len(s.Panes) >= PaneCap {
		return capErr("pane.*", len(s.Panes)+1, PaneCap)
	}
	get := sectionGetter(d, name)
	p := Pane{ID: id, Rect: parseRectValue(get("rect"))}
	p.Space = Space(s.enumOrDegrade(name, "space", get("space"), spaceNames[:], uint8(SpaceCourtroom)))
	if c, ok := parseRGBAValue(get("divider")); ok {
		p.Divider = c
	}
	p.DividerPx = uint8(value.Clamp(parse.AtoiOr(get("divider_px"), 0), 0, math.MaxUint8))

	hosts, err := parseTextList(get("hosts"), PaneHostCap)
	if err != nil {
		return capErr("["+name+"] hosts", PaneHostCap+1, PaneHostCap)
	}
	for _, h := range hosts {
		p.Hosts[p.NHosts] = h
		p.NHosts++
	}
	s.Panes = append(s.Panes, p)
	return s.markUnknownKeys(name, keys, paneKeyKnown)
}

// resolvePaneHosts enforces cross-pane rules after every pane is read; a violation drops the host, never the theme.
func (s *Sidecar) resolvePaneHosts() error {
	claimed := make(map[string]string, PaneCap*PaneHostCap)
	for i := range s.Panes {
		p := &s.Panes[i]
		kept := 0
		for h := 0; h < p.NHosts; h++ {
			key := p.Hosts[h]
			if forbiddenHost(key) {
				_ = s.note("[pane.%s] cannot host %q — it is the space every other rect lives in", p.ID, key)
				continue
			}
			if owner, dup := claimed[key]; dup {
				_ = s.note("[pane.%s] also claims %q, already hosted by [pane.%s] — a widget draws exactly once", p.ID, key, owner)
				continue
			}
			claimed[key] = p.ID
			p.Hosts[kept] = key
			kept++
		}
		for h := kept; h < p.NHosts; h++ {
			p.Hosts[h] = ""
		}
		p.NHosts = kept
	}
	return nil
}

func forbiddenHost(key string) bool {
	for _, k := range paneForbiddenHosts {
		if k == key {
			return true
		}
	}
	return false
}

var effectBindKeyKnown = keySet("effect", "color", "period_ms", "amp_pct", "clock")

// validEffectTarget is not validID, because the suffix is an AO2 widget key.
// The writer asks the same question, or it saves an effect the next load drops.
func validEffectTarget(target string) bool {
	return target != "" && utf8.RuneCountInString(target) <= AnchorRuneCap
}

func (s *Sidecar) readEffectBind(d *INIDoc, name string, keys []string) error {
	target := strings.TrimPrefix(name, secEffectPrefix)
	if !validEffectTarget(target) {
		_ = s.note("[%s] names no AO2 widget — preserved and ignored", name)
		return s.markUnknownSection(name, keys)
	}
	if len(s.Effects) >= EffectBindCap {
		return capErr("effect.*", len(s.Effects)+1, EffectBindCap)
	}
	get := sectionGetter(d, name)
	e := EffectBind{Target: target}
	e.Effect = EffectKind(s.enumOrDegrade(name, "effect", get("effect"), effectNames[:], uint8(FXNone)))
	if c, ok := parseRGBAValue(get("color")); ok {
		e.Color = c
	}
	e.PeriodMs = int32(value.Clamp(parse.AtoiOr(get("period_ms"), 0), TimeMsMin, TimeMsMax))
	e.AmpPct = int16(value.Clamp(parse.AtoiOr(get("amp_pct"), 0), 0, AmpMaxPct))
	e.Clock = parseClockIndex(get("clock"))
	s.Effects = append(s.Effects, e)
	return s.markUnknownKeys(name, keys, effectBindKeyKnown)
}

// elementKeyKnown is the writer's field table plus read-only aliases, so a written key is known.
var elementKeyKnown = func() func(string) bool {
	names := make([]string, 0, len(elementFieldKeys)+len(elementKeyAliases))
	for _, e := range elementFieldKeys {
		if e.key != "" {
			names = append(names, e.key)
		}
	}
	names = append(names, elementKeyAliases...)
	return keySet(names...)
}()

// elementKeyAliases are spellings read but never written; "nine_slice = N" is shorthand for a symmetric inset.
var elementKeyAliases = []string{"nine_slice"}

func (s *Sidecar) readElement(d *INIDoc, name string, keys []string) error {
	id := strings.TrimPrefix(name, secElementPrefix)
	if !validID(id) {
		_ = s.note("[%s] is not a valid element id ([a-z0-9_-], %d max) — preserved and ignored", name, IDRuneCap)
		return s.markUnknownSection(name, keys)
	}
	if len(s.Elements) >= ElementCap {
		return capErr("element.*", len(s.Elements)+1, ElementCap)
	}
	get := sectionGetter(d, name)

	// An unknown kind skips the element and preserves the section; absent is image, inert without media.
	rawKind := get("kind")
	kind := ElemImage
	if rawKind != "" {
		v, ok := enumValue(elementKindNames[:], rawKind)
		if !ok {
			// Past NoteCap this must keep loading, or elements from a newer client would refuse the whole theme.
			_ = s.note("[%s] kind = %q is not a kind this build draws — the element is skipped and its section preserved", name, rawKind)
			return s.markUnknownKeys(name, keys, elementKeyKnown)
		}
		kind = ElementKind(v)
	}

	e := Element{ID: id, Kind: kind}
	e.Band = ElementBand(s.enumOrDegrade(name, "band", get("band"), elementBandNames[:], uint8(BandBackdrop)))
	e.Z = int16(value.Clamp(parse.AtoiOr(get("z"), 0), ZMin, ZMax))
	e.Space = Space(s.enumOrDegrade(name, "space", get("space"), spaceNames[:], uint8(SpaceCourtroom)))

	var err error
	if e.Anchor, err = s.boundedText(name, "anchor", get("anchor"), AnchorRuneCap); err != nil {
		return err
	}
	e.Rect = parseRectValue(get("rect"))
	e.Rot = parseAngle(get("rot"))

	if e.Media, err = s.boundedText(name, "media", get("media"), IDRuneCap); err != nil {
		return err
	}
	if e.Shape, err = s.boundedText(name, "shape", get("shape"), IDRuneCap); err != nil {
		return err
	}
	if e.Gen, err = s.boundedText(name, "generator", get("generator"), IDRuneCap); err != nil {
		return err
	}
	if e.GenParams, err = s.genParams(name, get("gen_params")); err != nil {
		return err
	}
	e.Fit = ElementFit(s.enumOrDegrade(name, "fit", get("fit"), fitNames[:], uint8(FitStretch)))
	e.Slice = parseSliceValue(get("slice"))
	if raw := get("nine_slice"); raw != "" && get("slice") == "" {
		n := int16(value.Clamp(parse.AtoiOr(raw, 0), ZMin, ZMax))
		e.Slice = [4]int16{n, n, n, n}
	}

	if c, ok := parseRGBAValue(get("fill")); ok {
		e.Fill = c
	}
	if c, ok := parseRGBAValue(get("fill2")); ok {
		e.Fill2 = c
	}
	e.GradAngle = parseAngle(get("grad_angle"))
	e.GradRadial = parseBoolValue(get("grad_radial"))
	if c, ok := parseRGBAValue(get("stroke")); ok {
		e.Stroke = c
	}
	e.StrokePx = uint8(value.Clamp(parse.AtoiOr(get("stroke_px"), 0), 0, math.MaxUint8))
	if c, ok := parseRGBAValue(get("tint")); ok {
		e.Tint = c
	}
	e.Opacity = uint8(value.Clamp(parse.AtoiOr(get("opacity"), 0), 0, math.MaxUint8))

	if e.Text, err = s.elementText(name, get("text")); err != nil {
		return err
	}
	if e.Font, err = s.boundedText(name, "font", get("font"), NameRuneCap); err != nil {
		return err
	}
	e.Size = value.Clamp(parse.AtoiOr(get("size"), 0), 0, TextSizeMaxPt)
	if c, ok := parseRGBAValue(get("color")); ok {
		e.Color = c
	}
	e.Align = TextAlign(s.enumOrDegrade(name, "align", get("align"), alignNames[:], uint8(AlignLeft)))

	e.Clock = parseClockIndex(get("clock"))
	// Clamp, never convert: see TimeMsMin.
	e.PhaseMs = int32(value.Clamp(parse.AtoiOr(get("phase_ms"), 0), TimeMsMin, TimeMsMax))
	e.Loop = parseBoolValue(get("loop"))
	e.Effect = EffectKind(s.enumOrDegrade(name, "effect", get("effect"), effectNames[:], uint8(FXNone)))
	e.EffectPeriodMs = int32(value.Clamp(parse.AtoiOr(get("effect_period_ms"), 0), TimeMsMin, TimeMsMax))
	e.EffectAmpPct = int16(value.Clamp(parse.AtoiOr(get("effect_amp_pct"), 0), 0, AmpMaxPct))
	// "decimate" is stored inverted, so an absent key must leave NoDecimate false.
	if raw := get("decimate"); raw != "" {
		e.NoDecimate = !parseBoolValue(raw)
	}

	if e.VisibleWhen, err = s.condition(name, get("visible_when")); err != nil {
		return err
	}
	e.Locked = parseBoolValue(get("locked"))
	e.Hidden = parseBoolValue(get("hidden"))

	s.Elements = append(s.Elements, e)
	return s.markUnknownKeys(name, keys, elementKeyKnown)
}

// Value parsing. The writer compares format(parse(raw)), so unchanged and unknown values survive a save.

// sectionGetter binds a section, returning "" for an absent key; values arrive trimmed, without any ';' comment.
func sectionGetter(d *INIDoc, section string) func(string) string {
	return func(key string) string {
		v, _ := d.Get(section, key)
		return v
	}
}

func capErr(what string, got, limit int) error {
	return fmt.Errorf("%w: %s holds %d entries, cap is %d", ErrSidecarCap, what, got, limit)
}

// enumOrDegrade degrades an unknown wire name to the fallback with a note; the raw text stays.
func (s *Sidecar) enumOrDegrade(section, key, raw string, names []string, fallback uint8) uint8 {
	if raw == "" {
		return fallback
	}
	if v, ok := enumValue(names, raw); ok {
		return v
	}
	// Absorbed: losing a theme over a diagnostic is worse than a silent degrade past NoteCap.
	_ = s.note("[%s] %s = %q is not a value this build knows — using %q, and keeping the original in the file",
		section, key, raw, enumName(names, fallback))
	return fallback
}

// checkRuneCap is the one length rule for reader and writer, so the editor cannot save a file it cannot load.
func checkRuneCap(section, key, v string, limit int) error {
	if n := utf8.RuneCountInString(v); n > limit {
		return fmt.Errorf("%w: [%s] %s is %d runes, cap is %d", ErrSidecarCap, section, key, n, limit)
	}
	return nil
}

// plainText is length-bounded but never percent-decoded, as the format defines no escape here.
func (s *Sidecar) plainText(section, key, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	return v, checkRuneCap(section, key, v, NameRuneCap)
}

// metaFree and metaPlain skip the length refusal; degradeMeta applies the bound (metatext.go).
func metaFree(raw string) string  { return decodeFreeText(raw) }
func metaPlain(raw string) string { return strings.TrimSpace(raw) }

func (s *Sidecar) boundedText(section, key, raw string, limit int) (string, error) {
	v := strings.TrimSpace(raw)
	return v, checkRuneCap(section, key, v, limit)
}

// elementText is refused past TextRuneCap, as a shortened watermark saved back destroys the line.
func (s *Sidecar) elementText(section, raw string) (string, error) {
	v := decodeFreeText(raw)
	return v, checkRuneCap(section, elementTextKey, v, TextRuneCap)
}

// genParamsOf parses "k=v, k=v" in order, as the hash is a texture cache key; the writer shares it.
func genParamsOf(raw string) ([GenParamCap]KV, error) {
	var out [GenParamCap]KV
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	n := 0
	for _, part := range strings.Split(raw, tupleSeparator) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n >= GenParamCap {
			return out, capErr("gen_params", n+1, GenParamCap)
		}
		k, v, _ := strings.Cut(part, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if utf8.RuneCountInString(k) > GenParamRuneCap || utf8.RuneCountInString(v) > GenParamRuneCap {
			return out, fmt.Errorf("%w: a gen_params entry is longer than %d runes",
				ErrSidecarCap, GenParamRuneCap)
		}
		out[n] = KV{Key: k, Value: v}
		n++
	}
	return out, nil
}

func (s *Sidecar) genParams(section, raw string) ([GenParamCap]KV, error) {
	out, err := genParamsOf(raw)
	if err != nil {
		return out, fmt.Errorf("[%s]: %w", section, err)
	}
	return out, nil
}

// conditionOf parses "always | speaking | pos:X | char:X | side:X | shout:X".
// An unknown axis degrades to always visible; problems return as text so the writer invents no notes.
func conditionOf(raw string) (Condition, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Condition{}, "", nil
	}
	axisText, value, hasValue := strings.Cut(raw, ":")
	v, ok := enumValue(conditionAxisNames[:], axisText)
	if !ok {
		return Condition{}, fmt.Sprintf("= %q is not an axis this build knows — the element stays visible", raw), nil
	}
	axis := ConditionAxis(v)
	if !conditionAxisTakesValue[axis] {
		return Condition{Axis: axis}, "", nil
	}
	if !hasValue {
		return Condition{}, fmt.Sprintf("= %q names an axis with no value — the element stays visible", raw), nil
	}
	value = strings.TrimSpace(value)
	if n := utf8.RuneCountInString(value); n > ConditionValueRuneCap {
		return Condition{}, "", fmt.Errorf("%w: a visible_when value is %d runes, cap is %d",
			ErrSidecarCap, n, ConditionValueRuneCap)
	}
	return Condition{Axis: axis, Value: value}, "", nil
}

func (s *Sidecar) condition(section, raw string) (Condition, error) {
	c, problem, err := conditionOf(raw)
	if err != nil {
		return Condition{}, fmt.Errorf("[%s]: %w", section, err)
	}
	if problem != "" {
		_ = s.note("[%s] visible_when %s", section, problem)
	}
	return c, nil
}

// parseRectValue reads "x, y, w, h"; missing or bad components are 0 for AO2 parity (see parse.AtoiOr).
func parseRectValue(raw string) Rect {
	var r Rect
	if strings.TrimSpace(raw) == "" {
		return r
	}
	parts := strings.Split(raw, tupleSeparator)
	dst := [rectComponentCount]*int{&r.X, &r.Y, &r.W, &r.H}
	for i := 0; i < rectComponentCount && i < len(parts); i++ {
		*dst[i] = parse.AtoiOr(parts[i], 0)
	}
	return r
}

func parseSliceValue(raw string) [4]int16 {
	var out [4]int16
	if strings.TrimSpace(raw) == "" {
		return out
	}
	parts := strings.Split(raw, tupleSeparator)
	for i := 0; i < len(out) && i < len(parts); i++ {
		out[i] = int16(value.Clamp(parse.AtoiOr(parts[i], 0), ZMin, ZMax))
	}
	return out
}

// parseRGBAValue reads "#rrggbb", "#rrggbbaa" or "r, g, b[, a]"; '#' starts a comment only at a line's beginning.
func parseRGBAValue(raw string) (RGBA, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return RGBA{}, false
	}
	if v[0] == '#' {
		c, ok := parse.HexColor(v)
		return RGBA{c.R, c.G, c.B, c.A}, ok
	}
	parts := strings.Split(v, tupleSeparator)
	if len(parts) < rgbFields {
		return RGBA{}, false
	}
	c := RGBA{
		R: uint8(value.Clamp(parse.AtoiOr(parts[0], 0), 0, math.MaxUint8)),
		G: uint8(value.Clamp(parse.AtoiOr(parts[1], 0), 0, math.MaxUint8)),
		B: uint8(value.Clamp(parse.AtoiOr(parts[2], 0), 0, math.MaxUint8)),
		A: OpacityOpaque,
	}
	if len(parts) > rgbFields {
		c.A = uint8(value.Clamp(parse.AtoiOr(parts[3], 0), 0, math.MaxUint8))
	}
	return c, true
}

// parseBoolValue accepts the six defined spellings; anything else is false, as AO2 reads its flags.
func parseBoolValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "yes", "true":
		return true
	}
	return false
}

func parseTextList(raw string, max int) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	for _, part := range strings.Split(raw, tupleSeparator) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(out) >= max {
			return nil, fmt.Errorf("%w: list holds more than %d entries", ErrSidecarCap, max)
		}
		out = append(out, part)
	}
	return out, nil
}

// parseAngle reads the 360/256 angle byte, wrapping because an angle is modular.
func parseAngle(raw string) uint8 {
	v := parse.AtoiOr(raw, 0) % AngleCount
	if v < 0 {
		v += AngleCount
	}
	return uint8(v)
}

// parseSpeedPct clamps a clock's speed; 0 means undeclared and stays 0.
func parseSpeedPct(raw string) int16 {
	v := parse.AtoiOr(raw, 0)
	if v == 0 {
		return 0
	}
	return int16(value.Clamp(v, SpeedMinPct, SpeedMaxPct))
}

// parseClockIndex falls back to shared clock 0 past the pool, not a neighbour's group.
func parseClockIndex(raw string) uint8 {
	v := parse.AtoiOr(raw, 0)
	if v <= 0 || v >= ClockCap {
		return 0
	}
	return uint8(v)
}
