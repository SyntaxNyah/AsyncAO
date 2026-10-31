package ui

import (
	"strings"
	"time"
	"unsafe"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

const (
	// UIFontSize is the default chrome size, small because the renderer never scales down (downscaled text is blurry).
	UIFontSize = 12
	// UIFontSizeBig is for headings.
	UIFontSizeBig = 18

	// textCacheMax bounds the label texture cache, purged wholesale past it. Above the worst case visible at once
	// (a 4K char-select grid draws about 1200 labels), so a purge stays rare.
	textCacheMax = 2048

	scrollStepPx = 32
	caretBlink   = 500 * time.Millisecond
	// doubleClickWindow/Slop: a second left release within the window and within a few logical px of the first.
	doubleClickWindow = 400 * time.Millisecond
	doubleClickSlop   = 6

	// DefaultScalePct is the 1:1 text/layout scale (percent).
	DefaultScalePct = 100

	// fieldCaretWLog is the caret's logical width, projected to a constant device width so it can't flicker at
	// fractional scale.
	fieldCaretWLog = 2

	// scrollThumbMinPx keeps the scrollbar thumb grabbable on long lists.
	scrollThumbMinPx = 24
	// scrollGrabSlopPx widens the scrollbar's hit zone so drags don't drop when the cursor drifts.
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
	// Wardrobe/background folder icons; not theme-overridden, so kept out of defaultKitColors.
	ColFolder   = sdl.Color{R: 60, G: 72, B: 98, A: 255}
	ColFolderHi = sdl.Color{R: 80, G: 98, B: 132, A: 255}
)

// textKey keys the label cache by font pointer, so scaled fonts coexist with the chrome fonts in one cache.
type textKey struct {
	text  string
	color sdl.Color
	font  *ttf.Font
	// bold selects SDL_ttf's STYLE_BOLD; in the key because a bold and a plain string are different pixels.
	bold bool
}

type cachedText struct {
	tex  *sdl.Texture
	src  sdl.Rect // sub-rect inside tex (the atlas page or a dedicated texture)
	w, h int32    // DEVICE px of the rasterized texture
	// owned marks a dedicated non-atlas texture the purge must destroy individually.
	owned bool
	// devPct is the device font scale the texture was rasterized at (100 = 1:1); blitLabel divides w/h back to logical
	// by it.
	devPct int32
	// inkTop and inkBottom are the first and last device rows with a visible pixel; IconLabel centres a symbol by them.
	inkTop, inkBottom int32
}

// Label atlas: labels share pages, so a text-heavy frame costs a few texture binds instead of one per label.
const (
	// textAtlasPageEdge / textAtlasMaxPages bound the atlas; beyond that, labels fall back to dedicated textures.
	textAtlasPageEdge = 1024
	textAtlasMaxPages = 4
	// textAtlasPad keeps one transparent pixel between labels so linear filtering never bleeds neighbors.
	textAtlasPad = 1
)

// shelfPacker allocates left-to-right shelves top-down inside one square page.
type shelfPacker struct {
	edge   int32
	penX   int32
	shelfY int32
	shelfH int32
}

// take reserves a w×h area, opening a new shelf when the row is full; ok=false when the page cannot fit it.
func (p *shelfPacker) take(w, h int32) (sdl.Rect, bool) {
	if w > p.edge || h > p.edge {
		return sdl.Rect{}, false
	}
	w += textAtlasPad
	h += textAtlasPad
	if p.penX+w > p.edge || h > p.shelfH {
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
	// win backs FlashWindow; nil in headless tests, as flashing is best-effort.
	win *sdl.Window

	// musicMarquee is the one scrolling label's state; on Ctx because it is kit-level display state surviving between
	// frames.
	musicMarquee marqueeSlot

	font    *ttf.Font
	fontBig *ttf.Font
	// Device-scaled chrome faces, rasterized at final device size and drawn 1:1 to avoid SetScale blur. c.font stays
	// the logical layout reference; only textTexture uses these. At textDevPct==100 they share font/fontBig.
	fontDev, fontBigDev *ttf.Font

	// panelFont is the chrome face for the panel being drawn, nil for the client's own; widgets read it via
	// chromeFace(). An override, never a swap of c.font: every font-lifetime guard compares against c.font, so
	// assigning over it would Close a face still in use. BeginFrame also zeroes it, so a panic can't leave the client
	// in one panel's font.
	panelFont *ttf.Font
	// panelWidth memoises TextWidth for a panel face, keyed by face and string, as widthCache belongs to the chrome
	// face.
	panelWidth map[panelWidthKey]int32
	// logDevWidth memoises devTextWidthWeight, the device-face width for log selection; separate from panelWidth, which
	// holds logical widths for the same key. Keyed by the logical font so purgeTextCache covers it.
	logDevWidth map[panelWidthKey]int32

	// User-scaled font sets (chat box, log/OOC lists): the user's chain plus the embedded last resort.
	chatSet fontSet
	logSet  fontSet
	// Device-scaled siblings of chatSet/logSet; the logical sets stay the wrap/measure baseline.
	chatSetDev fontSet
	logSetDev  fontSet
	// The chrome set: the same chain headed by the chrome face, so plain labels can fall back to a covering face
	// instead of drawing tofu. Built on the first non-ASCII chrome label.
	chromeSet    fontSet
	chromeSetDev fontSet
	// textDevPct is the device font scale (device px = logical px × textDevPct/100). Normally uiPct; export and
	// offscreen paths set it to DefaultScalePct so the UI scale can't leak into export resolution.
	textDevPct int32
	// fontChain holds the override TTF/TTC bytes in chain order (≤ fontChainCap); they must outlive their fonts,
	// because RWFromMem points straight at them.
	fontChain    [][]byte
	fontNames    []string // diagnostics (settings status line)
	fontChainGen int      // bumped per SetFontChain; sets rebuild lazily
	// chromeData holds the whole-UI override bytes; nil = embedded. SetChromeFont compares by slice identity.
	chromeData []byte
	// pickMemo caches per-line font picks and whether the pick fully covers the line; a coverage scan per rune per row
	// every frame would be too slow. Cleared on any font rebuild.
	pickMemo map[pickKey]pickResult
	// coverHint is the last pick, so the covers() call right after *FontFor is a compare. coversFace is correct on a
	// miss.
	coverHintFont *ttf.Font
	coverHintText string
	coverHintOK   bool
	// Colour-emoji face, kept apart from the chain so the single-font fast path is unchanged. emojiData is the file
	// bytes (nil = not loaded) and must outlive the faces, which alias it through RWFromMem.
	emojiData  []byte
	emojiFonts map[int]*ttf.Font
	// emojiCover is emojiData's cmap, for buildSet. emojiAsked stops the byte scan for emoji after the first request.
	emojiCover *sfnt.Font
	emojiAsked bool
	// Broad-Unicode text fallback faces, loaded off-thread after the first non-ASCII rune; they join the per-size set.
	// The bytes must outlive the faces (RWFromMem). Bumping fontChainGen on install rebuilds every set.
	fallbackData [][]byte
	// sfnt coverage faces in the same order as a set's entries (chain, embedded, fallback). sfntBuf is the reused
	// GlyphIndex scratch. wantFallback is raised by the non-ASCII scan; the App drains it to start the load.
	chainCover    []*sfnt.Font
	fallbackCover []*sfnt.Font
	embeddedCover *sfnt.Font
	sfntBuf       sfnt.Buffer
	wantFallback  bool
	// Each request is raised once: on a machine without the faces, raising it on every draw would keep the loop busy.
	fallbackAsked bool
	cjkAsked      bool
	// CJK tier: big faces (13-35 MB each), loaded only when a CJK letter is drawn; apart so the tiers load
	// independently.
	cjkData  [][]byte
	cjkCover []*sfnt.Font
	wantCJK  bool
	// Faces the system-font search supplied for runes nothing in the curated chain covered (fontcensus.go). Appended
	// last, so they never outrank a curated pick.
	censusData  [][]byte
	censusCover []*sfnt.Font
	// missingRunes records runes nothing could draw, drained once a frame by the App. A fixed array because coverRunes
	// must not allocate; once full it stops recording, and the next frame picks up what is still missing.
	missingRunes [censusQueueCap]rune
	missingN     int
	// Per-element theme faces from courtroom_fonts.ini, read off-thread (≤ themeFaceCap). The bytes must outlive the
	// faces, so SetThemeFaces closes the element sets first. themeFaceCover holds the index-aligned sfnt cmap face.
	themeFaceData  [][]byte
	themeFaceCover []*sfnt.Font
	// themeFaceQt is each face's Qt row pitch: OS/2 usWinAscent/usWinDescent, which AO2's Qt stacks chatbox rows by and
	// SDL_ttf does not expose. Parsed once per apply.
	themeFaceQt []theme.QtRowPitch
	// One logical and one device set per element, not per face: each pins its own size, or two elements at different
	// sizes would make buildSet rebuild and purge on every draw. Built on first use.
	themeElemSets    [themeFontElemCount]fontSet
	themeElemSetsDev [themeFontElemCount]fontSet

	// uiPct is the global render scale percent; mouse coordinates unproject through it so hit-tests stay exact.
	uiPct int32
	// devScale: drawable px per window point (2 on macOS Retina, 1 elsewhere); folds into text raster only.
	devScale float32

	// renderPctOverride, when non-zero, is the scale Ren is actually at during an offscreen pass with its own SetScale.
	// Only the device-exact message blit consults it.
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
	// altHeld is the live Alt state, used by the bare-key binds (bareBindKey). Read with !ctrlHeld, because AltGr on
	// Windows reports as Ctrl+Alt and produces text: a bare test would eat every accented character on a European
	// layout.
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
	// Word editing chords, captured only for a focused field. wordFwdDel is Ctrl+Delete, under the WordDelete pref;
	// wordLeft/wordRight are Ctrl+Left/Right jumps, ungated, and Shift extends through c.shiftHeld.
	wordFwdDel bool
	wordLeft   bool
	wordRight  bool
	// Text-field selection: the anchor (rune index) in the focused field, -1 = none; the caret is the moving end.
	// prevMouseDown feeds the press edge that starts a drag-selection.
	selAnchor     int
	prevMouseDown bool
	// Per-field undo/redo histories: bounded map + LRU order.
	fieldHists   map[string]*fieldHistory
	fieldHistUse []string
	// wheelTaken marks the wheel consumed by a hovered widget, so page-level scrolls don't double-act.
	wheelTaken bool
	mouseDown  bool   // left button currently held (event-tracked)
	middleHeld bool   // middle (wheel) button held — fast log zoom modifier (event-tracked)
	dragID     string // widget owning the active drag ("" = none)
	// onRow, when non-nil, receives every Checkbox's label and y for the settings search; nil in normal play.
	onRow func(label string, y int32)
	// emojiLoad starts the one-shot colour-emoji read; the App owns it, but a widget can be the first to show an emoji.
	// nil in tests.
	emojiLoad func()
	// muted renders Checkbox rows greyed and inert because a master toggle overrules them. They still draw and register
	// with onRow, so settings search finds them.
	muted   bool
	dropped string      // SDL_DROPFILE path this frame ("" = none)
	hotkey  sdl.Keycode // non-clipboard Ctrl chord this frame (0 = none)
	// clipOn/clipRect mirror the renderer clip so hovering() can refuse hits outside it without a cgo query per hit
	// test.
	clipOn   bool
	clipRect sdl.Rect
	// cgoRect is the scratch rect for SDL calls taking *sdl.Rect: a pointer escapes through cgo, so &local allocates on
	// every call. Safe to share: SDL copies the rect during the call, and Ctx is render-thread-only.
	cgoRect sdl.Rect
	// cgoSrc is the second scratch rect, for Renderer.Copy with a non-nil source; reusing cgoRect would have the
	// destination overwrite the source before SDL read it.
	cgoSrc sdl.Rect
	// Chrome shape: resolved once per frame (refreshShapeMasks) so per-widget paths read a field without a store
	// lookup. activeShape=="" or shapeSharp disables the shaped path. shapeMaskReady also needs resident masks, so an
	// unbuilt frame renders sharp. Masks are white-RGB straight-alpha, tinted per draw; the rects are 9-slice scratch,
	// as for cgoRect.
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
	// tipAnchor is what the armed tip describes, unioned with any group rect; drawTooltip keeps the box off it. Zero =
	// none.
	tipAnchor sdl.Rect
	// tipGroup is a container's claim on a run of child tooltips; one rect, as one tooltip can be armed per frame.
	tipGroup sdl.Rect
	// Cached word-wrap of the current tooltip, rebuilt only when the text or wrap width changes.
	tipWrapText string
	tipWrapW    int32
	tipWrapLn   []string

	focusID    string
	caretOn    bool
	caret      int    // caret position (RUNE index) in the focused field's value
	caretField string // which field c.caret belongs to ("" = none); focus change resets it
	caretAcc   time.Duration
	// Hold-to-clear: holding holdKey for holdThreshold wipes the focused field. holdAcc uses SDL live state, so a
	// missed KEYUP can't strand it.
	holdOn        bool
	holdKey       sdl.Keycode
	holdThreshold time.Duration
	holdAcc       time.Duration
	holdFired     bool
	// wordDeleteOn mirrors the WordDelete pref, stamped per frame, as textField has no Prefs handle. Config state, not
	// reset.
	wordDeleteOn bool
	// The focused field's sticky horizontal scroll: it moves only when the caret would leave the view (scrollKeep), so
	// text doesn't slide under the cursor during a drag-selection. Resets with focus.
	fieldScrollID string
	fieldScroll   int32
	// Right-gutter reservation for the next text field with this id (ReserveFieldTail), so its text clips before a
	// widget painted inside its box. A single value, as a map would allocate on the draw path; cleared each BeginFrame.
	tailReserveID string
	tailReserveW  int32

	// Multiline TextArea state: taCaret/taSelAnchor are rune indices, taScroll is in pixels, taCaretField owns the
	// caret. Focus is shared with TextField, but the caret lives here so the single-line math stays independent.
	taCaret      int
	taSelAnchor  int
	taCaretField string
	taScroll     int32

	// Tab focus cycling: TextField records draw order here, and the next BeginFrame moves focus along it.
	// orderTabChain re-seats the chat block into AO2's showname, IC, OOC, OOC-name chain at Tab time.
	fieldSeq []string
	// drawnFieldSeq is fieldSeq from the last finished frame, swapped in BeginDraw. fieldSeq is empty for the
	// pre-screen half of App.Frame, so claimTypingForIC needs this. Swapped, not copied, so it costs no allocation.
	drawnFieldSeq []string
	tabShift      bool // shift held at the Tab press → cycle backwards
	// focusNext is a queued FocusField request, applied at the next BeginFrame so it survives this frame's click-away
	// unfocus.
	focusNext string

	// One dropdown may be open; it owns the pointer, and its list paints in FinishFrame above later widgets. modalOn
	// persists across frames, so widgets drawn before the dropdown are blocked too, and releases at the BeginFrame
	// after the close.
	ddOpen     string
	ddScroll   int32
	ddOpenList sdl.Rect // the OPEN dropdown's list rect (flip-adjusted), stashed each draw — read by boxFencesPointer so a list over a floating panel stays clickable
	// Row geometry of the open list, stamped by dropdownEx for DropdownRowRightClicked. ddHitID names the owner, so a
	// stale stamp from a list closed this frame can't answer for another id.
	ddHitID      string
	ddHitList    sdl.Rect
	ddHitRowH    int32
	ddHitScroll  int32
	ddHitRows    int
	modalOn      bool
	modalRelease bool
	ddDraws      []ddDraw // deferred overlay draws this frame (0 or 1)

	// Overlay registry (overlayfence.go): rects from chrome painted over other widgets, so hovering() can refuse hits
	// under them in a single-pass kit. A fixed array (hard rule 4). overlayOwner suspends the test for the occluder's
	// own widgets; overlayOwnerMark limits that to its own publication onwards, as registry order is paint order.
	// Zeroed every BeginFrame, so an unreleased entry can't freeze the UI. overlayFenceDrops counts refusals when full,
	// which are otherwise silent.
	overlayFences     [overlayFenceCap]sdl.Rect
	overlayFenceN     int
	overlayFenceDrops int
	overlayOwner      bool
	overlayOwnerMark  int

	// Pointer blocking: the floating panel runs the courtroom pass blind while the cursor is over it, then restores
	// state.
	ptrFenced               bool
	fMouseX, fMouseY        int32
	fClicked, fRight, fDown bool
	fWheel                  int32

	// hover preview tracking
	hoverID    string
	hoverSince time.Time
	hoverRect  sdl.Rect // the currently-hovered preview trigger's rect (for the close-on-leave travel corridor)
	// Delayed-tooltip dwell (TooltipAfter), separate from hoverID so sprite-preview and tooltip dwells never clobber
	// each other.
	tipHoverID    string
	tipHoverSince time.Time
	// tipHoverDelay is the active delayed tooltip's dwell; NextHoverDue schedules the event-loop wake from it. Zero =
	// tooltipDwell.
	tipHoverDelay time.Duration
	// Hover-preview config, stamped once per frame from prefs, so HoverPreview is a pure read.
	hoverPreviewOn    bool
	hoverPreviewDelay time.Duration

	textCache map[textKey]cachedText
	atlas     []*atlasPage // shared label pages (≤ textAtlasMaxPages)
	// rowLabelRight is the right edge for chrome row labels, 0 = unset, within pushRowLabelLimit / popRowLabelLimit.
	// rowLabelMemo holds its truncations.
	rowLabelRight int32
	rowLabelMemo  map[rowLabelKey]string

	widthCache map[string]int32 // chrome-font TextWidth memo
	// devWidthCache memoizes the device-face prefix width at a fractional UI scale. A caret must land on the device
	// glyph boundary, and SDL_ttf quantizes advances per point size, so logical and device widths diverge over a long
	// prefix. Stores device px. Purged wherever widthCache is. nil read and clear(nil) are safe, so test-built Ctx stay
	// valid.
	devWidthCache map[string]int32
	// emojiCache holds multi-font rasters for labels mixing text with colour emoji. Entries own dedicated textures, are
	// bounded, and are destroyed on purge (purgeTextCache) and on SetEmojiFont.
	emojiCache map[emojiKey]*render.MessageRaster

	// cgo scratch rects: the address of a stack rect for Ren.Copy forces a heap escape per draw.
	drawSrc, drawDst sdl.Rect
}

func loadEmbeddedFont(size int) (*ttf.Font, error) {
	rw, err := sdl.RWFromMem(goregular.TTF)
	if err != nil {
		return nil, err
	}
	return ttf.OpenFontRW(rw, 1, size)
}

// Font returns the chrome font regardless of which panel is mid-draw.
func (c *Ctx) Font() *ttf.Font { return c.font }

// panelWidthKey carries the face too, or two panels at different sizes would read each other's widths.
type panelWidthKey struct {
	font *ttf.Font
	text string
	// bold: synthetic emboldening widens advances, and the log places the message at name-start + name-width, so
	// measuring plain and drawing bold would slide the message onto the name's last glyph.
	bold bool
}

// chromeFace is the panel's face when one is set, else c.font, which keeps the same cache key and atlas position.
func (c *Ctx) chromeFace() *ttf.Font {
	if c.panelFont != nil {
		return c.panelFont
	}
	return c.font
}

// chromeFaceFor is chromeFace for a known string: for non-ASCII, the chrome set's first face, which carries the
// fallback chain. A panel face is returned as is, being the first face of its element's set.
func (c *Ctx) chromeFaceFor(text string) *ttf.Font {
	face := c.chromeFace()
	if !hasNonASCII(text) {
		return face
	}
	c.noteScript(text) // latch the off-thread fallback load, exactly as ChatFontFor does
	if c.panelFont != nil {
		return face
	}
	fonts := c.buildSet(&c.chromeSet, c.fontChain, c.chainCover, DefaultScalePct, DefaultScalePct, c.font, false)
	if len(fonts) <= 1 {
		return face // nothing loaded yet; the text re-draws when a face lands
	}
	return c.pickIn(&c.chromeSet, fonts, DefaultScalePct, text)
}

// hasNonASCII is the one-byte-pass check in front of every chrome fallback decision.
func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

// pushPanelFont sets f as the chrome face and returns the previous one: defer c.popPanelFont(c.pushPanelFont(face)). By
// value because a restore closure allocates per call on per-frame draws. A nil face or the chrome face writes nothing.
func (c *Ctx) pushPanelFont(f *ttf.Font) *ttf.Font {
	prev := c.panelFont
	if f != nil && f != c.font {
		c.panelFont = f
	}
	return prev
}

// popPanelFont restores what pushPanelFont returned, always via defer so an early return can't leak the face.
func (c *Ctx) popPanelFont(prev *ttf.Font) { c.panelFont = prev }

// fontSet is one scaled chain: overrides, embedded, then fallbacks. cover holds each entry's sfnt face, because
// SDL_ttf's GlyphMetrics returns .notdef metrics without error and can't report coverage.
type fontSet struct {
	fonts []*ttf.Font
	cover []*sfnt.Font
	pct   int
	gen   int
	// devPct is the device scale a device set was built at (logical sets: 0); a UI-scale change rebuilds on it.
	devPct int
	// srcChain/srcCover are the chain this set was built from, so the device sibling can rebuild from the same source.
	srcChain []([]byte)
	srcCover []*sfnt.Font
	// emoji is the set's colour-emoji face, the last entry of fonts, nil while not loaded.
	emoji *ttf.Font
}

// fontChainCap bounds the override chain (a primary plus a few CJK fallbacks).
const fontChainCap = 4

// ChatFont returns the IC box primary font at pct percent of the chrome size; ChatFontFor picks per text.
func (c *Ctx) ChatFont(pct int) *ttf.Font {
	return c.fontsFor(&c.chatSet, pct)[0]
}

// ChatFontFor returns the chat set's first face and records whether it covers text; non-ASCII requests the fallback
// load.
func (c *Ctx) ChatFontFor(pct int, text string) *ttf.Font {
	c.noteScript(text)
	return c.pickSet(&c.chatSet, pct, text)
}

// LogFont returns the log/OOC list primary font at pct percent.
func (c *Ctx) LogFont(pct int) *ttf.Font {
	return c.fontsFor(&c.logSet, pct)[0]
}

// LogFontFor picks the covering font for one log line (see ChatFontFor).
func (c *Ctx) LogFontFor(pct int, text string) *ttf.Font {
	c.noteScript(text)
	return c.pickSet(&c.logSet, pct, text)
}

// ThemeFont returns element el's primary face at pct, from el's own fontSet: the theme's family when face indexes one,
// else the user's chain.
func (c *Ctx) ThemeFont(el themeFontElem, face, pct int) *ttf.Font {
	return c.themeFontsFor(el, face, pct)[0]
}

// ThemeFontFor is ThemeFont with the per-line coverage pick (see ChatFontFor).
func (c *Ctx) ThemeFontFor(el themeFontElem, face, pct int, text string) *ttf.Font {
	c.noteScript(text)
	s := &c.themeElemSets[el]
	return c.pickIn(s, c.themeFontsFor(el, face, pct), pct, text)
}

// SetThemeFaces installs theme face bytes (nil clears). The element sets alias the outgoing bytes (RWFromMem), so they
// are closed here before the bytes lose their last reference. Render thread.
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
		// Same index, same bytes: the Qt row pitch can't belong to a different face than its cover.
		c.themeFaceQt = append(c.themeFaceQt, theme.ReadQtRowPitch(d))
	}
	c.fontChainGen++ // every fontSet (global AND per-element) rebuilds; wrap caches invalidate
	// Labels, emoji rasters and line picks key on the old face pointers; a theme apply is a user action.
	c.purgeTextCache()
	c.pickMemo = nil
	c.coverHintFont = nil
}

