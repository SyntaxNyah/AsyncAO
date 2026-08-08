package themepack

// INSPECT — read a bundle's CENTRAL DIRECTORY and nothing else.
//
// ONE concern: "what is in this file, and would I accept it?", answered without
// decompressing a byte.
//
// It is the sleeper primitive of the whole wave. A zip's central directory
// carries every entry's name and UNCOMPRESSED size, so the entire cap census —
// count, per-entry bytes, total bytes, path length, depth, zip-slip, and one file
// per name — is decided from metadata. That is what lets the consent sheet render
// BEFORE anything is written, what makes the zip-bomb refusal a refusal rather
// than a disk-full, and what would make a browse list or a preview card
// affordable later.
//
// THE CENSUS DESCRIBES WHAT WILL LAND, exactly. That is a stronger claim than
// "everything stays inside the folder", and it is the one the consent sheet rests
// on: an archive whose entries would collapse into each other on the recipient's
// filesystem is refused (caseSet), and a native tier spelled in the wrong case is
// folded onto the format's own name (canonicalSidecarRel), so the sidecar this
// file reads out of the archive is the sidecar theme.LoadSidecar reads back off
// the disk — on all three platforms.
//
// The one exception is deliberate and bounded: when the bundle carries an
// asyncao_theme.ini, exactly THAT entry is decompressed (bounded by the format's
// own theme.IniDocByteCap) so the sheet can show a name, an author and a licence
// instead of a folder name. One small entry, chosen by name, after every cap has
// already been checked.

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// packEntry is one accepted archive member, with the bundle-relative path it
// will be written to (i.e. with the single root folder stripped).
type packEntry struct {
	// idx is the index into zip.Reader.File, so Extract never has to match names
	// a second time.
	idx int
	// rel is the path INSIDE the theme folder, forward-slashed and already past
	// every guard: safepath.UnsafeRel, TooDeep and PackPathLenCap.
	rel string
	// size is the uncompressed size from the central directory.
	size int64
}

// Inspect reads path's central directory and reports what a bundle holds.
//
// BLOCKING — goroutine only (hard rule 2). It opens the file read-only, reads
// metadata, and closes it before returning.
func Inspect(name string) (*Manifest, error) {
	zr, err := openPack(name)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	m, _, err := inspectReader(zr.r, zr.zipBytes, SanitizeName(stemOf(name)))
	return m, err
}

// packReader is an open bundle plus its on-disk size, which is the number a user
// actually cares about ("will this fit on a chat message?").
// A POINTER to the zip.Reader, never an embedded value: zip.Reader carries a
// sync.Once, so copying one is a vet failure and a latent double-initialisation.
type packReader struct {
	r        *zip.Reader
	f        *os.File
	zipBytes int64
}

func (p *packReader) Close() error { return p.f.Close() }

// openPack opens a bundle and refuses anything that is not a zip BY ITS BYTES.
// The extension is never the test: a friend who re-zipped the folder sends a
// .zip, and a .aotheme that is really a screenshot must not be trusted because
// of its name (the assets.Sniff doctrine, applied to archives).
func openPack(name string) (*packReader, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		f.Close()
		if errors.Is(err, zip.ErrFormat) {
			// NAMED, not bare: a user with three files in their Downloads folder
			// needs to know which one this is about (design §3.3).
			return nil, errWrap(ErrPackNotABundle, filepath.Base(name))
		}
		return nil, err
	}
	return &packReader{r: zr, f: f, zipBytes: info.Size()}, nil
}

