package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/ui/wardrobe"
)

// wardrobeTestApp is a headless App with an empty Wardrobe store in a temp folder.
func wardrobeTestApp(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	a.ward.store = config.OpenWardrobe(filepath.Join(t.TempDir(), config.WardrobeFileName), nil)
	t.Cleanup(func() { _ = a.ward.store.Close() })
	a.ward.folder = wardrobe.AllFolders
	return a
}

func wardrobeFoldersOf(t *testing.T, a *App, char string) []string {
	t.Helper()
	entries, _ := a.ward.store.Snapshot()
	for _, e := range entries {
		if e.Char == char {
			return e.Folders
		}
	}
	t.Fatalf("%s is not saved", char)
	return nil
}

func TestWardrobeUseChoice(t *testing.T) {
	const freeSlot = 3
	for _, tc := range []struct {
		name        string
		slot        int
		taken, mine bool
		switches    bool
	}{
		{"free server character", freeSlot, false, false, true},
		{"taken", freeSlot, true, false, false},
		{"already ours", freeSlot, true, true, false},
		{"not on the server", -1, false, false, false},
	} {
		switches, why := wardrobeUseChoice(tc.slot, tc.taken, tc.mine)
		if switches != tc.switches {
			t.Errorf("%s: switch = %v, want %v", tc.name, switches, tc.switches)
		}
		if !switches && why == "" {
			t.Errorf("%s: wearing instead must say why", tc.name)
		}
	}
}

func TestWardrobeDropAddsFromAllAndMovesInsideAFolder(t *testing.T) {
	a := wardrobeTestApp(t)
	s := a.ward.store
	defense, _ := s.AddFolder("Defense", "", "", "")
	witnesses, _ := s.AddFolder("Witnesses", "", "", "")
	s.Add([]string{"Phoenix"}, defense)

	a.ward.folder = wardrobe.AllFolders
	a.dropWardrobeOnFolder("Phoenix", witnesses)
	if got := wardrobeFoldersOf(t, a, "Phoenix"); !slices.Equal(got, []string{defense, witnesses}) {
		t.Fatalf("a drop from All favourites must add the folder: %v", got)
	}

	a.ward.folder = witnesses
	a.dropWardrobeOnFolder("Phoenix", wardrobe.Unfiled)
	if got := wardrobeFoldersOf(t, a, "Phoenix"); !slices.Equal(got, []string{defense}) {
		t.Fatalf("a drop on Unfiled from inside a folder must only leave that folder: %v", got)
	}

	a.ward.folder = defense
	a.dropWardrobeOnFolder("Phoenix", witnesses)
	if got := wardrobeFoldersOf(t, a, "Phoenix"); !slices.Equal(got, []string{witnesses}) {
		t.Fatalf("a drop from inside a folder must move: %v", got)
	}

	a.ward.folder = wardrobe.AllFolders
	a.dropWardrobeOnFolder("Phoenix", wardrobe.Unfiled)
	if got := wardrobeFoldersOf(t, a, "Phoenix"); len(got) != 0 {
		t.Fatalf("a drop on Unfiled from All favourites must clear every folder: %v", got)
	}
}

func TestWardrobeFinishDragAppliesTheDrop(t *testing.T) {
	a := wardrobeTestApp(t)
	a.ward.store.Add([]string{"A", "B", "C"}, "")
	a.ward.dragKind, a.ward.dragID, a.ward.dragging = wardDragChar, "A", true
	a.ward.dropKind, a.ward.dropID, a.ward.dropOver = wardDragChar, "C", true
	a.finishWardrobeDrag() // the mouse is up in a fresh Ctx
	entries, _ := a.ward.store.Snapshot()
	var order []string
	for _, e := range entries {
		order = append(order, e.Char)
	}
	if !slices.Equal(order, []string{"B", "C", "A"}) {
		t.Fatalf("order after dropping A on C = %v", order)
	}
	if a.ward.dragKind != wardDragNone || a.ward.dragging {
		t.Fatal("the drag must end on release")
	}
}