// dropThemeElemSets closes every per-element set; a set may share the chrome face as its last resort, which it must not
// close.
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

// noteScript raises the load requests the first time a script is seen: broad fallback on any non-ASCII rune, CJK on a
// CJK rune, kept separate so the first European accent can't swallow a later CJK request. Settled, it skips the byte
// scan.
func (c *Ctx) noteScript(text string) {
	needBroad := c.fallbackData == nil && !c.fallbackAsked
	needCJK := c.cjkData == nil && !c.cjkAsked
	needEmoji := c.emojiData == nil && !c.emojiAsked && c.emojiLoad != nil
	if !needBroad && !needCJK && !needEmoji {
		return
	}
	nonASCII, cjkMaybe := scanScriptBytes(text)
	if !nonASCII {
		return
	}
	if needBroad {
		c.wantFallback, c.fallbackAsked = true, true
		PushWake() // the App starts the load next frame; an idle screen would never run one
	}
	if needCJK && cjkMaybe && hasCJKRune(text) {
		c.wantCJK, c.cjkAsked = true, true
		PushWake()
	}
	if needEmoji && render.NeedsEmojiFallback(text) {
		c.requestEmoji()
	}
}

// requestEmoji asks the App once per session to load the colour-emoji face; SetEmojiFont then rebuilds every set with
// it last.
func (c *Ctx) requestEmoji() {
	if c.emojiData != nil || c.emojiAsked || c.emojiLoad == nil {
		return
	}
	c.emojiAsked = true
	c.emojiLoad()
}

