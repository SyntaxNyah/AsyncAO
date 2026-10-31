package ui

// Bundled zoom speedline fallback art. AO2 reads <side>_speedlines from the speaker's folder, then themes/default; the
// stock theme is procedural, so a generated burst per side is embedded and pinned under render.Speedline*Key.

import (
	_ "embed"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

//go:embed assets/defense_speedlines.png
var defenseSpeedlinesPNG []byte

//go:embed assets/prosecution_speedlines.png
var prosecutionSpeedlinesPNG []byte

// uploadEmbeddedSpeedlines decodes and pins the two bundled fallbacks; called once at startup (render thread).
// A decode or upload failure leaves a zoom emote without speedlines drawing nothing behind the speaker.
func (a *App) uploadEmbeddedSpeedlines() {
	if a.d.Store == nil {
		return
	}
	a.uploadOneSpeedline(render.SpeedlineDefenseKey, defenseSpeedlinesPNG)
	a.uploadOneSpeedline(render.SpeedlineProsecutionKey, prosecutionSpeedlinesPNG)
}

func (a *App) uploadOneSpeedline(key string, b []byte) {
	dec, err := assets.DecodeImage(b, false) // static: the fallback is a single frame
	if err != nil {
		a.pushDebug("speedlines: embedded fallback failed to decode: " + err.Error())
		return
	}
	if err := a.d.Store.UploadPinned(key, dec); err != nil { // UploadPinned releases dec
		a.pushDebug("speedlines: embedded fallback failed to upload: " + err.Error())
	}
}
