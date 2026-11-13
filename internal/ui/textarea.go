package ui

import (
	"unicode/utf8"

	hvalue "github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Multiline text editing for the evidence editor. TextField is deliberately single-line; the evidence Description
// needs a real multiline editor. TextArea keeps its own caret, selection and scroll (the Ctx ta* fields) but shares
// c.focusID so HandleEvent captures clipboard chords and select-all for it as for TextField. It only draws in the
// evidence editor's edit mode, off the hot path, so it may allocate (layout and wrap rebuild per frame).

// editAreaUp/editAreaDown are TextArea-only caret moves. They extend the editOp space after editWordRight and are
// intercepted before editStep, which is line-agnostic and would treat them as a no-op.
const (
	editAreaUp editOp = editWordRight + 1 + iota
	editAreaDown
)

// taLine is one laid-out visual line: the text drawn (without the newline) and its [start,end) rune range into the
// full value. hard marks a line that ended with an explicit '\n' rather than a soft wrap.
type taLine struct {
	text  string
	start int
	end   int
	hard  bool
}

// TextArea edits a multiline string in place (id keys focus). Enter inserts a newline instead of submitting;
// Up/Down move between visual lines; long lines soft-wrap and the box scrolls vertically. Returns the new value.
func (c *Ctx) TextArea(id string, r sdl.Rect, value string, placeholder string) string {
	c.fieldSeq = append(c.fieldSeq, id) // Tab-cycle order = draw order
	hover := c.hovering(r)
	press := c.mouseDown && !c.prevMouseDown
	if press && hover && c.dragID == "" {
		c.selectAll = false
		c.focusID = id
	}
	if c.clicked && c.dragID != id {
		c.selectAll = false
		if hover {
			c.focusID = id
		} else if c.focusID == id {
			c.focusID = ""
		}
	}
	focused := c.focusID == id

	c.Fill(r, ColPanel)
	border := ColTextDim
	if focused {
		border = ColAccent
	}
	c.Border(r, border)

	rc := utf8.RuneCountInString(value)
	if focused {
		// Fresh focus puts the caret at the end; clamp against a value that shrank; Ctrl+A arms a whole-value
		// selection.
		if c.taCaretField != id {
			c.taCaretField = id
			c.taCaret = rc
			c.taSelAnchor = -1
		}
		c.taCaret = hvalue.Clamp(c.taCaret, 0, rc)
		if c.taSelAnchor > rc {
			c.taSelAnchor = rc
		}
		if c.selectAll {
			c.taSelAnchor, c.taCaret = 0, rc
			c.selectAll = false
		}
	} else if c.taCaretField == id {
		c.taCaretField = "" // dropped focus → a future re-focus resets the caret to the end
	}

	const padX = int32(6)
	const padY = int32(4)
	innerW := r.W - 2*padX
	if innerW < 1 {
		innerW = 1
	}
	lineH := int32(c.chromeFace().Height()) + 4
	if lineH < 14 {
		lineH = 14
	}
	innerH := r.H - 2*padY
	if innerH < lineH {
		innerH = lineH
	}

	lines := c.layoutTextArea(value, innerW)

	// Mouse: press places the caret and anchors a selection; holding drags the caret end; shift+press extends from
	// the
	// current caret (as textField does).
	if (press && hover && c.dragID == "") || (c.dragID == id && c.mouseDown) {
		ly := int((c.mouseY - (r.Y + padY) + c.taScroll) / lineH)
		idx := c.taIndexAt(lines, ly, c.mouseX-(r.X+padX))
		if press && hover && c.dragID == "" {
			if c.shiftHeld {
				if c.taSelAnchor < 0 {
					c.taSelAnchor = c.taCaret
				}
			} else {
				c.taSelAnchor = idx
			}
			c.dragID = id
		}
		c.taCaret = idx
	}

	if focused {
		selLo, selHi := c.taSelRange(rc)
		if c.copyReq {
			if selLo < selHi {
				_ = sdl.SetClipboardText(string([]rune(value)[selLo:selHi]))
			} else if value != "" {
				_ = sdl.SetClipboardText(value)
			}
		}
		if c.cutReq {
			if selLo < selHi {
				_ = sdl.SetClipboardText(string([]rune(value)[selLo:selHi]))
				value, c.taCaret = editStep(value, c.taCaret, editInput{op: editDelete, selStart: selLo, selEnd: selHi})
			} else {
				_ = sdl.SetClipboardText(value)
				value, c.taCaret = "", 0
			}
			c.taSelAnchor = -1
			c.selectAll = false
		} else {
			in := editInput{
				typed:    c.typed + c.pastedRaw, // raw paste keeps the newlines a multiline field is for
				back:     c.backspace,
				wordBack: c.wordBack && c.wordDeleteOn,
				wordFwd:  c.wordFwdDel && c.wordDeleteOn,
			}
			switch c.keyPressed {
			case sdl.K_LEFT:
				in.op = editLeft
			case sdl.K_RIGHT:
				in.op = editRight
			case sdl.K_HOME:
				in.op = editHome
			case sdl.K_END:
				in.op = editEnd
			case sdl.K_DELETE:
				in.op = editDelete
			case sdl.K_UP:
				in.op = editAreaUp
			case sdl.K_DOWN:
				in.op = editAreaDown
			}
			if in.op == editNone {
				switch {
				case c.wordLeft:
					in.op = editWordLeft
				case c.wordRight:
					in.op = editWordRight
				}
			}
			// Enter is a line break, not submit.
			if c.enter && in.typed == "" && in.op == editNone && !in.back && !in.wordBack && !in.wordFwd {
				in.typed = "\n"
			}
			if in.typed != "" || in.back || in.wordBack || in.wordFwd || in.op != editNone {
				nav := in.op == editLeft || in.op == editRight || in.op == editHome || in.op == editEnd ||
					in.op == editWordLeft || in.op == editWordRight
				extend := nav && in.typed == "" && !in.back && !in.wordBack && !in.wordFwd && c.shiftHeld
				if extend {
					if c.taSelAnchor < 0 {
						c.taSelAnchor = c.taCaret
					}
				} else {
					in.selStart, in.selEnd = selLo, selHi
				}
				if in.op == editAreaUp || in.op == editAreaDown {
					value = c.taMoveVertical(value, lines, in.op)
				} else {
					value, c.taCaret = editStep(value, c.taCaret, in)
				}
				if !extend {
					c.taSelAnchor = -1
				}
				switch c.keyPressed { // consume nav keys so char keybinds don't also fire
				case sdl.K_LEFT, sdl.K_RIGHT, sdl.K_HOME, sdl.K_END, sdl.K_DELETE, sdl.K_UP, sdl.K_DOWN:
					c.keyPressed = 0
				}
			}
		}
	}

	// Re-layout after edits so the caret line and scroll track the new text.
	lines = c.layoutTextArea(value, innerW)

	// Wheel scroll and keep the caret line visible, then clamp.
	c.taScroll -= c.WheelIn(r) * scrollStepPx
	contentH := int32(len(lines)) * lineH
	if focused {
		cl := c.taCaretLine(lines, c.taCaret)
		top := int32(cl)*lineH - c.taScroll
		if top < 0 {
			c.taScroll += top
		}
		if top+lineH > innerH {
			c.taScroll += (top + lineH) - innerH
		}
	}
	if contentH < innerH {
		c.taScroll = 0
	}
	if max := contentH - innerH; c.taScroll > max {
		c.taScroll = max
	}
	if c.taScroll < 0 {
		c.taScroll = 0
	}

	// Draw: selection highlight under the text, then text, then the caret.
	clipPrev, clipHad := c.pushClip(sdl.Rect{X: r.X + padX, Y: r.Y + padY, W: innerW, H: innerH})
	selLo, selHi := -1, -1
	if focused {
		selLo, selHi = c.taSelRange(rc)
	}
	if value == "" && !focused {
		c.Label(r.X+padX, r.Y+padY, placeholder, ColTextDim)
	} else {
		y := r.Y + padY - c.taScroll
		for i := range lines {
			ln := &lines[i]
			dy := y + int32(i)*lineH
			if dy+lineH < r.Y+padY || dy > r.Y+padY+innerH {
				continue
			}
			if selLo >= 0 && selLo < selHi && selLo < ln.end && selHi > ln.start {
				n := utf8.RuneCountInString(ln.text)
				x0 := c.TextWidth(string([]rune(ln.text)[:hvalue.Clamp(selLo-ln.start, 0, n)]))
				x1 := c.TextWidth(string([]rune(ln.text)[:hvalue.Clamp(selHi-ln.start, 0, n)]))
				if x1 > x0 {
					c.Fill(sdl.Rect{X: r.X + padX + x0, Y: dy, W: x1 - x0, H: lineH},
						sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 90})
				}
			}
			c.Label(r.X+padX, dy, ln.text, ColText)
		}
	}
	if focused && c.caretOn && len(lines) > 0 {
		cl := c.taCaretLine(lines, c.taCaret)
		if cl < 0 {
			cl = 0
		}
		if cl >= len(lines) {
			cl = len(lines) - 1
		}
		n := utf8.RuneCountInString(lines[cl].text)
		col := hvalue.Clamp(c.taCaret-lines[cl].start, 0, n)
		cx := r.X + padX + c.TextWidth(string([]rune(lines[cl].text)[:col]))
		c.Fill(sdl.Rect{X: cx, Y: r.Y + padY + int32(cl)*lineH - c.taScroll, W: 2, H: lineH}, ColText)
	}
	c.popClip(clipPrev, clipHad)

	// Draggable scrollbar on the right.
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	c.taScroll = c.VScrollbar("ta"+id, track, c.taScroll, contentH, innerH)

	return value
}

