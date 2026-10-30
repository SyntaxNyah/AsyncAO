package theme

// The four-tier preset merge; cold path only (hard rule 2), as it allocates a whole model.

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrPresetMergeCap reports a merge past a named cap; refused, as half a preset is nobody's theme.
	ErrPresetMergeCap = errors.New("theme: applying this preset would exceed a named cap")
	// ErrPresetMergeCollision reports one id across tiers, which Validate would only catch at save.
	ErrPresetMergeCollision = errors.New("theme: an element id is claimed by both the theme and the preset")
)

// PresetPair is the two axes a theme resolves to, one value so call sites cannot swap two pointers.
type PresetPair struct {
	Layout *Preset
	Style  *Preset
}

func (p PresetPair) Empty() bool { return p.Layout == nil && p.Style == nil }

// ResolvePresets reads a theme's declared axes; no caller may refuse the theme over missing ids.
func (l *PresetLibrary) ResolvePresets(sc *Sidecar) (pair PresetPair, missing []string) {
	if sc == nil {
		return PresetPair{}, nil
	}
	for _, ax := range [...]struct {
		kind PresetKind
		id   string
		into **Preset
	}{
		{PresetLayout, sc.Meta.LayoutPreset, &pair.Layout},
		{PresetStyle, sc.Meta.StylePreset, &pair.Style},
	} {
		p, err := l.Find(ax.kind, ax.id)
		if err != nil {
			missing = append(missing, ax.kind.String()+"/"+ax.id)
			continue
		}
		*ax.into = p
	}
	return pair, missing
}

