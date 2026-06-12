// Package assets implements the zero-fallback asset pipeline: the lock-free
// resolution engine (learned formats), the tier-walking manager
// (T1 → T2 → T3 → network → decode), and the SDL-free decode pool.
package assets

import (
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// AssetType identifies what kind of asset a path refers to, selecting its
// format policy (spec §4).
type AssetType int

const (
	AssetTypeCharIcon AssetType = iota
	AssetTypeCharSprite
	AssetTypeBackground
	AssetTypeDeskOverlay
	AssetTypeShoutBubble
	AssetTypeMisc
	AssetTypeSFX
	AssetTypeMusic
	AssetTypeBlip
	AssetTypeEmoteButton
	// AssetTypeCount is the sentinel for fixed-size per-type tables.
	AssetTypeCount
)

// typeNames maps AssetType to the canonical config key names. Index order
// must match the enum; TestTypeNamesMatchConfig pins the correspondence.
var typeNames = [AssetTypeCount]string{
	AssetTypeCharIcon:    config.TypeCharIcon,
	AssetTypeCharSprite:  config.TypeCharSprite,
	AssetTypeBackground:  config.TypeBackground,
	AssetTypeDeskOverlay: config.TypeDeskOverlay,
	AssetTypeShoutBubble: config.TypeShoutBubble,
	AssetTypeMisc:        config.TypeMisc,
	AssetTypeSFX:         config.TypeSFX,
	AssetTypeMusic:       config.TypeMusic,
	AssetTypeBlip:        config.TypeBlip,
	AssetTypeEmoteButton: config.TypeEmoteButton,
}

// Valid reports whether t is a concrete asset type (not the sentinel).
func (t AssetType) Valid() bool {
	return t >= 0 && t < AssetTypeCount
}

// Name returns the canonical type name shared with internal/config.
func (t AssetType) Name() string {
	if !t.Valid() {
		return fmt.Sprintf("AssetType(%d)", int(t))
	}
	return typeNames[t]
}

// IsAudio reports whether the type carries audio payloads, which bypass the
// image decode pool entirely (spec §8: bytes go straight to SDL_mixer).
func (t AssetType) IsAudio() bool {
	return t == AssetTypeSFX || t == AssetTypeMusic || t == AssetTypeBlip
}

// TypeFromName resolves a canonical name back to its AssetType, for learned
// table warm-up from persisted preferences.
func TypeFromName(name string) (AssetType, bool) {
	for t := AssetType(0); t < AssetTypeCount; t++ {
		if typeNames[t] == name {
			return t, true
		}
	}
	return 0, false
}

// Decoded is the decode pool's output: plain memory, zero SDL types. The
// render thread turns frames into textures and then calls Release.
type Decoded struct {
	// Frames holds RGBA pixel data per animation frame; static images have
	// exactly one frame.
	Frames []*image.RGBA
	// Delays holds the per-frame display duration, parallel to Frames.
	// Static images carry a single zero delay.
	Delays []time.Duration
	// Animated reports whether the payload itself was animated (VP8X ANIM
	// flag, APNG acTL, multi-frame GIF) — independent of whether all frames
	// were decoded (PreferAnimated=false decodes only the first).
	Animated bool
	// Partial marks a progressive first-frame delivery: the full frame set
	// for the same URL follows from the same decode job and replaces this
	// page on upload (§perf: giant preanims start in one frame-decode).
	Partial bool
	// Width and Height are the canvas dimensions in pixels.
	Width, Height int

	// pooledPix tracks which frame pixel buffers were drawn from the pixel
	// pool and must be returned on Release.
	pooledPix []*[]byte
}

// PixelBytes returns the canvas payload size the T1 texture budget accounts
// for: Σ w×h×4 per frame.
func (d *Decoded) PixelBytes() int64 {
	var total int64
	for _, f := range d.Frames {
		if f != nil {
			total += int64(len(f.Pix))
		}
	}
	return total
}

// Release returns pooled frame buffers to the pixel pool. Call exactly once,
// after the frames have been uploaded to textures; the Decoded must not be
// used afterwards.
func (d *Decoded) Release() {
	for _, buf := range d.pooledPix {
		putPixBuf(buf)
	}
	d.pooledPix = nil
	d.Frames = nil
}

// ResolvedAsset describes where an asset was found and how.
type ResolvedAsset struct {
	// URL is the full asset URL that answered.
	URL string
	// Ext is the extension that worked (".webp", ".png", ...).
	Ext string
	// Type is the asset's type.
	Type AssetType
	// Learned reports whether the format came from the learned table
	// (exactly one probe) rather than list probing.
	Learned bool
}

// ErrAllFormatsMissing reports that every candidate format 404'd. The UI
// surfaces it as a visible warning naming the asset and formats tried
// (spec §4: "enable fallbacks or ask the server to ship .webp").
type ErrAllFormatsMissing struct {
	Base  string // URL base without extension
	Type  AssetType
	Tried []string // extensions probed, in order
}

func (e *ErrAllFormatsMissing) Error() string {
	return fmt.Sprintf("assets: %s not found as %s (type %s); enable fallbacks or ask the server to ship %s",
		e.Base, strings.Join(e.Tried, ", "), e.Type.Name(), preferredExtFor(e.Type))
}

// preferredExtFor names the modern format the warning nudges content packs
// toward.
func preferredExtFor(t AssetType) string {
	if t.IsAudio() {
		return config.ExtOpus
	}
	if t == AssetTypeCharIcon {
		return config.ExtPNG
	}
	return config.ExtWebP
}
