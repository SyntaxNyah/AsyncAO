package ui

// "What is actually in this folder?" — the report the base-folder setup shows
// before it changes anything.
//
// It exists because the flow it replaces asked the user to type a path into a
// text field and then said nothing at all. A typo, a folder one level too high,
// and a folder full of characters that ship no char.ini all looked identical to
// a correct setup: the client went on quietly streaming and there was nothing on
// screen to disagree with. Every number here is counted off the disk, and
// anything the scan cannot establish is reported as unknown rather than guessed.
//
// OFF THE RENDER THREAD, ALWAYS (hard rule 2). Nothing in this file takes an
// *App or touches SDL; the single caller is the wizard's one bounded scan
// goroutine (basewizard.go). Keeping it a plain function over a path is also what
// lets it be tested against a real temp folder without a window.

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
)

// The AO on-disk layout, as folder names. The AUTHORITY is
// courtroom/urlbuilder.go's path constants — these are the same names seen from
// the filesystem side rather than the URL side, and they are unexported there.
// TestBaseScanFolderNamesMatchTheURLBuilder pins the pair together, so a layout
// change in the builder fails here instead of silently making every scan report
// an empty base.
const (
	baseDirCharacters = "characters"
	baseDirBackground = "background"
	baseDirSounds     = "sounds"
	baseDirEvidence   = "evidence"
	baseDirMisc       = "misc"
)

// baseCharINI is the per-character metadata file issue #72 is about: the emote
// list, showname, blips, chatbox skin, effects folder, scaling and idle pose. A
// base whose characters have none is a real and reportable state — the sprites
// load and everything else still comes from the server.
const baseCharINI = "char.ini"

const (
	// baseScanEntryCap bounds ONE listing (rule 4). A big public base ships around
	// four thousand character folders; twenty thousand is far above any real one
	// yet cheap to count, and past it the report says so instead of walking a
	// filesystem loop or a hostile archive to the end.
	baseScanEntryCap = 20000
	// baseScanBatch is how many directory entries are read per syscall. ReadDir(-1)
	// would materialise a whole 100k-entry folder before the cap could apply, which
	// is the allocation the cap exists to prevent.
	baseScanBatch = 512
	// baseScanZipEntryCap bounds the central-directory walk of a .zip pack. Entries,
	// not folders: a full base archive holds a few hundred thousand files, and the
	// walk is a prefix test per name.
	baseScanZipEntryCap = 200000
	// baseScanINISample is how many character folders get a char.ini stat. A folder
	// scan cannot check thousands without becoming a second index build, and it does
	// not need to — "do the characters here carry their inis" is answered by a
	// handful. The report always says how many it looked at (baseScan.iniOf), so the
	// sample is never presented as a whole-base count.
	baseScanINISample = 12
	// baseScanNestProbe is how many sub-folders are probed for a nested base when
	// the picked folder holds no characters/ of its own — the "I picked C:\AO2 and
	// the base is C:\AO2\base" correction.
	baseScanNestProbe = 24
)

// baseScan is one folder's report. The zero value is "nothing scanned yet"; the
// wizard tracks that separately (scanned) rather than inferring it from counts,
// because an empty folder is a legitimate answer with all-zero counts.
type baseScan struct {
	// path is what was scanned, verbatim as the user picked it. It is also the
	// staleness tag: a result whose path no longer matches the wizard's pick is
	// from a superseded scan.
	path string
	// isZip records that path was read as a .zip pack rather than walked as a
	// folder. The two arms count the same things; only the wording differs.
	isZip bool
	// chars / bgs are the sub-folder counts under characters/ and background/ —
	// the two directories that identify a base at a glance.
	chars, bgs int
	// iniOK of iniOf sampled character folders hold a char.ini. For a .zip the
	// sample is every character in it (the central directory lists them for free);
	// for a folder it is capped at baseScanINISample.
	iniOK, iniOf int
	// The supporting directories, reported as present/absent rather than counted:
	// nothing about the setup depends on how many sounds a base ships.
	hasSounds, hasEvidence, hasMisc bool
	// capped is set when a listing hit baseScanEntryCap / baseScanZipEntryCap, so
	// the counts are a floor rather than a total and the report says "or more".
	capped bool
	// suggest is a DIFFERENT path that looks more like a base than the picked one:
	// the sub-folder that holds characters/ when the user picked one level too
	// high, or the parent when they picked characters/ itself. "" when the pick
	// looks right, or when nothing better was found. The wizard offers it as a
	// one-click correction — the two mistakes it names are the ones that otherwise
	// end in "I set the folder and nothing happened".
	suggest string
	// err is why the folder could not be read at all ("" = it was read).
	err string
}

