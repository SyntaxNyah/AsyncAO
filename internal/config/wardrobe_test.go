package config

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func wardrobeChars(w *Wardrobe) []string {
	entries, _ := w.Snapshot()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Char)
	}
	return out
}

func newTestWardrobe(t *testing.T) *Wardrobe {
	t.Helper()
	w := OpenWardrobe(filepath.Join(t.TempDir(), WardrobeFileName), nil)
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestWardrobeMigrationMergesEveryServer(t *testing.T) {
	const serverA, serverB = "wss://a.example/", "wss://b.example/"
	old := &Settings{
		Wardrobe: []string{"Phoenix"},
		ServerWarm: map[string]ServerWarmInfo{
			serverB: {Wardrobe: []string{"phoenix", "Godot"}, WardrobeFolder: map[string]string{"godot": "Prosecution"}},
			serverA: {Wardrobe: []string{"Edgeworth", "Maya"}, WardrobeFolder: map[string]string{"edgeworth": "Prosecution"}},
		},
	}
	w := OpenWardrobe(filepath.Join(t.TempDir(), WardrobeFileName), old)
	t.Cleanup(func() { _ = w.Close() })

	if got, want := wardrobeChars(w), []string{"Phoenix", "Edgeworth", "Maya", "Godot"}; !slices.Equal(got, want) {
		t.Fatalf("migrated entries = %v, want %v (flat list first, servers in key order, duplicates collapsed)", got, want)
	}
	entries, folders := w.Snapshot()
	if len(folders) != 1 || folders[0].Name != "Prosecution" || folders[0].Server != serverA {
		t.Fatalf("folders = %+v, want one Prosecution folder from the first server", folders)
	}
	for _, e := range entries {
		filed := slices.Equal(e.Folders, []string{folders[0].ID})
		if want := e.Char == "Edgeworth" || e.Char == "Godot"; filed != want {
			t.Errorf("%s filed = %v, want %v", e.Char, filed, want)
		}
	}
}

func TestWardrobeMigratesOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), WardrobeFileName)
	old := &Settings{Wardrobe: []string{"Phoenix"}}
	w := OpenWardrobe(path, old)
	w.Remove("Phoenix")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	again := OpenWardrobe(path, old)
	t.Cleanup(func() { _ = again.Close() })
	if got := wardrobeChars(again); len(got) != 0 {
		t.Errorf("reopening migrated the old list again: %v", got)
	}
}

func TestWardrobeSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), WardrobeFileName)
	w := OpenWardrobe(path, nil)
	id, err := w.AddFolder("Defense", "wss://a.example/", "shield", "blue")
	if err != nil {
		t.Fatal(err)
	}
	w.Add([]string{"Phoenix", "Maya"}, id)
	w.SetNickname("Maya", "Mystic")
	worn := time.Unix(time.Now().Unix(), 0)
	w.MarkWorn("Phoenix", worn)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	back := OpenWardrobe(path, nil)
	t.Cleanup(func() { _ = back.Close() })
	entries, folders := back.Snapshot()
	if len(folders) != 1 || folders[0].ID != id || folders[0].Icon != "shield" || folders[0].Color != "blue" {
		t.Fatalf("folders after restart = %+v", folders)
	}
	if len(entries) != 2 || entries[0].LastWorn != worn.Unix() || entries[1].Nickname != "Mystic" || !slices.Equal(entries[1].Folders, []string{id}) {
		t.Fatalf("entries after restart = %+v", entries)
	}
}

func TestWardrobeAddSkipsSavedAndEmpty(t *testing.T) {
	w := newTestWardrobe(t)
	if n := w.Add([]string{"Phoenix", " ", "phoenix", "Maya"}, "no-such-folder"); n != len([]string{"Phoenix", "Maya"}) {
		t.Fatalf("Add returned %d", n)
	}
	entries, _ := w.Snapshot()
	for _, e := range entries {
		if len(e.Folders) != 0 {
			t.Errorf("%s was filed under an unknown folder %v", e.Char, e.Folders)
		}
	}
	if !w.Has("PHOENIX") {
		t.Error("Has must ignore case")
	}
}

func TestWardrobeMoveReordersLikeADrag(t *testing.T) {
	w := newTestWardrobe(t)
	w.Add([]string{"A", "B", "C", "D"}, "")
	w.Move("A", "C") // down: lands after the target
	if got, want := wardrobeChars(w), []string{"B", "C", "A", "D"}; !slices.Equal(got, want) {
		t.Fatalf("after moving down: %v, want %v", got, want)
	}
	w.Move("D", "B") // up: lands before the target
	if got, want := wardrobeChars(w), []string{"D", "B", "C", "A"}; !slices.Equal(got, want) {
		t.Fatalf("after moving up: %v, want %v", got, want)
	}
}

