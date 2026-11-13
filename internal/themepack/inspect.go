package themepack

// Inspect reads only the central directory, plus one bounded asyncao_theme.ini after every cap,
// so the sheet can show a name and licence.

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

// packEntry is one accepted archive member with its bundle-relative path.
type packEntry struct {
	// idx indexes zip.Reader.File, so Extract never re-matches names.
	idx int
	// rel is forward-slashed and past safepath.UnsafeRel, TooDeep and PackPathLenCap.
	rel string
	// size is the uncompressed size from the central directory.
	size int64
}

// Inspect reports what a bundle holds; blocking, goroutine only (hard rule 2).
func Inspect(name string) (*Manifest, error) {
	zr, err := openPack(name)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	m, _, err := inspectReader(zr.r, zr.zipBytes, SanitizeName(stemOf(name)))
	return m, err
}

// packReader holds a pointer because zip.Reader carries a sync.Once and must not be copied.
type packReader struct {
	r        *zip.Reader
	f        *os.File
	zipBytes int64
}

func (p *packReader) Close() error { return p.f.Close() }

// openPack refuses anything not a zip by its bytes, never its extension.
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
			// Named so a user knows which of their files this is about.
			return nil, errWrap(ErrPackNotABundle, filepath.Base(name))
		}
		return nil, err
	}
	return &packReader{r: zr, f: f, zipBytes: info.Size()}, nil
}

// inspectReader is the acceptance check, shared with Extract so there is no laxer second copy.
func inspectReader(zr *zip.Reader, zipBytes int64, fallbackName string) (*Manifest, []packEntry, error) {
	m := &Manifest{ZipBytes: zipBytes}
	if len(zr.File) > PackEntryCap {
		// Refused from the directory alone, before any entry is touched.
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
		// A re-zip of a theme's files: the folder is named after the archive.
		m.Root = fallbackName
	default:
		m.Root = SanitizeName(root)
	}
	if m.Root == "" {
		return nil, nil, fmt.Errorf("%w: no usable folder name could be made from it", ErrPackNotABundle)
	}

	entries := make([]packEntry, 0, len(zr.File))
	// seen is keyed on the path an entry will be written to, the name that can collide on disk.
	seen := newCaseSet()
	var total int64
	skippedLinks := 0
	for i, f := range zr.File {
		name := normalizeEntryName(f.Name)
		if name == "" || strings.HasSuffix(name, "/") {
			continue // a directory entry: the tree is created from the file paths
		}
		if !f.Mode().IsRegular() {
			// Symlinks are skipped as in safepath.WalkFiles: a link to ~/.ssh/id_rsa would be materialised.
			skippedLinks++
			continue
		}
		if len(name) > PackPathLenCap {
			return nil, nil, capErr("entry path length", int64(len(name)), PackPathLenCap, name)
		}
		// internal/safepath guards, before the root is stripped, so a "../.." behind the root is refused.
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
			// The zip-bomb refusal, against the central directory before decompression.
			return nil, nil, capErr("uncompressed total in bytes", total, PackTotalBytesCap, name)
		}
		rel := name
		if layout == packLayoutRooted {
			rel = strings.TrimPrefix(name, root+"/")
			if rel == name {
				// Outside the single root: refused, as a bundle is one theme and hoisting misstates identity.
				return nil, nil, unsafeErr(name)
			}
		}
		if rel == "" || safepath.UnsafeRel(rel) {
			return nil, nil, unsafeErr(name)
		}
		// Installed under the format's spelling, so the sheet and the loader read the same file.
		if canon, folded := canonicalSidecarRel(rel); folded {
			m.addWarning("%s is spelled %q in this bundle; it installs under the format's own name so it loads on every filesystem",
				sidecarName, rel)
			rel = canon
		}
		// Refused, not collapsed: two entries that are one file would let the last spelling decide the licence.
		// Claimed on rel, quoted as the archive spells it.
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

type packLayout uint8

const (
	packLayoutNone packLayout = iota
	// packLayoutRooted is the published layout (one root folder), what our exporter writes.
	packLayoutRooted
	// packLayoutFlat has a file at the top level, so the archive is the theme folder and takes its name.
	packLayoutFlat
	// packLayoutSplit has several top-level folders and nothing loose; refused rather than guessing.
	packLayoutSplit
)

func packRoot(zr *zip.Reader) (string, packLayout) {
	root, seen, flat, split := "", false, false, false
	for _, f := range zr.File {
		name := normalizeEntryName(f.Name)
		if name == "" || strings.HasSuffix(name, "/") || !f.Mode().IsRegular() {
			continue
		}
		if safepath.UnsafeRel(name) {
			// A hostile name is not part of the shape: "/etc/passwd" would look like a second folder.
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
		// A loose file settles it: the archive is the theme folder, and folders beside it are its fonts/ and misc/.
		return "", packLayoutFlat
	case split:
		return "", packLayoutSplit
	default:
		return root, packLayoutRooted
	}
}

// normalizeEntryName folds backslashes first, as Windows treats them as separators
// and `..\..\evil.dll` would pass a '/'-only check.
func normalizeEntryName(name string) string {
	return strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "./")
}

// describeContents fills the sheet's art and font lists, bounded by theme.MediaCap and theme.FontCap (hard rule 4).
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
				// OFL requires the licence to travel with the face, or whoever re-shares it breaks the terms.
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

// readPackSidecar decompresses the one asyncao_theme.ini, if any, so the sheet can show a name.
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

// unsafeErr names the entry, quoted so a name built to be invisible in a log still reads.
func unsafeErr(name string) error {
	return errWrap(ErrPackUnsafe, name)
}

func isFontFile(lower string) bool {
	return strings.HasSuffix(lower, ".ttf") || strings.HasSuffix(lower, ".otf")
}

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

// packMediaExts are what the sheet calls "media", display only; the decoder sniffs magic bytes at load time.
var packMediaExts = [...]string{".png", ".webp", ".gif", ".jpg", ".jpeg", ".apng", ".avif", ".bmp"}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
