// Package ui is a small immediate-mode widget kit over SDL2 plus the
// client's screens (lobby, character select, courtroom chrome, settings,
// about). Render-thread only.
package ui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// UIFontSize is the default chrome font size. Sized toward classic Windows UI
	// (~12px) so the base renders compact AND crisp — we never scale the renderer
	// DOWN (bitmap-downscaled text is a blurry mess); users only ever scale UP.
	UIFontSize = 12
	// UIFontSizeBig is for headings.
	UIFontSizeBig = 18

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

	// fieldCaretWLog is the text-field caret's LOGICAL width. At 100% it draws
	// 1:1; on the fractional device-exact path it is projected to a CONSTANT
	// integer device width (uiDeviceFromLogical), so the caret can't flicker
	// 2↔3 device px as its sub-pixel phase drifts with each keystroke (#77 S1a).
	fieldCaretWLog = 2

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

// activeKitColors is the BASE palette applyThemePalette restores to — the stock dark by
// default, or the user's chosen chrome preset (#M3 AsyncAO chrome themes). An AO2 theme's
// stylesheet colours still overlay it; with no theme colours (the common case) the preset
// IS the client chrome. Set on the render thread (theme/preset change), so no lock.
var activeKitColors = defaultKitColors

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

// blendCol linearly mixes two colors (t=0 → a, t=1 → b), alpha from a.
func blendCol(a, b sdl.Color, t float64) sdl.Color {
	mix := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return sdl.Color{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: a.A}
}

// cardColor is the Settings/About "card" surface: a step between the page
// background and the panel colour, so panel-coloured widgets still read clearly
// on top of it. Derived from the live palette, so chrome presets recolour it.
func cardColor() sdl.Color { return blendCol(ColBackground, ColPanel, 0.55) }

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
		activeKitColors[0], activeKitColors[1], activeKitColors[2],
		activeKitColors[3], activeKitColors[4], activeKitColors[5],
		activeKitColors[6]
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
	w, h int32    // DEVICE px of the rasterized texture
	// owned marks a dedicated (non-atlas) texture the purge must destroy
	// individually — labels too big for a shelf.
	owned bool
	// devPct (#77) is the device font scale the texture was rasterized at (100 =
	// 1:1). blitLabel divides w/h back to logical by this, so a label rasterized
	// at a device face draws at its logical size (the renderer's SetScale then
	// maps it 1:1 onto device pixels — crisp). Stamped in textTexture.
	devPct int32
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
	// Device-scaled chrome faces (#77 crisp scaling): font/fontBig opened at
	// UIFontSize×(textDevPct/100) so their glyph textures rasterize at final
	// DEVICE size and blit 1:1 (no ren.SetScale bilinear blur). c.font stays the
	// LOGICAL layout reference (every c.font.Height()/SizeUTF8 site keeps reading
	// it); ONLY textTexture rasterizes with the device face and blitLabel divides
	// the device dst back to logical (uiLogicalFromDevice). At textDevPct==100 these
	// SHARE font/fontBig (no duplicate raster). Rebuilt only on a real scale
	// change (SetTextDevScale), never per frame.
	fontDev, fontBigDev *ttf.Font

	// User-scaled font sets (chat box, log/OOC lists): the user's
	// override chain (CJK fallback) plus the embedded last resort,
	// rebuilt only when the percent or the chain changes — settings
	// actions, never per frame.
	chatSet fontSet
	logSet  fontSet
	// Device-scaled siblings of chatSet/logSet (#77): built at pct×(textDevPct/100)
	// so the message-raster/emoji paths rasterize crisp at final device size.
	// The LOGICAL sets above stay the wrap/measure baseline (unchanged callers);
	// deviceFontFor maps a logical set face to its device sibling by set+index.
	chatSetDev fontSet
	logSetDev  fontSet
	// textDevPct is the DEVICE font scale for text rasterization (#77): device px
	// = logical px × textDevPct/100. Normally == the global UI scale (uiPct); the
	// export/offscreen paths BRACKET it to DefaultScalePct (100) so the live UI
	// scale can't leak into export resolution. SetTextDevScale mutates it (no-op
	// when unchanged — the per-frame-safe entry). DefaultScalePct until set.
	textDevPct int32
	// fontChain holds the override TTF/TTC bytes in chain order
	// (≤ fontChainCap). Bytes are read OFF-thread (App pipeline); fonts
	// build here from memory. The slices must outlive their fonts —
	// SDL's RWFromMem points straight at them.
	fontChain    [][]byte
	fontNames    []string // diagnostics (settings status line)
	fontChainGen int      // bumped per SetFontChain; sets rebuild lazily
	// chromeData holds the whole-UI ("font everywhere") override bytes the
	// chrome fonts (c.font/c.fontBig) were built from; nil = the embedded
	// chrome font. SetChromeFont compares by slice identity, so a re-run of
	// applyFontConfig with the same bytes is a free no-op.
	chromeData []byte
	// pickMemo caches per-line font picks (log rows re-pick every frame; an sfnt
	// coverage scan per rune per row would be a hidden storm once a fallback is
	// installed). It also records whether the pick FULLY covers the line, so the
	// raster gate knows when to take the per-glyph mixed-script path without a second
	// scan. Cleared on any font rebuild.
	pickMemo map[string]pickResult
	// Color-emoji fallback face (e.g. Segoe UI Emoji), kept SEPARATE from the
	// chain so the common single-font fast path (pickSet len==1) is unchanged.
	// emojiData is the font file bytes (read off-thread by the App; nil = none/not
	// loaded); emojiFonts builds faces per size lazily. RWFromMem aliases the bytes,
	// so emojiData must outlive the faces.
	emojiData  []byte
	emojiFonts map[int]*ttf.Font
	// Broad-Unicode TEXT fallback faces (Segoe UI, Ebrima, Nirmala, Segoe UI Symbol),
	// loaded LAZILY off-thread only after a message carries a non-ASCII rune. Unlike
	// the emoji face these DO join the per-size set (after the embedded font), so
	// pickFont resolves a single-script run (a Cyrillic / Tifinagh / Indic showname)
	// to a covering face. nil until the first non-ASCII message → pure-ASCII sessions
	// keep the len==1 fast path. RWFromMem aliases the bytes, so they must outlive the
	// faces. Bumping fontChainGen on install rebuilds every set.
	fallbackData [][]byte
	// sfnt coverage faces for the PICK (parsed once from the same bytes as the ttf
	// faces, in the same order as a set's entries: chain, embedded, fallback). The
	// embedded one is parsed lazily on first build. sfntBuf is the reused GlyphIndex
	// scratch (render thread only). wantFallback is the latch the cheap non-ASCII scan
	// raises in ChatFontFor/LogFontFor; the App drains it to kick the off-thread load.
	chainCover    []*sfnt.Font
	fallbackCover []*sfnt.Font
	embeddedCover *sfnt.Font
	sfntBuf       sfnt.Buffer
	wantFallback  bool
	// CJK tier: big Han/Kana + Hangul faces (13-35 MB each), loaded only when a CJK
	// LETTER is actually drawn (not on every non-ASCII), appended after the broad set.
	// Separate from fallbackData so the two tiers load independently.
	cjkData  [][]byte
	cjkCover []*sfnt.Font
	wantCJK  bool

	// uiPct is the global render scale percent; mouse coordinates
	// unproject through it so logical hit-tests stay exact.
	uiPct int32

	mouseX, mouseY int32
	downX, downY   int32     // left-press origin (logical px); ClickedIn gates on it
	clicked        bool      // left released this frame
	dblClick       bool      // a double-click landed this frame (doubleClickWindow)
	tripleClick    bool      // a third click in the same spot/window (cycles 1→2→3→1)
	clickStreak    int       // consecutive same-spot clicks (double/triple detection)
	lastClickAt    time.Time // double-click detection state (persists across frames)
	lastClickX     int32
	lastClickY     int32
	ctrlHeld       bool // live modifier state (Ctrl+wheel font sizing)
	shiftHeld      bool // live modifier state (shift-extend text selection)
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
	copyReq        bool        // Ctrl+C: focused field copies its selection (else value)
	cutReq         bool        // Ctrl+X: focused field cuts its selection (else clears)
	selectAll      bool        // Ctrl+A armed: the focused field converts it to a real all-selection
	undoReq        bool        // Ctrl+Z routed to the focused field (App consumes the chord pre-screen)
	redoReq        bool        // Ctrl+Y / Ctrl+Shift+Z, same routing
	wordBack       bool        // Ctrl+Backspace in a focused field: delete the preceding word (event flag; gated on wordDeleteOn at consumption)
	// Text-field selection: the anchor end (rune index) in the FOCUSED field;
	// -1 = no selection. The caret is the moving end. Like c.caret it belongs
	// to caretField, and resets on focus change. prevMouseDown feeds the
	// press edge that starts a drag-selection.
	selAnchor     int
	prevMouseDown bool
	// Per-field undo/redo histories (fieldhistory.go): bounded map + LRU order.
	fieldHists   map[string]*fieldHistory
	fieldHistUse []string
	// wheelTaken marks this frame's wheel as consumed by a hovered widget
	// (spinbox rows, WheelIn lists) so page-level scrolls don't double-act.
	wheelTaken bool
	mouseDown  bool   // left button currently held (event-tracked)
	middleHeld bool   // middle (wheel) button held — fast log zoom modifier (event-tracked)
	dragID     string // widget owning the active drag ("" = none)
	// onRow, when non-nil, receives every Checkbox's label + drawn y — the
	// settings search's collect pass (#26 gather) uses it to index all tabs'
	// rows. nil in normal play: the hook costs one nil check per checkbox.
	onRow   func(label string, y int32)
	dropped string      // SDL_DROPFILE path this frame ("" = none)
	hotkey  sdl.Keycode // non-clipboard Ctrl chord this frame (0 = none)
	// clipOn/clipRect mirror the renderer clip set by pushClip so hovering()
	// can refuse hits outside it without a cgo query per hit test (a clipped
	// widget only exists inside its clip — input must agree with the pixels).
	clipOn   bool
	clipRect sdl.Rect
	// cgoRect is the persistent scratch rect for SDL calls that take *sdl.Rect
	// (FillRect/DrawRect/SetClipRect): a pointer argument escapes through cgo,
	// so passing &local heap-allocates on EVERY call — ~95 allocations per
	// courtroom frame, invisible until the whole-screen gate measured it.
	// Pointing SDL at this long-lived field instead costs nothing
	// (internal/render's idiom). Safe to share across call kinds: SDL copies
	// the rect during the call and never retains the pointer, and Ctx is
	// render-thread-only.
	cgoRect sdl.Rect
	// Chrome SHAPE (A5): the App resolves the active shape + its fill/stroke
	// mask textures into these fields ONCE per frame (refreshShapeMasks) so the
	// hot per-widget FillShaped / ButtonCol path is a plain field read — no
	// store lookup (which would clear every streaming generation bump) and no
	// alloc. activeShape=="" or shapeSharp means the shaped path is disabled and
	// every draw site falls through to the byte-identical Fill+Border body.
	// shapeMaskReady gates on both a non-sharp preset AND resident masks, so a
	// not-yet-built frame also renders sharp (no first-frame flash). The masks
	// are WHITE-RGB straight-alpha silhouettes tinted per draw via SetColorMod +
	// SetAlphaMod (the glyphcache idiom); the two scratch rects are the cgoRect
	// twins for the 9-slice src/dst Copies (a &local would escape through cgo).
	activeShape     string
	shapeMaskReady  bool
	shapeFillTex    *sdl.Texture // fill silhouette mask (nil ⇒ sharp fall-through)
	shapeStrokeTex  *sdl.Texture // 1px stroke-ring mask (nil ⇒ no border pass)
	shapeMaskDim    int32        // source mask side length in px (square)
	shapeMaskR      int32        // source corner-quadrant radius in px
	shapePill       bool         // pill preset: corner = min(w,h)/2 at draw time
	shapeSrcScratch sdl.Rect     // 9-slice source-rect scratch (never &local)
	shapeDstScratch sdl.Rect     // 9-slice dest-rect scratch (never &local)
	tipText         string       // hover hint to paint at end-of-frame ("" = none)
	// Cached word-wrap of the current tooltip, rebuilt only when the text or the wrap
	// width changes (drawTooltip) — so a hovered tooltip doesn't re-wrap (and re-allocate)
	// every frame. See WrapText's "callers cache the result" note.
	tipWrapText string
	tipWrapW    int32
	tipWrapLn   []string

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
	// wordDeleteOn mirrors the WordDelete pref (App stamps it per frame, like
	// holdOn). textField is a Ctx method with no Prefs handle, so the
	// consumption gate must read a stamped field — a pref that saves/loads but
	// is never stamped would leave the feature dead despite defaulting ON. Not
	// parked/reset: it's config state, not per-frame input.
	wordDeleteOn bool

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
	ddOpenList   sdl.Rect // the OPEN dropdown's list rect (flip-adjusted), stashed each draw — read by boxFencesPointer so a list over a floating panel stays clickable
	modalOn      bool
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
	hoverRect  sdl.Rect // the currently-hovered preview trigger's rect (for the close-on-leave travel corridor)
	// Delayed-tooltip dwell (TooltipAfter): the pointer must rest on a target
	// for tooltipDwell before its hint shows. Separate from hoverID so the
	// sprite-preview dwell and tooltip dwell never clobber each other.
	tipHoverID    string
	tipHoverSince time.Time
	// tipHoverDelay is the dwell the ACTIVE delayed tooltip is waiting on — the
	// fixed tooltipDwell for TooltipAfter, or a caller-chosen value for
	// TooltipAfterDelay (the emote-name tooltip's slider). NextHoverDue schedules
	// the event-loop wake off this so a longer custom dwell still fires without an
	// extra input event. Set alongside tipHoverID; a zero value means tooltipDwell.
	tipHoverDelay time.Duration
	// hover-preview config, stamped once per frame from prefs (App.Frame →
	// SetHoverPreview): whether previews are enabled, and the dwell before one
	// shows. Keeping them on Ctx keeps HoverPreview a pure read on the hot path.
	hoverPreviewOn    bool
	hoverPreviewDelay time.Duration

	textCache  map[textKey]cachedText
	atlas      []*atlasPage     // shared label pages (≤ textAtlasMaxPages)
	widthCache map[string]int32 // chrome-font TextWidth memo
	// devWidthCache memoizes the DEVICE-face prefix width (raw device px) of a
	// text-field prefix — the metric the drawn field texture actually uses at a
	// fractional UI scale. widthCache measures the LOGICAL chrome face (correct
	// for scale-invariant layout, TestTextWidthScaleInvariant); a field caret must
	// instead land on the DEVICE glyph seam of its device-rasterized texture, and
	// SDL_ttf quantizes per-glyph advances independently per point size, so the two
	// faces diverge over a long prefix (the length-growing caret drift, #77 S1b).
	// Stores DEVICE px (fold to logical on read via uiLogicalFromDevice); the raw
	// device value feeds the device-exact caret draw with no round-trip loss.
	// Only populated at a fractional scale (deviceExactText); at 100% the field
	// uses widthCache unchanged (the device face is the identity). Purged wherever
	// widthCache is (SetTextDevScale, SetChromeFont) so a stale-size entry after a
	// scale change is impossible. Lazily created (like emojiCache) so test-built
	// Ctx literals without this field stay valid — nil READ / clear(nil) are safe.
	devWidthCache map[string]int32
	// emojiCache holds multi-font rasters for the rare labels (shownames, IC/OOC
	// log lines) that mix text with colour emoji the chat font can't draw. Keyed
	// like textCache (text + colour + primary-font ptr); each entry owns dedicated
	// textures (not the shared atlas), so it's bounded and Destroy()ed on purge —
	// wherever the primary fonts are rebuilt (purgeTextCache) and on an emoji-font
	// swap (SetEmojiFont). Lazily created so test-built Ctx values stay valid.
	emojiCache map[emojiKey]*render.MessageRaster

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
		Ren:     ren,
		font:    font,
		fontBig: fontBig,
		// #77: at 100% the device faces SHARE the logical ones (no duplicate
		// raster). SetTextDevScale rebuilds them when the UI scale changes.
		fontDev:    font,
		fontBigDev: fontBig,
		textDevPct: DefaultScalePct,
		textCache:  map[textKey]cachedText{},
		widthCache: map[string]int32{},
	}, nil
}

