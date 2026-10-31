package ui

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// previewEmote points the hover preview at an emote: its looping pre-animation if any, else the talking sprite.
func (a *App) previewEmote(char string, e *courtroom.Emote) {
	// Caption set here where the *Emote is known; previewNameFor ties it to this base so other setters invalidate it.
	name := e.Comment
	if name == "" {
		name = e.Anim
	}
	if e.Preanim != "" && e.Preanim != "-" { // "-" / "" = no preanim (AO convention)
		a.previewBase = a.urls.Emote(char, e.Preanim, courtroom.EmotePreanim)
		a.previewName, a.previewNameFor = name, a.previewBase
		a.d.Manager.Prefetch(a.previewBase, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preanim preview)
		return
	}
	a.previewBase = a.urls.Emote(char, e.Anim, courtroom.EmoteTalk)
	a.previewName, a.previewNameFor = name, a.previewBase
	a.d.Manager.PrefetchChain(a.previewBase, a.urls.EmoteAlts(char, e.Anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
}

// followPinnedPreview retargets an open, pinned preview box to the picked emote; others are left alone.
func (a *App) followPinnedPreview(char string, e *courtroom.Emote) {
	if a.previewBase == "" || !a.previewIsPinned() {
		return
	}
	a.previewEmote(char, e)
}

// drawSpritePreviewFallback paints the preview box on passes where the emote grid did not (panel hidden, no
// `emotes` rect, char.ini loading); themed picks the dismissal that layout's grid uses.
func (a *App) drawSpritePreviewFallback(themed bool) {
	if a.previewBase == "" || a.emoteGridDrewPreview {
		return
	}
	// Set before drawing: the flag clears once per frame, so a second call in the frame does not draw the box again.
	a.emoteGridDrewPreview = true
	a.drawSpritePreview(a.winW, a.winH, false, a.previewedEmoteName())
	if themed {
		a.dismissPreviewOnClick()
		return
	}
	a.closeSpritePreviewOnLeave()
}

// previewedEmoteName returns the name previewEmote cached for the current previewBase, or "" after another setter.
func (a *App) previewedEmoteName() string {
	if a.previewBase == "" || a.previewNameFor != a.previewBase {
		return ""
	}
	return a.previewName
}

func (a *App) drawEmoteRow(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	// The grid has no backing panel by default; a set colour adds one.
	if col, ok := a.partPanel(partEmotes); ok {
		c.Fill(r, col)
	}
	if a.charINIBusy {
		c.Label(r.X, r.Y, "Loading emotes...", ColTextDim)
		return
	}
	me := a.activeCharName() // iniswap override drives emotes + buttons
	useImages := a.d.Prefs.Load().EmoteButtonImages
	// Read once per grid draw, keeping the pref lock out of the per-cell loop.
	hoverNamesOn := a.d.Prefs.Load().EmoteHoverNamesOn
	hoverNamesDelay := a.d.Prefs.EmoteHoverNamesDelay()
	a.refreshEmoteView() // build the favourite set + the visible-index list (#77)
	vis := a.emoteVisible

	cellW, cellH := emoteBtnCell, emoteBtnCell
	if !useImages {
		cellW, cellH = emoteTextCellW, btnH
	}
	// Read once and threaded through, never per cell: that would put a pref lock in the per-button path.
	gap := int32(a.d.Prefs.EmoteGridGap())
	cols := (r.W + gap) / (cellW + gap)
	if cols < 1 {
		cols = 1
	}
	// Reserve the arrow row only when paging is needed; reserving only shrinks a page, so the choice cannot oscillate.
	gridH := r.H
	if len(vis) > int(cols*gridRows(gridH, cellH, gap)) {
		gridH = r.H - (btnH + 4)
	}
	// emotescroll.go derives the scroll range, number-key origin and page readout from this.
	a.setEmoteGridMetrics(int(cols), int(gridRows(gridH, cellH, gap)), len(vis))
	perPage := a.emotePerPage
	// Wheel scrolls by row, up = earlier; WheelIn consumes the scroll so nothing else moves.
	if a.emoteGridScrolls() {
		if d := c.WheelIn(r); d != 0 {
			a.scrollEmoteRows(int(-d) * emoteWheelRows)
		}
	}
	start := a.emoteFirstSlot()

	for slot := start; slot < len(vis) && slot < start+perPage; slot++ {
		i := vis[slot] // real index into a.emotes (favs-only filters which show)
		e := &a.emotes[i]
		label := e.Comment
		if label == "" {
			label = e.Anim
		}
		n := int32(slot - start)
		btn := sdl.Rect{
			X: r.X + (n%cols)*(cellW+gap),
			Y: r.Y + (n/cols)*(cellH+gap),
			W: cellW, H: cellH,
		}
		selected := i == a.emoteIdx
		if selected {
			c.Fill(sdl.Rect{X: btn.X - 2, Y: btn.Y - 2, W: btn.W + 4, H: btn.H + 4}, ColAccent)
		}
		var picked bool
		if useImages {
			picked = a.drawEmoteImageButton(btn, me, i, selected, label)
		} else {
			picked = c.Button(btn, label)
		}
		// The favourite star is drawn on top and wins the click over selecting the emote.
		if a.drawEmoteFavStar(btn, i) {
			// star toggled — swallow this cell's select for the frame
		} else if picked {
			a.selectEmote(i)
		}
		// Full-size preview after a 3 s hover; right-click opens it at once.
		if c.rightClicked && c.hovering(btn) {
			a.previewPinned = true // right-click pins the preview open until you close it
			// Pins this box only; the box's own pin button writes the sticky preference.
		}
		if c.HoverPreview("emote:"+e.Anim, btn) {
			a.previewEmote(me, e)
		}
		// Name tooltip, separate from the preview; hovering(btn) first so the id concat runs only for the hovered cell.
		if hoverNamesOn && c.hovering(btn) {
			c.TooltipAfterDelay("emotename:"+e.Anim, btn, label, hoverNamesDelay)
		}
	}
	if len(vis) == 0 && a.d.Prefs.Load().EmoteFavOnly {
		c.Label(r.X, r.Y+4, "No favourite emotes yet — turn off ★ Favs, then click the ★ on an emote.", ColTextDim)
	}

	// "<" and ">" move a whole page; the per-row step belongs to the wheel.
	if a.emoteGridScrolls() {
		cy := r.Y + r.H - btnH
		if c.Button(sdl.Rect{X: r.X, Y: cy, W: 30, H: btnH}, "<") && a.emoteCanScrollUp() {
			a.scrollEmotePages(-1)
		}
		if c.Button(sdl.Rect{X: r.X + 34, Y: cy, W: 30, H: btnH}, ">") && a.emoteCanScrollDown() {
			a.scrollEmotePages(1)
		}
		c.Label(r.X+72, cy+6, a.emotePageCounter(a.emotePageShown(), a.emotePagesShown(), len(vis)), ColTextDim)
	}

	// Default position is the grid's bottom-right corner, but each button can be moved and hidden like any control.
	if !a.panelHidden("ctrl.favsfilter") {
		a.drawEmoteFavToggle(a.slotRect("ctrl.favsfilter", sdl.Rect{X: r.X + r.W - 158, Y: r.Y + r.H - btnH, W: 64, H: btnH}, a.winW, a.winH))
	}
	if !a.panelHidden("ctrl.randchar") {
		rcb := a.slotRect("ctrl.randchar", sdl.Rect{X: r.X + r.W - 92, Y: r.Y + r.H - btnH, W: 92, H: btnH}, a.winW, a.winH)
		if c.Button(rcb, "Rand char") {
			a.randomChar()
		}
	}

	if a.previewBase != "" {
		// Clamp to the window, not the stage: the box is draggable anywhere.
		a.drawSpritePreview(a.winW, a.winH, false, a.previewedEmoteName()) // ARM 2: caption the emote name
		a.closeSpritePreviewOnLeave()
		a.emoteGridDrewPreview = true // tell drawSpritePreviewFallback to stand down
	}
}

// emoteHasPreanim reports whether an emote has a usable pre-animation ("-" and "" mean none).
func emoteHasPreanim(e *courtroom.Emote) bool {
	return e.Preanim != "" && e.Preanim != "-"
}

// selectEmote picks emote i, warms its art and sprites, refocuses IC input (AO2 focus_ic_input); ignores bad indices.
func (a *App) selectEmote(i int) {
	if i < 0 || i >= len(a.emotes) {
		return
	}
	a.emoteIdx = i
	a.icPreanim = emoteHasPreanim(&a.emotes[i]) // "Pre" auto-follows the pick (AO2-Client ui_pre, emotes.cpp:211-221)
	me := a.activeCharName()
	// Warm both states: _on for the selected cell and _off, which it falls back to while _on streams in.
	a.d.Manager.Prefetch(a.urls.EmoteButton(me, i+1, true), assets.AssetTypeEmoteButton, network.PriorityHigh)  // AssetType: EmoteButton (on)
	a.d.Manager.Prefetch(a.urls.EmoteButton(me, i+1, false), assets.AssetTypeEmoteButton, network.PriorityHigh) // AssetType: EmoteButton (off fallback)
	a.speculateEmote(me, &a.emotes[i])
	a.followPinnedPreview(me, &a.emotes[i]) // a pinned box follows the pick; a closed one stays closed
	a.ctx.FocusField("ic")
}

// emotePageCounter returns the "page x/y · N emotes" label, memoized so a stable grid draw allocates nothing.
func (a *App) emotePageCounter(page, pages, n int) string {
	if key := [3]int{page, pages, n}; a.emotePageLabel == "" || key != a.emotePageLabelKey {
		a.emotePageLabel = fmt.Sprintf("page %d/%d · %d emotes", page, pages, n)
		a.emotePageLabelKey = key
	}
	return a.emotePageLabel
}

// shownTotalMemo memoizes a list header's "<shown> / <total>" counter to avoid a per-frame allocation.
// Each list owns its own: Music and Areas can be visible at once, and a shared memo would rebuild twice a frame.
type shownTotalMemo struct {
	label        string
	shown, total int
}

// nowPlayingMemo memoizes the Music header's "Now playing: <track>" label to avoid a per-frame allocation.
type nowPlayingMemo struct {
	track string // the RAW wire entry the label was built from
	label string
}

// musicIconButton is one square icon control of the Music header; the tooltip is its accessible name.
func (a *App) musicIconButton(r sdl.Rect, k iconKind, tip string) bool {
	c := a.ctx
	hover := c.hovering(r)
	if hover {
		c.Fill(r, ColPanelHi)
	}
	c.Border(r, ColAccent)
	drawToolIcon(c, k, r, ColText)
	c.Tooltip(r, tip)
	return hover && c.clicked
}

// playRandomMusic plays a random visible row of a.musicRows, excluding collapsed and filtered rows like
// AO2's music_random (courtroom.cpp:5947-5965).
func (a *App) playRandomMusic() {
	if a.sess == nil {
		return
	}
	n := 0
	for _, mr := range a.musicRows {
		if !mr.header && mr.ti >= 0 && mr.ti < len(a.sess.Music) {
			n++
		}
	}
	if n == 0 {
		return
	}
	pick := rand.IntN(n)
	for _, mr := range a.musicRows {
		if mr.header || mr.ti < 0 || mr.ti >= len(a.sess.Music) {
			continue
		}
		if pick == 0 {
			a.requestMusicTrack(a.sess.Music[mr.ti])
			return
		}
		pick--
	}
}

// requestMusicTrack sends AO2's MC#<song>#<cid>[#<showname>][#<flags>] (courtroom.cpp:5806-5819);
// area jumps keep the bare two-field RequestMusic because AO2 appends nothing there (:6036-6044).
func (a *App) requestMusicTrack(entry string) {
	if a.sess == nil {
		return
	}
	a.sess.RequestMusicWithFlags(entry, a.effectiveShowname(), a.d.Prefs.Load().MusicFlags())
}

// nextRandomEmote picks a random index different from cur without bias, or returns cur when n <= 1.
func nextRandomEmote(n, cur int) int {
	if n <= 1 {
		return cur
	}
	if cur < 0 || cur >= n { // nothing valid selected: any emote will do
		return rand.IntN(n)
	}
	i := rand.IntN(n - 1)
	if i >= cur {
		i++ // skip the current index — guarantees a different emote, no bias
	}
	return i
}

// randomEmoteForSend rolls a new emote among the visible ones (so favs-only rolls within favourites) and scrolls to it.
func (a *App) randomEmoteForSend() {
	a.refreshEmoteView()
	vis := a.emoteVisible
	if len(vis) <= 1 {
		return
	}
	cur := -1 // current selection's slot in the visible list (-1 if not visible)
	for k, idx := range vis {
		if idx == a.emoteIdx {
			cur = k
			break
		}
	}
	slot := nextRandomEmote(len(vis), cur)
	i := vis[slot]
	if i == a.emoteIdx {
		return
	}
	a.selectEmote(i)
	a.revealEmoteSlot(slot) // minimum scroll, not a page snap (emotescroll.go)
}

const (
	// msgCounterCap is the typical AO server IC length cap; past it the counter turns red.
	msgCounterCap     = 256
	msgCounterPad     = int32(6) // px inset of the count from the field's right edge
	msgCounterChipGap = int32(4) // gap kept between the count and the muted chip when both show
)

// drawMsgCounter draws the IC character count inside the input's right edge, left of the muted chip, if it fits.
func (a *App) drawMsgCounter(input sdl.Rect, on bool) {
	if !on {
		return
	}
	c := a.ctx
	col := ColTextDim
	if a.refreshICCount() > msgCounterCap {
		col = ColDanger
	}
	countW := a.icCountBandW()
	rightEdge := input.X + input.W - msgCounterPad - a.icMutedBandW()
	countX := rightEdge - countW
	if countX < input.X+msgCounterPad { // no room left inside the field: drop it (chip/text win)
		return
	}
	c.Label(countX, input.Y+(input.H-14)/2, a.icCountStr, col)
}

// refreshICCount updates the cached count string on a length change and returns the length;
// icFieldTailReserve measures the same string before the field draws, so the two cannot disagree.
func (a *App) refreshICCount() int {
	n := utf8.RuneCountInString(a.icInput)
	if n != a.icCountN {
		a.icCountN, a.icCountStr = n, strconv.Itoa(n)
	}
	return n
}

// icCountBandW is the count's glyph width plus its inset; width-cached, so a stable count does not allocate.
func (a *App) icCountBandW() int32 { return a.ctx.TextWidth(a.icCountStr) + msgCounterPad }

// icMutedBandW is the width of the input's right edge taken by the muted chip, 0 when not muted.
func (a *App) icMutedBandW() int32 {
	if a.sess == nil || !a.sess.Muted {
		return 0
	}
	return a.mutedChipW() + 2 + msgCounterChipGap
}

// mutedChipW is the muted chip's drawn width.
func (a *App) mutedChipW() int32 { return a.ctx.TextWidth(mutedChipLabel) + mutedChipPadPx }

// mutedChipLabel and mutedChipPadPx are shared by the chip draw, the counter maths and the field tail reservation.
const (
	mutedChipLabel = "🔇 muted"
	mutedChipPadPx = int32(12)
)

// icFieldTailReserve is the width at the IC input's right edge owned by the counter and muted chip, given to
// Ctx.ReserveFieldTail before the field draws; it reuses drawMsgCounter's helpers so the two cannot disagree.
func (a *App) icFieldTailReserve(counterOn bool) int32 {
	band := a.icMutedBandW()
	if counterOn {
		a.refreshICCount() // the cached string must be current BEFORE it is measured
		band += a.icCountBandW()
	}
	if band > 0 {
		band += msgCounterPad // the inset the counter keeps from the field's right edge
	}
	return band
}

// randomShowname sets the showname override to a random saved preset (Ctrl+H).
func (a *App) randomShowname() {
	presets := a.d.Prefs.Load().ShownameList()
	if len(presets) == 0 {
		a.warnLine = "No showname presets saved — add some in Settings → General"
		a.warnAt = a.now()
		return
	}
	a.shownameOverride = presets[rand.IntN(len(presets))]
	a.warnLine = clampLine("Showname → " + a.shownameOverride)
	a.warnAt = a.now()
}

// cycleShowname sets the showname override to the next saved preset, wrapping (Ctrl+B).
func (a *App) cycleShowname() {
	presets := a.d.Prefs.Load().ShownameList()
	if len(presets) == 0 {
		a.warnLine = "No showname presets saved — add some in Settings → General"
		a.warnAt = a.now()
		return
	}
	cur := a.effectiveShowname()
	next := 0
	for i, p := range presets {
		if strings.EqualFold(p, cur) {
			next = (i + 1) % len(presets)
			break
		}
	}
	a.shownameOverride = presets[next]
	a.warnLine = clampLine("Showname → " + a.shownameOverride)
	a.warnAt = a.now()
}

// cycleEmote steps the selection through the visible list by delta with wrap and scrolls it into view.
func (a *App) cycleEmote(delta int) {
	a.refreshEmoteView()
	vis := a.emoteVisible
	n := len(vis)
	if n == 0 {
		return
	}
	// Walk the visible list; cur -1 (nothing selected) is tolerated by the wrap.
	cur := -1
	for k, idx := range vis {
		if idx == a.emoteIdx {
			cur = k
			break
		}
	}
	slot := ((cur+delta)%n + n) % n
	a.selectEmote(vis[slot])
	a.revealEmoteSlot(slot) // minimum scroll, not a page snap (emotescroll.go)
}

// gridRows is how many cellH-tall rows with gap spacing fit in h, at least 1; gap is a parameter so the caller
// takes the pref lock once.
func gridRows(h, cellH, gap int32) int32 {
	if rows := (h + gap) / (cellH + gap); rows > 1 {
		return rows
	}
	return 1
}

// drawEmoteImageButton draws one emotions/button<N> cell with _off art, then character icon fallbacks; reports clicks.
func (a *App) drawEmoteImageButton(btn sdl.Rect, me string, i int, selected bool, label string) bool {
	c := a.ctx
	base := a.urls.EmoteButton(me, i+1, selected)
	cache, gen := &a.emoteBtnOff, &a.emoteBtnOffGen
	if selected {
		cache, gen = &a.emoteBtnOn, &a.emoteBtnOnGen
	}
	page, ok := a.cachedPage(cache, gen, len(a.emotes), i, base)
	if !ok {
		a.demandAsset(&a.emoteAsk, len(a.emotes), i, base, assets.AssetTypeEmoteButton) // AssetType: EmoteButton
		if selected {
			page, ok = a.cachedPage(&a.emoteBtnOff, &a.emoteBtnOffGen, len(a.emotes), i, a.urls.EmoteButton(me, i+1, false))
		}
	}
	if ok && len(page.Frames) > 0 {
		// Fills the cell: AO2 scales button art with Qt::IgnoreAspectRatio (aobutton.cpp AOButton::updateIcon).
		_ = c.Ren.Copy(page.Frames[0], nil, &btn)
	} else {
		// No button art (404 or still streaming): fall back to the character icon, one small image per character.
		iconBase := a.urls.CharIcon(me)
		if ip, iok := a.cachedPage(&a.emoteIconPages, &a.emoteIconGen, 1, 0, iconBase); iok && len(ip.Frames) > 0 {
			_ = c.Ren.Copy(ip.Frames[0], nil, &btn)
		} else {
			pending := a.demandAsset(&a.emoteIconAsk, 1, 0, iconBase, assets.AssetTypeCharIcon) // AssetType: CharIcon
			c.Fill(btn, ColPanel)
			c.Border(btn, ColPanelHi)
			if pending {
				// Keep the demand pump alive only while art can still arrive; flagging every blank cell
				// would hold a character with no art at the demand cadence forever.
				a.frameDemandPending = true
			}
		}
		// Name strip over the fallback icon, opt-in.
		if a.d.Prefs.Load().EmoteCaptions {
			strip := sdl.Rect{X: btn.X, Y: btn.Y + btn.H - 15, W: btn.W, H: 15}
			c.Fill(strip, sdl.Color{R: 0, G: 0, B: 0, A: 150})
			c.LabelClipped(btn.X+3, strip.Y+1, btn.W-6, label, ColText)
		}
	}
	return c.hovering(btn) && c.clicked
}

// sendPairOrder issues /pairorder to reorder the active group roster; op is front, back, up or down.
func (a *App) sendPairOrder(uid int, op string) {
	if a.sess == nil {
		return
	}
	a.sess.SendOOC(a.oocNameOrDefault(), fmt.Sprintf("/pairorder %d %s", uid, op))
}

func (a *App) drawPairPanel(w, h int32, pressed *bool) {
	c := a.ctx
	wasActive := a.pairWin.dragging || a.pairWin.resizing // detect the drag/resize-end frame for slot persistence
	r := a.pairPanelRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	// Non-blocking floating box, so the courtroom behind stays live.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	c.Heading(r.X+pad, r.Y+6, "Pairing", ColText)
	if c.Button(sdl.Rect{X: r.X + r.W - 84 - pad, Y: r.Y + 3, W: 84, H: btnH}, "Close") {
		a.showPair = false
		return
	}
	a.floatWinDrag(&a.pairWin, sdl.Rect{X: r.X, Y: r.Y, W: r.W - 94 - pad, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.pairWin, grip, r, pairPanelMinW, pairPanelMinH, pressed)
	a.drawResizeGrip(grip)
	if wasActive && !a.pairWin.dragging && !a.pairWin.resizing { // drag/resize just ended → remember where
		a.persistPanelSlot(slotPanelPair, r, w, h)
	}

	contentTop := r.Y + floatTitleH + 10

	// Left: searchable partner list.
	listW := r.W/2 - pad*2
	y := contentTop
	c.LabelClipped(r.X+pad, y, listW, "Partner: "+a.pairLabel(), ColAccent)
	y += 24
	a.pairSearch, _ = c.TextField("pairsearch", sdl.Rect{X: r.X + pad, Y: y, W: listW - 80, H: fieldH}, a.pairSearch, "Search...")
	if c.Button(sdl.Rect{X: r.X + pad + listW - 74, Y: y, W: 74, H: btnH}, "Unpair") {
		a.pairWith = protocol.UnpairedCharID
	}
	y += fieldH + 8

	a.ensureCharLower()
	query := a.pairQ.get(a.pairSearch)
	lineH := int32(22)
	listTop := y
	listH := r.Y + r.H - listTop - pad
	matches := int32(0)
	for i := range a.sess.Chars {
		if i != a.sess.MyCharID && (query == "" || strings.Contains(a.charLower[i], query)) {
			matches++
		}
	}
	if c.hovering(sdl.Rect{X: r.X + pad, Y: listTop, W: listW, H: listH}) {
		a.pairScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: r.X + pad + listW - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
	a.pairScroll = c.VScrollbar("pairscroll", track, a.pairScroll, matches*lineH, listH)
	rowY := listTop - a.pairScroll
	for i := range a.sess.Chars {
		if i == a.sess.MyCharID { // can't pair with yourself
			continue
		}
		if query != "" && !strings.Contains(a.charLower[i], query) {
			continue
		}
		if rowY > listTop+listH-lineH {
			break
		}
		if rowY >= listTop-lineH {
			row := sdl.Rect{X: r.X + pad, Y: rowY, W: listW - scrollBarW - scrollBarGap, H: lineH - 2}
			hover := c.hovering(row)
			if i == a.pairWith {
				c.Fill(row, ColAccent)
			} else if hover {
				c.Fill(row, ColPanelHi)
			}
			c.LabelClipped(row.X+4, rowY+3, row.W-8, a.sess.Chars[i].Name, ColText)
			if hover && c.clicked {
				a.pairWith = i
			}
		}
		rowY += lineH
	}

	// Right: placement controls (typed number, -/+ nudge or mouse wheel).
	rx := r.X + r.W/2 + pad
	ry := contentTop
	a.pairOffX = a.offsetControl("pairoffx", rx, ry, "Offset X %", a.pairOffX, &a.pairOffXText)
	ry += 34
	a.pairOffY = a.offsetControl("pairoffy", rx, ry, "Offset Y %", a.pairOffY, &a.pairOffYText)
	ry += 34
	a.pairFlip = c.Checkbox(rx, ry, "Flip my sprite", a.pairFlip)
	ry += 28
	// Wire-aware order control: FantaCode sets a.pairOrder for the ^order suffix; in a JSON group the server owns the
	// roster, so the same segments send /pairorder. Wording follows AO2-Client's ui_pair_order_dropdown.
	gp := a.sess.GroupPair
	inGroup := gp != nil && !gp.Empty()
	myIdx := -1
	if inGroup {
		for i, m := range gp.Members {
			if m.CharID == a.sess.MyCharID {
				myIdx = i
				break
			}
		}
	}
	c.Label(rx, ry+4, "Order:", ColText)
	const (
		pairOrderLabelW = 44 // "Order:" label lead before the segments
		pairOrderSegW   = 72 // one order segment ("To behind" fits in the chrome font)
		pairOrderSegGap = 4  // gap between the two segments
	)
	// Label, two segments and gap span 192 px, inside the right column even at pairPanelMinW.
	segX := rx + pairOrderLabelW
	orderSeg := func(label string, active bool) bool {
		r := sdl.Rect{X: segX, Y: ry, W: pairOrderSegW, H: btnH}
		segX += pairOrderSegW + pairOrderSegGap
		if active {
			// Accent on every state so it reads as selected, not hoverable; re-selecting is harmless.
			return c.ButtonCol(r, label, ColAccent, ColAccent, ColAccent, ColBackground)
		}
		return c.Button(r, label)
	}
	orderTip := "Pair order rides the classic ^0/^1 suffix (FantaCode): ^0 = you in front, ^1 = you behind. FantaCode can only carry one partner."
	if inGroup {
		orderTip = "Group order (JSON additional_chars): reorders the whole group roster — any member to any position, not just front/behind."
	}
	c.Tooltip(sdl.Rect{X: rx, Y: ry, W: pairOrderLabelW + 2*pairOrderSegW + pairOrderSegGap, H: btnH}, orderTip)

	frontActive := a.pairOrder == protocol.PairSpeakerInFront
	if inGroup {
		frontActive = myIdx == 0
	}
	if orderSeg("To front", frontActive) {
		if inGroup {
			a.sendPairOrder(a.sess.PlayerID, "front")
		} else {
			a.pairOrder = protocol.PairSpeakerInFront
		}
	}
	if orderSeg("To behind", !frontActive) {
		if inGroup {
			a.sendPairOrder(a.sess.PlayerID, "back")
		} else {
			a.pairOrder = protocol.PairSpeakerBehind
		}
	}
	ry += 36

	if inGroup {
		// Group roster, front to back; each row moves that member one step with /pairorder.
		c.Label(rx, ry, "Group (front→back):", ColText)
		ry += 20
		for _, m := range gp.Members {
			upR := sdl.Rect{X: rx, Y: ry, W: 22, H: btnH}
			dnR := sdl.Rect{X: rx + 26, Y: ry, W: 22, H: btnH}
			name := m.Name
			if name == "" {
				name = fmt.Sprintf("char %d", m.CharID)
			}
			if m.CharID == a.sess.MyCharID {
				name += " (you)"
			}
			c.Tooltip(upR, "Move this member toward the front")
			if c.Button(upR, "▲") {
				a.sendPairOrder(m.UID, "up")
			}
			c.Tooltip(dnR, "Move this member toward the back")
			if c.Button(dnR, "▼") {
				a.sendPairOrder(m.UID, "down")
			}
			c.LabelClipped(rx+54, ry+4, r.W/2-2*pad-54, name, ColText)
			ry += 24
		}
		ry += 4
	} else {
		c.Label(rx, ry, "Both sides must pair with each other;", ColTextDim)
		c.Label(rx, ry+18, "applies from your next message.", ColTextDim)
		ry += 42
	}

	// Offset editor: drag your sprite; the partner is a translucent ghost.
	// grip keeps a press on the panel's resize corner from starting a sprite drag.
	pv := sdl.Rect{X: rx, Y: ry, W: r.W/2 - 2*pad, H: r.Y + r.H - pad - ry}
	if pv.H >= ghostMinHeightPx {
		a.drawPairGhost(pv, grip)
	}
}

// pairPanelRect is the Pair menu's floating-window rect, with a default height that follows the window.
func (a *App) pairPanelRect(w, h int32) sdl.Rect {
	defH := value.Clamp(h-120, pairPanelMinH, 560)
	if r, ok := a.seedPanelFromSlot(&a.pairWin, slotPanelPair, pairPanelDefW, defH, pairPanelMinW, pairPanelMinH, w, h); ok {
		return r
	}
	return a.pairWin.rect(pairPanelDefW, defH, pairPanelMinW, pairPanelMinH, w, h)
}

const (
	pairPanelDefW = 580 // default width
	pairPanelMinW = 440 // floor: the two columns need room
	pairPanelMinH = 320 // floor: list + controls
)

// ghostMinHeightPx is the smallest useful ghost stage; below it the panel shows only the numeric controls.
const ghostMinHeightPx = 70

// ghostAlpha is the partner ghost's translucency.
const ghostAlpha = 110

// drawPairGhost renders the live offset editor: the partner translucent at their offsets, your idle sprite at yours.
// grip is the panel's resize corner, which overlaps the stage; a press there must not also start a drag.
func (a *App) drawPairGhost(pv, grip sdl.Rect) {
	c := a.ctx
	// Clip the stage: a sprite is fitted to stage height with its aspect ratio (animationlayer.cpp:229-260), so it can
	// be wider than this column; AO2's child widgets clip, ours must. pushClip so hovering() honours the same rect.
	prevClip, hadClip := c.pushClip(pv)
	defer c.popClip(prevClip, hadClip)
	// Real background behind the ghosts, from your own position; a flat fill until it streams in.
	bgPart, _ := courtroom.PositionScene(a.mySide())
	base := a.urls.Background(a.sess.Background, bgPart)
	if base != a.pairBgKey {
		a.pairBgPages, a.pairBgKey = nil, base
	}
	if page, ok := a.cachedPage(&a.pairBgPages, &a.pairBgGen, 1, 0, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &pv)
	} else {
		c.Fill(pv, sdl.Color{R: 12, G: 12, B: 16, A: 255})
		if a.sess.Background != "" {
			a.d.Manager.Prefetch(base, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background (pair preview)
		}
	}
	c.Border(pv, ColPanelHi)

	// Partner ghost first (behind), then me.
	if a.pairWith >= 0 && a.pairWith < len(a.sess.Chars) {
		name := a.sess.Chars[a.pairWith].Name
		// The scene's pair layer has their real offsets and idle sprite once a paired message arrived; before that use
		// their char.ini's first [Emotions] anim (AO2's emote 1), already fetched for blips, so no extra probe.
		gx, gy := 0, 0
		anim := a.iniIdleAnimFor(name, ghostFallbackEmote)
		base := a.urls.Emote(name, anim, courtroom.EmoteIdle)
		alts := a.urls.EmoteAlts(name, anim, courtroom.EmoteIdle)
		if sc := &a.room.Scene; sc.PairActive && strings.EqualFold(sc.Pair.Name, name) {
			gx, gy = sc.Pair.OffsetX, sc.Pair.OffsetY
			if sc.Pair.IdleBase != "" {
				base, alts = sc.Pair.IdleBase, nil // the exact sprite on stage — already resolved
			}
		}
		a.drawGhostSprite(pv, name, base, alts, gx, gy, false, ghostAlpha)
	}
	if me := a.activeCharName(); me != "" { // iniswap-aware: preview the folder that actually renders
		// The selected emote's idle is what the next message shows; many packs have no normal.* sprite.
		anim := ghostFallbackEmote
		if a.emoteIdx >= 0 && a.emoteIdx < len(a.emotes) {
			anim = a.emotes[a.emoteIdx].Anim
		}
		base := a.urls.Emote(me, anim, courtroom.EmoteIdle)
		alts := a.urls.EmoteAlts(me, anim, courtroom.EmoteIdle)
		a.drawGhostSprite(pv, me, base, alts, a.pairOffX, a.pairOffY, a.pairFlip, 255)
	}
	c.Label(pv.X+4, pv.Y+2, "drag your sprite to place — arrow keys nudge 1%", ColTextDim)

	// Drag anywhere in the stage moves your sprite, the only thing being edited.
	pressed := c.mouseDown && !a.ghostPrev
	a.ghostPrev = c.mouseDown
	// Except on the resize corner: floatWinResize already consumed the chrome's pressed flag, so exclude it by rect.
	if pressed && c.hovering(pv) && !pointIn(c.mouseX, c.mouseY, grip) {
		a.ghostDrag = true
		a.ghostStart = [2]int32{c.mouseX, c.mouseY}
		a.ghostBase = [2]int{a.pairOffX, a.pairOffY}
	}
	if a.pairWin.resizing { // a resize that started outside pv must not capture it mid-gesture either
		a.ghostDrag = false
	}
	if !c.mouseDown {
		a.ghostDrag = false
	}
	if a.ghostDrag && pv.W > 0 && pv.H > 0 {
		a.pairOffX = clampOffset(a.ghostBase[0] + int(c.mouseX-a.ghostStart[0])*100/int(pv.W))
		a.pairOffY = clampOffset(a.ghostBase[1] + int(c.mouseY-a.ghostStart[1])*100/int(pv.H))
		a.refreshPairOffsetBufs() // a focused numeric row must follow the drag live
	}
	// Arrow keys nudge 1%, only with no text field focused (they would move the caret); up lowers Y, matching the drag.
	if c.focusID == "" {
		switch c.keyPressed {
		case sdl.K_LEFT:
			a.pairOffX = clampOffset(a.pairOffX - 1)
		case sdl.K_RIGHT:
			a.pairOffX = clampOffset(a.pairOffX + 1)
		case sdl.K_UP:
			a.pairOffY = clampOffset(a.pairOffY - 1)
		case sdl.K_DOWN:
			a.pairOffY = clampOffset(a.pairOffY + 1)
		}
	}
}

// ghostFallbackEmote is AO's conventional default idle, guessed while a character's real sprite is unknown.
const ghostFallbackEmote = "normal"

// drawGhostSprite draws a sprite at offset% of the stage at full stage height; the alpha-mod is restored
// because pages are shared with the live viewport.
func (a *App) drawGhostSprite(pv sdl.Rect, name, base string, alts []string, offX, offY int, flip bool, alpha uint8) {
	c := a.ctx
	page, ok := a.d.Store.Get(base)
	if !ok || len(page.Frames) == 0 || page.H == 0 {
		// Warm once per (character, base); a changed base re-warms with the table full, only new characters are capped.
		if a.ghostWarm[name] != base {
			if a.ghostWarm == nil {
				a.ghostWarm = map[string]string{}
			}
			_, known := a.ghostWarm[name]
			if known || len(a.ghostWarm) < ghostWarmCap {
				a.ghostWarm[name] = base
				a.d.Manager.PrefetchChain(base, alts, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (ghost editor)
			}
		}
		c.Label(pv.X+pv.W/2-c.TextWidth(name)/2, pv.Y+pv.H/2, name, ColTextDim)
		return
	}
	dst := ghostSpriteRect(pv, page.W, page.H, offX, offY)
	if len(page.Frames) > 1 {
		a.NoteAnimating() // the editor ghost loops: keep frames coming through the static skip
	}
	tex := page.Frames[pageFrameLoop(page, a.themeElapsed())]
	_ = tex.SetAlphaMod(alpha)
	if flip {
		_ = c.Ren.CopyEx(tex, nil, &dst, 0, nil, sdl.FLIP_HORIZONTAL)
	} else {
		_ = c.Ren.Copy(tex, nil, &dst)
	}
	_ = tex.SetAlphaMod(255)
}

// ghostSpriteRect scales the sprite to stage height, centres it and slides it by the offsets; the caller clips.
// Not shared with render.splashFitRect: in AO2 only the character has an offset (courtroom.cpp on_pair_offset_changed).
func ghostSpriteRect(pv sdl.Rect, srcW, srcH int32, offX, offY int) sdl.Rect {
	if srcH <= 0 {
		return sdl.Rect{X: pv.X, Y: pv.Y}
	}
	dst := sdl.Rect{H: pv.H, W: pv.H * srcW / srcH}
	dst.X = pv.X + (pv.W-dst.W)/2 + int32(offX)*pv.W/100
	dst.Y = pv.Y + int32(offY)*pv.H/100
	return dst
}

// ghostWarmCap bounds the ghost editor's prefetch dedupe table.
const ghostWarmCap = 16

const offsetStep = 5

// offsetControl draws one pair-offset row: a typed field that commits only on a valid parse, -/+ buttons and wheel.
func (a *App) offsetControl(id string, x, y int32, label string, val int, buf *string) int {
	c := a.ctx
	c.Label(x, y+4, label, ColText)
	const fieldW = 56
	fx := x + 86
	if c.focusID != id {
		*buf = strconv.Itoa(val) // not editing: mirror the canonical value
	}
	next, _ := c.TextField(id, sdl.Rect{X: fx, Y: y, W: fieldW, H: fieldH}, *buf, "0")
	if next != *buf {
		*buf = next
		if n, err := strconv.Atoi(strings.TrimSpace(next)); err == nil {
			val = clampOffset(n)
		}
	}
	typed := val // the value as of the typed commit — any change below is a mouse edit
	bx := fx + fieldW + 6
	if c.Button(sdl.Rect{X: bx, Y: y, W: 24, H: 24}, "-") {
		val = clampOffset(val - offsetStep)
	}
	if c.Button(sdl.Rect{X: bx + 28, Y: y, W: 24, H: 24}, "+") {
		val = clampOffset(val + offsetStep)
	}
	row := sdl.Rect{X: x, Y: y, W: bx + 52 - x, H: 26}
	if c.hovering(row) && c.wheelY != 0 {
		val = clampOffset(val + int(c.wheelY)*offsetStep)
	}
	// While focused the field shows the edit buffer, so a mouse edit must refresh it; typing keeps its partial input.
	if val != typed && c.focusID == id {
		*buf = strconv.Itoa(val) // textField clamps the caret to the new length
	}
	return val
}

func clampOffset(v int) int {
	if v < -100 {
		return -100
	}
	if v > 100 {
		return 100
	}
	return v
}

// refreshPairOffsetBufs re-syncs a focused offset field's buffer after the value changed by stage drag or arrow nudge.
func (a *App) refreshPairOffsetBufs() {
	switch a.ctx.focusID {
	case "pairoffx":
		a.pairOffXText = strconv.Itoa(a.pairOffX)
	case "pairoffy":
		a.pairOffYText = strconv.Itoa(a.pairOffY)
	}
}

func (a *App) pairLabel() string {
	if a.pairWith <= protocol.UnpairedCharID || a.sess == nil || a.pairWith >= len(a.sess.Chars) {
		return "(unpaired)"
	}
	return fmt.Sprintf("%d — %s", a.pairWith, a.sess.Chars[a.pairWith].Name)
}

// funColor applies the outgoing colour modes, rainbow first: \cr markup, a random palette index, or customRGB >= 0 as
// \c#RRGGBB markup with the nearest standard index on the wire; blank and space sends are left alone.
func funColor(text string, color, ext, customRGB int, rainbow, random bool, randIdx int) (string, int) {
	if text == "" || text == " " {
		return text, color
	}
	switch {
	case rainbow:
		return "\\cr" + text, color
	case random:
		return text, randIdx
	case customRGB >= 0:
		return fmt.Sprintf("\\c#%06x%s", customRGB&0xFFFFFF, text),
			render.NearestTextColorIndex(uint8(customRGB>>16), uint8(customRGB>>8), uint8(customRGB))
	case ext >= 0 && ext < render.ExtColorCount():
		// Exact colour rides as inline markup; the wire text_color is the nearest standard index for stock AO2 clients.
		e := render.ExtColorAt(ext)
		return "\\c" + string(e.Code) + text, e.Wire
	}
	return text, color
}

// midEmoteResolver resolves a :stem: shortcode against the emote list (Anim or Comment, case-insensitive);
// the wire's silence sentinels "", "0" and "1" read as no sound.
func (a *App) midEmoteResolver(stem string) (courtroom.MidEmote, bool) {
	for i := range a.emotes {
		e := &a.emotes[i]
		if strings.EqualFold(e.Anim, stem) || strings.EqualFold(e.Comment, stem) {
			sfx := e.SFXName
			if sfx == "" || sfx == "0" || sfx == "1" {
				sfx = ""
			}
			return courtroom.MidEmote{Pre: e.Preanim, Emote: e.Anim, SFX: sfx}, true
		}
	}
	return courtroom.MidEmote{}, false
}

// sendIC builds and sends the MS; the interjection is armed state (a.icShout, AO2's objection_state) read here, as in
// on_chat_return_pressed (courtroom.cpp:2136-2147), so a shout button is not a send button.
func (a *App) sendIC() {
	shout := a.icShout
	text := strings.TrimSpace(a.icInput)
	if cmdHandled := a.handleChatCommand(text); cmdHandled {
		a.icInput = "" // commands clear instantly — the field's undo history catches it (Ctrl+Z)
		return
	}
	// Typed :emote: shortcodes become the internal <e:...> marker, readable to non-AsyncAO clients.
	text = courtroom.ExpandMidTextShortcodes(text, a.midEmoteResolver)
	// Blankpost: empty input sends AO's single-space message, since servers reject truly empty ones.
	if text == "" && shout == 0 {
		text = " "
	}
	if a.sess.MyCharID < 0 {
		return
	}
	// Muted (AO2 set_mute; on_chat_return_pressed returns early): refuse but keep the line, as no echo will clear it.
	if a.sess.Muted {
		a.pushOOC("[SERVER] You're muted — that message wasn't sent.", "")
		return
	}
	// AO2-Client chat_ratelimit parity: drop sends inside the window.
	if _, _, rateMs := a.d.Prefs.Load().Timing(); rateMs > 0 &&
		time.Since(a.lastICSend) < time.Duration(rateMs)*time.Millisecond {
		return
	}
	// Auto-random emote; placed after the command, blankpost and rate-limit guards so a dropped send does not roll.
	if a.d.Prefs.Load().RandomEmote {
		a.randomEmoteForSend()
	}
	emote := courtroom.Emote{Anim: "normal", Preanim: "-", DeskMod: protocol.DeskShow}
	if a.emoteIdx >= 0 && a.emoteIdx < len(a.emotes) {
		emote = a.emotes[a.emoteIdx]
	}
	hasPre := emoteHasPreanim(&emote)
	// The emote's sound accompanies the preanim only, so a line with Pre unticked carries the silence sentinel.
	sfxName := outgoingSFXName(&emote, a.icPreanim, a.sfxChoiceIdx, a.sfxChoices)
	// AO2's sfx_on_idle rewrite (courtroom.cpp:2102-2116): a picked sound on an idle or zoom line promotes the mod,
	// blanks the pre name and zeroes the delay so receivers play it with no animation.
	emoteMod := protocol.NormalizeOutgoingEmoteMod(emote.Mod, hasPre, a.icPreanim, false, a.sess.Features)
	preEmote, sfxDelay := emote.Preanim, emote.SFXDelay
	if m, promoted := sfxIdlePromotion(emoteMod, a.sfxChoiceIdx, a.d.Prefs.Load().SFXOnIdle, a.sess.Features.Has(protocol.FeaturePrezoom)); promoted {
		emoteMod, preEmote, sfxDelay = m, "", 0
	}
	// Per-emote blip override (2.9.1 custom_blips), else the character's.
	blip := emote.Blip
	if blip == "" {
		blip = a.charBlips
	}
	// Sticky FX wraps the message, then ParseTextEffects pulls the markup into display-indexed spans in an invisible
	// frame, so AO2/webAO show plain text. A markup-only message falls back to a blankpost.
	text = a.applyStickyEffect(text)
	text, effectSpans := courtroom.ParseTextEffects(text)
	if strings.TrimSpace(text) == "" && shout == 0 {
		text, effectSpans = " ", nil
	}
	// A typed trigger word flips status on the raw text, before markers, so the change rides this message.
	a.applyAutoStatus(text)
	customRGB := -1
	if a.icCustomOn {
		customRGB = a.icCustomRGB
	}
	text, msgColor := funColor(text, a.icColor, a.icExtColor-1, customRGB, a.d.Prefs.Load().RainbowMessages, a.d.Prefs.Load().RandomMessageColor, rand.IntN(render.TextColorCount))
	// Sprite style: invisible zero-width marker at the end, so a server length limit only drops the style. On change:
	// other clients typewriter the invisible run and blip per character, so a marker on every message is audible.
	curStyle := a.mySpriteStyle()
	if marker := curStyle.EncodeChangeMarker(a.lastSentStyle); marker != "" {
		text += marker
	}
	a.lastSentStyle = curStyle
	// Character profile: same channel, on change, after the style marker because older AsyncAO clients read only the
	// first zero-width run.
	curProfile := a.myWireProfile()
	if pm := curProfile.EncodeChangeMarker(a.lastSentProfile); pm != "" {
		text += pm
	}
	a.lastSentProfile = curProfile
	// Presence status: same invisible channel, sent on change.
	if sm := courtroom.EncodeStatusChangeMarker(a.myStatus, a.lastSentStatus); sm != "" {
		text += sm
	}
	a.lastSentStatus = a.myStatus
	// The additive prepend below shifts visible runes right by one but spans were indexed without it, so shift before
	// EncodeEffectsMarker bakes them. Same predicate as the prepend; the first visible rune is unchanged until there.
	if a.icAdditive && courtroom.AdditiveWantsLeadingSpace(text) {
		for i := range effectSpans {
			effectSpans[i].Start++
		}
	}
	// Effect spans: same channel, appended last; unlike the markers above they are per-message content.
	if marker := courtroom.EncodeEffectsMarker(effectSpans); marker != "" {
		text += marker
	}
	// A queued reaction rides this message as an invisible frame, then clears; your own echo shows it, no local echo.
	if a.pendingReactSet {
		if marker := a.pendingReact.EncodeMarker(); marker != "" {
			text += marker
		}
		a.pendingReactSet = false
	}
	// AO2 additive parity (courtroom.cpp:2320-2326, 6563-6573): prepend the separating space at packet build, not the
	// field, so a.icInput stays clean. Applied last: markers ride the tail, and the helper sees past funColor markup.
	if a.icAdditive {
		text = courtroom.AdditiveOutgoingLeadingSpace(text)
	}
	out := protocol.OutgoingMS{
		DeskMod:    emote.DeskMod,      // the emote's char.ini desk_mod (was hardcoded 1, so no-desk emotes never hid the desk)
		PreEmote:   preEmote,           // blanked by the sfx_on_idle promotion above, else the emote's own
		CharName:   a.activeCharName(), // iniswap: the wire carries the custom folder
		Emote:      emote.Anim,
		Message:    text,
		SFXName:    sfxName,
		SFXDelay:   sfxDelay, // zeroed by the sfx_on_idle promotion above, else the emote's own
		LoopingSFX: emote.SFXLoop,
		// char.ini [<emote>_Frame*] in AO2's wire format; an IC-bar SFX pick leaves the per-frame map alone, like AO2.
		FrameShake:   emote.FrameShake,
		FrameRealize: emote.FrameRealize,
		FrameSFX:     emote.FrameSFX,
		Blipname:     blip,
		Side:         a.mySide(),
		// Never ship raw char.ini emote mods: legacy 2/3/4 values make schema-strict clients drop the message.
		// Pre unticked forces idle, and the echo re-runs this through scenemaker, so local staging matches.
		EmoteMod:    emoteMod, // normalized + possibly sfx_on_idle-promoted above
		CharID:      a.sess.MyCharID,
		Objection:   shout,
		TextColor:   msgColor, // swatch cycler, or M61 random-per-message colour
		Showname:    a.effectiveShowname(),
		PairWith:    a.pairWith,
		PairOrder:   a.pairOrder,
		OffsetX:     a.pairOffX,
		OffsetY:     a.pairOffY,
		Flip:        a.pairFlip,
		Immediate:   outgoingImmediate(a.icImmediate, a.icPreanim), // non-interrupting preanim — only ships when Pre (a preanimation) is also on; AO2's pre_no_interrupt rides ui_pre
		Additive:    a.icAdditive,                                  // #14 2.8: this message appends to your last (gated to the additive server + pref in the IC row)
		Slide:       a.icSlide,                                     // #21: slide into position instead of cutting (AO2 ui_slide)
		Realization: a.icRealize,                                   // #21: white flash + sound on this message (AO2 realization_state)
		Screenshake: a.icShake,                                     // #21: shake the courtroom on this message (AO2 screenshake_state)
		KFOCompat:   a.sess.KFOCompat(),                            // KFO-Server only: fill empty frame/effect fields (its MS validator rejects them)
		// AO2 ui_effects_dropdown: "<name>|<folder>|<sound>", "" for None (courtroom.cpp:2296-2308).
		Effects: a.outgoingEffectsField(),
	}
	// Named custom interjection (2.10): the wire carries "4&<stem>"
	// (formatObjection assembles it; courtroom.cpp:2142).
	if shout == protocol.ShoutCustom && a.customIdx >= 0 && a.customIdx < len(a.customShouts) {
		out.CustomShout = a.customShouts[a.customIdx].File
	}
	// Armed evidence rides this message; the wire shifts ids by 1 because
	// 0 means "no evidence" (legacy standard, courtroom.cpp:2160).
	if a.evidPresent && a.evidIdx >= 0 && a.evidIdx < len(a.sess.Evidence) {
		out.EvidenceID = a.evidIdx + 1
	}
	a.sess.SendChat(out)
	a.lastICSend = time.Now()
	a.recordSentIC(a.icInput) // #8: remember the raw typed line for Up-arrow recall
	// Keep-until-echo (AO2 handle_chatmessage, courtroom.cpp): tsuserver-family servers silently swallow an MS inside
	// another's delay window (area.set_next_msg_delay), so noteOwnICEcho clears the input; evidPresent stays armed too.
	a.icPendingSent = a.icInput
	// One-shot arms clear optimistically where AO2 does (courtroom.cpp:2327-2329; courtroom.cpp:2362 for the slide):
	// a swallowed send costs one flash, a stuck arm fires on every later line.
	a.icSlide, a.icRealize, a.icShake = false, false, false
	a.icShout = 0 // objection_state, courtroom.cpp:2327 — the line it literally sits on
	// Sticky picks reset where AO2's reset_ui does (courtroom.cpp:2340-2359).
	a.clearEffectsAfterSend()
	a.clearSFXAfterSend()
	a.clearPreAfterSend()
}

// clearSFXAfterSend is courtroom.cpp:2340-2345: with Sticky Sounds off, reset the SFX dropdown, but only if the sound
// could have been heard (sfx_on_idle or Pre), so a pick made on an idle line survives until Pre is ticked.
func (a *App) clearSFXAfterSend() {
	if a.d.Prefs.Load().StickySounds {
		return
	}
	if !a.d.Prefs.Load().SFXOnIdle && !a.icPreanim {
		return
	}
	a.sfxChoiceIdx = 0 // row 0 is the "auto" / Default row (sfxAutoLabel)
}

// clearPreAfterSend is courtroom.cpp:2356-2359: with Sticky Preanims off, Pre unticks after every send.
func (a *App) clearPreAfterSend() {
	if a.d.Prefs.Load().StickyPreanims {
		return
	}
	a.icPreanim = false
}

// noteOwnICEcho handles the server's echo of our own IC message (AO2 handle_chatmessage): consumes the evidence state
// and strips the sent line from the start of the box, keeping anything typed during the round trip.
func (s *sessionState) noteOwnICEcho() {
	// An empty icPendingSent or a draft that no longer opens with the sent text leaves the box untouched;
	// the field's undo history records the clear at its next draw, so Ctrl+Z restores it.
	s.icInput = strings.TrimPrefix(s.icInput, s.icPendingSent)
	s.icPendingSent = ""
	s.evidPresent = false // presenting is one-shot: consumed by the message that displayed
}

// sentHistCap bounds a per-tab message recall ring.
const sentHistCap = 30

// recordSentLine pushes a sent line onto a recall ring, skipping blankposts and consecutive duplicates; resets *idx.
func recordSentLine(raw string, hist *[]string, idx *int) {
	*idx = -1 // any send returns you to a fresh draft
	t := strings.TrimSpace(raw)
	if t == "" { // blankpost / whitespace-only — nothing to recall
		return
	}
	if n := len(*hist); n > 0 && (*hist)[n-1] == t {
		return // don't stack the same line twice in a row
	}
	*hist = append(*hist, t)
	if len(*hist) > sentHistCap {
		*hist = (*hist)[len(*hist)-sentHistCap:]
	}
}

// recallLine walks a recall ring on Up/Down when focused, stashing the live draft;
// consuming the key stops fields that share an input acting twice.
func recallLine(c *Ctx, focused bool, input *string, hist []string, idx *int, draft *string) {
	if !focused || len(hist) == 0 {
		return
	}
	switch c.keyPressed {
	case sdl.K_UP:
		if *idx == -1 { // entering history: stash the live draft first
			*draft = *input
			*idx = len(hist)
		}
		if *idx > 0 {
			*idx--
			*input = hist[*idx]
		}
		c.keyPressed = 0
	case sdl.K_DOWN:
		if *idx >= 0 {
			*idx++
			if *idx >= len(hist) {
				*input, *idx = *draft, -1 // back to the draft
			} else {
				*input = hist[*idx]
			}
		}
		c.keyPressed = 0
	}
}

// recordSentIC records a line in the IC recall ring.
func (a *App) recordSentIC(raw string) { recordSentLine(raw, &a.icSentHist, &a.icRecallIdx) }

func (a *App) recallIC() {
	recallLine(a.ctx, a.ctx.focusID == "ic", &a.icInput, a.icSentHist, &a.icRecallIdx, &a.icRecallDraft)
}

// recordSentOOC records a line in the OOC recall ring, shared by every OOC input through oocInput.
func (a *App) recordSentOOC(raw string) { recordSentLine(raw, &a.oocSentHist, &a.oocRecallIdx) }

func (a *App) recallOOC() {
	f := a.ctx.focusID
	recallLine(a.ctx, f == "ooc" || f == "oocmsg", &a.oocInput, a.oocSentHist, &a.oocRecallIdx, &a.oocRecallDraft)
}

// Area card fills: locked dark red with a brighter outline, current area a dim accent.
var (
	areaLockedBg     = sdl.Color{R: 96, G: 32, B: 32, A: 255}
	areaLockedBorder = sdl.Color{R: 170, G: 64, B: 64, A: 255}
	areaCurrentBg    = sdl.Color{R: 40, G: 56, B: 96, A: 255}
)

// areaCardGap is the vertical space between area cards so each reads as a separate button.
const areaCardGap = 5

// areaStatusColor keys the area row color to the tsuserver status set.
func areaStatusColor(status string) sdl.Color {
	switch strings.ToUpper(status) {
	case "LOOKING-FOR-PLAYERS", "LFP":
		return sdl.Color{R: 110, G: 230, B: 110, A: 255} // recruiting: green
	case "CASING", "RP", "GAMING":
		return sdl.Color{R: 250, G: 190, B: 80, A: 255} // busy: amber
	case "RECESS":
		return sdl.Color{R: 120, G: 180, B: 255, A: 255} // paused: blue
	case "IDLE":
		return ColTextDim
	default:
		return ColText
	}
}

// defaultSide is the side AO assumes when nothing has set one — the same
// fallback AO2-Client's current_or_default_side() ends on.
const defaultSide = "wit"

// mySide is our position (char.ini side or /pos), never the last speaker's: strict receivers such as LemmyAO
// reject custom side strings.
func (a *App) mySide() string {
	if a.sidePref != "" {
		return a.sidePref
	}
	return defaultSide
}

// handleChatCommand implements /pair <id>, /unpair, /offset <x> [y] and /pos <side>.
func (a *App) handleChatCommand(text string) bool {
	switch {
	case strings.HasPrefix(text, "/pos "):
		// applySide lowercases, trims and forwards /pos to the server.
		a.applySide(strings.TrimPrefix(text, "/pos "))
		return true
	case strings.HasPrefix(text, "/pair "):
		if id, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(text, "/pair "))); err == nil {
			a.pairWith = id
		}
		return true
	case text == "/unpair":
		a.pairWith = protocol.UnpairedCharID
		return true
	case strings.HasPrefix(text, "/offset "):
		parts := strings.Fields(strings.TrimPrefix(text, "/offset "))
		if len(parts) >= 1 {
			if x, err := strconv.Atoi(parts[0]); err == nil {
				a.pairOffX = clampOffset(x)
			}
		}
		if len(parts) >= 2 {
			if y, err := strconv.Atoi(parts[1]); err == nil {
				a.pairOffY = clampOffset(y)
			}
		}
		a.refreshPairOffsetBufs() // pair panel rows mirror the command
		return true
	}
	return false
}

// renderRaster rasterizes the current message; comicInk forces the default colour dark for the comic's white bubble.
func renderRaster(a *App, sc *courtroom.Scene, wrapW int32, skinned bool, pct int, comicInk bool) (*render.MessageRaster, error) {
	col := chatBaseColor(a, sc, skinned, comicInk)
	// The theme's "message" family when declared, else the first covering font; the export passes a frame-fitted size.
	logFont := a.messageFontFor(pct, sc.MessageText)
	// Device-scaled face keeps the live chatbox crisp; exports bracket textDevPct to 100, so UI scale cannot leak in.
	dev := a.ctx.textDevPct
	font := a.ctx.deviceFontFor(logFont, pct)
	// AO2's row advance for the theme's message face in device pixels; 0 with no family or no OS/2 value in the face.
	pitch := a.themeRowPitchDevice(elemMessage, pct)
	// Per-glyph fallback for emoji or mixed scripts no single face covers; plain messages stay on the fast paths below.
	needEmoji := render.NeedsEmojiFallback(sc.MessageText)
	if needEmoji || !a.ctx.coversFace(logFont, sc.MessageText) {
		if needEmoji {
			a.ensureEmojiFontLoad() // kick off the one off-thread system-emoji read
		}
		var spans []render.ColorSpan
		if sceneNeedsStyled(sc.MessageStyles) {
			spans = buildColorSpans(sc.MessageStyles, col)
		} else {
			spans = []render.ColorSpan{{Len: len([]rune(sc.MessageText)), Color: col}}
		}
		textFonts := a.ctx.deviceCoverRunes(logFont, pct, []rune(sc.MessageText))
		m, err := render.RasterizeFallback(a.ctx.Ren, textFonts, a.ctx.emojiDeviceFont(pct), sc.MessageText, spans, wrapW, dev)
		return centerRaster(m, err, sc, wrapW, pitch)
	}
	// Inline \cN colours take the span raster; plain messages the single-colour path.
	if sceneNeedsStyled(sc.MessageStyles) {
		m, err := render.RasterizeStyled(a.ctx.Ren, font, sc.MessageText, buildColorSpans(sc.MessageStyles, col), wrapW, dev)
		return centerRaster(m, err, sc, wrapW, pitch)
	}
	m, err := render.Rasterize(a.ctx.Ren, font, sc.MessageText, wrapW, col, dev)
	return centerRaster(m, err, sc, wrapW, pitch)
}

// centerRaster applies webAO "~~" centring (Scene.Centered) and the theme's row pitch to a freshly built raster.
func centerRaster(m *render.MessageRaster, err error, sc *courtroom.Scene, wrapW int32, rowPitch int32) (*render.MessageRaster, error) {
	if err != nil || m == nil {
		return m, err
	}
	if sc.Centered {
		m.Center(wrapW)
	}
	// Applied here, not at the three build sites, so they cannot disagree on row spacing; a no-op without a row pitch.
	m.UseRowPitch(rowPitch)
	return m, err
}

// chatBaseColor resolves a message's default colour: the theme's "message" colour replaces only code 0, and only while
// the theme's own skin is drawn (its colours assume that skin); comicInk forces dark ink.
func chatBaseColor(a *App, sc *courtroom.Scene, skinned, comicInk bool) sdl.Color {
	col := render.TextColor(sc.TextColor)
	switch {
	case sc.TextColor != 0:
		// an explicit message colour always wins (reads on either background)
	case comicInk:
		col = comicBubbleInk
	case skinned && sc.ChatSkinMsgHas:
		// The speaker's own chatbox ink wins: AO2's get_design_element resolves the chatbox folder first (set_font).
		col = sdl.Color{R: sc.ChatSkinMsgColor.R, G: sc.ChatSkinMsgColor.G, B: sc.ChatSkinMsgColor.B, A: 255}
	case skinned && a.themeHasMsg:
		col = a.themeMsgCol
	}
	return col
}

// glyphCacheCap bounds the shared white-glyph cache, enough for a 256-char message's glyphs across a couple of sizes.
const glyphCacheCap = 512

// renderAnimated builds the per-glyph layout for an effects message and returns it with the glyph cache's face.
func renderAnimated(a *App, sc *courtroom.Scene, wrapW int32, skinned bool, pct int) (*render.AnimatedText, *ttf.Font) {
	col := chatBaseColor(a, sc, skinned, false)
	// Logical face: pen positions and effect offsets are not scale-aware, so this path draws 1:1, soft above 100%.
	font := a.messageFontFor(pct, sc.MessageText) // #39: the theme's "message" family, if it names one
	// Resolves each rune's face as a plain message does (CJK, emoji); built once per message so Draw stays zero-alloc.
	resolve := a.animFontResolver(font, pct, sc.MessageText)
	at := render.RasterizeAnimated(font, sc.MessageText, toRenderEffectSpans(sc.MessageEffects), animColors(sc, col), wrapW, resolve, a.ctx.fontChainGen)
	// Same AO2 row advance as the plain raster, in the logical pixels this path lays out in.
	at.UseRowPitch(a.themeRowPitchLogical(elemMessage, pct))
	return at, font
}

// animColors flattens inline-colour runs into a per-rune slice; an unstyled message returns one element that
// RasterizeAnimated clamps to. Bold and italic do not compose with effects.
func animColors(sc *courtroom.Scene, base sdl.Color) []sdl.Color {
	if !sceneNeedsStyled(sc.MessageStyles) {
		return []sdl.Color{base}
	}
	spans := buildColorSpans(sc.MessageStyles, base)
	n := 0
	for _, s := range spans {
		n += s.Len
	}
	out := make([]sdl.Color, 0, n)
	for _, s := range spans {
		for k := 0; k < s.Len; k++ {
			out = append(out, s.Color)
		}
	}
	return out
}

// toRenderEffectSpans maps courtroom spans to render spans; effect ids are equal, so this is a straight copy.
func toRenderEffectSpans(spans []courtroom.TextEffectSpan) []render.EffectSpan {
	if len(spans) == 0 {
		return nil
	}
	out := make([]render.EffectSpan, len(spans))
	for i, s := range spans {
		out[i] = render.EffectSpan{Start: s.Start, Len: s.Len, Effect: s.Effect}
	}
	return out
}

// effectsSig folds effect spans into a cache key because the effects frame is stripped from MessageRaw.
func effectsSig(spans []courtroom.TextEffectSpan) uint64 {
	const fnvOffset, fnvPrime = 1469598103934665603, 1099511628211
	h := uint64(fnvOffset)
	for _, s := range spans {
		h = (h ^ uint64(uint32(s.Start))) * fnvPrime
		h = (h ^ uint64(uint32(s.Len))) * fnvPrime
		h = (h ^ uint64(s.Effect)) * fnvPrime
	}
	return h
}

// sceneNeedsStyled reports whether any style run has a non-default colour, bold or italic (multi-span raster).
func sceneNeedsStyled(styles []courtroom.StyleRun) bool {
	for _, s := range styles {
		if s.Color != courtroom.ColorDefault || s.Bold || s.Italic {
			return true
		}
	}
	return false
}

// icColorChoices is the IC colour dropdown: wire palette, extended AsyncAO colours, then the fun modes;
// built at init because the IC row reads it every frame and must not allocate.
var (
	extColorFirst     = len(render.TextColorNames())
	icColorRainbowIdx = extColorFirst + render.ExtColorCount()
	icColorRandomIdx  = icColorRainbowIdx + 1
	icColorCustomIdx  = icColorRandomIdx + 1
	icColorChoices    = buildICColorChoices()
)

// buildICColorChoices assembles the dropdown labels: palette names, extended names, then Rainbow/Random/Custom.
func buildICColorChoices() []string {
	out := append([]string{}, render.TextColorNames()...)
	for i := 0; i < render.ExtColorCount(); i++ {
		out = append(out, render.ExtColorAt(i).Name)
	}
	return append(out, "Rainbow", "Random", "Custom…")
}

// applyICColorChoice routes a dropdown pick; the kinds are exclusive and only a standard 0..8 pick touches a.icColor,
// the wire text_color, because extended colours ship as markup.
func (a *App) applyICColorChoice(next int) {
	a.icCustomOn = false // every non-Custom pick turns the free hex off (mutual exclusion)
	switch {
	case next == icColorCustomIdx:
		// Seed from the last saved custom colour and open the wheel.
		a.icCustomOn = true
		a.icCustomRGB = a.d.Prefs.Load().ICCustomColorRGB()
		a.showICColorWheel = true
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = false })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = false })
		a.icExtColor = 0
	case next == icColorRainbowIdx:
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = true })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = false })
		a.icExtColor = 0
	case next == icColorRandomIdx:
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = true })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = false })
		a.icExtColor = 0
	case next >= extColorFirst && next < extColorFirst+render.ExtColorCount():
		a.icExtColor = next - extColorFirst + 1 // 1-based; 0 = none
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = false })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = false })
	default: // standard palette 0..8
		a.icColor = next
		a.icExtColor = 0
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = false })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = false })
	}
}

