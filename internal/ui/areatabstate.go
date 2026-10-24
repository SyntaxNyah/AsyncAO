package ui

// areaTabState owns areaTab state.
type areaTabState struct {
	areaCount shownTotalMemo
	// Areas-list search (#20 — parity with the Music tab beside it). Same
	// memoized-filter shape: the query + a cheap Areas-list identity so a
	// hub server's hundreds of areas aren't re-scanned (and re-lowercased —
	// that allocates) every frame. areaFiltered holds matching indices into
	// a.sess.Areas (which parallels a.sess.AreaInfo, so the ORIGINAL index
	// is what indexes both).
	areaSearch     string
	areaFiltered   [areaQuerySlots][]int
	areaFilterMemo [areaQuerySlots]areaFilterKey
	areaScroll     int32
	// areaInfoSeq bumps on every EventAreasUpdated (ARUP) so areaWrapped
	// (screens.go) knows when to re-wrap; it deliberately excludes cosmetic
	// per-frame state (a.curArea) which the draw loop re-reads live instead.
	areaInfoSeq uint64
}
