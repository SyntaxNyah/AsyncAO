package ui

import (
	"fmt"
	"image"
	"net/url"
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

// sceneExportFromPath begins exporting a recording straight to a GIF (the Studio
// "🎞 GIF" button), an animated WebP ("🎬 WebP"), or an MP4/WebM video ("🎥
// Video") — no trip through the maker. A bundled archive renders from its own
// folder (the archive source is dropped when the export finishes).
//
// The disk read + parse (ReadFile + demoToRecording / JSON unmarshal) is
// deferred to ONE goroutine off the render thread — a 30–60 MB .aorec or an
// all-day .demo froze the whole window otherwise. This function only VALIDATES,
// creates the job shell in its "loading" phase, sets a.gifExporting=true (so the
// progress overlay draws a "Reading demo…" screen immediately), and spawns that
// loader. tickGifExport's loading branch polls the result and finishes the
// export on the render thread. Single-flight: the a.gifExporting guard refuses a
// second attempt exactly as before.
func (a *App) sceneExportFromPath(path string, kind exportKind) {
	if a.gifExporting || a.replaying || a.recActive {
		a.warnLine = "Finish the current replay / recording first."
		a.warnAt = time.Now()
		return
	}
	// Resolve the origin an imported .demo streams from NOW, on the render thread
	// (a.urls is a render-thread field): the loader goroutine must not touch App.
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
	// The overlay draws while the disk work runs; without a bracket the loading
	// screen renders at the live UI scale, which is fine — the export scale bracket
	// is set later, when capture is set up (startSceneExport, after the load lands).
	a.warnLine = "Reading " + base + "…"
	a.warnAt = time.Now()
	go func() {
		// ONE bounded goroutine (§17.4). It does only pure disk work and sends once
		// on the per-job channel; the buffer (cap 1) always accepts, so a Cancel that
		// drops this job can't leave the goroutine blocked on send — it finishes and
		// dies, its result landing in a buffer nobody polls.
		loadCh <- loadRecordingForExport(path, origin)
	}()
}

// finishLoadedExport runs the render-thread tail of an async export once the
// loader delivers a parsed recording: replay the import notes, route a bundled
// archive to its own folder, and hand off to startSceneExport (which builds the
// capture target, spawns the encoder/ffmpeg, enumerates the scene's assets, and
// begins the incremental warm). Render thread only.
func (a *App) finishLoadedExport(res gifLoadResult) {
	kind := a.gif.loadKind
	path := a.gif.loadPath
	// The shell job is replaced wholesale by startSceneExport's real job; clear the
	// flags first so startSceneExport's `if a.gifExporting { return }` guard reads a
	// clean slate. This all runs inside ONE tickGifExport call before this frame's
	// draw, so the brief a.gifExporting=false window is never observed — and if
	// startSceneExport refuses (empty scene / no ffmpeg), the flags correctly stay
	// cleared and its warnLine shows on the normal screen.
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

	// gifWarmHardCap is the ABSOLUTE wall-clock ceiling on the whole warm phase,
	// measured from job creation (not from warmSubmitDone like gifWarmMax). It is
	// the backstop the incremental submitter needs: outstanding = len(warmInFlight)
	// = submitted bases not yet resident, and a submitted ref that 404s (or resolves
	// to a base other than r.Base) NEVER becomes T1-resident, so it never leaves the
	// in-flight set. If ≥warmInFlightWindow such refs pile up, room stays 0,
	// pendingWarm never drains, warmSubmitDone never latches, and the gifWarmMax
	// clock (which only starts at warmSubmitDone) would never run — the phase would
	// hang forever on a missing-origin imported .demo (exactly the 404-heavy scene
	// this async path exists to serve). This anchor guarantees the phase ends and
	// the export proceeds regardless of pending/submit state. Generous headroom
	// over gifWarmMax so a large, still-progressing warm isn't guillotined early.
	gifWarmHardCap = 20 * time.Second

	// Incremental warm submission (the freeze fix). startSceneExport MUST NOT loop
	// PrefetchChain(PriorityHigh) over every scene ref: the high lane holds only
	// highLaneCap(64) jobs and Submit BLOCKS the producer when it's full (live
	// work is never shed — network/pool.go). An all-day imported .demo yields
	// thousands of refs, so that loop saturates the lane and blocks the render
	// thread — for the whole storm — BEFORE the progress overlay ever draws (the
	// reported "whole window freezes with no loading screen"). Instead the refs
	// are stored on the job and submitted a bounded batch per warm tick.
	//
	// warmInFlightWindow is the OUTSTANDING (submitted-but-not-yet-resident) bound:
	// kept well under highLaneCap so a Submit can never find a full high lane and
	// block. warmPrefetchPerTick is the per-tick burst cap (= the pool's worker
	// count, DefaultWorkers), a small batch that drains within a frame. The two
	// together mean one warm tick's submissions cost microseconds, never a stall.
	warmInFlightWindow  = 32 // < highLaneCap(64): outstanding high-lane jobs stay below the cap
	warmPrefetchPerTick = 16 // = network.DefaultWorkers: a frame's-worth of submissions

	// gifLoadBuf is the buffer of the disk-load → render-thread result channel
	// (hard rule §17.4: every channel has a named cap). One slot is enough — a
	// single loader goroutine sends exactly once and tickGifExport drains it every
	// frame, so the goroutine blocks ≤1 frame on the handoff and then dies (it
	// never leaks blocked on send, even after a Cancel: the buffer always accepts).
	gifLoadBuf = 1

	// gifSeedBuf is the buffer of the format-seed → render-thread result channel
	// (§17.4). Same 1-slot leak-free contract as gifLoadBuf: the seed goroutine
	// sends exactly once, tickGifSeed drains it every frame, and a Cancel that nils
	// a.gif leaves the send in a buffer nobody polls (the goroutine dies).
	gifSeedBuf = 1
)

// exportResult is one finished export delivered back to the UI thread: the
// toast line plus — on success — the artifact's path, so the #71 clipboard-copy
// hook (and any future "reveal in folder") knows what was produced.
type exportResult struct {
	msg  string
	path string // "" on failure
}

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

// maxVideoHours is the wedge-brake for a single video export. Unlike the GIF cap
// (bounded by the HELD paletted-frame memory budget), video frames stream straight to
// ffmpeg and are never retained (V1's streaming verdict — memory stays flat regardless
// of length), so this is NOT a memory bound and must not bound the user's film: hours
// of footage cost nothing but disk. The brake exists solely to stop a WEDGED exporter
// (a scene stuck feeding frames forever) from encoding without end. 24 h is generous
// headroom over any real export while keeping the cap a named, finite §17.4 ceiling.
const maxVideoHours = 24

// videoMaxFrames derives the per-export video frame cap from the export's CHOSEN fps,
// so the brake scales with cadence (a 24 h film is 24 h whether it's 8 or 30 fps). At
// the config clamp's true ceiling (maxExportFPS=30 in internal/config — the UI presets
// top out at 24, but the stored value may be anything in [6,30]) that is
// 24*3600*30 = 2,592,000 frames; the frame counter and every cue offset are computed
// multiply-first (frameToMs) and stay well within int64, so millions of frames carry
// no overflow or drift. Pure + headless-testable.
func videoMaxFrames(fps int) int {
	if fps < 1 {
		fps = 1
	}
	return maxVideoHours * 3600 * fps
}

// gifResultBuf is the buffer of the off-thread export → UI result channel (hard
// rule §17.4: every channel has a named cap). One slot is enough: at most one
// export runs at a time and pollGifResult drains it every frame, so the finish
// goroutine blocks ≤1 frame on the handoff. (comicexport.go writes it too, but
// only ever for the single in-flight export.)
const gifResultBuf = 1

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
func (a *App) drawExportOptions(x, y int32, withSpeed bool) int32 {
	c := a.ctx
	// Shared by the settings Studio tab and the scene maker; align to the
	// caller's content origin so the rows + sliders land in the right column.
	a.formX = x
	pad := a.formX
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

	// #69: subtitle sidecar for the video export (cue-timed to the video itself).
	if next := c.Checkbox(pad, y, "Write subtitles beside the video (.srt + .vtt, speaker + line)", opts.Subtitles); next != opts.Subtitles {
		opts.Subtitles = next
		a.d.Prefs.SetExportOpts(opts)
	}
	y += 28

	// #71: put the finished export FILE on the clipboard (Windows) — paste it
	// straight into Discord without opening the recordings folder.
	if next := c.Checkbox(pad, y, "Copy the finished file to the clipboard (paste straight into Discord — Windows)", opts.CopyToClipboard); next != opts.CopyToClipboard {
		opts.CopyToClipboard = next
		a.d.Prefs.SetExportOpts(opts)
	}
	y += 28

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

	// #74 watermark: a small corner credit on GIF/WebP/Video exports (not the comic).
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
	audioCap  *audioCapture   // Video only — records the scene's music + SFX for the audio mux (#99)

	// Video only — #69 subtitle cues, tracked while capturing (frame-timed so
	// they match the video exactly). subPend waits for its text to become
	// visible; subOpen is on screen now; subs holds the closed cues.
	subsOn     bool
	subs       []subCue
	subPend    subCue
	subHasPend bool
	subOpen    subCue
	subHasOpen bool

	// Per-export output settings (from config ExportOptions, resolved once).
	w, h      int32         // capture/output size (4:3)
	fps       int           // chosen frame rate; drives frameToMs cue offsets (multiply-first)
	frameDt   time.Duration // room Update step per captured frame (= 1/fps)
	delayCs   int           // GIF per-frame delay, centiseconds
	loop      bool          // loop the animation forever vs play once
	maxFrames int           // frame cap for this size (memory-budgeted)
	stamp     string        // #74 watermark text ("" = off), resolved once at start

	// Conversation chatbox raster, rebuilt per message and revealed rune-by-rune
	// (so the GIF shows people talking). Self-cached here, never the live a.msRaster.
	chatRaster *render.MessageRaster
	chatText   string
	chatPct    int // export chat-font scale (fitted to the capture size, not live zoom)

	// Asset pre-warm phase: prefetch the scene's sprites/backgrounds and wait for
	// T1 residency before the first capture, so characters are on stage.
	warming      bool
	warmRefs     []courtroom.AssetRef // the FULL set (M in the overlay + residency counting)
	warmCreated  time.Time            // job-creation wall clock: anchors gifWarmHardCap (never-resident-ref backstop)
	warmStarted  time.Time
	warmLastGain time.Time
	warmBest     int // most assets seen resident so far (quiescence detector)
	warmResident int // resident count from the last warm tick (for the overlay)

	// Incremental warm submission (freeze fix): pendingWarm holds refs not yet
	// handed to the pool; tickGifWarm drains a bounded batch per tick so the
	// producer never blocks on a full high lane. warmInFlight is the set of
	// SUBMITTED bases that are not yet T1-resident — the true high-lane occupancy
	// this phase contributes; len(warmInFlight) is the outstanding window (kept <
	// the lane cap) and each tick prunes bases that have become resident so room
	// reopens as fetches land. It self-bounds: once it reaches warmInFlightWindow,
	// room is 0 and submission stops, so the set never exceeds that named cap. It
	// counts ONLY real submits (a pre-resident, never-submitted ref is never in it),
	// which is why pre-resident refs can't deflate the window and re-open the block.
	// warmSubmitted is a cumulative count of real submits (observability + the
	// overlay). warmSubmitDone latches once every ref has been submitted — only then
	// does the quiescence/timeout clock start (a huge ref list mustn't be
	// guillotined by gifWarmMax while it's still being fed to the pool).
	pendingWarm    []courtroom.AssetRef
	warmInFlight   map[string]struct{}
	warmSubmitted  int
	warmSubmitDone bool

	// Format-seed phase: BEFORE the warm, fetch <origin>/extensions.json and seed
	// the host's learned formats — exactly the session connect path — so the warm
	// (which feeds the RENDER path and therefore stays zero-fallback per the
	// pillar) probes the manifest-declared format instead of the wrong default. A
	// .demo's recorded origin is never the session origin, so without this the warm
	// walks [.webp] against a .gif/.png server and captures an empty stage. Gated
	// on the SAME http(s) + auto-detect + per-origin dedupe as the session path;
	// seedDone (1-buffered) delivers the outcome, drained by tickGifSeed. Where the
	// manifest didn't answer (no extensions.json, or it lacks a type), the warm
	// keeps its existing behavior — the demand pipeline + missingno handle the
	// stragglers — because full-chain-walking the warm would violate the pillar on
	// the render path. A prior content report / package of the SAME demo already
	// RecordSuccess-learned those formats (probeRef → ResolveRawFull), so a warm
	// right after a report is learned-first even for a no-manifest server.
	seeding      bool
	seedDone     chan seedResult
	seedOriginTo string // the origin being seeded (for WarmFromPrefs gating + the overlay)

	// Async load phase: the disk read + parse runs on ONE goroutine off the render
	// thread (a 30–60 MB .aorec or an all-day .demo froze the window otherwise).
	// loadCh (gifLoadBuf-buffered) delivers the parsed recording; the render thread
	// finishes the export on delivery. loadName is the file base for the overlay;
	// loadKind is the requested format. The channel is per-job identity: on Cancel
	// the job (and its channel) is dropped, so a late goroutine send lands in a
	// buffer nobody polls (never blocks — the goroutine finishes and dies).
	loading  bool
	loadCh   chan gifLoadResult
	loadName string
	loadPath string
	loadKind exportKind
}

// gifLoadResult is the disk-load goroutine's handoff to the render thread: the
// parsed recording plus the user-facing notes to emit ON the render thread
// (pushDebug / warnLine can't be touched off it). Populated purely off-thread.
type gifLoadResult struct {
	rec     *sceneRecording
	err     error
	debug   []string // skipped/truncated import notes (emitted via pushDebug)
	warnMsg string   // empty-origin honesty warning ("" = none), emitted via warnLine
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
	// WebP stores ONE integer per-frame duration in the container, so a rounded
	// constant is the best the format can do (unlike video/subtitle cue OFFSETS,
	// which we compute multiply-first via frameToMs to stay drift-free). Round to
	// nearest rather than truncate so 24 fps → 42 ms (≈41.67), not 41. WebP is
	// short-capped and has no audio track to desync against, so this is cosmetic.
	frameMs := (1000 + fps/2) / fps
	if frameMs < 1 {
		frameMs = 1
	}
	// Video streams to ffmpeg and never holds frames, so it isn't bound by the GIF
	// paletted-frame budget — let scene length drive it up to the duration wedge-brake
	// (24 h at the chosen fps), not the tiny memory cap GIF/WebP/Comic keep.
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
	// Audio sink per kind: GIF/WebP keep real audio (paced by their small per-frame
	// dt). A comic is a silent still page whose fast-forward would blast the whole
	// line's blips at once + rip through music, so it gets a no-op sink. A video is
	// silent at capture (export speed garbles live audio), but an audioCapture sink
	// records the scene's music + SFX so finishVideoExport can mux them in (#99).
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
	room := courtroom.NewCourtroom(courtroom.NewURLBuilder(scene.Origin), a.d.Manager, nil, audio)
	room.Typewriter.Interval, room.TextStay = a.replayTiming()
	room.CatchUp = false
	room.ReduceMotion = false // export the authored effects (screenshake/flash), not the viewer's accessibility pref
	room.ForceCharNames = a.d.Prefs.ForceCharNamesOn()
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

	// Pre-warm: enumerate every sprite / background / desk the scene needs; the
	// refs are SUBMITTED incrementally (tickGifWarm, a bounded batch per tick) so a
	// thousands-of-refs import can't fill the bounded high lane and block the render
	// thread — the freeze fix. tickGifWarm waits for them to decode into T1 before
	// capturing, otherwise the fast export outruns the async fetch and renders an
	// empty stage. Music refs (Exact) aren't textures. No PrefetchChain call fires
	// here — pendingWarm holds the whole list for the incremental submitter.
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
		// pendingWarm is the not-yet-submitted subset; tickGifWarm drains a bounded
		// batch per tick. The quiescence/timeout clock (warmStarted/warmLastGain) is
		// deliberately left ZERO here — it starts only when submission finishes
		// (warmSubmitDone), so a huge ref list isn't guillotined by gifWarmMax while
		// it is still being fed to the pool.
		pendingWarm: warmRefs,
	}
	a.gifExporting = true
	a.beginExportScaleBracket() // #77: exports render at native (100%) scale, not the live UI scale

	// Seed the recording's origin formats BEFORE the warm, so the warm probes the
	// manifest-declared format (see the seeding-phase field doc). Same http(s) +
	// auto-detect + per-origin dedupe as the session connect path; a non-http
	// origin (local:// mount) or auto-detect-off skips straight to warming, where
	// the demand pipeline + missingno still stage what resolves. tickGifSeed gates
	// the first warm tick on the result.
	if strings.HasPrefix(scene.Origin, "http") && a.d.Prefs.FormatAutoDetect() && !a.originSeeded(scene.Origin) {
		a.markOriginSeeded(scene.Origin)
		a.startExportSeed(a.gif, scene.Origin)
		a.warnLine = "Checking the server's format manifest…"
	} else {
		a.warnLine = "Loading scene assets…"
	}
	a.warnAt = now
}

