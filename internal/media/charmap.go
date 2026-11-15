package media

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// Character mapping: a character's folders are listed once, in the background, so a file the listings leave out is
// answered absent with no request. Only folders that answered with a real listing are trusted; anything else keeps the
// per-file search.

const (
	// mapDepth bounds how far below a character's folder a walk lists (costumes, emotions, (a)/ and (b)/ nest).
	mapDepth = 5
	// mapListingsCap bounds one character's listings; the largest folder measured needed 98.
	mapListingsCap = 128
	// listingEntriesCap bounds the entries read from one listing.
	listingEntriesCap = 4096
	// listedCap and walksCap bound the session's tables; past them a table starts over, which only costs requests.
	listedCap = 1 << 15
	walksCap  = 1 << 10
	// MapMaxAge is how old a character's map may be before its next use walks it again in the background.
	MapMaxAge = 7 * 24 * time.Hour
	// charactersDir is the folder every character lives in.
	charactersDir = "/characters/"
)

// listedFolder is what one folder's listing showed, by lowercased name, as a case-insensitive host serves any case.
type listedFolder struct {
	files map[string]struct{}
	dirs  map[string]struct{}
}

// walk is one character's mapping: when it finished, and what is still out.
type walk struct {
	at       time.Time
	listings int
	open     int
}

// listingResult is one listing request's answer, carried from a Network worker to the Manager's goroutine.
type listingResult struct {
	data []byte
	err  error
}

// folderKey is a folder or file URL decoded, so a listing's names and a request's escapes compare alike.
func folderKey(s string) string {
	if dec, err := url.PathUnescape(s); err == nil {
		return dec
	}
	return s
}

// splitLast cuts a decoded path into its folder (with the slash) and the last name.
func splitLast(path string) (string, string) {
	i := strings.LastIndexByte(path, '/')
	return path[:i+1], path[i+1:]
}

// mapAbsent reports whether a listing proves f isn't there: its folder listed without it, or its folder missing from a
// listed parent.
func (s *RemoteSource) mapAbsent(f File) bool {
	dir, name := splitLast(folderKey(string(f)))
	if lf := s.listed[dir]; lf != nil {
		_, ok := lf.files[strings.ToLower(name)]
		return !ok
	}
	parent, sub := splitLast(strings.TrimSuffix(dir, "/"))
	if pf := s.listed[parent]; pf != nil && sub != "" {
		_, ok := pf.dirs[strings.ToLower(sub)]
		return !ok
	}
	return false
}

// CharacterRoot is the character folder a sprite URL lives in, or "" for a URL outside characters/.
func CharacterRoot(u string) File {
	i := strings.Index(u, charactersDir)
	if i < 0 {
		return ""
	}
	start := i + len(charactersDir)
	end := strings.IndexByte(u[start:], '/')
	if end <= 0 {
		return ""
	}
	return File(u[:start+end+1])
}

// MapCharacter maps the character folder root on its origin and each extra host, unless a fresh map or a walk in
// progress already covers it; call it on the Manager's goroutine.
func MapCharacter(m *Manager, pool *Pool, s *RemoteSource, b *CandidateBuilder, root File) {
	origin := b.originOf(Key{Path: string(root)})
	mapFolder(m, pool, s, root)
	rel, ok := strings.CutPrefix(string(root), origin)
	if !ok {
		return
	}
	for _, host := range b.extras[origin] {
		mapFolder(m, pool, s, File(host+rel))
	}
}

// mapFolder starts root's walk; a host that answered a folder with anything but a listing is not asked again.
func mapFolder(m *Manager, pool *Pool, s *RemoteSource, root File) {
	key := folderKey(string(root))
	if s.refused[hostOf(root)] {
		return
	}
	if w := s.walks[key]; w != nil && (w.open > 0 || s.clock().Sub(w.at) < MapMaxAge) {
		return
	}
	if len(s.walks) >= walksCap {
		clear(s.walks)
	}
	w := &walk{}
	s.walks[key] = w
	s.list(m, pool, w, root, key, 0)
}

