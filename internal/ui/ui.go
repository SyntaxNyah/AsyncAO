// Package ui is a small immediate-mode widget kit over SDL2 plus the
// client's screens (lobby, character select, courtroom chrome, settings,
// about). Render-thread only.
package ui

import (
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
	"golang.org/x/image/font/gofont/goregular"
)

const (
	// UIFontSize is the default chrome font size.
	UIFontSize = 14
	// UIFontSizeBig is for headings.
	UIFontSizeBig = 22

	// textCacheMax bounds the rendered-label texture cache; past it the
	// cache purges wholesale (cheap, rare — screen switches).
	textCacheMax = 512

	// HoverPreviewDelay is how long the cursor must rest on an emote or
	// char icon before the full-size preview pops (right-click = instant).
	HoverPreviewDelay = 3 * time.Second

	scrollStepPx = 32
	caretBlink   = 500 * time.Millisecond
)

// Palette: dark chrome, AO-flavored accents, and the lobby tier colors.
var (
	ColBackground = sdl.Color{R: 24, G: 24, B: 28, A: 255}
	ColPanel      = sdl.Color{R: 36, G: 36, B: 42, A: 255}
	ColPanelHi    = sdl.Color{R: 52, G: 52, B: 60, A: 255}
	ColAccent     = sdl.Color{R: 120, G: 170, B: 255, A: 255}
	ColText       = sdl.Color{R: 235, G: 235, B: 235, A: 255}
	ColTextDim    = sdl.Color{R: 150, G: 150, B: 155, A: 255}
	ColDanger     = sdl.Color{R: 235, G: 80, B: 80, A: 255}

	// Lobby security tiers.
	ColTierGreen  = sdl.Color{R: 70, G: 200, B: 90, A: 255}  // wss: fastest & secure
	ColTierYellow = sdl.Color{R: 222, G: 205, B: 58, A: 255} // ws only: insecure
	ColTierBlack  = sdl.Color{R: 10, G: 10, B: 10, A: 255}   // legacy: unsupported
	ColStar       = sdl.Color{R: 255, G: 215, B: 0, A: 255}  // favorites
)

type textKey struct {
	text  string
	color sdl.Color
	big   bool
}

type cachedText struct {
	tex  *sdl.Texture
	w, h int32
}

// Ctx is the per-frame UI context: input snapshot, fonts, text cache, focus.
type Ctx struct {
	Ren *sdl.Renderer

	font    *ttf.Font
	fontBig *ttf.Font

	mouseX, mouseY int32
	clicked        bool // left released this frame
	rightClicked   bool
	wheelY         int32
	typed          string
	backspace      bool
	enter          bool
	tabPressed     bool

	focusID  string
	caretOn  bool
	caretAcc time.Duration

	// hover preview tracking
	hoverID    string
	hoverSince time.Time

	textCache map[textKey]cachedText
}

// NewCtx loads the embedded Go font and prepares the kit.
func NewCtx(ren *sdl.Renderer) (*Ctx, error) {
	font, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		return nil, err
	}
	fontBig, err := loadEmbeddedFont(UIFontSizeBig)
	if err != nil {
		return nil, err
	}
	return &Ctx{
		Ren:       ren,
		font:      font,
		fontBig:   fontBig,
		textCache: map[textKey]cachedText{},
	}, nil
}

func loadEmbeddedFont(size int) (*ttf.Font, error) {
	rw, err := sdl.RWFromMem(goregular.TTF)
	if err != nil {
		return nil, err
	}
	return ttf.OpenFontRW(rw, 1, size)
}

// Font exposes the chrome font (typewriter rasterizer reuse).
func (c *Ctx) Font() *ttf.Font { return c.font }

// BeginFrame ingests this frame's input events. Pass every SDL event.
func (c *Ctx) BeginFrame(dt time.Duration) {
	c.clicked = false
	c.rightClicked = false
	c.wheelY = 0
	c.typed = ""
	c.backspace = false
	c.enter = false
	c.tabPressed = false
	x, y, _ := sdl.GetMouseState()
	c.mouseX, c.mouseY = x, y

	c.caretAcc += dt
	if c.caretAcc >= caretBlink {
		c.caretAcc = 0
		c.caretOn = !c.caretOn
	}
}

