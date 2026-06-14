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
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
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
	return n, nil
}

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