// TakeWantsFallback / TakeWantsCJK report and clear whether their script was seen since the last drain.
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

// pickResult is the face chosen for a line's visible runes and whether it covers every rune, invisible ones included:
// SDL_ttf draws .notdef for any rune the face lacks. covered==false sends coversFace's reader to the per-glyph raster.
type pickResult struct {
	font    *ttf.Font
	covered bool
}

// pickKey scopes a memoized pick to set and scale; text alone would hand back another surface's face.
type pickKey struct {
	text string
	set  *fontSet
	pct  int32
}

// pickSet memoizes the per-line coverage pick; the single-font set (len==1) skips it.
func (c *Ctx) pickSet(s *fontSet, pct int, text string) *ttf.Font {
	return c.pickIn(s, c.fontsFor(s, pct), pct, text)
}

// pickIn is pickSet's body with the faces already built, so a per-element set shares the memo.
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
		// The first face lacks a rune; offer runes no other face has to the system-font search. In the memo-miss branch
		// so this runs once per string, not per frame. Without it, chrome labels could never ask for a missing script.
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

// noteUncoveredIn queues every rune no face in s covers; called only from pickIn's memo-miss branch.
func (c *Ctx) noteUncoveredIn(s *fontSet, text string) {
	for _, r := range text {
		if r < 0x80 || isInvisibleRune(r) {
			continue
		}
		if !anyCoversRune(s.cover, &c.sfntBuf, r) {
			c.noteUncovered(r)
		}
	}
}

