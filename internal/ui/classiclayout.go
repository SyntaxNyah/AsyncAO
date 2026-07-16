package ui

// Classic-layout slots: the DEFAULT (non-themed) courtroom — and the Legacy
// Developer theme, which shares the same procedural geometry — are laid out
// fresh every frame, so unlike the themed editor there are no design rects to
// drag. Instead each movable widget draws through slotRect, which returns a user
// override (persisted as a window FRACTION, so the drag is resolution-
// independent) or — when nothing is overridden — the exact rect the layout
// already computed. Un-edited ⇒ pixel-identical to before (the safety
// invariant); the override path is purely additive and off the hot frame.
//
// The editor (drawClassicEditor) reuses the themed editor's feel — drag = move,
// edge / corner handles = resize (independently horizontal or vertical),
// right-click = reset a box, Snap, Esc/Done — but works in screen space over the
// live courtroom and persists to config.ClassicLayout.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Slot names — string literals so the render path never formats a key (which
// would allocate every frame). Keep in sync with the call sites in drawCourtroom.
const (
	slotViewport = "viewport" // the stage (free move + resize; the scene fills it)
	slotRightCol = "rightcol" // IC log / right column (both themes)
	slotOOC      = "ooc"      // the new-default OOC box (independent of the log)
	slotEmotes   = "emotes"   // the emote grid (pages within its rect; both themes)
	// The whole-bar "icbar" slot was REMOVED in v1.50.5 (Nightingale: "remove
	// the panel, make every element independent") — the pieces below are the
	// only IC-bar slots now. A stale "icbar" override in old prefs is ignored.
	// Individually-movable pieces pulled OUT of the IC bar (the "build your own
	// layout" work). Each defaults to its spot in the bar — so an un-edited or
	// whole-bar-moved layout is pixel-identical — and only goes free once dragged.
	slotICImmediate = "icbar.immediate" // the Immediate (non-interrupting preanim) toggle
	slotICAdditive  = "icbar.additive"  // the Additive (2.8 append-to-last) toggle — shown only on additive servers, same wrap-not-extract rule as Immediate
	// More IC-bar pieces pulled out individually (#4a, Crystalwarrior — "split it up so
	// colours, checkboxes, etc. are located elsewhere"). Same wrap-not-extract rule as
	// Immediate: each draws through slotRect but the row cursor advances by the DEFAULT
	// width, so an un-edited bar is pixel-identical and moving one never cascades the rest.
	slotICColor    = "icbar.color"    // the text-colour swatch + dropdown
	slotICShowname = "icbar.showname" // the per-session showname box
	slotICSFX      = "icbar.sfx"      // the per-message SFX picker
	slotICEmoji    = "icbar.emoji"    // the emoji-picker button
	slotICFx       = "icbar.fx"       // the Text-FX cycle button
	slotICReact    = "icbar.react"    // the React button
	slotICInput    = "icbar.input"    // the IC text input field itself
	slotChatbox    = "chatbox"        // the in-stage message box (showname + text); move it off the sprites
	slotOOCBar     = "oocbar"         // the bottom OOC bar (name + full-width input; shown when OOC is a tab)
	slotControls   = "controls"       // the two control-button rows (shouts/pair/knobs + utility buttons) as one block
	slotTabBar     = "tabbar"         // the floating server-tab switcher strip (move-only; issue #2 — it used to overlap the dock tabs)
	// slotToolbox is the compact bottom-right toolbox grip's PERSISTED home (A1
	// Phase 2). It rides the same classic-slot mechanism as every other movable
	// piece: an absent override keeps the historical bottom-right default (untouched
	// installs are pixel-identical), and dragging it in the editor writes an override
	// via SetClassicSlot on drag-end. Move-only (slotResizeEdges) — the strip sizes
	// itself from its fixed chip count, so a resize would do nothing. The themed
	// courtroom has a parallel opt-in key "asyncao_toolbox" (mirrors slotICFx's
	// asyncao_ic_fx twin).
	slotToolbox = "toolbox"
	// slotMessages is the Group Chat / DMs panel's PERSISTED home. Unlike a torn
	// tab, slot presence is geometry only — visibility stays the orthogonal
	// showMessages toggle (Extras → Group Chat). The live floatWin (msgWin) seeds
	// from this slot on open and writes back on drag/resize end (messaging_panel.go).
	slotMessages = "panel:messages"
	// The remaining floatWin panels persist their geometry through the SAME
	// classic-slot mechanism as slotMessages (seedPanelFromSlot / persistPanelSlot,
	// driven by panelSlotTable). Like slotMessages, slot presence is geometry only —
	// each panel's OPEN state stays its own orthogonal flag. msgWin keeps its
	// historical slotMessages key (not slotPanelMsg) for backward compat.
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
	// slotExtras is the Extras box's persisted home. The Extras box keeps its
	// bespoke drag/resize handlers (it is NOT a floatWin) but rides the classic-slot
	// mechanism for cross-session position persistence.
	slotExtras = "panel:extras"
	// tornWidgetSlotPrefix keys a torn-off Extras widget's persisted box rect:
	// "torn:widget:<id>" where <id> indexes extrasWidgets(). Torn widgets survive
	// relaunch by riding the same classic-slot mechanism as slotExtras — the frac
	// rect persists on gesture-end and re-tears at that spot on the next courtroom
	// entry (floatbox.go: persistTornWidgetSlot / reconstructTornWidgets). Unlike
	// the panel/extras slots these keys are FORMATTED (id appended) — but only on
	// the cold gesture-end / reconstruction paths, never on a settled draw frame, so
	// the no-per-frame-alloc rule holds.
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
	// classicSlotRegCap pre-sizes the per-frame slot registry (cosmetic; the
	// durable bound is config.classicSlotCap on what persists).
	classicSlotRegCap = 24
	// classicMinPx floors a resized slot in screen px so a box can't vanish. Kept
	// small so thin bars (the IC input / OOC bar) shrink back to natural height —
	// 48 stranded them tall, unable to resize back down.
	classicMinPx = 20
	// classicBannerH is the editor's top banner height (drags stay below it).
	classicBannerH = 26
	// classicTabTrayBot is the bottom Y of the editor's stacked top chrome — the
	// tab tray sits at classicBannerH+8 (top-inset 4) with a 40 px strip
	// (drawClassicTabTray), so its bottom is classicBannerH + 8 - 4 + 40. Slot name
	// tags clamp below it (classicChromeBot). Kept in sync with drawClassicTabTray.
	classicTabTrayBot = int32(classicBannerH + 8 - 4 + 40)
	// ctrlBlockMinW floors a width-resized control-button block: wide enough
	// for the widest single button (Disconnect, 110 px) plus its spacing, so
	// no button can be wrapped into an unreachable zero-width column.
	ctrlBlockMinW = 140
)

