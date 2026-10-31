package ui

// The report the base-folder setup shows before it changes anything. A typed path with a typo, a folder one level
// too high, or characters with no char.ini all looked like a correct setup. Every number is counted off the disk,
// and anything the scan cannot establish is reported as unknown. Off the render thread always (hard rule 2): this
// file takes no *App and no SDL; the only caller is the wizard's bounded scan goroutine (basewizard.go), and it is
// a plain function over a path so it tests against a real temp folder.

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
)

// The AO on-disk layout as folder names. courtroom/urlbuilder.go's unexported path constants are the authority;
// these are the same names from the filesystem side. A layout change in the builder must fail a test here rather
// than make every scan report an empty base.
const (
	baseDirCharacters = "characters"
	baseDirBackground = "background"
	baseDirSounds     = "sounds"
	baseDirEvidence   = "evidence"
	baseDirMisc       = "misc"
)

// baseCharINI is the per-character metadata file: emote list, showname, blips, chatbox skin, effects folder,
// scaling and idle pose. A base whose characters have none is reportable: sprites load, the rest comes from the
// server.
const baseCharINI = "char.ini"

const (
	// baseScanEntryCap bounds one listing (hard rule 4). A big public base has about four thousand character folders;
	// past this cap the report says so instead of walking a filesystem loop or hostile archive to the end.
	baseScanEntryCap = 20000
	// baseScanBatch is how many directory entries are read per syscall. ReadDir(-1) would materialise a whole huge
	// folder before the cap could apply.
	baseScanBatch = 512
	// baseScanZipEntryCap bounds the central-directory walk of a .zip pack, counted in entries, not folders.
	baseScanZipEntryCap = 200000
	// baseScanINISample is how many character folders get a char.ini stat. A folder scan can't check thousands, and a
	// handful answers "do the characters here carry their inis". The report states how many it looked at
	// (baseScan.iniOf).
	baseScanINISample = 12
	// baseScanNestProbe is how many sub-folders are probed for a nested base when the picked folder has no
	// characters/
	// of its own (picked C:\AO2 when the base is C:\AO2\base).
	baseScanNestProbe = 24
)

// baseScan is one folder's report. The zero value is "nothing scanned yet"; the wizard tracks that separately,
// since an empty folder is a legitimate all-zero answer.
type baseScan struct {
	// path is what was scanned, as picked. It is also the staleness tag: a result whose path no longer matches the
	// wizard's pick is from a superseded scan.
	path string
	// isZip records that path was read as a .zip pack rather than walked as a folder; only the wording differs.
	isZip bool
	// chars / bgs are the sub-folder counts under characters/ and background/, which identify a base.
	chars, bgs int
	// iniOK of iniOf sampled character folders hold a char.ini. For a .zip the sample is every character; for a
	// folder
	// it is capped at baseScanINISample.
	iniOK, iniOf int
	// The supporting directories are reported as present or absent, not counted.
	hasSounds, hasEvidence, hasMisc bool
	// capped is set when a listing hit baseScanEntryCap / baseScanZipEntryCap, so counts are a floor ("or more").
	capped bool
	// suggest is a different path that looks more like a base than the picked one: the sub-folder holding characters/
	// when the user picked one level too high, or the parent when they picked characters/ itself. "" when the pick
	// looks right or nothing better was found. The wizard offers it as a one-click correction.
	suggest string
	// err is why the folder could not be read at all ("" = it was read).
	err string
}

// looksLikeBase reports whether the scan found the shape of an AO base. Either directory is enough, since a pack
// that only overrides backgrounds is supported.
func (s baseScan) looksLikeBase() bool { return s.chars > 0 || s.bgs > 0 }

// scanBaseFolder reports what a candidate base folder (or .zip pack) holds. Blocking disk I/O: run it on the
// wizard's scan goroutine.
func scanBaseFolder(path string) baseScan {
	s := baseScan{path: path}
	path = strings.TrimSpace(path)
	if path == "" {
		s.err = "no folder picked yet"
		return s
	}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		s.isZip = true
		return scanBaseZip(path, s)
	}
	st, err := os.Stat(path)
	if err != nil {
		s.err = err.Error()
		return s
	}
	if !st.IsDir() {
		s.err = "that is a file, not a folder"
		return s
	}

	var names []string
	var capped bool
	names, s.chars, capped = listSubdirs(filepath.Join(path, baseDirCharacters), baseScanINISample)
	s.capped = s.capped || capped
	_, s.bgs, capped = listSubdirs(filepath.Join(path, baseDirBackground), 0)
	s.capped = s.capped || capped

	// The char.ini sample uses os.Stat per candidate rather than a second ReadDir, since a character folder can hold
	// hundreds of sprites.
	for _, n := range names {
		s.iniOf++
		if st, err := os.Stat(filepath.Join(path, baseDirCharacters, n, baseCharINI)); err == nil && !st.IsDir() {
			s.iniOK++
		}
	}

	s.hasSounds = isDir(filepath.Join(path, baseDirSounds))
	s.hasEvidence = isDir(filepath.Join(path, baseDirEvidence))
	s.hasMisc = isDir(filepath.Join(path, baseDirMisc))
	if !s.looksLikeBase() {
		s.suggest = baseSuggestion(path)
	}
	return s
}

