package theme

// asyncao_theme.ini parsing allocates, so never run it on a draw, decode or resolver path (hard rule 2).

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	// SidecarFileName is outside AO2's three INI names, so no AO2 tool opens it.
	SidecarFileName = "asyncao_theme.ini"

	// ThemeFormatVersion is the grammar this build writes; reading is never limited by it.
	ThemeFormatVersion = 1

	// SidecarMediaDir holds imported art where AO2 never scans; relative, as it appears in [media] values.
	SidecarMediaDir = "asyncao/media"

	// VendorSectionPrefix and VendorKeyPrefix are reserved for third parties and carried through untouched.
	VendorSectionPrefix = "x."
	VendorKeyPrefix     = "x_"
)

// Named caps (hard rule 4). They refuse rather than truncate: a truncated theme that then gets saved is data loss.
const (
	// ElementCap bounds [element.*] at about 4x the densest style preset; baked entries are a fixed array.
	ElementCap = 96
	// PaneCap bounds [pane.*] — two-way splits on at most four regions.
	PaneCap = 8
	// PaneHostCap bounds the slot keys one pane may re-home.
	PaneHostCap = 8
	// TextRuneCap bounds an ElemText string, short enough that truncating it to its rect at bake stays cheap.
	TextRuneCap = 240
	// GenParamCap is exactly the addressable fields of ui.GenParams, so a twelfth pair could add nothing.
	GenParamCap = 11
	// ClockCap bounds the authoring clock groups; clock 0 is the shared theme anchor and is never declared.
	ClockCap = 16
	// MediaCap and FontCap bound [media] and [fonts]; MediaCap matches the 24-image texture budget.
	MediaCap = 24
	FontCap  = 6
	// FontBindCap bounds [fontbind], leaving room for append-only FontElements to grow.
	FontBindCap = 32
	// OverrideCap bounds [overrides] and [rotations] independently, at one entry per themeSlots row (ui.themeSlotCap).
	OverrideCap = 160
	// EffectBindCap aliases MediaCap, as each binding uses the stateful-effect pool sized to media.
	EffectBindCap = MediaCap
	// SoundCap bounds [sounds], the AsyncAO-tier sound overrides.
	SoundCap = 16
	// SubthemeCap bounds [import] subthemes, which are provenance rather than configuration.
	SubthemeCap = 16
	// ElementRetireCap is twice ElementCap: the document's elements plus sections this build skipped.
	ElementRetireCap = 2 * ElementCap
	// PreservedCap bounds the unknown keys and sections one file may carry; INIDoc keeps the lines anyway.
	PreservedCap = 512
	// NoteCap bounds the report, not the load: unknown kinds from a newer client must still load.
	NoteCap = 64
	// IDRuneCap is the id grammar's bound, [a-z0-9_-]{1,24}; ids are section suffixes.
	IDRuneCap = 24
	// ConditionValueRuneCap bounds a visible_when value, interned once per element at bake.
	ConditionValueRuneCap = 64
	// AnchorRuneCap bounds an anchor: a themeSlots key or a pane id.
	AnchorRuneCap = 48
	// NameRuneCap bounds every other short free-text field (name, author, licence, font family, media path).
	NameRuneCap = 240
	// GenParamRuneCap bounds one generator parameter's key or value.
	GenParamRuneCap = 48
)

// Numeric ranges, clamped on load rather than refused: an out-of-range percent is a typo, not a hostile file.
const (
	// OpacityOpaque is what an absent opacity means; 0 is the absent marker, so a zero Element cannot paint.
	OpacityOpaque = 255
	// SpeedNominalPct is a clock running at the shared theme anchor's rate.
	SpeedNominalPct = 100
	// SpeedMinPct and SpeedMaxPct: a tenth speed still reads as motion, and past 4x nothing is legible.
	SpeedMinPct = 10
	SpeedMaxPct = 400
	// AmpMaxPct bounds an effect amplitude (0..100 is the whole range).
	AmpMaxPct = 100
	// TextSizeMaxPt bounds an ElemText point size; 0 inherits the bound font's size.
	TextSizeMaxPt = 256
	// AngleCount is the modulus of the angle byte, the convention ThemeRectRotations persists, so 64 is 90 degrees.
	AngleCount = 256
	// ZMin and ZMax bound Element.Z and 9-slice insets (int16); reader, writer and docs/THEME-FORMAT.md
	// must agree, or a save rewrites an untouched line.
	ZMin = -32768
	ZMax = 32767
	// TimeMsMin and TimeMsMax bound millisecond keys (int32); clamp, never convert.
	// int32() would turn a legal large value negative, and only on 64-bit hosts.
	TimeMsMin = -2147483648
	TimeMsMax = 2147483647
)

