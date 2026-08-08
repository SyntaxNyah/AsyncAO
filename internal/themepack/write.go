package themepack

// WRITE — turn a theme folder into one shareable file.
//
// ONE concern: zipping a folder, under the same bounds an import is held to, and
// telling the user what is in the result before they hand it to anybody.
//
// SYMMETRY IS THE POINT. Write refuses exactly what Extract refuses — the same
// entry count, the same per-file and total byte caps, the same path length, the
// same one-file-per-name rule (caseSet), the same symlink skip through
// safepath.WalkFiles — so a theme this client exports is a theme this client can
// import. An exporter looser than the importer ships bundles that only the author
// can open, and the loosest of them is the one that only the author's FILESYSTEM
// can open.
//
// THE CENSUS COMES FIRST AND THE FILE COMES SECOND. scanFolder answers "what
// would this bundle contain?" — count, bytes, media, and every font with whether
// its licence travels with it — and only then is anything created, so a theme
// past a cap is refused with the destination untouched and the Manifest that
// comes back is what the UI reports.
//
// There is deliberately no exported "plan it first" entry point yet. The design's
// export SHEET wants those numbers before the click, and getting them means a
// disk walk, which is goroutine-only (hard rule 2) — so the sheet needs its own
// off-thread stage, and an exported primitive whose only caller was a test would
// be the parsed-but-never-applied shape rule 11 forbids. It arrives with the
// sheet.

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

// srcEntry is one file of the source folder, with the size the caps are counted
// against.
type srcEntry struct {
	rel  string // folder-relative, forward slashes
	size int64
}

// Extra is a bundle member produced by the CALLER rather than read off the
// folder: the AO2-compat export's baked courtroom_design.ini, its emptied
// sidecar, and ASYNCAO-LOST.txt.
//
// AN EXTRA REPLACES a folder entry with the same Rel, which is what makes
// "bake the overrides into the design INI, but only in the bundle" possible
// WITHOUT ever writing that file back into the author's own folder — the
// highest-blast-radius risk the design names (§Q5 R3). The bytes exist for the
// length of one zip and are never on disk under the theme's name.
type Extra struct {
	// Rel is the bundle path inside the theme folder, forward slashes.
	Rel string
	// Data is the whole file.
	Data []byte
}

// PackExtraCap bounds caller-supplied members (hard rule 4). Three is the
// AO2-compat set; eight leaves room for a future manifest or preview without
// letting a caller turn Write into an arbitrary archiver.
const PackExtraCap = 8

// Write zips srcDir into destPath and returns what it wrote.
//
// The bundle's single root entry is the theme folder (§2.5), named from srcDir,
// so the archive unpacks into a folder rather than over whatever directory the
// recipient happened to be standing in.
//
// TEMP THEN RENAME, exactly as Extract does: a partly written .aotheme with the
// right name is a file somebody will try to share.
//
// BLOCKING — goroutine only (hard rule 2).
func Write(srcDir, destPath string, extras []Extra) (*Manifest, error) {
	entries, m, err := scanFolder(srcDir)
	if err != nil {
		return nil, err
	}
	if destPath == "" {
		return nil, errors.New("themepack: no destination for the bundle")
	}
	if len(extras) > PackExtraCap {
		return nil, capErr("caller-supplied bundle entries", int64(len(extras)), PackExtraCap, "")
	}
	for _, x := range extras {
		if x.Rel == "" || safepath.UnsafeRel(x.Rel) || len(x.Rel) > PackPathLenCap {
			// The SAME guard the archive's own names go through. A caller is trusted
			// code, but "trusted code cannot make a mistake" is not a security model.
			return nil, unsafeErr(x.Rel)
		}
		if int64(len(x.Data)) > PackEntryBytesCap {
			return nil, capErr("one entry's size in bytes", int64(len(x.Data)), PackEntryBytesCap, x.Rel)
		}
	}
	if err := os.MkdirAll(filepath.Dir(destPath), packDirPerm); err != nil {
		return nil, err
	}
	part := destPath + packPartSuffix
	if err := writeArchive(srcDir, m.Root, entries, extras, part); err != nil {
		os.Remove(part)
		return nil, err
	}
	if info, err := os.Stat(part); err == nil {
		m.ZipBytes = info.Size()
	}
	if err := os.Rename(part, destPath); err != nil {
		os.Remove(part)
		return nil, err
	}
	m.Dir = destPath
	return m, nil
}

