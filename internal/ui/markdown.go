package ui

// Inline markdown for the What's New screen and the update window: **bold**, `code` and [text](link) become the
// chatbox's styled raster spans. Interim: like the chatbox it wraps by the regular weight's widths, and each block owns
// its textures, so only blocks on screen are rasterized.

import (
	"strings"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// markdownRasterMax bounds the block raster cache; each entry owns its textures, so a full cache is dropped whole.
	markdownRasterMax = 64
	markdownBold      = "**"
	markdownLinkMid   = "]("
	// markdownMaxHeading is the deepest heading level read; deeper "#" lines are paragraphs.
	markdownMaxHeading = 3
	// Heading levels the screens style.
	markdownTitle   = 1
	markdownSection = 2
	markdownSub     = 3
)

// markdownKey names one rasterized block: the same source, colour, width and device scale give the same raster.
type markdownKey struct {
	source string
	color  sdl.Color
	wrapW  int32
	dev    int32
}

// parseInlineMarkdown returns line without its markup and the runs over the result's runes, bold where ** was open.
// Backticks are dropped and a link keeps only its text; a ** left open runs to the end of the line.
func parseInlineMarkdown(line string, col sdl.Color) (string, []render.ColorSpan) {
	var b strings.Builder
	var spans []render.ColorSpan
	bold, runLen := false, 0
	flush := func() {
		if runLen > 0 {
			spans = append(spans, render.ColorSpan{Len: runLen, Color: col, Bold: bold})
			runLen = 0
		}
	}
	for i := 0; i < len(line); {
		rest := line[i:]
		switch {
		case strings.HasPrefix(rest, markdownBold):
			flush()
			bold = !bold
			i += len(markdownBold)
		case rest[0] == '`':
			i++
		case rest[0] == '[' && linkEnd(rest) > 0:
			mid := strings.Index(rest, markdownLinkMid)
			text := rest[1:mid]
			b.WriteString(text)
			runLen += utf8.RuneCountInString(text)
			i += linkEnd(rest)
		default:
			r, size := utf8.DecodeRuneInString(rest)
			b.WriteRune(r)
			runLen++
			i += size
		}
	}
	flush()
	return b.String(), spans
}

// markdownBlock is one heading, bullet or paragraph of a markdown document.
type markdownBlock struct {
	level      int    // heading level, 1 for "# "; 0 for a bullet or paragraph
	text       string // a heading's text without markup, or a bullet's or paragraph's markdown
	bullet     bool
	afterBlank bool // a blank line came before it
}

// parseMarkdownBlocks splits doc into headings ("#" to "###"), bullets ("- " or "* ") and paragraphs. A line that
// continues a bullet or paragraph is joined to it, so a hard-wrapped source reflows and ** spanning lines stays bold.
func parseMarkdownBlocks(doc string) []markdownBlock {
	var out []markdownBlock
	blank := false
	for _, raw := range strings.Split(doc, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			blank = true
			continue
		}
		if level := markdownHeadingLevel(line); level > 0 {
			out = append(out, markdownBlock{level: level, text: plainMarkdown(strings.TrimSpace(line[level+1:])), afterBlank: blank})
		} else if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			out = append(out, markdownBlock{text: strings.TrimSpace(line[2:]), bullet: true, afterBlank: blank})
		} else if n := len(out); n > 0 && out[n-1].level == 0 && !blank {
			out[n-1].text += " " + line
		} else {
			out = append(out, markdownBlock{text: line, afterBlank: blank})
		}
		blank = false
	}
	return out
}

// markdownHeadingLevel is the level of a "# " to "### " heading line, 0 for any other line.
func markdownHeadingLevel(line string) int {
	for level := 1; level <= markdownMaxHeading; level++ {
		if strings.HasPrefix(line, strings.Repeat("#", level)+" ") {
			return level
		}
	}
	return 0
}

// linkEnd is the byte length of a [text](target) at the start of s, or 0 when s doesn't start with one.
func linkEnd(s string) int {
	mid := strings.Index(s, markdownLinkMid)
	if mid <= 0 {
		return 0
	}
	end := strings.IndexByte(s[mid:], ')')
	if end < 0 {
		return 0
	}
	return mid + end + 1
}

