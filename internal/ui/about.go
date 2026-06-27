package ui

import (
	"log"
	"os/exec"
	"runtime"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// The built-in About page: who made AsyncAO, why, and the original
// developers this client owes everything to.
const (
	aboutRepoURL   = "https://github.com/SyntaxNyah/AsyncAO"
	aboutAO2URL    = "https://github.com/AttorneyOnline/AO2-Client"
	aboutWebAOURL  = "https://github.com/AttorneyOnline/webAO"
	aboutAOSDLURL  = "https://github.com/AttorneyOnline/AO-SDL"
	aboutAOSiteURL = "https://aceattorneyonline.com"
	aboutArtistURL = "https://www.instagram.com/hlenbchan2" // app icon artist
	// aboutMascotPx is the on-screen (logical) size of the Mayo portrait, drawn in
	// the Mayo section; the texture is Catmull-Rom downscaled to this exact size.
	aboutMascotPx = int32(200)
	// Reading-column + card geometry. The prose is constrained to a comfortable
	// measure (not stretched edge-to-edge on wide windows) and centered.
	aboutMaxColW  = 760 // max reading-column width
	aboutParaGap  = 12  // vertical space between paragraphs / blocks
	aboutCardGap  = 12  // vertical space between credit cards
	aboutCardPad  = 16  // inner padding of a credit card
	aboutLinkGap  = 6   // vertical space between link buttons in a card
	aboutTitleGap = 6   // gap under a card title, before its first row
)

// aboutBlockKind classifies a prose block so the renderer can colour/indent it.
type aboutBlockKind int

const (
	abTitle  aboutBlockKind = iota // the app name + version (accent)
	abPara                         // a normal wrapped paragraph
	abBullet                       // a "• " item with a hanging indent
	abMayo                         // a paragraph with the Mayo portrait centered above it
	abAccent                       // an accent-coloured call to action
)

// aboutBlock is one unit of About prose. The text is stored UN-wrapped (full
// paragraphs); the renderer reflows it to the current column width. The wording
// is identical to the old hard-wrapped lines — only the line breaks are gone, so
// the page reads as paragraphs at any width.
type aboutBlock struct {
	kind aboutBlockKind
	text string
}

var aboutBlocks = []aboutBlock{
	{abTitle, "AsyncAO " + protocol.Version},
	{abPara, "Made by SyntaxNyah."},
	{abPara, "Why this exists: I was tired of people having to download 20 gigabytes of files just to play, of client lookups taking ages, and — let's be honest — the AO2 client being a bit slow. AsyncAO streams exactly the assets it needs, learns what formats your server ships, caches everything, and renders on a zero-allocation hot path. Every millisecond counts."},
	{abPara, "Optional Discord Rich Presence shows what you're playing - on by default, toggle it in Settings -> Discord, and there's no Discord dependency. Don't want it at all? A Discord-free build (no Discord code) is on GitHub Actions."},
	{abPara, "All credit to the original Attorney Online developers:"},
	{abBullet, "FanatSors — creator of the original Attorney Online"},
	{abBullet, "OmniTroid — original AO2-Client developer, and a huge help on the protocol documentation"},
	{abBullet, "The AttorneyOnline organization and every AO2-Client contributor — AsyncAO mirrors their protocol and courtroom semantics"},
	{abBullet, "The webAO developers — the asset-URL conventions come from their work"},
	{abBullet, "The AO-SDL developers — the SDL2 rendering model reference"},
	{abPara, "Thank you for two decades of courtroom drama. None of this would exist without the things you built and the inspiration they provided."},
	{abPara, "Closed-source beta testers — thank you for the bug reports, feature requests and feedback that shaped AsyncAO during development:"},
	{abPara, "Cocoa Bean · Lala · Peen · Emerald · Extra7 · Poki · Xocfti · Dag · CherriPop · Nightingale"},
	{abAccent, "Special thanks to Nightingale — an enormous share of the v1.0.x quality-of-life polish came straight from their relentless, detailed playtesting. Go show them some love."},
	{abPara, "A special thank-you to Northgate — who backed this project, including financially, and gave me the inspiration to keep going. Without that support AsyncAO wouldn't have come this far this fast. Thank you."},
	{abMayo, "Mayo — the AsyncAO mascot and app icon. The client was almost named \"MayAO\" (Maya + AO), but became AsyncAO — we wanted more Maya representation, since the AO2 client only ever showed Phoenix and Edgeworth. So the mascot is Mayo: inspired by Maya Fey from Ace Attorney, with the Go gopher's blue palette (AsyncAO is written in Go)."},
	{abPara, "Art commissioned by Nyah and illustrated by hlenbchan — please go support their work! Instagram: @hlenbchan2. Thank you for bringing Mayo to life."},
	{abPara, "AsyncAO is FREE SOFTWARE — licensed under the GNU AGPL v3 (the LICENSE file), and free all the way down: EVERY dependency is open-source under an AGPL-v3-compatible licence (MIT / BSD / ISC / zlib / MPL-2.0 / LGPL, plus the GCC runtime's linking exception). No proprietary or closed-source pieces anywhere. Each one is linked and credited below — please support them too. Full details: docs/THIRD-PARTY-LICENSES.md in the repo."},
	{abPara, "Copyright (c) 2026 SyntaxNyah and the AsyncAO contributors. Because the whole stack is AGPL-v3-compatible free software, AsyncAO may be freely redistributed — including as binary GitHub releases — in full compliance with the AGPL and every dependency's licence (ship the third-party notices alongside binaries)."},
	{abAccent, "Pull requests, bug fixes and feature requests are welcome!"},
}

// aboutLink is one row in the About link list. A blank url marks a SECTION HEADER
// (a card title) — so the list renders as titled cards; the items under it are
// clickable buttons.
type aboutLink struct {
	label string
	url   string
}

var aboutLinks = []aboutLink{
	{"AsyncAO — source & pull requests (AGPL-3.0)", aboutRepoURL},

	{"Attorney Online — the project AsyncAO builds on", ""},
	{"AO2-Client — the original client (GPLv3)", aboutAO2URL},
	{"webAO — AO in the browser (asset URL conventions)", aboutWebAOURL},
	{"AO-SDL — an SDL2 AO client (thread-model reference)", aboutAOSDLURL},
	{"aceattorneyonline.com — the AO community", aboutAOSiteURL},

	{"Artwork", ""},
	{"hlenbchan — Mayo mascot & app icon artist (Instagram)", aboutArtistURL},

	// Every dependency, linked + credited. All AGPL-v3-compatible free software.
	{"Open-source dependencies — Go libraries (all free software)", ""},
	{"coder/websocket — WebSocket client (ISC)", "https://github.com/coder/websocket"},
	{"veandco/go-sdl2 — SDL2 / mixer / ttf bindings (BSD-3)", "https://github.com/veandco/go-sdl2"},
	{"golang.org/x/image — image scaling & codecs (BSD-3)", "https://pkg.go.dev/golang.org/x/image"},
	{"golang.org/x/sync — concurrency primitives (BSD-3)", "https://pkg.go.dev/golang.org/x/sync"},
	{"golang.org/x/text — text encoding (BSD-3)", "https://pkg.go.dev/golang.org/x/text"},
	{"cespare/xxhash — fast hashing for cache keys (MIT)", "https://github.com/cespare/xxhash"},
	{"hashicorp/golang-lru — LRU caches (MPL-2.0)", "https://github.com/hashicorp/golang-lru"},
	{"kettek/apng — animated-PNG decoding (BSD)", "https://github.com/kettek/apng"},
	{"klauspost/compress — compression (BSD-3)", "https://github.com/klauspost/compress"},

	{"Native engine — bundled C libraries (all free software)", ""},
	{"SDL2 — windowing, rendering & audio (zlib)", "https://www.libsdl.org"},
	{"libwebp — WebP image codec (BSD)", "https://chromium.googlesource.com/webm/libwebp"},
	{"libavif — AVIF image codec (BSD)", "https://github.com/AOMediaCodec/libavif"},
	{"dav1d / libaom — AV1 decoders (BSD)", "https://code.videolan.org/videolan/dav1d"},
	{"FreeType — font rasterizer (FreeType License)", "https://freetype.org"},
	{"HarfBuzz — text shaping (MIT)", "https://harfbuzz.github.io"},
	{"Opus / Ogg / Vorbis — audio codecs (BSD, Xiph.Org)", "https://xiph.org"},
	{"libpng / zlib — PNG & deflate (libpng / zlib licences)", "https://www.libpng.org"},

	{"Font", ""},
	{"OpenDyslexic — the dyslexia-friendly font (SIL OFL 1.1)", "https://opendyslexic.org"},
}

// aboutFlatLine is one rendered line after a block is wrapped to the column.
type aboutFlatLine struct {
	text   string
	indent int32 // x offset within the column (bullet hanging indent)
	col    sdl.Color
	mayo   bool  // draw the Mayo portrait centered above this line
	gap    int32 // extra vertical space BEFORE this line (paragraph spacing)
}

// buildAboutFlat reflows every prose block to colW. Cached by width in drawAbout
// so it runs only on a resize, never per frame.
func (a *App) buildAboutFlat(c *Ctx, colW int32) []aboutFlatLine {
	out := make([]aboutFlatLine, 0, len(aboutBlocks)*3)
	bulletIndent := c.TextWidth("•  ")
	for bi, b := range aboutBlocks {
		col := ColText
		switch b.kind {
		case abTitle, abAccent:
			col = ColAccent
		}
		gap := int32(aboutParaGap)
		if bi == 0 {
			gap = 0
		}
		switch b.kind {
		case abBullet:
			lines := c.WrapText(b.text, colW-bulletIndent, 0)
			for i, ln := range lines {
				fl := aboutFlatLine{text: ln, col: col, indent: bulletIndent}
				if i == 0 {
					fl.text = "•  " + ln
					fl.indent = 0
					fl.gap = 2 // bullets pack tighter than paragraphs
				}
				out = append(out, fl)
			}
		default:
			lines := c.WrapText(b.text, colW, 0)
			for i, ln := range lines {
				fl := aboutFlatLine{text: ln, col: col}
				if i == 0 {
					fl.gap = gap
					fl.mayo = b.kind == abMayo
				}
				out = append(out, fl)
			}
		}
	}
	return out
}

func (a *App) drawAbout(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)

	// --- header band ---------------------------------------------------------
	c.Heading(pad, pad, "About AsyncAO", ColText)
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.screen = a.prevScreen
		return
	}
	top := pad + 48
	c.Fill(sdl.Rect{X: 0, Y: top - 10, W: w, H: 1}, ColPanelHi) // hairline under the header
	viewH := h - top - pad

	// --- centered reading column --------------------------------------------
	colW := w - 2*pad - scrollBarW - 2*pad
	if colW > aboutMaxColW {
		colW = aboutMaxColW
	}
	if colW < 200 {
		colW = 200
	}
	x0 := (w - scrollBarW - colW) / 2
	if x0 < pad {
		x0 = pad
	}

	// Reflow only when the column width changes (off the hot path, but kept
	// alloc-free per frame per this repo's UI bar).
	if a.aboutFlat == nil || a.aboutFlatW != colW {
		a.aboutFlat = a.buildAboutFlat(c, colW)
		a.aboutFlatW = colW
	}

	lineH := int32(c.font.Height()) + 4
	mayoTex, mayoLogW, mayoLogH := a.mayoTexture()
	hasMayo := mayoTex != nil && mayoLogW > 0 && mayoLogH > 0
	mayoBlock := int32(0)
	if hasMayo {
		mayoBlock = mayoLogH + 12
	}

	// --- total content height (prose + portrait + link cards) ---------------
	contentH := int32(0)
	for _, fl := range a.aboutFlat {
		contentH += fl.gap + lineH
		if fl.mayo && hasMayo {
			contentH += mayoBlock
		}
	}
	contentH += aboutParaGap
	for _, g := range aboutLinkGroups() {
		contentH += aboutCardGap + g.height(lineH)
	}
	contentH += pad

	maxScroll := contentH - viewH
	if maxScroll < 0 {
		maxScroll = 0
	}
	a.aboutScroll -= c.WheelIn(sdl.Rect{X: 0, Y: top, W: w, H: viewH}) * scrollStepPx
	if a.aboutScroll < 0 {
		a.aboutScroll = 0
	}
	if a.aboutScroll > maxScroll {
		a.aboutScroll = maxScroll
	}

	clip := sdl.Rect{X: 0, Y: top, W: w, H: viewH}
	_ = c.Ren.SetClipRect(&clip)
	defer func() { _ = c.Ren.SetClipRect(nil) }()

	y := top - a.aboutScroll
	for _, fl := range a.aboutFlat {
		y += fl.gap
		if fl.mayo && hasMayo {
			dst := sdl.Rect{X: x0 + (colW-mayoLogW)/2, Y: y, W: mayoLogW, H: mayoLogH}
			_ = c.Ren.Copy(mayoTex, nil, &dst)
			y += mayoBlock
		}
		if y+lineH > top && y < top+viewH { // skip lines scrolled out of view
			c.Label(x0+fl.indent, y, fl.text, fl.col)
		}
		y += lineH
	}

	// --- credit cards --------------------------------------------------------
	y += aboutParaGap
	for _, g := range aboutLinkGroups() {
		y += aboutCardGap
		ch := g.height(lineH)
		// Only draw + hit-test a card while any of it is on screen.
		if y+ch > top && y < top+viewH {
			card := sdl.Rect{X: x0, Y: y, W: colW, H: ch}
			c.Fill(card, cardColor())
			c.Border(card, ColPanelHi)
			ry := y + aboutCardPad
			if g.title != "" {
				c.Label(x0+aboutCardPad, ry, g.title, ColAccent)
				ry += lineH + aboutTitleGap
			}
			for _, it := range g.items {
				bw := c.TextWidth(it.label) + 24
				if bw > colW-2*aboutCardPad {
					bw = colW - 2*aboutCardPad
				}
				// Per-button viewport guard: the kit hit-tests by cursor position,
				// not the clip rect, so a button scrolled behind the header must
				// not draw OR hit-test (else a header click opens a hidden link).
				if ry+btnH > top && ry < top+viewH {
					if c.Button(sdl.Rect{X: x0 + aboutCardPad, Y: ry, W: bw, H: btnH}, it.label) {
						openBrowser(it.url)
					}
				}
				ry += btnH + aboutLinkGap
			}
		}
		y += ch
	}
}

