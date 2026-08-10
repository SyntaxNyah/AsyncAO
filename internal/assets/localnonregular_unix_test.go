//go:build unix

package assets

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The fold's refusal of NON-REGULAR, NON-LINK names — the half a symlink-only predicate
// missed while its doc claimed the mount index's shape. Unix-only because a FIFO needs
// mkfifo; the symlink half is portable and lives in localsymlink_test.go.

// localFifoFetchDeadline bounds the one gate below. It is a TEST bound, not a product
// one: the failure it catches is an UNBOUNDED wait (os.ReadFile on a FIFO blocks until a
// writer opens it, and Fetch documents local reads as uncancellable), so the gate has to
// give up on its own or a regression hangs the package instead of failing it. Generous
// enough that a loaded CI box reading two small files never trips it.
const localFifoFetchDeadline = 5 * time.Second

// mustFifo makes a FIFO at p, skipping when the filesystem cannot hold one.
func mustFifo(t *testing.T, p string) {
	t.Helper()
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("FIFOs unavailable on this filesystem: %v", err)
	}
}

// TestLocalFetcherFoldAndTheIndexAgreeAboutAFifo is the parity claim itself, driven over
// ONE tree through BOTH byte sources — because "the fold refuses what the index walk
// skips" is the sentence the whole refusal rests on, and the first draft of that refusal
// made it while being false.
//
// The tree is a pack shipping characters/Phoenix/(a)Normal.png as a FIFO. The index walk
// skips it (WalkFiles refuses every non-regular entry) so the index over this mount is
// EMPTY. The fold, refusing only symlinks, matched the authored case the lowercased URL
// got wrong, handed the path back, and os.ReadFile blocked on the pool worker forever.
//
// So the gate asserts both halves and asserts them the same way: the index does not
// cover the URL, and the fetcher answers a MISS — promptly. The deadline is the load
// bearing part; without it a regression stalls the run instead of reporting.
func TestLocalFetcherFoldAndTheIndexAgreeAboutAFifo(t *testing.T) {
	mount := t.TempDir()
	folder := filepath.Join(mount, "characters", "Phoenix")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	mustFifo(t, filepath.Join(folder, "(a)Normal.png"))
	skipIfTheLoweredPathResolves(t, filepath.Join(mount, "characters", "phoenix"))

	const rel = "characters/phoenix/(a)normal.png"

	// Source one: the mount INDEX. Nothing here is a regular file, so it indexes nothing.
	ix, errs := BuildMountIndex([]string{mount})
	if len(errs) != 0 {
		t.Fatalf("BuildMountIndex: %v", errs)
	}
	defer ix.Retire()
	if files, _, _ := ix.Stats(); files != 0 {
		t.Errorf("the index holds %d files — WalkFiles visited a non-regular entry", files)
	}
	if ix.Covers(rel) {
		t.Errorf("the index covers %q — the two sources cannot agree if this one indexed the FIFO", rel)
	}

	// Source two: the no-streaming FETCHER, on the same tree, through the same URL.
	lf := NewLocalFetcher([]string{mount})
	type answer struct {
		data []byte
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		data, err := lf.Fetch(context.Background(), lf.BaseURL()+rel)
		done <- answer{data, err}
	}()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("the fold served %q out of a FIFO — the index skipped that name, so the two "+
				"byte sources disagree about what this mount contains", got.data)
		}
	case <-time.After(localFifoFetchDeadline):
		t.Fatalf("Fetch did not return within %v: the fold opened a FIFO on what is a pool worker in "+
			"production, and Fetch ignores its context, so nothing ever unblocks it", localFifoFetchDeadline)
	}
}
