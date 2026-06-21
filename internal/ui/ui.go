// Package ui is a small immediate-mode widget kit over SDL2 plus the
// client's screens (lobby, character select, courtroom chrome, settings,
// about). Render-thread only.
package ui

import (
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// UIFontSize is the default chrome font size.
	UIFontSize = 14
	// UIFontSizeBig is for headings.
	UIFontSizeBig = 22

	// textCacheMax bounds the rendered-label texture cache; past it the
	// cache purges wholesale. Sized ABOVE the worst case visible at once:
	// a 4K char-select grid draws ~600 cells × (name + initials) ≈ 1200
	// distinct labels per frame — at the old cap of 512 the cache purged
	// and re-rasterized every label every frame, a hidden TTF storm. 2048
	// label textures ≈ 12 MiB worst case, and the purge becomes what it
	// was meant to be: cheap and rare (screen switches).
	textCacheMax = 2048

	scrollStepPx = 32
	caretBlink   = 500 * time.Millisecond
	// doubleClickWindow/Slop define a double-click: a second left release within
	// the window AND within a few logical px of the first.
	doubleClickWindow = 400 * time.Millisecond
	doubleClickSlop   = 6

	// DefaultScalePct is the 1:1 text/layout scale (percent).
	DefaultScalePct = 100

	// scrollThumbMinPx keeps the scrollbar thumb grabbable on long lists.
	scrollThumbMinPx = 24
	// scrollGrabSlopPx widens the scrollbar's hit zone beyond its drawn
	// width so drags don't drop when the cursor drifts.
	scrollGrabSlopPx = 6
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
	// Wardrobe/background folder icons (a distinct blue-grey "folder" tone,
	// brighter on hover) — not theme-overridden, so kept out of defaultKitColors.
	ColFolder   = sdl.Color{R: 60, G: 72, B: 98, A: 255}
	ColFolderHi = sdl.Color{R: 80, G: 98, B: 132, A: 255}
)

// defaultKitColors snapshots the stock palette above so a theme switch (or
// picking "none") restores it exactly — themes override, never mutate-forever.
var defaultKitColors = [...]sdl.Color{
	ColBackground, ColPanel, ColPanelHi, ColAccent, ColText, ColTextDim, ColDanger,
}

// Palette derivation ratios for slots QSS doesn't define directly: the
// window background sits a step darker than panels and dim text a step
// darker than text, preserving the kit's depth cues under theme colors.
const (
	paletteBackgroundPct = 70  // background = panel × 70%
	paletteRaisePct      = 130 // derived PanelHi = panel × 130%
	paletteDimPct        = 64  // dim text = text × 64%
)

// scaleColor multiplies a color's channels by pct/100, clamped.
func scaleColor(c sdl.Color, pct int) sdl.Color {
	mul := func(v uint8) uint8 {
		n := int(v) * pct / 100
		if n > 255 {
			n = 255
		}
		return uint8(n)
	}
	return sdl.Color{R: mul(c.R), G: mul(c.G), B: mul(c.B), A: c.A}
}

// paletteDarkText is the dark ink the readability floor falls back to on
// LIGHT theme panels (light panels with unreadable sheet text).
var paletteDarkText = sdl.Color{R: 20, G: 20, B: 24, A: 255}

// paletteLightPanelLuma splits "dark panel → light text" from "light
// panel → dark text" in the readability floor.
const paletteLightPanelLuma = 128

// applyThemePalette restores the stock kit palette, then lays the theme's
// courtroom_stylesheets.css colors over it. Slots the stylesheet doesn't
// define keep stock values or derive from defined ones (background from
// panel, dim text from text) so partial stylesheets stay coherent.
func applyThemePalette(p theme.Palette) {
	ColBackground, ColPanel, ColPanelHi, ColAccent, ColText, ColTextDim, ColDanger =
		defaultKitColors[0], defaultKitColors[1], defaultKitColors[2],
		defaultKitColors[3], defaultKitColors[4], defaultKitColors[5],
		defaultKitColors[6]
	if p.Empty() {
		return
	}
	if p.Panel != nil {
		ColPanel = sdl.Color{R: p.Panel.R, G: p.Panel.G, B: p.Panel.B, A: 255}
		ColBackground = scaleColor(ColPanel, paletteBackgroundPct)
		// Raised surfaces brighten the panel unless the sheet styles
		// buttons itself.
		ColPanelHi = scaleColor(ColPanel, paletteRaisePct)
	}
	if p.PanelHi != nil {
		ColPanelHi = sdl.Color{R: p.PanelHi.R, G: p.PanelHi.G, B: p.PanelHi.B, A: 255}
	}
	if p.Text != nil {
		ColText = sdl.Color{R: p.Text.R, G: p.Text.G, B: p.Text.B, A: 255}
		ColTextDim = scaleColor(ColText, paletteDimPct)
	}
	if p.Accent != nil {
		ColAccent = sdl.Color{R: p.Accent.R, G: p.Accent.G, B: p.Accent.B, A: 255}
	}
	if p.Danger != nil {
		ColDanger = sdl.Color{R: p.Danger.R, G: p.Danger.G, B: p.Danger.B, A: 255}
	}
	// Readability floor — the chatbox ink guard's rule, applied to the
	// whole kit (playtest: GrayGarden rendered Settings black-on-black).
	// Labels draw on panels and raised surfaces, so text must clear the
	// contrast floor against BOTH; when the sheet's combination fails,
	// re-derive ink from the panel's lightness instead of trusting it.
	textL := colLuma(ColText)
	if absInt(textL-colLuma(ColPanel)) < minInkSkinContrast ||
		absInt(textL-colLuma(ColPanelHi)) < minInkSkinContrast {
		if colLuma(ColPanel) < paletteLightPanelLuma {
			ColText = defaultKitColors[4] // stock light ink on dark panels
		} else {
			ColText = paletteDarkText
		}
		ColTextDim = scaleColor(ColText, paletteDimPct)
	}
}

// textKey keys the label cache by font identity (pointer), so scaled
// fonts coexist with the chrome fonts in one cache.
type textKey struct {
	text  string
	color sdl.Color
	font  *ttf.Font
}

type cachedText struct {
	tex  *sdl.Texture
	src  sdl.Rect // sub-rect inside tex (the atlas page or a dedicated texture)
	w, h int32
	// owned marks a dedicated (non-atlas) texture the purge must destroy
	// individually — labels too big for a shelf.
	owned bool
}

// Label atlas (§perf texture atlas): labels pack into shared pages so a
// text-heavy frame (the 4K char grid draws ~1200 distinct labels) costs a
// handful of texture binds instead of one per label.
const (
	// textAtlasPageEdge / textAtlasMaxPages bound the atlas: four 1024²
	// ABGR pages ≈ 16 MiB worst case, holding the documented label storm
	// whole. Beyond that, labels fall back to dedicated textures.
	textAtlasPageEdge = 1024
	textAtlasMaxPages = 4
	// textAtlasPad keeps one transparent pixel between packed labels so
	// linear filtering never bleeds neighbors.
	textAtlasPad = 1
)

// shelfPacker allocates left-to-right shelves top-down inside one square
// page. Pure math — unit-tested without SDL.
type shelfPacker struct {
	edge   int32
	penX   int32
	shelfY int32
	shelfH int32
}

// take reserves a w×h slot, opening a new shelf when the current row is
// full. ok=false when the page cannot fit it.
func (p *shelfPacker) take(w, h int32) (sdl.Rect, bool) {
	if w > p.edge || h > p.edge {
		return sdl.Rect{}, false
	}
	w += textAtlasPad
	h += textAtlasPad
	if p.penX+w > p.edge || h > p.shelfH {
		// Open the next shelf tall enough for this label.
		newY := p.shelfY + p.shelfH
		if newY+h > p.edge {
			return sdl.Rect{}, false
		}
		p.shelfY, p.shelfH, p.penX = newY, h, 0
	}
	r := sdl.Rect{X: p.penX, Y: p.shelfY, W: w - textAtlasPad, H: h - textAtlasPad}
	p.penX += w
	return r, true
}

// atlasPage is one shared label texture plus its packer state.
type atlasPage struct {
	shelfPacker
	tex *sdl.Texture
}