// SetWindow attaches the SDL window for attention requests (FlashWindow).
func (c *Ctx) SetWindow(win *sdl.Window) { c.win = win }

// WindowFocused reports whether the window currently has input focus and isn't
// minimised — used to gate desktop (OS) toasts to "you're tabbed away" only (#M4). A nil
// window (headless tests) reads as focused, so tests never toast. Render thread only.
func (c *Ctx) WindowFocused() bool {
	if c.win == nil {
		return true
	}
	f := c.win.GetFlags()
	return f&sdl.WINDOW_INPUT_FOCUS != 0 && f&sdl.WINDOW_MINIMIZED == 0
}

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
// embedded font, then any broad-Unicode fallbacks. cover holds the sfnt
// face for each entry (same order) for the cmap-based coverage PICK —
// SDL_ttf's GlyphMetrics can't report coverage in this build (it returns
// .notdef metrics without error), so the rendering ttf.Font and the
// coverage sfnt.Font are parsed separately from the same bytes.
type fontSet struct {
	fonts []*ttf.Font
	cover []*sfnt.Font
	pct   int
	gen   int
	// devPct (#77) is the DEVICE scale a device set was built at (the logical
	// sets leave it 0). fontsForDev keys on it so a UI-scale change rebuilds the
	// device set even though the caller's pct arg is unchanged.
	devPct int
}

// fontChainCap bounds the override chain (a primary plus a few CJK
// fallbacks is the realistic maximum).
const fontChainCap = 4

// ChatFont returns the IC message box PRIMARY font at pct percent of the
// chrome size (metrics/wrap baseline); ChatFontFor picks per text.
func (c *Ctx) ChatFont(pct int) *ttf.Font {
	return c.fontsFor(&c.chatSet, pct)[0]
}

// ChatFontFor picks the font that covers every rune of text: the embedded
// font for Latin/Greek/Cyrillic, a broad fallback face (once loaded) for the
// scripts it lacks. Non-ASCII text also latches the one-time fallback load.
func (c *Ctx) ChatFontFor(pct int, text string) *ttf.Font {
	c.noteScript(text)
	return c.pickSet(&c.chatSet, pct, text)
}

// LogFont returns the log/OOC list PRIMARY font at pct percent.
func (c *Ctx) LogFont(pct int) *ttf.Font {
	return c.fontsFor(&c.logSet, pct)[0]
}

// LogFontFor picks the covering font for one log line (see ChatFontFor).
func (c *Ctx) LogFontFor(pct int, text string) *ttf.Font {
	c.noteScript(text)
	return c.pickSet(&c.logSet, pct, text)
}

// noteScript raises the load latches the first time their script is seen: the broad
// fallback on any non-ASCII rune, the (independent) CJK tier on a Han/Kana/Hangul
// letter. Once a tier is loaded or already latched its check drops out, and when both
// are settled it returns before the byte scan — so a pure-ASCII session never trips it
// and the single-font fast path stays untouched (0-perf rule). The CJK latch is kept
// SEPARATE from the broad one so the broad load (fired by the first European accent)
// can't swallow a later CJK trigger.
func (c *Ctx) noteScript(text string) {
	needBroad := c.fallbackData == nil && !c.wantFallback
	needCJK := c.cjkData == nil && !c.wantCJK
	if !needBroad && !needCJK {
		return
	}
	nonASCII, cjkMaybe := scanScriptBytes(text)
	if !nonASCII {
		return
	}
	if needBroad {
		c.wantFallback = true
	}
	if needCJK && cjkMaybe && hasCJKLetter(text) {
		c.wantCJK = true
	}
}

// TakeWantsFallback / TakeWantsCJK report (and clear) whether their script was seen
// since the last drain, so the App kicks the matching off-thread font read on demand.
func (c *Ctx) TakeWantsFallback() bool {
	if c.wantFallback {
		c.wantFallback = false
		return true
	}
	return false
}

func (c *Ctx) TakeWantsCJK() bool {
	if c.wantCJK {
		c.wantCJK = false
		return true
	}
	return false
}

// pickResult is one cached line pick: the covering face, and whether it covers EVERY
// rune (false = a mixed-script line the per-glyph raster should handle).
type pickResult struct {
	font    *ttf.Font
	covered bool
}

// pickSet memoizes the per-line coverage pick: repeat draws cost one map probe, and
// the no-fallback single-font set costs nothing at all (the len==1 fast path).
func (c *Ctx) pickSet(s *fontSet, pct int, text string) *ttf.Font {
	fonts := c.fontsFor(s, pct)
	if len(fonts) == 1 {
		return fonts[0]
	}
	if r, ok := c.pickMemo[text]; ok {
		return r.font
	}
	f, covered := pickFont(fonts, s.cover, &c.sfntBuf, text)
	if c.pickMemo == nil {
		c.pickMemo = make(map[string]pickResult, 256)
	} else if len(c.pickMemo) >= textCacheMax {
		clear(c.pickMemo) // bounded: wholesale reset, repopulates hot lines
	}
	c.pickMemo[text] = pickResult{font: f, covered: covered}
	return f
}

// covers reports whether the whole-message pick for text covered every rune. A line
// with no memo entry took the single-font fast path (no fallback loaded) and counts as
// covered — so the raster gate stays cheap for the common case. Read AFTER the matching
// *FontFor pick (same frame), which populated the entry.
func (c *Ctx) covers(text string) bool {
	r, ok := c.pickMemo[text]
	return !ok || r.covered
}

// setOf finds the built fontSet a picked face belongs to, by pointer. Font instances
// are unique per size, so this resolves to the set at the right scale without threading
// pct anywhere — the face was just returned from that set's fontsFor this frame.
func (c *Ctx) setOf(primary *ttf.Font) *fontSet {
	for _, s := range []*fontSet{&c.chatSet, &c.logSet} {
		for _, f := range s.fonts {
			if f == primary {
				return s
			}
		}
	}
	return nil
}

// coverRunes returns the per-rune text face for the per-glyph raster: each rune drawn
// by the first face in primary's set whose cmap has it, falling back to primary for an
// uncovered rune (or when the set can't be found). A mixed-script run thus resolves
// glyph by glyph. Paid once per raster BUILD (cache miss), never per frame.
func (c *Ctx) coverRunes(primary *ttf.Font, runes []rune) []*ttf.Font {
	out := make([]*ttf.Font, len(runes))
	s := c.setOf(primary)
	if s == nil {
		for i := range out {
			out[i] = primary
		}
		return out
	}
	for i, r := range runes {
		out[i] = primary
		for j, cov := range s.cover {
			if coverHasRune(cov, &c.sfntBuf, r) {
				out[i] = s.fonts[j]
				break
			}
		}
	}
	return out
}

// setIndexOf resolves a picked LOGICAL face to its set, that set's DEVICE sibling
// set, and its index within the set — so the #77 raster path can swap a logical
// face for the device-scaled one at the same chain position. (nil, nil, -1) when
// the face isn't in a scaled set (chrome-only / headless).
func (c *Ctx) setIndexOf(primary *ttf.Font) (logical, device *fontSet, idx int) {
	pairs := []struct{ log, dev *fontSet }{
		{&c.chatSet, &c.chatSetDev},
		{&c.logSet, &c.logSetDev},
	}
	for _, p := range pairs {
		for i, f := range p.log.fonts {
			if f == primary {
				return p.log, p.dev, i
			}
		}
	}
	return nil, nil, -1
}

// deviceFontFor returns the DEVICE-scaled sibling (#77) of a picked LOGICAL face
// so the message-raster/emoji paths rasterize crisp at final device size. pct is
// the per-element scale the logical face was picked at; the device set is built
// at the same pct (folding textDevPct on top). At textDevPct==100 the device set
// mirrors the logical one, so this returns the same face. Falls back to primary
// when the set can't be resolved (headless / chrome-only). Render thread.
func (c *Ctx) deviceFontFor(primary *ttf.Font, pct int) *ttf.Font {
	if c.textDevPct == DefaultScalePct || c.textDevPct == 0 {
		return primary // 1:1 — device set mirrors logical
	}
	log, dev, idx := c.setIndexOf(primary)
	if log == nil || idx < 0 {
		return primary
	}
	df := c.fontsForDev(dev, log.pct) // build the sibling at the SAME per-element pct
	if idx < len(df) {
		return df[idx]
	}
	return primary
}

// deviceCoverRunes is coverRunes over the DEVICE set (#77): each rune's covering
// face resolved from the device-scaled sibling set, so a mixed-script fallback
// raster is crisp too. Built once per raster (cache miss), never per frame.
func (c *Ctx) deviceCoverRunes(logicalPrimary *ttf.Font, pct int, runes []rune) []*ttf.Font {
	out := make([]*ttf.Font, len(runes))
	log, dev, _ := c.setIndexOf(logicalPrimary)
	if log == nil || c.textDevPct == DefaultScalePct || c.textDevPct == 0 {
		// 1:1 or unresolved: device faces == logical faces.
		return c.coverRunes(logicalPrimary, runes)
	}
	df := c.fontsForDev(dev, log.pct)
	devPrimary := logicalPrimary
	if _, _, idx := c.setIndexOf(logicalPrimary); idx >= 0 && idx < len(df) {
		devPrimary = df[idx]
	}
	for i, r := range runes {
		out[i] = devPrimary
		for j, cov := range log.cover { // cover is size-independent → shared with the device set
			if j < len(df) && coverHasRune(cov, &c.sfntBuf, r) {
				out[i] = df[j]
				break
			}
		}
	}
	return out
}

// allSameFont reports whether every entry is f — i.e. the per-rune assignment found no
// script fallback and the line is single-face after all.
func allSameFont(fonts []*ttf.Font, f *ttf.Font) bool {
	for _, x := range fonts {
		if x != f {
			return false
		}
	}
	return true
}

