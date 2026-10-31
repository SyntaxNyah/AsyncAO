package ui

// Classic-layout boxes: the default courtroom is laid out fresh every frame, so each movable widget draws through
// slotRect, which returns a user override (stored as a window fraction, so resolution-independent) or the computed
// rect. Un-edited must stay pixel-identical. The editor works in screen space and persists to config.ClassicLayout.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Slot names are string literals so the render path never formats a key, which would allocate every frame.
const (
	slotViewport = "viewport" // the stage (free move + resize; the scene fills it)
	slotRightCol = "rightcol" // IC log / right column (both themes)
	slotOOC      = "ooc"      // the new-default OOC box (independent of the log)
	slotEmotes   = "emotes"   // the emote grid (pages within its rect; both themes)
	// Pieces pulled out of the IC bar; each defaults to its spot in the bar and goes free only once dragged.
	// A stale "icbar" override in old prefs is ignored.
	slotICImmediate = "icbar.immediate" // the Immediate (non-interrupting preanim) toggle
	slotICPre       = "icbar.pre"       // the "Pre" toggle — send the emote's preanim (auto-follows each pick), same wrap-not-extract rule as Immediate
	slotICAdditive  = "icbar.additive"  // the Additive (2.8 append-to-last) toggle — shown only on additive servers, same wrap-not-extract rule as Immediate
	// Each draws through slotRect but the row cursor advances by the default width, so moving one never cascades the
	// rest.
	slotICColor    = "icbar.color"    // the text-colour swatch + dropdown
	slotICShowname = "icbar.showname" // the per-session showname box
	slotICSFX      = "icbar.sfx"      // the per-message SFX picker
	slotICEmoji    = "icbar.emoji"    // the emoji-picker button
	slotICFx       = "icbar.fx"       // the Text-FX cycle button
	slotICFlip     = "icbar.flip"     // the "Flip" toggle — mirror your emotes (AO2 ui_flip); shown only on flipping servers, mirrors the Pair panel's flip checkbox
	slotICReact    = "icbar.react"    // the React button
	slotICInput    = "icbar.input"    // the IC text input field itself
	slotChatbox    = "chatbox"        // the in-stage message box (showname + text); move it off the sprites
	slotOOCBar     = "oocbar"         // the bottom OOC bar (name + full-width input; shown when OOC is a tab)
	slotControls   = "controls"       // the two control-button rows (shouts/pair/knobs + utility buttons) as one block
	slotTabBar     = "tabbar"         // the floating server-tab switcher strip (move-only; issue #2 — it used to overlap the dock tabs)
	// slotToolbox is the compact toolbox's saved position. Move-only: the strip sizes itself from its fixed chip count.
	// The themed courtroom's parallel key is "asyncao_toolbox".
	slotToolbox = "toolbox"
	// slotMessages is the Group Chat / DMs panel's saved geometry only; visibility stays the showMessages toggle.
	slotMessages = "panel:messages"
	// The other floatWin panels save geometry the same way, via panelSlotTable; open state stays their own flag.
	// msgWin keeps its slotMessages key for backward compatibility.
	slotPanelPair    = "panel:pair"    // pairWin (Pairing)
	slotPanelMod     = "panel:mod"     // modWin (Mod dashboard)
	slotPanelCM      = "panel:cm"      // cmWin (CM room controls)
	slotPanelHK      = "panel:hk"      // hkWin (Hotkey sheet)
	slotPanelEvid    = "panel:evid"    // evidWin (Evidence)
	slotPanelModcall = "panel:modcall" // modcallWin (Call Mod)
	slotPanelVoice   = "panel:voice"   // voiceWin (Voice chat)
	slotPanelBan     = "panel:ban"     // banWin (ban / kick box)
	slotPanelDebug   = "panel:debug"   // debugWin (Debug panel)
	slotPanelClient  = "panel:client"  // clientWin (second-client / pinned server)
	slotPanelPieces  = "panel:pieces"  // piecesWin (Hide UI pieces)
	slotPanelTimer   = "panel:timer"   // timerWin (personal countdown, #31)
	slotPanelLogin   = "panel:login"   // loginWin (auto-login credentials, #31)
	slotPanelPairPop = "panel:pairpop" // pairPopWin (click-to-pair /pair <UID>, #31)
	// slotExtras is the Extras box's saved position; it is not a floatWin, with its own drag and resize handlers.
	slotExtras = "panel:extras"
	// tornWidgetSlotPrefix keys a torn-off Extras widget's rect: "torn:widget:<id>", id indexing extrasWidgets().
	// Formatted, but only on gesture end and reconstruction, never on a settled draw frame.
	tornWidgetSlotPrefix = "torn:widget:"
)

// Resize-edge bitmask: which sides of a box a drag moves.
const (
	edgeL uint8 = 1 << iota
	edgeR
	edgeT
	edgeB
)

