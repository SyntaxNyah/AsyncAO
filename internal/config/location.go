package config

import (
	"fmt"
	"os"
	"path/filepath"
)

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
// This runs once at startup (off every hot path), so the writability probe in
// step 3 — a single create+delete — is fine under hard rule #2.
func ConfigBaseDir() (string, error) {
	exeDir := executableDir()
	osDir, osErr := os.UserConfigDir()
	if exeDir == "" && osDir == "" {
		return "", fmt.Errorf("config: locating config dir: %w", osErr)
	}
	dir, _ := resolveConfigBase(exeDir, osDir, fileExists, dirWritable)
	return dir, nil
}

// ConfigIsPortable reports whether ConfigBaseDir resolves to a portable location
// beside the executable (vs. the OS config dir). Drives the Data-tab readout.
func ConfigIsPortable() bool {
	exeDir := executableDir()
	osDir, _ := os.UserConfigDir()
	if exeDir == "" && osDir == "" {
		return false
	}
	_, portable := resolveConfigBase(exeDir, osDir, fileExists, dirWritable)
	return portable
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