// SetFontChain installs the override font bytes (chain order; nil
// clears). Render thread; the byte slices were read off-thread and must
// stay referenced for the fonts' lifetime (RWFromMem aliases them).
func (c *Ctx) SetFontChain(names []string, data [][]byte) {
	if len(data) > fontChainCap {
		names, data = names[:fontChainCap], data[:fontChainCap]
	}
	c.fontNames, c.fontChain = names, data
	c.chainCover = c.chainCover[:0]
	for _, d := range data {
		c.chainCover = append(c.chainCover, parseCover(d)) // cmap face for the PICK
	}
	c.fontChainGen++
}

// FontChainNames reports the loaded override names (settings status).
func (c *Ctx) FontChainNames() []string { return c.fontNames }

// SetChromeFont swaps the CHROME fonts (c.font/c.fontBig — every menu, button,
// list and heading) to an override face built from data; nil restores the
// embedded font. The bytes were read off-thread and must stay referenced for
// the fonts' lifetime (RWFromMem aliases them). Idempotent on the same slice,
// so applyFontConfig can re-run freely. Returns false when the bytes don't
// open as a font — the current chrome stays untouched. Render thread.
func (c *Ctx) SetChromeFont(data []byte) bool {
	if len(data) == 0 {
		data = nil // normalize: "no override" is always the nil slice
	}
	if data == nil && c.chromeData == nil {
		return true // already on the embedded font — the common no-op
	}
	if data != nil && len(c.chromeData) == len(data) && &c.chromeData[0] == &data[0] {
		return true // same bytes installed — applyFontConfig re-ran on an unrelated change
	}
	// Build the replacements FIRST: a bad file must never tear down the UI font.
	var nf, nfBig *ttf.Font
	var err, errBig error
	if data != nil {
		nf, err = memFont(data, UIFontSize)
		nfBig, errBig = memFont(data, UIFontSizeBig)
	} else {
		nf, err = loadEmbeddedFont(UIFontSize)
		nfBig, errBig = loadEmbeddedFont(UIFontSizeBig)
	}
	if err != nil || errBig != nil {
		if nf != nil {
			nf.Close()
		}
		if nfBig != nil {
			nfBig.Close()
		}
		return false
	}
	// Drop the chat/log sets BEFORE swapping: they may share c.font (the
	// pct==100 fast path in fontsFor), and the close-guards everywhere compare
	// against the CURRENT c.font — swap first and the shared face would be
	// closed twice. The DEVICE sets (#77) share c.fontDev on the same rule, so
	// drop them here too (their close-guard is c.fontDev, about to change).
	for _, s := range []*fontSet{&c.chatSet, &c.logSet} {
		for _, f := range s.fonts {
			if f != c.font && f != nil {
				f.Close()
			}
		}
		s.fonts, s.cover = nil, nil
	}
	for _, s := range []*fontSet{&c.chatSetDev, &c.logSetDev} {
		for _, f := range s.fonts {
			if f != c.fontDev && f != nil {
				f.Close()
			}
		}
		s.fonts, s.cover = nil, nil
	}
	// Drop the device chrome faces too (they share or derive from the old chrome
	// pair); rebuildDeviceChrome below rebuilds them against the new c.font.
	if c.fontDev != nil && c.fontDev != c.font {
		c.fontDev.Close()
	}
	if c.fontBigDev != nil && c.fontBigDev != c.fontBig {
		c.fontBigDev.Close()
	}
	c.fontDev, c.fontBigDev = nil, nil
	old, oldBig := c.font, c.fontBig
	c.font, c.fontBig = nf, nfBig
	c.chromeData = data
	if old != nil {
		old.Close()
	}
	if oldBig != nil {
		oldBig.Close()
	}
	// Rebuild the device chrome pair against the new chrome bytes at the current
	// text device scale (#77): at 100% this just re-shares the new logical faces.
	c.rebuildDeviceChrome()
	// Every cached label texture, memoized width and line pick carries the old
	// faces' identity (pointer keys) — purge wholesale. A chrome swap is a
	// settings action, never per frame.
	c.purgeTextCache()
	clear(c.widthCache)
	clear(c.devWidthCache) // device-face field memo carries the OLD chrome metrics
	c.pickMemo = nil
	return true
}

// fontsFor returns the LOGICAL set's fonts (the wrap/measure baseline), rebuilding
// when the scale or the chain moved (settings actions — never per frame).
func (c *Ctx) fontsFor(s *fontSet, pct int) []*ttf.Font {
	return c.buildSet(s, pct, DefaultScalePct, c.font, false)
}

// fontsForDev returns the DEVICE-scaled sibling set (#77): the same chain opened
// at pct×(textDevPct/100) so the message-raster/emoji paths rasterize crisp. It
// keys on textDevPct via s.devPct so a UI-scale change rebuilds it even though
// pct is unchanged. Shares the device chrome face as the embedded last resort.
func (c *Ctx) fontsForDev(s *fontSet, pct int) []*ttf.Font {
	return c.buildSet(s, pct, int(c.textDevPct), c.fontDev, true)
}

// buildSet is the shared body: opens the chain at size = UIFontSize×pct×devPct/100²,
// sharing chromeShare (c.font at 1:1, c.fontDev for a device set) as the embedded
// last resort at 1:1. The close-guard and the last-resort share both key off
// chromeShare so a device set never double-closes/leaks the device chrome face.
// isDevice=true builds a device sibling set, whose faces the label/pick caches
// DON'T reference (those key on the LOGICAL font) — so a device rebuild must NOT
// purge them (doing so mid-frame could nil the pickMemo a logical pick just
// populated, one-frame-mis-routing a mixed-script message).
func (c *Ctx) buildSet(s *fontSet, pct, devPct int, chromeShare *ttf.Font, isDevice bool) []*ttf.Font {
	if devPct <= 0 {
		devPct = DefaultScalePct
	}
	if len(s.fonts) > 0 && s.pct == pct && s.devPct == devPct && s.gen == c.fontChainGen {
		return s.fonts
	}
	for _, f := range s.fonts {
		if f != chromeShare {
			f.Close()
		}
	}
	if !isDevice {
		// Stale-font cache entries would never be hit again (keys carry the
		// font pointer); purge wholesale — rebuilds are user actions. The
		// pick memo holds those same dead pointers, so it resets too. A DEVICE
		// rebuild skips this: those caches key on the logical face, unaffected.
		c.purgeTextCache()
		c.pickMemo = nil
	}
	s.fonts = s.fonts[:0]
	s.cover = s.cover[:0]
	s.pct, s.devPct, s.gen = pct, devPct, c.fontChainGen
	// Fold the device scale into the point size (#77): the per-element pct AND the
	// global device scale both multiply UIFontSize, so a device set rasterizes at
	// the final device pixel size. devPct==100 reproduces the logical size exactly.
	size := UIFontSize * pct * devPct / (DefaultScalePct * DefaultScalePct)
	if size < 1 {
		size = 1
	}
	// Each entry appends its rendering font (per size) and its sfnt cover face (size-
	// independent, parsed once) in lockstep, so a skipped face never desyncs them.
	for i, data := range c.fontChain {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, coverAt(c.chainCover, i))
		}
	}
	// Embedded last resort; share the chrome face at 1:1 (no duplicate
	// rasters for the common case) — but ONLY while the chrome IS the
	// embedded face: a custom chrome font ("font everywhere") must not stand
	// in for the true last resort, since its coverage isn't goregular's and
	// the cover entry appended below is the embedded cmap. "At 1:1" means the
	// requested size equals the chrome face's size: pct==100 AND devPct==100.
	if chromeShare == nil || (pct == DefaultScalePct && devPct == DefaultScalePct && c.chromeData == nil) {
		s.fonts = append(s.fonts, chromeShare)
	} else if f, err := loadEmbeddedFont(size); err == nil {
		s.fonts = append(s.fonts, f)
	} else {
		s.fonts = append(s.fonts, chromeShare)
	}
	if c.embeddedCover == nil {
		c.embeddedCover = parseCover(goregular.TTF) // goregular's cmap; parsed once
	}
	s.cover = append(s.cover, c.embeddedCover)
	// Broad-Unicode fallbacks AFTER the embedded font: only runes the embedded font
	// (and any user chain) lacks fall through to them, so Latin keeps the embedded
	// look. nil for pure-ASCII sessions (never loaded) → set stays single-font and
	// pickSet short-circuits. A bad/absent face is skipped.
	for i, data := range c.fallbackData {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, coverAt(c.fallbackCover, i))
		}
	}
	// CJK tier last (the big Han/Kana/Hangul faces) — reached only for runes nothing
	// above covers. Loaded lazily, so nil for the vast majority of sessions.
	for i, data := range c.cjkData {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, coverAt(c.cjkCover, i))
		}
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
	c.purgeEmojiCache() // cached rasters used the OLD emoji face — rebuild on next draw
}

// SetFallbackFonts installs the broad-Unicode TEXT fallback faces (read off-thread by
// the App after a non-ASCII message; nil clears). The byte slices must stay
// referenced for the faces' lifetime (RWFromMem aliases them). Bumps fontChainGen so
// every per-size set rebuilds with the new faces appended after the embedded font.
// Idempotent on identical bytes — guarded by the App's one-shot load gate. Render
// thread.
func (c *Ctx) SetFallbackFonts(data [][]byte) {
	if len(data) == 0 && c.fallbackData == nil {
		return
	}
	c.fallbackData = data
	c.fallbackCover = c.fallbackCover[:0]
	for _, d := range data {
		c.fallbackCover = append(c.fallbackCover, parseCover(d)) // cmap face for the PICK
	}
	c.fontChainGen++ // force every fontSet to rebuild with the fallbacks included
	// Emoji rasters bake per-rune coverRunes picks (and cached-nil "nothing to
	// add" verdicts) from the OLD chain — purge so a mixed-script label re-routes
	// through the new faces next frame instead of staying tofu until restart.
	c.purgeEmojiCache()
}

// SetCJKFonts installs the CJK tier (Han/Kana + Hangul faces) read off-thread; mirrors
// SetFallbackFonts. Appended after the broad set, so a CJK line resolves to one of
// these only when nothing earlier covered it. Render thread.
func (c *Ctx) SetCJKFonts(data [][]byte) {
	if len(data) == 0 && c.cjkData == nil {
		return
	}
	c.cjkData = data
	c.cjkCover = c.cjkCover[:0]
	for _, d := range data {
		c.cjkCover = append(c.cjkCover, parseCover(d))
	}
	c.fontChainGen++
	c.purgeEmojiCache() // same as SetFallbackFonts: baked coverRunes picks are chain-stale
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

// emojiDeviceFont is EmojiFont at the DEVICE scale (#77): the emoji face folded
// with textDevPct so a fallback raster's emoji glyphs are crisp too. Shares the
// per-size emojiFonts map (device sizes coexist with logical ones). At 100% it
// is EmojiFont(pct) exactly. Render thread.
func (c *Ctx) emojiDeviceFont(pct int) *ttf.Font {
	if c.textDevPct == DefaultScalePct || c.textDevPct == 0 {
		return c.EmojiFont(pct)
	}
	if c.emojiData == nil {
		return nil
	}
	size := UIFontSize * pct * int(c.textDevPct) / (DefaultScalePct * DefaultScalePct)
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
		c.emojiFonts[size] = nil
		return nil
	}
	c.emojiFonts[size] = f
	return f
}

// pickFont returns the rendering font for the first set entry whose sfnt cover
// provides every rune of text — a Cyrillic line stays on the embedded font, a
// Tifinagh / Indic line resolves to the covering fallback face. cover is aligned
// with fonts; the last entry is the unconditional fallback (used when nothing
// covers, so the result is at worst the same .notdef box as before).
func pickFont(fonts []*ttf.Font, cover []*sfnt.Font, buf *sfnt.Buffer, text string) (*ttf.Font, bool) {
	if len(fonts) == 1 {
		return fonts[0], true
	}
	for i, f := range fonts[:len(fonts)-1] {
		if i < len(cover) && coverHasAll(cover[i], buf, text) {
			return f, true
		}
	}
	// Fell through — no single face covers every rune (a mixed-script run). The
	// last entry renders it (at worst the same .notdef as before); covered=false
	// tells the raster gate to take the per-glyph path instead.
	return fonts[len(fonts)-1], false
}

// coverHasRune reports whether the sfnt face provides a glyph (cmap index != 0)
// for r. A nil face covers nothing; the reused buffer keeps it allocation-free.
func coverHasRune(f *sfnt.Font, buf *sfnt.Buffer, r rune) bool {
	if f == nil {
		return false
	}
	idx, err := f.GlyphIndex(buf, r)
	return err == nil && idx != 0
}

// coverHasAll reports whether the face covers EVERY rune of text (the whole-message
// PICK rule).
func coverHasAll(f *sfnt.Font, buf *sfnt.Buffer, text string) bool {
	if f == nil {
		return false
	}
	for _, r := range text {
		if !coverHasRune(f, buf, r) {
			return false
		}
	}
	return true
}

