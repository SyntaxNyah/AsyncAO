package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/gifenc"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/webpenc"
)

// sceneExportFromPath loads a recording and exports it straight to a GIF (the
// Studio "🎞 GIF" button) or an animated WebP ("🎬 WebP") — no trip through the
// maker. A bundled archive renders from its own folder (the archive source is
// dropped when the export finishes).
func (a *App) sceneExportFromPath(path string, asWebP bool) {
	if a.gifExporting || a.replaying || a.recActive {
		a.warnLine = "Finish the current replay / recording first."
		a.warnAt = time.Now()
		return
	}
	rec, err := loadRecording(path)
	if err != nil {
		a.warnLine = "Couldn't load recording: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	if rec.Bundled {
		a.beginBundledReplay(rec, filepath.Dir(path)) // archive source + repoint Origin
	}
	a.startSceneExport(rec, strings.TrimSuffix(filepath.Base(path), recordingExt), asWebP)
}

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
	gifDelayCs       = 100 / gifExportFPS  // per-frame GIF delay, centiseconds (≈8)
	webpFrameMs      = 1000 / gifExportFPS // per-frame WebP duration, ms (≈83)
	webpQuality      = 80                  // lossy WebP quality (0..100): small but clean
	maxGifFrames     = 400                 // ~33 s cap (§17.4): ~69 MB of paletted frames, sane GIF
	gifFramesPerTick = 4                   // frames captured per real frame (responsive + progress)

	// Asset pre-warm (so the GIF shows the characters, not an empty stage): the
	// export advances ~4× faster than a replay and outruns async sprite fetches,
	// so we prefetch every scene asset and wait for them to decode before
	// capturing. gifWarmQuiet ends the wait once no new asset has landed for a
	// beat (the common, warm-cache case finishes in well under a second);
	// gifWarmMax is the hard cap so a scene with 404-ing assets still exports.
	gifWarmQuiet = 600 * time.Millisecond
	gifWarmMax   = 6 * time.Second
)

// gifExportJob is the in-flight render state (allocated only while exporting).
// One job drives either format: GIF accumulates paletted frames for gifenc;
// WebP streams each RGBA frame into the libwebp encoder (so it never holds them).
type gifExportJob struct {
	room     *courtroom.Courtroom
	ct       *render.CaptureTarget
	frames   []*image.Paletted // GIF only — every frame, for gifenc.EncodeGIF
	events   []recEvent
	idx      int
	name     string
	captured int // frames captured (both formats) — drives the cap + progress

	asWebP bool             // export an animated WebP instead of a GIF
	webp   *webpenc.Encoder // WebP only — streams + compresses frames as they arrive

	// Conversation chatbox raster, rebuilt per message and revealed rune-by-rune
	// (so the GIF shows people talking). Self-cached here, never the live a.msRaster.
	chatRaster *render.MessageRaster
	chatText   string

	// Asset pre-warm phase: prefetch the scene's sprites/backgrounds and wait for
	// T1 residency before the first capture, so characters are on stage.
	warming      bool
	warmRefs     []courtroom.AssetRef
	warmStarted  time.Time
	warmLastGain time.Time
	warmBest     int // most assets seen resident so far (quiescence detector)
	warmResident int // resident count from the last warm tick (for the overlay)
}