func TestWardrobeEscClosesOneLayerAtATime(t *testing.T) {
	a := wardrobeTestApp(t)
	a.ward.show, a.ward.addOpen, a.ward.moreMenu, a.ward.lookFor = true, true, true, "f1"
	a.closeWardrobeTop()
	if a.ward.addOpen || !a.ward.moreMenu {
		t.Fatal("Esc must close the Add window first, and only it")
	}
	a.closeWardrobeTop()
	if a.ward.moreMenu || a.ward.lookFor == "" {
		t.Fatal("then the menus, and only them")
	}
	a.closeWardrobeTop()
	if a.ward.lookFor != "" || !a.ward.show {
		t.Fatal("then the folder editors, leaving the window open")
	}
	a.closeWardrobeTop()
	if a.ward.show {
		t.Fatal("and last the window itself")
	}
}

func TestWardrobeViewCountsEveryFolderACharacterIsIn(t *testing.T) {
	a := wardrobeTestApp(t)
	s := a.ward.store
	defense, _ := s.AddFolder("Defense", "", "", "")
	prosecution, _ := s.AddFolder("Prosecution", "", "", "")
	s.Add([]string{"Godot"}, defense)
	s.AddToFolder("Godot", prosecution)
	s.Add([]string{"Judge"}, "")
	a.refreshWardrobeView()

	counts := map[string]int{}
	for _, row := range a.ward.rows {
		counts[row.folder.Name] = row.count
	}
	want := map[string]int{"All favourites": 2, "Defense": 1, "Prosecution": 1, "Unfiled": 1}
	for name, n := range want {
		if counts[name] != n {
			t.Errorf("%s count = %d, want %d (all rows: %v)", name, counts[name], n, counts)
		}
	}
	if got := a.ward.folderLabels["Godot"]; got != "Defense +1" {
		t.Errorf("List view folder label = %q, want \"Defense +1\"", got)
	}
}

func TestWardrobeHasFollowsTheStore(t *testing.T) {
	a := wardrobeTestApp(t)
	if a.wardrobeHas("phoenix") {
		t.Fatal("empty store")
	}
	a.ward.store.Add([]string{"Phoenix"}, "")
	if !a.wardrobeHas("phoenix") {
		t.Fatal("an add must show on Character Select's star")
	}
	a.ward.store.Remove("Phoenix")
	if a.wardrobeHas("phoenix") {
		t.Fatal("a remove must show too")
	}
}

func TestWardrobeWornLabel(t *testing.T) {
	const now = int64(1_000_000)
	for _, tc := range []struct {
		worn int64
		want string
	}{
		{0, "Never"},
		{now - secondsPerMinute/2, "Just now"},
		{now - 5*secondsPerMinute, "5 min ago"},
		{now - 3*secondsPerHour, "3 h ago"},
		{now - 2*secondsPerDay, "2 d ago"},
		{now - 3*secondsPerWeek, "3 wk ago"},
	} {
		if got := wardrobeWornLabel(tc.worn, now); got != tc.want {
			t.Errorf("worn %d s ago = %q, want %q", now-tc.worn, got, tc.want)
		}
	}
}

func TestListFontNamesAndSuggestions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Arial.ttf", "arial.otf", "Comic Sans MS.ttf", "Segoe UI.ttc", "readme.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	names := listFontNames([]string{dir, filepath.Join(dir, "missing")})
	if want := []string{"Arial", "Comic Sans MS", "Segoe UI"}; !slices.Equal(names, want) {
		t.Fatalf("font names = %v, want %v (one per name ignoring case, no non-fonts)", names, want)
	}
	fp := fontPicker{names: names, lower: []string{"arial", "comic sans ms", "segoe ui"}}
	n := fp.fontSuggestions("s")
	var got []string
	for i := range n {
		got = append(got, fp.names[fp.match[i]])
	}
	if want := []string{"Segoe UI", "Comic Sans MS"}; !slices.Equal(got, want) {
		t.Fatalf("suggestions for \"s\" = %v, want names starting with it first: %v", got, want)
	}
}
