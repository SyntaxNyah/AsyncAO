package theme

// THE PRESET LIBRARY: the shipped 21 x 14 gallery, parsed once.
//
// ONE concern — turning the embedded fragment tree into an ORDERED, BOUNDED,
// IMMUTABLE set of *Preset. It owns no rendering, no merging and no UI; the
// merge is presetmerge.go and the gallery is internal/ui.
//
// IMMUTABLE AFTER LOAD, and that is load-bearing rather than tidy. Every caller
// holds *Preset values straight out of this table, so a caller that mutated one
// would change what every OTHER theme in the session gets applied. The merge
// therefore deep-copies on the way out (presetmerge.go) and nothing here hands
// back a slice a caller is invited to append to. Pinned by
// TestPresetLibraryIsImmutableAcrossCallers.
//
// PARSED ONCE, LAZILY. sync.Once, not an init(): a client that never opens the
// gallery should not pay 35 INI parses at start-up, and hard rule 2 forbids
// doing them on a draw path — Library() is called from the theme-apply
// goroutine and from the gallery's own open, both cold.

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/SyntaxNyah/AsyncAO/internal/theme/presets"
)

const (
	// PresetLayoutCap / PresetStyleCap bound each axis. Hard rule 4: the gallery
	// grid, its thumbnail cache and the coherence matrix are all products of
	// these two numbers, so neither may grow by someone dropping a file in a
	// directory. 21 layouts and 14 styles ship; the headroom is one wave's worth
	// of additions, and past it the LOADER REFUSES rather than truncating — a
	// silently missing preset is a gallery cell nobody can explain.
	PresetLayoutCap = 32
	PresetStyleCap  = 24
	// PresetComboCap is the gallery's size, and the coherence matrix's. A const
	// ALIAS of the product, never a third number: two copies of a bound are two
	// bounds that can disagree.
	PresetComboCap = PresetLayoutCap * PresetStyleCap

	// PresetFragmentByteCap bounds ONE fragment, and it is a second bound on top
	// of IniDocByteCap rather than a repeat of it. That one is 1 MiB because it
	// guards a STRANGER'S theme INI, which may legitimately be anything; this one
	// guards a file WE ship inside the executable, where 1 MiB of embedded bytes
	// arriving unremarked in a diff is the failure. The largest shipped fragment is
	// newspaper at ~40 KiB, so 64 KiB is that rounded to the next power of two:
	// half again of headroom for the class to grow, and an order of magnitude short
	// of the inherited bound, so a fragment that ballooned is a review question
	// rather than a silent binary-size change.
	//
	// REFUSED, not truncated, for the same reason the axis caps are: half a
	// fragment is a gallery card nobody can explain.
	PresetFragmentByteCap = 64 << 10
)

var (
	// ErrPresetCap reports an axis directory holding more fragments than its cap.
	ErrPresetCap = errors.New("theme: preset library exceeds a named cap")
	// ErrPresetDuplicate reports two fragments claiming one id. Refused rather
	// than last-wins: the id is what a theme's [theme] layout_preset stores, so a
	// duplicate makes a saved theme mean two different things.
	ErrPresetDuplicate = errors.New("theme: two presets claim the same id")
	// ErrPresetIDMismatch reports a fragment whose [preset] id disagrees with its
	// FILE STEM. Both are the identity — the stem is what a reviewer reads in a
	// diff, the key is what the loader indexes — and one theme cannot have two
	// names.
	ErrPresetIDMismatch = errors.New("theme: preset id does not match its file name")
	// ErrPresetAxisDir reports a fragment whose declared kind does not match the
	// directory it lives in. The directory IS the axis; a style fragment under
	// layout/ would be a partition violation nobody reading the tree could see.
	ErrPresetAxisDir = errors.New("theme: preset kind does not match its directory")
	// ErrPresetFragmentSize reports a fragment past PresetFragmentByteCap.
	ErrPresetFragmentSize = errors.New("theme: preset fragment is larger than the fragment cap")
	// ErrPresetUnknown reports a lookup for an id the library does not hold. It
	// is what the resolver returns for a theme naming a preset from a NEWER
	// build, and callers treat it as "no tier", never as "refuse the theme".
	ErrPresetUnknown = errors.New("theme: no such preset")
)

