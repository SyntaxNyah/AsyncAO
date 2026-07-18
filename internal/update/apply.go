package update

// Self-replace mechanism (M13). The choreography is PURE over paths so it is
// unit-testable in a temp dir without touching the live executable — the caller
// supplies os.Executable() at the one call site. On Windows a RUNNING .exe
// can't be deleted or overwritten, but it CAN be renamed, so the swap is:
//
//	download -> verify -> rename(target -> .old) -> rename(new -> target)
//	  (next boot) delete .old        (on failure) rename(.old -> target)
//
// Rollback is the safety net for the AV-quarantine-mid-swap case CLAUDE.md
// warns about: if installing the new binary fails, the old one is renamed back
// so the install is never left broken. Verification here is INTEGRITY (the
// download isn't corrupt/truncated), NOT authenticity — that needs release
// signing, which slots in at VerifyChecksum's call site later.

import (
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// downloadMaxBytes bounds a release asset download (rule §17.4). Far larger
	// than the JSON cap — a client binary is tens of MiB — but still finite so a
	// hostile or mislinked asset can't fill the disk.
	downloadMaxBytes = 256 << 20
	// downloadTimeout caps the asset download (a binary, not a tiny JSON).
	downloadTimeout = 5 * time.Minute
)

// BackupPath is where the current binary is moved during a swap (a sibling of
// the exe, so the rename stays same-volume and atomic). It also marks a swap
// for next-boot cleanup.
func BackupPath(targetPath string) string { return targetPath + ".old" }

// StagedPath is where a download lands before the swap — also a sibling of the
// exe, so moving it into place is an atomic same-volume rename.
func StagedPath(targetPath string) string { return targetPath + ".new" }

// CleanupOldVersion removes a leftover .old backup from a previous update.
// Safe to call on every boot: a no-op when absent, and a still-locked backup
// (an AV scan holding it) is simply left for a later boot. Caller should run it
// OFF the boot critical path (it touches the disk).
func CleanupOldVersion(targetPath string) {
	_ = os.Remove(BackupPath(targetPath))
}

// Download streams the asset at url to destPath (created/truncated), bounded by
// downloadMaxBytes. destPath MUST sit on the same volume as the install dir
// (use StagedPath) so the later rename into place is atomic. A truncated or
// over-cap download removes the partial file and errors, so a half file is
// never handed to the swap.
func Download(ctx context.Context, url, destPath string) (int64, error) {
	cctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "AsyncAO-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("update: downloading asset: %s", resp.Status)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, downloadMaxBytes))
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		_ = os.Remove(destPath)
		return n, copyErr
	case closeErr != nil:
		_ = os.Remove(destPath)
		return n, closeErr
	case n >= downloadMaxBytes:
		_ = os.Remove(destPath)
		return n, fmt.Errorf("update: asset exceeds the %d-byte cap", int64(downloadMaxBytes))
	}
	// The staged file must be EXECUTABLE before the swap: os.Create made it
	// 0644 (0666 & umask) and StageReplace's renames preserve mode, so without
	// this every macOS/Linux self-update would install a binary the OS refuses
	// to launch — and CleanupOldVersion unlinks the .old backup right after the
	// stage, so there'd be nothing runnable left. Windows ignores the exec bit.
	// Best-effort (the swap is the critical path; a chmod error surfaces at
	// launch anyway, and only exotic filesystems fail chmod on a file we own).
	if runtime.GOOS != "windows" {
		_ = os.Chmod(destPath, stagedBinaryMode)
	}
	return n, nil
}

// stagedBinaryMode is the file mode for a freshly staged self-update binary:
// rwxr-xr-x, the conventional mode for an installed executable (matches what
// a tar -xzf of the release tarball or a chmod +x by the user would yield).
const stagedBinaryMode = 0o755

