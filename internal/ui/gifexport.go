package ui

import (
	"fmt"
	"image"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/gifenc"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/videoenc"
	"github.com/SyntaxNyah/AsyncAO/internal/webpenc"
)

// exportChatFontDivisor sizes the export chatbox font to about vp.H / 18 px at 100% TextScale, so text fits the same
// number of characters per line at any export size. It derives from the capture size, not the live chat zoom
// (a.chatPct).
const exportChatFontDivisor = 252 // ≈ 18 text rows × UIFontSize(14)

// exportChatPct maps the capture height and the user's TextScale to a ChatFontFor percent, clamped to the chat-scale
// range.
func exportChatPct(vpH int32, textScale int) int {
	if textScale <= 0 {
		textScale = 100
	}
	pct := int(vpH) * textScale / exportChatFontDivisor
	pct = value.Clamp(pct, config.MinChatScalePercent, config.MaxChatScalePercent)
	return pct
}

// sceneExportFromPath begins exporting a recording straight to a GIF, an animated WebP or an MP4/WebM video, without
// the maker. A bundled archive renders from its own folder. The disk read and parse is deferred to one goroutine off
// the render thread; this function only validates, creates the job shell in its loading phase, sets a.gifExporting, and
// spawns the loader. tickGifExport polls the result. The a.gifExporting guard refuses a second export.
func (a *App) sceneExportFromPath(path string, kind exportKind) {
	if a.gifExporting || a.replaying || a.recActive {
		a.warnLine = "Finish the current replay / recording first."
		a.warnAt = time.Now()
		return
	}
	// Resolve the origin an imported .demo streams from now, on the render thread: a.urls is render-thread only and the
	// loader goroutine must not touch App.
	origin := a.demoDefaultOrigin()
	base := filepath.Base(path)
	loadCh := make(chan gifLoadResult, gifLoadBuf)
	a.gif = &gifExportJob{
		loading:  true,
		loadCh:   loadCh,
		loadName: base,
		loadPath: path,
		loadKind: kind,
	}
	a.gifExporting = true
	// The loading overlay draws at the live UI scale; the export scale bracket is set later in startSceneExport.
	a.warnLine = "Reading " + base + "…"
	a.warnAt = time.Now()
	go func() {
		// One bounded goroutine (hard rule 4) doing only disk work and sending once. The channel buffer (cap 1) always
		// accepts, so a Cancel that drops this job cannot leave the goroutine blocked on send.
		loadCh <- loadRecordingForExport(path, origin)
	}()
}

// finishLoadedExport runs the render-thread tail of an async export once the loader delivers a parsed recording: replay
// the import notes, route a bundled archive to its own folder, and hand off to startSceneExport. Render thread only.
func (a *App) finishLoadedExport(res gifLoadResult) {
	kind := a.gif.loadKind
	path := a.gif.loadPath
	// The shell job is replaced by startSceneExport's real job; clear the flags first so its `if a.gifExporting` guard
	// sees a clean state. If startSceneExport refuses (empty scene, no ffmpeg), the flags stay cleared and its warnLine
	// shows.
	a.gif = nil
	a.gifExporting = false
	a.emitLoadNotes(res)
	rec := res.rec
	if rec.Bundled {
		a.beginBundledReplay(rec, filepath.Dir(path)) // archive source + repoint Origin
	}
	stem := strings.TrimSuffix(filepath.Base(path), recordingExt)
	stem = strings.TrimSuffix(stem, demoExt)
	a.startSceneExport(rec, stem, kind)
}

// Scene GIF export: render a scene through a throwaway replay room into a fixed offscreen target
// (render.CaptureTarget), quantize each frame to a paletted image, and encode an animated GIF (gifenc). A small batch
// of frames runs per frame-loop tick on the render thread (SDL capture must be on-thread) behind a progress overlay. It
// costs nothing on the live render path when not exporting.
const (
	gifExportH       = 360 // default output height (4:3); config ExportOptions overrides
	gifExportW       = gifExportH * 4 / 3
	maxGifFrames     = 400 // absolute frame cap (§17.4) + the memory-budget basis
	minExportFrames  = 60  // floor so even the largest size still gets a few seconds
	gifFramesPerTick = 4   // frames captured per real frame (responsive + progress)

	// gifFrameBudgetBytes bounds GIF memory: every paletted frame is 1 byte/px and all are held for the final encode,
	// so larger output caps at proportionally fewer frames (exportMaxFrames).
	gifFrameBudgetBytes = gifExportW * gifExportH * maxGifFrames

	// Asset pre-warm: the export advances about 4x faster than a replay and outruns async sprite fetches, so every
	// scene asset is prefetched and decoded before capture. gifWarmQuiet ends the wait once no new asset has landed for
	// a beat; gifWarmMax is the hard cap so a scene with 404-ing assets still exports.
	gifWarmQuiet = 600 * time.Millisecond
	gifWarmMax   = 6 * time.Second

	// gifWarmHardCap is the absolute ceiling on the warm phase, measured from job creation (gifWarmMax starts only at
	// warmSubmitDone). A submitted ref that 404s, or resolves to another base, never becomes T1-resident and stays in
	// warmInFlight. Enough of them leave room at 0, so pendingWarm never drains, warmSubmitDone never latches and
	// gifWarmMax never starts. This cap guarantees the phase ends; it is generous over gifWarmMax so a large,
	// progressing warm is not cut early.
	gifWarmHardCap = 20 * time.Second

	// Incremental warm submission: startSceneExport must not loop PrefetchChain(PriorityHigh) over every scene ref. The
	// high lane holds only highLaneCap (64) jobs and Submit blocks the producer when full (network/pool.go), so
	// thousands of refs from an all-day .demo would block the render thread before the overlay draws. Refs are stored
	// on the job and submitted a bounded batch per warm tick. warmInFlightWindow bounds submitted-but-not-resident
	// refs, well under highLaneCap. warmPrefetchPerTick is the per-tick burst cap (the pool's worker count,
	// DefaultWorkers).
	warmInFlightWindow  = 32 // < highLaneCap(64): outstanding high-lane jobs stay below the cap
	warmPrefetchPerTick = 16 // = network.DefaultWorkers: a frame's-worth of submissions

	// gifLoadBuf is the buffer of the disk-load result channel (hard rule 4). One is enough: a single loader sends once
	// and tickGifExport drains it every frame, and the buffer always accepts, even after a Cancel.
	gifLoadBuf = 1

	// gifSeedBuf is the buffer of the format-seed result channel (hard rule 4), with the same contract as gifLoadBuf:
	// tickGifSeed drains it, and a Cancel that nils a.gif leaves the send in an unpolled buffer.
	gifSeedBuf = 1
)

// exportResult is one finished export delivered to the UI thread: the toast line plus, on success, the artifact's path
// for the clipboard copy.
type exportResult struct {
	msg  string
	path string // "" on failure
}

// exportKind selects the output format of one gifExportJob. GIF and WebP use the encoders here; Video streams raw
// frames into a system ffmpeg (internal/videoenc).
type exportKind int

const (
	exportGIF   exportKind = iota // animated GIF (paletted, every frame held for gifenc)
	exportWebP                    // animated WebP (libwebp streams + compresses each frame)
	exportVideo                   // MP4/WebM via a system ffmpeg (videoenc streams frames)
	exportComic                   // a single PNG storyboard: one still panel per IC line (pure Go)
)

