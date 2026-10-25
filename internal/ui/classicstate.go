package ui

import "github.com/veandco/go-sdl2/sdl"

// classicState owns classic state.
type classicState struct {
	env *env
	// --- classic-layout slot editor (the default/non-themed courtroom) ---
	// Mirrors the themed editor's feel, but edits SCREEN-space "slots" persisted
	// as window FRACTIONS (config.ClassicLayout) so they survive window resizes.
	// classicOv is an App-local lock-free snapshot read by slotRect on the render
	// path (the editor is the sole writer); slotReg is this frame's registered
	// slots, populated ONLY while editing so the common frame stays alloc-free.
	classicEdit     bool
	classicOv       map[string][4]float64
	classicOvLoaded bool
	// classicAnchor holds per-slot window pins (parsed from
	// config.ClassicAnchors alongside classicOv): a pinned slot's override
	// re-bases to pixel offsets from its window corner/centre on resize
	// instead of scaling with the fractions (classicanchor.go).
	classicAnchor map[string]anchorRef
	// classicRot holds per-slot ROTATION angle bytes (A4), snapshotted from
	// config.ClassicRotations alongside classicOv/classicAnchor. Read by the draw
	// path when resolving a texture-backed classic element's angle (the HP bar);
	// an absent entry means angle 0 → the plain Copy path (byte-identical to the
	// unrotated draw). The editor is the sole writer thereafter.
	classicRot map[string]uint8
	slotReg    map[string]slotInfo
	// classicTgt is the classic editor's selection, the spaceClassic half of the
	// shared edit-target model (edittarget.go). It was a bare slot-name string;
	// the space it lives in — SCREEN px, persisted as a window fraction — is now part
	// of the value rather than something the reader had to know.
	classicTgt       editTarget
	classicEditDrag  int // 0 none, 1 move, 2 resize
	classicEditStart [2]int32
	classicEditBase  sdl.Rect
	classicEditEdges uint8 // which edges a resize drags (edgeL/R/T/B bitmask); 0 mid-move
	classicEditPrev  bool  // press-edge latch (mouse-down rising edge)
	classicEditMoved bool  // a drag actually moved/resized (else a click persists nothing)
	// Undo/redo history of the whole override map (Ctrl+Z / Ctrl+Y), edit-mode only and
	// bounded by layoutUndoCap. classicPickIdx/Sig drive Tab-cycling the stack of slots
	// under the cursor so a big box hidden under a small one is still reachable.
	classicUndo    []map[string][4]float64
	classicRedo    []map[string][4]float64
	classicPickIdx int
	classicPickSig string
	// classicChromeBot is the bottom Y of the editor's stacked top chrome
	// (banner + tab tray), set to classicTabTrayBot each edit frame — slot name
	// tags clamp below it so a box parked in the top strip can't plaster its tag
	// over the editor's own controls.
	classicChromeBot int32
	// tornZ is the session-scoped tear-off panel z-order (torntabs.go), a
	// permutation of tornTabTable indices back-to-front. Empty = table order.
	// It exists so clicking a floating panel brings it to the front instead of
	// the fixed order always drawing Players over Music/Areas.
	tornZ  []int
	hidden map[string]bool
}
