package ui

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// previewEmote points the hover preview at an emote: its pre-animation when it
// has one (looped in the preview box — scrub the flourish before sending), else
// the talking sprite (what actually plays). Shared by both emote rows.
func (a *App) previewEmote(char string, e *courtroom.Emote) {
	// Stamp the caption AT SET TIME — we hold the *Emote right here, so the
	// preview box never has to reverse-map the URL back to a name (the old
	// lookup linear-scanned a.emotes rebuilding URLs every frame the box was
	// up — real alloc churn on 4000+ emote packs). previewNameFor ties the
	// name to THIS base; other previewBase setters auto-invalidate it.
	name := e.Comment
	if name == "" {
		name = e.Anim
	}
	if e.Preanim != "" && e.Preanim != "-" { // "-" / "" = no preanim (AO convention)
		a.budget.previewBase = a.link.urls.Emote(char, e.Preanim, courtroom.EmotePreanim)
		a.budget.previewName, a.budget.previewNameFor = name, a.budget.previewBase
		a.d.Manager.Prefetch(a.budget.previewBase, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preanim preview)
		return
	}
	a.budget.previewBase = a.link.urls.Emote(char, e.Anim, courtroom.EmoteTalk)
	a.budget.previewName, a.budget.previewNameFor = name, a.budget.previewBase
	a.d.Manager.PrefetchChain(a.budget.previewBase, a.link.urls.EmoteAlts(char, e.Anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
}

// followPinnedPreview retargets an ALREADY-OPEN, PINNED preview box to the emote
// that was just picked, so "pinned" means "keep showing whatever's currently
// selected" instead of freezing on whichever cell the hover/right-click that
// opened it happened to land on (playtest: "stuck on one you called it on").
func (a *App) followPinnedPreview(char string, e *courtroom.Emote) {
	if a.budget.previewBase == "" || !a.previewIsPinned() {
		return
	}
	a.previewEmote(char, e)
}

// drawSpritePreviewFallback paints the shared sprite-preview box on the courtroom
// passes where its PRIMARY draw site — the tail of the emote grid — did not run.
func (a *App) drawSpritePreviewFallback(themed bool) {
	if a.budget.previewBase == "" || a.emoteGridDrewPreview {
		return
	}
	// Latch BEFORE drawing, not after reading: the latch is cleared once per frame
	// by the poll cluster, so a pass that draws the box twice (a layout that ends up
	// calling both fallbacks, a future second courtroom pass) still composites it
	// exactly once.
	a.emoteGridDrewPreview = true
	// Clamp to the WINDOW, like the grid's own site.
	a.drawSpritePreview(a.frame.winW, a.frame.winH, false, a.previewedEmoteName())
	if themed {
		a.dismissPreviewOnClick()
		return
	}
	a.closeSpritePreviewOnLeave()
}

// previewedEmoteName returns the display name of whichever emote produced the
// currently-shown hover-preview box (a.previewBase), or "" if none. The name is
// CACHED at set time by previewEmote (which holds the *Emote), keyed to the base
// it was stamped for — two string compares per call, zero allocs. A previewBase
// set by anyone else (bgpicker, the wardrobe cycle) fails the key check and
// yields "" without those sites knowing the cache exists. Persisting through
// previewBase (not a per-cell hover) keeps the caption up while the cursor
// travels from the grid cell to the box (the preview boxes show the name too).
func (a *App) previewedEmoteName() string {
	if a.budget.previewBase == "" || a.budget.previewNameFor != a.budget.previewBase {
		return ""
	}
	return a.budget.previewName
}

func (a *App) drawEmoteRow(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	// Per-part tint: the grid has NO backing panel by default, so a
	// set colour ADDS one — un-set stays exactly as before (no fill at all).
	if col, ok := a.partPanel(partEmotes); ok {
		c.Fill(r, col)
	}
	if a.compose.charINIBusy {
		c.Label(r.X, r.Y, "Loading emotes...", ColTextDim)
		return
	}
	me := a.activeCharName() // iniswap override drives emotes + buttons
	useImages := a.d.Prefs.Load().EmoteButtonImages
	// Emote-name hover tooltip config, read ONCE per grid draw (the pref RLock stays
	// off the per-cell loop). When off, the loop's tooltip branch is skipped entirely.
	hoverNamesOn := a.d.Prefs.Load().EmoteHoverNamesOn
	hoverNamesDelay := a.d.Prefs.EmoteHoverNamesDelay()
	a.refreshEmoteView() // build the favourite set + the visible-index list (#77)
	vis := a.favstyle.emoteVisible

	// Uniform grid so hundreds of emotes page cleanly. The old layout
	// wrapped and `break`ed on overflow, silently dropping every emote past
	// the visible rows (playtest: many-emote characters were unusable). The
	// themed layout already paged (theme_layout.go); this brings the classic
	// row to parity.
	cellW, cellH := emoteBtnCell, emoteBtnCell
	if !useImages {
		cellW, cellH = emoteTextCellW, btnH
	}
	// Icon spacing is the user's (Settings → Interface), read ONCE here and threaded
	// through the layout maths + the cell loop — never re-read per cell, which would
	// put a pref RLock in the per-button path.
	gap := int32(a.d.Prefs.EmoteGridGap())
	cols := (r.W + gap) / (cellW + gap)
	if cols < 1 {
		cols = 1
	}
	// Reserve the arrow row ONLY when paging is needed, so normal characters
	// keep the full height. Two-pass and oscillation-free: reserving height
	// only shrinks a page, so a list that needed paging still does.
	gridH := r.H
	if len(vis) > int(cols*gridRows(gridH, cellH, gap)) {
		gridH = r.H - (btnH + 4)
	}
	// Stamp the layout this frame resolved; emotescroll.go derives the scroll
	// range, the number-key origin and the page readout from it.
	a.setEmoteGridMetrics(int(cols), int(gridRows(gridH, cellH, gap)), len(vis))
	perPage := a.emotepick.emotePerPage
	// Mouse-wheel over the grid SCROLLS IT BY ROW (up = earlier). It used to jump
	// a whole page per notch, which made the two rows either side of a page seam
	// impossible to see together. WheelIn fences the scroll so nothing else moves.
	if a.emoteGridScrolls() {
		if d := c.WheelIn(r); d != 0 {
			a.scrollEmoteRows(int(-d) * emoteWheelRows)
		}
	}
	start := a.emoteFirstSlot()

	for slot := start; slot < len(vis) && slot < start+perPage; slot++ {
		i := vis[slot] // real index into a.emotes (favs-only filters which show)
		e := &a.emotepick.emotes[i]
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
		selected := i == a.emotepick.emoteIdx
		if selected {
			c.Fill(sdl.Rect{X: btn.X - 2, Y: btn.Y - 2, W: btn.W + 4, H: btn.H + 4}, ColAccent)
		}
		var picked bool
		if useImages {
			picked = a.drawEmoteImageButton(btn, me, i, selected, label)
		} else {
			picked = c.Button(btn, label)
		}
		// The favourite ★ is drawn on top and wins the click: a press on the
		// corner star toggles the favourite instead of selecting the emote.
		if a.drawEmoteFavStar(btn, i) {
			// star toggled — swallow this cell's select for the frame
		} else if picked {
			a.selectEmote(i)
		}
		// Full-size preview after a 3 s hover (right-click = instant). If the
		// emote has a pre-animation, scrub THAT (it loops in the preview box) so
		// you can watch the flourish before sending; otherwise the TALKING
		// sprite — what actually plays when this emote is sent.
		if c.rightClicked && c.hovering(btn) {
			a.previewPinned = true // right-click pins the preview open until you close it
			// The LATCH only, never the pref: right-clicking one cell is a
			// this-box gesture, not "pin every preview from now on". The box's own
			// pin button is what writes the sticky preference.
		}
		if c.HoverPreview("emote:"+e.Anim, btn) {
			a.previewEmote(me, e)
		}
		// Emote-name tooltip: after the configured dwell, resting on a cell shows
		// its name (the [Emotions] comment, already computed as `label`). Independent
		// of the sprite hover-PREVIEW above — people who keep the heavier preview off
		// can still identify a cell. Gated on hovering(btn) so the id-string concat
		// runs ONLY for the single hovered cell, never per-cell on the settled frame
		// (the whole-screen 0-alloc gate). `label` is never empty (Comment→Anim above).
		if hoverNamesOn && c.hovering(btn) {
			c.TooltipAfterDelay("emotename:"+e.Anim, btn, label, hoverNamesDelay)
		}
	}
	// Favs-only with nothing starred yet: explain how to get out / add some.
	if len(vis) == 0 && a.d.Prefs.Load().EmoteFavOnly {
		c.Label(r.X, r.Y+4, "No favourite emotes yet — turn off ★ Favs, then click the ★ on an emote.", ColTextDim)
	}

	// Page arrows + counter (only when the list overflows one page). "<"/">"
	// match the Settings theme-cycle buttons and still move a WHOLE page — the
	// per-row step belongs to the wheel; a button that nudged one row would need
	// a dozen clicks to cross a big roster.
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

	// ★ Favs filter toggle (always present, so you can switch it off even with an
	// empty favs-only grid) + Random. Their DEFAULT is the grid's bottom-right
	// corner, but each is its own movable slot now ("the random char
	// button is always stuck in the emote grid corner") and hideable like any
	// control button.
	if !a.classic.panelHidden("ctrl.favsfilter") {
		a.drawEmoteFavToggle(a.classic.slotRect("ctrl.favsfilter", sdl.Rect{X: r.X + r.W - 158, Y: r.Y + r.H - btnH, W: 64, H: btnH}, a.frame.winW, a.frame.winH))
	}
	// Swap to a random available character — replaced the old "Random" emote
	// button (redundant with per-send auto-random + the wheel cycling emotes).
	if !a.classic.panelHidden("ctrl.randchar") {
		rcb := a.classic.slotRect("ctrl.randchar", sdl.Rect{X: r.X + r.W - 92, Y: r.Y + r.H - btnH, W: 92, H: btnH}, a.frame.winW, a.frame.winH)
		if c.Button(rcb, "Rand char") {
			a.randomChar()
		}
	}

	if a.budget.previewBase != "" {
		// Clamp to the WINDOW, not the stage — the box is draggable anywhere
		// (playtest: the preview could not be moved out of the viewport).
		a.drawSpritePreview(a.frame.winW, a.frame.winH, false, a.previewedEmoteName()) // ARM 2: caption the emote name
		a.closeSpritePreviewOnLeave()
		a.emoteGridDrewPreview = true // tell drawSpritePreviewFallback to stand down
	}
}

// emoteHasPreanim reports whether an emote defines a usable pre-animation.
func emoteHasPreanim(e *courtroom.Emote) bool {
	return e.Preanim != "" && e.Preanim != "-"
}

// selectEmote picks emote i (shared by click, the Random button, and the
// number-key shortcut): warms its pressed button art + the sprites the next
// message will need, and refocuses the IC input (AO2 focus_ic_input).
func (a *App) selectEmote(i int) {
	if i < 0 || i >= len(a.emotepick.emotes) {
		return
	}
	a.emotepick.emoteIdx = i
	a.compose.icPreanim = emoteHasPreanim(&a.emotepick.emotes[i]) // "Pre" auto-follows the pick (AO2-Client ui_pre, emotes.cpp:211-221)
	me := a.activeCharName()
	// Warm BOTH button states at HIGH: the _on (pressed) art the selected
	// cell wants, AND the _off art it falls back to while _on streams in —
	// so a freshly-pressed cell shows its sprite immediately instead of
	// blanking to the text-clipped fallback (the "button didn't load on
	// press" report).
	a.d.Manager.Prefetch(a.link.urls.EmoteButton(me, i+1, true), assets.AssetTypeEmoteButton, network.PriorityHigh)  // AssetType: EmoteButton (on)
	a.d.Manager.Prefetch(a.link.urls.EmoteButton(me, i+1, false), assets.AssetTypeEmoteButton, network.PriorityHigh) // AssetType: EmoteButton (off fallback)
	a.speculateEmote(me, &a.emotepick.emotes[i])
	a.followPinnedPreview(me, &a.emotepick.emotes[i]) // a pinned box follows the pick; a closed one stays closed
	a.ctx.FocusField("ic")
}

// emotePageCounter returns the "page x/y · N emotes" label, memoized so the
// per-frame emote-grid draw allocates nothing while paging is stable — the
// fmt.Sprintf runs only when the page, page count, or emote total changes (the
// same memoize-on-change idiom as the generation-cached texture pages).
func (a *App) emotePageCounter(page, pages, n int) string {
	if key := [3]int{page, pages, n}; a.emotepick.emotePageLabel == "" || key != a.emotepick.emotePageLabelKey {
		a.emotepick.emotePageLabel = fmt.Sprintf("page %d/%d · %d emotes", page, pages, n)
		a.emotepick.emotePageLabelKey = key
	}
	return a.emotepick.emotePageLabel
}

// shownTotalMemo memoizes a list header's "<shown> / <total>" counter, the same
// memoize-on-change idiom as emotePageCounter above. Without it the Sprintf is
// one heap allocation per frame for as long as the tab is open — invisible to
// the classic whole-screen gate (it only ever draws the IC-log tab) but measured
// the moment the themed gate put the music/areas panel on screen.
type shownTotalMemo struct {
	label        string
	shown, total int
}

// nowPlayingMemo memoizes the Music header's "Now playing: <track>" readout, the
// same memoize-on-change idiom as shownTotalMemo — the concatenation was one heap
// allocation per frame for as long as the Music tab was open.
type nowPlayingMemo struct {
	track string // the RAW wire entry the label was built from
	label string
}

// musicIconButton is one square icon control of the Music header: a hover plate,
// a vector glyph and a tooltip carrying the word the icon replaced. Tooltips are
// the accessible name here — every iconified control gets one.
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

// playRandomMusic plays a uniformly-random track from what the list currently
// SHOWS — AO2's music_random picks from the visible, non-header rows
// (courtroom.cpp:5947-5965), so a collapsed category or an active search filter
// is excluded exactly as it is there. a.musicRows is already that set.
func (a *App) playRandomMusic() {
	if a.link.sess == nil {
		return
	}
	n := 0
	for _, mr := range a.musicTab.musicRows {
		if !mr.header && mr.ti >= 0 && mr.ti < len(a.link.sess.Music) {
			n++
		}
	}
	if n == 0 {
		return
	}
	pick := rand.IntN(n)
	for _, mr := range a.musicTab.musicRows {
		if mr.header || mr.ti < 0 || mr.ti >= len(a.link.sess.Music) {
			continue
		}
		if pick == 0 {
			a.requestMusicTrack(a.link.sess.Music[mr.ti])
			return
		}
		pick--
	}
}

// requestMusicTrack is the ONE place a track play leaves the client: AO2's
// MC#<song>#<cid>[#<showname>][#<flags>] (courtroom.cpp:5806-5819). The area-jump
// call site deliberately keeps the bare two-field RequestMusic instead — AO2
// appends nothing at all for a jump (:6036-6044).
func (a *App) requestMusicTrack(entry string) {
	if a.link.sess == nil {
		return
	}
	a.link.sess.RequestMusicWithFlags(entry, a.effectiveShowname(), a.d.Prefs.Load().MusicFlags())
}

// nextRandomEmote picks a random emote index for auto-random mode. With more
// than one emote it returns a DIFFERENT index than cur (uniform over the rest,
// via an unbiased skip-current), so every send visibly changes the sprite;
// with 0 or 1 it can't vary and returns cur (the caller no-ops). Pure for tests.
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

// randomEmoteForSend rolls a fresh emote on send for auto-random mode, reusing
// selectEmote so the button art + sprites warm exactly as a manual pick would,
// and scrolls the grid to it. It varies among the VISIBLE emotes, so with the
// favs-only filter on it rolls within your favourites (and with it off — the
// default — `vis` is the whole list, so behaviour is unchanged). No-op with 0 or
// 1 visible emote.
func (a *App) randomEmoteForSend() {
	a.refreshEmoteView()
	vis := a.favstyle.emoteVisible
	if len(vis) <= 1 {
		return
	}
	cur := -1 // current selection's slot in the visible list (-1 if not visible)
	for k, idx := range vis {
		if idx == a.emotepick.emoteIdx {
			cur = k
			break
		}
	}
	slot := nextRandomEmote(len(vis), cur)
	i := vis[slot]
	if i == a.emotepick.emoteIdx {
		return
	}
	a.selectEmote(i)
	a.revealEmoteSlot(slot) // minimum scroll, not a page snap (emotescroll.go)
}

// cycleEmote steps the selection by delta with wrap and scrolls its page into
// view — keyboard-only emote stepping during RP (the Ctrl+emote-cycle hotkey).
// Unlike Random (which jumps anywhere), this walks the list in order. No-op
// with no emotes; selectEmote clamps the index and refocuses the IC input.
const (
	// msgCounterCap is the typical AO server IC length cap — past it the counter
	// turns red (the line may truncate). The counter now draws INSIDE the input's
	// right edge (see drawMsgCounter), so it no longer reserves any tail width — the
	// old msgCounterReserve tail slice was removed so the Pre/FX buttons win room on
	// a narrow bar instead of the counter's blank gutter.
	msgCounterCap     = 256
	msgCounterPad     = int32(6) // px inset of the count from the field's right edge
	msgCounterChipGap = int32(4) // gap kept between the count and the muted chip when both show
)

// drawMsgCounter draws the live IC character count INSIDE the input box's right
// edge (M5; ON by default, Settings-toggleable), mirroring the muted-chip idiom:
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
	c.Label(countX, input.Y+(input.H-14)/2, a.compose.icCountStr, col)
}

// refreshICCount refreshes the cached count string when the typed length changed
// and returns the length. Split out because the RESERVATION (icFieldTailReserve)
func (a *App) refreshICCount() int {
	n := utf8.RuneCountInString(a.compose.icInput)
	if n != a.compose.icCountN {
		a.compose.icCountN, a.compose.icCountStr = n, strconv.Itoa(n)
	}
	return n
}

// icCountBandW is the count's own footprint measured from wherever it starts:
// its glyphs plus the inset that separates it from whatever is to its right.
// Width-cached, so a stable count is a map hit and this stays alloc-free.
func (a *App) icCountBandW() int32 { return a.ctx.TextWidth(a.compose.icCountStr) + msgCounterPad }

// icMutedBandW is how much of the input's right edge the muted chip owns
// (0 when not muted). The chip metric MUST match the one the IC-bar draw site
// uses, so it lives here and both read it.
func (a *App) icMutedBandW() int32 {
	if a.link.sess == nil || !a.link.sess.Muted {
		return 0
	}
	return a.mutedChipW() + 2 + msgCounterChipGap
}

// mutedChipW is the muted chip's drawn width — one definition, read by the chip's
// own draw and by the reservation maths.
func (a *App) mutedChipW() int32 { return a.ctx.TextWidth(mutedChipLabel) + mutedChipPadPx }

// mutedChipLabel / mutedChipPadPx are the muted chip's text and its horizontal
// padding. Named (hard rule 9) because three sites — the chip draw, the counter's
// yield maths and the field tail reservation — have to agree on the same pixels.
const (
	mutedChipLabel = "🔇 muted"
	mutedChipPadPx = int32(12)
)

// icFieldTailReserve is the width of the band at the IC input's RIGHT edge that
// belongs to the overlays painted on top of it — the character counter and the
// muted chip. It is handed to Ctx.ReserveFieldTail BEFORE the field draws, so
// the typed line clips (and the keep-caret-visible scroll turns over) short of
// them instead of running underneath: the reported symptom was typing straight
// through the counter until the digits were unreadable.
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

// randomShowname swaps the active showname to a random saved preset (M6, the
// Ctrl+H hotkey). It sets the in-courtroom override, which effectiveShowname
// reads, so the next message carries it.
func (a *App) randomShowname() {
	presets := a.d.Prefs.Load().ShownameList()
	if len(presets) == 0 {
		a.warnLine = "No showname presets saved — add some in Settings → General"
		a.warnAt = a.now()
		return
	}
	a.compose.shownameOverride = presets[rand.IntN(len(presets))]
	a.warnLine = clampLine("Showname → " + a.compose.shownameOverride)
	a.warnAt = a.now()
}

// cycleShowname swaps the active showname to the NEXT saved preset (Ctrl+B),
// wrapping around — a quick in-courtroom flip through your shownames.
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
	a.compose.shownameOverride = presets[next]
	a.warnLine = clampLine("Showname → " + a.compose.shownameOverride)
	a.warnAt = a.now()
}