// HandleEvent feeds one SDL event into the frame's input snapshot.
func (c *Ctx) HandleEvent(ev sdl.Event) {
	switch e := ev.(type) {
	case *sdl.MouseButtonEvent:
		if e.Type == sdl.MOUSEBUTTONUP {
			switch e.Button {
			case sdl.BUTTON_LEFT:
				c.clicked = true
			case sdl.BUTTON_RIGHT:
				c.rightClicked = true
			}
		}
	case *sdl.MouseWheelEvent:
		c.wheelY += e.Y
	case *sdl.TextInputEvent:
		c.typed += e.GetText()
	case *sdl.KeyboardEvent:
		if e.Type == sdl.KEYDOWN {
			switch e.Keysym.Sym {
			case sdl.K_BACKSPACE:
				c.backspace = true
			case sdl.K_RETURN, sdl.K_KP_ENTER:
				c.enter = true
			case sdl.K_TAB:
				c.tabPressed = true
			}
		}
	}
}

// hovering reports whether the mouse is inside r.
func (c *Ctx) hovering(r sdl.Rect) bool {
	return c.mouseX >= r.X && c.mouseX < r.X+r.W && c.mouseY >= r.Y && c.mouseY < r.Y+r.H
}

// --- draw primitives -----------------------------------------------------------

// Fill draws a solid rect.
func (c *Ctx) Fill(r sdl.Rect, col sdl.Color) {
	_ = c.Ren.SetDrawColor(col.R, col.G, col.B, col.A)
	_ = c.Ren.FillRect(&r)
}

// Border outlines a rect.
func (c *Ctx) Border(r sdl.Rect, col sdl.Color) {
	_ = c.Ren.SetDrawColor(col.R, col.G, col.B, col.A)
	_ = c.Ren.DrawRect(&r)
}

// textTexture returns (and caches) a rendered label.
func (c *Ctx) textTexture(text string, col sdl.Color, big bool) (cachedText, bool) {
	if text == "" {
		return cachedText{}, false
	}
	key := textKey{text: text, color: col, big: big}
	if t, ok := c.textCache[key]; ok {
		return t, true
	}
	font := c.font
	if big {
		font = c.fontBig
	}
	surf, err := font.RenderUTF8Blended(text, col)
	if err != nil {
		return cachedText{}, false
	}
	defer surf.Free()
	tex, err := c.Ren.CreateTextureFromSurface(surf)
	if err != nil {
		return cachedText{}, false
	}
	if len(c.textCache) >= textCacheMax {
		c.purgeTextCache()
	}
	entry := cachedText{tex: tex, w: surf.W, h: surf.H}
	c.textCache[key] = entry
	return entry, true
}

func (c *Ctx) purgeTextCache() {
	for k, v := range c.textCache {
		if v.tex != nil {
			_ = v.tex.Destroy()
		}
		delete(c.textCache, k)
	}
}

// Label draws text at (x, y) and returns its pixel width.
func (c *Ctx) Label(x, y int32, text string, col sdl.Color) int32 {
	t, ok := c.textTexture(text, col, false)
	if !ok {
		return 0
	}
	dst := sdl.Rect{X: x, Y: y, W: t.w, H: t.h}
	_ = c.Ren.Copy(t.tex, nil, &dst)
	return t.w
}

// Heading draws large text.
func (c *Ctx) Heading(x, y int32, text string, col sdl.Color) {
	t, ok := c.textTexture(text, col, true)
	if !ok {
		return
	}
	dst := sdl.Rect{X: x, Y: y, W: t.w, H: t.h}
	_ = c.Ren.Copy(t.tex, nil, &dst)
}