func TestWardrobeFolderNamesAreUniqueAndNotEmpty(t *testing.T) {
	w := newTestWardrobe(t)
	id, err := w.AddFolder("Defense", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddFolder("  ", "", "", ""); !errors.Is(err, ErrWardrobeNameEmpty) {
		t.Errorf("empty name: %v", err)
	}
	if _, err := w.AddFolder("defense", "", "", ""); !errors.Is(err, ErrWardrobeNameTaken) {
		t.Errorf("duplicate name: %v", err)
	}
	if err := w.RenameFolder(id, "DEFENSE"); err != nil {
		t.Errorf("renaming a folder to its own name in another case must work: %v", err)
	}
	long := strings.Repeat("x", WardrobeNameMaxRunes+1)
	other, err := w.AddFolder(long, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, folders := w.Snapshot()
	for _, f := range folders {
		if f.ID == other && len([]rune(f.Name)) != WardrobeNameMaxRunes {
			t.Errorf("a long name was not capped: %d runes", len([]rune(f.Name)))
		}
	}
}

func TestWardrobeDeleteFolderKeepsOrDropsMembers(t *testing.T) {
	w := newTestWardrobe(t)
	keep, _ := w.AddFolder("Keep", "", "", "")
	drop, _ := w.AddFolder("Drop", "", "", "")
	other, _ := w.AddFolder("Other", "", "", "")
	w.Add([]string{"A"}, keep)
	w.Add([]string{"B", "C"}, drop)
	w.AddToFolder("C", other)

	w.DeleteFolder(keep, true)
	w.DeleteFolder(drop, false)
	entries, folders := w.Snapshot()
	if len(folders) != 1 || folders[0].ID != other {
		t.Fatalf("folders left: %+v", folders)
	}
	if got := wardrobeChars(w); !slices.Equal(got, []string{"A", "C"}) {
		t.Fatalf("entries = %v, want A kept unfiled and C kept for its other folder; B, only in Drop, gone", got)
	}
	if len(entries[0].Folders) != 0 || !slices.Equal(entries[1].Folders, []string{other}) {
		t.Fatalf("folders after delete: %+v", entries)
	}
}

func TestWardrobeCharacterInSeveralFolders(t *testing.T) {
	w := newTestWardrobe(t)
	a, _ := w.AddFolder("A", "", "", "")
	b, _ := w.AddFolder("B", "", "", "")
	w.Add([]string{"Phoenix"}, a)
	w.AddToFolder("Phoenix", b)
	w.AddToFolder("Phoenix", b) // already there
	w.AddToFolder("Phoenix", "no-such-folder")
	entries, _ := w.Snapshot()
	if !slices.Equal(entries[0].Folders, []string{a, b}) {
		t.Fatalf("folders = %v, want both", entries[0].Folders)
	}
	w.MoveToFolder("Phoenix", a, b) // already in b: just leaves a
	entries, _ = w.Snapshot()
	if !slices.Equal(entries[0].Folders, []string{b}) {
		t.Fatalf("after moving into a folder it was already in: %v", entries[0].Folders)
	}
	w.MoveToFolder("Phoenix", b, a)
	w.AddToFolder("Phoenix", b)
	w.RemoveFromFolder("Phoenix", a)
	entries, _ = w.Snapshot()
	if !slices.Equal(entries[0].Folders, []string{b}) {
		t.Fatalf("after move, add and remove: %v", entries[0].Folders)
	}
	w.ClearFolders("Phoenix")
	entries, _ = w.Snapshot()
	if len(entries[0].Folders) != 0 {
		t.Fatalf("after clearing: %v", entries[0].Folders)
	}
}

func TestWardrobeSnapshotDoesNotShareFolders(t *testing.T) {
	w := newTestWardrobe(t)
	a, _ := w.AddFolder("A", "", "", "")
	w.Add([]string{"Phoenix"}, a)
	entries, _ := w.Snapshot()
	entries[0].Folders[0] = "changed"
	again, _ := w.Snapshot()
	if again[0].Folders[0] != a {
		t.Fatal("changing a snapshot changed the store")
	}
}

func TestWardrobeNicknameEqualToTheNameClearsIt(t *testing.T) {
	w := newTestWardrobe(t)
	w.Add([]string{"Phoenix"}, "")
	w.SetNickname("Phoenix", "Nick")
	w.SetNickname("Phoenix", "phoenix")
	entries, _ := w.Snapshot()
	if entries[0].Nickname != "" {
		t.Errorf("nickname = %q, want cleared", entries[0].Nickname)
	}
}

func TestWardrobeLookAndImage(t *testing.T) {
	w := newTestWardrobe(t)
	id, _ := w.AddFolder("F", "", "folder", "blue")
	w.SetFolderImage(id, filepath.Join("..", "evil.png"))
	_, folders := w.Snapshot()
	if folders[0].Image != "evil.png" {
		t.Fatalf("image = %q, want only the base name", folders[0].Image)
	}
	w.SetFolderLook(id, "star", "red")
	_, folders = w.Snapshot()
	if folders[0].Image != "" || folders[0].Icon != "star" || folders[0].Color != "red" {
		t.Fatalf("after picking a built-in icon: %+v", folders[0])
	}
}

func TestWardrobeRevChangesOnlyOnChange(t *testing.T) {
	w := newTestWardrobe(t)
	start := w.Rev()
	w.Remove("nobody")
	w.AddToFolder("nobody", "")
	if w.Rev() != start {
		t.Error("a no-op bumped the revision")
	}
	w.Add([]string{"A"}, "")
	if w.Rev() == start {
		t.Error("an add did not bump the revision")
	}
}
