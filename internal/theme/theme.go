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

// FontSpec is one courtroom_fonts.ini entry (AO2-Client Courtroom::set_font,
// courtroom.cpp:1212 — "<id>" size, "<id>_font" family, "<id>_bold",
// "<id>_sharp", "<id>_color").
type FontSpec struct {
	Size  int
	Bold  bool
	Color RGB
	Font  string // optional "<name>_font" family (AO2); used to find a bundled .ttf
	// Sharp mirrors "<id>_sharp = 1" — AO2 renders that element WITHOUT
	// antialiasing (courtroom.cpp:1237, QFont::NoAntialias). Parsed for parity;
	// AsyncAO's label cache has no solid-render dimension yet, so it is unused.
	Sharp bool
	// SizeSet reports that the theme actually declared "<id>" — Size otherwise
	// carries the parser's default and must NOT override the client's own scale.
	// ColorSet is the same signal for "<id>_color". HasFont conflates the two,
	// so it cannot answer either question on its own.
	SizeSet  bool
	ColorSet bool
}

// FontElements are the courtroom_fonts.ini identifiers AsyncAO honours, drawn
// from AO2-Client Courtroom::set_fonts (courtroom.cpp:1195-1207).
//
// Order is load-bearing but is NOT AO2's call order: internal/ui indexes its
// resolved per-element table by position in this list, so the pairing that must
// hold is with the themeFontElem enum there (pinned by TestThemeFontElemOrder).
// New identifiers are APPENDED for exactly that reason — inserting one at AO2's
// position would silently hand every later element another's family and size.
//
// Still unhonoured, each for its own reason: "clock_N" has no themed clock
// widget, and "evidence_name" / "evidence_image_name" / "evidence_description"
// are set from a different translation unit (evidence.cpp:94-96) onto a surface
// AsyncAO does not dress yet.
var FontElements = [...]string{
	"showname",
	"message",
	"ic_chatlog",
	"server_chatlog",
	"music_list",
	"music_name",
	"area_list",
	// debug_log dresses the panel drawn at the ms_chatlog RECT. It was skipped
	// while AsyncAO's debug log was chrome-only; the themed debug panel gave it a
	// surface inside the design canvas, so it needs its own font like every other
	// widget in there. AO2: set_font(ui_debug_log, "", "debug_log", p_char)
	// (courtroom.cpp:1198) — note this is NOT server_chatlog's font, which is the
	// neighbouring key it is easiest to mistake it for, and NOT "ms_chatlog":
	// set_fonts never reads that identifier, it survives only as the RECT key
	// (courtroom.cpp:831).
	"debug_log",
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
	// FLAT TIER (#21): themes ship as a zip holding one folder, and users drop
	// that folder straight onto the window. AO2 never sees this shape — its root
	// is always base/, so get_theme_path always joins "themes/" — but a streaming
	// client's content root is whatever the user pointed at. Probing <root>/<name>
	// AFTER every <root>/themes/<name> means a real base can never lose to it:
	// loadFirstINI takes the FIRST hit per key and FindAsset walks dirs in order.
	// A well-formed base simply misses here (the dir does not exist) and pays two
	// os.Open per INI on the theme-apply goroutine — never on a render, decode or
	// resolver path (hard rule 2).
	for _, root := range roots {
		if root == "" {
			continue
		}
		t.dirs = append(t.dirs, filepath.Join(root, name))
	}
	// No flat tier for the DEFAULT fallback, deliberately: a bare-folder import
	// supplies exactly one theme, and synthesising <root>/default would let a
	// stray folder named "default" sitting beside the drop hijack every key the
	// real theme leaves unresolved.
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
			spec.Size, spec.SizeSet = size, true
		}
	}
	if raw, ok := t.fonts.Get(name + "_color"); ok {
		if c, ok := parseRGB(raw); ok {
			spec.Color, spec.ColorSet = c, true
		}
	}
	if raw, ok := t.fonts.Get(name + "_bold"); ok {
		spec.Bold = raw == "1"
	}
	if raw, ok := t.fonts.Get(name + "_sharp"); ok {
		spec.Sharp = raw == "1"
	}
	if raw, ok := t.fonts.Get(name + "_font"); ok {
		spec.Font = strings.TrimSpace(raw)
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

// FontFile returns the path to the font file (.ttf/.otf) the ACTIVE theme wants
// its courtroom text drawn in, so a streaming client can honour a theme's font
// (#6/#39, Crystalwarrior). Resolution order:
//
//  1. A file matching the "message_font" family bundled in the theme's own dir.
//  2. That same declared family under the content root's base "fonts/" folder —
//     AO themes reference fonts by NAME expecting them in base/fonts/, so this is
//     where an imported theme's font actually lives (#39). The declared family is
//     required here: base/fonts/ holds many faces, so we never grab an arbitrary
//     one.
//  3. Any font file bundled in the theme's own dir (a theme that ships one .ttf
//     but declares no family — the original #6 case).
//
// The default-theme fallback dirs are skipped — only the active theme may impose
// a font. "" = none found (keep the client font). FontFileFor / FontFiles resolve
// the OTHER elements the same way (#39).
// NamesAnyFontFamily reports whether courtroom_fonts.ini declares a "<id>_font"
// family for ANY element. A theme that does is dressing its elements
// individually, so nothing may impose a face on the whole client on its behalf.
func (t *Theme) NamesAnyFontFamily() bool {
	for _, id := range FontElements {
		if t.Font(id).Font != "" {
			return true
		}
	}
	return false
}

func (t *Theme) FontFile() string {
	// A theme that NAMES families dresses its elements one by one (FontFiles, #39)
	// and must not also impose a face on the whole client. This used to return the
	// declared MESSAGE family first, which predates per-element fonts: it meant
	// aceattorney2x's chatbox family became the app font — menus, lobby, settings
	// and all — while the per-element table was separately applying the same face
	// to the message anyway. The client-wide install is now only for the case that
	// motivated it: a theme that ships one .ttf and declares nothing.
	if t.NamesAnyFontFamily() {
		return ""
	}
	// (3): any font file bundled in the active theme's own dir — a theme that
	// ships one .ttf but declares no family (the original #6 case). Deliberately
	// NOT recursive: a themes/<name>/fonts/ folder holding a dozen faces must not
	// hand an arbitrary one to the whole client.
	for _, dir := range t.dirs {
		if filepath.Base(dir) != t.Name {
			continue // skip the default-theme fallback dirs
		}
		if f := firstFontIn(dir); f != "" {
			return f
		}
	}
	return ""
}

// fontScanMaxDepth bounds the recursive font walks. AO2-Client registers
// <base>/fonts with an UNBOUNDED QDirIterator::Subdirectories (main.cpp:44-55);
// a named cap keeps hard rule 4 (nothing unbounded) while covering every real
// layout seen in the wild: <theme>/x.ttf, <theme>/fonts/x.otf,
// <base>/fonts/<family>/x.ttf.
const fontScanMaxDepth = 3

// fontScanMaxFiles caps one resolution's total file visits, so a user who points
// the theme root at a whole drive can't stall the off-thread theme-apply. 4096 is
// far past any real theme pack (C:\Windows\Fonts itself holds ~400 files).
const fontScanMaxFiles = 4096

// systemFontAliases maps a declared family to its Windows font FILE stem where
// normalizeFontKey can't bridge the two ("Times New Roman" ships as times.ttf).
// Families whose stem already normalizes to the family (Arial, Verdana, Tahoma,
// Georgia, Impact, Calibri, Segoe UI, Consolas) need no entry. AO2 gets these for
// free from Qt's system font database (get_qfont, courtroom.cpp:1263); a
// streaming client has to name them.
var systemFontAliases = map[string]string{
	"timesnewroman":        "times",
	"couriernew":           "cour",
	"comicsansms":          "comic",
	"trebuchetms":          "trebuc",
	"franklingothicmedium": "framd",
	"palatinolinotype":     "pala",
	"lucidaconsole":        "lucon",
	"msshelldlg2":          "tahoma",
	"msgothic":             "msgothic",
	"microsoftsansserif":   "micross",
}

// FontFileFor resolves ONE courtroom_fonts.ini element's declared family
// ("<id>_font") to a font file on disk. The ladder mirrors what AO2-Client gets
// from Qt — main.cpp:44-55 registers <base>/fonts RECURSIVELY into the
// application font database, and QFont(family) then resolves against that plus
// the system fonts:
//
//  1. the ACTIVE theme's own dir, recursively (DR Theme/igiari.ttf,
//     Lymantriina/fonts/IBMPlexSerif-Regular.otf, 3DS Widescreen/fonts/*)
//  2. <root>/fonts for every content root, recursively
//  3. sysDirs (the OS font folders — pass nil to skip), stem match plus
//     systemFontAliases, which is what makes a theme declaring plain "Arial"
//     (DRRetribution, KFO qHD) resolve at all
//
// "" when the element declares no family or nothing matches.
func (t *Theme) FontFileFor(element string, sysDirs []string) string {
	return t.FontFiles(sysDirs)[element]
}

// FontFiles resolves every FontElements entry in ONE bounded disk walk, so a
// theme apply pays a single scan instead of one per element. Absent keys mean
// "no family declared, or nothing matched". Off-thread only (hard rule 2).
func (t *Theme) FontFiles(sysDirs []string) map[string]string {
	// Collect the declared families first: a theme that names none (the common
	// case, and every theme before AO2 2.8) must not touch the disk at all.
	want := make(map[string]string, len(FontElements))
	for _, id := range FontElements {
		if fam := strings.TrimSpace(t.Font(id).Font); fam != "" {
			want[id] = fam
		}
	}
	out := map[string]string{}
	if len(want) == 0 {
		return out
	}
	idx := &fontIndex{budget: fontScanMaxFiles}
	// (1) the ACTIVE theme's own directory tree.
	for _, dir := range t.dirs {
		if filepath.Base(dir) != t.Name {
			continue // skip the default-theme fallback dirs: only the active theme imposes fonts
		}
		idx.scan(dir, fontScanMaxDepth)
	}
	// (2) <root>/fonts — a theme dir is "<root>/themes/<name>", so the AO base
	// "fonts/" sibling is "<root>/fonts/". Deduped across roots.
	seen := make(map[string]struct{}, len(t.dirs))
	for _, dir := range t.dirs {
		fontsDir := filepath.Join(filepath.Dir(filepath.Dir(dir)), "fonts")
		if _, dup := seen[fontsDir]; dup {
			continue
		}
		seen[fontsDir] = struct{}{}
		idx.scan(fontsDir, fontScanMaxDepth)
	}
	resolve := func() int {
		missing := 0
		for id, fam := range want {
			if out[id] != "" {
				continue
			}
			if p := idx.match(fam); p != "" {
				out[id] = p
			} else {
				missing++
			}
		}
		return missing
	}
	if resolve() == 0 {
		return out
	}
	// (3) the OS font folders, scanned only when something is still unresolved —
	// so a fully self-contained theme never reads C:\Windows\Fonts.
	for _, dir := range sysDirs {
		idx.scan(dir, 1) // flat: the system font folder has no per-family subdirs
	}
	resolve()
	return out
}

// fontEntry is one indexed font file: its NORMALIZED file stem (the comparison
// key) and the full path.
type fontEntry struct{ stem, path string }

// fontIndex is the flat, priority-ordered list of candidate font files built by
// one resolution pass. budget is the shared remaining file allowance
// (fontScanMaxFiles) so the whole walk — every tier, every root — is bounded.
type fontIndex struct {
	ents   []fontEntry
	budget int
}

// scan appends dir's .ttf/.otf files to the index, then recurses into its
// sub-directories while depth allows. FILES BEFORE DIRS so a font at the theme
// root outranks one buried in a subfolder at the same tier.
func (idx *fontIndex) scan(dir string, depth int) {
	if depth <= 0 || idx.budget <= 0 || dir == "" {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if idx.budget <= 0 {
			return
		}
		idx.budget--
		name := e.Name()
		switch strings.ToLower(filepath.Ext(name)) {
		case ".ttf", ".otf", ".ttc":
		default:
			continue
		}
		idx.ents = append(idx.ents, fontEntry{
			stem: normalizeFontKey(strings.TrimSuffix(name, filepath.Ext(name))),
			path: filepath.Join(dir, name),
		})
	}
	for _, e := range ents {
		if e.IsDir() {
			idx.scan(filepath.Join(dir, e.Name()), depth-1)
		}
	}
}

// match resolves a declared family to an indexed path: an EXACT normalized stem
// match first (so "Igiari" can't lose to "igiari-cyrillic.ttf"), then a stem that
// CONTAINS the family ("IBM Plex Serif" → "IBMPlexSerif-Regular.otf"), then the
// same two passes through systemFontAliases. "" when nothing matches.
func (idx *fontIndex) match(family string) string {
	want := normalizeFontKey(family)
	if want == "" {
		return ""
	}
	cands := [2]string{want, systemFontAliases[want]}
	for _, w := range cands {
		if w == "" {
			continue
		}
		for _, e := range idx.ents {
			if e.stem == w {
				return e.path
			}
		}
		for _, e := range idx.ents {
			if strings.Contains(e.stem, w) {
				return e.path
			}
		}
	}
	return ""
}

// firstFontIn returns any .ttf/.otf directly inside dir (no recursion) — the
// family-less fallback for a theme that simply bundles one font file.
func firstFontIn(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".ttf", ".otf":
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// normalizeFontKey folds a font family or file stem to a comparison key:
// lowercased with spaces, underscores and hyphens dropped, so the many spellings
// of one family ("Igiari", "igiari", "ig-iari") collapse together.
func normalizeFontKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case ' ', '_', '-':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
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
		if path, ok := FindAssetIn(dir, stem, exts); ok {
			return path, true
		}
	}
	return "", false
}