var (
	// ErrSidecarCap reports a file refused for exceeding a named cap.
	ErrSidecarCap = errors.New("theme: sidecar exceeds a named cap")
	// ErrSidecarUnreadable reports a file the reader could not parse: not a theme, rather than a huge one.
	ErrSidecarUnreadable = errors.New("theme: sidecar is not readable as INI")
)

// Enums. Wire names are the format: append only, never renamed, or every theme on disk is re-mapped.

// ElementKind is what a free element paints.
type ElementKind uint8

const (
	ElemImage    ElementKind = iota // imported PNG/WebP/GIF/APNG/AVIF, static or animated
	ElemShape                       // procedural SDF silhouette (the shapemask family)
	ElemGradient                    // two-stop linear/radial plate
	ElemGen                         // procedural texture TILE
	ElemText                        // author-authored static string (watermark, credit, stamp)
	ElemPane                        // the plate/divider of a split-screen [pane.*]
	// ElementKindCount sizes internal/ui's painter table, a fixed array so the draw path needs no map.
	ElementKindCount
)

var elementKindNames = [ElementKindCount]string{"image", "shape", "gradient", "gen", "text", "pane"}

// ElementBand is where in the themed pass an element paints.
// Not a z-sort: the pass is hand-ordered and has a mid-sequence abort (the courtroom modals' early return).
type ElementBand uint8

const (
	BandBackdrop ElementBand = iota // above the courtroom backdrop, below every widget
	BandMid                         // between the stage and the panels — behind the logs
	BandOverlay                     // above everything, below editor chrome
	// ElementBandCount is the number of insertion points and of band loops internal/ui runs per themed frame.
	ElementBandCount
)

var elementBandNames = [ElementBandCount]string{"backdrop", "mid", "overlay"}

// Space is the coordinate space of an element's rect; the first four mirror ui.themeSlotRelSpace exactly.
type Space uint8

const (
	SpaceCourtroom    Space = iota // design-space, transformed by the canvas fit (default)
	SpaceChatbox                   // child of ao2_chatbox
	SpaceViewport                  // child of the stage
	SpaceMusicDisplay              // child of the music_display plate
	// SpaceWindow is window pixels outside the design canvas (letterbox bars, chrome band, vignette).
	SpaceWindow
	// SpacePane is a child of a named [pane.*]; the element's Anchor is a pane id rather than a slot key.
	SpacePane
	SpaceCount
)

var spaceNames = [SpaceCount]string{"courtroom", "chatbox", "viewport", "music_display", "window", "pane"}

// ElementFit is how a source image or tile fills the element's rect.
type ElementFit uint8

const (
	FitStretch ElementFit = iota // scale to the rect, ignoring aspect
	FitTile                      // repeat at source size
	FitContain                   // scale to fit, letterboxed
	FitCover                     // scale to fill, cropped
	FitNine                      // 9-slice using Element.Slice
	FitCount
)

var fitNames = [FitCount]string{"stretch", "tile", "contain", "cover", "nine"}

// EffectKind is an element's motion; the zero value FXNone keeps an element that names no effect static.
type EffectKind uint8

const (
	FXNone    EffectKind = iota
	FXFade               // decaying alpha ramp — self-clearing
	FXPulse              // periodic alpha
	FXBreathe            // periodic scale
	FXGlow               // periodic additive halo
	FXDrift              // periodic offset
	FXSpin               // continuous rotation
	FXShake              // decaying offset noise — self-clearing
	FXSlam               // one-shot scale impact — self-clearing
	// EffectKindCount sizes internal/ui's resolver table.
	EffectKindCount
)