// Ctx is the per-frame UI context: input snapshot, fonts, text cache, focus.
type Ctx struct {
	Ren *sdl.Renderer
	// win backs FlashWindow (taskbar attention on modcalls/case alerts);
	// nil in headless tests — flashing is best-effort everywhere.
	win *sdl.Window

	font    *ttf.Font
	fontBig *ttf.Font

	// User-scaled font sets (chat box, log/OOC lists): the user's
	// override chain (CJK fallback) plus the embedded last resort,
	// rebuilt only when the percent or the chain changes — settings
	// actions, never per frame.
	chatSet fontSet
	logSet  fontSet
	// fontChain holds the override TTF/TTC bytes in chain order
	// (≤ fontChainCap). Bytes are read OFF-thread (App pipeline); fonts
	// build here from memory. The slices must outlive their fonts —
	// SDL's RWFromMem points straight at them.
	fontChain    [][]byte
	fontNames    []string // diagnostics (settings status line)
	fontChainGen int      // bumped per SetFontChain; sets rebuild lazily
	// pickMemo caches per-line font picks (log rows re-pick every frame;
	// GlyphMetrics per rune per row would be a hidden TTF storm whenever
	// an override chain is installed). Cleared on any font rebuild.
	pickMemo map[string]*ttf.Font
	// Color-emoji fallback face (e.g. Segoe UI Emoji), kept SEPARATE from the
	// chain so the common single-font fast path (pickCached len==1) is unchanged.
	// emojiData is the font file bytes (read off-thread by the App; nil = none/not
	// loaded); emojiFonts builds faces per size lazily. RWFromMem aliases the bytes,
	// so emojiData must outlive the faces.
	emojiData  []byte
	emojiFonts map[int]*ttf.Font

	// uiPct is the global render scale percent; mouse coordinates
	// unproject through it so logical hit-tests stay exact.
	uiPct int32

	mouseX, mouseY int32
	downX, downY   int32     // left-press origin (logical px); ClickedIn gates on it
	clicked        bool      // left released this frame
	dblClick       bool      // a double-click landed this frame (doubleClickWindow)
	lastClickAt    time.Time // double-click detection state (persists across frames)
	lastClickX     int32
	lastClickY     int32
	ctrlHeld       bool // live modifier state (Ctrl+wheel font sizing)
	rightClicked   bool
	wheelY         int32
	typed          string
	backspace      bool
	enter          bool
	tabPressed     bool
	escPressed     bool
	fullscreenReq  bool        // F11: toggle fullscreen this frame (consumed in app.Frame)
	keyPressed     sdl.Keycode // plain (non-ctrl) keydown this frame (0 = none)
	pasted         string      // Ctrl+V clipboard text (flattened to one line)
	copyReq        bool        // Ctrl+C: focused field copies its value
	cutReq         bool        // Ctrl+X: focused field copies, then clears
	selectAll      bool        // Ctrl+A armed: next edit replaces the whole value
	// wheelTaken marks this frame's wheel as consumed by a hovered widget
	// (spinbox rows, WheelIn lists) so page-level scrolls don't double-act.
	wheelTaken bool
	mouseDown  bool        // left button currently held (event-tracked)
	middleHeld bool        // middle (wheel) button held — fast log zoom modifier (event-tracked)
	dragID     string      // widget owning the active drag ("" = none)
	dropped    string      // SDL_DROPFILE path this frame ("" = none)
	hotkey     sdl.Keycode // non-clipboard Ctrl chord this frame (0 = none)
	tipText    string      // hover hint to paint at end-of-frame ("" = none)

	focusID    string
	caretOn    bool
	caret      int    // caret position (RUNE index) in the focused field's value
	caretField string // which field c.caret belongs to ("" = none); focus change resets it
	caretAcc   time.Duration
	// Hold-to-clear: holding holdKey (stamped per frame from prefs) for
	// holdThreshold wipes the focused field. holdAcc accumulates while it's
	// physically held (SDL live state, so a missed KEYUP can't strand it).
	holdOn        bool
	holdKey       sdl.Keycode
	holdThreshold time.Duration
	holdAcc       time.Duration
	holdFired     bool

	// Tab focus cycling (playtest: "focus on ic, press tab, it goes to
	// ooc"): TextField records draw order here each frame; the next
	// BeginFrame moves focus along it. Bounded by fields drawn per frame.
	fieldSeq []string
	tabShift bool // shift held at the Tab press → cycle backwards
	// focusNext is a queued FocusField request, applied at the next
	// BeginFrame so it survives this frame's click-away unfocus no matter
	// where the requesting widget sits in draw order.
	focusNext string

	// Dropdown state: one dropdown may be open; while open it owns the
	// pointer (modal capture) and its list paints in FinishFrame so it
	// stacks above widgets drawn later. modalOn persists across frames —
	// widgets drawn BEFORE the dropdown in draw order are fenced too —
	// and releases at the BeginFrame after the close.
	ddOpen       string
	ddScroll     int32
	modalOn      bool
	modalRect    sdl.Rect
	modalRelease bool
	ddDraws      []ddDraw // deferred overlay draws this frame (0 or 1)

	// Pointer fence (fencePointer/unfencePointer): the non-blocking floating
	// panel runs the courtroom pass pointer-blind while the cursor sits over it,
	// then restores the stashed state for its own pass.
	ptrFenced               bool
	fMouseX, fMouseY        int32
	fClicked, fRight, fDown bool
	fWheel                  int32

	// hover preview tracking
	hoverID    string
	hoverSince time.Time
	// Delayed-tooltip dwell (TooltipAfter): the pointer must rest on a target
	// for tooltipDwell before its hint shows. Separate from hoverID so the
	// sprite-preview dwell and tooltip dwell never clobber each other.
	tipHoverID    string
	tipHoverSince time.Time
	// hover-preview config, stamped once per frame from prefs (App.Frame →
	// SetHoverPreview): whether previews are enabled, and the dwell before one
	// shows. Keeping them on Ctx keeps HoverPreview a pure read on the hot path.
	hoverPreviewOn    bool
	hoverPreviewDelay time.Duration

	textCache  map[textKey]cachedText
	atlas      []*atlasPage     // shared label pages (≤ textAtlasMaxPages)
	widthCache map[string]int32 // chrome-font TextWidth memo

	// cgo-call scratch rects (the Viewport trick): taking the address of
	// a stack rect for Ren.Copy forces a heap escape per draw — at ~1200
	// labels per grid frame that's real garbage. These live here instead.
	drawSrc, drawDst sdl.Rect
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
		Ren:        ren,
		font:       font,
		fontBig:    fontBig,
		textCache:  map[textKey]cachedText{},
		widthCache: map[string]int32{},
	}, nil
}

// SetWindow attaches the SDL window for attention requests (FlashWindow).
func (c *Ctx) SetWindow(win *sdl.Window) { c.win = win }

// FlashWindow requests user attention until the window regains focus —
// AO2-Client's QApplication::alert on modcalls/case announcements.
func (c *Ctx) FlashWindow() {
	if c.win != nil {
		_ = c.win.Flash(sdl.FLASH_UNTIL_FOCUSED)
	}
}

// WindowDisplayUsable returns the usable size (work area, minus the taskbar) of
// the display the window currently sits on; (0,0) if unknown (headless). The
// caller clamps a requested size into this (config.ClampWindowSize). Render
// thread only.
func (c *Ctx) WindowDisplayUsable() (int, int) {
	if c.win == nil {
		return 0, 0
	}
	di, err := c.win.GetDisplayIndex()
	if err != nil {
		di = 0
	}
	r, err := sdl.GetDisplayUsableBounds(di)
	if err != nil {
		return 0, 0
	}
	return int(r.W), int(r.H)
}

// WindowSize reports the current window size (0,0 headless). Render thread only.
func (c *Ctx) WindowSize() (int, int) {
	if c.win == nil {
		return 0, 0
	}
	w, h := c.win.GetSize()
	return int(w), int(h)
}

// ResizeWindow sets the windowed size and recenters it on its display, so a
// too-big or off-screen window snaps back into view. No-op headless; the caller
// clamps the size first. Render thread only.
func (c *Ctx) ResizeWindow(w, h int) {
	if c.win == nil {
		return
	}
	c.win.SetSize(int32(w), int32(h))
	c.win.SetPosition(sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED)
}

