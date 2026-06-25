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
	// aboutLinkHeaderGap is the leading space before a section header in the link list.
	aboutLinkHeaderGap = int32(12)
)

// aboutMayoHeadLine is the first line of the Mayo section. The portrait draws
// directly above it, so the constant is shared by the text and the draw matcher
// (they can't drift).
const aboutMayoHeadLine = "Mayo — the AsyncAO mascot and app icon. The client was almost"

var aboutLines = []string{
	"AsyncAO " + protocol.Version,
	"",
	"Made by SyntaxNyah.",
	"",
	"Why this exists: I was tired of people having to download 20 gigabytes of",
	"files just to play, of client lookups taking ages, and — let's be honest —",
	"the AO2 client being a bit slow. AsyncAO streams exactly the assets it",
	"needs, learns what formats your server ships, caches everything, and",
	"renders on a zero-allocation hot path. Every millisecond counts.",
	"",
	"Optional Discord Rich Presence shows what you're playing - on by default,",
	"toggle it in Settings -> Discord, and there's no Discord dependency. Don't",
	"want it at all? A Discord-free build (no Discord code) is on GitHub Actions.",
	"",
	"All credit to the original Attorney Online developers:",
	"  • FanatSors — creator of the original Attorney Online",
	"  • OmniTroid — original AO2-Client developer, and a huge help on the",
	"    protocol documentation",
	"  • The AttorneyOnline organization and every AO2-Client contributor —",
	"    AsyncAO mirrors their protocol and courtroom semantics",
	"  • The webAO developers — the asset-URL conventions come from their work",
	"  • The AO-SDL developers — the SDL2 rendering model reference",
	"Thank you for two decades of courtroom drama. None of this would exist",
	"without the things you built and the inspiration they provided.",
	"",
	"Closed-source beta testers — thank you for the bug reports, feature",
	"requests and feedback that shaped AsyncAO during development:",
	"  Cocoa Bean · Lala · Peen · Emerald · Extra7 · Poki · Xocfti · Dag · CherriPop",
	"",
	"A special thank-you to Northgate — who backed this project, including",
	"financially, and gave me the inspiration to keep going. Without that",
	"support AsyncAO wouldn't have come this far this fast. Thank you.",
	"",
	aboutMayoHeadLine,
	"named \"MayAO\" (Maya + AO), but became AsyncAO — we wanted more Maya",
	"representation, since the AO2 client only ever showed Phoenix and",
	"Edgeworth. So the mascot is Mayo: inspired by Maya Fey from Ace",
	"Attorney, with the Go gopher's blue palette (AsyncAO is written in Go).",
	"",
	"Art commissioned by Nyah and illustrated by hlenbchan — please go support",
	"their work! Instagram: @hlenbchan2. Thank you for bringing Mayo to life.",
	"",
	"AsyncAO is FREE SOFTWARE — licensed under the GNU AGPL v3 (the LICENSE file),",
	"and free all the way down: EVERY dependency is open-source under an AGPL-v3-",
	"compatible licence (MIT / BSD / ISC / zlib / MPL-2.0 / LGPL, plus the GCC",
	"runtime's linking exception). No proprietary or closed-source pieces anywhere.",
	"Each one is linked and credited below — please support them too. Full details:",
	"docs/THIRD-PARTY-LICENSES.md in the repo.",
	"",
	"Copyright (c) 2026 SyntaxNyah and the AsyncAO contributors. Because the whole",
	"stack is AGPL-v3-compatible free software, AsyncAO may be freely redistributed",
	"— including as binary GitHub releases — in full compliance with the AGPL and",
	"every dependency's licence (ship the third-party notices alongside binaries).",
	"",
	"Pull requests, bug fixes and feature requests are welcome!",
}

// aboutLink is one row in the About link list. A blank url marks a SECTION HEADER
// (drawn as a label, not a clickable button) — so the list renders as titled
// groups; a blank label is just a spacer.
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

func (a *App) drawAbout(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	c.Heading(pad, pad, "About AsyncAO", ColText)
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.screen = a.prevScreen
		return
	}

	lineH := int32(c.font.Height()) + 4
	top := pad + 48 // content viewport starts below the heading
	viewH := h - top - pad
	// Mayo portrait — built once (lazily), drawn within the Mayo section below (not
	// alone at the top). mayoTexture returns the logical draw size.
	mayoTex, mayoLogW, mayoLogH := a.mayoTexture()
	mascotBlock := int32(0)
	if mayoTex != nil && mayoLogW > 0 && mayoLogH > 0 {
		mascotBlock = mayoLogH + 12 // portrait + gap above the Mayo text
	}
	// The page outgrew small windows, so it scrolls. Total height = text lines + the
	// portrait + the gap + the link rows. Link rows are buttons (btnH) or section
	// headers (a label, taller by the leading gap); sum exactly so the clamp is right.
	linksH := int32(0)
	for _, link := range aboutLinks {
		if link.url == "" { // section header (or blank spacer)
			linksH += aboutLinkHeaderGap
			if link.label != "" {
				linksH += lineH
			}
		} else {
			linksH += btnH + 6
		}
	}
	contentH := mascotBlock + int32(len(aboutLines))*lineH + 10 + linksH
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
	for _, line := range aboutLines {
		// The Mayo portrait sits with its section (centered above the head line),
		// not floating alone at the top of the page. Clip handles partial visibility.
		if line == aboutMayoHeadLine && mascotBlock > 0 {
			dst := sdl.Rect{X: (w - mayoLogW) / 2, Y: y, W: mayoLogW, H: mayoLogH}
			_ = c.Ren.Copy(mayoTex, nil, &dst)
			y += mascotBlock
		}
		col := ColText
		if line == "Pull requests, bug fixes and feature requests are welcome!" {
			col = ColAccent
		}
		c.Label(pad, y, line, col)
		y += lineH
	}

	y += 10
	for _, link := range aboutLinks {
		// A blank url is a SECTION HEADER (accent label), so the long credits list
		// reads as titled groups instead of one undifferentiated wall of buttons.
		if link.url == "" {
			y += aboutLinkHeaderGap
			if link.label != "" {
				if y+lineH > top && y < top+viewH {
					c.Label(pad, y, link.label, ColAccent)
				}
				y += lineH
			}
			continue
		}
		bw := c.TextWidth(link.label) + 24
		// Only draw + hit-test a button while it's inside the scroll viewport, so one
		// scrolled out of view can't be clicked through the heading or the page edge.
		if y+btnH > top && y < top+viewH {
			if c.Button(sdl.Rect{X: pad, Y: y, W: bw, H: btnH}, link.label) {
				openBrowser(link.url)
			}
		}
		y += btnH + 6
	}
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
