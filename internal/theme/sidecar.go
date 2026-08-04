package theme

// asyncao_theme.ini — the AsyncAO extension tier of a theme folder.
//
// An AsyncAO theme IS an AO2 theme folder plus ONE sibling file. AO2-Client
// never opens this file (get_theme_path only ever joins the three known INIs),
// so a theme carrying one still loads byte-identically in stock AO2, and the
// author's own courtroom_design.ini is never written in the normal path.
//
// The public, third-party-implementable grammar is docs/THEME-FORMAT.md. That
// document and this file are the same format; when they disagree, the tests in
// sidecar_test.go say which one is wrong.
//
// FOUR RULES GOVERN EVERYTHING BELOW (docs/THEME-FORMAT.md §7, normative):
//
//  1. Reading NEVER refuses on the format version. A theme from a newer client
//     renders with everything this build understands.
//  2. Unknown keys, unknown sections and the reserved [x.<vendor>.*] namespace
//     are PRESERVED VERBATIM — carried by INIDoc, never rebuilt from the model.
//  3. Unknown enum VALUES degrade to the nearest safe primitive and are still
//     preserved on save; the element keeps drawing.
//  4. Keys are additive-only and never repurposed. A new key's ZERO VALUE must
//     mean "behave as version 1 did"; the presence of a key is its version.
//
// SDL-free by hard rule 1, and cold-path only: parsing walks maps and allocates
// strings, so it belongs on the theme-apply goroutine and never on a draw,
// decode or resolver path (rule 2).

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	// SidecarFileName is the AsyncAO native tier of a theme folder. The name is
	// deliberately outside AO2's three (courtroom_design/fonts/sounds.ini) so no
	// AO2 build, script or community tool has ever had a reason to open it.
	SidecarFileName = "asyncao_theme.ini"

	// ThemeFormatVersion is the sidecar grammar this build AUTHORS. READING is
	// never gated on it (rule 1 above): a format-2 theme renders here with
	// everything format 1 understands, and saves without losing the rest.
	ThemeFormatVersion = 1

	// SidecarMediaDir is where imported art lives inside a theme folder. AO2
	// never scans it, so a bundled theme stays a valid AO2 theme; it is a
	// forward-slash RELATIVE path because it appears verbatim in [media] values.
	SidecarMediaDir = "asyncao/media"

	// VendorSectionPrefix / VendorKeyPrefix are RESERVED FOR THIRD PARTIES and
	// are never assigned an official meaning. Anything matching them is carried
	// through untouched, so a third-party tool can squat here forever without
	// ever colliding with a key AsyncAO adds later.
	// Pinned by TestVendorNamespaceIsPreservedAndNeverAssigned.
	VendorSectionPrefix = "x."
	VendorKeyPrefix     = "x_"
)