// startExportSeed spawns the ONE bounded format-seed goroutine for an export job
// and flips it into the seeding phase (which tickGifExport gates ahead of the
// warm). Off-thread fetch + prefs seed; the render thread republishes the
// resolver table on delivery (tickGifSeed). Same leak-free 1-buffer contract as
// the loader. Render thread only.
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

// tickGifSeed polls the format-seed goroutine without blocking (render thread).
// On delivery it republishes the resolver table (so the warm's first candidate
// sees the seeded formats — WarmFromPrefs on the render thread, matching
// pollManifest discipline) and drops into the warm phase. Nothing to do until the
// goroutine sends. A Cancel that nils a.gif never reaches here (tickGifExport's
// j==nil guard fires and the buffered send is GC'd with the job).
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
		// Still seeding — the overlay's "Checking … manifest" branch keeps drawing.
	}
}

// beginExportScaleBracket pins the text device scale (#77) to 100 for the export
// session and remembers the live value; endExportScaleBracket restores it. Set
// ONCE per session (not per frame): the modal export doesn't redraw the live
// chrome underneath, and no scale-change event fires while it runs, so a single
// bracket holds. Idempotent (a second begin keeps the first saved value).
func (a *App) beginExportScaleBracket() {
	if a.exportSavedDevScale != 0 {
		return // already bracketed
	}
	a.exportSavedDevScale = int(a.ctx.textDevPct)
	a.ctx.SetTextDevScale(DefaultScalePct)
}

