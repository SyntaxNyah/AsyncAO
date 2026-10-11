package ui

import (
	"strings"
	"time"
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

// sliderIdleThumbColor is the un-hovered fill for Slider's and VScrollbar's
// thumb (GH #50, "black slider ball"). ColPanelHi's contrast is only ever
// proven against ColBackground — its real job is a raised BUTTON surface
// (see the comment on its assignment below) — so reusing it unchecked here
// puts a small thumb directly on ColPanel instead, a tighter context
// nothing had contrast-checked. Every built-in chrome preset fails the
// kit's own minInkSkinContrast floor for that pair (dark/soft/midnight
// Δ16, warm Δ14, light Δ20, even "High contrast" Δ28 — all under 48), so
// idle the thumb reads as one flat block fused to the track. This reuses
// that SAME floor and the SAME two proven-safe ink extremes
// applyThemePalette's text-readability guard already falls back to below
// (light ink on a dark surface, dark ink on a light one) instead of a
// second contrast system or a constant hand-tuned only for the stock
// defaults — so any preset, custom scheme, or AO2-derived palette gets a
// readable thumb, not just the six shipped today.
func sliderIdleThumbColor() sdl.Color {
	if absInt(colLuma(ColPanelHi)-colLuma(ColPanel)) >= minInkSkinContrast {
		return ColPanelHi // already clears the floor — untouched, today and for any future palette that does too
	}
	if colLuma(ColPanel) < paletteLightPanelLuma {
		return defaultKitColors[4] // stock light ink (ColText's slot), safe on a dark track
	}
	return paletteDarkText // dark ink, safe on a light track
}

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
	// bold selects the REAL bold cut of `font` (SDL_ttf's STYLE_BOLD, synthesised
	// from the same face). It is part of the key because the two are different
	// pixels for the same string: without it a bold speaker name and the identical
	// plain word elsewhere on screen would share one texture, and whichever drew
	// first would decide the weight of both.
	bold bool
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

	// musicMarquee is the phase of the ONE scrolling label the client has, the
	// music banner's (musicmarquee.go). It lives on Ctx rather than App because
	// it is display state of a kit-level text surface, it has to survive between
	// frames, and it is bounded to a single slot by its type.
	musicMarquee marqueeSlot

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

	// panelFont is the CHROME face for the panel currently being drawn, or nil for
	// the client's own. A panel the user gave a font brackets its draw with
	// pushPanelFont/popPanelFont, and every chrome-faced widget inside it — labels,
	// buttons, checkboxes, dropdowns, text fields — follows, because they all read
	// chromeFace() rather than c.font directly.
	//
	// AN OVERRIDE, NEVER A SWAP OF c.font, and that distinction is load-bearing
	// rather than stylistic: c.font is the IDENTITY every font-lifetime guard
	// compares against. buildSet passes it as the chromeShare that decides whether
	// a set adopts it as an embedded last resort, and dropThemeElemSets, Destroy
	// and SetChromeFont each refuse to Close a face while `f == c.font`. Assigning
	// a panel's face over c.font would point those guards at the wrong pointer and
	// Close a face still in use — a use-after-free, not a cosmetic bug.
	//
	// Zeroed by BeginFrame as well as by the panels' own defers, for the same
	// reason the overlay fence documents: a panic unwinding a bracket must not
	// leave the whole client dressed in one panel's font.
	panelFont *ttf.Font
	// panelWidth memoises TextWidth while a panel face is armed. widthCache is
	// keyed by STRING alone and belongs to the chrome face, so a panel face must
	// never write into it; this one is keyed by face AND string. Nil until a
	// bracket is actually armed, so a session with no per-panel font never
	// allocates it.
	panelWidth map[panelWidthKey]int32
	// logDevWidth memoises devTextWidthWeight — the DEVICE-face prefix width
	// (raw device px) logPrefixWidth uses for log selection/highlight. Same key
	// shape as panelWidth (font, text, bold) but a SEPARATE map, since the two
	// hold different numbers for the same key: panelWidth is the LOGICAL-face
	// SizeUTF8, this is the DEVICE sibling's. Keying by the LOGICAL font pointer
	// (not the device one) is what lets it share panelWidth's purge site
	// (purgeTextCache): every path that can make the device sibling stale first
	// either changes the logical font pointer itself (new key, old entries
	// unreachable) or calls purgeTextCache directly (SetTextDevScale). Nil until
	// a fractional-scale selection is measured, so 100%-only sessions never
	// allocate it.
	logDevWidth map[panelWidthKey]int32

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
	// The CHROME set: the same chain again, but headed by the chrome face and at the
	// fixed chrome point size. It exists so plain Label/LabelClipped/Heading — the
	// ~800 call sites that draw menus, buttons, tab titles, character names, area
	// names, tooltips — can fall back to a covering face instead of rendering
	// whatever the embedded Latin font makes of a non-Latin string, which was tofu.
	// Built lazily on the first non-ASCII chrome label, so a Latin-only session
	// never pays for it.
	chromeSet    fontSet
	chromeSetDev fontSet
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
	// scan. Cleared on any font rebuild. Keyed by (text, SET, pct) — see pickKey.
	pickMemo map[pickKey]pickResult
	// coverHint is the LAST pick made, so the covers() gate a caller runs right
	// after its own *FontFor call resolves as a compare instead of a set scan +
	// map probe. A pure accelerator: coversFace stays correct when it misses.
	coverHintFont *ttf.Font
	coverHintText string
	coverHintOK   bool
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
	// Census tier: faces the system-font census supplied on demand, because a rune
	// reached the draw that NOTHING in the curated chain covered (fontcensus.go).
	// Appended last, so it never outranks a curated pick — the census answers "some
	// font here has this glyph", the tiers answer "this is the right face for this
	// script", and the second question is the better one when both can.
	censusData  [][]byte
	censusCover []*sfnt.Font
	// missingRunes is the render thread's fixed-size record of runes nothing could
	// draw, drained once a frame by the App and handed to the census goroutine. A
	// FIXED ARRAY, not a slice or map: this is written from coverRunes, which the
	// whole-screen zero-allocation gates cover, so it must never allocate. Once full
	// it simply stops recording — a paragraph of unknown runes needs one answer, not
	// four hundred, and the next frame picks up whatever is still missing.
	missingRunes [censusQueueCap]rune
	missingN     int
	// Per-element theme faces (#39): the font FILES the active theme's
	// courtroom_fonts.ini named, read off-thread on theme apply (≤ themeFaceCap)
	// and installed via SetThemeFaces. RWFromMem aliases the bytes, so they must
	// outlive their faces — SetThemeFaces closes the element sets BEFORE dropping
	// them. themeFaceCover holds the index-aligned sfnt cmap face for the PICK.
	themeFaceData  [][]byte
	themeFaceCover []*sfnt.Font
	// themeFaceQt is the index-aligned Qt ROW PITCH of each of those faces —
	// OS/2 usWinAscent/usWinDescent, the metric AO2's Qt actually stacks its
	// chatbox rows by, which SDL_ttf does not expose at all (themerowpitch.go,
	// internal/theme.ReadQtRowPitch). Parsed here, once per apply, off every draw
	// path; the draw side does one slice read and integer arithmetic.
	themeFaceQt []theme.QtRowPitch
	// One logical + one device fontSet PER ELEMENT (not per face): each element
	// pins its OWN point size, so a theme with showname = 12 and message = 24
	// can't thrash one shared set between two sizes every frame (buildSet's pct
	// guard would rebuild — and purgeTextCache — on every draw). Built LAZILY: an
	// element the theme doesn't dress never allocates one.
	themeElemSets    [themeFontElemCount]fontSet
	themeElemSetsDev [themeFontElemCount]fontSet

	// uiPct is the global render scale percent; mouse coordinates
	// unproject through it so logical hit-tests stay exact.
	uiPct int32
	// devScale: drawable px per window point (2 on macOS Retina, 1 elsewhere); folds into text raster only.
	devScale float32

	// renderPctOverride, when non-zero, is the scale percent Ren is ACTUALLY at,
	// for the offscreen passes that bracket a SetScale of their own (the pinned-tab
	// client texture). Outside such a bracket the frame loop has the renderer at
	// uiPct/100, so RenderScalePct falls back to uiPct. Only the device-exact
	// message blit consults it, and it only ever costs that blit its fast path.
	renderPctOverride int32

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
	// altHeld is the live Alt state. It became load-bearing when the bare-key
	// bind namespaces (character keys, showname keys, IC phrases, jukebox keys,
	// the emote number row) moved onto Alt chords so they stop competing with the
	// IC input for plain letters — see bareBindKey (qol.go). Read with !ctrlHeld
	// everywhere, because AltGr on Windows reports as Ctrl+Alt and DOES produce
	// text: a bare altHeld test would eat every accented character on a European
	// keyboard layout.
	altHeld       bool
	rightClicked  bool
	wheelY        int32
	typed         string
	backspace     bool
	enter         bool
	tabPressed    bool
	escPressed    bool
	fullscreenReq bool        // F11: toggle fullscreen this frame (consumed in app.Frame)
	keyPressed    sdl.Keycode // plain (non-ctrl) keydown this frame (0 = none)
	pasted        string      // Ctrl+V clipboard text (flattened to one line)
	pastedRaw     string      // Ctrl+V clipboard text, newlines preserved (TextArea only)
	copyReq       bool        // Ctrl+C: focused field copies its selection (else value)
	cutReq        bool        // Ctrl+X: focused field cuts its selection (else clears)
	selectAll     bool        // Ctrl+A armed: the focused field converts it to a real all-selection
	undoReq       bool        // Ctrl+Z routed to the focused field (App consumes the chord pre-screen)
	redoReq       bool        // Ctrl+Y / Ctrl+Shift+Z, same routing
	wordBack      bool        // Ctrl+Backspace in a focused field: delete the preceding word (event flag; gated on wordDeleteOn at consumption)
	// Word-granular editing chords, all captured the same way wordBack is (a
	// focused field only — otherwise the key falls through to the configurable
	// Ctrl hotkeys). wordFwdDel is Ctrl+Delete (the forward twin of
	// Ctrl+Backspace, gated on the same WordDelete pref); wordLeft/wordRight are
	// Ctrl+Left / Ctrl+Right caret JUMPS, which are navigation and therefore
	// ungated — Shift composes with them through the existing c.shiftHeld
	// extend path, so Ctrl+Shift+arrow selects by word for free.
	wordFwdDel bool
	wordLeft   bool
	wordRight  bool
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
	onRow func(label string, y int32)
	// emojiLoad kicks the one-shot colour-emoji face read. It lives on the App
	// (it owns the off-thread read and the result channel), but the KIT needs to
	// trigger it too: a dropdown row or a lobby label can be the first thing on
	// screen to contain an emoji, and a widget that can't ask for the face would
	// draw tofu forever while the chatbox got colour. nil in tests.
	emojiLoad func()
	// muted, while set, renders Checkbox rows greyed and inert: a master toggle
	// upstream has already overruled them, so showing them live would invite a
	// click that visibly does nothing. They still draw (the player can SEE what
	// the master is suppressing, and what they'll get back when they turn it
	// off) and still register with onRow, so settings search keeps finding them.
	muted   bool
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
	// cgoSrc is the SECOND persistent scratch rect, for the one call shape that
	// needs two at once: Renderer.Copy(tex, src, dst) with a NON-NIL source rect.
	// The free elements' 9-slice painter (themeelements.go) issues nine of those
	// per element, and reusing cgoRect for both arguments would have the
	// destination overwrite the source before SDL read it. Identical contract to
	// cgoRect otherwise — SDL copies during the call and never retains the
	// pointer, and Ctx is render-thread-only, so the pair is set and consumed one
	// blit at a time.
	cgoSrc sdl.Rect
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
	// tipAnchor is what the armed tip DESCRIBES: the widget's rect, unioned with any
	// container group rect in force when it armed. drawTooltip keeps the box off it,
	// so a hint can never cover the control it is explaining (nor its neighbours in
	// the same strip). Zero rect = no constraint, i.e. exactly today's placement.
	tipAnchor sdl.Rect
	// tipGroup is a container's claim ("keep the whole strip visible"), set by
	// TooltipGroup around a run of child tooltips and cleared after. One rect, not a
	// registry: only one tooltip can be armed per frame anyway.
	tipGroup sdl.Rect
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
	// The focused field's STICKY horizontal scroll. It used to be recomputed from
	// scratch every frame as "centre the caret" (scrollFor), which meant a
	// click-to-caret — and every mouse-move of a drag-selection — re-centred the
	// view and slid the text out from under the cursor mid-gesture. Now the view
	// stays exactly where it is and only moves when the caret would otherwise
	// leave it (scrollKeep), which is what every native editor does. Belongs to
	// fieldScrollID the way c.caret belongs to caretField, and resets with focus.
	fieldScrollID string
	fieldScroll   int32
	// One-slot right-gutter reservation for the NEXT text field with this id
	// (ReserveFieldTail). A widget the caller paints INSIDE a field's box — the IC
	// message counter, the muted chip — declares its width here so the field's
	// text is clipped before it instead of running underneath it. Single slot, not
	// a map: exactly one field per frame needs it, and a map would put an
	// allocation on the draw path. Cleared each BeginFrame, so an unconsumed
	// reservation can never leak into the next frame's layout.
	tailReserveID string
	tailReserveW  int32

	// Multiline TextArea state (#83). taCaret/taSelAnchor are RUNE indices into
	// the area's value; taScroll is its vertical scroll in pixels; taCaretField
	// names which area owns the caret (focus change resets it, like caretField).
	// Focus is shared with TextField (c.focusID) so clipboard chords / select-all
	// are captured the same way, but the caret lives HERE and never touches
	// c.caret/c.selAnchor — the single-line field's math stays independent.
	taCaret      int
	taSelAnchor  int
	taCaretField string
	taScroll     int32

	// Tab focus cycling (playtest: Tab out of the IC box landed on the wrong
	// field): TextField records draw order here each frame; the next
	// BeginFrame moves focus along it. Bounded by fields drawn per frame.
	// Order is DRAW order; orderTabChain (tabchain.go) re-seats the chat
	// block into AO2's showname → IC → OOC → OOC-name chain at Tab time.
	fieldSeq []string
	// drawnFieldSeq is the field order of the last frame that FINISHED drawing —
	// the double-buffer partner of fieldSeq, swapped in BeginDraw. It exists
	// because fieldSeq is empty for the whole pre-screen half of App.Frame:
	// BeginDraw truncates it at the top of the pass and the TextFields only
	// re-register ~280 lines later, so a pre-screen reader (claimTypingForIC,
	// chatfocus.go) asking fieldSeq "is that box on screen?" always got "no" and
	// its whole feature was dead code. Swapped, never copied, so the answer costs
	// no allocation and no per-frame walk.
	drawnFieldSeq []string
	tabShift      bool // shift held at the Tab press → cycle backwards
	// focusNext is a queued FocusField request, applied at the next
	// BeginFrame so it survives this frame's click-away unfocus no matter
	// where the requesting widget sits in draw order.
	focusNext string

	// Dropdown state: one dropdown may be open; while open it owns the
	// pointer (modal capture) and its list paints in FinishFrame so it
	// stacks above widgets drawn later. modalOn persists across frames —
	// widgets drawn BEFORE the dropdown in draw order are fenced too —
	// and releases at the BeginFrame after the close.
	ddOpen     string
	ddScroll   int32
	ddOpenList sdl.Rect // the OPEN dropdown's list rect (flip-adjusted), stashed each draw — read by boxFencesPointer so a list over a floating panel stays clickable
	// Row hit-test geometry of the open list, stamped by dropdownEx so
	// DropdownRowRightClicked can answer "which row is under the pointer" without
	// widening Dropdown's signature. ddHitID names the owner, so a stale stamp
	// from a list closed earlier this frame can never answer for another id.
	ddHitID      string
	ddHitList    sdl.Rect
	ddHitRowH    int32
	ddHitScroll  int32
	ddHitRows    int
	modalOn      bool
	modalRelease bool
	ddDraws      []ddDraw // deferred overlay draws this frame (0 or 1)

	// Overlay fence registry (overlayfence.go): rects published by chrome that
	// paints OVER other widgets — the compact toolbox strip today, the menu bar
	// next — so hovering() can refuse hits underneath them in a single-pass kit.
	// A fixed array + count (hard rule 4): publishing never grows a slice and
	// never allocates. overlayOwner suspends the test while the occluder
	// hit-tests its OWN widgets (the dropdown's raw-pointIn precedent, scoped),
	// and overlayOwnerMark bounds that suspend to the owner's own publication
	// onwards — fences published EARLIER (an app-wide menu bar's open dropdown)
	// still occlude the owner, because registry order is paint order.
	// Zeroed every BeginFrame, so an un-released fence can't freeze the UI the
	// way a stranded modalOn would.
	// overlayFenceDrops counts publications refused this frame because the registry
	// was full. Dropping is silent by construction (the occluder still paints; only
	// the input protection is missing), so the counter is the only way the condition
	// can ever be detected — the tests assert it stays zero across a real frame.
	overlayFences     [overlayFenceCap]sdl.Rect
	overlayFenceN     int
	overlayFenceDrops int
	overlayOwner      bool
	overlayOwnerMark  int

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

	textCache map[textKey]cachedText
	atlas     []*atlasPage // shared label pages (≤ textAtlasMaxPages)
	// rowLabelRight is the armed right edge for chrome ROW labels (F2), 0 = unarmed.
	// Set only inside a pushRowLabelLimit / popRowLabelLimit bracket; rowLabelMemo
	// holds the truncations it produced (lazily created, capped like widthCache).
	rowLabelRight int32
	rowLabelMemo  map[rowLabelKey]string

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
		devScale:   1.0,
		textCache:  map[textKey]cachedText{},
		widthCache: map[string]int32{},
	}, nil
}