func (a *App) cycleEmote(delta int) {
	a.refreshEmoteView()
	vis := a.favstyle.emoteVisible
	n := len(vis)
	if n == 0 {
		return
	}
	// Walk the VISIBLE list in order (so favs-only steps through favourites).
	// Start from the current selection's slot, or -1 ("nothing selected") which
	// the wrap tolerates.
	cur := -1
	for k, idx := range vis {
		if idx == a.emotepick.emoteIdx {
			cur = k
			break
		}
	}
	slot := ((cur+delta)%n + n) % n
	a.selectEmote(vis[slot])
	a.revealEmoteSlot(slot) // minimum scroll, not a page snap (emotescroll.go)
}

// gridRows is how many cellH-tall rows (plus a gap px gap) fit in h, ≥ 1. gap is
// the user's emote-grid spacing, passed in rather than read from prefs so the
// arithmetic stays pure — and so the caller takes the pref lock ONCE per grid
// draw instead of once per call. cellH+gap is ≥ 1 for every reachable input
// (cellH is a positive constant), so the division cannot trap.
func gridRows(h, cellH, gap int32) int32 {
	if rows := (h + gap) / (cellH + gap); rows > 1 {
		return rows
	}
	return 1
}

// drawEmoteImageButton draws one emotions/button<N> cell, preferring the
// state-correct art and falling back to the _off art (selection ring still
// reads) and finally a text chip while textures stream in. Reports clicks.
// Pages resolve through the generation-keyed caches — a full emote grid
// redraw costs zero store locks while nothing was uploaded/evicted (the
// same trick as the char grid; this was the last per-frame LRU storm).
func (a *App) drawEmoteImageButton(btn sdl.Rect, me string, i int, selected bool, label string) bool {
	c := a.ctx
	base := a.link.urls.EmoteButton(me, i+1, selected)
	cache, gen := &a.rcache.emoteBtnOff, &a.rcache.emoteBtnOffGen
	if selected {
		cache, gen = &a.rcache.emoteBtnOn, &a.rcache.emoteBtnOnGen
	}
	page, ok := a.cachedPage(cache, gen, len(a.emotepick.emotes), i, base)
	if !ok {
		a.demandAsset(&a.emotepick.emoteAsk, len(a.emotepick.emotes), i, base, assets.AssetTypeEmoteButton) // AssetType: EmoteButton
		if selected {
			page, ok = a.cachedPage(&a.rcache.emoteBtnOff, &a.rcache.emoteBtnOffGen, len(a.emotepick.emotes), i, a.link.urls.EmoteButton(me, i+1, false))
		}
	}
	if ok && len(page.Frames) > 0 {
		// Fills the cell deliberately: AO2 scales the button art into the button
		// with Qt::IgnoreAspectRatio (aobutton.cpp AOButton::updateIcon), and the
		// cell itself already carries the theme's designed aspect (fixed
		// emoteBtnCell here, uniformly scaled emote_button_size in the themed grid
		// — see emoteCellScale). Letterboxing here would diverge from AO2.
		_ = c.Ren.Copy(page.Frames[0], nil, &btn)
	} else {
		// No emotions/button<N> art (404 / still streaming): fall back to the character
		// ICON — a face beats a bare grey box (players disliked the grey). The emote label
		// rides a strip at the bottom so emotes stay distinguishable even when every cell
		// shows the same icon. Grey only remains until even the icon streams in. One small
		// icon per character (NOT the full sprite), so no streaming storm / budget hit.
		iconBase := a.link.urls.CharIcon(me)
		if ip, iok := a.cachedPage(&a.rcache.emoteIconPages, &a.rcache.emoteIconGen, 1, 0, iconBase); iok && len(ip.Frames) > 0 {
			_ = c.Ren.Copy(ip.Frames[0], nil, &btn)
		} else {
			pending := a.demandAsset(&a.rcache.emoteIconAsk, 1, 0, iconBase, assets.AssetTypeCharIcon) // AssetType: CharIcon
			c.Fill(btn, ColPanel)
			c.Border(btn, ColPanelHi)
			if pending {
				// Blank cell, art still possible: keep the demand pump alive at idle.
				a.frame.frameDemandPending = true
			}
		}
		// Emote-name caption: opt-in (default OFF). Off ⇒ the fallback shows a clean
		// icon with no text overlay, which is what most players want; on ⇒ the name
		// strip returns for telling otherwise-identical fallback cells apart.
		if a.d.Prefs.Load().EmoteCaptions {
			strip := sdl.Rect{X: btn.X, Y: btn.Y + btn.H - 15, W: btn.W, H: 15}
			c.Fill(strip, sdl.Color{R: 0, G: 0, B: 0, A: 150})
			c.LabelClipped(btn.X+3, strip.Y+1, btn.W-6, label, ColText)
		}
	}
	return c.hovering(btn) && c.clicked
}