// taSelRange returns the ordered (lo, hi) rune selection, or (0,0) when none.
func (c *Ctx) taSelRange(rc int) (int, int) {
	a, b := c.taSelAnchor, c.taCaret
	if a < 0 {
		return 0, 0
	}
	if a > b {
		a, b = b, a
	}
	if a < 0 {
		a = 0
	}
	if b > rc {
		b = rc
	}
	return a, b
}

// taMoveVertical moves the caret one visual line up or down, clamping the column to the target line's length. It
// returns the unchanged value and sets the caret.
func (c *Ctx) taMoveVertical(value string, lines []taLine, op editOp) string {
	caret := c.taCaret
	li := c.taCaretLine(lines, caret)
	col := caret - lines[li].start
	ni := li + 1
	if op == editAreaUp {
		ni = li - 1
	}
	if ni < 0 || ni >= len(lines) {
		return value
	}
	nr := utf8.RuneCountInString(lines[ni].text)
	if col > nr {
		col = nr
	}
	c.taCaret = lines[ni].start + col
	return value
}

// taCaretLine maps a caret rune index to its visual line. A caret at a hard-break boundary belongs to the next
// line (it renders at that line's start); the end of the value belongs to the last line.
func (c *Ctx) taCaretLine(lines []taLine, caret int) int {
	for i := range lines {
		if caret >= lines[i].start && caret < lines[i].end {
			return i
		}
	}
	for i := range lines {
		if caret == lines[i].end && i < len(lines)-1 {
			return i + 1
		}
	}
	if len(lines) == 0 {
		return 0
	}
	return len(lines) - 1
}

