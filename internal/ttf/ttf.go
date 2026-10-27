package ttf

import (
	sdl2ttf "github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

type (
	Font = sdl2ttf.Font
)

const (
	STYLE_BOLD   = sdl2ttf.STYLE_BOLD
	STYLE_ITALIC = sdl2ttf.STYLE_ITALIC
	STYLE_NORMAL = sdl2ttf.STYLE_NORMAL
)

func Init() error {
	return sdl2ttf.Init()
}

func OpenFontRW(src *sdl.RWops, freesrc, size int) (*Font, error) {
	return sdl2ttf.OpenFontRW(src, freesrc, size)
}

func Quit() {
	sdl2ttf.Quit()
}