// endExportScaleBracket restores the live text device scale saved by
// beginExportScaleBracket. No-op when not bracketed, so finishers can call it
// unconditionally.
func (a *App) endExportScaleBracket() {
	if a.exportSavedDevScale == 0 {
		return
	}
	a.ctx.SetTextDevScale(a.exportSavedDevScale)
	a.exportSavedDevScale = 0
}

// tickGifExport captures a bounded batch of frames (render thread). Feeds the
// next event when the room goes idle (same idle-gating as replay), Updates by a
// FIXED dt (faster than real-time), renders into the offscreen target, and
// quantizes — dropping the RGBA at once so only paletted frames are retained.
func (a *App) tickGifExport() {
	j := a.gif
	if j == nil {
		a.gifExporting = false
		// Unreachable today (a.gif is set before a.gifExporting), but this abort path
		// must restore the live text scale like every other export exit or a future
		// refactor that makes it reachable strands the UI on 100% device fonts. end
		// is a no-op when the bracket isn't active, so calling it here is always safe.
		a.endExportScaleBracket()
		return
	}
	// Loading phase: the disk read + parse runs off-thread; poll its result without
	// blocking. This runs BEFORE the Viewport guard because loading needs neither
	// the viewport nor a capture target — only the goroutine + channel. On delivery,
	// finishLoadedExport runs the render-thread export tail (which DOES need them).
	if j.loading {
		a.tickGifLoad(j)
		return
	}
	if j.seeding {
		// Format-seed phase: an off-thread fetch + prefs seed, gated AHEAD of the
		// warm. Like loading it needs neither the viewport nor a capture target —
		// only the goroutine + channel — so it runs BEFORE the Viewport guard.
		a.tickGifSeed(j)
		return
	}
	if a.d.Viewport == nil {
		// No viewport to render into: end the (already past-loading) export cleanly,
		// restoring the text scale like every other exit. startSceneExport set the
		// bracket when it built this job, so it must be released here.
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
			a.d.Viewport.Render(a.ctx.Ren, &j.room.Scene, dst)
			a.drawGifChatbox(j, &j.room.Scene, dst) // composite the conversation over the scene
			a.drawExportStamp(j, dst)               // #74 opt-in watermark, top-right
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
		j.subAnchor(j.room.Scene.VisibleRunes, j.captured) // #69: cue starts on the first frame that shows text
		j.captured++
	}
}