// noteCoverHint records the pick so the covers() check right after it is a pointer and string compare.
func (c *Ctx) noteCoverHint(f *ttf.Font, text string, covered bool) {
	c.coverHintFont, c.coverHintText, c.coverHintOK = f, text, covered
}

// coversFace reports whether the pick that returned primary covered text. No memo entry means the single-font fast
// path, which counts as covered. Read after the matching *FontFor in the same frame; primary identifies the set.
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

// setPairCount is every (logical, device) pair: chat, log, chrome, plus one per theme-dressable element.
const setPairCount = 3 + int(themeFontElemCount)

// setPairs returns every pair in one place, so setOf, setIndexOf, Destroy and SetChromeFont can't miss a set.
// A fixed array, walked only on raster-build paths and font swaps.
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

// setOf finds the set a picked face belongs to, by pointer; font instances are unique per size, so no pct is needed.
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

// coverRunes returns each rune's face: the first in primary's set whose cmap has it, else primary. Once per raster
// build.
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
		// About to draw as .notdef: queue it so the search can find an installed font and this text re-rasters a frame
		// or two later. Zero-width runes are skipped; no font is expected to cover them and they would waste the queue.
		if !found && !isInvisibleRune(r) {
			c.noteUncovered(r)
		}
	}
	return out
}

// uncoveredPictograph reports a BMP pictograph (♥ ✨ ⭐ ⌛) no face in primary's chain covers. Without VS16,
// render.NeedsEmojiFallback routes nothing to the colour face, so it would draw as a box. Called only after the pick
// failed.
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

// uncoveredPictographMask is true per rune where the colour face must take over; nil when none, allocating nothing.
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

// setCovers reports whether any face in s has r, so the pictograph check and face resolution agree on "covered".
func (c *Ctx) setCovers(s *fontSet, r rune) bool {
	return anyCoversRune(s.cover, &c.sfntBuf, r)
}

// isInvisibleRune reports formatting characters meant to render as nothing (joiners, variation selectors, directional
// marks, the BOM). They rarely appear in a cmap, so a miss is not a missing font.
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

// setIndexOf resolves a logical face to its set, device sibling and index; (nil, nil, -1) when not in a scaled set.
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

// deviceFontFor returns the device-scaled sibling of a logical face picked at pct, or primary when the set can't be
// resolved. At textDevPct==100 it is the same face. Render thread.
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

// deviceCoverRunes is coverRunes over the device set; once per raster build.
func (c *Ctx) deviceCoverRunes(logicalPrimary *ttf.Font, pct int, runes []rune) []*ttf.Font {
	out := make([]*ttf.Font, len(runes))
	log, dev, _ := c.setIndexOf(logicalPrimary)
	if log == nil || c.textDevPct == DefaultScalePct || c.textDevPct == 0 {
		// 1:1 or unresolved: device faces are the logical faces.
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
		// Queued here too: this branch runs at every scale except 1:1, and auto-scale is on by default, so the IC
		// chatbox would otherwise never ask for a missing font.
		if !found && !isInvisibleRune(r) {
			c.noteUncovered(r)
		}
	}
	return out
}

// allSameFont reports whether every entry is f, so the line is single-face after all.
func allSameFont(fonts []*ttf.Font, f *ttf.Font) bool {
	for _, x := range fonts {
		if x != f {
			return false
		}
	}
	return true
}

// SetFontChain installs override bytes (chain order; nil clears). They must stay referenced (RWFromMem). Render thread.
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

// FontChainNames reports the loaded override names.
func (c *Ctx) FontChainNames() []string { return c.fontNames }

// SetChromeFont swaps the chrome fonts to a face built from data (nil = embedded); the bytes must stay referenced
// (RWFromMem). Idempotent on the same slice. Returns false, leaving chrome untouched, if they don't open. Render
// thread.
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
	// Build the replacements first: a bad file must never tear down the UI font.
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
	// Drop the sets before swapping: they may share c.font (the pct==100 path in fontsFor), and the close guards
	// compare against the current c.font, so swapping first would close the shared face twice. Device sets share
	// c.fontDev likewise.
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
	// The device chrome faces share or derive from the old pair; rebuildDeviceChrome rebuilds them below.
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
	// At 100% this just re-shares the new logical faces.
	c.rebuildDeviceChrome()
	// Labels, widths and line picks key on the old face pointers; a chrome swap is a settings action.
	c.purgeTextCache()
	clear(c.widthCache)
	clear(c.devWidthCache) // device-face field memo carries the OLD chrome metrics
	c.pickMemo = nil
	c.coverHintFont = nil
	return true
}

// fontsFor returns the logical set's fonts, rebuilding when the scale or chain moved.
func (c *Ctx) fontsFor(s *fontSet, pct int) []*ttf.Font {
	return c.buildSet(s, c.fontChain, c.chainCover, pct, DefaultScalePct, c.font, false)
}

// themeFontsFor builds el's own logical set from the theme's face (face indexes themeFaceData), else the user's chain;
// still a dedicated set, so an element the theme only sized pins its point size.
func (c *Ctx) themeFontsFor(el themeFontElem, face, pct int) []*ttf.Font {
	chain, cover := c.fontChain, c.chainCover
	if face >= 0 && face < len(c.themeFaceData) && face < len(c.themeFaceCover) {
		chain, cover = c.themeFaceData[face:face+1], c.themeFaceCover[face:face+1]
	}
	return c.buildSet(&c.themeElemSets[el], chain, cover, pct, DefaultScalePct, c.font, false)
}

// fontsForDev returns the device sibling opened at pct×(textDevPct/100), keyed on s.devPct, rebuilt from the logical
// set's srcChain so a per-element sibling uses the theme face. The device chrome face is its embedded last resort.
func (c *Ctx) fontsForDev(logical, dev *fontSet, pct int) []*ttf.Font {
	return c.buildSet(dev, logical.srcChain, logical.srcCover, pct, int(c.textDevPct), c.fontDev, true)
}

