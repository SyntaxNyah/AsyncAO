package ui

import "github.com/veandco/go-sdl2/sdl"

// clientState owns client state.
type clientState struct {
	env *env
	// The pinned server draws in a free-floating, movable/resizable "second client"
	// window (geometry in env.wins) overlaying the primary — drag it anywhere, not a
	// fixed split. clientHdr caches the title by server name so the per-frame draw
	// never re-concats (0-alloc, like the single-pane path).
	clientHdr     string
	clientHdrName string
	// clientFull toggles the full-theme view (the WHOLE pinned client UI, view-only)
	// vs compact (stage + log + input). Full-theme renders the pinned drawCourtroom
	// into clientTex at the logical screen size (so the theme lays out exactly as the
	// primary), then shrink-blits it into the window — recreated only on a size change.
	// VIEW-ONLY by necessity: the kit is single-instance (fixed widget ids would
	// collide with the primary's, and a.screen / modal writes would leak), so input is
	// neutralized during that pass; you watch the whole theme and chat via the input
	// strip below it. Opt-in (default compact) — the primary path is untouched.
	clientFull bool
	clientTex  *sdl.Texture
	clientTexW int32
	clientTexH int32
	// pinnedPass is true during a VIEW-ONLY drawCourtroom pass over a session that is
	// not the live one: the full-theme render of the pinned client, and the theme
	// editor's canned preview (themeeditorpreview.go). Single-consumer App channel
	// drains (pollCharINI) skip while it's set, so such a pass can't eat an async
	// result meant for the primary — the pinned client is already fully loaded in its
	// parked sessionState, and the preview's demo session never asks for one.
	pinnedPass bool
	// pendingControlSwap: a click on the floating client's view requests "control
	// this server" (make it the live, fully-interactive courtroom). Applied at the
	// NEXT frame's top — never mid-render — since it swaps the active session/room.
	pendingControlSwap bool
	// Full-theme view zoom/pan: clientZoom magnifies (1 = fit), clientPan{X,Y} is the
	// view centre in texture space (0..1) when zoomed. clientPanning tracks a drag.
	// clientZoomLbl memoizes the readout so the per-frame draw doesn't Sprintf.
	clientZoom       float64
	clientPanning    bool
	clientZoomLbl    string
	clientZoomLblPct int
	// theme-fit Custom preview (Settings → Theme): drag to pan the big preview.
	themeFitDrag      bool
	themeFitDragStart [2]int32
	themeFitDragBase  [2]int
	vpExactBuf        string // edit buffer for the Settings exact-stage-width px field (commit on Enter)
}