// slotInfo records, per registered slot this frame, the rect it actually drew at
// (cur) and the rect it WOULD draw at with no override (def, for reset).
// Populated only while editing, so the common frame is alloc-free.
type slotInfo struct {
	cur sdl.Rect
	def sdl.Rect
}

// ensureClassicOv loads the persisted overrides into the App-local snapshot once
// (the editor is the sole writer thereafter). A nil snapshot means "no edits" —
// slotRect then just returns the computed default. Called every courtroom frame;
// after the first it is a single bool check (alloc-free).
func (a *App) ensureClassicOv() {
	if a.classicOvLoaded {
		return
	}
	a.classicOv = a.d.Prefs.ClassicLayoutOverrides()
	a.classicAnchor = parseAnchors(a.d.Prefs.ClassicAnchorSnapshot()) // window pins ride alongside
	a.classicRot = a.d.Prefs.ClassicRotationSnapshot()                // rotations ride alongside too (A4)
	a.classicOvLoaded = true
}

// fracToRect converts a stored window-fraction override to screen pixels.
func fracToRect(f [4]float64, w, h int32) sdl.Rect {
	return sdl.Rect{
		X: int32(f[0] * float64(w)),
		Y: int32(f[1] * float64(h)),
		W: int32(f[2] * float64(w)),
		H: int32(f[3] * float64(h)),
	}
}

// rectToFrac is the inverse — screen pixels to window fractions for persistence.
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

// snapViewportTo43 sets the stage to 4:3 RIGHT NOW (width wins, height derived,
// shrunk only if the result would leave the window) — the immediate feedback for
// the editor's "4:3" toggle. Undoable and persisted like any drag. No-op when
// the stage is already 4:3 or isn't registered this frame.
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

// regSlot records a slot's drawn/default rects for the editor (edit-only path).
func (a *App) regSlot(name string, cur, def sdl.Rect) {
	if a.slotReg == nil {
		a.slotReg = make(map[string]slotInfo, classicSlotRegCap)
	}
	a.slotReg[name] = slotInfo{cur: cur, def: def}
}

// slotRect returns a movable+resizable widget's rect: the user override (frac→px)
// if present, else the layout's computed default. Reads a.classicOv lock-free; on
// the common (non-edit) frame it touches no map writer and allocates nothing.
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

// movableButton draws a control button that can be pulled out of its row into its
// own spot in the classic editor. Its rect routes through slotRect(key), so an
// override repositions it, while the caller keeps advancing its layout cursor by the
// button's DEFAULT width — so an un-edited row is pixel-identical and moving one
// button never cascades the rest (the "wrap, not extract" pattern). key MUST be a
// string literal (no per-frame formatting) to keep the row allocation-free. Returns
// whether the button was clicked this frame.
func (a *App) movableButton(key string, def sdl.Rect, label string, w, h int32) bool {
	return a.ctx.Button(a.slotRect(key, def, w, h), label)
}

