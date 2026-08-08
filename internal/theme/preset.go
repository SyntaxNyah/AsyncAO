package theme

// ONE PRESET FRAGMENT: the parsed form of internal/theme/presets/{layout,style}/<id>.ini.
//
// A fragment IS an asyncao_theme.ini (the same grammar, the same reader, the
// same caps) plus a [preset] header that names the axis it belongs to. There is
// no second parser and no second format — which is the property that makes the
// gallery's data hand-editable and a user's own theme a legal preset source.
//
// THE TWO AXES ARE A PARTITION, NOT A CONVENTION. A layout fragment may declare
// only the sections that place things; a style fragment only the sections that
// dress them. The partition is a table here (presetTierSections), it is enforced
// on every parse, and it is what makes the 21x14 gallery orthogonal: two
// fragments drawn from different axes can never write the same key, so merging
// them is a concatenation rather than a negotiation.
//
// It is also what makes APPLYING one reversible. "Replace the layout tier" is a
// well-defined act precisely because the tier owns a closed set of sections; if
// the sets overlapped, replacing one would have to guess which half of an
// overlapping section belonged to whom.
//
// SDL-free and cold-path only, exactly like the reader it wraps (hard rules 1
// and 2).

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// secPreset is the fragment header section. It is NOT part of the sidecar
// grammar: the sidecar reader classifies it as unknown and preserves it
// verbatim (format rule 2), which is why a fragment stays a legal theme file
// and why a theme file with no [preset] header is still a legal fragment body.
const secPreset = "preset"

const (
	// PresetIDRuneCap is the fragment id grammar's bound. Deliberately the same
	// constant the element/pane/clock ids use (IDRuneCap): an id is a file name,
	// a section suffix and a [theme] layout_preset value, and three different
	// length rules for one concept is three chances to disagree.
	PresetIDRuneCap = IDRuneCap
	// PresetFullCanvasCap bounds one fragment's `full_canvas` list. It can never
	// usefully exceed the element cap — every entry names an element in the same
	// file — so it is written as a const alias rather than as a second number.
	PresetFullCanvasCap = ElementCap
	// PresetDesignMinPx / PresetDesignMaxPx bound a layout fragment's declared
	// design canvas. The floor is AO2's own smallest shipped canvas class
	// (retro_lowres is 640x480) halved, so a deliberately tiny canvas is still
	// legal; the ceiling is four times the widest shipped fragment (21:9 at
	// 1512), which is past any canvas a design-space geometry stays readable in.
	//
	// CLAMPED, never refused: a typo'd canvas is a fragment that lays out
	// strangely, not a fragment that must stop the gallery from opening.
	PresetDesignMinPx = 240
	PresetDesignMaxPx = 6048
)

var (
	// ErrPresetKind reports a fragment whose [preset] kind is neither axis. It
	// is a REFUSAL rather than a degrade, unlike every enum in the sidecar
	// itself: the kind decides which section partition applies, so guessing it
	// would be guessing whether the file is allowed to contain what it contains.
	ErrPresetKind = errors.New("theme: preset kind must be \"layout\" or \"style\"")
	// ErrPresetID reports an id outside the [a-z0-9_-]{1,24} grammar.
	ErrPresetID = errors.New("theme: preset id must be [a-z0-9_-] and at most " +
		strconv.Itoa(PresetIDRuneCap) + " characters")
	// ErrPresetTier reports a fragment declaring a section the OTHER axis owns —
	// the disjointness violation TestLayoutAndStylePresetKeySetsAreDisjoint
	// exists to make impossible. Refused rather than ignored: a style fragment
	// carrying [overrides] would silently move widgets the layout axis owns, and
	// the two axes would stop being swappable.
	ErrPresetTier = errors.New("theme: preset declares a section its axis does not own")
	// ErrPresetElementKind reports `kind = pane` in a style fragment or a
	// non-pane element in a layout fragment. Same partition, one level down:
	// [element.*] is the one section BOTH axes may declare, so the kind decides
	// which tier the element belongs to.
	ErrPresetElementKind = errors.New("theme: preset element kind belongs to the other axis")
	// ErrPresetAnchor reports a style element that neither anchors to a slot key
	// nor appears in its own [preset] full_canvas list (design §6.1, gated by
	// TestStylePresetElementsAlwaysAnchor). It is THE orthogonality rule: an
	// absolutely-placed decoration is decoration that only fits one layout.
	ErrPresetAnchor = errors.New("theme: style preset element is neither anchored nor declared full_canvas")
)

