package ui

// The theme catalog: what themes exist, where they came from, and what shape
// they are in — scanned OFF-THREAD and published as an immutable snapshot.
//
// It exists because the theme picker is about to stop being a list of names.
// A user drops a folder in, exports an edited copy, or has a locked-down
// install where the client writes themes somewhere the drop convention never
// looked; every one of those questions is answered by reading directories, and
// reading directories is exactly what a draw helper may never do (hard rule 2).
// So the scan runs on a goroutine, the answer lands in an atomic.Pointer, and
// the settings screen only ever reads the pointer (hard rule 5).
//
// There is deliberately NO filesystem watcher. A cross-platform watcher is a
// dependency, a goroutine and an unbounded event stream bought to save one
// second over four triggers that cost nothing: drag-and-drop (which knows what
// it just imported), opening the picker (floored at themeScanMinInterval), a
// single os.Stat on focus-gain, and an explicit Refresh.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// themeCatalogCap bounds one catalog snapshot (hard rule 4). 128 is several
	// times the largest theme collection seen in the wild (the reference corpus
	// is ~80) and the list is a dropdown, not a database: past this the scan
	// stops and says so rather than growing without limit.
	themeCatalogCap = 128
	// themeSubthemeCap bounds the subtheme folders recorded per theme. AO2
	// themes ship one or two variants ("night", "alt"); the cap is the format's
	// own [import] subthemes bound so the catalog and the sidecar agree.
	themeSubthemeCap = theme.SubthemeCap
	// themeScanMinInterval floors the on-open rescan, mirroring the lobby's
	// refresh-on-open-with-a-floor idiom: opening the picker twice in a second
	// must not walk the disk twice. Explicit refresh ignores it.
	themeScanMinInterval = 5 * time.Second
)

// themeKind is what a theme folder actually contains — the badge that answers
// "will this do anything?". Zero value is themeKindBroken on purpose: a folder
// nothing recognised is the honest default, not a silent "fine".
type themeKind uint8

const (
	// themeKindBroken: none of the four files this client reads out of a theme
	// folder (themeINIFiles + the sidecar) is here, so nothing in it can load.
	themeKindBroken themeKind = iota
	// themeKindAO2: at least one of the three AO2 courtroom INIs — design, fonts
	// or sounds. Any one of them is a theme that does something (theme.Load merges
	// the three independently), and it is the same test normalizeThemeRoot applies
	// to a dropped folder.
	themeKindAO2
	// themeKindNative: asyncao_theme.ini only — native, with no AO2 base.
	themeKindNative
	// themeKindBoth: an AO2 theme carrying the native sidecar. The shape every
	// theme edited in AsyncAO ends up in, since the sidecar is additive.
	themeKindBoth
)

// themeKindLabel is the badge text. Short by design: it sits beside a name in a
// row that already has a dropdown and two arrows.
//
// The zero-value badge names WHAT IS MISSING rather than diagnosing the folder:
// "broken" would be a verdict the rest of the client disagrees with the moment
// this function's file list and normalizeThemeRoot's drift apart again.
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

// themeOrigin is WHERE a theme lives, which is the same question as "may I edit
// it in place?" — the editor copies before touching anything it does not own.
type themeOrigin uint8