// LabelClipped draws text clipped to maxW.
func (c *Ctx) LabelClipped(x, y, maxW int32, text string, col sdl.Color) {
	t, ok := c.textTexture(text, col, false)
	if !ok {
		return
	}
	w := t.w
	if w > maxW {
		w = maxW
	}
	src := sdl.Rect{X: 0, Y: 0, W: w, H: t.h}
	dst := sdl.Rect{X: x, Y: y, W: w, H: t.h}
	_ = c.Ren.Copy(t.tex, &src, &dst)
}

// TextWidth measures a label.
func (c *Ctx) TextWidth(text string) int32 {
	w, _, err := c.font.SizeUTF8(text)
	if err != nil {
		return 0
	}
	return int32(w)
}

// --- widgets ---------------------------------------------------------------------

// Button draws a clickable button; returns true on click.
func (c *Ctx) Button(r sdl.Rect, label string) bool {
	hover := c.hovering(r)
	bg := ColPanel
	if hover {
		bg = ColPanelHi
	}
	c.Fill(r, bg)
	c.Border(r, ColAccent)
	t, ok := c.textTexture(label, ColText, false)
	if ok {
		dst := sdl.Rect{X: r.X + (r.W-t.w)/2, Y: r.Y + (r.H-t.h)/2, W: t.w, H: t.h}
		_ = c.Ren.Copy(t.tex, nil, &dst)
	}
	return hover && c.clicked
}

// Checkbox draws a toggle; returns the (possibly flipped) value.
func (c *Ctx) Checkbox(x, y int32, label string, value bool) bool {
	const box = 16
	r := sdl.Rect{X: x, Y: y, W: box, H: box}
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	if value {
		inner := sdl.Rect{X: x + 4, Y: y + 4, W: box - 8, H: box - 8}
		c.Fill(inner, ColAccent)
	}
	w := c.Label(x+box+6, y, label, ColText)
	hit := sdl.Rect{X: x, Y: y, W: box + 6 + w, H: box}
	if c.hovering(hit) && c.clicked {
		return !value
	}
	return value
}

// TextField edits value in place; id keys focus. Returns (newValue,
// enterPressed).
func (c *Ctx) TextField(id string, r sdl.Rect, value string, placeholder string) (string, bool) {
	hover := c.hovering(r)
	if c.clicked {
		if hover {
			c.focusID = id
		} else if c.focusID == id {
			c.focusID = ""
		}
	}
	focused := c.focusID == id

	c.Fill(r, ColPanel)
	border := ColTextDim
	if focused {
		border = ColAccent
	}
	c.Border(r, border)

	enter := false
	if focused {
		if c.typed != "" {
			value += c.typed
		}
		if c.backspace && len(value) > 0 {
			runes := []rune(value)
			value = string(runes[:len(runes)-1])
		}
		if c.enter {
			enter = true
		}
	}

	const padX = 6
	show := value
	col := ColText
	if show == "" && !focused {
		show = placeholder
		col = ColTextDim
	}
	textY := r.Y + (r.H-int32(c.font.Height()))/2
	c.LabelClipped(r.X+padX, textY, r.W-2*padX, show, col)
	if focused && c.caretOn {
		caretX := r.X + padX + c.TextWidth(value)
		if caretX > r.X+r.W-padX {
			caretX = r.X + r.W - padX
		}
		c.Fill(sdl.Rect{X: caretX, Y: r.Y + 4, W: 2, H: r.H - 8}, ColText)
	}
	return value, enter
}

// HoverPreview tracks dwell time on a widget id; returns true when the
// full-size preview should show: 3 s hover, or right-click toggles instantly.
func (c *Ctx) HoverPreview(id string, r sdl.Rect) bool {
	if !c.hovering(r) {
		if c.hoverID == id {
			c.hoverID = ""
		}
		return false
	}
	if c.rightClicked {
		return true
	}
	if c.hoverID != id {
		c.hoverID = id
		c.hoverSince = time.Now()
		return false
	}
	return time.Since(c.hoverSince) >= HoverPreviewDelay
}

// Destroy frees cached textures and fonts.
func (c *Ctx) Destroy() {
	c.purgeTextCache()
	if c.font != nil {
		c.font.Close()
	}
	if c.fontBig != nil {
		c.fontBig.Close()
	}
}
