package themepack

// COPY FOR EDITING — "editing makes your own copy".
//
// ONE concern: reproducing somebody else's theme folder inside the write root, so
// that everything the editor does afterwards lands on a folder we own.
//
// WHY IT IS NOT A ZIP ROUND TRIP. Write-then-Extract would give the same tree and
// would also compress and decompress every byte for no reason, and would report a
// bundle's caps ("this .aotheme is too big") for a folder nobody is bundling. The
// caps are the same, the guards are the same, and the walk is the same
// safepath.WalkFiles — only the destination differs.
//
// NOTHING IS SKIPPED. A theme's unknown files are its author's business; a copy
// that dropped what this build did not recognise would silently make the copy
// worse than the original, and the user would find out when they exported it.
//
// THE ORIGINAL IS NEVER OPENED FOR WRITING. Every path on the source side is
// read-only, and that is what TestOriginalThemeIsNeverMutatedByEditing hashes.

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// CopyForEditing copies srcDir into a new folder under themesRoot and stamps the
// copy's [import] provenance.
//
// name is the folder the user asked for; "" takes the source's own name. It is
// sanitized and collision-suffixed exactly as an import is, because it is one —
// the bytes just arrive over a shorter wire.
//
// PROVENANCE IS WRITTEN, and it is four lines of format that buy three things:
// tamper/corruption detection on a re-shared copy, an answerable "upstream
// updated — what changed?", and — socially the biggest — CREDIT THAT SURVIVES
// RE-SHARE. A copy that forgot where it came from is how an ecosystem loses its
// authors.
//
// BLOCKING — goroutine only (hard rule 2).
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
	if err := stampProvenance(part, filepath.Base(srcDir)); err != nil {
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

// copyEntries reproduces the tree. Every destination goes through safepath.Join,
// so a source that grew a hostile name between the scan and the copy still cannot
// be written outside dest.
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

// stampProvenance writes [import] derived_from / derived_at / derived_hash into
// the copy's sidecar, creating one when the source had none.
//
// THE HASH IS OF courtroom_design.ini — the file the upstream author actually
// maintains — using the same xxhash the disk cache keys by, so "has upstream
// changed?" is one cheap comparison rather than a diff.
//
// IT GOES THROUGH Sidecar.Bytes, which means it goes through the WRITE GUARD: a
// provenance stamp cannot be the thing that produces a theme the reader refuses.
func stampProvenance(dir, from string) error {
	path := filepath.Join(dir, sidecarName)
	sc, err := loadSidecarFile(path)
	if err != nil {
		// A sidecar this build cannot read is the AUTHOR's file and is left exactly
		// as it is. Losing the provenance stamp is a smaller harm than rewriting a
		// stranger's native tier from a model we could not parse.
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
