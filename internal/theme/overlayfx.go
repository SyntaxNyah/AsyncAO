package theme

// AO2 screen-effect overlays (effects.ini): parse, two-pass merge and art lookup; ../AO2-Client wins.
// INI has no section enumerator, so the group scan walks indices up to OverlayFXIndexScanCap.

import (
	"bufio"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

const (
	// OverlayFXCap bounds effects per effects.ini (hard rule 4); the largest real pack seen has 27.
	OverlayFXCap = 128
	// overlayFXInitialCap sizes the read slice for a typical theme.
	overlayFXInitialCap = 8
	// OverlayFXIndexScanCap bounds the group probe; AO2 enumerates childGroups (text_file_functions.cpp:801-812).
	OverlayFXIndexScanCap = 512
	// OverlayFXNameLen bounds an effect name; refused because truncating would name a different effect.
	OverlayFXNameLen = 64
	// OverlayFXListCap: get_effects concatenates theme and misc undeduped (text_file_functions.cpp:768-832).
	OverlayFXListCap = 2 * OverlayFXCap
	// OverlayFXFileCap: AO2's 11 tiers (path_functions.cpp:175-216) plus AsyncAO's flat-import tier.
	OverlayFXFileCap = 12
	// overlayFXMaxBytes bounds one read; a theme folder may have arrived in a stranger's bundle (hard rule 4).
	overlayFXMaxBytes = 1 << 20
)

// Layout names inside a theme or misc folder, per AO2's get_effect(s).
const (
	overlayFXDirName  = "effects" // themes/<t>/effects/… (text_file_functions.cpp:771, :841)
	overlayFXFileName = "effects.ini"
	overlayFXIconDir  = "icons"   // get_effect("icons/" + name, …) (courtroom.cpp:5608)
	overlayMiscDir    = "misc"    // get_misc_path / get_theme_path("misc/"+m+"/"+e)
	overlayVersionSec = "version" // [version] major = 2
	overlayVersionKey = "major"
	// overlayFXVersion2 introduced [N] groups; lower means flat v1 (text_file_functions.cpp:788-799).
	overlayFXVersion2 = 2
)

// overlayImageExts is get_image_suffix's order (text_file_functions.cpp:381-404), as in ui's themeImageExts.
var overlayImageExts = [...]string{".webp", ".apng", ".gif", ".png"}

// OverlayLayer is where do_effect parents the overlay (courtroom.cpp:3216-3237).
type OverlayLayer uint8

const (
	// OverlayLayerChat is the else branch and zero value: absent, empty, "beho" and "under" land here.
	OverlayLayerChat OverlayLayer = iota
	// OverlayLayerBehind stacks under the speaker sprite.
	OverlayLayerBehind
	// OverlayLayerCharacter stacks over the characters and under the desk.
	OverlayLayerCharacter
	// OverlayLayerOver raises the overlay inside the viewport.
	OverlayLayerOver
)

func (l OverlayLayer) String() string {
	switch l {
	case OverlayLayerBehind:
		return "behind"
	case OverlayLayerCharacter:
		return "character"
	case OverlayLayerOver:
		return "over"
	default:
		return "chat"
	}
}

// overlayFXProp indexes one property, kept raw (text_file_functions.cpp:853-894) and coerced
// by do_effect (courtroom.cpp:3182-3273), so the merge can clobber to empty before coercion.
type overlayFXProp int

const (
	fxPropSound overlayFXProp = iota
	fxPropCull
	fxPropLayer
	fxPropLoop
	fxPropMaxDuration
	fxPropRespectFlip
	fxPropRespectOffset
	fxPropScaling
	fxPropSticky
	fxPropStretch
	overlayFXPropCount
)

var overlayFXPropKeys = [overlayFXPropCount]string{
	fxPropSound:         "sound",
	fxPropCull:          "cull",
	fxPropLayer:         "layer",
	fxPropLoop:          "loop",
	fxPropMaxDuration:   "max_duration",
	fxPropRespectFlip:   "respect_flip",
	fxPropRespectOffset: "respect_offset",
	fxPropScaling:       "scaling",
	fxPropSticky:        "sticky",
	fxPropStretch:       "stretch",
}

// overlayBoolTrue is do_effect's case-sensitive prefix (courtroom.cpp:3206/3208/3210/3214).
// "TRUE" is false in AO2, so normalising would flip effects.
const overlayBoolTrue = "true"

// OverlayFX is one effects.ini entry coerced as do_effect does; raw is for merging before coercion.
type OverlayFX struct {
	// Name is the authored spelling; matching is case-insensitive (get_effect_property:877).
	Name string
	// Sound is the SFX name; "0" means no sound (aosfxplayer.cpp:63-70), filtered at the play site.
	Sound string
	// Scaling is the raw token, as internal/theme must not import internal/courtroom's ScalingMode.
	Scaling string
	// Layer is do_effect's parenting switch. Zero value = chat (the else branch).
	Layer OverlayLayer
	// Cull hides a finished play-once clip (animationlayer.cpp:660-666); Sticky is never on the wire.
	Loop, Cull, Stretch, RespectFlip, RespectOffset, Sticky bool
	// MaxFrameMS clamps each frame's delay, not the clip's lifetime (animationlayer.cpp:281-286); 0 means no clamp.
	MaxFrameMS int

	raw [overlayFXPropCount]string
}

// Raw exposes one property's authored string; "" means absent or empty, which QSettings conflates.
func (f OverlayFX) Raw(key string) string {
	for i, k := range overlayFXPropKeys {
		if k == key {
			return f.raw[i]
		}
	}
	return ""
}

// coerce fills the exported fields from raw, reproducing do_effect's coercions.
func (f OverlayFX) coerce() OverlayFX {
	f.Sound = f.raw[fxPropSound]
	f.Scaling = f.raw[fxPropScaling]
	// startsWith("true"), case-sensitive (courtroom.cpp:3206, 3208, 3210, 3214).
	f.Stretch = strings.HasPrefix(f.raw[fxPropStretch], overlayBoolTrue)
	f.Loop = strings.HasPrefix(f.raw[fxPropLoop], overlayBoolTrue)
	f.Cull = strings.HasPrefix(f.raw[fxPropCull], overlayBoolTrue)
	// respect_flip is ANDed with FLIP==1 at the call site.
	f.RespectFlip = strings.HasPrefix(f.raw[fxPropRespectFlip], overlayBoolTrue)
	// respect_offset is the one exact-equality test (courtroom.cpp:3249), not a prefix;
	// readIniLine strips whitespace first (text_file_functions.cpp:869), as ParseINI does.
	f.RespectOffset = f.raw[fxPropRespectOffset] == overlayBoolTrue
	// sticky is read on the sending side only (courtroom.cpp:2348-2354).
	f.Sticky = strings.HasPrefix(f.raw[fxPropSticky], overlayBoolTrue)
	// QString::toInt() gives 0 for anything unparseable, like parse.AtoiOr.
	f.MaxFrameMS = parse.AtoiOr(f.raw[fxPropMaxDuration], 0)
	if f.MaxFrameMS < 0 {
		f.MaxFrameMS = 0 // a negative clamp is not a clamp; AO2's timer would reject it
	}
	f.Layer = parseOverlayLayer(f.raw[fxPropLayer])
	return f
}

// parseOverlayLayer reproduces do_effect's layer switch, defects included (courtroom.cpp:3216-3237).
func parseOverlayLayer(raw string) OverlayLayer {
	switch strings.ToLower(raw) {
	case "behind":
		return OverlayLayerBehind
	case "character":
		return OverlayLayerCharacter
	case "over":
		return OverlayLayerOver
	default:
		return OverlayLayerChat
	}
}

// ParseOverlayFX visits numeric groups in ascending order (text_file_functions.cpp:812, :814-829).
// v1 files migrate in memory, never writing a .old file.
func ParseOverlayFX(r io.Reader) ([]OverlayFX, error) {
	ini, err := ParseINI(r)
	if err != nil {
		return nil, err
	}
	if major, _ := ini.GetSection(overlayVersionSec, overlayVersionKey); parse.AtoiOr(major, 0) < overlayFXVersion2 {
		return migrateOverlayFXV1(ini), nil
	}
	out := make([]OverlayFX, 0, overlayFXInitialCap)
	for i := 0; i < OverlayFXIndexScanCap; i++ {
		if len(out) >= OverlayFXCap {
			break
		}
		group := strconv.Itoa(i)
		name, ok := ini.GetSection(group, "name")
		if !ok || name == "" || len(name) > OverlayFXNameLen {
			continue
		}
		fx := OverlayFX{Name: name}
		for p := overlayFXProp(0); p < overlayFXPropCount; p++ {
			fx.raw[p], _ = ini.GetSection(group, overlayFXPropKeys[p])
		}
		out = append(out, fx.coerce())
	}
	return out, nil
}

// v1 is flat, "<effect> = <sfx>" plus suffixed keys (aoutils.cpp:10-88); AO2 rewrites it on disk,
// but a streaming client must never rewrite a user's theme folder.
var overlayFXV1Props = [...]string{"sound", "scaling", "stretch", "ignore_offset", "under_chatbox"}

// migrateOverlayFXV1 ports migrateEffects; alphabetical because AO2 iterates a sorted QMap.
func migrateOverlayFXV1(ini *INI) []OverlayFX {
	rootKeys := ini.SectionKeys("")
	names := make([]string, 0, len(rootKeys))
	for key := range rootKeys {
		// AO2's regex (aoutils.cpp:49): "realization_scaling" is a property, "realization_alt" an effect.
		if overlayFXV1IsProperty(key) {
			continue
		}
		names = append(names, key)
	}
	sort.Strings(names)
	out := make([]OverlayFX, 0, len(names))
	for _, name := range names {
		if len(out) >= OverlayFXCap {
			break
		}
		if name == "" || len(name) > OverlayFXNameLen {
			continue
		}
		fx := OverlayFX{Name: name}
		fx.raw[fxPropSound] = rootKeys[name]
		// Every migrated effect is cull=true, layer=character (aoutils.cpp:65-66).
		fx.raw[fxPropCull] = "true"
		fx.raw[fxPropLayer] = "character"
		if name == "realization" {
			// The one special case (aoutils.cpp:68-72).
			fx.raw[fxPropStretch] = "true"
			fx.raw[fxPropLayer] = "chat"
		}
		for _, prop := range overlayFXV1Props {
			val, ok := rootKeys[name+"_"+prop]
			if !ok {
				continue
			}
			switch prop {
			case "under_chatbox":
				// Fixed pair regardless of value (aoutils.cpp:38-40, 80-81): "false" still forces character.
				fx.raw[fxPropLayer] = "character"
			case "ignore_offset":
				// Migrated upstream but dead, since v2 reads respect_offset; dropped.
			case "sound":
				fx.raw[fxPropSound] = val
			case "scaling":
				fx.raw[fxPropScaling] = val
			case "stretch":
				fx.raw[fxPropStretch] = val
			}
		}
		out = append(out, fx.coerce())
	}
	return out
}

// overlayFXV1IsProperty implements aoutils.cpp:49's regex; a bare "_sound" is an effect name.
func overlayFXV1IsProperty(key string) bool {
	for _, prop := range overlayFXV1Props {
		suffix := "_" + prop
		if head, found := strings.CutSuffix(key, suffix); found && head != "" {
			return true
		}
	}
	return false
}

// OverlayFXSet is the merged roster: list-pass display order and last-wins property table.
type OverlayFXSet struct {
	// Order is theme-tier names then misc-tier names, duplicates kept (text_file_functions.cpp:770-831).
	Order  []string
	byName map[string]OverlayFX
}

// MergeOverlayFX builds the roster from the display list and every effects.ini across both tiers.
// AO2 never breaks the outer file loop (text_file_functions.cpp:860-888), so never use loadFirstINI.
func MergeOverlayFX(list []string, files ...[]OverlayFX) *OverlayFXSet {
	set := &OverlayFXSet{byName: make(map[string]OverlayFX, OverlayFXCap)}
	for _, name := range list {
		if len(set.Order) >= OverlayFXListCap {
			break
		}
		if name == "" || len(name) > OverlayFXNameLen {
			continue
		}
		set.Order = append(set.Order, name)
	}
	// Over the cap, drop from the front: the merge is last-wins, so the last files survive.
	if drop := len(files) - OverlayFXFileCap; drop > 0 {
		files = files[drop:]
	}
	for _, file := range files {
		collapsed := make(map[string]OverlayFX, len(file))
		order := make([]string, 0, len(file))
		for _, fx := range file {
			key := strings.ToLower(fx.Name)
			prev, seen := collapsed[key]
			if !seen {
				collapsed[key] = fx
				order = append(order, key)
				continue
			}
			// Duplicate name in one file: fill only properties the earlier group left empty.
			for p := overlayFXProp(0); p < overlayFXPropCount; p++ {
				if prev.raw[p] == "" {
					prev.raw[p] = fx.raw[p]
				}
			}
			collapsed[key] = prev.coerce()
		}
		for _, key := range order {
			set.byName[key] = collapsed[key]
		}
	}
	return set
}

// Get returns an effect's merged properties, case-insensitively (get_effect_property:877).
func (s *OverlayFXSet) Get(name string) (OverlayFX, bool) {
	if s == nil || s.byName == nil {
		return OverlayFX{}, false
	}
	fx, ok := s.byName[strings.ToLower(strings.TrimSpace(name))]
	return fx, ok
}

func (s *OverlayFXSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.byName)
}