// list asks for one folder's listing and, when it lands, lists the folders it shows.
func (s *RemoteSource) list(m *Manager, pool *Pool, w *walk, folder File, key string, depth int) {
	w.listings++
	w.open++
	m.Run(pool, Later, func() any {
		data, err := s.fetch.Fetch(s.ctx, string(folder))
		return listingResult{data, err}
	}, func(v any) {
		w.open--
		// Every path ends the walk once nothing is out, a failed listing too, or it would walk again on every use.
		defer func() {
			if w.open == 0 {
				w.at = s.clock()
			}
		}()
		res := v.(listingResult)
		host := hostOf(folder)
		switch {
		case res.err == nil:
			s.answered[host] = true
		case errors.Is(res.err, network.ErrAssetNotFound):
			s.answered[host] = true
			return // no such folder: nothing is proven, so the search stays as it was
		case errors.Is(res.err, network.ErrUnexpectedStatus):
			s.answered[host] = true
			s.refused[host] = true // 403 and the like: this host doesn't list folders
			return
		default:
			return
		}
		entries, ok := ParseListing(res.data, listingEntriesCap)
		if !ok {
			s.refused[host] = true // an index page or soft 404 where a listing should be
			return
		}
		lf := &listedFolder{files: map[string]struct{}{}, dirs: map[string]struct{}{}}
		for _, e := range entries {
			name := strings.ToLower(e.Name)
			if !e.Dir {
				lf.files[name] = struct{}{}
				continue
			}
			lf.dirs[name] = struct{}{}
			if depth+1 < mapDepth && w.listings < mapListingsCap {
				s.list(m, pool, w, folder+File(e.Href), key+e.Name+"/", depth+1)
			}
		}
		if len(s.listed) >= listedCap {
			clear(s.listed)
		}
		s.listed[key] = lf
	})
}

// Mapped reports whether root's walk has finished; call it on the Manager's goroutine.
func (s *RemoteSource) Mapped(root File) bool {
	w := s.walks[folderKey(string(root))]
	return w != nil && w.open == 0 && !w.at.IsZero()
}

// forgetMaps drops the maps and listings under a decoded prefix, so the next use walks them again.
func (s *RemoteSource) forgetMaps(prefix string) {
	for k := range s.listed {
		if strings.HasPrefix(k, prefix) {
			delete(s.listed, k)
		}
	}
	for k := range s.walks {
		if strings.HasPrefix(k, prefix) {
			delete(s.walks, k)
		}
	}
}

// learnedMapsCap bounds the character maps kept between sessions; the most recently walked stay.
const learnedMapsCap = 512

// savedMap is one character's finished map, as kept between sessions. Its folders are shared, not copied: a listing is
// never changed once recorded, only replaced.
type savedMap struct {
	at      time.Time
	folders map[string]*listedFolder
}

// LearnedMap is one character's map in learned.json: when it was walked, and each folder below its root (relative,
// "" for the root) with its names, a folder's ending in "/".
type LearnedMap struct {
	At      int64               `json:"t"`
	Folders map[string][]string `json:"f"`
}

// rememberMaps copies the finished maps; on the Manager's goroutine, and only pointers, so it is cheap.
func (s *RemoteSource) rememberMaps() map[string]savedMap {
	out := map[string]savedMap{}
	for key, lf := range s.listed {
		root := string(CharacterRoot(key))
		w := s.walks[root]
		if root == "" || w == nil || w.open > 0 || w.at.IsZero() {
			continue
		}
		sm, ok := out[root]
		if !ok {
			sm = savedMap{at: w.at, folders: map[string]*listedFolder{}}
			out[root] = sm
		}
		sm.folders[key] = lf
	}
	return out
}

// recallMaps adds saved maps the session hasn't walked itself; an old one still answers until its next use walks it.
func (s *RemoteSource) recallMaps(saved map[string]savedMap) {
	for root, sm := range saved {
		if _, walked := s.walks[root]; walked || len(s.walks) >= walksCap {
			continue
		}
		s.walks[root] = &walk{at: sm.at}
		for key, lf := range sm.folders {
			if _, ok := s.listed[key]; !ok && len(s.listed) < listedCap {
				s.listed[key] = lf
			}
		}
	}
}

// learnedMap writes one map with its folders relative to root.
func learnedMap(root string, sm savedMap) *LearnedMap {
	lm := &LearnedMap{At: sm.at.Unix(), Folders: make(map[string][]string, len(sm.folders))}
	for key, lf := range sm.folders {
		names := make([]string, 0, len(lf.files)+len(lf.dirs))
		for n := range lf.files {
			names = append(names, n)
		}
		for n := range lf.dirs {
			names = append(names, n+"/")
		}
		slices.Sort(names)
		lm.Folders[strings.TrimPrefix(key, root)] = names
	}
	return lm
}

// readMap rebuilds a saved map under root; false when root isn't a character folder.
func readMap(root string, lm *LearnedMap) (savedMap, bool) {
	if lm == nil || CharacterRoot(root) != File(root) {
		return savedMap{}, false
	}
	sm := savedMap{at: time.Unix(lm.At, 0), folders: map[string]*listedFolder{}}
	for rel, names := range lm.Folders {
		if rel != "" && !strings.HasSuffix(rel, "/") || strings.Contains(rel, "..") || len(names) > listingEntriesCap {
			continue
		}
		lf := &listedFolder{files: map[string]struct{}{}, dirs: map[string]struct{}{}}
		for _, n := range names {
			name, dir := strings.CutSuffix(n, "/")
			if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
				continue
			}
			if dir {
				lf.dirs[strings.ToLower(name)] = struct{}{}
			} else {
				lf.files[strings.ToLower(name)] = struct{}{}
			}
		}
		sm.folders[root+rel] = lf
	}
	return sm, true
}