// drawPairPanel: partner picking is a searchable click-to-pick list (the
// old one-by-one </> cycle was unusable on 4000-char servers); offsets,
// flip and z-order live in the right column.
func (a *App) sendPairOrder(uid int, op string) {
	if a.link.sess == nil {
		return
	}
	a.link.sess.SendOOC(a.oocNameOrDefault(), fmt.Sprintf("/pairorder %d %s", uid, op))
}

func (a *App) drawPairPanel(w, h int32, pressed *bool) {
	c := a.ctx
	wasActive := a.wins.pairWin.dragging || a.wins.pairWin.resizing // detect the drag/resize-end frame for slot persistence
	r := a.classic.pairPanelRect(&a.env, w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	// Title bar / drag handle + close + bottom-right resize grip — a non-blocking
	// floating box (drawn in the box pass), so the courtroom behind stays live:
	// you can keep chatting with the pair menu open, and drag/resize it freely.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	c.Heading(r.X+pad, r.Y+6, "Pairing", ColText)
	if c.Button(sdl.Rect{X: r.X + r.W - 84 - pad, Y: r.Y + 3, W: 84, H: btnH}, "Close") {
		a.pairbox.showPair = false
		return
	}
	a.floatWinDrag(&a.wins.pairWin, sdl.Rect{X: r.X, Y: r.Y, W: r.W - 94 - pad, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.wins.pairWin, grip, r, pairPanelMinW, pairPanelMinH, pressed)
	a.drawResizeGrip(grip)
	if wasActive && !a.wins.pairWin.dragging && !a.wins.pairWin.resizing { // drag/resize just ended → remember where
		a.classic.persistPanelSlot(&a.env, slotPanelPair, r, w, h)
	}

	contentTop := r.Y + floatTitleH + 10

	// Left: searchable partner list.
	listW := r.W/2 - pad*2
	y := contentTop
	c.LabelClipped(r.X+pad, y, listW, "Partner: "+a.pairLabel(), ColAccent)
	y += 24
	a.pairbox.pairSearch, _ = c.TextField("pairsearch", sdl.Rect{X: r.X + pad, Y: y, W: listW - 80, H: fieldH}, a.pairbox.pairSearch, "Search...")
	if c.Button(sdl.Rect{X: r.X + pad + listW - 74, Y: y, W: 74, H: btnH}, "Unpair") {
		a.pairbox.pairWith = protocol.UnpairedCharID
	}
	y += fieldH + 8

	a.ensureCharLower()
	query := a.charsel.pairQ.get(a.pairbox.pairSearch)
	lineH := int32(22)
	listTop := y
	listH := r.Y + r.H - listTop - pad
	matches := int32(0)
	for i := range a.link.sess.Chars {
		if i != a.link.sess.MyCharID && (query == "" || strings.Contains(a.charsel.charLower[i], query)) {
			matches++
		}
	}
	if c.hovering(sdl.Rect{X: r.X + pad, Y: listTop, W: listW, H: listH}) {
		a.pairbox.pairScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: r.X + pad + listW - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
	a.pairbox.pairScroll = c.VScrollbar("pairscroll", track, a.pairbox.pairScroll, matches*lineH, listH)
	rowY := listTop - a.pairbox.pairScroll
	for i := range a.link.sess.Chars {
		if i == a.link.sess.MyCharID { // can't pair with yourself
			continue
		}
		if query != "" && !strings.Contains(a.charsel.charLower[i], query) {
			continue
		}
		if rowY > listTop+listH-lineH {
			break
		}
		if rowY >= listTop-lineH {
			row := sdl.Rect{X: r.X + pad, Y: rowY, W: listW - scrollBarW - scrollBarGap, H: lineH - 2}
			hover := c.hovering(row)
			if i == a.pairbox.pairWith {
				c.Fill(row, ColAccent)
			} else if hover {
				c.Fill(row, ColPanelHi)
			}
			c.LabelClipped(row.X+4, rowY+3, row.W-8, a.link.sess.Chars[i].Name, ColText)
			if hover && c.clicked {
				a.pairbox.pairWith = i
			}
		}
		rowY += lineH
	}

	// Right: placement controls (type the number, nudge with −/+, or
	// mousewheel over the row — all three work).
	rx := r.X + r.W/2 + pad
	ry := contentTop
	a.pairbox.pairOffX = a.offsetControl("pairoffx", rx, ry, "Offset X %", a.pairbox.pairOffX, &a.pairbox.pairOffXText)
	ry += 34
	a.pairbox.pairOffY = a.offsetControl("pairoffy", rx, ry, "Offset Y %", a.pairbox.pairOffY, &a.pairbox.pairOffYText)
	ry += 34
	a.pairbox.pairFlip = c.Checkbox(rx, ry, "Flip my sprite", a.pairbox.pairFlip)
	ry += 28
	// Explicit two-segment order control — WIRE-AWARE. On a FantaCode server the
	// classic ^order suffix is the only mechanism, so the segments set a.pairOrder
	// exactly as before. In a group (JSON) the server owns the roster and /pairorder
	// reorders it, so the same segments send the OOC command instead. The button
	// therefore "changes to reflect the wire" without changing its look. Wording
	// follows AO2-Client's ui_pair_order_dropdown ("To front"/"To behind").
	gp := a.link.sess.GroupPair
	inGroup := gp != nil && !gp.Empty()
	myIdx := -1
	if inGroup {
		for i, m := range gp.Members {
			if m.CharID == a.link.sess.MyCharID {
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
	// The whole control (label + 2 segments + gap) spans pairOrderLabelW + 2*segW + gap =
	// 192px from rx, inside the ~200px right column the offset rows already use — so it fits
	// even at the pair panel's minimum width (pairPanelMinW=440, right column ~200px). No
	// overflow at the fixed floating-panel width.
	segX := rx + pairOrderLabelW
	orderSeg := func(label string, active bool) bool {
		r := sdl.Rect{X: segX, Y: ry, W: pairOrderSegW, H: btnH}
		segX += pairOrderSegW + pairOrderSegGap
		if active {
			// Accent fill marks the live choice (bg=hover=accent so it reads selected, not
			// hoverable). Re-selecting the active segment is a harmless no-op below.
			return c.ButtonCol(r, label, ColAccent, ColAccent, ColAccent, ColBackground)
		}
		return c.Button(r, label)
	}
	// Tooltip explains the nuance of whichever wire we're on.
	orderTip := "Pair order rides the classic ^0/^1 suffix (FantaCode): ^0 = you in front, ^1 = you behind. FantaCode can only carry one partner."
	if inGroup {
		orderTip = "Group order (JSON additional_chars): reorders the whole group roster — any member to any position, not just front/behind."
	}
	c.Tooltip(sdl.Rect{X: rx, Y: ry, W: pairOrderLabelW + 2*pairOrderSegW + pairOrderSegGap, H: btnH}, orderTip)

	frontActive := a.pairbox.pairOrder == protocol.PairSpeakerInFront
	if inGroup {
		frontActive = myIdx == 0
	}
	if orderSeg("To front", frontActive) {
		if inGroup {
			a.sendPairOrder(a.link.sess.PlayerID, "front")
		} else {
			a.pairbox.pairOrder = protocol.PairSpeakerInFront
		}
	}
	if orderSeg("To behind", !frontActive) {
		if inGroup {
			a.sendPairOrder(a.link.sess.PlayerID, "back")
		} else {
			a.pairbox.pairOrder = protocol.PairSpeakerBehind
		}
	}
	ry += 36

	if inGroup {
		// Group roster (front→back) with per-member reorder buttons. Each row moves
		// that member one step toward the front (▲) or back (▼) via /pairorder.
		c.Label(rx, ry, "Group (front→back):", ColText)
		ry += 20
		for _, m := range gp.Members {
			upR := sdl.Rect{X: rx, Y: ry, W: 22, H: btnH}
			dnR := sdl.Rect{X: rx + 26, Y: ry, W: 22, H: btnH}
			name := m.Name
			if name == "" {
				name = fmt.Sprintf("char %d", m.CharID)
			}
			if m.CharID == a.link.sess.MyCharID {
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

	// Offset ghost editor: drag your sprite live; partner shows as a
	// translucent ghost at their last-known placement.
	pv := sdl.Rect{X: rx, Y: ry, W: r.W/2 - 2*pad, H: r.Y + r.H - pad - ry}
	if pv.H >= ghostMinHeightPx {
		a.drawPairGhost(pv, grip)
	}
}

// pairPanelRect is the Pair menu's floating-window rect (floatwin.go): movable +
// resizable, clamped on-screen, with a window-height-responsive default size.
func (classic1 *classicState) pairPanelRect(e *env, w, h int32) sdl.Rect {
	defH := clampI32(h-120, pairPanelMinH, 560)
	if r, ok := classic1.seedPanelFromSlot(&e.wins.pairWin, slotPanelPair, pairPanelDefW, defH, pairPanelMinW, pairPanelMinH, w, h); ok {
		return r
	}
	return e.wins.pairWin.rect(pairPanelDefW, defH, pairPanelMinW, pairPanelMinH, w, h)
}

const (
	pairPanelDefW = 580 // default width
	pairPanelMinW = 440 // floor: the two columns need room
	pairPanelMinH = 320 // floor: list + controls
)

// ghostMinHeightPx is the smallest useful ghost stage; below it the panel
// keeps just the numeric controls.
const ghostMinHeightPx = 70

// ghostAlpha is the partner ghost's translucency.
const ghostAlpha = 110

// drawPairGhost renders the live offset editor: a miniature stage, the
// pair partner translucent at THEIR offsets (from the last paired
// message), and your idle sprite at YOUR offsets — drag it to set them
// (the numeric rows above mirror live). Same offset math as the real
// viewport: percent of stage width/height.
func (a *App) drawPairGhost(pv, grip sdl.Rect) {
	c := a.ctx
	// CLIP THE STAGE. A character sprite is fitted to the stage HEIGHT with its
	// width following the aspect ratio (drawGhostSprite, the same rule the real
	// viewport and AO2's non-stretch splash arm use — animationlayer.cpp:229-260),
	// so a wide sprite is legitimately wider than this narrow preview column, and
	// the offset then slides it further still. AO2 gets away with the same
	// arithmetic because ui_vp_player_char is a CHILD WIDGET and Qt clips children
	// to the viewport; nothing clipped here, so the ghost painted straight over the
	// panel edge and out onto the courtroom, the music list and the IC bar.
	prevClip, hadClip := c.pushClip(pv)
	defer c.popClip(prevClip, hadClip)
	// Real background behind the ghosts, so you place your sprite against the
	// actual stage instead of a black void ("otherwise what's the point"). Uses
	// YOUR position's bg; a flat fill stands in until it streams in.
	bgPart, _ := courtroom.PositionScene(a.mySide())
	base := a.link.urls.Background(a.link.sess.Background, bgPart)
	if base != a.pairbox.pairBgKey {
		a.pairbox.pairBgPages, a.pairbox.pairBgKey = nil, base
	}
	if page, ok := a.cachedPage(&a.pairbox.pairBgPages, &a.pairbox.pairBgGen, 1, 0, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &pv)
	} else {
		c.Fill(pv, sdl.Color{R: 12, G: 12, B: 16, A: 255})
		if a.link.sess.Background != "" {
			a.d.Manager.Prefetch(base, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background (pair preview)
		}
	}
	c.Border(pv, ColPanelHi)

	// Partner ghost first (behind), then me.
	if a.pairbox.pairWith >= 0 && a.pairbox.pairWith < len(a.link.sess.Chars) {
		name := a.link.sess.Chars[a.pairbox.pairWith].Name
		// The scene's pair layer knows their REAL offsets — and their real idle
		// sprite — once a paired message arrived. Before that we ask their char.ini
		// for the pose they actually stand in (iniIdleAnimFor: the first [Emotions]
		// anim, AO2's emote 1) rather than minting the "normal" literal at them.
		gx, gy := 0, 0
		anim := a.iniIdleAnimFor(name, ghostFallbackEmote)
		base := a.link.urls.Emote(name, anim, courtroom.EmoteIdle)
		alts := a.link.urls.EmoteAlts(name, anim, courtroom.EmoteIdle)
		if sc := &a.link.room.Scene; sc.PairActive && strings.EqualFold(sc.Pair.Name, name) {
			gx, gy = sc.Pair.OffsetX, sc.Pair.OffsetY
			if sc.Pair.IdleBase != "" {
				base, alts = sc.Pair.IdleBase, nil // the exact sprite on stage — already resolved
			}
		}
		a.drawGhostSprite(pv, name, base, alts, gx, gy, false, ghostAlpha)
	}
	if me := a.activeCharName(); me != "" { // iniswap-aware: preview the folder that actually renders
		// YOUR ghost is the SELECTED emote's idle — the sprite the next message
		// really shows. The old hardcoded "normal" drew nothing for the many
		// packs without a normal.* sprite (playtest: the ghost sprite did not
		// display at all) while the live viewport worked fine.
		anim := ghostFallbackEmote
		if a.emotepick.emoteIdx >= 0 && a.emotepick.emoteIdx < len(a.emotepick.emotes) {
			anim = a.emotepick.emotes[a.emotepick.emoteIdx].Anim
		}
		base := a.link.urls.Emote(me, anim, courtroom.EmoteIdle)
		alts := a.link.urls.EmoteAlts(me, anim, courtroom.EmoteIdle)
		a.drawGhostSprite(pv, me, base, alts, a.pairbox.pairOffX, a.pairbox.pairOffY, a.pairbox.pairFlip, 255)
	}
	c.Label(pv.X+4, pv.Y+2, "drag your sprite to place — arrow keys nudge 1%", ColTextDim)

	// Drag: anywhere in the stage moves YOUR sprite (it is the only
	// thing being edited here — no hit-testing pixel art).
	pressed := c.mouseDown && !a.ghostPrev
	a.ghostPrev = c.mouseDown
	// …except on the resize corner, which the panel chrome owns. Excluded by
	// RECT rather than by reading the chrome's `pressed` flag because the flag is
	// already false by the time the ghost draws (floatWinResize consumed it), so
	// there is nothing left here to test — the geometry is the only honest signal.
	if pressed && c.hovering(pv) && !pointIn(c.mouseX, c.mouseY, grip) {
		a.ghostDrag = true
		a.ghostStart = [2]int32{c.mouseX, c.mouseY}
		a.ghostBase = [2]int{a.pairbox.pairOffX, a.pairbox.pairOffY}
	}
	if a.wins.pairWin.resizing { // a resize that started outside pv must not capture it mid-gesture either
		a.ghostDrag = false
	}
	if !c.mouseDown {
		a.ghostDrag = false
	}
	if a.ghostDrag && pv.W > 0 && pv.H > 0 {
		a.pairbox.pairOffX = clampOffset(a.ghostBase[0] + int(c.mouseX-a.ghostStart[0])*100/int(pv.W))
		a.pairbox.pairOffY = clampOffset(a.ghostBase[1] + int(c.mouseY-a.ghostStart[1])*100/int(pv.H))
		a.refreshPairOffsetBufs() // a focused numeric row must follow the drag live
	}
	// Arrow keys nudge your offset 1% for fine placement — only with no text
	// field focused (otherwise arrows move the text caret). Up = toward the
	// top of the stage (lower Y), matching the drag.
	if c.focusID == "" {
		switch c.keyPressed {
		case sdl.K_LEFT:
			a.pairbox.pairOffX = clampOffset(a.pairbox.pairOffX - 1)
		case sdl.K_RIGHT:
			a.pairbox.pairOffX = clampOffset(a.pairbox.pairOffX + 1)
		case sdl.K_UP:
			a.pairbox.pairOffY = clampOffset(a.pairbox.pairOffY - 1)
		case sdl.K_DOWN:
			a.pairbox.pairOffY = clampOffset(a.pairbox.pairOffY + 1)
		}
	}
}

// ghostFallbackEmote is the idle guessed for a character whose real sprite
// isn't known yet (no emote list / no paired message) — AO's conventional
// default idle.
const ghostFallbackEmote = "normal"

// drawGhostSprite draws one character's sprite (base + its spelling alts)
// at offset% of the stage, sized like the real viewport sizes sprites
// (full stage height). The texture's alpha-mod restores immediately —
// pages are shared with the live viewport.
func (a *App) drawGhostSprite(pv sdl.Rect, name, base string, alts []string, offX, offY int, flip bool, alpha uint8) {
	c := a.ctx
	page, ok := a.d.Store.Get(base)
	if !ok || len(page.Frames) == 0 || page.H == 0 {
		// Warm once per (character, base) — not per frame. Re-warms when the
		// base CHANGES (emote pick, pair message) even with the table full:
		// only brand-new characters count against the cap.
		if a.courtkeys.ghostWarm[name] != base {
			if a.courtkeys.ghostWarm == nil {
				a.courtkeys.ghostWarm = map[string]string{}
			}
			_, known := a.courtkeys.ghostWarm[name]
			if known || len(a.courtkeys.ghostWarm) < ghostWarmCap {
				a.courtkeys.ghostWarm[name] = base
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

// ghostSpriteRect is where one ghost lands on the miniature stage: the sprite
// scaled to the stage HEIGHT with its own aspect ratio, centred, then slid by
// the pair offsets as a percentage of the stage.
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

// offsetControl draws one pair-offset row: a typed field (text buffer
// commits only on a valid parse, so partial input like "-" survives
// typing), −/+ nudge buttons, and mousewheel over the row. Returns the
// possibly-updated value; the caller persists on change.
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
	// A mouse edit (−/+ button, wheel) while the field is FOCUSED: the field
	// displays the edit buffer, not val, so refresh it — wheeling never blurs,
	// and the row froze at its old number until a click-off (playtest: the rows
	// answered the buttons and the wheel but displayed 0 until clicked away from).
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

// refreshPairOffsetBufs re-mirrors a FOCUSED offset field's edit buffer after
// the value changed somewhere else (stage drag, arrow nudge) — while focused
// the field displays the buffer, so without this the number froze until blur.
func (a *App) refreshPairOffsetBufs() {
	switch a.ctx.focusID {
	case "pairoffx":
		a.pairbox.pairOffXText = strconv.Itoa(a.pairbox.pairOffX)
	case "pairoffy":
		a.pairbox.pairOffYText = strconv.Itoa(a.pairbox.pairOffY)
	}
}

func (a *App) pairLabel() string {
	if a.pairbox.pairWith <= protocol.UnpairedCharID || a.link.sess == nil || a.pairbox.pairWith >= len(a.link.sess.Chars) {
		return "(unpaired)"
	}
	return fmt.Sprintf("%d — %s", a.pairbox.pairWith, a.link.sess.Chars[a.pairbox.pairWith].Name)
}

// sendIC builds and sends the outgoing MS message (chat commands handled
// first: /pair, /unpair, /offset — AO2-Client parity).
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
		// Extended AsyncAO colour (#98): the exact colour rides as inline markup
		// (AsyncAO renders it); the wire text_color is the nearest standard index
		// so stock AO2 clients still see a sensible colour.
		e := render.ExtColorAt(ext)
		return "\\c" + string(e.Code) + text, e.Wire
	}
	return text, color
}

// midEmoteResolver resolves a :stem: shortcode against this character's emote list
// (case-insensitive on Anim or Comment) into the mid-text emote it stands for,
// carrying that emote's preanim and per-emote sound. The wire's silence sentinels
// ("", "0", "1") read as "no sound", matching outgoingSFXName's convention.
func (a *App) midEmoteResolver(stem string) (courtroom.MidEmote, bool) {
	for i := range a.emotepick.emotes {
		e := &a.emotepick.emotes[i]
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

// sendIC takes no shout argument any more. The interjection is ARMED state
// (a.icShout = AO2's objection_state) and the packet reads it here, which is
// where canon reads it too — on_chat_return_pressed, courtroom.cpp:2136-2147.
func (a *App) sendIC() {
	shout := a.compose.icShout
	text := strings.TrimSpace(a.compose.icInput)
	if cmdHandled := a.handleChatCommand(text); cmdHandled {
		a.compose.icInput = "" // commands clear instantly — the field's undo history catches it (Ctrl+Z)
		return
	}
	// Mid-text emotes (#130): a typed :emote: shortcode becomes the internal <e:...>
	// marker on the wire (readable to non-AsyncAO clients, rendered inline here).
	// Unknown :tokens: and plain text are untouched.
	text = courtroom.ExpandMidTextShortcodes(text, a.midEmoteResolver)
	// Blankpost: Enter on an empty input sends the AO single-space
	// message — your sprite plays with no text (the RP "just show my
	// character" convention; truly empty messages get server-rejected).
	if text == "" && shout == 0 {
		text = " "
	}
	if a.link.sess.MyCharID < 0 {
		return
	}
	// Muted (MU): the server disabled our IC speech (AO2 set_mute disables
	// ui_ic_chat_message; on_chat_return_pressed returns early when is_muted). Refuse
	// the send but KEEP the typed line — keep-until-echo never clears it (there's no
	// echo for a swallowed send), so the text stays in the field for when we're
	// unmuted. Surface it so a muted player isn't left typing into a void.
	if a.link.sess.Muted {
		a.pushOOC("[SERVER] You're muted — that message wasn't sent.", "")
		return
	}
	// AO2-Client chat_ratelimit parity: drop sends inside the window.
	if _, _, rateMs := a.d.Prefs.Load().Timing(); rateMs > 0 &&
		time.Since(a.srvMeta.lastICSend) < time.Duration(rateMs)*time.Millisecond {
		return
	}
	// Auto-random emote (OFF by default): roll a fresh sprite from this char's
	// set on every accepted send — for people who'd rather not click the grid,
	// and to surface emotes they'd never pick. Behaviorally identical to picking
	// that emote by hand (same Anim/Preanim/SFX/Mod ride along below). Placed
	// after the command/blankpost/rate-limit guards so a dropped send won't roll.
	if a.d.Prefs.Load().RandomEmote {
		a.randomEmoteForSend()
	}
	emote := courtroom.Emote{Anim: "normal", Preanim: "-", DeskMod: protocol.DeskShow}
	if a.emotepick.emoteIdx >= 0 && a.emotepick.emoteIdx < len(a.emotepick.emotes) {
		emote = a.emotepick.emotes[a.emotepick.emoteIdx]
	}
	hasPre := emoteHasPreanim(&emote)
	// Per-emote audio from char.ini: the emote's own sound accompanies the PREANIM and
	// nothing else, so a line sent with Pre unticked carries the silence sentinel. The
	// whole rule (and the canon it is transcribed from) lives in icsfx.go.
	sfxName := outgoingSFXName(&emote, a.compose.icPreanim, a.emotepick.sfxChoiceIdx, a.emotepick.sfxChoices)
	// AO2's sfx_on_idle rewrite (courtroom.cpp:2102-2116, OFF by default like
	// upstream): a hand-picked sound on an IDLE/ZOOM line promotes the emote mod so
	// receivers actually play it, with the pre name blanked and the delay zeroed so
	// no animation runs. All three fields move together — see sfxIdlePromotion.
	emoteMod := protocol.NormalizeOutgoingEmoteMod(emote.Mod, hasPre, a.compose.icPreanim, false, a.link.sess.Features)
	preEmote, sfxDelay := emote.Preanim, emote.SFXDelay
	if m, promoted := sfxIdlePromotion(emoteMod, a.emotepick.sfxChoiceIdx, a.d.Prefs.Load().SFXOnIdle, a.link.sess.Features.Has(protocol.FeaturePrezoom)); promoted {
		emoteMod, preEmote, sfxDelay = m, "", 0
	}
	// Per-emote blip override (2.9.1 custom_blips), else the character's.
	blip := emote.Blip
	if blip == "" {
		blip = a.compose.charBlips
	}
	// #M5 animated text: the sticky FX button wraps the whole message first (no-op when off
	// / blankpost / you already typed inline markup), then ParseTextEffects pulls all the
	// [shake]/[wave]/[rainbow] markup out into the wire text (tags removed, \cN colour kept)
	text = a.applyStickyEffect(text)
	text, effectSpans := courtroom.ParseTextEffects(text)
	if strings.TrimSpace(text) == "" && shout == 0 {
		text, effectSpans = " ", nil
	}
	// #M1 auto-status: a trigger word you typed (e.g. "brb") flips your status on the raw
	// text, BEFORE markup/markers, so the status change rides this very message.
	a.applyAutoStatus(text)
	// M61 fun colour: rainbow (\cr prefix), an extended AsyncAO colour (\c<letter>
	// + nearest-standard wire fallback, #98), or a random palette colour per message.
	customRGB := -1
	if a.compose.icCustomOn {
		customRGB = a.compose.icCustomRGB
	}
	text, msgColor := funColor(text, a.compose.icColor, a.compose.icExtColor-1, customRGB, a.d.Prefs.Load().RainbowMessages, a.d.Prefs.Load().RandomMessageColor, rand.IntN(render.TextColorCount))
	// Transmitted sprite style (#103): append the invisible zero-width marker at
	// the END of the text — other AsyncAO clients decode + render it on this
	// character; AO2/webAO see nothing. End placement keeps the visible text intact
	// if a server length-limits the message (worst case: the style is dropped).
	curStyle := a.mySpriteStyle()
	if marker := curStyle.EncodeChangeMarker(a.compose.lastSentStyle); marker != "" {
		text += marker
	}
	a.compose.lastSentStyle = curStyle
	// Transmitted character profile (#101 slice 2): the same invisible channel, also
	// send-on-CHANGE, appended AFTER the style marker (an older AsyncAO client reads only
	// the first zero-width run, so the style must stay first). Only pronouns + tagline
	// ride; the receiver shows them on this character's player-list card.
	curProfile := a.myWireProfile()
	if pm := curProfile.EncodeChangeMarker(a.compose.lastSentProfile); pm != "" {
		text += pm
	}
	a.compose.lastSentProfile = curProfile
	// Cross-client presence status (#M1): same invisible channel, send-on-change.
	if sm := courtroom.EncodeStatusChangeMarker(a.compose.myStatus, a.compose.lastSentStatus); sm != "" {
		text += sm
	}
	a.compose.lastSentStatus = a.compose.myStatus
	// Additive leading-space span shift: when the additive box prepends a leading
	// space below (line ~6188), every visible rune shifts right by one, but the
	// effect spans were indexed over the space-LESS visible text. Shift each span's
	// Start by +1 here — BEFORE EncodeEffectsMarker bakes them into the invisible
	// frame — so an AsyncAO receiver's begin() aligns the transmitted [shake]/[wave]/
	// [rainbow] over the right runes (the receiver's own prefix shift then composes
	// on top). The decision uses the SAME predicate the prepend uses, so the two can
	// never disagree; it's evaluated here because the first visible rune is invariant
	// from the blankpost fallback down to the prepend (applyAutoStatus doesn't
	// reassign text, funColor only prepends strippable \c markup, and every invisible
	// marker is appended at the end). effectSpans is nil on a blankpost and the
	// predicate is false on a leading-space message, so this is a no-op unless a
	// non-blank additive message actually carries effect markup.
	if a.compose.icAdditive && courtroom.AdditiveWantsLeadingSpace(text) {
		for i := range effectSpans {
			effectSpans[i].Start++
		}
	}
	// Animated-text spans (#M5): same invisible channel, appended LAST. Unlike the
	// send-on-change markers above, the effects frame is per-message CONTENT, so it rides
	// every message that carries [shake]/[wave]/[rainbow] markup.
	if marker := courtroom.EncodeEffectsMarker(effectSpans); marker != "" {
		text += marker
	}
	// #2 reactions: a queued reaction (you clicked React + picked an emoji) rides THIS
	// message as an invisible frame referencing the message you reacted to, then clears —
	// per-message content like the effects frame. AsyncAO viewers float the emoji; AO2/webAO
	// see clean text. When your own message echoes back you'll see it float too (matched
	// against your log), so there's no separate local echo.
	if a.reactUI.pendingReactSet {
		if marker := a.reactUI.pendingReact.EncodeMarker(); marker != "" {
			text += marker
		}
		a.reactUI.pendingReactSet = false
	}
	// AO2 additive parity (courtroom.cpp:2320-2326, 6563-6573): with the additive
	// box on, AO2 glues each fragment onto your previous line, so it keeps a literal
	// leading space in the IC input to separate sentences. We prepend that space at
	// packet build instead of in the field, so your typed line (a.icInput, read by
	// recordSentIC/icPendingSent below) stays clean — the wire result is identical.
	if a.compose.icAdditive {
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
		// #17 networked frame-synced effects: the char.ini [<emote>_Frame*]
		// sections, pre-assembled at parse into AO2's wire format. Empty for the
		// (vast majority of) emotes with no frame data — KFOCompat still fills the
		// template for KFO-Server. An IC-bar SFX-picker override doesn't rewrite the
		// per-frame SFX map (it targets the single main SFX), matching AO2.
		FrameShake:   emote.FrameShake,
		FrameRealize: emote.FrameRealize,
		FrameSFX:     emote.FrameSFX,
		Blipname:     blip,
		Side:         a.mySide(),
		// Never ship raw char.ini emote mods: legacy 2/3/4 values make
		// schema-strict clients drop the whole message.
		// "Pre" unchecked forces idle even when the emote has a preanim; the
		// echo re-runs this EmoteMod through scenemaker, so the LOCAL staging
		// (scenemaker.go's pre := EmoteMod==Preanim…) follows the same decision
		// — what you see matches what you sent. immediate stays false here:
		EmoteMod:    emoteMod, // normalized + possibly sfx_on_idle-promoted above
		CharID:      a.link.sess.MyCharID,
		Objection:   shout,
		TextColor:   msgColor, // swatch cycler, or M61 random-per-message colour
		Showname:    a.effectiveShowname(),
		PairWith:    a.pairbox.pairWith,
		PairOrder:   a.pairbox.pairOrder,
		OffsetX:     a.pairbox.pairOffX,
		OffsetY:     a.pairbox.pairOffY,
		Flip:        a.pairbox.pairFlip,
		Immediate:   outgoingImmediate(a.compose.icImmediate, a.compose.icPreanim), // non-interrupting preanim — only ships when Pre (a preanimation) is also on; AO2's pre_no_interrupt rides ui_pre
		Additive:    a.compose.icAdditive,                                          // #14 2.8: this message appends to your last (gated to the additive server + pref in the IC row)
		Slide:       a.compose.icSlide,                                             // #21: slide into position instead of cutting (AO2 ui_slide)
		Realization: a.compose.icRealize,                                           // #21: white flash + sound on this message (AO2 realization_state)
		Screenshake: a.compose.icShake,                                             // #21: shake the courtroom on this message (AO2 screenshake_state)
		KFOCompat:   a.link.sess.KFOCompat(),                                       // KFO-Server only: fill empty frame/effect fields (its MS validator rejects them)
		// AO2 screen effect (ui_effects_dropdown): "<name>|<folder>|<sound>", where
		// folder is OUR char.ini [Options] effects and sound is the merged property
		// (courtroom.cpp:2296-2308). "" for the "None" row.
		Effects: a.outgoingEffectsField(),
	}
	// Named custom interjection (2.10): the wire carries "4&<stem>"
	// (formatObjection assembles it; courtroom.cpp:2142).
	if shout == protocol.ShoutCustom && a.compose.customIdx >= 0 && a.compose.customIdx < len(a.compose.customShouts) {
		out.CustomShout = a.compose.customShouts[a.compose.customIdx].File
	}
	// Armed evidence rides this message; the wire shifts ids by 1 because
	// 0 means "no evidence" (legacy standard, courtroom.cpp:2160).
	if a.evid.evidPresent && a.evid.evidIdx >= 0 && a.evid.evidIdx < len(a.link.sess.Evidence) {
		out.EvidenceID = a.evid.evidIdx + 1
	}
	a.link.sess.SendChat(out)
	a.srvMeta.lastICSend = time.Now()
	a.recordSentIC(a.compose.icInput) // #8: remember the raw typed line for Up-arrow recall
	// Keep-until-echo (AO2-Client parity, courtroom.cpp handle_chatmessage):
	// The echo handler is prefix-aware (issue #56): if the box still starts
	a.compose.icPendingSent = a.compose.icInput
	// The ONE-SHOT arms clear here, exactly where AO2 clears them — beside
	// objection_state the moment the packet is built (courtroom.cpp:2327-2329,
	// and :2362 for the slide, "Slides can't be sticky for nausea reasons").
	a.compose.icSlide, a.compose.icRealize, a.compose.icShake = false, false, false
	a.compose.icShout = 0 // objection_state, courtroom.cpp:2327 — the line it literally sits on
	// The three STICKY picks get their reset here, in the same place AO2's
	// Courtroom::reset_ui does (courtroom.cpp:2340-2359). All three upstream
	// options ship TRUE ("sticky"), so out of the box every one of these is a
	// no-op and the picks survive the send — that IS AO2's behaviour, not an
	// AsyncAO shortcut. See clearEffectsAfterSend for why the upstream accessor
	// names read backwards.
	a.clearEffectsAfterSend()
	a.clearSFXAfterSend()
	a.clearPreAfterSend()
	// "Slides can't be sticky for nausea reasons" (courtroom.cpp:2362) — already
	// covered by the unconditional icSlide clear above.
}

// clearSFXAfterSend is courtroom.cpp:2340-2345: with Sticky Sounds OFF, a send
// puts the IC SFX dropdown back on its "Default" row — but ONLY when the sound
// could actually have been heard, i.e. sfx_on_idle is on or Pre was ticked. AO2
// guards it that way so a pick made on an idle line (which a stock client
// transmits silently — see outgoingSFXName) is not thrown away before the user
// gets to tick Pre and send it for real.
func (a *App) clearSFXAfterSend() {
	if a.d.Prefs.Load().StickySounds {
		return
	}
	if !a.d.Prefs.Load().SFXOnIdle && !a.compose.icPreanim {
		return
	}
	a.emotepick.sfxChoiceIdx = 0 // row 0 is the "auto" / Default row (sfxAutoLabel)
}

// clearPreAfterSend is courtroom.cpp:2356-2359: with Sticky Preanims OFF the Pre
// checkbox unticks after every send. There is no second condition upstream, and
// there is none here.
func (a *App) clearPreAfterSend() {
	if a.d.Prefs.Load().StickyPreanims {
		return
	}
	a.compose.icPreanim = false
}

// noteOwnICEcho lands the server's echo of OUR OWN IC message (CHAR_ID ==
// MyCharID) — the AO2-Client own-echo block in handle_chatmessage. Consumes
// the one-shot evidence-present state unconditionally, and resolves the box
// with a prefix-aware check (issue #56 — a plain exact-match gate discarded
// the pending snapshot on ANY edit, including simply continuing to type the
// next line during the round trip, and the box was then never cleared):
func (s *sessionState) noteOwnICEcho() {
	// Strip only the part that actually sent; whatever was typed on top
	// of it during the round trip is real, unsent draft and stays. When
	// icPendingSent is "" (nothing sent, or an earlier echo already
	// consumed it) this strips zero characters — a safe no-op, which is
	// why it needs no branch of its own. A draft that no longer opens
	// with the sent text is the genuine-edit case and TrimPrefix leaves
	// it untouched, same as the old guarded slice did. The IC field's
	// undo history records the clear at its next draw (fieldhistory.go's
	// out-of-band detector), so Ctrl+Z still brings a fully-cleared
	// sent line back — with real redo on top.
	s.compose.icInput = strings.TrimPrefix(s.compose.icInput, s.compose.icPendingSent)
	s.compose.icPendingSent = ""
	s.evid.evidPresent = false // presenting is one-shot: consumed by the message that displayed
}

// sentHistCap bounds a per-tab message recall ring (#8: IC, and the OOC ring that mirrors it).
const sentHistCap = 30

// recordSentLine pushes a just-sent line onto a recall ring (newest last), skipping blankposts and
// consecutive duplicates, capping at sentHistCap, and resetting the cursor (*idx) to the live draft.
// Shared by IC and OOC; off the render path (called once per send), so the growth never hits a frame.
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

// recallLine walks a recall ring (shell-style) when its field is focused and Up/Down is pressed: Up
// goes older, Down newer and back to the stashed live draft. Consumes the key so it can't double-act
// (also dedups when two fields sharing one input both call it in a frame). A few comparisons, no alloc.
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

// recordSentIC / recallIC: the IC recall ring (#8).
func (a *App) recordSentIC(raw string) {
	recordSentLine(raw, &a.compose.icSentHist, &a.compose.icRecallIdx)
}

func (a *App) recallIC() {
	recallLine(a.ctx, a.ctx.focusID == "ic", &a.compose.icInput, a.compose.icSentHist, &a.compose.icRecallIdx, &a.compose.icRecallDraft)
}

// recordSentOOC / recallOOC: the OOC recall ring — the same Up/Down history, fired from any of the
// OOC inputs (the OOC box "oocmsg", the bottom bar "ooc", the themed "ooc"), all sharing oocInput.
func (a *App) recordSentOOC(raw string) {
	recordSentLine(raw, &a.compose.oocSentHist, &a.compose.oocRecallIdx)
}

func (a *App) recallOOC() {
	f := a.ctx.focusID
	recallLine(a.ctx, f == "ooc" || f == "oocmsg", &a.compose.oocInput, a.compose.oocSentHist, &a.compose.oocRecallIdx, &a.compose.oocRecallDraft)
}

// areaLockedBg / areaCurrentBg fill a locked area's card dark red and the area
// you're in a dim accent (light text stays readable on top); areaLockedBorder is
// the brighter red outline that makes a locked card pop as its own button.
var (
	areaLockedBg     = sdl.Color{R: 96, G: 32, B: 32, A: 255}
	areaLockedBorder = sdl.Color{R: 170, G: 64, B: 64, A: 255}
	areaCurrentBg    = sdl.Color{R: 40, G: 56, B: 96, A: 255}
)

// areaCardGap is the vertical space left between area cards so each reads as a
// separate button rather than one continuous striped list (playtest request).
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

// mySide is OUR position: the char.ini side (or /pos override) — never
// the last speaker's position. Inheriting Scene.Position teleported us
// to whoever spoke last AND leaked custom side strings that strict
// receivers (LemmyAO's MS schema enumerates the eight standard sides)
func (a *App) mySide() string {
	if a.compose.sidePref != "" {
		return a.compose.sidePref
	}
	return defaultSide
}

// handleChatCommand implements /pair <id>, /unpair, /offset <x> [y],
// /pos <side>.
func (a *App) handleChatCommand(text string) bool {
	switch {
	case strings.HasPrefix(text, "/pos "):
		// applySide lowercases/trims and forwards /pos to the server, so typing it
		// in the IC box now moves you instantly too (matching the OOC-box command).
		a.applySide(strings.TrimPrefix(text, "/pos "))
		return true
	case strings.HasPrefix(text, "/pair "):
		if id, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(text, "/pair "))); err == nil {
			a.pairbox.pairWith = id
		}
		return true
	case text == "/unpair":
		a.pairbox.pairWith = protocol.UnpairedCharID
		return true
	case strings.HasPrefix(text, "/offset "):
		parts := strings.Fields(strings.TrimPrefix(text, "/offset "))
		if len(parts) >= 1 {
			if x, err := strconv.Atoi(parts[0]); err == nil {
				a.pairbox.pairOffX = clampOffset(x)
			}
		}
		if len(parts) >= 2 {
			if y, err := strconv.Atoi(parts[1]); err == nil {
				a.pairbox.pairOffY = clampOffset(y)
			}
		}
		a.refreshPairOffsetBufs() // pair panel rows mirror the command
		return true
	}
	return false
}

// renderRaster rasterizes the current message with its AO color. comicInk forces the
// DEFAULT colour to dark "ink" for the comic export's paper speech bubble (explicit
// \cN colours still win), so light default text isn't invisible on the white bubble.
func renderRaster(a *App, sc *courtroom.Scene, wrapW int32, skinned bool, pct int, comicInk bool) (*render.MessageRaster, error) {
	// The chat zoom font: rebuilt only when the Text knob changes.
	col := chatBaseColor(a, sc, skinned, comicInk)
	// Per-message font pick at the given scale (pct): the theme's "message" family
	// when it declares one (#39), else the override chain's first covering font
	// (CJK fallback) / the embedded font. The live chatbox passes the RESOLVED
	// message scale; the export passes a size fitted to the capture frame — so an
	// export gets the theme's family without its point size overriding a size
	// already fitted to the frame.
	logFont := a.messageFontFor(pct, sc.MessageText)
	// #77: rasterize with the DEVICE-scaled sibling at the ambient device scale so
	// the LIVE chatbox is crisp at UI scale. Exports run with textDevPct BRACKETED
	// to 100 for the whole session (see beginExportScaleBracket), so this ambient
	// read is already 100 there — native offscreen resolution, the live UI scale
	// can never leak into export pixels.
	dev := a.ctx.textDevPct
	font := a.ctx.deviceFontFor(logFont, pct)
	// The row advance AO2's Qt would use for the theme's own message face, in the
	// same DEVICE pixels the raster keeps its lineH in. 0 whenever the theme named
	// no message family (or its face has no OS/2 opinion), which is every
	// unthemed message and most themed ones.
	pitch := a.themeRowPitchDevice(elemMessage, pct)
	// Per-glyph fallback raster when the message has emoji OR mixes scripts no single
	// face covers (covers() reads the pick just made above — no rescan): each rune
	// routes to the color-emoji face or its covering text face, baseline-aligned.
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
	// Inline \cN colors → the multi-color span raster; plain messages keep the
	// untouched single-color path (col is their whole-message color).
	if sceneNeedsStyled(sc.MessageStyles) {
		m, err := render.RasterizeStyled(a.ctx.Ren, font, sc.MessageText, buildColorSpans(sc.MessageStyles, col), wrapW, dev)
		return centerRaster(m, err, sc, wrapW, pitch)
	}
	m, err := render.Rasterize(a.ctx.Ren, font, sc.MessageText, wrapW, col, dev)
	return centerRaster(m, err, sc, wrapW, pitch)
}

// centerRaster applies the webAO "~~" centre alignment to a freshly built raster when
// the scene asked for it (Scene.Centered). Wraps the (raster, err) return so the three
// build paths stay one-liners; a no-op for the common left-aligned case and on error.
func centerRaster(m *render.MessageRaster, err error, sc *courtroom.Scene, wrapW int32, rowPitch int32) (*render.MessageRaster, error) {
	if err != nil || m == nil {
		return m, err
	}
	if sc.Centered {
		m.Center(wrapW)
	}
	// AO2's row advance, when the theme's own message face has one SDL_ttf cannot
	// see (themerowpitch.go). Applied here rather than at the three build sites so
	// the plain, the styled and the per-glyph-fallback raster can never disagree
	// about how far apart the rows go. A no-op for every face without an OS/2
	// quarrel, and for every message drawn in the client's own font.
	m.UseRowPitch(rowPitch)
	return m, err
}

// chatBaseColor resolves a message's DEFAULT text colour (used as the whole-message colour
// and as the base for the animated-text path). The theme's "message" colour replaces only
// AO's default code 0 — explicit \cN colours always win — and only while the theme's own
// chatbox skin is drawn (theme colours assume their skin: black on paper, not our dark
// panel). comicInk forces dark ink for the comic export's white bubble.
func chatBaseColor(a *App, sc *courtroom.Scene, skinned, comicInk bool) sdl.Color {
	col := render.TextColor(sc.TextColor)
	switch {
	case sc.TextColor != 0:
		// an explicit message colour always wins (reads on either background)
	case comicInk:
		col = comicBubbleInk
	case skinned && sc.ChatSkinMsgHas:
		// The speaker's OWN chatbox ink (misc/<chat>/courtroom_fonts.ini) wins over
		// the theme's message colour — AO2's get_design_element ladder resolves the
		// chatbox folder before the theme (Courtroom::set_font).
		col = sdl.Color{R: sc.ChatSkinMsgColor.R, G: sc.ChatSkinMsgColor.G, B: sc.ChatSkinMsgColor.B, A: 255}
	case skinned && a.skin.themeHasMsg:
		col = a.skin.themeMsgCol
	}
	return col
}

// glyphCacheCap bounds the shared #M5 white-glyph cache. One chatbox message is ≤ the AO
// ~256-char limit, so this comfortably holds a whole message's distinct glyphs across a
// couple of font sizes without evicting its own glyphs mid-reveal (NewGlyphCache floors it).
const glyphCacheCap = 512

// renderAnimated builds the #M5 animated-text layout for a message that carries effect spans
// — the per-glyph path that can shake/wave/displace and rainbow-recolour. Single base colour
// (the message default) with rainbow overriding per glyph; inline \cN colour + emoji/CJK
// per-glyph fallback don't compose with effects yet (a documented follow-up). Returns the
// layout and the face it used (the glyph cache keys on it).
func renderAnimated(a *App, sc *courtroom.Scene, wrapW int32, skinned bool, pct int) (*render.AnimatedText, *ttf.Font) {
	col := chatBaseColor(a, sc, skinned, false)
	// #77 Part A PUNT: the animated per-glyph path (AnimatedText.Draw + GlyphCache)
	font := a.messageFontFor(pct, sc.MessageText) // #39: the theme's "message" family, if it names one
	// Per-rune fallback resolver (Task E): an effects line resolves each rune's face the
	// SAME way a plain message does — the covering CJK/broad face for scripts the chat font
	// lacks, the colour-emoji face for emoji — so emoji stop rendering as tofu and CJK stops
	// getting uniform .notdef advances. Built ONCE per message (layout time); Draw reads the
	// stored per-rune face, so the per-frame path keeps its zero-alloc guarantee.
	resolve := a.animFontResolver(font, pct, sc.MessageText)
	at := render.RasterizeAnimated(font, sc.MessageText, toRenderEffectSpans(sc.MessageEffects), animColors(sc, col), wrapW, resolve, a.ctx.fontChainGen)
	// The same AO2 row advance the plain raster gets (centerRaster), in the
	// LOGICAL pixels this path lays out in — an effects message must not be spaced
	// differently from the identical message without effects.
	at.UseRowPitch(a.themeRowPitchLogical(elemMessage, pct))
	return at, font
}

// animColors flattens the message's inline-colour runs into a per-rune colour slice so #M5
// effects compose with \cN colours (a red shaking word; the rainbow EFFECT still overrides
// per glyph at draw time). A message with no inline styling returns a single-element slice —
// RasterizeAnimated clamps every rune to that base colour. Bold/italic don't compose with
// effects yet (the animated path is single-font); only colour does.
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

// toRenderEffectSpans maps the courtroom (SDL-free) spans to the render package's spans. The
// effect ids are pinned equal, so the cast is a straight copy.
// Allocates once per effects message (build time), never per frame.
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

// effectsSig folds the effect spans into a cache key so ensureChatRaster rebuilds when they
// change even if the visible text didn't (the zero-width effects frame is stripped out of
// MessageRaw, so two messages with the same text but different effects share a raw key).
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

// sceneNeedsStyled reports whether any style run has a non-default color or
// bold/italic (so the message needs the multi-span raster). A plain message has
// a single default, non-bold, non-italic run (or none).
func sceneNeedsStyled(styles []courtroom.StyleRun) bool {
	for _, s := range styles {
		if s.Color != courtroom.ColorDefault || s.Bold || s.Italic {
			return true
		}
	}
	return false
}

// icColorChoices is the IC colour dropdown (#79/#98): the wire palette, then the
// extended AsyncAO colours (#98), then the two "fun colour" modes — so every
// colour is picked from one list rather than hidden in Settings. Built once at
// package init — the IC input row reads it every frame, so it must not allocate.
var (
	extColorFirst     = len(render.TextColorNames())
	icColorRainbowIdx = extColorFirst + render.ExtColorCount()
	icColorRandomIdx  = icColorRainbowIdx + 1
	icColorCustomIdx  = icColorRandomIdx + 1
	icColorChoices    = buildICColorChoices()
)

// buildICColorChoices assembles the dropdown label list once at init: standard
// palette names, extended colour names, then Rainbow/Random/Custom.
func buildICColorChoices() []string {
	out := append([]string{}, render.TextColorNames()...)
	for i := 0; i < render.ExtColorCount(); i++ {
		out = append(out, render.ExtColorAt(i).Name)
	}
	return append(out, "Rainbow", "Random", "Custom…")
}

// applyICColorChoice routes an IC colour-dropdown selection (shared by the
// classic and themed rows so they can't drift). The four kinds are mutually
// exclusive — picking one clears the others. Critically, only a standard 0..8
// pick touches a.icColor (the wire text_color); extended colours live in
// a.icExtColor and ship as inline markup with a nearest-standard wire fallback,
// so the wire field never leaves 0..8 (#98).
func (a *App) applyICColorChoice(next int) {
	a.compose.icCustomOn = false // every non-Custom pick turns the free hex off (mutual exclusion)
	switch {
	case next == icColorCustomIdx:
		// Free hex pick (Tifera): seed from the last persisted custom
		// colour and open the wheel. Selecting Custom… again just reopens it.
		a.compose.icCustomOn = true
		a.compose.icCustomRGB = a.d.Prefs.Load().ICCustomColorRGB()
		a.lookUI.showICColorWheel = true
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = false })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = false })
		a.compose.icExtColor = 0
	case next == icColorRainbowIdx:
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = true })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = false })
		a.compose.icExtColor = 0
	case next == icColorRandomIdx:
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = true })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = false })
		a.compose.icExtColor = 0
	case next >= extColorFirst && next < extColorFirst+render.ExtColorCount():
		a.compose.icExtColor = next - extColorFirst + 1 // 1-based; 0 = none
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = false })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = false })
	default: // standard palette 0..8
		a.compose.icColor = next
		a.compose.icExtColor = 0
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowMessages = false })
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RandomMessageColor = false })
	}
}