// writeArchive is the zip itself. The root folder is prefixed onto every entry
// name, which is the whole of §2.5's layout.
func writeArchive(srcDir, root string, entries []srcEntry, extras []Extra, destPath string) error {
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	replaced := make(map[string]bool, len(extras))
	for _, x := range extras {
		replaced[strings.ToLower(x.Rel)] = true
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		if replaced[strings.ToLower(e.rel)] {
			continue // an Extra stands in for this file (see Extra)
		}
		src, err := safepath.Join(srcDir, e.rel)
		if err != nil {
			f.Close()
			return unsafeErr(e.rel)
		}
		if err := addOneFile(zw, root+"/"+e.rel, src); err != nil {
			f.Close()
			return err
		}
	}
	for _, x := range extras {
		w, err := zw.Create(root + "/" + x.Rel)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(x.Data); err != nil {
			f.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	// FSYNC BEFORE THE RENAME. Without it the directory entry can land before the
	// bytes on a crash, which is a zero-byte .aotheme wearing a valid name — the
	// exact shape the temp-then-rename dance exists to prevent.
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// addOneFile streams one file into the archive, bounded by PackEntryBytesCap.
func addOneFile(zw *zip.Writer, name, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	n, err := io.Copy(w, io.LimitReader(in, PackEntryBytesCap+1))
	if err != nil {
		return err
	}
	if n > PackEntryBytesCap {
		// A file that GREW between the scan and the copy. Refused rather than
		// truncated, for the reason the import side gives: half a texture is worse
		// than none.
		return capErr("one entry's size in bytes", n, PackEntryBytesCap, name)
	}
	return nil
}

// scanFolder walks a theme folder under the import's own caps and builds the
// manifest.
//
// safepath.WalkFiles does the walking, which is what makes the symlink skip and
// the depth bound the SAME implementation the archive side uses (see the package
// comment). Nothing is filtered by name: a theme's unknown files are its author's
// business, and a bundler that quietly dropped what it did not recognise would
// break the very re-share it exists for.
func scanFolder(srcDir string) ([]srcEntry, *Manifest, error) {
	if srcDir == "" {
		return nil, nil, errors.New("themepack: no theme folder to bundle")
	}
	info, err := os.Stat(srcDir)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, errors.New("themepack: " + srcDir + " is not a folder")
	}
	root := SanitizeName(filepath.Base(srcDir))
	if root == "" {
		return nil, nil, errors.New("themepack: " + filepath.Base(srcDir) + " is not a usable theme folder name")
	}
	m := &Manifest{Root: root}
	entries := make([]srcEntry, 0, 64)
	// The SAME "one file per name" guard the import census applies. It can only
	// ever fire on a case-SENSITIVE filesystem, because that is the only kind that
	// can hold the pair — and that is exactly the machine on which an export would
	// otherwise produce a bundle this client refuses to import, which is the
	// asymmetry this file's header promises does not exist.
	seen := newCaseSet()
	var total int64
	var capFail error
	walkErr := safepath.WalkFiles(srcDir, packMaxDepth, func(rel string) bool {
		if len(entries) >= PackEntryCap {
			capFail = capErr("entry count", int64(len(entries))+1, PackEntryCap, rel)
			return false
		}
		if len(root)+1+len(rel) > PackPathLenCap {
			capFail = capErr("entry path length", int64(len(root)+1+len(rel)), PackPathLenCap, rel)
			return false
		}
		full, err := safepath.Join(srcDir, rel)
		if err != nil {
			capFail = unsafeErr(rel)
			return false
		}
		fi, err := os.Stat(full)
		if err != nil {
			return true // it vanished mid-walk: skip it rather than fail the export
		}
		if fi.Size() > PackEntryBytesCap {
			capFail = capErr("one entry's size in bytes", fi.Size(), PackEntryBytesCap, rel)
			return false
		}
		total += fi.Size()
		if total > PackTotalBytesCap {
			capFail = capErr("uncompressed total in bytes", total, PackTotalBytesCap, rel)
			return false
		}
		// The write path and the quoted name are the same string here: on the folder
		// side nothing is folded, so the file is spelled exactly as it will be packed.
		if err := seen.claim(rel, rel); err != nil {
			capFail = err
			return false
		}
		entries = append(entries, srcEntry{rel: rel, size: fi.Size()})
		return true
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	if capFail != nil {
		return nil, nil, capFail
	}
	if len(entries) == 0 {
		return nil, nil, ErrPackEmpty
	}
	m.Files = len(entries)
	m.Bytes = total
	describeContents(m, asPackEntries(entries))
	readFolderSidecar(m, srcDir, entries)
	return entries, m, nil
}

// asPackEntries adapts the folder walk's rows to the shape describeContents
// reads, so the media/font census is ONE function for both directions — an
// exporter that classified fonts differently from the importer would warn about a
// missing licence on the way in and not on the way out.
func asPackEntries(entries []srcEntry) []packEntry {
	out := make([]packEntry, len(entries))
	for i, e := range entries {
		out[i] = packEntry{idx: i, rel: e.rel, size: e.size}
	}
	return out
}

// readFolderSidecar fills the display metadata from the folder's own sidecar, or
// warns that there is none — the folder-side twin of readPackSidecar.
//
// IT OPENS THE SPELLING THE WALK FOUND, not the format's canonical one, and the
// difference is only visible on a case-SENSITIVE filesystem: a folder holding
// ASYNCAO_THEME.INI matches the EqualFold above on every platform, but opening
// the canonical name finds nothing on Linux — so the export sheet would report
// "a plain AO2 theme with no name or licence of its own" about a theme that
// declares both, and would then ship a bundle whose import (canonicalSidecarRel)
// says otherwise. Same sheet-describes-a-different-file defect as the archive
// side's, arriving through the author's own folder.
func readFolderSidecar(m *Manifest, srcDir string, entries []srcEntry) {
	for _, e := range entries {
		if !strings.EqualFold(e.rel, sidecarName) {
			continue
		}
		sc, err := loadSidecarFile(filepath.Join(srcDir, filepath.FromSlash(e.rel)))
		if err != nil {
			m.addWarning("%s will not load: %v", sidecarName, err)
			return
		}
		if sc == nil {
			break
		}
		m.Name, m.Author, m.License, m.Format = sc.Meta.Name, sc.Meta.Author, sc.Meta.License, sc.Format
		if m.License == "" {
			// NAMED, because the licence is the one field a recipient needs and the
			// one an author forgets. Not an error: plenty of themes are shared with
			// no licence at all, and refusing that would be inventing a policy.
			m.addWarning("this theme declares no [theme] license — say what people may do with it")
		}
		return
	}
	m.addWarning("no %s — this is a plain AO2 theme, which bundles fine but has no name or licence of its own", sidecarName)
}
