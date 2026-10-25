// Package themepack moves a theme FOLDER in and out of one shareable file.
package themepack

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Named caps (hard rule 9) — docs/wip/THEME-EDITOR-DESIGN.md §Q4
// ---------------------------------------------------------------------------

const (
	// PackExt is the bundle extension. The IMPORTER never trusts it — Inspect
	// sniffs the zip's own magic — but the exporter writes it and the drop router
	// claims it, so it has to be spelled once.
	PackExt = ".aotheme"

	// PackEntryCap bounds the entries in one bundle. A dense theme is roughly
	// ninety skins plus fonts and media; 512 leaves room for a lavish one and
	// still refuses an archive that is a file system rather than a theme.
	PackEntryCap = 512

	// PackTotalBytesCap bounds the UNCOMPRESSED total, checked against the central
	// directory BEFORE any extraction — this is the zip-bomb refusal. 192 MiB is
	// far past any real theme (the largest in the reference corpus is ~30 MiB) and
	// far under a budget that could fill a disk.
	PackTotalBytesCap = 192 << 20

	// PackEntryBytesCap bounds one entry, uncompressed. It mirrors
	// assets.mountZipEntryMaxBytes deliberately: the two archives arrive from the
	// same place (a stranger on the internet) and there is no reason for one to
	// trust a bigger file than the other.
	PackEntryBytesCap = 64 << 20

	// PackPathLenCap bounds one entry's path. Windows' classic MAX_PATH is 260 for
	// the WHOLE path, and the destination directory eats most of it, so a bundle
	// whose own names are longer than this cannot be extracted anywhere useful —
	// better to say so by name than to fail per-file half way through.
	PackPathLenCap = 180

	// PackCollisionCap bounds the " (2)", " (3)" … suffixes tried when the
	// destination name is taken. Past it the import refuses and says so: a user
	// with sixteen copies of one theme has a different problem, and an unbounded
	// probe loop is a hard-rule-4 violation wearing a for-statement.
	PackCollisionCap = 16

	// packPartSuffix names the in-progress destination. It sits BESIDE the final
	// name so the rename that publishes it is atomic (same filesystem).
	packPartSuffix = ".aotheme-part"

	// packDirPerm / packFilePerm are the permissions extraction creates with —
	// the same 0755 / 0644 every other directory and file this client writes uses.
	packDirPerm  = 0o755
	packFilePerm = 0o644

	// packFontDir is where a theme's faces live (theme.SidecarFontPath's own
	// convention), and the only directory the licence probe looks in.
	packFontDir = "fonts/"
)

// packLicenseHints are the substrings that make a file beside a font look like
// its licence. OFL specifically requires the licence to travel with the face, so
// a bundled font with no licence file beside it is a NAMED warning rather than a
// silent redistribution — and the probe has to recognise what people actually
// name these files.
var packLicenseHints = [...]string{
	"licen",   // LICENSE, LICENCE, Court-License.txt
	"ofl",     // <Face>-OFL.txt — the SIL Open Font License's usual spelling
	"copying", // the GNU convention, which some faces still ship under
}

// packMaxDepth is the directory recursion bound, taken from safepath rather than
// restated: the theme guards and the mount guards are one implementation and one
// number (see the package comment).
const packMaxDepth = safepath.MaxDepth

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	// ErrPackCap reports a bundle past one of the named caps above. Distinct from
	// ErrPackUnsafe because the fix differs: this one says "this is too big", the
	// other says "this archive is trying to write outside the folder you chose".
	ErrPackCap = errors.New("themepack: bundle is past a named limit")

	// ErrPackUnsafe reports an entry that could escape its destination — zip-slip,
	// an absolute name, a ".." segment, a drive-relative name. Surfaced as a
	// refusal and never as a skip: a hostile name must not look like an absent file.
	ErrPackUnsafe = errors.New("themepack: an entry would be written outside the theme folder")

	// ErrPackNotABundle reports an archive this build will not treat as ONE theme.
	// Three shapes reach it, and they share an answer ("this file is not a theme
	// bundle; fix it where it was made"):
	ErrPackNotABundle = errors.New("themepack: not a zip archive")

	// ErrPackEmpty reports an archive with no usable entry — every one of them was
	// a directory, a symlink or refused. It is separate from ErrPackNotABundle
	// because the user-facing answer differs ("this zip holds no files" vs "this
	// is not a zip").
	ErrPackEmpty = errors.New("themepack: the archive holds no theme files")
)

