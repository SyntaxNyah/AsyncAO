package theme

// THE FOUR-TIER MERGE.
// A rendered AsyncAO theme is four tiers stacked in this order, lowest first:
// WHAT "APPLYING A PRESET" MEANS, stated once, because everything else follows:

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrPresetMergeCap reports a merge whose result would exceed a named sidecar
	// cap — ElementCap above all, since a layout's panes and a style's decoration
	// both land in one list. REFUSED, never truncated, exactly as the reader
	// refuses: half a preset is a theme nobody authored.
	ErrPresetMergeCap = errors.New("theme: applying this preset would exceed a named cap")
	// ErrPresetMergeCollision reports two elements claiming one id across the
	// tiers. It cannot happen with the shipped set (the ids are disjoint and the
	// gate asserts it), but a user element sharing an id with a preset's would
	// otherwise produce a sidecar whose own Validate refuses it — at SAVE time,
	// long after the act that caused it.
	ErrPresetMergeCollision = errors.New("theme: an element id is claimed by both the theme and the preset")
)

// PresetPair is the two axes a theme resolves to. It exists so the many call
// sites that carry "a layout and a style, either of which may be absent" carry
// ONE value instead of two positional pointers nothing stops them swapping.
type PresetPair struct {
	Layout *Preset
	Style  *Preset
}

// Empty reports a pair that would change nothing.
func (p PresetPair) Empty() bool { return p.Layout == nil && p.Style == nil }

// ResolvePresets reads a theme's declared axes out of its own [theme] section.
// AN UNKNOWN ID IS NOT AN ERROR IN THE THEME'S SENSE (format rule 1): a theme
// authored on a newer build names a preset this one has never heard of, and the
// right behaviour is to render everything else and say so. The unresolved ids
// come back in `missing` for the import report; the caller decides how loud to
// be about them, and no caller may refuse the theme over one.
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

// MergePresets returns a NEW sidecar: `base` with both tiers applied.
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

// PresetGeneratedBy is the [theme] generated_by stamp a gallery-built theme
// carries. It is PROVENANCE, never a version check (Meta.MinClient's own comment
// says why enforcing one would brick themes on downgrade): its job is to answer
// "where did this file come from" three months later, in a zip somebody
// re-shared.
const PresetGeneratedBy = "AsyncAO theme gallery"

// NewThemeFromPresets builds a theme FROM NOTHING: a fresh sidecar carrying both
// tiers, its identity stamped, its document empty.
func NewThemeFromPresets(name string, pair PresetPair) (*Sidecar, error) {
	out, err := MergePresets(nil, pair)
	if err != nil {
		return nil, err
	}
	out.Meta.Name = name
	out.Meta.GeneratedBy = PresetGeneratedBy
	// The art-provenance line comes from the STYLE axis because the style axis is
	// what draws: §6.5 clause 7 asks every preset to state that it ships zero bytes
	// of art, and it is the style fragments that would have been the ones to ship
	// some. A layout contributes rectangles.
	if pair.Style != nil {
		out.Meta.Credit = pair.Style.Credit
		out.Meta.Description = pair.Style.Description
	}
	return out, nil
}

// PresetUndo is the one-deep snapshot a preset apply can be reverted from.
type PresetUndo struct{ snap *Sidecar }

// Valid reports a snapshot that can be restored. A zero PresetUndo is "nothing
// to go back to", which is what the gallery's Undo button reads to disable
// itself, so it must be answerable without unwrapping anything.
func (u *PresetUndo) Valid() bool { return u != nil && u.snap != nil }

// SnapshotForPreset captures everything ApplyPresetTier can change.
// IT IS clonePresetBase, DELIBERATELY — the same deep copy the merge uses, so the
// snapshot cannot fall behind a field the merge learned to touch. A hand-written
// field list here would be the second place that has to know Sidecar's shape, and
// the first time the two disagreed a preset apply would become unundoable in
// exactly one section.
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