// PresetKind is which axis a fragment belongs to.
//
// WIRE NAMES ARE THE FORMAT, exactly as they are for every sidecar enum:
// append-only, at the end, never renamed. Unlike those enums this one does not
// degrade — see ErrPresetKind.
type PresetKind uint8

const (
	// PresetLayout places: [overrides], [rotations], [pane.*] and pane elements.
	PresetLayout PresetKind = iota
	// PresetStyle dresses: [palette], [chrome], [fonts], [fontbind], [media],
	// [sounds], [clock.*], [effect.*] and every non-pane element.
	PresetStyle
	// PresetKindCount bounds every per-kind table.
	PresetKindCount
)

var presetKindNames = [PresetKindCount]string{"layout", "style"}

// String returns the wire name, or "" for a value outside the enum.
func (k PresetKind) String() string { return enumName(presetKindNames[:], uint8(k)) }

// PresetKindOf resolves a wire name.
func PresetKindOf(s string) (PresetKind, bool) {
	v, ok := enumValue(presetKindNames[:], strings.TrimSpace(s))
	return PresetKind(v), ok
}

// presetTierSections is THE PARTITION, as data.
//
// Indexed by PresetKind. Each entry lists the exact sections that axis owns:
// plain names, and prefixes for the id-suffixed families. A section in neither
// list is owned by neither axis and is refused in both (there is no third
// tier), which is what keeps [theme] and [import] — identity and provenance —
// out of preset data entirely: a fragment is a contribution to a theme, never a
// theme, and a fragment that could name itself the author would overwrite one.
//
// TestLayoutAndStylePresetKeySetsAreDisjoint asserts over this table AND over
// the shipped fragments, so neither the rule nor the data can drift alone.
var presetTierSections = [PresetKindCount]struct {
	exact    []string
	prefixes []string
}{
	PresetLayout: {
		exact:    []string{secOverrides, secRotations},
		prefixes: []string{secPanePrefix, secElementPrefix},
	},
	PresetStyle: {
		exact:    []string{secPalette, secChrome, secFonts, secFontBind, secMedia, secSounds},
		prefixes: []string{secClockPrefix, secEffectPrefix, secElementPrefix},
	},
}

// presetManifestSections are the three sections a tier MERGES rather than
// replaces (presetmerge.go's header says why: every value in them names a file
// inside the theme folder, and dropping the entry orphans the bytes). They are
// listed here, beside the partition they are the exception to, because the tier
// CLEAR below is the only place the distinction has teeth.
var presetManifestSections = []string{secFonts, secMedia, secSounds}

// presetTierClears reports whether applying tier k must delete this section from
// the carrier before the writer re-emits the tier's own model into it.
//
// IT IS THE PARTITION AGAIN, with the two exceptions the merge already states:
// the manifests are merged rather than replaced, and [element.*] is owned by both
// axes so it is decided per ELEMENT (droppedElementIDs) rather than per section.
// Everything else a tier owns is CONFIGURATION, which presetmerge.go's header
// says is replaced wholesale — and a replacement that left the previous tier's
// lines in the file is not one. Anything an axis does not own is untouched, so a
// style apply cannot reach [overrides] and a layout apply cannot reach [palette].
func presetTierClears(k PresetKind, section string) bool {
	if section == secPreset || strings.HasPrefix(section, secElementPrefix) {
		return false
	}
	if !presetOwns(k, section) {
		return false
	}
	for _, m := range presetManifestSections {
		if section == m {
			return false
		}
	}
	return true
}

// presetOwns reports whether an axis may declare a section. [preset] itself is
// owned by both: it is the header that says which axis this is.
func presetOwns(k PresetKind, section string) bool {
	if section == secPreset {
		return true
	}
	if k >= PresetKindCount {
		return false
	}
	t := presetTierSections[k]
	for _, name := range t.exact {
		if section == name {
			return true
		}
	}
	for _, p := range t.prefixes {
		if strings.HasPrefix(section, p) {
			return true
		}
	}
	return false
}

