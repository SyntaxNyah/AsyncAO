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
	"Pull requests, bug fixes and feature requests are welcome!",
}

type aboutLink struct {
	label string
	url   string
}

var aboutLinks = []aboutLink{
	{"AsyncAO repository (PRs welcome!)", aboutRepoURL},
	{"AO2-Client — the original client", aboutAO2URL},
	{"webAO — AO in the browser", aboutWebAOURL},
	{"AO-SDL — SDL2 AO client", aboutAOSDLURL},
	{"aceattorneyonline.com", aboutAOSiteURL},
	{"hlenbchan — app icon artist (Instagram)", aboutArtistURL},
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
	// portrait + the gap + the link buttons; clamp the wheel offset to the ends.
	contentH := mascotBlock + int32(len(aboutLines))*lineH + 10 + int32(len(aboutLinks))*(btnH+6)
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