// SetWindow attaches the SDL window for attention requests (FlashWindow).
func (c *Ctx) SetWindow(win *sdl.Window) { c.win = win }

// MousePos returns this frame's logical cursor position — the same
// coordinates every widget's hit-testing consults. Exported so a caller
// outside this package can observe the shared cursor state (e.g. proving a
// second window's input never reached it) without duplicating HandleEvent's
// own coordinate math.
func (c *Ctx) MousePos() (x, y int32) { return c.mouseX, c.mouseY }

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

// windowDisplayIndex is the index of the display the window currently sits on,
// falling back to the primary display (0) when it can't be determined (headless,
// or SDL failing the query). Every "which monitor are we on?" caller must go
// through this one accessor: reading the usable bounds of display N while
// recentering on display 0 is exactly the multi-monitor bug CenteredWindowPos
// documents. Render thread only.
func (c *Ctx) windowDisplayIndex() int {
	if c.win == nil {
		return primaryDisplayIndex
	}
	di, err := c.win.GetDisplayIndex()
	if err != nil || di < 0 {
		return primaryDisplayIndex
	}
	return di
}

// WindowDisplayUsable returns the usable size (work area, minus the taskbar) of
// the display the window currently sits on; (0,0) if unknown (headless). The
// caller clamps a requested size into this (config.ClampWindowSize). Render
// thread only.
func (c *Ctx) WindowDisplayUsable() (int, int) {
	if c.win == nil {
		return 0, 0
	}
	r, err := sdl.GetDisplayUsableBounds(c.windowDisplayIndex())
	if err != nil {
		return 0, 0
	}
	return int(r.W), int(r.H)
}