// SetWindowFullscreen toggles borderless desktop fullscreen (no display-mode
// change — safe on every monitor). Render thread only.
func (c *Ctx) SetWindowFullscreen(on bool) {
	if c.win == nil {
		return
	}
	var flags uint32
	if on {
		flags = sdl.WINDOW_FULLSCREEN_DESKTOP
	}
	_ = c.win.SetFullscreen(flags)
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

// fontSet is one scaled font chain: override fonts in order, the
// embedded font as the guaranteed last entry.
type fontSet struct {
	fonts []*ttf.Font
	pct   int
	gen   int
}

// fontChainCap bounds the override chain (a primary plus a few CJK
// fallbacks is the realistic maximum).
const fontChainCap = 4

// ChatFont returns the IC message box PRIMARY font at pct percent of the
// chrome size (metrics/wrap baseline); ChatFontFor picks per text.
func (c *Ctx) ChatFont(pct int) *ttf.Font {
	return c.fontsFor(&c.chatSet, pct)[0]
}

// ChatFontFor picks the first chain font that covers every rune of text
// (the CJK fallback rule), the embedded font as last resort.
func (c *Ctx) ChatFontFor(pct int, text string) *ttf.Font {
	return c.pickCached(c.fontsFor(&c.chatSet, pct), text)
}

// LogFont returns the log/OOC list PRIMARY font at pct percent.
func (c *Ctx) LogFont(pct int) *ttf.Font {
	return c.fontsFor(&c.logSet, pct)[0]
}

// LogFontFor picks the covering chain font for one log line.
func (c *Ctx) LogFontFor(pct int, text string) *ttf.Font {
	return c.pickCached(c.fontsFor(&c.logSet, pct), text)
}

// pickCached memoizes pickFont per line: repeat draws cost one map probe.
// The no-override case (single-font set) costs nothing at all.
func (c *Ctx) pickCached(fonts []*ttf.Font, text string) *ttf.Font {
	if len(fonts) == 1 {
		return fonts[0]
	}
	if f, ok := c.pickMemo[text]; ok {
		return f
	}
	f := pickFont(fonts, text)
	if c.pickMemo == nil {
		c.pickMemo = make(map[string]*ttf.Font, 256)
	} else if len(c.pickMemo) >= textCacheMax {
		clear(c.pickMemo) // bounded: wholesale reset, repopulates hot lines
	}
	c.pickMemo[text] = f
	return f
}

// SetFontChain installs the override font bytes (chain order; nil
// clears). Render thread; the byte slices were read off-thread and must
// stay referenced for the fonts' lifetime (RWFromMem aliases them).
func (c *Ctx) SetFontChain(names []string, data [][]byte) {
	if len(data) > fontChainCap {
		names, data = names[:fontChainCap], data[:fontChainCap]
	}
	c.fontNames, c.fontChain = names, data
	c.fontChainGen++
}

// FontChainNames reports the loaded override names (settings status).
func (c *Ctx) FontChainNames() []string { return c.fontNames }

// fontsFor returns the set's fonts, rebuilding when the scale or the
// chain moved (settings actions — never per frame).
func (c *Ctx) fontsFor(s *fontSet, pct int) []*ttf.Font {
	if len(s.fonts) > 0 && s.pct == pct && s.gen == c.fontChainGen {
		return s.fonts
	}
	for _, f := range s.fonts {
		if f != c.font {
			f.Close()
		}
	}
	// Stale-font cache entries would never be hit again (keys carry the
	// font pointer); purge wholesale — rebuilds are user actions. The
	// pick memo holds those same dead pointers, so it resets too.
	c.purgeTextCache()
	c.pickMemo = nil
	s.fonts = s.fonts[:0]
	s.pct, s.gen = pct, c.fontChainGen
	size := UIFontSize * pct / DefaultScalePct
	if size < 1 {
		size = 1
	}
	for _, data := range c.fontChain {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
		}
	}
	// Embedded last resort; share the chrome font at 1:1 (no duplicate
	// rasters for the common case).
	if pct == DefaultScalePct || c.font == nil {
		s.fonts = append(s.fonts, c.font)
	} else if f, err := loadEmbeddedFont(size); err == nil {
		s.fonts = append(s.fonts, f)
	} else {
		s.fonts = append(s.fonts, c.font)
	}
	return s.fonts
}

// memFont opens a TTF/TTC from bytes the caller keeps alive.
func memFont(data []byte, size int) (*ttf.Font, error) {
	rw, err := sdl.RWFromMem(data)
	if err != nil {
		return nil, err
	}
	return ttf.OpenFontRW(rw, 1, size)
}

// SetEmojiFont installs the color-emoji fallback face bytes (read off-thread by
// the App; nil clears). The bytes must stay referenced for the faces' lifetime
// (RWFromMem aliases them). Drops any built faces so they rebuild. Render thread.
func (c *Ctx) SetEmojiFont(data []byte) {
	for _, f := range c.emojiFonts {
		if f != nil {
			f.Close()
		}
	}
	c.emojiFonts = nil
	c.emojiData = data
}

// EmojiFont returns the color-emoji fallback face at pct percent (the chat/log
// scale), built lazily and cached per size, or nil when no face is loaded (then a
// mixed message renders emoji as the chat font's tofu — today's behavior). Sized
// to match the text font so baselines line up after RasterizeFallback's per-run
// offset. A failed build caches nil so it isn't retried every message.
func (c *Ctx) EmojiFont(pct int) *ttf.Font {
	if c.emojiData == nil {
		return nil
	}
	size := UIFontSize * pct / DefaultScalePct
	if size < 1 {
		size = 1
	}
	if f, ok := c.emojiFonts[size]; ok {
		return f
	}
	if c.emojiFonts == nil {
		c.emojiFonts = make(map[int]*ttf.Font, 4)
	}
	f, err := memFont(c.emojiData, size)
	if err != nil {
		c.emojiFonts[size] = nil // remember the failure; don't retry per message
		return nil
	}
	c.emojiFonts[size] = f
	return f
}

// pickFont returns the first font covering every rune of text — mixed
// Latin+CJK lines resolve to the first CJK-capable entry (CJK fonts
// cover Latin). The last entry is the unconditional fallback.
func pickFont(fonts []*ttf.Font, text string) *ttf.Font {
	if len(fonts) == 1 {
		return fonts[0]
	}
	for _, f := range fonts[:len(fonts)-1] {
		if fontCovers(f, text) {
			return f
		}
	}
	return fonts[len(fonts)-1]
}

// fontCovers reports whether f provides a glyph for every rune.
// GlyphMetrics errors exactly on missing glyphs; SDL_ttf's metrics are
// BMP-only, so supplementary-plane runes count as uncovered.
func fontCovers(f *ttf.Font, text string) bool {
	if f == nil {
		return false
	}
	for _, r := range text {
		if r > 0xFFFF {
			return false
		}
		if _, err := f.GlyphMetrics(r); err != nil {
			return false
		}
	}
	return true
}

// BeginFrame opens a frame: it clears the input snapshot, so it must run
// BEFORE this frame's events are fed in via HandleEvent. The GetMouseState
// seed predates the frame's event pump; mouse events override it as they
// arrive.
func (c *Ctx) BeginFrame(dt time.Duration) {
	// Tab focus cycling runs on the PREVIOUS frame's field order — by now
	// the draw pass that followed the keypress has recorded every visible
	// TextField (one frame of latency, imperceptible at frame rate).
	if c.tabPressed && len(c.fieldSeq) > 0 {
		c.focusID = cycleField(c.fieldSeq, c.focusID, c.tabShift)
		c.caretOn = true // land visible: the caret shows where focus went
		c.caretAcc = 0
	}
	c.fieldSeq = c.fieldSeq[:0]
	if c.focusNext != "" {
		c.focusID = c.focusNext
		c.focusNext = ""
		c.caretOn = true
		c.caretAcc = 0
	}
	// A dropdown closed last frame: the modal fence held through that
	// whole frame (so the closing click reached nothing underneath) and
	// lifts now.
	if c.modalRelease {
		c.modalOn = false
		c.modalRelease = false
	}
	c.ddDraws = c.ddDraws[:0]

	c.clicked = false
	c.dblClick = false
	c.rightClicked = false
	c.wheelY = 0
	c.wheelTaken = false
	c.typed = ""
	c.backspace = false
	c.enter = false
	c.tabPressed = false
	c.escPressed = false
	c.fullscreenReq = false
	c.keyPressed = 0
	c.pasted = ""
	c.copyReq = false
	c.cutReq = false
	c.dropped = ""
	c.hotkey = 0
	c.tipText = ""
	x, y, _ := sdl.GetMouseState()
	c.mouseX, c.mouseY = c.toLogical(x), c.toLogical(y)
	c.ctrlHeld = sdl.GetModState()&sdl.KMOD_CTRL != 0
	if !c.mouseDown {
		c.dragID = "" // drags end with the button release
	}

	c.caretAcc += dt
	if c.caretAcc >= caretBlink {
		c.caretAcc = 0
		c.caretOn = !c.caretOn
	}
	// Hold-to-clear timer: accumulate while the bound key is physically held,
	// reset (and re-arm) the moment it's released. holdKey/holdOn were stamped
	// last frame by App.Frame — one frame stale is fine.
	if c.holdOn && c.keyHeld(c.holdKey) {
		c.holdAcc += dt
	} else {
		c.holdAcc = 0
		c.holdFired = false
	}
}