// maxVideoHours stops a wedged exporter from encoding forever. It is not a memory bound: video frames stream to ffmpeg
// and are never retained, so it must not limit a real film. 24 h is far above any real export.
const maxVideoHours = 24

// videoMaxFrames derives the video frame cap from the chosen fps, so the cap is the same duration at any cadence. The
// config clamp allows [6,30] fps (maxExportFPS=30 in internal/config), so the cap reaches 24*3600*30 frames. The frame
// counter and cue offsets are computed multiply-first (frameToMs) and stay within int64. Pure.
func videoMaxFrames(fps int) int {
	if fps < 1 {
		fps = 1
	}
	return maxVideoHours * 3600 * fps
}

// gifResultBuf is the buffer of the off-thread export result channel (hard rule 4). One is enough: one export runs at a
// time and pollGifResult drains it every frame. comicexport.go also writes it, for the single in-flight export.
const gifResultBuf = 1

// Export size and frame-rate dropdown presets (4:3). Bounds mirror the config clamp, so a pick is always in range.
var (
	exportHeightPresets = []int{288, 360, 480, 540}
	exportHeightLabels  = []string{"Small · 384×288", "Medium · 480×360", "Large · 640×480", "XL · 720×540"}
	exportFPSPresets    = []int{8, 12, 15, 24}
	exportFPSLabels     = []string{"8 fps · smallest", "12 fps", "15 fps", "24 fps · smoothest"}

	// Video container/codec choices; the index matches the persisted VideoFormat string, MP4/H.264 first as the
	// default.
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

// nearestPresetIdx returns the index of the preset closest to v, so a hand-edited pref lands on a sensible row.
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

// drawExportOptions draws the shared GIF/WebP export controls (size, frame rate, WebP quality, loop and, with
// withSpeed, playback speed) at the pad column, persisting each change immediately, and returns the y below the panel.
// Settings Studio and the maker's export panel share it. withSpeed is off in Settings, whose Replay section owns the
// slider.
func (a *App) drawExportOptions(x, y int32, withSpeed bool) int32 {
	c := a.ctx
	// Aligned to the caller's content origin so rows and sliders land in the right column.
	a.formX = x
	pad := a.formX
	opts := a.d.Prefs.Load().Export
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

	// Subtitle sidecar for the video export, cue-timed to the video.
	if next := c.Checkbox(pad, y, "Write subtitles beside the video (.srt + .vtt, speaker + line)", opts.Subtitles); next != opts.Subtitles {
		opts.Subtitles = next
		a.d.Prefs.SetExportOpts(opts)
	}
	y += 28

	// Put the finished export file on the clipboard (Windows) so it pastes straight into Discord.
	if next := c.Checkbox(pad, y, "Copy the finished file to the clipboard (paste straight into Discord — Windows)", opts.CopyToClipboard); next != opts.CopyToClipboard {
		opts.CopyToClipboard = next
		a.d.Prefs.SetExportOpts(opts)
	}
	y += 28

	// Quality drives both the WebP encoder and the video CRF (higher = better and bigger).
	if next := a.sliderRow(y, "  Quality % (WebP / video)", opts.Quality, 5, 20, 100); next != opts.Quality {
		opts.Quality = next
		a.d.Prefs.SetExportOpts(opts)
	}
	y += 30

	// Chat text size in the export, fitted to the output size at 100% and independent of the live chat zoom. Bounds
	// mirror the config clamp.
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

	// Watermark: a small corner credit on GIF/WebP/Video exports, not the comic.
	if next := c.Checkbox(pad, y, "Watermark the export (top-right corner stamp)", opts.Watermark); next != opts.Watermark {
		opts.Watermark = next
		a.d.Prefs.SetExportOpts(opts)
	}
	y += 28
	if opts.Watermark {
		c.Label(pad+16, y+5, "Text:", ColTextDim)
		if next, _ := c.TextField("exp_wmtext", sdl.Rect{X: fx, Y: y, W: 220, H: fieldH}, opts.WatermarkText, "blank = server · date"); next != opts.WatermarkText {
			opts.WatermarkText = next
			a.d.Prefs.SetExportOpts(opts)
		}
		c.Label(fx+230, y+5, "blank stamps the recording's server + date", ColTextDim)
		y += 30
	}

	// Playback speed is shared with the replay player and also drives export pacing.
	if withSpeed {
		spd := a.d.Prefs.Load().ReplayPlaybackSpeed
		if next := a.sliderRow(y, "  Playback speed %", spd, 5, 25, 200); next != spd {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.ReplayPlaybackSpeed = next })
		}
		y += 30
	}
	return y
}

// exportMaxFrames is the frame cap for a w×h export: bounded by the paletted-frame memory budget, floored so the
// largest size still gets a few seconds, and never above the absolute cap.
func exportMaxFrames(w, h int32) int {
	if w <= 0 || h <= 0 {
		return maxGifFrames
	}
	n := gifFrameBudgetBytes / (int(w) * int(h))
	n = value.Clamp(n, minExportFrames, maxGifFrames)
	return n
}

