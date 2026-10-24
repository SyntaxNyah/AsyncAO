package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/veandco/go-sdl2/ttf"
)

// rasterCacheState owns raster state.
type rasterCacheState struct {
	// chat raster invalidation extras (text/color tracked separately)
	rasterScale      int
	rasterW          int32
	rasterSkinned    bool      // a drawn chatbox skin (theme OR per-character) gates theme text colors (readability)
	rasterChatMsg    theme.RGB // per-character chatbox ink the message raster was built with (cache key)
	rasterChatMsgHas bool
	rasterDevPct     int32 // #77: the device font scale the raster was built at — a UI-scale change must rebuild it
	msRaster         *render.MessageRaster
	rasterText       string
	// rasterFailedText is the message whose raster could NOT be built, arming the
	// chatbox's plain-label fallback. Empty in the normal case. It exists because
	// the failure is otherwise invisible AND permanent: msRaster stays nil, the
	// cache key is never recorded, so every frame retries, fails the same way, and
	// draws no text at all under a showname that still paints — which reads as
	// "the client stopped receiving IC" rather than as a font/texture fault.
	rasterFailedText string
	// chatDiag is the last frame's chatbox geometry, shown in Debug → Session.
	// It exists because the "message text is cut off at a scaled UI" report could
	// not be reproduced on the machines the developers have: every candidate was
	// eliminated by measurement EXCEPT the numbers on the reporter's own screen,
	// which nobody could see. Printing them turns the affected machine into the
	// measuring instrument instead of asking it for another screenshot.
	chatDiag struct {
		vpH, boxY, boxH     int32
		textY, wrapW        int32
		lines, lineH        int32
		devScale, renderPct int32
		visible, total      int32
		grew                bool
	}
	// rasterRaw is the pre-strip message the cached raster was built from — the
	// cache key, since two differently-colored messages can share the same
	// stripped MessageText (the cachedPage index-key class of bug).
	rasterRaw string
	// last-applied scene text color (raster invalidation)
	rasterColor int
	// Animated chat text (#M5): when the message carries effect spans, msAnim
	// replaces msRaster for this message (the per-glyph path that can displace +
	// recolour). msAnimFont is the face it was laid out with (the glyph cache keys
	// on it); glyphCache holds the white glyph textures, shared + bounded. A plain
	// message never builds msAnim, so its draw path is byte-identical to before.
	// rasterEffSig folds the effect spans into the raster cache key.
	msAnim       *render.AnimatedText
	msAnimFont   *ttf.Font
	glyphCache   *render.GlyphCache
	rasterEffSig uint64
	// rasterCentered folds the webAO "~~" centre flag into the raster cache key, so a
	// centred "hi" and a plain "hi" (same stripped text) don't share one raster.
	rasterCentered bool
}
