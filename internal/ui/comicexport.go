package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Replay to comic export: one still panel per IC line, in a grid on a PNG page, using the shared export steps.
// PNG is pure Go, so unlike the other kinds this needs no Available() check.
const (
	// comicPanelW/H are fixed rather than following the animation size, keeping page and held panel memory predictable;
	// 270 tall leaves the speech box enough height that ordinary lines don't shrink to the font floor.
	comicPanelW = 360
	comicPanelH = 270

	// comicPanelsPerPage is one page: 30 at 3 columns makes 10 rows; more lines spill onto further pages.
	comicPanelsPerPage = 30
	// maxComicPages bounds total held panel RGBA (hard rule 4); a longer scene exports its first maxComicPanels lines.
	maxComicPages  = 3
	maxComicPanels = comicPanelsPerPage * maxComicPages

	// Storyboard grid. comicMargin must be at least comicBorder so the first row and column's frames stay on the page.
	comicCols   = 3
	comicGutter = 12
	comicMargin = 16
	comicBorder = 2

	// comicAdvanceSteps bounds the per-message fast-forward. comicBigStep collapses every room phase timer in one
	// Update
	// (they clamp, so the room never spins), landing on PhaseLinger in a few steps.
	comicAdvanceSteps = 12
	comicBigStep      = time.Hour
	// comicViewportStep is small because the viewport's clock drains dt frame by frame; a huge dt would spin a looping
	// sprite.
	comicViewportStep = 80 * time.Millisecond
)

// comicPageColor is near-white paper; comicBorderColor frames each panel.
var (
	comicPageColor   = color.RGBA{R: 244, G: 244, B: 248, A: 255}
	comicBorderColor = color.RGBA{R: 36, G: 36, B: 48, A: 255}
)

// tickComicExport captures one panel per IC line per tick, keeping the window responsive; bg and music events only
// carry the scene forward. Each line drains to idle first, because enqueue() would merge two lines into one panel.
func (a *App) tickComicExport(j *gifExportJob) {
	if j.captured >= j.maxFrames {
		a.finishGifExport() // page full → compose + write (routes to finishComicExport)
		return
	}
	for j.idx < len(j.events) {
		ev := j.events[j.idx]
		if ev.Kind != int(courtroom.EventMessage) {
			j.room.HandleEvent(eventFromRec(ev)) // bg/music: carry the scene forward, no panel
			j.idx++
			continue
		}
		a.captureComicPanel(j, ev) // one line → one panel, then yield the tick
		j.idx++
		return
	}
	a.finishGifExport() // events exhausted
}

// captureComicPanel feeds one message, fast-forwards to PhaseLinger, captures a panel unless it's a blank post, and
// drains to idle. The room is idle on entry, so HandleEvent begins this message at once rather than queueing it.
func (a *App) captureComicPanel(j *gifExportJob, ev recEvent) {
	j.room.HandleEvent(eventFromRec(ev))
	// Stop at PhaseLinger (full text, still on stage); the viewport advances too, so the sprite shows its talking pose.
	for i := 0; i < comicAdvanceSteps && j.room.Phase() != courtroom.PhaseLinger && j.room.Phase() != courtroom.PhaseIdle; i++ {
		// SkipToEnd rather than relying on comicBigStep, which would reveal the text but leave blips dribbling out
		// under
		// courtroom.blipsPerUpdate. Harmless before the text phase.
		j.room.Typewriter.SkipToEnd()
		j.room.Update(comicBigStep) // collapse the room's phase timers (no spin: it clamps)
		a.d.Viewport.SetSpriteFX(a.spriteFX())
		a.d.Viewport.Update(&j.room.Scene, comicViewportStep) // sane step so a looping sprite can't spin
	}
	// If a 404-ing preanim never reached linger, force the full reveal; no Update runs before Capture to overwrite it.
	j.room.Scene.VisibleRunes = utf8.RuneCountInString(j.room.Scene.MessageText)
	if !j.room.Scene.IsBlankPost && j.room.Scene.MessageText != "" {
		img, err := j.ct.Capture(a.ctx.Ren, func(dst sdl.Rect) {
			a.d.Viewport.Render(a.ctx.Ren, &j.room.Scene, dst)
			a.drawComicBubble(j, &j.room.Scene, dst) // a paper speech bubble, not the dark GIF box
		})
		if err != nil {
			a.pushDebug("comic capture: " + err.Error())
		} else {
			j.panels = append(j.panels, img)
			j.captured++
		}
	}
	j.room.SkipToIdle() // drain to idle so the NEXT message begins fresh (1 line : 1 panel)
}

// finishComicExport writes the page off-thread; a.gif is already nil, so this goroutine solely owns the panels.
func (a *App) finishComicExport(j *gifExportJob) {
	if len(j.panels) == 0 {
		a.warnLine = "Comic export: nothing rendered (set an Origin/CDN and add a line)."
		a.warnAt = time.Now()
		return
	}
	panels := j.panels
	capped := j.captured >= maxComicPanels && j.idx < len(j.events) // hit the page cap with lines left
	stem := j.name + "-comic-" + time.Now().Format("20060102-150405")
	a.warnLine = fmt.Sprintf("Encoding comic (%d panels)…", len(panels))
	a.warnAt = time.Now()
	ch := a.gifResultCh
	go func() { ch <- composeAndWriteComic(panels, stem, capped) }()
}