// HandleEvent feeds one SDL event into the frame's input snapshot.
func (c *Ctx) HandleEvent(ev sdl.Event) {
	switch e := ev.(type) {
	case *sdl.MouseMotionEvent:
		c.mouseX, c.mouseY = c.toLogical(e.X), c.toLogical(e.Y)
	case *sdl.MouseButtonEvent:
		c.mouseX, c.mouseY = c.toLogical(e.X), c.toLogical(e.Y)
		switch e.Type {
		case sdl.MOUSEBUTTONDOWN:
			switch e.Button {
			case sdl.BUTTON_LEFT:
				c.mouseDown = true
				c.downX, c.downY = c.mouseX, c.mouseY // press origin (set above) for ClickedIn gating
			case sdl.BUTTON_MIDDLE:
				c.middleHeld = true // held = fast log-zoom modifier
			}
		case sdl.MOUSEBUTTONUP:
			switch e.Button {
			case sdl.BUTTON_LEFT:
				c.clicked = true
				c.mouseDown = false
				now := time.Now()
				dx, dy := c.mouseX-c.lastClickX, c.mouseY-c.lastClickY
				if dx < 0 {
					dx = -dx
				}
				if dy < 0 {
					dy = -dy
				}
				if now.Sub(c.lastClickAt) < doubleClickWindow && dx < doubleClickSlop && dy < doubleClickSlop {
					c.dblClick = true
				}
				c.lastClickAt, c.lastClickX, c.lastClickY = now, c.mouseX, c.mouseY
			case sdl.BUTTON_RIGHT:
				c.rightClicked = true
			case sdl.BUTTON_MIDDLE:
				c.middleHeld = false
			}
		}
	case *sdl.MouseWheelEvent:
		c.wheelY += e.Y
	case *sdl.TextInputEvent:
		c.typed += e.GetText()
	case *sdl.KeyboardEvent:
		if e.Type != sdl.KEYDOWN {
			return
		}
		if e.Keysym.Mod&sdl.KMOD_CTRL != 0 {
			// Clipboard chords. SDL filters control chords out of
			// TEXTINPUT, so there is no double insert. GetClipboardText is
			// an SDL call — legal here, HandleEvent runs render-thread.
			switch e.Keysym.Sym {
			case sdl.K_v:
				if text, err := sdl.GetClipboardText(); err == nil && text != "" {
					c.pasted += flattenClipboard(text)
				}
			case sdl.K_c:
				c.copyReq = true
			case sdl.K_x:
				// Cut only makes sense in a focused field; with nothing focused
				// let Ctrl+X fall through to the configurable hotkeys (else the
				// Extras toggle bound to "x" is dead — clipboard ate it).
				if c.focusID != "" {
					c.cutReq = true
				} else {
					c.hotkey = e.Keysym.Sym
				}
			case sdl.K_a:
				// Arm select-all on the focused field: the next typed or
				// pasted text replaces the whole value, backspace clears
				// it, Ctrl+C/X already act on the full value.
				c.selectAll = true
			default:
				// Everything else is the app's: configurable Ctrl-chord
				// hotkeys (shouts, pos cycle, screenshot, ...).
				c.hotkey = e.Keysym.Sym
			}
			return
		}
		switch e.Keysym.Sym {
		case sdl.K_BACKSPACE:
			c.backspace = true
		case sdl.K_RETURN, sdl.K_KP_ENTER:
			c.enter = true
		case sdl.K_TAB:
			c.tabPressed = true
			c.tabShift = e.Keysym.Mod&sdl.KMOD_SHIFT != 0
		case sdl.K_ESCAPE:
			c.escPressed = true
		case sdl.K_F11:
			c.fullscreenReq = true // toggle fullscreen — the keyboard escape from a too-big window
		default:
			// Plain keys feed the character keybinds (and the wardrobe's
			// bind-capture); consumers check focus before acting.
			c.keyPressed = e.Keysym.Sym
		}
	case *sdl.DropEvent:
		if e.Type == sdl.DROPFILE {
			c.dropped = e.File // consumed by the visible screen this frame
		}
	}
}

// flattenClipboard makes pasted text safe for the kit's single-line fields.
func flattenClipboard(s string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(s)
}

// SetUIScale stores the global render scale percent for mouse
// unprojection (main sets the matching renderer scale each frame).
func (c *Ctx) SetUIScale(pct int) {
	if pct <= 0 {
		pct = DefaultScalePct
	}
	c.uiPct = int32(pct)
}

// toLogical converts window pixels to logical (pre-scale) coordinates.
func (c *Ctx) toLogical(v int32) int32 {
	if c.uiPct == 0 || c.uiPct == DefaultScalePct {
		return v
	}
	return v * DefaultScalePct / c.uiPct
}

// hovering reports whether the mouse is inside r.
func (c *Ctx) hovering(r sdl.Rect) bool {
	// An open dropdown owns the pointer: everything outside its modal
	// rect reads as not-hovered, so clicks/hovers can't leak underneath.
	if c.modalOn && !(c.mouseX >= c.modalRect.X && c.mouseX < c.modalRect.X+c.modalRect.W &&
		c.mouseY >= c.modalRect.Y && c.mouseY < c.modalRect.Y+c.modalRect.H) {
		return false
	}
	return c.mouseX >= r.X && c.mouseX < r.X+r.W && c.mouseY >= r.Y && c.mouseY < r.Y+r.H
}

// ClickedIn reports a committed left-click on r: the button was both pressed AND
// released inside r. Plain `c.clicked && hovering(r)` fires on the RELEASE alone,
// so a gesture that began elsewhere — a scrollbar grab or panel move whose cursor
// drifts onto a row before the button comes up — triggers whatever it's released
// over. For navigational rows (area list / area-jump header / music track) that
// stray release sends an area transfer: "I only hovered and ended up in another
// area." Gating on the press origin (downX/downY) requires the click to start on
// the row, which a drag-in never does.
func (c *Ctx) ClickedIn(r sdl.Rect) bool {
	return c.clicked && c.hovering(r) &&
		c.downX >= r.X && c.downX < r.X+r.W && c.downY >= r.Y && c.downY < r.Y+r.H
}

// fencePointer blanks the live pointer state (cursor off-screen, no click / drag
// / wheel) after stashing it, so an immediate-mode pass draws as if the mouse
// were absent. unfencePointer restores it. The kit has no z-aware input, so a
// non-blocking floating panel hides the pointer from the courtroom pass while it
// sits underneath, then unfences for its own pass. Keyboard is untouched (you
// keep typing in chat with the panel up). Idempotent; restore is a direct method
// (no closure) so the deferred unfence on the courtroom path stays alloc-free.
func (c *Ctx) fencePointer() {
	if c.ptrFenced {
		return
	}
	c.ptrFenced = true
	c.fMouseX, c.fMouseY = c.mouseX, c.mouseY
	c.fClicked, c.fRight, c.fDown, c.fWheel = c.clicked, c.rightClicked, c.mouseDown, c.wheelY
	c.mouseX, c.mouseY = -1, -1
	c.clicked, c.rightClicked, c.mouseDown, c.wheelY = false, false, false, 0
}

// unfencePointer restores the pointer state stashed by fencePointer. No-op when
// not fenced, so it's safe to defer unconditionally.
func (c *Ctx) unfencePointer() {
	if !c.ptrFenced {
		return
	}
	c.ptrFenced = false
	c.mouseX, c.mouseY = c.fMouseX, c.fMouseY
	c.clicked, c.rightClicked, c.mouseDown, c.wheelY = c.fClicked, c.fRight, c.fDown, c.fWheel
}

// --- draw primitives -----------------------------------------------------------

// Fill draws a solid rect.
func (c *Ctx) Fill(r sdl.Rect, col sdl.Color) {
	_ = c.Ren.SetDrawColor(col.R, col.G, col.B, col.A)
	_ = c.Ren.FillRect(&r)
}

