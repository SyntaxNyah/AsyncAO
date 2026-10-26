package ui

// Bundled zoom speedline fallback art (issue #126). AO2 reads <side>_speedlines
// from the speaker's own folder first and falls back to themes/default; most
// packs ship none, and AsyncAO's stock theme is procedural (no art files), so a
// generated radial burst per side is embedded here and pinned under
// render.Speedline*Key. The renderer draws it when a zoom emote's speaker ships
// no speedline of their own. Generated offline as a radial burst per side (a
// defence-blue tint and a prosecution-red tint on transparent).

import (
	_ "embed"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

//go:embed assets/defense_speedlines.png
var defenseSpeedlinesPNG []byte

//go:embed assets/prosecution_speedlines.png
var prosecutionSpeedlinesPNG []byte

// uploadEmbeddedSpeedlines decodes and pins the two bundled speedline fallbacks.
// Called once at startup (render thread), best-effort like uploadEmbeddedMissingno:
// a decode/upload failure just means a zoom emote on a pack without speedlines
// draws nothing behind the speaker — the effect degrades, it does not crash.
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
