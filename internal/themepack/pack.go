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

const (
	// PackExt is the bundle extension; Inspect sniffs the zip magic and never trusts it.
	PackExt = ".aotheme"

	// PackEntryCap bounds entries in one bundle, refusing an archive that is a file system rather than a theme.
	PackEntryCap = 512

	// PackTotalBytesCap bounds the uncompressed total from the central directory: the zip-bomb refusal.
	PackTotalBytesCap = 192 << 20

	// PackEntryBytesCap bounds one entry, matching assets.mountZipEntryMaxBytes (both from strangers).
	PackEntryBytesCap = 64 << 20

	// PackPathLenCap bounds an entry's path: MAX_PATH of 260 covers the whole path including destination.
	PackPathLenCap = 180

	// PackCollisionCap bounds the " (2)", " (3)" suffixes tried when a name is taken (hard rule 4).
	PackCollisionCap = 16

	// packPartSuffix names the in-progress destination, beside the final name so the rename is atomic.
	packPartSuffix = ".aotheme-part"

	// packDirPerm and packFilePerm apply on extraction; a stranger's executable bit is dropped.
	packDirPerm  = 0o755
	packFilePerm = 0o644

	// packFontDir is where a theme's faces live, and the only directory the licence probe checks.
	packFontDir = "fonts/"
)

// packLicenseHints mark a font's licence, which OFL requires to travel with the face.
// "ofl" catches "<Face>-OFL.txt", which has neither "license" nor "licence".
var packLicenseHints = [...]string{
	"licen",   // LICENSE, LICENCE, Court-License.txt
	"ofl",     // <Face>-OFL.txt — the SIL Open Font License's usual spelling
	"copying", // the GNU convention, which some faces still ship under
}

// packMaxDepth is safepath's bound, so theme and mount guards share one number.
const packMaxDepth = safepath.MaxDepth

var (
	// ErrPackCap reports a bundle past a named cap; distinct from ErrPackUnsafe because the fix differs.
	ErrPackCap = errors.New("themepack: bundle is past a named limit")

	// ErrPackUnsafe reports an entry that could escape; a refusal, so a hostile name is not an absent file.
	ErrPackUnsafe = errors.New("themepack: an entry would be written outside the theme folder")

	// ErrPackNotABundle reports an archive this build will not treat as one theme.
	ErrPackNotABundle = errors.New("themepack: not a zip archive")

	// ErrPackEmpty reports an archive with no usable entry, a different answer from "not a zip".
	ErrPackEmpty = errors.New("themepack: the archive holds no theme files")
)

type FontEntry struct {
	File string
	// LicenseFile is a licence-looking file beside it, or "" when none, which the export sheet warns about.
	LicenseFile string
}

// Manifest is what a user sees before committing; a value, so no draw path holds a file (hard rule 2).
type Manifest struct {
	// Root is the sanitized single root folder, the theme's identity; after Extract it may carry a " (2)" suffix.
	Root string
	// Dir is the destination on disk ("" for a bare Inspect).
	Dir string
	// Name, Author, License and Format come from the bundle's asyncao_theme.ini; display only.
	Name    string
	Author  string
	License string
	Format  int
	// Bytes is the uncompressed total; ZipBytes is what gets attached to a chat message.
	Files    int
	Bytes    int64
	ZipBytes int64
	// Media and Fonts are what a user must see: the art taken on, typefaces they may not pass on.
	Media []string
	Fonts []FontEntry
	// Warnings are named non-fatal problems: a font with no licence, no sidecar, skipped symlinks.
	Warnings []string
}

// HasSidecar reports a native tier; without one the bundle is a plain AO2 theme, which still imports.
func (m *Manifest) HasSidecar() bool { return m != nil && m.Format > 0 }

// addWarning records a non-fatal problem, bounded by PackEntryCap (hard rule 4).
func (m *Manifest) addWarning(format string, args ...any) {
	if len(m.Warnings) >= PackEntryCap {
		return
	}
	m.Warnings = append(m.Warnings, fmt.Sprintf(format, args...))
}

// capErr is the one spelling of a cap refusal, naming the offender, the number and the limit.
func capErr(what string, got, limit int64, offender string) error {
	if offender == "" {
		return fmt.Errorf("%w: %s is %d, the limit is %d", ErrPackCap, what, got, limit)
	}
	return fmt.Errorf("%w: %s is %d, the limit is %d (at %q)", ErrPackCap, what, got, limit, offender)
}

// errWrap names the entry in a sentinel error, quoted so a name built to be invisible in a log still reads.
func errWrap(sentinel error, name string) error {
	return fmt.Errorf("%w: %q", sentinel, name)
}

// caseCollisionErr names both spellings, since the pair is what explains the refusal.
func caseCollisionErr(first, second string) error {
	return fmt.Errorf("%w: %q and %q differ only in case, so on Windows and macOS they are ONE file "+
		"and one would silently replace the other — rename one of them and pack it again",
		ErrPackNotABundle, first, second)
}

// sidecarName comes from internal/theme so transport and format cannot disagree.
const sidecarName = theme.SidecarFileName

// NameRuneCap bounds a theme folder name, far under PackPathLenCap so a legal name cannot push an entry over it.
const NameRuneCap = 48

// SanitizeName makes a lowercase folder name, as "Paper" and "paper" collide on some filesystems.
func SanitizeName(s string) string {
	var b []rune
	dot := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '(', r == ')':
			// Parentheses are legal: " (2)" and "<name> (edited)" use them, or a second import would collide.
			b = append(b, r)
			dot = false
		case r == '-' || r == '_' || r == ' ':
			// Never first and never doubled: a folder called " - " is legal and unusable.
			if len(b) > 0 && b[len(b)-1] != '-' && b[len(b)-1] != '_' && b[len(b)-1] != ' ' {
				b = append(b, r)
			}
			dot = false
		case r == '.':
			// A leading dot hides the folder on unix and two are a traversal segment; refused where names are made.
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

// stemOf is a file's name without extension, how a flat archive gets a folder name.
func stemOf(p string) string {
	base := filepath.Base(p)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// packCaseSetHint sizes the collision map for an ordinary theme; a hint, since PackEntryCap is the bound.
const packCaseSetHint = 64

// caseSet detects paths equal on a case-insensitive filesystem; macOS's Unicode normalisation folding is not modelled.
type caseSet struct {
	// seen maps the lowered path to the claiming spelling, so a refusal can name the pair.
	seen map[string]string
}

func newCaseSet() *caseSet {
	return &caseSet{seen: make(map[string]string, packCaseSetHint)}
}

// claim records rel, or refuses when an earlier entry differs only in case.
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

// canonicalSidecarRel folds a mis-cased root-level sidecar, so it also loads on Linux.
func canonicalSidecarRel(rel string) (string, bool) {
	if rel != sidecarName && strings.EqualFold(rel, sidecarName) {
		return sidecarName, true
	}
	return rel, false
}
