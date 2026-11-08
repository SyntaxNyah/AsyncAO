package ui

// missingno is the placeholder drawn for a character sprite whose whole fallback chain 404'd. It mirrors AO2's
// placeholder asset (animationlayer.cpp appends the default theme's placeholder to the Idle/Talk fallback chain).
// A streaming client has no theme dir, so assets/missingno.webp (AO2-Client's bin/base/themes/default/placeholder.webp,
// copied verbatim) is embedded, decoded once at startup and uploaded under render.MissingKey.

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/platform"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

//go:embed assets/missingno.webp
var missingnoWebP []byte

const (
	// errorSpriteMaxBytes caps a custom placeholder file read (hard rule 4); 8 MiB rejects an accidental huge file.
	errorSpriteMaxBytes = 8 << 20
	// errorSpriteFieldID keys the custom image path field's focus.
	errorSpriteFieldID = "errorsprite"
	// errorSpriteUseLabel is the checkbox that switches the custom image on; errorSpriteBrowse opens the file dialog.
	errorSpriteUseLabel = "Use a custom image"
	errorSpriteBrowse   = "Browse…"
)

// errorSpriteFilters limits the file dialog to the formats the decoder reads.
var errorSpriteFilters = []sdl.DialogFilter{{Name: "Images", Pattern: "png;webp;gif;apng;jpg;jpeg"}}

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

// reloadErrorSprite pins the built-in placeholder, then starts loading the custom image when one is switched on, so a
// failed load leaves a valid page in place.
func (a *App) reloadErrorSprite() {
	a.errorSpriteErr = ""
	a.uploadEmbeddedMissingno()
	if path := a.d.Prefs.Load().CustomErrorSprite(); path != "" {
		a.loadErrorSprite(path)
	}
}

// loadErrorSprite loads the custom placeholder from path off-thread (hard rule 2), landing the result on
// errorSpriteRes for pollErrorSprite.
func (a *App) loadErrorSprite(path string) {
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

// drawMissingSpriteSettings is the Missing sprites card: the placeholder switch, then the custom image switch and path.
func (a *App) drawMissingSpriteSettings(y int32) int32 {
	c := a.ctx
	pad := a.formX
	snap := a.d.Prefs.Load()
	if next := c.Checkbox(pad, y, "Show a placeholder when a character sprite can't be found", snap.ShowMissingPlaceholder); next != snap.ShowMissingPlaceholder {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ShowMissingPlaceholder = next })
	}
	y += settRowH

	custom := snap.ErrorSpriteCustom
	if next := c.Checkbox(pad, y, errorSpriteUseLabel, custom); next != custom {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ErrorSpriteCustom = next })
		a.reloadErrorSprite()
	}
	fieldX := pad + c.CheckboxWidth(errorSpriteUseLabel) + settBtnGap
	fieldW := pad + a.formW - fieldX
	picker := a.d.Capabilities.Has(platform.PickFile)
	if picker {
		browseW := c.TextWidth(errorSpriteBrowse) + labelBtnPadX
		fieldW -= browseW + settBtnGap
		if c.Button(sdl.Rect{X: fieldX + fieldW + settBtnGap, Y: y, W: browseW, H: btnH}, errorSpriteBrowse) {
			a.browseForErrorSprite()
		}
	}
	field := sdl.Rect{X: fieldX, Y: y, W: fieldW, H: fieldH}
	path, enter := c.TextField(errorSpriteFieldID, field, snap.ErrorSpriteFile, "PNG, WebP, GIF, APNG or JPG file")
	if path != snap.ErrorSpriteFile {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ErrorSpriteFile = path })
	}
	focused := c.focusID == errorSpriteFieldID
	if enter || (settings.errorSpriteFocused && !focused) {
		a.reloadErrorSprite()
	}
	settings.errorSpriteFocused = focused
	y += fieldH + settBtnGap

	if a.errorSpriteErr != "" {
		y = a.settingsDesc(pad, y, "Couldn't load that image, so the built-in one is shown: "+a.errorSpriteErr, ColDanger)
	}
	return y + settSectionGap
}

// browseForErrorSprite shows the image file dialog; the result lands on errorSpritePickRes for pollErrorSpritePick.
func (a *App) browseForErrorSprite() {
	if settings.errorSpritePickBusy {
		settings.statusLine = "The file picker is already open, possibly behind this window."
		return
	}
	settings.errorSpritePickBusy = true
	location := ""
	if current := a.d.Prefs.Load().ErrorSpriteFile; current != "" {
		location = filepath.Dir(current)
	}
	a.d.Desktop.PickFiles(sdl.DialogOptions{
		Window:   a.ctx.win,
		Title:    "Pick a missing-sprite image",
		Location: location,
		Filters:  errorSpriteFilters,
	}, func(paths []string, err error) {
		// errorSpritePickBusy leaves the channel's one place free, so SDL's callback never blocks here.
		settings.errorSpritePickRes <- dialogPick{paths: paths, err: err}
		PushWake()
	})
}

// pollErrorSpritePick switches the custom image on with the picked file and loads it.
func (a *App) pollErrorSpritePick() {
	select {
	case pick := <-settings.errorSpritePickRes:
		settings.errorSpritePickBusy = false
		switch {
		case pick.err != nil:
			settings.statusLine = "Couldn't open the file picker, so type the path instead."
		case len(pick.paths) > 0:
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.ErrorSpriteCustom = true
				sn.ErrorSpriteFile = pick.paths[0]
			})
			a.reloadErrorSprite()
		}
	default:
	}
}
