package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Reactions, UI side: floating-emoji overlay, react palette and piggyback on send (codec in courtroom/reactionwire.go).
// A queued reaction rides your next IC message as an invisible frame; AO2 and webAO see clean text.

const (
	// reactionFloatPct sizes the floating emoji as a percent of the UI font.
	reactionFloatPct = 220
	// reactionFloatLifeMs is one float's lifetime: rise + fade, then it's culled.
	reactionFloatLifeMs = 1600
	// reactionFloatRisePx is how far a float drifts upward over its life.
	reactionFloatRisePx = 130
	// reactionFloatsMax bounds the active floats (hard rule 4); the oldest is evicted past the cap.
	reactionFloatsMax = 16
	// reactionFadeInFrac and reactionFadeOutFrac are the lifetime fractions spent fading in and out.
	reactionFadeInFrac  = 0.15
	reactionFadeOutFrac = 0.35
	// reactionFloatBottomMargin is how far above the stage's bottom edge a float is born.
	reactionFloatBottomMargin = 22
	// reactionFloatSpreadPx is the horizontal band floats scatter across so they do not stack on one column.
	reactionFloatSpreadPx = 130
	// reactionMatchScan bounds how far back, newest first, the ref match looks.
	reactionMatchScan = 120

	// reactPaletteCols lays the fixed palette out as a compact grid above the React button.
	reactPaletteCols = 6
)

// reactionFloat is one emoji rising over the stage: index keys the cached badge, born drives the rise and fade.
type reactionFloat struct {
	index uint8
	born  time.Time
	xJit  int32
}

// spawnReactionFloat starts a float for a palette index; an unknown index, from a newer peer, is a no-op.
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

// drawReactionFloats renders and ages the active floats over vp, culling expired ones; no allocation on a warm cache.
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
		// Mirror the current clip rather than assume none, so a caller inside a pushClip cannot hit
		// the macOS Metal lazy-clip bug Badge.Draw describes.
		var clip *sdl.Rect
		if c.clipOn {
			clip = &c.clipRect
		}
		b.Draw(c.Ren, &a.reactRect, reactionAlpha(prog), c.RenderScalePct(), clip)
	}
	a.reactionFloats = a.reactionFloats[:w]
}

// reactionAlpha is the fade over a float's life: ramp up, hold, ramp down.
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

// ensureReactBadge returns the cached badge for a palette index at the device text scale; nil while the face loads.
// A UI-scale change purges every cached badge, or one built at the old scale would stay soft.
func (a *App) ensureReactBadge(index uint8) *render.Badge {
	if a.ctx.textDevPct != a.reactBadgeDevScale {
		a.purgeReactBadges()
	}
	if b, ok := a.reactBadges[index]; ok {
		return b
	}
	// emojiDeviceFont, not EmojiFont: Badge.Draw's device-exact path needs the glyph rasterized at the device scale.
	emoji := a.ctx.emojiDeviceFont(reactionFloatPct)
	if emoji == nil {
		return nil
	}
	s, ok := courtroom.ReactionEmoji(index)
	if !ok {
		return nil
	}
	b, err := render.RasterizeBadge(a.ctx.Ren, emoji, s, sdl.Color{R: 255, G: 255, B: 255, A: 255}, a.ctx.textDevPct)
	if err != nil || b == nil {
		return nil
	}
	if a.reactBadges == nil {
		a.reactBadges = make(map[uint8]*render.Badge, courtroom.ReactionCount())
	}
	a.reactBadges[index] = b
	a.reactBadgeDevScale = a.ctx.textDevPct
	return b
}

// purgeReactBadges destroys every cached reaction badge texture; render thread only.
func (a *App) purgeReactBadges() {
	for k, b := range a.reactBadges {
		if b != nil {
			b.Destroy()
		}
		delete(a.reactBadges, k)
	}
}

// warmReactBadges pre-builds every reaction badge once the emoji face lands, so the first float never hitches.
func (a *App) warmReactBadges() {
	if a.ctx.EmojiFont(reactionFloatPct) == nil {
		return
	}
	for i := 0; i < courtroom.ReactionCount(); i++ {
		a.ensureReactBadge(uint8(i))
	}
}

// onIncomingReaction floats a reaction whose ref matches a recent message, unless reactions or effects are off.
func (a *App) onIncomingReaction(r courtroom.WireReaction) {
	if a.d.Prefs.Load().HideReactions || a.d.Prefs.Load().DisableEffects {
		return // opted out of reactions specifically, or of every visual extra
	}
	if a.reactionTargetKnown(r.Ref) {
		a.spawnReactionFloat(r.Index)
	}
}

// reactionTargetKnown reports whether a recent IC log entry has the content ref, scanning newest-first.
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

// The IC bar has no React button, so nothing opens the palette; incoming floats still render.

// drawReactPalette draws the open palette above the React button and queues the clicked emoji against the target.
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

// queueReaction stores the chosen emoji against the snapshotted target; it rides the next IC send.
func (a *App) queueReaction(index uint8) {
	a.showReactPicker = false
	if a.reactTargetRef == 0 {
		return
	}
	a.pendingReact = courtroom.WireReaction{Ref: a.reactTargetRef, Index: index}
	a.pendingReactSet = true
}

// reactPickerFence manages the palette's modal each frame before the screen draws.
// c.modalOn persists across frames, so it must be released the frame the palette closes or the UI freezes.
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
