package ui

// The theme catalog: scanned off-thread (hard rule 2) and published as an immutable snapshot behind an atomic.Pointer
// (hard rule 5). No filesystem watcher: drop, picker open, one os.Stat on focus gain and Refresh are enough, and a
// watcher means a dependency, a goroutine and an unbounded event stream.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/platform"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// themeCatalogCap bounds one snapshot (hard rule 4); past it the scan stops and says so.
	themeCatalogCap = 128
	// themeSubthemeCap bounds subtheme folders per theme, using the format's own [import] subthemes bound.
	themeSubthemeCap = theme.SubthemeCap
	// themeSubthemeScanCap bounds the work of scanSubthemes (up to four os.Stat per candidate), since the output cap
	// bounds nothing when a theme holds hundreds of rejected asset subdirectories (hard rule 4).
	themeSubthemeScanCap = 64
	// themeScanMinInterval floors the on-open rescan; opening twice in a second must not walk the disk twice.
	themeScanMinInterval = 5 * time.Second
)

// themeKind is what a theme folder contains. The zero value is broken: an unrecognised folder must not read as fine.
type themeKind uint8

const (
	// themeKindBroken: none of the four files this client reads from a theme folder is here.
	themeKindBroken themeKind = iota
	// themeKindAO2: at least one of the design, fonts or sounds INIs; theme.Load merges them independently.
	themeKindAO2
	// themeKindNative: asyncao_theme.ini only. The badge reports the files: theme.Load and normalizeThemeRoot still
	// need an AO2 INI, so such a folder browses but does not apply.
	themeKindNative
	// themeKindBoth: an AO2 theme carrying the native sidecar.
	themeKindBoth
)

// themeKindLabel is the badge text, sitting in a crowded row. The zero-value badge names what is missing, no verdict.
func themeKindLabel(k themeKind) string {
	switch k {
	case themeKindAO2:
		return "AO2"
	case themeKindNative:
		return "native"
	case themeKindBoth:
		return "AO2 + native"
	default:
		return "no theme files"
	}
}

// themeOrigin is where a theme lives, which decides whether the editor must copy before editing.
type themeOrigin uint8

const (
	// themeOriginYours: under config.UserThemesDir(), ours to write.
	themeOriginYours themeOrigin = iota
	// themeOriginBundled: beside the executable.
	themeOriginBundled
	// themeOriginReadOnly: inside a custom theme folder the user pointed at.
	themeOriginReadOnly
)

func themeOriginLabel(o themeOrigin) string {
	switch o {
	case themeOriginYours:
		return "yours"
	case themeOriginBundled:
		return "bundled"
	default:
		return "read-only"
	}
}

type themeCatalogEntry struct {
	// Name is the folder name, the identity every subsystem keys by; never the sidecar's display name.
	Name string
	// Dir is the resolved folder the entry was found in.
	Dir string
	// Kind is the badge.
	Kind themeKind
	// Origin says whether the editor may write to it.
	Origin themeOrigin
	// Subthemes are the AO2 variant folders inside it (at most themeSubthemeCap).
	Subthemes []string
	// SidecarErr is why asyncao_theme.ini was refused ("" when absent or parsed); see themeEntryKind.
	SidecarErr string
}

// themeCatalog is immutable after publish: the scanner CASes in a fresh value, so the draw path reads with no lock.
type themeCatalog struct {
	Entries []themeCatalogEntry
	// WriteRoot is config.UserThemesDir() as this scan resolved it.
	WriteRoot string
	// RootMod is WriteRoot's mtime at scan time, compared by the focus-gain stat.
	RootMod time.Time
	// ScannedAt is what themeScanMinInterval floors the automatic triggers on.
	ScannedAt time.Time
	// Truncated: themeCatalogCap was hit, so the list is a prefix and the UI says so.
	Truncated bool
}

// Entry returns the named theme's entry, if the snapshot has one.
func (c *themeCatalog) Entry(name string) (themeCatalogEntry, bool) {
	if c == nil {
		return themeCatalogEntry{}, false
	}
	for _, e := range c.Entries {
		if strings.EqualFold(e.Name, name) {
			return e, true
		}
	}
	return themeCatalogEntry{}, false
}

// themeScanReason names the triggers, so the floor applies only to the automatic ones.
type themeScanReason uint8