// paginateComic splits panels into in-order pages of at most perPage, or nil for no panels.
func paginateComic(panels []*image.RGBA, perPage int) [][]*image.RGBA {
	if perPage < 1 {
		perPage = 1
	}
	var pages [][]*image.RGBA
	for i := 0; i < len(panels); i += perPage {
		end := i + perPage
		if end > len(panels) {
			end = len(panels)
		}
		pages = append(pages, panels[i:end])
	}
	return pages
}

// composeAndWriteComic (off-thread) writes <stem>.png, or <stem>-pN.png per page; returns the result and first path.
func composeAndWriteComic(panels []*image.RGBA, stem string, capped bool) exportResult {
	pages := paginateComic(panels, comicPanelsPerPage)
	if len(pages) == 0 {
		return exportResult{msg: "Comic export failed: no panels."}
	}
	dir := recordingsDir()
	if dir == "" {
		return exportResult{msg: "Comic export failed: no recordings folder."}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return exportResult{msg: "Comic export failed: " + err.Error()}
	}
	var totalBytes, startNum int = 0, 1
	firstPath := ""
	for i, pagePanels := range pages {
		img := composeComicPage(pagePanels, comicCols, comicPanelW, comicPanelH, comicGutter, comicMargin, comicBorder, startNum)
		startNum += len(pagePanels) // page 2's panels continue numbering, not restart at 1
		if img == nil {
			continue
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return exportResult{msg: "Comic export failed: " + err.Error()}
		}
		name := stem + ".png"
		if len(pages) > 1 { // number the files only when the comic spans multiple pages
			name = fmt.Sprintf("%s-p%d.png", stem, i+1)
		}
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
			return exportResult{msg: "Comic export failed: " + err.Error()}
		}
		if firstPath == "" {
			firstPath = full
		}
		totalBytes += buf.Len()
	}
	saved := stem + ".png"
	pageNote := ""
	if len(pages) > 1 {
		saved = fmt.Sprintf("%s-p1..p%d.png", stem, len(pages))
		pageNote = fmt.Sprintf(" across %d pages", len(pages))
	}
	msg := fmt.Sprintf("Comic saved: recordings\\%s (%d panels%s, %.1f MB).",
		saved, len(panels), pageNote, float64(totalBytes)/(1024*1024))
	if capped {
		msg += fmt.Sprintf(" Showing the first %d lines (the %d-page max).", len(panels), maxComicPages)
	}
	return exportResult{msg: msg, path: firstPath}
}

// composeComicPage lays panels in a bordered grid on a paper background, or returns nil for no panels.
func composeComicPage(panels []*image.RGBA, cols, panelW, panelH, gutter, margin, border, startNum int) *image.RGBA {
	n := len(panels)
	if n == 0 {
		return nil
	}
	if cols > n {
		cols = n // a short strip uses a single row
	}
	if cols < 1 {
		cols = 1
	}
	rows := (n + cols - 1) / cols
	pageW := margin*2 + cols*panelW + (cols-1)*gutter
	pageH := margin*2 + rows*panelH + (rows-1)*gutter
	page := image.NewRGBA(image.Rect(0, 0, pageW, pageH))
	draw.Draw(page, page.Bounds(), &image.Uniform{C: comicPageColor}, image.Point{}, draw.Src)
	for i, panel := range panels {
		col := i % cols
		row := i / cols
		x := margin + col*(panelW+gutter)
		y := margin + row*(panelH+gutter)
		// The frame sits just outside the panel, in the gutter.
		frame := image.Rect(x-border, y-border, x+panelW+border, y+panelH+border)
		draw.Draw(page, frame, &image.Uniform{C: comicBorderColor}, image.Point{}, draw.Src)
		if panel != nil {
			dst := image.Rect(x, y, x+panelW, y+panelH)
			draw.Draw(page, dst, panel, panel.Bounds().Min, draw.Src)
		}
		if panelW >= 28 && panelH >= 24 { // room for the badge without swallowing the panel
			drawPanelNumber(page, x, y, startNum+i) // storyboard reading order
		}
	}
	return page
}

// Panel-number badge geometry.
const (
	comicDigitW        = 7  // basicfont.Face7x13 advance width
	comicBadgeH        = 16 // badge height
	comicBadgeBaseline = 12 // text baseline within the badge (face ascent ≈ 11)
)

var (
	comicBadgeColor     = color.RGBA{R: 28, G: 28, B: 44, A: 255}
	comicBadgeTextColor = color.RGBA{R: 245, G: 245, B: 250, A: 255}
)

// drawPanelNumber paints a small reading-order badge in a panel's top-left corner.
func drawPanelNumber(img *image.RGBA, x, y, n int) {
	s := strconv.Itoa(n)
	bw := len(s)*comicDigitW + 6
	draw.Draw(img, image.Rect(x, y, x+bw, y+comicBadgeH), &image.Uniform{C: comicBadgeColor}, image.Point{}, draw.Src)
	drawComicText(img, x+3, y+comicBadgeBaseline, s, comicBadgeTextColor)
}

