package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestStageReplaceSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	staged := StagedPath(target)
	backup := BackupPath(target)
	writeFile(t, target, "OLD")
	writeFile(t, staged, "NEW")

	if err := StageReplace(staged, target, backup); err != nil {
		t.Fatalf("StageReplace: %v", err)
	}
	if got := readFile(t, target); got != "NEW" {
		t.Errorf("target = %q, want the new binary", got)
	}
	if got := readFile(t, backup); got != "OLD" {
		t.Errorf("backup = %q, want the old binary kept for next-boot cleanup", got)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("the staged file should have been consumed by the rename")
	}
}

func TestStageReplaceRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	staged := StagedPath(target)
	backup := BackupPath(target)
	writeFile(t, target, "OLD")
	writeFile(t, staged, "NEW")

	// Simulate the install rename (new -> target) failing mid-swap, the
	// AV-quarantine case; the backup -> target rollback must still run.
	orig := renameFn
	renameFn = func(from, to string) error {
		if from == staged {
			return errors.New("simulated AV lock")
		}
		return orig(from, to)
	}
	defer func() { renameFn = orig }()

	if err := StageReplace(staged, target, backup); err == nil {
		t.Fatal("a failed install must report an error")
	}
	if got := readFile(t, target); got != "OLD" {
		t.Errorf("after rollback target = %q, want the original binary restored", got)
	}
}

func TestStageReplaceMissingStaged(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	writeFile(t, target, "OLD")
	// No staged file: must error WITHOUT touching the live binary.
	if err := StageReplace(StagedPath(target), target, BackupPath(target)); err == nil {
		t.Fatal("missing staged binary must error")
	}
	if got := readFile(t, target); got != "OLD" {
		t.Errorf("target must be untouched, got %q", got)
	}
}

func TestCleanupOldVersion(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	backup := BackupPath(target)
	writeFile(t, backup, "stale")
	CleanupOldVersion(target)
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("CleanupOldVersion must remove the .old backup")
	}
	CleanupOldVersion(target) // absent now: must be a safe no-op
}

func TestDownload(t *testing.T) {
	const payload = "this-is-a-binary"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "app.exe.new")
	n, err := Download(context.Background(), srv.URL, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if int(n) != len(payload) || readFile(t, dest) != payload {
		t.Errorf("download wrote %d bytes / %q", n, readFile(t, dest))
	}
}

func TestDownloadServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "app.exe.new")
	if _, err := Download(context.Background(), srv.URL, dest); err == nil {
		t.Fatal("a 404 must error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a failed download must not leave a partial file")
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob")
	writeFile(t, path, "hello world")
	sum := sha256.Sum256([]byte("hello world"))
	hexSum := hex.EncodeToString(sum[:])

	if err := VerifyChecksum(path, hexSum); err != nil {
		t.Errorf("matching checksum must pass: %v", err)
	}
	if err := VerifyChecksum(path, "deadbeef"); err == nil {
		t.Error("a wrong checksum must fail")
	}
	if err := VerifyChecksum(path, ""); err != nil {
		t.Errorf("empty checksum must skip (no sum published): %v", err)
	}
}

func TestTargetWritable(t *testing.T) {
	dir := t.TempDir()
	if !TargetWritable(filepath.Join(dir, "app.exe")) {
		t.Error("a fresh temp dir must be writable")
	}
}