// inspectReader is the census itself, split out so Extract can run it against the
// same reader it is about to extract from — one implementation of "would I accept
// this?", never a second one that could be laxer.
func inspectReader(zr *zip.Reader, zipBytes int64, fallbackName string) (*Manifest, []packEntry, error) {
	m := &Manifest{ZipBytes: zipBytes}
	if len(zr.File) > PackEntryCap {
		// REFUSED FROM THE DIRECTORY ALONE, before a single entry is touched.
		return nil, nil, capErr("entry count", int64(len(zr.File)), PackEntryCap, "")
	}
	root, layout := packRoot(zr)
	switch layout {
	case packLayoutNone:
		return nil, nil, ErrPackEmpty
	case packLayoutSplit:
		return nil, nil, fmt.Errorf("%w: it holds more than one top-level folder, so it is not one theme",
			ErrPackNotABundle)
	case packLayoutFlat:
		// A FRIEND'S RE-ZIP. The files are the theme, so the folder is named after
		// the archive — refusing this shape would fail the one user story the wave
		// exists for ("someone sent me a zip").
		m.Root = fallbackName
	default:
		m.Root = SanitizeName(root)
	}
	if m.Root == "" {
		return nil, nil, fmt.Errorf("%w: no usable folder name could be made from it", ErrPackNotABundle)
	}

	entries := make([]packEntry, 0, len(zr.File))
	// seen is the "one file per name" guard. It is keyed on the path an entry will
	// be WRITTEN to (after the root is stripped and the sidecar's case is folded),
	// because that is the name that can collide on disk.
	seen := newCaseSet()
	var total int64
	skippedLinks := 0
	for i, f := range zr.File {
		name := normalizeEntryName(f.Name)
		if name == "" || strings.HasSuffix(name, "/") {
			continue // a directory entry: the tree is created from the file paths
		}
		if !f.Mode().IsRegular() {
			// SYMLINKS ARE SKIPPED, exactly as safepath.WalkFiles skips them on the
			// folder side: a link at fonts/x.ttf pointing at ~/.ssh/id_rsa would
			// otherwise be materialised — or, worse, followed by the next writer.
			skippedLinks++
			continue
		}
		if len(name) > PackPathLenCap {
			return nil, nil, capErr("entry path length", int64(len(name)), PackPathLenCap, name)
		}
		// THE GUARDS, from the one implementation (internal/safepath). Applied to the
		// ARCHIVE'S OWN name before the root is stripped, so a "../.." that hides
		// behind the root folder is refused too.
		if safepath.UnsafeRel(name) {
			return nil, nil, unsafeErr(name)
		}
		if safepath.TooDeep(name, packMaxDepth) {
			return nil, nil, capErr("entry depth", int64(strings.Count(name, "/")), packMaxDepth, name)
		}
		size := int64(f.UncompressedSize64)
		if size > PackEntryBytesCap {
			return nil, nil, capErr("one entry's size in bytes", size, PackEntryBytesCap, name)
		}
		total += size
		if total > PackTotalBytesCap {
			// THE ZIP-BOMB REFUSAL, and it happens here — against the central
			// directory, before anything is decompressed.
			return nil, nil, capErr("uncompressed total in bytes", total, PackTotalBytesCap, name)
		}
		rel := name
		if layout == packLayoutRooted {
			rel = strings.TrimPrefix(name, root+"/")
			if rel == name {
				// An entry outside the single root folder. Refused rather than hoisted:
				// a bundle is ONE theme (§2.5), and quietly importing a second folder's
				// worth of files under the first folder's name is a lie about identity.
				return nil, nil, unsafeErr(name)
			}
		}
		if rel == "" || safepath.UnsafeRel(rel) {
			return nil, nil, unsafeErr(name)
		}
		// The native tier is installed under the format's own spelling, so the sheet
		// below and the loader on disk read the same file (canonicalSidecarRel).
		if canon, folded := canonicalSidecarRel(rel); folded {
			m.addWarning("%s is spelled %q in this bundle; it installs under the format's own name so it loads on every filesystem",
				sidecarName, rel)
			rel = canon
		}
		// ONE FILE PER NAME. Refused rather than collapsed: two entries that are one
		// file on the recipient's disk would make Files count a file that is not
		// there, and would let the LAST spelling decide what the theme claims about
		// its author and its licence.
		//
		// CLAIMED ON rel AND QUOTED AS name: the collision is decided by the path that
		// will be written, and the refusal names the entry the way the ARCHIVE spells
		// it — root folder included — because that is the name the user has to find in
		// their zip and rename.
		if err := seen.claim(rel, name); err != nil {
			return nil, nil, err
		}
		entries = append(entries, packEntry{idx: i, rel: rel, size: size})
	}
	if len(entries) == 0 {
		return nil, nil, ErrPackEmpty
	}
	if skippedLinks > 0 {
		m.addWarning("%d entr%s skipped: a theme bundle may not carry links",
			skippedLinks, plural(skippedLinks, "y was", "ies were"))
	}
	m.Files = len(entries)
	m.Bytes = total
	describeContents(m, entries)
	readPackSidecar(m, zr, entries)
	return m, entries, nil
}