// drawComicText draws with the built-in 7x13 face, avoiding SDL on the off-thread path; y is the baseline.
func drawComicText(img *image.RGBA, x, y int, text string, col color.RGBA) {
	(&font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y),
	}).DrawString(text)
}

// Comic speech bubble: drawn during capture via SDL so it reuses the live font stack (Unicode names, inline colours),
// with message text forced to dark ink. Render thread only.
const (
	comicBubbleMargin = 10 // gap from the panel edges
	comicBubblePad    = 8  // text padding inside the bubble
	comicNameTagH     = 20 // speaker name-caption height (straddles the bubble top)
	comicTailHalf     = 11 // half-width of the tail's base
	comicTailH        = 13 // tail height (its apex points up toward the speaker)
)

var (
	comicBubbleFill = sdl.Color{R: 248, G: 248, B: 252, A: 240} // paper
	comicBubbleInk  = sdl.Color{R: 26, G: 26, B: 38, A: 255}    // dark text + border on the paper
	comicNameFill   = sdl.Color{R: 26, G: 26, B: 42, A: 255}    // name caption tag
	comicNameInk    = sdl.Color{R: 246, G: 246, B: 250, A: 255} // name text
)

// comicBubbleRect places the bubble across the panel's bottom, sized to the text, capped to leave room for the sprite.
func comicBubbleRect(vp sdl.Rect, textH int32) sdl.Rect {
	bubbleH := textH + 2*comicBubblePad
	if maxH := vp.H * 3 / 5; bubbleH > maxH {
		bubbleH = maxH
	}
	if minH := int32(comicNameTagH + comicBubblePad); bubbleH < minH {
		bubbleH = minH
	}
	return sdl.Rect{
		X: vp.X + comicBubbleMargin,
		Y: vp.Y + vp.H - comicBubbleMargin - bubbleH,
		W: vp.W - 2*comicBubbleMargin,
		H: bubbleH,
	}
}

// drawComicBubble draws the tail, bubble, name caption and text for one panel, inside the capture callback.
func (a *App) drawComicBubble(j *gifExportJob, sc *courtroom.Scene, vp sdl.Rect) {
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	c := a.ctx
	innerW := vp.W - 2*comicBubbleMargin - 2*comicBubblePad
	if j.chatRaster == nil || j.chatText != sc.MessageText {
		if j.chatRaster != nil {
			j.chatRaster.Destroy()
			j.chatRaster = nil
		}
		if sc.MessageText != "" {
			j.chatRaster = a.fitChatRaster(sc, innerW, vp.H, j.chatPct, false, true) // skinned=false: comicInk → dark text
		}
		j.chatText = sc.MessageText
	}
	textH := int32(0)
	if j.chatRaster != nil {
		textH = j.chatRaster.Height()
	}
	bubble := comicBubbleRect(vp, textH)

	// Tail first, so the bubble and its border cover its base.
	a.drawComicTail(bubble.X+bubble.W*2/3, bubble.Y, comicTailHalf, comicTailH)
	c.Fill(bubble, comicBubbleFill)
	c.Border(bubble, comicBubbleInk)

	if j.chatRaster != nil {
		_ = c.Ren.SetClipRect(&bubble) // a long line clips INSIDE the bubble, never off-panel
		j.chatRaster.Draw(c.Ren, sc.VisibleRunes, bubble.X+comicBubblePad, bubble.Y+comicBubblePad)
		_ = c.Ren.SetClipRect(nil)
	}

	// A caption tag straddling the bubble's top-left; the covering-face pick keeps a non-Latin showname from being
	// tofu.
	if sc.ShownameText != "" {
		nameW := c.TextWidth(sc.ShownameText) + 14
		if nameW > bubble.W {
			nameW = bubble.W
		}
		tag := sdl.Rect{X: bubble.X + 6, Y: bubble.Y - comicNameTagH + 1, W: nameW, H: comicNameTagH}
		c.Fill(tag, comicNameFill)
		a.labelEmoji(c.ChatFontFor(DefaultScalePct, sc.ShownameText), c.EmojiFont(DefaultScalePct), tag.X+7, tag.Y+3, tag.W-12, sc.ShownameText, comicNameInk)
	}
}

// drawComicTail fills an upward tail with its apex at (cx, baseY-h); a dark span then a 1px-inset paper span per row
// borders the slanted sides.
func (a *App) drawComicTail(cx, baseY, half, h int32) {
	c := a.ctx
	for dy := int32(0); dy < h; dy++ {
		w := half * dy / h // widens from the apex (0) down to the base (half)
		y := baseY - h + dy
		c.Fill(sdl.Rect{X: cx - w - 1, Y: y, W: 2*w + 2, H: 1}, comicBubbleInk) // dark border row
		if w > 0 {
			c.Fill(sdl.Rect{X: cx - w, Y: y, W: 2 * w, H: 1}, comicBubbleFill) // paper inset
		}
	}
}
