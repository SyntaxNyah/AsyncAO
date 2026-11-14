package render

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

const (
	// copyCacheBudget caps the pixels a CopyCache keeps, in bytes: about four 2560×1440 windows.
	copyCacheBudget = 64 << 20
	// maxCachedCopyBytes is the largest one copy may be; a bigger draw goes to SDL directly.
	maxCachedCopyBytes = copyCacheBudget / 4
	copyBytesPerPixel  = 4
	// copyCacheMaxEntries caps the copies kept, so an eviction scan stays short.
	copyCacheMaxEntries = 512
	// copyCacheMaxSeen caps the remembered mods; past it they are forgotten and relearned.
	copyCacheMaxSeen = 2 * copyCacheMaxEntries
)

// copyKey is what a cached copy was made from; any change makes a new one.
type copyKey struct {
	id         uint32 // the texture's SDL properties ID, never reused
	version    int64
	src        sdl.Rect
	w, h       int32
	flip       sdl.RendererFlip
	r, g, b, a uint8
	scale      sdl.ScaleMode
}

// copyMods is the tint and fade a draw of one texture region last had.
type copyMods struct {
	r, g, b, a uint8
	version    int64
}

// copyRegion names one texture region drawn at one size and mirror, whatever its mods.
type copyRegion struct {
	id   uint32
	src  sdl.Rect
	w, h int32
	flip sdl.RendererFlip
}

type cachedCopy struct {
	tex   *sdl.Texture
	bytes int64
	used  uint64
}

// CopyCache draws on SDL's software renderer from copies already scaled, mirrored, tinted and faded the way they are
// drawn, so each draw is a 1:1 blend: that renderer blends a scaled, tinted or faded texture 12 to 33 times slower
// than a plain one. Render targets, whose pixels change without an update, and other renderers draw directly. Render
// thread only.
type CopyCache struct {
	software bool
	entries  map[copyKey]*cachedCopy
	// seen is each region's last mods: a region whose fade or tint moved since its last draw is animating, and a copy
	// built for every step would cost more than it saves, so it draws directly until its mods hold still.
	seen  map[copyRegion]copyMods
	bytes int64
	uses  uint64
	// mirror holds a mirrored region while a copy is built; mirrorW, mirrorH is its size.
	mirror           *sdl.Texture
	mirrorW, mirrorH int32
	// Scratch, so a draw allocates nothing.
	state         sdl.TextureState
	src, dst, clp sdl.Rect
}

// NewCopyCache makes a cache for ren; it only keeps copies when ren is SDL's software renderer.
func NewCopyCache(ren *sdl.Renderer) *CopyCache {
	return &CopyCache{software: isSoftwareRenderer(ren), entries: map[copyKey]*cachedCopy{},
		seen: map[copyRegion]copyMods{}}
}

// Copy draws src of t into dst as Renderer.Copy does.
func (c *CopyCache) Copy(ren *sdl.Renderer, t *sdl.Texture, src, dst *sdl.Rect) error {
	return c.CopyFlip(ren, t, src, dst, sdl.FLIP_NONE)
}

// CopyFlip is Copy mirrored by flip, as Renderer.CopyEx with no rotation draws. A nil cache draws directly.
func (c *CopyCache) CopyFlip(ren *sdl.Renderer, t *sdl.Texture, src, dst *sdl.Rect, flip sdl.RendererFlip) error {
	if c != nil && c.software && t != nil && dst != nil && c.drawCached(ren, t, src, dst, flip) {
		return nil
	}
	if flip == sdl.FLIP_NONE {
		return ren.Copy(t, src, dst)
	}
	return ren.CopyEx(t, src, dst, 0, nil, flip)
}

// drawCached draws from a cached copy and reports whether it did; false leaves the draw to SDL.
func (c *CopyCache) drawCached(ren *sdl.Renderer, t *sdl.Texture, src, dst *sdl.Rect, flip sdl.RendererFlip) bool {
	t.State(&c.state)
	if c.state.Access == sdl.TEXTUREACCESS_TARGET {
		return false
	}
	c.src = sdl.Rect{W: c.state.W, H: c.state.H}
	if src != nil {
		c.src = *src
	}
	sx, sy := ren.GetScale()
	c.dst = sdl.Rect{X: scaledPixels(dst.X, sx), Y: scaledPixels(dst.Y, sy), W: scaledPixels(dst.W, sx),
		H: scaledPixels(dst.H, sy)}
	plain := c.state.R == channelMax && c.state.G == channelMax && c.state.B == channelMax && c.state.A == channelMax
	if plain && flip == sdl.FLIP_NONE && c.dst.W == c.src.W && c.dst.H == c.src.H {
		return false // already a 1:1 blend
	}
	bytes := int64(c.dst.W) * int64(c.dst.H) * copyBytesPerPixel
	if c.dst.W <= 0 || c.dst.H <= 0 || c.src.W <= 0 || c.src.H <= 0 || bytes > maxCachedCopyBytes {
		return false
	}
	key := copyKey{id: c.state.ID, version: c.state.Version, src: c.src, w: c.dst.W, h: c.dst.H, flip: flip,
		r: c.state.R, g: c.state.G, b: c.state.B, a: c.state.A, scale: c.state.Scale}
	e, ok := c.entries[key]
	if !ok && !c.holdsStill(key) {
		return false
	}
	if !ok {
		if e = c.build(ren, t, key, bytes); e == nil {
			return false
		}
	}
	c.uses++
	e.used = c.uses
	_ = e.tex.SetBlendMode(c.state.Blend)
	c.blit(ren, e.tex, sx, sy)
	return true
}

