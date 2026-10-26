package ui

// missingno — the placeholder drawn for a CONCLUSIVELY-MISSING character sprite
// (its whole prefix→bare / every-format fallback chain 404'd). This is AsyncAO's
// nickname for AO2's "placeholder" asset: AO2-Client appends the default theme's
// placeholder to the Idle/Talk emote fallback chain (animationlayer.cpp), shown
// whenever a character emote sprite can't be found. A streaming client has no
// local theme dir to read, so we embed the art and upload it once under the
// reserved render.MissingKey; render/viewport.go probes store.IsMissing(base) in
// the miss branch and blits this page.
//
// Art origin: internal/ui/assets/missingno.webp is AO2-Client's canonical default
// theme placeholder (bin/base/themes/default/placeholder.webp) copied verbatim —
// the scrambled purple/white glitch sprite AO players recognise. Kept as its
// native .webp and decoded ONCE at startup through the SDL-free assets decoder
// (magic-byte sniffing), so no PNG re-encode ceremony is needed. Glitch content
// is not authored expression; this comment records provenance for maintainers.

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

//go:embed assets/missingno.webp
var missingnoWebP []byte

// errorSpriteMaxBytes caps a user-supplied custom placeholder file read (rule
// §17.4 — every read is bounded). A placeholder is a small portrait sprite; 8 MiB
// is a generous ceiling that still rejects an accidental huge file.
const errorSpriteMaxBytes = 8 << 20

// errorSpriteLoad carries an off-thread custom-placeholder load result back to the
// render thread. On success dec is a decoded page ready to UploadPinned over
// render.MissingKey; on failure dec is nil and err describes why (surfaced inline
// in Settings + the debug overlay), leaving the embedded default in place.
type errorSpriteLoad struct {
	dec *assets.Decoded
	err error
}

// uploadEmbeddedMissingno decodes the embedded default placeholder and pins it
// under render.MissingKey. Called ONCE at startup (render thread) BEFORE any
// custom load, so a later custom-load failure simply leaves this default in place
// (nothing to undo). Best-effort: a decode/upload failure just means the miss
// branch finds no MissingKey page and falls through to hold-previous/blank — the
// feature degrades to today's behaviour rather than crashing.
func (a *App) uploadEmbeddedMissingno() {
	if a.d.Store == nil {
		return
	}
	dec, err := assets.DecodeImage(missingnoWebP, false) // static: first frame only (the canonical placeholder is single-frame)
	if err != nil {
		a.pushDebug("missingno: embedded placeholder failed to decode: " + err.Error())
		return
	}
	if err := a.d.Store.UploadPinned(render.MissingKey, dec); err != nil { // UploadPinned releases dec
		a.pushDebug("missingno: embedded placeholder failed to upload: " + err.Error())
	}
}

// applyErrorSprite (re)loads the custom placeholder from path, OFF-THREAD (rule
// §17.2 — no synchronous disk I/O on the render path), landing the result on
// errorSpriteRes for pollErrorSprite to install. An empty path re-installs the
// embedded default synchronously (no file to read). Used both at startup (if the
// pref is non-empty) and on Settings → Apply, so a changed path re-triggers the
// load rather than latching once like the font-fallback read.
func (a *App) applyErrorSprite(path string) {
	if path == "" {
		// Cleared: restore the embedded default immediately (render thread, no I/O).
		a.errorSpriteErr = ""
		a.uploadEmbeddedMissingno()
		return
	}
	go func() {
		b, err := os.ReadFile(path)
		if err != nil {
			a.deliverErrorSprite(errorSpriteLoad{err: err})
			return
		}
		if len(b) == 0 {
			a.deliverErrorSprite(errorSpriteLoad{err: fmt.Errorf("file is empty")})
			return
		}
		if len(b) > errorSpriteMaxBytes {
			a.deliverErrorSprite(errorSpriteLoad{err: fmt.Errorf("file is too large (%d bytes, max %d)", len(b), errorSpriteMaxBytes)})
			return
		}
		dec, derr := assets.DecodeImage(b, false) // static: one frame, like the embedded default
		if derr != nil {
			a.deliverErrorSprite(errorSpriteLoad{err: derr})
			return
		}
		a.deliverErrorSprite(errorSpriteLoad{dec: dec})
	}()
}

// deliverErrorSprite hands a load result to the render thread. If the buffered
// slot already holds a stale result (a rapid re-Apply), drop the OLD one and
// release its decoded page so we never leak — the newest Apply wins. Releasing
// off-thread here is safe: Decoded.Release only returns pixel buffers to a
// sync.Pool (goroutine-safe), and the render thread's pollErrorSprite is the
// only other drainer of this cap-1 channel.
func (a *App) deliverErrorSprite(res errorSpriteLoad) {
	for {
		select {
		case a.errorSpriteRes <- res:
			return
		default:
			select {
			case old := <-a.errorSpriteRes:
				if old.dec != nil {
					old.dec.Release()
				}
			default:
			}
		}
	}
}

// pollErrorSprite installs a landed custom-placeholder load on the render thread:
// on success it UploadPinned's over render.MissingKey (replacing the embedded
// default or a prior custom); on failure it records the inline Settings error,
// pushes one debug line, and leaves whatever page is currently pinned (the
// embedded default at worst). Drained once per frame beside the font polls.
func (a *App) pollErrorSprite() {
	select {
	case res := <-a.errorSpriteRes:
		if res.err != nil {
			a.errorSpriteErr = res.err.Error()
			a.pushDebug("Custom missing-sprite image failed to load: " + res.err.Error())
			if res.dec != nil {
				res.dec.Release()
			}
			return
		}
		a.errorSpriteErr = ""
		if a.d.Store != nil && res.dec != nil {
			if err := a.d.Store.UploadPinned(render.MissingKey, res.dec); err != nil { // releases res.dec
				a.errorSpriteErr = err.Error()
				a.pushDebug("Custom missing-sprite image failed to upload: " + err.Error())
			}
		} else if res.dec != nil {
			res.dec.Release()
		}
	default:
	}
}