// ---------------------------------------------------------------------------
// Named caps. Hard rule 4: nothing unbounded, and every number says why it is
// that number. They REFUSE rather than truncate — a silently truncated theme
// that then gets saved is data loss, which is the one failure this feature
// cannot recover from. Pinned by TestSidecarCapsRefuseNotTruncate.
//
// NoteCap is the exception and says so at its own declaration: it bounds the
// import REPORT, never the load. Published in docs/THEME-FORMAT.md §7 and
// pinned against it by TestPublishedCapsMatchTheConstants.
// ---------------------------------------------------------------------------
const (
	// ElementCap bounds [element.*]. ~4x the densest style preset (Manga Panel
	// is ~22 elements); 96 baked entries is a fixed array that never grows.
	ElementCap = 96
	// PaneCap bounds [pane.*] — two-way splits on at most four regions.
	PaneCap = 8
	// PaneHostCap bounds the slot keys one pane may re-home.
	PaneHostCap = 8
	// TextRuneCap bounds an ElemText string: a verdict line, and short enough
	// that truncating it to its rect at bake stays cheap.
	TextRuneCap = 240
	// GenParamCap bounds a generator's parameter list. The widest generator
	// (halftone: dot, angle, tint, bg, jitter) uses five.
	GenParamCap = 8
	// ClockCap bounds the authoring clock groups. Clock 0 is the shared theme
	// anchor and is never declared; the densest style preset wants four.
	ClockCap = 16
	// MediaCap / FontCap bound [media] and [fonts]: manifest entry bounds that
	// match the texture budget (24 images) and a theme's realistic face count.
	MediaCap = 24
	FontCap  = 6
	// FontBindCap bounds [fontbind]. FontElements is append-only and currently
	// holds 11 ids; 32 leaves room for it to grow without touching the format.
	FontBindCap = 32
	// OverrideCap bounds [overrides] and [rotations] independently: one entry
	// per themeSlots row (ui.themeSlotCap = 160) is the most a theme could
	// possibly restate.
	OverrideCap = 160
	// EffectBindCap bounds [effect.*], the effects bound to EXISTING AO2
	// widgets. Every one of them occupies a slot in the same stateful-effect
	// pool an element's own effect draws from, so the number belongs to that
	// pool rather than to this section — and the pool is sized to the media
	// budget, because a stateful effect always dresses art or a generator and
	// both are bounded by MediaCap. It is written as a CONST ALIAS, not as a
	// second 24, for the reason the texture budget already const-aliases its
	// producer and store caps: two copies of a number are two numbers.
	//
	// internal/ui has no FX pool yet. When one lands it must alias THIS
	// constant rather than restate the value.
	EffectBindCap = MediaCap
	// SoundCap bounds [sounds], the AsyncAO-tier sound overrides.
	SoundCap = 16
	// SubthemeCap bounds [import] subthemes — AO2 subtheme folders are a
	// handful per pack, and the list is provenance, not configuration.
	SubthemeCap = 16
	// PreservedCap bounds how many unknown keys and sections one file may carry
	// BEFORE it stops looking like a theme. INIDoc preserves the lines either
	// way; this caps the enumeration the import report reads.
	PreservedCap = 512
	// NoteCap bounds the degrade notes one parse may collect (a skipped
	// element, a dropped pane host). It is the ONE cap here that bounds the
	// REPORT rather than the LOAD: past it the reader stops recording notes and
	// keeps going, because refusing would contradict rule 1 and gut rule 3 —
	// an unknown kind is the forward-compat path and notes once per element, so
	// 65 of them from a newer client must still load. Every note call site
	// absorbs the error; see note() in sidecar_read.go, and
	// TestUnknownKindsPastNoteCapStillLoad.
	NoteCap = 64
	// IDRuneCap is the id grammar's length bound: [a-z0-9_-]{1,24}. Ids are
	// section suffixes, so they must stay short enough to read in a diff.
	IDRuneCap = 24
	// ConditionValueRuneCap bounds a visible_when value (a pos, character or
	// side name). Every one in the reference corpus is far under it, and the
	// value is interned once per element at bake.
	ConditionValueRuneCap = 64
	// AnchorRuneCap bounds an anchor: a themeSlots key or a pane id. The
	// longest shipped slot key is well under half of it.
	AnchorRuneCap = 48
	// NameRuneCap bounds every other short free-text field (theme name, author,
	// licence, a font family, a media file path).
	NameRuneCap = 240
	// GenParamRuneCap bounds one generator parameter's key or value.
	GenParamRuneCap = 48
)