// Names returns a copy of the display order, because the picker prepends "None".
func (s *OverlayFXSet) Names() []string {
	if s == nil || len(s.Order) == 0 {
		return nil
	}
	out := make([]string, len(s.Order))
	copy(out, s.Order)
	return out
}

func OverlayFXNames(file []OverlayFX) []string {
	if len(file) == 0 {
		return nil
	}
	out := make([]string, 0, len(file))
	for _, fx := range file {
		out = append(out, fx.Name)
	}
	return out
}

// dirIsSubtheme reads the theme name one level up, as the base is the subtheme (path_functions.cpp:181-185).
func (t *Theme) dirIsSubtheme(dir string) bool {
	return t.Sub != "" && filepath.Base(dir) == t.Sub && filepath.Base(filepath.Dir(dir)) == t.Name
}

// dirIsActiveTheme reports the active theme's own folder or a subtheme inside it.
func (t *Theme) dirIsActiveTheme(dir string) bool {
	return filepath.Base(dir) == t.Name || t.dirIsSubtheme(dir)
}

// filterDirs keeps accepted t.dirs in theme.Load's priority order, already AO2's tier order within a root.
func (t *Theme) filterDirs(keep func(string) bool) []string {
	out := make([]string, 0, len(t.dirs))
	for _, dir := range t.dirs {
		if keep(dir) {
			out = append(out, dir)
		}
	}
	return out
}