var effectNames = [EffectKindCount]string{
	"none", "fade", "pulse", "breathe", "glow", "drift", "spin", "shake", "slam",
}

// TextAlign is an ElemText's horizontal alignment inside its rect.
type TextAlign uint8

const (
	AlignLeft TextAlign = iota
	AlignCenter
	AlignRight
	AlignCount
)

var alignNames = [AlignCount]string{"left", "center", "right"}

// ConditionAxis is the one axis a VisibleWhen may test; an expression language needs a frame-path parser.
type ConditionAxis uint8

const (
	CondAlways   ConditionAxis = iota // the zero value: always visible
	CondPos                           // pos:def, pos:pro, …
	CondChar                          // char:<folder>
	CondSide                          // side:def, side:pro, …
	CondSpeaking                      // someone is speaking (no value)
	CondShout                         // shout:objection, …
	ConditionAxisCount
)

var conditionAxisNames = [ConditionAxisCount]string{
	"always", "pos", "char", "side", "speaking", "shout",
}

// conditionAxisTakesValue reports whether an axis is spelled "<axis>:<value>".
// "always" and "speaking" are whole values on their own; the rest are prefixes.
var conditionAxisTakesValue = [ConditionAxisCount]bool{
	CondAlways: false, CondPos: true, CondChar: true,
	CondSide: true, CondSpeaking: false, CondShout: true,
}

// String returns the wire name, or "" for a value outside the enum.
func (k ElementKind) String() string   { return enumName(elementKindNames[:], uint8(k)) }
func (b ElementBand) String() string   { return enumName(elementBandNames[:], uint8(b)) }
func (s Space) String() string         { return enumName(spaceNames[:], uint8(s)) }
func (f ElementFit) String() string    { return enumName(fitNames[:], uint8(f)) }
func (e EffectKind) String() string    { return enumName(effectNames[:], uint8(e)) }
func (a TextAlign) String() string     { return enumName(alignNames[:], uint8(a)) }
func (a ConditionAxis) String() string { return enumName(conditionAxisNames[:], uint8(a)) }

func enumName(names []string, v uint8) string {
	if int(v) >= len(names) {
		return ""
	}
	return names[v]
}

// enumValue resolves a wire name case-insensitively; on ok=false callers degrade, and the raw text stays.
func enumValue(names []string, s string) (uint8, bool) {
	want := strings.ToLower(strings.TrimSpace(s))
	for i, n := range names {
		if n == want {
			return uint8(i), true
		}
	}
	return 0, false
}

// RGBA is a sidecar colour; alpha is needed because a frame element is a stroke over a transparent fill.
type RGBA struct{ R, G, B, A uint8 }

func (c RGBA) Opaque() bool { return c.A > 0 }

func (c RGBA) RGB() RGB { return RGB{c.R, c.G, c.B} }

// KV is one ordered generator parameter or sound override; the parameter hash is a cache key.
type KV struct{ Key, Value string }

type Condition struct {
	Axis  ConditionAxis
	Value string // interned to an index at bake; "" for the value-less axes
}

