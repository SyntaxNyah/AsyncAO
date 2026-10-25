package themepack

// COPY FOR EDITING — "editing makes your own copy".
// WHY IT IS NOT A ZIP ROUND TRIP. Write-then-Extract would give the same tree and
// would also compress and decompress every byte for no reason, and would report a
// bundle's caps ("this .aotheme is too big") for a folder nobody is bundling. The
// caps are the same, the guards are the same, and the walk is the same
// safepath.WalkFiles — only the destination differs.
// THE ORIGINAL IS NEVER OPENED FOR WRITING. Every path on the source side is
// read-only, and that is what TestCopyForEditingLeavesTheOriginalUntouched
// hashes (pack_test.go). The name that used to stand here —
// TestOriginalThemeIsNeverMutatedByEditing — is the DESIGN document's name for
// the gate and has never been a function in this tree; a comment pointing at a
// test that does not exist is a claim nobody can check by grepping for it.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// CopyForEditing copies srcDir into a new folder under themesRoot and stamps the
// copy's [import] provenance.
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
	// The spelling the SOURCE actually used, carried across so the stamp lands in
	// the file the copy has rather than beside it — see stampProvenance.
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

// foundSidecarRel is the spelling the SOURCE FOLDER used for its native tier, or
// "" when it had none. Case-folded, because that is the question: on a
// case-sensitive filesystem `ASYNCAO_THEME.INI` is a different file from
// `asyncao_theme.ini`, and the copy has whichever one the source had.
// It is readFolderSidecar's own probe, factored out rather than repeated: the two
// callers must agree about which entry IS the sidecar, or one of them reads the
// author's metadata while the other writes beside it.
func foundSidecarRel(entries []srcEntry) string {
	for _, e := range entries {
		if strings.EqualFold(e.rel, sidecarName) {
			return e.rel
		}
	}
	return ""
}

// stampProvenance writes [import] derived_from / derived_at / derived_hash into
// the copy's sidecar, creating one when the source had none.
func stampProvenance(dir, from, rel string) error {
	if rel == "" {
		rel = sidecarName // the source had none; the copy gets one under the format's name
	}
	path := filepath.Join(dir, filepath.FromSlash(rel))
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