const (
	// classicSlotRegCap pre-sizes the per-frame registry; the durable bound is config.classicSlotCap.
	classicSlotRegCap = 24
	// classicMinPx floors a resized box so it can't vanish, small enough that thin bars return to natural height.
	classicMinPx = 20
	// classicBannerH is the editor's top banner height (drags stay below it).
	classicBannerH = 26
	// classicTabTrayBot is the bottom Y of the editor's top chrome; keep in sync with drawClassicTabTray.
	classicTabTrayBot = int32(classicBannerH + 8 - 4 + 40)
	// ctrlBlockMinW floors the control block: the widest button (Disconnect, 110 px) plus spacing, so none is
	// unreachable.
	ctrlBlockMinW = 140
)

// slotInfo holds a slot's drawn rect (cur) and its rect with no override (def). Filled only while editing.
type slotInfo struct {
	cur sdl.Rect
	def sdl.Rect
}

// ensureClassicOv loads saved overrides once; the editor is the sole writer after. A nil snapshot means no edits.
func (a *App) ensureClassicOv() {
	if a.classicOvLoaded {
		return
	}
	a.classicOv = a.d.Prefs.Load().ClassicLayoutOverrides()
	a.classicAnchor = parseAnchors(a.d.Prefs.Load().ClassicAnchorSnapshot()) // window pins ride alongside
	a.classicRot = a.d.Prefs.Load().ClassicRotationSnapshot()                // rotations ride alongside too (A4)
	a.classicOvLoaded = true
}

// fracPx rounds to the nearest pixel because truncation does not round-trip: x/e*e can fall an ulp short and lose a
// whole pixel, which is an entire arrow-key nudge. Floor(v+0.5) so a negative fraction rounds the same way.
func fracPx(f float64, extent int32) int32 {
	return int32(math.Floor(f*float64(extent) + 0.5))
}

func fracToRect(f [4]float64, w, h int32) sdl.Rect {
	return sdl.Rect{
		X: fracPx(f[0], w),
		Y: fracPx(f[1], h),
		W: fracPx(f[2], w),
		H: fracPx(f[3], h),
	}
}

// rectToFrac is the plain quotient on purpose: a drag onto 360 of 1000 px must persist 0.36. Exactness is fracPx's job.
func rectToFrac(r sdl.Rect, w, h int32) [4]float64 {
	if w <= 0 || h <= 0 {
		return [4]float64{}
	}
	return [4]float64{
		float64(r.X) / float64(w),
		float64(r.Y) / float64(h),
		float64(r.W) / float64(w),
		float64(r.H) / float64(h),
	}
}

// snapViewportTo43 sets the stage to 4:3 at once (width wins; height derived, shrunk only to stay in the window).
func (a *App) snapViewportTo43(w, h int32) {
	info, ok := a.slotReg[slotViewport]
	if !ok {
		return
	}
	r := info.cur
	nh := r.W * 3 / 4
	if nh < classicMinPx { // keep the ratio by growing width off the floor instead
		nh = classicMinPx
		r.W = nh * 4 / 3
	}
	if r.Y+nh > h { // keep it on-screen: shrink until the derived height fits
		nh = h - r.Y
		r.W = nh * 4 / 3
	}
	if nh == r.H && r.W == info.cur.W {
		return // already 4:3 — no phantom undo entry
	}
	r.H = nh
	a.pushClassicUndo()
	if a.classicOv == nil {
		a.classicOv = make(map[string][4]float64, classicSlotRegCap)
	}
	ov := rectToFrac(r, w, h)
	a.classicOv[slotViewport] = ov
	a.d.Prefs.SetClassicSlot(slotViewport, ov)
	a.pushDebug("edit layout: stage snapped to 4:3")
}

// regSlot records a slot's rects for the editor. An empty name is refused: "" means "nothing selected", and its
// release would call config.SetClassicSlot("").
func (a *App) regSlot(name string, cur, def sdl.Rect) {
	if name == "" {
		return
	}
	if a.slotReg == nil {
		a.slotReg = make(map[string]slotInfo, classicSlotRegCap)
	}
	a.slotReg[name] = slotInfo{cur: cur, def: def}
}

// slotRect returns the override if present, else the default. Reads a.classicOv lock-free and allocates nothing.
func (a *App) slotRect(name string, def sdl.Rect, w, h int32) sdl.Rect {
	cur := def
	if ov, ok := a.classicOv[name]; ok {
		cur = a.anchoredRect(name, ov, w, h) // fracToRect unless the slot is window-pinned
	}
	if a.classicEdit {
		a.regSlot(name, cur, def)
	}
	return cur
}

// movableButton draws a button movable in the classic editor; the caller advances by its default width, so moving it
// never cascades the row. key must be a string literal, to stay allocation-free. Returns whether it was clicked.
func (a *App) movableButton(key string, def sdl.Rect, label string, w, h int32) bool {
	return a.ctx.Button(a.slotRect(key, def, w, h), label)
}

// ctrlSlot places one control button at *x and advances past it by adv, unless the user hid it: then ok=false and the
// cursor stays, so the row closes the gap.
func (a *App) ctrlSlot(x *int32, y2, wdt, adv, w, h int32, key string) (sdl.Rect, bool) {
	if a.panelHidden(key) {
		return sdl.Rect{}, false
	}
	r := a.slotRect(key, sdl.Rect{X: *x, Y: y2, W: wdt, H: btnH}, w, h)
	*x += adv
	return r, true
}

