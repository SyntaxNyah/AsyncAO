package cache

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const relocateURL = "https://example.com/characters/phoenix/char_icon.png"

// relocateCache opens a cache under a fresh temp folder and stores one blob directly on disk.
func relocateCache(t *testing.T) (*DiskCache, string) {
	t.Helper()
	root := DiskRootIn(t.TempDir())
	d, err := NewDiskCache(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	path := d.pathFor(relocateURL)
	if err := os.MkdirAll(filepath.Dir(path), diskDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("png"), diskDirPerm); err != nil {
		t.Fatal(err)
	}
	return d, root
}

func TestRelocateMovesTheCache(t *testing.T) {
	d, old := relocateCache(t)
	next := DiskRootIn(t.TempDir())
	if err := d.Relocate(next, true); err != nil {
		t.Fatal(err)
	}
	if d.Root() != next {
		t.Fatalf("root = %s, want %s", d.Root(), next)
	}
	if data, ok := d.Get(relocateURL); !ok || string(data) != "png" {
		t.Fatal("the moved blob must read from the new folder")
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the old folder must be gone: %v", err)
	}
}

func TestRelocateWithoutMovingDeletesTheOldCache(t *testing.T) {
	d, old := relocateCache(t)
	next := DiskRootIn(t.TempDir())
	if err := d.Relocate(next, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Get(relocateURL); ok {
		t.Fatal("nothing was moved, so the new folder must miss")
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the old folder must be deleted: %v", err)
	}
}

func TestRelocateRefusesNestedFolders(t *testing.T) {
	d, old := relocateCache(t)
	for _, next := range []string{filepath.Join(old, "inner"), filepath.Dir(old)} {
		if err := d.Relocate(next, true); !errors.Is(err, ErrRelocateNested) {
			t.Errorf("Relocate(%s) = %v, want ErrRelocateNested", next, err)
		}
	}
	if d.Root() != old {
		t.Fatal("a refused move must leave the cache where it was")
	}
}

func TestDiskRootInNeverUsesTheChosenFolderItself(t *testing.T) {
	dir := t.TempDir()
	root := DiskRootIn(dir)
	if root == dir || !within(dir, root) {
		t.Fatalf("DiskRootIn(%s) = %s, want a folder of AsyncAO's own inside it", dir, root)
	}
}
