package ui

import "time"

// framePaceState owns frame state.
type framePaceState struct {
	// spriteCapBase is the display-derived decode-downscale base height (set once
	// from cmd/asyncao); the downscale knobs scale it (config.EffectiveSpriteCap).
	spriteCapBase int
	// lastInputAt is the most recent SDL input event (frame pacing: full rate
	// through the InputGraceFrames hold after any interaction).
	lastInputAt time.Time
	// lastMotionAt is the most recent BARE pointer motion (experimental loop):
	lastMotionAt time.Time
	// lastFrameDrawn is when Frame last actually rendered — the idle-cadence
	// anchor both loops measure from: the classic loop re-renders a static
	// courtroom once per idle-budget, and the event-driven loop schedules its
	// idle-rate wake from it (NextWakeDelay). idle=off → neither fires.
	lastFrameDrawn time.Time
	// drawnGen is the texture-store generation as of the last drawn frame: a
	// mismatch means textures uploaded/evicted since the screen was painted
	// (streamed-in icons, healed pages), which is redraw-worthy damage under
	// the experimental loop.
	drawnGen uint64
	// drawnCaretOn is the caret blink state as of the last drawn frame; a flip
	// since then is the scheduled damage that redraws a focused text field at
	// 2 Hz instead of holding the whole idle frame rate (experimental loop).
	drawnCaretOn bool
	// drawnOverlayGen is Scene.Overlay.Gen as of the last frame that either DREW
	// the stage or observed the trigger from Background — the latch behind
	// noteOverlayStart. A screen effect armed while the loop is parked has no draw
	// site to census it, so its Gen bump is the doorbell that gets the first frame
	// out on time instead of an idle period late.
	drawnOverlayGen uint32
	// drawnScreen is the screen as of the last Frame — noteScreenTransition
	// compares against it to tear down hover-preview state that would otherwise
	// ride across a screen switch with no draw site left to close it (the
	// orphaned-preview cap latch; distinct from prevScreen, the back-nav memory).
	drawnScreen Screen
	// frameAnimChrome is set (via NoteAnimating) during a draw pass whenever it
	// renders a self-driven, TIME-STEPPED UI animation outside the viewport's
	// anim scheduler — an animated theme page (themeFrame), the looping testimony
	// badge, a WT/CE splash, the layout editor's ghost, or any future widget that
	// tweens on its own clock. drawnAnimChrome is the last completed frame's
	// value: while true, SkipFrame keeps frames coming AND FramePace paces at the
	// active cap, so the motion stays smooth (the general "self-driven animation →
	// uncap" mechanism); it falls back to idle the frame the draw stops marking it.
	frameAnimChrome bool
	drawnAnimChrome bool
	// lastPacerTier records which FramePace branch set the last budget — a
	// write-only DIAGNOSTIC for the F8 panel (#50), never read by any pacing
	// decision (see pacerTier's own doc).
	lastPacerTier pacerTier
	// roomPreAdvanced is set by the main loop's audio-paced sub-stepping: while a
	// message types at a low PRESENT rate, the loop advances the courtroom (and
	// plays its blips) at the fine audio cadence via Background between presents, so
	// audio never batches to the frame rate. When set, the next Frame skips its own
	// room.Update (the room is already current) but still advances the UI + draws.
	roomPreAdvanced bool
	// lastSpeakerDiagBase is the speaker sprite base last logged to the debug
	// log (pushDebug) — the #110 animation diagnostic logs once per base change,
	// never per frame, so the ring isn't flooded with identical rows.
	lastSpeakerDiagBase string
	// frameDemandPending is set during a draw pass whenever it leaves a
	// DEMAND-STREAMED cell (an emote-grid button with neither its art nor its
	// fallback icon resident yet) truly blank. drawnDemandPending is the last
	// completed frame's value: while true, NextWakeDelay schedules a re-render at
	// the demand cadence so demandAsset keeps issuing asks. Without it the pump
	// stalls at idle=0 — a batch of asks that all 404 uploads nothing, so nothing
	// bumps the store generation and no self-wake fires, and the grid can sit
	// half-filled until unrelated damage or input. Clears the moment every cell
	// shows something (art or the shared icon), so it costs nothing at rest.
	frameDemandPending bool
	drawnDemandPending bool
	// winW/winH cache the logical window size Frame was last given, for draw
	// helpers deep in a call chain that need window bounds (e.g. the sprite
	// preview's on-screen clamp) without threading params through every layer.
	winW, winH int32
}