// gradientSteps bounds the horizontal strips a vertical gradient draws — smooth
// enough at any panel height, cheap enough off the render hot path (gradients
// are opt-in and only paint on an open panel).
const gradientSteps = int32(48)

// FillGradient paints r as a top→bottom vertical gradient from top to bottom.
func (c *Ctx) FillGradient(r sdl.Rect, top, bottom sdl.Color) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	n := gradientSteps
	if r.H < n {
		n = r.H // never more strips than pixels
	}
	lerp := func(a, b uint8, t int32) uint8 { return uint8(int32(a) + (int32(b)-int32(a))*t/(n-1)) }
	if n == 1 {
		c.Fill(r, top)
		return
	}
	for i := int32(0); i < n; i++ {
		y0, y1 := r.Y+i*r.H/n, r.Y+(i+1)*r.H/n
		c.Fill(sdl.Rect{X: r.X, Y: y0, W: r.W, H: y1 - y0},
			sdl.Color{R: lerp(top.R, bottom.R, i), G: lerp(top.G, bottom.G, i), B: lerp(top.B, bottom.B, i), A: 255})
	}
}

// Border outlines a rect.
func (c *Ctx) Border(r sdl.Rect, col sdl.Color) {
	_ = c.Ren.SetDrawColor(col.R, col.G, col.B, col.A)
	_ = c.Ren.DrawRect(&r)
}

// pushClip scissors rendering to r and returns the clip state to restore
// with popClip (the themed log lists draw inside element rects, so a blind
// SetClipRect(nil) reset could clobber an outer clip). Scrollable lists wrap
// their line loop in push/pop so a partially scrolled top/bottom row is cut
// at the rect edge instead of spilling over neighbouring widgets — that
// overspill is what struck the tab strip through the first OOC line.
//
// Returns by value (no closure) so the per-frame log/list draws stay
// allocation-free — the render loop must not heap-allocate.
func (c *Ctx) pushClip(r sdl.Rect) (prev sdl.Rect, had bool) {
	had = c.Ren.IsClipEnabled()
	prev = c.Ren.GetClipRect()
	_ = c.Ren.SetClipRect(&r)
	return prev, had
}

// popClip restores the clip captured by pushClip.
func (c *Ctx) popClip(prev sdl.Rect, had bool) {
	if had {
		_ = c.Ren.SetClipRect(&prev)
	} else {
		_ = c.Ren.SetClipRect(nil)
	}
}

// textTexture returns (and caches) a rendered label for the given font.
// Labels pack into shared atlas pages; oversized ones get a dedicated
// texture (the pre-atlas behavior).
func (c *Ctx) textTexture(text string, col sdl.Color, font *ttf.Font) (cachedText, bool) {
	if text == "" || font == nil {
		return cachedText{}, false
	}
	key := textKey{text: text, color: col, font: font}
	if t, ok := c.textCache[key]; ok {
		return t, true
	}
	surf, err := font.RenderUTF8Blended(text, col)
	if err != nil {
		return cachedText{}, false
	}
	defer surf.Free()
	if len(c.textCache) >= textCacheMax {
		c.purgeTextCache()
	}

	if tex, slot, ok := c.atlasPlace(surf); ok {
		entry := cachedText{tex: tex, src: slot, w: surf.W, h: surf.H}
		c.textCache[key] = entry
		return entry, true
	}

	// Atlas full or label oversized: dedicated texture fallback.
	tex, err := c.Ren.CreateTextureFromSurface(surf)
	if err != nil {
		return cachedText{}, false
	}
	entry := cachedText{tex: tex, src: sdl.Rect{W: surf.W, H: surf.H}, w: surf.W, h: surf.H, owned: true}
	c.textCache[key] = entry
	return entry, true
}

// atlasPlace uploads a label surface into a shared page, opening pages up
// to the cap. ok=false → caller uses a dedicated texture.
func (c *Ctx) atlasPlace(surf *sdl.Surface) (*sdl.Texture, sdl.Rect, bool) {
	// Texture.Update needs the page's pixel format; convert once here
	// (TTF blended output is ARGB8888 on SDL2 — usually a no-op check).
	up := surf
	if surf.Format.Format != uint32(sdl.PIXELFORMAT_ARGB8888) {
		conv, err := surf.ConvertFormat(uint32(sdl.PIXELFORMAT_ARGB8888), 0)
		if err != nil {
			return nil, sdl.Rect{}, false
		}
		defer conv.Free()
		up = conv
	}
	for {
		// Try the newest page first (older ones are mostly full).
		if n := len(c.atlas); n > 0 {
			page := c.atlas[n-1]
			if slot, ok := page.take(up.W, up.H); ok {
				if err := page.tex.Update(&slot, unsafe.Pointer(&up.Pixels()[0]), int(up.Pitch)); err != nil {
					return nil, sdl.Rect{}, false
				}
				return page.tex, slot, true
			}
		}
		if len(c.atlas) >= textAtlasMaxPages {
			return nil, sdl.Rect{}, false
		}
		tex, err := c.Ren.CreateTexture(uint32(sdl.PIXELFORMAT_ARGB8888),
			sdl.TEXTUREACCESS_STATIC, textAtlasPageEdge, textAtlasPageEdge)
		if err != nil {
			return nil, sdl.Rect{}, false
		}
		_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
		c.atlas = append(c.atlas, &atlasPage{
			shelfPacker: shelfPacker{edge: textAtlasPageEdge},
			tex:         tex,
		})
	}
}

func (c *Ctx) purgeTextCache() {
	for k, v := range c.textCache {
		if v.owned && v.tex != nil {
			_ = v.tex.Destroy()
		}
		delete(c.textCache, k)
	}
	for _, page := range c.atlas {
		if page.tex != nil {
			_ = page.tex.Destroy()
		}
	}
	c.atlas = c.atlas[:0]
}

// blitLabel copies a cached label (atlas sub-rect aware) through the
// scratch rects — zero heap escapes on the per-frame draw path.
func (c *Ctx) blitLabel(t cachedText, x, y, w int32) {
	c.drawSrc = sdl.Rect{X: t.src.X, Y: t.src.Y, W: w, H: t.h}
	c.drawDst = sdl.Rect{X: x, Y: y, W: w, H: t.h}
	_ = c.Ren.Copy(t.tex, &c.drawSrc, &c.drawDst)
}

// Label draws text at (x, y) and returns its pixel width.
func (c *Ctx) Label(x, y int32, text string, col sdl.Color) int32 {
	t, ok := c.textTexture(text, col, c.font)
	if !ok {
		return 0
	}
	c.blitLabel(t, x, y, t.w)
	return t.w
}

// Heading draws large text.
func (c *Ctx) Heading(x, y int32, text string, col sdl.Color) {
	t, ok := c.textTexture(text, col, c.fontBig)
	if !ok {
		return
	}
	c.blitLabel(t, x, y, t.w)
}

// LabelClipped draws text clipped to maxW.
func (c *Ctx) LabelClipped(x, y, maxW int32, text string, col sdl.Color) {
	c.LabelClippedFont(c.font, x, y, maxW, text, col)
}

// LabelClippedFont is LabelClipped with an explicit font (scaled log/OOC
// text). Cached like every label; the cache keys by font identity. The
// clip composes with the label's atlas sub-rect.
func (c *Ctx) LabelClippedFont(font *ttf.Font, x, y, maxW int32, text string, col sdl.Color) {
	t, ok := c.textTexture(text, col, font)
	if !ok {
		return
	}
	w := t.w
	if w > maxW {
		w = maxW
	}
	c.blitLabel(t, x, y, w)
}

// TextWidth measures a label in the chrome font, memoized — screens call
// it per frame for fixed labels and each miss is a CGO TTF measure. The
// memo shares the text cache's lifecycle (purged together, same bound).
func (c *Ctx) TextWidth(text string) int32 {
	if c.font == nil {
		return 0 // headless tests; real Ctx always has the chrome font
	}
	if w, ok := c.widthCache[text]; ok {
		return w
	}
	w, _, err := c.font.SizeUTF8(text)
	if err != nil {
		return 0
	}
	if len(c.widthCache) >= textCacheMax {
		c.widthCache = make(map[string]int32, textCacheMax)
	}
	c.widthCache[text] = int32(w)
	return int32(w)
}

// --- widgets ---------------------------------------------------------------------

