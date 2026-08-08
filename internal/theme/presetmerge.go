package theme

// THE FOUR-TIER MERGE.
//
// A rendered AsyncAO theme is four tiers stacked in this order, lowest first:
//
//	1. the AO2 theme itself       — courtroom_design.ini, resolved by theme.Load
//	2. the LAYOUT preset          — where things are
//	3. the STYLE preset           — what they look like
//	4. the author's own sidecar   — everything they changed by hand
//
// Tier 1 is not in this file: it is the AO2 rect map internal/ui folds
// [overrides] onto, and it never enters the sidecar model. Tiers 2 and 3 are
// applied HERE, into tier 4's model, which is why the whole thing is a merge of
// three objects and not of four.
//
// WHY THIS IS A CONCATENATION AND NOT A NEGOTIATION. The two preset axes own
// DISJOINT sections (preset.go's presetTierSections), so no key can be claimed
// twice and there is no precedence question between tiers 2 and 3 to answer.
// That is the entire orthogonality argument for a 21 x 14 gallery, and it is
// enforced at parse time rather than assumed here.
//
// WHAT "APPLYING A PRESET" MEANS, stated once, because everything else follows:
//
//	REPLACE the tier's CONFIGURATION, MERGE the tier's MANIFESTS.
//
// Configuration is [overrides], [rotations], [pane.*], [palette], [chrome],
// [fontbind], [clock.*], [effect.*] and the elements of that axis's kind. It is
// replaced WHOLESALE — including anything the user hand-authored into it —
// because "apply a layout" that left half of the previous layout behind is not
// an act anyone can predict the result of, and because a tier you cannot replace
// is a tier you cannot swap.
//
// Manifests are [fonts], [media] and [sounds]. They are MERGED BY ID, never
// pruned, and the exception is not a softening — it is the same rule one level
// down. Every value in those three sections NAMES A FILE INSIDE THE THEME
// FOLDER: art the user dragged in, a font they licensed, a blip they recorded.
// Dropping the entry does not drop the bytes, it ORPHANS them — the file stays
// on disk, referenced by nothing, and the element that used it goes blank with
// no way back. The shipped presets declare zero manifest entries of any kind
// (§6.4: no bundled art), so today this costs nothing at all; it exists so that
// the first preset that ever does declare one cannot delete a stranger's work.
//
// COLD PATH ONLY (hard rule 2): it allocates a whole model.

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
//
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
//
// `base` IS NEVER TOUCHED — not its model, not its lossless document, not one
// slice header it shares. That is the property the gallery's live preview rests
// on (scrub a row, render it, throw it away) and the one
// TestMergePresetsNeverMutatesItsBase hashes before and after.
//
// A nil `base` is "a theme from nothing", which is what the gallery's create
// action passes: the result is a fresh sidecar whose document is empty, so the
// writer emits every section canonically instead of editing someone's file.
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
//
// The empty document is the point. Every other path into a sidecar edits a file
// somebody wrote, and the writer's whole contract is "emit the original bytes on
// any line I did not deliberately change" — which for a file that does not exist
// yet means it emits the model, canonically ordered, and nothing else. There is
// no stale section to inherit and nothing of the user's to preserve, so this is
// the one path where replacing a tier can never leave a trace of the old one.
//
// It is therefore the gallery's PRIMARY action, and the reason the gallery
// creates rather than converts.
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
//
// WHY A SNAPSHOT AND NOT AN UNDO-RING OP. internal/ui's ring is built from
// fixed-size field edits whose identity is an ELEMENT INDEX (themeeditordoc.go),
// and applying a preset replaces the whole element list — so every op already in
// the ring points at a row that is no longer the row it meant. The editor's own
// answer to that class is a snapshot (restoreBaseline, for "throw away what I
// have not saved"), for the stated reason that a capped replay restores most of a
// session and stays silent about the rest. A preset apply is the same shape of
// act and takes the same shape of answer: ALL OF IT OR NONE OF IT.
//
// The snapshot is opaque on purpose. It holds a whole *Sidecar, and exposing that
// would hand a caller a second model of the same theme with no arbitration —
// which is the exact door Sidecar.Doc() was deleted for (ledger L32).
type PresetUndo struct{ snap *Sidecar }

