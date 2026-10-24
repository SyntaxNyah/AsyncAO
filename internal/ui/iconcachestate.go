package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// iconCacheState owns rcache state.
type iconCacheState struct {
	// iconAsk[i] is when char i's icon was last demanded by the visible
	// grid (bounded by the server's char list length).
	iconAsk []time.Time
	// Generation-keyed texture page caches (the viewport's animState
	// trick applied to grids): while the store generation is unchanged a
	// grid redraw costs ZERO LRU lookups/locks for resident icons.
	iconPages    []*render.TexturePage
	iconPagesGen uint64
	iniPages     []*render.TexturePage
	iniPagesGen  uint64
	// Emote button art (off/on variants) rides the same gen-keyed trick:
	// the grid was the last draw path paying an LRU lock per cell/frame.
	emoteBtnOff    []*render.TexturePage
	emoteBtnOffGen uint64
	emoteBtnOn     []*render.TexturePage
	emoteBtnOnGen  uint64
	// emoteIconPages: the active character's icon (one slot), drawn behind the emote
	// label when an emotions/button<N> image is absent/streaming — a face beats a bare
	// grey box. Same gen-/index-keyed page cache, so it's nulled on a character change.
	emoteIconPages []*render.TexturePage
	emoteIconGen   uint64
	emoteIconAsk   []time.Time
}