// ---------------------------------------------------------------------------
// Manifest — what the consent sheet renders BEFORE anything is written
// ---------------------------------------------------------------------------

// FontEntry is one bundled face and whether its licence travels with it.
type FontEntry struct {
	// File is the bundle-relative path of the face.
	File string
	// LicenseFile is a licence-looking file beside it, or "" when there is none —
	// which is the case the export sheet warns about.
	LicenseFile string
}

// Manifest is everything a user can be shown about a bundle before committing to
// it, plus what Extract/Write/CopyForEditing actually did.
type Manifest struct {
	// Root is the bundle's single root folder — the theme's IDENTITY
	// (docs/THEME-FORMAT.md §1), sanitized. After Extract it is the folder that
	// was actually created, which may carry a " (2)" collision suffix.
	Root string
	// Dir is the destination on disk, once one exists ("" for a bare Inspect).
	Dir string
	// Name / Author / License / Format come from the bundle's own
	// asyncao_theme.ini when it has one. Display only.
	Name    string
	Author  string
	License string
	Format  int
	// Files / Bytes are the entries and their UNCOMPRESSED total. ZipBytes is what
	// actually gets attached to a chat message, which is the number that decides
	// whether a theme is shareable at all.
	Files    int
	Bytes    int64
	ZipBytes int64
	// Media / Fonts are the two lists a user needs to see before agreeing: what art
	// they are taking on, and what typefaces they may not be allowed to pass on.
	Media []string
	Fonts []FontEntry
	// Warnings are the named, non-fatal problems: a font with no licence file, a
	// bundle with no sidecar, entries skipped because they were symlinks.
	Warnings []string
}

// HasSidecar reports whether the bundle carried a native tier. A bundle without
// one is a plain AO2 theme, which imports perfectly well — the sheet just has no
// name or licence to show.
func (m *Manifest) HasSidecar() bool { return m != nil && m.Format > 0 }

// addWarning records a named non-fatal problem, bounded by PackEntryCap so a
// pathological archive cannot grow the list without limit (hard rule 4).
func (m *Manifest) addWarning(format string, args ...any) {
	if len(m.Warnings) >= PackEntryCap {
		return
	}
	m.Warnings = append(m.Warnings, fmt.Sprintf(format, args...))
}

// capErr is the one spelling of a cap refusal, so every one of them names the
// offender, the number and the limit (design §3.3).
func capErr(what string, got, limit int64, offender string) error {
	if offender == "" {
		return fmt.Errorf("%w: %s is %d, the limit is %d", ErrPackCap, what, got, limit)
	}
	return fmt.Errorf("%w: %s is %d, the limit is %d (at %q)", ErrPackCap, what, got, limit, offender)
}

// errWrap names the offending entry in a sentinel error. The path is quoted so a
// name built to be invisible in a log (trailing spaces, control characters) still
// reads as one.
func errWrap(sentinel error, name string) error {
	return fmt.Errorf("%w: %q", sentinel, name)
}

// caseCollisionErr names BOTH spellings, because "one of these two files" is not
// something a user can act on — and because the pair IS the evidence: the two
// names side by side are the whole explanation of why a bundle that looks fine on
// the machine it was made on is refused here.
func caseCollisionErr(first, second string) error {
	return fmt.Errorf("%w: %q and %q differ only in case, so on Windows and macOS they are ONE file "+
		"and one would silently replace the other — rename one of them and pack it again",
		ErrPackNotABundle, first, second)
}