// PresetLibrary is the parsed, ordered, immutable fragment set.
//
// Order is BY ID, ascending, and it is the gallery's row/column order. Sorted
// rather than left in fs.ReadDir order because a directory listing is the
// filesystem's opinion and the gallery's layout must not be.
type PresetLibrary struct {
	layouts []*Preset
	styles  []*Preset
	byID    [PresetKindCount]map[string]*Preset
}

var (
	shippedLibOnce sync.Once
	shippedLib     *PresetLibrary
	shippedLibErr  error
)

// ShippedPresets returns the library embedded in this binary.
//
// A FAILURE IS A BUILD BUG, NOT A USER'S PROBLEM: the fragments ship inside the
// executable, so nothing a user does can break them, and every one of them is
// parsed by TestEveryEmbeddedPresetParses before the binary exists. The error is
// still returned rather than panicked on, because a panic on the theme-apply
// goroutine would take the client down over decoration.
//
// COLD PATH ONLY (hard rule 2): first call parses 35 INI files.
func ShippedPresets() (*PresetLibrary, error) {
	shippedLibOnce.Do(func() {
		shippedLib, shippedLibErr = LoadPresetLibrary(presets.FS)
	})
	return shippedLib, shippedLibErr
}

// LoadPresetLibrary parses an axis tree out of any fs.FS.
//
// CONSUMER-DEFINED INTERFACE (hard rule 11, interface segregation): it takes
// fs.FS rather than *embed.FS so the gates can drive it from a hand-built
// fstest.MapFS — which is the only way to test what happens to a DUPLICATE id or
// an OVER-CAP directory, neither of which the shipped tree may ever contain.
func LoadPresetLibrary(src fs.FS) (*PresetLibrary, error) {
	lib := &PresetLibrary{}
	for i := range lib.byID {
		lib.byID[i] = map[string]*Preset{}
	}
	axes := [PresetKindCount]struct {
		cap  int
		into *[]*Preset
	}{
		PresetLayout: {PresetLayoutCap, &lib.layouts},
		PresetStyle:  {PresetStyleCap, &lib.styles},
	}
	for kind, axis := range axes {
		dir := presetAxisDir(PresetKind(kind))
		entries, err := fs.ReadDir(src, dir)
		if err != nil {
			return nil, fmt.Errorf("theme: preset dir %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), presets.FileExt) {
				continue
			}
			p, err := loadOnePreset(src, dir, e.Name())
			if err != nil {
				return nil, err
			}
			if PresetKind(kind) != p.Kind {
				return nil, fmt.Errorf("%w: %s/%s declares kind = %s",
					ErrPresetAxisDir, dir, e.Name(), p.Kind)
			}
			if _, dup := lib.byID[kind][p.ID]; dup {
				return nil, fmt.Errorf("%w: %s/%s", ErrPresetDuplicate, dir, e.Name())
			}
			if len(*axis.into) >= axis.cap {
				return nil, fmt.Errorf("%w: %s holds more than %d fragments",
					ErrPresetCap, dir, axis.cap)
			}
			lib.byID[kind][p.ID] = p
			*axis.into = append(*axis.into, p)
		}
		sort.Slice(*axis.into, func(i, j int) bool { return (*axis.into)[i].ID < (*axis.into)[j].ID })
	}
	return lib, nil
}

// presetAxisDir is THE mapping from an axis to the directory its fragments live
// in, and it is a function rather than a pair of literals at each call site
// because every gate that goes back to a fragment's BYTES has to name the file
// the loader read. It was spelled out three times before this; the copy that
// mattered was a corpus lint's, where editing the pair down to one entry quietly
// halved what got linted. Unknown kinds return "", so a caller outside the enum
// gets an empty path (and a read error) rather than a plausible-looking one.
func presetAxisDir(k PresetKind) string {
	switch k {
	case PresetLayout:
		return presets.LayoutDir
	case PresetStyle:
		return presets.StyleDir
	}
	return ""
}

