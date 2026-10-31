package ui

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// IC emoji picker: a button on the IC bar opens a grid of common emojis; clicking one inserts it into your message.
// Purely local: it just adds emoji to the text you type. The grid renders colour emoji through the chatbox's
// fallback raster. While open it is modal-fenced (drawCourtroom sets c.modalOn) so a cell click can't fall through
// to the stage, log or emote row; the picker and its button use the raw pointIn hit test, like the dropdown.

// icEmojiSet is a curated set of common reaction/chat emojis (emojiPickerCols per row). Two coverage rules avoid
// tofu boxes:
//  1. BMP symbol emoji (U+2xxx: ✨ ⭐ ❓ ❗ ✅ ❌ …) carry a trailing VARIATION SELECTOR-16 (U+FE0F). Without it they
//     are text-presentation code points and never reach the colour-emoji font (isEmojiBase only auto-promotes the
//     supplementary plane). The selector is invisible; ❤️ ✌️ ⚖️ already have it.
//  2. No bleeding-edge code points (e.g. 🫡 🫠, Unicode 14): emoji fonts that predate them lack them and a
//     streaming client can't ship its own glyphs, so the set stays within Unicode 11 and earlier.
var icEmojiSet = []string{
	"😀", "😂", "🙂", "😉", "😍", "😎", "😭", "😡",
	"👍", "👎", "👏", "🙏", "💪", "🤝", "✌️", "👋",
	"❤️", "💔", "💯", "🔥", "✨️", "⭐️", "🎉", "💀",
	"😱", "😳", "🤔", "😴", "😅", "😏", "🥺", "😤",
	"⚖️", "🔨", "📌", "❓️", "❗️", "✅️", "❌️", "💬",
	"🤣", "😘", "😩", "🙄", "🤡", "👀", "🤗", "😬",
}

const (
	emojiPickerCols = 8
	emojiCellPx     = 34
	emojiPickerPct  = 170 // emoji glyph size as a % of the UI font (~24px in a 34px cell)
	emojiBtnPct     = 120 // the IC-bar button's emoji size
)

// drawEmojiBarButton draws the IC-bar emoji button and reports a toggle click. It uses the raw pointIn test so it
// works whether or not the fence is up. Stores its rect so the overlay can anchor above it.
func (a *App) drawEmojiBarButton(btn sdl.Rect) bool {
	c := a.ctx
	a.emojiBtnRect = btn
	c.Fill(btn, ColPanel)
	hover := pointIn(c.mouseX, c.mouseY, btn)
	if a.showEmojiPicker || hover {
		c.Border(btn, ColAccent)
	} else {
		c.Border(btn, ColTextDim)
	}
	// Pre-warm the ~12 MB colour-emoji face on hover, so the grid and this button's glyph are in colour when the
	// picker
	// opens. A user who never hovers the button never triggers the read.
	if hover {
		a.ensureEmojiFontLoad()
	}
	a.labelEmoji(c.font, c.EmojiFont(emojiBtnPct), btn.X+4, btn.Y+(btn.H-18)/2, btn.W-6, "🙂", ColText)
	return c.clicked && hover
}

// drawEmojiPicker draws the open picker overlay (a grid anchored above the button) and inserts the clicked emoji.
// Called once per frame after the courtroom, for both layouts. A click outside the panel and button closes it.
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

// insertICEmoji appends an emoji to the IC input and re-focuses it; the kit puts the caret at the end.
func (a *App) insertICEmoji(emoji string) {
	a.icInput += emoji
	a.ctx.FocusField("ic")
}

// emojiPickerFence manages the picker's modal fence each frame, before the screen draws. c.modalOn persists across
// frames, so it must be released the frame the picker closes or the whole UI stays frozen. The picker is
// courtroom-only, so it is force-closed on any other screen or overlay.
func (a *App) emojiPickerFence(c *Ctx) {
	courtroomActive := a.screen == ScreenCourtroom && !a.gifExporting && !a.replaying && !a.makerOpen
	if a.showEmojiPicker && !courtroomActive {
		a.showEmojiPicker = false
	}
	if a.showEmojiPicker {
		c.modalOn = true
		a.emojiFenceOn = true
	} else if a.emojiFenceOn {
		c.modalOn = false // picker just closed → release the persistent fence
		a.emojiFenceOn = false
	}
}