// tickGifLoad polls the off-thread disk-load result without blocking (render
// thread). Nothing to do until the loader sends; on an error the export exits
// cleanly, and on success finishLoadedExport runs the render-thread export tail.
// The loading shell job holds no capture target or encoder yet, so an error
// teardown here can't leak either — it just clears the export flags (no
// endExportScaleBracket needed: loading never set the bracket).
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
		// Still loading — the overlay's "Reading …" branch keeps drawing.
	}
}

// nextWarmBatch pulls up to `batch` refs off the FRONT of pending that still need
// submitting, skipping any already resident (resident(base) == true — those are
// in T1, so re-submitting them would waste a lane slot). It returns the refs to
// submit now and the remaining pending tail. Pure (resident is injected) so the
// per-tick submission cadence unit-tests without a live Store. A non-positive
// batch submits nothing (the caller's in-flight window has no room this tick).
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

// warmRoom is how many more refs the pre-warm phase may submit THIS tick without
// risking a full high lane, given the current OUTSTANDING count (submitted bases
// not yet resident — see warmInFlight). The key invariant is that `outstanding`
// counts only refs THIS phase actually submitted: a ref can be resident without
// ever having been submitted (a warm cache — re-export, export-after-view, or
// assets already in T1 from the live session), and such refs must NOT deflate the
// window, or room re-opens and true in-flight ratchets past highLaneCap until the
// next Submit blocks the render thread (the freeze this whole path removes).
// Because warmInFlight only ever holds real submits, len(warmInFlight) is exactly
// that count and warmRoom is a plain clamp: window − outstanding, capped at the
// per-tick burst and floored at 0. Pure so it unit-tests without a Store.
func warmRoom(outstanding int) int {
	room := warmInFlightWindow - outstanding
	if room > warmPrefetchPerTick {
		room = warmPrefetchPerTick
	}
	if room < 0 {
		room = 0
	}
	return room
}