// icColorSelected returns the active dropdown row and its swatch preview colour
// for the current IC colour state, so both layouts highlight + preview the same
// thing. Reads only fields/prefs the IC row already reads each frame (0 alloc).
func (a *App) icColorSelected() (sel int, swatch sdl.Color) {
	switch {
	case a.d.Prefs.Load().RainbowMessages:
		return icColorRainbowIdx, chatRainbow[0]
	case a.d.Prefs.Load().RandomMessageColor:
		return icColorRandomIdx, ColTextDim
	case a.compose.icCustomOn:
		return icColorCustomIdx, sdl.Color{R: uint8(a.compose.icCustomRGB >> 16), G: uint8(a.compose.icCustomRGB >> 8), B: uint8(a.compose.icCustomRGB), A: 255}
	case a.compose.icExtColor > 0 && a.compose.icExtColor <= render.ExtColorCount():
		return extColorFirst + a.compose.icExtColor - 1, render.ExtColorAt(a.compose.icExtColor - 1).Color
	default:
		return a.compose.icColor, render.TextColor(a.compose.icColor)
	}
}

// icFieldID is the immediate-mode id of the main IC text field (both the classic
// and themed rows draw it under this id). captureICColorSel reads the field's
// selection by this id (only the focused field's selection is meaningful).
const icFieldID = "ic"