// startSceneExport spins up the offscreen render of scene into an animated GIF
// (asWebP false) or an animated WebP (asWebP true). Must be called on the render
// thread (it creates the capture target).
func (a *App) startSceneExport(scene *sceneRecording, name string, asWebP bool) {
	if a.gifExporting {
		return
	}
	if scene == nil || len(scene.Events) == 0 {
		a.warnLine = "Add at least one line before exporting."
		a.warnAt = time.Now()
		return
	}
	if asWebP && !webpenc.Available() {
		a.warnLine = "Animated WebP isn't available in this build — use 🎞 GIF."
		a.warnAt = time.Now()
		return
	}
	ct, err := render.NewCaptureTarget(a.ctx.Ren, gifExportW, gifExportH)
	if err != nil {
		a.warnLine = "Export unavailable: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	var enc *webpenc.Encoder
	if asWebP {
		if enc, err = webpenc.New(gifExportW, gifExportH, webpQuality, webpFrameMs); err != nil {
			ct.Close()
			a.warnLine = "WebP export unavailable: " + err.Error()
			a.warnAt = time.Now()
			return
		}
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

	// Pre-warm: enumerate every sprite / background / desk the scene needs and
	// prefetch them at high priority, then (in tickGifWarm) wait for them to
	// decode into T1 before capturing — otherwise the fast export outruns the
	// async fetch and renders an empty stage. Music refs (Exact) aren't textures.
	urls := courtroom.NewURLBuilder(scene.Origin)
	evs := make([]courtroom.Event, 0, len(scene.Events))
	for _, re := range scene.Events {
		evs = append(evs, eventFromRec(re))
	}
	var warmRefs []courtroom.AssetRef
	for _, r := range courtroom.SceneAssets(urls, scene.StartBg, evs) {
		if r.Exact {
			continue
		}
		warmRefs = append(warmRefs, r)
		if r.Alt != "" {
			a.d.Manager.PrefetchWithFallback(r.Base, r.Alt, r.Type, network.PriorityHigh) // AssetType: from SceneAssets
		} else {
			a.d.Manager.Prefetch(r.Base, r.Type, network.PriorityHigh) // AssetType: from SceneAssets
		}
	}

	now := time.Now()
	a.gif = &gifExportJob{
		room:         room,
		ct:           ct,
		events:       append([]recEvent(nil), scene.Events...),
		name:         sanitizeStem(name),
		asWebP:       asWebP,
		webp:         enc,
		warming:      true,
		warmRefs:     warmRefs,
		warmStarted:  now,
		warmLastGain: now,
	}
	a.gifExporting = true
	a.warnLine = "Loading scene assets…"
	a.warnAt = now
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
	if j.warming {
		a.tickGifWarm(j) // wait for the scene's sprites to decode before capturing
		return
	}
	for n := 0; n < gifFramesPerTick; n++ {
		if j.captured >= maxGifFrames {
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
			a.drawGifChatbox(j, &j.room.Scene, dst) // composite the conversation over the scene
		})
		if err != nil {
			a.pushDebug("scene export capture: " + err.Error())
			a.finishGifExport()
			return
		}
		if j.asWebP {
			// Stream into the WebP encoder (it compresses + drops the RGBA now, so
			// memory stays flat); GIF keeps the quantized frame for the final encode.
			if err := j.webp.AddFrame(img); err != nil {
				a.pushDebug("webp add: " + err.Error())
				a.finishGifExport()
				return
			}
		} else {
			j.frames = append(j.frames, gifenc.Quantize(img))
		}
		j.captured++
	}
}

// tickGifWarm runs during the pre-warm phase: it counts how many of the scene's
// assets are resident in T1 (the upload Pump fills it each frame) and ends the
// wait once they're all ready, OR no new asset has landed for gifWarmQuiet (the
// rest are 404-ing or evicted), OR gifWarmMax elapses. Then capture begins.
func (a *App) tickGifWarm(j *gifExportJob) {
	now := time.Now()
	resident := 0
	for _, r := range j.warmRefs {
		if a.d.Store.Contains(r.Base) {
			resident++
		}
	}
	j.warmResident = resident
	if resident > j.warmBest {
		j.warmBest = resident
		j.warmLastGain = now
	}
	allReady := len(j.warmRefs) > 0 && resident >= len(j.warmRefs)
	quiesced := resident > 0 && now.Sub(j.warmLastGain) > gifWarmQuiet
	timedOut := now.Sub(j.warmStarted) > gifWarmMax
	if allReady || quiesced || timedOut || len(j.warmRefs) == 0 {
		j.warming = false
		a.warnLine = "Rendering GIF…"
		a.warnAt = now
	}
}

// drawGifChatbox composites the conversation chatbox — speaker name + the message
// typed out to the current rune — over the captured scene, so an exported GIF
// shows people talking rather than a silent stage. It mirrors the live chatbox's
// flat-panel look and reuses renderRaster (same colours / inline markup), but is
// INPUT-FREE (no wheel-zoom side effect) and self-caches its raster on the export
// job, never touching the live a.msRaster or the render hot path. Render thread
// only (called inside the capture's draw callback).
// gifChatNameRowH is the speaker-name row above the message text in the export
// chatbox; the message is drawn this far below the box top.
const gifChatNameRowH = 24

// gifChatboxHeight sizes the export chatbox to FIT the rasterized message (height
// textH) within the fixed capture frame (height vpH): name row + text + padding,
// floored so a one-word line still gets a real panel and capped at 3/5 of the
// frame so a very long message can't swallow the whole picture (it then clips
// INSIDE the box, never off the bottom edge of the frame — the reported bug).
func gifChatboxHeight(textH, vpH int32) int32 {
	h := gifChatNameRowH + textH + 10
	if minH := vpH / 5; h < minH {
		h = minH
	}
	if maxH := vpH * 3 / 5; h > maxH {
		h = maxH
	}
	return h
}

func (a *App) drawGifChatbox(j *gifExportJob, sc *courtroom.Scene, vp sdl.Rect) {
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	c := a.ctx
	wrapW := vp.W - 16
	// Rasterize FIRST, so the box can be sized to FIT the message. The capture is a
	// small fixed frame (480×360), so a live-proportioned box (¼ of the viewport)
	// is too short and clips a multi-line message off the bottom EDGE of the frame
	// — which is exactly the bug. Rebuilt only when the line changes.
	if j.chatRaster == nil || j.chatText != sc.MessageText {
		if j.chatRaster != nil {
			j.chatRaster.Destroy()
			j.chatRaster = nil
		}
		if sc.MessageText != "" {
			if r, err := renderRaster(a, sc, wrapW, false); err == nil {
				j.chatRaster = r
			}
		}
		j.chatText = sc.MessageText
	}

	textH := int32(0)
	if j.chatRaster != nil {
		textH = j.chatRaster.Height()
	}
	boxH := gifChatboxHeight(textH, vp.H)
	box := sdl.Rect{X: vp.X, Y: vp.Y + vp.H - boxH, W: vp.W, H: boxH}
	c.Fill(box, sdl.Color{R: 16, G: 16, B: 24, A: 215})
	c.Border(box, ColAccent)

	nameCol := ColAccent
	if a.d.Prefs.NameColorsOn() { // per-speaker name colour, same as the live chatbox
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.NameColorSat())/100, float64(a.d.Prefs.NameColorVal())/100)
	}
	c.Label(box.X+8, box.Y+4, sc.ShownameText, nameCol)

	if j.chatRaster != nil {
		_ = c.Ren.SetClipRect(&box) // the cap case stays inside the box, never off-frame
		j.chatRaster.Draw(c.Ren, sc.VisibleRunes, box.X+8, box.Y+gifChatNameRowH)
		_ = c.Ren.SetClipRect(nil)
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
	if j.chatRaster != nil {
		j.chatRaster.Destroy()
		j.chatRaster = nil
	}
	a.endBundledReplay() // drop the archive source if the export was from a bundled archive
	a.restoreViewportPreanim()
	a.makerPreviewIdx = -1 // the export drove the shared viewport — force the preview pane to rebuild

	stem := j.name + "-" + time.Now().Format("20060102-150405")
	if j.asWebP {
		a.finishWebpExport(j, stem)
		return
	}
	if len(j.frames) == 0 {
		a.warnLine = "GIF export: nothing rendered (set an Origin/CDN and add a line)."
		a.warnAt = time.Now()
		return
	}
	frames := j.frames
	a.warnLine = fmt.Sprintf("Encoding GIF (%d frames)…", len(frames))
	a.warnAt = time.Now()
	go func() { a.gifResultCh <- encodeAndWriteGIF(frames, stem) }()
}

