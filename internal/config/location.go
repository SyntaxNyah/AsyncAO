package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ErrAlreadyPortable is returned by MigrateToPortable when the active config is
// already the portable one (nothing to copy).
var ErrAlreadyPortable = errors.New("config: already portable")

// PortableDirName is the folder beside the executable that holds a *portable*
// config set (asset_preferences.json, notebooks/, jukebox.json). Its presence
// is what makes a copied AsyncAO folder — or a USB stick — carry its settings
// with it. Kept a plain, obvious name so it's "readily available right there".
const PortableDirName = "config"

// writeProbeName is the throwaway file ConfigBaseDir creates to test whether the
// executable's directory is writable (Program Files / read-only mounts are not).
const writeProbeName = ".asyncao-writetest"

// ConfigBaseDir returns the directory that holds asset_preferences.json and the
// rest of AsyncAO's per-user data (notebooks, jukebox). It is **portable-first**
// with an OS-config-dir fallback:
//
//  1. If a portable config already exists beside the executable
//     (<exeDir>/config/asset_preferences.json), use it — a copied folder or a
//     stick keeps its settings.
//  2. Otherwise, if the classic OS config dir already has one
//     (<os.UserConfigDir>/AsyncAO/asset_preferences.json), use that — existing
//     installs are never stranded.
//  3. Fresh install: portable beside the exe when that directory is writable
//     (the common case — unzipped to Desktop/Downloads/a stick); else fall back
//     to the OS config dir (Program Files / locked-down installs).
//
// The result is memoized: the active location is fixed for the process lifetime,
// so the writability probe in step 3 (a single create+delete) runs exactly once
// — never per call. That matters because DefaultPath() flows through here and is
// read on hot paths (e.g. the settings screen shows the path every frame); a
// per-frame disk probe would violate hard rule #2.
var (
	configBaseOnce     sync.Once
	configBaseDirCache string
	configBasePortable bool
	configBaseErr      error
)

func resolveConfigBaseOnce() {
	configBaseOnce.Do(func() {
		exeDir := executableDir()
		osDir, osErr := os.UserConfigDir()
		if exeDir == "" && osDir == "" {
			configBaseErr = fmt.Errorf("config: locating config dir: %w", osErr)
			return
		}
		configBaseDirCache, configBasePortable = resolveConfigBase(exeDir, osDir, fileExists, dirWritable)
	})
}

// ConfigBaseDir returns the directory that holds asset_preferences.json and the
// rest of AsyncAO's per-user data. See the package doc above for the policy.
func ConfigBaseDir() (string, error) {
	resolveConfigBaseOnce()
	return configBaseDirCache, configBaseErr
}

// ConfigIsPortable reports whether the active config (ConfigBaseDir) is the
// portable location beside the executable (vs. the OS config dir). Memoized;
// after a MigrateToPortable it still reflects *this* session — the move takes
// effect on the next launch, which is exactly what the UI should report.
func ConfigIsPortable() bool {
	resolveConfigBaseOnce()
	return configBasePortable
}

// PortableConfigDir returns where a portable config set would live for this exe
// (<exeDir>/config), independent of which location is currently active — used by
// the "Make portable" migration. Empty string if the exe path can't be resolved.
func PortableConfigDir() string {
	exeDir := executableDir()
	if exeDir == "" {
		return ""
	}
	return filepath.Join(exeDir, PortableDirName)
}

// FontsDirName is the folder inside the config directory where a user may drop
// font files for themes to use.
const FontsDirName = "fonts"

// UserFontsDir is where AsyncAO looks for fonts the USER supplied — the last
// resort before the operating system's own font folders.
//
// It exists because of how AO themes are actually distributed. A theme names its
// families ("Ace Name", "Igiari Cyrillic", "DangitSpeaker") and ships none of the
// files, because in AO2 they live in the BASE's fonts/ folder and Qt registers
// that folder globally at startup. Themes, however, are shared on their own — a
// themes-only repository, a zip from a friend — so a user who has never
// downloaded a full base has the theme and none of its fonts, and every string
// in it renders in the wrong face with nothing to say why.
//
// Putting the folder beside the preferences rather than beside a content root is
// deliberate: it belongs to the USER, not to any one asset pack, so it keeps
// working when they switch packs, add a second one, or have none at all. It
// follows a portable install onto the stick with everything else.
//
// Empty string if the config location cannot be resolved, which callers treat as
// "no such tier" rather than as an error — a missing font folder is the normal
// state, not a fault.
func UserFontsDir() string {
	base, err := ConfigBaseDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, FontsDirName)
}

// ThemesDirName is the folder holding theme folders (and the .aotheme bundles
// dropped beside them). It is a CONST ALIAS of theme.ThemesDirName rather than a
// second spelling of "themes", so config and theme cannot drift: the writer here
// and the reader there are the same string by construction.
const ThemesDirName = theme.ThemesDirName