// VerifyChecksum reports whether the file at path has the given hex SHA-256.
// This is INTEGRITY (corruption/truncation), not authenticity: a compromised
// release could publish a matching sum. An empty wantHex skips the check (no
// sum was published) and returns nil so the caller can still proceed.
func VerifyChecksum(path, wantHex string) error {
	if strings.TrimSpace(wantHex) == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(wantHex)) {
		return fmt.Errorf("update: checksum mismatch (got %s)", got)
	}
	return nil
}

// renameFn is os.Rename, overridable in tests to simulate a mid-swap failure
// (the one operation that can leave a half-applied state without rollback).
var renameFn = os.Rename

// StageReplace swaps the binary at targetPath with the one at newPath, keeping
// the old binary at backupPath. Pure over paths (no os.Executable). A failed
// install rolls back so the install always works; a failed rollback is a hard
// error naming where the good binary is. All three paths must be on one volume.
func StageReplace(newPath, targetPath, backupPath string) error {
	if _, err := os.Stat(newPath); err != nil {
		return fmt.Errorf("update: staged binary missing: %w", err)
	}
	// macOS SONAME-skew preflight, BEFORE any mutation (§ finding: the bare-binary
	// self-update swaps only the executable, never the sibling ./lib the tarball
	// shipped — see docs/CODE-SIGNING.md). If the new binary needs an @rpath dylib
	// the installed lib/ doesn't have, refuse HERE, while the install is still
	// pristine (no backup rename yet), so the caller degrades to "re-download the
	// tarball" and nothing is left broken. Fail-open by construction (see the fn).
	if err := preflightDarwinSwap(newPath, targetPath); err != nil {
		return err
	}
	_ = os.Remove(backupPath) // clear a stale backup so the rename can't collide
	if err := renameFn(targetPath, backupPath); err != nil {
		return fmt.Errorf("update: backing up current binary: %w", err)
	}
	if err := renameFn(newPath, targetPath); err != nil {
		if rbErr := renameFn(backupPath, targetPath); rbErr != nil {
			return fmt.Errorf("update: install failed (%v) and rollback failed (%v); working binary is at %s", err, rbErr, backupPath)
		}
		return fmt.Errorf("update: installing new binary (rolled back cleanly): %w", err)
	}
	return nil
}

