package ui

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// IC emoji picker (#M2 slice 1): a button on the IC bar opens a grid of common emojis;
// clicking one inserts it into your message. Purely LOCAL and standard-client-safe — it
// just adds emoji to the text you type. The grid renders colour emoji through the same
// fallback raster as the chatbox. While open it is MODAL-FENCED (drawCourtroom sets
// c.modalOn) so a click on a cell can't fall through to the stage / log / emote row
// beneath; the picker + its button use the raw pointIn hit test, like the dropdown.
//
// The popup also carries the #M5 "Text FX" strip (shake / wave / rainbow): clicking one
// wraps your whole message in that effect's markup, so the animated-text feature is
// discoverable + one-click without learning the inline [shake]…[/shake] syntax. Other
// AsyncAO clients animate the text; AO2/webAO see the plain message (the markup is stripped
// + the spans ride an invisible frame — see courtroom/effectwire.go).

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
	fxStripH        = 30  // the #M5 Text FX button strip above the emoji grid
)

// icEffectTags is the #M5 one-click Text FX strip: each wraps the whole IC input in its
// markup tag. Power users can still type per-word [tag]…[/tag] directly.
var icEffectTags = []struct{ label, tag string }{
	{"Shake", "shake"},
	{"Wave", "wave"},
	{"Rainbow", "rainbow"},
}

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
	ph := rows*emojiCellPx + 8 + fxStripH
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
	a.drawEffectStrip(sdl.Rect{X: panel.X + 4, Y: panel.Y + 4, W: pw - 8, H: fxStripH - 6}) // #M5
	gridTop := panel.Y + 4 + fxStripH
	for i, e := range icEmojiSet {
		cx := panel.X + 4 + int32(i%emojiPickerCols)*emojiCellPx
		cy := gridTop + int32(i/emojiPickerCols)*emojiCellPx
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

// drawEffectStrip draws the #M5 Text FX buttons across r and wraps the IC input on a click.
// Raw pointIn hit test (the modal fence blocks c.hovering, so c.Button wouldn't register —
// same reason the emoji cells hit-test raw). Label centring mirrors ButtonCol.
func (a *App) drawEffectStrip(r sdl.Rect) {
	c := a.ctx
	n := int32(len(icEffectTags))
	bw := (r.W - (n-1)*4) / n
	for i, fx := range icEffectTags {
		btn := sdl.Rect{X: r.X + int32(i)*(bw+4), Y: r.Y, W: bw, H: r.H}
		hot := pointIn(c.mouseX, c.mouseY, btn)
		bg := ColPanel
		if hot {
			bg = ColPanelHi
		}
		c.Fill(btn, bg)
		c.Border(btn, ColTextDim)
		if t, ok := c.textTexture(fx.label, ColText, c.font); ok {
			c.blitLabel(t, btn.X+(btn.W-t.w)/2, btn.Y+(btn.H-t.h)/2, t.w)
		}
		if c.clicked && hot {
			a.wrapICEffect(fx.tag)
		}
	}
}

// wrapICEffect wraps the whole current IC input in an effect tag — the one-click "make my
// message shake/wave/rainbow". Toggles off if the input is already exactly that wrap. No-op
// on empty input (the caret lands after the closing tag, so wrapping nothing is useless).
func (a *App) wrapICEffect(tag string) {
	open, close := "["+tag+"]", "[/"+tag+"]"
	s := strings.TrimSpace(a.icInput)
	if s == "" {
		return
	}
	if strings.HasPrefix(s, open) && strings.HasSuffix(s, close) && len(s) > len(open)+len(close) {
		a.icInput = strings.TrimSuffix(strings.TrimPrefix(s, open), close) // toggle off
	} else {
		a.icInput = open + a.icInput + close
	}
	a.ctx.FocusField("ic")
}

// emojiPickerFence manages the picker's modal fence each frame, BEFORE the screen draws.
// c.modalOn PERSISTS across frames (like the dropdown's), so it MUST be released the
// frame the picker closes — otherwise the whole UI stays fenced and frozen (the reported
// open-then-close bug). The picker is courtroom-only, so it's force-closed if any other
// screen / overlay is up, to never strand the fence elsewhere.
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