// aboutLinkGroup is a titled card of links. Built once from aboutLinks.
type aboutLinkGroup struct {
	title string
	items []aboutLink
}

// height is the card's pixel height for a given line height: padding, an optional
// title line, and one button per item.
func (g aboutLinkGroup) height(lineH int32) int32 {
	hgt := int32(2 * aboutCardPad)
	if g.title != "" {
		hgt += lineH + aboutTitleGap
	}
	n := int32(len(g.items))
	hgt += n*btnH + max32(0, n-1)*aboutLinkGap
	return hgt
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// aboutGroupsCache holds the parsed link groups (aboutLinks is static).
var aboutGroupsCache []aboutLinkGroup

// aboutLinkGroups parses aboutLinks into titled cards once: a blank-url entry
// starts a new card (its label is the title); url entries are that card's items.
// Items before the first header (the AsyncAO repo link) form an untitled card.
func aboutLinkGroups() []aboutLinkGroup {
	if aboutGroupsCache != nil {
		return aboutGroupsCache
	}
	var groups []aboutLinkGroup
	cur := -1
	for _, l := range aboutLinks {
		if l.url == "" { // section header → new card
			groups = append(groups, aboutLinkGroup{title: l.label})
			cur = len(groups) - 1
			continue
		}
		if cur < 0 { // leading item with no header yet
			groups = append(groups, aboutLinkGroup{})
			cur = 0
		}
		groups[cur].items = append(groups[cur].items, l)
	}
	aboutGroupsCache = groups
	return aboutGroupsCache
}

// openBrowser launches the system browser (go-sdl2 has no SDL_OpenURL
// binding; per-OS shellout is the portable fallback).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("ui: opening %s: %v", url, err)
	}
}