// Button draws a clickable button; returns true on click.
func (c *Ctx) Button(r sdl.Rect, label string) bool {
	return c.ButtonCol(r, label, ColPanel, ColPanelHi, ColAccent, ColText)
}

// ButtonCol is Button in explicit colours (bg, hover bg, border, label) — so a
// themed panel can colour its own buttons. Same look, click semantics and cost
// as Button; Button is just ButtonCol in the stock palette.
func (c *Ctx) ButtonCol(r sdl.Rect, label string, bg, hoverBg, border, text sdl.Color) bool {
	hover := c.hovering(r)
	col := bg
	if hover {
		col = hoverBg
	}
	c.Fill(r, col)
	c.Border(r, border)
	if t, ok := c.textTexture(label, text, c.font); ok {
		// Clip to the button: tiny themed rects must never leak their
		// label over the neighbors (Qt elided these).
		w, x := t.w, r.X+(r.W-t.w)/2
		if maxW := r.W - 8; w > maxW && maxW > 0 {
			w, x = maxW, r.X+4
		}
		c.blitLabel(t, x, r.Y+(r.H-t.h)/2, w)
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

// FocusField queues keyboard focus onto a TextField id (e.g. the IC input
// after an emote pick — AO2-Client's focus_ic_input parity).
func (c *Ctx) FocusField(id string) { c.focusNext = id }

// WheelIn returns this frame's wheel ticks when the cursor is inside r,
// else 0 — scrollables only react under the pointer (playtest: the music
// list scrolled on wheel from anywhere on screen). A hit marks the wheel
// taken, fencing page-level scroll handlers.
func (c *Ctx) WheelIn(r sdl.Rect) int32 {
	if c.wheelY != 0 && c.hovering(r) {
		c.wheelTaken = true
		return c.wheelY
	}
	return 0
}

// cycleField picks the next focus target for Tab / Shift+Tab: the field
// after (or before) the current one in draw order, wrapping; when nothing
// is focused, the first field — standard toolkit behavior.
func cycleField(seq []string, cur string, back bool) string {
	for i, id := range seq {
		if id != cur {
			continue
		}
		if back {
			return seq[(i+len(seq)-1)%len(seq)]
		}
		return seq[(i+1)%len(seq)]
	}
	return seq[0]
}

// TextField edits value in place; id keys focus. Returns (newValue,
// enterPressed).
func (c *Ctx) TextField(id string, r sdl.Rect, value string, placeholder string) (string, bool) {
	return c.textField(id, r, value, placeholder, false)
}

// PasswordField is TextField rendered as asterisks (screenshare-safe).
// Ctrl+C/X never put the secret on the clipboard either — clipboard
// viewers are just as visible on a stream; Ctrl+X still clears.
func (c *Ctx) PasswordField(id string, r sdl.Rect, value string, placeholder string) (string, bool) {
	return c.textField(id, r, value, placeholder, true)
}

// editOp is a caret movement / forward-delete a text field applies this frame.
type editOp int

const (
	editNone editOp = iota
	editLeft
	editRight
	editHome
	editEnd
	editDelete
)

// editInput is one frame of edits to a focused text field.
type editInput struct {
	typed  string // inserted text (typed + pasted)
	back   bool   // backspace (delete the rune before the caret)
	op     editOp // caret move or forward delete
	selAll bool   // a pending select-all: an insert/delete replaces the whole value
}

// editStep applies one frame of edits to value at caret (a RUNE index), returning
// the new value and caret. Pure and rune-aware (multibyte shownames — Häschen,
// fünfzehn, 🍅 — so the caret is by rune, never by byte), so it carries all the
// edit logic that the draw path (which needs a renderer) can't unit-test.
func editStep(value string, caret int, in editInput) (string, int) {
	runes := []rune(value)
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	// A pending select-all: the next insert/delete replaces everything.
	if in.selAll && (in.typed != "" || in.back || in.op == editDelete) {
		if in.typed != "" {
			t := []rune(in.typed)
			return string(t), len(t)
		}
		return "", 0
	}
	switch {
	case in.typed != "":
		t := []rune(in.typed)
		out := make([]rune, 0, len(runes)+len(t))
		out = append(out, runes[:caret]...)
		out = append(out, t...)
		out = append(out, runes[caret:]...)
		return string(out), caret + len(t)
	case in.back && caret > 0:
		out := make([]rune, 0, len(runes)-1)
		out = append(out, runes[:caret-1]...)
		out = append(out, runes[caret:]...)
		return string(out), caret - 1
	case in.op == editDelete && caret < len(runes):
		out := make([]rune, 0, len(runes)-1)
		out = append(out, runes[:caret]...)
		out = append(out, runes[caret+1:]...)
		return string(out), caret
	case in.op == editLeft && caret > 0:
		caret--
	case in.op == editRight && caret < len(runes):
		caret++
	case in.op == editHome:
		caret = 0
	case in.op == editEnd:
		caret = len(runes)
	}
	return value, caret
}

func (c *Ctx) textField(id string, r sdl.Rect, value string, placeholder string, mask bool) (string, bool) {
	c.fieldSeq = append(c.fieldSeq, id) // Tab-cycle order = draw order
	hover := c.hovering(r)
	if c.clicked {
		c.selectAll = false // any single click drops a pending select-all
		if hover {
			c.focusID = id
		} else if c.focusID == id {
			c.focusID = ""
		}
	}
	if c.dblClick && hover { // double-click selects all the text (quick replace/clear)
		c.focusID = id
		c.selectAll = true
	}
	focused := c.focusID == id

	c.Fill(r, ColPanel)
	border := ColTextDim
	if focused {
		border = ColAccent
	}
	c.Border(r, border)

	const padX = 6
	avail := r.W - 2*padX
	// maskOf maps a value to what's drawn (one '*' per rune when masked). The
	// caret math measures the DISPLAY, so a password field never leaks its value.
	maskOf := func(v string) string {
		if mask && v != "" {
			return strings.Repeat("*", utf8.RuneCountInString(v))
		}
		return v
	}
	if !focused && c.caretField == id {
		c.caretField = "" // dropped focus → a future re-focus resets the caret to the end
	}

	enter := false
	if focused {
		// The caret is the focused field's. Default to the end on a focus change,
		// and CLAMP every frame — value can shrink underneath us (click-to-pair,
		// macros set a.icInput while it's focused), so a stale caret must never
		// index past the end (the live-crash the advisor flagged).
		rc := utf8.RuneCountInString(value)
		if c.caretField != id {
			c.caret = rc
			c.caretField = id
		}
		if c.caret > rc {
			c.caret = rc
		}
		if c.caret < 0 {
			c.caret = 0
		}
		// A click positions the caret (measured on the display, so masked maps 1:1).
		if c.clicked && hover {
			pre := maskOf(value)
			c.caret = c.caretIndexAtX(pre, c.mouseX-(r.X+padX)+c.fieldScroll(pre, c.caret, avail))
		}
		if c.copyReq && value != "" && !mask {
			_ = sdl.SetClipboardText(value)
		}
		switch {
		case c.holdOn && c.holdAcc >= c.holdThreshold && !c.holdFired && value != "":
			// Hold-to-clear: the bound key (default Backspace) held past the
			// threshold wipes the whole field at once — no slow char-by-char.
			c.holdFired = true
			value, c.caret = "", 0
		case c.cutReq:
			if value != "" && !mask {
				_ = sdl.SetClipboardText(value)
			}
			value, c.caret = "", 0
			c.selectAll = false
		default:
			in := editInput{typed: c.typed + c.pasted, back: c.backspace, selAll: c.selectAll}
			switch c.keyPressed {
			case sdl.K_LEFT:
				in.op = editLeft
			case sdl.K_RIGHT:
				in.op = editRight
			case sdl.K_HOME:
				in.op = editHome
			case sdl.K_END:
				in.op = editEnd
			case sdl.K_DELETE:
				in.op = editDelete
			}
			if in.typed != "" || in.back || in.op != editNone {
				value, c.caret = editStep(value, c.caret, in)
				c.selectAll = false   // any edit/nav drops the pending select-all
				switch c.keyPressed { // consume nav keys so char keybinds don't also fire
				case sdl.K_LEFT, sdl.K_RIGHT, sdl.K_HOME, sdl.K_END, sdl.K_DELETE:
					c.keyPressed = 0
				}
			}
		}
		if c.enter {
			enter = true
		}
	}

	display := maskOf(value)
	show := display
	col := ColText
	if show == "" && !focused {
		show = placeholder
		col = ColTextDim
	}
	textY := r.Y + (r.H-int32(c.font.Height()))/2
	// Horizontal scroll keeps the CARET visible (roughly centered) once the text
	// overflows the box — type or arrow anywhere and you can see it, instead of
	// typing blind past the right edge. Unfocused/fitting fields stay head-aligned.
	fullW := c.TextWidth(display)
	scroll, caretX := int32(0), int32(0)
	if focused {
		caretX = c.caretPixelX(display, c.caret)
		if fullW > avail && avail > 0 {
			scroll = caretX - avail/2
			if scroll < 0 {
				scroll = 0
			}
			if m := fullW - avail; scroll > m {
				scroll = m
			}
		}
	}
	if focused && c.selectAll && value != "" {
		selW := fullW - scroll // select-all highlight behind the visible text
		if selW > avail {
			selW = avail
		}
		if selW > 0 {
			c.Fill(sdl.Rect{X: r.X + padX, Y: r.Y + 3, W: selW, H: r.H - 6},
				sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 90})
		}
	}
	if scroll > 0 {
		// Clip to the field interior so the scrolled-off head doesn't spill left.
		cp, ch := c.pushClip(sdl.Rect{X: r.X + padX, Y: r.Y, W: avail, H: r.H})
		c.Label(r.X+padX-scroll, textY, show, col)
		c.popClip(cp, ch)
	} else {
		c.LabelClipped(r.X+padX, textY, avail, show, col)
	}
	if focused && c.caretOn {
		c.Fill(sdl.Rect{X: r.X + padX + caretX - scroll, Y: r.Y + 4, W: 2, H: r.H - 8}, ColText)
	}
	return value, enter
}

// caretPixelX is the x-pixel offset of the caret (a rune index) within display.
func (c *Ctx) caretPixelX(display string, caret int) int32 {
	if caret <= 0 || display == "" {
		return 0
	}
	runes := []rune(display)
	if caret > len(runes) {
		caret = len(runes)
	}
	return c.TextWidth(string(runes[:caret]))
}

// caretIndexAtX returns the rune index nearest pixel relX (a click position
// relative to the text's left edge), measured on the display string — so a
// click lands the caret between the characters under the cursor.
func (c *Ctx) caretIndexAtX(display string, relX int32) int {
	if relX <= 0 || display == "" {
		return 0
	}
	runes := []rune(display)
	prevW := int32(0)
	for i := 1; i <= len(runes); i++ {
		w := c.TextWidth(string(runes[:i]))
		if relX < (prevW+w)/2 {
			return i - 1
		}
		prevW = w
	}
	return len(runes)
}

// fieldScroll is the horizontal pixel scroll that keeps the caret visible in a
// field of interior width avail — caret roughly centered once the text overflows.
// Stateless (deterministic per caret), so it never jitters frame to frame.
func (c *Ctx) fieldScroll(display string, caret int, avail int32) int32 {
	full := c.TextWidth(display)
	if full <= avail || avail <= 0 {
		return 0
	}
	scroll := c.caretPixelX(display, caret) - avail/2
	if scroll < 0 {
		scroll = 0
	}
	if m := full - avail; scroll > m {
		scroll = m
	}
	return scroll
}

// SetHoldClear stamps the hold-to-clear config for the frame (App resolves it
// from prefs). The accumulation runs in BeginFrame; the focused field clears.
func (c *Ctx) SetHoldClear(on bool, key sdl.Keycode, threshold time.Duration) {
	c.holdOn, c.holdKey, c.holdThreshold = on, key, threshold
}

// keyHeld reports whether key is physically down right now, via SDL's live
// keyboard state — correct even if a KEYUP was missed (window focus loss).
func (c *Ctx) keyHeld(key sdl.Keycode) bool {
	if key == sdl.K_UNKNOWN {
		return false
	}
	sc := sdl.GetScancodeFromKey(key)
	state := sdl.GetKeyboardState()
	return int(sc) < len(state) && state[sc] != 0
}

// --- dropdown ---------------------------------------------------------------

// ddDraw is one deferred dropdown overlay: geometry and content resolve at
// Dropdown call time, FinishFrame only paints (so the list stacks above
// widgets drawn after the dropdown).
type ddDraw struct {
	list    sdl.Rect
	options []string
	cur     int
	scroll  int32
	rowH    int32
}

// ddMaxVisibleRows caps an open dropdown list's height; longer lists
// wheel-scroll inside the overlay.
const ddMaxVisibleRows = 12

// Dropdown is a click-to-open selector (playtest: "PLEASE make the color
// and pos selection a dropdown"). Closed, it shows options[cur] plus a
// chevron; open, the option list paints above everything and the pointer
// is modally captured so widgets underneath stay inert. Returns the
// (possibly new) index and whether it changed this frame.
func (c *Ctx) Dropdown(id string, r sdl.Rect, options []string, cur int) (int, bool) {
	if len(options) == 0 {
		return cur, false
	}
	if cur < 0 || cur >= len(options) {
		cur = 0
	}
	open := c.ddOpen == id

	// Closed control: button chrome with the pick and a chevron.
	bg := ColPanel
	if c.hovering(r) || open {
		bg = ColPanelHi
	}
	c.Fill(r, bg)
	c.Border(r, ColAccent)
	if t, ok := c.textTexture(options[cur], ColText, c.font); ok {
		w := t.w
		if maxW := r.W - 22; w > maxW && maxW > 0 {
			w = maxW
		}
		c.blitLabel(t, r.X+6, r.Y+(r.H-t.h)/2, w)
	}
	c.Label(r.X+r.W-14, r.Y+(r.H-int32(c.font.Height()))/2, "▾", ColTextDim)

	if !open {
		if c.hovering(r) && c.clicked {
			c.ddOpen = id
			c.ddScroll = 0
		}
		return cur, false
	}

	// Open: geometry. The list grows to the widest option (tiny controls
	// stay usable), drops below the control, flips above at the window's
	// bottom edge, and shifts left at the right edge.
	rowH := r.H
	if rowH < int32(c.font.Height())+6 {
		rowH = int32(c.font.Height()) + 6
	}
	visible := int32(len(options))
	if visible > ddMaxVisibleRows {
		visible = ddMaxVisibleRows
	}
	listW := r.W
	for _, o := range options {
		if w := c.TextWidth(o) + 16; w > listW {
			listW = w
		}
	}
	list := sdl.Rect{X: r.X, Y: r.Y + r.H, W: listW, H: visible * rowH}
	if outW, outH, err := c.Ren.GetOutputSize(); err == nil {
		if limit := c.toLogical(outH); list.Y+list.H > limit && r.Y-list.H >= 0 {
			list.Y = r.Y - list.H
		}
		if limit := c.toLogical(outW); list.X+list.W > limit {
			list.X = limit - list.W
		}
	}

	// Modal capture: the control∪list union owns the pointer until the
	// close releases (next BeginFrame), fencing widgets in both draw
	// orders.
	left, right, top := r.X, r.X+r.W, r.Y
	if list.X < left {
		left = list.X
	}
	if e := list.X + list.W; e > right {
		right = e
	}
	if list.Y < top {
		top = list.Y
	}
	c.modalOn = true
	c.modalRect = sdl.Rect{X: left, Y: top, W: right - left, H: r.H + list.H}

	// Wheel scrolls long lists; clamp to content.
	contentH := int32(len(options)) * rowH
	c.ddScroll -= c.WheelIn(list) * rowH
	if max := contentH - list.H; c.ddScroll > max {
		c.ddScroll = max
	}
	if c.ddScroll < 0 {
		c.ddScroll = 0
	}

	// Interaction resolves NOW (frame-consistent); painting defers.
	next, changed := cur, false
	if c.clicked {
		switch {
		case c.hovering(list):
			idx := int((c.mouseY - list.Y + c.ddScroll) / rowH)
			if idx >= 0 && idx < len(options) {
				next, changed = idx, idx != cur
			}
			c.ddOpen = ""
			c.modalRelease = true
		case c.hovering(r):
			// Toggling the control shut.
			c.ddOpen = ""
			c.modalRelease = true
		default:
			// Click-away closes and the fence swallows the click.
			c.ddOpen = ""
			c.modalRelease = true
		}
	}
	c.ddDraws = append(c.ddDraws, ddDraw{
		list: list, options: options, cur: next, scroll: c.ddScroll, rowH: rowH,
	})
	return next, changed
}

// FinishFrame paints deferred overlays (open dropdown lists). Call after
// every screen draw of the frame.
// Tooltip arms a one-line hover hint for rect r; drawTooltip paints it near
// the cursor at end-of-frame so it sits above the grid cells it describes.
// Skipped while a dropdown owns the pointer (modal fence).
func (c *Ctx) Tooltip(r sdl.Rect, text string) {
	if text != "" && !c.modalOn && c.hovering(r) {
		c.tipText = text
	}
}

// tooltipDwell is how long the pointer must rest on a TooltipAfter target
// before its hint shows — long enough to stay unobtrusive (the Extras buttons).
const tooltipDwell = 2 * time.Second

// TooltipAfter is Tooltip with a dwell: r's hint shows only after the pointer
// has rested on it (keyed by id) for tooltipDwell. id distinguishes adjacent
// targets so moving between them restarts the timer.
func (c *Ctx) TooltipAfter(id string, r sdl.Rect, text string) {
	if text == "" || c.modalOn || !c.hovering(r) {
		if c.tipHoverID == id {
			c.tipHoverID = ""
		}
		return
	}
	if c.tipHoverID != id {
		c.tipHoverID = id
		c.tipHoverSince = time.Now()
		return
	}
	if time.Since(c.tipHoverSince) >= tooltipDwell {
		c.tipText = text
	}
}

// drawTooltip paints the armed hover hint near the cursor, flipped at the
// window edges so it never spills off-screen. Called last in the frame.
func (c *Ctx) drawTooltip(w, h int32) {
	if c.tipText == "" {
		return
	}
	box := sdl.Rect{X: c.mouseX + 16, Y: c.mouseY + 18, W: c.TextWidth(c.tipText) + 12, H: 22}
	if box.X+box.W > w {
		box.X = c.mouseX - box.W - 8 // flip left of the pointer
	}
	if box.X < 0 {
		box.X = 0
	}
	if box.Y+box.H > h {
		box.Y = c.mouseY - box.H - 6 // flip above the pointer
	}
	c.Fill(box, sdl.Color{R: 0, G: 0, B: 0, A: 235})
	c.Border(box, ColAccent)
	c.Label(box.X+6, box.Y+3, c.tipText, ColText)
}

func (c *Ctx) FinishFrame() {
	for i := range c.ddDraws {
		d := &c.ddDraws[i]
		c.Fill(d.list, ColPanel)
		c.Border(d.list, ColAccent)
		y := d.list.Y - d.scroll
		for idx, opt := range d.options {
			row := sdl.Rect{X: d.list.X, Y: y, W: d.list.W, H: d.rowH}
			y += d.rowH
			if row.Y+row.H <= d.list.Y || row.Y >= d.list.Y+d.list.H {
				continue
			}
			switch {
			case idx == d.cur:
				c.Fill(row, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 110})
			case c.hovering(row):
				c.Fill(row, ColPanelHi)
			}
			if t, ok := c.textTexture(opt, ColText, c.font); ok {
				w := t.w
				if maxW := row.W - 12; w > maxW && maxW > 0 {
					w = maxW
				}
				c.blitLabel(t, row.X+6, row.Y+(d.rowH-t.h)/2, w)
			}
		}
	}
	c.ddDraws = c.ddDraws[:0]
}

