package theme

// One preset fragment, the parsed form of presets/{layout,style}/<id>.ini; cold path only (hard rule 2).

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

// secPreset is the header, unknown to the sidecar reader, so a fragment stays a legal theme file.
const secPreset = "preset"

const (
	// PresetIDRuneCap shares IDRuneCap, as an id is a file name, section suffix and layout_preset value.
	PresetIDRuneCap = IDRuneCap
	// PresetFullCanvasCap aliases ElementCap, as every entry names an element in the same file.
	PresetFullCanvasCap = ElementCap
	// PresetDesignMinPx and PresetDesignMaxPx clamp a layout's canvas, as a typo must not stop the gallery.
	PresetDesignMinPx = 240
	PresetDesignMaxPx = 6048
)

var (
	// ErrPresetKind is a refusal, unlike sidecar enums, because the kind decides which sections the file may contain.
	ErrPresetKind = errors.New("theme: preset kind must be \"layout\" or \"style\"")
	// ErrPresetID reports an id outside the [a-z0-9_-]{1,24} grammar.
	ErrPresetID = errors.New("theme: preset id must be [a-z0-9_-] and at most " +
		strconv.Itoa(PresetIDRuneCap) + " characters")
	// ErrPresetTier reports a section the other axis owns, or the axes stop being swappable.
	ErrPresetTier = errors.New("theme: preset declares a section its axis does not own")
	// ErrPresetElementKind reports an element kind on the wrong axis; both declare [element.*].
	ErrPresetElementKind = errors.New("theme: preset element kind belongs to the other axis")
	// ErrPresetAnchor reports a style element neither anchored nor in full_canvas, fitting one layout only.
	ErrPresetAnchor = errors.New("theme: style preset element is neither anchored nor declared full_canvas")
)

// PresetKind is a fragment's axis; wire names are append-only, and it does not degrade.
type PresetKind uint8

const (
	// PresetLayout places: [overrides], [rotations], [pane.*] and pane elements.
	PresetLayout PresetKind = iota
	// PresetStyle dresses: [palette], [chrome], [fonts], [fontbind], [media], [sounds], [clock.*],
	// [effect.*] and non-pane elements.
	PresetStyle
	PresetKindCount
)

var presetKindNames = [PresetKindCount]string{"layout", "style"}

func (k PresetKind) String() string { return enumName(presetKindNames[:], uint8(k)) }

func PresetKindOf(s string) (PresetKind, bool) {
	v, ok := enumValue(presetKindNames[:], strings.TrimSpace(s))
	return PresetKind(v), ok
}

// presetTierSections is the partition of sections between axes, as data.
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

// presetManifestSections merge rather than replace: dropping an entry orphans the theme's file.
var presetManifestSections = []string{secFonts, secMedia, secSounds}

// presetTierClears reports whether applying tier k deletes this section before the writer re-emits it.
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

// presetOwns reports whether an axis may declare a section; both own [preset], the header naming the axis.
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
func presetElementKindOK(k PresetKind, ek ElementKind) bool {
	if k == PresetLayout {
		return ek == ElemPane
	}
	return ek != ElemPane
}

// Preset is one parsed fragment; Side is a full *Sidecar, as a subset of the grammar would drift.
type Preset struct {
	Kind        PresetKind
	ID          string // [a-z0-9_-]{1,24}; the file stem and the [theme] *_preset value
	Name        string // gallery card title
	Description string // gallery card body
	Credit      string // the "zero bytes of art" line (§6.5 clause 7)

	// DesignW and DesignH are a layout's authoring canvas; zero on a style, whose geometry is anchor-relative.
	DesignW, DesignH int

	// FullCanvas lists layout-independent ids, explicit so a large decoration cannot skip the anchor rule.
	FullCanvas []string

	// Side is the fragment's contribution, with zero Meta since neither axis owns [theme].
	Side *Sidecar
}

// IsFullCanvas reports whether the fragment declared this id layout-independent; linear, cold path.
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

// Elements is the fragment's element list (nil-safe; not a copy, as the library is immutable after load).
func (p *Preset) Elements() []Element {
	if p == nil || p.Side == nil {
		return nil
	}
	return p.Side.Elements
}

// ParsePreset reads one fragment, its body through ParseSidecar; cold path only (hard rule 2).
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

	// Free text uses the format's percent-encoding, so a semicolon survives the INI comment rule.
	p.Name = sc.degradePresetText(p, "name", metaFree(mustGet(doc, secPreset, "name")))
	p.Description = sc.degradePresetText(p, "description", metaFree(mustGet(doc, secPreset, "description")))
	p.Credit = sc.degradePresetText(p, "credit", metaFree(mustGet(doc, secPreset, "credit")))
	if p.Name == "" {
		p.Name = p.ID // a card with no title is a card nobody can pick
	}

	if raw, present := doc.Get(secPreset, "design_w"); present {
		p.DesignW = value.Clamp(parse.AtoiOr(raw, 0), PresetDesignMinPx, PresetDesignMaxPx)
	}
	if raw, present := doc.Get(secPreset, "design_h"); present {
		p.DesignH = value.Clamp(parse.AtoiOr(raw, 0), PresetDesignMinPx, PresetDesignMaxPx)
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

// mustGet is Get for keys whose absence and emptiness mean the same.
func mustGet(d *INIDoc, section, key string) string {
	v, _ := d.Get(section, key)
	return v
}

// degradePresetText truncates a [preset] header string to NameRuneCap with a note, never refusing (metatext.go).
func (s *Sidecar) degradePresetText(p *Preset, key, v string) string {
	cut, dropped := truncMetaRunes(v)
	if dropped == 0 {
		return v
	}
	_ = s.note("[%s] %s of preset %q is %d runes, %d over the %d-rune limit — it is shortened for display and the file is kept as written",
		secPreset, key, p.ID, utf8.RuneCountInString(cut)+dropped, dropped, NameRuneCap)
	return cut
}

// validateTier enforces the partition; it walks the document, as dropped sections were still declared.
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