// gifExportJob is the in-flight render state, allocated only while exporting. GIF accumulates paletted frames for
// gifenc; WebP streams each RGBA frame into the libwebp encoder and holds none.
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

	// Video only: ffmpeg streams frames to vidPath as they arrive and holds none. vidPath is resolved up front because
	// ffmpeg owns the file while it runs.
	vid       *videoenc.Encoder
	vidPath   string
	vidFormat videoenc.Format // Video only — codec choice for the post-capture audio mux
	audioCap  *audioCapture   // Video only — records the scene's music + SFX for the audio mux (#99)

	// Video only: subtitle cues, frame-timed to match the video. subPend waits for its text to become visible, subOpen
	// is on screen now, subs holds the closed cues.
	subsOn     bool
	subs       []subCue
	subPend    subCue
	subHasPend bool
	subOpen    subCue
	subHasOpen bool

	// Per-export output settings from config ExportOptions, resolved once.
	w, h      int32         // capture/output size (4:3)
	fps       int           // chosen frame rate; drives frameToMs cue offsets (multiply-first)
	frameDt   time.Duration // room Update step per captured frame (= 1/fps)
	delayCs   int           // GIF per-frame delay, centiseconds
	loop      bool          // loop the animation forever vs play once
	maxFrames int           // frame cap for this size (memory-budgeted)
	stamp     string        // #74 watermark text ("" = off), resolved once at start

	// Conversation chatbox raster, rebuilt per message and revealed rune by rune. Cached here, never the live
	// a.msRaster.
	chatRaster *render.MessageRaster
	chatText   string
	chatPct    int // export chat-font scale (fitted to the capture size, not live zoom)

	// Asset pre-warm phase: prefetch the scene's assets and wait for T1 residency before the first capture.
	warming      bool
	warmRefs     []courtroom.AssetRef // the FULL set (M in the overlay + residency counting)
	warmCreated  time.Time            // job-creation wall clock: anchors gifWarmHardCap (never-resident-ref backstop)
	warmStarted  time.Time
	warmLastGain time.Time
	warmBest     int // most assets seen SETTLED (resident or conclusively 404'd) so far (quiescence detector)
	warmResident int // settled count from the last warm tick (for the overlay's "N ready")

	// Incremental warm submission: pendingWarm holds refs not yet handed to the pool; tickGifWarm drains a bounded
	// batch per tick so the producer never blocks on a full high lane. warmInFlight is the set of submitted bases not
	// yet T1-resident, the high-lane occupancy this phase contributes. Each tick prunes resident bases. Once it reaches
	// warmInFlightWindow, room is 0 and submission stops. It counts only real submits, so pre-resident refs cannot
	// deflate the window and reopen the block. warmSubmitted counts real submits. warmSubmitDone is set once every ref
	// has been submitted; only then does the quiescence/timeout clock start, so a large ref list is not cut by
	// gifWarmMax while still being fed in.
	pendingWarm    []courtroom.AssetRef
	warmInFlight   map[string]struct{}
	warmSubmitted  int
	warmSubmitDone bool

	// Format-seed phase: before the warm, fetch <origin>/extensions.json and seed the host's learned formats, as the
	// session connect path does. The warm feeds the render path, which stays zero-fallback, so it must probe the
	// manifest-declared format. A .demo's recorded origin is never the session origin, so without this the warm walks
	// [.webp] against a .gif/.png server and captures an empty stage. Gated on the same http(s), auto-detect and
	// per-origin dedupe as the session path. seedDone (1-buffered) delivers the outcome to tickGifSeed. Where the
	// manifest does not answer, the warm keeps its behavior (demand pipeline and missingno): walking the full chain on
	// the render path would break zero-fallback. A prior content report of the same demo already learned the formats
	// (probeRef, ResolveRawFull).
	seeding      bool
	seedDone     chan seedResult
	seedOriginTo string // the origin being seeded (for WarmFromPrefs gating + the overlay)

	// Async load phase: the disk read and parse run on one goroutine off the render thread. loadCh
	// (gifLoadBuf-buffered) delivers the parsed recording; loadName is the file base for the overlay and loadKind the
	// requested format. On Cancel the job and its channel are dropped, so a late send lands in an unpolled buffer and
	// the goroutine ends.
	loading  bool
	loadCh   chan gifLoadResult
	loadName string
	loadPath string
	loadKind exportKind
}

// gifLoadResult is the disk-load goroutine's handoff: the parsed recording plus notes to emit on the render thread
// (pushDebug / warnLine cannot be touched off it).
type gifLoadResult struct {
	rec     *sceneRecording
	err     error
	debug   []string // skipped/truncated import notes (emitted via pushDebug)
	warnMsg string   // empty-origin honesty warning ("" = none), emitted via warnLine
}

// startSceneExport starts the offscreen render of scene into an animated GIF (exportGIF), WebP (exportWebP) or MP4/WebM
// video (exportVideo). Render thread only (it creates the capture target).
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
		// ffmpeg is optional at runtime; only this action needs it.
		a.warnLine = "Video export needs ffmpeg — install it and add it to PATH (the app works fine without it; GIF/WebP still export)."
		a.warnAt = time.Now()
		return
	}
	// Resolve the sticky export options into this job's size and cadence.
	opts := a.d.Prefs.Load().Export
	h := int32(opts.HeightPx)
	w := h * 4 / 3
	// A comic page renders fixed, small panels (one per line) regardless of the Size knob, with a bounded held-RGBA
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
	// WebP stores one integer per-frame duration, so a rounded constant is the best it can do (video and subtitle cue
	// offsets use frameToMs instead). Round to nearest so 24 fps gives 42 ms (about 41.67), not 41.
	frameMs := (1000 + fps/2) / fps
	if frameMs < 1 {
		frameMs = 1
	}
	// Video streams to ffmpeg and holds no frames, so scene length drives the cap up to the duration limit (24 h at the
	// chosen fps), not the small memory cap GIF/WebP/Comic keep.
	maxFrames := exportMaxFrames(w, h)
	if kind == exportVideo {
		maxFrames = videoMaxFrames(fps)
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
	// Audio sink per kind: GIF/WebP keep real audio. A comic is a silent still page whose fast-forward would play every
	// blip at once and rip through music, so it gets a no-op sink. A video is silent at capture (export speed garbles
	// live audio), but an audioCapture sink records music and SFX for finishVideoExport to mux in.
	var audio courtroom.AudioSink = a.d.Audio
	var audioCap *audioCapture
	switch kind {
	case exportComic:
		audio = courtroom.NopAudio{}
	case exportVideo:
		audioCap = &audioCapture{frameRef: func() int {
			if a.gif != nil {
				return a.gif.captured
			}
			return 0
		}}
		audio = audioCap
	}
	// Constructed and wired through newroom.go so the export room gets the screen-effect resolver the comic and video
	// ask for below.
	room := a.newRoom(courtroom.NewURLBuilder(scene.Origin), nil, audio)
	room.Typewriter.Interval, room.TextStay = a.replayTiming()
	room.CatchUp = false
	// Post-construction override (newroom.go): export the authored effects, not the viewer's accessibility pref,
	// screen-effects toggle or sprite-style off-switch. ReduceMotion alone leaves effectsVisible() false when the
	// exporter watches with effects off.
	a.applyAuthoringVisualsToRoom(room)
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = room.NotifyPreanimDone
		a.d.Viewport.OnFrameShown = room.NotifyFrameShown // #17: frame effects follow the export room's sprite
	}
	if scene.StartBg != "" {
		room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: scene.StartBg})
	}
	if a.gifResultCh == nil {
		a.gifResultCh = make(chan exportResult, gifResultBuf)
	}

	// Pre-warm: enumerate every sprite, background and desk the scene needs. Refs are submitted incrementally by
	// tickGifWarm so a thousands-of-refs import cannot fill the high lane and block the render thread; tickGifWarm then
	// waits for them to decode into T1, or the export outruns the fetch and captures an empty stage. Music refs (Exact)
	// are not textures. pendingWarm holds the whole list; no PrefetchChain call fires here.
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
		warmRefs = append(warmRefs, r) // AssetType: from SceneAssets — submitted in tickGifWarm
	}

	now := time.Now()
	a.gif = &gifExportJob{
		room:        room,
		ct:          ct,
		events:      append([]recEvent(nil), scene.Events...),
		name:        sanitizeStem(name),
		kind:        kind,
		webp:        enc,
		vid:         vid,
		vidPath:     vidPath,
		vidFormat:   vidFormat,
		audioCap:    audioCap,
		w:           w,
		h:           h,
		fps:         fps,
		frameDt:     frameDt,
		delayCs:     delayCs,
		loop:        opts.Loop,
		maxFrames:   maxFrames,
		stamp:       exportStamp(opts, scene),
		subsOn:      kind == exportVideo && opts.Subtitles, // #69 frame-timed .srt/.vtt beside the video
		chatPct:     exportChatPct(h, opts.TextScale),      // font fitted to the capture, not the live zoom
		warming:     true,
		warmRefs:    warmRefs, // full set (M in the overlay + residency counting)
		warmCreated: now,      // anchors gifWarmHardCap so never-resident refs can't hang the phase
		// pendingWarm is the not-yet-submitted subset. The quiescence/timeout clock (warmStarted/warmLastGain) stays
		// zero here and starts at warmSubmitDone, so a huge ref list is not cut by gifWarmMax while still being fed in.
		pendingWarm: warmRefs,
	}
	a.gifExporting = true
	a.beginExportScaleBracket() // #77: exports render at native (100%) scale, not the live UI scale

	// Seed the recording's origin formats before the warm so it probes the manifest-declared format (see the
	// seeding-phase field). Same http(s), auto-detect and per-origin dedupe as the session connect path; a non-http
	// origin (local:// mount) or auto-detect off skips to warming. tickGifSeed gates the first warm tick on the result.
	if strings.HasPrefix(scene.Origin, "http") && a.d.Prefs.Load().AutoDetectFormats && !a.originSeeded(scene.Origin) {
		a.markOriginSeeded(scene.Origin)
		a.startExportSeed(a.gif, scene.Origin)
		a.warnLine = "Checking the server's format manifest…"
	} else {
		a.warnLine = "Loading scene assets…"
	}
	a.warnAt = now
}

