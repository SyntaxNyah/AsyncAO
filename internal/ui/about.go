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
)

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
	"  Cocoa Bean · Lala · Peen · Emerald · Extra7 · Poki · Xocfti · Dag",
	"",
	"A special thank-you to Northgate — who backed this project, including",
	"financially, and gave me the inspiration to keep going. Without that",
	"support AsyncAO wouldn't have come this far this fast. Thank you.",
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
	y := pad + 48*1
	for _, line := range aboutLines {
		col := ColText
		if line == "Pull requests, bug fixes and feature requests are welcome!" ||
			line == "  Cocoa Bean · Lala · Peen · Emerald · Extra7 · Poki · Xocfti · Dag" {
			col = ColAccent
		}
		c.Label(pad, y, line, col)
		y += lineH
	}

	y += 10
	for _, link := range aboutLinks {
		bw := c.TextWidth(link.label) + 24
		if c.Button(sdl.Rect{X: pad, Y: y, W: bw, H: btnH}, link.label) {
			openBrowser(link.url)
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
