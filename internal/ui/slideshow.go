package ui

// M5 background slideshow: while enabled (Settings, OFF by default) AND the
// courtroom is idle, cycle the stage through the server's backgrounds as
// ambiance. It never touches the courtroom state or a live scene — it only
// substitutes the background at RENDER time, so the instant a message arrives
// (any non-idle phase) the real area scene is back.

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// updateSlideshow advances the rotation. Called once per frame with the room
// phase; a no-op (and clears the override) whenever the slideshow is off or the
// courtroom isn't idle.
func (a *App) updateSlideshow(phase courtroom.MessagePhase) {
	if !a.d.Prefs.BgSlideshowEnabled() || phase != courtroom.PhaseIdle {
		a.slideBG = "" // off, or a message is on stage: show the real scene
		return
	}
	names := a.bgPick.server // the server's discovered background list
	if len(names) == 0 {
		a.ensureBgList() // kick discovery once; Frame's pollBgList drains it
		a.slideBG = ""
		return
	}
	interval := time.Duration(a.d.Prefs.BgSlideshowSeconds()) * time.Second
	if a.slideBG != "" && a.now().Sub(a.slideAt) < interval {
		return // current background still has its time on screen
	}
	if a.slideIdx >= len(names) {
		a.slideIdx = 0
	}
	a.slideBG = a.urls.Background(names[a.slideIdx], bgThumbPart)
	a.slideAt = a.now()
	a.slideIdx = (a.slideIdx + 1) % len(names)
	// Warm the next background at low priority (never fights live assets).
	a.d.Manager.Prefetch(a.slideBG, assets.AssetTypeBackground, network.PriorityLow) // AssetType: Background (slideshow)
}

// renderScene returns the scene the viewport should draw: the live room scene
// normally, or — while the slideshow override is active — a shallow copy that
// swaps in the rotating background (desk hidden so it reads as a clean stage).
// The real scene is never mutated; the copy is stored in slideScene so taking
// its address adds no per-frame allocation.
func (a *App) renderScene() *courtroom.Scene {
	if a.replaying && a.replayRoom != nil { // M16: the stage shows the replay, not the live room
		return &a.replayRoom.Scene
	}
	if a.slideBG == "" {
		return &a.room.Scene
	}
	a.slideScene = a.room.Scene // shallow copy: slices shared, render is read-only
	a.slideScene.BackgroundBase = a.slideBG
	a.slideScene.ShowDesk = false
	return &a.slideScene
}
