package ui

// cameraState owns cam state.
type cameraState struct {
	// --- viewport camera (hyperfocus zoom; 0 or 1 = off) ---
	vpZoom   float64
	vpPanX   float64 // pan fractions of the zoom overflow (0..1)
	vpPanY   float64
	zoomDrag bool
	zoomPrev bool // last frame's mouseDown (edge detect)
	// M16: drag the viewport's right edge to resize it (vpPct), the mouse
	// alternative to the View knob. dragVpDivider is the active grab.
	dragVpDivider  bool
	dividerPrevDwn bool // mouseDown edge detect for the divider grab
	zoomStart      [2]int32
	zoomBase       [2]float64
}
