package ui

// THE IMPORT REPORT PANEL (v1.90.0 W9) — design §3.1's "Import report … rail
// footer chip; a panel", and §Q5's "expanding to an in-canvas panel".
//
// ONE concern: showing the report that themereport.go already built. It computes
// nothing — every line comes out of App.themeReport, produced on the theme-apply
// goroutine by buildThemeReport — and it decides nothing about what a note says.
//
// WHY IT IS THIS WAVE'S AND NOT A LATER ONE. themereport.go shipped in W8 with the
// count and the FIRST note on the warn line and left the rest for here, in as many
// words ("The expandable panel is W9's, on top of the same slice"). Until it
// existed, App.themeReport was a bounded slice that was written on every apply,
// kept for a reader, and read by exactly one test — which is the precise shape
// CLAUDE.md rule 11 names as the failure the sidecar [overrides] tier shipped in
// for four waves. A report with sixty-four notes and a surface for one is a
// truncation with extra steps, and the notes it drops are the ones a theme author
// most needs: a skipped element is note twelve, not note one.
//
// THE WRAP IS BAKED, NOT DRAWN. Ctx.WrapText allocates, the editor frame is gated
// by editorChromeAllocBudget, and a report is up to themeReportCap notes of
// arbitrary length — so wrapping on the draw would be the per-frame-string-in-a-
// loop regression that budget is named for. The bake runs when the panel opens and
// when its width changes; every other frame walks a fixed array. Same pattern, same
// reason, as galleryCredit and bakeToggleLabels.
//
// A PANEL, NOT A MODAL (design §3.1, and the editor is allowed exactly two modals,
// neither of which is this): it joins editorPanelUp so the rails behind it go
// click-dead, and closeEditorPanels so Esc does not walk past it.

import (
	"strconv"

	"github.com/veandco/go-sdl2/sdl"
)

// ---------------------------------------------------------------------------
// Named caps and metrics (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// editReportLineCap bounds the baked, wrapped report. themeReportCap notes
	// (69) at the three wrapped lines a long degrade takes in a two-rail column is
	// 207; 224 is that with room for the longest few, and it is a HARD bound —
	// past it the bake stops and the footer says how many notes it stopped at, so
	// a pathological report cannot turn into an unbounded string array on the
	// editor (hard rule 4).
	editReportLineCap = 224
	// editReportPanelW / editReportRowH size the surface. Two rails wide and one
	// field row tall per line, exactly like the font and gallery panels, so the
	// editor keeps one answer to "how big is a panel".
	editReportPanelW = editRailW * 2
	editReportRowH   = editFieldRowH
	// editReportVisibleRows bounds how many lines are drawn at once; the panel is
	// height-clamped, so this is a ceiling rather than a promise about what fits.
	editReportVisibleRows = 14
	// editReportBullet / editReportIndent open a note's first line and its
	// continuations. Named because the bake writes them and the width the wrap is
	// measured against has to subtract the same thing.
	editReportBullet = "• "
	editReportIndent = "   "
	// editReportCloseW is the header's Close button. Named because a gate has to
	// press it to prove there is a live way out, and a gate that hard-coded 60 would
	// stop pressing the button the day the header was re-spaced — silently, since a
	// press that lands on nothing looks exactly like a press that did nothing.
	editReportCloseW = 60
	// editReportChromeRows is how many editReportRowH rows the body is NOT: the
	// heading above it and the footer plus its gutter below.
	editReportChromeRows = 3
)