const (
	// themeOriginYours: under config.UserThemesDir() — ours to write.
	themeOriginYours themeOrigin = iota
	// themeOriginBundled: beside the executable, where the client's own themes ship.
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

// themeCatalogEntry is one theme, as scanned.
type themeCatalogEntry struct {
	// Name is the FOLDER name — the identity every other subsystem keys by
	// (docs/THEME-FORMAT.md §1), never the sidecar's display name.
	Name string
	// Dir is the resolved folder the entry was found in.
	Dir string
	// Kind is the badge: what INIs this folder actually holds.
	Kind themeKind
	// Origin is where it lives, i.e. whether the editor may write to it.
	Origin themeOrigin
	// Subthemes are the AO2 variant folders found inside it (bounded by
	// themeSubthemeCap).
	Subthemes []string
}

// themeCatalog is an IMMUTABLE snapshot. Nothing mutates one after publish:
// the scanner builds a fresh value and CASes the pointer, which is what lets
// the draw path read it with no lock at all.
type themeCatalog struct {
	Entries []themeCatalogEntry
	// WriteRoot is config.UserThemesDir() as this scan resolved it.
	WriteRoot string
	// RootMod is WriteRoot's mtime at scan time — the one value the focus-gain
	// stat compares against, and the reason that trigger costs one syscall.
	RootMod time.Time
	// ScannedAt is what themeScanMinInterval floors the automatic triggers on.
	ScannedAt time.Time
	// Truncated reports that themeCatalogCap was hit, so the list is a prefix
	// and the UI says so rather than implying these are all the themes there are.
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

// themeScanReason names the four triggers. It exists so the floor is applied
// where it belongs — to the automatic ones — instead of everywhere.
type themeScanReason uint8

const (
	// themeScanOnOpen: the settings screen or the picker opened — FLOORED by
	// themeScanMinInterval, because this one can fire on its own.
	themeScanOnOpen themeScanReason = iota
	// themeScanOnFocus: the window regained focus — one os.Stat of the write
	// root, and a walk only if its mtime moved.
	themeScanOnFocus
	// themeScanExplicit: the user pressed Refresh. Unconditional: a floor on a
	// button the user just pressed reads as a broken button.
	themeScanExplicit
	// themeScanAfterDrop: something was just imported. Unconditional, same reason.
	themeScanAfterDrop
)

// themeCatalogSnapshot is the ONLY read a draw path may do.
func (a *App) themeCatalogSnapshot() *themeCatalog { return a.themeCat.Load() }

// requestThemeCatalog kicks a scan if this reason allows one. Render thread;
// everything expensive happens on the goroutine it starts.
//
// Single-flight via an atomic bool rather than a plain field: the flag is
// cleared by the scanner goroutine when it publishes, so both ends touch it and
// -race would (rightly) fail on anything less.
func (a *App) requestThemeCatalog(reason themeScanReason) {
	if !a.themeCatBusy.CompareAndSwap(false, true) {
		return // a scan is already in flight; its result is the fresher one anyway
	}
	prev := a.themeCat.Load()
	if reason == themeScanOnOpen && prev != nil && time.Since(prev.ScannedAt) < themeScanMinInterval {
		a.themeCatBusy.Store(false)
		return
	}
	// Everything the scan needs, snapshotted on this thread: the custom root is
	// a preference and the goroutine must not take the preferences lock.
	_, customRoot := a.d.Prefs.Theme()
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

// scanThemeCatalog walks the theme roots and builds a snapshot. BLOCKING —
// goroutine only. Returns nil when statOnly found the write root unchanged,
// which is the whole point of the focus-gain trigger: one os.Stat, not a walk.
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
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	configBase, _ := config.ConfigBaseDir()

	out := &themeCatalog{
		WriteRoot: writeRoot,
		RootMod:   rootMod,
		ScannedAt: time.Now(),
	}
	seen := map[string]bool{}
	// The SAME list, in the same order, that themeLoadRoots and the picker build:
	// the catalog must not claim a theme the loader would never find, nor hide one
	// it would. First hit per name wins, and its origin badge is the root it was
	// found in.
	type rootSpec struct {
		dir    string
		origin themeOrigin
	}
	roots := make([]rootSpec, 0, themeSearchRootCap+1) // +1: the write-root backstop below
	for _, dir := range themeSearchRoots(root, exeDir, configBase) {
		roots = append(roots, rootSpec{dir, themeOriginOf(dir, exeDir, configBase)})
	}
	// The write root's PARENT is already one of the roots above under every
	// location policy this client has: portable writes <exeDir>/themes (parent =
	// the exe dir) and everything else writes <configBase>/themes (parent = the
	// config base). This arm is the backstop for the day that stops being true —
	// a theme the client itself just wrote must never be invisible in the browser.
	if writeRoot != "" {
		if parent := filepath.Dir(writeRoot); !samePath(parent, exeDir) && !samePath(parent, configBase) && !samePath(parent, root) {
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
			if len(out.Entries) >= themeCatalogCap {
				out.Truncated = true
				break
			}
			if !e.IsDir() {
				continue
			}
			key := strings.ToLower(e.Name())
			if seen[key] {
				continue // an earlier root already answers this name
			}
			seen[key] = true
			themeDir := filepath.Join(dir, e.Name())
			origin := rs.origin
			if writeRoot != "" && samePath(dir, writeRoot) {
				origin = themeOriginYours
			}
			out.Entries = append(out.Entries, themeCatalogEntry{
				Name:      e.Name(),
				Dir:       themeDir,
				Kind:      themeKindOf(themeDir),
				Origin:    origin,
				Subthemes: scanSubthemes(themeDir),
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

// themeOriginOf answers "may the editor write here?" for one search root: the
// executable's directory ships the themes the client itself bundles, the config
// base is the folder we write into, and anything else is a folder the user
// pointed us at and that we therefore copy before touching. Kept beside the root
// list it classifies so the two are read together.
func themeOriginOf(dir, exeDir, configBase string) themeOrigin {
	switch {
	case samePath(dir, exeDir):
		return themeOriginBundled
	case samePath(dir, configBase):
		return themeOriginYours
	default:
		return themeOriginReadOnly
	}
}

// themeKindOf badges a folder by what it holds. A handful of os.Stat calls,
// off-thread.
//
// The AO2 arm reads themeINIFiles — ALL THREE courtroom INIs — and not just the
// design one, because that list is the app's own definition of a theme folder:
// normalizeThemeRoot uses it to decide a dropped folder IS a theme, and
// theme.Load merges the fonts and sounds INIs independently of the design one
// (theme.go:254-256), so a fonts-or-sounds-only theme is a theme that loads and
// does something. Badging one of those "no theme files" would have the browser
// contradict the client that had just accepted it on drop. The zero-value badge
// therefore says exactly one testable thing: not one of the four files this
// client reads out of a theme folder is present.
// TestThemeBadgeAgreesWithWhatTheClientAcceptsAsATheme.
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

// scanSubthemes lists the AO2 subtheme folders inside a theme: a subdirectory
// that is itself a theme folder by this client's own definition (themeKindOf) is
// what AO2 resolves as a subtheme, and it is the only shape we can honestly offer
// as a variant. Bounded by themeSubthemeCap.
//
// The test is themeKindOf and not "has courtroom_design.ini" because AO2 probes
// the subtheme tier per ELEMENT, not per theme (path_functions.cpp
// get_asset_paths: "Subtheme path" is tried for whatever file is being resolved),
// so a variant that only overrides courtroom_fonts.ini is a real one — and our
// own Load tiers the three INIs the same way.
func scanSubthemes(themeDir string) []string {
	entries, err := os.ReadDir(themeDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if len(out) >= themeSubthemeCap {
			break
		}
		if !e.IsDir() {
			continue
		}
		name := theme.SanitizeSubtheme(e.Name())
		if name == "" {
			continue // a folder name the pref could never legally hold (dotfiles, over-long)
		}
		if themeKindOf(filepath.Join(themeDir, e.Name())) == themeKindBroken {
			continue // a plain asset folder (misc/, fonts/, penalty/) is not a variant
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// dirModTime is the one stat behind the focus-gain trigger. A missing directory
// reports the zero time, which compares equal to the previous zero time — so a
// user with no write root costs exactly one stat and never a rescan.
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

// fileExists is the local spelling of "is there a file here", kept private to
// this file so the scan reads as one thing.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// NoteFocusGained is called from the event loop when the window regains input
// focus. It only LATCHES: the actual stat happens off-thread, and only while
// the screen that cares is the visible one — the user story is "I dropped a
// theme in Explorer and came back", not "poll the disk forever".
func (a *App) NoteFocusGained() { a.themeCatFocusStat = true }

// consumeThemeFocusStat is the settings screen's end of that latch.
func (a *App) consumeThemeFocusStat() bool {
	if !a.themeCatFocusStat {
		return false
	}
	a.themeCatFocusStat = false
	return true
}

// openThemesFolder opens the write root in the OS file manager, creating it
// first when it does not exist yet — "Open folder" on a folder that is not
// there is a dead button, and the one directory the client writes themes into
// is one it may create. Off-thread: MkdirAll is disk I/O (hard rule 2).
func openThemesFolder(dir string) {
	if dir == "" {
		return
	}
	go func() {
		_ = os.MkdirAll(dir, themesDirPerm)
		openInFileManager(dir)
	}()
}

// themesDirPerm is the permission the themes folder is created with — the same
// 0755 every other directory this client creates uses.
const themesDirPerm = 0o755