// ctrlSlot positions one courtroom control button at the shared row cursor *x and
// advances it past the button — UNLESS the user hid that button (the customizable
// toolbar: UI… popup → Buttons), in which case it returns ok=false and leaves the
// cursor PUT, so the row COMPACTS with no gap. `adv` is the exact cursor step
// (width + spacing) the inline row used, so a row with nothing hidden is
// pixel-identical to before. The returned rect still routes through slotRect, so an
// Edit-Layout override repositions a visible button as usual.
func (a *App) ctrlSlot(x *int32, y2, wdt, adv, w, h int32, key string) (sdl.Rect, bool) {
	if a.panelHidden(key) {
		return sdl.Rect{}, false
	}
	r := a.slotRect(key, sdl.Rect{X: *x, Y: y2, W: wdt, H: btnH}, w, h)
	*x += adv
	return r, true
}

// classicSnapTo rounds a screen coordinate to the editor's grid step g
// (user-tunable — the banner's Grid chip; config.LayoutGridSize).
func classicSnapTo(v, g int32) int32 {
	if v < 0 {
		return 0
	}
	if g <= 0 {
		g = layoutGridDesign
	}
	return (v + g/2) / g * g
}

// classicSlotLabel is the human name shown on a slot's outline in the editor.
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
		// Individually-movable control buttons carry a "ctrl.<name>" key.
		if rest, ok := strings.CutPrefix(k, "ctrl."); ok {
			return strings.ToUpper(rest[:1]) + rest[1:] + " button"
		}
		// Torn-off tab panels carry a "tab:<name>" key.
		for i := range tornTabTable {
			if tornTabTable[i].key == k {
				return tornTabTable[i].title + " (panel)"
			}
		}
		return k
	}
}

// classicEdgeAt reports which sides of r the cursor grips, within margin px. A
// corner returns two adjacent sides; 0 means "inside / not on an edge" (= move).
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

// slotResizeEdges reports which edges of a slot honour a resize drag; 0 means
// move-only. Restricted slots paint only their live handles and the resize
// hit-test masks the rest — critically so an inert edge can't STEAL a
// neighbour's grip (smallest-area-wins would otherwise let a slot swallow the
// resize of a box it merely touches, then do nothing — the "boxes in the
// middle won't resize" bug).
func slotResizeEdges(name string) uint8 {
	switch name {
	case slotControls:
		// v1.52.0 (Tifera): the control-button block is WIDTH-resizable — the
		// override width drives the row-wrap edge (controlsBlockOrigin), so
		// narrowing it re-wraps the buttons into a taller stack. Height stays
		// content-driven (the rows decide it), so the horizontal edges stay
		// inert rather than pretending to work.
		return edgeL | edgeR
	case slotTabBar:
		// The server-tab strip sizes itself from its chips, so resizing it
		// would do nothing — move-only.
		return 0
	case slotToolbox:
		// The compact toolbox strip sizes itself from its fixed chip count
		// (compactToolboxStripRect), so a resize would do nothing — move-only,
		// exactly like the server-tab strip.
		return 0
	default:
		return edgeL | edgeR | edgeT | edgeB
	}
}

// slotResizable reports whether a slot honours ANY resize edge (label + handle
// gating; the per-edge mask above does the precise work).
func slotResizable(name string) bool { return slotResizeEdges(name) != 0 }

// pickResizeSlot chooses which slot a resize grip at (mx,my) targets. It RESPECTS the
// hovered box — the same box move would act on (the highlighted, Tab-selectable one) —
// so "if I can move it, I can resize it": resize the hovered box when it's resizable
// and its edge is gripped, otherwise don't resize it (the caller moves it). This is the
// fix for "boxes in the middle won't resize": the old code resized the smallest gripped
// box across ALL boxes, so a neighbour merely touching the edge (or the move-only
// control block) would steal the grip. Only when the cursor is over NO box (a grip in
// the outer margin between/around boxes) does it fall back to the smallest gripped
// RESIZABLE box. Move-only slots are always skipped. Pure + testable.
func pickResizeSlot(reg map[string]slotInfo, keys []string, hoverKey string, mx, my, margin int32) (string, uint8) {
	if hoverKey != "" { // pointing at a box → only that box may resize (matches move)
		if e := classicEdgeAt(mx, my, reg[hoverKey].cur, margin) & slotResizeEdges(hoverKey); e != 0 {
			return hoverKey, e
		}
		return "", 0
	}
	best := int64(-1) // cursor outside every box: smallest gripped resizable wins
	bestKey, bestEdges := "", uint8(0)
	for _, k := range keys {
		r := reg[k].cur
		if e := classicEdgeAt(mx, my, r, margin) & slotResizeEdges(k); e != 0 {
			if area := int64(r.W) * int64(r.H); best < 0 || area < best {
				bestKey, bestEdges, best = k, e, area
			}
		}
	}
	return bestKey, bestEdges
}