// FindAssetIn is FindAsset restricted to ONE directory — the same extension
// probe, without the fall-through to the next theme dir (and therefore without
// the fall-through to the bundled default theme).
//
// It exists for the variants AO2 derives from an image it has ALREADY resolved
// rather than looking up by name. AOImage remembers the resolved path of the
// picture it is showing (AO2-Client aoimage.cpp m_file_name) and the chatbox's
// widen-and-swap ladder builds its med/big candidates by string-appending to
// that path minus its extension (courtroom.cpp:3358-3368) — so those two files
// can only ever come out of the directory the base skin came out of. A theme
// that ships chatmed.png but no chat.png does not get its med art in AO2, and
// must not get it here either: pairing one theme's variant with another theme's
// base would stretch mismatched art into the same box.
func FindAssetIn(dir, stem string, exts []string) (string, bool) {
	for _, ext := range exts {
		path := filepath.Join(dir, stem+ext)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
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

// DesignKeys returns every ROOT-SECTION courtroom_design.ini key the theme
// resolved (lowercased, values raw). Keys under a [section] are excluded, which
// is what the caller wants: AO2 reads courtroom_design.ini's widget rects from
// the root only.
//
// Diagnostics only — it allocates a map, so it must never be called from a draw
// path. The one caller runs on the theme-apply goroutine.
func (t *Theme) DesignKeys() map[string]string { return t.design.SectionKeys("") }

// atoiTrim discards the parse error ON PURPOSE — that is AO2 parity, not
// sloppiness. Every design/font tuple in AO2 is read with QString::toInt(),
// which returns 0 for anything it cannot parse (verified against Qt 6.5.3:
// "8SS" → 0) and which AO2 then uses unchecked
// (AO2-Client text_file_functions.cpp:236-239 for rects, :212-213 for pairs).
// Reporting the error here and rejecting the whole tuple would make AsyncAO
// STRICTER than the client every theme was authored against, silently dropping
// rects that render fine in AO2. Callers that need "is this usable" ask
// Rect.Valid(), exactly as AO2 checks width/height before laying a widget out.
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