// primaryDisplayIndex is SDL's first display — the fallback whenever the real
// one is unknowable (no window yet, or a failed query). Named because it is also
// precisely what the bare sdl.WINDOWPOS_CENTERED constant hard-codes.
const primaryDisplayIndex = 0

// sdlWindowPosDisplayMax is the largest display index SDL's window-position
// encoding can carry. SDL_video.h defines SDL_WINDOWPOS_CENTERED_DISPLAY(X) as
// (SDL_WINDOWPOS_CENTERED_MASK|(X)) — a bare OR that does NOT mask X — while
// SDL_SetWindowPosition reads the target back out of the low 16 bits only
// (`int displayIndex = (x & 0xFFFF)`, SDL 2.32.10 src/video/SDL_video.c). So the
// index field is exactly 16 bits wide, and an index that overflows it does not
// merely wrap: its high bits corrupt the 0x2FFF0000 mask, the value stops reading
// as "centered" at all, and SDL places the window at a garbage ABSOLUTE position
// off every monitor. Hence the explicit upper bound as well as the lower one —
// CenteredWindowPos is exported and called from cmd/asyncao, so its input is not
// confined to the bounded values SDL's own GetDisplayIndex returns.
const sdlWindowPosDisplayMax = 0xFFFF

// CenteredWindowPos builds the SDL_WINDOWPOS_CENTERED_DISPLAY(n) magic value for
// one display, for SetPosition / CreateWindow.
//
// This exists because the bare sdl.WINDOWPOS_CENTERED is NOT "centre on whatever
// display you are on": SDL_video.h defines it as SDL_WINDOWPOS_CENTERED_DISPLAY(0),
// and SDL_SetWindowPosition derives the target from the low 16 bits, so it always
// means display 0. Recentering with it teleports a window the user dragged onto a
// second monitor back to the primary one — and worse, it does so with a size that
// was clamped against the OTHER monitor's usable bounds.
//
// Anything outside the encodable range degrades to the primary display. SDL
// itself clamps an in-range-but-nonexistent index back to 0, so only the encoding
// bound has to be enforced here — see sdlWindowPosDisplayMax for why a too-large
// index would otherwise produce a position word that is not "centered" at all.
func CenteredWindowPos(display int) int32 {
	if display < 0 || display > sdlWindowPosDisplayMax {
		display = primaryDisplayIndex
	}
	return int32(sdl.WINDOWPOS_CENTERED_MASK | uint32(display))
}

// WindowMaximized reports whether the OS window is currently maximized (false
// headless). Render thread only.
func (c *Ctx) WindowMaximized() bool {
	if c.win == nil {
		return false
	}
	return c.win.GetFlags()&sdl.WINDOW_MAXIMIZED != 0
}

// UnmaximizeWindow drops a maximized window back to its restored geometry so a
// following SetSize/SetPosition actually sticks.
//
// It has to be explicit because SDL_SetWindowSize has NO maximized guard: it
// writes window->windowed.w/h and calls the backend regardless
// (SDL 2.32.10 src/video/SDL_video.c), and WIN_SetWindowSize's SetWindowPos runs
// against an HWND that still carries WS_MAXIMIZE
// (src/video/windows/SDL_windowswindow.c), so Windows keeps the maximized
// geometry and the resize silently does nothing.
//
// The MAXIMIZED-only gate is deliberate and must not be replaced by SDL's own:
// SDL_RestoreWindow self-gates on (MAXIMIZED | MINIMIZED), so calling it blind
// would also pop a MINIMIZED window back onto the screen. Render thread only.
func (c *Ctx) UnmaximizeWindow() {
	if c.win == nil || c.win.GetFlags()&sdl.WINDOW_MAXIMIZED == 0 {
		return
	}
	c.win.Restore()
}

// WindowSize reports the current window size (0,0 headless). Render thread only.
func (c *Ctx) WindowSize() (int, int) {
	if c.win == nil {
		return 0, 0
	}
	w, h := c.win.GetSize()
	return int(w), int(h)
}

