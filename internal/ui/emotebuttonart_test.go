package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// A character with only _off button art shows it on the selected button too, from the first frame it is selected.
func TestSelectedEmoteButtonWithoutOnArtLoadsItsOffArt(t *testing.T) {
	const (
		me       = "witch"
		buttonPx = 48
		artPx    = 8
	)
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()
	local := wireEmptyPackManager(t, a)
	writeThemePNG(t, local.Mounts()[0], "characters/"+me+"/emotions/button1_off.png", artPx)
	a.installAssetOrigin(local.BaseURL())
	a.emotes = make([]courtroom.Emote, 1)
	btn := sdl.Rect{W: buttonPx, H: buttonPx}
	off := a.urls.EmoteButton(me, 1, false)

	deadline := time.Now().Add(missGateSettle)
	for time.Now().Before(deadline) {
		a.iconAskBudget = charIconAskPerFrame
		a.drawEmoteImageButton(btn, me, 0, true, "normal")
		for {
			select {
			case d := <-a.d.Manager.Decoded():
				if d.Asset != nil {
					d.Asset.Release()
				}
				if d.Base == off && d.Err == nil {
					return
				}
				continue
			case <-a.d.Manager.Warnings():
				continue
			default:
			}
			break
		}
		time.Sleep(missGatePollStep)
	}
	t.Fatal("the selected button never asked for its _off art, so it stays empty until another emote is picked")
}