// MergePresets returns a new sidecar, base with both tiers; a nil base gives an empty document.
func MergePresets(base *Sidecar, pair PresetPair) (*Sidecar, error) {
	out, err := clonePresetBase(base)
	if err != nil {
		return nil, err
	}
	for _, p := range [...]*Preset{pair.Layout, pair.Style} {
		if err := out.ApplyPresetTier(p); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// PresetGeneratedBy is the [theme] generated_by stamp, provenance and never a version check.
const PresetGeneratedBy = "AsyncAO theme gallery"

// NewThemeFromPresets builds a theme from nothing: both tiers, identity stamped, document empty.
func NewThemeFromPresets(name string, pair PresetPair) (*Sidecar, error) {
	out, err := MergePresets(nil, pair)
	if err != nil {
		return nil, err
	}
	out.Meta.Name = name
	out.Meta.GeneratedBy = PresetGeneratedBy
	// The art-provenance line comes from the style axis, which is what draws; a layout contributes rectangles.
	if pair.Style != nil {
		out.Meta.Credit = pair.Style.Credit
		out.Meta.Description = pair.Style.Description
	}
	return out, nil
}

// PresetUndo is the one-deep snapshot of a preset apply; opaque so callers get no second model of the theme.
type PresetUndo struct{ snap *Sidecar }

// Valid reports a snapshot that can be restored; a zero PresetUndo disables the gallery's Undo button.
func (u *PresetUndo) Valid() bool { return u != nil && u.snap != nil }

// SnapshotForPreset uses the merge's own deep copy, so it cannot fall behind a field.
func (s *Sidecar) SnapshotForPreset() (*PresetUndo, error) {
	if s == nil {
		return nil, nil
	}
	snap, err := clonePresetBase(s)
	if err != nil {
		return nil, err
	}
	return &PresetUndo{snap: snap}, nil
}

// RestorePreset restores the document too, or the next save would write neither state.
func (s *Sidecar) RestorePreset(u *PresetUndo) bool {
	if s == nil || !u.Valid() {
		return false
	}
	doc := u.snap.doc
	*s = *u.snap
	s.doc = doc
	// Restoring twice from one capture would silently undo an edit made after the restore.
	u.snap = nil
	return true
}

// AdoptPresetMerge takes over a merged model in place; a no-op if src is nil or this sidecar.
func (s *Sidecar) AdoptPresetMerge(src *Sidecar) {
	if s == nil || src == nil || s == src {
		return
	}
	doc := src.doc
	*s = *src
	s.doc = doc
}

// clonePresetBase re-parses the document, as a struct copy would alias lines and write through.
func clonePresetBase(base *Sidecar) (*Sidecar, error) {
	if base == nil {
		return NewSidecar(), nil
	}
	doc, err := ParseINIDoc(base.doc.Bytes())
	if err != nil {
		return nil, err
	}
	out := *base // value copy: scalars and Meta land correctly, slices are aliased
	out.doc = doc
	// Every slice and map field is re-allocated; a field missed here is shared with the base.
	out.Overrides = append([]KeyRect(nil), base.Overrides...)
	out.Rotations = append([]KeyAngle(nil), base.Rotations...)
	out.Fonts = append([]FontFamily(nil), base.Fonts...)
	out.FontBind = append([]KV(nil), base.FontBind...)
	out.Media = append([]MediaEntry(nil), base.Media...)
	out.Panes = append([]Pane(nil), base.Panes...)
	out.Elements = append([]Element(nil), base.Elements...)
	out.Effects = append([]EffectBind(nil), base.Effects...)
	out.Sounds = append([]KV(nil), base.Sounds...)
	out.Import.Subthemes = append([]string(nil), base.Import.Subthemes...)
	out.notes = append([]string(nil), base.notes...)
	out.unknown = append([]string(nil), base.unknown...)
	out.retire = append([]string(nil), base.retire...)
	return &out, nil
}

// ApplyPresetTier replaces one axis in place, as the editor's model is shared with the renderer.
// Every refusal comes before the first field moves.
func (s *Sidecar) ApplyPresetTier(p *Preset) error {
	if s == nil || p == nil {
		return nil
	}
	kept, adopted := s.splitElementsByTier(p)
	if n := len(kept) + len(adopted); n > ElementCap {
		return fmt.Errorf("%w: %d elements, cap is %d", ErrPresetMergeCap, n, ElementCap)
	}
	if err := elementIDsDisjoint(kept, adopted); err != nil {
		return err
	}
	// Past this line nothing can fail; clear the document first, or the reader returns both tiers.
	s.clearReplacedTier(p)
	s.Elements = append(kept, adopted...)
	switch p.Kind {
	case PresetLayout:
		s.Overrides = append([]KeyRect(nil), p.Side.Overrides...)
		s.Rotations = append([]KeyAngle(nil), p.Side.Rotations...)
		s.Panes = append([]Pane(nil), p.Side.Panes...)
		s.Meta.LayoutPreset = p.ID
	case PresetStyle:
		s.Palette = p.Side.Palette
		s.Chrome = p.Side.Chrome
		s.FontBind = append([]KV(nil), p.Side.FontBind...)
		s.Clocks = p.Side.Clocks
		s.Effects = append([]EffectBind(nil), p.Side.Effects...)
		s.Meta.StylePreset = p.ID
	}
	// Manifests merge by id rather than replace, because their values name files in the theme folder.
	s.Fonts = mergeFonts(s.Fonts, p.Side.Fonts)
	s.Media = mergeMedia(s.Media, p.Side.Media)
	s.Sounds = mergeKV(s.Sounds, p.Side.Sounds)
	return nil
}

// clearReplacedTier deletes the sections this replace took over; one never carried is no error.
func (s *Sidecar) clearReplacedTier(p *Preset) {
	if s.doc == nil {
		return
	}
	dropped := s.droppedElementIDs(p)
	for _, name := range walkSections(s.doc).names {
		if name == "" {
			continue // the root section: INI preamble comments live there
		}
		if strings.HasPrefix(name, secElementPrefix) {
			if _, gone := dropped[strings.TrimPrefix(name, secElementPrefix)]; !gone {
				continue
			}
		} else if !presetTierClears(p.Kind, name) {
			continue
		}
		_ = s.doc.RemoveSection(name)
	}
}

// droppedElementIDs is what the incoming axis owns or declares; a preserved unknown kind stays.
func (s *Sidecar) droppedElementIDs(p *Preset) map[string]struct{} {
	out := make(map[string]struct{}, len(s.Elements))
	for i := range s.Elements {
		if presetElementKindOK(p.Kind, s.Elements[i].Kind) {
			out[s.Elements[i].ID] = struct{}{}
		}
	}
	for _, e := range p.Elements() {
		out[e.ID] = struct{}{}
	}
	return out
}

// splitElementsByTier returns kept and adopted elements; ownership is by kind (presetElementKindOK).
func (s *Sidecar) splitElementsByTier(p *Preset) (kept, adopted []Element) {
	for i := range s.Elements {
		if !presetElementKindOK(p.Kind, s.Elements[i].Kind) {
			kept = append(kept, s.Elements[i])
		}
	}
	// A value copy keeps the library immutable: Element is flat, so nothing stays aliased.
	src := p.Elements()
	adopted = make([]Element, len(src))
	copy(adopted, src)
	return kept, adopted
}

// elementIDsDisjoint refuses two [element.<id>] at the click, not later at save.
func elementIDsDisjoint(kept, adopted []Element) error {
	if len(adopted) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(kept))
	for i := range kept {
		seen[kept[i].ID] = struct{}{}
	}
	for i := range adopted {
		if _, dup := seen[adopted[i].ID]; dup {
			return fmt.Errorf("%w: %q", ErrPresetMergeCollision, adopted[i].ID)
		}
	}
	return nil
}

// mergeFonts, mergeMedia and mergeKV upsert by id; not generic because id fields and caps differ.
func mergeFonts(dst, src []FontFamily) []FontFamily {
	for _, e := range src {
		replaced := false
		for i := range dst {
			if dst[i].Family == e.Family {
				dst[i], replaced = e, true
				break
			}
		}
		if !replaced && len(dst) < FontCap {
			dst = append(dst, e)
		}
	}
	return dst
}

func mergeMedia(dst, src []MediaEntry) []MediaEntry {
	for _, e := range src {
		replaced := false
		for i := range dst {
			if dst[i].ID == e.ID {
				dst[i], replaced = e, true
				break
			}
		}
		if !replaced && len(dst) < MediaCap {
			dst = append(dst, e)
		}
	}
	return dst
}

func mergeKV(dst, src []KV) []KV {
	for _, e := range src {
		replaced := false
		for i := range dst {
			if dst[i].Key == e.Key {
				dst[i], replaced = e, true
				break
			}
		}
		if !replaced && len(dst) < SoundCap {
			dst = append(dst, e)
		}
	}
	return dst
}
