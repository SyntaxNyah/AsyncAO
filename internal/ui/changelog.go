package ui

import (
	_ "embed"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/update"
)

// The built-in "What's New" / version-history screen. It renders an embedded
// copy of CHANGELOG.md, so every build ships its full history offline; the entry
// matching the running build (update.Version, stamped at link time) is tagged
// "installed". This complements the post-self-update modal (update_ui.go), which
// fetches only the LATEST release's notes from GitHub — this screen is the
// always-available history, opened on demand from the lobby top bar.
//
// Rendering mirrors drawAbout: the markdown is reflowed to the column width and
// cached by width (a.changelogFlat / a.changelogFlatW), so the wrap runs only on
// a resize, never per frame — the page is off the hot path, but this repo keeps
// every UI draw allocation-free.

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

// buildChangelogFlat reflows the embedded changelog to colW. A "## " line is a
// version header (accent, extra top gap, tagged when it names the running
// build); "### " is a sub-header; "- "/"* " is a bullet with a hanging indent;
// blank lines become paragraph gaps; the document H1 ("# ") is dropped since the
// screen has its own header. The content is a shipped, developer-controlled file
// (not hostile server input), so unlike the update modal's GitHub body it needs
// no runtime line cap (§17.4) — it's bounded by what we ship.
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

func (a *App) drawChangelog(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)

	// --- header band ---------------------------------------------------------
	c.Heading(pad, pad, "What's New — Version History", ColText)
	sub := "You're running AsyncAO " + strings.TrimSpace(update.Version)
	if update.IsDev() {
		sub = "You're running a development build"
	}
	c.Label(pad, pad+30, sub, ColTextDim)
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.screen = a.prevScreen
		return
	}
	top := pad + 56
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

	// Reflow only when the column width changes (kept alloc-free per frame per
	// this repo's UI bar, exactly like drawAbout).
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
	// A draggable scrollbar on the right for fast up/down navigation of a long history.
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