// presetElementKindOK reports whether an element kind belongs to an axis.
// [element.*] is the ONE section both axes declare, so this is the second half
// of the partition and it is spelled once, here.
func presetElementKindOK(k PresetKind, ek ElementKind) bool {
	if k == PresetLayout {
		return ek == ElemPane
	}
	return ek != ElemPane
}

// Preset is one parsed fragment: its header, and the sidecar tier it carries.
//
// Side is a FULL *Sidecar rather than a reduced struct on purpose. The whole
// argument for the format is that a preset is written in exactly the grammar a
// user authors, so anything a user can write a preset must be able to carry —
// and the moment the preset model is a subset, the two grammars start to drift
// and the editor grows a second code path for "things only presets can do".
type Preset struct {
	Kind        PresetKind
	ID          string // [a-z0-9_-]{1,24}; the file stem and the [theme] *_preset value
	Name        string // gallery card title
	Description string // gallery card body
	Credit      string // the "zero bytes of art" line (§6.5 clause 7)

	// DesignW / DesignH are the canvas a LAYOUT fragment's geometry was authored
	// against. Zero on a style fragment, which is not an omission: style geometry
	// is anchor-relative by rule, so it has no canvas of its own to declare.
	DesignW, DesignH int

	// FullCanvas lists the element ids this fragment declares layout-independent
	// (design §6.1: scanlines, vignette, grid floor, noise). It is an EXPLICIT
	// allow-list rather than an inference from the rect, so a decoration that
	// merely happens to be large cannot quietly opt out of the anchor rule.
	FullCanvas []string

	// Side is the fragment's contribution, parsed by the ordinary sidecar reader.
	// Its Meta is always zero — [theme] is not a section either axis owns.
	Side *Sidecar
}

// IsFullCanvas reports whether the fragment declared this element id
// layout-independent. Linear over a list bounded by PresetFullCanvasCap, on the
// cold path only; a map would be an allocation per fragment for a list whose
// median length is three.
func (p *Preset) IsFullCanvas(id string) bool {
	if p == nil {
		return false
	}
	for _, e := range p.FullCanvas {
		if e == id {
			return true
		}
	}
	return false
}

// Elements is the fragment's element list (nil-safe; never a copy — callers
// read it, and the library is immutable after load).
func (p *Preset) Elements() []Element {
	if p == nil || p.Side == nil {
		return nil
	}
	return p.Side.Elements
}

