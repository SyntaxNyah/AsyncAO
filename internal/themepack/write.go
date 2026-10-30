package themepack

// Write turns a theme folder into one shareable file.

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

// srcEntry is one source file with the size the caps count.
type srcEntry struct {
	rel  string // folder-relative, forward slashes
	size int64
}

// Extra is a caller-made member replacing a folder entry, so baked output never enters the author's folder.
type Extra struct {
	// Rel is the bundle path inside the theme folder, forward slashes.
	Rel string
	// Data is the whole file.
	Data []byte
}

// PackExtraCap bounds caller-supplied members (hard rule 4), so Write cannot become an arbitrary archiver.
const PackExtraCap = 8

// Write zips srcDir into destPath and returns what it wrote; blocking, goroutine only (hard rule 2).
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
			// The same guard as archive names, as trusted code can still make a mistake.
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

// writeArchive prefixes the root folder onto every entry name, the published layout.
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
	// Fsync before the rename: otherwise a crash can leave a zero-byte .aotheme with a valid name.
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

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
		// A file that grew between scan and copy is refused, not truncated: half a texture is worse than none.
		return capErr("one entry's size in bytes", n, PackEntryBytesCap, name)
	}
	return nil
}

// scanFolder walks a theme folder under the import's caps and builds the manifest.
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
	// The import's "one file per name" check, or a case-sensitive export makes a bundle we refuse.
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
		// Nothing folds on the folder side, so the write path and quoted name are the same.
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

// asPackEntries adapts folder rows for describeContents, so export and import classify fonts identically.
func asPackEntries(entries []srcEntry) []packEntry {
	out := make([]packEntry, len(entries))
	for i, e := range entries {
		out[i] = packEntry{idx: i, rel: e.rel, size: e.size}
	}
	return out
}

// readFolderSidecar fills display metadata from the folder's sidecar, or warns there is none.
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
			// Not an error: refusing themes shared without a licence would invent a policy.
			m.addWarning("this theme declares no [theme] license — say what people may do with it")
		}
		return
	}
	m.addWarning("no %s — this is a plain AO2 theme, which bundles fine but has no name or licence of its own", sidecarName)
}
