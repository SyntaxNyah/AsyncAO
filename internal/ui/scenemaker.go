package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/archive"
	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Scene maker: an in-app editor over the same .aorec model the recorder/replayer use. It previews through the
// existing replay engine and saves a fresh .aorec, so there is no second renderer or asset path. It is a full-window
// overlay drawn only while makerOpen, so it costs nothing on the live render path.

const (
	// defaultMakerEmote is the starter sprite stem for a new line: the bare form, which the prefetch fallback chain
	// probes as both "(a)normal" and "normal" (AO2-Client CharLayer::load_image tries both).
	defaultMakerEmote = "normal"
	// defaultMakerSide centres a new speaker on the witness stand.
	defaultMakerSide = "wit"

	makerListW int32 = 300 // left event-list column width
	makerRowH  int32 = 24  // event-list row height

	// makerCharSuggest caps the character-autocomplete chips; the scan stops once this many matches are found.
	makerCharSuggest = 6
)

// makerSideLabels / makerSideValues map friendly position names to the AO side codes (urlbuilder.go positionFolder).
var (
	makerSideLabels = []string{"Defense", "Prosecution", "Witness", "Judge", "Defense (hold)", "Prosecution (hold)", "Jury", "Seance"}
	makerSideValues = []string{"def", "pro", "wit", "jud", "hld", "hlp", "jur", "sea"}
)

func makerSideIndex(side string) int {
	for i, v := range makerSideValues {
		if v == side {
			return i
		}
	}
	return 2 // default: witness
}

// newMessageEvent builds a synthetic IC line. EmoteMod defaults to idle, not preanim: a from-scratch line must not
// wait on a preanim asset that may not exist at the origin, which would stall the preview.
func newMessageEvent(char, emote, text string) recEvent {
	if strings.TrimSpace(emote) == "" {
		emote = defaultMakerEmote
	}
	return recEvent{
		Kind: int(courtroom.EventMessage),
		Message: &protocol.ChatMessage{
			CharName: char,
			Emote:    emote,
			Message:  text,
			Side:     defaultMakerSide,
			EmoteMod: protocol.EmoteModIdle,
			DeskMod:  protocol.DeskShow, // stand behind the desk = grounded, natural AO framing (off = full-body character floating on the bg)
		},
	}
}

func newBackgroundEvent(name string) recEvent {
	return recEvent{Kind: int(courtroom.EventBackground), Text: name}
}

func newMusicEvent(track string) recEvent {
	return recEvent{Kind: int(courtroom.EventMusic), Text: track}
}

// sanitizeStem turns a user-typed scene name into a safe filename stem so Save cannot escape recordings\.
func sanitizeStem(s string) string {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), recordingExt))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = "scene"
	}
	return out
}

// cloneScene copies a scene (Events and each Message pointer) so edits and Preview never reach the original.
func cloneScene(rec *sceneRecording) *sceneRecording {
	if rec == nil {
		return &sceneRecording{Version: recordingVersion}
	}
	out := *rec
	out.Events = make([]recEvent, len(rec.Events))
	for i, e := range rec.Events {
		out.Events[i] = cloneEvent(e)
	}
	if out.Version == 0 {
		out.Version = recordingVersion
	}
	return &out
}

// cloneEvent deep-copies one event, duplicating the Message pointer.
func cloneEvent(e recEvent) recEvent {
	if e.Message != nil {
		m := *e.Message
		e.Message = &m
	}
	return e
}

// openSceneMaker shows the maker editing a clone of rec. Recording and replay are mutually exclusive with it.
func (a *App) openSceneMaker(rec *sceneRecording, name string) {
	// This runs in a Settings/picker click handler that is not recover-wrapped (recoverMaker only guards the maker's
	// own draw), so catch and log a panic here instead of crashing without a log.
	defer func() {
		if r := recover(); r != nil {
			a.warnLine = a.logMakerCrash(r)
			a.warnAt = time.Now()
			a.makerOpen = false
			a.makerScene = nil
		}
	}()
	if a.recActive {
		a.warnLine = "Stop recording first, then open the Scene Maker."
		a.warnAt = time.Now()
		return
	}
	if a.replaying {
		a.stopReplay()
	}
	a.makerScene = cloneScene(rec)
	a.makerName = sanitizeStem(name)
	a.makerSel = 0
	a.makerScroll = 0
	a.makerPickerOpen = false
	a.makerExportOpen = false
	a.makerTrimStart = -1 // no crop until the user sets In/Out
	a.makerTrimEnd = -1
	a.makerDragSeg = -1    // no reorder drag in progress
	a.makerPreviewIdx = -1 // force the preview pane to rebuild for the new scene
	a.makerOpen = true
	a.ensureBgList() // warm the background list once so the BG picker has suggestions
}

// newScene opens the maker on a fresh one-line scene seeded with the connected server's origin and current character.
func (a *App) newScene() {
	origin := a.urls.Origin() // the connected server's asset base, "" when offline
	bg := ""
	if a.sess != nil {
		bg = a.sess.Background // seed the live background so Preview lands on a real scene (with a desk), not a character floating on black
	}
	rec := &sceneRecording{
		Version: recordingVersion,
		Origin:  origin,
		StartBg: bg,
		Events:  []recEvent{newMessageEvent(a.activeCharName(), defaultMakerEmote, "Hello!")},
	}
	a.openSceneMaker(rec, "untitled")
}