// coverAt returns the cover face at i, or nil when out of range — keeps the
// fonts/cover slices aligned even if a face failed to build or parse.
func coverAt(s []*sfnt.Font, i int) *sfnt.Font {
	if i < len(s) {
		return s[i]
	}
	return nil
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
	// fieldSeq deliberately does NOT clear here: it clears in BeginDraw, at
	// the top of a real draw pass. BeginFrame also runs on frames the loop
	// SKIPS (static-screen passes draw nothing), and clearing it there left
	// the tab-cycle above reading an empty list — a Tab pressed after a
	// skipped stretch cycled nowhere. Keeping the last DRAWN frame's field
	// order across skips is exactly right: it's what's on screen.
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

	// prevMouseDown snapshots LAST frame's held state before this frame's
	// events arrive — the press edge (mouseDown && !prevMouseDown) that starts
	// a text-field drag-selection.
	c.prevMouseDown = c.mouseDown
	c.clicked = false
	c.dblClick = false
	c.tripleClick = false
	c.rightClicked = false
	c.wheelY = 0
	c.wheelTaken = false
	c.typed = ""
	c.backspace = false
	c.wordBack = false
	c.enter = false
	c.tabPressed = false
	c.escPressed = false
	c.fullscreenReq = false
	c.keyPressed = 0
	c.pasted = ""
	c.copyReq = false
	c.cutReq = false
	c.undoReq = false
	c.redoReq = false
	c.dropped = ""
	c.hotkey = 0
	c.tipText = ""
	x, y, _ := sdl.GetMouseState()
	c.mouseX, c.mouseY = c.toLogical(x), c.toLogical(y)
	mods := sdl.GetModState()
	c.ctrlHeld = mods&sdl.KMOD_CTRL != 0
	c.shiftHeld = mods&sdl.KMOD_SHIFT != 0
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
	// !ctrlHeld: a Ctrl-held "hold" is meaningless for any bound key (Ctrl+key
	// is a chord, not a hold), and it MUST NOT accumulate when the bound key is
	// Backspace — else holding Ctrl+Backspace would fire the whole-field wipe
	// instead of word-deletes. ctrlHeld is fresh (set this BeginFrame at ~1314).
	if c.holdOn && c.keyHeld(c.holdKey) && !c.ctrlHeld {
		c.holdAcc += dt
	} else {
		c.holdAcc = 0
		c.holdFired = false
	}
}

// BeginDraw opens a real draw pass (App.Frame calls it before any screen
// draws): the visible-field order rebuilds from scratch as this frame's
// TextFields register. Split from BeginFrame so input-only skipped passes
// keep the last drawn frame's field order for Tab cycling (see BeginFrame).
func (c *Ctx) BeginDraw() {
	c.fieldSeq = c.fieldSeq[:0]
}

// NextCaretFlip reports the time until the focused caret next toggles
// visibility (ok=false when no field is focused). The experimental loop
// schedules a wake exactly at the flip instead of idle-rendering a static
// screen just to blink a cursor.
func (c *Ctx) NextCaretFlip() (time.Duration, bool) {
	if c.focusID == "" {
		return 0, false
	}
	d := caretBlink - c.caretAcc
	if d < 0 {
		d = 0
	}
	return d, true
}

// CaretVisible reports the current caret blink state (with whether any field
// is focused) — the experimental loop's skip check compares it against the
// state last drawn, so a flip that already happened forces the redraw that
// shows it.
func (c *Ctx) CaretVisible() (on, focused bool) {
	return c.caretOn, c.focusID != ""
}

