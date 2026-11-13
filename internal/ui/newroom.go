package ui

// newRoom is the only production call site of courtroom.NewCourtroom, so every mode is wired the same way.
// Render thread only.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// newRoom builds a courtroom and wires it: char.ini callbacks, the screen-effect overlay engine and the visual prefs.
// sess and audio may be nil; NewCourtroom substitutes a no-op sink.
func (a *App) newRoom(urls courtroom.URLBuilder, sess *courtroom.Session, audio courtroom.AudioSink) *courtroom.Courtroom {
	room := courtroom.NewCourtroom(urls, a.d.Manager, sess, audio)
	// One call so a speaker blips, skins and flashes identically in every mode.
	a.wireRoomCharMeta(room)
	a.applyVisualPrefsToRoom(room)
	return room
}

// applyVisualPrefsToRoom pushes the prefs that gate art resolution onto a room.
// The master off-switch wins and implies reduce-motion, no AO2 effect codes and no transmitted sprite style.
// Shared by newRoom and applyTimingToRoom so the two cannot answer differently.
func (a *App) applyVisualPrefsToRoom(room *courtroom.Courtroom) {
	if room == nil || a.d.Prefs == nil {
		return
	}
	noFX := a.d.Prefs.Load().DisableEffects
	room.ReduceMotion = a.d.Prefs.Load().ReduceMotionOn || noFX
	room.ScreenEffects = a.d.Prefs.Load().ScreenEffects && !noFX // AO2 \s/\f + field shake/flash (default ON)
	room.HideSpriteStyles = a.d.Prefs.Load().HideSpriteStyles || noFX
	room.ForceCharNames = a.d.Prefs.Load().ForceCharNames
}

// applyAuthoringVisualsToRoom gives the effect gates to the author: the room renders the recording, not viewer prefs.
// Used by the export, the maker preview and a replay while the maker is open.
// ReduceMotion = false alone restores nothing: effectsVisible() = ScreenEffects && !ReduceMotion.
// ForceCharNames is left alone: it only renames the speaker chip and deletes no authored content.
// Called after newRoom, never inside it.
func (a *App) applyAuthoringVisualsToRoom(room *courtroom.Courtroom) {
	if room == nil {
		return
	}
	room.ReduceMotion = false     // accessibility is a VIEWING choice; the authored motion is content
	room.ScreenEffects = true     // …and without this, ReduceMotion = false un-gates nothing (effectsVisible)
	room.HideSpriteStyles = false // a recorded speaker's transmitted style is authored too
}
