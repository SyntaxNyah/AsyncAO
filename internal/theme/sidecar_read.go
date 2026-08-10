package theme

// The sidecar READER. It walks the lossless document (never a second parser),
// so classification here is the same classification the write path uses and the
// same one ParseINI uses — the parity FuzzINIDocRoundTrip pins.
//
// It never refuses on version, degrades every unknown enum value, and records
// everything it did not understand so the import report can say so out loud.
// It DOES refuse a file past a named LOAD cap: truncating a theme and then
// saving it back is data loss (hard rule 4's "refuse, never truncate").
//
// NoteCap is the one cap that is NOT a load cap. It bounds the import REPORT,
// so every degrade/skip site absorbs its error (`_ = s.note(...)`) and keeps
// loading: a theme is never refused for having too many things to say about it.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Known section names and prefixes. A section that matches neither is unknown:
// preserved verbatim, counted, and ignored.
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

	// elementTextKey is the wire key ElemText's string rides on. Named because
	// THREE places spell it — elementFieldKeys (the writer's schema), the
	// reader's rune-cap message and Sidecar.Validate — and hard rule 9 applies
	// to strings that carry meaning exactly as it does to numbers.
	elementTextKey = "text"
)

// ParseSidecar reads asyncao_theme.ini content into a Sidecar, keeping the
// lossless document for the write path.
//
// COLD PATH ONLY (hard rule 2): it allocates freely and must run on the
// theme-apply goroutine, never on a draw, decode or resolver path.
func ParseSidecar(src []byte) (*Sidecar, error) {
	doc, err := ParseINIDoc(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSidecarUnreadable, err)
	}
	s := &Sidecar{Format: ThemeFormatVersion, doc: doc}

	// The version is read BEFORE anything else so a migration runs against the
	// document the rest of the parse then walks.
	if raw, ok := doc.Get(secTheme, "format"); ok {
		if v := atoiTrim(raw); v > 0 {
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
	// THE METADATA DEGRADE, after the sections and before the caller sees the model:
	// an over-long credit shortens the STRING and notes it rather than refusing the
	// theme (metatext.go). It runs here, once, over the table both directions share,
	// so no reader of a metadata field ever has to remember to bound it.
	s.degradeMeta()
	return s, nil
}

// LoadSidecar reads one asyncao_theme.ini. A missing file is NOT an error: it
// is the stock-AO2-theme case, and it returns (nil, nil) so callers get the
// nil-safe Sidecar accessors instead of a branch.
//
// BLOCKING — goroutine only (hard rule 2).
func LoadSidecar(path string) (*Sidecar, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	// The stream, not a stat, enforces the cap: a file that grows between the
	// two cannot get past it. One byte over is read so the cap can refuse.
	src, err := io.ReadAll(io.LimitReader(f, IniDocByteCap+1))
	if err != nil {
		return nil, err
	}
	return ParseSidecar(src)
}

// LoadSidecarIn returns the sidecar of the FIRST directory that has one, plus
// that directory.
//
// ATOMIC, never merged per key across the theme ladder — unlike the three AO2
// INIs, which merge because AO2 merges them. Merging elements per id would
// materialise a fallback theme's decoration onto rects the author's canvas does
// not have, which is a visual bug with no author to blame.
//
// BLOCKING — goroutine only (hard rule 2).
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

// docSections is the ordered section view of a document: section names in
// DECLARATION order, each with its unique keys in declaration order.
//
// Order is load-bearing twice over: element z-ties break on declaration index,
// and a file whose element order depended on Go's map iteration would render
// differently on every launch.
type docSections struct {
	names []string
	keys  map[string][]string
}

// walkSections reads the document's own line list. INIDoc's fields are package
// -private and this is the package: there is no second parse, so there is
// nothing for a second parse to disagree with.
func walkSections(d *INIDoc) docSections {
	out := docSections{keys: map[string][]string{}}
	seenSec := map[string]bool{}
	seenKey := map[string]bool{}
	for _, l := range d.lines {
		if !seenSec[l.section] {
			// A section is "declared" by its header OR by its first key: a
			// root-section key needs no header at all.
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

// readSections dispatches every section in declaration order.
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

// note records a degrade for the import report. Bounded by NoteCap (hard rule
// 4), which bounds the REPORT and never the load: every caller absorbs the
// error with `_ =` and carries on, so past the cap a degrade is silent but
// still correct. Refusing here would make §7.1 ("a reader NEVER refuses to
// load because of format") unenforceable, because §7.4's unknown-kind skip —
// the forward-compat path itself — notes once per element.
//
// The error return is kept so the drop is VISIBLE at each call site rather
// than hidden inside this function; a site that legitimately refuses uses a
// load cap (ElementCap, PreservedCap, …), never this one.
func (s *Sidecar) note(format string, args ...any) error {
	if len(s.notes) >= NoteCap {
		return fmt.Errorf("%w: more than %d problems in one sidecar", ErrSidecarCap, NoteCap)
	}
	s.notes = append(s.notes, fmt.Sprintf(format, args...))
	return nil
}

// markUnknown records one preserved entry: "section/key", or "section/" for a
// whole section. The LINES are preserved by INIDoc either way — this is only
// the enumeration the editor's card counts.
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

// markUnknownKeys records the keys of a KNOWN section that this build does not
// define — the forward-compat case that matters most, because it is how a
// newer AsyncAO's element key survives an older one's save.
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

// keySet turns a wire-key table into a membership test.
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

// ---------------------------------------------------------------------------
// Sections
// ---------------------------------------------------------------------------

var themeKeyKnown = keySet(
	"format", "revision", "name", "author", "license", "credit", "description",
	"min_client", "base", "subtheme", "layout_preset", "style_preset", "generated_by",
)

// readTheme fills [theme]. Every key here except `base` and `subtheme` is
// METADATA (metatext.go), so it is read UNBOUNDED and degraded once at the end of
// the parse — a 241-rune credit shortens with a note instead of costing the user
// the entire theme. The two exceptions name FOLDERS the loader resolves, so a
// short version means a different theme and the file is refused.
func (s *Sidecar) readTheme(d *INIDoc, keys []string) error {
	get := sectionGetter(d, secTheme)
	s.Revision = atoiTrim(get("revision"))
	s.Meta.Name = metaFree(get("name"))
	s.Meta.Author = metaFree(get("author"))
	s.Meta.License = metaFree(get("license"))
	s.Meta.Credit = metaFree(get("credit"))
	s.Meta.Description = metaFree(get("description"))
	// min_client is INFORMATIONAL and never enforced: enforcing it bricks
	// themes on downgrade for no gain.
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
			// DEGRADE path: NoteCap bounds the report, not the load (see note).
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
	s.Chrome.ShapeTier = atoiTrim(get("shape_tier"))
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
			// SKIP path: NoteCap bounds the report, not the load (see note).
			_ = s.note("[media] %q is not a valid id ([a-z0-9_-], %d max) — the row is kept in the file and ignored", k, IDRuneCap)
			continue
		}
		// "file, bytes, hash" — the size and hash are the author's declaration,
		// checked by the importer BEFORE it decodes a stranger's file.
		parts := strings.Split(get(k), tupleSeparator)
		e := MediaEntry{ID: k}
		var err error
		if len(parts) > 0 {
			if e.File, err = s.plainText(secMedia, k, parts[0]); err != nil {
				return err
			}
		}
		if len(parts) > 1 {
			e.Bytes = int64(atoiTrim(parts[1]))
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
	// [import] is provenance in its entirety and therefore metadata: read unbounded,
	// degraded with a note at the end of the parse (metatext.go).
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
	n := atoiTrim(idx)
	// Clock 0 is the shared theme anchor and is never declared; an index past
	// the pool is a file we cannot honour, so it is preserved and reported
	// rather than silently folded onto somebody else's group.
	if !validID(idx) || n <= 0 || n >= ClockCap {
		// SKIP path: NoteCap bounds the report, not the load (see note).
		_ = s.note("[%s] is not a clock group (1..%d) — preserved and ignored", name, ClockCap-1)
		return s.markUnknownSection(name, keys)
	}
	get := sectionGetter(d, name)
	// The id is kept AS SPELLED (see ClockSpec.ID): "01" reads as clock 1 and
	// must be written back to [clock.01], not to a second section this build
	// invented from the index.
	s.Clocks[n] = ClockSpec{ID: idx, SpeedPct: parseSpeedPct(get("speed_pct")), Declared: true}
	return s.markUnknownKeys(name, keys, clockKeyKnown)
}

var clockKeyKnown = keySet("speed_pct")

var paneKeyKnown = keySet("rect", "space", "hosts", "divider", "divider_px")

func (s *Sidecar) readPane(d *INIDoc, name string, keys []string) error {
	id := strings.TrimPrefix(name, secPanePrefix)
	if !validID(id) {
		// SKIP path: NoteCap bounds the report, not the load (see note).
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
	p.DividerPx = uint8(clampInt(atoiTrim(get("divider_px")), 0, 255))

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

// resolvePaneHosts enforces the two structural pane rules AFTER every pane is
// read, because both are cross-pane properties:
//
//   - host sets are DISJOINT (a widget draws exactly once, so it can belong to
//     exactly one pane; a later claim on the same key is dropped)
//   - "courtroom" and "viewport" can never be hosted (paneForbiddenHosts)
//
// A violation drops the HOST, never the theme: the rest of the pane is a
// perfectly good split, and refusing to load would punish the reader of a file
// the author can no longer fix.
func (s *Sidecar) resolvePaneHosts() error {
	claimed := make(map[string]string, PaneCap*PaneHostCap)
	for i := range s.Panes {
		p := &s.Panes[i]
		kept := 0
		for h := 0; h < p.NHosts; h++ {
			key := p.Hosts[h]
			if forbiddenHost(key) {
				// DROP-the-host path: NoteCap bounds the report, not the load (see note).
				_ = s.note("[pane.%s] cannot host %q — it is the space every other rect lives in", p.ID, key)
				continue
			}
			if owner, dup := claimed[key]; dup {
				// DROP-the-host path: NoteCap bounds the report, not the load (see note).
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

// validEffectTarget is THE rule for an [effect.<target>] section name, and it is
// deliberately NOT validID: the suffix is an AO2 WIDGET KEY the client resolves
// against themeSlots, not an author-chosen id, so the only grammar the format can
// state is "there is one, and it fits the widget-key bound" — AnchorRuneCap, the
// same bound an element's `anchor` is held to, because both name that same
// vocabulary.
//
// It is a named predicate rather than the inline test it used to be because the
// WRITER's guard (Sidecar.validateIDs) must ask exactly this question of the
// model: a target this refuses is preserved in the file and dropped from the
// model on the next load, so a writer that did not ask it would save an effect
// the author can no longer see. One spelling, both directions.
func validEffectTarget(target string) bool {
	return target != "" && utf8.RuneCountInString(target) <= AnchorRuneCap
}

func (s *Sidecar) readEffectBind(d *INIDoc, name string, keys []string) error {
	target := strings.TrimPrefix(name, secEffectPrefix)
	if !validEffectTarget(target) {
		// SKIP path: NoteCap bounds the report, not the load (see note).
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
	e.PeriodMs = int32(clampInt(atoiTrim(get("period_ms")), TimeMsMin, TimeMsMax))
	e.AmpPct = int16(clampInt(atoiTrim(get("amp_pct")), 0, AmpMaxPct))
	e.Clock = parseClockIndex(get("clock"))
	s.Effects = append(s.Effects, e)
	return s.markUnknownKeys(name, keys, effectBindKeyKnown)
}

// elementKeyKnown is built from the writer's own field table plus the read-only
// aliases, so the two halves of the format cannot drift: a key the writer emits
// is a key the reader calls known, by construction.
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

// elementKeyAliases are spellings the reader accepts and the writer never
// emits. "nine_slice = N" is the scalar shorthand for a symmetric 9-slice
// inset; it appears in the published examples, so it must parse, but one
// spelling has to be canonical or a round trip would have to choose.
var elementKeyAliases = []string{"nine_slice"}

func (s *Sidecar) readElement(d *INIDoc, name string, keys []string) error {
	id := strings.TrimPrefix(name, secElementPrefix)
	if !validID(id) {
		// SKIP path: NoteCap bounds the report, not the load (see note).
		_ = s.note("[%s] is not a valid element id ([a-z0-9_-], %d max) — preserved and ignored", name, IDRuneCap)
		return s.markUnknownSection(name, keys)
	}
	if len(s.Elements) >= ElementCap {
		return capErr("element.*", len(s.Elements)+1, ElementCap)
	}
	get := sectionGetter(d, name)

	// An UNKNOWN KIND skips the element and preserves the section (rule 3's
	// strongest case: a newer client's element type must not cost the rest of
	// the theme). An ABSENT kind is the zero value, image, which is inert
	// without media — so a half-written section cannot paint either.
	rawKind := get("kind")
	kind := ElemImage
	if rawKind != "" {
		v, ok := enumValue(elementKindNames[:], rawKind)
		if !ok {
			// THE forward-compat path (§7.4): NoteCap bounds the report, never
			// the load, or 65 elements from a newer client would refuse the
			// whole theme — the exact opposite of what rule 3 exists for.
			_ = s.note("[%s] kind = %q is not a kind this build draws — the element is skipped and its section preserved", name, rawKind)
			return s.markUnknownKeys(name, keys, elementKeyKnown)
		}
		kind = ElementKind(v)
	}

	e := Element{ID: id, Kind: kind}
	e.Band = ElementBand(s.enumOrDegrade(name, "band", get("band"), elementBandNames[:], uint8(BandBackdrop)))
	e.Z = int16(clampInt(atoiTrim(get("z")), ZMin, ZMax))
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
		// The scalar shorthand: one inset on all four edges.
		n := int16(clampInt(atoiTrim(raw), ZMin, ZMax))
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
	e.StrokePx = uint8(clampInt(atoiTrim(get("stroke_px")), 0, 255))
	if c, ok := parseRGBAValue(get("tint")); ok {
		e.Tint = c
	}
	e.Opacity = uint8(clampInt(atoiTrim(get("opacity")), 0, 255))

	if e.Text, err = s.elementText(name, get("text")); err != nil {
		return err
	}
	if e.Font, err = s.boundedText(name, "font", get("font"), NameRuneCap); err != nil {
		return err
	}
	e.Size = clampInt(atoiTrim(get("size")), 0, TextSizeMaxPt)
	if c, ok := parseRGBAValue(get("color")); ok {
		e.Color = c
	}
	e.Align = TextAlign(s.enumOrDegrade(name, "align", get("align"), alignNames[:], uint8(AlignLeft)))

	e.Clock = parseClockIndex(get("clock"))
	// The millisecond fields CLAMP into int32 rather than converting into it:
	// see TimeMsMin. A bare int32() would read a legal 3000000000 as a negative
	// phase here and as 2147483647 on a 32-bit build.
	e.PhaseMs = int32(clampInt(atoiTrim(get("phase_ms")), TimeMsMin, TimeMsMax))
	e.Loop = parseBoolValue(get("loop"))
	e.Effect = EffectKind(s.enumOrDegrade(name, "effect", get("effect"), effectNames[:], uint8(FXNone)))
	e.EffectPeriodMs = int32(clampInt(atoiTrim(get("effect_period_ms")), TimeMsMin, TimeMsMax))
	e.EffectAmpPct = int16(clampInt(atoiTrim(get("effect_amp_pct")), 0, AmpMaxPct))
	// "decimate" is stored INVERTED so the zero value is version 1's behaviour.
	// An ABSENT key must therefore leave NoDecimate false, which is why the
	// negation is guarded rather than applied to an empty string.
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

// ---------------------------------------------------------------------------
// Value parsing. Every parser here has a formatter twin in sidecar_write.go,
// and the writer compares format(parse(raw)) against what it wants to write —
// which is what makes an unchanged key, an out-of-range value and an UNKNOWN
// ENUM VALUE all survive a save byte-identically.
// ---------------------------------------------------------------------------

// sectionGetter binds a section, returning "" for an absent key. Values arrive
// already trimmed and already stripped of any ';' comment (INIDoc.Get).
func sectionGetter(d *INIDoc, section string) func(string) string {
	return func(key string) string {
		v, _ := d.Get(section, key)
		return v
	}
}

func capErr(what string, got, limit int) error {
	return fmt.Errorf("%w: %s holds %d entries, cap is %d", ErrSidecarCap, what, got, limit)
}

// enumOrDegrade resolves a wire name, degrading an unknown value to the safe
// primitive and NOTING it. The raw text stays in the file: the writer skips a
// key whose meaning did not change, and a degraded value's meaning is exactly
// what the degrade produced.
func (s *Sidecar) enumOrDegrade(section, key, raw string, names []string, fallback uint8) uint8 {
	if raw == "" {
		return fallback
	}
	if v, ok := enumValue(names, raw); ok {
		return v
	}
	// A note allocation failure here would lose the whole theme over a
	// diagnostic, so the cap is absorbed: past NoteCap the degrade is silent
	// but still correct.
	_ = s.note("[%s] %s = %q is not a value this build knows — using %q, and keeping the original in the file",
		section, key, raw, enumName(names, fallback))
	return fallback
}

// checkRuneCap is THE length rule for every bounded text value in the format —
// the reader's four helpers below and the writer's guard (Sidecar.Validate) all
// run this one function, so a value the reader refuses is a value the writer
// refuses, with the same sentinel and the same sentence. A second spelling here
// is how the editor would come to save a file it cannot load.
func checkRuneCap(section, key, v string, limit int) error {
	if n := utf8.RuneCountInString(v); n > limit {
		return fmt.Errorf("%w: [%s] %s is %d runes, cap is %d", ErrSidecarCap, section, key, n, limit)
	}
	return nil
}

// (There is no NameRuneCap-bounded freeText reader. Every percent-encoded value in
// the format is now read by exactly one of the two rules W8 split them into: the
// twelve attribution/provenance strings are METADATA and truncate with a note
// (metaFree + degradeMeta, §7), and the one element value is elementText, bounded by
// TextRuneCap. A third helper that refused a free-text value at NameRuneCap would be
// the pre-§7 rule wearing the new spelling — the exact behaviour that cost a theme
// its whole appearance over a long credit line.)

// plainText is a value that is NOT free text: a family name, a file path, an
// id. It is length-bounded but never percent-decoded, because inventing an
// escape where the format does not define one is how two writers stop agreeing.
func (s *Sidecar) plainText(section, key, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	return v, checkRuneCap(section, key, v, NameRuneCap)
}

// metaFree / metaPlain are the free-text decode / plainText WITHOUT the length
// refusal, for the metadata class alone. The bound is not skipped, it is MOVED:
// degradeMeta applies it once, at the end of the parse, over the one table both
// directions share (metatext.go). Splitting it that way is what lets the rule be stated in a
// single place instead of at a dozen assignment sites that each have to remember
// which of the two caps they are under.
func metaFree(raw string) string  { return decodeFreeText(raw) }
func metaPlain(raw string) string { return strings.TrimSpace(raw) }

func (s *Sidecar) boundedText(section, key, raw string, limit int) (string, error) {
	v := strings.TrimSpace(raw)
	return v, checkRuneCap(section, key, v, limit)
}

// elementText is the one free-text ELEMENT value, bounded by TextRuneCap.
// REFUSED, not truncated: a silently shortened watermark that then gets saved
// back has destroyed the author's line.
func (s *Sidecar) elementText(section, raw string) (string, error) {
	v := decodeFreeText(raw)
	return v, checkRuneCap(section, elementTextKey, v, TextRuneCap)
}

// genParamsOf parses "k=v, k=v". Ordered, because the generator's parameter
// hash is the texture cache key and is order-sensitive.
//
// PURE, with no Sidecar receiver, because the WRITER canonicalises through the
// same function: a value the writer could not re-derive by parsing is a value
// it would silently reformat.
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
// An unknown axis degrades to always-visible: an element a future axis would
// have hidden is better shown than lost.
//
// It returns the PROBLEM as text rather than noting it, so the writer can share
// the parse without inventing notes on a save.
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

// parseRectValue reads "x, y, w, h". Missing components are 0 and a bad one is
// 0, because atoiTrim swallows the error ON PURPOSE — AO2 parity, so a tuple
// AO2 renders can never be stricter here (see atoiTrim).
func parseRectValue(raw string) Rect {
	var r Rect
	if strings.TrimSpace(raw) == "" {
		return r
	}
	parts := strings.Split(raw, tupleSeparator)
	dst := [rectComponentCount]*int{&r.X, &r.Y, &r.W, &r.H}
	for i := 0; i < rectComponentCount && i < len(parts); i++ {
		*dst[i] = atoiTrim(parts[i])
	}
	return r
}

// parseSliceValue reads the four 9-slice insets in source pixels.
func parseSliceValue(raw string) [4]int16 {
	var out [4]int16
	if strings.TrimSpace(raw) == "" {
		return out
	}
	parts := strings.Split(raw, tupleSeparator)
	for i := 0; i < len(out) && i < len(parts); i++ {
		out[i] = int16(clampInt(atoiTrim(parts[i]), ZMin, ZMax))
	}
	return out
}

// parseRGBAValue reads "#rrggbb", "#rrggbbaa" or "r, g, b[, a]". '#' is safe in
// an INI value: it starts a comment only at the beginning of a line.
func parseRGBAValue(raw string) (RGBA, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return RGBA{}, false
	}
	if v[0] == '#' {
		hex := v[1:]
		if len(hex) != 6 && len(hex) != 8 {
			return RGBA{}, false
		}
		var out [4]uint8
		out[3] = 255
		for i := 0; i < len(hex)/2; i++ {
			hi, okHi := hexNibble(hex[i*2])
			lo, okLo := hexNibble(hex[i*2+1])
			if !okHi || !okLo {
				return RGBA{}, false
			}
			out[i] = hi<<4 | lo
		}
		return RGBA{out[0], out[1], out[2], out[3]}, true
	}
	parts := strings.Split(v, tupleSeparator)
	if len(parts) < 3 {
		return RGBA{}, false
	}
	c := RGBA{
		R: uint8(clampInt(atoiTrim(parts[0]), 0, 255)),
		G: uint8(clampInt(atoiTrim(parts[1]), 0, 255)),
		B: uint8(clampInt(atoiTrim(parts[2]), 0, 255)),
		A: 255,
	}
	if len(parts) > 3 {
		c.A = uint8(clampInt(atoiTrim(parts[3]), 0, 255))
	}
	return c, true
}

// parseBoolValue accepts the six spellings the format defines; anything else is
// false, which is the same leniency AO2 reads its own flags with.
func parseBoolValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "yes", "true":
		return true
	}
	return false
}

// parseTextList reads "a, b, c", refusing past max rather than truncating.
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

// parseAngle reads the 360/256 angle byte, wrapping rather than clamping: an
// angle is modular, so 300 is a real rotation and 256 is zero.
func parseAngle(raw string) uint8 {
	v := atoiTrim(raw) % AngleCount
	if v < 0 {
		v += AngleCount
	}
	return uint8(v)
}

// parseSpeedPct clamps a clock group's speed. 0 means UNDECLARED and resolves
// to nominal at use (ClockSpec.EffectivePct), so it is preserved as 0.
func parseSpeedPct(raw string) int16 {
	v := atoiTrim(raw)
	if v == 0 {
		return 0
	}
	return int16(clampInt(v, SpeedMinPct, SpeedMaxPct))
}

// parseClockIndex resolves a clock reference. An index past the pool falls back
// to the SHARED anchor (clock 0) rather than to a neighbour's group: being in
// step with the theme is the only safe wrong answer.
func parseClockIndex(raw string) uint8 {
	v := atoiTrim(raw)
	if v <= 0 || v >= ClockCap {
		return 0
	}
	return uint8(v)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