// startExportSeed spawns the one bounded format-seed goroutine for an export job and enters the seeding phase, which
// tickGifExport gates ahead of the warm. The render thread republishes the resolver table on delivery (tickGifSeed).
// Render thread only.
func (a *App) startExportSeed(j *gifExportJob, origin string) {
	j.seeding = true
	j.seedOriginTo = origin
	j.seedDone = make(chan seedResult, gifSeedBuf)
	done := j.seedDone
	mgr := a.d.Manager
	prefs := a.d.Prefs
	go func() {
		done <- seedOriginFormats(mgr, prefs, origin) // 1-buffered: always accepts
	}()
}

// tickGifSeed polls the format-seed goroutine without blocking (render thread). On delivery it republishes the resolver
// table (WarmFromPrefs, as pollManifest does) so the warm's first candidate sees the seeded formats, then enters the
// warm phase. A Cancel that nils a.gif never reaches here (tickGifExport's j==nil guard fires).
func (a *App) tickGifSeed(j *gifExportJob) {
	select {
	case res := <-j.seedDone:
		if res.status == seedApplied {
			a.d.Resolver.WarmFromPrefs()
		}
		j.seeding = false
		a.warnLine = "Loading scene assets…"
		a.warnAt = time.Now()
	default:
		// Still seeding; the overlay's "Checking ... manifest" branch keeps drawing.
	}
}

// beginExportScaleBracket pins the text device scale to 100 for the export session and remembers the live value;
// endExportScaleBracket restores it. Set once per session: the modal export does not redraw the chrome and no
// scale-change event fires while it runs. Idempotent (a second begin keeps the first saved value).
func (a *App) beginExportScaleBracket() {
	if a.exportSavedDevScale != 0 {
		return // already bracketed
	}
	a.exportSavedDevScale = int(a.ctx.textDevPct)
	a.ctx.SetTextDevScale(DefaultScalePct)
}

// endExportScaleBracket restores the live text device scale saved by beginExportScaleBracket. No-op when not bracketed.
func (a *App) endExportScaleBracket() {
	if a.exportSavedDevScale == 0 {
		return
	}
	a.ctx.SetTextDevScale(a.exportSavedDevScale)
	a.exportSavedDevScale = 0
}

// tickGifExport captures a bounded batch of frames (render thread). It feeds the next event when the room goes idle (as
// replay does), updates by a fixed dt faster than real time, renders into the offscreen target, and quantizes, dropping
// the RGBA so only paletted frames are retained.
func (a *App) tickGifExport() {
	j := a.gif
	if j == nil {
		a.gifExporting = false
		// Currently unreachable (a.gif is set before a.gifExporting), but this abort path must restore the live text
		// scale like every other export exit; end is a no-op when the bracket is not active.
		a.endExportScaleBracket()
		return
	}
	// Loading phase: poll the off-thread disk read without blocking. It runs before the Viewport guard because loading
	// needs neither the viewport nor a capture target. On delivery finishLoadedExport runs the render-thread tail,
	// which does.
	if j.loading {
		a.tickGifLoad(j)
		return
	}
	if j.seeding {
		// Format-seed phase: an off-thread fetch and prefs seed, gated ahead of the warm. It needs neither viewport nor
		// capture target, so it runs before the Viewport guard.
		a.tickGifSeed(j)
		return
	}
	if a.d.Viewport == nil {
		// No viewport to render into: end the export cleanly and release the scale bracket startSceneExport set.
		a.gif = nil
		a.gifExporting = false
		a.endExportScaleBracket()
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
			ev := j.events[j.idx]
			if courtroom.EventKind(ev.Kind) == courtroom.EventMessage {
				j.subFeed(ev.Message, j.captured) // #69: close the on-screen cue, pend the new line
			}
			j.room.HandleEvent(eventFromRec(ev))
			j.idx++
		}
		j.room.Update(j.frameDt)
		a.d.Viewport.SetSpriteFX(a.spriteFX())
		a.d.Viewport.Update(&j.room.Scene, j.frameDt)
		img, err := j.ct.Capture(a.ctx.Ren, func(dst sdl.Rect) {
			a.drawExportScene(j, &j.room.Scene, dst) // themed stage when a layout theme is active, else full-frame
			a.drawExportStamp(j, dst)                // #74 opt-in watermark, top-right
		})
		if err != nil {
			a.pushDebug("scene export capture: " + err.Error())
			a.finishGifExport()
			return
		}
		switch j.kind {
		case exportWebP:
			// Stream into the WebP encoder, which compresses and drops the RGBA; GIF keeps the quantized frame for the
			// final encode.
			if err := j.webp.AddFrame(img); err != nil {
				a.pushDebug("webp add: " + err.Error())
				a.finishGifExport()
				return
			}
		case exportVideo:
			// Stream the raw frame into ffmpeg (synchronous; the export owns the window). A broken pipe means ffmpeg
			// died: stop, and finishVideoExport reaps the process and reports the stderr tail.
			if err := j.vid.AddFrame(img); err != nil {
				a.pushDebug("video add: " + err.Error())
				a.finishGifExport()
				return
			}
		default: // exportGIF
			j.frames = append(j.frames, gifenc.Quantize(img))
		}
		j.subAnchor(j.room.Scene.VisibleRunes, j.captured) // #69: cue starts on the first frame that shows text
		j.captured++
	}
}

// tickGifLoad polls the off-thread disk-load result without blocking (render thread). On error the export exits
// cleanly; on success finishLoadedExport runs the render-thread tail. The loading shell holds no capture target or
// encoder, and loading never set the scale bracket, so an error teardown only clears the export flags.
func (a *App) tickGifLoad(j *gifExportJob) {
	select {
	case res := <-j.loadCh:
		if res.err != nil {
			a.gif = nil
			a.gifExporting = false
			a.warnLine = "Couldn't load recording: " + res.err.Error()
			a.warnAt = time.Now()
			return
		}
		a.finishLoadedExport(res)
	default:
		// Still loading; the overlay's "Reading ..." branch keeps drawing.
	}
}