// TargetWritable reports whether the directory holding targetPath can be
// written — the swap renames within it, so a read-only install (Program Files
// without elevation) must degrade to "open the release page" instead. It probes
// by creating and removing a temp file (the only reliable cross-FS check).
func TargetWritable(targetPath string) bool {
	dir := targetPath[:strings.LastIndexAny(targetPath, `/\`)+1]
	if dir == "" {
		dir = "."
	}
	f, err := os.CreateTemp(dir, ".asyncao-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// bundledLibDirName is the sibling folder the macOS first-install tarball ships
// its dylib closure in (scripts/bundle-macos.sh stages the binary + this dir;
// the binary's rpath list is @executable_path/lib first). Its presence beside
// the running binary is how preflightDarwinSwap tells the no-Homebrew tarball
// install (which CAN skew) from a bare brew install (which falls through to
// /opt/homebrew/lib and can't).
const bundledLibDirName = "lib"

// preflightDarwinSwap refuses a macOS self-update that would leave the new
// binary unable to load one of its @rpath dylibs from the STALE sibling lib/
// the tarball shipped. This is the SONAME-skew brick documented in
// docs/CODE-SIGNING.md: the self-updater swaps only the bare binary, not lib/,
// so a Homebrew dependency crossing a SONAME bump (e.g. libavif.16 → libavif.17)
// makes the new binary reference a dylib the stale lib/ lacks; @executable_path/lib
// misses and — for the no-Homebrew tarball audience the bundle exists to serve —
// there is no /opt/homebrew/lib fallback, so dyld fails to launch it.
//
// It is a NO-OP on every non-darwin platform (Windows/Linux never bundle a
// sibling lib/ this way) and — critically — FAIL-OPEN: any inability to inspect
// the binary or the lib/ (parse error, no macho, unreadable dir) returns nil so
// a real update is never blocked by the guard itself. It refuses only the two
// provable cases when a sibling lib/ EXISTS: the new binary imports a dylib
// straight from a Homebrew prefix (mis-bundled asset — a correct release build
// never does, see bundle-macos.sh check_clean), or it imports an @rpath dylib
// the installed lib/ lacks (SONAME skew). No sibling lib/ (a brew install, or a
// bare download beside no lib/) → nil, because the rpath fallback to
// /opt/homebrew/lib covers those.
//
// Called as the FIRST thing in StageReplace, before any rename, so a refusal
// leaves the install completely untouched — a false positive costs the user a
// manual tarball re-download, never a broken install.
func preflightDarwinSwap(newPath, targetPath string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	libDir := filepath.Join(filepath.Dir(targetPath), bundledLibDirName)
	if info, err := os.Stat(libDir); err != nil || !info.IsDir() {
		return nil // no bundled lib/ → brew/rpath-fallback install, can't skew
	}
	f, err := macho.Open(newPath)
	if err != nil {
		return nil // not inspectable as Mach-O → fail open, let the swap proceed
	}
	defer f.Close()
	imported, err := f.ImportedLibraries()
	if err != nil {
		return nil // fail open
	}
	// A tarball install updating to a binary that imports straight from a
	// Homebrew prefix is PROVABLY mis-bundled: bundle-macos.sh's check_clean
	// gate guarantees a correctly built release binary never carries such an
	// import (everything is rewritten to @rpath), and the tarball audience by
	// definition may have no Homebrew — dyld would fail at launch. This is the
	// one shape a failed release-side bundle step could emit, so refuse it
	// outright; it is not fail-open erosion because the case is affirmatively
	// inspectable and never legitimate.
	if hb := homebrewImports(imported); len(hb) > 0 {
		// "or any newer release": besides a mis-bundled asset, this fires on an
		// experimental-channel DOWNGRADE onto a pre-bundling release, which
		// published no tarball — don't send that user chasing one.
		return fmt.Errorf("update: the new macOS build links %s directly from Homebrew instead of its bundled lib/ "+
			"(a mis-bundled release asset) — install from the release page's bundle tarball, or any newer release", hb[0])
	}
	missing := missingBundledDylibs(imported, libDir)
	if len(missing) == 0 {
		return nil
	}
	// Name the first missing dylib so the log/modal is actionable; the recovery
	// is the tarball, which carries a matching lib/ (docs/CODE-SIGNING.md).
	return fmt.Errorf("update: the new macOS build needs %s, which the installed lib/ doesn't have "+
		"(a dependency changed its version) — re-download the bundle tarball from the release page to update", missing[0])
}

// homebrewImports returns the imported dylib paths that resolve inside a
// Homebrew prefix (/opt/homebrew on Apple Silicon, /usr/local on Intel).
// System locations (/usr/lib, /System/…) and @-relative names pass through.
// Pure (no I/O) and split out so it is unit-testable off macOS, like
// missingBundledDylibs below.
func homebrewImports(imported []string) []string {
	var bad []string
	for _, name := range imported {
		if strings.HasPrefix(name, "/opt/homebrew/") || strings.HasPrefix(name, "/usr/local/") {
			bad = append(bad, name)
		}
	}
	return bad
}

// missingBundledDylibs returns the base names of the binary's @rpath-resolved
// dylib dependencies that are absent from libDir. Only @rpath/ imports are
// considered: absolute (/usr/lib/…, /System/…) and @executable_path/…-relative
// names resolve elsewhere and are never bundled. Split out from
// preflightDarwinSwap so this pure name-set logic is unit-testable off macOS.
func missingBundledDylibs(imported []string, libDir string) []string {
	const rpathPrefix = "@rpath/"
	var missing []string
	for _, name := range imported {
		if !strings.HasPrefix(name, rpathPrefix) {
			continue // absolute or @executable_path/@loader_path: not bundled here
		}
		base := path.Base(name[len(rpathPrefix):]) // dylib SONAMEs use / — use path, not filepath
		if _, err := os.Stat(filepath.Join(libDir, base)); err != nil {
			missing = append(missing, base)
		}
	}
	return missing
}
