package ui

// Room CONSTRUCTION — the one place a *courtroom.Courtroom is built.
// Render thread only, like every other App method here.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// newRoom builds a courtroom AND wires it: the char.ini-driven callbacks
// (blips, chatbox skins, sprite scaling), the screen-effect overlay engine, and
// the pref-driven visual gates every mode shares.
//
// sess may be nil (a replay, a preview and an export all stage a recording
// rather than a live session) and audio may be nil — NewCourtroom substitutes
// its own no-op sink, and the wiring below is nil-safe throughout.
func (a *App) newRoom(urls courtroom.URLBuilder, sess *courtroom.Session, audio courtroom.AudioSink) *courtroom.Courtroom {
	room := courtroom.NewCourtroom(urls, a.d.Manager, sess, audio)
	// Per-character blips + chatbox skins + scaling from the speaker's char.ini,
	// and — through wireRoomOverlay — the screen-effect overlay resolver. One
	// call, because a speaker must blip, skin and flash identically in every mode.
	a.wireRoomCharMeta(room)
	a.applyVisualPrefsToRoom(room)
	return room
}

// applyVisualPrefsToRoom pushes the four PREF-DRIVEN VISUAL GATES onto a room.
// These are VIEWING prefs, and the three authoring surfaces take them back with
// applyAuthoringVisualsToRoom immediately below.
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

// applyAuthoringVisualsToRoom takes the three EFFECT gates back off the viewer
// and gives them to the author: this room renders what the recording says, not
// what the person at the keyboard prefers to look at.
// (courtroom.go:1668). The moment the viewer's ScreenEffects reached this room,
// Called AFTER newRoom, never inside it: an authoring room is a call-site
// decision, and TestRoomConstructionSitesDoNotHandRollTheVisualGates is what
// stops the next mode from spelling these three lines out again.
func (a *App) applyAuthoringVisualsToRoom(room *courtroom.Courtroom) {
	if room == nil {
		return
	}
	room.ReduceMotion = false     // accessibility is a VIEWING choice; the authored motion is content
	room.ScreenEffects = true     // …and without this, ReduceMotion = false un-gates nothing (effectsVisible)
	room.HideSpriteStyles = false // a recorded speaker's transmitted style is authored too
}