// icColorSelected returns the active dropdown row and its swatch colour, shared by both layouts.
func (a *App) icColorSelected() (sel int, swatch sdl.Color) {
	switch {
	case a.d.Prefs.Load().RainbowMessages:
		return icColorRainbowIdx, chatRainbow[0]
	case a.d.Prefs.Load().RandomMessageColor:
		return icColorRandomIdx, ColTextDim
	case a.icCustomOn:
		return icColorCustomIdx, sdl.Color{R: uint8(a.icCustomRGB >> 16), G: uint8(a.icCustomRGB >> 8), B: uint8(a.icCustomRGB), A: 255}
	case a.icExtColor > 0 && a.icExtColor <= render.ExtColorCount():
		return extColorFirst + a.icExtColor - 1, render.ExtColorAt(a.icExtColor - 1).Color
	default:
		return a.icColor, render.TextColor(a.icColor)
	}
}

// icFieldID is the immediate-mode id of the main IC field in both rows.
const icFieldID = "ic"

// icColorDDID is the immediate-mode id of the IC colour dropdown in both rows.
const icColorDDID = "colordd"

// icColorSel is a snapshot of the IC field's [lo,hi) rune selection, kept for the dropdown after focus is lost.
type icColorSel struct {
	lo, hi int
	active bool
}