// Numeric ranges. Clamped on load rather than refused: a percent outside its
// range is a typo, not a hostile file, and rule 1 says render what we can.
const (
	// OpacityOpaque is what an ABSENT opacity means. 0 is the absent marker in
	// the FILE and in the STRUCT alike (see Element.EffectiveOpacity), so the
	// value round-trips symmetrically and the zero Element cannot paint.
	OpacityOpaque = 255
	// SpeedNominalPct is a clock running at the shared theme anchor's rate.
	SpeedNominalPct = 100
	// SpeedMinPct / SpeedMaxPct bound a clock group: a tenth speed still reads
	// as motion, and 4x is past the point where any of it is legible.
	SpeedMinPct = 10
	SpeedMaxPct = 400
	// AmpMaxPct bounds an effect amplitude (0..100 is the whole range).
	AmpMaxPct = 100
	// TextSizeMaxPt bounds an ElemText point size. 0 = inherit the bound font's
	// own size; 256 pt is taller than the design canvas.
	TextSizeMaxPt = 256
	// AngleCount is the 360/256 angle byte's modulus — the same convention
	// ThemeRectRotations already persists, so 64 is 90 degrees.
	AngleCount = 256
	// ZMin / ZMax bound Element.Z and the four 9-slice insets: both are int16
	// in the model, and the file's grammar has to say so out loud.
	//
	// NAMED because THREE places must agree on the number — the reader, the
	// writer's canonicaliser and docs/THEME-FORMAT.md. When they do not, the
	// writer decides an untouched file "does not already mean" what it says and
	// rewrites the author's line on a save nobody asked for.
	ZMin = -32768
	ZMax = 32767
	// TimeMsMin / TimeMsMax bound every millisecond-valued key (phase_ms,
	// effect_period_ms and [effect.*] period_ms), which are int32 in the model.
	//
	// CLAMPED, never truncated. A bare int32() conversion would turn a legal
	// "phase_ms = 3000000000" into a NEGATIVE phase, and it would do it only on
	// a 64-bit host — atoiTrim saturates at the platform int width, so a 32-bit
	// build would read the same file as 2147483647. A published grammar cannot
	// mean two things on two machines.
	TimeMsMin = -2147483648
	TimeMsMax = 2147483647
)

var (
	// ErrSidecarCap reports a REFUSED file: one of the named caps above was
	// exceeded. Never a truncation — see the caps block.
	ErrSidecarCap = errors.New("theme: sidecar exceeds a named cap")
	// ErrSidecarUnreadable reports a file the READER could not parse at all
	// (bufio's own line limit, or an INIDoc cap). It is distinct from a cap
	// refusal because the fix is different: one is a huge theme, one is not a
	// theme.
	ErrSidecarUnreadable = errors.New("theme: sidecar is not readable as INI")
)

// ---------------------------------------------------------------------------
// Enums. WIRE NAMES ARE THE FORMAT: append-only, at the END, never renamed and
// never repurposed. Inserting a value in the middle silently re-maps every
// theme already on disk. Pinned by TestElementKindNamesAreAppendOnly and
// TestElementBandNamesAreAppendOnly.
// ---------------------------------------------------------------------------

// ElementKind is what a free element paints.
type ElementKind uint8

const (
	ElemImage    ElementKind = iota // imported PNG/WebP/GIF/APNG/AVIF, static or animated
	ElemShape                       // procedural SDF silhouette (the shapemask family)
	ElemGradient                    // two-stop linear/radial plate
	ElemGen                         // procedural texture TILE
	ElemText                        // author-authored static string (watermark, credit, stamp)
	ElemPane                        // the plate/divider of a split-screen [pane.*]
	// ElementKindCount sizes internal/ui's painter table — a fixed array
	// indexed by kind, never a map on the draw path.
	ElementKindCount
)

var elementKindNames = [ElementKindCount]string{"image", "shape", "gradient", "gen", "text", "pane"}

// ElementBand is WHERE in the themed pass an element paints.
//
// THREE, not a global z-sort, because the themed pass is hand-ordered across
// the stage and panel phases and carries a mid-sequence abort (the courtroom
// modals' early return) that a flat sortable list cannot reproduce.
type ElementBand uint8

const (
	BandBackdrop ElementBand = iota // above the courtroom backdrop, below every widget
	BandMid                         // between the stage and the panels — behind the logs
	BandOverlay                     // above everything, below editor chrome
	// ElementBandCount is the number of insertion points, and the number of
	// band loops internal/ui runs per themed frame.
	ElementBandCount
)

var elementBandNames = [ElementBandCount]string{"backdrop", "mid", "overlay"}