// nextWarmBatch pulls up to batch refs off the front of pending that still need submitting, skipping resident ones
// (they are in T1 and would waste lane space). It returns the refs to submit and the remaining tail. Pure (resident is
// injected). A non-positive batch submits nothing.
func nextWarmBatch(pending []courtroom.AssetRef, batch int, resident func(base string) bool) (submit, rest []courtroom.AssetRef) {
	if batch <= 0 || len(pending) == 0 {
		return nil, pending
	}
	for i, r := range pending {
		if len(submit) >= batch {
			return submit, pending[i:]
		}
		if resident != nil && resident(r.Base) {
			continue // already in T1 — don't spend a lane slot on it
		}
		submit = append(submit, r)
	}
	return submit, nil // drained the whole pending list
}

// warmRoom is how many more refs the pre-warm may submit this tick without filling the high lane, given the outstanding
// count (submitted bases not yet resident, warmInFlight). outstanding counts only refs this phase submitted. A ref can
// be resident without being submitted (warm cache, assets already in T1), and counting it would deflate the window and
// push in-flight past highLaneCap until Submit blocks the render thread. So warmRoom is window - outstanding, capped at
// the per-tick burst and floored at 0. Pure.
func warmRoom(outstanding int) int {
	room := warmInFlightWindow - outstanding
	room = value.Clamp(room, 0, warmPrefetchPerTick)
	return room
}

// warmSettled reports whether a warm ref is done loading: T1-resident (Store.Contains) or its whole fallback chain
// returned a clean 404 (Store.IsMissing; drawSprite then draws the missingno placeholder).
// IsMissing is set by drainWarnings only when walkCandidates exhausts every candidate with a clean 404
// (manager.go reportMissing). A transport error (empty origin, unreachable host, 5xx) never sets it, so a dead origin
// exits via the wall-clock ceiling (gifWarmMax, up to gifWarmHardCap) and captures a blank stage.
// drainWarnings marks only AssetTypeCharSprite and AssetTypeDeskOverlay bases missing (app.go), so a 404'd background
// never reads IsMissing. Render thread only.
func (a *App) warmSettled(base string) bool {
	return a.d.Store.Contains(base) || a.d.Store.IsMissing(base)
}

// tickGifWarm runs during the pre-warm phase. It submits a bounded batch of pending refs to the pool (never the whole
// list, so the high lane cannot fill and block the render thread), then counts assets that are settled (T1-resident or
// conclusively 404'd, warmSettled). The wait ends when all are settled, when none has settled for gifWarmQuiet, or at
// gifWarmMax. The quiescence/timeout clock starts only once every ref has been submitted (warmSubmitDone).
func (a *App) tickGifWarm(j *gifExportJob) {
	now := time.Now()

	settled := 0
	for _, r := range j.warmRefs {
		if a.warmSettled(r.Base) {
			settled++
		}
	}
	j.warmResident = settled

	// Prune the in-flight set: a submitted base that settled (resident or conclusively 404'd) has left the high lane
	// and frees window room. Pruning on settled lets a 404-heavy scene submit past the first window so warmSubmitDone
	// can set. Only submitted bases are counted, so a warm cache cannot deflate the window and reopen the block.
	for base := range j.warmInFlight {
		if a.warmSettled(base) {
			delete(j.warmInFlight, base)
		}
	}

	// Incremental submission: bound outstanding high-lane occupancy (len(warmInFlight)) to warmInFlightWindow so Submit
	// never finds a full lane, and cap the per-tick burst at warmPrefetchPerTick.
	if len(j.pendingWarm) > 0 {
		room := warmRoom(len(j.warmInFlight))
		submit, rest := nextWarmBatch(j.pendingWarm, room, a.warmSettled)
		for _, r := range submit {
			if r.Base == "" {
				continue // prefetchChain drops an empty base — don't count a no-op submit
			}
			a.d.Manager.PrefetchChain(r.Base, r.Alts, r.Type, network.PriorityHigh) // AssetType: from SceneAssets
			if j.warmInFlight == nil {
				j.warmInFlight = make(map[string]struct{})
			}
			j.warmInFlight[r.Base] = struct{}{} // occupies a window slot until it settles (or the hard cap ends the phase)
			j.warmSubmitted++
		}
		j.pendingWarm = rest
	}
	if len(j.pendingWarm) == 0 && !j.warmSubmitDone {
		// Submission just finished: start the quiescence/timeout clock now, not at job creation.
		j.warmSubmitDone = true
		j.warmStarted = now
		j.warmLastGain = now
		j.warmBest = settled
	}

	if settled > j.warmBest {
		j.warmBest = settled
		j.warmLastGain = now
	}
	// Absolute backstop, measured from job creation so it holds before warmSubmitDone is set. It is the last resort for
	// a ref that neither resolves nor reports a conclusive miss: a transport-error origin never becomes IsMissing (see
	// warmSettled). Force the phase to end and render whatever loaded.
	if !j.warmCreated.IsZero() && now.Sub(j.warmCreated) > gifWarmHardCap {
		j.warming = false
		a.warnLine = "Rendering…" // kind-neutral; the progress overlay names the format
		a.warnAt = now
		return
	}
	// The end conditions apply only once every ref has been submitted, or a warm-cache scene could quiesce before its
	// later refs are in the lane.
	if !j.warmSubmitDone {
		return
	}
	allReady := len(j.warmRefs) > 0 && settled >= len(j.warmRefs)
	quiesced := settled > 0 && now.Sub(j.warmLastGain) > gifWarmQuiet
	timedOut := now.Sub(j.warmStarted) > gifWarmMax
	if allReady || quiesced || timedOut || len(j.warmRefs) == 0 {
		j.warming = false
		a.warnLine = "Rendering…" // kind-neutral; the progress overlay names the format
		a.warnAt = now
	}
}

// drawGifChatbox composites the conversation chatbox (speaker name and the message typed out to the current rune) over
// the captured scene. It mirrors the live flat-panel look and reuses renderRaster, but takes no input (no wheel zoom)
// and caches its raster on the export job, never the live a.msRaster. Render thread only (inside the capture draw
// callback). gifChatNameRowH is the speaker-name row above the message text; the message is drawn this far below the
// box top.
const gifChatNameRowH = 24

// gifChatboxHeight sizes the export chatbox to fit the rasterized message (height textH) in the capture frame (height
// vpH): name row, text and padding, floored so a one-word line still gets a panel and capped at 3/5 of the frame. A
// long message clips inside the box.
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

