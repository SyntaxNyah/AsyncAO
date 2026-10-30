package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ErrAlreadyPortable is returned by MigrateToPortable when the active config is already the portable one.
var ErrAlreadyPortable = errors.New("config: already portable")

// ErrThemesNotMigrated marks a partial success: settings copied, themes did not; test with errors.Is.
var ErrThemesNotMigrated = errors.New("config: settings copied, but the themes folder did not")

// PortableDirName is the folder beside the executable that makes a copied install carry its settings.
const PortableDirName = "config"

// writeProbeName is the throwaway file used to test whether the executable's directory is writable.
const writeProbeName = ".asyncao-writetest"

// The location is fixed for the process, so the probe runs once; settings reads it every frame (hard rule 2).
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

// ConfigBaseDir returns the directory holding preferences and per-user data: portable first, then the OS config dir.
func ConfigBaseDir() (string, error) {
	resolveConfigBaseOnce()
	return configBaseDirCache, configBaseErr
}

// ConfigIsPortable describes this session even after MigrateToPortable, which applies on the next launch.
func ConfigIsPortable() bool {
	resolveConfigBaseOnce()
	return configBasePortable
}

// PortableConfigDir returns where a portable config would live for this exe, or "" if the path is unknown.
func PortableConfigDir() string {
	exeDir := executableDir()
	if exeDir == "" {
		return ""
	}
	return filepath.Join(exeDir, PortableDirName)
}

// FontsDirName is the folder inside the config directory for user-supplied theme fonts.
const FontsDirName = "fonts"

// UserFontsDir is the last lookup before OS fonts, as shared themes name families without shipping files.
// "" means no such folder, not an error.
func UserFontsDir() string {
	base, err := ConfigBaseDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, FontsDirName)
}

// ThemesDirName aliases theme.ThemesDirName so the writer here and the reader there can't drift.
const ThemesDirName = theme.ThemesDirName

// UserThemesDir is where themes are written and the last root lookups search; "" means no write root.
func UserThemesDir() string {
	base, err := ConfigBaseDir()
	if err != nil {
		base = ""
	}
	return userThemesDir(ConfigIsPortable(), executableDir(), base)
}

// userThemesDir is the policy without I/O: portable beside the exe, otherwise beside the config file.
func userThemesDir(portable bool, exeDir, configBase string) string {
	if portable && exeDir != "" {
		return filepath.Join(exeDir, ThemesDirName)
	}
	if configBase == "" {
		return ""
	}
	return filepath.Join(configBase, ThemesDirName)
}

// resolveConfigBase picks the base and reports if it is portable; step 1 tests the prefs file, never the directory.
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
	// Only reachable when osDir is empty.
	return portableDir, portableDir != ""
}

// MigrateToPortable copies preferences, notebooks and jukebox beside the executable, returning the destination.
// It is a copy, never a move, so a botched run can't lose settings; it applies on the next launch.
func (p *AssetPreferences) MigrateToPortable() (string, error) {
	dest := PortableConfigDir()
	if dest == "" {
		return "", errors.New("config: cannot locate the executable directory")
	}
	src := filepath.Dir(p.path)
	if filepath.Clean(src) == filepath.Clean(dest) {
		return dest, ErrAlreadyPortable
	}
	// Flush debounced changes so the on-disk source is current.
	if err := p.SaveNow(); err != nil {
		return "", err
	}
	if err := copyTreeExcept(src, dest, map[string]bool{ThemesDirName: true}); err != nil {
		return "", err
	}
	if err := migrateThemesToPortable(src, filepath.Dir(dest)); err != nil {
		// Settings did move and dest is valid; the wrapped error tells the caller this from a total failure.
		return dest, err
	}
	return dest, nil
}

// migrateThemesToPortable copies the themes folder; a missing source is normal, source == destination a no-op.
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
		return fmt.Errorf("%w: %w", ErrThemesNotMigrated, err)
	}
	return nil
}

// copyTree recursively copies src into dst, never removing from src; the write-probe file is skipped.
func copyTree(src, dst string) error { return copyTreeExcept(src, dst, nil) }

// copyTreeExcept skips named top-level entries, ignoring case because Windows and macOS filesystems fold it.
func copyTreeExcept(src, dst string, skip map[string]bool) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("config: reading %s: %w", src, err)
	}
	if err := os.MkdirAll(dst, prefsDirPerm); err != nil {
		return fmt.Errorf("config: creating %s: %w", dst, err)
	}
	for _, e := range entries {
		if e.Name() == writeProbeName || skipEntry(skip, e.Name()) {
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

// skipEntry folds case; the skip set is a small caller literal, so the walk can't grow with input.
func skipEntry(skip map[string]bool, name string) bool {
	if len(skip) == 0 {
		return false
	}
	if skip[name] {
		return true
	}
	for k := range skip {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// copyFile copies via temp file + rename so an interrupted copy can't leave a half-written destination.
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

// executableDirOnce memoizes: EvalSymlinks is one Lstat per path component, reached while drawing (hard rule 2).
var (
	executableDirOnce  sync.Once
	executableDirCache string
)

// ExecutableDir is the one spelling of this directory: theme code compares paths as strings.
// A raw os.Executable without EvalSymlinks would scan a root twice or mislabel a bundled theme.
func ExecutableDir() string { return executableDir() }

// executableDir returns the executable's directory with symlinks resolved, or ""; memoized, so draw paths may call it.
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

// fileExists reports whether path names anything stat-able.
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// dirWritable creates then removes a probe file; MkdirAll first so a new config parent tests its real parent.
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
