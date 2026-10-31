package ui

// missingno is the placeholder drawn for a character sprite whose whole fallback chain 404'd. It mirrors AO2's
// placeholder asset (animationlayer.cpp appends the default theme's placeholder to the Idle/Talk fallback chain).
// A streaming client has no theme dir, so assets/missingno.webp (AO2-Client's bin/base/themes/default/placeholder.webp,
// copied verbatim) is embedded, decoded once at startup and uploaded under render.MissingKey.

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

//go:embed assets/missingno.webp
var missingnoWebP []byte

// errorSpriteMaxBytes caps a custom placeholder file read (hard rule 4); 8 MiB rejects an accidental huge file.
const errorSpriteMaxBytes = 8 << 20

// errorSpriteLoad carries an off-thread custom-placeholder load result to the render thread. On failure dec is nil
// and err says why (shown in Settings and the debug overlay), leaving the embedded default in place.
type errorSpriteLoad struct {
	dec *assets.Decoded
	err error
}

// uploadEmbeddedMissingno decodes the embedded placeholder and pins it under render.MissingKey. Called once at
// startup on the render thread, before any custom load. A failure leaves no MissingKey page, so the miss branch
// holds the previous frame or draws blank.
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

// applyErrorSprite loads the custom placeholder from path off-thread (hard rule 2), landing the result on
// errorSpriteRes for pollErrorSprite. An empty path re-installs the embedded default synchronously.
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

// deliverErrorSprite hands a load result to the render thread. A stale result already in the buffer is dropped and
// its decoded page released; Release only returns buffers to a sync.Pool, so it is safe off-thread.
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

// pollErrorSprite installs a landed custom-placeholder load on the render thread. On success it uploads over
// render.MissingKey; on failure it records the Settings error, pushes a debug line and keeps the current page.
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
