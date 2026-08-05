package theme

// AO2 screen-effect overlays (`effects.ini`) — the parse, the two-pass merge and
// the theme-local art ladder. Canon is ../AO2-Client; it wins every conflict.
//
// NAMING FENCE. internal/theme already owns EffectBind / EffectKind /
// effectNames / FXNone (sidecar*.go — theme-editor WIDGET DECORATION, unrelated),
// and internal/courtroom owns TextEffect* / EffectSpan / EffectMark (animated chat
// TEXT). Everything this file adds is therefore prefixed Overlay… / overlay…. A
// bare `Effect` type in this package is a review-blocking defect.
//
// This file deliberately does NOT touch ini.go: INI has no section enumerator
// (GetSection needs the name up front), so the group scan below walks indices
// 0…OverlayFXIndexScanCap. That is rule-4/rule-9 clean and immune to whatever
// Sections() helper a later wave adds.

import (
	"bufio"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

const (
	// OverlayFXCap bounds how many effects one effects.ini contributes (rule
	// §17.4). The stock KFO roster is 17 and the largest real misc pack seen is
	// 27, so 128 is an order of magnitude past anything authored while still
	// bounding a hostile file.
	OverlayFXCap = 128
	// OverlayFXIndexScanCap bounds the numeric-group scan. AO2 enumerates
	// QSettings::childGroups() and keeps whatever parses as an int
	// (text_file_functions.cpp:801-812); a streaming parser has to probe indices
	// instead, so the probe range is capped — the charEmoteScanCap idiom.
	OverlayFXIndexScanCap = 512
	// OverlayFXNameLen bounds an effect NAME. The name becomes a URL path
	// segment (misc/<folder>/<name>.webp) and a T1 cache key, so a pathological
	// one is refused outright rather than truncated into a different effect.
	OverlayFXNameLen = 64
	// OverlayFXListCap bounds the DISPLAY list. get_effects concatenates the
	// theme file and the misc file with no dedupe (text_file_functions.cpp:768-832),
	// so two capped files is the natural bound.
	OverlayFXListCap = 2 * OverlayFXCap
	// OverlayFXFileCap bounds how many effects.ini files the PROPERTY pass merges.
	// The canon ladder is 11 rungs (path_functions.cpp:175-216) across two
	// get_asset_paths calls; 12 covers it with room for AsyncAO's flat-import tier.
	OverlayFXFileCap = 12
	// overlayFXMaxBytes bounds one effects.ini read. Real files are 1-4 KiB; a
	// theme folder is local content, but it may have arrived in a bundle from a
	// stranger, so the read is still bounded (rule §17.4).
	overlayFXMaxBytes = 1 << 20
)

// Layout names inside a theme / misc folder, per AO2's get_effect(s).
const (
	overlayFXDirName  = "effects" // themes/<t>/effects/… (text_file_functions.cpp:771, :841)
	overlayFXFileName = "effects.ini"
	overlayFXIconDir  = "icons"   // get_effect("icons/" + name, …) (courtroom.cpp:5608)
	overlayMiscDir    = "misc"    // get_misc_path / get_theme_path("misc/"+m+"/"+e)
	overlayVersionSec = "version" // [version] major = 2
	overlayVersionKey = "major"
	// overlayFXVersion2 is the format revision that introduced the [N] group
	// layout. Absent or lower ⇒ the flat v1 file (text_file_functions.cpp:788-799).
	overlayFXVersion2 = 2
)

// overlayImageExts is get_image_suffix's probe order
// (text_file_functions.cpp:381-404) — identical to internal/ui's themeImageExts,
// stated here so the theme-local effect ladder cannot drift from it.
var overlayImageExts = [...]string{".webp", ".apng", ".gif", ".png"}

// OverlayImageExts returns a COPY of the effect art extension ladder
// (.webp → .apng → .gif → .png). A copy, because FindAsset takes a []string and
// an exported slice would be mutable by every caller.
func OverlayImageExts() []string {
	out := make([]string, len(overlayImageExts))
	copy(out, overlayImageExts[:])
	return out
}

// OverlayLayer is where do_effect parents the overlay (courtroom.cpp:3216-3237).
type OverlayLayer uint8

const (
	// OverlayLayerChat is do_effect's ELSE branch — reparented to the Courtroom
	// and stacked under the shout bubble. It is the ZERO value on purpose: the
	// canon default for an absent, empty, misspelled ("beho") or documented-but-
	// unimplemented ("under") layer value is chat, not behind.
	OverlayLayerChat OverlayLayer = iota
	// OverlayLayerBehind stacks under the speaker sprite (stackUnder(ui_vp_player_char)).
	OverlayLayerBehind
	// OverlayLayerCharacter stacks over the characters and UNDER the desk
	// (stackUnder(ui_vp_desk)).
	OverlayLayerCharacter
	// OverlayLayerOver raises the overlay inside the viewport (raise()).
	OverlayLayerOver
)

// String names the layer for diagnostics and test failures.
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

// overlayFXProp indexes one effects.ini property. Every property is read as a
// RAW string by get_effect_property (text_file_functions.cpp:853-894) and coerced
// at the call site in do_effect (courtroom.cpp:3182-3273); this parser keeps the
// raw strings so the last-file-wins merge can reproduce the clobber-to-empty rule
// before anything is coerced.
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

// overlayFXPropKeys maps the property index to its effects.ini key.
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

// overlayBoolTrue is the prefix do_effect tests for every boolean but one
// (`…startsWith("true")`, courtroom.cpp:3206/3208/3210/3214). It is
// CASE-SENSITIVE lowercase: "TRUE" and "True" are false in AO2, and normalising
// them would flip effects their authors left off.
const overlayBoolTrue = "true"

// OverlayFX is one parsed effects.ini entry, coerced exactly as do_effect coerces
// it. raw keeps the authored strings so MergeOverlayFX can merge before coercion.
type OverlayFX struct {
	// Name is the AUTHORED spelling: the display label, the wire value and the
	// URL/T1 identity. Matching is case-insensitive (get_effect_property:877).
	Name string
	// Sound is the SFX name played beside the overlay. "0" is the community
	// "no sound" idiom rather than a coded sentinel — AO2 simply never resolves
	// a file called 0.* (aosfxplayer.cpp:63-70) — so it survives the parse and
	// is filtered at the play site.
	Sound string
	// Scaling is the RAW get_scaling token ("smooth" / "pixel" / "fast" / …).
	// Kept raw because internal/theme must not import internal/courtroom, which
	// owns ScalingMode; the courtroom maps it.
	Scaling string
	// Layer is do_effect's parenting switch. Zero value = chat (the else branch).
	Layer OverlayLayer
	// Loop wraps forever; Cull hides the layer once a PLAY-ONCE clip finishes
	// (animationlayer.cpp:660-666 — inert while looping); Stretch fills the
	// viewport instead of fitting its height; RespectFlip mirrors with FLIP=1;
	// RespectOffset shifts by the speaker's self-offset; Sticky keeps the sender's
	// dropdown selection after a send (sender-side only, never on the wire).
	Loop, Cull, Stretch, RespectFlip, RespectOffset, Sticky bool
	// MaxFrameMS clamps EACH FRAME's delay, not the clip's total lifetime
	// (animationlayer.cpp:281-286). 0 = no clamp. The shipped comment calling it
	// "maximum duration for this effect" is only true for a 1-frame effect.
	MaxFrameMS int

	raw [overlayFXPropCount]string
}

// Raw exposes one property's authored string ("" = absent OR empty — QSettings
// conflates the two, and so does get_effect_property's emptiness test). For
// tests and diagnostics.
func (f OverlayFX) Raw(key string) string {
	for i, k := range overlayFXPropKeys {
		if k == key {
			return f.raw[i]
		}
	}
	return ""
}

// coerce fills the exported fields from raw, reproducing do_effect's coercions
// one for one. Every rule below carries its canon line.
func (f OverlayFX) coerce() OverlayFX {
	f.Sound = f.raw[fxPropSound]
	f.Scaling = f.raw[fxPropScaling]
	// startsWith("true"), case-sensitive (courtroom.cpp:3206/:3208/:3210/:3214).
	f.Stretch = strings.HasPrefix(f.raw[fxPropStretch], overlayBoolTrue)
	f.Loop = strings.HasPrefix(f.raw[fxPropLoop], overlayBoolTrue)
	f.Cull = strings.HasPrefix(f.raw[fxPropCull], overlayBoolTrue)
	// respect_flip is ANDed with FLIP==1 at the call site; the parse keeps the
	// declaration only.
	f.RespectFlip = strings.HasPrefix(f.raw[fxPropRespectFlip], overlayBoolTrue)
	// respect_offset is the ONE exact-equality test (courtroom.cpp:3249), not a
	// prefix test: "truely", "TRUE" and "True" are false here and true nowhere
	// else — pinned by TestRespectOffsetRequiresExactTrue. Trailing whitespace is
	// NOT a case this has to reject: ParseINI stores TrimSpace(value) (ini.go:120)
	// and so does canon, which reads this through QSettings IniFormat
	// (text_file_functions.cpp:869) — readIniLine strips it before the compare.
	f.RespectOffset = f.raw[fxPropRespectOffset] == overlayBoolTrue
	// sticky is read on the SENDING side only (courtroom.cpp:2348-2354).
	f.Sticky = strings.HasPrefix(f.raw[fxPropSticky], overlayBoolTrue)
	// QString::toInt() → 0 for anything unparseable, exactly like atoiTrim.
	f.MaxFrameMS = atoiTrim(f.raw[fxPropMaxDuration])
	if f.MaxFrameMS < 0 {
		f.MaxFrameMS = 0 // a negative clamp is not a clamp; AO2's timer would reject it
	}
	f.Layer = parseOverlayLayer(f.raw[fxPropLayer])
	return f
}

// parseOverlayLayer reproduces do_effect's layer switch INCLUDING its two
// documented defects (courtroom.cpp:3216-3237):
//
//   - "under" does NOT exist. The shipped effects.ini header documents
//     under/character/over/chat; the code implements behind/character/over/else,
//     so "under" lands on CHAT. Both stock inis actually write "behind".
//   - "beho" is a truncation typo in the `gloom` entry, present in KFO's file AND
//     in AO2's own bin/base/themes/default/effects/effects.ini:151. It falls to
//     the else branch → CHAT. Do not normalise it.
//
// layer is the only property AO2 lowercases before comparing (:3217).
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

// ParseOverlayFX reads ONE effects.ini. Groups are visited in ascending NUMERIC
// index order (AO2 sorts its childGroups the same way for the LIST,
// text_file_functions.cpp:812); non-numeric groups — including [version] — and
// groups with no `name`, an empty `name` or an over-long one are skipped
// (:814-829). A v1 file (no [version] major, or major < 2) is migrated IN MEMORY
// first; unlike AO2 this never writes a .old file back to the user's theme.
//
// Pure: no disk access, no SDL, bounded by OverlayFXCap / OverlayFXIndexScanCap.
func ParseOverlayFX(r io.Reader) ([]OverlayFX, error) {
	ini, err := ParseINI(r)
	if err != nil {
		return nil, err
	}
	if major, _ := ini.GetSection(overlayVersionSec, overlayVersionKey); atoiTrim(major) < overlayFXVersion2 {
		return migrateOverlayFXV1(ini), nil
	}
	out := make([]OverlayFX, 0, 8)
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

// v1 legacy format (aoutils.cpp:10-88). The file is FLAT — "<effect> = <sfx>"
// plus "<effect>_sound|_scaling|_stretch|_ignore_offset|_under_chatbox" — and AO2
// rewrites it on disk after copying the original to <file>.old, ABORTING the
// migration if the copy fails. AsyncAO migrates in memory and never writes: a
// streaming client has no business rewriting a user's theme folder, and the
// in-memory result is what every consumer reads anyway
// (TestV1MigrationNeverWritesToDisk pins it).
var overlayFXV1Props = [...]string{"sound", "scaling", "stretch", "ignore_offset", "under_chatbox"}

// migrateOverlayFXV1 ports migrateEffects. Order is ALPHABETICAL because AO2
// iterates a QMap (sorted by key) and the group indices it writes follow that
// iteration — the authored order of a v1 file is genuinely lost upstream too.
func migrateOverlayFXV1(ini *INI) []OverlayFX {
	rootKeys := ini.SectionKeys("")
	names := make([]string, 0, len(rootKeys))
	for key := range rootKeys {
		// The name/property ambiguity is resolved by AO2's own regex
		// `(\w+)_(sound|scaling|stretch|ignore_offset|under_chatbox)$`
		// (aoutils.cpp:49): "realization_scaling" is a PROPERTY, "realization_alt"
		// is an EFFECT (its suffix is not in the property list), "hearts" is an
		// effect. Reproduced literally so both cases keep behaving.
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
			// The one special case (:68-72).
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
				// Mapped to the FIXED pair ("layer","character") regardless of the
				// value (aoutils.cpp:38-40, :80-81) — so "…_under_chatbox = false"
				// STILL forces layer=character, and it overwrites realization's
				// layer=chat when both are present. Reproduced, not fixed.
				fx.raw[fxPropLayer] = "character"
			case "ignore_offset":
				// Migrated verbatim upstream and DEAD: v2 reads respect_offset, so
				// nothing ever consumes it. Dropped here rather than carried as a
				// field nothing reads.
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

// overlayFXV1IsProperty implements aoutils.cpp:49's regex without a regexp: the
// key must end in "_<property>" AND have at least one word character before that
// underscore (`(\w+)_`), which is what keeps a bare "_sound" an effect name.
func overlayFXV1IsProperty(key string) bool {
	for _, prop := range overlayFXV1Props {
		suffix := "_" + prop
		if head, found := strings.CutSuffix(key, suffix); found && head != "" {
			return true
		}
	}
	return false
}

// OverlayFXSet is the merged roster: the DISPLAY order from the list pass and the
// last-wins property table from the property pass. The two passes are genuinely
// different walks — see MergeOverlayFX.
type OverlayFXSet struct {
	// Order is the display list: the first theme-tier file's names followed by
	// the first misc-tier file's, duplicates preserved, never re-sorted
	// (get_effects, text_file_functions.cpp:770-831).
	Order  []string
	byName map[string]OverlayFX
}

// MergeOverlayFX builds the roster. list is the DISPLAY order (the list pass);
// files are every existing effects.ini across BOTH ladders in probe order (the
// property pass).
//
// THE MERGE ASYMMETRY — the trap this whole function exists for.
// get_effect_property (text_file_functions.cpp:860-888) walks paths + misc_paths
// and never breaks the OUTER file loop, only the inner group loop (:883).
// Therefore:
//
//   - the LAST file in the ladder wins, which is the opposite of art resolution
//     (first hit wins) and the opposite of loadFirstINI's first-hit-per-key merge;
//   - the assignment at :879 precedes the emptiness test at :880, so a later file
//     that HAS the group but OMITS the property clobbers an earlier non-empty
//     value back to "" — i.e. to the coerced default.
//
// Both fall out of replacing the whole record per file: "which file was last to
// carry a matching group" depends only on the NAME, so it is the same file for
// every property, and a per-record swap is exactly a per-property last-wins.
// Within one file, duplicate names collapse per property to the FIRST non-empty
// value in group order — the inner loop's break condition.
//
// Never use loadFirstINI for effects.ini.
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
	// OverlayFXFileCap truncates from the FRONT, keeping the LAST files. This merge
	// is LAST-WINS, so the last file in the ladder is the most specific one — the
	// character's own misc/<folder>/effects.ini, then the origin manifest. Dropping
	// the tail (the obvious `i >= cap: break`) would throw away exactly the files
	// whose values survive and keep only the ones every later file overrides, i.e.
	// it would silently invert the ladder for any character whose ladder is deeper
	// than the cap.
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
			// Duplicate name inside one file: fill only the properties the earlier
			// group left EMPTY (the inner loop breaks on the first non-empty value).
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

// Get returns the merged properties for an effect NAME, case-insensitively
// (get_effect_property compares toLower on both sides, :877).
func (s *OverlayFXSet) Get(name string) (OverlayFX, bool) {
	if s == nil || s.byName == nil {
		return OverlayFX{}, false
	}
	fx, ok := s.byName[strings.ToLower(strings.TrimSpace(name))]
	return fx, ok
}

// Len reports how many DISTINCT effects carry properties.
func (s *OverlayFXSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.byName)
}

// Names returns a COPY of the display order (callers mutate their own list —
// "None" gets prepended by the picker).
func (s *OverlayFXSet) Names() []string {
	if s == nil || len(s.Order) == 0 {
		return nil
	}
	out := make([]string, len(s.Order))
	copy(out, s.Order)
	return out
}

// OverlayFXNames pulls the display names out of one parsed file, in file order.
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

// --- theme-local resolution --------------------------------------------------

// dirIsSubtheme reports whether one of t.dirs is a SUBTHEME folder of the active
// theme — "<…>/themes/<name>/<sub>" or the flat-import "<…>/<name>/<sub>".
//
// A base-name test alone cannot answer this: the subtheme dir's base is the
// SUBTHEME name, so the theme name has to be read one level up. That is exactly
// the bug this pair replaced — with a base-only filter every subtheme directory
// fell out of activeDirs and into defaultDirs, which silently deleted AO2's
// subtheme misc rung (get_asset_paths tier 2, path_functions.cpp:181-185) and
// demoted rung 7 to rung-10 position.
func (t *Theme) dirIsSubtheme(dir string) bool {
	return t.Sub != "" && filepath.Base(dir) == t.Sub && filepath.Base(filepath.Dir(dir)) == t.Name
}

// dirIsActiveTheme reports whether one of t.dirs belongs to the ACTIVE theme —
// its own folder OR its subtheme folder inside it.
func (t *Theme) dirIsActiveTheme(dir string) bool {
	return filepath.Base(dir) == t.Name || t.dirIsSubtheme(dir)
}

// filterDirs keeps the t.dirs entries `keep` accepts, in theme.Load's own
// priority order (which is already AO2's tier order within a root).
func (t *Theme) filterDirs(keep func(string) bool) []string {
	out := make([]string, 0, len(t.dirs))
	for _, dir := range t.dirs {
		if keep(dir) {
			out = append(out, dir)
		}
	}
	return out
}

// activeDirs are the probe directories belonging to the ACTIVE theme, subtheme
// folders FIRST (theme.Load already orders them that way). themes/default is
// deliberately excluded: AO2 probes the misc tier under the active theme and its
// subtheme only, never under the default theme (path_functions.cpp:181-189).
func (t *Theme) activeDirs() []string { return t.filterDirs(t.dirIsActiveTheme) }

// subthemeDirs are the active theme's SUBTHEME folders alone — AO2's rung 7
// ("Subtheme path", path_functions.cpp:188-191), which sits AHEAD of the base
// misc rung and of the theme's own folder. Empty when no subtheme is set, and
// then the whole rung disappears exactly as it does in AO2.
func (t *Theme) subthemeDirs() []string { return t.filterDirs(t.dirIsSubtheme) }

// themeOnlyDirs are the active theme's own folders WITHOUT the subtheme ones —
// AO2's rung 9 ("Theme path", :195-198), which canon reaches only after the base
// misc rung.
func (t *Theme) themeOnlyDirs() []string {
	return t.filterDirs(func(dir string) bool { return !t.dirIsSubtheme(dir) && t.dirIsActiveTheme(dir) })
}

// defaultDirs are the themes/default fallback dirs (everything activeDirs skips).
func (t *Theme) defaultDirs() []string {
	return t.filterDirs(func(dir string) bool { return !t.dirIsActiveTheme(dir) })
}

// contentRoots recovers the base/ roots behind t.dirs — a theme dir is
// "<root>/themes/<name>", so the root is two levels up. AO2's last two rungs
// ("misc/<m>/<e>" and the bare "<e>") are root-relative, and there is no other
// way to name them from a *Theme. Deduped, order preserved. The flat-import tier
// ("<root>/<name>", AsyncAO's own) contributes no root: its parent IS the root
// already and treating it as one would probe a level above the user's folder.
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

// FindAssetMisc locates a per-character MISC asset in the theme's own folders:
// themes/<theme>/<subtheme>/misc/<misc>/<stem>.<ext> then
// themes/<theme>/misc/<misc>/<stem>.<ext> — AO2's get_asset_paths tiers 2 and 3
// (path_functions.cpp:181-189).
//
// This is a THEME-LOCAL directory probe: zero network, no zero-fallback exposure.
// It is the shared seam between the theme editor's per-character chatbox work
// (Q5/W8) and the effects overlay's rungs 5-6 — one implementation, two callers,
// the internal/safepath precedent. Under no circumstances add a second misc
// resolver.
//
// misc comes from a server-supplied char.ini, so it goes through safepath.Join:
// a "../../.." folder must be a refusal, not a read of somebody's private files.
// Off-thread only (hard rule 2) — it stats the disk.
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

// findAssetSafe is FindAssetIn with the stem routed through safepath: an effect
// NAME arrives on the wire (the MS effects field), so "../../../etc/passwd" must
// refuse rather than resolve. The extension probe itself is unchanged.
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

// FindOverlayArt locates an effect's art in the THEME tier only — rungs 1-4 of
// the ladder, i.e. get_image("effects/"+name, …, misc=""):
//
//	themes/<t>/<sub>/effects/<n>, themes/<t>/effects/<n>,
//	themes/default/effects/<n>, effects/<n>
//
// Extensions walk .webp → .apng → .gif → .png at EVERY rung (get_image_suffix).
// Off-thread only.
func (t *Theme) FindOverlayArt(name string) (string, bool) {
	return t.findOverlayThemeTier(name)
}

// FindOverlayIcon is FindOverlayArt for the dropdown row icon: AO2 asks for the
// element "icons/<name>" (courtroom.cpp:5608), so the icon lives beside the art
// in an icons/ subfolder at whichever rung answers.
func (t *Theme) FindOverlayIcon(name string) (string, bool) {
	return t.findOverlayThemeTier(overlayFXIconDir + "/" + name)
}

// FindOverlayMisc is rungs 5-6 — the per-character misc folder inside the theme,
// resolved through the shared FindAssetMisc.
func (t *Theme) FindOverlayMisc(misc, name string) (string, bool) {
	return t.FindAssetMisc(misc, name, overlayImageExts[:])
}

// FindOverlayElement walks the WHOLE local ladder for one element, in AO2's
// order (path_functions.cpp:175-216 through get_effect's two get_image calls):
//
//	 1-3  themes/<t>/<sub>/effects/<e>, themes/<t>/effects/<e>, themes/default/effects/<e>
//	 4    effects/<e>
//	 5-6  themes/<t>/<sub>/misc/<m>/<e>, themes/<t>/misc/<m>/<e>     [FindAssetMisc]
//	 7    themes/<t>/<sub>/<e>
//	 8    misc/<m>/<e>            [ALSO the one ORIGIN rung — see URLBuilder.OverlayEffectMisc]
//	 9    themes/<t>/<e>
//	10    themes/default/<e>
//	11    <e>
//
// Rungs 1/5/7 exist only while a SUBTHEME is set; with none they simply drop out
// of the walk (theme.Load contributes no subtheme dirs), which is how AO2's own
// `p_subtheme != ""` guards behave. The flat-import tier ("<root>/<name>") rides
// with the active theme's dirs, which is where theme.Load already puts it.
//
// Rung 8 is the only rung a streaming client may also have to ask the ORIGIN for;
// on disk it is an ordinary probe, so it stays in this walk and the network chain
// is only reached when the whole local ladder misses. Off-thread only.
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

// FindOverlayElementIcon is FindOverlayElement for the "icons/<name>" element.
func (t *Theme) FindOverlayElementIcon(misc, name string) (string, bool) {
	return t.FindOverlayElement(misc, overlayFXIconDir+"/"+name)
}

// findOverlayThemeTier walks rungs 1-4 (the "effects/" prefixed element).
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

// OverlayFXPaths returns the existing effects.ini files of each tier, in probe
// order: the THEME tier ("effects/effects.ini", rungs 1-4) and the MISC tier
// ("effects.ini" with the character's folder, rungs 5-11). get_effects reads the
// FIRST of each for the display list; get_effect_property reads ALL of both for
// the properties (text_file_functions.cpp:860-864). Off-thread only.
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

// OverlayFXFiles parses the theme tier: `first` is the FIRST existing file (the
// display-list source) and `all` every existing one in probe order (the property
// pass). A missing or unreadable file is simply absent. Off-thread only (hard
// rule 2) — the theme-apply goroutine is the one caller.
func (t *Theme) OverlayFXFiles() (first []OverlayFX, all [][]OverlayFX) {
	themeTier, _ := t.OverlayFXPaths("")
	return parseOverlayFXPaths(themeTier)
}

// OverlayFXMiscFiles is OverlayFXFiles for the character's misc tier — the LOCAL
// rungs only. The one network rung (misc/<m>/effects.ini at the origin) is the
// UI's, fetched and merged there.
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

// readOverlayFXFile parses one effects.ini from disk under a byte cap.
func readOverlayFXFile(p string) ([]OverlayFX, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseOverlayFX(bufio.NewReader(io.LimitReader(f, overlayFXMaxBytes)))
}