// themeReportPanel is the baked, wrapped view of one report.
//
// BORN ON OPEN, and the pointer IS the open flag — the same shape themeGallery
// uses, and for the same reason: it carries a 224-entry line array that a session
// which never opens the panel should not be paying for.
type themeReportPanel struct {
	// w is the width the bake was wrapped to; src is the report it was of. Both are
	// the bake's inputs, and a change in either rewraps.
	//
	// src IS THE SLICE, compared entry by entry, not its length. A theme re-applied
	// under an open panel replaces App.themeReport wholesale, and two different
	// themes very easily produce the same NUMBER of notes — a count-keyed bake would
	// then leave the previous theme's diagnostics on screen under the new theme's
	// name, which is worse than showing nothing at all. sameReport is themereport.go's
	// own comparison, reused rather than repeated, and bounded by themeReportCap.
	w     int32
	src   []string
	notes int
	n     int
	line  [editReportLineCap]string
	// dropped counts notes the line cap cut off, so the footer can say so rather
	// than the panel silently ending.
	dropped int
	// foot is the footer SENTENCE, baked with the lines. Three concatenations and
	// two Itoas whose inputs change only when the bake runs — built at the draw it
	// is an allocation on every frame the panel is up, which is the rail chip's
	// lesson one surface along.
	foot   string
	scroll int
	// bakes counts rewraps — the test seam for "this wraps on change, not per
	// frame", which nothing observable from outside distinguishes.
	bakes int
}

// bake wraps a report into the fixed array, only when its inputs changed.
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
	// The honest footer: how many notes, how many lines, and — when the line cap cut
	// in — that it did, rather than the panel simply ending.
	p.foot = "notes " + strconv.Itoa(p.notes) + " · lines " + strconv.Itoa(p.n)
	if p.dropped > 0 {
		p.foot += " · " + strconv.Itoa(p.dropped) + " more notes than this panel can show"
	}
}

// ---------------------------------------------------------------------------
// Open / close
// ---------------------------------------------------------------------------

// openThemeReport arms the panel over the applied theme's report.
//
// A REPORT WITH NO NOTES DOES NOT OPEN, and the chip that would have opened it is
// not drawn either: "nothing was wrong with this theme" is best said by there
// being nothing to click, not by a panel that says "0 notes".
func (a *App) openThemeReport() {
	if a.te == nil || len(a.themeReport) == 0 {
		return
	}
	a.te.report = &themeReportPanel{}
	a.uiDirty = true
}

// themeReportPanelUp reports the panel being on screen. One spelling, so the
// fence's two halves and the draw cannot disagree.
func (a *App) themeReportPanelUp() bool {
	return a.te != nil && a.te.report != nil
}

// ---------------------------------------------------------------------------
// Drawing
// ---------------------------------------------------------------------------

// editorReportPanelRect is the panel's box — the gallery's geometry.
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

// drawThemeReportPanel paints the report. Called with the panel fence RELEASED,
// beside the gallery, so its own controls are live while the rails behind are not.
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
	// The wheel BEFORE the clip and through WheelIn — the kit's single-consumer
	// wheel, so the canvas underneath cannot also scroll from the same notch.
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

// drawEditorReportChip is the LEFT RAIL FOOTER chip design §3.1 names —
// `Imported · 4 notes` — and the only way into the panel.
//
// It returns the height it took so the rail's own footer can sit above it, and 0
// when there is nothing to report: an editor over a theme this client understood
// completely says nothing at all, which is the whole difference between a
// diagnostic and a nag.
//
// IT OPENS AND DOES NOT TOGGLE, and that is the panel fence rather than a choice.
// The chip lives on a RAIL, and the rail draws under c.modalOn while any panel is
// up (drawThemeEditor's bracket), so hovering() — and therefore Button — is blank
// here for exactly as long as the panel it opened is on screen. A toggle branch
// would be a branch that can never run. The way out is the panel's own Close, or
// Esc through closeEditorPanels; TestTheImportReportReachesAUserSurface presses
// both doors.
func (a *App) drawEditorReportChip(rail sdl.Rect, y int32) int32 {
	n := len(a.themeReport)
	if n == 0 {
		return 0
	}
	// THE LABEL IS BAKED, not built. The rail draws every frame and this string is
	// three concatenations and an Itoa; built here it would be a per-frame
	// allocation inside editorChromeAllocBudget — the bakeToggleLabels lesson,
	// applied to the one new string the rail gained. It is rebuilt only when the
	// count changes, which is once per theme apply.
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