// scanBaseZip reports a .zip pack from its central directory in one linear pass, without extraction.
func scanBaseZip(path string, s baseScan) baseScan {
	r, err := zip.OpenReader(path)
	if err != nil {
		s.err = err.Error()
		return s
	}
	defer r.Close()

	// Sets, because a character contributes one row per sprite; counting entries would count a nine-file character as
	// nine. Each is bounded by baseScanEntryCap like the folder walk.
	chars := make(map[string]bool)
	bgs := make(map[string]bool)
	inis := make(map[string]bool)
	for i, f := range r.File {
		if i >= baseScanZipEntryCap {
			s.capped = true
			break
		}
		// Lowered and slash-normalised: zip names use forward slashes, but archives from Windows tools carry
		// backslashes,
		// and the mount index folds case for the same reason.
		rel := strings.ToLower(strings.ReplaceAll(f.Name, `\`, "/"))
		switch {
		case strings.HasPrefix(rel, baseDirCharacters+"/"):
			name, tail, _ := strings.Cut(strings.TrimPrefix(rel, baseDirCharacters+"/"), "/")
			if name == "" {
				continue
			}
			if len(chars) >= baseScanEntryCap {
				s.capped = true
				continue
			}
			chars[name] = true
			if tail == baseCharINI {
				inis[name] = true
			}
		case strings.HasPrefix(rel, baseDirBackground+"/"):
			name, _, _ := strings.Cut(strings.TrimPrefix(rel, baseDirBackground+"/"), "/")
			if name == "" {
				continue
			}
			if len(bgs) >= baseScanEntryCap {
				s.capped = true
				continue
			}
			bgs[name] = true
		case strings.HasPrefix(rel, baseDirSounds+"/"):
			s.hasSounds = true
		case strings.HasPrefix(rel, baseDirEvidence+"/"):
			s.hasEvidence = true
		case strings.HasPrefix(rel, baseDirMisc+"/"):
			s.hasMisc = true
		}
	}
	s.chars, s.bgs = len(chars), len(bgs)
	// The archive lists every character, so the sample is the whole set.
	s.iniOf, s.iniOK = len(chars), len(inis)
	return s
}

// listSubdirs counts a directory's sub-folders and returns the first `sample` names (0 = count only). A missing
// directory is not an error: "no characters/ folder" is a reportable answer. It reads in batches against
// baseScanEntryCap instead of ReadDir(-1), so a huge directory costs a bounded slice (hard rule 4).
func listSubdirs(dir string, sample int) (names []string, n int, capped bool) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, 0, false
	}
	defer f.Close()
	seen := 0
	for seen < baseScanEntryCap {
		ents, err := f.ReadDir(baseScanBatch)
		seen += len(ents)
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			n++
			if len(names) < sample {
				names = append(names, e.Name())
			}
		}
		// err is io.EOF at the end of the listing; anything else means the directory became unreadable mid-walk.
		// Either
		// way the count so far is honest, and a partial count is not a cap.
		if err != nil || len(ents) == 0 {
			return names, n, false
		}
	}
	return names, n, true
}

// baseSuggestion finds a better path for the two common mistakes. Only called when the pick doesn't look like a
// base. One level too deep is the folder named characters/ (or a sibling): its parent is the base. One level too
// high is the install root that contains the base, the shape of every AO2 client install.
func baseSuggestion(path string) string {
	if isBaseSubdirName(filepath.Base(path)) {
		if parent := filepath.Dir(path); parent != path {
			return parent
		}
		return ""
	}
	// Bounded by baseScanNestProbe: a hint, not a search.
	names, _, _ := listSubdirs(path, baseScanNestProbe)
	for _, n := range names {
		cand := filepath.Join(path, n)
		if isDir(filepath.Join(cand, baseDirCharacters)) || isDir(filepath.Join(cand, baseDirBackground)) {
			return cand
		}
	}
	return ""
}

// isBaseSubdirName reports a folder name that belongs inside a base, so the picker is one level too deep.
func isBaseSubdirName(name string) bool {
	switch strings.ToLower(name) {
	case baseDirCharacters, baseDirBackground, baseDirSounds, baseDirEvidence, baseDirMisc:
		return true
	}
	return false
}

// isDir is os.Stat's IsDir with the error folded into false; an unreadable folder answers the same for a report.
func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
