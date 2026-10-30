// Package assets implements the asset pipeline: lock-free resolver, tier-walking manager and SDL-free decode pool.
package assets

import (
	"fmt"
	"image"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// AssetType identifies what kind of asset a path refers to, selecting its format policy.
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
	// AssetTypeEffect is AO2 screen-effect art; extensions.json has no effects key, so it is learned per host.
	AssetTypeEffect
	// AssetTypeCount is the sentinel for fixed-size per-type tables.
	AssetTypeCount
)

// typeNames maps AssetType to config key names; index order must match the enum.
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
	AssetTypeEffect:      config.TypeEffect,
}

func (t AssetType) Valid() bool {
	return t >= 0 && t < AssetTypeCount
}

// Name returns the type name shared with internal/config.
func (t AssetType) Name() string {
	if !t.Valid() {
		return fmt.Sprintf("AssetType(%d)", int(t))
	}
	return typeNames[t]
}

// IsAudio reports whether the type carries audio, which bypasses the image decode pool and goes straight to SDL_mixer.
func (t AssetType) IsAudio() bool {
	return t == AssetTypeSFX || t == AssetTypeMusic || t == AssetTypeBlip
}

// TypeFromName resolves a config key name back to its AssetType.
func TypeFromName(name string) (AssetType, bool) {
	for t := AssetType(0); t < AssetTypeCount; t++ {
		if typeNames[t] == name {
			return t, true
		}
	}
	return 0, false
}

// Decoded is the decode pool's output, plain memory with no SDL types; the render thread calls Release after upload.
type Decoded struct {
	// Frames holds RGBA pixels per frame; static images have one.
	Frames []*image.RGBA
	// Delays is parallel to Frames; static images carry a single zero delay.
	Delays []time.Duration
	// Animated reports whether the payload was animated, even if only the first frame was decoded.
	Animated bool
	// SourceFrames is the pre-decimation count, the space remote frame-effect indices use; 0 means len(Frames).
	SourceFrames int
	// Partial marks an incomplete delivery: the full set later replaces this page, or for a Stream the page grows.
	Partial bool
	// Stream marks frames extending the resident page rather than replacing it, from kept index FrameOffset;
	// frame 0 parks the page in the oversized map to avoid LRU churn. Partial false ends the stream.
	Stream      bool
	FrameOffset int
	// Width and Height are the canvas size in pixels.
	Width, Height int
	// Compressed holds DXT payloads parallel to Frames, nil when off; when set, Frames has been released.
	Compressed [][]byte
	// DXTFormat is the FOURCC of Compressed (0 = raw RGBA).
	DXTFormat uint32

	// pooledPix tracks pool-drawn frame buffers to return on Release.
	pooledPix []*[]byte
}

// PixelBytes returns the payload size the T1 budget accounts for: w*h*4 per frame, or the DXT size when compressed.
func (d *Decoded) PixelBytes() int64 {
	var total int64
	if d.DXTFormat != 0 && len(d.Compressed) > 0 {
		for _, c := range d.Compressed {
			total += int64(len(c))
		}
		return total
	}
	for _, f := range d.Frames {
		if f != nil {
			total += int64(len(f.Pix))
		}
	}
	return total
}

// Release returns pooled frame buffers; call exactly once, after upload, and do not use the Decoded afterwards.
func (d *Decoded) Release() {
	for _, buf := range d.pooledPix {
		putPixBuf(buf)
	}
	d.pooledPix = nil
	d.Frames = nil
	d.Compressed = nil
}

// ResolvedAsset describes where an asset was found and how.
type ResolvedAsset struct {
	// URL is the asset URL that answered.
	URL string
	// Ext is the extension that worked.
	Ext string
	// Type is the asset type.
	Type AssetType
	// Learned reports whether the format came from the learned table, which means exactly one probe.
	Learned bool
}