// Element is one free element; a field with no key in elementFieldKeys silently saves as nothing.
type Element struct {
	ID    string // [a-z0-9_-]{1,24}; the section suffix; editor-only
	Kind  ElementKind
	Band  ElementBand
	Z     int16 // order WITHIN the band; ties break on declaration index
	Space Space

	// Anchor is a themeSlots key, or a pane id when Space == SpacePane; "" makes Rect absolute in the Space.
	// Anchors may not chain to another element: one bake pass, no cycle detection, no depth cap.
	Anchor string
	Rect   Rect  // design px in the four AO2 spaces + SpacePane; WINDOW px in SpaceWindow
	Rot    uint8 // 360/256 angle byte — the ThemeRectRotations convention

	Media      string // ElemImage: an id into Sidecar.Media
	Shape      string // ElemShape: silhouette name ("sharp", "rounded", "pill", "hex", "ribbon", "tape", …)
	Gen        string // ElemGen: generator kind
	GenParams  [GenParamCap]KV
	Fit        ElementFit
	Slice      [4]int16 // 9-slice insets in SOURCE px, when Fit == FitNine
	Fill       RGBA     // ElemGradient stop A / ElemShape fill / ElemPane plate
	Fill2      RGBA     // ElemGradient stop B
	GradAngle  uint8    // 360/256; 0 = left-to-right, 64 = top-to-bottom
	GradRadial bool
	Stroke     RGBA
	StrokePx   uint8
	Tint       RGBA // multiplied into ElemImage/ElemGen at draw
	// Opacity is 0..255; 0 means absent and draws opaque (EffectiveOpacity).
	Opacity uint8

	Text string // <= TextRuneCap runes
	// Font and Size are carried, not drawn, in format 1, so a later format can honour them.
	Font  string // a FontElements id (inherit that element's face) or a [fonts] family
	Size  int    // points; 0 = inherit
	Color RGBA
	Align TextAlign

	Clock          uint8 // 0..ClockCap-1; 0 = the shared theme clock
	PhaseMs        int32 // per-element phase offset — FREE, consumes no clock slot
	Loop           bool  // false = one-shot, true = loop
	Effect         EffectKind
	EffectPeriodMs int32
	EffectAmpPct   int16 // 0..AmpMaxPct
	// NoDecimate is inverted so the zero value allows decimation; ignored past the per-item media budget.
	NoDecimate bool

	VisibleWhen Condition
	Locked      bool // editor-only: excluded from marquee + drag
	Hidden      bool // authored-off; still in the file, still in the element list
}

// EffectiveOpacity is the one place the absent marker is substituted, so values round-trip.
func (e Element) EffectiveOpacity() uint8 {
	if e.Opacity == 0 {
		return OpacityOpaque
	}
	return e.Opacity
}

// EffectiveTint resolves absent to opaque white, as multiplying by transparent black blacks out art.
func (e Element) EffectiveTint() RGBA {
	if e.Tint.A == 0 {
		return RGBA{255, 255, 255, 255}
	}
	return e.Tint
}

// EffectiveColor resolves an absent ElemText colour to opaque white, FontSpec's own default.
func (e Element) EffectiveColor() RGBA {
	if e.Color.A == 0 {
		return RGBA{255, 255, 255, 255}
	}
	return e.Color
}

// GenParamCount counts parameters packed from index 0; an empty Key ends the list.
func (e Element) GenParamCount() int {
	for i := range e.GenParams {
		if e.GenParams[i].Key == "" {
			return i
		}
	}
	return len(e.GenParams)
}

// Inert reports that this element can never produce a pixel; a zero Element is inert, so a cleared row is harmless.
func (e Element) Inert() bool {
	if e.Hidden {
		return true
	}
	switch e.Kind {
	case ElemImage:
		return e.Media == "" // no page to resolve
	case ElemGen:
		return e.Gen == "" // no generator named
	case ElemText:
		// An absent colour resolves to white, so only an empty string means nothing to draw.
		return e.Text == ""
	case ElemShape:
		return e.Shape == "" || (!e.Fill.Opaque() && !e.Stroke.Opaque())
	case ElemGradient, ElemPane:
		return !e.Fill.Opaque() && !e.Fill2.Opaque() && !e.Stroke.Opaque()
	}
	return true // an unknown kind never reaches the model, and never draws
}

// Pane reparents slot keys into a second rect; each host draws once, as the kit has one drag owner.
type Pane struct {
	ID        string
	Rect      Rect
	Space     Space
	Hosts     [PaneHostCap]string // themeSlots keys re-homed into this pane
	NHosts    int
	Divider   RGBA
	DividerPx uint8
}

// paneForbiddenHosts are the design canvas and the stage; moving one reparents a space onto itself.
var paneForbiddenHosts = [...]string{"courtroom", "viewport"}

// HostKeys reports the slot keys this pane re-homes (a copy; cold path only).
func (p *Pane) HostKeys() []string {
	out := make([]string, p.NHosts)
	copy(out, p.Hosts[:p.NHosts])
	return out
}

// MediaEntry is "id = file, bytes, xxhash64"; an import verifies these before decoding a stranger's file.
type MediaEntry struct {
	ID    string
	File  string // theme-relative, forward slashes
	Bytes int64
	Hash  string // xxhash64, lowercase hex, as text — verification is the import's
}