// Space is the coordinate space an element's rect is expressed in. The first
// four mirror ui.themeSlotRelSpace exactly; the last two are AsyncAO's.
type Space uint8

const (
	SpaceCourtroom    Space = iota // design-space, transformed by the canvas fit (default)
	SpaceChatbox                   // child of ao2_chatbox
	SpaceViewport                  // child of the stage
	SpaceMusicDisplay              // child of the music_display plate
	// SpaceWindow is WINDOW pixels, OUTSIDE the design canvas entirely — the
	// letterbox bars, the chrome band, a full-bleed vignette. It is the one
	// space AO2 has no analogue for, and it is the AO2-full-parity rule
	// ("extras go in chrome OUTSIDE the canvas") expressed as a type instead of
	// a prohibition.
	SpaceWindow
	// SpacePane is a child of a named [pane.*]; the element's Anchor is a pane
	// id rather than a slot key.
	SpacePane
	// SpaceCount bounds every space table.
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
	// FitCount bounds the fit tables.
	FitCount
)

var fitNames = [FitCount]string{"stretch", "tile", "contain", "cover", "nine"}

// EffectKind is an element's motion. FXNone is the zero value, so an element
// that names no effect is static — rule 4's "zero value behaves as version 1".
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
	// AlignCount bounds the alignment tables.
	AlignCount
)

var alignNames = [AlignCount]string{"left", "center", "right"}

// ConditionAxis is the ONE axis a VisibleWhen gate may test. Deliberately not
// an expression language: anything richer needs a parser on the frame path,
// and this compiles to an integer compare against an index interned at bake.
type ConditionAxis uint8

