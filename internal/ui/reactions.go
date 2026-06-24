package ui

import (
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// Real reactions (#2), UI side. The wire codec lives in courtroom (reactionwire.go); this
// file is the parts that need SDL: the floating-emoji overlay over the stage, the react
// button + palette, and the per-message piggyback on send.
//
// Flow: you click React → pick an emoji from the palette → it's QUEUED (rides your next IC
// message as an invisible frame referencing the message you reacted to). Every AsyncAO
// viewer (including you, when your message echoes back) decodes the frame, matches the
// content-stable ref to a message in their IC log, and floats the emoji up from the stage.
// AO2 / webAO see clean text. Incoming floats display by default (like received sprite
// styles / profiles / effects); HideReactions is the viewer opt-out. The overlay is a
// 0-alloc early-return when nothing is floating, so a room with no reactions pays nothing.

const (
	// reactionFloatPct sizes the floating emoji as a percent of the UI font — chunky, so a
	// reaction reads at a glance over the busy stage.
	reactionFloatPct = 220
	// reactionFloatLifeMs is one float's lifetime: rise + fade, then it's culled.
	reactionFloatLifeMs = 1600
	// reactionFloatRisePx is how far a float drifts upward over its life.
	reactionFloatRisePx = 130
	// reactionFloatsMax bounds the active floats (hard rule §17.4): a burst of reactions
	// can't grow the slice without bound — the oldest float is evicted past the cap.
	reactionFloatsMax = 16
	// reactionFadeInFrac / reactionFadeOutFrac are the fractions of the lifetime spent
	// fading in (from 0) and fading out (to 0); full opacity in between.
	reactionFadeInFrac  = 0.15
	reactionFadeOutFrac = 0.35
	// reactionFloatBottomMargin is how far above the stage's bottom edge a float is born.
	reactionFloatBottomMargin = 22
	// reactionFloatSpreadPx is the horizontal band floats scatter across (so several
	// reactions don't stack on one column).
	reactionFloatSpreadPx = 130
	// reactionMatchScan bounds how far back the ref match looks (newest-first) — reactions
	// target recent messages, and a bound keeps the scan cheap on a long log.
	reactionMatchScan = 120

	// reactPaletteCols lays the fixed palette out as a compact grid above the React button.
	reactPaletteCols = 6
	// reactBtnW is the IC-bar React button width (fits "React" in the chrome font).
	reactBtnW = 58
)

// reactionFloat is one emoji rising over the stage. Plain data; the index keys the cached
// badge, born drives the rise + fade, xJit scatters it horizontally.
type reactionFloat struct {
	index uint8
	born  time.Time
	xJit  int32
}

// spawnReactionFloat starts a float for a palette index (no-op for an unknown index — a
// reaction from a newer peer). Past the cap the oldest float is dropped (bounded ring).
func (a *App) spawnReactionFloat(index uint8) {
	if _, ok := courtroom.ReactionEmoji(index); !ok {
		return
	}
	a.reactSpawnSeq++
	xJit := int32(a.reactSpawnSeq*53%reactionFloatSpreadPx) - reactionFloatSpreadPx/2
	f := reactionFloat{index: index, born: a.now(), xJit: xJit}
	if len(a.reactionFloats) >= reactionFloatsMax {
		copy(a.reactionFloats, a.reactionFloats[1:]) // drop the oldest
		a.reactionFloats[len(a.reactionFloats)-1] = f
		return
	}
	a.reactionFloats = append(a.reactionFloats, f)
}

// drawReactionFloats renders + ages the active floats over the stage rect vp, culling
// expired ones in place. Called from BOTH courtroom draw paths (classic + themed). A
// 0-alloc early return when nothing is floating keeps the common frame free (the zero-perf
// constraint for received content); with floats active it is also 0-alloc on a warm badge
// cache (a reused scratch rect, cached badges, no per-frame build).
func (a *App) drawReactionFloats(vp sdl.Rect) {
	if len(a.reactionFloats) == 0 {
		return
	}
	c := a.ctx
	now := a.now()
	baseX := vp.X + vp.W/2
	baseY := vp.Y + vp.H - reactionFloatBottomMargin
	w := 0 // in-place compaction write index (drops expired floats)
	for _, f := range a.reactionFloats {
		ageMs := now.Sub(f.born).Milliseconds()
		if ageMs >= reactionFloatLifeMs {
			continue
		}
		a.reactionFloats[w] = f
		w++
		b := a.ensureReactBadge(f.index)
		if b == nil {
			continue // face not loaded yet; the float still ages out
		}
		prog := float64(ageMs) / float64(reactionFloatLifeMs)
		bw, bh := b.Size()
		x := baseX + f.xJit - bw/2
		y := baseY - int32(float64(reactionFloatRisePx)*prog) - bh
		a.reactRect = sdl.Rect{X: x, Y: y, W: bw, H: bh}
		b.Draw(c.Ren, &a.reactRect, reactionAlpha(prog))
	}
	a.reactionFloats = a.reactionFloats[:w]
}

// reactionAlpha is the fade envelope over a float's life: ramp up, hold, ramp down.
func reactionAlpha(prog float64) uint8 {
	switch {
	case prog < reactionFadeInFrac:
		return uint8(255 * prog / reactionFadeInFrac)
	case prog > 1-reactionFadeOutFrac:
		return uint8(255 * (1 - prog) / reactionFadeOutFrac)
	default:
		return 255
	}
}

// ensureReactBadge returns the cached float badge for a palette index, building it once
// from the colour-emoji face. Returns nil (not cached) while the face is still loading, so
// it retries on a later frame; warmReactBadges pre-builds all of them once the face lands.
func (a *App) ensureReactBadge(index uint8) *render.Badge {
	if b, ok := a.reactBadges[index]; ok {
		return b
	}
	emoji := a.ctx.EmojiFont(reactionFloatPct)
	if emoji == nil {
		return nil
	}
	s, ok := courtroom.ReactionEmoji(index)
	if !ok {
		return nil
	}
	b, err := render.RasterizeBadge(a.ctx.Ren, emoji, s, sdl.Color{R: 255, G: 255, B: 255, A: 255})
	if err != nil || b == nil {
		return nil
	}
	if a.reactBadges == nil {
		a.reactBadges = make(map[uint8]*render.Badge, courtroom.ReactionCount())
	}
	a.reactBadges[index] = b
	return b
}

// warmReactBadges pre-builds every reaction badge once the emoji face is available, so the
// first real float never hitches on a synchronous build (and the 0-alloc draw test isn't
// measuring a one-time raster). Called from pollEmojiFont when the face lands.
func (a *App) warmReactBadges() {
	if a.ctx.EmojiFont(reactionFloatPct) == nil {
		return
	}
	for i := 0; i < courtroom.ReactionCount(); i++ {
		a.ensureReactBadge(uint8(i))
	}
}

// onIncomingReaction handles a decoded reaction frame: float it only if its ref matches a
// recent message we've actually seen (a stray / late-join ref matches nothing — benign).
// Gated by the HideReactions viewer opt-out.
func (a *App) onIncomingReaction(r courtroom.WireReaction) {
	if a.d.Prefs.HideReactionsOn() {
		return
	}
	if a.reactionTargetKnown(r.Ref) {
		a.spawnReactionFloat(r.Index)
	}
}

// reactionTargetKnown reports whether a recent IC log entry has the given content ref. It
// scans newest-first (so a same-content hash collision resolves to the most recent message)
// and stops at the first match, bounded to the last reactionMatchScan entries.
func (a *App) reactionTargetKnown(ref uint32) bool {
	if ref == 0 {
		return false
	}
	n := len(a.icLog)
	lo := n - reactionMatchScan
	if lo < 0 {
		lo = 0
	}
	for i := n - 1; i >= lo; i-- {
		if a.icLog[i].ref == ref {
			return true
		}
	}
	return false
}

// --- react trigger: button + palette --------------------------------------------------

// reactButton draws the IC-bar React button (stores its rect so the palette can anchor
// above it) and reports a toggle click. Accent border while the palette is open OR a
// reaction is queued — the "queued" affordance the user otherwise couldn't see (the
// reaction doesn't send until their next message). Raw pointIn hit test so it works under
// the palette's modal fence, like the emoji button.
func (a *App) reactButton(btn sdl.Rect) bool {
	c := a.ctx
	a.reactBtnRect = btn
	label, border, txt := "React", ColTextDim, ColText
	if a.showReactPicker || a.pendingReactSet {
		border, txt = ColAccent, ColAccent
	}
	clicked := c.ButtonCol(btn, label, ColPanel, ColPanelHi, border, txt)
	tip := "React to the last message with an emoji — it rides your next message so other AsyncAO players see it float (AO2/webAO see plain text)."
	if a.pendingReactSet {
		if e, ok := courtroom.ReactionEmoji(a.pendingReact.Index); ok {
			tip = "Reaction " + e + " queued — it sends with your next message. Click to change."
		}
	}
	c.Tooltip(btn, tip)
	return clicked
}

// toggleReactPicker opens or closes the palette. Opening SNAPSHOTS the current "last
// message" as the reaction target, so a message arriving while the palette is open doesn't
// shift what you react to (the click consumes the snapshot). Opening the react palette
// closes the emoji picker (one modal-fenced popup at a time).
func (a *App) toggleReactPicker() {
	if a.showReactPicker {
		a.showReactPicker = false
		return
	}
	a.showReactPicker = true
	a.showEmojiPicker = false
	a.reactTargetRef = a.lastReactRef
	a.reactTargetName = a.lastReactName
}

// drawReactPalette draws the open reaction palette (a small grid anchored above the React
// button) and queues the clicked emoji against the snapshotted target. No-op when closed;
// a click outside the panel and the button closes it. Called once per frame after the
// courtroom (covers both layouts), like the emoji picker.
func (a *App) drawReactPalette(w, h int32) {
	if !a.showReactPicker {
		return
	}
	c := a.ctx
	a.ensureEmojiFontLoad()
	n := courtroom.ReactionCount()
	rows := int32((n + reactPaletteCols - 1) / reactPaletteCols)
	const headerH = 20
	pw := int32(reactPaletteCols*emojiCellPx) + 8
	ph := rows*emojiCellPx + 8 + headerH
	px := a.reactBtnRect.X
	if px+pw > w-pad {
		px = w - pad - pw
	}
	if px < pad {
		px = pad
	}
	py := a.reactBtnRect.Y - ph - 4
	if py < pad {
		py = a.reactBtnRect.Y + a.reactBtnRect.H + 4 // no room above → drop below the bar
	}
	panel := sdl.Rect{X: px, Y: py, W: pw, H: ph}
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)
	// Header: who you're reacting to (or a hint when there's nothing yet).
	head := "React to last message"
	if a.reactTargetName != "" {
		head = "React to " + a.reactTargetName
	} else if a.reactTargetRef == 0 {
		head = "No message to react to yet"
	}
	c.Label(panel.X+5, panel.Y+4, head, ColTextDim)
	gridY := panel.Y + headerH
	for i := 0; i < n; i++ {
		e, _ := courtroom.ReactionEmoji(uint8(i))
		cx := panel.X + 4 + int32(i%reactPaletteCols)*emojiCellPx
		cy := gridY + int32(i/reactPaletteCols)*emojiCellPx
		cell := sdl.Rect{X: cx, Y: cy, W: emojiCellPx, H: emojiCellPx}
		if pointIn(c.mouseX, c.mouseY, cell) {
			c.Fill(cell, ColPanelHi)
			if c.clicked {
				a.queueReaction(uint8(i))
			}
		}
		a.labelEmoji(c.font, c.EmojiFont(emojiPickerPct), cx+5, cy+4, emojiCellPx, e, ColText)
	}
	if c.clicked && !pointIn(c.mouseX, c.mouseY, panel) && !pointIn(c.mouseX, c.mouseY, a.reactBtnRect) {
		a.showReactPicker = false
	}
}

// queueReaction stores the chosen emoji against the snapshotted target message; it rides
// the next IC send. A click with no target (nothing reactable yet) just closes the palette.
func (a *App) queueReaction(index uint8) {
	a.showReactPicker = false
	if a.reactTargetRef == 0 {
		return
	}
	a.pendingReact = courtroom.WireReaction{Ref: a.reactTargetRef, Index: index}
	a.pendingReactSet = true
}

// reactPickerFence manages the palette's modal fence each frame, BEFORE the screen draws —
// mirrors emojiPickerFence (c.modalOn persists across frames, so it MUST be released the
// frame the palette closes, or the UI freezes). Courtroom-only, force-closed elsewhere.
func (a *App) reactPickerFence(c *Ctx) {
	courtroomActive := a.screen == ScreenCourtroom && !a.gifExporting && !a.replaying && !a.makerOpen
	if a.showReactPicker && !courtroomActive {
		a.showReactPicker = false
	}
	if a.showReactPicker {
		c.modalOn = true
		a.reactFenceOn = true
	} else if a.reactFenceOn {
		c.modalOn = false
		a.reactFenceOn = false
	}
}