// classicHandles returns the 8 resize handles (4 corners + 4 edge midpoints) of r
// so the editor can paint them — making "drag an edge to resize one dimension"
// discoverable.
func classicHandles(r sdl.Rect) [8]sdl.Rect {
	const hp = layoutHandlePx
	cx := r.X + r.W/2 - hp/2
	cy := r.Y + r.H/2 - hp/2
	return [8]sdl.Rect{
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

// startClassicEdit arms the default-courtroom slot editor. Open modals close
// (they'd be fenced shut); mirrors startLayoutEdit.
func (a *App) startClassicEdit() {
	a.ensureClassicOv()
	a.classicEdit = true
	// A1 Phase 1: the compact toolbox (grip + per-piece hide/show panel) now SURVIVES
	// into edit mode — it's the clean replacement for the retired top-band chip strip,
	// so toolboxPinned/toolboxPieces are left as the user set them (they draw
	// post-courtroom with the fence released; the strip is gone).
	a.showIni, a.showEvid, a.showModcall, a.showLogin, a.showPair = false, false, false, false, false
	a.showModDash, a.banBoxKind, a.showCMPanel = false, 0, false
	a.showDebugPanel, a.showFxPicker = false, false
	a.bgPick.show = false
	a.classicEditKey = ""
	a.classicEditDrag = 0
	a.classicEditEdges = 0
	a.classicEditMoved = false
	a.classicUndo, a.classicRedo = nil, nil
	a.classicPickSig, a.classicPickIdx = "", 0
	a.layoutSnap = true        // tidy placement by default; toggle off in the editor
	a.layoutAspect = true      // keep the stage 4:3 while resizing (toggle off for a free / letterboxed stage)
	a.layoutProfileCursor = -1 // no saved profile applied via the banner chip yet this edit
	// layoutMagnetOff is NOT reset here: the sibling magnet applies in normal play
	// too, so its zero value (magnet on) must persist across editor sessions — the
	// chip is the only thing that flips it.
	a.pushDebug("edit layout (default courtroom): drag to move, edges/corners to resize, Ctrl+Z to undo, Esc to finish")
}

// stopClassicEdit disarms and releases the input fence.
func (a *App) stopClassicEdit() {
	a.classicEdit = false
	a.classicEditKey = ""
	a.classicEditDrag = 0
	a.classicEditEdges = 0
	a.classicUndo, a.classicRedo = nil, nil // free the history (it's edit-session-scoped)
	a.ctx.modalOn = false
}

// classicEditFence claims the pointer for the slot editor BEFORE the default
// courtroom's widgets draw — they see hovering()==false and stay inert while the
// editor reads raw cursor coordinates (pointIn). Mirrors layoutEditFence.
func (a *App) classicEditFence() {
	if a.classicEdit {
		a.ctx.modalOn = true
	}
}

// controlsBlockOrigin computes the control-button block's draw origin (clusterX,
// blockY), its vertical offset from the default top (dy), and the row-wrap edge
// (clusterRight) from the block's slot override, if present. Un-edited, the
// content width is w-2*pad — clusterX==pad, dy==0, clusterRight==w-pad, so the
// un-edited courtroom stays byte-identical (the safety invariant). An override
// translates the block AND, since v1.52.0 (Tifera), its WIDTH drives the wrap
// edge: narrowing re-wraps the rows into a taller stack (widgets below anchor
// to the re-wrapped bottom, keeping clear of it). ctrlBlockMinW floors the
// width so every button stays reachable; an override height is ignored (the
// rows decide it). Old move-only overrides saved W as the full content width,
// so they land exactly on the default wrap — nothing shifts on upgrade. Pure +
// alloc-free so the invariant is unit-pinnable.
// r is the slot's RESOLVED rect (anchoredRect — window pins included); ok
// mirrors the override's presence.
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

// viewportOverridden reports whether the user has dragged/resized the stage in
// the classic editor. The View knob and the edge divider then defer to that
// override (it would otherwise change vpPct silently, which the override shadows)
// until the box is reset.
func (a *App) viewportOverridden() bool {
	_, ok := a.classicOv[slotViewport]
	return ok
}

// clearClassicSlot drops one slot's override from both the durable pref and the
// App-local snapshot so it reverts to the computed default the same frame. The
// window pin goes with it (config's ClearClassicSlot already cascades).
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

// cloneClassicOv copies the override map for the undo history — the map is a reference,
// so aliasing it would let a later edit silently mutate a snapshot (the classic undo
// bug). An empty map clones to nil = the "no overrides" state, so it round-trips.
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

// pushClassicUndo snapshots the overrides BEFORE an edit and forks history (a fresh edit
// drops the redo stack). Bounded by layoutUndoCap; edit-mode only, never on the render
// frame, so the alloc-free guarantee for normal play is untouched.
func (a *App) pushClassicUndo() {
	a.classicUndo = append(a.classicUndo, cloneClassicOv(a.classicOv))
	if len(a.classicUndo) > layoutUndoCap {
		a.classicUndo = a.classicUndo[1:]
	}
	a.classicRedo = a.classicRedo[:0]
}

// restoreClassicOv applies an undo/redo snapshot: it becomes the live overrides AND the
// durable pref (so the result survives a relog, like the themed editor's restoreLayout).
// The snapshot is cloned so the live map never aliases a history entry.
func (a *App) restoreClassicOv(snap map[string][4]float64) {
	a.classicOv = cloneClassicOv(snap)
	a.d.Prefs.SetClassicLayout(a.classicOv)
}

// classicEditUndo / classicEditRedo swap the live override map with a history
// snapshot and re-sync the durable pref, so a misdrag is one keystroke away —
// and the result persists. (Right-click only resets a box to DEFAULT; undo gets
// a previous CUSTOM spot back.) Driven by editorUndoChord — Ctrl chords arrive
// on the hotkey channel, so the editor's draw loop never sees them.
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

// drawClassicEditor paints the slot-editor overlay and owns every interaction.
// Called LAST from drawCourtroom (default layout only), after every widget has
// registered its rect via slotRect this frame.
func (a *App) drawClassicEditor(w, h int32) {
	c := a.ctx
	if w <= 0 || h <= 0 {
		a.stopClassicEdit()
		return
	}

	// Banner GEOMETRY first — the click handlers below read these rects. The
	// paint happens at the very END of this function, so the editor's own
	// chrome always sits above slot outlines, name tags and the tab strip: a
	// box parked in the top strip used to plaster its tag straight over the
	// Snap/Done buttons (the "layering mess" playtest shot).
	doneBtn := sdl.Rect{X: w - 62 - pad, Y: 2, W: 62, H: classicBannerH - 4}
	resetBtn := sdl.Rect{X: doneBtn.X - 90 - 6, Y: 2, W: 90, H: classicBannerH - 4}
	snapBtn := sdl.Rect{X: resetBtn.X - 92 - 6, Y: 2, W: 92, H: classicBannerH - 4}
	gridBtn := sdl.Rect{X: snapBtn.X - 78 - 6, Y: 2, W: 78, H: classicBannerH - 4}
	aspectBtn := sdl.Rect{X: gridBtn.X - 80 - 6, Y: 2, W: 80, H: classicBannerH - 4}
	// Magnet chip (Phase 3): persistent toggle beside Snap for the piece-to-piece
	// sibling magnet, so it can be loosened without holding Shift on every drag.
	magnetBtn := sdl.Rect{X: aspectBtn.X - editChipMagnetW - 6, Y: 2, W: editChipMagnetW, H: classicBannerH - 4}
	// Profile chip (Phase 3): surfaces the existing full-state layout-profile
	// system (applyProfile) as a banner control — a click cycles to the next saved
	// profile. Drawn only when at least one profile exists (else it would be dead).
	profileBtn := sdl.Rect{X: magnetBtn.X - editChipProfileW - 6, Y: 2, W: editChipProfileW, H: classicBannerH - 4}

	pressed := c.mouseDown && !a.classicEditPrev
	a.classicEditPrev = c.mouseDown

	// Undo / redo (Ctrl+Z / Ctrl+Y) fire from editorUndoChord (handleHotkeys):
	// Ctrl chords ride c.hotkey, never c.keyPressed, so an in-draw keyPressed
	// check here was dead code — the chord had already been routed away.

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
	// Grid chip: cycles the snap step (4 → 8 → 16 → 32, persisted) — playtest
	// (Tifera): "allow us to edit the snap grid".
	if c.clicked && pointIn(c.mouseX, c.mouseY, gridBtn) {
		a.d.Prefs.SetLayoutGridSize(nextLayoutGridSize(a.d.Prefs.LayoutGridSize()))
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, aspectBtn) {
		a.layoutAspect = !a.layoutAspect // lock/unlock the stage's 4:3 while resizing it
		// Turning it ON snaps the stage to 4:3 RIGHT NOW (width wins) — the old
		// behaviour only constrained future resize drags, which read as "the 4:3
		// button does nothing" in the playtest.
		if a.layoutAspect {
			a.snapViewportTo43(w, h)
		}
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, magnetBtn) {
		a.layoutMagnetOff = !a.layoutMagnetOff // persistent sibling-magnet toggle (session-only, like Snap)
	}
	if c.clicked && a.hasLayoutProfiles() && pointIn(c.mouseX, c.mouseY, profileBtn) {
		a.cycleLayoutProfile() // apply the next saved full-state profile (applyProfile)
		// Return like "Reset all" does: applyProfile replaced the whole override map,
		// so this frame's slotReg (built earlier from the OLD overrides) is stale —
		// bail and let the next frame rebuild from the applied profile.
		return
	}

	// Pop-out tray (bottom): tear a log tab out into its own movable panel, or
	// redock it. overTray suppresses a slot-move when you press a chip. (torntabs.go)
	overTray := a.drawClassicTabTray(w, h)
	// The stacked top chrome now ends at the tab-tray bottom (the full-width toolbox
	// strip that used to sit under it is retired — A1 Phase 1). Slot name tags clamp
	// below this so a box parked in the top strip can't plaster its tag over the
	// editor's own controls (drawSlotTag reads classicChromeBot).
	a.classicChromeBot = classicTabTrayBot
	// Compact toolbox (bottom-right grip → Theater / Edit / Hide-UI): it now draws in
	// edit mode too (post-courtroom, app.go), so a press over its strip or open
	// pieces panel must suppress a slot-move — else the click would grab whatever box
	// sits under the bottom-right corner. Mirrors overTray for the top tab tray.
	overToolbox := a.editOverToolbox(w, h)

	// Slot names this frame, stable order.
	keys := make([]string, 0, len(a.slotReg))
	for k := range a.slotReg {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Hover: the stack of boxes under the cursor, SMALLEST area first (so a small box on
	// a big one is grabbable), with Tab cycling which is armed — now that there are many
	// slots they really can overlap (drag the control block over the emote grid, etc.).
	// The pick index resets whenever the stack under the cursor changes.
	hoverKey := ""
	var stack []string
	if a.classicEditDrag != 0 {
		hoverKey = a.classicEditKey // mid-drag: keep the grabbed box highlighted
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

	// A pins the hovered box to a window corner / the centre (cycles off →
	// ↖ → ↗ → ↙ → ↘ → ● — Tifera: "anchor layout items to corners and center
	// of the entire screen"): a pinned box keeps its PIXEL offsets from that
	// reference when the window resizes, instead of drifting with the
	// fractions. Consumed so a char keybind on A can't also fire mid-edit.
	if a.classicEditDrag == 0 && hoverKey != "" && c.keyPressed == sdl.K_a {
		a.cycleSlotAnchor(hoverKey, w, h)
		c.keyPressed = 0
	}

	// R rotates the hovered box's texture-backed chrome (A4): coarse 0/90/180/270,
	// Shift+R a fine 15° step. Non-undoable like the anchor pin. Consumed so a char
	// keybind on R can't also fire mid-edit. No classic slot is texture-backed
	// today, so cycleSlotRotation reports an "n/a" hint (honest, not silent).
	if a.classicEditDrag == 0 && hoverKey != "" && c.keyPressed == sdl.K_r {
		a.cycleSlotRotation(hoverKey, sdl.GetModState()&sdl.KMOD_SHIFT != 0)
		c.keyPressed = 0
	}

	// Begin a drag on press: RESIZE (an edge/corner is gripped) takes priority over
	// MOVE. pickResizeSlot targets the HOVERED box (so resize hits the box you're
	// pointing at, like move does) and skips move-only slots so the little control
	// block can't steal a neighbour's edge grip.
	// A drag can begin ANYWHERE except over the editor's own chips (so the top strip
	// is grabbable instead of a blanket no-drag banner row) or the pop-out tray.
	overChip := pointIn(c.mouseX, c.mouseY, doneBtn) || pointIn(c.mouseX, c.mouseY, resetBtn) ||
		pointIn(c.mouseX, c.mouseY, snapBtn) || pointIn(c.mouseX, c.mouseY, aspectBtn) ||
		pointIn(c.mouseX, c.mouseY, gridBtn)
	if pressed && a.classicEditDrag == 0 && !overChip && !overTray && !overToolbox {
		// Alt forces a MOVE (skips the resize-edge test). A small widget — a single
		// button — is almost ALL edge, so a plain drag kept resizing it instead of
		// moving it (playtest: "I try to drag Disconnect and it just resizes unless I
		// make it very big"). Hold Alt to grab-and-move anything regardless of size.
		// Shift stays pixel-precise, so Alt+Shift = a precise move.
		resizeKey, resizeEdges := "", uint8(0)
		if sdl.GetModState()&sdl.KMOD_ALT == 0 {
			resizeKey, resizeEdges = pickResizeSlot(a.slotReg, keys, hoverKey, c.mouseX, c.mouseY, layoutHandlePx)
		}
		switch {
		case resizeKey != "":
			a.classicEditKey, a.classicEditDrag, a.classicEditEdges = resizeKey, 2, resizeEdges
		case hoverKey != "":
			a.classicEditKey, a.classicEditDrag, a.classicEditEdges = hoverKey, 1, 0
		}
		if a.classicEditDrag != 0 {
			a.classicEditStart = [2]int32{c.mouseX, c.mouseY}
			a.classicEditBase = a.slotReg[a.classicEditKey].cur
			a.classicEditMoved = false
			a.pushClassicUndo() // snapshot before the move/resize (popped at release if it was a no-op)
		}
	}

	// Right-click resets the hovered slot to its computed default (undoable, but only
	// snapshot when there's actually an override to clear — else a right-click on a
	// default box would litter the history with no-ops).
	if c.rightClicked && hoverKey != "" && !overTray && !overToolbox {
		if _, ov := a.classicOv[hoverKey]; ov {
			a.pushClassicUndo()
			a.clearClassicSlot(hoverKey)
		}
	}

	// Live drag: screen deltas applied directly (screen space), clamped on-stage,
	// snapped, then written to the App-local override (px→frac) so the widget
	// redraws at the new spot NEXT frame.
	if a.classicEditDrag != 0 && c.mouseDown && a.classicEditKey != "" {
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
		// Hold Shift while dragging = pixel-precise: it bypasses the grid AND the
		// alignment magnet for this drag (the "Snap" button is the persistent
		// toggle). GetModState is a cheap render-thread query, so the default
		// snap path stays allocation-free.
		a.alignGuides = a.alignGuides[:0]
		if a.layoutSnap && sdl.GetModState()&sdl.KMOD_SHIFT == 0 {
			g := int32(a.d.Prefs.LayoutGridSize())
			r.X, r.Y, r.W, r.H = classicSnapTo(r.X, g), classicSnapTo(r.Y, g), classicSnapTo(r.W, g), classicSnapTo(r.H, g)
			if r.W < classicMinPx {
				r.W = classicMinPx
			}
			if r.H < classicMinPx {
				r.H = classicMinPx
			}
			// Alignment magnet (Tifera: "nothing was aligning properly" — the
			// defaults aren't grid-aligned, so the grid alone can't make two
			// boxes flush): the dragged box's edges/centre snap to the OTHER
			// boxes' edges/centres and the window edges/centre, overriding the
			// grid on the matched axis. Guides draw in the overlay below.
			a.alignScratch = a.alignScratch[:0]
			for _, k := range keys {
				if k != a.classicEditKey {
					a.alignScratch = append(a.alignScratch, a.slotReg[k].cur)
				}
			}
			r, a.alignGuides = alignRect(r, a.alignScratch, w, h, a.classicEditDrag == 1, a.classicEditEdges, a.alignGuides)
		}
		// Lock the stage to 4:3 while resizing (banner toggle): drive the other
		// dimension from the edge you grabbed, so the scene never stretches off
		// 4:3. Applied AFTER the grid snap — snapping W and H independently used
		// to re-break the ratio the lock had just computed (the driven dimension
		// deliberately leaves the grid; the ratio is the point of the lock).
		if a.layoutAspect && a.classicEditDrag != 1 && a.classicEditKey == slotViewport {
			e := a.classicEditEdges
			if e&(edgeL|edgeR) != 0 { // a side/corner handle → width drives height
				r.H = r.W * 3 / 4
			} else if e&(edgeT|edgeB) != 0 { // a top/bottom handle → height drives width
				r.W = r.H * 4 / 3
			}
		}
		// Keep it on-screen (solid feel; below the editor banner).
		if r.X < 0 {
			r.X = 0
		}
		if r.Y < 0 { // the top strip is usable now (the banner is translucent and only its chips block a drag)
			r.Y = 0
		}
		if r.X+r.W > w {
			r.X = w - r.W
		}
		if r.Y+r.H > h {
			r.Y = h - r.H
		}
		if a.classicEditMoved {
			if a.classicOv == nil {
				a.classicOv = make(map[string][4]float64, classicSlotRegCap)
			}
			a.classicOv[a.classicEditKey] = rectToFrac(r, w, h)
			// A pinned slot's override now describes THIS window size —
			// re-base the local anchor so resolution round-trips exactly.
			a.syncAnchorWindow(a.classicEditKey, w, h)
		}
	}

	// Release persists the edit (a no-move click changes nothing — and discards the
	// begin snapshot so a bare click doesn't leave a no-op on the undo stack). The old
	// "drag a slotted piece onto the toolbox to hide it" gesture is retired with the
	// top-band strip (A1 Phase 1): per-piece hide/show now lives entirely in the
	// pinned pieces panel (drawToolboxPieces), which is cleaner and reachable in edit.
	if a.classicEditDrag != 0 && !c.mouseDown {
		switch {
		case a.classicEditMoved && a.classicEditKey != "":
			if ov, ok := a.classicOv[a.classicEditKey]; ok {
				a.d.Prefs.SetClassicSlot(a.classicEditKey, ov)
				// Persist the pin's re-based window size with the override.
				if m := a.slotAnchorMode(a.classicEditKey); m != "" {
					a.d.Prefs.SetClassicAnchor(a.classicEditKey, config.ClassicAnchor{Mode: m, WinW: int(w), WinH: int(h)})
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

	// Overlay: a QUIET outline on every slot so the layout structure reads, but the
	// full treatment — bright border + resize handles + a name tag — ONLY on the box
	// under the cursor (or being dragged). This keeps the editor from being a wall of
	// boxes, handles and labels, and a slot's name never sits on the real buttons it
	// covers (the tag floats just above the box). A dragged slot reflects its live
	// (this-frame) override position.
	dimEdge := blendCol(ColAccent, ColBackground, 0.6)
	for _, k := range keys {
		r := a.slotReg[k].cur
		if a.classicEditDrag != 0 && k == a.classicEditKey {
			if ov, ok := a.classicOv[k]; ok {
				r = a.anchoredRect(k, ov, w, h)
			}
		}
		switch {
		case k == a.classicEditKey:
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
	// Alignment guides: full-length hairlines at whatever the dragged box just
	// snapped flush to — the Inkscape-style "you are aligned" feedback.
	if a.classicEditDrag != 0 {
		for _, g := range a.alignGuides {
			if g.vertical {
				c.Fill(sdl.Rect{X: g.pos, Y: 0, W: 1, H: h}, ColTierGreen)
			} else {
				c.Fill(sdl.Rect{X: 0, Y: g.pos, W: w, H: 1}, ColTierGreen)
			}
		}
	}
	// Editor chrome LAST — topmost over every outline, tag and strip. It stays
	// translucent so widgets parked in the top strip remain visible through it
	// (you can drag boxes up there — playtest: "make use of that space"). A
	// side effect of painting after the click handlers: the Snap/4:3 labels
	// show the state a click just set, not last frame's.
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
	// The hint clips to end before the LEFTMOST chip actually drawn: the profile
	// chip when it exists, else the magnet chip (both sit left of aspect). Using
	// aspectBtn.X would let the hint overdraw the two Phase-3 chips.
	leftmostChipX := magnetBtn.X
	if a.hasLayoutProfiles() {
		leftmostChipX = profileBtn.X
	}
	hintX := pad + c.TextWidth("Edit Layout") + 18
	c.LabelClipped(hintX, 6, leftmostChipX-hintX-10, "Drag to move · edge to resize · green lines = aligned · Alt = move · Shift = precise", ColTextDim)
	a.rawChip(aspectBtn, aspectLabel)
	a.rawChip(magnetBtn, magnetLabel)
	if name := a.currentLayoutProfileLabel(); name != "" {
		a.rawChip(profileBtn, name)
	}
	a.rawChip(gridBtn, fmt.Sprintf("Grid: %d", a.d.Prefs.LayoutGridSize()))
	a.rawChip(snapBtn, snapLabel)
	a.rawChip(resetBtn, "Reset all")
	a.rawChip(doneBtn, "Done")
	// Rot readout (A4): a passive chip beside the Grid/Aspect chips, shown only when
	// the hovered box carries a nonzero angle. Classic slots don't rotate today, so
	// this stays hidden in practice — kept for symmetry with the themed editor.
	if hoverKey != "" {
		if label := rotationChipLabel(a.classicRot[hoverKey]); label != "" {
			// Left of the Phase-3 chips (profile when present, else magnet) so it can't
			// overpaint them — same as the themed editor's rot chip.
			rotRightX := magnetBtn.X
			if a.hasLayoutProfiles() {
				rotRightX = profileBtn.X
			}
			rotBtn := sdl.Rect{X: rotRightX - 74 - 6, Y: 2, W: 74, H: classicBannerH - 4}
			a.rawChip(rotBtn, label)
		}
	}

	switch { // bottom line: the per-box context (kept off the busy top banner)
	case a.classicEditKey != "":
		c.Label(pad, h-22, "Moving "+classicSlotLabel(a.classicEditKey)+"  ·  release to save", ColText)
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

// handleEdgeMask maps each classicHandles index to the edges it grips (same
// order: 4 corners, then top/bottom/left/right midpoints).
var handleEdgeMask = [8]uint8{
	edgeL | edgeT, edgeR | edgeT, edgeL | edgeB, edgeR | edgeB,
	edgeT, edgeB, edgeL, edgeR,
}

// drawSlotHandles paints a slot's resize grips — only the handles whose every
// edge the slot honours (a width-only slot shows just its side midpoints, so
// the affordance never lies), each with a dark outline so the bright squares
// read on any background underneath. Move-only slots paint none.
func (a *App) drawSlotHandles(r sdl.Rect, key string, col sdl.Color) {
	allowed := slotResizeEdges(key)
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

// drawSlotTag floats a slot's name on a small dark pill just ABOVE the box (so it
// never covers the box's own content), tucking it just inside the top edge only when
// there's no room above. Drawn only for the hovered / active slot, so at most one tag
// shows at a time — no labels plastered across every box. Tags never enter the
// editor's stacked top chrome (banner + tray, bottom recorded in classicChromeBot =
// classicTabTrayBot): a box parked in the top strip gets its tag just BELOW the
// chrome instead of over the hint text and buttons.
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