// captureICColorSel snapshots the IC selection; call it every frame right before c.Dropdown("colordd", …) in both rows.
// The colour strip draws before the field, so the field is still focused here; the dropdown's open-click then
// reaches the field and unfocuses it before the pick lands next frame.
func (a *App) captureICColorSel() {
	c := a.ctx
	if lo, hi, ok := c.selectionFor(icFieldID, a.icInput); ok {
		a.icColorSel = icColorSel{lo: lo, hi: hi, active: true} // focused + non-empty: track live
		return
	}
	if c.focusID == icFieldID || c.ddOpen != icColorDDID {
		// Any other unfocus (a click on another widget) makes the snapshot stale; never wrap old rune indices.
		a.icColorSel.active = false
		return
	}
	// Unfocused while the colour dropdown is open: keep the frozen pre-open selection.
}

// applyICColorPick is Courtroom::on_text_color_changed (courtroom.cpp:6364-6403) for both IC bars: wrap a selection
// in its colour's markup (:6366-6389, none for c0), else set the message colour (:6391-6401), then focus_ic_input.
// AsyncAO-only extras have no AO2 markup, so they always set the whole-message colour.
func (a *App) applyICColorPick(next int) {
	sel := a.icColorSel
	a.icColorSel.active = false // consume the snapshot: the next open re-captures
	if sel.active && next >= 0 && next < extColorFirst {
		if start, end, ok := courtroom.AO2MarkupFor(next); ok {
			a.wrapICSelection(sel.lo, sel.hi, start, end)
		}
		// AO2 leaves the whole-message text_color untouched on a selection.
	} else {
		// No snapshot, or an AsyncAO-only colour with no AO2 markup.
		a.applyICColorChoice(next)
	}
	// Safe after a wrap: the open-click cleared caretField, so refocus cannot bring back the consumed selection.
	if !icColorPickOpensWheel(next) {
		a.bounceFocusToIC()
	}
}

