package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/veandco/go-sdl2/sdl"
)

// pairBoxState owns pairbox state.
type pairBoxState struct {
	// pair placement (session-scoped: each tab keeps its own, seeded from prefs in
	// resetSessionState, so it can't leak across tabs like the App-global version
	// did). pairOffXText/Y are the typed edit buffers (commit on valid parse).
	pairOffX, pairOffY         int
	pairFlip                   bool
	pairOffXText, pairOffYText string
	// Pair-preview background cache (drawPairGhost): the stage bg drawn behind the
	// ghost sprites via cachedPage; pairBgKey invalidates it when the bg/position changes.
	pairBgPages []*render.TexturePage
	pairBgGen   uint64
	pairBgKey   string
	// Pos-dropdown thumbnail cache (drawPosSelect): one bg thumbnail per position,
	// keyed by row index; posBgKey (the current bg name) invalidates the whole set
	// when the background changes so a stale stage can't show under the wrong bg.
	posBgPages []*render.TexturePage
	posBgGen   uint64
	posBgKey   string
	posBgAsk   []time.Time               // demandAsset pacing for the pos-dropdown thumbnails
	posThumbFn func(idx int, r sdl.Rect) // stable (one-time) pos-thumbnail draw fn — avoids a per-frame closure alloc on the hot Pos selector
	// Click-to-pair (/pair <uid> for servers that sync pairs via the OOC command).
	// areaUIDs/areaPlayers are parsed from /getarea in pushOOC; the popup pre-fills
	// the UID when it can confidently match the clicked char, else manual entry.
	pairPopupOpen bool
	pairPopupChar string
	pairPopupUID  string
	// pairing panel
	pairSearch string
	pairScroll int32
	pairWith   int
	pairOrder  int
	showPair   bool
}