// finishWebpExport assembles the streamed WebP frames and writes the file. The
// per-frame compression already happened during capture, so assembling + writing
// runs off-thread (the encoder is owned solely by this goroutine now — a.gif is
// already nil — so the cross-goroutine handoff is safe).
func (a *App) finishWebpExport(j *gifExportJob, stem string) {
	if j.webp == nil {
		return
	}
	if j.captured == 0 {
		j.webp.Close()
		a.warnLine = "WebP export: nothing rendered (set an Origin/CDN and add a line)."
		a.warnAt = time.Now()
		return
	}
	enc := j.webp
	a.warnLine = fmt.Sprintf("Encoding WebP (%d frames)…", j.captured)
	a.warnAt = time.Now()
	go func() {
		data, err := enc.Assemble()
		enc.Close()
		if err != nil {
			a.gifResultCh <- "WebP export failed: " + err.Error()
			return
		}
		a.gifResultCh <- writeWebp(data, stem)
	}()
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

// writeWebp (off-thread) writes the assembled animated WebP to recordings\<stem>.webp.
func writeWebp(data []byte, stem string) string {
	dir := recordingsDir()
	if dir == "" {
		return "WebP export failed: no recordings folder."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "WebP export failed: " + err.Error()
	}
	name := stem + ".webp"
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "WebP export failed: " + err.Error()
	}
	return fmt.Sprintf("WebP saved: recordings\\%s (%.2f MB).", name, float64(len(data))/(1024*1024))
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
	cx := winW / 2
	cy := winH / 2

	// Pre-warm phase: loading the scene's sprites before the first capture.
	if j != nil && j.warming {
		total := len(j.warmRefs)
		c.Label(cx-120, cy-30, "🎞  Loading scene assets…", ColText)
		c.Label(cx-120, cy, fmt.Sprintf("%d / %d ready", j.warmResident, total), ColTextDim)
		bar := sdl.Rect{X: cx - 160, Y: cy + 26, W: 320, H: 14}
		c.Fill(bar, ColPanel)
		if total > 0 {
			fillW := int32(j.warmResident) * bar.W / int32(total)
			c.Fill(sdl.Rect{X: bar.X, Y: bar.Y, W: fillW, H: bar.H}, ColAccent)
		}
		if c.Button(sdl.Rect{X: cx - 60, Y: cy + 54, W: 120, H: btnH}, "▶ Start now") {
			j.warming = false // skip the wait; capture with whatever has loaded
		}
		return
	}

	done := 0
	label := "🎞  Rendering GIF…"
	if j != nil {
		done = j.captured
		if j.asWebP {
			label = "🎬  Rendering WebP…"
		}
	}
	pct := done * 100 / maxGifFrames
	c.Label(cx-120, cy-30, label, ColText)
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