// icColorDDID is the immediate-mode id of the IC colour dropdown (shared by the
// classic and themed rows). captureICColorSel keeps its frozen selection alive
// ONLY while this dropdown is the open one — any other unfocus means the
// snapshot is stale and must not wrap on a later pick.
const icColorDDID = "colordd"

// icColorSel is a frozen snapshot of the IC field's [lo,hi) RUNE selection, taken
// while the field is still focused so the colour dropdown can wrap it even though
// the open-click has since unfocused the field. active is false when there was no
// selection to wrap. A plain value (no pointer) — the App holds one directly.
type icColorSel struct {
	lo, hi int
	active bool
}

// captureICColorSel freezes the IC field's live selection into a.icColorSel so a
// colour picked from the dropdown can wrap it (§3.8 select-and-colour, folded into
// the colour dropdown to match AO2's own on_text_color_changed). It MUST be called
// every frame right before c.Dropdown("colordd", …) in BOTH the classic and themed
// rows: the colour strip draws BEFORE the IC field, so at this point the field is
// still focused and its selAnchor/caret are intact — but the dropdown's open-click
// (dropdownEx doesn't consume it) reaches the later-drawn IC field and click-away
// unfocuses it, dropping the live selection before the pick lands next frame. So we
// snapshot here while it exists; once the dropdown is open the field is unfocused,
// selectionFor returns ok=false, and we leave the frozen snapshot untouched.
func (a *App) captureICColorSel() {
	c := a.ctx
	if lo, hi, ok := c.selectionFor(icFieldID, a.compose.icInput); ok {
		a.compose.icColorSel = icColorSel{lo: lo, hi: hi, active: true} // focused + non-empty: track live
		return
	}
	if c.focusID == icFieldID || c.ddOpen != icColorDDID {
		// Focused with no selection, OR unfocused without the colour dropdown
		// being the open one: any snapshot is stale. The second case is the
		// review-caught hazard — a click on some OTHER widget also arms the
		// snapshot on its way to unfocusing the field, and a much-later
		// dropdown pick must not wrap that long-gone selection (possibly at
		// stale rune indices into edited text). The frozen snapshot is only
		// meaningful inside the open colour dropdown's own focus-stealing
		// window, so that is the only unfocused state that preserves it.
		a.compose.icColorSel.active = false
		return
	}
	// Unfocused while the colour dropdown is open: keep the frozen pre-open selection.
}