const (
	// themeScanOnOpen: the settings screen or picker opened; floored by themeScanMinInterval.
	themeScanOnOpen themeScanReason = iota
	// themeScanOnFocus: the window regained focus; one os.Stat of the write root, a walk only if its mtime moved.
	themeScanOnFocus
	// themeScanExplicit: the user pressed Refresh; unconditional.
	themeScanExplicit
	// themeScanAfterDrop: something was just imported; unconditional.
	themeScanAfterDrop
)

// themeCatalogSnapshot is the only read a draw path may do.
func (a *App) themeCatalogSnapshot() *themeCatalog { return a.themeCat.Load() }

// requestThemeCatalog starts a scan if the reason allows one. The in-flight flag is atomic because the scanner
// goroutine clears it on publish.
func (a *App) requestThemeCatalog(reason themeScanReason) {
	if !a.themeCatBusy.CompareAndSwap(false, true) {
		return // a scan is already in flight; its result is the fresher one anyway
	}
	prev := a.themeCat.Load()
	if reason == themeScanOnOpen && prev != nil && time.Since(prev.ScannedAt) < themeScanMinInterval {
		a.themeCatBusy.Store(false)
		return
	}
	// Snapshotted here: the goroutine must not take the preferences lock.
	_, customRoot := a.d.Prefs.Load().Theme()
	statOnly := reason == themeScanOnFocus
	go func() {
		defer a.themeCatBusy.Store(false)
		next := scanThemeCatalog(customRoot, prev, statOnly)
		if next == nil {
			return // focus-gain stat found nothing new: keep the snapshot we have
		}
		a.themeCat.Store(next)
	}()
}