// taIndexAt maps a click (visual line ly, x px) to a rune index into the value.
func (c *Ctx) taIndexAt(lines []taLine, ly int, mx int32) int {
	if len(lines) == 0 {
		return 0
	}
	if ly < 0 {
		ly = 0
	}
	if ly >= len(lines) {
		ly = len(lines) - 1
	}
	ln := lines[ly]
	runes := []rune(ln.text)
	for i := range runes {
		if c.TextWidth(string(runes[:i+1])) >= mx {
			return ln.start + i
		}
	}
	return ln.end
}

// layoutTextArea lays the value out into visual lines: hard breaks at '\n', then a greedy word wrap at maxW. Each
// line records its [start,end) rune range so caret, click mapping and selection share coordinates.
func (c *Ctx) layoutTextArea(value string, maxW int32) []taLine {
	runes := []rune(value)
	lines := make([]taLine, 0, 8)
	seg := make([]rune, 0, 32) // the current hard segment (no '\n')
	segStart := 0              // absolute rune index of seg[0]

	flush := func(hard bool) {
		if len(seg) == 0 {
			// An empty line (two adjacent newlines, or a trailing newline).
			lines = append(lines, taLine{text: "", start: segStart, end: segStart, hard: hard})
			return
		}
		lo := 0
		for lo < len(seg) {
			hi, lastSpace := lo, -1
			for hi < len(seg) {
				if hi > lo && c.TextWidth(string(seg[lo:hi+1])) > maxW {
					break
				}
				if seg[hi] == ' ' {
					lastSpace = hi
				}
				hi++
			}
			if hi == lo {
				hi = lo + 1 // one overlong word still gets at least one rune per line
			}
			if hi < len(seg) && lastSpace >= lo && lastSpace < hi {
				hi = lastSpace + 1 // prefer breaking at the last space
			}
			lines = append(lines, taLine{text: string(seg[lo:hi]), start: segStart + lo, end: segStart + hi, hard: false})
			lo = hi
		}
		if hard {
			lines[len(lines)-1].hard = true
		}
	}

	for i := 0; i < len(runes); i++ {
		if runes[i] == '\n' {
			flush(true)
			seg = seg[:0]
			segStart = i + 1
		} else {
			seg = append(seg, runes[i])
		}
	}
	if len(seg) > 0 {
		flush(false)
	} else if len(runes) == 0 || runes[len(runes)-1] == '\n' {
		lines = append(lines, taLine{text: "", start: segStart, end: segStart, hard: false})
	}
	return lines
}