type FontFamily struct {
	Family string
	File   string
}

// ClockSpec is one authoring clock group; SpeedPct 0 means never declared, running at SpeedNominalPct.
type ClockSpec struct {
	// ID is the [clock.<ID>] suffix as spelled, or empty for a group the editor declared.
	ID       string
	SpeedPct int16
	Declared bool
}

func (c ClockSpec) EffectivePct() int16 {
	if c.SpeedPct == 0 {
		return SpeedNominalPct
	}
	return c.SpeedPct
}

// EffectBind is an [effect.<slot key>] section: an effect on an existing AO2 widget rather than a free element.
type EffectBind struct {
	Target   string // a themeSlots key
	Effect   EffectKind
	Color    RGBA
	PeriodMs int32
	AmpPct   int16
	Clock    uint8
}

// Chrome is [chrome]: AsyncAO chrome outside the AO2 design canvas.
type Chrome struct {
	Shape     string // a shapemask kind
	ShapeTier int
	TabbarHex RGBA
}

// Meta is [theme]; identity is the sanitized folder name, as re-shared zips get renamed.
type Meta struct {
	Name         string
	Author       string
	License      string
	Credit       string
	Description  string
	MinClient    string // INFORMATIONAL ONLY — never enforced (it would brick themes on downgrade)
	Base         string // terminal AO2 fallback theme
	Subtheme     string // AO2 subtheme folder to prefer ("" = none)
	LayoutPreset string
	StylePreset  string
	GeneratedBy  string
}

// ImportInfo is [import], written by convert-to-editable-copy; PerMisc and LobbyDesign are not applied.
type ImportInfo struct {
	DerivedFrom string
	DerivedAt   string
	DerivedHash string
	Subthemes   []string
	PerMisc     bool
	LobbyDesign bool
}

// KeyRect is one [overrides] row over courtroom_design.ini; only existing editable keys are honoured.
type KeyRect struct {
	Key  string
	Rect Rect
}

// KeyAngle is one [rotations] row (360/256 angle byte).
type KeyAngle struct {
	Key   string
	Angle uint8
}

// Sidecar is a parsed asyncao_theme.ini plus its lossless document; nil is the stock AO2 theme.
type Sidecar struct {
	Format   int // as READ; may be greater than ThemeFormatVersion
	Revision int // the author's own bump; informational

	Meta      Meta
	Overrides []KeyRect
	Rotations []KeyAngle
	Palette   Palette // overlays the QSS-derived palette; absent roles inherit it
	Chrome    Chrome
	Fonts     []FontFamily
	FontBind  []KV // FontElements id -> [fonts] family
	Media     []MediaEntry
	Clocks    [ClockCap]ClockSpec
	Panes     []Pane
	Elements  []Element
	Effects   []EffectBind
	Sounds    []KV
	Import    ImportInfo

	notes []string
	// unknown lists unrecognised "section/key" entries and "section/" sections; doc preserves them regardless.
	unknown []string
	// retire lists element ids the next write must delete from doc; bounded by ElementRetireCap.
	retire []string
	// doc is the lossless source: saving through it keeps every unmodified line byte-identical.
	doc *INIDoc
}

// NewSidecar returns an empty sidecar backed by an empty document.
func NewSidecar() *Sidecar {
	d, _ := ParseINIDoc(nil) // an empty document cannot fail a cap
	return &Sidecar{Format: ThemeFormatVersion, doc: d}
}

// The lossless document is not exposed: INIDoc's Set would be a second writer, silently reverted by the next Bytes().

// IsFutureFormat reports a theme from a newer AsyncAO; it never blocks reading, and makes the editor read-only.
func (s *Sidecar) IsFutureFormat() bool { return s != nil && s.Format > ThemeFormatVersion }

// Notes returns the degrade reports (a copy; cold path only).
func (s *Sidecar) Notes() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.notes))
	copy(out, s.notes)
	return out
}

// Unknown returns unrecognised "section/key" entries and "section/" sections (a copy; cold path only).
func (s *Sidecar) Unknown() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.unknown))
	copy(out, s.unknown)
	return out
}

