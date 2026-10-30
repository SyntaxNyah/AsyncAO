package theme

// The shipped preset library, parsed once lazily; never on a draw path (hard rule 2).

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
	// PresetLayoutCap and PresetStyleCap bound each axis; the loader refuses past them, never truncating.
	PresetLayoutCap = 32
	PresetStyleCap  = 24
	// PresetComboCap is the gallery's size, an alias of the product so the bounds cannot disagree.
	PresetComboCap = PresetLayoutCap * PresetStyleCap

	// PresetFragmentByteCap bounds a fragment we ship, far below IniDocByteCap, so growth gets reviewed.
	PresetFragmentByteCap = 64 << 10
)

var (
	ErrPresetCap = errors.New("theme: preset library exceeds a named cap")
	// ErrPresetIDMismatch reports a [preset] id differing from its file stem; both are the identity.
	ErrPresetIDMismatch = errors.New("theme: preset id does not match its file name")
	// ErrPresetAxisDir reports a declared kind not matching its directory, which is the axis.
	ErrPresetAxisDir = errors.New("theme: preset kind does not match its directory")
	// ErrPresetFragmentSize reports a fragment past PresetFragmentByteCap.
	ErrPresetFragmentSize = errors.New("theme: preset fragment is larger than the fragment cap")
	// ErrPresetUnknown reports an id not held; callers treat it as no tier, never refusing the theme.
	ErrPresetUnknown = errors.New("theme: no such preset")
)

// PresetLibrary is the immutable fragment set, sorted by id for gallery order, independent of directory listing order.
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

// ShippedPresets returns the embedded library; cold path only (hard rule 2), as the first call parses every fragment.
func ShippedPresets() (*PresetLibrary, error) {
	shippedLibOnce.Do(func() {
		shippedLib, shippedLibErr = LoadPresetLibrary(presets.FS)
	})
	return shippedLib, shippedLibErr
}

// LoadPresetLibrary takes an fs.FS, so fstest.MapFS can build trees the shipped one cannot hold.
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

// presetAxisDir maps an axis to its directory; an unknown kind returns "", which fails to read.
func presetAxisDir(k PresetKind) string {
	switch k {
	case PresetLayout:
		return presets.LayoutDir
	case PresetStyle:
		return presets.StyleDir
	}
	return ""
}

func loadOnePreset(src fs.FS, dir, name string) (*Preset, error) {
	raw, err := fs.ReadFile(src, path.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("theme: preset %s/%s: %w", dir, name, err)
	}
	// Size first: measuring is free and parsing is not.
	if len(raw) > PresetFragmentByteCap {
		return nil, fmt.Errorf("%w: %s/%s is %d bytes, cap is %d",
			ErrPresetFragmentSize, dir, name, len(raw), PresetFragmentByteCap)
	}
	p, err := ParsePreset(raw)
	if err != nil {
		return nil, fmt.Errorf("theme: preset %s/%s: %w", dir, name, err)
	}
	// Stem/id equality is also the duplicate guard, as a directory cannot hold two equal file names.
	if stem := strings.TrimSuffix(name, presets.FileExt); stem != p.ID {
		return nil, fmt.Errorf("%w: %s/%s declares id = %s", ErrPresetIDMismatch, dir, name, p.ID)
	}
	return p, nil
}

// Layouts returns one axis's fragments; shared, not copied, to avoid allocating per gallery frame.
func (l *PresetLibrary) Layouts() []*Preset { return l.axis(PresetLayout) }

func (l *PresetLibrary) Styles() []*Preset { return l.axis(PresetStyle) }

// axis resolves one axis by kind; nil-safe and range-safe, so a failed load renders an empty gallery.
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

func (l *PresetLibrary) Count(k PresetKind) int { return len(l.axis(k)) }

func (l *PresetLibrary) Combinations() int {
	return l.Count(PresetLayout) * l.Count(PresetStyle)
}

// Find resolves an id: empty gives (nil, nil), no tier; unknown gives ErrPresetUnknown, never a refusal.
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
