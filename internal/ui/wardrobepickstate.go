package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// wardrobePickState owns wardrobe state.
type wardrobePickState struct {
	// wardSection selects the open wardrobe tab (wardSectionCharacters /
	// wardSectionBackgrounds). The Backgrounds section organizes favourite
	// backgrounds into the same navigable folders as characters.
	wardSection int
	// wardDelFolder is the folder a delete confirmation is open for ("" = none);
	// the active section decides whether it deletes characters or backgrounds.
	wardDelFolder string
	// Backgrounds-section state. bgFavList is the favourites in ONE stable
	// order (FavBackgroundList); navigation filters via a predicate, never by
	// rebuilding the list, so the index-keyed bgFavPages cache stays valid
	// (see the cachedPage reorder invariant). bgFavPages is its OWN cache —
	// never share bgPick.pages (different index space). Nulled only when the
	// favourites set/order changes (rebuildBgFav), not on folder navigation.
	bgFavList     []string
	bgFavLower    []string // lowercased, parallel — search
	bgFavFolders  []string // folder per favourite, parallel — nav filter
	bgFavFolder   string   // open background folder ("" = top level)
	bgFavNewFold  string   // "new folder" text input (Backgrounds section)
	bgFavSearch   string
	bgFavScroll   int32
	bgFavAsk      []time.Time // demand pacing stamps, parallel to bgFavList
	bgFavPages    []*render.TexturePage
	bgFavPagesGen uint64
}