// buildSet opens the chain at UIFontSize×pct×devPct/100², sharing chromeShare (c.font, or c.fontDev for a device set)
// as last resort at 1:1; close guards key off it too. A device rebuild must not purge the label and pick caches, which
// key on logical faces: mid-frame that could clear a pick just made and mis-route a mixed-script message for one frame.
func (c *Ctx) buildSet(s *fontSet, chain [][]byte, chainCover []*sfnt.Font, pct, devPct int, chromeShare *ttf.Font, isDevice bool) []*ttf.Font {
	if devPct <= 0 {
		devPct = DefaultScalePct
	}
	if len(s.fonts) > 0 && s.pct == pct && s.devPct == devPct && s.gen == c.fontChainGen {
		return s.fonts
	}
	if len(s.fonts) > 1 && s.fonts[0] != nil && s.fonts[0] != chromeShare {
		s.fonts[0].ClearFallbackFonts() // detach before the faces it points at close
	}
	for _, f := range s.fonts {
		if f != chromeShare {
			f.Close()
		}
	}
	if !isDevice {
		// Keys carry the old font pointers, and rebuilds are user actions. A device rebuild skips this.
		c.purgeTextCache()
		c.pickMemo = nil
		c.coverHintFont = nil
	}
	s.fonts = s.fonts[:0]
	s.cover = s.cover[:0]
	s.emoji = nil
	s.pct, s.devPct, s.gen = pct, devPct, c.fontChainGen
	s.srcChain, s.srcCover = chain, chainCover
	// pct and the device scale both multiply UIFontSize; devPct==100 reproduces the logical size exactly.
	size := UIFontSize * pct * devPct / (DefaultScalePct * DefaultScalePct)
	if size < 1 {
		size = 1
	}
	// The rendering font (per size) and the sfnt cover (size-independent) append in lockstep, so a skipped face can't
	// desync them.
	for i, data := range chain {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, coverAt(chainCover, i))
		}
	}
	// Embedded last resort, sharing the chrome face only at 1:1 (pct and devPct both 100) and only while chrome is the
	// embedded face: a custom font's coverage isn't goregular's. Not with fallback faces either, because the first face
	// carries them, and a shared chrome face would keep pointers to faces another set's rebuild closes.
	hasFallbacks := len(chain) > 0 || len(c.fallbackData) > 0 || len(c.cjkData) > 0 || len(c.censusData) > 0 || len(c.emojiData) > 0
	if chromeShare == nil || (!hasFallbacks && pct == DefaultScalePct && devPct == DefaultScalePct && c.chromeData == nil) {
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
	// Fallbacks after the embedded font, so Latin keeps its look. Nil for pure-ASCII sessions; a bad face is skipped.
	for i, data := range c.fallbackData {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, coverAt(c.fallbackCover, i))
		}
	}
	// CJK tier: reached only for runes nothing above covers.
	for i, data := range c.cjkData {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, coverAt(c.cjkCover, i))
		}
	}
	// System-search faces after that, so they never win a rune a curated face could have drawn better (fontcensus.go).
	for i, data := range c.censusData {
		if f, err := memFont(data, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, coverAt(c.censusCover, i))
		}
	}
	// Colour-emoji last: a text face that has a symbol draws it in the text's style rather than as a picture.
	if len(c.emojiData) > 0 {
		if f, err := memFont(c.emojiData, size); err == nil {
			s.fonts = append(s.fonts, f)
			s.cover = append(s.cover, c.emojiCover)
			s.emoji = f
		}
	}
	// Per-glyph fallback, the way Qt resolves fonts. Measuring goes through this chain; drawing mixed text does not, as
	// SDL_ttf 3.2.2 misplaces fallback glyphs vertically: labels compose runs (renderLabel), messages use the per-run
	// raster.
	if primary := s.fonts[0]; primary != nil && primary != chromeShare {
		for _, f := range s.fonts[1:] {
			if f != nil && f != chromeShare {
				_ = primary.AddFallbackFont(f) // a refused face leaves its glyphs as boxes, as before
			}
		}
	}
	return s.fonts
}

// noteUncovered records a rune no face could draw, for the system-font search. Raster build path only; allocation-free,
// the queue is a fixed array that stops accepting once full.
func (c *Ctx) noteUncovered(r rune) {
	if render.BMPPictograph(r) {
		c.requestEmoji() // no text face has this pictograph; the colour face may
	}
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
	PushWake() // the App hands queued runes to the census next frame, idle screen or not
}

// TakeMissingRunes copies out and clears the runes nothing could draw; the result aliases dst. Render thread.
func (c *Ctx) TakeMissingRunes(dst []rune) []rune {
	dst = dst[:0]
	for i := 0; i < c.missingN; i++ {
		dst = append(dst, c.missingRunes[i])
	}
	c.missingN = 0
	return dst
}

// AddCensusFace installs a face found for an undrawable rune. As SetFallbackFonts: the bytes must outlive the faces
// (RWFromMem), every set rebuilds, and caches purge. A no-op past censusFaceCap. Render thread.
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

// SetEmojiFont installs the colour-emoji bytes (nil clears); they must stay referenced (RWFromMem). Render thread.
func (c *Ctx) SetEmojiFont(data []byte) {
	for _, f := range c.emojiFonts {
		if f != nil {
			f.Close()
		}
	}
	c.emojiFonts = nil
	c.emojiData = data
	c.emojiCover = nil
	if len(data) > 0 {
		c.emojiCover = parseCover(data)
	}
	c.fontChainGen++    // every set rebuilds with the face last; labels drawn as boxes redraw
	c.purgeEmojiCache() // cached rasters used the OLD emoji face — rebuild on next draw
}

// SetFallbackFonts installs the broad-Unicode text faces (nil clears); they must stay referenced (RWFromMem). Bumps
// fontChainGen so every set rebuilds with them after the embedded font. Render thread.
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
	// Emoji rasters bake per-rune picks from the old chain, including cached "nothing to add" answers: purge, or a
	// label stays tofu until restart.
	c.purgeEmojiCache()
}

// SetCJKFonts installs the CJK tier; mirrors SetFallbackFonts, appended after the broad set. Render thread.
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

// EmojiFont returns the colour-emoji face at pct, built on first use and cached per size, nil when no face is loaded.
// Sized to match the text font so baselines line up. A failed build caches nil, so it isn't retried every message.
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

// emojiDeviceFont is EmojiFont at the device scale, sharing the per-size emojiFonts map. Render thread.
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

// pickFont returns the set's first face, which draws every line, with the rest attached as SDL_ttf fallbacks, as Qt
// does. The second result is whether that face alone has every rune, invisible runes included. When false, the caller
// takes the per-run raster, because SDL_ttf 3.2.2 places fallback glyphs by their own font's ascent (renderLabel).
func pickFont(fonts []*ttf.Font, cover []*sfnt.Font, buf *sfnt.Buffer, text string) (*ttf.Font, bool) {
	if len(fonts) == 1 {
		return fonts[0], true
	}
	return fonts[0], faceCovers(coverAt(cover, 0), buf, text)
}

// faceCovers reports whether f has a glyph for every rune of text; allocation-free with the reused buffer.
func faceCovers(f *sfnt.Font, buf *sfnt.Buffer, text string) bool {
	for _, r := range text {
		if !coverHasRune(f, buf, r) {
			return false
		}
	}
	return true
}

func anyCoversRune(cover []*sfnt.Font, buf *sfnt.Buffer, r rune) bool {
	for _, f := range cover {
		if coverHasRune(f, buf, r) {
			return true
		}
	}
	return false
}

// coverHasRune reports whether f has a glyph (cmap index != 0) for r; a nil face covers nothing.
func coverHasRune(f *sfnt.Font, buf *sfnt.Buffer, r rune) bool {
	if f == nil {
		return false
	}
	idx, err := f.GlyphIndex(buf, r)
	return err == nil && idx != 0
}

// coverAt returns the cover face at i or nil, keeping fonts/cover aligned if a face failed to build or parse.
func coverAt(s []*sfnt.Font, i int) *sfnt.Font {
	if i < len(s) {
		return s[i]
	}
	return nil
}

