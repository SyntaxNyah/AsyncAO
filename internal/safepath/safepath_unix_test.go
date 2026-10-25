//go:build unix

package safepath

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The NON-LINK half of UnsafePath's refusal, in the one place it can be built: a FIFO
// and a socket need a syscall Windows has no equivalent of, so this file is unix-only
// while the link cases stay in the portable gate.
//
// It is not a curiosity. A symlink-only predicate was the first draft of this guard and
// it claimed WalkFiles' shape while refusing a strict subset of it; the FIFO is the
// cheapest name that proves the difference, and it is also the one that HURTS — opening
// a FIFO for reading blocks until a writer appears, and the mount fetcher's Fetch runs
// on a pool worker and documents local reads as uncancellable, so the worker never comes
// back. TestWalkFilesSkipsANonRegularEntry is the other source's half of the same claim:
// the two must skip the same names or "the two byte sources agree" is only a sentence.

// mustFifo makes a FIFO at p, skipping the test if the filesystem cannot hold one
// (some CI sandboxes and network mounts refuse mknod) — the same accommodation the
// symlink gates make for Windows without developer mode.
func mustFifo(t *testing.T, p string) {
	t.Helper()
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("FIFOs unavailable on this filesystem: %v", err)
	}
}

// TestUnsafePathRefusesNonRegularNonLinks pins the widening: everything that is
// neither a plain directory nor a regular file is refused, whether or not it is a link.
// /dev/null is a real character device on every unix, so the device case costs nothing
// to set up and needs no privileges.
func TestUnsafePathRefusesNonRegularNonLinks(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "(a)Normal.png")
	mustFifo(t, fifo)

	for _, tc := range []struct {
		what string
		path string
		want bool
	}{
		{"a FIFO wearing a sprite's name", fifo, true},
		{"a character device", "/dev/null", true},
		{"the directory holding them", root, false},
	} {
		if got := UnsafePath(tc.path); got != tc.want {
			t.Errorf("UnsafePath(%s) = %v, want %v", tc.what, got, tc.want)
		}
	}
}

// TestWalkFilesSkipsANonRegularEntry is the claim UnsafePath's shape is copied FROM,
// driven rather than quoted: the index walk yields the regular file beside the FIFO and
// never the FIFO. Both halves of "the two byte sources agree about non-regular names"
// are then measured — this one here, the fetcher's in
// internal/assets/localnonregular_unix_test.go.
func TestWalkFilesSkipsANonRegularEntry(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "characters", "phoenix")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "(b)normal.png"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustFifo(t, filepath.Join(dir, "(a)normal.png"))

	got := walkAll(t, root)
	if len(got) != 1 || got[0] != "characters/phoenix/(b)normal.png" {
		t.Errorf("walk yielded %v — a non-regular entry was visited", got)
	}
}