// FindElement returns the element with this id; a repeated [element.x] is one section.
func (s *Sidecar) FindElement(id string) (*Element, bool) {
	if s == nil {
		return nil, false
	}
	for i := range s.Elements {
		if s.Elements[i].ID == id {
			return &s.Elements[i], true
		}
	}
	return nil, false
}

// RetireElementSection marks an element's lines for deletion and reports whether the file had any.
func (s *Sidecar) RetireElementSection(id string) bool {
	if s == nil || s.doc == nil || !s.doc.hasSection(secElementPrefix+id) {
		// Refusing a section the file never carried is what bounds the list.
		return false
	}
	for _, have := range s.retire {
		if have == id {
			return true
		}
	}
	if len(s.retire) >= ElementRetireCap {
		// At the bound the file stays correct and only the undo's byte-exactness is given up.
		_ = s.doc.RemoveSection(secElementPrefix + id)
		return true
	}
	s.retire = append(s.retire, id)
	return true
}

// UnretireElementSection cancels a pending retirement; only undoing a delete may call it.
func (s *Sidecar) UnretireElementSection(id string) {
	if s == nil {
		return
	}
	for i, have := range s.retire {
		if have == id {
			s.retire = append(s.retire[:i], s.retire[i+1:]...)
			return
		}
	}
}

// flushRetiredSections removes retired sections before Bytes writes any key.
// An id still in the model does not cancel it: only UnretireElementSection knows it is the same element.
func (s *Sidecar) flushRetiredSections() {
	if s.doc == nil || len(s.retire) == 0 {
		return
	}
	for _, id := range s.retire {
		_ = s.doc.RemoveSection(secElementPrefix + id)
	}
	s.retire = s.retire[:0]
}

func (s *Sidecar) FindPane(id string) (*Pane, bool) {
	if s == nil {
		return nil, false
	}
	for i := range s.Panes {
		if s.Panes[i].ID == id {
			return &s.Panes[i], true
		}
	}
	return nil, false
}

// PaneHosting returns the pane re-homing a slot key; the reader drops duplicate claims.
func (s *Sidecar) PaneHosting(slotKey string) (*Pane, bool) {
	if s == nil {
		return nil, false
	}
	for i := range s.Panes {
		p := &s.Panes[i]
		for h := 0; h < p.NHosts; h++ {
			if p.Hosts[h] == slotKey {
				return p, true
			}
		}
	}
	return nil, false
}

func (s *Sidecar) FindMedia(id string) (MediaEntry, bool) {
	if s == nil {
		return MediaEntry{}, false
	}
	for _, m := range s.Media {
		if m.ID == id {
			return m, true
		}
	}
	return MediaEntry{}, false
}

func (s *Sidecar) OverrideRect(key string) (Rect, bool) {
	if s == nil {
		return Rect{}, false
	}
	for _, o := range s.Overrides {
		if o.Key == key {
			return o.Rect, true
		}
	}
	return Rect{}, false
}

func (s *Sidecar) RotationFor(key string) (uint8, bool) {
	if s == nil {
		return 0, false
	}
	for _, r := range s.Rotations {
		if r.Key == key {
			return r.Angle, true
		}
	}
	return 0, false
}

func (s *Sidecar) FontFamilyFile(family string) (string, bool) {
	if s == nil {
		return "", false
	}
	for _, f := range s.Fonts {
		if strings.EqualFold(f.Family, family) {
			return f.File, true
		}
	}
	return "", false
}

func (s *Sidecar) BoundFamily(elementID string) (string, bool) {
	if s == nil {
		return "", false
	}
	for _, b := range s.FontBind {
		if b.Key == elementID {
			return b.Value, true
		}
	}
	return "", false
}

// ClockSpeedPct returns a clock's speed; a bad index gives the shared anchor, as files are untrusted.
func (s *Sidecar) ClockSpeedPct(i int) int16 {
	if s == nil || i < 0 || i >= ClockCap {
		return SpeedNominalPct
	}
	return s.Clocks[i].EffectivePct()
}