// packLayout is the SHAPE of an archive — the question "where is the theme?".
type packLayout uint8

const (
	// packLayoutNone: not one usable file. Refused as empty.
	packLayoutNone packLayout = iota
	// packLayoutRooted: the published layout (§2.5) — one root folder, everything
	// inside it. What our own exporter writes.
	packLayoutRooted
	// packLayoutFlat: at least one file at the TOP LEVEL, so the archive itself is
	// the theme folder. A friend who selected the files inside a theme and pressed
	// "compress" produces this, and refusing it would fail the one user story the
	// wave exists for. The folder is then named after the archive.
	packLayoutFlat
	// packLayoutSplit: two or more top-level folders and nothing loose. Refused —
	// a bundle is ONE theme, and picking one of the folders for the user would be
	// guessing at which of somebody's themes they meant.
	packLayoutSplit
)

// packRoot classifies an archive and, for the rooted shape, names the folder.
func packRoot(zr *zip.Reader) (string, packLayout) {
	root, seen, flat, split := "", false, false, false
	for _, f := range zr.File {
		name := normalizeEntryName(f.Name)
		if name == "" || strings.HasSuffix(name, "/") || !f.Mode().IsRegular() {
			continue
		}
		if safepath.UnsafeRel(name) {
			// A HOSTILE NAME IS NOT PART OF THE SHAPE. "/etc/passwd" has an empty
			// first segment and would otherwise read as a SECOND top-level folder,
			// so the archive would be refused as "more than one theme" instead of as
			// the traversal attempt it is — a true refusal with a false reason, which
			// is how a security guard gets "simplified" away by somebody who read
			// only the message. The main loop refuses it by name.
			continue
		}
		seen = true
		first, rest, hasSlash := strings.Cut(name, "/")
		if !hasSlash || rest == "" {
			flat = true
			continue
		}
		if root == "" {
			root = first
		} else if first != root {
			split = true
		}
	}
	switch {
	case !seen:
		return "", packLayoutNone
	case flat:
		// A loose file at the root settles it even when folders sit beside it: the
		// archive IS the theme folder, and those folders are its fonts/ and misc/.
		return "", packLayoutFlat
	case split:
		return "", packLayoutSplit
	default:
		return root, packLayoutRooted
	}
}

// normalizeEntryName folds an archive entry name into the one spelling every
// guard below is written against: forward slashes, no "./" prefix.
//
// BACKSLASHES ARE FOLDED FIRST and that is not cosmetic. A zip entry name is
// '/'-separated by the format, but the name is ATTACKER-CONTROLLED metadata and
// Windows treats '\' as a separator too — an entry called `..\..\evil.dll` would
// otherwise sail past a '/'-only ".." check and be joined into a real traversal.
// safepath.UnsafeRel and TooDeep both count either separator for the same reason;
// folding here means the root-stripping and length rules see it too.
func normalizeEntryName(name string) string {
	return strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "./")
}

