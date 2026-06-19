package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/gifenc"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// Scene GIF export (M16): render a scene through a throwaway replay room into a
// fixed offscreen target (render.CaptureTarget), quantize each frame to a
// paletted image (so the RGBA is freed immediately), and encode an animated GIF
// (gifenc). It runs INCREMENTALLY — a small batch of frames per frame-loop tick
// on the render thread (SDL capture must be on-thread) behind a progress overlay
// — so the window stays responsive and nothing blocks. Off by default; zero cost
// on the live render path when not exporting.
const (
	gifExportH       = 360                // capped output height (4:3) — bounds memory + GIF size
	gifExportW       = gifExportH * 4 / 3 // 480
	gifExportFPS     = 12                 // capture cadence
	gifFrameDt       = time.Second / gifExportFPS
	gifDelayCs       = 100 / gifExportFPS // per-frame GIF delay, centiseconds (≈8)
	maxGifFrames     = 400                // ~33 s cap (§17.4): ~69 MB of paletted frames, sane GIF
	gifFramesPerTick = 4                  // frames captured per real frame (responsive + progress)
)

// gifExportJob is the in-flight render state (allocated only while exporting).
type gifExportJob struct {
	room   *courtroom.Courtroom
	ct     *render.CaptureTarget
	frames []*image.Paletted
	events []recEvent
	idx    int
	name   string
}

// startGifExport spins up the offscreen render of scene into an animated GIF.
// Must be called on the render thread (it creates the capture target).
func (a *App) startGifExport(scene *sceneRecording, name string) {
	if a.gifExporting {
		return
	}
	if scene == nil || len(scene.Events) == 0 {
		a.warnLine = "Add at least one line before exporting a GIF."
		a.warnAt = time.Now()
		return
	}
	ct, err := render.NewCaptureTarget(a.ctx.Ren, gifExportW, gifExportH)
	if err != nil {
		a.warnLine = "GIF export unavailable: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	room := courtroom.NewCourtroom(courtroom.NewURLBuilder(scene.Origin), a.d.Manager, nil, a.d.Audio)
	room.Typewriter.Interval, room.TextStay = a.replayTiming()
	room.CatchUp = false
	room.ReduceMotion = a.d.Prefs.ReduceMotion()
	room.ForceCharNames = a.d.Prefs.ForceCharNamesOn()
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = room.NotifyPreanimDone
	}
	if scene.StartBg != "" {
		room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: scene.StartBg})
	}
	if a.gifResultCh == nil {
		a.gifResultCh = make(chan string, 1)
	}
	a.gif = &gifExportJob{
		room:   room,
		ct:     ct,
		events: append([]recEvent(nil), scene.Events...),
		name:   sanitizeStem(name),
	}
	a.gifExporting = true
	a.warnLine = "Rendering GIF…"
	a.warnAt = time.Now()
}

// tickGifExport captures a bounded batch of frames (render thread). Feeds the
// next event when the room goes idle (same idle-gating as replay), Updates by a
// FIXED dt (faster than real-time), renders into the offscreen target, and
// quantizes — dropping the RGBA at once so only paletted frames are retained.
func (a *App) tickGifExport() {
	j := a.gif
	if j == nil || a.d.Viewport == nil {
		a.gifExporting = false
		return
	}
	for n := 0; n < gifFramesPerTick; n++ {
		if len(j.frames) >= maxGifFrames {
			a.finishGifExport()
			return
		}
		if j.room.Phase() == courtroom.PhaseIdle && j.room.QueueLen() == 0 {
			if j.idx >= len(j.events) { // exhausted and settled — done
				a.finishGifExport()
				return
			}
			j.room.HandleEvent(eventFromRec(j.events[j.idx]))
			j.idx++
		}
		j.room.Update(gifFrameDt)
		a.d.Viewport.SetSpriteFX(a.spriteFX())
		a.d.Viewport.Update(&j.room.Scene, gifFrameDt)
		img, err := j.ct.Capture(a.ctx.Ren, func(dst sdl.Rect) {
			a.d.Viewport.Render(a.ctx.Ren, &j.room.Scene, dst)
		})
		if err != nil {
			a.pushDebug("gif capture: " + err.Error())
			a.finishGifExport()
			return
		}
		j.frames = append(j.frames, gifenc.Quantize(img))
	}
}

