package themepack

// EXTRACT — turn a bundle into a theme folder, or leave the disk untouched.
// COLLISIONS SUFFIX, THEY DO NOT OVERWRITE. Re-shared themes are renamed
// constantly, so "paper" arriving twice is two themes far more often than it is
// one theme twice — and the cost of guessing wrong in the overwrite direction is
// somebody else's work, silently gone.

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

// Extract imports the bundle at packPath into a new folder under themesRoot.
// BLOCKING — goroutine only (hard rule 2).
func Extract(packPath, themesRoot string) (*Manifest, error) {
	if themesRoot == "" {
		return nil, errors.New("themepack: no themes folder to import into")
	}
	zr, err := openPack(packPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	// EVERY REFUSAL HAPPENS HERE, from metadata, with the destination untouched.
	m, entries, err := inspectReader(zr.r, zr.zipBytes, SanitizeName(stemOf(packPath)))
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(themesRoot, packDirPerm); err != nil {
		return nil, err
	}
	name, err := freeName(themesRoot, m.Root)
	if err != nil {
		return nil, err
	}
	m.Root = name
	final := filepath.Join(themesRoot, name)
	part := final + packPartSuffix
	// A leftover .part from a crashed run is scrap, not data: it was never a
	// theme and nothing has ever read it.
	if err := os.RemoveAll(part); err != nil {
		return nil, err
	}
	if err := writeEntries(zr.r, entries, part); err != nil {
		os.RemoveAll(part) // never leave a half-theme behind
		return nil, err
	}
	if err := os.Rename(part, final); err != nil {
		os.RemoveAll(part)
		return nil, err
	}
	m.Dir = final
	return m, nil
}

// writeEntries materialises the accepted entries under dest.
// THAT INVARIANT IS PINNED, not assumed: TestTheDeclaredSizeIsAHardCeilingOnWhat
// AnEntryCanDeliver drives a forged under-declaring archive through the real
// reader, so a future Go that relaxed the check would fail this package rather
// than silently un-bound its extractor. The per-entry cap below is re-checked
// anyway — it is one comparison, and it is the one line that would hold if that
// gate ever went red.
func writeEntries(zr *zip.Reader, entries []packEntry, dest string) error {
	for _, e := range entries {
		out, err := safepath.Join(dest, e.rel)
		if err != nil {
			return unsafeErr(e.rel)
		}
		if err := os.MkdirAll(filepath.Dir(out), packDirPerm); err != nil {
			return err
		}
		if err := writeOneEntry(zr.File[e.idx], out, e.rel); err != nil {
			return err
		}
	}
	return nil
}

// writeOneEntry copies one member, bounded.
func writeOneEntry(f *zip.File, out, rel string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	defer rc.Close()
	w, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, packFilePerm)
	if err != nil {
		return err
	}
	// One byte past the cap is read so the cap can REFUSE rather than silently
	// truncate: a theme missing the tail of a texture is worse than no theme.
	n, err := io.Copy(w, io.LimitReader(rc, PackEntryBytesCap+1))
	closeErr := w.Close()
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if closeErr != nil {
		return closeErr
	}
	if n > PackEntryBytesCap {
		return capErr("one entry's size in bytes", n, PackEntryBytesCap, rel)
	}
	return nil
}

// freeName returns the first unused folder name under root: base, then
// "base (2)", "base (3)" … up to PackCollisionCap.
// BOUNDED (hard rule 4), and the bound has a behaviour: past it the import
// refuses and names the number, rather than probing forever or overwriting the
// sixteenth copy.
func freeName(root, base string) (string, error) {
	for i := 1; i <= PackCollisionCap; i++ {
		name := base
		if i > 1 {
			name = base + " (" + strconv.Itoa(i) + ")"
		}
		if _, err := os.Stat(filepath.Join(root, name)); errors.Is(err, os.ErrNotExist) {
			return name, nil
		}
	}
	return "", capErr("copies of one theme name", PackCollisionCap+1, PackCollisionCap, base)
}