// sidecarName is the file Inspect reads for the display metadata. Taken from
// internal/theme rather than spelled again: the transport must not be able to
// disagree with the format about what a theme's native tier is called.
const sidecarName = theme.SidecarFileName

// ---------------------------------------------------------------------------
// Identity — the folder name
// ---------------------------------------------------------------------------

// NameRuneCap bounds a theme folder name. It is deliberately far under
// PackPathLenCap so a legal name can never be the reason an entry inside it is
// over the path bound.
const NameRuneCap = 48

// SanitizeName turns any string into a folder name this client is willing to
// create: lowercase, and only the characters a theme folder has ever needed.
func SanitizeName(s string) string {
	var b []rune
	dot := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '(', r == ')':
			// PARENTHESES ARE LEGAL, and not as a nicety: the collision suffix IS
			// " (2)" and the copy-for-editing sheet pre-fills "<name> (edited)", so a
			// sanitizer that dropped them would rename every second import of a theme
			// to the same string as the first and then refuse it as taken.
			b = append(b, r)
			dot = false
		case r == '-' || r == '_' || r == ' ':
			// Never as the first character, and never doubled: a folder called
			// " - " is legal and unusable.
			if len(b) > 0 && b[len(b)-1] != '-' && b[len(b)-1] != '_' && b[len(b)-1] != ' ' {
				b = append(b, r)
			}
			dot = false
		case r == '.':
			// A leading dot hides the folder on unix; two of them are a traversal
			// segment. Both are refused here rather than by safepath, because this
			// is the one place a NAME is invented rather than checked.
			if len(b) > 0 && !dot {
				b = append(b, r)
			}
			dot = true
		}
		if len(b) >= NameRuneCap {
			break
		}
	}
	out := strings.TrimRight(string(b), " -_.")
	if safepath.UnsafeRel(out) {
		return "" // belt and braces: the ONE guard implementation gets the last word
	}
	return out
}

// stemOf is a file's name without its extension — how a flat archive gets a
// folder name.
func stemOf(p string) string {
	base := filepath.Base(p)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// ---------------------------------------------------------------------------
// Identity — ONE FILE PER NAME, on every filesystem this ships on
// ---------------------------------------------------------------------------

// packCaseSetHint sizes the collision map for an ordinary theme (a dense one is
// roughly ninety skins plus fonts and media, and most are far smaller), so the
// common bundle never rehashes. It is a HINT, not a bound: the bound is
// PackEntryCap, which caseSet.claim enforces.
const packCaseSetHint = 64

// caseSet answers "have I already accepted an entry that IS this path, on a
// filesystem that does not care about case?".
type caseSet struct {
	// seen maps the lowered path to the SOURCE spelling that claimed it, so a
	// refusal can name the pair rather than only the newcomer.
	seen map[string]string
}

func newCaseSet() *caseSet {
	return &caseSet{seen: make(map[string]string, packCaseSetHint)}
}

// claim records rel, or refuses when an earlier entry differs from it only in
// case.
func (c *caseSet) claim(rel, spelled string) error {
	if len(c.seen) >= PackEntryCap {
		return capErr("entry count", int64(len(c.seen))+1, PackEntryCap, spelled)
	}
	key := strings.ToLower(rel)
	if first, ok := c.seen[key]; ok {
		return caseCollisionErr(first, spelled)
	}
	c.seen[key] = spelled
	return nil
}

// canonicalSidecarRel folds a mis-cased native tier onto the format's own
// spelling, and reports whether it did.
// Installing it under the canonical name closes that on ALL THREE platforms
// (including Linux, where the shouted spelling would otherwise never load at
// all), costs the recipient nothing — the format has exactly one spelling for
// this file — and is NAMED in a warning rather than done quietly. Only the
// theme's own root-level tier folds: `sub/ASYNCAO_THEME.INI` is somebody's
// ordinary file and is left exactly as it is.
func canonicalSidecarRel(rel string) (string, bool) {
	if rel != sidecarName && strings.EqualFold(rel, sidecarName) {
		return sidecarName, true
	}
	return rel, false
}