const (
	CondAlways   ConditionAxis = iota // the zero value: always visible
	CondPos                           // pos:def, pos:pro, …
	CondChar                          // char:<folder>
	CondSide                          // side:def, side:pro, …
	CondSpeaking                      // someone is speaking (no value)
	CondShout                         // shout:objection, …
	// ConditionAxisCount bounds the axis tables.
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

// String returns the wire name, or "" for a value outside the enum (which the
// reader can never produce — every parse degrades into range).
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

// enumValue resolves a wire name case-insensitively. ok=false means UNKNOWN,
// and every caller degrades rather than failing (rule 3) — the raw text is then
// preserved on save because the writer only rewrites a key whose meaning
// actually changed.
func enumValue(names []string, s string) (uint8, bool) {
	want := strings.ToLower(strings.TrimSpace(s))
	for i, n := range names {
		if n == want {
			return uint8(i), true
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Values
// ---------------------------------------------------------------------------

// RGBA is a sidecar colour. The AO2 side of a theme is RGB (theme.RGB); the
// extension tier needs alpha because a frame element is a stroke over a
// TRANSPARENT fill, which RGB cannot say.
type RGBA struct{ R, G, B, A uint8 }

// Opaque reports whether the colour would paint anything at all.
func (c RGBA) Opaque() bool { return c.A > 0 }

// RGB drops the alpha, for the many call sites that want AO2's own tuple.
func (c RGBA) RGB() RGB { return RGB{c.R, c.G, c.B} }

// KV is one ordered generator parameter or sound override. Ordered, because a
// generator's parameter hash is order-sensitive and is used as a cache key.
type KV struct{ Key, Value string }

// Condition gates an element on live session state.
type Condition struct {
	Axis  ConditionAxis
	Value string // interned to an index at bake; "" for the value-less axes
}

// Element is one free element: a box the AO2 registry has no row for.
//
// IDENTITY IS THE INDEX in Sidecar.Elements, not the ID — ID is authoring and
// UI only (it is the [element.<id>] section suffix). Nothing about an element
// ever enters the AO2 rect registry, which is why the totality census over
// courtroom_design.ini's root keys structurally cannot see one.
//
// EVERY FIELD IS IN elementFieldKeys (sidecar_write.go), and
// TestSidecarSchemaMatchesTheElementStruct fails on any that is not: a field
// added here without a wire key would save silently as nothing, which is the
// model-vs-format drift class the preferences save/load gate closed for prefs.
type Element struct {
	ID    string // [a-z0-9_-]{1,24}; the section suffix; editor-only
	Kind  ElementKind
	Band  ElementBand
	Z     int16 // order WITHIN the band; ties break on declaration index
	Space Space

	// Anchor is a themeSlots key (or a pane id when Space == SpacePane). ""
	// means the Space's own origin, and Rect is then absolute in that space.
	//
	// ANCHORS MAY NOT CHAIN. An anchor is a slot key or a pane id, NEVER
	// another element: one bake pass, no cycle detection, no depth cap.
	Anchor string
	Rect   Rect  // design px in the four AO2 spaces + SpacePane; WINDOW px in SpaceWindow
	Rot    uint8 // 360/256 angle byte — the ThemeRectRotations convention

	// --- fill: exactly one is meaningful per Kind --------------------------
	Media      string // ElemImage: an id into Sidecar.Media
	Shape      string // ElemShape: shape kind ("rounded", "hex", "ribbon", "tape", …)
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
	// Opacity is 0..255, where 0 means ABSENT and is drawn as opaque
	// (EffectiveOpacity). The absent marker is the same in the file and in the
	// struct on purpose: it is what lets a value survive write-then-read
	// unchanged, and what makes the zero Element unable to paint.
	Opacity uint8

	// --- text --------------------------------------------------------------
	Text  string // <= TextRuneCap runes
	Font  string // a FontElements id (inherit that element's face) or a [fonts] family
	Size  int    // points; 0 = inherit
	Color RGBA
	Align TextAlign

	// --- motion -------------------------------------------------------------
	Clock          uint8 // 0..ClockCap-1; 0 = the shared theme clock
	PhaseMs        int32 // per-element phase offset — FREE, consumes no clock slot
	Loop           bool  // false = one-shot, true = loop
	Effect         EffectKind
	EffectPeriodMs int32
	EffectAmpPct   int16 // 0..AmpMaxPct
	// NoDecimate is "decimate = no", stored INVERTED so the zero value is the
	// version-1 behaviour (decimation allowed). It is honoured only when the
	// undecimated page fits the per-item media budget; otherwise it is ignored
	// and the inspector says why.
	NoDecimate bool

	// --- behaviour ----------------------------------------------------------
	VisibleWhen Condition
	Locked      bool // editor-only: excluded from marquee + drag
	Hidden      bool // authored-off; still in the file, still in the element list
}

// EffectiveOpacity resolves the absent marker. It is the ONE place the
// substitution happens, so Element values compare, write and read back
// symmetrically everywhere else.
func (e Element) EffectiveOpacity() uint8 {
	if e.Opacity == 0 {
		return OpacityOpaque
	}
	return e.Opacity
}

// EffectiveTint resolves the absent marker for a multiply tint. A zero RGBA is
// "no tint declared", and a multiply by transparent black would black out the
// art it was meant to leave alone — so absent resolves to opaque white, the
// identity of the operation. Same shape as EffectiveOpacity, same reason: rule
// 4 says the zero value must behave as version 1 did.
func (e Element) EffectiveTint() RGBA {
	if e.Tint.A == 0 {
		return RGBA{255, 255, 255, 255}
	}
	return e.Tint
}

// EffectiveColor resolves an absent ElemText colour to opaque white, which is
// what FontSpec already defaults to when a theme declares no "<id>_color".
func (e Element) EffectiveColor() RGBA {
	if e.Color.A == 0 {
		return RGBA{255, 255, 255, 255}
	}
	return e.Color
}

// GenParamCount is how many generator parameters are packed into GenParams.
// Entries are packed from index 0 and an empty Key terminates the list, so the
// fixed array needs no companion count field to fall out of sync with.
func (e Element) GenParamCount() int {
	for i := range e.GenParams {
		if e.GenParams[i].Key == "" {
			return i
		}
	}
	return len(e.GenParams)
}

// Inert reports that this element can never produce a pixel, whatever the
// session does. It is the property TestZeroValueElementIsInert pins: an
// Element nobody filled in is an ElemImage with no media, so a zeroed array
// slot, a cleared row or a struct built by a future caller is silently
// harmless instead of silently painting.
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
		// The colour is NOT part of this test: an absent one resolves to white
		// (EffectiveColor), so only an empty string means nothing to draw.
		return e.Text == ""
	case ElemShape:
		// A shape with no kind has no silhouette; one with neither fill nor
		// stroke has nothing to put inside it.
		return e.Shape == "" || (!e.Fill.Opaque() && !e.Stroke.Opaque())
	case ElemGradient, ElemPane:
		return !e.Fill.Opaque() && !e.Fill2.Opaque() && !e.Stroke.Opaque()
	}
	return true // an unknown kind never reaches the model, and never draws
}

// Pane is split-screen. It REPARENTS the listed slot keys into a second rect:
// each host widget draws EXACTLY ONCE, at the pane's position.
//
// Calling the host's draw site twice was rejected and must stay rejected: the
// immediate-mode kit has ONE drag owner and a documented single wheel consumer,
// so a widget cannot be in two places.
type Pane struct {
	ID        string
	Rect      Rect
	Space     Space
	Hosts     [PaneHostCap]string // themeSlots keys re-homed into this pane
	NHosts    int
	Divider   RGBA
	DividerPx uint8
}

// paneForbiddenHosts can never be re-homed. "courtroom" IS the design canvas
// every other rect is expressed inside, and "viewport" is the stage the scene
// composites into; moving either into a pane would reparent a space onto
// itself. Pinned by TestPaneCannotHostCourtroom.
var paneForbiddenHosts = [...]string{"courtroom", "viewport"}

// Hosts reports the slot keys this pane re-homes (a copy; cold path only).
func (p *Pane) HostKeys() []string {
	out := make([]string, p.NHosts)
	copy(out, p.Hosts[:p.NHosts])
	return out
}

// MediaEntry is one [media] row: "id = file, bytes, xxhash64". The size and
// hash are declared by the author so an import can BUDGET and VERIFY before it
// decodes anything a stranger sent.
type MediaEntry struct {
	ID    string
	File  string // theme-relative, forward slashes
	Bytes int64
	Hash  string // xxhash64, lowercase hex, as text — verification is the import's
}

// FontFamily is one [fonts] row: a family name and the file under <theme>/fonts.
type FontFamily struct {
	Family string
	File   string
}

// ClockSpec is one authoring clock group. SpeedPct 0 means the group was never
// declared and runs at SpeedNominalPct.
type ClockSpec struct {
	// ID is the [clock.<ID>] suffix EXACTLY as the file spells it, and it is
	// carried for the same reason Pane and Element carry theirs: the section
	// name is the author's, not ours to re-derive.
	//
	// "01" is a legal id under the format's [a-z0-9_-]{1,24} grammar and reads
	// as clock 1. Rebuilding the name from the array index would look for
	// [clock.1], not find it, and APPEND a second section beside the author's
	// saying the same thing — a file mutation on a save that changed nothing.
	//
	// It is empty for a group the editor declared rather than read; that one is
	// written in the canonical spelling, because there is no author's spelling
	// to preserve.
	ID       string
	SpeedPct int16
	Declared bool
}

// EffectivePct resolves the absent marker, exactly as EffectiveOpacity does.
func (c ClockSpec) EffectivePct() int16 {
	if c.SpeedPct == 0 {
		return SpeedNominalPct
	}
	return c.SpeedPct
}

// EffectBind is an [effect.<slot key>] section: an effect attached to an
// EXISTING AO2 widget rather than to a free element ("attach an effect to
// anything").
type EffectBind struct {
	Target   string // a themeSlots key
	Effect   EffectKind
	Color    RGBA
	PeriodMs int32
	AmpPct   int16
	Clock    uint8
}

// Chrome is [chrome]: AsyncAO chrome OUTSIDE the AO2 design canvas. The
// full-parity rule's "extras go in chrome outside the canvas", as data.
type Chrome struct {
	Shape     string // a shapemask kind
	ShapeTier int
	TabbarHex RGBA
}

// Meta is [theme]: identity and provenance. IDENTITY IS THE FOLDER NAME
// (sanitized, lowercased) — Name is a display string only, because re-shared
// zips get renamed constantly and a manifest-name identity would make two
// different themes the same theme.
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

// ImportInfo is [import]: provenance written by convert-to-editable-copy, never
// authored by hand. PerMisc and LobbyDesign are RECORDED, not applied — they
// name the two AO2 features AsyncAO deliberately inherits rather than imports.
type ImportInfo struct {
	DerivedFrom string
	DerivedAt   string
	DerivedHash string
	Subthemes   []string
	PerMisc     bool
	LobbyDesign bool
}

// KeyRect is one [overrides] row: a design-space rect laid OVER
// courtroom_design.ini. Only keys that EXIST and are editable are honoured by
// the applier, which is unchanged by this format.
type KeyRect struct {
	Key  string
	Rect Rect
}

// KeyAngle is one [rotations] row (360/256 angle byte).
type KeyAngle struct {
	Key   string
	Angle uint8
}

// Sidecar is a parsed asyncao_theme.ini PLUS the lossless document it came
// from. The document is what preserves everything this build does not
// understand; the struct is what this build draws.
//
// A nil *Sidecar is the STOCK AO2 THEME: every accessor is nil-safe and returns
// nothing, so "a theme with no sidecar produces zero elements" needs no branch
// at any call site.
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

	// notes are degrade reports for the import panel: what was skipped and why.
	notes []string
	// unknown lists the "section/key" entries this build does not define, and
	// the unknown sections themselves as "section/". They are PRESERVED by doc
	// regardless; this is the enumeration the editor's read-only card counts.
	unknown []string
	// doc is the lossless source. Saving goes through it, so every comment,
	// blank line, key order and unknown section survives byte-identically.
	doc *INIDoc
}

// NewSidecar returns an empty sidecar backed by an empty document — the
// starting point for a theme authored from scratch.
func NewSidecar() *Sidecar {
	d, _ := ParseINIDoc(nil) // an empty document cannot fail a cap
	return &Sidecar{Format: ThemeFormatVersion, doc: d}
}

// Doc exposes the lossless document (the writer, and diagnostics).
func (s *Sidecar) Doc() *INIDoc {
	if s == nil {
		return nil
	}
	return s.doc
}

// IsFutureFormat reports a theme authored by a NEWER AsyncAO. It never blocks
// reading (rule 1); it is what gates the editor into read-only with a
// Duplicate-for-editing card.
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

// Unknown returns the "section/key" entries this build does not define, and
// "section/" for whole unknown sections (a copy; cold path only).
func (s *Sidecar) Unknown() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.unknown))
	copy(out, s.unknown)
	return out
}