// fitChatRaster rasterizes the message at the export font scale (pct), shrinking the font if the message would overflow
// the tallest the box can be (3/5 of the frame), so a full ~256-char line fits. Bounded iterations, floored at the min
// chat scale. Render thread only.
func (a *App) fitChatRaster(sc *courtroom.Scene, wrapW, vpH int32, pct int, skinned, comicInk bool) *render.MessageRaster {
	maxTextH := vpH*3/5 - gifChatNameRowH - 10
	if maxTextH < gifChatNameRowH {
		maxTextH = gifChatNameRowH
	}
	for attempt := 0; attempt < 5; attempt++ {
		r, err := renderRaster(a, sc, wrapW, skinned, pct, comicInk)
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
	r, _ := renderRaster(a, sc, wrapW, skinned, config.MinChatScalePercent, comicInk) // safety: show text at the floor
	return r
}

// drawExportScene composites one exported frame. When a layout theme is active (a valid design layout and the
// theme-layout pref on, as the live courtroom requires), the frame mirrors the themed stage: courtroom backdrop,
// viewport at its design rect, chatbox at ao2_chatbox. Otherwise the viewport fills the frame and the chatbox is
// pinned to the
// bottom. The layout is computed for the export frame's size (dst is origin-anchored at 0,0). It uses themeLayout, not
// themeWindowLayout, so no menu bar or tab strip band appears as a black stripe in an export. The shared layout cache
// (a.themeLay) is borrowed, not thrashed: an export is a full-window mode, and App.Frame's gif > replay > maker >
// screen precedence draws tickGifExport + drawGifProgress instead of any screen (see screenDispatchPreempted in
// chrometop.go). So drawCourtroomThemed, the only other builder of this cache, does not run for the job, and all
// captures within a tick pass the same dst. The cost is two cold rebuilds per job, like a resize.
func (a *App) drawExportScene(j *gifExportJob, sc *courtroom.Scene, dst sdl.Rect) {
	if lay := a.themeLayout(dst.W, dst.H); lay.valid && a.d.Prefs.Load().ThemeLayoutOn {
		if _, ok := lay.rect("viewport"); ok {
			a.drawGifThemedFrame(j, sc, dst, lay)
			return
		}
	}
	a.d.Viewport.Render(a.ctx.Ren, sc, dst)
	a.drawGifChatbox(j, sc, dst) // composite the conversation over the scene
}

// drawGifThemedFrame renders the export frame through the theme's design geometry: the courtroom backdrop over the
// letterboxed design area, the scene viewport at the theme's "viewport" rect, and the chatbox at "ao2_chatbox" (a
// bottom overlay over the viewport when the theme omits it). Geometry comes from the same themeLayout as the live
// themed courtroom. Free elements paint here too: the three band calls are the same three drawCourtroomThemed makes.
// Without them a native theme's wallpaper, chatbox frame and masthead would be missing from exports. An export has no
// logs, panels or modals, so the mid band and the overlay band both land after the chatbox, in that order. Band order
// is what the format promises (docs/THEME-FORMAT.md); the widgets between bands are the client's. The comic exporter is
// not wired: it composes paper panels with its own speech bubble (captureComicPanel renders the bare viewport).
func (a *App) drawGifThemedFrame(j *gifExportJob, sc *courtroom.Scene, dst sdl.Rect, lay *themeLayoutCache) {
	c := a.ctx
	// Stage frame: the theme's window art over the centered design area (the capture's own clear gives the black
	// letterbox bars), as drawCourtroomThemed does. lay.area is the cached canvas, the same rect that function paints
	// into.
	court := lay.area
	if page, ok := a.themePage("courtroombackground"); ok {
		c.cgoRect = court
		_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
	} else {
		c.Fill(court, ColPanel)
	}
	// visible_when is resolved once per export frame against the export's room (j.room), not the live session, so
	// `visible_when = shout:objection` lights up on the recorded objection.
	a.refreshElementConditionsFrom(lay, a.exportCondSource(j))

	// Band 1 of 3: over the courtroom backdrop, under everything the export draws.
	a.drawElementBands(lay, theme.BandBackdrop)

	vp, _ := lay.rect("viewport")
	c.Fill(vp, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	a.d.Viewport.Render(c.Ren, sc, vp)
	if box, ok := lay.rect("ao2_chatbox"); ok {
		a.drawGifThemedChatbox(j, sc, box, lay)
	} else {
		a.drawGifChatbox(j, sc, vp) // theme has no chatbox rect: bottom overlay over the stage
	}

	// Bands 2 and 3: over the stage and the chatbox. They are adjacent because the widgets the live pass puts between
	// them (logs, music list, emote grid) do not exist in an export frame.
	a.drawElementBands(lay, theme.BandMid)
	a.drawElementBands(lay, theme.BandOverlay)
}

// exportCondSource is elemCondSource for an export frame: the stage axes come from the job's own room, the two
// client-state axes (pos and char, the local player's pos dropdown and character folder) from the live client, since a
// .aorec records the stage and not the reader's UI state.
func (a *App) exportCondSource(j *gifExportJob) elemCondSource {
	src := elemCondSource{pos: a.mySide(), char: a.myCharName()}
	if j == nil || j.room == nil {
		return src
	}
	src.speaking = j.room.Phase() != courtroom.PhaseIdle
	src.side = j.room.Scene.Position
	src.shout = j.room.CurrentShout()
	return src
}

// drawGifThemedChatbox draws the export chatbox at the theme's ao2_chatbox rect: the chatbox skin (or a flat panel),
// and the showname and message at their chatbox-relative design rects, clipped to the box. It mirrors drawThemedChatBox
// but reads the export room's scene and its own raster (j.chatRaster).
func (a *App) drawGifThemedChatbox(j *gifExportJob, sc *courtroom.Scene, box sdl.Rect, lay *themeLayoutCache) {
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	c := a.ctx
	// Per-character chatbox wins over the theme skin (AO2 get_chat priority), as in the live themed chatbox.
	var charSkin *sdl.Texture
	if sc.ChatSkinBase != "" {
		if page, ok := a.d.Store.Get(sc.ChatSkinBase); ok && len(page.Frames) > 0 {
			charSkin = page.Frames[0]
		}
	}
	skinPage, themeSkinned := a.themePage(themeStemChatbox)
	skinned := charSkin != nil || themeSkinned

	// showname and message sit at their chatbox-relative design rects (AO2 child semantics), falling back to the
	// classic offsets when the theme omits them. The arithmetic is shared with the live chatbox; the export keeps only
	// its taller fallback name row (gifChatNameRowH), used when the theme declares no `message` rect. Resolved before
	// the skin is painted because the widen-and-swap ladder below decides which skin is painted.
	nameBox, msgBox := chatboxTextRects(box, lay, box.Y+gifChatNameRowH)
	msgX, msgY, wrapW := msgBox.X, msgBox.Y, msgBox.W

	nameCol := ColAccent
	if skinned && sc.ChatSkinNameHas {
		nameCol = sdl.Color{R: sc.ChatSkinNameColor.R, G: sc.ChatSkinNameColor.G, B: sc.ChatSkinNameColor.B, A: 255}
	} else if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	if a.d.Prefs.Load().NameColors { // per-speaker name colour wins over accent/theme
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.Load().NameSat)/100, float64(a.d.Prefs.Load().NameVal)/100)
	}
	// Shared with the live chatbox (chatboxfit.go): the showname's point size folds with the export layout's own canvas
	// scale (lay was built for the capture frame), so a name keeps its proportion to its box.
	snFont, snEmoji := a.themedChatFace(elemShowname, DefaultScalePct, lay, sc.ShownameText)
	// The widen-and-swap ladder is shared too: a long showname that pushed the live chatbox onto its `med` skin must do
	// the same for the export, against the capture-frame layout.
	if charSkin == nil {
		nameBox, skinPage = a.chatboxSkinLadder(nameBox, lay, skinPage, snFont, snEmoji, sc.ShownameText, nameCol)
	}

	if charSkin != nil {
		c.cgoRect = box
		_ = c.Ren.Copy(charSkin, nil, &c.cgoRect)
	} else if themeSkinned {
		c.cgoRect = box
		_ = c.Ren.Copy(a.themeFrame(skinPage), nil, &c.cgoRect)
	} else {
		c.Fill(box, sdl.Color{R: 16, G: 16, B: 24, A: 215})
		c.Border(box, ColAccent)
	}
	nameX, nameW := a.shownameSpanFor(nameBox, snFont, snEmoji, sc.ShownameText, nameCol)
	a.labelEmoji(snFont, snEmoji, nameX, nameBox.Y, nameW, sc.ShownameText, nameCol)

	// Rasterize once per line, wrapped to the design message width and shrunk to fit the box height (keyed by text like
	// the default path; only one chatbox path runs per export). The message does not take the canvas fold the showname
	// takes: j.chatPct (exportChatPct) already derives from the capture height and fitChatRaster shrinks it to fit, so
	// a third factor would fight the fit loop.
	if j.chatRaster == nil || j.chatText != sc.MessageText {
		if j.chatRaster != nil {
			j.chatRaster.Destroy()
			j.chatRaster = nil
		}
		if sc.MessageText != "" {
			j.chatRaster = a.fitChatRaster(sc, wrapW, box.H, j.chatPct, skinned, false)
		}
		j.chatText = sc.MessageText
	}
	if j.chatRaster != nil {
		_ = c.Ren.SetClipRect(&box)
		j.chatRaster.Draw(c.Ren, sc.VisibleRunes, msgX, msgY)
		_ = c.Ren.SetClipRect(nil)
	}
}

