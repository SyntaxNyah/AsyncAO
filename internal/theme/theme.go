package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// DesignFileName matches AO2-Client's courtroom design INI.
	DesignFileName = "courtroom_design.ini"
	// FontsFileName matches AO2-Client's courtroom fonts INI.
	FontsFileName = "courtroom_fonts.ini"
	// SoundsFileName matches AO2-Client's courtroom sounds INI.
	SoundsFileName = "courtroom_sounds.ini"
	// DefaultThemeName is the theme every lookup falls back to, exactly
	// like AO2-Client's default_theme.
	DefaultThemeName = "default"
	// ThemesDirName is the folder (under each content root) holding themes.
	ThemesDirName = "themes"
	// PenaltyFileName is the HP-bar config (AO2-Client get_penalty_value
	// reads "penalty/penalty.ini" from the theme).
	PenaltyFileName = "penalty/penalty.ini"

	rectComponentCount = 4
	tupleSeparator     = ","
)

// Rect is an element position from courtroom_design.ini: "x, y, w, h".
type Rect struct {
	X, Y, W, H int
}

// Valid reports whether the element carried usable dimensions.
func (r Rect) Valid() bool { return r.W > 0 && r.H > 0 }

// FontSpec is one courtroom_fonts.ini entry.
type FontSpec struct {
	Size  int
	Bold  bool
	Color RGB
}

// RGB is a theme color tuple.
type RGB struct{ R, G, B uint8 }

// Theme resolves AO2 theme assets with the AO2-Client lookup order:
// the active theme first, then the default theme. Images keep their original
// theme file names (chatbox.png, chat_arrow.png, holdit_bubble.*, ...).
type Theme struct {
	// Name is the active theme's directory name.
	Name string
	// dirs are the candidate theme directories in priority order:
	// <root>/themes/<name>, then <root>/themes/default for every root.
	dirs    []string
	design  *INI
	fonts   *INI
	sounds  *INI
	penalty *INI
}

// Load opens the named theme across the given content roots (e.g. the
// user config dir and the executable's directory). Missing INIs are
// tolerated; lookups then simply miss into defaults.
func Load(name string, roots []string) (*Theme, error) {
	if name == "" {
		name = DefaultThemeName
	}
	t := &Theme{Name: name}
	for _, root := range roots {
		if root == "" {
			continue
		}
		t.dirs = append(t.dirs, filepath.Join(root, ThemesDirName, name))
	}
	for _, root := range roots {
		if root == "" || name == DefaultThemeName {
			continue
		}
		t.dirs = append(t.dirs, filepath.Join(root, ThemesDirName, DefaultThemeName))
	}
	if len(t.dirs) == 0 {
		return nil, fmt.Errorf("theme: no content roots supplied")
	}

	t.design = t.loadFirstINI(DesignFileName)
	t.fonts = t.loadFirstINI(FontsFileName)
	t.sounds = t.loadFirstINI(SoundsFileName)
	t.penalty = t.loadFirstINI(filepath.FromSlash(PenaltyFileName))
	return t, nil
}

// loadFirstINI merges the named INI across dirs, FIRST hit per key winning
// (active theme overrides default).
func (t *Theme) loadFirstINI(fileName string) *INI {
	merged := &INI{values: map[string]string{}}
	for _, dir := range t.dirs {
		ini, err := LoadINI(filepath.Join(dir, fileName))
		if err != nil {
			continue
		}
		for k, v := range ini.values {
			if _, exists := merged.values[k]; !exists {
				merged.values[k] = v
			}
		}
	}
	return merged
}

// ElementRect returns the design rect for an element name (AO2-Client
// get_element_dimensions): "name = x, y, w, h".
func (t *Theme) ElementRect(name string) (Rect, bool) {
	raw, ok := t.design.Get(name)
	if !ok {
		return Rect{}, false
	}
	parts := strings.Split(raw, tupleSeparator)
	if len(parts) < rectComponentCount {
		return Rect{}, false
	}
	return Rect{
		X: atoiTrim(parts[0]),
		Y: atoiTrim(parts[1]),
		W: atoiTrim(parts[2]),
		H: atoiTrim(parts[3]),
	}, true
}

// DesignValue exposes a raw design key (e.g. "music_display_x" extras).
func (t *Theme) DesignValue(key string) (string, bool) {
	return t.design.Get(key)
}

// Font returns the font spec for an element: AO2 stores "<name> = <size>"
// plus optional "<name>_color = r, g, b" and "<name>_bold = 1".
func (t *Theme) Font(name string) FontSpec {
	const defaultFontSize = 12
	spec := FontSpec{Size: defaultFontSize, Color: RGB{255, 255, 255}}
	if raw, ok := t.fonts.Get(name); ok {
		if size := atoiTrim(raw); size > 0 {
			spec.Size = size
		}
	}
	if raw, ok := t.fonts.Get(name + "_color"); ok {
		if c, ok := parseRGB(raw); ok {
			spec.Color = c
		}
	}
	if raw, ok := t.fonts.Get(name + "_bold"); ok {
		spec.Bold = raw == "1"
	}
	return spec
}

// HasFont reports whether the theme's fonts INI defines the element at
// all (size or color) — callers keep their own defaults otherwise.
func (t *Theme) HasFont(name string) bool {
	if _, ok := t.fonts.Get(name); ok {
		return true
	}
	_, ok := t.fonts.Get(name + "_color")
	return ok
}

// SoundName returns the courtroom_sounds.ini entry (e.g. "word_call").
func (t *Theme) SoundName(key string) (string, bool) {
	return t.sounds.Get(key)
}

// PenaltyValue returns a penalty/penalty.ini entry (hp_increased_sfx,
// hp_decreased_sfx, ... — AO2-Client get_penalty_value).
func (t *Theme) PenaltyValue(key string) (string, bool) {
	return t.penalty.Get(key)
}

// FindAsset locates a theme file by stem, probing the given extensions in
// order across the theme directories (active theme first). Returns the
// first existing path.
func (t *Theme) FindAsset(stem string, exts []string) (string, bool) {
	for _, dir := range t.dirs {
		for _, ext := range exts {
			path := filepath.Join(dir, stem+ext)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, true
			}
		}
	}
	return "", false
}

// Dirs exposes the probe order (diagnostics / docs tooling).
func (t *Theme) Dirs() []string {
	out := make([]string, len(t.dirs))
	copy(out, t.dirs)
	return out
}

// KeyCount totals the keys loaded across the three INIs — 0 means the
// theme directories contributed nothing (diagnostics).
func (t *Theme) KeyCount() int {
	return t.design.Len() + t.fonts.Len() + t.sounds.Len()
}

func atoiTrim(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

func parseRGB(raw string) (RGB, bool) {
	parts := strings.Split(raw, tupleSeparator)
	if len(parts) < 3 {
		return RGB{}, false
	}
	return RGB{
		R: uint8(atoiTrim(parts[0])),
		G: uint8(atoiTrim(parts[1])),
		B: uint8(atoiTrim(parts[2])),
	}, true
}