// classicSnapTo rounds a coordinate to the editor's grid step g (config.Settings.LayoutGridSize).
func classicSnapTo(v, g int32) int32 {
	if v < 0 {
		return 0
	}
	if g <= 0 {
		g = layoutGridDesign
	}
	return (v + g/2) / g * g
}

func classicSlotLabel(k string) string {
	switch k {
	case slotViewport:
		return "Viewport (stage)"
	case slotRightCol:
		return "Log / right column"
	case slotOOC:
		return "OOC box"
	case slotEmotes:
		return "Emote grid"
	case slotICImmediate:
		return "Immediate toggle"
	case slotICPre:
		return "Pre (preanim) toggle"
	case slotICAdditive:
		return "Additive toggle"
	case slotChatbox:
		return "Chatbox (message)"
	case slotOOCBar:
		return "OOC bar"
	case slotControls:
		return "Control buttons (drag the sides to re-wrap)"
	case slotTabBar:
		return "Server tabs (move only)"
	case slotToolbox:
		return "Toolbox (move only)"
	case slotICColor:
		return "IC colour picker"
	case slotICShowname:
		return "IC showname box"
	case slotICSFX:
		return "IC sound picker"
	case slotICEmoji:
		return "Emoji button"
	case slotICFx:
		return "Text-FX button"
	case slotICFlip:
		return "Flip toggle"
	case slotICReact:
		return "React button"
	case slotICInput:
		return "IC text input"
	case slotMessages:
		return "Group Chat (panel)"
	case "ctrl.pos":
		return "Pos selector"
	case "ctrl.groupchat":
		return "Group Chat button"
	case "ctrl.voice":
		return "Voice chat button"
	case "ctrl.randchar":
		return "Rand char button"
	case "ctrl.favsfilter":
		return "★ Favs filter"
	default:
		if rest, ok := strings.CutPrefix(k, "ctrl."); ok {
			return strings.ToUpper(rest[:1]) + rest[1:] + " button"
		}
		for i := range tornTabTable {
			if tornTabTable[i].key == k {
				return tornTabTable[i].title + " (panel)"
			}
		}
		return k
	}
}

// classicEdgeAt reports which sides of r the cursor grips within margin px; a corner gives two, 0 means a move.
func classicEdgeAt(mx, my int32, r sdl.Rect, margin int32) uint8 {
	if mx < r.X-margin || mx > r.X+r.W+margin || my < r.Y-margin || my > r.Y+r.H+margin {
		return 0
	}
	var e uint8
	if abs32(mx-r.X) <= margin {
		e |= edgeL
	}
	if abs32(mx-(r.X+r.W)) <= margin {
		e |= edgeR
	}
	if abs32(my-r.Y) <= margin {
		e |= edgeT
	}
	if abs32(my-(r.Y+r.H)) <= margin {
		e |= edgeB
	}
	return e
}

// slotResizeEdges reports which edges honour a resize drag; 0 means move-only. The hit-test masks the rest so an inert
// edge can't take a neighbour's grip.
func slotResizeEdges(name string) uint8 {
	switch name {
	case slotControls:
		// Width drives the row-wrap edge (controlsBlockOrigin); height comes from the rows, so those edges stay inert.
		return edgeL | edgeR
	case slotTabBar:
		// The server-tab strip sizes itself from its chips.
		return 0
	case slotToolbox:
		// The compact toolbox strip sizes itself from its fixed chip count.
		return 0
	default:
		return edgeL | edgeR | edgeT | edgeB
	}
}

// pickResizeSlot picks the slot a grip at (mx,my) targets: the hovered box if resizable and gripped, so a touching
// neighbour can't take the grip. Over no box, the smallest gripped resizable box. Edges come from resizeEdgesFor so
// both editors give the same answer for a widget.
func pickResizeSlot(reg map[string]slotInfo, keys []string, hoverKey string, mx, my, margin int32) (string, uint8) {
	if hoverKey != "" { // pointing at a box → only that box may resize (matches move)
		if e := classicEdgeAt(mx, my, reg[hoverKey].cur, margin) & resizeEdgesFor(classicTarget(hoverKey)); e != 0 {
			return hoverKey, e
		}
		return "", 0
	}
	best := int64(-1) // cursor outside every box: smallest gripped resizable wins
	bestKey, bestEdges := "", uint8(0)
	for _, k := range keys {
		r := reg[k].cur
		if e := classicEdgeAt(mx, my, r, margin) & resizeEdgesFor(classicTarget(k)); e != 0 {
			if area := int64(r.W) * int64(r.H); best < 0 || area < best {
				bestKey, bestEdges, best = k, e, area
			}
		}
	}
	return bestKey, bestEdges
}

