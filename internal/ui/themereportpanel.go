package ui

// The theme import report panel; it only shows what buildThemeReport produced on the theme-apply goroutine.
// Wrapping is baked on open and width change, because Ctx.WrapText allocates and the editor frame has an allocation
// budget. A panel, not a modal: it joins editorPanelUp and closeEditorPanels.

import (
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// editReportLineCap is a hard bound on the wrapped report (hard rule 4); past it the footer says where the bake
	// stopped.
	editReportLineCap = 224
	// editReportPanelW / editReportRowH: two rails wide, one field row per line, like the font and gallery panels.
	editReportPanelW = editRailW * 2
	editReportRowH   = editFieldRowH
	// editReportVisibleRows is a ceiling on lines drawn at once; the panel is height-clamped.
	editReportVisibleRows = 14
	// editReportBullet / editReportIndent open a note's first line and its continuations; the wrap width subtracts
	// them.
	editReportBullet = "• "
	editReportIndent = "   "
	// editReportCloseW is the width of the header's Close button.
	editReportCloseW = 60
	// editReportChromeRows counts the rows that are not body: the heading, the footer and its gutter.
	editReportChromeRows = 3
)

// themeReportPanel is the wrapped view of one report; the pointer is the open flag, so an unopened panel costs nothing.
type themeReportPanel struct {
	// w and src are the bake's inputs. src is compared entry by entry, not by length: two themes easily produce the
	// same number of notes, and a count key would leave the previous theme's notes on screen.
	w     int32
	src   []string
	notes int
	n     int
	line  [editReportLineCap]string
	// dropped counts notes the line cap cut off, so the footer can say so.
	dropped int
	// foot is the footer sentence, baked with the lines to avoid allocating per frame.
	foot   string
	scroll int
	// bakes counts rewraps: nothing else distinguishes wrapping on change from wrapping per frame.
	bakes int
}

func (p *themeReportPanel) bake(c *Ctx, report []string, w int32) {
	if p.w == w && sameReport(p.src, report) {
		return
	}
	p.w, p.src, p.notes = w, report, len(report)
	p.n, p.dropped, p.bakes = 0, 0, p.bakes+1
	textW := w - c.TextWidth(editReportBullet)
	if textW < editReportRowH {
		textW = editReportRowH // a panel too narrow to wrap in still lists something
	}
	for i, note := range report {
		lines := c.WrapText(note, textW, editReportLineCap)
		if len(lines) == 0 {
			continue
		}
		if p.n+len(lines) > len(p.line) {
			p.dropped = len(report) - i
			break
		}
		for j, l := range lines {
			prefix := editReportIndent
			if j == 0 {
				prefix = editReportBullet
			}
			p.line[p.n] = prefix + l
			p.n++
		}
	}
	// The footer also says when the line cap cut in, rather than the panel simply ending.
	p.foot = "notes " + strconv.Itoa(p.notes) + " · lines " + strconv.Itoa(p.n)
	if p.dropped > 0 {
		p.foot += " · " + strconv.Itoa(p.dropped) + " more notes than this panel can show"
	}
}

// openThemeReport does nothing with no notes: no chip is drawn either, as a panel saying "0 notes" says nothing useful.
func (a *App) openThemeReport() {
	if a.te == nil || len(a.themeReport) == 0 {
		return
	}
	a.te.report = &themeReportPanel{}
	a.uiDirty = true
}

// themeReportPanelUp is the one test, so the pointer blocking and the draw cannot disagree.
func (a *App) themeReportPanelUp() bool {
	return a.te != nil && a.te.report != nil
}

func editorReportPanelRect(w, h int32) sdl.Rect {
	pw := int32(editReportPanelW)
	if pw > w-editRowPad*2 {
		pw = w - editRowPad*2
	}
	ph := editReportRowH*int32(editReportVisibleRows+editReportChromeRows) + editRowPad*2
	if ph > h-editBannerH-editRowPad*2 {
		ph = h - editBannerH - editRowPad*2
	}
	return sdl.Rect{X: (w - pw) / 2, Y: editBannerH + editRowPad, W: pw, H: ph}
}

// drawThemeReportPanel is called with panel blocking released, so its controls are live while the rails behind are not.
func (a *App) drawThemeReportPanel(w, h int32) {
	if !a.themeReportPanelUp() {
		return
	}
	c := a.ctx
	p := a.te.report
	r := editorReportPanelRect(w, h)
	c.Fill(r, ColBackground)
	c.Border(r, ColAccent)
	c.Label(r.X+editRowPad, r.Y+4, "What this client noticed about this theme", ColText)
	closeR := sdl.Rect{X: r.X + r.W - editReportCloseW - editRowPad, Y: r.Y + 2, W: editReportCloseW, H: editReportRowH - 2}
	if c.Button(closeR, "Close") {
		a.te.report = nil
		a.uiDirty = true
		return
	}

	body := sdl.Rect{X: r.X + editRowPad, Y: r.Y + editReportRowH + 2,
		W: r.W - editRowPad*2, H: r.H - editReportRowH*editReportChromeRows - editRowPad}
	p.bake(c, a.themeReport, body.W)

	rows := int(body.H / editReportRowH)
	if rows > editReportVisibleRows {
		rows = editReportVisibleRows
	}
	// The wheel before the clip and through WheelIn, the single-consumer wheel, so the canvas below can't scroll too.
	if d := c.WheelIn(body); d != 0 {
		p.scroll -= int(d)
	}
	if p.scroll > p.n-rows {
		p.scroll = p.n - rows
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
	prev, had := c.pushClip(body)
	y := body.Y
	for i := p.scroll; i < p.n && i < p.scroll+rows; i++ {
		c.LabelClipped(body.X, y+3, body.W, p.line[i], ColTextDim)
		y += editReportRowH
	}
	c.popClip(prev, had)

	c.LabelClipped(r.X+editRowPad, r.Y+r.H-editReportRowH-2, r.W-editRowPad*2, p.foot, ColTextDim)
}

// drawEditorReportChip is the rail footer chip and the only way in; returns its height, 0 with nothing to report.
// It opens and does not toggle: the rail draws under c.modalOn while a panel is up, so Button cannot fire then.
func (a *App) drawEditorReportChip(rail sdl.Rect, y int32) int32 {
	n := len(a.themeReport)
	if n == 0 {
		return 0
	}
	// Baked, not built: the rail draws every frame. Rebuilt only when the count changes.
	if a.te.reportChipN != n || a.te.reportChip == "" {
		a.te.reportChipN = n
		a.te.reportChip = "Imported · " + strconv.Itoa(n) + " note"
		if n != 1 {
			a.te.reportChip += "s"
		}
	}
	row := sdl.Rect{X: rail.X + editRowPad, Y: y, W: rail.W - editRowPad*2, H: editRowH - 2}
	if a.ctx.Button(row, a.te.reportChip) {
		a.openThemeReport()
	}
	return editRowH
}