// SoundFile returns an AsyncAO-tier [sounds] override; courtroom_sounds.ini stays authoritative for AO2's own keys.
func (s *Sidecar) SoundFile(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	for _, kv := range s.Sounds {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return "", false
}

func (s *Sidecar) EffectFor(target string) (EffectBind, bool) {
	if s == nil {
		return EffectBind{}, false
	}
	for _, e := range s.Effects {
		if e.Target == target {
			return e, true
		}
	}
	return EffectBind{}, false
}

// sidecarMigration rewrites a document from format N to N+1 in place through INIDoc.
type sidecarMigration func(*INIDoc) error

// sidecarMigrations[i] migrates format i+1 to i+2; empty until a second format exists.
var sidecarMigrations = []sidecarMigration{}

// migrateSidecar walks the chain up to this build's format; its length is ThemeFormatVersion-1.
func migrateSidecar(from int, d *INIDoc) error {
	if d == nil {
		return nil
	}
	if from < 1 {
		// A file with no [theme] format key is read as the first grammar; there has never been a format 0.
		from = 1
	}
	for v := from; v-1 < len(sidecarMigrations); v++ {
		step := sidecarMigrations[v-1]
		if step == nil {
			continue
		}
		if err := step(d); err != nil {
			return err
		}
	}
	return nil
}

// The four characters percent-encoding covers; asyncao_theme.ini free text only, for AO2 parity.
const (
	freeTextEscape  = '%'
	freeTextPercent = "%25"
	freeTextSemi    = "%3B"
	freeTextLF      = "%0A"
	freeTextCR      = "%0D"
)

// encodeFreeText makes a value safe to write; it trims because the reader trims.
func encodeFreeText(s string) string {
	s = strings.TrimSpace(s)
	if !strings.ContainsAny(s, "%;\n\r") {
		return s // the overwhelmingly common case allocates nothing
	}
	var b strings.Builder
	b.Grow(len(s) + len(s)/8)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '%':
			b.WriteString(freeTextPercent)
		case ';':
			b.WriteString(freeTextSemi)
		case '\n':
			b.WriteString(freeTextLF)
		case '\r':
			b.WriteString(freeTextCR)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// decodeFreeText reverses the four sequences only, so a typed "%20" stays literal; hex in either case.
func decodeFreeText(s string) string {
	if !strings.ContainsRune(s, freeTextEscape) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == freeTextEscape && i+2 < len(s) {
			if c, ok := freeTextByte(s[i+1], s[i+2]); ok {
				b.WriteByte(c)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func freeTextByte(a, b byte) (byte, bool) {
	switch {
	case a == '2' && (b == '5'):
		return '%', true
	case a == '3' && (b == 'B' || b == 'b'):
		return ';', true
	case a == '0' && (b == 'A' || b == 'a'):
		return '\n', true
	case a == '0' && (b == 'D' || b == 'd'):
		return '\r', true
	}
	return 0, false
}

// validID reports s matches [a-z0-9_-]{1,24}; otherwise the section is preserved, ignored and reported.
func validID(s string) bool {
	if s == "" || utf8.RuneCountInString(s) > IDRuneCap {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// assignPaletteSlot mirrors ParseStylesheet's closure, as [palette] overlays the QSS palette.
func assignPaletteSlot(p *Palette, slot paletteSlot, c RGB) {
	v := c
	switch slot {
	case slotText:
		p.Text = &v
	case slotPanel:
		p.Panel = &v
	case slotPanelHi:
		p.PanelHi = &v
	case slotAccent:
		p.Accent = &v
	case slotDanger:
		p.Danger = &v
	}
}

func paletteSlotColor(p Palette, slot paletteSlot) *RGB {
	switch slot {
	case slotText:
		return p.Text
	case slotPanel:
		return p.Panel
	case slotPanelHi:
		return p.PanelHi
	case slotAccent:
		return p.Accent
	case slotDanger:
		return p.Danger
	}
	return nil
}

// paletteRoleKeys maps [palette] wire keys onto the QSS palette fields they overlay; append-only.
var paletteRoleKeys = [...]struct {
	key  string
	slot paletteSlot
}{
	{"text", slotText},
	{"panel", slotPanel},
	{"panel_hi", slotPanelHi},
	{"accent", slotAccent},
	{"danger", slotDanger},
}