// scanThemeCatalog builds a snapshot; blocking, goroutine only. Returns nil when statOnly found the write root
// unchanged.
func scanThemeCatalog(customRoot string, prev *themeCatalog, statOnly bool) *themeCatalog {
	writeRoot := config.UserThemesDir()
	rootMod := dirModTime(writeRoot)
	if statOnly {
		if prev == nil {
			return nil // nothing to compare against; a real trigger will fill it
		}
		if rootMod.Equal(prev.RootMod) {
			return nil
		}
	}

	root, _ := normalizeThemeRoot(customRoot)
	// config.ExecutableDir, not os.Executable: platform.SamePath compares strings and UserThemesDir resolves symlinks, so a raw
	// spelling on a symlinked install would scan one root twice or badge a bundled theme read-only.
	exeDir := config.ExecutableDir()
	configBase, _ := config.ConfigBaseDir()

	out := &themeCatalog{
		WriteRoot: writeRoot,
		RootMod:   rootMod,
		ScannedAt: time.Now(),
	}
	seen := map[string]bool{}
	// The same list, order and strings as themeLoadRoots and the picker, or the catalog would claim themes the loader
	// can't find or hide ones it can. First hit per name wins, and the origin badge is the root it was found in.
	type rootSpec struct {
		dir    string
		origin themeOrigin
	}
	roots := make([]rootSpec, 0, themeSearchRootCap+1) // +1: the write-root backstop below
	for _, dir := range themeSearchRoots(root, exeDir, configBase) {
		roots = append(roots, rootSpec{dir, themeOriginOf(dir, exeDir, configBase)})
	}
	// Backstop: the write root's parent is already listed under every location policy today, but a theme the client
	// just wrote must never be invisible in the browser.
	if writeRoot != "" {
		if parent := filepath.Dir(writeRoot); !platform.SamePath(parent, exeDir) && !platform.SamePath(parent, configBase) && !platform.SamePath(parent, root) {
			roots = append(roots, rootSpec{parent, themeOriginYours})
		}
	}

	for _, rs := range roots {
		dir := filepath.Join(rs.dir, theme.ThemesDirName)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // a missing themes/ folder is the normal state, not a fault
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			key := strings.ToLower(e.Name())
			if seen[key] {
				continue // an earlier root already answers this name
			}
			// After both filters, so Truncated means a theme that would really have been listed was dropped.
			if len(out.Entries) >= themeCatalogCap {
				out.Truncated = true
				break
			}
			seen[key] = true
			themeDir := filepath.Join(dir, e.Name())
			origin := rs.origin
			if writeRoot != "" && platform.SamePath(dir, writeRoot) {
				origin = themeOriginYours
			}
			kind, sidecarErr := themeEntryKind(themeDir)
			out.Entries = append(out.Entries, themeCatalogEntry{
				Name:       e.Name(),
				Dir:        themeDir,
				Kind:       kind,
				Origin:     origin,
				Subthemes:  scanSubthemes(themeDir),
				SidecarErr: sidecarErr,
			})
		}
		if out.Truncated {
			break
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out
}

// themeOriginOf classifies one search root: exe dir ships bundled themes, config base is ours, anything else is copied.
func themeOriginOf(dir, exeDir, configBase string) themeOrigin {
	switch {
	case platform.SamePath(dir, exeDir):
		return themeOriginBundled
	case platform.SamePath(dir, configBase):
		return themeOriginYours
	default:
		return themeOriginReadOnly
	}
}

// themeKindOf badges a folder; a few os.Stat calls, off-thread. Reads all of themeINIFiles because normalizeThemeRoot
// accepts any of them and theme.Load merges them independently, so a fonts-only theme is a real theme.
func themeKindOf(dir string) themeKind {
	ao2 := false
	for _, ini := range themeINIFiles {
		if fileExists(filepath.Join(dir, ini)) {
			ao2 = true
			break
		}
	}
	native := fileExists(filepath.Join(dir, theme.SidecarFileName))
	switch {
	case ao2 && native:
		return themeKindBoth
	case ao2:
		return themeKindAO2
	case native:
		return themeKindNative
	default:
		return themeKindBroken
	}
}

// themeEntryKind is themeKindOf for a top-level entry, plus one parse so the badge can't claim a native tier the loader
// will refuse (a refused sidecar contributes nothing). Separate because themeKindOf also filters subthemes, up to
// themeSubthemeScanCap times per theme, and parsing there would be thousands of reads.
func themeEntryKind(dir string) (themeKind, string) {
	kind := themeKindOf(dir)
	if kind != themeKindNative && kind != themeKindBoth {
		return kind, "" // no sidecar to probe
	}
	if _, err := theme.LoadSidecar(filepath.Join(dir, theme.SidecarFileName)); err != nil {
		if kind == themeKindBoth {
			return themeKindAO2, err.Error()
		}
		return themeKindBroken, err.Error()
	}
	return kind, ""
}

// scanSubthemes lists subdirectories that are themselves theme folders (themeKindOf), up to themeSubthemeCap.
// Not just "has courtroom_design.ini": AO2 probes subthemes per element (path_functions.cpp get_asset_paths).
func scanSubthemes(themeDir string) []string {
	entries, err := os.ReadDir(themeDir)
	if err != nil {
		return nil
	}
	var out []string
	probed := 0 // candidates actually STATTED — the bound on this scan's work
	for _, e := range entries {
		if len(out) >= themeSubthemeCap || probed >= themeSubthemeScanCap {
			break
		}
		if !e.IsDir() {
			continue
		}
		name := theme.SanitizeSubtheme(e.Name())
		if name == "" {
			continue // a folder name the pref could never legally hold (dotfiles, over-long)
		}
		probed++
		if themeKindOf(filepath.Join(themeDir, e.Name())) == themeKindBroken {
			continue // a plain asset folder (misc/, fonts/, penalty/) is not a variant
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// dirModTime is the stat behind focus gain. A missing directory is the zero time, equal to the previous zero, so no
// rescan.
func dirModTime(dir string) time.Time {
	if dir == "" {
		return time.Time{}
	}
	info, err := os.Stat(dir)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// NoteFocusGained only records the event; the stat happens off-thread, and only while the screen that cares is visible.
func (a *App) NoteFocusGained() { a.themeCatFocusStat = true }

func (a *App) consumeThemeFocusStat() bool {
	if !a.themeCatFocusStat {
		return false
	}
	a.themeCatFocusStat = false
	return true
}

// openThemesFolder opens the write root, creating it first; off-thread because MkdirAll is disk I/O (hard rule 2).
func (a *App) openThemesFolder(dir string) {
	if dir == "" {
		return
	}
	go func() {
		_ = os.MkdirAll(dir, themesDirPerm)
		a.themesDirReady.Store(&dir)
		PushWake()
	}()
}

// pollThemesFolder opens the folder openThemesFolder created; SDL_OpenURL belongs on the main thread (hard rule 1).
func (a *App) pollThemesFolder() {
	if dir := a.themesDirReady.Swap(nil); dir != nil {
		a.openInFileManager(*dir)
	}
}

// themesDirPerm is the permission the themes folder is created with.
const themesDirPerm = 0o755