// BeginFrame clears the input snapshot, so it must run before this frame's events reach HandleEvent. The GetMouseState
// seed predates the event pump; mouse events override it.
func (c *Ctx) BeginFrame(dt time.Duration) {
	// Brackets restore this by defer; zeroed anyway so a panic can't leave the client in one panel's font.
	c.panelFont = nil
	// Tab cycling runs on the previous frame's field order.
	if c.tabPressed && len(c.fieldSeq) > 0 {
		// The chat fields cycle in AO2's chain (courtroom.cpp:187-210 construction order), not the draw order.
		orderTabChain(c.fieldSeq)
		c.focusID = cycleField(c.fieldSeq, c.focusID, c.tabShift)
		c.caretOn = true // land visible: the caret shows where focus went
		c.caretAcc = 0
	}
	// fieldSeq is not cleared here: BeginFrame also runs on frames the loop skips, which would leave Tab cycling an
	// empty list. It rolls over in BeginDraw.
	if c.focusNext != "" {
		c.focusID = c.focusNext
		c.focusNext = ""
		c.caretOn = true
		c.caretAcc = 0
	}
	// A dropdown closed last frame: blocking held through that frame, so the closing click reached nothing, and lifts
	// now.
	if c.modalRelease {
		c.modalOn = false
		c.modalRelease = false
	}
	c.ddDraws = c.ddDraws[:0]
	// Zeroed here rather than trusting each publisher, so a pass that returns early can't freeze the UI the way a
	// stranded modalOn would. overlayOwner resets too, so a panic can't leave overlay blocking disabled.
	c.overlayFenceN, c.overlayFenceDrops = 0, 0
	c.overlayOwner, c.overlayOwnerMark = false, 0

	// prevMouseDown is last frame's state, for the press edge that starts a drag-selection.
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
	// Per-frame too: a stale anchor would push a tooltip away from a widget no longer there.
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
	// Hold-to-clear timer, using holdKey/holdOn stamped last frame. !ctrlHeld: Ctrl+key is a chord, and with Backspace
	// bound holding Ctrl+Backspace would wipe the field instead of deleting words.
	if c.holdOn && c.keyHeld(c.holdKey) && !c.ctrlHeld {
		c.holdAcc += dt
	} else {
		c.holdAcc = 0
		c.holdFired = false
	}
}

// BeginDraw opens a real draw pass, separate from BeginFrame so skipped passes keep the last drawn order for Tab.
// fieldSeq is swapped into drawnFieldSeq, so pre-screen readers of "did this field draw?" have an answer without
// allocating.
func (c *Ctx) BeginDraw() {
	c.drawnFieldSeq, c.fieldSeq = c.fieldSeq, c.drawnFieldSeq[:0]
}

// NextCaretFlip reports the time until the focused caret toggles (ok=false with no focus), so the loop can wake then.
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

// CaretVisible reports the blink state; the loop's skip check compares it with the state last drawn.
func (c *Ctx) CaretVisible() (on, focused bool) {
	return c.caretOn, c.focusID != ""
}

