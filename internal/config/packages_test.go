package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writePackageTree lays out a packages/ folder under root and returns its path.
func writePackageTree(t *testing.T, root string, dirs []string, sortLines []string) string {
	t.Helper()
	pk := filepath.Join(root, PackagesDirName)
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(pk, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if sortLines != nil {
		data := ""
		for _, l := range sortLines {
			data += l + "\n"
		}
		if err := os.WriteFile(filepath.Join(pk, PackagesSortName), []byte(data), 0o644); err != nil {
			t.Fatalf("write sorting.txt: %v", err)
		}
	}
	return pk
}

// TestDiscoverPackagesSorting pins the ordering contract: sorting.txt entries
// first (in listed order), then unlisted packages lexically, and stray files
// never count as packages.
func TestDiscoverPackagesSorting(t *testing.T) {
	root := t.TempDir()
	pk := writePackageTree(t, root,
		[]string{"Zeta", "Alpha", "Beta", "music pack"},
		[]string{"# priority order", "music pack", "Beta"},
	)

	// A stray file at the top level must be ignored.
	if err := os.WriteFile(filepath.Join(pk, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := DiscoverPackages(root)
	want := []string{
		filepath.Join(pk, "music pack"), // sorting.txt order, first listed wins
		filepath.Join(pk, "Beta"),       // sorting.txt order
		filepath.Join(pk, "Alpha"),      // unlisted, lexical
		filepath.Join(pk, "Zeta"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverPackages = %v, want %v", got, want)
	}
}

// TestDiscoverPackagesNoSortFile pins lexical order when sorting.txt is absent.
func TestDiscoverPackagesNoSortFile(t *testing.T) {
	root := t.TempDir()
	pk := writePackageTree(t, root, []string{"B", "A", "C"}, nil)
	got := DiscoverPackages(root)
	want := []string{
		filepath.Join(pk, "A"),
		filepath.Join(pk, "B"),
		filepath.Join(pk, "C"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverPackages = %v, want %v", got, want)
	}
}

// TestDiscoverPackagesDegrades pins the "no packages is not an error" contract:
// a missing folder, an empty exeDir, and a listed-but-absent package all come
// back cleanly.
func TestDiscoverPackagesDegrades(t *testing.T) {
	if got := DiscoverPackages(""); got != nil {
		t.Fatalf("empty exeDir = %v, want nil", got)
	}
	if got := DiscoverPackages(filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Fatalf("missing packages dir = %v, want nil", got)
	}

	root := t.TempDir()
	pk := writePackageTree(t, root, []string{"Real"}, []string{"Ghost", "Real"})
	got := DiscoverPackages(root)
	if len(got) != 1 || got[0] != filepath.Join(pk, "Real") {
		t.Fatalf("a listed-but-absent package must be skipped: %v", got)
	}
}

// TestCombineMountsAppends pins manual-first ordering and the no-auto fast path.
func TestCombineMountsAppends(t *testing.T) {
	manual := []string{"m1", "m2"}
	auto := []string{"p1", "p2"}
	if got := CombineMounts(manual, auto); !reflect.DeepEqual(got, []string{"m1", "m2", "p1", "p2"}) {
		t.Fatalf("CombineMounts = %v", got)
	}
	if got := CombineMounts(manual, nil); !reflect.DeepEqual(got, manual) {
		t.Fatalf("no auto mounts must return the manual list unchanged, got %v", got)
	}
}
