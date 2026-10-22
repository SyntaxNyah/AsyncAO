package ui

import "github.com/SyntaxNyah/AsyncAO/internal/config"

// jukeboxState owns jukebox state.
type jukeboxState struct {
	env *env
	// --- jukebox (global DJ /play music-link library) ---
	// App-global like dlPaused and OUTSIDE sessionState: the library is shared
	// across every server and must survive disconnects and tab switches.
	juke            *config.Jukebox      // global store, loaded off-thread
	jukeRes         chan *config.Jukebox // off-thread load landing
	jukeIORes       chan string          // off-thread export/import result → in-app toast
	jukeCache       []config.Playlist    // rev-keyed Playlists() snapshot for drawing
	jukeCacheRev    int64
	jukeOpen        int    // -1 = playlist list; else the open playlist index
	jukeSearch      string // filters playlists (top) or songs (inside one)
	jukeScroll      int32
	jukeNewName     string // "new playlist" input
	jukeAddURL      string // "add song" URL input
	jukeAddTitle    string // "add song" optional title input
	jukeDelPlaylist int    // playlist index awaiting delete confirmation (-1 = none)
	jukeBindFor     string // key-capture target: "" / "p:<i>" / "e:<pl>:<i>"
	jukeFiltered    []int  // memoized matching entry indices in the open playlist
	jukeFilteredKey jukeFilterKey
	musicHist       []musicHistEntry // session-only "recently played" ring (MRU, capped)
	jukeShowRecent  bool             // top-level jukebox view: recently-played vs playlists
	jukeHistScroll  int32
	jukeRecentLbl   string            // cached "Recently played (N)" toggle label (rebuilt only on change, so the per-frame draw never Sprintfs)
	musicHostInput  string            // Settings: "add a domain" field for the music-history allowlist
	jukeGroupRows   []jukeGroupRow    // memoized domain-grouped row layout for the "Music history" playlist
	jukeGroupKey    jukeGroupCacheKey // (playlist, revision) the grouped layout was built for
	jukeShowFav     bool              // top-level jukebox view: ★ favorites vs playlists/recent
	jukeFavScroll   int32
	jukeFavs        []favRef // memoized list of starred songs across all playlists
	jukeFavRev      int64    // library revision the favorites list was built for
	jukeFavLbl      string   // cached "★ Favorites (N)" toggle label (rebuilt only on change)
}