// icColorPickOpensWheel reports whether row next opens the colour wheel, the one pick that must not refocus IC input.
// Diverges from courtroom.cpp:6402: AO2 has no Custom… row, and the IC field's Enter would send under the open wheel.
func icColorPickOpensWheel(next int) bool { return next == icColorCustomIdx }

// wrapICSelection splices the AO2 delimiters around the [lo,hi) rune range of a.icInput; field undo history catches it.
func (a *App) wrapICSelection(lo, hi int, start, end rune) {
	rs := []rune(a.icInput)
	if lo < 0 || hi > len(rs) || lo > hi { // stale snapshot vs a value edited since: ignore
		return
	}
	out := make([]rune, 0, len(rs)+2)
	out = append(out, rs[:lo]...)
	out = append(out, start)
	out = append(out, rs[lo:hi]...)
	out = append(out, end)
	out = append(out, rs[hi:]...)
	a.icInput = string(out)
}

// chatRainbow is the palette inline rainbow (\cr) cycles through, per rune.
var chatRainbow = []sdl.Color{
	{R: 255, G: 80, B: 80, A: 255},   // red
	{R: 255, G: 165, B: 0, A: 255},   // orange
	{R: 250, G: 235, B: 60, A: 255},  // yellow
	{R: 90, G: 220, B: 90, A: 255},   // green
	{R: 80, G: 200, B: 255, A: 255},  // blue
	{R: 185, G: 120, B: 255, A: 255}, // violet
}