// describeContents fills the two lists the consent sheet shows: what art comes
// with the theme, and what typefaces — with whether their licence travels along.
//
// BOTH BOUNDED (hard rule 4): theme.MediaCap and theme.FontCap are the format's
// own manifest bounds, reused rather than restated so the sheet cannot claim more
// entries than a sidecar could ever declare.
func describeContents(m *Manifest, entries []packEntry) {
	licenses := make(map[string]string, theme.FontCap)
	for _, e := range entries {
		lower := strings.ToLower(e.rel)
		if strings.HasPrefix(lower, packFontDir) && looksLikeLicense(lower) {
			licenses[path.Dir(e.rel)] = e.rel
		}
	}
	for _, e := range entries {
		lower := strings.ToLower(e.rel)
		switch {
		case strings.HasPrefix(lower, packFontDir) && isFontFile(lower):
			if len(m.Fonts) >= theme.FontCap {
				continue
			}
			fe := FontEntry{File: e.rel, LicenseFile: licenses[path.Dir(e.rel)]}
			m.Fonts = append(m.Fonts, fe)
			if fe.LicenseFile == "" {
				// NAMED, NEVER SILENT. OFL requires the licence to travel with the
				// face, and a bundle that quietly omits it makes the person who
				// re-shares it the one breaking the terms.
				m.addWarning("%s has no licence file beside it — fonts you did not make may not be redistributable", e.rel)
			}
		case isMediaFile(lower):
			if len(m.Media) >= theme.MediaCap {
				continue
			}
			m.Media = append(m.Media, e.rel)
		}
	}
}

// readPackSidecar decompresses the ONE entry named asyncao_theme.ini, if the
// bundle has one, so the sheet can show a name rather than a folder.
//
// BOUNDED BY THE FORMAT'S OWN CAP (theme.IniDocByteCap) and read through an
// io.LimitReader, so a central directory that lies about the size cannot make
// this allocate more than the format would ever accept. A parse failure is a
// WARNING, never a refusal: the theme is still importable, the sheet just has
// nothing to say about it.
//
// THE EXACT-NAME MATCH IS SOUND BY CONSTRUCTION, and only by construction: the
// census folded a mis-cased spelling onto sidecarName and refused a second entry
// that differed from it only in case, so at most one entry can be the theme's
// native tier and it is spelled the way the format spells it. Both of those are
// load-bearing here — without them, the entry this reads and the file
// theme.LoadSidecar opens after extraction are two different files, and this
// function is what puts the theme's name, author and licence on the consent line.
func readPackSidecar(m *Manifest, zr *zip.Reader, entries []packEntry) {
	for _, e := range entries {
		if e.rel != sidecarName {
			continue
		}
		rc, err := zr.File[e.idx].Open()
		if err != nil {
			m.addWarning("%s could not be read: %v", sidecarName, err)
			return
		}
		src, err := io.ReadAll(io.LimitReader(rc, theme.IniDocByteCap+1))
		rc.Close()
		if err != nil {
			m.addWarning("%s could not be read: %v", sidecarName, err)
			return
		}
		sc, err := theme.ParseSidecar(src)
		if err != nil {
			m.addWarning("%s will not load: %v", sidecarName, err)
			return
		}
		m.Name, m.Author, m.License, m.Format = sc.Meta.Name, sc.Meta.Author, sc.Meta.License, sc.Format
		return
	}
	m.addWarning("no %s — this is a plain AO2 theme, which imports fine but has no name or licence of its own", sidecarName)
}

// unsafeErr names the offending entry. The path is quoted so a name built to be
// invisible in a log (trailing spaces, control characters) still reads as one.
func unsafeErr(name string) error {
	return errWrap(ErrPackUnsafe, name)
}

func isFontFile(lower string) bool {
	return strings.HasSuffix(lower, ".ttf") || strings.HasSuffix(lower, ".otf")
}

// looksLikeLicense reports whether an already-lowered path is a licence file.
func looksLikeLicense(lower string) bool {
	if isFontFile(lower) {
		return false // a face is never its own licence
	}
	for _, hint := range packLicenseHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func isMediaFile(lower string) bool {
	for _, ext := range packMediaExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// packMediaExts are the image extensions the sheet calls "media". Display only —
// the decoder decides what it can actually read, by magic bytes, at load time.
var packMediaExts = [...]string{".png", ".webp", ".gif", ".jpg", ".jpeg", ".apng", ".avif", ".bmp"}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