// classicHandles returns the 8 resize handles of r (4 corners + 4 edge midpoints); the themed editor shares them.
func classicHandles(r sdl.Rect) [handleCount]sdl.Rect {
	const hp = layoutHandlePx
	cx := r.X + r.W/2 - hp/2
	cy := r.Y + r.H/2 - hp/2
	return [handleCount]sdl.Rect{
		{X: r.X, Y: r.Y, W: hp, H: hp},                       // top-left
		{X: r.X + r.W - hp, Y: r.Y, W: hp, H: hp},            // top-right
		{X: r.X, Y: r.Y + r.H - hp, W: hp, H: hp},            // bottom-left
		{X: r.X + r.W - hp, Y: r.Y + r.H - hp, W: hp, H: hp}, // bottom-right
		{X: cx, Y: r.Y, W: hp, H: hp},                        // top edge
		{X: cx, Y: r.Y + r.H - hp, W: hp, H: hp},             // bottom edge
		{X: r.X, Y: cy, W: hp, H: hp},                        // left edge
		{X: r.X + r.W - hp, Y: cy, W: hp, H: hp},             // right edge
	}
}

// startClassicEdit arms the default-courtroom editor and closes open modals, which it would block anyway.
func (a *App) startClassicEdit() {
	a.ensureClassicOv()
	a.classicEdit = true
	// The compact toolbox stays up in edit mode, drawn post-courtroom with pointer blocking released.
	a.closeEditorBlockingOverlays() // table-driven, shared with startLayoutEdit (layoutedit.go)
	a.classicTgt = noTarget()
	a.classicEditDrag = 0
	a.classicEditEdges = 0
	a.classicEditMoved = false
	a.classicUndo, a.classicRedo = nil, nil
	a.classicPickSig, a.classicPickIdx = "", 0
	a.layoutSnap = true        // tidy placement by default; toggle off in the editor
	a.layoutAspect = true      // keep the stage 4:3 while resizing (toggle off for a free / letterboxed stage)
	a.layoutProfileCursor = -1 // no saved profile applied via the banner chip yet this edit
	// layoutMagnetOff is not reset: the magnet also applies in normal play, so its zero value must persist across
	// sessions.
	a.pushDebug("edit layout (default courtroom): drag to move, edges/corners to resize, Ctrl+Z to undo, Esc to finish")
}

// stopClassicEdit disarms and releases input blocking.
func (a *App) stopClassicEdit() {
	a.classicEdit = false
	a.classicTgt = noTarget()
	a.classicEditDrag = 0
	a.classicEditEdges = 0
	a.classicUndo, a.classicRedo = nil, nil // free the history (it's edit-session-scoped)
	a.ctx.modalOn = false
}

// classicEditFence claims the pointer before the courtroom's widgets draw, so they stay inert. Mirrors layoutEditFence.
func (a *App) classicEditFence() {
	if a.classicEdit {
		a.ctx.modalOn = true
	}
}

// controlsBlockOrigin computes the block's origin, its offset from the default top (dy) and the wrap edge. Un-edited:
// clusterX==pad, dy==0, clusterRight==w-pad. An override translates it and its width drives the wrap (floored by
// ctrlBlockMinW); its height is ignored. r is the resolved rect; ok mirrors the override's presence.
func controlsBlockOrigin(r sdl.Rect, ok bool, w, defY int32) (clusterX, blockY, dy, clusterRight int32) {
	clusterX, blockY = pad, defY
	contentW := w - 2*pad
	if ok {
		clusterX, blockY = r.X, r.Y
		contentW = r.W
		if contentW < ctrlBlockMinW {
			contentW = ctrlBlockMinW // clamp UP: a too-narrow drag stays a narrow block, never a full-width jump
		}
	}
	dy = blockY - defY
	clusterRight = clusterX + contentW
	return
}

// viewportOverridden is true once the stage was dragged; the View knob and divider then defer until the box is reset.
func (a *App) viewportOverridden() bool {
	_, ok := a.classicOv[slotViewport]
	return ok
}

// clearClassicSlot drops an override from pref and snapshot, so it reverts the same frame; the window pin goes too.
func (a *App) clearClassicSlot(name string) {
	a.d.Prefs.ClearClassicSlot(name)
	if a.classicOv != nil {
		delete(a.classicOv, name)
	}
	if a.classicAnchor != nil {
		delete(a.classicAnchor, name)
	}
	if a.classicRot != nil {
		delete(a.classicRot, name) // rotation dies with the override (A4), like the pin
	}
}