// presetFragmentPath is where one fragment ships inside the embedded tree. The
// id IS the file stem (loadOnePreset refuses any fragment where it is not), so
// the library's own ids are enough to name every shipped file.
func presetFragmentPath(k PresetKind, id string) string {
	return path.Join(presetAxisDir(k), id+presets.FileExt)
}

// loadOnePreset reads and parses one fragment, checking the stem/id agreement.
func loadOnePreset(src fs.FS, dir, name string) (*Preset, error) {
	raw, err := fs.ReadFile(src, path.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("theme: preset %s/%s: %w", dir, name, err)
	}
	// SIZE FIRST, before a byte is parsed — the same order ParseINIDoc uses, and for
	// the same reason: measuring is free and parsing is not.
	if len(raw) > PresetFragmentByteCap {
		return nil, fmt.Errorf("%w: %s/%s is %d bytes, cap is %d",
			ErrPresetFragmentSize, dir, name, len(raw), PresetFragmentByteCap)
	}
	p, err := ParsePreset(raw)
	if err != nil {
		return nil, fmt.Errorf("theme: preset %s/%s: %w", dir, name, err)
	}
	if stem := strings.TrimSuffix(name, presets.FileExt); stem != p.ID {
		return nil, fmt.Errorf("%w: %s/%s declares id = %s", ErrPresetIDMismatch, dir, name, p.ID)
	}
	return p, nil
}

// Layouts / Styles return the ordered fragments of one axis.
//
// THE SLICE IS SHARED, NOT COPIED, and the type is what keeps that safe: a
// caller can reorder its own copy of the header but cannot reach a *Preset's
// contents without going through the merge, which deep-copies. Copying here
// would allocate 35 pointers on every gallery frame for no property the caller
// does not already have. Nil-safe, so a library that failed to load renders an
// empty gallery instead of crashing one.
func (l *PresetLibrary) Layouts() []*Preset { return l.axis(PresetLayout) }

// Styles is Layouts' twin for the other axis.
func (l *PresetLibrary) Styles() []*Preset { return l.axis(PresetStyle) }

// axis resolves ONE axis by kind, and every other accessor here goes through it.
// Layouts, Styles and Count used to answer "which slice is this axis" three
// times; a by-kind caller — the corpus gates walk `k < PresetKindCount` rather
// than naming the two axes, so that an axis cannot be dropped by editing a list
// — would have been a fourth. Nil-safe and out-of-range-safe for the same reason
// Layouts is: a library that failed to load renders an empty gallery.
func (l *PresetLibrary) axis(k PresetKind) []*Preset {
	if l == nil {
		return nil
	}
	switch k {
	case PresetLayout:
		return l.layouts
	case PresetStyle:
		return l.styles
	}
	return nil
}

// Count reports how many fragments one axis holds.
func (l *PresetLibrary) Count(k PresetKind) int { return len(l.axis(k)) }

// Combinations is the gallery's cell count — the number this wave's headline is.
func (l *PresetLibrary) Combinations() int {
	return l.Count(PresetLayout) * l.Count(PresetStyle)
}

// Find resolves an id on one axis. An EMPTY id is "no tier on this axis" and
// returns (nil, nil): a theme that names only a style is a legal theme, and
// forcing the caller to distinguish "" from a typo at every site is how a
// missing tier turns into an error dialog.
//
// An UNKNOWN id returns ErrPresetUnknown, which callers surface as a note and
// never as a refusal — it is exactly what a theme authored on a newer build
// looks like here (format rule 1).
func (l *PresetLibrary) Find(k PresetKind, id string) (*Preset, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	if l == nil || k >= PresetKindCount {
		return nil, fmt.Errorf("%w: %s/%s", ErrPresetUnknown, k, id)
	}
	p, ok := l.byID[k][id]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrPresetUnknown, k, id)
	}
	return p, nil
}
