package ui

// Background slideshow: substitutes the background at render time only, so courtroom state is never touched and
// any non-idle phase brings the real scene back at once.

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// updateSlideshow advances the rotation, clearing the override when the slideshow is off or the room isn't idle.
func (a *App) updateSlideshow(phase courtroom.MessagePhase) {
	if !a.d.Prefs.Load().BgSlideshow || phase != courtroom.PhaseIdle {
		a.slideBG = "" // off, or a message is on stage: show the real scene
		return
	}
	names := a.bgPick.server // the server's discovered background list
	if len(names) == 0 {
		a.ensureBgList() // kick discovery once; Frame's pollBgList drains it
		a.slideBG = ""
		return
	}
	interval := time.Duration(a.d.Prefs.Load().BgSlideshowSeconds()) * time.Second
	if a.slideBG != "" && a.now().Sub(a.slideAt) < interval {
		return // current background still has its time on screen
	}
	if a.slideIdx >= len(names) {
		a.slideIdx = 0
	}
	a.slideBG = a.urls.Background(names[a.slideIdx], bgThumbPart)
	a.slideAt = a.now()
	a.slideIdx = (a.slideIdx + 1) % len(names)
	// Low priority so it never competes with live assets.
	a.d.Manager.Prefetch(a.slideBG, assets.AssetTypeBackground, network.PriorityLow) // AssetType: Background (slideshow)
}

// renderScene returns the scene to draw: the replay, the live scene, or a copy with the rotating background and no
// desk.
// The copy lives in slideScene so taking its address adds no per-frame allocation.
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