// editRecordingInMaker loads a saved .aorec into the maker for editing.
func (a *App) editRecordingInMaker(path string) {
	rec, err := a.loadRecordingAny(path) // .aorec, or an AO2 .demo converted on the fly
	if err != nil {
		a.warnLine = "Couldn't open recording: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	stem := strings.TrimSuffix(filepath.Base(path), recordingExt)
	stem = strings.TrimSuffix(stem, demoExt)
	a.openSceneMaker(rec, stem)
}

// closeSceneMaker hides the maker, frees its buffer and tears down the preview room, restoring the viewport's
// preanim callback to the live room.
func (a *App) closeSceneMaker() {
	a.makerOpen = false
	a.makerScene = nil
	a.makerSel = 0
	a.makerScroll = 0
	a.makerPickerOpen = false
	a.makerExportOpen = false
	a.makerTrimStart = -1
	a.makerTrimEnd = -1
	a.makerDragSeg = -1
	a.makerDragMoved = false
	a.teardownMakerPreview()
}

// makerPreviewKey is the visual identity of the previewed line, everything that changes the pane except the message
// text, so typing does not rebuild on every keystroke. Compared field-by-field, alloc-free.
type makerPreviewKey struct {
	bg, char, emote, side, pre string
	emoteMod, deskMod, kind    int
	offX, offY                 int
	flip, shake, flash         bool
}

// makerPreviewKeyFor computes the key for the selected event: the effective background (its own, else the most
// recent earlier BG, else StartBg) plus the message's visual fields.
func (a *App) makerPreviewKeyFor(sel int) makerPreviewKey {
	rec := a.makerScene
	e := rec.Events[sel]
	k := makerPreviewKey{kind: e.Kind}
	if courtroom.EventKind(e.Kind) == courtroom.EventBackground {
		k.bg = e.Text
	} else {
		k.bg = rec.StartBg
		for j := sel - 1; j >= 0; j-- {
			if courtroom.EventKind(rec.Events[j].Kind) == courtroom.EventBackground {
				k.bg = rec.Events[j].Text
				break
			}
		}
	}
	if m := e.Message; m != nil {
		k.char, k.emote, k.side, k.pre = m.CharName, m.Emote, m.Side, m.PreEmote
		k.emoteMod, k.deskMod, k.flip = m.EmoteMod, m.DeskMod, m.Flip
		k.offX, k.offY = m.SelfOffsetX, m.SelfOffsetY
		k.shake, k.flash = m.Screenshake, m.Realization // so the pane replays when an effect is toggled
	}
	return k
}

// ensureMakerPreview rebuilds the preview-pane room only when the origin, selection or the line's visual identity
// changed. It feeds the background context, then the selected message.
func (a *App) ensureMakerPreview() {
	if a.makerScene == nil || a.d.Manager == nil {
		return
	}
	sel := a.makerSel
	if sel < 0 || sel >= len(a.makerScene.Events) {
		return
	}
	key := a.makerPreviewKeyFor(sel)
	if a.makerPreviewRoom != nil && a.makerPreviewOrig == a.makerScene.Origin &&
		a.makerPreviewIdx == sel && a.makerPreviewKey == key {
		return // unchanged — keep playing the current preview
	}
	// Construction is wiring (newroom.go): preview speakers blip, skin and flash like live, under the author's own
	// visual prefs, minus the effect gates overridden below.
	room := a.newRoom(courtroom.NewURLBuilder(a.makerScene.Origin), nil, a.d.Audio)
	room.Typewriter.Interval, room.TextStay = a.replayTiming()
	room.CatchUp = false
	// Post-construction override (newroom.go): the pane shows screenshake/flash/overlays and recorded sprite styles
	// even with reduce-motion, screen effects or sprite styles off, so the preview matches the export.
	a.applyAuthoringVisualsToRoom(room)
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = room.NotifyPreanimDone
		a.d.Viewport.OnFrameShown = room.NotifyFrameShown // #17: frame effects follow the preview room's sprite
	}
	if key.bg != "" {
		room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: key.bg})
	}
	if e := a.makerScene.Events[sel]; courtroom.EventKind(e.Kind) == courtroom.EventMessage && e.Message != nil {
		room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: cloneEvent(e).Message}) // copy: the room must not alias the edit buffer
	}
	a.makerPreviewRoom = room
	a.makerPreviewOrig = a.makerScene.Origin
	a.makerPreviewIdx = sel
	a.makerPreviewKey = key
}

// driveMakerPreview advances the preview-pane room each frame, recovering from a bad scene or asset.
func (a *App) driveMakerPreview(dt time.Duration) {
	defer a.recoverMakerPreview()
	if a.makerPickerOpen { // the picker covers the body — no pane to drive
		return
	}
	a.ensureMakerPreview()
	if a.makerPreviewRoom == nil {
		return
	}
	if a.d.Viewport != nil {
		// Re-assert each frame: a full Preview (replay) rebinds this to the replay room and stopReplay
		// points it at the live room.
		a.d.Viewport.OnPreanimDone = a.makerPreviewRoom.NotifyPreanimDone
		a.d.Viewport.OnFrameShown = a.makerPreviewRoom.NotifyFrameShown // #17: same rebind for frame effects
		a.d.Viewport.SetSpriteFX(a.spriteFX())
	}
	a.makerPreviewRoom.Update(dt)
	a.applySpriteOverridesTo(a.makerPreviewRoom) // the preview must match what a real room would draw
	if a.d.Viewport != nil {
		a.d.Viewport.Update(&a.makerPreviewRoom.Scene, dt)
	}
}

// recoverMakerPreview turns a panic in the preview drive/render into a clean teardown and debug line.
func (a *App) recoverMakerPreview() {
	if r := recover(); r != nil {
		a.pushDebug("maker preview panic: " + fmt.Sprint(r))
		a.teardownMakerPreview()
	}
}

// recoverMaker turns a panic in the maker draw (list / editor / actions, which the preview's recover does not
// cover) into a debug line and a clean close.
func (a *App) recoverMaker() {
	if r := recover(); r != nil {
		a.warnLine = a.logMakerCrash(r)
		a.warnAt = time.Now()
		a.closeSceneMaker()
	}
}

// logMakerCrash records a recovered maker panic: a debug line plus the panic and stack in
// recordings\scene-maker-crash.log (synchronous, on the exceptional path). Returns the toast string to show.
func (a *App) logMakerCrash(r any) string {
	msg := fmt.Sprint(r)
	a.pushDebug("scene maker panic: " + msg)
	writeCrashLog("scene maker panic: ", r)
	return "Scene Maker error: " + msg + " — details saved to recordings\\scene-maker-crash.log"
}

// writeCrashLog best-effort writes a panic and stack to recordings\scene-maker-crash.log. It touches only
// os/debug, so it is safe from any goroutine (the background bg-list fetch logs here directly).
func writeCrashLog(header string, r any) {
	dir := recordingsDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "scene-maker-crash.log"),
		[]byte(header+fmt.Sprint(r)+"\n\n"+string(debug.Stack())), 0o644)
}

