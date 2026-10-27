package ttf

// Text calls pass the Go string's own bytes with an explicit length: SDL3_ttf
// never keeps them, so no C copy is made per measurement. noescape keeps the
// out-parameters on the stack.

/*
#cgo pkg-config: sdl3-ttf
#cgo noescape TTF_GetStringSize
#cgo nocallback TTF_GetStringSize
#cgo noescape TTF_RenderText_Blended
#cgo nocallback TTF_RenderText_Blended
#include <SDL3_ttf/SDL_ttf.h>

static const char *asyncaoEmptyText(void) { return ""; }
*/
import "C"

import (
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Font is an open font at one size.
type Font struct{ f *C.TTF_Font }

// Font styles.
const (
	STYLE_NORMAL = C.TTF_STYLE_NORMAL
	STYLE_BOLD   = C.TTF_STYLE_BOLD
	STYLE_ITALIC = C.TTF_STYLE_ITALIC
)

// Init starts SDL_ttf. Calls nest: each Init needs a matching Quit.
func Init() error {
	if !C.TTF_Init() {
		return sdl.GetError()
	}
	return nil
}

// Quit undoes one Init.
func Quit() { C.TTF_Quit() }

// OpenFontRW opens a font from a stream at size points. A non-zero freesrc
// closes the stream with the font; the stream's memory must outlive the font.
func OpenFontRW(src *sdl.RWops, freesrc, size int) (*Font, error) {
	f := C.TTF_OpenFontIO((*C.SDL_IOStream)(unsafe.Pointer(src)), freesrc != 0, C.float(size))
	if f == nil {
		return nil, sdl.GetError()
	}
	return &Font{f: f}, nil
}

// Close frees the font.
func (f *Font) Close() { C.TTF_CloseFont(f.f) }

// Height is the font's maximum glyph height in pixels.
func (f *Font) Height() int { return int(C.TTF_GetFontHeight(f.f)) }

// Ascent is the distance from the baseline to the top, in pixels.
func (f *Font) Ascent() int { return int(C.TTF_GetFontAscent(f.f)) }

// LineSkip is the recommended distance between baselines.
func (f *Font) LineSkip() int { return int(C.TTF_GetFontLineSkip(f.f)) }

// GetStyle returns the STYLE_* flags.
func (f *Font) GetStyle() int { return int(C.TTF_GetFontStyle(f.f)) }

// SetStyle sets the STYLE_* flags.
func (f *Font) SetStyle(style int) { C.TTF_SetFontStyle(f.f, C.TTF_FontStyleFlags(style)) }

func cText(text string) (*C.char, C.size_t) {
	if text == "" {
		return C.asyncaoEmptyText(), 0
	}
	return (*C.char)(unsafe.Pointer(unsafe.StringData(text))), C.size_t(len(text))
}

// SizeUTF8 returns the pixel size text would render at.
func (f *Font) SizeUTF8(text string) (w, h int, err error) {
	var cw, ch C.int
	p, n := cText(text)
	if !C.TTF_GetStringSize(f.f, p, n, &cw, &ch) {
		return 0, 0, sdl.GetError()
	}
	return int(cw), int(ch), nil
}

// RenderUTF8Blended renders text anti-aliased onto a new ARGB surface.
func (f *Font) RenderUTF8Blended(text string, fg sdl.Color) (*sdl.Surface, error) {
	p, n := cText(text)
	col := C.SDL_Color{r: C.Uint8(fg.R), g: C.Uint8(fg.G), b: C.Uint8(fg.B), a: C.Uint8(fg.A)}
	return sdl.WrapSurface(unsafe.Pointer(C.TTF_RenderText_Blended(f.f, p, n, col)))
}
