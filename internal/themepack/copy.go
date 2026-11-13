package themepack

// Copy for editing is not a zip round trip, which would report bundle caps; the original stays read-only.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// CopyForEditing copies srcDir under themesRoot and stamps [import]; goroutine only (hard rule 2).
func CopyForEditing(srcDir, themesRoot, name string) (*Manifest, error) {
	entries, m, err := scanFolder(srcDir)
	if err != nil {
		return nil, err
	}
	if themesRoot == "" {
		return nil, errors.New("themepack: no themes folder to copy into")
	}
	want := SanitizeName(name)
	if want == "" {
		want = m.Root
	}
	if err := os.MkdirAll(themesRoot, packDirPerm); err != nil {
		return nil, err
	}
	final, err := freeName(themesRoot, want)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(themesRoot, final)
	part := dest + packPartSuffix
	if err := os.RemoveAll(part); err != nil {
		return nil, err
	}
	if err := copyEntries(srcDir, part, entries); err != nil {
		os.RemoveAll(part)
		return nil, err
	}
	// The source's spelling, so the stamp lands in the file the copy has rather than beside it.
	if err := stampProvenance(part, filepath.Base(srcDir), foundSidecarRel(entries)); err != nil {
		os.RemoveAll(part)
		return nil, err
	}
	if err := os.Rename(part, dest); err != nil {
		os.RemoveAll(part)
		return nil, err
	}
	m.Root, m.Dir = final, dest
	return m, nil
}

// copyEntries goes through safepath.Join, so a hostile name after the scan still cannot leave dest.
func copyEntries(srcDir, dest string, entries []srcEntry) error {
	for _, e := range entries {
		from, err := safepath.Join(srcDir, e.rel)
		if err != nil {
			return unsafeErr(e.rel)
		}
		to, err := safepath.Join(dest, e.rel)
		if err != nil {
			return unsafeErr(e.rel)
		}
		if err := os.MkdirAll(filepath.Dir(to), packDirPerm); err != nil {
			return err
		}
		if err := copyOneFile(from, to, e.rel); err != nil {
			return err
		}
	}
	return nil
}

func copyOneFile(from, to, rel string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, packFilePerm)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(in, PackEntryBytesCap+1))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n > PackEntryBytesCap {
		return capErr("one entry's size in bytes", n, PackEntryBytesCap, rel)
	}
	return nil
}

// foundSidecarRel is the source's sidecar spelling, or ""; shared so both callers agree on the entry.
func foundSidecarRel(entries []srcEntry) string {
	for _, e := range entries {
		if strings.EqualFold(e.rel, sidecarName) {
			return e.rel
		}
	}
	return ""
}

// stampProvenance writes [import] derived_from, derived_at and derived_hash, creating a sidecar if needed.
func stampProvenance(dir, from, rel string) error {
	if rel == "" {
		rel = sidecarName // the source had none; the copy gets one under the format's name
	}
	path := filepath.Join(dir, filepath.FromSlash(rel))
	sc, err := loadSidecarFile(path)
	if err != nil {
		// An unreadable sidecar is the author's and left alone; losing the stamp is the smaller harm.
		return nil
	}
	if sc == nil {
		sc = theme.NewSidecar()
	}
	sc.Import.DerivedFrom = from
	sc.Import.DerivedAt = nowStamp()
	sc.Import.DerivedHash = designHash(dir)
	b, err := sc.Bytes()
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, packFilePerm)
}