// WrapText greedily word-wraps s to maxW pixels in the chrome font,
// capped at maxLines (the last line gains an ellipsis when truncated).
// Widths ride the TextWidth memo; callers cache the result per string.
func (c *Ctx) WrapText(s string, maxW int32, maxLines int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, maxLines)
	line := ""
	for _, word := range words {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		if c.TextWidth(candidate) <= maxW || line == "" {
			line = candidate
			continue
		}
		lines = append(lines, line)
		line = word
		if len(lines) == maxLines {
			lines[maxLines-1] += "…"
			return lines
		}
	}
	return append(lines, line)
}

// VScrollbar draws a proportional vertical scrollbar and returns the scroll
// offset, updated by thumb drags: pressing anywhere on the track centers
// the thumb there, so one click reaches any point — including the very
// bottom of a 4000-character list. content and visible are pixel heights;
// the result is clamped to [0, content-visible] (use it to clamp wheel
// scrolling too). Draws nothing when everything fits.
func (c *Ctx) VScrollbar(id string, track sdl.Rect, scroll, content, visible int32) int32 {
	maxScroll := content - visible
	if maxScroll <= 0 {
		return 0
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}

	thumbH := track.H * visible / content
	if thumbH < scrollThumbMinPx {
		thumbH = scrollThumbMinPx
	}
	if thumbH > track.H {
		thumbH = track.H
	}
	span := track.H - thumbH

	grab := sdl.Rect{X: track.X - scrollGrabSlopPx, Y: track.Y, W: track.W + 2*scrollGrabSlopPx, H: track.H}
	if c.mouseDown && (c.dragID == id || (c.dragID == "" && c.hovering(grab))) {
		c.dragID = id
		if span > 0 {
			pos := c.mouseY - track.Y - thumbH/2
			if pos < 0 {
				pos = 0
			}
			if pos > span {
				pos = span
			}
			scroll = pos * maxScroll / span
		}
	}

	c.Fill(track, ColPanel)
	thumbY := track.Y + scroll*span/maxScroll
	col := ColPanelHi
	if c.dragID == id || c.hovering(grab) {
		col = ColAccent
	}
	c.Fill(sdl.Rect{X: track.X, Y: thumbY, W: track.W, H: thumbH}, col)
	return scroll
}