// drawMakerPreviewPane renders the selected line into a 4:3 stage, the WYSIWYG view.
func (a *App) drawMakerPreviewPane(x, y, w, h int32) {
	defer a.recoverMakerPreview()
	c := a.ctx
	c.Label(x, y, "Live preview — selected line:", ColTextDim)
	y += 22
	stageW := w
	stageH := stageW * 3 / 4
	if stageH > h-44 {
		stageH = h - 44
		stageW = stageH * 4 / 3
	}
	stage := sdl.Rect{X: x + (w-stageW)/2, Y: y, W: stageW, H: stageH}
	c.Fill(stage, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	if a.makerPreviewRoom == nil || a.d.Viewport == nil {
		c.Label(stage.X+10, stage.Y+10, "Set an Origin/CDN above to preview.", ColTextDim)
		return
	}
	a.d.Viewport.Render(c.Ren, &a.makerPreviewRoom.Scene, stage)
	sc := &a.makerPreviewRoom.Scene
	if sc.ShownameText != "" || sc.MessageText != "" {
		line := strings.TrimSpace(sc.ShownameText + ": " + sc.MessageText)
		c.LabelClippedFont(c.LogFontFor(DefaultScalePct, line), stage.X, stage.Y+stage.H+4, stage.W, line, ColText) // covering face for non-Latin previews
	}
}

// teardownMakerPreview disposes the preview-pane room and restores the viewport's preanim callback.
func (a *App) teardownMakerPreview() {
	a.makerPreviewRoom = nil
	a.makerPreviewOrig = ""
	a.makerPreviewIdx = -1
	if a.d.Viewport != nil {
		if a.room != nil {
			a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
			a.d.Viewport.OnFrameShown = a.room.NotifyFrameShown // #17: frame effects back to the live room
		} else {
			a.d.Viewport.OnPreanimDone = nil
			a.d.Viewport.OnFrameShown = nil
		}
	}
}

// makerInsert adds an event just after the selection (bounded by the recording cap) and selects it.
func (a *App) makerInsert(ev recEvent) {
	if a.makerScene == nil {
		return
	}
	if len(a.makerScene.Events) >= maxRecordedEvents {
		a.warnLine = "Scene is at the event cap (" + fmt.Sprint(maxRecordedEvents) + ")."
		a.warnAt = time.Now()
		return
	}
	at := a.makerSel + 1
	if at < 0 || at > len(a.makerScene.Events) {
		at = len(a.makerScene.Events)
	}
	evs := append(a.makerScene.Events, recEvent{})
	copy(evs[at+1:], evs[at:])
	evs[at] = ev
	a.makerScene.Events = evs
	a.makerSel = at
}

// makerDuplicateSel clones the selected event right after it.
func (a *App) makerDuplicateSel() {
	if a.makerScene == nil {
		return
	}
	i := a.makerSel
	if i < 0 || i >= len(a.makerScene.Events) {
		return
	}
	a.makerInsert(cloneEvent(a.makerScene.Events[i]))
}

// makerNewLineSeed builds the event a fresh "+ Line" inserts. It inherits the speaker of the line it follows
// (char/emote/position/colour/flip/desk); the first line falls back to your current character.
func (a *App) makerNewLineSeed() recEvent {
	if a.makerScene != nil {
		i := a.makerSel
		if i >= 0 && i < len(a.makerScene.Events) {
			if m := a.makerScene.Events[i].Message; m != nil {
				return recEvent{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{
					CharName:  m.CharName,
					Emote:     m.Emote,
					Side:      m.Side,
					TextColor: m.TextColor,
					Flip:      m.Flip,
					DeskMod:   m.DeskMod,
					EmoteMod:  protocol.EmoteModIdle, // a fresh line, not a preanim
				}}
			}
		}
	}
	return newMessageEvent(a.activeCharName(), defaultMakerEmote, "")
}

// makerPreviewFrom previews the scene from idx, carrying the most recent earlier background so it is not blank.
func (a *App) makerPreviewFrom(idx int) {
	if a.makerScene == nil || idx < 0 || idx >= len(a.makerScene.Events) {
		return
	}
	sub := cloneScene(a.makerScene)
	for j := idx - 1; j >= 0; j-- { // last background change before idx becomes the start scene
		if courtroom.EventKind(sub.Events[j].Kind) == courtroom.EventBackground {
			sub.StartBg = sub.Events[j].Text
			break
		}
	}
	sub.Events = sub.Events[idx:]
	a.startReplay(sub, a.makerName+" (from line "+strconv.Itoa(idx+1)+")")
}

// trimActive reports whether a crop In or Out point is set.
func (a *App) trimActive() bool {
	return a.makerTrimStart >= 0 || a.makerTrimEnd >= 0
}

// trimRange resolves the In/Out points to a concrete in-bounds inclusive [start, end]: an unset or out-of-range
// end means "to the end", and an inverted range degrades to the whole scene.
func (a *App) trimRange() (int, int) {
	n := 0
	if a.makerScene != nil {
		n = len(a.makerScene.Events)
	}
	if n == 0 {
		return 0, -1
	}
	s, e := a.makerTrimStart, a.makerTrimEnd
	if s < 0 || s >= n {
		s = 0
	}
	if e < 0 || e >= n {
		e = n - 1
	}
	if s > e {
		s, e = 0, n-1
	}
	return s, e
}

// inTrim reports whether event i is inside the crop range (always true when no crop is set).
func (a *App) inTrim(i int) bool {
	if !a.trimActive() {
		return true
	}
	s, e := a.trimRange()
	return i >= s && i <= e
}

// trimmedScene returns a fresh clone of the scene clipped to the crop range, so Preview/export cannot mutate the
// edit buffer. StartBg carries the background in effect at the crop start; music before the crop is not carried.
func (a *App) trimmedScene() *sceneRecording {
	sub := cloneScene(a.makerScene)
	if !a.trimActive() || len(sub.Events) == 0 {
		return sub
	}
	s, e := a.trimRange()
	for j := s - 1; j >= 0; j-- { // last background change before the crop becomes the start scene
		if courtroom.EventKind(sub.Events[j].Kind) == courtroom.EventBackground {
			sub.StartBg = sub.Events[j].Text
			break
		}
	}
	sub.Events = sub.Events[s : e+1]
	return sub
}

// makerDeleteSel removes the selected event.
func (a *App) makerDeleteSel() {
	if a.makerScene == nil {
		return
	}
	i := a.makerSel
	if i < 0 || i >= len(a.makerScene.Events) {
		return
	}
	a.makerScene.Events = append(a.makerScene.Events[:i], a.makerScene.Events[i+1:]...)
	if a.makerSel >= len(a.makerScene.Events) {
		a.makerSel = len(a.makerScene.Events) - 1
	}
	if a.makerSel < 0 {
		a.makerSel = 0
	}
}

// makerMoveSel moves the selection one place in dir via the shared reorder path (makerMoveEvent bounds-checks dst).
func (a *App) makerMoveSel(dir int) {
	if a.makerScene == nil {
		return
	}
	a.makerMoveEvent(a.makerSel, a.makerSel+dir)
}

// reindexAfterMove maps index p to where it lands after the event at src moves to dst, so the selection and the
// crop In/Out follow their events.
func reindexAfterMove(p, src, dst int) int {
	switch {
	case p == src:
		return dst
	case src < dst && p > src && p <= dst:
		return p - 1
	case src > dst && p >= dst && p < src:
		return p + 1
	default:
		return p
	}
}

// makerMoveEvent moves the event at src to dst, reindexing the selection and crop In/Out so they keep pointing at
// the same events. The single reorder path for the buttons and the timeline drag.
func (a *App) makerMoveEvent(src, dst int) {
	if a.makerScene == nil {
		return
	}
	evs := a.makerScene.Events
	n := len(evs)
	if src < 0 || src >= n || dst < 0 || dst >= n || src == dst {
		return
	}
	ev := evs[src]
	if src < dst {
		copy(evs[src:dst], evs[src+1:dst+1]) // shift (src,dst] left one
	} else {
		copy(evs[dst+1:src+1], evs[dst:src]) // shift [dst,src) right one
	}
	evs[dst] = ev
	a.makerSel = reindexAfterMove(a.makerSel, src, dst)
	if a.makerTrimStart >= 0 {
		a.makerTrimStart = reindexAfterMove(a.makerTrimStart, src, dst)
	}
	if a.makerTrimEnd >= 0 {
		a.makerTrimEnd = reindexAfterMove(a.makerTrimEnd, src, dst)
	}
}

// makerPreview plays the scene through the replay engine on a copy; on Stop the maker reappears with the edit intact.
func (a *App) makerPreview() {
	if a.makerScene == nil || len(a.makerScene.Events) == 0 {
		a.warnLine = "Nothing to preview yet — add a line first."
		a.warnAt = time.Now()
		return
	}
	name := a.makerName + " (preview)"
	if a.trimActive() {
		s, e := a.trimRange()
		name = fmt.Sprintf("%s (crop %d–%d)", a.makerName, s+1, e+1)
	}
	a.startReplay(a.trimmedScene(), name) // honours the crop range
}

// makerSave writes the scene as a new timestamped .aorec, never overwriting the loaded file. The write is off the
// render thread (saveSceneRecording).
func (a *App) makerSave() {
	if a.makerScene == nil || len(a.makerScene.Events) == 0 {
		a.warnLine = "Nothing to save yet — add a line first."
		a.warnAt = time.Now()
		return
	}
	a.makerScene.Version = recordingVersion
	if a.makerScene.RecordedAt == "" {
		a.makerScene.RecordedAt = time.Now().Format(time.RFC3339)
	}
	stem := sanitizeStem(a.makerName) + "-" + time.Now().Format("20060102-150405")
	name, _, err := saveSceneRecording(a.makerScene, stem)
	if err != nil {
		a.warnLine = "Save failed: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	a.warnLine = "Scene saved: recordings\\" + name
	a.warnAt = time.Now()
}

// exportSceneArchive bundles the scene's assets into a self-contained folder so the .aorec keeps its visuals when
// the origin dies. It downloads through the live Manager, so it must run connected and off the UI thread.
func (a *App) exportSceneArchive(scene *sceneRecording, name string) {
	if a.makerExporting {
		return
	}
	if scene == nil || len(scene.Events) == 0 || strings.TrimSpace(scene.Origin) == "" {
		a.warnLine = "Set the Origin/CDN and add at least one line before exporting an archive."
		a.warnAt = time.Now()
		return
	}
	// A bundle is handed to someone else, so bundling art that exists only in the user's own folders must be said
	// first. Counting is a pure index lookup (no reads or fetches), so asking is free when there is nothing to ask.
	if n := a.packContribution(scene); n > 0 && !a.makerExportPackOK {
		a.makerExportPack = n
		a.makerExportScene, a.makerExportName = scene, name
		return
	}
	a.makerExportPackOK = false // one confirmation covers one export
	snap := cloneScene(scene)   // the goroutine must not race the edit buffer
	stem := sanitizeStem(name) + "-archive-" + time.Now().Format("20060102-150405")
	if a.makerExportCh == nil {
		a.makerExportCh = make(chan string, 1)
	}
	a.makerExporting = true
	a.warnLine = "Exporting self-contained archive… downloading assets (this can take a moment)."
	a.warnAt = time.Now()
	mgr := a.d.Manager
	ch := a.makerExportCh
	go func() { ch <- runArchiveExport(mgr, snap, stem) }()
}

// runArchiveExport (off-thread) writes every asset the scene needs into recordings\<stem>\ plus a bundled
// .aorec, and returns a status line for the UI.
func runArchiveExport(mgr *assets.Manager, scene *sceneRecording, stem string) string {
	dir := recordingsDir()
	if dir == "" {
		return "Export failed: no recordings folder."
	}
	destDir := filepath.Join(dir, stem)
	events := make([]courtroom.Event, len(scene.Events))
	for i, e := range scene.Events {
		events[i] = eventFromRec(e)
	}
	res, err := archive.ExportAssets(context.Background(), mgr, scene.Origin, scene.StartBg, events, destDir)
	if err != nil {
		return "Export failed: " + err.Error()
	}
	out := cloneScene(scene)
	out.Bundled = true
	out.Formats = res.Formats
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "Export failed: " + err.Error()
	}
	if err := os.WriteFile(filepath.Join(destDir, stem+recordingExt), data, 0o644); err != nil {
		return "Export failed: " + err.Error()
	}
	return fmt.Sprintf("Archive exported (plays without the CDN): recordings\\%s — %d files, %.1f MB.",
		stem, res.Files, float64(res.Bytes)/(1024*1024))
}

// pollMakerExport delivers the archive-export result back to the UI (called each frame from Update).
func (a *App) pollMakerExport() {
	if !a.makerExporting || a.makerExportCh == nil {
		return
	}
	select {
	case msg := <-a.makerExportCh:
		a.makerExporting = false
		a.warnLine = msg
		a.warnAt = time.Now()
	default:
	}
}

// eventSummary returns a short tag and one-line description for the event list. It is a display string, so the
// zero-width marker comes off (as icMessageBody does); the event keeps its literal Message, which replay needs to
// re-decode the sprite style.
func eventSummary(e recEvent) (tag, text string) {
	switch courtroom.EventKind(e.Kind) {
	case courtroom.EventMessage:
		tag = "MSG"
		if e.Message != nil {
			name := e.Message.CharName
			if e.Message.Showname != "" {
				name = e.Message.Showname
			}
			text = strings.TrimSpace(name + ": " + courtroom.StripSpriteStyle(e.Message.Message))
		}
	case courtroom.EventBackground:
		tag, text = "BG", e.Text
	case courtroom.EventMusic:
		tag, text = "MUS", e.Text
	default:
		tag = "?"
	}
	return tag, text
}

// drawSceneMaker paints the full-window editor overlay (dispatched instead of a screen while makerOpen, so it
// owns input): event list on the left, per-event editor on the right, name/origin/actions on top.
func (a *App) drawSceneMaker(winW, winH int32) {
	defer a.recoverMaker() // a draw panic closes the maker + names itself, never kills the app
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: winW, H: winH}, ColBackground)
	if a.makerScene == nil { // defensive: never draw an open maker with no scene
		a.newScene()
	}
	// Mouse-press edge for the timeline crop-handle grab, computed once per frame so a drag from elsewhere cannot grab.
	press := c.mouseDown && !a.makerPrevDown
	a.makerPrevDown = c.mouseDown

	// menuBarShows() is false for the maker, so the band below is just the server-tab strip's, which paints over the
	// maker (app.go draws it after the screen dispatch).
	y := a.topChromeH() + int32(8)
	c.Label(pad, y, "🎬 Scene Maker — build or edit a scene, then Preview / Save", ColText)
	if c.Button(sdl.Rect{X: winW - pad - 92, Y: y - 4, W: 92, H: btnH}, "✖ Close") {
		a.closeSceneMaker()
		return
	}
	y += 28

	// Name + Origin/CDN.
	c.Label(pad, y+5, "Name:", ColText)
	a.makerName, _ = c.TextField("mk_name", sdl.Rect{X: pad + 52, Y: y, W: 200, H: fieldH}, a.makerName, "scene name")
	c.Label(pad+266, y+5, "Origin/CDN:", ColText)
	originX := pad + 352
	originW := winW - originX - pad
	if originW < 160 {
		originW = 160
	}
	a.makerScene.Origin, _ = c.TextField("mk_origin", sdl.Rect{X: originX, Y: y, W: originW, H: fieldH}, a.makerScene.Origin, "https://your-cdn/base  — where characters/ and background/ live")
	y += 34

	// Actions.
	bx := pad
	if c.Button(sdl.Rect{X: bx, Y: y, W: 104, H: btnH}, "▶ Preview") {
		a.makerPreview()
		return // a Preview takes over the window this frame
	}
	bx += 112
	if c.Button(sdl.Rect{X: bx, Y: y, W: 88, H: btnH}, "📂 Open") {
		a.makerPickerOpen = !a.makerPickerOpen
		a.makerExportOpen = false
	}
	bx += 96
	if c.Button(sdl.Rect{X: bx, Y: y, W: 120, H: btnH}, "💾 Save .aorec") {
		a.makerSave()
	}
	bx += 128
	if c.Button(sdl.Rect{X: bx, Y: y, W: 96, H: btnH}, "⇄ .demo") {
		a.makerExportDemo() // AO2 interchange: plays in AO2's own demo player
	}
	c.Tooltip(sdl.Rect{X: bx, Y: y, W: 96, H: btnH}, "Export as an AO2 .demo — watchable in vanilla AO2 (backwards compatibility). AO2 demos also OPEN here (📂 Open / Settings → Studio).")
	bx += 104
	if a.makerExporting {
		c.Label(bx, y+6, "📦 exporting…", ColAccent)
	} else if c.Button(sdl.Rect{X: bx, Y: y, W: 136, H: btnH}, "📦 Export archive") {
		a.exportSceneArchive(a.makerScene, a.makerName) // CDN-proof: bundles the assets
	}
	bx += 144
	if c.Button(sdl.Rect{X: bx, Y: y, W: 120, H: btnH}, "🎞 Export GIF") {
		a.startSceneExport(a.trimmedScene(), a.makerName, exportGIF) // honours the crop range
	}
	bx += 128
	if c.Button(sdl.Rect{X: bx, Y: y, W: 104, H: btnH}, "🎬 WebP") {
		a.startSceneExport(a.trimmedScene(), a.makerName, exportWebP) // higher-quality animated WebP, cropped
	}
	bx += 112
	if c.Button(sdl.Rect{X: bx, Y: y, W: 104, H: btnH}, "🎥 Video") {
		a.startSceneExport(a.trimmedScene(), a.makerName, exportVideo) // MP4/WebM via ffmpeg (format in ⚙ Export)
	}
	bx += 112
	if c.Button(sdl.Rect{X: bx, Y: y, W: 110, H: btnH}, "🖼 Comic") {
		a.startSceneExport(a.trimmedScene(), a.makerName, exportComic) // PNG storyboard page (pure Go), cropped
	}
	bx += 118
	if c.Button(sdl.Rect{X: bx, Y: y, W: 104, H: btnH}, "⚙ Export") {
		a.makerExportOpen = !a.makerExportOpen // size / fps / quality / loop / speed
	}
	bx += 112
	if c.Button(sdl.Rect{X: bx, Y: y, W: 108, H: btnH}, "🆕 New scene") {
		a.newScene()
	}
	bx += 116
	if c.Button(sdl.Rect{X: bx, Y: y, W: 96, H: btnH}, "📁 Folder") {
		a.openRecordingsFolder()
	}
	y += btnH + 8
	c.Fill(sdl.Rect{X: pad, Y: y, W: winW - 2*pad, H: 1}, ColPanelHi)
	y += 8

	bodyY := y
	if a.makerPickerOpen { // the in-maker "Open a recording" list replaces the body
		a.drawMakerOpenPicker(pad, bodyY, winW-2*pad, winH-bodyY-pad)
		return
	}
	if a.makerExportOpen { // the in-maker export-options panel replaces the body
		a.drawMakerExportPanel(pad, bodyY)
		return
	}
	// Reserve the bottom strip for the timeline; the columns fill above it.
	tlY := winH - pad - makerTimelineH
	bodyH := tlY - bodyY - 10
	if bodyH < makerRowH*4 { // keep the columns usable on a short window
		bodyH = makerRowH * 4
	}
	a.drawMakerList(pad, bodyY, makerListW, bodyH)
	edX := pad + makerListW + 16
	edRight := winW - pad
	// Far-right live preview pane, when the window is wide enough to keep makerEditorMinW for the fields.
	const makerPaneW, makerEditorMinW = 380, 320
	if winW-edX-16-makerPaneW >= makerEditorMinW {
		paneX := winW - pad - makerPaneW
		a.drawMakerPreviewPane(paneX, bodyY, makerPaneW, bodyH)
		edRight = paneX - 16
	}
	a.drawMakerEditor(edX, bodyY, edRight-edX)
	a.drawMakerTimeline(pad, tlY, winW-2*pad, press)
}

