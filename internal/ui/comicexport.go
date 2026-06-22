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
	"time"
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Replay → comic / storyboard export (#100): turn a recording into a single PNG
// "page" — one still panel per IC line (the scene + that line's speech box) laid out
// in a grid. It rides the SAME machinery as the GIF/WebP/Video kinds (asset
// pre-warm, throwaway replay room, offscreen capture target, progress overlay,
// off-thread write); the only differences are that it captures ONE frame per message
// instead of an animation and writes a PNG. PNG is pure Go (image/png), so — unlike
// the other kinds — comic needs no Available() gate (no CGO, no ffmpeg). Off by
// default; nothing runs on the live render path unless the user exports.
const (
	// comicPanelW/H size one panel (4:3). Deliberately small + FIXED (not the
	// animation Size knob): the page stays a predictable size and the held panels
	// stay within budget — maxComicPanels × (W·H·4 bytes) ≈ 11 MB at 360×270 (vs
	// ~46 MB if a panel tracked an XL animation size). 270px tall leaves the speech
	// box enough height that ordinary lines read without shrinking to the font floor.
	comicPanelW = 360
	comicPanelH = 270

	// maxComicPanels bounds one page: the held panel RGBA (hard rule §17.4) AND the
	// final image size. A longer scene exports its first maxComicPanels lines (the
	// user is told); pagination is a later slice.
	maxComicPanels = 30

	// Page layout (a storyboard grid): comicCols panels per row, comicGutter between
	// panels, comicMargin around the page, a comicBorder frame per panel. comicMargin
	// must be ≥ comicBorder so the first row/column's frame stays on-page.
	comicCols   = 3
	comicGutter = 12
	comicMargin = 16
	comicBorder = 2

	// comicAdvanceSteps bounds the per-message fast-forward (shout→preanim→talking→
	// linger is ≤4 phase hops; the margin covers a slow reveal). comicBigStep
	// collapses every phase ROOM timer in one Update (the typewriter/timers clamp, so
	// the room never spins — same step SkipToIdle uses), so the loop lands on
	// PhaseLinger — the line fully typed and still on stage — in a handful of steps.
	comicAdvanceSteps = 12
	comicBigStep      = time.Hour
	// comicViewportStep advances the VIEWPORT a sane amount each step. Unlike the
	// room, the viewport's animation clock drains dt frame-by-frame, so a huge dt
	// would spin a looping sprite tens of thousands of iterations. A small step is
	// all that's needed: the room reaches the talking pose on its own timer, and the
	// viewport just has to sync that sprite onto the stage before the capture.
	comicViewportStep = 80 * time.Millisecond
)

// comicPageColor is the page background (near-white "paper" so the panels read as a
// printed comic page); comicBorderColor frames each panel.
var (
	comicPageColor   = color.RGBA{R: 244, G: 244, B: 248, A: 255}
	comicBorderColor = color.RGBA{R: 36, G: 36, B: 48, A: 255}
)

// tickComicExport captures ONE still panel per IC line (render thread). Non-message
// events (background / music) are applied to the room so a later panel shows the
// right scene, but emit no panel; blank posts (animated, no text) are skipped. One
// line per tick keeps the window responsive. Each line is drained to idle before the
// next is fed — REQUIRED because enqueue() queues a message fed while the room is
// busy, which would otherwise merge two lines into one panel.
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

// captureComicPanel feeds one IC message, fast-forwards it to the fully-revealed
// (PhaseLinger) state, captures a single panel (unless it's a blank post), and
// drains the room back to idle for the next line. The room is idle on entry (the
// previous line drained, and bg/music don't change phase), so HandleEvent begins
// this message immediately rather than queueing it.
func (a *App) captureComicPanel(j *gifExportJob, ev recEvent) {
	j.room.HandleEvent(eventFromRec(ev))
	// Advance past shout/preanim and reveal the whole line, stopping AT PhaseLinger
	// (full text, still on stage). A huge dt collapses each phase timer; the viewport
	// advances with it so the sprite shows its talking pose, not a frozen preanim frame.
	for i := 0; i < comicAdvanceSteps && j.room.Phase() != courtroom.PhaseLinger && j.room.Phase() != courtroom.PhaseIdle; i++ {
		j.room.Update(comicBigStep) // collapse the room's phase timers (no spin: it clamps)
		a.d.Viewport.SetSpriteFX(a.spriteFX())
		a.d.Viewport.Update(&j.room.Scene, comicViewportStep) // sane step so a looping sprite can't spin
	}
	// Safety net: if a 404-ing preanim never reached linger within the cap, force the
	// full reveal so the panel still shows the line. No Update runs before Capture, so
	// the room can't overwrite it back.
	j.room.Scene.VisibleRunes = utf8.RuneCountInString(j.room.Scene.MessageText)
	if !j.room.Scene.IsBlankPost && j.room.Scene.MessageText != "" {
		img, err := j.ct.Capture(a.ctx.Ren, func(dst sdl.Rect) {
			a.d.Viewport.Render(a.ctx.Ren, &j.room.Scene, dst)
			a.drawGifChatbox(j, &j.room.Scene, dst) // same speech box as the GIF export
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

// finishComicExport composes the captured panels into a PNG page and writes it
// off-thread. The panels are CPU RGBA owned solely by this goroutine now (a.gif is
// already nil), so the handoff is race-free — mirrors encodeAndWriteGIF. Called from
// finishGifExport AFTER the shared teardown, so the capture target is already closed.
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
	go func() { a.gifResultCh <- composeAndWriteComic(panels, stem, capped) }()
}

// composeAndWriteComic (off-thread) lays the panels into a page, PNG-encodes it, and
// writes recordings\<stem>.png. Returns the result line for the UI.
func composeAndWriteComic(panels []*image.RGBA, stem string, capped bool) string {
	page := composeComicPage(panels, comicCols, comicPanelW, comicPanelH, comicGutter, comicMargin, comicBorder)
	if page == nil {
		return "Comic export failed: no panels."
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, page); err != nil {
		return "Comic export failed: " + err.Error()
	}
	dir := recordingsDir()
	if dir == "" {
		return "Comic export failed: no recordings folder."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "Comic export failed: " + err.Error()
	}
	name := stem + ".png"
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		return "Comic export failed: " + err.Error()
	}
	msg := fmt.Sprintf("Comic saved: recordings\\%s (%d panels, %.1f MB).", name, len(panels), float64(buf.Len())/(1024*1024))
	if capped {
		msg += fmt.Sprintf(" Showing the first %d lines (one page's worth).", len(panels))
	}
	return msg
}

// composeComicPage lays panels into a storyboard grid: `cols` per row (fewer if
// there aren't that many), a gutter between panels, a margin around the page, and a
// border frame per panel on a paper-coloured background. Pure (no SDL) so the layout
// math is unit-tested. Returns nil for no panels. panelW/H are parameters (not the
// consts) so the test can drive it with tiny synthetic panels.
func composeComicPage(panels []*image.RGBA, cols, panelW, panelH, gutter, margin, border int) *image.RGBA {
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
		// Border frame just outside the panel (it sits in the gutter, leaving a gap).
		frame := image.Rect(x-border, y-border, x+panelW+border, y+panelH+border)
		draw.Draw(page, frame, &image.Uniform{C: comicBorderColor}, image.Point{}, draw.Src)
		if panel != nil {
			dst := image.Rect(x, y, x+panelW, y+panelH)
			draw.Draw(page, dst, panel, panel.Bounds().Min, draw.Src)
		}
	}
	return page
}