// NextHoverDue reports the nearest PENDING hover deadline — a tooltip dwell
// (TooltipAfter) or a sprite hover-preview — so the experimental loop can wake
// and render the reveal even though a resting pointer generates no events.
// Deadlines already reached report ok=false: the reveal drew on the frame the
// dwell crossed, and a shown tooltip/preview needs no further wakes from here.
func (c *Ctx) NextHoverDue() (time.Duration, bool) {
	due, ok := time.Duration(0), false
	consider := func(d time.Duration) {
		if d > 0 && (!ok || d < due) {
			due, ok = d, true
		}
	}
	if c.tipHoverID != "" {
		dwell := c.tipHoverDelay // a custom TooltipAfterDelay dwell; 0 = the fixed default
		if dwell <= 0 {
			dwell = tooltipDwell
		}
		consider(dwell - time.Since(c.tipHoverSince))
	}
	if c.hoverPreviewOn && c.hoverID != "" {
		consider(c.hoverPreviewDelay - time.Since(c.hoverSince))
	}
	return due, ok
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
				// Click streak: same-spot clicks inside the window cycle
				// 1→2→3→1 (single/double/triple, native-editor style —
				// fields use double for word-select, triple for all).
				if now.Sub(c.lastClickAt) < doubleClickWindow && dx < doubleClickSlop && dy < doubleClickSlop {
					c.clickStreak = c.clickStreak%3 + 1
				} else {
					c.clickStreak = 1
				}
				c.dblClick = c.clickStreak == 2
				c.tripleClick = c.clickStreak == 3
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
				// Paste only into a focused field; with nothing focused let Ctrl+V
				// fall through to the configurable hotkeys (else a hotkey bound to
				// "v" — the Hide-desk default — is dead, clipboard ate it).
				if c.focusID != "" {
					if text, err := sdl.GetClipboardText(); err == nil && text != "" {
						c.pasted += flattenClipboard(text)
					}
				} else {
					c.hotkey = e.Keysym.Sym
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
				// Arm select-all: the focused field converts it into a REAL
				// whole-value selection (visible highlight; typing replaces,
				// backspace clears, Ctrl+C/X act on it — textField). (Kept
				// unconditional — select-all matters more than the Ctrl+A
				// Fav-emotes hotkey, which stays on the Extras button.)
				c.selectAll = true
			case sdl.K_BACKSPACE:
				// Word-delete only inside a focused field; with nothing focused
				// let Ctrl+Backspace fall through to the configurable hotkeys (so a
				// user-bound Ctrl+Backspace still works). Set unconditionally when
				// focused — the WordDelete pref is checked at CONSUMPTION (textField),
				// so a focused Ctrl+Backspace is always captured: pref-off is a
				// consumed no-op that never leaks to a hotkey mid-typing.
				if c.focusID != "" {
					c.wordBack = true
				} else {
					c.hotkey = e.Keysym.Sym
				}
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
// unprojection (main sets the matching renderer scale each frame). It also
// drives the #77 text DEVICE scale so chrome/log/chat glyphs rasterize at the
// final device size (crisp) instead of being bilinearly stretched by SetScale.
func (c *Ctx) SetUIScale(pct int) {
	if pct <= 0 {
		pct = DefaultScalePct
	}
	c.uiPct = int32(pct)
	c.SetTextDevScale(pct) // fold the global scale into font point size (#77 Part A)
}

// SetTextDevScale sets the DEVICE font scale (#77) used to rasterize text, and
// rebuilds the device chrome faces + bumps the set generation so device sets
// rebuild lazily. NO-OP when unchanged: this is the per-frame-safe entry (the
// export/split brackets call it every frame; only a real user scale change pays
// the rebuild/purge). Render thread.
func (c *Ctx) SetTextDevScale(pct int) {
	if pct <= 0 {
		pct = DefaultScalePct
	}
	if int32(pct) == c.textDevPct {
		return
	}
	c.textDevPct = int32(pct)
	c.rebuildDeviceChrome()
	// The device SETS key on textDevPct via fontSet.devPct, so a change makes
	// fontsForDev rebuild them; the label/emoji caches carry device-face pointers
	// that just changed identity — purge them (a scale change is a user action).
	c.purgeTextCache()
	clear(c.widthCache)
	clear(c.devWidthCache) // device-face field memo carries the OLD device point size
}

// rebuildDeviceChrome (re)opens the device-scaled chrome pair at
// UIFontSize×(textDevPct/100). At 100% (or when a face can't open) it SHARES the
// logical faces — no duplicate raster for the common case. Closes any prior
// non-shared device faces first. Render thread; called only on a scale change.
func (c *Ctx) rebuildDeviceChrome() {
	// Close previously-built (non-shared) device faces.
	if c.fontDev != nil && c.fontDev != c.font {
		c.fontDev.Close()
	}
	if c.fontBigDev != nil && c.fontBigDev != c.fontBig {
		c.fontBigDev.Close()
	}
	c.fontDev, c.fontBigDev = c.font, c.fontBig
	if c.textDevPct == DefaultScalePct || c.textDevPct == 0 {
		return // 1:1 — share the logical faces
	}
	size := UIFontSize * int(c.textDevPct) / DefaultScalePct
	bigSize := UIFontSizeBig * int(c.textDevPct) / DefaultScalePct
	// Build from the same source the logical chrome faces used: the custom
	// "font everywhere" bytes if set, else the embedded face.
	if c.chromeData != nil {
		if f, err := memFont(c.chromeData, size); err == nil {
			c.fontDev = f
		}
		if f, err := memFont(c.chromeData, bigSize); err == nil {
			c.fontBigDev = f
		}
		return
	}
	if f, err := loadEmbeddedFont(size); err == nil {
		c.fontDev = f
	}
	if f, err := loadEmbeddedFont(bigSize); err == nil {
		c.fontBigDev = f
	}
}

// toLogical converts window pixels to logical (pre-scale) coordinates. Unchanged
// by #77 Part A: ren.SetScale stays active, so mouse unprojection still divides
// by the same global scale.
func (c *Ctx) toLogical(v int32) int32 {
	if c.uiPct == 0 || c.uiPct == DefaultScalePct {
		return v
	}
	return v * DefaultScalePct / c.uiPct
}

// hovering reports whether the mouse is inside r.
func (c *Ctx) hovering(r sdl.Rect) bool {
	// An open dropdown (or the layout editor) owns the pointer entirely: while a
	// modal is up, EVERY other widget reads as not-hovered — including one drawn
	// directly UNDER the open list — so a click on the list can't also fall
	// through to it (the "click Random, the Character behind it gets clicked too"
	// bug). The modal owner uses the raw pointIn() hit test for its own interaction.
	if c.modalOn {
		return false
	}
	// A widget drawn under a pushClip only EXISTS inside that clip: the pointer
	// past the clip edge must not hover it. Clipping used to be draw-only, so a
	// list's half-culled bottom row still hit-tested BELOW its panel — hovering
	// the IC bar highlighted an invisible area row, and clicking FX transferred
	// you to that area (the "sent to the movie room" playtest bug).
	if c.clipOn && !(c.mouseX >= c.clipRect.X && c.mouseX < c.clipRect.X+c.clipRect.W &&
		c.mouseY >= c.clipRect.Y && c.mouseY < c.clipRect.Y+c.clipRect.H) {
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
	c.cgoRect = r // &local would escape through cgo and heap-allocate per call
	_ = c.Ren.FillRect(&c.cgoRect)
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
	c.cgoRect = r // &local would escape through cgo and heap-allocate per call
	_ = c.Ren.DrawRect(&c.cgoRect)
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
	// The previous clip comes from the mirror fields, NOT an SDL query:
	// GetClipRect's named-return pointer escapes through cgo and heap-allocates
	// per call. The mirror is authoritative because all NESTED clipping flows
	// through pushClip/popClip — the remaining raw Ren.SetClipRect sites are
	// leaf draw-only regions (the #31 audit) that never pushClip inside their
	// clip window.
	prev, had = c.clipRect, c.clipOn
	c.cgoRect = r // &local would escape through cgo and heap-allocate per call
	_ = c.Ren.SetClipRect(&c.cgoRect)
	// Mirror the clip in plain fields so hovering() can honour it without a cgo
	// query per hit test (input clipping — see hovering).
	c.clipOn, c.clipRect = true, r
	return prev, had
}

// popClip restores the clip captured by pushClip.
func (c *Ctx) popClip(prev sdl.Rect, had bool) {
	c.clipOn, c.clipRect = had, prev
	if had {
		c.cgoRect = prev // &prev would escape through cgo and heap-allocate per call
		_ = c.Ren.SetClipRect(&c.cgoRect)
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
	// Cache by the LOGICAL font (what callers pass) but RASTERIZE with its device
	// sibling (#77): the texture is device-sized, and cachedText.devPct records
	// the scale so blitLabel divides it back to logical. A textDevPct change
	// purges this whole cache (SetTextDevScale), so a stale-size entry can't leak.
	key := textKey{text: text, color: col, font: font}
	if t, ok := c.textCache[key]; ok {
		return t, true
	}
	dev := c.deviceTextFont(font) // chrome/set logical face → device-scaled face
	surf, err := dev.RenderUTF8Blended(text, col)
	if err != nil {
		return cachedText{}, false
	}
	defer surf.Free()
	if len(c.textCache) >= textCacheMax {
		c.purgeTextCache()
	}

	if tex, slot, ok := c.atlasPlace(surf); ok {
		entry := cachedText{tex: tex, src: slot, w: surf.W, h: surf.H, devPct: c.textDevPct}
		c.textCache[key] = entry
		return entry, true
	}

	// Atlas full or label oversized: dedicated texture fallback.
	tex, err := c.Ren.CreateTextureFromSurface(surf)
	if err != nil {
		return cachedText{}, false
	}
	entry := cachedText{tex: tex, src: sdl.Rect{W: surf.W, H: surf.H}, w: surf.W, h: surf.H, owned: true, devPct: c.textDevPct}
	c.textCache[key] = entry
	return entry, true
}

// deviceTextFont maps a LOGICAL rasterization face to its DEVICE-scaled sibling
// (#77). Chrome (c.font/c.fontBig) map to c.fontDev/c.fontBigDev; a scaled-set
// face routes through deviceFontFor. At textDevPct==100 every mapping is the
// identity (the device faces SHARE the logical ones). Render thread.
func (c *Ctx) deviceTextFont(font *ttf.Font) *ttf.Font {
	if c.textDevPct == DefaultScalePct || c.textDevPct == 0 {
		return font
	}
	switch font {
	case c.font:
		return c.fontDev
	case c.fontBig:
		return c.fontBigDev
	}
	// A picked chat/log set face: find its device sibling at the same per-element
	// pct. deviceFontFor returns font unchanged when the set can't be resolved.
	return c.deviceFontForAnyPct(font)
}

// deviceFontForAnyPct resolves a set face's device sibling without the caller
// knowing its per-element pct (the face carries its set, whose .pct is authoritative).
func (c *Ctx) deviceFontForAnyPct(font *ttf.Font) *ttf.Font {
	log, dev, idx := c.setIndexOf(font)
	if log == nil || idx < 0 {
		return font
	}
	df := c.fontsForDev(dev, log.pct)
	if idx < len(df) {
		return df[idx]
	}
	return font
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
	c.purgeEmojiCache() // emoji rasters carry the same now-dead primary-font pointers
}

// logicalW is the label's LOGICAL width (#77): the device texture width divided
// back down by the scale it was rasterized at. Callers lay out / clamp in this.
func (t cachedText) logicalW() int32 { return uiLogicalFromDevice(t.w, t.devPct) }

// uiLogicalFromDevice is the ui-package twin of render.logicalFromDevice — it
// MUST use the identical "round half up" rule (add half the divisor before
// dividing) so a kit label and a message raster of the same string agree to the
// pixel (the roadmap's flagged off-by-one at odd scales). Pinned equal by
// TestUILogicalFromDeviceMatchesRender.
func uiLogicalFromDevice(device, devPct int32) int32 {
	if devPct <= 0 || devPct == DefaultScalePct {
		return device
	}
	return (device*DefaultScalePct + devPct/2) / devPct
}

// uiDeviceFromLogical is the exact inverse of uiLogicalFromDevice: it maps a
// LOGICAL coordinate to the device pixel the renderer's SetScale(devPct/100)
// lands it on, using the SAME "round half up" rule (add half the divisor before
// dividing). #77 draws whole-string label textures into logical dst rects and
// lets SetScale resample them; the focused text field instead projects its own
// moving parts here and draws them under SetScale(1,1) so a fractional scale can
// never phase-shift or stretch them (strategy B — see textField's device path).
// At devPct==100 (or unset) it is the identity, matching uiLogicalFromDevice's
// twin at ui.go's documented rule. Pinned by TestUIDeviceFromLogical.
func uiDeviceFromLogical(logical, devPct int32) int32 {
	if devPct <= 0 || devPct == DefaultScalePct {
		return logical
	}
	return (logical*devPct + DefaultScalePct/2) / DefaultScalePct
}

// deviceExactText reports whether the focused text field should draw its moving
// parts (value texture, selection, caret) on the device-exact path (#77 S1/S2).
// Pure predicate over c.textDevPct so a test can pin the gate without a renderer:
// only a FRACTIONAL user scale needs it. At 100% (or unset) it is false and the
// field keeps every line of its pre-#77-fix scaled behavior byte-identical — no
// SetScale flip, no projection. c.textDevPct tracks the same UIScale main.go
// feeds ren.SetScale (SetUIScale → SetTextDevScale, ui.go:1469/1482), and the
// only path that brackets textDevPct away from that scale — the gif export —
// never draws a live textField (tickGifExport renders the scene + drawGifChatbox
// only), so the two can't diverge while this path runs.
func deviceExactText(devPct int32) bool {
	return devPct != DefaultScalePct && devPct != 0
}

// blitLabel copies a cached label (atlas sub-rect aware) through the scratch
// rects — zero heap escapes on the per-frame draw path. wLog is the LOGICAL
// destination width (a full label passes t.logicalW(); a clipped one a smaller
// logical maxW). #77: the SRC sub-rect stays in DEVICE px (the texture is
// device-sized), the DST in LOGICAL px (the renderer's SetScale maps it 1:1 onto
// device pixels — crisp). devPct==100 reproduces the pre-#77 1:1 blit.
func (c *Ctx) blitLabel(t cachedText, x, y, wLog int32) {
	srcW := wLog
	if t.devPct > 0 && t.devPct != DefaultScalePct {
		srcW = wLog * t.devPct / DefaultScalePct // logical width → device sample width
	}
	if srcW > t.w {
		srcW = t.w
	}
	c.drawSrc = sdl.Rect{X: t.src.X, Y: t.src.Y, W: srcW, H: t.h}
	c.drawDst = sdl.Rect{X: x, Y: y, W: wLog, H: uiLogicalFromDevice(t.h, t.devPct)}
	_ = c.Ren.Copy(t.tex, &c.drawSrc, &c.drawDst)
}

// Label draws text at (x, y) and returns its LOGICAL pixel width.
func (c *Ctx) Label(x, y int32, text string, col sdl.Color) int32 {
	t, ok := c.textTexture(text, col, c.font)
	if !ok {
		return 0
	}
	lw := t.logicalW()
	c.blitLabel(t, x, y, lw)
	return lw
}

// Heading draws large text.
func (c *Ctx) Heading(x, y int32, text string, col sdl.Color) {
	t, ok := c.textTexture(text, col, c.fontBig)
	if !ok {
		return
	}
	c.blitLabel(t, x, y, t.logicalW())
}

// LabelClipped draws text clipped to maxW.
func (c *Ctx) LabelClipped(x, y, maxW int32, text string, col sdl.Color) {
	c.LabelClippedFont(c.font, x, y, maxW, text, col)
}

// LabelClippedFont is LabelClipped with an explicit font (scaled log/OOC
// text). Cached like every label; the cache keys by font identity. The
// clip composes with the label's atlas sub-rect. maxW is LOGICAL px.
func (c *Ctx) LabelClippedFont(font *ttf.Font, x, y, maxW int32, text string, col sdl.Color) {
	t, ok := c.textTexture(text, col, font)
	if !ok {
		return
	}
	w := t.logicalW()
	if w > maxW {
		w = maxW
	}
	c.blitLabel(t, x, y, w)
}

// TextWidth measures a label in the chrome font, memoized — screens call
// it per frame for fixed labels and each miss is a CGO TTF measure. The
// memo shares the text cache's lifecycle (purged together, same bound).
//
// #77: measures the LOGICAL chrome face (c.font), NOT the device sibling — so
// the width is already in logical layout units (no ÷scale rounding needed here;
// the rounding rule lives only in the blit dst). This is exact for LAYOUT: the
// width is scale-invariant, which every chrome layout site (and
// TestTextWidthScaleInvariant) requires. It is NOT the device glyph seam: the
// device face carries the SAME advances scaled up, but SDL_ttf re-quantizes each
// glyph independently at the larger point size, so the folded logical width can
// drift from a device measurement over a long prefix. Text-field caret /
// selection / click metrics therefore measure the DEVICE face instead (see
// devTextWidth + fieldPrefixW; sibling contract render/text.go:568-570). Callers
// that lay out chrome stay on this logical face untouched.
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

// devTextWidth measures text on the DEVICE chrome face — the metric the drawn
// field texture uses at a fractional UI scale — returning DEVICE px. It is the
// field-metric twin of TextWidth (which measures the logical face for
// scale-invariant layout): the value texture is rasterized on deviceTextFont, and
// SDL_ttf quantizes each glyph's advance independently per point size, so only a
// device measure lands the caret on the actual glyph seam (kills the
// length-growing caret-to-glyph drift, #77 S1b). Mirrors render/text.go:568-570,
// where the non-ASCII field path already measures device advances and folds back.
//
// Memoized in its own map (raw device px), CAPPED by textCacheMax like widthCache
// and PURGED wherever widthCache is (SetTextDevScale, SetChromeFont) — so a
// stale-size entry after a scale change is impossible. Callers at 100% must NOT
// reach here: deviceTextFont is the identity there, so the widthCache path is
// used unchanged (see fieldPrefixW). One map probe per prefix; the map is
// lazily created (a nil-map write would panic on test-built Ctx literals).
func (c *Ctx) devTextWidth(text string) int32 {
	if c.font == nil {
		return 0 // headless tests; real Ctx always has the chrome font
	}
	if w, ok := c.devWidthCache[text]; ok {
		return w
	}
	w, _, err := c.deviceTextFont(c.font).SizeUTF8(text)
	if err != nil {
		return 0
	}
	if c.devWidthCache == nil {
		c.devWidthCache = make(map[string]int32, textCacheMax)
	} else if len(c.devWidthCache) >= textCacheMax {
		c.devWidthCache = make(map[string]int32, textCacheMax)
	}
	c.devWidthCache[text] = int32(w)
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
	// Chrome SHAPE (A5): a non-sharp preset reshapes the button silhouette via
	// 9-sliced alpha masks; the label then insets by shapeLabelInset so a glyph
	// never tucks under a corner curve. The hit-test rect (hovering above) is
	// unchanged. On "sharp" (or before masks are resident) shaped==false and the
	// body below is the LITERAL pre-A5 Fill+Border+label — byte-identical, so the
	// settled-frame 0-alloc gate (default sharp) is untouched by construction.
	shaped := c.activeShape != shapeSharp && c.activeShape != "" && c.shapeMaskReady
	if shaped {
		c.FillShaped(r, col)
		c.borderShaped(r, border)
	} else {
		c.Fill(r, col)
		c.Border(r, border)
	}
	if t, ok := c.textTexture(label, text, c.font); ok {
		// Clip to the button: tiny themed rects must never leak their
		// label over the neighbors (Qt elided these). All #77-LOGICAL px.
		lw, lh := t.logicalW(), uiLogicalFromDevice(t.h, t.devPct)
		w, x := lw, r.X+(r.W-lw)/2
		// A shaped button reserves shapeLabelInset extra px each side (the sharp
		// path keeps its original r.W-8 clamp exactly — no change on the default).
		clampMargin := int32(8)
		edge := int32(4)
		if shaped {
			clampMargin = 8 + 2*shapeLabelInset
			edge = 4 + shapeLabelInset
		}
		if maxW := r.W - clampMargin; w > maxW && maxW > 0 {
			w, x = maxW, r.X+edge
		}
		c.blitLabel(t, x, r.Y+(r.H-lh)/2, w)
	}
	return hover && c.clicked
}

// Checkbox draws a toggle; returns the (possibly flipped) value.
func (c *Ctx) Checkbox(x, y int32, label string, value bool) bool {
	if c.onRow != nil {
		c.onRow(label, y) // settings-search collect pass (#26 gather): record and draw as normal
	}
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

// WheelIn returns this frame's wheel ticks when the cursor is inside r and no
// earlier widget consumed them, else 0 — scrollables only react under the
// pointer (playtest: the music list scrolled on wheel from anywhere on screen),
// and only ONE scrollable reacts per frame. A hit marks the wheel taken, which
// now fences every LATER WheelIn too, not just the page-level handlers that
// check wheelTaken — two stacked surfaces could otherwise both scroll (the
// What's New modal scrolled AND the lobby list behind it scrolled). Priority
// follows processing order: pre-screen overlay handlers run first and win;
// anything drawn later that should win instead blinds the passes beneath it
// via the pointer fence (fencePointer / boxFencesPointer / modalOn).
func (c *Ctx) WheelIn(r sdl.Rect) int32 {
	if c.wheelTaken || c.wheelY == 0 || !c.hovering(r) {
		return 0
	}
	c.wheelTaken = true
	return c.wheelY
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

// fieldFonts carries the optional emoji/unicode-aware faces for a text field. Zero value
// (both nil) = the plain single-font path (TextField / PasswordField), byte-identical to
// before. TextFieldEmoji passes a fallback-capable primary (a chat/log-set face, whose
// embedded member IS the chrome font at 1:1) + the colour-emoji face, so what you TYPE shows
// real glyphs instead of tofu — used by the IC / OOC inputs.
type fieldFonts struct{ primary, emoji *ttf.Font }

// TextField edits value in place; id keys focus. Returns (newValue,
// enterPressed).
func (c *Ctx) TextField(id string, r sdl.Rect, value string, placeholder string) (string, bool) {
	return c.textField(id, r, value, placeholder, false, fieldFonts{})
}

// TextFieldEmoji is TextField that renders typed emoji / non-Latin runes through the
// per-glyph fallback raster (primary = a chat/log-set face, emoji = the colour-emoji face),
// so the input box doesn't show tofu while you type them. Plain ASCII stays on the exact
// single-font fast path (the caret math is unchanged), so the common case is byte-identical.
func (c *Ctx) TextFieldEmoji(id string, r sdl.Rect, value, placeholder string, primary, emoji *ttf.Font) (string, bool) {
	return c.textField(id, r, value, placeholder, false, fieldFonts{primary: primary, emoji: emoji})
}

// PasswordField is TextField rendered as asterisks (screenshare-safe).
// Ctrl+C/X never put the secret on the clipboard either — clipboard
// viewers are just as visible on a stream; Ctrl+X still clears.
func (c *Ctx) PasswordField(id string, r sdl.Rect, value string, placeholder string) (string, bool) {
	return c.textField(id, r, value, placeholder, true, fieldFonts{})
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

// editInput is one frame of edits to a focused text field. selStart/selEnd
// carry the active selection as a rune range [selStart, selEnd) — selEnd <=
// selStart means none. Ctrl+A / double-click resolve to a real range before
// this runs, so editStep has exactly one selection representation.
type editInput struct {
	typed            string // inserted text (typed + pasted)
	back             bool   // backspace (delete the rune before the caret)
	wordBack         bool   // Ctrl+Backspace: delete the preceding word (trailing whitespace + the run before it)
	op               editOp // caret move or forward delete
	selStart, selEnd int    // active selection (rune range); selEnd <= selStart = none
}

// wordDeleteBoundary returns the rune index a Ctrl+Backspace at pos should
// delete back to: it skips any trailing whitespace run immediately before the
// caret, then the non-whitespace run before that (the "word"). The result is
// the new caret position; [result, pos) is the range to remove. Pure and
// rune-aware (never byte-slices mid-rune) so it unit-tests without SDL. pos at
// or before 0 returns 0 (nothing to delete). This matches the near-universal
// editor convention (delete the word to the left, plus the spaces glued to it).
func wordDeleteBoundary(runes []rune, pos int) int {
	if pos > len(runes) {
		pos = len(runes)
	}
	i := pos
	for i > 0 && unicode.IsSpace(runes[i-1]) { // eat the whitespace run before the caret
		i--
	}
	for i > 0 && !unicode.IsSpace(runes[i-1]) { // then eat the non-whitespace word
		i--
	}
	return i
}

// editStep applies one frame of edits to value at caret (a RUNE index), returning
// the new value and caret. Pure and rune-aware (multibyte shownames — Häschen,
// fünfzehn, 🍅 — so the caret is by rune, never by byte), so it carries all the
// edit logic that the draw path (which needs a renderer) can't unit-test.
// Native selection semantics: insert/backspace/delete replace the range;
// plain Left/Right collapse to the range's edge; Home/End collapse to the
// text's ends. (Shift-extension keeps the anchor OUT of the input — the
// caller moves only the caret.)
func editStep(value string, caret int, in editInput) (string, int) {
	runes := []rune(value)
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	selA, selB := in.selStart, in.selEnd
	if selA < 0 {
		selA = 0
	}
	if selB > len(runes) {
		selB = len(runes)
	}
	if sel := selB > selA; sel {
		// A word-delete with an active selection deletes just the selection,
		// exactly like a plain backspace does (in.wordBack joins in.back here).
		if in.typed != "" || in.back || in.wordBack || in.op == editDelete {
			t := []rune(in.typed)
			out := make([]rune, 0, len(runes)-(selB-selA)+len(t))
			out = append(out, runes[:selA]...)
			out = append(out, t...)
			out = append(out, runes[selB:]...)
			return string(out), selA + len(t)
		}
		switch in.op {
		case editLeft:
			return value, selA // collapse to the selection's left edge
		case editRight:
			return value, selB // …or its right edge
		case editHome:
			return value, 0
		case editEnd:
			return value, len(runes)
		}
		return value, caret
	}
	switch {
	case in.typed != "":
		t := []rune(in.typed)
		out := make([]rune, 0, len(runes)+len(t))
		out = append(out, runes[:caret]...)
		out = append(out, t...)
		out = append(out, runes[caret:]...)
		return string(out), caret + len(t)
	case in.wordBack && caret > 0:
		// Ctrl+Backspace: delete the whole preceding word (whitespace + word run).
		start := wordDeleteBoundary(runes, caret)
		out := make([]rune, 0, len(runes)-(caret-start))
		out = append(out, runes[:start]...)
		out = append(out, runes[caret:]...)
		return string(out), start
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

func (c *Ctx) textField(id string, r sdl.Rect, value string, placeholder string, mask bool, fb fieldFonts) (string, bool) {
	c.fieldSeq = append(c.fieldSeq, id) // Tab-cycle order = draw order
	hover := c.hovering(r)
	// Out-of-band change detection + this field's undo history
	// (fieldhistory.go): a macro fill, a palette command template, or the
	// own-echo IC clear lands in the history HERE — whatever changed since
	// this field's last draw, focused or not — so Ctrl+Z brings it back.
	hist := c.fieldTrack(id, value)
	press := c.mouseDown && !c.prevMouseDown
	if press && hover && c.dragID == "" {
		// Focus moves on the PRESS (native), so a drag-selection starts
		// immediately; the release block below still covers click-away
		// unfocus and synthetic clicks.
		c.selectAll = false
		c.focusID = id
	}
	if c.clicked && c.dragID != id { // our drag-select's release is not a click: never unfocus/refocus on it
		c.selectAll = false // any single click drops a pending select-all
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
			c.selAnchor = -1 // fresh focus: no selection
		}
		if c.caret > rc {
			c.caret = rc
		}
		if c.caret < 0 {
			c.caret = 0
		}
		if c.selAnchor > rc {
			c.selAnchor = rc // value shrank under a live selection
		}
		// Ctrl+A becomes a REAL whole-value selection (highlight + native
		// replace/copy/cut semantics), not just an armed flag.
		if c.selectAll {
			c.selAnchor, c.caret = 0, rc
			c.selectAll = false
		}
		// Mouse: the press places the caret AND anchors a selection; holding
		// drags the caret end (auto-scrolls via the keep-caret-visible
		// scroll); shift+press extends from the current caret. Measured on
		// the display through the SAME raster the field draws with
		// (fieldRaster), so non-Latin text maps clicks to the right rune
		// (the Cyrillic caret bug).
		if (press && hover && c.dragID == "") || (c.dragID == id && c.mouseDown) {
			pre := maskOf(value)
			preRaster := c.fieldRaster(fb, mask, pre)
			preRC := utf8.RuneCountInString(pre)
			preScroll := scrollFor(c.fieldPrefixW(pre, preRaster, preRC), c.fieldPrefixW(pre, preRaster, c.caret), avail)
			idx := c.fieldIndexAtX(pre, preRaster, c.mouseX-(r.X+padX)+preScroll)
			if press && hover && c.dragID == "" {
				if c.shiftHeld {
					if c.selAnchor < 0 {
						c.selAnchor = c.caret // shift+click: extend from the old caret
					}
				} else {
					c.selAnchor = idx
				}
				c.dragID = id // own the drag: selecting keeps working past the box edge
			}
			c.caret = idx
		}
		// Double-click selects the word under the caret, triple-click the
		// whole value (native). A masked field has no words — both select all.
		if c.dblClick && hover {
			if mask {
				c.selAnchor, c.caret = 0, rc
			} else {
				lo, hi := wordBoundsAt([]rune(value), c.caret)
				c.selAnchor, c.caret = lo, hi
			}
		}
		if c.tripleClick && hover {
			c.selAnchor, c.caret = 0, rc
		}
		// Ctrl+Z / Ctrl+Y (routed into undoReq/redoReq pre-screen): step this
		// field's history. Consumed even when the stack is empty — the chord
		// was aimed at the focused field, never at a z/y-bound hotkey.
		if c.undoReq || c.redoReq {
			if snap, ok := hist.step(value, c.caret, c.redoReq); ok {
				value = snap.value
				rc = utf8.RuneCountInString(value)
				c.caret = snap.caret
				if c.caret > rc {
					c.caret = rc
				}
				c.selAnchor = -1
			}
			c.undoReq, c.redoReq = false, false
		}
		selLo, selHi := c.fieldSel(rc)
		if c.copyReq && !mask {
			if selLo < selHi { // copy the selection when one exists, else the whole value
				_ = sdl.SetClipboardText(string([]rune(value)[selLo:selHi]))
			} else if value != "" {
				_ = sdl.SetClipboardText(value)
			}
		}
		prevVal, prevCaret := value, c.caret // undo snapshot base for this frame's edits
		switch {
		case c.holdOn && c.holdAcc >= c.holdThreshold && !c.holdFired && value != "":
			// Hold-to-clear: the bound key (default Backspace) held past the
			// threshold wipes the whole field at once — no slow char-by-char.
			c.holdFired = true
			value, c.caret = "", 0
			c.selAnchor = -1
		case c.cutReq:
			if selLo < selHi { // native cut: copy + remove the SELECTION when one exists
				if !mask {
					_ = sdl.SetClipboardText(string([]rune(value)[selLo:selHi]))
				}
				value, c.caret = editStep(value, c.caret, editInput{op: editDelete, selStart: selLo, selEnd: selHi})
			} else {
				if value != "" && !mask {
					_ = sdl.SetClipboardText(value)
				}
				value, c.caret = "", 0
			}
			c.selAnchor = -1
			c.selectAll = false
		default:
			// wordBack is gated on the WordDelete pref HERE (at consumption): with
			// the pref off, a focused Ctrl+Backspace was still captured (wordBack
			// set, never routed to c.hotkey), so it becomes a consumed no-op — it
			// can't leak into a Ctrl+Backspace hotkey mid-typing.
			in := editInput{typed: c.typed + c.pasted, back: c.backspace, wordBack: c.wordBack && c.wordDeleteOn}
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
			if in.typed != "" || in.back || in.wordBack || in.op != editNone {
				nav := in.op == editLeft || in.op == editRight || in.op == editHome || in.op == editEnd
				extend := nav && in.typed == "" && !in.back && !in.wordBack && c.shiftHeld
				if extend {
					// Shift+arrow/Home/End extends: fix the anchor, move only
					// the caret (the selection is anchor..caret).
					if c.selAnchor < 0 {
						c.selAnchor = c.caret
					}
				} else {
					in.selStart, in.selEnd = selLo, selHi // edits replace / plain nav collapses
				}
				value, c.caret = editStep(value, c.caret, in)
				if !extend {
					c.selAnchor = -1
				}
				switch c.keyPressed { // consume nav keys so char keybinds don't also fire
				case sdl.K_LEFT, sdl.K_RIGHT, sdl.K_HOME, sdl.K_END, sdl.K_DELETE:
					c.keyPressed = 0
				}
			}
		}
		if value != prevVal {
			hist.record(prevVal, prevCaret, value, time.Now()) // the field's own edits feed the history too
		}
		if c.enter {
			enter = true
		}
	}
	// Track the drawn value for the out-of-band detector (both branches: an
	// unfocused field must notice a rewrite too). lastCaret only matters as a
	// restore position, so it follows the live caret while focused.
	hist.lastKnown = value
	if focused {
		hist.lastCaret = c.caret
	}

	display := maskOf(value)
	// The fallback raster the value will DRAW with (nil = single-font path).
	// Resolved ONCE here so every measurement below — caret x, full width, the
	// keep-caret-visible scroll — reads the same glyph advances the draw blits.
	// Measuring with the chrome font while drawing with a fallback face put the
	// caret several letters off in Cyrillic (the playtest report).
	fbRaster := c.fieldRaster(fb, mask, display)
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
	fullW := c.fieldPrefixW(display, fbRaster, utf8.RuneCountInString(display))
	scroll, caretX := int32(0), int32(0)
	if focused {
		caretX = c.fieldPrefixW(display, fbRaster, c.caret)
		scroll = scrollFor(fullW, caretX, avail)
	}
	// The focused ASCII field draws its moving parts (selection, value texture,
	// caret) DEVICE-EXACT at a fractional UI scale (#77 S1/S2): under a fractional
	// ren.SetScale, the whole-string label texture would resample at a NEW
	// sub-pixel phase every keystroke as the scroll advances (the "shimmer"), and
	// the 2px caret's device column count would flip 2↔3 with that phase (the
	// "stretch"). devFieldValue brackets SetScale(1,1) and projects each rect to
	// device pixels itself, so nothing phase-shifts. The gate is fractional-scale
	// only; at 100% and on the fallback-raster / placeholder / unfocused paths this
	// is false and the pre-fix scaled draws below run byte-identically.
	devExact := focused && fbRaster == nil && show == display && deviceExactText(c.textDevPct)

	// Selection highlight, UNDER the text: the ordered anchor..caret range,
	// measured through the same raster the glyphs draw with. A focused field
	// already pays per-frame measurement for the caret; the selection adds
	// two prefix widths only WHILE one exists.
	selX0, selX1, hasSel := int32(0), int32(0), false
	if focused {
		if selLo, selHi := c.fieldSel(utf8.RuneCountInString(display)); selLo < selHi {
			x0 := c.fieldPrefixW(display, fbRaster, selLo) - scroll
			x1 := c.fieldPrefixW(display, fbRaster, selHi) - scroll
			if x0 < 0 {
				x0 = 0
			}
			if x1 > avail {
				x1 = avail
			}
			if x1 > x0 {
				hasSel, selX0, selX1 = true, x0, x1
				if !devExact {
					c.Fill(sdl.Rect{X: r.X + padX + x0, Y: r.Y + 3, W: x1 - x0, H: r.H - 6},
						sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 90})
				}
			}
		}
	}
	if devExact {
		// One SetScale(1,1) bracket for all three moving parts (selection under,
		// value texture, caret over) — the label blits 1:1 from its device-sized
		// texture (zero resample) and the caret gets a constant integer device
		// width. Skips the scaled selection Fill above and the scaled value/caret
		// draws below. The caret consumes the RAW DEVICE prefix width (caretXDev,
		// same device face as the drawn texture) so it lands on the exact device
		// glyph seam — folding to logical and re-projecting would lose up to 1
		// device px of the per-glyph quantization the texture preserves (#77 S1b).
		caretXDev := c.caretPixelXDev(show, c.caret)
		c.devFieldValue(r, padX, avail, scroll, caretXDev, textY, show, col, selX0, selX1, hasSel && focused, focused && c.caretOn)
		return value, enter
	}
	// #M5 emoji/unicode input: when the field opted in (IC/OOC) and the text has any
	// non-ASCII rune, draw it through the per-glyph fallback raster so emoji + non-Latin
	// scripts show real glyphs instead of the chrome font's tofu. Plain ASCII (the common
	// case) and every other field stay on the exact single-font path below — caret math is
	// unchanged, so it's approximate only for the rare wide glyph (far better than tofu).
	drawn := false
	if fbRaster != nil && show == display {
		cp, ch := c.pushClip(sdl.Rect{X: r.X + padX, Y: r.Y, W: avail, H: r.H})
		fbRaster.Draw(c.Ren, fbRaster.TotalRunes(), r.X+padX-scroll, r.Y+(r.H-fbRaster.Height())/2)
		c.popClip(cp, ch)
		drawn = true
	}
	// The PLACEHOLDER (shown empty + unfocused) also tofus on the chrome font for
	// non-ASCII — e.g. a saved Japanese showname greyed in the empty showname box.
	// Route it through the same per-glyph raster on an opted-in field (fb.primary !=
	// nil), keyed by the dim colour so the value/placeholder rasters never collide.
	// Cached + self-invalidating like the value raster (one map hit/frame, no
	// per-frame alloc); plain-ASCII placeholders stay on the single-font path.
	if !drawn && show == placeholder && show != "" && fb.primary != nil && !isASCII(show) {
		if m := c.emojiRaster(show, col, fb.primary, fb.emoji); m != nil {
			cp, ch := c.pushClip(sdl.Rect{X: r.X + padX, Y: r.Y, W: avail, H: r.H})
			m.Draw(c.Ren, m.TotalRunes(), r.X+padX, r.Y+(r.H-m.Height())/2)
			c.popClip(cp, ch)
			drawn = true
		}
	}
	if !drawn {
		if scroll > 0 {
			// Clip to the field interior so the scrolled-off head doesn't spill left.
			cp, ch := c.pushClip(sdl.Rect{X: r.X + padX, Y: r.Y, W: avail, H: r.H})
			c.Label(r.X+padX-scroll, textY, show, col)
			c.popClip(cp, ch)
		} else {
			c.LabelClipped(r.X+padX, textY, avail, show, col)
		}
	}
	if focused && c.caretOn {
		c.Fill(sdl.Rect{X: r.X + padX + caretX - scroll, Y: r.Y + 4, W: fieldCaretWLog, H: r.H - 8}, ColText)
	}
	return value, enter
}

// devFieldValue draws a focused ASCII text field's moving parts DEVICE-EXACT at a
// fractional UI scale (#77 S1/S2). It follows the split-log precedent
// (app.go:4366-4378): read the renderer's live scale, flip SetScale(1,1), draw
// with rects projected to device pixels by uiDeviceFromLogical (round half up —
// the twin of the label blit's rounding), then restore the scale and the prior
// clip. Under SetScale(1,1) the value texture blits src→dst 1:1 (no resample, so
// strictly crisper than the ambient fractional-scale blit) and the caret gets a
// constant integer device width (no 2↔3 flicker). Render thread; allocation-free
// (sdl.Rect VALUES + the Ctx scratch rects — a &stackRect into a Ren call would
// heap-escape through cgo every frame, the cgoRect lesson).
//
// selX0/selX1 are the selection's interior-relative logical offsets (0..avail,
// already clamped); the caller passes hasSel/caretOn already ANDed with focus.
// caretXDev is the caret's prefix width in RAW DEVICE px (measured on the same
// device face as the value texture) — NOT logical: the caret adds it to the value
// texture's device origin so it lands on the exact device glyph seam (#77 S1b).
func (c *Ctx) devFieldValue(r sdl.Rect, padX, avail, scroll, caretXDev, textY int32, show string, col sdl.Color, selX0, selX1 int32, hasSel, caretOn bool) {
	dev := c.textDevPct
	// The device clip = the field interior projected to device px, so the
	// scrolled-off head still can't spill left. Set the SDL clip DIRECTLY (not
	// pushClip, which mirrors a LOGICAL rect for hovering()): device draws never
	// hit-test, so the mirror must keep reflecting the ambient logical clip. Save
	// the prior clip from the mirror and restore it after the scale is back.
	prevClip, prevOn := c.clipRect, c.clipOn
	clipX := uiDeviceFromLogical(r.X+padX, dev)
	clipY := uiDeviceFromLogical(r.Y, dev)
	clipRt := uiDeviceFromLogical(r.X+padX+avail, dev)
	clipBt := uiDeviceFromLogical(r.Y+r.H, dev)

	// Read the renderer's ACTUAL scale to restore it (ground truth, matching
	// app.go's pinned-pass restore); flip to 1:1 for the device-projected draws.
	sx, sy := c.Ren.GetScale()
	c.cgoRect = sdl.Rect{X: clipX, Y: clipY, W: clipRt - clipX, H: clipBt - clipY}
	_ = c.Ren.SetScale(1, 1)
	_ = c.Ren.SetClipRect(&c.cgoRect)

	// Selection highlight, UNDER the text. Project both edges independently so the
	// fill spans exactly the projected glyph gap (consistent with the value
	// texture, which starts at the same projected origin).
	if hasSel && selX1 > selX0 {
		sx0 := uiDeviceFromLogical(r.X+padX+selX0, dev)
		sx1 := uiDeviceFromLogical(r.X+padX+selX1, dev)
		sTop := uiDeviceFromLogical(r.Y+3, dev)     // +3/-6: the selection inset used by the scaled path
		sBot := uiDeviceFromLogical(r.Y+r.H-3, dev) // (r.H-6 tall, 3px from each edge)
		c.Fill(sdl.Rect{X: sx0, Y: sTop, W: sx1 - sx0, H: sBot - sTop},
			sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 90})
	}

	// ox is the value texture's DEVICE origin (the projected left edge of the text
	// after scroll). The caret shares this exact anchor below: the value texture and
	// the caret metric are both measured on the device face, so caretXDev device px
	// into the texture IS the glyph seam. Hoisted so that shared anchor is visible.
	//
	// UNIT SPLIT (deliberate, #77 S1b): the caret consumes RAW DEVICE caretXDev
	// (ox+caretXDev) with no logical round-trip, so it lands on the true device
	// seam. scroll and the selection edges stay on folded-LOGICAL widths — a
	// sub-pixel error there only shifts which texture column the 1:1 blit starts
	// at (it cannot resample), so the cheaper folded path is exact enough for them.
	ox := uiDeviceFromLogical(r.X+padX-scroll, dev)

	// Value texture, blitted 1:1 from its DEVICE-sized cache entry at the projected
	// text origin — zero resampling, so the string can't shimmer as the scroll
	// (hence the origin's sub-pixel phase) advances per keystroke.
	if t, ok := c.textTexture(show, col, c.font); ok {
		oy := uiDeviceFromLogical(textY, dev)
		c.drawSrc = sdl.Rect{X: t.src.X, Y: t.src.Y, W: t.w, H: t.h}
		c.drawDst = sdl.Rect{X: ox, Y: oy, W: t.w, H: t.h} // device→device 1:1
		_ = c.Ren.Copy(t.tex, &c.drawSrc, &c.drawDst)
	}

	// Caret, OVER the text, at ox + caretXDev (the device glyph seam — same anchor
	// and same device face as the value texture, so it can't drift as the prefix
	// grows). Its width is fieldCaretWLog projected with the round-half-up rule: a
	// CONSTANT integer per scale (125%→3, 150%→3 device px), so it can't flip 2↔3
	// as its phase drifts. Y/H project the same +4/-8 inset.
	if caretOn {
		cx := ox + caretXDev
		cTop := uiDeviceFromLogical(r.Y+4, dev)
		cBot := uiDeviceFromLogical(r.Y+r.H-4, dev) // r.H-8 tall, 4px from each edge
		cw := uiDeviceFromLogical(fieldCaretWLog, dev)
		c.Fill(sdl.Rect{X: cx, Y: cTop, W: cw, H: cBot - cTop}, ColText)
	}

	// Restore the ambient render scale FIRST (SDL clip rects are in scaled
	// coordinates), then the prior clip exactly as popClip would.
	_ = c.Ren.SetScale(sx, sy)
	if prevOn {
		c.cgoRect = prevClip
		_ = c.Ren.SetClipRect(&c.cgoRect)
	} else {
		_ = c.Ren.SetClipRect(nil)
	}
}

// isASCII reports whether s is all 7-bit ASCII — the cheap gate that keeps a plain text
// field on the single-font fast path (no per-frame font work) until it actually holds
// emoji / non-Latin runes.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// fieldRaster returns the fallback raster a text field draws its VALUE with
// (nil = the single-font Label path). One resolution point, shared by the draw
// and every caret/scroll measurement, so the two can never use different fonts.
func (c *Ctx) fieldRaster(fb fieldFonts, mask bool, display string) *render.MessageRaster {
	if fb.primary == nil || mask || display == "" || isASCII(display) {
		return nil
	}
	return c.emojiRaster(display, ColText, fb.primary, fb.emoji)
}

// fieldPrefixW is the LOGICAL width of display's first n runes as the field
// will draw them: the raster's own advances when one is in play, else the chrome
// font. On the ASCII path at a fractional scale it measures the DEVICE chrome
// face (what the drawn texture uses) and folds back to logical — so caretX,
// scroll and the selection all agree with the device-exact glyph seam, not the
// logical-face approximation (#77 S1b). At 100% the device face is the identity,
// so it stays on the logical widthCache path unchanged.
func (c *Ctx) fieldPrefixW(display string, m *render.MessageRaster, n int) int32 {
	if m != nil {
		return m.PrefixWidth(n)
	}
	if deviceExactText(c.textDevPct) {
		return uiLogicalFromDevice(c.caretPixelXDev(display, n), c.textDevPct)
	}
	return c.caretPixelX(display, n)
}

// fieldIndexAtX maps a click x (relative to the text's left edge) to the
// nearest rune boundary under the same metrics the field draws with.
func (c *Ctx) fieldIndexAtX(display string, m *render.MessageRaster, relX int32) int {
	if m == nil {
		if deviceExactText(c.textDevPct) {
			// Hit-test on the SAME device-face metric the caret/selection use, or a
			// click lands off by exactly the drift this fix removes (#77 point 4).
			return c.caretIndexAtXDev(display, relX)
		}
		return c.caretIndexAtX(display, relX)
	}
	if relX <= 0 {
		return 0
	}
	n := m.TotalRunes()
	prevW := int32(0)
	for i := 1; i <= n; i++ {
		w := m.PrefixWidth(i)
		if relX < (prevW+w)/2 {
			return i - 1
		}
		prevW = w
	}
	return n
}

// scrollFor is the keep-the-caret-visible horizontal scroll for a field of
// interior width avail (caret roughly centered once the text overflows).
// Stateless (deterministic per caret), so it never jitters frame to frame.
func scrollFor(fullW, caretX, avail int32) int32 {
	if fullW <= avail || avail <= 0 {
		return 0
	}
	s := caretX - avail/2
	if s < 0 {
		s = 0
	}
	if m := fullW - avail; s > m {
		s = m
	}
	return s
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

// caretPixelXDev is caretPixelX measured on the DEVICE chrome face, returning
// raw DEVICE px — the exact seam of the field's device-rasterized texture. The
// caret draw consumes this directly (no logical round-trip); fieldPrefixW folds
// it to logical for the scroll/selection consumers. Only reached at a fractional
// scale (the ASCII device-exact field path). Early return before any []rune/probe
// so an empty focused field stays zero-alloc.
func (c *Ctx) caretPixelXDev(display string, caret int) int32 {
	if caret <= 0 || display == "" {
		return 0
	}
	runes := []rune(display)
	if caret > len(runes) {
		caret = len(runes)
	}
	return c.devTextWidth(string(runes[:caret]))
}

// caretIndexAtXDev is caretIndexAtX on the DEVICE-face metric folded back to
// logical: relX is a LOGICAL click offset (mouse coords are logical), and the
// caret/selection sit on the folded-logical device widths, so hit-testing against
// those same folded widths lands the click on the drawn glyph seam (#77 point 4).
func (c *Ctx) caretIndexAtXDev(display string, relX int32) int {
	if relX <= 0 || display == "" {
		return 0
	}
	runes := []rune(display)
	prevW := int32(0)
	for i := 1; i <= len(runes); i++ {
		w := uiLogicalFromDevice(c.devTextWidth(string(runes[:i])), c.textDevPct)
		if relX < (prevW+w)/2 {
			return i - 1
		}
		prevW = w
	}
	return len(runes)
}

// SetHoldClear stamps the hold-to-clear config for the frame (App resolves it
// from prefs). The accumulation runs in BeginFrame; the focused field clears.
func (c *Ctx) SetHoldClear(on bool, key sdl.Keycode, threshold time.Duration) {
	c.holdOn, c.holdKey, c.holdThreshold = on, key, threshold
}

// SetWordDelete stamps the Ctrl+Backspace word-delete pref for the frame (App
// resolves it from prefs). textField reads the stamped field to gate the
// captured Ctrl+Backspace at consumption — without this stamp the gate would
// read the zero value and the feature would be dead despite defaulting ON.
func (c *Ctx) SetWordDelete(on bool) {
	c.wordDeleteOn = on
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
	thumbW  int32                     // per-row thumbnail strip width (0 = text-only)
	thumb   func(idx int, r sdl.Rect) // paints the idx-th row's thumbnail (deferred, in FinishFrame)
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
	return c.dropdownEx(id, r, options, cur, 0, 0, nil)
}

// DropdownThumbs is Dropdown with a thumbnail strip down the left of each open
// row: rowHWant sets the (taller) open-row height, thumbW the strip width, and
// thumb paints the idx-th row's image at end-of-frame (it shares the deferred
// overlay paint, so the thumbnails sit above everything like the list itself).
// The closed control is unchanged. thumb runs on the render thread in
// FinishFrame, so an on-demand cachedPage/Prefetch from it is safe.
func (c *Ctx) DropdownThumbs(id string, r sdl.Rect, options []string, cur int, rowHWant, thumbW int32, thumb func(idx int, r sdl.Rect)) (int, bool) {
	return c.dropdownEx(id, r, options, cur, rowHWant, thumbW, thumb)
}

func (c *Ctx) dropdownEx(id string, r sdl.Rect, options []string, cur int, rowHWant, thumbW int32, thumb func(idx int, r sdl.Rect)) (int, bool) {
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
	labelX := r.X + 6
	if thumbW > 0 && thumb != nil { // a thumbnail of the CURRENT pick on the closed control, sized to its height
		tw := (r.H - 4) * 4 / 3
		thumb(cur, sdl.Rect{X: r.X + 2, Y: r.Y + 2, W: tw, H: r.H - 4})
		labelX = r.X + 2 + tw + 4
	}
	if t, ok := c.textTexture(options[cur], ColText, c.font); ok {
		w := t.logicalW() // #77-LOGICAL px
		if maxW := r.X + r.W - 16 - labelX; w > maxW && maxW > 0 {
			w = maxW
		}
		c.blitLabel(t, labelX, r.Y+(r.H-uiLogicalFromDevice(t.h, t.devPct))/2, w)
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
	if rowHWant > rowH { // thumbnail rows want to be taller than the closed control
		rowH = rowHWant
	}
	visible := int32(len(options))
	if visible > ddMaxVisibleRows {
		visible = ddMaxVisibleRows
	}
	listW := r.W
	for _, o := range options {
		if w := c.TextWidth(o) + 16 + thumbW; w > listW { // leave room for the thumbnail strip
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

	// Modal capture: while open, the dropdown owns the pointer until the close
	// releases it (next BeginFrame). hovering() blanks for EVERY other widget —
	// even one drawn directly under the list — so a click on a list row can't also
	// fall through to it. The dropdown uses the raw pointIn() hit test below for
	// its own interaction (the fence would otherwise blank it too).
	c.modalOn = true
	// Publish the open list rect for boxFencesPointer: the list paints ABOVE the
	// floating panels (FinishFrame), so input must follow the visuals — while the
	// cursor is over the list, the courtroom pass must not run pointer-blind, or a
	// list flipped up over a torn tab has dead rows (the custom-layout playtest bug).
	c.ddOpenList = list

	// Wheel scrolls long lists (raw hit test, since hovering() is fenced while open).
	contentH := int32(len(options)) * rowH
	if c.wheelY != 0 && pointIn(c.mouseX, c.mouseY, list) {
		c.wheelTaken = true
		c.ddScroll -= c.wheelY * rowH
	}
	if max := contentH - list.H; c.ddScroll > max {
		c.ddScroll = max
	}
	if c.ddScroll < 0 {
		c.ddScroll = 0
	}

	// Interaction resolves NOW (frame-consistent); painting defers. Every branch
	// CONSUMES the click (the modal owns it): widgets later in the frame — floating
	// panels draw with the pointer restored — must never also react to a click that
	// visually landed on (or dismissed) the list.
	next, changed := cur, false
	if c.clicked {
		switch {
		case pointIn(c.mouseX, c.mouseY, list): // raw: the dropdown owns the pointer, bypass the fence
			idx := int((c.mouseY - list.Y + c.ddScroll) / rowH)
			if idx >= 0 && idx < len(options) {
				next, changed = idx, idx != cur
			}
			c.ddOpen = ""
			c.modalRelease = true
		case pointIn(c.mouseX, c.mouseY, r):
			// Toggling the control shut.
			c.ddOpen = ""
			c.modalRelease = true
		default:
			// Click-away closes and the fence swallows the click.
			c.ddOpen = ""
			c.modalRelease = true
		}
		c.clicked = false
	}
	c.ddDraws = append(c.ddDraws, ddDraw{
		list: list, options: options, cur: next, scroll: c.ddScroll, rowH: rowH,
		thumbW: thumbW, thumb: thumb,
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
	c.TooltipAfterDelay(id, r, text, tooltipDwell)
}

// TooltipAfterDelay is TooltipAfter with a caller-chosen dwell instead of the
// fixed tooltipDwell — for hints whose delay is user-configurable (the emote-name
// hover tooltip's slider). A single shared dwell timer (tipHoverID/tipHoverSince)
// backs both: moving off r, or to a different id, resets the timer, so two
// TooltipAfterDelay targets can't fire each other early.
func (c *Ctx) TooltipAfterDelay(id string, r sdl.Rect, text string, delay time.Duration) {
	if text == "" || c.modalOn || !c.hovering(r) {
		if c.tipHoverID == id {
			c.tipHoverID = ""
		}
		return
	}
	if c.tipHoverID != id {
		c.tipHoverID = id
		c.tipHoverSince = time.Now()
		c.tipHoverDelay = delay // NextHoverDue schedules the wake off this
		return
	}
	if time.Since(c.tipHoverSince) >= delay {
		c.tipText = text
	}
}

const (
	tooltipMargin = 8   // keep the tooltip box this far inside the window edges
	tooltipPad    = 6   // inner padding around the text
	tooltipLineH  = 18  // wrapped-line pitch
	tooltipMaxLn  = 10  // line cap so a huge description can't wallpaper the screen (WrapText ellipsizes)
	tooltipMaxW   = 460 // preferred max text width before it word-wraps
)

// tipBox places a tooltip of size boxW×boxH near the cursor (mx,my): it offsets to
// the lower-right, flips to the other side of the pointer when that would overflow,
// and then clamps so the WHOLE box stays inside the w×h window — even when the box
// is wider/taller than the room on either side (a long server description). Pure, so
// the "never off-screen" rule (the reported bug) is unit-tested.
func tipBox(mx, my, boxW, boxH, w, h int32) sdl.Rect {
	x := mx + 16
	if x+boxW > w-tooltipMargin {
		x = mx - boxW - 8 // flip left of the pointer
	}
	if x+boxW > w-tooltipMargin {
		x = w - tooltipMargin - boxW // still over (box wider than the flip room): pin to the right edge
	}
	if x < tooltipMargin {
		x = tooltipMargin
	}
	y := my + 18
	if y+boxH > h-tooltipMargin {
		y = my - boxH - 6 // flip above the pointer
	}
	if y+boxH > h-tooltipMargin {
		y = h - tooltipMargin - boxH
	}
	if y < tooltipMargin {
		y = tooltipMargin
	}
	return sdl.Rect{X: x, Y: y, W: boxW, H: boxH}
}

// drawTooltip paints the armed hover hint near the cursor. Long text (e.g. a server
// description) WORD-WRAPS to a capped width instead of running off-screen, and the
// box flips/clamps at the window edges so it's always fully visible. Called last in
// the frame. The wrap is cached (tipWrapText/W) so a hovered tooltip costs nothing
// extra per frame.
func (c *Ctx) drawTooltip(w, h int32) {
	if c.tipText == "" {
		return
	}
	// Wrap to the smaller of the preferred max and the window width (minus margins/pad).
	maxW := int32(tooltipMaxW)
	if lim := w - 2*tooltipMargin - 2*tooltipPad; maxW > lim {
		maxW = lim
	}
	if maxW < 40 {
		maxW = 40
	}
	if c.tipWrapText != c.tipText || c.tipWrapW != maxW { // re-wrap only when the text or width changed
		c.tipWrapLn = c.WrapText(c.tipText, maxW, tooltipMaxLn)
		c.tipWrapText, c.tipWrapW = c.tipText, maxW
	}
	lines := c.tipWrapLn
	if len(lines) == 0 {
		return
	}
	boxW := int32(0)
	for _, ln := range lines {
		if tw := c.TextWidth(ln); tw > boxW {
			boxW = tw
		}
	}
	boxW += 2 * tooltipPad
	boxH := int32(len(lines))*tooltipLineH + 2*tooltipPad
	box := tipBox(c.mouseX, c.mouseY, boxW, boxH, w, h)
	c.Fill(box, sdl.Color{R: 0, G: 0, B: 0, A: 235})
	c.Border(box, ColAccent)
	ty := box.Y + tooltipPad
	for _, ln := range lines {
		c.Label(box.X+tooltipPad, ty, ln, ColText)
		ty += tooltipLineH
	}
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
			labelX := row.X + 6
			if d.thumbW > 0 && d.thumb != nil { // thumbnail strip down the left; text shifts past it
				d.thumb(idx, sdl.Rect{X: row.X + 2, Y: row.Y + 2, W: d.thumbW, H: d.rowH - 4})
				labelX = row.X + d.thumbW + 8
			}
			if t, ok := c.textTexture(opt, ColText, c.font); ok {
				w := t.logicalW() // #77-LOGICAL px
				if maxW := row.X + row.W - labelX - 6; w > maxW && maxW > 0 {
					w = maxW
				}
				c.blitLabel(t, labelX, row.Y+(d.rowH-uiLogicalFromDevice(t.h, t.devPct))/2, w)
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
	// A degenerate panel (a layout-edited or fixed-geometry slot laid out to a
	// non-positive height) hands us visible <= 0. With an empty list content is
	// 0 too, so maxScroll = content - visible is POSITIVE and slips past the
	// guard below, then thumbH = track.H*visible/content divides by content=0.
	// Guard both: nothing scrolls (and nothing draws) when there's no content or
	// no visible viewport — the same "everything fits" outcome as maxScroll<=0.
	if content <= 0 || visible <= 0 {
		return 0
	}
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
// sliderWheelDivisor sets the wheel step to 1/100 of a slider's range (≥1), so
// the ubiquitous percent sliders step by exactly 1 per tick.
const sliderWheelDivisor = 100

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
	// Wheel over the track nudges the value (playtest ask: every slider scrolls).
	// One tick = 1/sliderWheelDivisor of the range, floored at 1 so a percent
	// slider steps by exactly 1. Claiming the wheel keeps the page beneath from
	// scrolling on the same tick (the hovered-control-owns-the-wheel contract).
	if !c.wheelTaken && c.wheelY != 0 && c.hovering(grab) {
		c.wheelTaken = true
		step := maxVal / sliderWheelDivisor
		if step < 1 {
			step = 1
		}
		value += c.wheelY * step
		if value < 0 {
			value = 0
		}
		if value > maxVal {
			value = maxVal
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
// instantly. The Settings previews toggle gates ONLY the dwell path — it
// exists to stop popups you didn't ask for, and an explicit right-click IS
// asking (playtest: turning hover-previews off silently killed the
// right-click preview too — "the feature doesn't work").
func (c *Ctx) HoverPreview(id string, r sdl.Rect) bool {
	if !c.hovering(r) {
		if c.hoverID == id {
			c.hoverID = ""
		}
		return false
	}
	c.hoverRect = r // remember the hovered trigger's rect for the close-on-leave corridor
	if c.rightClicked {
		c.hoverID = id // pin it so close-on-leave keeps the box up after an instant open
		return true
	}
	if !c.hoverPreviewOn {
		// Dwell disabled: never START a preview from hover. hoverID is left
		// alone so a right-click-opened box keeps its active trigger (the
		// close-on-leave contract); each trigger clears its own id above the
		// moment the cursor leaves it.
		return false
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
	// Device-scaled sets (#77): their non-shared faces share c.fontDev.
	for _, s := range []*fontSet{&c.chatSetDev, &c.logSetDev} {
		for _, f := range s.fonts {
			if f != c.fontDev && f != nil {
				f.Close()
			}
		}
		s.fonts = nil
	}
	// Device chrome faces: close only when NOT sharing the logical faces (the
	// 100% case shares them, so the logical closes below cover it).
	if c.fontDev != nil && c.fontDev != c.font {
		c.fontDev.Close()
	}
	if c.fontBigDev != nil && c.fontBigDev != c.fontBig {
		c.fontBigDev.Close()
	}
	if c.font != nil {
		c.font.Close()
	}
	if c.fontBig != nil {
		c.fontBig.Close()
	}
}
