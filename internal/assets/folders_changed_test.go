package assets

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestChangedFolderFilesReloadsAnEdit: an edited folder file is reported once, the next read gets the new copy, and
// a file set aside after a failed decode is tried again.
func TestChangedFolderFilesReloadsAnEdit(t *testing.T) {
	const before, after = "[Options]\nshowname = Old\n", "[Options]\nshowname = New\n"
	pr := newPackRig(t, nil, map[string]string{"characters/witch/char.ini": before})
	m := pr.rig.manager
	url := pr.base("characters/witch/char.ini")
	if data, err := m.FetchRaw(context.Background(), url); err != nil || string(data) != before {
		t.Fatalf("first read = %q, %v", data, err)
	}
	if got := m.ChangedFolderFiles(); len(got) != 0 {
		t.Fatalf("nothing was edited, ChangedFolderFiles() = %v", got)
	}

	p := filepath.Join(pr.dir, "characters", "witch", "char.ini")
	if err := os.WriteFile(p, []byte(after), packFileMode); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(packLater)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if !m.QuarantinePack(url) {
		t.Fatal("the folder file could not be set aside")
	}
	if got := m.ChangedFolderFiles(); !slices.Equal(got, []string{url}) {
		t.Fatalf("ChangedFolderFiles() = %v, want [%s]", got, url)
	}
	if data, err := m.FetchRaw(context.Background(), url); err != nil || string(data) != after {
		t.Errorf("read after the edit = %q, %v; want the new copy", data, err)
	}
}
