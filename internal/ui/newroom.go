package ui

// Room CONSTRUCTION — the one place a *courtroom.Courtroom is built.
//
// WHY THIS FILE EXISTS. The App stages messages in five places: the live
// courtroom, the pinned split pane, a replay, the scene maker's preview and the
// comic/video export. Wiring used to be a CONVENTION — call NewCourtroom, then
// remember to call wireRoomCharMeta, then remember to push the prefs — and the
// convention was not kept. Two of the five never got the overlay engine (the
// split pane and the export room), and FOUR of the five never got the user's
// ScreenEffects preference, so they silently ran on NewCourtroom's hardcoded
// `true`. Both failures were invisible: screen effects simply did not appear in
// the pinned pane or in an exported comic, and with the preference OFF a replay
// or a preview still resolved effect rosters, fetched misc manifests, decoded
// art and uploaded pages — the exact cost TestVisualGateOffCostsNoResolve
// exists to forbid, defeated by rooms that test never saw.
//
// The cure is not a better comment on wireRoomOverlay. It is that CONSTRUCTION
// AND WIRING ARE THE SAME STEP: newRoom below is the only production call site
// of courtroom.NewCourtroom in this package, and
// TestRoomConstructionHasExactlyOneCallSite fails the build gate on a sixth
// room built any other way. A future mode cannot be born unwired, because there
// is nothing to forget.
//
// WHAT A MODE STILL OWNS. Everything that is genuinely per-mode stays at the
// call site as a POST-CONSTRUCTION override: every timing knob, and — for the
// three AUTHORING surfaces — applyAuthoringVisualsToRoom below, which is the
// export's "export the authored effects, not the viewer's accessibility pref",
// the maker preview's WYSIWYG version of the same, and the replay's maker-open
// exemption, all in one place. newRoom seeds the defaults every mode agrees on;
// it does not normalise a deliberate deviation away.
//
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
//
// These four and no others: they are the ones a room resolves ART for, so a
// room that misses them does not merely look wrong, it does WORK the user
// switched off. ScreenEffects is the one that motivated the split — with it
// off, effectsVisible() is false and the whole overlay ladder (roster resolve,
// misc manifest fetch, art decode, page upload) never starts.
//
// The MASTER off-switch wins over both effect knobs, exactly as
// applyTimingToRoom has always had it: it implies reduce-motion, no AO2 \s/\f
// and none of another player's transmitted sprite style. It never writes the
// individual prefs, so turning it back off restores what the player chose.
//
// Shared by newRoom (every mode, at construction) and applyTimingToRoom (the
// live room, whenever a setting changes) so the two cannot answer differently —
// the second-spelling hazard that let four rooms keep a hardcoded `true`.
//
// These are VIEWING prefs, and the three authoring surfaces take them back with
// applyAuthoringVisualsToRoom immediately below.
func (a *App) applyVisualPrefsToRoom(room *courtroom.Courtroom) {
	if room == nil || a.d.Prefs == nil {
		return
	}
	noFX := a.d.Prefs.EffectsDisabled()
	room.ReduceMotion = a.d.Prefs.ReduceMotion() || noFX
	room.ScreenEffects = a.d.Prefs.ScreenEffectsOn() && !noFX // AO2 \s/\f + field shake/flash (default ON)
	room.HideSpriteStyles = a.d.Prefs.HideSpriteStylesOn() || noFX
	room.ForceCharNames = a.d.Prefs.ForceCharNamesOn()
}

// applyAuthoringVisualsToRoom takes the three EFFECT gates back off the viewer
// and gives them to the author: this room renders what the recording says, not
// what the person at the keyboard prefers to look at.
//
// Three surfaces are authoring surfaces — the comic/video export, the scene
// maker's preview pane, and a replay driven while the maker is open — and all
// three want the same three answers. They are HERE, next to the pref push,
// because separating them is what broke: the export has always carried its
// deliberate `ReduceMotion = false` ("export the authored effects"), and that
// line alone cannot restore an effect, because the room gates every shake,
// flash and overlay on effectsVisible() = ScreenEffects && !ReduceMotion
// (courtroom.go:1668). The moment the viewer's ScreenEffects reached this room,
// an exporter who keeps screen effects off — or who has the master off-switch
// on — got a comic with no shake, no flash and no overlays, while the comment
// on the next line promised the opposite. Two facts, one place, so they cannot
// disagree again. Ruled: docs/wip/OOP-AUDIT.md DO-NOT-FIX 14.
//
// ForceCharNames is deliberately NOT here. It gates no work and suppresses no
// authored content — it renames the speaker chip from data the recording still
// carries — and a viewer who runs with it on (true-roleplay / anti-impersonation
// casing) wants it on in their own export too. Only the effect gates, which
// silently DELETE what the author wrote, are exempt.
//
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