// FindElement returns the element with the given id. Ids are unique within a
// file (a repeated [element.x] is one section, last declaration winning per
// key, exactly as the reader treats a repeated key).
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

// FindPane returns the pane with the given id.
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

// PaneHosting returns the pane that re-homes a slot key, if any. Host sets are
// disjoint by construction (the reader drops a duplicate claim), so the first
// match is the only match.
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

// FindMedia returns the [media] row for an id.
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

// OverrideRect returns the [overrides] rect for a slot key.
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

// RotationFor returns the [rotations] angle byte for a slot key.
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

// FontFamilyFile returns the file declared for a [fonts] family.
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

// BoundFamily returns the [fontbind] family for a FontElements id.
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

// ClockSpeedPct returns the effective speed of a clock group. Out-of-range
// indices resolve to the shared anchor rather than panicking: the value comes
// from a file a stranger wrote.
func (s *Sidecar) ClockSpeedPct(i int) int16 {
	if s == nil || i < 0 || i >= ClockCap {
		return SpeedNominalPct
	}
	return s.Clocks[i].EffectivePct()
}

// SoundFile returns an AsyncAO-tier [sounds] override. The AO2
// courtroom_sounds.ini stays authoritative for AO2's own keys.
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

// EffectFor returns the [effect.<key>] binding for an AO2 widget.
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