// NextHoverDue reports the nearest pending hover deadline, as a resting pointer generates no events.
// Deadlines already reached report ok=false: the reveal drew on the frame the dwell crossed.
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
				// Same-spot clicks in the window cycle 1→2→3→1: fields use double for word-select, triple for all.
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
			// SDL filters control chords out of TEXTINPUT, so no double insert. HandleEvent runs on the render thread.
			switch e.Keysym.Sym {
			case sdl.K_v:
				// Only into a focused field; otherwise fall through, so a hotkey bound to "v" still works.
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
				// Only in a focused field; otherwise fall through, so a hotkey bound to "x" still works.
				if c.focusID != "" {
					c.cutReq = true
				} else {
					c.hotkey = e.Keysym.Sym
				}
			case sdl.K_a:
				// The focused field converts this into a real whole-value selection; select-all outranks the Ctrl+A
				// hotkey.
				c.selectAll = true
			case sdl.K_BACKSPACE:
				// Focused only, so an unfocused Ctrl+Backspace reaches the hotkeys. The WordDelete pref is checked in
				// textField, so with it off this is a consumed no-op that never leaks to a hotkey mid-typing.
				if c.focusID != "" {
					c.wordBack = true
				} else {
					c.hotkey = e.Keysym.Sym
				}
			case sdl.K_DELETE:
				// Ctrl+Delete: the same focused-only capture and consumed no-op as Ctrl+Backspace.
				if c.focusID != "" {
					c.wordFwdDel = true
				} else {
					c.hotkey = e.Keysym.Sym
				}
			case sdl.K_LEFT, sdl.K_RIGHT:
				// Word caret jump; Shift makes it a selection through textField's extend path. Navigation, so not under
				// WordDelete.
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
				// Everything else goes to the configurable Ctrl-chord hotkeys.
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
			// Plain keys feed the character keybinds; consumers check focus before acting.
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

// SetUIScale stores the render scale for mouse unprojection and drives the text device scale, so glyphs rasterize at
// final size rather than being stretched by SetScale.
func (c *Ctx) SetUIScale(pct int) {
	if pct <= 0 {
		pct = DefaultScalePct
	}
	c.uiPct = int32(pct)
	c.SetTextDevScale(int(float32(pct)*c.devScale + 0.5)) // device scale (crisp); mouse stays logical
}

// SetDeviceScaleFactor records drawable px per window point. It folds into text raster only, never mouse unprojection:
// SDL reports the mouse in points on macOS, so the pointer stays on the logical scale.
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

// RenderScalePct reports the scale Ren is drawing at. The device-exact blit needs this, not textDevPct: the export
// bracket moves textDevPct alone, the pinned-tab pass the renderer alone. It also avoids Ren.GetScale(), which
// allocates through cgo.
func (c *Ctx) RenderScalePct() int32 {
	if c.renderPctOverride > 0 {
		return c.renderPctOverride
	}
	return c.uiPct
}

// beginRenderScaleOverride / endRenderScaleOverride bracket an offscreen pass that sets its own renderer scale.
func (c *Ctx) beginRenderScaleOverride(pct int32) { c.renderPctOverride = pct }

func (c *Ctx) endRenderScaleOverride() { c.renderPctOverride = 0 }

// SetTextDevScale sets the device font scale and rebuilds device chrome. A no-op when unchanged, so it is safe per
// frame. Render thread.
func (c *Ctx) SetTextDevScale(pct int) {
	if pct <= 0 {
		pct = DefaultScalePct
	}
	if int32(pct) == c.textDevPct {
		return
	}
	c.textDevPct = int32(pct)
	c.rebuildDeviceChrome()
	// Label and emoji caches carry device-face pointers that just changed; a scale change is a user action.
	c.purgeTextCache()
	clear(c.widthCache)
	clear(c.devWidthCache) // device-face field memo carries the OLD device point size
}

// rebuildDeviceChrome reopens the device chrome pair at UIFontSize×(textDevPct/100); at 100%, or if a face can't open,
// it shares the logical faces. Render thread, on a scale change only.
func (c *Ctx) rebuildDeviceChrome() {
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
	// From the same source as the logical faces: the custom bytes if set, else the embedded face.
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

// toLogical converts window pixels to logical coordinates by dividing by the global scale.
func (c *Ctx) toLogical(v int32) int32 {
	if c.uiPct == 0 || c.uiPct == DefaultScalePct {
		return v
	}
	return v * DefaultScalePct / c.uiPct
}

// hovering reports whether the mouse is inside r.
func (c *Ctx) hovering(r sdl.Rect) bool {
	// While a modal is up every other widget reads as not hovered, even one directly under an open list, so a click
	// can't fall through. The modal owner uses raw pointIn().
	if c.modalOn {
		return false
	}
	// A clipped widget only exists inside its clip; without this a half-culled list row hit-tests below its panel.
	if c.clipOn && !(c.mouseX >= c.clipRect.X && c.mouseX < c.clipRect.X+c.clipRect.W &&
		c.mouseY >= c.clipRect.Y && c.mouseY < c.clipRect.Y+c.clipRect.H) {
		return false
	}
	// A later-drawn overlay owns its pixels, and so the input there. This is the only place the registry is consulted,
	// so raw pointIn() sites still see through unless they call c.overlayFenced. After modalOn and clipOn, which
	// outrank it. The occluder hit-tests from inside pushOverlayOwner.
	if c.overlayFenced(c.mouseX, c.mouseY) {
		return false
	}
	return c.mouseX >= r.X && c.mouseX < r.X+r.W && c.mouseY >= r.Y && c.mouseY < r.Y+r.H
}

// ClickedIn reports a left-click pressed and released inside r. `c.clicked && hovering(r)` fires on the release alone,
// so a drag ending over a navigational row would send an area transfer.
func (c *Ctx) ClickedIn(r sdl.Rect) bool {
	return c.clicked && c.hovering(r) &&
		c.downX >= r.X && c.downX < r.X+r.W && c.downY >= r.Y && c.downY < r.Y+r.H
}

// fencePointer stashes and blanks the pointer so a pass draws as if the mouse were absent: the kit has no z-aware
// input, so a floating panel hides it from the courtroom pass. Keyboard is untouched. Idempotent, no closure,
// allocation-free.
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

// unfencePointer restores the stashed state; a no-op when not blanked, so safe to defer.
func (c *Ctx) unfencePointer() {
	if !c.ptrFenced {
		return
	}
	c.ptrFenced = false
	c.mouseX, c.mouseY = c.fMouseX, c.fMouseY
	c.clicked, c.rightClicked, c.mouseDown, c.wheelY = c.fClicked, c.fRight, c.fDown, c.fWheel
}

// Fill draws a solid rect.
func (c *Ctx) Fill(r sdl.Rect, col sdl.Color) {
	_ = c.Ren.SetDrawColor(col.R, col.G, col.B, col.A)
	c.cgoRect = r // &local would escape through cgo and heap-allocate per call
	_ = c.Ren.FillRect(&c.cgoRect)
}

// gradientSteps bounds the strips a vertical gradient draws.
const gradientSteps = int32(48)

// FillGradient paints r as a vertical gradient from top to bottom.
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

// clipNowhere is a far off-screen 1x1 rect standing in for an empty clip: SDL treats a w<=0 || h<=0 rect as "disable
// clipping" (SDL_render.c), the opposite of what the caller wants.
var clipNowhere = sdl.Rect{X: -1 << 14, Y: -1 << 14, W: 1, H: 1}

// clipIntersect is pushClip's arithmetic: an inner clip can only narrow; a disjoint result becomes clipNowhere.
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

// pushClip scissors to r intersected with the clip in force and returns the state for popClip. Intersecting matters:
// an inner push reaching outside the panel (labelEmoji's raster band on a part-scrolled row) would otherwise let
// that row paint over the border. By value, no closure, so per-frame list draws stay allocation-free.
func (c *Ctx) pushClip(r sdl.Rect) (prev sdl.Rect, had bool) {
	// From the mirror fields: GetClipRect's named-return pointer allocates through cgo. The mirror is authoritative
	// because all nested clipping flows through pushClip/popClip; raw Ren.SetClipRect sites are leaf draw-only regions.
	prev, had = c.clipRect, c.clipOn
	r = clipIntersect(r, prev, had)
	c.cgoRect = r // &local would escape through cgo and heap-allocate per call
	_ = c.Ren.SetClipRect(&c.cgoRect)
	// Mirrored so hovering() can honour it without a cgo query per hit test.
	c.clipOn, c.clipRect = true, r
	return prev, had
}

// pushMuted greys out and disarms every Checkbox until popMuted; returns the previous value so the brackets nest.
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

// textTexture returns and caches a label; labels pack into atlas pages, oversized ones get a dedicated texture.
func (c *Ctx) textTexture(text string, col sdl.Color, font *ttf.Font) (cachedText, bool) {
	if text == "" || font == nil {
		return cachedText{}, false
	}
	return c.textTextureBold(text, col, font, false)
}

// textTextureBold rasterizes through STYLE_BOLD rather than drawing twice a pixel apart: at a fractional scale the two
// passes land on separate sub-pixel phases and smear. The style is set on the device face for the one render and
// restored by defer, so the shared cached face never leaks a weight.
func (c *Ctx) textTextureBold(text string, col sdl.Color, font *ttf.Font, bold bool) (cachedText, bool) {
	if text == "" || font == nil {
		return cachedText{}, false
	}
	// Cached by the logical font, rasterized with its device sibling; cachedText.devPct records the scale.
	// A textDevPct change purges the whole cache.
	key := textKey{text: text, color: col, font: font, bold: bold}
	if t, ok := c.textCache[key]; ok {
		return t, true
	}
	dev := c.deviceTextFont(font) // chrome/set logical face → device-scaled face
	surf, err := c.renderLabel(font, dev, text, col, bold)
	if err != nil {
		return cachedText{}, false
	}
	defer surf.Free()
	if len(c.textCache) >= textCacheMax {
		c.purgeTextCache()
	}
	inkTop, inkBottom, _ := inkRows(surf)

	if tex, slot, ok := c.atlasPlace(surf); ok {
		entry := cachedText{tex: tex, src: slot, w: surf.W, h: surf.H, devPct: c.textDevPct, inkTop: inkTop, inkBottom: inkBottom}
		c.textCache[key] = entry
		return entry, true
	}

	// Atlas full or label oversized: dedicated texture.
	tex, err := c.Ren.CreateTextureFromSurface(surf)
	if err != nil {
		return cachedText{}, false
	}
	entry := cachedText{tex: tex, src: sdl.Rect{W: surf.W, H: surf.H}, w: surf.W, h: surf.H, owned: true, devPct: c.textDevPct, inkTop: inkTop, inkBottom: inkBottom}
	c.textCache[key] = entry
	return entry, true
}

// renderLabel rasterizes text in dev, the device face of font: one render if dev has every glyph, else run by run on
// dev's baseline. SDL_ttf 3.2.2 places a fallback glyph by its own font's ascent, not the line's (fixed on main,
// unreleased). The label grows above dev's top only when a run's ink reaches past it.
func (c *Ctx) renderLabel(font, dev *ttf.Font, text string, col sdl.Color, bold bool) (*sdl.Surface, error) {
	runs := c.labelRuns(font, text, true)
	if runs == nil {
		return renderStyled(dev, text, col, bold)
	}
	surfs := make([]*sdl.Surface, 0, len(runs))
	defer func() {
		for _, s := range surfs {
			s.Free()
		}
	}()
	ascent := int32(dev.Ascent())
	var above, width int32 // rows a run's ink needs above dev's top; total width
	for _, r := range runs {
		s, err := renderStyled(r.face, r.text, col, bold)
		if err != nil {
			return nil, err
		}
		surfs = append(surfs, s)
		if inkTop, _, inked := inkRows(s); inked { // a run with no ink (a space) needs no room
			above = max(above, int32(r.face.Ascent())-ascent-inkTop)
		}
		width += s.W
	}
	height := above + int32(dev.Height())
	for i, r := range runs {
		height = max(height, above+ascent-int32(r.face.Ascent())+surfs[i].H)
	}
	out, err := sdl.CreateRGBSurfaceWithFormat(0, width, height, 0, uint32(sdl.PIXELFORMAT_ARGB8888))
	if err != nil {
		return nil, err
	}
	x := int32(0)
	for i, r := range runs {
		if err := surfs[i].CopyTo(out, x, above+ascent-int32(r.face.Ascent())); err != nil {
			out.Free()
			return nil, err
		}
		x += surfs[i].W
	}
	return out, nil
}

// measureLabel is the width renderLabel draws at: one measure or the sum of runs. Measuring through the fallback chain
// rounds advances differently at run boundaries and would be off by a pixel.
func (c *Ctx) measureLabel(f *ttf.Font, text string, bold bool) (int32, error) {
	runs := c.labelRuns(f, text, false)
	if runs == nil {
		return sizeStyled(f, text, bold)
	}
	var width int32
	for _, r := range runs {
		w, err := sizeStyled(r.face, r.text, bold)
		if err != nil {
			return 0, err
		}
		width += w
	}
	return width, nil
}

// textRun is a maximal piece of a label drawn in one face.
type textRun struct {
	face *ttf.Font
	text string
}

// labelRuns splits text into maximal one-face runs, keeping whole emoji sequences in the colour face (joiners,
// variation selectors, skin tones), or a family or flag would split apart. device picks device faces for drawing; nil
// when font draws everything itself.
func (c *Ctx) labelRuns(font *ttf.Font, text string, device bool) []textRun {
	s := c.setOf(font)
	if !hasNonASCII(text) || s == nil || len(s.fonts) <= 1 {
		return nil // no fallback faces to take glyphs from
	}
	runes := []rune(text)
	faces, self, emoji := c.coverRunes(font, runes), font, s.emoji
	if device {
		faces, self, emoji = c.deviceCoverRunes(font, DefaultScalePct, runes), c.deviceTextFont(font), c.deviceEmojiFace(font)
	}
	if emoji != nil && render.NeedsEmojiFallback(text) {
		for i, isEmoji := range render.AssignEmoji(runes) {
			if isEmoji {
				faces[i] = emoji
			}
		}
	}
	if allSameFont(faces, self) {
		return nil
	}
	var runs []textRun
	for i := 0; i < len(runes); {
		j := i + 1
		for j < len(runes) && faces[j] == faces[i] {
			j++
		}
		runs = append(runs, textRun{face: faces[i], text: string(runes[i:j])})
		i = j
	}
	return runs
}

// deviceEmojiFace is the colour-emoji face of font's device set, or nil.
func (c *Ctx) deviceEmojiFace(font *ttf.Font) *ttf.Font {
	log, dev, _ := c.setIndexOf(font)
	if log == nil {
		return nil
	}
	if c.textDevPct == DefaultScalePct || c.textDevPct == 0 {
		return log.emoji // 1:1: the device set is the logical one
	}
	c.fontsForDev(log, dev, log.pct)
	return dev.emoji
}

// renderStyled renders at a weight, restoring f's style so the shared face never leaks it.
func renderStyled(f *ttf.Font, text string, col sdl.Color, bold bool) (*sdl.Surface, error) {
	if bold {
		f.SetStyle(ttf.STYLE_BOLD)
		defer f.SetStyle(ttf.STYLE_NORMAL)
	}
	return f.RenderUTF8Blended(text, col)
}

// sizeStyled is renderStyled's measuring counterpart.
func sizeStyled(f *ttf.Font, text string, bold bool) (int32, error) {
	if bold {
		f.SetStyle(ttf.STYLE_BOLD)
		defer f.SetStyle(ttf.STYLE_NORMAL)
	}
	w, _, err := f.SizeUTF8(text)
	return int32(w), err
}

// deviceTextFont maps a logical face to its device sibling; at textDevPct==100 every mapping is the identity. Render
// thread.
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
	// A picked set face; returned unchanged when the set can't be resolved.
	return c.deviceFontForAnyPct(font)
}

// deviceFontForAnyPct resolves a set face's device sibling; the face's set knows its pct.
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

// atlasPlace uploads a label surface into a shared page, up to the cap; ok=false means use a dedicated texture.
func (c *Ctx) atlasPlace(surf *sdl.Surface) (*sdl.Texture, sdl.Rect, bool) {
	// Texture.Update needs the page's pixel format (TTF blended output is ARGB8888, usually a no-op).
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
		// Newest page first; older ones are mostly full.
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
	// Keyed by font pointer, so cleared here, where every face-invalidating path passes. SetThemeFaces Closes
	// per-element faces but does not clear widthCache, so clearing elsewhere could leave dangling pointers.
	clear(c.panelWidth)
	// Same lifecycle as panelWidth; a stale entry would serve the previous scale's metrics.
	clear(c.logDevWidth)
}

// logicalW is the device texture width divided by its raster scale.
func (t cachedText) logicalW() int32 { return uiLogicalFromDevice(t.w, t.devPct) }

// uiLogicalFromDevice must use the same round-half-up rule as render.logicalFromDevice, so a kit label and a message
// raster of one string agree to the pixel.
func uiLogicalFromDevice(device, devPct int32) int32 {
	if devPct <= 0 || devPct == DefaultScalePct {
		return device
	}
	return (device*DefaultScalePct + devPct/2) / devPct
}

// uiDeviceFromLogical is the exact inverse of uiLogicalFromDevice, used by the focused field to draw its moving parts
// under SetScale(1,1), so a fractional scale can't phase-shift or stretch them. The identity at 100%.
func uiDeviceFromLogical(logical, devPct int32) int32 {
	if devPct <= 0 || devPct == DefaultScalePct {
		return logical
	}
	return (logical*devPct + DefaultScalePct/2) / DefaultScalePct
}

// deviceExactText reports whether the focused field draws on the device-exact path: only at a fractional scale. The gif
// export moves textDevPct away from the UI scale, but it never draws a live textField, so they can't diverge here.
func deviceExactText(devPct int32) bool {
	return devPct != DefaultScalePct && devPct != 0
}

// uiDeviceExactAt mirrors render.deviceExactAt: it compares the renderer's current scale with the texture's own build
// scale, since a label blitted during another scale (export, pinned tab) must not take the 1:1 path, or its size is
// wrong.
func uiDeviceExactAt(renderPct, devPct int32) bool {
	return renderPct > 0 && renderPct != DefaultScalePct && renderPct == devPct
}

// blitLabel copies a cached label through the scratch rects; wLog is the logical destination width. On the scaled path,
// source and destination round independently and can differ, which bilinear filtering (HINT_RENDER_SCALE_QUALITY=1)
// turns into edge blur; when uiDeviceExactAt holds, blitLabelExact avoids any resample.
func (c *Ctx) blitLabel(t cachedText, x, y, wLog int32) {
	if uiDeviceExactAt(c.RenderScalePct(), t.devPct) {
		c.blitLabelExact(t, x, y, wLog)
		return
	}
	c.blitLabelScaled(t, x, y, wLog)
}

// blitLabelScaled keeps src in device px and dst in logical px, for SetScale to map back; exact only when that
// round-trips. A separate function so a test can drive it for pixel comparison.
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

// blitLabelExact draws at SetScale(1,1) with src.W == dst.W, so SDL never resamples. Only when uiDeviceExactAt holds.
// Metal evaluates a clip against the scale in force when used, not when set, so a logical clip would shrink to 1/scale;
// it is re-projected to device px and re-asserted inside the bracket, in the order text.go and badge.go use.
// The scale is restored by recomputing from c.RenderScalePct(), as Ren.GetScale() allocates per call through cgo.
func (c *Ctx) blitLabelExact(t cachedText, x, y, wLog int32) {
	dev := t.devPct

	// One device width feeds src and dst, clamped to the texture's own.
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

// Label draws text at (x, y) and returns its logical width. Under a row-label limit it ellipsises at the container's
// right edge instead of a hard cut at the clip; with none, fitRowLabel is the identity.
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

// IconLabel centres a symbol in r by its visible ink rather than the line box: fonts place symbols variously against
// the baseline, and SDL_ttf places fallback glyphs by their own ascent, so a symbol drawn like text reads low.
func (c *Ctx) IconLabel(r sdl.Rect, glyph string, col sdl.Color) {
	t, ok := c.textTexture(glyph, col, c.chromeFaceFor(glyph))
	if !ok {
		return
	}
	w := t.logicalW()
	inkTop := uiLogicalFromDevice(t.inkTop, t.devPct)
	inkH := uiLogicalFromDevice(t.inkBottom+1, t.devPct) - inkTop
	c.blitLabel(t, r.X+(r.W-w)/2, r.Y+(r.H-inkH)/2-inkTop, w)
}

// inkRows returns the first and last rows of surf with a visible pixel. Other formats than ARGB8888 report full height
// as inked; a surface with no visible pixel reports full height and inked false.
func inkRows(surf *sdl.Surface) (top, bottom int32, inked bool) {
	const (
		bytesPerPixel = 4
		alphaByte     = 3 // ARGB8888 is B, G, R, A in memory on little-endian hosts
	)
	top, bottom = 0, surf.H-1
	if surf.Format.Format != uint32(sdl.PIXELFORMAT_ARGB8888) {
		return top, bottom, true
	}
	px := surf.Pixels()
	rowInked := func(y int32) bool {
		row := px[int(y)*int(surf.Pitch):]
		for x := int32(0); x < surf.W; x++ {
			if row[int(x)*bytesPerPixel+alphaByte] != 0 {
				return true
			}
		}
		return false
	}
	for top <= bottom && !rowInked(top) {
		top++
	}
	for bottom >= top && !rowInked(bottom) {
		bottom--
	}
	if top > bottom {
		return 0, surf.H - 1, false
	}
	return top, bottom, true
}

// Heading draws large text.
func (c *Ctx) Heading(x, y int32, text string, col sdl.Color) {
	t, ok := c.textTexture(text, col, c.fontBig)
	if !ok {
		return
	}
	c.blitLabel(t, x, y, t.logicalW())
}

// LabelClipped draws text clipped to maxW; a row-label limit also narrows it to the container's edge and ellipsises.
func (c *Ctx) LabelClipped(x, y, maxW int32, text string, col sdl.Color) {
	if shown, fit := c.fitRowLabel(x, text); !fit {
		text = shown
		if avail := c.rowLabelRight - x; avail > 0 && avail < maxW {
			maxW = avail
		}
	}
	c.LabelClippedFont(c.chromeFaceFor(text), x, y, maxW, text, col)
}

// LabelClippedFont is LabelClipped with an explicit font; maxW is logical px.
func (c *Ctx) LabelClippedFont(font *ttf.Font, x, y, maxW int32, text string, col sdl.Color) {
	c.LabelClippedFontWeight(font, x, y, maxW, text, col, false)
}

// LabelClippedFontWeight adds a weight: bold draws the face's bold cut once (textTextureBold).
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
