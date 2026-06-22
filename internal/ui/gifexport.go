package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/gifenc"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/videoenc"
	"github.com/SyntaxNyah/AsyncAO/internal/webpenc"
)

// exportChatFontDivisor sizes the export chatbox font: at 100% TextScale the font
// is ≈ vp.H / 18 px (= this divisor / UIFontSize), so text fits ~the same number
// of characters per line at any export size — and, crucially, the export font is
// derived from the CAPTURE size, NOT the live chat zoom (a.chatPct), so a user who
// zoomed their live chat doesn't get giant, clipped text in the small GIF.
const exportChatFontDivisor = 252 // ≈ 18 text rows × UIFontSize(14)

// exportChatPct maps the capture height + the user's TextScale to a ChatFontFor
// percent, clamped to the chat-scale range.
func exportChatPct(vpH int32, textScale int) int {
	if textScale <= 0 {
		textScale = 100
	}
	pct := int(vpH) * textScale / exportChatFontDivisor
	if pct < config.MinChatScalePercent {
		pct = config.MinChatScalePercent
	}
	if pct > config.MaxChatScalePercent {
		pct = config.MaxChatScalePercent
	}
	return pct
}

// sceneExportFromPath loads a recording and exports it straight to a GIF (the
// Studio "🎞 GIF" button), an animated WebP ("🎬 WebP"), or an MP4/WebM video
// ("🎥 Video") — no trip through the maker. A bundled archive renders from its
// own folder (the archive source is dropped when the export finishes).
func (a *App) sceneExportFromPath(path string, kind exportKind) {
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
	a.startSceneExport(rec, strings.TrimSuffix(filepath.Base(path), recordingExt), kind)
}

// Scene GIF export (M16): render a scene through a throwaway replay room into a
// fixed offscreen target (render.CaptureTarget), quantize each frame to a
// paletted image (so the RGBA is freed immediately), and encode an animated GIF
// (gifenc). It runs INCREMENTALLY — a small batch of frames per frame-loop tick
// on the render thread (SDL capture must be on-thread) behind a progress overlay
// — so the window stays responsive and nothing blocks. Off by default; zero cost
// on the live render path when not exporting.
const (
	gifExportH       = 360 // default output height (4:3); config ExportOptions overrides
	gifExportW       = gifExportH * 4 / 3
	maxGifFrames     = 400 // absolute frame cap (§17.4) + the memory-budget basis
	minExportFrames  = 60  // floor so even the largest size still gets a few seconds
	gifFramesPerTick = 4   // frames captured per real frame (responsive + progress)

	// gifFrameBudgetBytes bounds GIF memory: every paletted frame is 1 byte/px and
	// all are held for the final encode, so 480×360×400 ≈ 69 MB is the budget. A
	// bigger output keeps that budget by capping at proportionally fewer frames
	// (exportMaxFrames), so resolution can't blow the 256 MiB envelope.
	gifFrameBudgetBytes = gifExportW * gifExportH * maxGifFrames

	// Asset pre-warm (so the GIF shows the characters, not an empty stage): the
	// export advances ~4× faster than a replay and outruns async sprite fetches,
	// so we prefetch every scene asset and wait for them to decode before
	// capturing. gifWarmQuiet ends the wait once no new asset has landed for a
	// beat (the common, warm-cache case finishes in well under a second);
	// gifWarmMax is the hard cap so a scene with 404-ing assets still exports.
	gifWarmQuiet = 600 * time.Millisecond
	gifWarmMax   = 6 * time.Second
)

// exportKind selects the output format one gifExportJob produces. GIF and WebP
// share the held/streamed encoders already here; Video streams raw frames into a
// system ffmpeg (internal/videoenc).
type exportKind int

const (
	exportGIF   exportKind = iota // animated GIF (paletted, every frame held for gifenc)
	exportWebP                    // animated WebP (libwebp streams + compresses each frame)
	exportVideo                   // MP4/WebM via a system ffmpeg (videoenc streams frames)
	exportComic                   // a single PNG storyboard: one still panel per IC line (pure Go)
)

