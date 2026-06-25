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

// mayoTexture lazily uploads the mascot art to a static SDL texture for the About
// page (render thread only — drawAbout runs there). Cached on the App; nil on
// failure (About then omits the portrait). Mirrors the colour-wheel texture path.
func (a *App) mayoTexture() (*sdl.Texture, int32, int32) {
	if a.mayoTex != nil {
		return a.mayoTex, a.mayoW, a.mayoH
	}
	if a.mayoTexTried {
		return nil, 0, 0 // decode/upload already failed once — don't retry every frame
	}
	a.mayoTexTried = true
	rgba, ok := decodeMayoRGBA()
	if !ok {
		return nil, 0, 0
	}
	b := rgba.Bounds()
	w, h := int32(b.Dx()), int32(b.Dy())
	tex, err := a.ctx.Ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_STATIC, w, h)
	if err != nil {
		return nil, 0, 0
	}
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	_ = tex.Update(nil, unsafe.Pointer(&rgba.Pix[0]), rgba.Stride)
	a.mayoTex, a.mayoW, a.mayoH = tex, w, h
	return tex, w, h
}