// ---------------------------------------------------------------------------
// Version migration
// ---------------------------------------------------------------------------

// sidecarMigration rewrites a document from format N to format N+1, in place,
// through INIDoc — so a migration preserves comments and unknown sections like
// every other write does.
type sidecarMigration func(*INIDoc) error

// sidecarMigrations[i] migrates format (i+1) to (i+2). It ships EMPTY, and that
// is the point: the chain exists, is called, and is tested BEFORE it is needed,
// so the first real migration is one entry in a table rather than a design
// decision taken under pressure.
//
// TestMigrationChainIsIdentityAtV1 pins both halves: the chain is empty at v1,
// and running it changes not one byte.
var sidecarMigrations = [...]sidecarMigration{}

// migrateSidecar walks the chain from the file's format up to this build's.
//
// It NEVER refuses on version (rule 1). A file NEWER than this build is left
// exactly as it is: we render what we understand and preserve the rest, which
// is what makes "your friend upgraded, you did not" a non-event.
func migrateSidecar(from int, d *INIDoc) error {
	if d == nil {
		return nil
	}
	if from < 1 {
		// A file with no [theme] format key predates nothing — there has never
		// been a format 0 — so it is read as the first grammar.
		from = 1
	}
	for v := from; v < ThemeFormatVersion; v++ {
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

// ---------------------------------------------------------------------------
// Free text
// ---------------------------------------------------------------------------

// The FOUR characters percent-encoding covers, and nothing else.
//
// ';' cannot round-trip through this reader: QSettings truncates a line at the
// first one (see iniCommentStart) and AsyncAO matches that ON PURPOSE, measured
// against the reference corpus. So free text is percent-encoded on exactly
// '%', ';', LF and CR. Percent-encoding beats a \x3b-style escape because it is
// the convention a third-party writer will guess correctly.
//
// SCOPE IS ENUMERATED, NOT GENERAL: decoding applies ONLY to the free-text keys
// of asyncao_theme.ini, never to an AO2 root-section value, where inventing an
// escape AO2 does not have would break parity with every existing theme.
const (
	freeTextEscape  = '%'
	freeTextPercent = "%25"
	freeTextSemi    = "%3B"
	freeTextLF      = "%0A"
	freeTextCR      = "%0D"
)

// encodeFreeText makes a value safe to write. It also TRIMS: the reader trims
// every value it returns, so a string with edge whitespace could not read back
// as itself in any INI, and INIDoc would (correctly) refuse it.
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

// decodeFreeText reverses encodeFreeText. Only the four sequences are decoded —
// "nothing else is escaped" — so a literal "%20" a human typed stays "%20",
// which is consistent because our own writer spells a literal '%' as "%25".
// The hex digits are matched case-insensitively so a third-party writer that
// emits "%3b" is understood.
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

// freeTextByte decodes ONE escape pair, and only the four the format defines.
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

// ---------------------------------------------------------------------------
// Identifiers
// ---------------------------------------------------------------------------

// validID reports whether s matches the id grammar [a-z0-9_-]{1,24} — element,
// pane, media and clock ids. Section names arrive lowercased from the reader,
// so an id with capitals in the file is already folded by the time it is
// checked here; anything else (a space, a dot, a slash) is not an id and the
// section is treated as one this build does not understand: preserved, ignored,
// and named in the import report.
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

// assignPaletteSlot writes one role into a Palette. It mirrors the closure in
// ParseStylesheet deliberately: the sidecar's [palette] OVERLAYS the QSS
// palette, so both must land in the same five slots or a theme's own colours
// would disagree with its stylesheet's.
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

// paletteSlotColor reads one role back out.
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

// paletteRoleKeys maps the [palette] wire keys onto the QSS palette slots they
// overlay. APPEND-ONLY, like every other wire-name table here.
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