// finishGifExport tears down the capture, restores the viewport's preanim
// callback, and hands the frames to an off-thread encode+write.
func (a *App) finishGifExport() {
	j := a.gif
	a.gif = nil
	a.gifExporting = false
	if j == nil {
		return
	}
	if j.ct != nil {
		j.ct.Close()
	}
	a.restoreViewportPreanim()
	a.makerPreviewIdx = -1 // the export drove the shared viewport — force the preview pane to rebuild
	if len(j.frames) == 0 {
		a.warnLine = "GIF export: nothing rendered (set an Origin/CDN and add a line)."
		a.warnAt = time.Now()
		return
	}
	frames := j.frames
	stem := j.name + "-" + time.Now().Format("20060102-150405")
	a.warnLine = fmt.Sprintf("Encoding GIF (%d frames)…", len(frames))
	a.warnAt = time.Now()
	go func() { a.gifResultCh <- encodeAndWriteGIF(frames, stem) }()
}

// restoreViewportPreanim rebinds the viewport's one-shot preanim callback to
// whatever room is now live (maker preview, the live room, or none).
func (a *App) restoreViewportPreanim() {
	if a.d.Viewport == nil {
		return
	}
	switch {
	case a.makerOpen && a.makerPreviewRoom != nil:
		a.d.Viewport.OnPreanimDone = a.makerPreviewRoom.NotifyPreanimDone
	case a.room != nil:
		a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
	default:
		a.d.Viewport.OnPreanimDone = nil
	}
}

// encodeAndWriteGIF (off-thread) encodes the frames and writes recordings\<stem>.gif.
func encodeAndWriteGIF(frames []*image.Paletted, stem string) string {
	data, err := gifenc.EncodeGIF(frames, gifDelayCs)
	if err != nil {
		return "GIF export failed: " + err.Error()
	}
	dir := recordingsDir()
	if dir == "" {
		return "GIF export failed: no recordings folder."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "GIF export failed: " + err.Error()
	}
	name := stem + ".gif"
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "GIF export failed: " + err.Error()
	}
	return fmt.Sprintf("GIF saved: recordings\\%s (%.1f MB).", name, float64(len(data))/(1024*1024))
}

// pollGifExport delivers the off-thread encode result to the UI.
func (a *App) pollGifExport() {
	if a.gifResultCh == nil {
		return
	}
	select {
	case msg := <-a.gifResultCh:
		a.warnLine = msg
		a.warnAt = time.Now()
	default:
	}
}

// drawGifProgress paints the export progress overlay (shown instead of the
// normal screen while rendering, since the export owns the viewport).
func (a *App) drawGifProgress(winW, winH int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: winW, H: winH}, sdl.Color{R: 10, G: 10, B: 14, A: 255})
	j := a.gif
	done := 0
	if j != nil {
		done = len(j.frames)
	}
	pct := done * 100 / maxGifFrames
	cx := winW / 2
	cy := winH / 2
	c.Label(cx-120, cy-30, "🎞  Rendering GIF…", ColText)
	c.Label(cx-120, cy, fmt.Sprintf("%d frames captured (%d%% of the cap)", done, pct), ColTextDim)
	// progress bar
	bar := sdl.Rect{X: cx - 160, Y: cy + 26, W: 320, H: 14}
	c.Fill(bar, ColPanel)
	fillW := int32(done) * bar.W / int32(maxGifFrames)
	if fillW > bar.W {
		fillW = bar.W
	}
	c.Fill(sdl.Rect{X: bar.X, Y: bar.Y, W: fillW, H: bar.H}, ColAccent)
	if c.Button(sdl.Rect{X: cx - 60, Y: cy + 54, W: 120, H: btnH}, "■ Stop & save") {
		a.finishGifExport() // keep what's been captured so far
	}
}
