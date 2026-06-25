package ui

// Mayo — the AsyncAO mascot AND app icon (commissioned by Nyah, illustrated by
// hlenbchan / @hlenbchan2). She's a mashup of the Go gopher (AsyncAO is written
// in Go — hence the gopher-blue palette) and Maya Fey from Ace Attorney; the name
// began as "MayAO" (Maya + AO) and became "Mayo". The 512x512 art is embedded
// once and used three ways: the SDL window/taskbar icon (SetWindowIcon), a
// texture on the About page (mayoTexture), and the README.

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	"image/png"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
	xdraw "golang.org/x/image/draw"
)

//go:embed assets/mayo.png
var mayoPNG []byte

// SetWindowIcon sets the window / taskbar icon to Mayo. It's split per-platform
// (mascot_icon_*.go): on Windows it's a no-op because the executable embeds a
// proper multi-resolution .ico resource (cmd/asyncao/rsrc_windows.syso) that
// Windows renders crisply at every size — handing SDL one big surface would
// override that resource with a badly downscaled icon. Elsewhere it uploads the
// embedded art via SDL_SetWindowIcon.

// decodeMayoRGBA decodes the embedded mascot into a tightly-packed RGBA image
// (image.RGBA byte order == SDL ABGR8888). ok=false on any decode failure, so
// every caller degrades to "no image" instead of crashing.
func decodeMayoRGBA() (*image.RGBA, bool) {
	img, err := png.Decode(bytes.NewReader(mayoPNG))
	if err != nil {
		return nil, false
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba, true
	}
	b := img.Bounds()
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, img, b.Min, draw.Src)
	return rgba, true
}

// mayoTexture returns the About-page portrait texture and the LOGICAL size to draw
// it at (aboutMascotPx, aspect-kept). The texture is a high-quality Catmull-Rom
// downscale of the embedded 512² art, baked to the exact PHYSICAL pixel size
// (logical × the UI render scale) so it draws 1:1 on screen — far sharper than
// letting the GPU shrink the full-res art, and independent of the smooth-scaling
// setting. Built once (render thread), cached, and rebuilt only when the UI scale
// changes; nil on failure (About omits the portrait). ZERO per-frame cost — the
// resample never touches the render loop.
func (a *App) mayoTexture() (*sdl.Texture, int32, int32) {
	scale := a.UIScale() // percent; bake to physical px = logical × scale
	if a.mayoTex != nil {
		if a.mayoTexScale == scale {
			return a.mayoTex, a.mayoLogW, a.mayoLogH
		}
		_ = a.mayoTex.Destroy() // UI scale changed — rebake at the new physical size
		a.mayoTex = nil
	} else if a.mayoTexFailed {
		return nil, 0, 0 // decode/upload failed once — don't retry (and re-resample) every frame
	}
	a.mayoTexScale = scale

	src, ok := decodeMayoRGBA()
	if !ok {
		a.mayoTexFailed = true
		return nil, 0, 0
	}
	sb := src.Bounds()
	// Logical (on-screen) size: cap the larger side to aboutMascotPx, keep aspect.
	logW, logH := aboutMascotPx, aboutMascotPx
	if sb.Dx() > sb.Dy() {
		logH = aboutMascotPx * int32(sb.Dy()) / int32(sb.Dx())
	} else if sb.Dy() > sb.Dx() {
		logW = aboutMascotPx * int32(sb.Dx()) / int32(sb.Dy())
	}
	// Physical pixels actually shown = logical × UI scale. Bake exactly that (1:1),
	// capped at the source resolution so we never upscale past the art.
	physW := clampInt(int(logW)*scale/100, 1, sb.Dx())
	physH := clampInt(int(logH)*scale/100, 1, sb.Dy())
	dst := image.NewRGBA(image.Rect(0, 0, physW, physH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, sb, xdraw.Src, nil)

	tex, err := a.ctx.Ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_STATIC, int32(physW), int32(physH))
	if err != nil {
		a.mayoTexFailed = true
		return nil, 0, 0
	}
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	_ = tex.Update(nil, unsafe.Pointer(&dst.Pix[0]), dst.Stride)
	a.mayoTex, a.mayoLogW, a.mayoLogH = tex, logW, logH
	return tex, logW, logH
}