// holdsStill reports whether key's region was last drawn with the same mods and pixels, and remembers these.
func (c *CopyCache) holdsStill(key copyKey) bool {
	region := copyRegion{id: key.id, src: key.src, w: key.w, h: key.h, flip: key.flip}
	mods := copyMods{r: key.r, g: key.g, b: key.b, a: key.a, version: key.version}
	last, known := c.seen[region]
	if len(c.seen) >= copyCacheMaxSeen {
		clear(c.seen)
	}
	c.seen[region] = mods
	return known && last == mods
}

// build makes the copy of key: t's region scaled to the copy's size with its mods and mirror applied, alpha kept.
// Blending off writes the modulated pixels, alpha included, over the whole target. A mirror is drawn first, at the
// region's size and without mods: SDL's software renderer mirrors through its rotation path, which applies an alpha
// mod to the colour as well, so a faded mirrored copy came out twice as faded.
func (c *CopyCache) build(ren *sdl.Renderer, t *sdl.Texture, key copyKey, bytes int64) *cachedCopy {
	c.evictFor(bytes)
	tex, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ARGB8888), sdl.TEXTUREACCESS_TARGET, key.w, key.h)
	if err != nil {
		return nil
	}
	prev := ren.GetRenderTarget()
	src, srcRect := t, &c.src
	_ = t.SetBlendMode(sdl.BLENDMODE_NONE)
	if key.flip != sdl.FLIP_NONE {
		if !fitTarget(ren, &c.mirror, &c.mirrorW, &c.mirrorH, c.src.W, c.src.H) || ren.SetRenderTarget(c.mirror) != nil {
			_ = t.SetBlendMode(c.state.Blend)
			_ = tex.Destroy()
			return nil
		}
		_ = t.SetColorMod(channelMax, channelMax, channelMax)
		_ = t.SetAlphaMod(channelMax)
		_ = ren.CopyEx(t, &c.src, nil, 0, nil, key.flip)
		_ = t.SetColorMod(key.r, key.g, key.b)
		_ = t.SetAlphaMod(key.a)
		_ = c.mirror.SetBlendMode(sdl.BLENDMODE_NONE)
		_ = c.mirror.SetColorMod(key.r, key.g, key.b)
		_ = c.mirror.SetAlphaMod(key.a)
		_ = c.mirror.SetScaleMode(key.scale)
		src, srcRect = c.mirror, nil
	}
	ok := ren.SetRenderTarget(tex) == nil
	if ok {
		_ = ren.Copy(src, srcRect, nil)
	}
	_ = t.SetBlendMode(c.state.Blend)
	_ = ren.SetRenderTarget(prev)
	if !ok {
		_ = tex.Destroy()
		return nil
	}
	e := &cachedCopy{tex: tex, bytes: bytes}
	c.entries[key] = e
	c.bytes += bytes
	return e
}

// evictFor drops the least recently drawn copies until bytes more and one more entry fit.
func (c *CopyCache) evictFor(bytes int64) {
	for (c.bytes+bytes > copyCacheBudget || len(c.entries) >= copyCacheMaxEntries) && len(c.entries) > 0 {
		var oldest copyKey
		var oldestUse uint64
		first := true
		for k, e := range c.entries {
			if first || e.used < oldestUse {
				oldest, oldestUse, first = k, e.used, false
			}
		}
		c.drop(oldest)
	}
}

func (c *CopyCache) drop(k copyKey) {
	e := c.entries[k]
	_ = e.tex.Destroy()
	c.bytes -= e.bytes
	delete(c.entries, k)
}

// blit draws a copy 1:1 at c.dst, in pixels: a clip set in render units is converted for it and put back after.
func (c *CopyCache) blit(ren *sdl.Renderer, tex *sdl.Texture, sx, sy float32) {
	if sx == 1 && sy == 1 {
		_ = ren.Copy(tex, nil, &c.dst)
		return
	}
	clipped := ren.IsClipEnabled()
	if clipped {
		c.clp = ren.GetClipRect()
	}
	_ = ren.SetScale(1, 1)
	if clipped {
		logical := c.clp
		c.clp = sdl.Rect{X: scaledPixels(logical.X, sx), Y: scaledPixels(logical.Y, sy), W: scaledPixels(logical.W, sx),
			H: scaledPixels(logical.H, sy)}
		_ = ren.SetClipRect(&c.clp)
		c.clp = logical
	}
	_ = ren.Copy(tex, nil, &c.dst)
	_ = ren.SetScale(sx, sy)
	if clipped {
		_ = ren.SetClipRect(&c.clp)
	}
}

// Release destroys every cached copy; render thread only.
func (c *CopyCache) Release() {
	for k := range c.entries {
		c.drop(k)
	}
	if c.mirror != nil {
		_ = c.mirror.Destroy()
		c.mirror = nil
	}
}