func (a *App) drawGifChatbox(j *gifExportJob, sc *courtroom.Scene, vp sdl.Rect) {
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	c := a.ctx
	wrapW := vp.W - 16
	// Rasterize first so the box can be sized to fit the message: the capture is a small fixed frame, and a
	// live-proportioned box would clip a multi-line message. Rebuilt only when the line changes; the font shrinks so a
	// full ~256-char line fits.
	if j.chatRaster == nil || j.chatText != sc.MessageText {
		if j.chatRaster != nil {
			j.chatRaster.Destroy()
			j.chatRaster = nil
		}
		if sc.MessageText != "" {
			j.chatRaster = a.fitChatRaster(sc, wrapW, vp.H, j.chatPct, false, false)
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
	if a.d.Prefs.Load().NameColors { // per-speaker name colour, same as the live chatbox
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.Load().NameSat)/100, float64(a.d.Prefs.Load().NameVal)/100)
	}
	// Same covering-face pick as the live nameplate, so a non-Latin showname renders instead of tofu.
	a.labelEmoji(a.elemFontFor(elemShowname, DefaultScalePct, sc.ShownameText), a.elemEmoji(elemShowname, DefaultScalePct), // #39: theme showname family/size
		box.X+8, box.Y+4, box.W-16, sc.ShownameText, nameCol)

	if j.chatRaster != nil {
		_ = c.Ren.SetClipRect(&box) // the cap case stays inside the box, never off-frame
		j.chatRaster.Draw(c.Ren, sc.VisibleRunes, box.X+8, box.Y+gifChatNameRowH)
		_ = c.Ren.SetClipRect(nil)
	}
}

// exportStamp resolves the watermark text once per export: the custom text, or when blank the recording's asset host
// and recorded date. "" is off. A bundled archive's loopback origin is skipped, so those fall back to the date alone.
func exportStamp(opts config.ExportOptions, rec *sceneRecording) string {
	if !opts.Watermark {
		return ""
	}
	if t := strings.TrimSpace(opts.WatermarkText); t != "" {
		return t
	}
	date := time.Now().Format("2 Jan 2006")
	if ts, err := time.Parse(time.RFC3339, rec.RecordedAt); err == nil {
		date = ts.Format("2 Jan 2006")
	}
	host := ""
	if u, err := url.Parse(rec.Origin); err == nil {
		host = u.Host
	}
	if h := strings.ToLower(host); h == "" || strings.HasPrefix(h, "127.0.0.1") || strings.HasPrefix(h, "localhost") {
		return date
	}
	return host + " · " + date
}

// drawExportStamp paints the watermark in the capture's top-right (the chatbox owns the bottom band) with a 1-px shadow
// pass. The stamp is constant per export, so its label texture is cached. Render thread only.
func (a *App) drawExportStamp(j *gifExportJob, vp sdl.Rect) {
	if j.stamp == "" {
		return
	}
	c := a.ctx
	w := c.TextWidth(j.stamp)
	x := vp.X + vp.W - w - 8
	y := vp.Y + 6
	c.Label(x+1, y+1, j.stamp, sdl.Color{R: 10, G: 10, B: 12, A: 255})
	c.Label(x, y, j.stamp, sdl.Color{R: 228, G: 228, B: 232, A: 255})
}

// finishGifExport tears down the capture, restores the viewport's preanim callback, and hands the frames to an
// off-thread encode and write.
func (a *App) finishGifExport() {
	j := a.gif
	a.gif = nil
	a.gifExporting = false
	a.endExportScaleBracket() // #77: restore the live UI scale for the message raster
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
		// Sits after the shared teardown (ct.Close / chatRaster.Destroy / restoreViewportPreanim) on purpose: an early
		// return would leak the capture target and leave the viewport's preanim callback pointing at the dead comic
		// room. The captured panels are CPU copies, so closing the target first is fine.
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
	ch := a.gifResultCh
	go func() { ch <- encodeAndWriteGIF(frames, stem, delayCs, loop) }()
}

// finishWebpExport assembles the streamed WebP frames and writes the file off-thread. Per-frame compression already
// happened during capture, and this goroutine solely owns the encoder (a.gif is already nil).
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
	ch := a.gifResultCh
	go func() {
		data, err := enc.Assemble()
		enc.Close()
		if err != nil {
			ch <- exportResult{msg: "WebP export failed: " + err.Error()}
			return
		}
		ch <- writeWebp(data, stem)
	}()
}

// finishVideoExport closes ffmpeg's stdin and waits off-thread for the encode to finalize (ffmpeg writes the moov atom
// on EOF, which can take a moment). ffmpeg has written vidPath throughout, so nothing is assembled. This goroutine
// solely owns the encoder (a.gif is already nil).
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
	// Resolve the soundtrack now, on the render thread: the capture sink's cues are complete and a.gif is about to be
	// cleared. The cue functions place each cue with frameToMs(frame, fps) (multiply-first, so long exports do not
	// drift). The mux runs off-thread below and falls back to the silent video on any failure.
	var songs []songSegment
	var sfx []sfxPlacement
	audioDropped := 0
	if j.audioCap != nil {
		songs = j.audioCap.songSegments(j.fps, j.captured) // windows to the final frame count
		sfx = j.audioCap.sfxPlacements(j.fps)
		// Cues are turned away at two caps: the per-list guard in audioCapture (droppedAtCapture) and the combined
		// maxAudioClips cap the mux applies (music first, then SFX). Report both so a trimmed soundtrack is not a
		// surprise.
		offered := len(songs) + len(sfx)
		admitted := offered
		if admitted > maxAudioClips {
			admitted = maxAudioClips
		}
		audioDropped = j.audioCap.droppedAtCapture + (offered - admitted)
	}
	format := j.vidFormat
	mgr := a.d.Manager
	// Close the final subtitle cue at the last captured frame and hand the list to the finisher goroutine (j is dead
	// after this).
	j.subClose(j.captured)
	subs := j.subs
	subFps := j.fps
	a.warnLine = fmt.Sprintf("Finishing video (%d frames)…", j.captured)
	a.warnAt = time.Now()
	ch := a.gifResultCh
	musicClient := a.sockets().Client(musicHTTPTimeout)
	go func() {
		if err := enc.Finish(); err != nil {
			_ = os.Remove(path) // a failed encode leaves a corrupt file — don't keep it
			ch <- exportResult{msg: "Video export failed: " + err.Error()}
			return
		}
		// Mux first to learn the final on-disk name: a successful mux renames the video to <stem>-audio.<ext> and
		// deletes the silent original (videomux.go). Write the .srt/.vtt sidecars against finalPath afterwards; on the
		// silent fallback finalPath is the original.
		finalPath, soundNote := path, ""
		if len(songs) > 0 || len(sfx) > 0 {
			if muxed, ok := muxSceneAudio(mgr, musicClient, path, songs, sfx, format); ok {
				finalPath, soundNote = muxed, "  ♪ with sound"
				// A factual note when the 64-clip cap trimmed the soundtrack, on the success branch only (the
				// silent-fallback message already says there is no sound). Say "left out", not "past the cap": failed
				// downloads do not consume cap slots, so the count can include resolve failures.
				if audioDropped > 0 {
					soundNote += fmt.Sprintf(" (%d sound cue(s) left out)", audioDropped)
				}
			} else {
				soundNote = "  (couldn't add the sound — saved silent)"
			}
		}
		subNote := ""
		if writeSubtitleFiles(finalPath, subs, subFps) {
			subNote = "  + subtitles (.srt/.vtt)"
		}
		ch <- exportResult{msg: videoSavedMsg(finalPath) + soundNote + subNote, path: finalPath}
	}()
}