// cloneClassicOv copies for the undo history, since aliasing lets a later edit mutate a snapshot. Empty clones to nil.
func cloneClassicOv(m map[string][4]float64) map[string][4]float64 {
	if len(m) == 0 {
		return nil
	}
	cp := make(map[string][4]float64, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// pushClassicUndo snapshots before an edit and clears redo. Edit-mode only; bounded by layoutUndoCap.
func (a *App) pushClassicUndo() {
	a.classicUndo = append(a.classicUndo, cloneClassicOv(a.classicOv))
	if len(a.classicUndo) > layoutUndoCap {
		a.classicUndo = a.classicUndo[1:]
	}
	a.classicRedo = a.classicRedo[:0]
}

// restoreClassicOv applies a snapshot to live overrides and the durable pref; cloned so nothing aliases a history
// entry.
func (a *App) restoreClassicOv(snap map[string][4]float64) {
	a.classicOv = cloneClassicOv(snap)
	a.d.Prefs.SetClassicLayout(a.classicOv)
}

// classicEditUndo / classicEditRedo swap the override map with a snapshot and persist. Driven by editorUndoChord,
// because Ctrl chords arrive on the hotkey channel and the editor's draw never sees them.
func (a *App) classicEditUndo() {
	if len(a.classicUndo) == 0 {
		return
	}
	a.classicRedo = append(a.classicRedo, cloneClassicOv(a.classicOv))
	snap := a.classicUndo[len(a.classicUndo)-1]
	a.classicUndo = a.classicUndo[:len(a.classicUndo)-1]
	a.restoreClassicOv(snap)
}

func (a *App) classicEditRedo() {
	if len(a.classicRedo) == 0 {
		return
	}
	a.classicUndo = append(a.classicUndo, cloneClassicOv(a.classicOv))
	snap := a.classicRedo[len(a.classicRedo)-1]
	a.classicRedo = a.classicRedo[:len(a.classicRedo)-1]
	a.restoreClassicOv(snap)
}

// drawClassicEditor paints the overlay and owns input. Called last from drawCourtroom, after every slotRect this frame.
func (a *App) drawClassicEditor(w, h int32) {
	c := a.ctx
	if w <= 0 || h <= 0 {
		a.stopClassicEdit()
		return
	}

	// Banner geometry first, as the click handlers read it; the paint comes last so chrome sits above outlines and
	// tags.
	doneBtn := sdl.Rect{X: w - 62 - pad, Y: 2, W: 62, H: classicBannerH - 4}
	resetBtn := sdl.Rect{X: doneBtn.X - 90 - 6, Y: 2, W: 90, H: classicBannerH - 4}
	snapBtn := sdl.Rect{X: resetBtn.X - 92 - 6, Y: 2, W: 92, H: classicBannerH - 4}
	gridBtn := sdl.Rect{X: snapBtn.X - 78 - 6, Y: 2, W: 78, H: classicBannerH - 4}
	aspectBtn := sdl.Rect{X: gridBtn.X - 80 - 6, Y: 2, W: 80, H: classicBannerH - 4}
	// Toggle for the sibling magnet, so it can be loosened without holding Shift.
	magnetBtn := sdl.Rect{X: aspectBtn.X - editChipMagnetW - 6, Y: 2, W: editChipMagnetW, H: classicBannerH - 4}
	// Cycles saved layout profiles; drawn only when at least one exists.
	profileBtn := sdl.Rect{X: magnetBtn.X - editChipProfileW - 6, Y: 2, W: editChipProfileW, H: classicBannerH - 4}

	pressed := c.mouseDown && !a.classicEditPrev
	a.classicEditPrev = c.mouseDown

	// Undo / redo fire from editorUndoChord: Ctrl chords use c.hotkey, never c.keyPressed.

	if c.escPressed || (c.clicked && pointIn(c.mouseX, c.mouseY, doneBtn)) {
		a.stopClassicEdit()
		return
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, resetBtn) {
		a.pushClassicUndo() // so Reset all is itself undoable (pins aren't in history — see below)
		a.d.Prefs.ClearClassicSlot("")
		a.classicOv = nil
		a.classicAnchor = nil // pins die with their overrides
		a.classicRot = nil    // rotations too (A4)
		a.pushDebug("edit layout: all boxes reset to default")
		return
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, snapBtn) {
		a.layoutSnap = !a.layoutSnap
	}
	// Cycles the snap step (4, 8, 16, 32), persisted.
	if c.clicked && pointIn(c.mouseX, c.mouseY, gridBtn) {
		gridPx := nextLayoutGridSize(a.d.Prefs.Load().LayoutGridSize())
		a.d.Prefs.Update(func(sn *config.Settings) {
			sn.LayoutGridPx = gridPx
		})
	}

	if c.clicked && pointIn(c.mouseX, c.mouseY, aspectBtn) {
		a.layoutAspect = !a.layoutAspect // lock/unlock the stage's 4:3 while resizing it
		// Turning it on snaps the stage to 4:3 at once, not only on future resize drags.
		if a.layoutAspect {
			a.snapViewportTo43(w, h)
		}
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, magnetBtn) {
		a.layoutMagnetOff = !a.layoutMagnetOff // persistent sibling-magnet toggle (session-only, like Snap)
	}
	if c.clicked && a.hasLayoutProfiles() && pointIn(c.mouseX, c.mouseY, profileBtn) {
		a.cycleLayoutProfile() // apply the next saved full-state profile (applyProfile)
		// applyProfile replaced the override map, so this frame's slotReg is stale: bail and rebuild next frame.
		return
	}

	// overTray suppresses a slot move when a tray chip is pressed.
	overTray := a.drawClassicTabTray(w, h)
	// Name tags clamp below this so a box in the top strip can't draw its tag over the editor's controls.
	a.classicChromeBot = classicTabTrayBot
	// A press over the toolbox must not move the box beneath the bottom-right corner.
	overToolbox := a.editOverToolbox(w, h)

	keys := make([]string, 0, len(a.slotReg))
	for k := range a.slotReg {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Boxes under the cursor, smallest first so a small box on a big one is grabbable; Tab cycles which is armed.
	// The pick index resets whenever that stack changes.
	hoverKey := ""
	var stack []string
	// Resolved once, so the target's space is checked once per frame.
	editKey := a.classicTgt.classicKey()
	if a.classicEditDrag != 0 {
		hoverKey = editKey // mid-drag: keep the grabbed box highlighted
	} else {
		for _, k := range keys {
			if pointIn(c.mouseX, c.mouseY, a.slotReg[k].cur) {
				stack = append(stack, k)
			}
		}
		sort.SliceStable(stack, func(i, j int) bool {
			ri, rj := a.slotReg[stack[i]].cur, a.slotReg[stack[j]].cur
			return int64(ri.W)*int64(ri.H) < int64(rj.W)*int64(rj.H)
		})
		if len(stack) > 0 {
			if sig := strings.Join(stack, "\x00"); sig != a.classicPickSig {
				a.classicPickSig, a.classicPickIdx = sig, 0 // a new stack under the cursor
			}
			if c.keyPressed == sdl.K_TAB {
				a.classicPickIdx++
				c.keyPressed = 0
			}
			a.classicPickIdx %= len(stack)
			hoverKey = stack[a.classicPickIdx]
		} else {
			a.classicPickSig, a.classicPickIdx = "", 0
		}
	}

	// A pins the hovered box to a window corner or the centre, keeping its pixel offsets on resize.
	// Consumed so a char keybind on A can't also fire.
	if a.classicEditDrag == 0 && hoverKey != "" && c.keyPressed == sdl.K_a {
		a.cycleSlotAnchor(hoverKey, w, h)
		c.keyPressed = 0
	}

	// R rotates the hovered box's texture-backed chrome (Shift+R: fine step). Not undoable. Consumed like A.
	// No classic slot is texture-backed today, so cycleSlotRotation reports "n/a".
	if a.classicEditDrag == 0 && hoverKey != "" && c.keyPressed == sdl.K_r {
		a.cycleSlotRotation(hoverKey, sdl.GetModState()&sdl.KMOD_SHIFT != 0)
		c.keyPressed = 0
	}

	// Resize takes priority over move, on the hovered box. A drag can begin anywhere except over the editor's own chips
	// or the pop-out tray.
	overChip := pointIn(c.mouseX, c.mouseY, doneBtn) || pointIn(c.mouseX, c.mouseY, resetBtn) ||
		pointIn(c.mouseX, c.mouseY, snapBtn) || pointIn(c.mouseX, c.mouseY, aspectBtn) ||
		pointIn(c.mouseX, c.mouseY, gridBtn)
	if pressed && a.classicEditDrag == 0 && !overChip && !overTray && !overToolbox {
		// Alt forces a move: a small widget is almost all edge, so a plain drag would resize it. Shift stays
		// pixel-precise.
		resizeKey, resizeEdges := "", uint8(0)
		if sdl.GetModState()&sdl.KMOD_ALT == 0 {
			resizeKey, resizeEdges = pickResizeSlot(a.slotReg, keys, hoverKey, c.mouseX, c.mouseY, layoutHandlePx)
		}
		switch {
		case resizeKey != "":
			a.classicTgt, a.classicEditDrag, a.classicEditEdges = classicTarget(resizeKey), 2, resizeEdges
			editKey = resizeKey
		case hoverKey != "":
			a.classicTgt, a.classicEditDrag, a.classicEditEdges = classicTarget(hoverKey), 1, 0
			editKey = hoverKey
		}
		if a.classicEditDrag != 0 {
			a.classicEditStart = [2]int32{c.mouseX, c.mouseY}
			a.classicEditBase = a.slotReg[editKey].cur
			a.classicEditMoved = false
			a.pushClassicUndo() // snapshot before the move/resize (popped at release if it was a no-op)
		}
	}

	// Right-click resets to default; snapshot only with an override to clear, so the history gets no no-ops.
	if c.rightClicked && hoverKey != "" && !overTray && !overToolbox {
		if _, ov := a.classicOv[hoverKey]; ov {
			a.pushClassicUndo()
			a.clearClassicSlot(hoverKey)
		}
	}

	// Screen deltas, clamped and snapped, then written to the local override; the widget redraws there next frame.
	if a.classicEditDrag != 0 && c.mouseDown && editKey != "" {
		dx := c.mouseX - a.classicEditStart[0]
		dy := c.mouseY - a.classicEditStart[1]
		if dx != 0 || dy != 0 {
			a.classicEditMoved = true
		}
		base := a.classicEditBase
		r := base
		if a.classicEditDrag == 1 { // move
			r.X = base.X + dx
			r.Y = base.Y + dy
		} else { // resize the gripped edges (one or both dimensions)
			e := a.classicEditEdges
			if e&edgeR != 0 {
				r.W = base.W + dx
			}
			if e&edgeL != 0 {
				r.X = base.X + dx
				r.W = base.W - dx
			}
			if e&edgeB != 0 {
				r.H = base.H + dy
			}
			if e&edgeT != 0 {
				r.Y = base.Y + dy
				r.H = base.H - dy
			}
			if r.W < classicMinPx { // floor without inverting; keep the anchored edge fixed
				if e&edgeL != 0 {
					r.X = base.X + base.W - classicMinPx
				}
				r.W = classicMinPx
			}
			if r.H < classicMinPx {
				if e&edgeT != 0 {
					r.Y = base.Y + base.H - classicMinPx
				}
				r.H = classicMinPx
			}
		}
		// Shift bypasses the grid and the magnet for this drag. GetModState is a cheap render-thread query.
		a.alignGuides = a.alignGuides[:0]
		if a.layoutSnap && sdl.GetModState()&sdl.KMOD_SHIFT == 0 {
			g := int32(a.d.Prefs.Load().LayoutGridSize())
			r.X, r.Y, r.W, r.H = classicSnapTo(r.X, g), classicSnapTo(r.Y, g), classicSnapTo(r.W, g), classicSnapTo(r.H, g)
			if r.W < classicMinPx {
				r.W = classicMinPx
			}
			if r.H < classicMinPx {
				r.H = classicMinPx
			}
			// The defaults aren't grid-aligned, so edges and centres also snap to other boxes and the window,
			// overriding the grid on the matched axis.
			a.alignScratch = a.alignScratch[:0]
			for _, k := range keys {
				if k != editKey {
					a.alignScratch = append(a.alignScratch, a.slotReg[k].cur)
				}
			}
			r, a.alignGuides = alignRect(r, a.alignScratch, w, h, a.classicEditDrag == 1, a.classicEditEdges, a.alignGuides)
		}
		// Lock the stage to 4:3 from the grabbed edge. After the grid snap, which would otherwise break the ratio.
		if a.layoutAspect && a.classicEditDrag != 1 && editKey == slotViewport {
			e := a.classicEditEdges
			if e&(edgeL|edgeR) != 0 { // a side/corner handle → width drives height
				r.H = r.W * 3 / 4
			} else if e&(edgeT|edgeB) != 0 { // a top/bottom handle → height drives width
				r.W = r.H * 4 / 3
			}
		}
		// Shared with the arrow-key nudge, so both stop in the same places. Only the banner's chips block a drag.
		r = clampSlotToWindow(r, w, h)
		if a.classicEditMoved {
			if a.classicOv == nil {
				a.classicOv = make(map[string][4]float64, classicSlotRegCap)
			}
			a.classicOv[editKey] = rectToFrac(r, w, h)
			// A pinned slot's override now describes this window size; re-base the anchor so resolution round-trips.
			a.syncAnchorWindow(editKey, w, h)
		}
	}

	// Release persists the edit; a no-move click discards the snapshot, leaving no no-op on the undo stack.
	if a.classicEditDrag != 0 && !c.mouseDown {
		switch {
		case a.classicEditMoved && editKey != "":
			if ov, ok := a.classicOv[editKey]; ok {
				a.d.Prefs.SetClassicSlot(editKey, ov)
				if m := a.slotAnchorMode(editKey); m != "" {
					a.d.Prefs.SetClassicAnchor(editKey, config.ClassicAnchor{Mode: m, WinW: int(w), WinH: int(h)})
				}
			}
		default:
			if n := len(a.classicUndo); n > 0 {
				a.classicUndo = a.classicUndo[:n-1]
			}
		}
		a.classicEditDrag = 0
		a.classicEditEdges = 0
		a.classicEditMoved = false
	}

	// A quiet outline on every slot; border, handles and name tag only on the hovered or dragged box.
	dimEdge := blendCol(ColAccent, ColBackground, 0.6)
	for _, k := range keys {
		r := a.slotReg[k].cur
		if a.classicEditDrag != 0 && k == editKey {
			if ov, ok := a.classicOv[k]; ok {
				r = a.anchoredRect(k, ov, w, h)
			}
		}
		switch {
		case k == editKey:
			c.Border(r, ColDanger)
			a.drawSlotHandles(r, k, ColDanger)
			a.drawSlotTag(r, k, ColDanger)
		case k == hoverKey:
			c.Border(r, ColTierYellow)
			a.drawSlotHandles(r, k, ColTierYellow)
			a.drawSlotTag(r, k, ColTierYellow)
		default:
			c.Border(r, dimEdge) // resting: structure only, no clutter
		}
		a.drawAnchorPin(r, k) // pinned boxes show a green dot at their reference
	}
	// Full-length hairlines at whatever the dragged box just snapped flush to.
	if a.classicEditDrag != 0 {
		for _, g := range a.alignGuides {
			if g.vertical {
				c.Fill(sdl.Rect{X: g.pos, Y: 0, W: 1, H: h}, ColTierGreen)
			} else {
				c.Fill(sdl.Rect{X: 0, Y: g.pos, W: w, H: 1}, ColTierGreen)
			}
		}
	}
	// Chrome last, topmost and translucent so widgets in the top strip stay visible. Painting after the click handlers
	// means labels show the state a click just set.
	a.noteEditorBanner() // the top band is ours this frame, so the menu bar stands down (menubar.go)
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: classicBannerH}, sdl.Color{R: 0, G: 0, B: 0, A: 150})
	c.Label(pad, 6, "Edit Layout", ColTierYellow)
	snapLabel := "Snap: off"
	if a.layoutSnap {
		snapLabel = "Snap: on"
	}
	aspectLabel := "4:3: off"
	if a.layoutAspect {
		aspectLabel = "4:3: on"
	}
	magnetLabel := "Magnet: on"
	if a.layoutMagnetOff {
		magnetLabel = "Magnet: off"
	}
	// The hint ends before the leftmost chip drawn: the profile chip if it exists, else the magnet chip.
	leftmostChipX := magnetBtn.X
	if a.hasLayoutProfiles() {
		leftmostChipX = profileBtn.X
	}
	hintX := pad + c.TextWidth("Edit Layout") + 18
	// The line clips from the right, so the tail is what a narrow window loses first.
	c.LabelClipped(hintX, 6, leftmostChipX-hintX-editBannerHintGap, "Drag to move · arrows = nudge 1 px (Ctrl = grid) · edge to resize · green lines = aligned · Alt = move · Shift = precise", ColTextDim)
	a.rawChip(aspectBtn, aspectLabel)
	a.rawChip(magnetBtn, magnetLabel)
	if name := a.currentLayoutProfileLabel(); name != "" {
		a.rawChip(profileBtn, name)
	}
	a.rawChip(gridBtn, fmt.Sprintf("Grid: %d", a.d.Prefs.Load().LayoutGridSize()))
	a.rawChip(snapBtn, snapLabel)
	a.rawChip(resetBtn, "Reset all")
	a.rawChip(doneBtn, "Done")
	// Shown only for a nonzero angle; classic slots don't rotate today, kept for symmetry with the themed editor.
	if hoverKey != "" {
		if label := rotationChipLabel(a.classicRot[hoverKey]); label != "" {
			// Left of the profile or magnet chip, so it can't overpaint them.
			rotRightX := magnetBtn.X
			if a.hasLayoutProfiles() {
				rotRightX = profileBtn.X
			}
			rotBtn := sdl.Rect{X: rotRightX - 74 - 6, Y: 2, W: 74, H: classicBannerH - 4}
			a.rawChip(rotBtn, label)
		}
	}

	switch { // bottom line: the per-box context (kept off the busy top banner)
	case editKey != "":
		c.Label(pad, h-22, "Moving "+classicSlotLabel(editKey)+"  ·  release to save", ColText)
	case len(stack) > 1:
		c.Label(pad, h-22, fmt.Sprintf("%s  ·  %d boxes here — Tab to pick (%d/%d)",
			classicSlotLabel(hoverKey), len(stack), a.classicPickIdx+1, len(stack)), ColTierYellow)
	case hoverKey != "":
		hint := classicSlotLabel(hoverKey) + "  ·  drag to move (Alt = always move) · edge to resize · right-click to reset · A = pin · R = rotate"
		if m := a.slotAnchorMode(hoverKey); m != "" {
			hint = classicSlotLabel(hoverKey) + "  ·  pinned to the " + anchorModeLabel(m) + " (stays glued when the window resizes) · A = next pin · right-click to reset"
		}
		c.Label(pad, h-22, hint, ColTierYellow)
	case len(keys) > 0:
		c.Label(pad, h-22, "Hover a box to move or resize it  ·  Ctrl+Z undo  ·  changes save automatically", ColTextDim)
	}
}

// handleEdgeMask maps each classicHandles index to the edges it grips.
var handleEdgeMask = [handleCount]uint8{
	edgeL | edgeT, edgeR | edgeT, edgeL | edgeB, edgeR | edgeB,
	edgeT, edgeB, edgeL, edgeR,
}

// drawSlotHandles paints only handles whose every edge the slot honours; move-only slots paint none.
func (a *App) drawSlotHandles(r sdl.Rect, key string, col sdl.Color) {
	allowed := resizeEdgesFor(classicTarget(key))
	if allowed == 0 {
		return
	}
	c := a.ctx
	for i, hnd := range classicHandles(r) {
		if handleEdgeMask[i]&^allowed != 0 {
			continue // the handle grips an edge this slot doesn't honour
		}
		c.Fill(hnd, col)
		c.Border(hnd, sdl.Color{R: 0, G: 0, B: 0, A: 170})
	}
}

// drawSlotTag floats a slot's name just above the box, or just inside when there's no room, and below the editor's
// top chrome (classicChromeBot) so it never covers the hint text and buttons.
func (a *App) drawSlotTag(r sdl.Rect, key string, col sdl.Color) {
	c := a.ctx
	label := classicSlotLabel(key)
	const th = int32(16)
	tw := c.TextWidth(label) + 14
	if tw > r.W && r.W > 0 {
		tw = r.W
	}
	chromeBot := a.classicChromeBot
	if chromeBot < classicBannerH { // toolbox not drawn this frame (shouldn't happen mid-edit): at least clear the banner
		chromeBot = classicBannerH
	}
	tagY := r.Y - th - 2
	if tagY < chromeBot+2 { // top boxes: no room above → tuck inside the top edge…
		tagY = r.Y + 2
		if tagY < chromeBot+2 { // …unless the box top itself is under the chrome
			tagY = chromeBot + 2
		}
	}
	tag := sdl.Rect{X: r.X, Y: tagY, W: tw, H: th}
	c.Fill(tag, sdl.Color{R: 12, G: 14, B: 20, A: 235})
	c.Border(tag, col)
	c.LabelClipped(tag.X+6, tag.Y+1, tw-12, label, col)
}