// tickGifWarm runs during the pre-warm phase. It (1) SUBMITS a bounded batch of
// pending refs to the pool this tick — never the whole list at once, so the
// bounded high lane can't fill and block the render thread (the freeze fix) — and
// (2) counts how many of the scene's assets are resident in T1 (the upload Pump
// fills it each frame), ending the wait once they're all ready, OR no new asset
// has landed for gifWarmQuiet, OR gifWarmMax elapses. The quiescence/timeout
// clock only STARTS once every ref has been submitted (warmSubmitDone), so a huge
// ref list isn't guillotined by the 6 s cap while it is still being fed in.
func (a *App) tickGifWarm(j *gifExportJob) {
	now := time.Now()

	resident := 0
	for _, r := range j.warmRefs {
		if a.d.Store.Contains(r.Base) {
			resident++
		}
	}
	j.warmResident = resident

	// Prune the in-flight set: a submitted base that has become T1-resident has left
	// the high lane, so it frees a window slot for a new submission. Counting only
	// SUBMITTED bases (never a pre-resident, never-submitted one) is what keeps the
	// warm-cache case from deflating the window and re-opening the block.
	for base := range j.warmInFlight {
		if a.d.Store.Contains(base) {
			delete(j.warmInFlight, base)
		}
	}

	// Incremental submission: bound the OUTSTANDING (= len(warmInFlight)) high-lane
	// occupancy to warmInFlightWindow so a Submit can never find a full lane and
	// block, and cap the per-tick burst at warmPrefetchPerTick. The set self-bounds:
	// room hits 0 at the window, so len(warmInFlight) never exceeds that named cap.
	if len(j.pendingWarm) > 0 {
		room := warmRoom(len(j.warmInFlight))
		submit, rest := nextWarmBatch(j.pendingWarm, room, a.d.Store.Contains)
		for _, r := range submit {
			if r.Base == "" {
				continue // prefetchChain drops an empty base — don't count a no-op submit
			}
			a.d.Manager.PrefetchChain(r.Base, r.Alts, r.Type, network.PriorityHigh) // AssetType: from SceneAssets
			if j.warmInFlight == nil {
				j.warmInFlight = make(map[string]struct{})
			}
			j.warmInFlight[r.Base] = struct{}{} // occupies a window slot until it lands (or the hard cap ends the phase)
			j.warmSubmitted++
		}
		j.pendingWarm = rest
	}
	if len(j.pendingWarm) == 0 && !j.warmSubmitDone {
		// Submission just finished: START the quiescence/timeout clock now (not at job
		// creation), so the phase can't time out while refs were still being fed in.
		j.warmSubmitDone = true
		j.warmStarted = now
		j.warmLastGain = now
		j.warmBest = resident
	}

	if resident > j.warmBest {
		j.warmBest = resident
		j.warmLastGain = now
	}
	// Absolute backstop (measured from job creation, so it holds even before
	// warmSubmitDone latches): if a scene's assets never become T1-resident — a
	// missing-origin imported .demo 404s every ref — outstanding pins at
	// warmInFlightWindow, room stays ≤0, pendingWarm never drains, warmSubmitDone
	// never latches, and the gifWarmMax clock (which only starts at warmSubmitDone)
	// never runs. Without this anchor the phase would hang forever on exactly the
	// 404-heavy scene this async path serves. Force the phase to end and capture
	// whatever loaded (may be nothing — an empty stage, same as gifWarmMax's cap).
	if !j.warmCreated.IsZero() && now.Sub(j.warmCreated) > gifWarmHardCap {
		j.warming = false
		a.warnLine = "Rendering…" // kind-neutral; the progress overlay names the format
		a.warnAt = now
		return
	}
	// The end conditions only apply once every ref has been submitted — otherwise a
	// warm-cache scene could quiesce before its later refs are even in the lane.
	if !j.warmSubmitDone {
		return
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
func (a *App) fitChatRaster(sc *courtroom.Scene, wrapW, vpH int32, pct int, comicInk bool) *render.MessageRaster {
	maxTextH := vpH*3/5 - gifChatNameRowH - 10
	if maxTextH < gifChatNameRowH {
		maxTextH = gifChatNameRowH
	}
	for attempt := 0; attempt < 5; attempt++ {
		r, err := renderRaster(a, sc, wrapW, false, pct, comicInk)
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
	r, _ := renderRaster(a, sc, wrapW, false, config.MinChatScalePercent, comicInk) // safety: show text at the floor
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
			j.chatRaster = a.fitChatRaster(sc, wrapW, vp.H, j.chatPct, false)
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
	// Same covering-face pick as the live nameplate, so a non-Latin showname renders
	// into the exported comic/video instead of tofu (ASCII names keep the chrome font).
	a.labelEmoji(c.ChatFontFor(DefaultScalePct, sc.ShownameText), c.EmojiFont(DefaultScalePct), box.X+8, box.Y+4, box.W-16, sc.ShownameText, nameCol)

	if j.chatRaster != nil {
		_ = c.Ren.SetClipRect(&box) // the cap case stays inside the box, never off-frame
		j.chatRaster.Draw(c.Ren, sc.VisibleRunes, box.X+8, box.Y+gifChatNameRowH)
		_ = c.Ren.SetClipRect(nil)
	}
}

// exportStamp resolves the #74 watermark text once per export: the user's custom
// text, or — blank — the recording's asset host + its recorded date. "" = off.
// A bundled archive's rewritten local origin (loopback) is skipped: stamping
// "127.0.0.1" credits nobody, so those fall back to the date alone.
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

// drawExportStamp paints the watermark in the capture's top-right (the chatbox
// owns the bottom band): a 1-px shadow pass keeps it readable over any scene.
// The stamp string is constant per export, so the label texture is cached after
// the first frame. Render thread only (inside the capture callback).
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

// finishGifExport tears down the capture, restores the viewport's preanim
// callback, and hands the frames to an off-thread encode+write.
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
			a.gifResultCh <- exportResult{msg: "WebP export failed: " + err.Error()}
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
	// Resolve the soundtrack NOW, on the render thread — the capture sink's cues are
	// complete and a.gif is about to be cleared. The cue functions place each cue via
	// frameToMs(frame, fps) (multiply-first, so an hours-long export doesn't drift; V1's
	// HEADLINE risk). The mux itself (download + ResolveRaw + 2nd ffmpeg) runs off-thread
	// below and degrades to the silent video on ANY failure (#99).
	var songs []songSegment
	var sfx []sfxPlacement
	audioDropped := 0
	if j.audioCap != nil {
		songs = j.audioCap.songSegments(j.fps, j.captured) // windows to the final frame count
		sfx = j.audioCap.sfxPlacements(j.fps)
		// Honesty note: cues are turned away at TWO cap sites — the per-list guard in
		// audioCapture (droppedAtCapture) and the combined maxAudioClips cap the mux
		// applies (music first, then SFX). Report both so a busy scene's silently-
		// trimmed soundtrack isn't a surprise (V1 found no user-visible signal here).
		offered := len(songs) + len(sfx)
		admitted := offered
		if admitted > maxAudioClips {
			admitted = maxAudioClips
		}
		audioDropped = j.audioCap.droppedAtCapture + (offered - admitted)
	}
	format := j.vidFormat
	mgr := a.d.Manager
	// #69: close the final cue at the last captured frame and hand the cue list to
	// the finisher goroutine (j is dead after this — the handoff owns the slice).
	j.subClose(j.captured)
	subs := j.subs
	subFps := j.fps
	a.warnLine = fmt.Sprintf("Finishing video (%d frames)…", j.captured)
	a.warnAt = time.Now()
	go func() {
		if err := enc.Finish(); err != nil {
			_ = os.Remove(path) // a failed encode leaves a corrupt file — don't keep it
			a.gifResultCh <- exportResult{msg: "Video export failed: " + err.Error()}
			return
		}
		// Mux FIRST so we know the final on-disk name: on a successful mux the video is
		// renamed to <stem>-audio.<ext> and the silent original deleted (videomux.go).
		// Subtitles must land beside the FINAL file, so write the .srt/.vtt sidecars
		// against finalPath afterwards — on the silent-fallback path finalPath stays the
		// original, so the sidecars are named exactly as before (#69).
		finalPath, soundNote := path, ""
		if len(songs) > 0 || len(sfx) > 0 {
			if muxed, ok := muxSceneAudio(mgr, path, songs, sfx, format); ok {
				finalPath, soundNote = muxed, "  ♪ with sound"
				// Factual note when the 64-clip cap trimmed the soundtrack (short, no
				// exclamation — settings-text style): so an hours-long, SFX-heavy scene
				// isn't silently missing most of its sound with no word to the user. Only
				// on the success branch — the silent-fallback message below already says
				// there's no sound, so a "N cues left out" tail there would just confuse.
				// "left out", not "past the cap": failed downloads don't consume cap
				// slots, so this count can include resolve failures — keep the wording
				// directional rather than claiming a precise cause (review nit).
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
		a.gifResultCh <- exportResult{msg: videoSavedMsg(finalPath) + soundNote + subNote, path: finalPath}
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

// pollGifExport delivers the off-thread encode result to the UI — and, when the
// #71 opt-in is on and the export succeeded, puts the finished FILE on the OS
// clipboard so it pastes straight into Discord/Explorer.
func (a *App) pollGifExport() {
	if a.gifResultCh == nil {
		return
	}
	select {
	case res := <-a.gifResultCh:
		a.warnLine = res.msg
		if res.path != "" && a.d.Prefs.ExportOpts().CopyToClipboard {
			if copyFileToClipboard(res.path) {
				a.warnLine += "  📋 on the clipboard"
			}
		}
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

	// Loading phase: the disk read + parse runs off-thread. An always-alive overlay
	// (this is the whole point of the fix — no more frozen window with no screen)
	// with a Cancel that abandons the pending result. Cancelling drops the job: the
	// loader goroutine's send lands in a buffer nobody polls, so it finishes and
	// dies (never leaks blocked on send — gifLoadBuf guarantees the buffer accepts).
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

	// Format-seed phase: fetching the server's extensions.json before the warm.
	// A brief, network-bounded step; a plain spinner-less note with a Cancel that
	// abandons the pending seed result (the goroutine finishes into a dead buffer).
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
