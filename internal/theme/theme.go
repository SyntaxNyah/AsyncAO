package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

const (
	DesignFileName = "courtroom_design.ini"
	// FontsFileName matches AO2-Client's courtroom fonts INI.
	FontsFileName = "courtroom_fonts.ini"
	// SoundsFileName matches AO2-Client's courtroom sounds INI.
	SoundsFileName = "courtroom_sounds.ini"
	// DefaultThemeName is the theme every lookup falls back to, like AO2-Client's default_theme.
	DefaultThemeName = "default"
	// ThemesDirName is the folder (under each content root) holding themes.
	ThemesDirName = "themes"
	// PenaltyFileName is the HP-bar config (AO2-Client get_penalty_value).
	PenaltyFileName = "penalty/penalty.ini"

	rectComponentCount = 4
	rgbFields          = 3 // "r, g, b"; a fourth field is alpha
	tupleSeparator     = ","
)

// Rect is an element position from courtroom_design.ini: "x, y, w, h".
type Rect struct {
	X, Y, W, H int
}

func (r Rect) Valid() bool { return r.W > 0 && r.H > 0 }

// FontSpec is one courtroom_fonts.ini entry (AO2-Client Courtroom::set_font, courtroom.cpp:1212).
type FontSpec struct {
	Size  int
	Bold  bool
	Color RGB
	Font  string // optional "<name>_font" family (AO2); used to find a bundled .ttf
	// Sharp mirrors "<id>_sharp = 1", no antialiasing; only the literal "1" counts (courtroom.cpp:1237).
	Sharp bool
	// SizeSet and ColorSet report declared keys; otherwise Size must not override the client's scale.
	SizeSet  bool
	ColorSet bool
}

// FontElements are the fonts INI ids AsyncAO honours (courtroom.cpp:1195-1207).
// internal/ui indexes its table by position, so the order must match its themeFontElem enum.
// Not honoured: "clock_N" (no widget) and evidence_* (evidence.cpp:94-96, a surface not dressed).
var FontElements = [...]string{
	"showname",
	"message",
	"ic_chatlog",
	"server_chatlog",
	"music_list",
	"music_name",
	"area_list",
	// debug_log dresses the panel at the ms_chatlog rect (courtroom.cpp:1198, courtroom.cpp:831).
	// It is not server_chatlog's font and not "ms_chatlog".
	"debug_log",

	// AsyncAO's own panels, below AO2's set: parsing, resolution and Settings treat them identically.
	"player_list",
	"notes",
	"friends",
}

type RGB struct{ R, G, B uint8 }

// Theme resolves AO2 theme assets in AO2-Client's order: the active theme, then the default theme.
type Theme struct {
	Name string
	// Sub is the sanitized AO2 subtheme folder ("" = none), a higher-priority directory inside the same theme.
	Sub string
	// dirs in priority order: <root>/themes/<name>/<sub>, <root>/themes/<name>, <root>/themes/default.
	dirs    []string
	design  *INI
	fonts   *INI
	sounds  *INI
	penalty *INI

	// ownDirN counts leading dirs of this theme; the rest are the per-key default fallback.
	ownDirN int
	// sidecar is read whole from this theme's first dir having one, never merged or inherited.
	// Either would put decoration on rects the author's canvas lacks.
	sidecar    *Sidecar
	sidecarDir string
	// sidecarErr is a sidecar that failed to parse; it never fails Load, as such a theme renders like stock AO2.
	sidecarErr error
}

// SubthemeNameLen bounds a name that joins a filesystem path from a hand-editable file (hard rule 4).
const SubthemeNameLen = 48

