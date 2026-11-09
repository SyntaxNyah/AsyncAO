package wardrobe

import (
	"slices"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

func chars(groups []Group) []string {
	var out []string
	for _, g := range groups {
		for _, it := range g.Items {
			out = append(out, it.Char)
		}
	}
	return out
}

func onServerOf(names ...string) func(string) bool {
	return func(c string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, c) })
	}
}

func TestParseServerListKeepsTheServersOrder(t *testing.T) {
	got := ParseServerList([]byte("Godot\r\n\n  Phoenix  \nphoenix\nAdrian"))
	if want := []string{"Godot", "Phoenix", "Adrian"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseServerListIsCapped(t *testing.T) {
	var b strings.Builder
	for i := range ServerListCap + 1 {
		b.WriteString("c")
		b.WriteString(strings.Repeat("x", i))
		b.WriteString("\n")
	}
	if got := ParseServerList([]byte(b.String())); len(got) != ServerListCap {
		t.Fatalf("parsed %d names, want the cap %d", len(got), ServerListCap)
	}
}

var (
	testFolders = []config.WardrobeFolder{{ID: "f1", Name: "Defense"}, {ID: "f2", Name: "Prosecution"}}
	testEntries = []config.WardrobeEntry{
		{Char: "Phoenix", Folders: []string{"f1"}, LastWorn: 300},
		{Char: "Phoenix (AJ)", Nickname: "Older Phoenix", Folders: []string{"f1"}},
		{Char: "Godot", Folders: []string{"f2", "f1"}, LastWorn: 900},
		{Char: "Judge", LastWorn: 100},
	}
)

func TestBuildFiltersByFolder(t *testing.T) {
	on := onServerOf("Phoenix", "Godot", "Judge")
	if got := chars(Build(testEntries, testFolders, nil, on, Query{Folder: "f1"})); !slices.Equal(got, []string{"Phoenix", "Phoenix (AJ)", "Godot"}) {
		t.Errorf("folder f1, Godot filed in two folders: %v", got)
	}
	if got := chars(Build(testEntries, testFolders, nil, on, Query{Folder: "f2"})); !slices.Equal(got, []string{"Godot"}) {
		t.Errorf("folder f2: %v", got)
	}
	if got := chars(Build(testEntries, testFolders, nil, on, Query{Folder: Unfiled})); !slices.Equal(got, []string{"Judge"}) {
		t.Errorf("unfiled: %v", got)
	}
	if got := chars(Build(testEntries, testFolders, nil, on, Query{Folder: AllFolders})); len(got) != len(testEntries) {
		t.Errorf("all: %v", got)
	}
}

func TestBuildSearchSpansFoldersAndMatchesBothNames(t *testing.T) {
	on := onServerOf()
	if got := chars(Build(testEntries, testFolders, nil, on, Query{Folder: "f2", Search: "older"})); !slices.Equal(got, []string{"Phoenix (AJ)"}) {
		t.Errorf("nickname search from another folder: %v", got)
	}
	if got := chars(Build(testEntries, testFolders, nil, on, Query{Folder: Unfiled, Search: "(aj)"})); !slices.Equal(got, []string{"Phoenix (AJ)"}) {
		t.Errorf("folder-name search: %v", got)
	}
}

func TestBuildMarksIniswapsAndNicknames(t *testing.T) {
	groups := Build(testEntries, testFolders, nil, onServerOf("Phoenix"), Query{Folder: "f1"})
	items := groups[0].Items
	if items[0].Iniswap || !items[1].Iniswap {
		t.Errorf("iniswap marks: %+v", items)
	}
	if items[1].Name != "Older Phoenix" || !items[1].HasNickname || items[0].HasNickname {
		t.Errorf("nicknames: %+v", items)
	}
}

func TestBuildSorts(t *testing.T) {
	on := onServerOf()
	all := Query{Folder: AllFolders}
	if got := chars(Build(testEntries, testFolders, nil, on, all)); !slices.Equal(got, []string{"Phoenix", "Phoenix (AJ)", "Godot", "Judge"}) {
		t.Errorf("custom keeps the saved order: %v", got)
	}
	all.Sort = SortName
	if got := chars(Build(testEntries, testFolders, nil, on, all)); !slices.Equal(got, []string{"Godot", "Judge", "Phoenix (AJ)", "Phoenix"}) {
		t.Errorf("name sorts by the shown name: %v", got)
	}
	all.Sort = SortLastWorn
	if got := chars(Build(testEntries, testFolders, nil, on, all)); !slices.Equal(got, []string{"Godot", "Phoenix", "Judge", "Phoenix (AJ)"}) {
		t.Errorf("last worn, never-worn last: %v", got)
	}
}

func TestBuildGroupsByFolderInFolderOrder(t *testing.T) {
	groups := Build(testEntries, testFolders, nil, onServerOf(), Query{Folder: AllFolders, Sort: SortFolder})
	var order []string
	for _, g := range groups {
		if !g.Titled {
			t.Fatalf("folder groups must carry a heading: %+v", g)
		}
		order = append(order, g.Folder)
	}
	if !slices.Equal(order, []string{"f1", "f2", Unfiled}) {
		t.Fatalf("group order %v", order)
	}
	if got := chars(groups[:1]); !slices.Contains(got, "Godot") {
		t.Errorf("Godot is in f1 too, so its group must list him: %v", got)
	}
}

func TestBuildServerListMarksSavedOnes(t *testing.T) {
	groups := Build(testEntries, testFolders, []string{"Adrian", "phoenix"}, onServerOf("Phoenix"), Query{Source: SourceServer, Sort: SortFolder})
	items := groups[0].Items
	if len(groups) != 1 || groups[0].Titled {
		t.Fatalf("the server list has no folders, so no headings: %+v", groups)
	}
	if items[0].Char != "Adrian" || items[0].Saved || !items[0].Iniswap || !items[1].Saved || items[1].Iniswap {
		t.Fatalf("server items: %+v", items)
	}
}

func TestParseNames(t *testing.T) {
	const limit = 3
	got := ParseNames("Phoenix\r\n Maya, phoenix,,\nGodot\nJudge", limit)
	if want := []string{"Phoenix", "Maya", "Godot"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