// drawMakerOpenPicker lists saved recordings to load straight into the maker.
// drawMakerExportPanel is the in-maker export options body: the shared export controls plus guidance.
func (a *App) drawMakerExportPanel(x, y int32) {
	c := a.ctx
	c.Label(x, y, "⚙ Export options — apply to 🎞 GIF / 🎬 WebP (buttons above)", ColAccent)
	y += 30
	y = a.drawExportOptions(x, y, true)
	y += 10
	c.Label(x, y, "Bigger size / higher frame-rate = a larger file and a shorter GIF (WebP can run longer).", ColTextDim)
	y += 20
	c.Label(x, y, "Tip: screenshake and busy animated backgrounds bloat a GIF — every pixel changes each frame, so it can't compress.", ColTextDim)
}

func (a *App) drawMakerOpenPicker(x, y, w, h int32) {
	c := a.ctx
	recs := listRecordings()
	c.Label(x, y, fmt.Sprintf("Open a recording to edit (%d found) — click one, or Cancel:", len(recs)), ColText)
	if c.Button(sdl.Rect{X: x + w - 90, Y: y - 4, W: 90, H: btnH}, "Cancel") {
		a.makerPickerOpen = false
		return
	}
	y += 28
	if len(recs) == 0 {
		c.Label(x, y, "No recordings yet — record a scene (Ctrl+W) or build one with 🆕 New scene.", ColTextDim)
		return
	}
	panel := sdl.Rect{X: x, Y: y, W: w, H: h - 28}
	c.Fill(panel, ColPanel)
	rowH := int32(26)
	maxRows := (panel.H - 8) / rowH
	for i := range recs {
		if int32(i) >= maxRows {
			c.Label(x+8, y+int32(maxRows)*rowH+2, fmt.Sprintf("… and %d more (use the 📁 Folder button).", len(recs)-int(maxRows)), ColTextDim)
			break
		}
		row := sdl.Rect{X: x + 4, Y: y + 4 + int32(i)*rowH, W: w - 8, H: rowH - 2}
		if c.hovering(row) && c.clicked {
			a.editRecordingInMaker(recs[i].path) // loads + closes the picker (openSceneMaker)
		}
		if c.hovering(row) {
			c.Fill(row, ColPanelHi)
		}
		c.LabelClipped(row.X+8, row.Y+4, row.W-16, recs[i].name, ColText)
	}
}