// UserThemesDir is the ONE directory AsyncAO writes themes into — imports,
// exports, editable copies, saved presets — and the third root theme lookups
// search (internal/ui's themeLoadRoots, appended LAST so nothing that resolves
// today resolves differently).
//
// Portable-first, exactly like ConfigBaseDir and for the same reason: an
// unzipped-to-Desktop or USB-stick install must carry its themes with it, and a
// Program Files install must not try to write beside the exe. In the portable
// case the folder sits beside the executable — NOT inside config/ — because that
// is the drop convention the client has always documented (<exeDir>/themes/<name>),
// so a portable user's write root and their drop folder are the same folder.
//
// The writability question is ALREADY answered and memoized by ConfigBaseDir's
// step-3 probe, and the executable's directory is memoized by executableDir, so
// every input this joins is resolved at most once per process: no fresh
// MkdirAll/create/remove per session, no risk of the write root silently
// relocating between sessions, and after the first call no syscalls at all — a
// draw path may call it (hard rule 2). The FIRST call, on a cold process, does
// pay for whichever memo is still empty, so a caller that can avoid it on a
// draw path still should (see drawThemeCatalogRows, which prefers the scan's
// published WriteRoot).
//
// Empty string if the config location cannot be resolved — callers treat that as
// "no write root", never as an error, exactly like UserFontsDir.
func UserThemesDir() string {
	base, err := ConfigBaseDir()
	if err != nil {
		base = ""
	}
	return userThemesDir(ConfigIsPortable(), executableDir(), base)
}

// userThemesDir is the pure policy behind UserThemesDir (no I/O of its own, so
// it unit-tests directly): portable puts themes beside the exe, everything else
// beside the config file, and an unresolvable location yields "".
func userThemesDir(portable bool, exeDir, configBase string) string {
	if portable && exeDir != "" {
		return filepath.Join(exeDir, ThemesDirName)
	}
	if configBase == "" {
		return ""
	}
	return filepath.Join(configBase, ThemesDirName)
}

// OSConfigDir returns the classic OS config location (<os.UserConfigDir>/AsyncAO),
// independent of which location is active. Empty string if it can't be resolved.
func OSConfigDir() string {
	osDir, err := os.UserConfigDir()
	if err != nil || osDir == "" {
		return ""
	}
	return filepath.Join(osDir, PrefsDirName)
}

// resolveConfigBase is the pure resolution policy (no I/O of its own — existence
// and writability are injected so it's unit-testable). Returns the chosen base
// directory and whether that base is the portable one.
//
// The step-1 trigger is the existence of the prefs *file*, never the directory:
// an accidentally-created empty config/ folder must not hijack an existing
// OS-config user onto fresh defaults. Do not "simplify" this to a dir check.
func resolveConfigBase(exeDir, osDir string, exists func(string) bool, writable func(string) bool) (dir string, portable bool) {
	portableDir := ""
	if exeDir != "" {
		portableDir = filepath.Join(exeDir, PortableDirName)
	}
	classicDir := ""
	if osDir != "" {
		classicDir = filepath.Join(osDir, PrefsDirName)
	}

	// 1. An existing portable config wins outright.
	if portableDir != "" && exists(filepath.Join(portableDir, PrefsFileName)) {
		return portableDir, true
	}
	// 2. An existing classic config keeps existing users in place.
	if classicDir != "" && exists(filepath.Join(classicDir, PrefsFileName)) {
		return classicDir, false
	}
	// 3. Fresh: prefer portable when the exe dir is actually writable.
	if portableDir != "" && writable(exeDir) {
		return portableDir, true
	}
	if classicDir != "" {
		return classicDir, false
	}
	// Only reachable when osDir is empty; portableDir is the sole option.
	return portableDir, portableDir != ""
}