// RestorePreset puts this sidecar back exactly as the snapshot found it.
// The DOCUMENT is restored too. Skipping it would leave the carrier holding the
// preset's lines after the model had gone back, and the next save would write a
// file that is neither state.
func (s *Sidecar) RestorePreset(u *PresetUndo) bool {
	if s == nil || !u.Valid() {
		return false
	}
	doc := u.snap.doc
	*s = *u.snap
	s.doc = doc
	// The snapshot is spent: restoring twice from one capture would silently undo
	// an edit made AFTER the restore. The gallery re-captures before every apply.
	u.snap = nil
	return true
}

// AdoptPresetMerge takes over a merged model IN PLACE, keeping this object's
// identity.
func (s *Sidecar) AdoptPresetMerge(src *Sidecar) {
	if s == nil || src == nil || s == src {
		return
	}
	doc := src.doc
	*s = *src
	s.doc = doc
}

// clonePresetBase deep-copies a sidecar, document included.
// THE DOCUMENT IS RE-PARSED FROM ITS OWN BYTES rather than field-copied. INIDoc
// holds a line list, a section index and a touched set, and a struct copy would
// alias the first two — so the "copy" would write through into the original the
// moment anything set a key, which is the exact failure this function exists to
// prevent. Re-parsing is O(file) on a cold path and is pinned lossless by
// FuzzINIDocRoundTrip, so it is the cheap way to be certain rather than careful.
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
	// Every slice and map field re-allocated, in the order Sidecar declares them,
	// so a field added to the struct without a line here shows up as an aliasing
	// bug in TestMergePresetsNeverMutatesItsBase rather than as a silent share.
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

// ApplyPresetTier replaces ONE axis of this sidecar IN PLACE.
// In place, because the editor's live model is shared by pointer with the
// courtroom's renderer (a.themeSidecar and themeDoc.side are the same object):
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
	// --- past this line nothing can fail -------------------------------------
	// THE CARRIER LOSES WHAT THE MODEL LOST, before the model changes, or the file
	// would hold both tiers and the reader would hand back their union.
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
	// The manifests, merged by id rather than replaced — see the file header.
	s.Fonts = mergeFonts(s.Fonts, p.Side.Fonts)
	s.Media = mergeMedia(s.Media, p.Side.Media)
	s.Sounds = mergeKV(s.Sounds, p.Side.Sounds)
	return nil
}

// clearReplacedTier deletes, from the lossless CARRIER, every section this tier
// replace has just taken over in the MODEL.
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

// droppedElementIDs is the set of element ids whose sections this tier replace
// takes over: the ones the incoming axis OWNS in the model, plus every id the
// incoming fragment itself declares.
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

// splitElementsByTier partitions this sidecar's elements into the ones the
// incoming tier does NOT own (kept, in their existing order) and returns the
// tier's own contribution (adopted, deep-copied out of the immutable library).
// OWNERSHIP IS BY KIND, and it is the same test the parser enforces on the
// fragment (presetElementKindOK). One rule, one spelling: a pane element is
// layout, everything else is style, in the file and in the apply alike.
func (s *Sidecar) splitElementsByTier(p *Preset) (kept, adopted []Element) {
	for i := range s.Elements {
		if !presetElementKindOK(p.Kind, s.Elements[i].Kind) {
			kept = append(kept, s.Elements[i])
		}
	}
	// A VALUE COPY out of the library, which is what keeps the library immutable
	// (presetlib.go): Element is a flat struct — every field is a scalar, a string
	// or a fixed array — so one copy is a complete one, with nothing left aliased
	// for a later edit to write through. TestPresetLibraryIsImmutableAcrossCallers
	// drives exactly that: apply, edit the result, apply again elsewhere.
	src := p.Elements()
	adopted = make([]Element, len(src))
	copy(adopted, src)
	return kept, adopted
}

// elementIDsDisjoint refuses a merge that would produce two [element.<id>]
// sections. Sidecar.Validate would catch it too — but at SAVE time, which is
// hours after the click that caused it.
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

// mergeFonts / mergeMedia / mergeKV upsert a manifest by its id field, keeping
// the existing order and appending what is new. Three near-identical functions
// rather than one generic: the id field has a different name in each, the caps
// differ, and a reflective or type-parameterised version would be harder to read
// than the thing it replaced.
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
