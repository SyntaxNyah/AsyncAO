package assets

import (
	"errors"
	"io/fs"
	"testing"
)

// prune's budget arithmetic, driven through the remove hook.
// TestThumbPruneBudget above stages real files and calls tc.prune(), so it can
// only ever see the interleavings the encode worker's open sweep happens to
// produce — which is exactly how the one that matters reached CI as a flake
// (c.bin, a file the budget had room for, deleted). These drive that
// interleaving on purpose.

// vanishing returns a remove hook that reports the named paths as already gone
// — what a concurrent sweep leaves behind between prune's ReadDir snapshot and
// its delete pass — and records everything it actually deletes.
func vanishing(gone ...string) (func(string) error, *[]string) {
	missing := make(map[string]bool, len(gone))
	for _, p := range gone {
		missing[p] = true
	}
	var deleted []string
	return func(path string) error {
		if missing[path] {
			return &fs.PathError{Op: "remove", Path: path, Err: fs.ErrNotExist}
		}
		deleted = append(deleted, path)
		return nil
	}, &deleted
}

// fourKiB is the snapshot both cases below start from: four 1 KiB files, a the
// oldest through d the newest, against a budget with room for two.
func fourKiB() []thumbFile {
	return []thumbFile{
		{path: "a", size: 1024, mod: 1},
		{path: "b", size: 1024, mod: 2},
		{path: "c", size: 1024, mod: 3},
		{path: "d", size: 1024, mod: 4},
	}
}

const (
	fourKiBTotal  = 4096
	twoFileBudget = 2048
)

// TestPruneToBudgetCreditsAFileThatIsAlreadyGone is the regression. The oldest
// two are gone before this pass reaches them, so the store is ALREADY at budget
// and nothing more may be deleted. Crediting only remove's own successes left
// total at 4096 and marched on into c and d — live thumbnails, inside the
// budget, deleted because someone else got there first.
func TestPruneToBudgetCreditsAFileThatIsAlreadyGone(t *testing.T) {
	remove, deleted := vanishing("a", "b")
	pruneToBudget(fourKiB(), fourKiBTotal, twoFileBudget, remove)
	if len(*deleted) != 0 {
		t.Errorf("prune deleted %v — the two files another sweep already took had freed the budget, so nothing was over it", *deleted)
	}
}

// TestPruneToBudgetKeepsTheBytesOfAFileItCouldNotDelete pins the other half of
// that distinction, and is the reason the fix tests the error rather than
// subtracting unconditionally: a file that failed to delete for any REASON
// OTHER than being gone still occupies its bytes, so the sweep must keep
// counting them and take the next-oldest instead of stopping a file short of
// the budget.
func TestPruneToBudgetKeepsTheBytesOfAFileItCouldNotDelete(t *testing.T) {
	var deleted []string
	remove := func(path string) error {
		if path == "a" {
			return errors.New("locked by another process")
		}
		deleted = append(deleted, path)
		return nil
	}
	pruneToBudget(fourKiB(), fourKiBTotal, twoFileBudget, remove)
	// a survives this sweep, so freeing 2048 bytes has to come from b and c.
	if len(deleted) != 2 || deleted[0] != "b" || deleted[1] != "c" {
		t.Errorf("prune deleted %v, want [b c] — a locked file keeps its bytes, so the sweep owes two more deletions", deleted)
	}
}

// TestPruneToBudgetStopsAtTheBudget is the ordinary path, kept here beside the
// two above so the oldest-first order and the stop condition are pinned against
// the same fixture the edge cases use.
func TestPruneToBudgetStopsAtTheBudget(t *testing.T) {
	remove, deleted := vanishing()
	pruneToBudget(fourKiB(), fourKiBTotal, twoFileBudget, remove)
	if len(*deleted) != 2 || (*deleted)[0] != "a" || (*deleted)[1] != "b" {
		t.Errorf("prune deleted %v, want [a b] — oldest first, stopping the moment the store is at budget", *deleted)
	}
}