// drawMakerList renders the scrollable event list and its add/reorder/delete toolbar.
func (a *App) drawMakerList(x, y, w, h int32) {
	c := a.ctx
	evs := a.makerScene.Events
	c.Label(x, y, fmt.Sprintf("Events (%d) — click to edit:", len(evs)), ColText)
	y += 24

	const toolH = 170 // reserved for the toolbar rows (add / reorder / preview / crop) below the list
	listH := h - toolH
	if listH < makerRowH*3 {
		listH = makerRowH * 3
	}
	listRect := sdl.Rect{X: x, Y: y, W: w, H: listH}
	c.Fill(listRect, ColPanel)

	contentH := int32(len(evs)) * makerRowH
	maxScroll := contentH - listH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if c.hovering(listRect) && c.wheelY != 0 {
		c.wheelTaken = true
		a.makerScroll -= int32(c.wheelY) * makerRowH
	}
	a.makerScroll = value.Clamp(a.makerScroll, 0, maxScroll)

	for i := range evs {
		ry := y + int32(i)*makerRowH - a.makerScroll
		if ry+makerRowH < y || ry > y+listH { // cull rows outside the list window
			continue
		}
		rowRect := sdl.Rect{X: x, Y: ry, W: w, H: makerRowH - 2}
		if i == a.makerSel {
			c.Fill(rowRect, ColPanelHi)
		}
		if c.hovering(rowRect) && c.clicked {
			a.makerSel = i
		}
		// Crop visuals: in-range rows get an accent stripe; excluded rows dim.
		tag, text := eventSummary(evs[i])
		tagCol, textCol := ColAccent, ColText
		if a.trimActive() {
			if a.inTrim(i) {
				c.Fill(sdl.Rect{X: x, Y: ry, W: 3, H: makerRowH - 2}, ColAccent)
			} else {
				c.Fill(rowRect, sdl.Color{R: 0, G: 0, B: 0, A: 110})
				tagCol, textCol = ColTextDim, ColTextDim
			}
		}
		c.Label(x+8, ry+4, fmt.Sprintf("%d", i+1), ColTextDim)
		c.Label(x+34, ry+4, tag, tagCol)
		c.LabelClipped(x+76, ry+4, w-82, text, textCol)
	}

	ty := y + listH + 8
	if c.Button(sdl.Rect{X: x, Y: ty, W: 62, H: btnH}, "+ Line") {
		a.makerInsert(a.makerNewLineSeed()) // inherits the previous speaker
	}
	if c.Button(sdl.Rect{X: x + 68, Y: ty, W: 56, H: btnH}, "+ BG") {
		a.makerInsert(newBackgroundEvent(""))
	}
	if c.Button(sdl.Rect{X: x + 130, Y: ty, W: 72, H: btnH}, "+ Music") {
		a.makerInsert(newMusicEvent(""))
	}
	ty += btnH + 6
	if c.Button(sdl.Rect{X: x, Y: ty, W: 40, H: btnH}, "▲") {
		a.makerMoveSel(-1)
	}
	if c.Button(sdl.Rect{X: x + 46, Y: ty, W: 40, H: btnH}, "▼") {
		a.makerMoveSel(1)
	}
	if c.Button(sdl.Rect{X: x + 92, Y: ty, W: 56, H: btnH}, "⎘ Dup") {
		a.makerDuplicateSel()
	}
	if c.Button(sdl.Rect{X: x + 154, Y: ty, W: 56, H: btnH}, "✖ Del") {
		a.makerDeleteSel()
	}
	ty += btnH + 6
	if c.Button(sdl.Rect{X: x, Y: ty, W: 184, H: btnH}, "▶ Preview from this line") {
		a.makerPreviewFrom(a.makerSel)
	}
	// Crop / trim: set In/Out around a span; Preview and Export use the range and excluded lines dim.
	ty += btnH + 6
	if c.Button(sdl.Rect{X: x, Y: ty, W: 76, H: btnH}, "⏮ Set In") {
		a.makerTrimStart = a.makerSel
		if a.makerTrimEnd >= 0 && a.makerTrimEnd < a.makerTrimStart {
			a.makerTrimEnd = -1 // Out before In is meaningless — clear it
		}
	}
	if c.Button(sdl.Rect{X: x + 82, Y: ty, W: 84, H: btnH}, "⏭ Set Out") {
		a.makerTrimEnd = a.makerSel
		if a.makerTrimStart > a.makerTrimEnd {
			a.makerTrimStart = -1
		}
	}
	if a.trimActive() {
		if c.Button(sdl.Rect{X: x + 172, Y: ty, W: 64, H: btnH}, "✕ Crop") {
			a.makerTrimStart, a.makerTrimEnd = -1, -1
		}
	}
	ty += btnH + 4
	if a.trimActive() {
		s, e := a.trimRange()
		c.Label(x, ty, fmt.Sprintf("Crop: lines %d–%d of %d — Preview / Export use this range", s+1, e+1, len(evs)), ColAccent)
	} else {
		c.Label(x, ty, "Crop: full scene — pick a line, then ⏮ Set In / ⏭ Set Out", ColTextDim)
	}
}

