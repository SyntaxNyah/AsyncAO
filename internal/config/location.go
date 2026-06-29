package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
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
	if err := copyTree(src, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// copyTree recursively copies the contents of src into dst (creating dst), never
// removing anything from src. The write-probe throwaway is skipped.
func copyTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("config: reading %s: %w", src, err)
	}
	if err := os.MkdirAll(dst, prefsDirPerm); err != nil {
		return fmt.Errorf("config: creating %s: %w", dst, err)
	}
	for _, e := range entries {
		if e.Name() == writeProbeName {
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

// executableDir returns the directory containing the running executable, with
// symlinks resolved, or "" if it can't be determined.
func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if rp, err := filepath.EvalSymlinks(exe); err == nil {
		exe = rp
	}
	return filepath.Dir(exe)
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