// buildColorSpans resolves style runs into render spans; rainbow expands into per-rune spans across the message.
func buildColorSpans(styles []courtroom.StyleRun, def sdl.Color) []render.ColorSpan {
	out := make([]render.ColorSpan, 0, len(styles))
	rb := 0
	for _, s := range styles {
		switch {
		case s.Color == courtroom.ColorRainbow:
			for k := 0; k < s.Len; k++ {
				out = append(out, render.ColorSpan{Len: 1, Color: chatRainbow[rb%len(chatRainbow)], Bold: s.Bold, Italic: s.Italic})
				rb++
			}
		case s.Color == courtroom.ColorDefault:
			out = append(out, render.ColorSpan{Len: s.Len, Color: def, Bold: s.Bold, Italic: s.Italic})
		case s.Color >= courtroom.ColorHexBase: // exact transmitted hex: unpack 0xRRGGBB
			rgb := s.Color - courtroom.ColorHexBase
			col := sdl.Color{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 255}
			out = append(out, render.ColorSpan{Len: s.Len, Color: col, Bold: s.Bold, Italic: s.Italic})
		case s.Color >= courtroom.ColorExtBase: // extended AsyncAO color (#98): resolve by inline letter
			col, ok := render.ExtColorByCode(byte(s.Color - courtroom.ColorExtBase))
			if !ok {
				col = def // unknown code → the message's own colour
			}
			out = append(out, render.ColorSpan{Len: s.Len, Color: col, Bold: s.Bold, Italic: s.Italic})
		default:
			out = append(out, render.ColorSpan{Len: s.Len, Color: render.TextColor(s.Color), Bold: s.Bold, Italic: s.Italic})
		}
	}
	return out
}

// emoteDropdownLabelSep separates the ordinal from the comment, matching AO2's QString::number(n + 1) + ": " + comment.
const emoteDropdownLabelSep = ": "

// ensureEmoteChoices rebuilds the emote_dropdown list on a character or count change (AO2-Client emotes.cpp:176);
// it walks a.emotes, not a.emoteVisible, because the row index is the one selectEmote takes.
func (a *App) ensureEmoteChoices() {
	name := a.activeCharName()
	if a.emoteChoicesForName == name && a.emoteChoicesForCount == len(a.emotes) {
		return
	}
	a.emoteChoicesForName, a.emoteChoicesForCount = name, len(a.emotes)
	a.emoteChoices = a.emoteChoices[:0]
	for i := range a.emotes {
		// Deliberate divergence: AO2 shows a bare "2: " for an empty comment; fall back to the anim name like the grid.
		label := a.emotes[i].Comment
		if label == "" {
			label = a.emotes[i].Anim
		}
		a.emoteChoices = append(a.emoteChoices,
			strconv.Itoa(i+1)+emoteDropdownLabelSep+label)
	}
}