// Slider draws a horizontal value slider over [0, maxVal] and returns the
// (possibly updated) value. It mirrors VScrollbar's drag model: drag-captured
// by id, hover-gated, and mouse-down anywhere on the track jumps the thumb —
// so it's grabbable rather than a fiddly +/- button press. Continuous (any
// int in range).
func (c *Ctx) Slider(id string, track sdl.Rect, value, maxVal int32) int32 {
	if maxVal <= 0 {
		return 0
	}
	if value < 0 {
		value = 0
	}
	if value > maxVal {
		value = maxVal
	}
	const sliderThumbW = 10
	span := track.W - sliderThumbW
	if span < 0 {
		span = 0
	}
	grab := sdl.Rect{X: track.X - scrollGrabSlopPx, Y: track.Y - scrollGrabSlopPx, W: track.W + 2*scrollGrabSlopPx, H: track.H + 2*scrollGrabSlopPx}
	if c.mouseDown && (c.dragID == id || (c.dragID == "" && c.hovering(grab))) {
		c.dragID = id
		if span > 0 {
			pos := c.mouseX - track.X - sliderThumbW/2
			if pos < 0 {
				pos = 0
			}
			if pos > span {
				pos = span
			}
			value = pos * maxVal / span
		}
	}
	c.Fill(track, ColPanel)
	thumbX := track.X
	if span > 0 {
		thumbX = track.X + value*span/maxVal
	}
	if thumbX > track.X { // filled portion left of the thumb reads as "level"
		c.Fill(sdl.Rect{X: track.X, Y: track.Y, W: thumbX - track.X, H: track.H}, ColPanelHi)
	}
	col := ColPanelHi
	if c.dragID == id || c.hovering(grab) {
		col = ColAccent
	}
	c.Fill(sdl.Rect{X: thumbX, Y: track.Y, W: sliderThumbW, H: track.H}, col)
	return value
}

// HoverPreview tracks dwell time on a widget id; returns true when the
// full-size preview should show: the configured hover dwell, or right-click
// toggles instantly. Returns false outright when previews are disabled
// (Settings → General), so callers light up nothing.
func (c *Ctx) HoverPreview(id string, r sdl.Rect) bool {
	if !c.hoverPreviewOn || !c.hovering(r) {
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
	return time.Since(c.hoverSince) >= c.hoverPreviewDelay
}

// SetHoverPreview stamps the per-frame sprite-preview config (enabled + dwell)
// onto the context; App.Frame calls it once before any screen draws.
func (c *Ctx) SetHoverPreview(on bool, delay time.Duration) {
	c.hoverPreviewOn = on
	c.hoverPreviewDelay = delay
}

// Destroy frees cached textures and fonts.
func (c *Ctx) Destroy() {
	c.purgeTextCache()
	for _, s := range []*fontSet{&c.chatSet, &c.logSet} {
		for _, f := range s.fonts {
			if f != c.font && f != nil {
				f.Close()
			}
		}
		s.fonts = nil
	}
	if c.font != nil {
		c.font.Close()
	}
	if c.fontBig != nil {
		c.fontBig.Close()
	}
}
