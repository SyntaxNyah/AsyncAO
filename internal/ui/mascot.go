package ui

// Mayo, the AsyncAO mascot and app icon (art by hlenbchan). The 512x512 art is embedded once and used for the SDL
// window icon (SetWindowIcon), the About page texture (mayoTexture) and the README.

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	"image/png"
	"unsafe"

	xdraw "golang.org/x/image/draw"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

//go:embed assets/mayo.png
var mayoPNG []byte

// SetWindowIcon (mascot_icon.go) is a no-op on Windows because the exe embeds a multi-resolution
// .ico (cmd/asyncao/rsrc_windows.syso) that one large SDL surface would override with a poor downscale.

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

// mayoTexture returns the About-page portrait texture and its logical size (aboutMascotPx, aspect kept). It is a
// Catmull-Rom downscale baked at the physical pixel size (logical x UI scale) so it draws 1:1; built once on the
// render thread and rebuilt only when the UI scale changes. Nil on failure.
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
	physW := value.Clamp(int(logW)*scale/100, 1, sb.Dx())
	physH := value.Clamp(int(logH)*scale/100, 1, sb.Dy())
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
