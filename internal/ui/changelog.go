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
	changelogBullet       = "•"
	// changelogBulletGap separates consecutive bullets so wrapped ones read apart.
	changelogBulletGap int32 = 4
	// changelogBackW is the Back button's width.
	changelogBackW int32 = 90
)

// changelogBlock is one header, bullet or paragraph of the changelog. A header is a plain label; a bullet or paragraph
// is markdown drawn through the block raster (markdown.go).
type changelogBlock struct {
	header string // the header text; empty for a bullet or paragraph
	md     string // the markdown source of a bullet or paragraph
	col    sdl.Color
	gap    int32 // extra space before the block
	bullet bool
	// estW and estH cache the height estimate at one width, until the block is rasterized and its height is known.
	estW, estH int32
}

// buildChangelogBlocks styles the embedded changelog's blocks: "## " is a version header (tagged when it names the
// running build), "### " a sub-header, the "# " title is dropped (the screen shows its own). The file is shipped, not
// server input, so it needs no cap.
func buildChangelogBlocks() []changelogBlock {
	cur := strings.TrimPrefix(strings.TrimSpace(update.Version), "v")
	markInstalled := !update.IsDev()
	src := parseMarkdownBlocks(changelogMD)
	out := make([]changelogBlock, 0, len(src))
	for i, b := range src {
		gap := int32(0)
		switch {
		case b.afterBlank:
			gap = changelogParaGap
		case b.bullet && i > 0 && src[i-1].bullet:
			gap = changelogBulletGap
		}
		switch b.level {
		case markdownTitle:
			continue
		case markdownSection:
			title := b.text
			if markInstalled && changelogHeaderMatches(title, cur) {
				title += changelogInstalledTag
			}
			gap = changelogVersionGap
			if len(out) == 0 {
				gap = 0 // a header opening the screen hugs the top hairline
			}
			out = append(out, changelogBlock{header: title, col: ColAccent, gap: gap})
		case markdownSub:
			out = append(out, changelogBlock{header: b.text, col: ColTextDim, gap: gap})
		default:
			out = append(out, changelogBlock{md: b.text, col: ColText, gap: gap, bullet: b.bullet})
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

	// One text column; the header row sits on the same margins so the title, the text and Back line up.
	colW := w - 2*pad - scrollBarW - 2*pad
	colW = value.Clamp(colW, 200, aboutMaxColW)
	x0 := (w - scrollBarW - colW) / 2
	if x0 < pad {
		x0 = pad
	}

	// Content starts below the app-chrome band.
	hdrY := a.topChromeH() + pad
	c.Heading(x0, hdrY, "What's New — Version History", ColText)
	sub := "You're running AsyncAO " + strings.TrimSpace(update.Version)
	if update.IsDev() {
		sub = "You're running a development build"
	}
	c.Label(x0, hdrY+30, sub, ColTextDim)
	if c.Button(sdl.Rect{X: x0 + colW - changelogBackW, Y: hdrY, W: changelogBackW, H: btnH}, "Back") {
		a.screen = a.prevScreen
		return
	}
	top := hdrY + 56
	c.Fill(sdl.Rect{X: x0, Y: top - 10, W: colW, H: 1}, ColPanelHi) // hairline under the header, column wide
	viewH := h - top - pad

	if a.changelogBlocks == nil {
		a.changelogBlocks = buildChangelogBlocks()
	}
	lineH := int32(c.font.Height()) + 4
	bulletIndent := c.TextWidth(changelogBullet + "  ")
	blockH := func(b *changelogBlock) int32 {
		if b.header != "" {
			return lineH
		}
		wrapW := colW
		if b.bullet {
			wrapW -= bulletIndent
		}
		if h, ok := c.markdownBuiltHeight(b.md, b.col, wrapW); ok {
			return h
		}
		if b.estW != wrapW {
			b.estW, b.estH = wrapW, c.markdownEstimate(b.md, wrapW)
		}
		return b.estH
	}
	contentH := int32(0)
	for i := range a.changelogBlocks {
		contentH += a.changelogBlocks[i].gap + blockH(&a.changelogBlocks[i])
	}
	contentH += pad

	a.changelogScroll -= c.WheelIn(sdl.Rect{X: 0, Y: top, W: w, H: viewH}) * scrollStepPx
	track := sdl.Rect{X: w - scrollBarW - pad, Y: top, W: scrollBarW, H: viewH}
	a.changelogScroll = c.VScrollbar("changelogbar", track, a.changelogScroll, contentH, viewH)

	clipPrev, clipHad := c.pushClip(sdl.Rect{X: 0, Y: top, W: w, H: viewH})
	defer c.popClip(clipPrev, clipHad)

	y := top - a.changelogScroll
	for i := range a.changelogBlocks {
		b := &a.changelogBlocks[i]
		y += b.gap
		h := blockH(b)
		if y+h > top && y < top+viewH { // only blocks on screen draw, and so only they are rasterized
			switch {
			case b.header != "":
				c.Label(x0, y, b.header, b.col)
			case b.bullet:
				c.Label(x0, y, changelogBullet, b.col)
				h = c.drawMarkdown(x0+bulletIndent, y, colW-bulletIndent, b.md, b.col)
			default:
				h = c.drawMarkdown(x0, y, colW, b.md, b.col)
			}
		}
		y += h
	}
}