// SanitizeSubtheme reduces a subtheme to one safe folder name (path_functions.cpp:192), or "".
// It refuses rather than cleans, as an impossible value means no subtheme.
func SanitizeSubtheme(sub string) string {
	sub = strings.TrimSpace(sub)
	if sub == "" || safepath.UnsafeRel(sub) {
		return ""
	}
	// One segment: UnsafeRel allows "night/deep", which would be two folders.
	if strings.ContainsAny(sub, `/\`) {
		return ""
	}
	// "." and dotfiles do not name a subtheme and point a probe back at the theme folder.
	if strings.HasPrefix(sub, ".") {
		return ""
	}
	if len([]rune(sub)) > SubthemeNameLen {
		return ""
	}
	return sub
}

// Load opens the theme across roots, in AO2's order (path_functions.cpp:190-193).
// The default fallback has no subtheme tier (path_functions.cpp:202-205).
func Load(name, sub string, roots []string) (*Theme, error) {
	if name == "" {
		name = DefaultThemeName
	}
	sub = SanitizeSubtheme(sub)
	t := &Theme{Name: name, Sub: sub}
	for _, root := range roots {
		if root == "" {
			continue
		}
		// Per root, not per tier: root order is priority, so a root's subtheme and theme stay together.
		if sub != "" {
			t.dirs = append(t.dirs, filepath.Join(root, ThemesDirName, name, sub))
		}
		t.dirs = append(t.dirs, filepath.Join(root, ThemesDirName, name))
	}
	// Flat tier for a dropped theme folder; probed after every themes/<name>, so a real base wins.
	for _, root := range roots {
		if root == "" {
			continue
		}
		if sub != "" {
			t.dirs = append(t.dirs, filepath.Join(root, name, sub))
		}
		t.dirs = append(t.dirs, filepath.Join(root, name))
	}
	// The boundary between this theme and the default fallback is the sidecar's no-inheritance rule.
	t.ownDirN = len(t.dirs)
	// No flat tier for the default, or a stray "default" folder hijacks unresolved keys.
	// No subtheme tier either, matching AO2 (path_functions.cpp:202-205).
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
	// Own dirs only; a parse failure is recorded and swallowed, as reading never refuses.
	t.sidecar, t.sidecarDir, t.sidecarErr = LoadSidecarIn(t.dirs[:t.ownDirN])
	return t, nil
}

// ownDirs excludes the default-theme fallback.
func (t *Theme) ownDirs() []string { return t.dirs[:t.ownDirN] }

// Sidecar is the parsed asyncao_theme.ini, or nil for a stock AO2 theme.
func (t *Theme) Sidecar() *Sidecar {
	if t == nil {
		return nil
	}
	return t.sidecar
}

// SidecarDir is where the sidecar came from ("" when none), and where [media] paths and the theme's fonts/ resolve.
func (t *Theme) SidecarDir() string {
	if t == nil {
		return ""
	}
	return t.sidecarDir
}

// SidecarErr reports a sidecar that exists but could not be parsed; diagnostics only.
func (t *Theme) SidecarErr() error {
	if t == nil {
		return nil
	}
	return t.sidecarErr
}

// loadFirstINI merges the named INI across dirs, the first hit per key winning.
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

// ElementRect returns the design rect for an element (AO2-Client get_element_dimensions).
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
		X: parse.AtoiOr(parts[0], 0),
		Y: parse.AtoiOr(parts[1], 0),
		W: parse.AtoiOr(parts[2], 0),
		H: parse.AtoiOr(parts[3], 0),
	}, true
}

func (t *Theme) DesignValue(key string) (string, bool) {
	return t.design.Get(key)
}

// Font returns an element's font spec from "<name> = <size>" plus optional "_color" and "_bold" keys.
func (t *Theme) Font(name string) FontSpec {
	const defaultFontSize = 12
	spec := FontSpec{Size: defaultFontSize, Color: RGB{255, 255, 255}}
	if raw, ok := t.fonts.Get(name); ok {
		if size := parse.AtoiOr(raw, 0); size > 0 {
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

// HasFont reports whether the fonts INI defines the element at all; callers keep their own defaults otherwise.
func (t *Theme) HasFont(name string) bool {
	if _, ok := t.fonts.Get(name); ok {
		return true
	}
	_, ok := t.fonts.Get(name + "_color")
	return ok
}

// NamesAnyFontFamily and FontFile use the active theme only: nothing else may impose a font.
func (t *Theme) NamesAnyFontFamily() bool {
	for _, id := range FontElements {
		if t.Font(id).Font != "" {
			return true
		}
	}
	return false
}

func (t *Theme) FontFile() string {
	// A theme that names families dresses elements one by one and must not also impose a face on the whole client.
	if t.NamesAnyFontFamily() {
		return ""
	}
	// A bundled font for a theme that declares no family; not recursive, to avoid an arbitrary face.
	for _, dir := range t.ownDirs() { // this theme's own tiers, subtheme FIRST (ownDirs)
		if f := firstFontIn(dir); f != "" {
			return f
		}
	}
	return ""
}

// fontScanMaxDepth bounds font walks (hard rule 4), unbounded in AO2 (main.cpp:44-55).
const fontScanMaxDepth = 3

// fontScanMaxFiles caps one resolution's file visits, so a theme root set to a whole drive cannot stall theme apply.
const fontScanMaxFiles = 4096

// systemFontAliases maps a family to its Windows file stem; AO2 uses Qt's database (courtroom.cpp:1263).
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

// HasAnyFontFile reports a font file reachable through this theme's dirs; off-thread only, walks the disk.
func (t *Theme) HasAnyFontFile() bool {
	idx := &fontIndex{budget: fontScanMaxFiles}
	for _, dir := range t.ownDirs() { // the active theme's own tree only, as FontFiles does
		idx.scan(dir, fontScanMaxDepth)
		if len(idx.ents) > 0 {
			return true
		}
	}
	seen := make(map[string]struct{}, len(t.dirs))
	for _, dir := range t.dirs {
		fontsDir := filepath.Join(filepath.Dir(filepath.Dir(dir)), "fonts")
		if _, dup := seen[fontsDir]; dup {
			continue
		}
		seen[fontsDir] = struct{}{}
		idx.scan(fontsDir, fontScanMaxDepth)
		if len(idx.ents) > 0 {
			return true
		}
	}
	return false
}

// ResolveFamilies maps family names to font paths, omitting misses; off-thread only (hard rule 2).
func ResolveFamilies(families []string, contentDirs, sysDirs []string) map[string]string {
	out := make(map[string]string, len(families))
	want := make(map[string]struct{}, len(families))
	for _, fam := range families {
		fam = strings.TrimSpace(fam)
		if fam == "" {
			continue
		}
		if looksLikeFontPath(fam) {
			if st, err := os.Stat(fam); err == nil && !st.IsDir() {
				out[fam] = fam
				continue
			}
		}
		want[fam] = struct{}{}
	}
	if len(want) == 0 {
		return out
	}
	idx := &fontIndex{budget: fontScanMaxFiles}
	for _, dir := range contentDirs {
		idx.scan(dir, fontScanMaxDepth)
	}
	resolve := func() int {
		missing := 0
		for fam := range want {
			if out[fam] != "" {
				continue
			}
			if p := idx.match(fam); p != "" {
				out[fam] = p
			} else {
				missing++
			}
		}
		return missing
	}
	if resolve() == 0 {
		return out
	}
	for _, dir := range sysDirs {
		idx.scan(dir, 1) // flat, like FontFiles: no per-family subdirs there
	}
	resolve()
	return out
}

// looksLikeFontPath: families never contain a separator or end in a font extension.
func looksLikeFontPath(s string) bool {
	if strings.ContainsAny(s, `/\`) {
		return true
	}
	ext := strings.ToLower(filepath.Ext(s))
	return ext == ".ttf" || ext == ".otf" || ext == ".ttc"
}

// FontFileFor resolves an element's "<id>_font" family as Qt would after main.cpp:44-55; "" if none.
func (t *Theme) FontFileFor(element string, sysDirs []string) string {
	return t.FontFiles(sysDirs)[element]
}

// FontFiles resolves every FontElements entry in one bounded disk walk; off-thread only (hard rule 2).
func (t *Theme) FontFiles(sysDirs []string) map[string]string {
	// A theme that names no family must not touch the disk at all.
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
	// The active theme's own tree, never the default fallback: only the active theme imposes fonts.
	for _, dir := range t.ownDirs() {
		idx.scan(dir, fontScanMaxDepth)
	}
	// <root>/fonts, the AO base sibling of themes/, deduped across roots.
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
	// OS font folders, only when something is unresolved, so a self-contained theme never reads C:\Windows\Fonts.
	for _, dir := range sysDirs {
		idx.scan(dir, 1) // flat: the system font folder has no per-family subdirs
	}
	resolve()
	return out
}

// fontEntry is one indexed font file; stem is normalized for comparison.
type fontEntry struct{ stem, path string }

// fontIndex is one pass's priority-ordered files; budget is what remains of fontScanMaxFiles.
type fontIndex struct {
	ents   []fontEntry
	budget int
}

// scan appends dir's font files, then recurses; files first, so the theme root outranks a subfolder.
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

// match resolves a family: exact stem first, so "Igiari" cannot lose to "igiari-cyrillic.ttf",
// then a containing stem, then both through systemFontAliases.
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

// firstFontIn returns any font file directly inside dir, for a theme that bundles one.
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

// normalizeFontKey lowercases and drops spaces, underscores and hyphens, so spellings of one family compare equal.
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

func (t *Theme) SoundName(key string) (string, bool) {
	return t.sounds.Get(key)
}

// PenaltyValue returns a penalty/penalty.ini entry (AO2-Client get_penalty_value).
func (t *Theme) PenaltyValue(key string) (string, bool) {
	return t.penalty.Get(key)
}

// FindAsset returns the first existing theme file for stem, probing exts in order across the theme dirs.
func (t *Theme) FindAsset(stem string, exts []string) (string, bool) {
	for _, dir := range t.dirs {
		if path, ok := FindAssetIn(dir, stem, exts); ok {
			return path, true
		}
	}
	return "", false
}

// FindAssetIn is FindAsset for one directory: AO2 derives chat variants from the base skin's path
// (courtroom.cpp:3358-3368), so they must not pair with another theme's base.
func FindAssetIn(dir, stem string, exts []string) (string, bool) {
	// An empty stem means no variant; otherwise Join builds <dir>/.png, adoptable as a skin.
	if stem == "" {
		return "", false
	}
	for _, ext := range exts {
		path := filepath.Join(dir, stem+ext)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func (t *Theme) Dirs() []string {
	out := make([]string, len(t.dirs))
	copy(out, t.dirs)
	return out
}

// KeyCount totals the keys loaded across the three INIs; 0 means the theme dirs contributed nothing.
func (t *Theme) KeyCount() int {
	return t.design.Len() + t.fonts.Len() + t.sounds.Len()
}

// DesignKeys returns root-section keys, where AO2 reads rects; it allocates, so never on a draw path.
func (t *Theme) DesignKeys() map[string]string { return t.design.SectionKeys("") }

func parseRGB(raw string) (RGB, bool) {
	parts := strings.Split(raw, tupleSeparator)
	if len(parts) < rgbFields {
		return RGB{}, false
	}
	return RGB{
		R: uint8(parse.AtoiOr(parts[0], 0)),
		G: uint8(parse.AtoiOr(parts[1], 0)),
		B: uint8(parse.AtoiOr(parts[2], 0)),
	}, true
}
