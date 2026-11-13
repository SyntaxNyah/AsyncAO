// Package wardrobe builds what the Wardrobe window shows from the saved entries and the server's iniswaps.ini: the
// filtering, sorting and grouping, with no drawing, so the window only paints the result.
package wardrobe

import (
	"bytes"
	"cmp"
	"slices"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

const (
	// ServerListFileName is the server's list of iniswap folders at its asset origin, AO2's name for the file
	// (courtroom.cpp:5349).
	ServerListFileName = "iniswaps.ini"
	// ServerListCap bounds a parsed list against a hostile file.
	ServerListCap = 4096
)

// ParseServerList reads one character folder per line, as AO2's get_list_file does, dropping blank lines and
// repeats (ignoring case) and keeping the server's order.
func ParseServerList(data []byte) []string {
	var out []string
	seen := map[string]bool{}
	for line := range bytes.Lines(data) {
		name := strings.TrimSpace(string(line))
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
		if len(out) == ServerListCap {
			break
		}
	}
	return out
}

// Source is which list the window shows.
type Source int

const (
	SourceUser Source = iota
	SourceServer
)

// Sort is the order items are shown in.
type Sort int

const (
	// SortCustom is the player's own order (the server's order for the server list).
	SortCustom Sort = iota
	SortName
	SortLastWorn
	// SortFolder groups items by folder, in folder order, by name inside each.
	SortFolder
)

// Folder filters for Query.Folder besides a folder id.
const (
	// AllFolders shows every saved entry; folder ids never start with this.
	AllFolders = "*"
	// Unfiled shows entries in no folder.
	Unfiled = ""
)

// Query selects and orders the items.
type Query struct {
	Source Source
	Folder string
	// Search is matched, ignoring case, against the shown name and the character folder.
	Search string
	Sort   Sort
	// Reverse turns the order around, folders included; the saved custom order itself never changes.
	Reverse bool
}

// Item is one card or row.
type Item struct {
	// Char is the character folder, the item's identity.
	Char string
	// Name is what the card shows: the nickname when there is one, else Char.
	Name string
	// HasNickname says Char is shown smaller under Name.
	HasNickname bool
	// Folders are the entry's folder ids; none for an unfiled entry and for server items.
	Folders []string
	// Iniswap says the server has no character slot for Char.
	Iniswap bool
	// LastWorn is Unix seconds; 0 is never.
	LastWorn int64
	// Saved says a server item is already in the player's favourites.
	Saved bool
}

// Group is a run of items under an optional folder heading; Folder is "" with Titled false when there is no heading.
type Group struct {
	Titled bool
	Folder string
	Items  []Item
}

// Build returns the groups the window shows. onServer reports whether the server has a slot for a character folder.
func Build(entries []config.WardrobeEntry, folders []config.WardrobeFolder, server []string, onServer func(string) bool, q Query) []Group {
	search := strings.ToLower(strings.TrimSpace(q.Search))
	var items []Item
	if q.Source == SourceServer {
		saved := map[string]bool{}
		for _, e := range entries {
			saved[strings.ToLower(e.Char)] = true
		}
		for _, c := range server {
			it := Item{Char: c, Name: c, Iniswap: !onServer(c), Saved: saved[strings.ToLower(c)]}
			if matches(it, search) {
				items = append(items, it)
			}
		}
	} else {
		for _, e := range entries {
			if search == "" && !InFolder(e.Folders, q.Folder) {
				continue
			}
			it := Item{Char: e.Char, Name: e.Char, Folders: e.Folders, Iniswap: !onServer(e.Char), LastWorn: e.LastWorn}
			if e.Nickname != "" {
				it.Name, it.HasNickname = e.Nickname, true
			}
			if matches(it, search) {
				items = append(items, it)
			}
		}
	}

	sortBy := q.Sort
	if q.Source == SourceServer && sortBy == SortFolder {
		sortBy = SortName
	}
	switch sortBy {
	case SortName, SortFolder:
		slices.SortStableFunc(items, byName)
	case SortLastWorn:
		slices.SortStableFunc(items, byLastWorn)
	}
	if q.Reverse {
		slices.Reverse(items)
	}
	if sortBy != SortFolder {
		return []Group{{Items: items}}
	}
	var groups []Group
	for _, f := range folders {
		if g := folderGroup(items, f.ID); len(g.Items) > 0 {
			groups = append(groups, g)
		}
	}
	if g := folderGroup(items, Unfiled); len(g.Items) > 0 {
		groups = append(groups, g)
	}
	if q.Reverse {
		slices.Reverse(groups)
	}
	return groups
}

func matches(it Item, search string) bool {
	return search == "" || strings.Contains(strings.ToLower(it.Name), search) || strings.Contains(strings.ToLower(it.Char), search)
}

// InFolder reports whether an entry filed in folders belongs under filter: AllFolders, Unfiled, or a folder id.
func InFolder(folders []string, filter string) bool {
	switch filter {
	case AllFolders:
		return true
	case Unfiled:
		return len(folders) == 0
	}
	return slices.Contains(folders, filter)
}

// folderGroup is the items under folder id; an item in several folders appears in each of their groups.
func folderGroup(items []Item, id string) Group {
	g := Group{Titled: true, Folder: id}
	for _, it := range items {
		if InFolder(it.Folders, id) {
			g.Items = append(g.Items, it)
		}
	}
	return g
}

// byName orders by the shown name, then by character folder so equal names keep a fixed order.
func byName(a, b Item) int {
	return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Char, b.Char))
}

// byLastWorn puts the most recently worn first and never-worn last, by name.
func byLastWorn(a, b Item) int {
	switch {
	case a.LastWorn == b.LastWorn:
		return byName(a, b)
	case a.LastWorn == 0:
		return 1
	case b.LastWorn == 0:
		return -1
	}
	return cmp.Compare(b.LastWorn, a.LastWorn)
}

// ParseNames splits text typed into the Add window, one name per line or separated by commas, dropping blanks and
// repeats (ignoring case) and stopping at limit.
func ParseNames(text string, limit int) []string {
	var out []string
	seen := map[string]bool{}
	for _, field := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		name := strings.TrimSpace(field)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
		if len(out) == limit {
			break
		}
	}
	return out
}
