package cache

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const partialTestURL = "https://host.test/base/sounds/music/a.opus"

var partialTestBody = []byte("a whole track, arrived")

func TestAdoptMakesAFinishedDownloadTheCachedCopy(t *testing.T) {
	d, err := NewDiskCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f, err := d.PartialFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(partialTestBody); err != nil {
		t.Fatal(err)
	}
	f.Close()
	d.Adopt(partialTestURL, f.Name())
	if got := waitForBlob(t, d, partialTestURL); !bytes.Equal(got, partialTestBody) {
		t.Fatalf("cached %q, want %q", got, partialTestBody)
	}
	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Fatalf("the partial file is still there: %v", err)
	}
	if got, err := os.ReadFile(d.Path(partialTestURL)); err != nil || !bytes.Equal(got, partialTestBody) {
		t.Fatalf("Path reads %q, %v", got, err)
	}
}

func TestOpeningClearsDownloadsALastSessionLeft(t *testing.T) {
	root := t.TempDir()
	d, err := NewDiskCache(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.PartialFile()
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	d.Close()
	again, err := NewDiskCache(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	if _, err := os.Stat(filepath.Join(root, partialDir)); !os.IsNotExist(err) {
		t.Fatalf("leftover downloads survived opening: %v", err)
	}
}