// ParsePreset reads one fragment. The BODY goes through ParseSidecar — the same
// reader, the same caps, the same degrades — and only the [preset] header and
// the tier partition are this function's own.
//
// IT REFUSES where the sidecar reader degrades, and the asymmetry is deliberate.
// Format rule 1 ("reading never refuses") protects a STRANGER'S theme, which
// must render as well as it can whatever is wrong with it. A fragment is OURS,
// shipped inside the binary: a refusal here is a build-time gate failure, not a
// user's evening ruined, and the one thing worse than a preset that fails to
// parse is a preset that half-parses into a gallery card nobody can explain.
//
// COLD PATH ONLY (hard rule 2).
func ParsePreset(src []byte) (*Preset, error) {
	sc, err := ParseSidecar(src)
	if err != nil {
		return nil, err
	}
	doc := sc.doc // in-package: the reader kept it, and this is the same package
	p := &Preset{Side: sc}

	kindRaw, _ := doc.Get(secPreset, "kind")
	kind, ok := PresetKindOf(kindRaw)
	if !ok {
		return nil, fmt.Errorf("%w (got %q)", ErrPresetKind, strings.TrimSpace(kindRaw))
	}
	p.Kind = kind

	idRaw, _ := doc.Get(secPreset, "id")
	p.ID = strings.TrimSpace(idRaw)
	if !validID(p.ID) {
		return nil, fmt.Errorf("%w (got %q)", ErrPresetID, p.ID)
	}

	// Free text rides the same four-character encoding every other free-text
	// value in the format does (%25 %3B %0A %0D), so a semicolon in a description
	// survives the INI comment rule.
	//
	// THE LENGTH RULE IS THE METADATA DEGRADE (metatext.go), not the refusal
	// freeText applies. name/description/credit are display text in the exact
	// sense that ruling names: nothing resolves them, nothing paints them into the
	// courtroom, and no other value's meaning depends on them. A 377-rune
	// attribution — which `investigation` genuinely carries, and should, because a
	// "zero bytes of art" claim is worth spelling out — must not be able to take
	// the whole preset out of the gallery. Same seam, same constant, same note:
	// truncMetaRunes is called, never re-implemented.
	p.Name = sc.degradePresetText(p, "name", metaFree(mustGet(doc, secPreset, "name")))
	p.Description = sc.degradePresetText(p, "description", metaFree(mustGet(doc, secPreset, "description")))
	p.Credit = sc.degradePresetText(p, "credit", metaFree(mustGet(doc, secPreset, "credit")))
	if p.Name == "" {
		p.Name = p.ID // a card with no title is a card nobody can pick
	}

	if raw, present := doc.Get(secPreset, "design_w"); present {
		p.DesignW = clampInt(atoiTrim(raw), PresetDesignMinPx, PresetDesignMaxPx)
	}
	if raw, present := doc.Get(secPreset, "design_h"); present {
		p.DesignH = clampInt(atoiTrim(raw), PresetDesignMinPx, PresetDesignMaxPx)
	}
	if raw, present := doc.Get(secPreset, "full_canvas"); present {
		if p.FullCanvas, err = parseTextList(raw, PresetFullCanvasCap); err != nil {
			return nil, err
		}
	}

	if err := p.validateTier(doc); err != nil {
		return nil, err
	}
	return p, nil
}

// mustGet is Get without the presence flag, for the keys whose absence and
// whose emptiness mean the same thing (a free-text header field).
func mustGet(d *INIDoc, section, key string) string {
	v, _ := d.Get(section, key)
	return v
}

// degradePresetText applies the METADATA class rule (metatext.go) to one
// [preset] header string: truncate to NameRuneCap and note it, never refuse.
//
// It calls truncMetaRunes rather than repeating the rune walk — the note text is
// the same sentence degradeMeta writes, because a user who hits the cap from a
// preset and from a theme should not have to learn that they are two rules.
func (s *Sidecar) degradePresetText(p *Preset, key, v string) string {
	cut, dropped := truncMetaRunes(v)
	if dropped == 0 {
		return v
	}
	// DEGRADE path: NoteCap bounds the report, not the load (see note).
	_ = s.note("[%s] %s of preset %q is %d runes, %d over the %d-rune limit — it is shortened for display and the file is kept as written",
		secPreset, key, p.ID, utf8.RuneCountInString(cut)+dropped, dropped, NameRuneCap)
	return cut
}

// validateTier enforces the partition: the sections, the element kinds and the
// style axis's anchor rule.
//
// It walks the DOCUMENT for sections rather than the model, because a section
// the model dropped (an unknown one, a [theme] block) is still a section the
// fragment declared — and "the reader ignored it" is exactly the shape a
// disjointness violation would hide behind.
func (p *Preset) validateTier(d *INIDoc) error {
	for _, name := range walkSections(d).names {
		if name == "" {
			continue // the root section: INI preamble comments live there
		}
		if !presetOwns(p.Kind, name) {
			return fmt.Errorf("%w: %s preset %q declares [%s]", ErrPresetTier, p.Kind, p.ID, name)
		}
	}
	for i := range p.Side.Elements {
		e := &p.Side.Elements[i]
		if !presetElementKindOK(p.Kind, e.Kind) {
			return fmt.Errorf("%w: %s preset %q element %q is kind = %s",
				ErrPresetElementKind, p.Kind, p.ID, e.ID, e.Kind)
		}
		if p.Kind != PresetStyle {
			continue
		}
		if e.Anchor == "" && !p.IsFullCanvas(e.ID) {
			return fmt.Errorf("%w: style preset %q element %q", ErrPresetAnchor, p.ID, e.ID)
		}
	}
	return nil
}