// drawCharSuggestions shows up to makerCharSuggest server characters whose name contains the typed text as
// click-to-fill chips. Hidden when offline or empty. Returns the new y (== y when hidden). Alloc-free fold match.
func (a *App) drawCharSuggestions(x, y, leftX, availW int32, m *protocol.ChatMessage) int32 {
	typed := strings.TrimSpace(m.CharName)
	if a.sess == nil || typed == "" {
		return y
	}
	var buf [makerCharSuggest]string
	n := 0
	for i := 0; i < len(a.sess.Chars) && n < makerCharSuggest; i++ {
		name := a.sess.Chars[i].Name
		if strings.EqualFold(name, typed) {
			continue // don't suggest the exact thing already typed
		}
		if containsFold(name, typed) {
			buf[n] = name
			n++
		}
	}
	return a.drawSuggestionChips(x, y, leftX, availW, buf[:n], &m.CharName)
}

// drawBgSuggestions is drawCharSuggestions for backgrounds, from the server's discovered list (a.bgPick.server).
// Free text stays for names a host's autoindex does not list.
func (a *App) drawBgSuggestions(x, y, leftX, availW int32, target *string) int32 {
	typed := strings.TrimSpace(*target)
	if typed == "" || len(a.bgPick.server) == 0 {
		return y
	}
	var buf [makerCharSuggest]string
	n := 0
	for i := 0; i < len(a.bgPick.server) && n < makerCharSuggest; i++ {
		name := a.bgPick.server[i]
		if strings.EqualFold(name, typed) {
			continue
		}
		if containsFold(name, typed) {
			buf[n] = name
			n++
		}
	}
	return a.drawSuggestionChips(x, y, leftX, availW, buf[:n], target)
}