// MigrateToPortable copies the *entire* active config set — preferences,
// notebooks/ and jukebox.json — into the portable folder beside the executable
// (<exeDir>/config), so the next launch resolves there and the folder travels
// with a copied install or a USB stick. The source (e.g. AppData) is left
// untouched: migration is a copy, never a move, so a botched run can't lose
// settings. Takes effect on the next launch. Returns the destination directory.
//
// All three are copied together because, while resolution keeps them in one
// place automatically, the migration copy is the single spot where that
// consistency isn't free — copying only prefs would strand notebooks/jukebox.
//
// THEMES travel too, but not into config/. The write root is portable-aware
// (UserThemesDir): while config lives in AppData the user's themes sit at
// <AppData>/AsyncAO/themes, and after this migration the client writes them to
// <exeDir>/themes. Letting the tree copy carry them would land them in
// <exeDir>/config/themes instead — still readable (it is the config root), but
// no longer the folder anything WRITES to, so the collection would silently
// split in two and every later export would land somewhere the old themes are
// not. The themes folder is therefore excluded from the tree copy and copied to
// the portable write root instead.
func (p *AssetPreferences) MigrateToPortable() (string, error) {
	dest := PortableConfigDir()
	if dest == "" {
		return "", errors.New("config: cannot locate the executable directory")
	}
	src := filepath.Dir(p.path)
	if filepath.Clean(src) == filepath.Clean(dest) {
		return dest, ErrAlreadyPortable
	}
	// Flush any debounced changes so the on-disk source is current before copy.
	if err := p.SaveNow(); err != nil {
		return "", err
	}
	if err := copyTreeExcept(src, dest, map[string]bool{ThemesDirName: true}); err != nil {
		return "", err
	}
	if err := migrateThemesToPortable(src, filepath.Dir(dest)); err != nil {
		// The settings DID move; say so rather than reporting a total failure,
		// because the user's next action ("restart to use the portable copy")
		// is still the right one and only the themes need attention.
		return dest, err
	}
	return dest, nil
}

// migrateThemesToPortable copies <srcBase>/themes to <exeDir>/themes — the
// portable write root UserThemesDir will resolve on the next launch. A missing
// source folder is the normal case (no themes yet) and is not an error, and a
// source that already IS the destination is a no-op rather than a self-copy.
func migrateThemesToPortable(srcBase, exeDir string) error {
	if srcBase == "" || exeDir == "" {
		return nil
	}
	from := filepath.Join(srcBase, ThemesDirName)
	to := filepath.Join(exeDir, ThemesDirName)
	if filepath.Clean(from) == filepath.Clean(to) {
		return nil // the two roots already collapsed
	}
	if info, err := os.Stat(from); err != nil || !info.IsDir() {
		return nil // no themes to carry
	}
	if err := copyTree(from, to); err != nil {
		return fmt.Errorf("config: settings copied, but the themes folder did not: %w", err)
	}
	return nil
}

// copyTree recursively copies the contents of src into dst (creating dst), never
// removing anything from src. The write-probe throwaway is skipped.
func copyTree(src, dst string) error { return copyTreeExcept(src, dst, nil) }

// copyTreeExcept is copyTree with a set of TOP-LEVEL entry names to leave behind
// (matched by name, so only the roots of the copy are filtered — a nested
// "themes" folder inside a notebook directory is still copied).
func copyTreeExcept(src, dst string, skip map[string]bool) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("config: reading %s: %w", src, err)
	}
	if err := os.MkdirAll(dst, prefsDirPerm); err != nil {
		return fmt.Errorf("config: creating %s: %w", dst, err)
	}
	for _, e := range entries {
		if e.Name() == writeProbeName || skip[e.Name()] {
			continue
		}
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies a single regular file s to d (overwriting), via a temp file +
// rename so an interrupted copy can't leave a half-written destination.
func copyFile(s, d string) error {
	in, err := os.Open(s)
	if err != nil {
		return fmt.Errorf("config: opening %s: %w", s, err)
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(d), prefsTmpPattern)
	if err != nil {
		return fmt.Errorf("config: temp for %s: %w", d, err)
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("config: copying %s: %w", s, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, d); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("config: placing %s: %w", d, err)
	}
	return nil
}

// executableDirOnce memoizes executableDir. The path cannot change while the
// process runs, and resolving it is NOT free: os.Executable plus
// filepath.EvalSymlinks is a chain of Lstat syscalls, one per path component.
// UserThemesDir and PortableConfigDir call executableDir on every invocation and
// the settings screen calls those from a draw helper, so without this the walk
// would run every frame — hard rule #2 (no synchronous disk I/O on a draw path).
var (
	executableDirOnce  sync.Once
	executableDirCache string
)

// executableDir returns the directory containing the running executable, with
// symlinks resolved, or "" if it can't be determined. Memoized (see above): the
// syscalls happen exactly once per process, so callers on a draw path are safe.
func executableDir() string {
	executableDirOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		if rp, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = rp
		}
		executableDirCache = filepath.Dir(exe)
	})
	return executableDirCache
}

// fileExists reports whether path names an existing file (or anything stat-able).
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// dirWritable reports whether dir (which need not exist yet) can be written to,
// by attempting to create — then immediately remove — a throwaway probe file.
// MkdirAll first so a not-yet-created config parent still tests its real parent.
func dirWritable(dir string) bool {
	if dir == "" {
		return false
	}
	if err := os.MkdirAll(dir, prefsDirPerm); err != nil {
		return false
	}
	probe := filepath.Join(dir, writeProbeName)
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, prefsFilePerm)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}