// videoExportPath resolves recordings\<stem>-<timestamp>.<ext> (creating the folder). The path must exist up front
// because ffmpeg owns the file for the whole encode.
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

// restoreViewportPreanim rebinds the viewport's one-shot preanim callback to whatever room is now live (maker preview,
// the live room, or none).
func (a *App) restoreViewportPreanim() {
	if a.d.Viewport == nil {
		return
	}
	switch {
	case a.makerOpen && a.makerPreviewRoom != nil:
		a.d.Viewport.OnPreanimDone = a.makerPreviewRoom.NotifyPreanimDone
		a.d.Viewport.OnFrameShown = a.makerPreviewRoom.NotifyFrameShown // #17: frame effects follow the same room
	case a.room != nil:
		a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
		a.d.Viewport.OnFrameShown = a.room.NotifyFrameShown
	default:
		a.d.Viewport.OnPreanimDone = nil
		a.d.Viewport.OnFrameShown = nil
	}
}

// encodeAndWriteGIF (off-thread) encodes the frames and writes recordings\<stem>.gif.
func encodeAndWriteGIF(frames []*image.Paletted, stem string, delayCs int, loop bool) exportResult {
	data, err := gifenc.EncodeGIF(frames, delayCs, loop)
	if err != nil {
		return exportResult{msg: "GIF export failed: " + err.Error()}
	}
	dir := recordingsDir()
	if dir == "" {
		return exportResult{msg: "GIF export failed: no recordings folder."}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return exportResult{msg: "GIF export failed: " + err.Error()}
	}
	name := stem + ".gif"
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return exportResult{msg: "GIF export failed: " + err.Error()}
	}
	return exportResult{msg: fmt.Sprintf("GIF saved: recordings\\%s (%.1f MB).", name, float64(len(data))/(1024*1024)), path: full}
}

// writeWebp (off-thread) writes the assembled animated WebP to recordings\<stem>.webp.
func writeWebp(data []byte, stem string) exportResult {
	dir := recordingsDir()
	if dir == "" {
		return exportResult{msg: "WebP export failed: no recordings folder."}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return exportResult{msg: "WebP export failed: " + err.Error()}
	}
	name := stem + ".webp"
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return exportResult{msg: "WebP export failed: " + err.Error()}
	}
	return exportResult{msg: fmt.Sprintf("WebP saved: recordings\\%s (%.2f MB).", name, float64(len(data))/(1024*1024)), path: full}
}

// pollGifExport delivers the off-thread encode result to the UI and, when the clipboard-copy option is on and the
// export succeeded, puts the finished file on the OS clipboard.
func (a *App) pollGifExport() {
	if a.gifResultCh == nil {
		return
	}
	select {
	case res := <-a.gifResultCh:
		a.warnLine = res.msg
		if res.path != "" && a.d.Prefs.Load().Export.CopyToClipboard {
			if a.d.Desktop.CopyFileToClipboard(a.ctx.win, res.path) {
				a.warnLine += "  📋 on the clipboard"
			}
		}
		a.warnAt = time.Now()
	default:
	}
}

// drawGifProgress paints the export progress overlay, shown instead of the normal screen because the export owns the
// viewport.
func (a *App) drawGifProgress(winW, winH int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: winW, H: winH}, sdl.Color{R: 10, G: 10, B: 14, A: 255})
	j := a.gif
	cx := winW / 2
	cy := winH / 2

	// Loading phase: an always-alive overlay with a Cancel that abandons the pending result. Cancelling drops the job;
	// the loader's send lands in an unpolled buffer (gifLoadBuf always accepts) and the goroutine ends.
	if j != nil && j.loading {
		verb := "demo"
		if strings.EqualFold(filepath.Ext(j.loadPath), recordingExt) {
			verb = "recording"
		}
		c.Label(cx-120, cy-30, "📖  Reading "+verb+"…", ColText)
		c.LabelClipped(cx-160, cy, 320, j.loadName, ColTextDim)
		if c.Button(sdl.Rect{X: cx - 60, Y: cy + 54, W: 120, H: btnH}, "✕ Cancel") {
			a.gif = nil // abandon the pending result; the goroutine finishes and dies
			a.gifExporting = false
		}
		return
	}

	// Format-seed phase: fetching the server's extensions.json before the warm. A brief, network-bounded step with a
	// Cancel that abandons the pending seed result.
	if j != nil && j.seeding {
		c.Label(cx-120, cy-30, "🔎  Checking server formats…", ColText)
		c.LabelClipped(cx-160, cy, 320, j.seedOriginTo, ColTextDim)
		if c.Button(sdl.Rect{X: cx - 60, Y: cy + 54, W: 120, H: btnH}, "✕ Cancel") {
			a.gif = nil // abandon the pending seed; the goroutine finishes and dies
			a.gifExporting = false
			a.endExportScaleBracket() // startSceneExport set the bracket before seeding
		}
		return
	}

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

	// Progress fraction: GIF/WebP are bounded by the frame cap, so their bar is frames/cap. Video almost always
	// finishes by exhausting the script long before its cap, so its bar tracks progress through the events.
	var frac float64
	var sub string
	if j != nil && (j.kind == exportVideo || j.kind == exportComic) {
		// Both stream the script rather than fill a frame cap, so the bar tracks progress through the events.
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
	fillW = value.Clamp(fillW, 0, bar.W)
	c.Fill(sdl.Rect{X: bar.X, Y: bar.Y, W: fillW, H: bar.H}, ColAccent)
	if c.Button(sdl.Rect{X: cx - 60, Y: cy + 54, W: 120, H: btnH}, "■ Stop & save") {
		a.finishGifExport() // keep what's been captured so far
	}
}