// applyICColorPick is Courtroom::on_text_color_changed, transcribed whole
// (src/courtroom.cpp:6364-6403) — the IC colour dropdown's handler for BOTH IC
// bars. Three halves, in canon's order:
func (a *App) applyICColorPick(next int) {
	sel := a.compose.icColorSel
	a.compose.icColorSel.active = false // consume the snapshot: the next open re-captures
	if sel.active && next >= 0 && next < extColorFirst {
		if start, end, ok := courtroom.AO2MarkupFor(next); ok {
			a.wrapICSelection(sel.lo, sel.hi, start, end)
		}
		// No applyICColorChoice here: AO2 leaves the whole-message text_color
		// untouched on a selection.
	} else {
		// No frozen selection (or an AsyncAO-only extra: extended/rainbow/random/
		// custom hex, which have no AO2 wire markup): set the whole-message colour.
		a.applyICColorChoice(next)
	}
	// :6402, the handler's last statement — a colour pick is a step in composing the
	// line, so the keyboard goes back to the line (chatfocus.go half (b)). Safe on
	// the wrap branch too: the dropdown's open-click already unfocused the field,
	// which cleared caretField (ui.go textField), so the re-focus lands with the
	// caret at the end and selAnchor reset — it cannot resurrect the consumed
	// selection over the freshly spliced text.
	if !icColorPickOpensWheel(next) {
		a.bounceFocusToIC()
	}
}

