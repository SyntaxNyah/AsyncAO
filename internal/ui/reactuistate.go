package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/veandco/go-sdl2/sdl"
)

// reactUIState owns reactUI state.
type reactUIState struct {
	// #2 real reactions. reactionFloats is the bounded ring of emoji rising over the stage
	// (drawReactionFloats); reactBadges caches one alpha-fadeable texture per palette index
	// (built once the colour-emoji face lands); reactRect is the reused draw scratch (a fresh
	// local would heap-escape through cgo). reactSpawnSeq scatters floats horizontally.
	// invalidates everything" idiom SetTextDevScale uses for the label/emoji caches (#77);
	reactionFloats     []reactionFloat
	reactBadges        map[uint8]*render.Badge
	reactBadgeDevScale int32
	reactRect          sdl.Rect
	reactSpawnSeq      int32
	// React trigger: showReactPicker toggles the palette (reactFenceOn tracks our modal
	// fence so it's released on close, like the emoji picker); reactBtnRect anchors it.
	// reactTarget{Ref,Name} is the message snapshotted when the palette opened. pendingReact
	// (+set) is the queued reaction that rides the next IC send; lastReact{Ref,Name} tracks
	// the most recent IC message, the snapshot source.
	showReactPicker bool
	reactFenceOn    bool
	reactBtnRect    sdl.Rect
	reactTargetRef  uint32
	reactTargetName string
	pendingReact    courtroom.WireReaction
	pendingReactSet bool
	lastReactRef    uint32
	lastReactName   string
}