// maxVideoFrames caps a single video export. Unlike the GIF cap (bounded by the
// HELD paletted-frame memory budget), video frames are streamed straight to
// ffmpeg and never retained, so this isn't a memory bound — it's a runaway /
// file-size guard so a stuck scene can't encode forever. 18000 frames is 15 min
// at 20 fps (12.5 min at 24 fps) — far longer than anyone exports in one go.
const maxVideoFrames = 18000

// Export size / frame-rate dropdown presets (4:3). Bounds mirror the config
// clamp — SetExportOpts is authoritative — so a pick is always in range.
var (
	exportHeightPresets = []int{288, 360, 480, 540}
	exportHeightLabels  = []string{"Small · 384×288", "Medium · 480×360", "Large · 640×480", "XL · 720×540"}
	exportFPSPresets    = []int{8, 12, 15, 24}
	exportFPSLabels     = []string{"8 fps · smallest", "12 fps", "15 fps", "24 fps · smoothest"}

	// Video container/codec choices for the 🎥 Video button. Index ↔ the persisted
	// VideoFormat string; MP4/H.264 first (the play-everywhere default).
	videoFormatValues = []string{"mp4", "webm"}
	videoFormatLabels = []string{"MP4 · H.264 (plays everywhere)", "WebM · VP9 (smaller, open)"}
)

// videoFormatIdx maps a persisted VideoFormat to its dropdown row.
func videoFormatIdx(s string) int {
	if strings.EqualFold(s, "webm") {
		return 1
	}
	return 0
}

// nearestPresetIdx returns the index of the preset value closest to v (so a
// hand-edited or legacy pref lands on a sensible dropdown row).
func nearestPresetIdx(presets []int, v int) int {
	best, bestD := 0, 1<<30
	for i, p := range presets {
		d := p - v
		if d < 0 {
			d = -d
		}
		if d < bestD {
			bestD, best = d, i
		}
	}
	return best
}

// drawExportOptions draws the shared GIF/WebP export controls — size, frame rate,
// WebP quality, loop, and (when withSpeed) playback speed — at the pad column,
// persisting each change immediately. Returns the y below the panel. Shared by
// Settings → Studio and the in-maker "⚙ Export options" panel so they can't
// drift. withSpeed is off in Settings (its Replay-playback section owns the
// slider) and on in the maker (so the speed is editable while building).
func (a *App) drawExportOptions(y int32, withSpeed bool) int32 {
	c := a.ctx
	opts := a.d.Prefs.ExportOpts()
	fx := int32(pad + 130)

	c.Label(pad, y+5, "Size:", ColText)
	if next, changed := c.Dropdown("exp_size", sdl.Rect{X: fx, Y: y, W: 180, H: fieldH}, exportHeightLabels, nearestPresetIdx(exportHeightPresets, opts.HeightPx)); changed {
		opts.HeightPx = exportHeightPresets[next]
		a.d.Prefs.SetExportOpts(opts)
	}
	hh := int32(opts.HeightPx)
	maxSecs := exportMaxFrames(hh*4/3, hh) / max(1, opts.FPS)
	c.Label(fx+190, y+5, fmt.Sprintf("GIF up to ~%ds (bigger = shorter); WebP runs longer", maxSecs), ColTextDim)
	y += 32

	c.Label(pad, y+5, "Frame rate:", ColText)
	if next, changed := c.Dropdown("exp_fps", sdl.Rect{X: fx, Y: y, W: 180, H: fieldH}, exportFPSLabels, nearestPresetIdx(exportFPSPresets, opts.FPS)); changed {
		opts.FPS = exportFPSPresets[next]
		a.d.Prefs.SetExportOpts(opts)
	}
	c.Label(fx+190, y+5, "higher = smoother but bigger", ColTextDim)
	y += 32

	c.Label(pad, y+5, "Video format:", ColText)
	if next, changed := c.Dropdown("exp_vfmt", sdl.Rect{X: fx, Y: y, W: 180, H: fieldH}, videoFormatLabels, videoFormatIdx(opts.VideoFormat)); changed {
		opts.VideoFormat = videoFormatValues[next]
		a.d.Prefs.SetExportOpts(opts)
	}
	if videoenc.Available() {
		c.Label(fx+190, y+5, "for the 🎥 Video button", ColTextDim)
	} else {
		c.Label(fx+190, y+5, "🎥 Video needs ffmpeg on PATH — GIF/WebP work without it", ColDanger)
	}
	y += 32

	// Quality drives both the WebP encoder and the video CRF (higher = better/bigger).
	if next := a.sliderRow(y, "  Quality % (WebP / video)", opts.Quality, 5, 20, 100); next != opts.Quality {
		opts.Quality = next
		a.d.Prefs.SetExportOpts(opts)
	}
	y += 30

	// Chat text size in the export (fitted to the output size at 100%, so long
	// lines fit; independent of the live chat zoom). Bounds mirror the config clamp.
	if next := a.sliderRow(y, "  Text size %", opts.TextScale, 10, 50, 200); next != opts.TextScale {
		opts.TextScale = next
		a.d.Prefs.SetExportOpts(opts)
	}
	y += 30

	if next := c.Checkbox(pad, y, "Loop the animation forever (off = play once)", opts.Loop); next != opts.Loop {
		opts.Loop = next
		a.d.Prefs.SetExportOpts(opts)
	}
	y += 28

	// Playback speed is shared with the replay player (it drives the export's
	// pacing too) — surfaced here so the whole "look" is set in one place.
	if withSpeed {
		spd := a.d.Prefs.ReplaySpeed()
		if next := a.sliderRow(y, "  Playback speed %", spd, 5, 25, 200); next != spd {
			a.d.Prefs.SetReplaySpeed(next)
		}
		y += 30
	}
	return y
}

