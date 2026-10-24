package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

// selState is App's "IC/OOC log text selection" banner group lifted into a named component: the fields
// used to sit flat on App, readable at depth 1 from every ui file. They are
// reached as a.sel.<field> now.
type selState struct {
	// Anchored to (wrapped-line index, rune offset in that line); see
	// logselect.go. logSelWhich names the owning log so only it highlights/
	// copies; logSelPressed is the once-per-frame press edge (both logs read
	// the same value, so whichever draws first can't steal the arm).
	logSelWhich    int
	logSelActive   bool
	logSelDragging bool
	logSelAnchor   selPoint
	logSelHead     selPoint
	logSelPressed  bool
	logSelPrevDown bool
	logSelFill     sdl.Color // configured highlight colour, cached per frame
	// Chatbox (in-viewport IC message) selection: drag across the message to
	// highlight the RANGE you want (the raster exposes per-line rune geometry
	// now — RuneAt/LineSpanX), double-click a word, triple-click the whole
	// message; Ctrl+C / right-click copies just the selection. chatSelA/B are
	// source-rune boundaries (anchor/head). Only the animated-text path
	// (msAnim, per-glyph motion) keeps the old whole-message behavior.
	chatSelActive   bool
	chatSelDragging bool
	chatSelPrevDown bool
	chatSelDownX    int32
	chatSelDownY    int32
	chatSelA        int
	chatSelB        int
	// Highlight-colour picker (Settings): the hue/sat wheel texture (built once)
	// and the in-progress hex field text.
	colorWheel     *sdl.Texture
	colorHex       string
	chromeEditSlot int    // which custom-chrome kit colour the wheel edits (0..6)
	chromeHexBuf   string // in-progress hex for the custom-chrome wheel's hex field
	// Mayo mascot portrait on the About page: a high-quality Catmull-Rom downscale
	// of the embedded art, uploaded once (lazily, render thread). Baked to the exact
	// PHYSICAL pixel size (logical × UI scale) so it draws 1:1 — crisp at any scale —
	// and rebuilt only when the UI scale changes. mayoTexFailed latches a failed
	// decode/upload so it isn't retried every frame. Zero per-frame cost. (mascot.go)
	mayoTex            *sdl.Texture
	mayoLogW, mayoLogH int32 // on-screen (logical) size; the texture itself is physical px
	mayoTexScale       int   // UIScale() the texture was baked at
	mayoTexFailed      bool
	// #234 pet-the-gopher easter egg: clicking the About portrait "pets" Mayo.
	// mayoPets is the session pet count; mayoPetAt drives the wiggle + floating
	// caption. Session-only — a bit of joy that resets each launch.
	mayoPets  int
	mayoPetAt time.Time
	// #234 "…and all of that": each pet also launches a bouncing, spinning Mayo
	// that roams the About screen DVD-logo style. Bounded pile; mayoRoamAt stamps
	// the last update so the motion is frame-rate independent.
	mayoRoamers []mayoRoamer
	mayoRoamAt  time.Time
	// spriteTintHex is the in-progress hex text for the solid sprite-tint field
	// (Settings); reflected from the pref when the field isn't focused.
	spriteTintHex string
	// lastOSToast rate-limits friend desktop toasts (osToastMinInterval).
	lastOSToast time.Time
	// lastModSFX debounces the mod-command feedback sounds per action (#60),
	// indexed by render.ModAction — collapses the OOC actor-confirm + area
	// broadcast burst into a single play. See modsfx.go.
	lastModSFX [3]time.Time
	// Scene recording (M16, see replay.go): when recActive, the event loop taps
	// the scene-mutating events into rec; recStart anchors the relative offsets.
	recActive bool
	rec       *sceneRecording
	recStart  time.Time
	// Instant replay (rolling clip buffer, see replay.go): when the opt-in pref is
	// on, every scene-mutating event is also stamped into replayBuf — a bounded
	// ring — so the clip key can save the last window WITHOUT a prior recording.
	// Allocated lazily on first capture, nilled when disabled or the origin changes
	// (a clip must never mix two servers' assets). replayBufW = next write slot,
	// replayBufN = entries held (<= cap), replayBufOrigin = the CDN they came from.
	replayBuf       []replayBufEntry
	replayBufW      int
	replayBufN      int
	replayBufOrigin string
	// Scene replay (M16 [2/2], see replay.go): while replaying, a throwaway
	// replayRoom (pointed at the recorded asset origin) is driven instead of the
	// live room and the viewport/renderScene read its scene. The driver feeds
	// replayEvents[replayIdx] into it whenever it returns to idle.
	replaying    bool
	replayRoom   *courtroom.Courtroom
	replayEvents []recEvent
	replayIdx    int
	replayName   string
	replayPaused bool            // player ⏸ — freeze the scene (Next/Restart still work)
	replayRec    *sceneRecording // the source, kept so ⏮ Restart can rebuild from the top
	// #70 auto-chapters: the jump list derived once per replay (bg/music/shout
	// beats) and whether its panel is open in the overlay player.
	replayChapters     []replayChapter
	replayChaptersOpen bool
	// H5 replay pre-warm: a FLAT (non-bundled) recording streams its sprites +
	// music from the origin, so a bounded warm phase loads them BEFORE the first
	// event plays — nothing pops in or plays silent. Non-nil only while that phase
	// runs (allocated in startReplay, cleared when it ends or the replay stops). All
	// its state + tick logic lives in replaywarm.go; see replayWarmState.
	replayWarm *replayWarmState
	// Scene maker (M16 [3/3], see scenemaker.go): an in-app editor over the SAME
	// .aorec model — build a scene from scratch or edit a recording, preview it
	// through the replay engine, and Save a new .aorec. All state lives here and
	// is allocated only while makerOpen, so the maker costs nothing on the live
	// render path (it's drawn as a full-window overlay, like a replay).
	makerOpen       bool
	makerScene      *sceneRecording
	makerSel        int    // selected event index
	makerName       string // working filename stem (sanitized on Save)
	makerScroll     int32  // event-list scroll offset (px)
	makerPickerOpen bool   // the in-maker "Open a recording" list is showing
	makerExportOpen bool   // the in-maker "⚙ Export options" panel is showing
	makerTrimStart  int    // crop In point (event index), -1 = scene start
	makerTrimEnd    int    // crop Out point (event index), -1 = scene end
	// Timeline strip (#75): a proportional film-strip of the scene events, widths
	// from the recorded OffsetMs pacing. Reused per-frame layout buffers (segX/W
	// in px, dur in ms) so the maker draw stays alloc-free; horizontal scroll;
	// makerDragHandle is which crop handle is being dragged (0 none, 1 In, 2 Out);
	// makerPrevDown carries the mouse-press edge for grabbing a handle.
	makerSegX       []int32
	makerSegW       []int32
	makerTLDur      []float64
	makerTLScroll   int32
	makerDragHandle int
	makerPrevDown   bool
	makerDragSeg    int
	makerDragMoved  bool
	makerDragX      int32
	makerScrub      bool // #68: dragging the timeline's scrub lane (selection follows the cursor)
	// makerPreviewRoom is a throwaway courtroom that renders the selected line
	// into the maker's live preview pane (the "studio" WYSIWYG). makerPreviewIdx
	// is the line it currently reflects, so the pane is rebuilt only when the
	// selection/scene changes. Driven by driveMakerPreview while makerOpen.
	makerPreviewRoom *courtroom.Courtroom
	makerPreviewIdx  int
	makerPreviewOrig string          // origin the preview room was built for (rebuild on change)
	makerPreviewKey  makerPreviewKey // visual identity of the previewed line (rebuild on change)
	// Self-contained archive (CDN-proof): export bundles a scene's assets into a
	// folder; replayBundled marks a replay whose assets stream from that folder
	// (Manager archive-source override) instead of the origin.
	replayBundled  bool
	makerExporting bool
	makerExportCh  chan string // archive-export goroutine → UI (result line)
	// Scene GIF export (gifexport.go): renders a scene offscreen into paletted
	// frames across several frame-loop ticks, then encodes off-thread. Allocated
	// only while exporting; zero cost on the live path otherwise.
	gifExporting bool
	// exportSavedDevScale remembers the live text device scale (#77) while an
	// export session brackets ctx.textDevPct to 100 (native offscreen resolution —
	// the live UI scale must never leak into export pixels). 0 = not exporting;
	// set at export start, restored (and zeroed) at every finisher. gif/webp/video/
	// comic all render offscreen and share this bracket.
	exportSavedDevScale int
	gif                 *gifExportJob
	gifResultCh         chan exportResult // off-thread encode → UI (result line + artifact path)
	// Content report / "Package this RP" (contentjob.go): enumerate a recording's
	// assets, probe found/missing at the origin (bounded off-thread workers,
	// render-thread polled), then optionally package the found bytes into a
	// self-contained bundle folder. Single-flight (contentBusy); contentGen
	// invalidates a cancelled job's late goroutine sends. Allocated only while a
	// job runs — nil, zero-cost, otherwise.
	content     *contentJob
	contentBusy bool
	contentGen  int
}