// icColorPickOpensWheel reports whether dropdown row next hands the keyboard to
// the free-hex colour wheel rather than back to the message — the ONE arm of the
// pick that must not bounce.
// This is a deliberate divergence from courtroom.cpp:6402, and it is forced:
func icColorPickOpensWheel(next int) bool { return next == icColorCustomIdx }

// wrapICSelection splices the AO2 delimiters start/end around the [lo,hi) RUNE
// range of a.icInput, so a real AO2/webAO peer renders the wrapped span in colour
// (the characters survive to the wire). Rune-indexed (emoji/CJK selections splice
// on rune boundaries, never mid-codepoint). The splice lands in a.icInput directly,
// so the field's out-of-band change detector records the prior value on the next
// draw and Ctrl+Z undoes the wrap (fieldhistory.go). The build runs only on a pick
// (a rare event), so a plain slice build is fine — it is off the per-frame path.
func (a *App) wrapICSelection(lo, hi int, start, end rune) {
	rs := []rune(a.compose.icInput)
	if lo < 0 || hi > len(rs) || lo > hi { // stale snapshot vs a value edited since: ignore
		return
	}
	out := make([]rune, 0, len(rs)+2)
	out = append(out, rs[:lo]...)
	out = append(out, start)
	out = append(out, rs[lo:hi]...)
	out = append(out, end)
	out = append(out, rs[hi:]...)
	a.compose.icInput = string(out)
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

// buildColorSpans resolves the courtroom style runs into render color spans:
// ColorDefault → the message color, a palette index → that color, and rainbow
// expands into per-rune spans that flow continuously across the message.
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

// emoteDropdownLabelSep separates the 1-based ordinal from the emote's comment
// in the emote_dropdown row, matching AO2's QString::number(n + 1) + ": " + comment.
const emoteDropdownLabelSep = ": "

// ensureEmoteChoices rebuilds the emote_dropdown option list when the character
// or its emote count changes (AO2-Client emotes.cpp:176 —
// `addItem(QString::number(n + 1) + ": " + get_emote_comment(current_char, n))`).
func (a *App) ensureEmoteChoices() {
	name := a.activeCharName()
	if a.emotepick.emoteChoicesForName == name && a.emotepick.emoteChoicesForCount == len(a.emotepick.emotes) {
		return
	}
	a.emotepick.emoteChoicesForName, a.emotepick.emoteChoicesForCount = name, len(a.emotepick.emotes)
	a.emotepick.emoteChoices = a.emotepick.emoteChoices[:0]
	for i := range a.emotepick.emotes {
		// DIVERGENCE, deliberate: AO2's get_emote_comment returns the comment field
		// verbatim and never falls back, so an emote with an empty comment shows as
		// a bare "2: " there. We fall back to the animation name, matching what the
		// themed emote GRID already does, because a list of bare ordinals is
		// unusable and the two surfaces must agree on what an emote is called.
		label := a.emotepick.emotes[i].Comment
		if label == "" {
			label = a.emotepick.emotes[i].Anim
		}
		a.emotepick.emoteChoices = append(a.emotepick.emoteChoices,
			strconv.Itoa(i+1)+emoteDropdownLabelSep+label)
	}
}