// exportMaxFrames is the frame cap for a w×h export: bounded by the paletted-frame
// memory budget (so a large GIF can't blow the budget), floored so even the
// largest size still gets a few seconds, and never above the absolute cap.
func exportMaxFrames(w, h int32) int {
	if w <= 0 || h <= 0 {
		return maxGifFrames
	}
	n := gifFrameBudgetBytes / (int(w) * int(h))
	if n < minExportFrames {
		n = minExportFrames
	}
	if n > maxGifFrames {
		n = maxGifFrames
	}
	return n
}

// gifExportJob is the in-flight render state (allocated only while exporting).
// One job drives either format: GIF accumulates paletted frames for gifenc;
// WebP streams each RGBA frame into the libwebp encoder (so it never holds them).
type gifExportJob struct {
	room     *courtroom.Courtroom
	ct       *render.CaptureTarget
	frames   []*image.Paletted // GIF only — every frame, for gifenc.EncodeGIF
	panels   []*image.RGBA     // Comic only — one still per IC line, composed into a PNG page at the end
	events   []recEvent
	idx      int
	name     string
	captured int // frames captured (both formats) — drives the cap + progress

	kind exportKind       // which format this job produces (GIF / WebP / Video)
	webp *webpenc.Encoder // WebP only — streams + compresses frames as they arrive

	// Video only — a system ffmpeg streams frames to vidPath as they arrive (so,
	// like WebP, no frames are held). vidPath is resolved up front because ffmpeg
	// owns the file the whole time it runs.
	vid       *videoenc.Encoder
	vidPath   string
	vidFormat videoenc.Format // Video only — codec choice for the post-capture audio mux
	musicCap  *musicCapture   // Video only — records the scene's music for the audio mux (#99)

	// Per-export output settings (from config ExportOptions, resolved once).
	w, h      int32         // capture/output size (4:3)
	frameDt   time.Duration // room Update step per captured frame (= 1/fps)
	delayCs   int           // GIF per-frame delay, centiseconds
	loop      bool          // loop the animation forever vs play once
	maxFrames int           // frame cap for this size (memory-budgeted)

	// Conversation chatbox raster, rebuilt per message and revealed rune-by-rune
	// (so the GIF shows people talking). Self-cached here, never the live a.msRaster.
	chatRaster *render.MessageRaster
	chatText   string
	chatPct    int // export chat-font scale (fitted to the capture size, not live zoom)

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
// (exportGIF), an animated WebP (exportWebP), or an MP4/WebM video (exportVideo).
// Must be called on the render thread (it creates the capture target).
func (a *App) startSceneExport(scene *sceneRecording, name string, kind exportKind) {
	if a.gifExporting {
		return
	}
	if scene == nil || len(scene.Events) == 0 {
		a.warnLine = "Add at least one line before exporting."
		a.warnAt = time.Now()
		return
	}
	if kind == exportWebP && !webpenc.Available() {
		a.warnLine = "Animated WebP isn't available in this build — use 🎞 GIF."
		a.warnAt = time.Now()
		return
	}
	if kind == exportVideo && !videoenc.Available() {
		// Runtime-optional: the app runs fine without ffmpeg; only this action needs it.
		a.warnLine = "Video export needs ffmpeg — install it and add it to PATH (the app works fine without it; GIF/WebP still export)."
		a.warnAt = time.Now()
		return
	}
	// Resolve the sticky export options into this job's size/cadence.
	opts := a.d.Prefs.ExportOpts()
	h := int32(opts.HeightPx)
	w := h * 4 / 3
	// A comic page renders fixed, small panels (one per line) regardless of the
	// animation Size knob — predictable page dimensions and a bounded held-RGBA
	// budget (maxComicPanels panels at this size).
	if kind == exportComic {
		w, h = comicPanelW, comicPanelH
	}
	fps := opts.FPS
	if fps < 1 {
		fps = 1
	}
	frameDt := time.Second / time.Duration(fps)
	delayCs := 100 / fps
	if delayCs < 1 {
		delayCs = 1
	}
	frameMs := 1000 / fps
	if frameMs < 1 {
		frameMs = 1
	}
	// Video streams to ffmpeg and never holds frames, so it isn't bound by the GIF
	// paletted-frame budget — let scene length drive it (capped against runaway).
	maxFrames := exportMaxFrames(w, h)
	if kind == exportVideo {
		maxFrames = maxVideoFrames
	}
	if kind == exportComic {
		maxFrames = maxComicPanels // one page's worth of panels (the cap bounds the held RGBA)
	}

	ct, err := render.NewCaptureTarget(a.ctx.Ren, w, h)
	if err != nil {
		a.warnLine = "Export unavailable: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	var enc *webpenc.Encoder
	var vid *videoenc.Encoder
	var vidPath string
	var vidFormat videoenc.Format
	switch kind {
	case exportWebP:
		if enc, err = webpenc.New(int(w), int(h), opts.Quality, frameMs, opts.Loop); err != nil {
			ct.Close()
			a.warnLine = "WebP export unavailable: " + err.Error()
			a.warnAt = time.Now()
			return
		}
	case exportVideo:
		vidFormat = videoenc.FormatFromString(opts.VideoFormat)
		vidPath, err = videoExportPath(sanitizeStem(name), vidFormat)
		if err != nil {
			ct.Close()
			a.warnLine = "Video export unavailable: " + err.Error()
			a.warnAt = time.Now()
			return
		}
		if vid, err = videoenc.New(vidPath, int(w), int(h), fps, opts.Quality, vidFormat); err != nil {
			ct.Close()
			a.warnLine = "Video export unavailable: " + err.Error()
			a.warnAt = time.Now()
			return
		}
	}
	// Audio sink per kind: GIF/WebP keep real audio (paced by their small per-frame
	// dt). A comic is a silent still page whose fast-forward would blast the whole
	// line's blips at once + rip through music, so it gets a no-op sink. A video is
	// silent at capture (export speed garbles live audio), but a musicCapture sink
	// records the scene's song so finishVideoExport can mux it in afterward (#99).
	var audio courtroom.AudioSink = a.d.Audio
	var musicCap *musicCapture
	switch kind {
	case exportComic:
		audio = courtroom.NopAudio{}
	case exportVideo:
		musicCap = &musicCapture{frameRef: func() int {
			if a.gif != nil {
				return a.gif.captured
			}
			return 0
		}}
		audio = musicCap
	}
	room := courtroom.NewCourtroom(courtroom.NewURLBuilder(scene.Origin), a.d.Manager, nil, audio)
	room.Typewriter.Interval, room.TextStay = a.replayTiming()
	room.CatchUp = false
	room.ReduceMotion = false // export the authored effects (screenshake/flash), not the viewer's accessibility pref
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
		kind:         kind,
		webp:         enc,
		vid:          vid,
		vidPath:      vidPath,
		vidFormat:    vidFormat,
		musicCap:     musicCap,
		w:            w,
		h:            h,
		frameDt:      frameDt,
		delayCs:      delayCs,
		loop:         opts.Loop,
		maxFrames:    maxFrames,
		chatPct:      exportChatPct(h, opts.TextScale), // font fitted to the capture, not the live zoom
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
	if j.kind == exportComic {
		a.tickComicExport(j) // one still panel per IC line, composed into a PNG page at the end
		return
	}
	for n := 0; n < gifFramesPerTick; n++ {
		if j.captured >= j.maxFrames {
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
		j.room.Update(j.frameDt)
		a.d.Viewport.SetSpriteFX(a.spriteFX())
		a.d.Viewport.Update(&j.room.Scene, j.frameDt)
		img, err := j.ct.Capture(a.ctx.Ren, func(dst sdl.Rect) {
			a.d.Viewport.Render(a.ctx.Ren, &j.room.Scene, dst)
			a.drawGifChatbox(j, &j.room.Scene, dst) // composite the conversation over the scene
		})
		if err != nil {
			a.pushDebug("scene export capture: " + err.Error())
			a.finishGifExport()
			return
		}
		switch j.kind {
		case exportWebP:
			// Stream into the WebP encoder (it compresses + drops the RGBA now, so
			// memory stays flat); GIF keeps the quantized frame for the final encode.
			if err := j.webp.AddFrame(img); err != nil {
				a.pushDebug("webp add: " + err.Error())
				a.finishGifExport()
				return
			}
		case exportVideo:
			// Stream the raw frame into ffmpeg (synchronous; the export owns the
			// window). A broken pipe means ffmpeg died — stop; finishVideoExport
			// reaps the process and surfaces the stderr tail as the result.
			if err := j.vid.AddFrame(img); err != nil {
				a.pushDebug("video add: " + err.Error())
				a.finishGifExport()
				return
			}
		default: // exportGIF
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
		a.warnLine = "Rendering…" // kind-neutral; the progress overlay names the format
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

// fitChatRaster rasterizes the message at the export font scale (pct), shrinking
// the font if the whole message would overflow the tallest the box can be (3/5 of
// the frame), so a full ~256-char line always fits instead of clipping — "fit it
// no matter what". Converges fast (fewer wrapped lines as the font drops); bounded
// iterations, floored at the min chat scale. Render thread only.
func (a *App) fitChatRaster(sc *courtroom.Scene, wrapW, vpH int32, pct int) *render.MessageRaster {
	maxTextH := vpH*3/5 - gifChatNameRowH - 10
	if maxTextH < gifChatNameRowH {
		maxTextH = gifChatNameRowH
	}
	for attempt := 0; attempt < 5; attempt++ {
		r, err := renderRaster(a, sc, wrapW, false, pct)
		if err != nil {
			return nil
		}
		if h := r.Height(); h <= maxTextH || pct <= config.MinChatScalePercent {
			return r // fits (or already at the smallest font)
		} else {
			next := pct * int(maxTextH) / int(h) // proportional shrink toward fitting
			if next >= pct {
				next = pct - 10 // guarantee progress
			}
			if next < config.MinChatScalePercent {
				next = config.MinChatScalePercent
			}
			r.Destroy()
			pct = next
		}
	}
	r, _ := renderRaster(a, sc, wrapW, false, config.MinChatScalePercent) // safety: show text at the floor
	return r
}

func (a *App) drawGifChatbox(j *gifExportJob, sc *courtroom.Scene, vp sdl.Rect) {
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	c := a.ctx
	wrapW := vp.W - 16
	// Rasterize FIRST, so the box can be sized to FIT the message. The capture is a
	// small fixed frame, so a live-proportioned box would clip a multi-line message
	// off the bottom edge. Rebuilt only when the line changes; the font shrinks (if
	// needed) so even a full ~256-char line fits the box rather than clipping.
	if j.chatRaster == nil || j.chatText != sc.MessageText {
		if j.chatRaster != nil {
			j.chatRaster.Destroy()
			j.chatRaster = nil
		}
		if sc.MessageText != "" {
			j.chatRaster = a.fitChatRaster(sc, wrapW, vp.H, j.chatPct)
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

	if j.kind == exportVideo {
		a.finishVideoExport(j) // ffmpeg has been writing vidPath all along — just flush + wait
		return
	}
	if j.kind == exportComic {
		// Sits AFTER the shared teardown above (ct.Close / chatRaster.Destroy /
		// restoreViewportPreanim) on purpose: an early return would leak the capture
		// target and leave the viewport's preanim callback dangling at the dead comic
		// room, corrupting the next LIVE message. The captured panels are CPU copies,
		// so closing the target first is fine.
		a.finishComicExport(j)
		return
	}
	stem := j.name + "-" + time.Now().Format("20060102-150405")
	if j.kind == exportWebP {
		a.finishWebpExport(j, stem)
		return
	}
	if len(j.frames) == 0 {
		a.warnLine = "GIF export: nothing rendered (set an Origin/CDN and add a line)."
		a.warnAt = time.Now()
		return
	}
	frames := j.frames
	delayCs, loop := j.delayCs, j.loop
	a.warnLine = fmt.Sprintf("Encoding GIF (%d frames)…", len(frames))
	a.warnAt = time.Now()
	go func() { a.gifResultCh <- encodeAndWriteGIF(frames, stem, delayCs, loop) }()
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

// finishVideoExport closes ffmpeg's stdin and waits for the encode to finalize
// off-thread — ffmpeg flushes + writes the moov atom on EOF, which can take a
// beat (mirrors finishWebpExport's off-thread Assemble). ffmpeg has been writing
// vidPath the whole capture, so there's nothing to assemble; we just wait and
// report. The encoder is owned solely by this goroutine now (a.gif is already
// nil), so the handoff is race-free.
func (a *App) finishVideoExport(j *gifExportJob) {
	if j.vid == nil {
		return
	}
	enc, path := j.vid, j.vidPath
	if j.captured == 0 {
		enc.Close()
		_ = os.Remove(path) // drop the empty container ffmpeg may have created
		a.warnLine = "Video export: nothing rendered (set an Origin/CDN and add a line)."
		a.warnAt = time.Now()
		return
	}
	// Resolve the music bed NOW, on the render thread — the capture sink's cues are
	// complete and a.gif is about to be cleared. frameMs converts the song's start
	// frame to ms. The mux itself (download + 2nd ffmpeg) runs off-thread below; on
	// ANY failure it degrades to the silent video that was just written (#99).
	var songURL string
	var delayMs int
	if j.musicCap != nil {
		frameMs := int(j.frameDt / time.Millisecond)
		if frameMs < 1 {
			frameMs = 1
		}
		songURL, delayMs, _ = j.musicCap.firstSong(frameMs)
	}
	format := j.vidFormat
	a.warnLine = fmt.Sprintf("Finishing video (%d frames)…", j.captured)
	a.warnAt = time.Now()
	go func() {
		if err := enc.Finish(); err != nil {
			_ = os.Remove(path) // a failed encode leaves a corrupt file — don't keep it
			a.gifResultCh <- "Video export failed: " + err.Error()
			return
		}
		if songURL != "" {
			if finalPath, ok := muxMusicBed(path, songURL, delayMs, format); ok {
				a.gifResultCh <- videoSavedMsg(finalPath) + "  ♪ music added"
				return
			}
			a.gifResultCh <- videoSavedMsg(path) + "  (couldn't add the music — saved silent)"
			return
		}
		a.gifResultCh <- videoSavedMsg(path)
	}()
}

// videoExportPath resolves recordings\<stem>-<timestamp>.<ext> (creating the
// folder). The path must exist up front because ffmpeg owns the file for the
// whole encode, unlike GIF/WebP which write only at the end.
func videoExportPath(stem string, format videoenc.Format) (string, error) {
	dir := recordingsDir()
	if dir == "" {
		return "", fmt.Errorf("no recordings folder")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := stem + "-" + time.Now().Format("20060102-150405") + "." + videoenc.FormatExt(format)
	return filepath.Join(dir, name), nil
}

// videoSavedMsg reports a finished video with its on-disk size.
func videoSavedMsg(path string) string {
	name := filepath.Base(path)
	if st, err := os.Stat(path); err == nil {
		return fmt.Sprintf("Video saved: recordings\\%s (%.2f MB).", name, float64(st.Size())/(1024*1024))
	}
	return "Video saved: recordings\\" + name + "."
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
func encodeAndWriteGIF(frames []*image.Paletted, stem string, delayCs int, loop bool) string {
	data, err := gifenc.EncodeGIF(frames, delayCs, loop)
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

	done, label := 0, "🎞  Rendering GIF…"
	if j != nil {
		done = j.captured
		switch j.kind {
		case exportWebP:
			label = "🎬  Rendering WebP…"
		case exportVideo:
			label = "🎥  Rendering video…"
		case exportComic:
			label = "🖼  Rendering comic…"
		}
	}
	c.Label(cx-120, cy-30, label, ColText)

	// Progress fraction: GIF/WebP are bounded by the (memory-budgeted) frame cap,
	// so their bar is frames/cap. Video streams and almost always finishes by
	// exhausting the script long before the runaway cap, so its bar tracks how far
	// through the events we are — a real 0–100%, not a sliver of the huge cap.
	var frac float64
	var sub string
	if j != nil && (j.kind == exportVideo || j.kind == exportComic) {
		// Both stream the script rather than fill a memory-budgeted frame cap, so the
		// bar tracks how far through the events we are — a real 0–100%.
		if n := len(j.events); n > 0 {
			frac = float64(j.idx) / float64(n)
		}
		if j.kind == exportComic {
			sub = fmt.Sprintf("%d panels (%d%% of the script)", done, int(frac*100))
		} else {
			sub = fmt.Sprintf("%d frames written (%d%% of the script)", done, int(frac*100))
		}
	} else {
		capFrames := maxGifFrames
		if j != nil && j.maxFrames > 0 {
			capFrames = j.maxFrames
		}
		frac = float64(done) / float64(capFrames)
		sub = fmt.Sprintf("%d frames captured (%d%% of the cap)", done, int(frac*100))
	}
	c.Label(cx-120, cy, sub, ColTextDim)

	bar := sdl.Rect{X: cx - 160, Y: cy + 26, W: 320, H: 14}
	c.Fill(bar, ColPanel)
	fillW := int32(frac * float64(bar.W))
	if fillW > bar.W {
		fillW = bar.W
	}
	if fillW < 0 {
		fillW = 0
	}
	c.Fill(sdl.Rect{X: bar.X, Y: bar.Y, W: fillW, H: bar.H}, ColAccent)
	if c.Button(sdl.Rect{X: cx - 60, Y: cy + 54, W: 120, H: btnH}, "■ Stop & save") {
		a.finishGifExport() // keep what's been captured so far
	}
}