// ResizeWindow sets the windowed size and recenters it on the display it is
// actually on (NOT display 0 — see CenteredWindowPos), so a too-big or
// off-screen window snaps back into view. No-op headless; the caller clamps the
// size first.
//
// Deliberately does NOT un-maximize: applyFullscreen's leave-fullscreen snap-back
// shares this call, and F11 must not also change a maximized window's state.
// App.applyWindowSize owns that (it is the layer that persists the size too).
// Render thread only.
func (c *Ctx) ResizeWindow(w, h int) {
	if c.win == nil {
		return
	}
	pos := CenteredWindowPos(c.windowDisplayIndex())
	c.win.SetSize(int32(w), int32(h))
	c.win.SetPosition(pos, pos)
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
//
// Deliberately NOT panel-scoped: the typewriter rasterizer and the other callers
// of this want the client's own face regardless of which panel is mid-draw.
func (c *Ctx) Font() *ttf.Font { return c.font }

// panelWidthKey keys the panel-face width memo. The chrome memo is keyed by
// string alone because it only ever holds one face's measurements; this one has
// to carry the face too, or two panels at different sizes would read each
// other's widths.
type panelWidthKey struct {
	font *ttf.Font
	text string
	// bold: SDL_ttf's synthetic emboldening widens the advances, so a bold run
	// measures wider than the same string plain. The log's named-row layout places
	// the message text at name-start + name-WIDTH, so measuring the plain width and
	// drawing the bold one would slide the message left onto the name's last glyph.
	bold bool
}

// chromeFace is the face every chrome widget draws and measures in: the panel's
// own when one is armed, the client's otherwise.
//
// One always-false, perfectly-predicted branch on the unset path, returning the
// identical c.font pointer — so textTexture receives the identical
// textKey{text, color, font}, hits the identical cache entry and the identical
// atlas slot. Identical pointer in, identical pixels out.
func (c *Ctx) chromeFace() *ttf.Font {
	if c.panelFont != nil {
		return c.panelFont
	}
	return c.font
}

// chromeFaceFor is chromeFace for a KNOWN string: the same face when it can draw
// the text, otherwise the first face in the fallback chain that can.
//
// Plain chrome labels used to render straight from one face with no chain at all,
// so every non-Latin string outside the chat/log paths — character names in the
// picker, area names, tab titles, menu entries, tooltips, player names — drew as
// .notdef boxes no matter how many fallback faces were loaded. This is a
// single-face pick rather than the per-glyph raster the message paths use: a
// chrome label is nearly always one script, the pick is memoized, and it keeps
// the label on the shared texture atlas instead of giving every name its own
// textures.
//
// Pure ASCII short-circuits before anything is built, so the common case is a
// one-pass byte scan and nothing else. A panel-dressed face is left alone: its
// whole point is that the user chose that family for that panel.
func (c *Ctx) chromeFaceFor(text string) *ttf.Font {
	face := c.chromeFace()
	if c.panelFont != nil || !hasNonASCII(text) {
		return face
	}
	c.noteScript(text) // latch the off-thread fallback load, exactly as ChatFontFor does
	fonts := c.buildSet(&c.chromeSet, c.fontChain, c.chainCover, DefaultScalePct, DefaultScalePct, c.font, false)
	if len(fonts) <= 1 {
		return face // nothing loaded yet; the text re-draws when a face lands
	}
	return c.pickIn(&c.chromeSet, fonts, DefaultScalePct, text)
}

// hasNonASCII is the cheap gate in front of every chrome fallback decision: one
// byte pass, no rune decode, no allocation. A pure-ASCII label — the overwhelming
// majority — costs this and nothing more.
func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

// pushPanelFont arms f as the chrome face and RETURNS THE PREVIOUS ONE, which
// the caller hands straight back to popPanelFont:
//
//	defer c.popPanelFont(c.pushPanelFont(face))
//
// By value rather than returning a restore closure, and that is measured rather
// than assumed: the closure form costs 1 allocation per call, and these brackets
// sit on the per-frame panel draws that the whole-screen allocation gates cover.
// Same idiom, and the same reason, as pushClip.
//
// A nil face (the overwhelmingly common case — no override set) writes nothing.
// So is a face that IS the chrome face: arming it would only add a pointless
// non-nil panelFont for every later branch to test.
func (c *Ctx) pushPanelFont(f *ttf.Font) *ttf.Font {
	prev := c.panelFont
	if f != nil && f != c.font {
		c.panelFont = f
	}
	return prev
}

// popPanelFont restores what pushPanelFont returned. Always via defer, so an
// early return — a panel with an empty roster, a Notes tab with no notebook, a
// music panel in volume mode — cannot leak the face into whatever draws next.
func (c *Ctx) popPanelFont(prev *ttf.Font) { c.panelFont = prev }

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
	// srcChain/srcCover record the override chain this set was built FROM: the
	// user's font chain for chat/log, or the ONE theme-declared face for a
	// per-element set (#39). Recorded so the DEVICE sibling can be rebuilt from
	// the same source without every caller threading it (deviceFontFor only
	// resolves a face to its set, not to which chain that set came from).
	srcChain []([]byte)
	srcCover []*sfnt.Font
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

// ThemeFont returns element el's PRIMARY face at pct (#39): the theme-declared
// family when face indexes one, else the user's own chain — either way out of
// el's OWN fontSet, so two elements at different point sizes never fight over
// one shared set. Render thread.
func (c *Ctx) ThemeFont(el themeFontElem, face, pct int) *ttf.Font {
	return c.themeFontsFor(el, face, pct)[0]
}

// ThemeFontFor is ThemeFont with the per-line coverage pick (see ChatFontFor).
func (c *Ctx) ThemeFontFor(el themeFontElem, face, pct int, text string) *ttf.Font {
	c.noteScript(text)
	s := &c.themeElemSets[el]
	return c.pickIn(s, c.themeFontsFor(el, face, pct), pct, text)
}

// SetThemeFaces installs the theme-declared face bytes (nil clears), read
// off-thread by the App on theme apply. Mirrors SetFallbackFonts: parse the cmap
// covers, bump fontChainGen so every set rebuilds, purge the pointer-keyed
// caches. The element sets' faces ALIAS the outgoing bytes (RWFromMem), so they
// are closed here BEFORE the bytes lose their last reference. Render thread.
func (c *Ctx) SetThemeFaces(data [][]byte) {
	if len(data) > themeFaceCap {
		data = data[:themeFaceCap]
	}
	if len(data) == 0 && c.themeFaceData == nil {
		return // already clear — the overwhelmingly common no-theme-font case
	}
	c.dropThemeElemSets()
	c.themeFaceData = data
	c.themeFaceCover = c.themeFaceCover[:0]
	c.themeFaceQt = c.themeFaceQt[:0]
	for _, d := range data {
		c.themeFaceCover = append(c.themeFaceCover, parseCover(d))
		// Same loop, same index, same bytes: the Qt row pitch must never be able
		// to belong to a different face than the cover it sits beside.
		c.themeFaceQt = append(c.themeFaceQt, theme.ReadQtRowPitch(d))
	}
	c.fontChainGen++ // every fontSet (global AND per-element) rebuilds; wrap caches invalidate
	// Labels, emoji rasters and line picks all carry the OLD faces' identity
	// (pointer keys) — purge wholesale. A theme apply is a user action.
	c.purgeTextCache()
	c.pickMemo = nil
	c.coverHintFont = nil
}

// dropThemeElemSets closes and empties every per-element set (logical and
// device). The close-guards mirror buildSet's: a set may SHARE the chrome face
// as its embedded last resort, which it must never close.
func (c *Ctx) dropThemeElemSets() {
	for i := range c.themeElemSets {
		s := &c.themeElemSets[i]
		for _, f := range s.fonts {
			if f != nil && f != c.font {
				f.Close()
			}
		}
		s.fonts, s.cover, s.srcChain, s.srcCover = nil, nil, nil, nil
		s.pct, s.devPct, s.gen = 0, 0, 0
	}
	for i := range c.themeElemSetsDev {
		s := &c.themeElemSetsDev[i]
		for _, f := range s.fonts {
			if f != nil && f != c.fontDev {
				f.Close()
			}
		}
		s.fonts, s.cover, s.srcChain, s.srcCover = nil, nil, nil, nil
		s.pct, s.devPct, s.gen = 0, 0, 0
	}
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

// pickResult is one cached line pick: the face chosen for the line's VISIBLE runes,
// and whether that face covers EVERY rune, invisible ones included.
//
// The two questions have different answers and both matter. The pick may not be held
// hostage by a formatting rune — that is what threw whole log rows onto a stray family
// (see pickFont) — but the flag has to stay honest, because SDL_ttf draws .notdef for
// any rune the face lacks, invisible or not. So covered==false has two causes now: a
// mixed-SCRIPT line no single face can draw, or a line whose face merely lacks one
// formatting rune. Both want the same thing from the reader (coversFace): take the
// per-glyph raster path rather than blitting the line through one face.
type pickResult struct {
	font    *ttf.Font
	covered bool
}

// pickKey scopes one memoized line pick to the SET and SCALE it was made for.
// Keying by TEXT ALONE (the pre-#39 key) silently handed back another surface's
// face the moment any fallback tier loaded — and with per-element theme sets it
// would be a wrong-FAMILY bug on every theme, not just a wrong size. A composite
// comparable key costs no allocation on lookup.
type pickKey struct {
	text string
	set  *fontSet
	pct  int32
}

// pickSet memoizes the per-line coverage pick: repeat draws cost one map probe, and
// the no-fallback single-font set costs nothing at all (the len==1 fast path).
func (c *Ctx) pickSet(s *fontSet, pct int, text string) *ttf.Font {
	return c.pickIn(s, c.fontsFor(s, pct), pct, text)
}

// pickIn is pickSet's body with the set's faces already built — so a per-element
// theme set (built from its own chain) shares the exact same memo and gate.
func (c *Ctx) pickIn(s *fontSet, fonts []*ttf.Font, pct int, text string) *ttf.Font {
	if len(fonts) == 1 {
		c.noteCoverHint(fonts[0], text, true)
		return fonts[0]
	}
	key := pickKey{text: text, set: s, pct: int32(pct)}
	if r, ok := c.pickMemo[key]; ok {
		c.noteCoverHint(r.font, text, r.covered)
		return r.font
	}
	f, covered := pickFont(fonts, s.cover, &c.sfntBuf, text)
	if !covered {
		// The picked face can't draw the whole string, so some of it may be about to
		// come out as .notdef — offer those runes to the census.
		//
		// "May", not "is": covered==false no longer implies nothing in the set covers
		// the line, since the pick is now made on the VISIBLE runes alone and a face
		// that merely lacks a formatting rune still wins it. noteUncoveredIn re-checks
		// every cover in the set and skips invisible runes outright, so an
		// invisible-only gap queues nothing — it just costs one scan of a string this
		// client had not drawn before.
		//
		// This sits in the memo-MISS branch on purpose. Every single-face pick lands
		// here (chat, log, the per-element theme sets, and now chrome labels), and a
		// chrome label is re-picked for every frame it is on screen — but only the
		// first of those frames reaches this line, because the memo returns above.
		// That keeps noteUncovered's contract intact: raster/pick BUILD path only,
		// never per frame.
		//
		// Without it the chrome path could never feed the census at all, so a Thai or
		// Cherokee character name in the picker drew boxes forever and only ever came
		// right by accident — if some MESSAGE elsewhere in the session happened to
		// contain the same script and pulled the face in through coverRunes.
		c.noteUncoveredIn(s, text)
	}
	if c.pickMemo == nil {
		c.pickMemo = make(map[pickKey]pickResult, 256)
	} else if len(c.pickMemo) >= textCacheMax {
		clear(c.pickMemo) // bounded: wholesale reset, repopulates hot lines
	}
	c.pickMemo[key] = pickResult{font: f, covered: covered}
	c.noteCoverHint(f, text, covered)
	return f
}

// noteUncoveredIn queues every rune of text that no face in set s covers. Called
// only from pickIn's memo-miss branch, so it runs once per (string, set) rather
// than once per frame.
func (c *Ctx) noteUncoveredIn(s *fontSet, text string) {
	for _, r := range text {
		if r < 0x80 || isInvisibleRune(r) {
			continue
		}
		covered := false
		for _, cov := range s.cover {
			if coverHasRune(cov, &c.sfntBuf, r) {
				covered = true
				break
			}
		}
		if !covered {
			c.noteUncovered(r)
		}
	}
}

// noteCoverHint records the pick just made so the covers() gate every caller runs
// IMMEDIATELY afterwards — with the face it just received — is a pointer + string
// compare instead of a set scan plus a map probe. Purely an accelerator:
// coversFace stays correct when the hint misses.
func (c *Ctx) noteCoverHint(f *ttf.Font, text string, covered bool) {
	c.coverHintFont, c.coverHintText, c.coverHintOK = f, text, covered
}

// coversFace reports whether the whole-message pick that returned `primary`
// covered every rune of text. A line with no memo entry took the single-font fast
// path (no fallback loaded) and counts as covered — so the raster gate stays cheap
// for the common case. Read AFTER the matching *FontFor pick (same frame), which
// populated the entry; primary identifies WHICH set (and therefore which element
// and scale) that pick was made in.
func (c *Ctx) coversFace(primary *ttf.Font, text string) bool {
	if primary != nil && primary == c.coverHintFont && text == c.coverHintText {
		return c.coverHintOK
	}
	s := c.setOf(primary)
	if s == nil {
		return true // chrome-only / headless: no pick was memoized for it
	}
	r, ok := c.pickMemo[pickKey{text: text, set: s, pct: int32(s.pct)}]
	return !ok || r.covered
}

// setPairCount is every (logical, device) fontSet pair: the three global sets
// (chat, log, chrome) plus one per theme-dressable element (#39).
const setPairCount = 3 + int(themeFontElemCount)

// setPairs returns every (logical, device) pair in ONE place, so setOf /
// setIndexOf / Destroy / SetChromeFont can never miss a set as sets are added.
// Returned BY VALUE as a fixed-size array — stack, no allocation — and walked
// only on raster-BUILD paths (coverRunes / deviceTextFont / a font swap), never
// per frame: ≤ setPairCount pairs × ≤ 8 faces is a bounded pointer scan.
func (c *Ctx) setPairs() [setPairCount][2]*fontSet {
	var out [setPairCount][2]*fontSet
	out[0] = [2]*fontSet{&c.chatSet, &c.chatSetDev}
	out[1] = [2]*fontSet{&c.logSet, &c.logSetDev}
	out[2] = [2]*fontSet{&c.chromeSet, &c.chromeSetDev}
	for i := 0; i < int(themeFontElemCount); i++ {
		out[3+i] = [2]*fontSet{&c.themeElemSets[i], &c.themeElemSetsDev[i]}
	}
	return out
}

// setOf finds the built fontSet a picked face belongs to, by pointer. Font instances
// are unique per size, so this resolves to the set at the right scale without threading
// pct anywhere — the face was just returned from that set's fontsFor this frame.
func (c *Ctx) setOf(primary *ttf.Font) *fontSet {
	pairs := c.setPairs()
	for _, p := range pairs {
		for _, f := range p[0].fonts {
			if f == primary {
				return p[0]
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
		found := false
		for j, cov := range s.cover {
			if coverHasRune(cov, &c.sfntBuf, r) {
				out[i] = s.fonts[j]
				found = true
				break
			}
		}
		// Nothing in the chain has this glyph, so it is about to draw as .notdef —
		// a tofu box. Queue it: the census will look for an installed font that has
		// it and, if one exists, this same text re-rasters with real glyphs a frame
		// or two later. Zero-width formatting runes are skipped, both because no
		// font is expected to "cover" them and because chasing them would waste the
		// queue on characters that are invisible when they work.
		if !found && !isInvisibleRune(r) {
			c.noteUncovered(r)
		}
	}
	return out
}

// uncoveredPictograph reports whether text holds a BMP pictograph (♥ ✨ ⭐ ⌛ …) that
// NO face in primary's text chain covers — the F5 gap. Such a rune is not a
// supplementary-plane emoji and carries no VS16, so render.NeedsEmojiFallback says
// there is nothing to route and the colour-emoji face is never even loaded, while
// the text chain has no glyph either: the character draws as a box forever.
//
// Cost: one BMPPictograph compare per rune, and a cmap walk only for the runes that
// pass it — so an ASCII or CJK line pays a byte-cheap scan and no font work. It is
// called only from the paths that have ALREADY decided the whole-line pick failed
// (labelEmoji's raster branch), never per frame on the fast path.
func (c *Ctx) uncoveredPictograph(primary *ttf.Font, text string) bool {
	s := c.setOf(primary)
	if s == nil {
		return false // chrome-only / headless: no chain to be uncovered by
	}
	for _, r := range text {
		if render.BMPPictograph(r) && !c.setCovers(s, r) {
			return true
		}
	}
	return false
}

// uncoveredPictographMask is uncoveredPictograph's per-rune answer, for the raster
// build: true where the colour-emoji face must take over from the text chain. nil
// when there is nothing to promote — the overwhelming case — so a normal label
// allocates nothing extra.
func (c *Ctx) uncoveredPictographMask(primary *ttf.Font, runes []rune) []bool {
	s := c.setOf(primary)
	if s == nil {
		return nil
	}
	var mask []bool
	for i, r := range runes {
		if !render.BMPPictograph(r) || c.setCovers(s, r) {
			continue
		}
		if mask == nil {
			mask = make([]bool, len(runes))
		}
		mask[i] = true
	}
	return mask
}

// setCovers reports whether ANY face in set s has a glyph for r — the same cmap
// question coverRunes asks, factored out so the pictograph gate and the per-rune
// face resolution can never disagree about what "covered" means.
func (c *Ctx) setCovers(s *fontSet, r rune) bool {
	for _, cov := range s.cover {
		if coverHasRune(cov, &c.sfntBuf, r) {
			return true
		}
	}
	return false
}

// isInvisibleRune reports the formatting characters that are SUPPOSED to render as
// nothing — joiners, variation selectors, directional marks, the BOM. They rarely
// appear in a cmap, so a coverage miss on them is not a missing font and must not
// spend a census slot (or, once the last-resort draw lands, put a visible box
// where the text intends a seam).
func isInvisibleRune(r rune) bool {
	switch {
	case r == 0x00AD: // soft hyphen
		return true
	case r == 0x200B, r == 0x200C, r == 0x200D: // ZWSP / ZWNJ / ZWJ
		return true
	case r >= 0x200E && r <= 0x200F: // LRM / RLM
		return true
	case r >= 0x202A && r <= 0x202E: // bidi embedding / override
		return true
	case r >= 0x2060 && r <= 0x2064: // word joiner and invisible operators
		return true
	case r >= 0x2066 && r <= 0x2069: // bidi isolates
		return true
	case r == 0xFEFF: // BOM / zero-width no-break space
		return true
	case r >= 0xFE00 && r <= 0xFE0F: // variation selectors
		return true
	case r >= 0xE0000 && r <= 0xE007F: // tag characters (emoji tag sequences)
		return true
	case r >= 0xE0100 && r <= 0xE01EF: // variation selectors supplement
		return true
	}
	return false
}

// setIndexOf resolves a picked LOGICAL face to its set, that set's DEVICE sibling
// set, and its index within the set — so the #77 raster path can swap a logical
// face for the device-scaled one at the same chain position. (nil, nil, -1) when
// the face isn't in a scaled set (chrome-only / headless).
func (c *Ctx) setIndexOf(primary *ttf.Font) (logical, device *fontSet, idx int) {
	pairs := c.setPairs()
	for _, p := range pairs {
		for i, f := range p[0].fonts {
			if f == primary {
				return p[0], p[1], i
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
	df := c.fontsForDev(log, dev, log.pct) // build the sibling at the SAME per-element pct
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
	df := c.fontsForDev(log, dev, log.pct)
	devPrimary := logicalPrimary
	if _, _, idx := c.setIndexOf(logicalPrimary); idx >= 0 && idx < len(df) {
		devPrimary = df[idx]
	}
	for i, r := range runes {
		out[i] = devPrimary
		found := false
		for j, cov := range log.cover { // cover is size-independent → shared with the device set
			if j < len(df) && coverHasRune(cov, &c.sfntBuf, r) {
				out[i] = df[j]
				found = true
				break
			}
		}
		// Same census hand-off as coverRunes, and it has to be here too: this branch
		// runs at every UI scale EXCEPT 1:1, the 1:1 case is the only one that
		// delegates upward, and auto-scale ships ON — so a typical maximised window
		// sits at ~130% and the IC chatbox, the single surface this was written for,
		// would never once ask the census for a font it was missing.
		if !found && !isInvisibleRune(r) {
			c.noteUncovered(r)
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
	c.dropThemeElemSets() // the per-element sets (#39) share c.font/c.fontDev on the same rule
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
	c.coverHintFont = nil
	return true
}

// fontsFor returns the LOGICAL set's fonts (the wrap/measure baseline), rebuilding
// when the scale or the chain moved (settings actions — never per frame).
func (c *Ctx) fontsFor(s *fontSet, pct int) []*ttf.Font {
	return c.buildSet(s, c.fontChain, c.chainCover, pct, DefaultScalePct, c.font, false)
}

// themeFontsFor builds element el's own LOGICAL set (#39): the theme-declared
// face when one resolved (face indexes themeFaceData), else the user's own chain
// — the second case still gets a DEDICATED set so an element the theme merely
// SIZED (dangitx2new declares sizes and no families) pins its point size instead
// of thrashing the shared chat/log set.
func (c *Ctx) themeFontsFor(el themeFontElem, face, pct int) []*ttf.Font {
	chain, cover := c.fontChain, c.chainCover
	if face >= 0 && face < len(c.themeFaceData) && face < len(c.themeFaceCover) {
		chain, cover = c.themeFaceData[face:face+1], c.themeFaceCover[face:face+1]
	}
	return c.buildSet(&c.themeElemSets[el], chain, cover, pct, DefaultScalePct, c.font, false)
}

// fontsForDev returns the DEVICE-scaled sibling set (#77): the same chain opened
// at pct×(textDevPct/100) so the message-raster/emoji paths rasterize crisp. It
// keys on textDevPct via s.devPct so a UI-scale change rebuilds it even though
// pct is unchanged. Shares the device chrome face as the embedded last resort.
// It rebuilds from the LOGICAL set's recorded source chain (fontSet.srcChain), so
// a per-element theme set's device sibling uses the theme face, not the global
// chain.
func (c *Ctx) fontsForDev(logical, dev *fontSet, pct int) []*ttf.Font {
	return c.buildSet(dev, logical.srcChain, logical.srcCover, pct, int(c.textDevPct), c.fontDev, true)
}

// buildSet is the shared body: opens the chain at size = UIFontSize×pct×devPct/100²,
// sharing chromeShare (c.font at 1:1, c.fontDev for a device set) as the embedded
// last resort at 1:1. The close-guard and the last-resort share both key off
// chromeShare so a device set never double-closes/leaks the device chrome face.
// isDevice=true builds a device sibling set, whose faces the label/pick caches
// DON'T reference (those key on the LOGICAL font) — so a device rebuild must NOT
// purge them (doing so mid-frame could nil the pickMemo a logical pick just
// populated, one-frame-mis-routing a mixed-script message).
// chain/chainCover are the override faces this set is built FROM: the user's own
// chain for the global chat/log sets, one theme-declared face for a per-element
// set (#39). They are recorded on the set so its device sibling can be rebuilt
// from the same source.
func (c *Ctx) buildSet(s *fontSet, chain [][]byte, chainCover []*sfnt.Font, pct, devPct int, chromeShare *ttf.Font, isDevice bool) []*ttf.Font {
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
		c.coverHintFont = nil
	}
	s.fonts = s.fonts[:0]
	s.cover = s.cover[:0]
	s.pct, s.devPct, s.gen = pct, devPct, c.fontChainGen
	s.srcChain, s.srcCover = chain, chainCover
	// Fold the device scale into the point size (#77): the per-element pct AND the
	// global device scale both multiply UIFontSize, so a device set rasterizes at
	// the final device pixel size. devPct==100 reproduces the logical size exactly.
	size := UIFontSize * pct * devPct / (DefaultScalePct * DefaultScalePct)
	if size < 1 {
		size = 1
	}
	// Each entry appends its rendering font (per size) and its sfnt cover face (size-
	// independent, parsed once) in lockstep, so a skipped face never desyncs them.
	for i, data := range chain {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, coverAt(chainCover, i))
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
	// Census faces after even that: they exist only because something reached the
	// draw that no curated tier covered, so they must never win a rune a curated
	// face could have drawn better (fontcensus.go explains the ranking).
	for i, data := range c.censusData {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, coverAt(c.censusCover, i))
		}
	}
	return s.fonts
}

// noteUncovered records a rune that no face in the chain could draw, so the App
// can ask the census whether anything installed on this machine can. Called from
// the raster BUILD path only (coverRunes), never per frame, and allocation-free:
// the queue is a fixed array that stops accepting once full.
func (c *Ctx) noteUncovered(r rune) {
	if c.missingN >= len(c.missingRunes) {
		return
	}
	for i := 0; i < c.missingN; i++ {
		if c.missingRunes[i] == r {
			return // already queued this frame
		}
	}
	c.missingRunes[c.missingN] = r
	c.missingN++
}

// TakeMissingRunes copies out (and clears) the runes nothing could draw. dst is
// the caller's reusable buffer so the drain allocates nothing; the returned slice
// aliases it. Render thread.
func (c *Ctx) TakeMissingRunes(dst []rune) []rune {
	dst = dst[:0]
	for i := 0; i < c.missingN; i++ {
		dst = append(dst, c.missingRunes[i])
	}
	c.missingN = 0
	return dst
}

// AddCensusFace installs one face the census found for a rune the chain could not
// draw. Mirrors SetFallbackFonts: the bytes must outlive the faces (RWFromMem
// aliases them), the chain generation bumps so every per-size set rebuilds, and
// the pointer-keyed caches purge because their baked per-rune picks — including
// the cached "nothing to add" verdicts — were decided against the old chain.
// Bounded by censusFaceCap; past it the call is a no-op. Render thread.
func (c *Ctx) AddCensusFace(data []byte) {
	if len(data) == 0 || len(c.censusData) >= censusFaceCap {
		return
	}
	cover := parseCover(data)
	if cover == nil {
		return // unparseable: adding it could only displace a face that works
	}
	c.censusData = append(c.censusData, data)
	c.censusCover = append(c.censusCover, cover)
	c.fontChainGen++
	c.purgeEmojiCache()
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
// provides every VISIBLE rune of text — a Cyrillic line stays on the embedded font,
// a Tifinagh / Indic line resolves to the covering fallback face. cover is aligned
// with fonts; the last entry is the unconditional fallback (used when nothing
// covers, so the result is at worst the same .notdef box as before).
//
// VISIBLE is the whole rule. An invisible formatting rune — a joiner, a directional
// mark, the zero-width sidechannel courtroom/spritestyle.go rides on IC text — draws
// as nothing when it works, so it must never VETO a face for the words around it. It
// used to: the embedded face has a glyph for none of the nine codec runes, so one of
// them anywhere in a message walked the pick off the primary face and onto whichever
// fallback happened to carry zero-width glyphs, and the whole ASCII line then rendered
// in that stray family. noteUncoveredIn has applied exactly this skip to the CENSUS
// since it was written; the pick, which is what the reader actually sees, never got it.
//
// The second result stays HONEST — true only when the face covers every rune, the
// invisible ones included. That bool is the raster gate (coversFace), and a face with
// no glyph for a formatting rune must still let the per-glyph raster hand that one rune
// to a face that has it, instead of blitting the whole line through a .notdef box.
func pickFont(fonts []*ttf.Font, cover []*sfnt.Font, buf *sfnt.Buffer, text string) (*ttf.Font, bool) {
	if len(fonts) == 1 {
		return fonts[0], true
	}
	for i, f := range fonts[:len(fonts)-1] {
		if i >= len(cover) {
			continue
		}
		if visible, all := coverScan(cover[i], buf, text); visible {
			return f, all
		}
	}
	// Fell through — no face covers even the visible runes (a mixed-script run). The
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

// coverScan answers the pick's two different questions in ONE pass over text:
//
//	visible — does f have a glyph for every rune that is SUPPOSED to draw something?
//	          This is the PICK rule (see pickFont): an isInvisibleRune formatting
//	          character cannot disqualify a face for the visible text around it.
//	all     — does f have a glyph for every rune, invisible ones included? This is
//	          the raster gate, and it must stay honest so a formatting rune the face
//	          lacks still routes through coverRunes instead of drawing .notdef.
//
// The two answers differ only for the runes isInvisibleRune names, which is exactly
// the point. A nil face covers nothing. Allocation-free: the reused sfnt buffer, and
// an early return the moment a VISIBLE rune is missing (the common rejection).
func coverScan(f *sfnt.Font, buf *sfnt.Buffer, text string) (visible, all bool) {
	if f == nil {
		return false, false
	}
	all = true
	for _, r := range text {
		if coverHasRune(f, buf, r) {
			continue
		}
		all = false
		if !isInvisibleRune(r) {
			return false, false // a rune that must draw is missing: this face is out
		}
	}
	return true, all
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
	// Belt and braces on the panel font: every bracket restores it with defer, so
	// this should always already be nil. It is zeroed anyway for the reason the
	// overlay fence below documents — a panic unwinding a bracket must not leave
	// the whole client permanently dressed in one panel's font.
	c.panelFont = nil
	// Tab focus cycling runs on the PREVIOUS frame's field order — by now
	// the draw pass that followed the keypress has recorded every visible
	// TextField (one frame of latency, imperceptible at frame rate).
	if c.tabPressed && len(c.fieldSeq) > 0 {
		// The chat fields cycle in AO2's chain (showname → IC → OOC → OOC name,
		// courtroom.cpp:187-210 construction order), not in the draw order the
		// layouts happen to use — every other field keeps draw order. See
		// tabchain.go. Runs only on a Tab press, so the render path is untouched.
		orderTabChain(c.fieldSeq)
		c.focusID = cycleField(c.fieldSeq, c.focusID, c.tabShift)
		c.caretOn = true // land visible: the caret shows where focus went
		c.caretAcc = 0
	}
	// fieldSeq deliberately does NOT clear here: it rolls over in BeginDraw, at
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
	// Overlay fences are per-frame publications (overlayfence.go). Zeroing them
	// here — not trusting each publisher's release — is what keeps the registry
	// from ever becoming the stranded-modalOn freeze: a pass that returns early
	// still starts the next frame with a clear registry. overlayOwner is a scoped
	// suspend that never outlives its draw, but it resets with them so a panic
	// unwinding an owner bracket can't leave the fence permanently disabled.
	c.overlayFenceN, c.overlayFenceDrops = 0, 0
	c.overlayOwner, c.overlayOwnerMark = false, 0

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
	c.wordFwdDel, c.wordLeft, c.wordRight = false, false, false
	c.tailReserveID, c.tailReserveW = "", 0
	c.enter = false
	c.tabPressed = false
	c.escPressed = false
	c.fullscreenReq = false
	c.keyPressed = 0
	c.pasted = ""
	c.pastedRaw = ""
	c.copyReq = false
	c.cutReq = false
	c.undoReq = false
	c.redoReq = false
	c.dropped = ""
	c.hotkey = 0
	c.tipText = ""
	// The armed tip's avoid rect and any container's group rect are per-frame too:
	// a stale anchor would push next frame's tooltip away from a widget that is no
	// longer there.
	c.tipAnchor, c.tipGroup = sdl.Rect{}, sdl.Rect{}
	x, y, _ := sdl.GetMouseState()
	c.mouseX, c.mouseY = c.toLogical(x), c.toLogical(y)
	mods := sdl.GetModState()
	c.ctrlHeld = mods&sdl.KMOD_CTRL != 0
	c.shiftHeld = mods&sdl.KMOD_SHIFT != 0
	c.altHeld = mods&sdl.KMOD_ALT != 0
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
//
// The truncate is a SWAP, not a clear: the outgoing list becomes drawnFieldSeq
// (what is on screen right now) and this frame builds into the buffer that held
// the frame-before-last's, so both answers exist for the whole pass and neither
// costs an allocation. Without it every pre-screen reader of "did this field
// draw?" — App.Frame runs ~280 lines of them before the first TextField
// registers — was reading an empty list.
func (c *Ctx) BeginDraw() {
	c.drawnFieldSeq, c.fieldSeq = c.fieldSeq, c.drawnFieldSeq[:0]
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
				// "v" is dead — clipboard ate it).
				if c.focusID != "" {
					if text, err := sdl.GetClipboardText(); err == nil && text != "" {
						c.pasted += flattenClipboard(text)
						c.pastedRaw += text
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
			case sdl.K_DELETE:
				// Ctrl+Delete = the FORWARD word delete, Ctrl+Backspace's twin. Same
				// focused-only capture rule and the same WordDelete pref gate at
				// consumption, for the same reason: with the pref off it must be a
				// consumed no-op inside a field, never a key that leaks to a hotkey
				// while you are typing.
				if c.focusID != "" {
					c.wordFwdDel = true
				} else {
					c.hotkey = e.Keysym.Sym
				}
			case sdl.K_LEFT, sdl.K_RIGHT:
				// Ctrl+arrow = word-granular caret jump; with Shift the existing
				// extend path (c.shiftHeld in textField) turns it into a word-granular
				// SELECTION. Navigation, so it is NOT gated on the WordDelete pref —
				// that preference is about destructive edits. Unfocused it falls
				// through to the configurable hotkeys like every other Ctrl chord.
				if c.focusID != "" {
					if e.Keysym.Sym == sdl.K_LEFT {
						c.wordLeft = true
					} else {
						c.wordRight = true
					}
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
	c.SetTextDevScale(int(float32(pct)*c.devScale + 0.5)) // device scale (crisp); mouse stays logical
}

// SetDeviceScaleFactor records the renderer drawable's device pixel scale
// (drawable px per window point; 2 on a macOS Retina window, 1 elsewhere). It
// folds into text rasterization only (via SetUIScale), never mouse unprojection:
// SDL reports the mouse in POINTS on macOS, so the pointer must stay on the
// logical scale. Called once at startup after the renderer is created.
func (c *Ctx) SetDeviceScaleFactor(f float32) {
	if f < 1 {
		f = 1
	}
	if c.devScale == f {
		return
	}
	c.devScale = f
	c.SetTextDevScale(int(float32(c.uiPct)*f + 0.5))
}

// RenderScalePct reports the scale percent Ren is currently drawing at — what the
// frame loop passed to SetScale, unless an offscreen pass has bracketed one of its
// own (renderPctOverride). The device-exact message blit needs this and NOT
// textDevPct: the two normally agree, but the export bracket moves textDevPct
// alone and the pinned-tab pass moves the renderer alone, and a blit that assumed
// they always matched would draw at the wrong size in both. Reading it here also
// avoids a per-frame Ren.GetScale(), which heap-allocates through cgo.
func (c *Ctx) RenderScalePct() int32 {
	if c.renderPctOverride > 0 {
		return c.renderPctOverride
	}
	return c.uiPct
}

// beginRenderScaleOverride / endRenderScaleOverride bracket an offscreen pass that
// sets its own renderer scale, so RenderScalePct keeps telling the truth inside it.
func (c *Ctx) beginRenderScaleOverride(pct int32) { c.renderPctOverride = pct }

func (c *Ctx) endRenderScaleOverride() { c.renderPctOverride = 0 }

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
	// A later-drawn overlay published its painted footprint this pass
	// (overlayfence.go): it owns those PIXELS, so it must own the INPUT there too.
	// Without this, pressing a compact-toolbox chip that sits over an AO2 theme's
	// control rect also pressed the button underneath — issue #26, and PART of open
	// issue #37: this is the only place the registry is consulted, so it covers
	// hovering()-based hit tests only. Raw pointIn() sites (editOverToolbox, the
	// dropdown's own list rows, the editors' drag tests) still see through a fence
	// unless they call c.overlayFenced themselves. Checked AFTER modalOn and clipOn:
	// a modal outranks every rect-scoped rule, and a clipped widget doesn't exist
	// outside its clip at all. The occluder itself hit-tests from inside
	// pushOverlayOwner, so its own chips stay live.
	if c.overlayFenced(c.mouseX, c.mouseY) {
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

// clipNowhere is the 1x1 rect parked far off-screen that stands in for an EMPTY
// clip. SDL_RenderSetClipRect treats a w<=0 || h<=0 rect as "disable clipping"
// (SDL_render.c: `if (rect && rect->w > 0 && rect->h > 0) … else clipping_enabled
// = SDL_FALSE`), i.e. handing it a degenerate rect does the exact OPPOSITE of
// what the caller asked for. Clipping to a rect no draw and no hit test can
// reach is the only way to say "nothing lands". Same idiom as the settings-index
// harvest pass (buildSettingsIndex).
var clipNowhere = sdl.Rect{X: -1 << 14, Y: -1 << 14, W: 1, H: 1}

// clipIntersect is pushClip's rect arithmetic, split out so the nesting
// contract is testable without a renderer. An inner clip can only ever NARROW
// the region already in force; a disjoint (or degenerate) result becomes
// clipNowhere rather than an empty rect, because SDL reads an empty rect as
// "clipping off".
func clipIntersect(r, prev sdl.Rect, had bool) sdl.Rect {
	if had {
		sect, hit := r.Intersect(&prev)
		if !hit || sect.W <= 0 || sect.H <= 0 {
			return clipNowhere
		}
		return sect
	}
	if r.W <= 0 || r.H <= 0 {
		return clipNowhere
	}
	return r
}

// pushClip scissors rendering to r INTERSECTED with the clip already in force,
// and returns the clip state to restore with popClip (the themed log lists draw
// inside element rects, so a blind SetClipRect(nil) reset could clobber an outer
// clip). Scrollable lists wrap their line loop in push/pop so a partially
// scrolled top/bottom row is cut at the rect edge instead of spilling over
// neighbouring widgets — that overspill is what struck the tab strip through the
// first OOC line.
//
// The intersection is load-bearing, not tidiness. A nested push USED to replace
// the outer clip outright, so any inner push that deliberately reaches outside
// the panel — labelEmoji's raster band is exactly the row height at the row's y,
// which for the partially scrolled top row starts ABOVE the list — punched a
// hole in the panel's own scissor for the duration of the blit and then restored
// it, letting that one row paint over the widget's border and the art behind it.
// Only rows that took the raster path (colour emoji / mixed script) escaped,
// which is why it looked like a font bug rather than a clip bug.
//
// Returns by value (no closure) so the per-frame log/list draws stay
// allocation-free — the render loop must not heap-allocate. sdl.Rect.Intersect
// is pure Go in go-sdl2 (no cgo, nothing escapes).
func (c *Ctx) pushClip(r sdl.Rect) (prev sdl.Rect, had bool) {
	// The previous clip comes from the mirror fields, NOT an SDL query:
	// GetClipRect's named-return pointer escapes through cgo and heap-allocates
	// per call. The mirror is authoritative because all NESTED clipping flows
	// through pushClip/popClip — the remaining raw Ren.SetClipRect sites are
	// leaf draw-only regions (the #31 audit) that never pushClip inside their
	// clip window.
	prev, had = c.clipRect, c.clipOn
	r = clipIntersect(r, prev, had)
	c.cgoRect = r // &local would escape through cgo and heap-allocate per call
	_ = c.Ren.SetClipRect(&c.cgoRect)
	// Mirror the clip in plain fields so hovering() can honour it without a cgo
	// query per hit test (input clipping — see hovering).
	c.clipOn, c.clipRect = true, r
	return prev, had
}

// pushMuted greys out and disarms every Checkbox drawn until the matching
// popMuted, for rows a master toggle has overruled. It returns the previous
// value rather than assuming false so the brackets nest, matching pushClip.
func (c *Ctx) pushMuted(on bool) (prev bool) {
	prev, c.muted = c.muted, c.muted || on
	return prev
}

// popMuted restores the mute state captured by pushMuted.
func (c *Ctx) popMuted(prev bool) { c.muted = prev }

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
	return c.textTextureBold(text, col, font, false)
}

// textTextureBold is textTexture with an explicit weight: bold rasterizes the
// SAME face through SDL_ttf's STYLE_BOLD (the emboldening render/text.go's
// buildSpan already uses for styled message spans) instead of drawing the plain
// glyphs twice a pixel apart.
//
// WHY THE DOUBLE DRAW HAD TO GO (F1b). A 1 px offset is a LOGICAL offset, and at a
// fractional UI scale the renderer multiplies it: at 105% the two passes land 1.05
// device px apart, each on its own sub-pixel phase, and the result reads as two
// smeared copies of the name rather than one bold one — beside message text on the
// same row that draws once and stays crisp. Rendering the weight into the glyphs
// makes the label ONE device-exact texture at any scale, which is the same rule the
// #77 field/message work applies everywhere else.
//
// The style is set on the DEVICE face for exactly the RenderUTF8Blended call and
// restored by defer, so the shared cached face never leaks a weight into the plain
// text drawn next (buildSpan's idiom, same reason).
func (c *Ctx) textTextureBold(text string, col sdl.Color, font *ttf.Font, bold bool) (cachedText, bool) {
	if text == "" || font == nil {
		return cachedText{}, false
	}
	// Cache by the LOGICAL font (what callers pass) but RASTERIZE with its device
	// sibling (#77): the texture is device-sized, and cachedText.devPct records
	// the scale so blitLabel divides it back to logical. A textDevPct change
	// purges this whole cache (SetTextDevScale), so a stale-size entry can't leak.
	key := textKey{text: text, color: col, font: font, bold: bold}
	if t, ok := c.textCache[key]; ok {
		return t, true
	}
	dev := c.deviceTextFont(font) // chrome/set logical face → device-scaled face
	if bold {
		dev.SetStyle(ttf.STYLE_BOLD)
		defer dev.SetStyle(ttf.STYLE_NORMAL) // always restore, even on error/panic
	}
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
	df := c.fontsForDev(log, dev, log.pct)
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
	// The panel-face width memo is keyed BY FONT POINTER, so it has to die with the
	// faces — and it is cleared HERE rather than beside the widthCache clears for a
	// specific reason. This is the one choke point every face-invalidating path
	// reaches (SetThemeFaces, SetChromeFont, buildSet's rebuild, SetTextDevScale),
	// and SetThemeFaces is NOT one of the sites that clears widthCache even though
	// it calls dropThemeElemSets, which Closes every per-element face. A
	// per-element face is exactly what a panel bracket arms, so anywhere else this
	// map would hold dangling pointers across a theme swap.
	clear(c.panelWidth)
	// logDevWidth shares panelWidth's face-pointer-keyed lifecycle (see its field
	// doc): a stale entry here would silently keep serving a log selection the
	// PREVIOUS device scale's glyph metrics after a scale or face change.
	clear(c.logDevWidth)
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

// uiDeviceExactAt is blitLabel's gate, the ui-package twin of
// render.deviceExactAt (internal/render/text.go) — same equality, same
// "not worth it at the identity scale" exclusion. It compares the RENDERER'S
// CURRENT scale (renderPct, from Ctx.RenderScalePct — an offscreen export
// bracket can move this away from the label's own devPct) against the specific
// TEXTURE's own device build scale (devPct, cachedText.devPct, stamped at
// rasterization time), not the weaker "is any fractional scale active"
// predicate deviceExactText uses: a label rasterized during one scale and
// blitted during another (the export/pinned-tab brackets) must NOT take the
// 1:1 shortcut, or it draws at the wrong on-screen size.
func uiDeviceExactAt(renderPct, devPct int32) bool {
	return renderPct > 0 && renderPct != DefaultScalePct && renderPct == devPct
}

// blitLabel copies a cached label (atlas sub-rect aware) through the scratch
// rects — zero heap escapes on the per-frame draw path. wLog is the LOGICAL
// destination width (a full label passes t.logicalW(); a clipped one a smaller
// logical maxW).
//
// F1 (blur still there after v1.93.0): at a fractional UI scale the SCALED path
// below computes its source crop and destination width through two
// INDEPENDENTLY-rounding formulas (a truncating multiply here, a separate
// round-half-up divide in whichever caller measured wLog, then the ambient
// SetScale re-multiplies the destination) — the two rects can end up describing
// different physical device spans, and HINT_RENDER_SCALE_QUALITY=1 (main.go)
// turns that mismatch into a real bilinear blend at the label's edge, on every
// plain Label/Button/Checkbox/Heading/Tooltip in the client, not just the
// emoji/mixed-script surfaces v1.93.0 (12d1965) fixed. Gated on the SAME
// equality MessageRaster.deviceExact/Badge.Draw already use (uiDeviceExactAt),
// blitLabelExact draws src.W==dst.W under SetScale(1,1) instead — no resample is
// even possible when the two rects are the same value. devPct==100 (or an
// export bracket that disagrees with the renderer's own scale) keeps this exact
// pre-#77 1:1-at-scale-1 / scaled-at-scale-N behavior, byte-identical.
func (c *Ctx) blitLabel(t cachedText, x, y, wLog int32) {
	if uiDeviceExactAt(c.RenderScalePct(), t.devPct) {
		c.blitLabelExact(t, x, y, wLog)
		return
	}
	c.blitLabelScaled(t, x, y, wLog)
}

// blitLabelScaled is blitLabel's pre-generalization path, kept as its own named
// function (not inlined) so a test can drive it directly as the "old behavior"
// reference for an A/B pixel comparison without re-deriving its arithmetic. #77:
// the SRC sub-rect stays in DEVICE px (the texture is device-sized), the DST in
// LOGICAL px (the renderer's ambient SetScale maps it back onto device pixels —
// exact only when that mapping happens to round-trip, see blitLabel's doc).
func (c *Ctx) blitLabelScaled(t cachedText, x, y, wLog int32) {
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

// blitLabelExact is blitLabel's device-exact bracket — the same SetScale(1,1) /
// reassert-the-clip-in-device-pixels / restore-by-recomputing shape
// devFieldValue (ui.go), MessageRaster.draw (internal/render/text.go) and
// Badge.Draw (internal/render/badge.go) already use, generalized to every kit
// label instead of only the focused field's moving parts. Only reached when
// uiDeviceExactAt has already confirmed the texture's own device scale matches
// the renderer's current one.
//
// THE FIX, restated: src.W and dst.W are the SAME computed value (devW below),
// so SDL is never asked to resample — the two rects can no longer describe
// different physical spans, which is the whole mismatch blitLabelScaled can hit.
//
// THE METAL CLIP TRAP (documented at length in text.go's DrawScaled and
// badge.go's Draw): macOS's Metal backend evaluates a clip rect against the
// scale in force when it's USED, not when it was SET, so flipping to
// SetScale(1,1) underneath an already-set logical clip silently shrinks it to
// 1/scale of its intended size for the duration of this blit. c.clipOn/
// c.clipRect (the pushClip mirror) is re-projected to device pixels and
// re-asserted INSIDE the bracket, in the same order those three call sites use,
// so every backend reads the same rows. c.cgoRect is blitLabel's own scratch
// mirror for this (same reuse discipline devFieldValue's clip handling already
// uses on this field — nothing else touches it mid-bracket, this function calls
// no pushClip/popClip/Fill/Border of its own).
//
// Allocation-free: c.drawSrc/c.drawDst (already-reused scratch rects) and
// c.cgoRect carry every rect: no &local ever crosses into cgo. The scale is
// restored by RECOMPUTING it from c.RenderScalePct(), never by Ren.GetScale(),
// which takes the address of named returns for cgo and heap-allocates two
// float32s per call — a per-widget-per-frame allocation this draw path (called
// for every Label/Button/Checkbox on screen) cannot have.
func (c *Ctx) blitLabelExact(t cachedText, x, y, wLog int32) {
	dev := t.devPct

	// One device-width computation feeds BOTH src and dst (the fix). Clamped to
	// the texture's own device width as a safety net — the same clamp
	// blitLabelScaled applies to its srcW, now guarding a dst that can never
	// disagree with it.
	devW := uiDeviceFromLogical(wLog, dev)
	if devW > t.w {
		devW = t.w
	}

	hadClip, prevClip := c.clipOn, c.clipRect
	_ = c.Ren.SetScale(1, 1)
	if hadClip {
		c.cgoRect = sdl.Rect{
			X: uiDeviceFromLogical(prevClip.X, dev), Y: uiDeviceFromLogical(prevClip.Y, dev),
			W: uiDeviceFromLogical(prevClip.W, dev), H: uiDeviceFromLogical(prevClip.H, dev),
		}
		_ = c.Ren.SetClipRect(&c.cgoRect)
	}

	c.drawSrc = sdl.Rect{X: t.src.X, Y: t.src.Y, W: devW, H: t.h}
	c.drawDst = sdl.Rect{X: uiDeviceFromLogical(x, dev), Y: uiDeviceFromLogical(y, dev), W: devW, H: t.h}
	_ = c.Ren.Copy(t.tex, &c.drawSrc, &c.drawDst)

	s := float32(c.RenderScalePct()) / float32(DefaultScalePct)
	_ = c.Ren.SetScale(s, s)
	if hadClip {
		c.cgoRect = prevClip
		_ = c.Ren.SetClipRect(&c.cgoRect)
	} else {
		_ = c.Ren.SetClipRect(nil)
	}
}

// Label draws text at (x, y) and returns its LOGICAL pixel width.
//
// F2: under an armed row-label limit the text is ellipsised at the container's
// right edge instead of running past it. Checkbox is not the only row that carries
// a sentence — the settings form draws roughly a hundred help lines as bare labels,
// and the card's clip turned each of them into a HARD cut at the border, which is
// indistinguishable from a rendering fault and is exactly how the overflow was
// reported. Unarmed (every caller outside a push/popRowLabelLimit bracket — the
// courtroom, the lobby, every themed screen) fitRowLabel is the identity on one
// integer compare, so those draws are byte-for-byte what they were.
func (c *Ctx) Label(x, y int32, text string, col sdl.Color) int32 {
	if shown, fit := c.fitRowLabel(x, text); !fit {
		text = shown
	}
	t, ok := c.textTexture(text, col, c.chromeFaceFor(text))
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
//
// F2: an armed row-label limit narrows maxW to the container's right edge AND
// ellipsises, because maxW alone is a HARD cut — the caller's own bound, which for
// the settings font-chain line is the WINDOW rather than the card it draws in. The
// two bounds compose: whichever is tighter wins, and unarmed this is the previous
// call verbatim.
func (c *Ctx) LabelClipped(x, y, maxW int32, text string, col sdl.Color) {
	if shown, fit := c.fitRowLabel(x, text); !fit {
		text = shown
		if avail := c.rowLabelRight - x; avail > 0 && avail < maxW {
			maxW = avail
		}
	}
	c.LabelClippedFont(c.chromeFaceFor(text), x, y, maxW, text, col)
}

// LabelClippedFont is LabelClipped with an explicit font (scaled log/OOC
// text). Cached like every label; the cache keys by font identity. The
// clip composes with the label's atlas sub-rect. maxW is LOGICAL px.
func (c *Ctx) LabelClippedFont(font *ttf.Font, x, y, maxW int32, text string, col sdl.Color) {
	c.LabelClippedFontWeight(font, x, y, maxW, text, col, false)
}

// LabelClippedFontWeight is LabelClippedFont with a weight: bold draws the face's
// real bold cut ONCE (textTextureBold), replacing the 1 px-offset second pass the
// bold-name surfaces used to make. bold==false is byte-identical to
// LabelClippedFont — same key, same texture, same blit — so every plain caller is
// untouched.
func (c *Ctx) LabelClippedFontWeight(font *ttf.Font, x, y, maxW int32, text string, col sdl.Color, bold bool) {
	t, ok := c.textTextureBold(text, col, font, bold)
	if !ok {
		return
	}
	w := t.logicalW()
	if w > maxW {
		w = maxW
	}
	c.blitLabel(t, x, y, w)
}
