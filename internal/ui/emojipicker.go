package ui

import "github.com/veandco/go-sdl2/sdl"

// IC emoji picker (#M2 slice 1): a button on the IC bar opens a grid of common emojis;
// clicking one inserts it into your message. Purely LOCAL and standard-client-safe — it
// just adds emoji to the text you type. The grid renders colour emoji through the same
// fallback raster as the chatbox. While open it is MODAL-FENCED (drawCourtroom sets
// c.modalOn) so a click on a cell can't fall through to the stage / log / emote row
// beneath; the picker + its button use the raw pointIn hit test, like the dropdown.

// icEmojiSet is a curated set of common reaction/chat emojis (emojiPickerCols per row).
var icEmojiSet = []string{
	"😀", "😂", "🙂", "😉", "😍", "😎", "😭", "😡",
	"👍", "👎", "👏", "🙏", "💪", "🤝", "✌️", "👋",
	"❤️", "💔", "💯", "🔥", "✨", "⭐", "🎉", "💀",
	"😱", "😳", "🤔", "😴", "😅", "😏", "🥺", "😤",
	"⚖️", "🔨", "📌", "❓", "❗", "✅", "❌", "💬",
	"🤣", "😘", "😩", "🙄", "🤡", "👀", "🫡", "🫠",
}

const (
	emojiPickerCols = 8
	emojiCellPx     = 34
	emojiPickerPct  = 170 // emoji glyph size as a % of the UI font (~24px in a 34px cell)
	emojiBtnPct     = 120 // the IC-bar button's emoji size
)

// drawEmojiBarButton draws the IC-bar emoji button and reports a toggle click. It uses
// the raw pointIn test so it works whether or not the fence is up (it's up while the
// picker is open). Stores its rect so the overlay can anchor above it.
func (a *App) drawEmojiBarButton(btn sdl.Rect) bool {
	c := a.ctx
	a.emojiBtnRect = btn
	c.Fill(btn, ColPanel)
	if a.showEmojiPicker || pointIn(c.mouseX, c.mouseY, btn) {
		c.Border(btn, ColAccent)
	} else {
		c.Border(btn, ColTextDim)
	}
	a.labelEmoji(c.font, c.EmojiFont(emojiBtnPct), btn.X+4, btn.Y+(btn.H-18)/2, btn.W-6, "🙂", ColText)
	return c.clicked && pointIn(c.mouseX, c.mouseY, btn)
}

// drawEmojiPicker draws the open picker overlay (a grid anchored above the button) and
// inserts the clicked emoji. Called once per frame after the courtroom (covers both
// layouts). No-op when closed. A click outside the panel and the button closes it.
func (a *App) drawEmojiPicker(w, h int32) {
	if !a.showEmojiPicker {
		return
	}
	c := a.ctx
	a.ensureEmojiFontLoad() // stream the colour emoji face in if it isn't already
	rows := int32((len(icEmojiSet) + emojiPickerCols - 1) / emojiPickerCols)
	pw := int32(emojiPickerCols*emojiCellPx) + 8
	ph := rows*emojiCellPx + 8
	px := a.emojiBtnRect.X
	if px+pw > w-pad {
		px = w - pad - pw
	}
	if px < pad {
		px = pad
	}
	py := a.emojiBtnRect.Y - ph - 4
	if py < pad {
		py = a.emojiBtnRect.Y + a.emojiBtnRect.H + 4 // no room above → drop below the bar
	}
	panel := sdl.Rect{X: px, Y: py, W: pw, H: ph}
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)
	for i, e := range icEmojiSet {
		cx := panel.X + 4 + int32(i%emojiPickerCols)*emojiCellPx
		cy := panel.Y + 4 + int32(i/emojiPickerCols)*emojiCellPx
		cell := sdl.Rect{X: cx, Y: cy, W: emojiCellPx, H: emojiCellPx}
		if pointIn(c.mouseX, c.mouseY, cell) {
			c.Fill(cell, ColPanelHi)
			if c.clicked {
				a.insertICEmoji(e) // keep open so you can add several
			}
		}
		a.labelEmoji(c.font, c.EmojiFont(emojiPickerPct), cx+5, cy+4, emojiCellPx, e, ColText)
	}
	if c.clicked && !pointIn(c.mouseX, c.mouseY, panel) && !pointIn(c.mouseX, c.mouseY, a.emojiBtnRect) {
		a.showEmojiPicker = false
	}
}

// insertICEmoji appends an emoji to the IC input and re-focuses it; on the focus change
// the kit puts the caret at the end, right after the inserted emoji.
func (a *App) insertICEmoji(emoji string) {
	a.icInput += emoji
	a.ctx.FocusField("ic")
}