// drawSuggestionChips renders up to makerCharSuggest click-to-fill chips, writing the clicked name into *target.
// Returns the y below the chips (== y when none).
func (a *App) drawSuggestionChips(x, y, leftX, availW int32, names []string, target *string) int32 {
	if len(names) == 0 {
		return y
	}
	c := a.ctx
	c.Label(x, y+5, "Matches:", ColTextDim)
	cx, cy := leftX, y
	for _, name := range names {
		bw := c.TextWidth(name) + 16
		if bw > 170 {
			bw = 170
		}
		if cx+bw > leftX+availW && cx > leftX { // wrap to the next row
			cx = leftX
			cy += btnH + 4
		}
		if c.Button(sdl.Rect{X: cx, Y: cy, W: bw, H: btnH}, name) {
			*target = name
		}
		cx += bw + 6
	}
	return cy + btnH + 6
}

// containsFold reports whether s contains sub, ASCII-case-insensitively, without allocating (it runs over the
// whole char list while the autocomplete is open).
func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if lowerASCII(s[i+j]) != lowerASCII(sub[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// drawMakerEditor renders the fields for the selected event, writing edits straight into the cloned scene buffer.
// makerOffsetSlider draws a -100..100 % sprite-offset slider at column fx with a numeric readout (0 = centred).
// The kit slider is 0..max, so the value is biased by +100 and unbiased on the way out.
func makerOffsetSlider(c *Ctx, id string, fx, y int32, v int) int {
	track := sdl.Rect{X: fx, Y: y + 6, W: 150, H: 14}
	nv := int(c.Slider(id, track, int32(v+100), 200)) - 100
	c.Label(fx+162, y+5, fmt.Sprintf("%d%%", nv), ColAccent)
	return nv
}

func (a *App) drawMakerEditor(x, y, w int32) {
	c := a.ctx
	if a.makerScene == nil || a.makerSel < 0 || a.makerSel >= len(a.makerScene.Events) {
		c.Label(x, y, "No line selected — use + Line / + BG / + Music to add one.", ColTextDim)
		return
	}
	if w < 200 {
		w = 200
	}
	e := &a.makerScene.Events[a.makerSel]
	const labelW = 96
	fx := x + labelW
	fw := w - labelW

	switch courtroom.EventKind(e.Kind) {
	case courtroom.EventMessage:
		if e.Message == nil {
			e.Message = &protocol.ChatMessage{Side: defaultMakerSide, Emote: defaultMakerEmote}
		}
		m := e.Message
		c.Label(x, y, "Message line", ColAccent)
		y += 28
		c.Label(x, y+5, "Character:", ColText)
		m.CharName, _ = c.TextField("mk_char", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.CharName, "character folder, e.g. Phoenix")
		y += 32
		y = a.drawCharSuggestions(x, y, fx, fw, m) // searchable picker from the server roster
		c.Label(x, y+5, "Emote:", ColText)
		m.Emote, _ = c.TextField("mk_emote", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.Emote, "sprite stem, e.g. normal or (a)normal")
		y += 32
		c.Label(x, y+5, "Showname:", ColText)
		mkSnP, mkSnE := a.icFieldFonts(m.Showname) // CJK-safe scene showname (ASCII stays fast path)
		m.Showname, _ = c.TextFieldEmoji("mk_show", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.Showname, "optional display name (blank = character name)", mkSnP, mkSnE)
		y += 32
		c.Label(x, y+5, "Text:", ColText)
		m.Message, _ = c.TextField("mk_text", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.Message, "what the character says")
		y += 36
		c.Label(x, y+5, "Position:", ColText)
		if next, changed := c.Dropdown("mk_side", sdl.Rect{X: fx, Y: y, W: 150, H: fieldH}, makerSideLabels, makerSideIndex(m.Side)); changed {
			m.Side = makerSideValues[next]
		}
		c.Label(fx+162, y+5, "Colour:", ColText)
		col := m.TextColor
		if col < 0 || col >= render.TextColorCount {
			col = 0
		}
		if next, changed := c.Dropdown("mk_color", sdl.Rect{X: fx + 220, Y: y, W: 130, H: fieldH}, render.TextColorNames(), col); changed {
			m.TextColor = next
		}
		y += 36
		if next := c.Checkbox(x, y, "Flip the sprite horizontally", m.Flip); next != m.Flip {
			m.Flip = next
		}
		y += 26
		// Desk: on, the character stands behind the desk; off, the whole body floats on the background.
		showDesk := m.DeskMod != protocol.DeskHide
		if next := c.Checkbox(x, y, "Show the desk (character stands behind it — off = full body on the background)", showDesk); next != showDesk {
			if next {
				m.DeskMod = protocol.DeskShow
			} else {
				m.DeskMod = protocol.DeskHide
			}
		}
		y += 26
		pre := m.EmoteMod == protocol.EmoteModPreanim || m.EmoteMod == protocol.EmoteModPreanimZoom
		if next := c.Checkbox(x, y, "Play a pre-animation before the line", pre); next != pre {
			if next {
				m.EmoteMod = protocol.EmoteModPreanim
			} else {
				m.EmoteMod = protocol.EmoteModIdle
			}
			pre = next
		}
		y += 26
		if pre {
			c.Label(x, y+5, "Pre-anim:", ColText)
			m.PreEmote, _ = c.TextField("mk_pre", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.PreEmote, "pre-animation stem, e.g. (a)point")
			y += 32
		}
		// Effects render in the live preview and the GIF/WebP export (the export ignores the live reduce-motion pref).
		shakeLabel := "Screenshake"
		if pre {
			shakeLabel = "Screenshake (only without a pre-anim)" // AO fires shake on idle/zoom only
		}
		if next := c.Checkbox(x, y, shakeLabel, m.Screenshake); next != m.Screenshake {
			m.Screenshake = next
		}
		if next := c.Checkbox(x+190, y, "Realization flash", m.Realization); next != m.Realization {
			m.Realization = next
		}
		y += 28
		// Move the character (offset as a percent of the viewport; 0 = centred).
		c.Label(x, y+5, "Move X:", ColText)
		m.SelfOffsetX = makerOffsetSlider(c, "mk_offx", fx, y, m.SelfOffsetX)
		y += 28
		c.Label(x, y+5, "Move Y:", ColText)
		m.SelfOffsetY = makerOffsetSlider(c, "mk_offy", fx, y, m.SelfOffsetY)
		y += 30
		// Sound effect: plays in Preview / a recording; a GIF/WebP has no audio.
		c.Label(x, y+5, "Sound FX:", ColText)
		m.SFXName, _ = c.TextField("mk_sfx", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, m.SFXName, "sfx name — plays in replay, silent in the GIF")
		y += 34
		c.Label(x, y, "Effects + move show in Preview and the export. The character + emote must exist at the Origin/CDN.", ColTextDim)

	case courtroom.EventBackground:
		c.Label(x, y, "Background change", ColAccent)
		y += 28
		c.Label(x, y+5, "Background:", ColText)
		e.Text, _ = c.TextField("mk_bg", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, e.Text, "background folder, e.g. courtroom or gs4")
		y += 32
		y = a.drawBgSuggestions(x, y, fx, fw, &e.Text) // searchable picker from the server's background list
		c.Label(x, y, "Sets the scene from this point on (shown under every line that follows).", ColTextDim)

	case courtroom.EventMusic:
		c.Label(x, y, "Music change", ColAccent)
		y += 28
		c.Label(x, y+5, "Track:", ColText)
		e.Text, _ = c.TextField("mk_mus", sdl.Rect{X: fx, Y: y, W: fw, H: fieldH}, e.Text, "music filename or full URL, e.g. trial.opus")
		y += 36
		c.Label(x, y, "Plays from this point on. Use a full URL for a CDN-hosted track.", ColTextDim)
	}
}

// packContribution counts how many of the scene's assets would be bundled from the user's own local folders
// rather than the server (a pure index lookup, no reads or fetches).
func (a *App) packContribution(scene *sceneRecording) int {
	if a.d.Manager == nil || scene == nil || a.d.Manager.MountLayer() == nil {
		return 0
	}
	events := make([]courtroom.Event, len(scene.Events))
	for i, e := range scene.Events {
		events[i] = eventFromRec(e)
	}
	urls := courtroom.NewURLBuilder(scene.Origin)
	n := 0
	for _, ref := range courtroom.SceneAssets(urls, scene.StartBg, events) {
		if a.d.Manager.PackCovers(ref.Base) {
			n++
			continue
		}
		for _, alt := range ref.Alts {
			if alt != "" && a.d.Manager.PackCovers(alt) {
				n++
				break
			}
		}
	}
	return n
}

// drawExportPackConfirm asks before a bundle carries the user's private art. Declining exports nothing: a partial,
// server-only bundle would play wrong for the recipient.
func (a *App) drawExportPackConfirm(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 560, 244
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Include your own files in this bundle?", ColText)
	c.Label(m.X+pad, m.Y+52, fmt.Sprintf("%d of this scene's assets come from your local folders,", a.makerExportPack), ColText)
	c.Label(m.X+pad, m.Y+74, "not from the server.", ColText)
	c.Label(m.X+pad, m.Y+104, "Including them makes the bundle play correctly for whoever you", ColTextDim)
	c.Label(m.X+pad, m.Y+124, "send it to — but it copies your own art into that file.", ColTextDim)
	c.Label(m.X+pad, m.Y+152, "Only share it with people you're happy to share that art with.", ColTierYellow)

	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 210, H: btnH}, "Include my files") {
		scene, name := a.makerExportScene, a.makerExportName
		a.makerExportPack, a.makerExportScene = 0, nil
		a.makerExportPackOK = true
		a.exportSceneArchive(scene, name)
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.makerExportPack, a.makerExportScene = 0, nil
		a.makerExportPackOK = false
	}
}