// looksLikeBase reports whether the scan found the shape of an AO base. Either
// directory is enough: a pack that only overrides backgrounds is a real and
// supported thing, and demanding both would refuse it.
func (s baseScan) looksLikeBase() bool { return s.chars > 0 || s.bgs > 0 }

// scanBaseFolder reports what a candidate base folder (or .zip pack) holds.
// Blocking disk I/O — callers run it on the wizard's scan goroutine, never on
// the render thread.
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

	// The char.ini sample. os.Stat per candidate rather than a second ReadDir: a
	// character folder holds every sprite that character owns, and listing four
	// hundred WebPs to find one filename is work with no answer in it.
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

// scanBaseZip reports a .zip pack from its central directory — one linear pass,
// no extraction. Mounting a .zip is supported, so the setup flow has to be able
// to describe one; refusing to would send the user back to the text field this
// whole surface exists to replace.
func scanBaseZip(path string, s baseScan) baseScan {
	r, err := zip.OpenReader(path)
	if err != nil {
		s.err = err.Error()
		return s
	}
	defer r.Close()

	// Sets, because a character contributes one row per sprite: counting entries
	// would report a nine-file character as nine characters. Each is bounded by
	// baseScanEntryCap for the same reason the folder walk is.
	chars := make(map[string]bool)
	bgs := make(map[string]bool)
	inis := make(map[string]bool)
	for i, f := range r.File {
		if i >= baseScanZipEntryCap {
			s.capped = true
			break
		}
		// Lowered and slash-normalised: zip names use forward slashes by spec, but
		// archives written by Windows tools in the wild carry backslashes, and the
		// mount index folds case for the same reason (a base authored on Windows is
		// read on Linux).
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
	// The archive listed every character for free, so the sample IS the whole set.
	s.iniOf, s.iniOK = len(chars), len(inis)
	return s
}

// listSubdirs counts a directory's sub-folders and returns the first `sample` of
// their names (sample 0 = count only). A missing directory is not an error here:
// "there is no characters/ folder" is the answer, and it is one the report has to
// be able to state.
//
// It reads in batches against baseScanEntryCap rather than calling ReadDir(-1),
// so a directory with a hundred thousand entries costs a bounded slice instead of
// materialising all of them (rule 4).
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
		// err is io.EOF at the end of the listing, and anything else means the
		// directory stopped being readable mid-walk. Either way what was counted so
		// far is the honest answer; a partial count is not a cap.
		if err != nil || len(ents) == 0 {
			return names, n, false
		}
	}
	return names, n, true
}

// baseSuggestion finds a better path than the one picked, for the two mistakes
// that actually happen. Only called when the pick itself does not look like a
// base, so it never second-guesses a correct answer.
//
// ONE LEVEL TOO DEEP is the folder literally named characters/ (or one of its
// siblings): its parent is the base. ONE LEVEL TOO HIGH is the install root that
// CONTAINS the base, which is the shape every AO2 client install has.
func baseSuggestion(path string) string {
	if isBaseSubdirName(filepath.Base(path)) {
		if parent := filepath.Dir(path); parent != path {
			return parent
		}
		return ""
	}
	// Bounded by baseScanNestProbe: this is a hint, not a search. A base nested
	// deeper than one level is not a case worth walking a disk for.
	names, _, _ := listSubdirs(path, baseScanNestProbe)
	for _, n := range names {
		cand := filepath.Join(path, n)
		if isDir(filepath.Join(cand, baseDirCharacters)) || isDir(filepath.Join(cand, baseDirBackground)) {
			return cand
		}
	}
	return ""
}

// isBaseSubdirName reports a folder name that belongs INSIDE a base, which means
// whoever picked it is one level too deep.
func isBaseSubdirName(name string) bool {
	switch strings.ToLower(name) {
	case baseDirCharacters, baseDirBackground, baseDirSounds, baseDirEvidence, baseDirMisc:
		return true
	}
	return false
}

// isDir is os.Stat's IsDir with the error folded into false — every caller here
// is asking "is there one of these", and "no, because it is unreadable" is the
// same answer for the purpose of a report.
func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