// activeDirs excludes themes/default: AO2's misc tier skips it (path_functions.cpp:181-189).
func (t *Theme) activeDirs() []string { return t.filterDirs(t.dirIsActiveTheme) }

// subthemeDirs are AO2's subtheme tier (path_functions.cpp:188-191), ahead of base misc.
func (t *Theme) subthemeDirs() []string { return t.filterDirs(t.dirIsSubtheme) }

// themeOnlyDirs are AO2's theme path tier (path_functions.cpp:195-198), reached only after base misc.
func (t *Theme) themeOnlyDirs() []string {
	return t.filterDirs(func(dir string) bool { return !t.dirIsSubtheme(dir) && t.dirIsActiveTheme(dir) })
}

// defaultDirs are the themes/default fallback dirs activeDirs skips.
func (t *Theme) defaultDirs() []string {
	return t.filterDirs(func(dir string) bool { return !t.dirIsActiveTheme(dir) })
}

// contentRoots recovers base/ roots two levels above t.dirs; the flat tier's parent is the root.
func (t *Theme) contentRoots() []string {
	out := make([]string, 0, len(t.dirs))
	seen := make(map[string]struct{}, len(t.dirs))
	for _, dir := range t.dirs {
		parent := filepath.Dir(dir)
		if filepath.Base(parent) != ThemesDirName {
			continue
		}
		root := filepath.Dir(parent)
		if _, dup := seen[root]; dup {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	return out
}

// FindAssetMisc looks under the subtheme, then the theme (path_functions.cpp:181-189); "../.." is refused.
func (t *Theme) FindAssetMisc(misc, stem string, exts []string) (string, bool) {
	misc = strings.TrimSpace(misc)
	if misc == "" || stem == "" {
		return "", false
	}
	rel := path.Join(overlayMiscDir, misc)
	for _, dir := range t.activeDirs() {
		miscDir, err := safepath.Join(dir, rel)
		if err != nil {
			return "", false // hostile folder name: refuse the whole probe, don't fall through
		}
		if p, ok := findAssetSafe(miscDir, stem, exts); ok {
			return p, true
		}
	}
	return "", false
}

// findAssetSafe routes the stem through safepath, because an effect name arrives on the wire.
func findAssetSafe(dir, stem string, exts []string) (string, bool) {
	if dir == "" || stem == "" {
		return "", false
	}
	base, err := safepath.Join(dir, stem)
	if err != nil {
		return "", false
	}
	for _, ext := range exts {
		p := base + ext
		if info, serr := os.Stat(p); serr == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// FindOverlayArt locates effect art in the theme tier only, tiers 1-4, walking the extensions at each.
func (t *Theme) FindOverlayArt(name string) (string, bool) {
	return t.findOverlayThemeTier(name)
}

// FindOverlayIcon is FindOverlayArt for the dropdown icon, element "icons/<name>" (courtroom.cpp:5608).
func (t *Theme) FindOverlayIcon(name string) (string, bool) {
	return t.findOverlayThemeTier(overlayFXIconDir + "/" + name)
}

// FindOverlayMisc is tiers 5-6, the per-character misc folder inside the theme.
func (t *Theme) FindOverlayMisc(misc, name string) (string, bool) {
	return t.FindAssetMisc(misc, name, overlayImageExts[:])
}

// FindOverlayElement walks the whole local order (path_functions.cpp:175-216); off-thread only.
// Tier 8 may also need the origin, reached only when all of this misses.
func (t *Theme) FindOverlayElement(misc, element string) (string, bool) {
	if element == "" {
		return "", false
	}
	exts := overlayImageExts[:]
	if p, ok := t.findOverlayThemeTier(element); ok { // rungs 1-4
		return p, true
	}
	if p, ok := t.FindAssetMisc(misc, element, exts); ok { // rungs 5-6
		return p, true
	}
	for _, dir := range t.subthemeDirs() { // rung 7 — BEFORE the base misc rung
		if p, ok := findAssetSafe(dir, element, exts); ok {
			return p, true
		}
	}
	if misc = strings.TrimSpace(misc); misc != "" { // rung 8 (base misc, on disk)
		rel := path.Join(overlayMiscDir, misc)
		for _, root := range t.contentRoots() {
			miscDir, err := safepath.Join(root, rel)
			if err != nil {
				break
			}
			if p, ok := findAssetSafe(miscDir, element, exts); ok {
				return p, true
			}
		}
	}
	for _, dir := range t.themeOnlyDirs() { // rung 9
		if p, ok := findAssetSafe(dir, element, exts); ok {
			return p, true
		}
	}
	for _, dir := range t.defaultDirs() { // rung 10
		if p, ok := findAssetSafe(dir, element, exts); ok {
			return p, true
		}
	}
	for _, root := range t.contentRoots() { // rung 11
		if p, ok := findAssetSafe(root, element, exts); ok {
			return p, true
		}
	}
	return "", false
}

func (t *Theme) FindOverlayElementIcon(misc, name string) (string, bool) {
	return t.FindOverlayElement(misc, overlayFXIconDir+"/"+name)
}

// findOverlayThemeTier walks tiers 1-4 (the "effects/" prefixed element).
func (t *Theme) findOverlayThemeTier(element string) (string, bool) {
	if element == "" {
		return "", false
	}
	exts := overlayImageExts[:]
	for _, dir := range t.dirs { // rungs 1-3, in theme.Load's own priority order
		if p, ok := findAssetSafe(filepath.Join(dir, overlayFXDirName), element, exts); ok {
			return p, true
		}
	}
	for _, root := range t.contentRoots() { // rung 4
		if p, ok := findAssetSafe(filepath.Join(root, overlayFXDirName), element, exts); ok {
			return p, true
		}
	}
	return "", false
}

// OverlayFXPaths returns existing effects.ini files per tier; off-thread only.
// get_effects reads the first of each, get_effect_property all (text_file_functions.cpp:860-864).
func (t *Theme) OverlayFXPaths(misc string) (themeTier, miscTier []string) {
	statInto := func(dst []string, p string) []string {
		if len(dst) >= OverlayFXFileCap {
			return dst
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			dst = append(dst, p)
		}
		return dst
	}
	for _, dir := range t.dirs { // rungs 1-3
		themeTier = statInto(themeTier, filepath.Join(dir, overlayFXDirName, overlayFXFileName))
	}
	for _, root := range t.contentRoots() { // rung 4
		themeTier = statInto(themeTier, filepath.Join(root, overlayFXDirName, overlayFXFileName))
	}
	misc = strings.TrimSpace(misc)
	if misc != "" {
		rel := path.Join(overlayMiscDir, misc)
		for _, dir := range t.activeDirs() { // rungs 5-6 (subtheme misc first)
			if miscDir, err := safepath.Join(dir, rel); err == nil {
				miscTier = statInto(miscTier, filepath.Join(miscDir, overlayFXFileName))
			}
		}
	}
	for _, dir := range t.subthemeDirs() { // rung 7 — BEFORE the base misc rung
		miscTier = statInto(miscTier, filepath.Join(dir, overlayFXFileName))
	}
	if misc != "" {
		rel := path.Join(overlayMiscDir, misc)
		for _, root := range t.contentRoots() { // rung 8 (base misc, on disk)
			if miscDir, err := safepath.Join(root, rel); err == nil {
				miscTier = statInto(miscTier, filepath.Join(miscDir, overlayFXFileName))
			}
		}
	}
	for _, dir := range t.themeOnlyDirs() { // rung 9
		miscTier = statInto(miscTier, filepath.Join(dir, overlayFXFileName))
	}
	for _, dir := range t.defaultDirs() { // rung 10
		miscTier = statInto(miscTier, filepath.Join(dir, overlayFXFileName))
	}
	for _, root := range t.contentRoots() { // rung 11
		miscTier = statInto(miscTier, filepath.Join(root, overlayFXFileName))
	}
	return themeTier, miscTier
}

// OverlayFXFiles parses the theme tier: first for the display list, all for properties; off-thread only.
func (t *Theme) OverlayFXFiles() (first []OverlayFX, all [][]OverlayFX) {
	themeTier, _ := t.OverlayFXPaths("")
	return parseOverlayFXPaths(themeTier)
}

// OverlayFXMiscFiles is OverlayFXFiles for local misc tiers; the UI fetches the origin's file.
func (t *Theme) OverlayFXMiscFiles(misc string) (first []OverlayFX, all [][]OverlayFX) {
	_, miscTier := t.OverlayFXPaths(misc)
	return parseOverlayFXPaths(miscTier)
}

func parseOverlayFXPaths(paths []string) (first []OverlayFX, all [][]OverlayFX) {
	for _, p := range paths {
		file, err := readOverlayFXFile(p)
		if err != nil {
			continue
		}
		if first == nil {
			first = file
		}
		all = append(all, file)
	}
	return first, all
}

func readOverlayFXFile(p string) ([]OverlayFX, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseOverlayFX(bufio.NewReader(io.LimitReader(f, overlayFXMaxBytes)))
}