// Valid reports a snapshot that can be restored. A zero PresetUndo is "nothing
// to go back to", which is what the gallery's Undo button reads to disable
// itself, so it must be answerable without unwrapping anything.
func (u *PresetUndo) Valid() bool { return u != nil && u.snap != nil }

// SnapshotForPreset captures everything ApplyPresetTier can change.
//
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
//
// IN PLACE, into the SAME object — the editor's document and the courtroom's
// renderer hold this pointer, and handing back a new one would leave the two
// looking at different themes (layoutnudge.go's evaporation hazard).
//
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
//
// THE POINTER IS THE THING, and that is why this exists instead of the caller
// assigning the merge result. internal/ui's a.themeSidecar, themeDoc.side and
// every element index in the editor's selection all name ONE *Sidecar; handing
// back a different one leaves the courtroom rendering the old theme and the
// editor saving the new one, which is layoutnudge.go's evaporation hazard with
// the arrow pointing the other way.
//
// The DOCUMENT comes with it. The merge cloned the carrier alongside the model,
// so keeping the old one would leave a file whose lines describe neither state.
//
// It is a no-op if src is nil or is this very sidecar: adopting yourself is what
// a caller does when the merge changed nothing, and it must not be a way to
// half-assign.
func (s *Sidecar) AdoptPresetMerge(src *Sidecar) {
	if s == nil || src == nil || s == src {
		return
	}
	doc := src.doc
	*s = *src
	s.doc = doc
}

// clonePresetBase deep-copies a sidecar, document included.
//
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
//
// In place, because the editor's live model is shared by pointer with the
// courtroom's renderer (a.themeSidecar and themeDoc.side are the same object):
// a function that returned a new sidecar would leave the two pointing at
// different themes, which is the evaporation hazard layoutnudge.go already
// documents. The UNDO of this act is a snapshot taken by the caller, for the
// same reason every other structural editor op takes one.
//
// A nil preset is a no-op and a success — "this theme declares no style" is a
// legal state, not a failure, and making every call site branch on it is how a
// missing tier becomes an error dialog.
//
// ALL OF IT OR NONE OF IT. Every refusal is decided before the first field
// moves, so a failed apply leaves a model that was never half-written.
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
//
// IT IS THE OTHER HALF OF "REPLACE THE TIER'S CONFIGURATION" (see this file's
// header), and it was missing. The writer edits the document the reader kept and
// has no key or section delete of its own, so replacing the model's element list
// left the previous style's [element.*] sections standing: 35 elements were
// written and 70 read back, and the worst shipped re-style reloaded at 97 against
// ElementCap = 96 — a theme the reader REFUSES the moment its author reopens it.
// The same leak reached [effect.*] and [clock.*] (6 effects out, 10 back) and
// would reach [overrides] the first time two layouts declared different key sets.
//
// The rule is presetTierClears plus droppedElementIDs, so the sections a tier
// clears are the sections the partition already says it owns — there is no second
// list to keep in step, and the manifests keep their documented exemption.
//
// A section the document never carried is not an error: an element authored this
// session, or a whole theme built from nothing, has no lines to remove yet.
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
//
// The ownership test is presetElementKindOK — the same one splitElementsByTier
// uses, and the same one the parser enforces on the fragment — so "which elements
// does this tier replace" has exactly one spelling in the package.
//
// AN ID THE NEW TIER RE-DECLARES IS CLEARED TOO, and leaving it in place was
// wrong twice. The writer only edits a key when the model has something to say
// about it (setKey: an empty value leaves the author's line alone), so an old
// element's `anchor`, `media`, `shape`, `generator` or `text` would survive
// underneath a new element of the same name that happens not to set them — a row
// that is half of each style. And the surviving section keeps its OLD position in
// the file, so the reload's declaration order stops matching the model's.
//
// A section for an id in NEITHER set is untouched, which is what keeps a
// preserved forward-compat element (one whose `kind` this build does not know, so
// the reader skipped it and kept its lines — format rule 3) out of the blast.
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
//
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
