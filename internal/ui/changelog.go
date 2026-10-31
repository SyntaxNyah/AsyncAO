package ui

import (
	_ "embed"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/update"
)

// The built-in What's New / version-history screen. It renders an embedded CHANGELOG.md, so every build ships its
// history offline; the entry matching update.Version is tagged installed. Wrapped lines are cached by width.

//go:embed assets/CHANGELOG.md
var changelogMD string

const (
	// changelogParaGap is the vertical space before a new paragraph or bullet group.
	changelogParaGap int32 = 8
	// changelogVersionGap is the larger space above a "## version" header.
	changelogVersionGap int32 = 20
	// changelogInstalledTag flags the running build's entry in the history.
	changelogInstalledTag = "    • installed"
)

// buildChangelogFlat reflows the embedded changelog to colW. "## " is a version header (tagged when it names the
// running build), "### " a sub-header, "- "/"* " a bullet with hanging indent; blank lines are paragraph gaps; the H1
// is dropped. The file is shipped, not server input, so it needs no line cap.
func (a *App) buildChangelogFlat(c *Ctx, colW int32) []aboutFlatLine {
	out := make([]aboutFlatLine, 0, 256)
	bulletIndent := c.TextWidth("•  ")
	cur := strings.TrimPrefix(strings.TrimSpace(update.Version), "v")
	markInstalled := !update.IsDev()
	pendingGap := int32(0)
	seenVersion := false
	for _, raw := range strings.Split(changelogMD, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		switch {
		case trimmed == "":
			if pendingGap < changelogParaGap {
				pendingGap = changelogParaGap
			}
		case strings.HasPrefix(trimmed, "## "):
			title := strings.TrimSpace(trimmed[len("## "):])
			if markInstalled && changelogHeaderMatches(title, cur) {
				title += changelogInstalledTag
			}
			gap := changelogVersionGap
			if !seenVersion {
				gap = 0 // the first header hugs the top hairline
			}
			seenVersion = true
			out = append(out, aboutFlatLine{text: title, col: ColAccent, gap: gap})
			pendingGap = 0
		case strings.HasPrefix(trimmed, "# "):
			continue // document title — the screen already shows its own heading
		case strings.HasPrefix(trimmed, "### "):
			out = append(out, aboutFlatLine{text: strings.TrimSpace(trimmed[len("### "):]), col: ColTextDim, gap: pendingGap})
			pendingGap = 0
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
			lines := c.WrapText(strings.TrimSpace(trimmed[2:]), colW-bulletIndent, 0)
			for i, ln := range lines {
				fl := aboutFlatLine{text: ln, col: ColText, indent: bulletIndent}
				if i == 0 {
					fl.text = "•  " + ln
					fl.indent = 0
					fl.gap = pendingGap
				}
				out = append(out, fl)
			}
			pendingGap = 0
		default:
			lines := c.WrapText(trimmed, colW, 0)
			for i, ln := range lines {
				fl := aboutFlatLine{text: ln, col: ColText}
				if i == 0 {
					fl.gap = pendingGap
				}
				out = append(out, fl)
			}
			pendingGap = 0
		}
	}
	return out
}

// changelogHeaderMatches reports whether a "## " header names the running
// version: its first whitespace-separated token, minus a leading "v", equals cur
// (already v-stripped). So "## v1.0.0 — 2026-06-27" matches cur "1.0.0".
func changelogHeaderMatches(title, cur string) bool {
	if cur == "" {
		return false
	}
	fields := strings.Fields(title)
	if len(fields) == 0 {
		return false
	}
	return strings.TrimPrefix(fields[0], "v") == cur
}

// markChangelogSeen records the current build version as seen when the What's New screen opens.
// Nothing shows "unread" any more; the stamp is the only record of which notes were seen.
func (a *App) markChangelogSeen() {
	{
		pv1 := strings.TrimSpace(update.Version)
		a.d.Prefs.Update(func(sn *config.Settings) {
			sn.ChangelogSeen = pv1
		})
	}
}

func (a *App) drawChangelog(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)

	// Content starts below the app-chrome band.
	hdrY := a.topChromeH() + pad
	c.Heading(pad, hdrY, "What's New — Version History", ColText)
	sub := "You're running AsyncAO " + strings.TrimSpace(update.Version)
	if update.IsDev() {
		sub = "You're running a development build"
	}
	c.Label(pad, hdrY+30, sub, ColTextDim)
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: hdrY, W: 90, H: btnH}, "Back") {
		a.screen = a.prevScreen
		return
	}
	top := hdrY + 56
	c.Fill(sdl.Rect{X: 0, Y: top - 10, W: w, H: 1}, ColPanelHi) // hairline under the header
	viewH := h - top - pad

	colW := w - 2*pad - scrollBarW - 2*pad
	colW = value.Clamp(colW, 200, aboutMaxColW)
	x0 := (w - scrollBarW - colW) / 2
	if x0 < pad {
		x0 = pad
	}

	// Reflow only when the column width changes, as drawAbout does.
	if a.changelogFlat == nil || a.changelogFlatW != colW {
		a.changelogFlat = a.buildChangelogFlat(c, colW)
		a.changelogFlatW = colW
	}

	lineH := int32(c.font.Height()) + 4
	contentH := int32(0)
	for _, fl := range a.changelogFlat {
		contentH += fl.gap + lineH
	}
	contentH += pad

	a.changelogScroll -= c.WheelIn(sdl.Rect{X: 0, Y: top, W: w, H: viewH}) * scrollStepPx
	track := sdl.Rect{X: w - scrollBarW - pad, Y: top, W: scrollBarW, H: viewH}
	a.changelogScroll = c.VScrollbar("changelogbar", track, a.changelogScroll, contentH, viewH)

	clip := sdl.Rect{X: 0, Y: top, W: w, H: viewH}
	_ = c.Ren.SetClipRect(&clip)
	defer func() { _ = c.Ren.SetClipRect(nil) }()

	y := top - a.changelogScroll
	for _, fl := range a.changelogFlat {
		y += fl.gap
		if y+lineH > top && y < top+viewH { // skip lines scrolled out of view
			c.Label(x0+fl.indent, y, fl.text, fl.col)
		}
		y += lineH
	}
}
