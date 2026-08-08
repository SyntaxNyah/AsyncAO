package themepack

// EXTRACT — turn a bundle into a theme folder, or leave the disk untouched.
//
// ONE concern: getting a stranger's archive onto disk without ever producing a
// half-written theme and without ever writing outside the folder the caller
// chose.
//
// THE ORDER IS THE DESIGN. Every cap and every guard is decided by Inspect,
// against the CENTRAL DIRECTORY, before one byte is decompressed — so a zip bomb,
// a zip-slip name or a 600-entry archive is refused while the destination is
// still empty. Only then does anything get written, and it gets written into a
// `.part` directory beside the final name. The last statement is an os.Rename:
// the theme appears whole or does not appear at all, which is the same
// temp-then-rename shape config's preference saver and the disk cache's writer
// use, and the reason a crash mid-import cannot leave a theme that half-loads.
//
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
//
// themesRoot is the caller's write root and is NEVER derived here: "may I write
// in this directory?" is a question about preferences, portability and the
// catalog, and the package that answers it (internal/config, through
// internal/ui) keeps the answer. This one only refuses to leave whatever it is
// handed.
//
// The returned Manifest names the folder that was actually created — which may
// carry a " (2)" collision suffix — in Root, and its absolute path in Dir.
//
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
//
// EVERY PATH GOES THROUGH safepath.Join — the ONE guard implementation — so a
// name that survived the census by some spelling nobody thought of still cannot
// be joined into a traversal. Belt and braces on purpose: this is the function
// where a mistake writes a file into somebody's home directory.
//
// WHY THE UNCOMPRESSED TOTAL IS NOT RE-COUNTED HERE, and why that is a fact
// about archive/zip rather than an omission.
//
// Inspect decides the zip-bomb refusal from the CENTRAL DIRECTORY, which is
// attacker-controlled metadata, so the obvious worry is an archive that declares
// a demure few kilobytes and then delivers gigabytes — with the aggregate
// bounded only by PackEntryCap × PackEntryBytesCap (512 × 64 MiB = 32 GiB) while
// every individual entry stayed legal. It cannot happen: archive/zip's
// checksumReader refuses on the read that would take an entry PAST its declared
// UncompressedSize64 (reader.go, `if r.nread > r.f.UncompressedSize64`), so no
// entry can ever hand out more bytes than the directory promised. The declared
// total Inspect verified is therefore a true bound on what reaches the disk, and
// a running total here would count to a number that cannot be reached.
//
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
//
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