// plainMarkdown is line without its markup, for labels and width estimates.
func plainMarkdown(line string) string {
	text, _ := parseInlineMarkdown(line, sdl.Color{})
	return text
}

// markdownRaster is line rasterized at wrapW logical px with its bold runs, cached; nil when building fails.
func (c *Ctx) markdownRaster(line string, col sdl.Color, wrapW int32) *render.MessageRaster {
	key := markdownKey{source: line, color: col, wrapW: wrapW, dev: c.textDevPct}
	if m, ok := c.markdownCache[key]; ok {
		return m
	}
	text, spans := parseInlineMarkdown(line, col)
	var m *render.MessageRaster
	var err error
	if render.NeedsEmojiFallback(text) || !c.coversFace(c.font, text) {
		if c.emojiLoad != nil {
			c.emojiLoad()
		}
		m, err = render.RasterizeFallback(c.Ren, c.deviceCoverRunes(c.font, DefaultScalePct, []rune(text)),
			c.emojiDeviceFont(DefaultScalePct), text, spans, wrapW, c.textDevPct)
	} else {
		m, err = render.RasterizeStyled(c.Ren, c.deviceTextFont(c.font), text, spans, wrapW, c.textDevPct)
	}
	if err != nil {
		m = nil
	}
	if c.markdownCache == nil {
		c.markdownCache = make(map[markdownKey]*render.MessageRaster, markdownRasterMax)
	} else if len(c.markdownCache) >= markdownRasterMax {
		c.purgeMarkdownCache()
	}
	c.markdownCache[key] = m
	if m != nil {
		c.markdownLineH = m.LineH()
	}
	return m
}

// markdownHeight is the height line takes at wrapW: the raster's when it is built, else markdownEstimate.
func (c *Ctx) markdownHeight(line string, col sdl.Color, wrapW int32) int32 {
	if h, ok := c.markdownBuiltHeight(line, col, wrapW); ok {
		return h
	}
	return c.markdownEstimate(line, wrapW)
}

// markdownBuiltHeight is the height of line's raster at wrapW, when that raster is cached.
func (c *Ctx) markdownBuiltHeight(line string, col sdl.Color, wrapW int32) (int32, bool) {
	m, ok := c.markdownCache[markdownKey{source: line, color: col, wrapW: wrapW, dev: c.textDevPct}]
	if !ok || m == nil {
		return 0, false
	}
	return m.Height(), true
}

// markdownEstimate is line's height from the regular weight's wrap, before its raster exists; callers that lay out
// many blocks cache it per width.
func (c *Ctx) markdownEstimate(line string, wrapW int32) int32 {
	lineH := c.markdownLineH
	if lineH <= 0 && c.font != nil {
		lineH = int32(c.font.Height())
	}
	return int32(max(1, len(c.WrapText(plainMarkdown(line), wrapW, 0)))) * lineH
}

// drawMarkdown draws line wrapped to wrapW at x, y and returns the height it took; plain text if rasterizing fails.
// Draw inside a pushClip so the raster re-asserts the clip in device pixels.
func (c *Ctx) drawMarkdown(x, y, wrapW int32, line string, col sdl.Color) int32 {
	m := c.markdownRaster(line, col, wrapW)
	if m == nil {
		c.LabelClipped(x, y, wrapW, plainMarkdown(line), col)
		return c.markdownHeight(line, col, wrapW)
	}
	var clip *sdl.Rect
	if c.clipOn {
		clip = &c.clipRect
	}
	m.DrawScaled(c.Ren, m.TotalRunes(), x, y, c.RenderScalePct(), clip)
	return m.Height()
}

// purgeMarkdownCache destroys every cached block raster; render thread only.
func (c *Ctx) purgeMarkdownCache() {
	for k, m := range c.markdownCache {
		if m != nil {
			m.Destroy()
		}
		delete(c.markdownCache, k)
	}
}
