package ui

// Case notebook UI: the Notes tab; pins come from the IC log, the evidence panel or the input here, stored per server.

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// pinNote adds one line to the current server's notebook; a no-op before the off-thread load lands.
func (a *App) pinNote(line string) {
	if a.notebook == nil {
		return
	}
	a.notebook.Add(line)
	a.pushDebug("notebook: pinned " + clampLine(line))
	a.warnLine = "Pinned to notebook (Notes tab)"
	a.warnAt = time.Now()
}

// pollNotebook lands a load on the session or parked tab whose key matches, so a tab switch cannot cross notebooks.
func (a *App) pollNotebook() {
	select {
	case res := <-a.notebookRes:
		if res.key == a.serverKey {
			a.notebook = res.nb
			return
		}
		for i, t := range a.tabs {
			if i != a.activeTab && t.state.serverKey == res.key {
				t.state.notebook = res.nb
				return
			}
		}
		// The owning tab was closed: drop.
	default:
	}
}

// drawNotesTab renders the wrapped notebook list, per-row delete, the free-form input and a copy-all button.
func (a *App) drawNotesTab(r sdl.Rect) {
	// The whole panel follows the user's font pick, or nothing does.
	defer a.ctx.popPanelFont(a.ctx.pushPanelFont(a.elemChromeFont(elemNotes, a.logPct)))
	c := a.ctx
	if a.notebook == nil {
		c.Label(r.X+4, r.Y+4, "Notebook loading...", ColTextDim)
		return
	}

	const inputH = 26
	list := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - inputH - 6}
	// Lines() copies, so take a snapshot only when the notebook changed.
	if rev := a.notebook.Rev(); a.noteCache == nil || rev != a.noteCacheRev {
		a.noteCache, a.noteCacheRev = a.notebook.Lines(), rev
	}
	lines := a.noteCache
	// Resolved through the per-element table so the user's Settings override reaches it.
	font := a.elemFont(elemNotes, a.logPct)
	lineH := int32(font.Height()) + 4
	wrapW := list.W - scrollBarW - scrollBarGap - 18 // room for the ✕ hit zone
	contentH := int32(len(lines)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	if !c.ctrlHeld {
		a.noteScroll -= c.WheelIn(list) * scrollStepPx
	}
	if maxScroll := contentH - list.H; maxScroll > 0 && a.noteScroll >= maxScroll-lineH {
		a.noteScroll = maxScroll
	}
	a.noteScroll = c.VScrollbar("notescroll", track, a.noteScroll, contentH, list.H)

	if len(lines) == 0 {
		c.Label(list.X+4, list.Y+4, "No pins yet — right-click an IC log line,", ColTextDim)
		c.Label(list.X+4, list.Y+4+lineH, "Pin evidence, or type a note below.", ColTextDim)
	}
	clipPrev, clipHad := c.pushClip(list) // scrollback only; restored before the input row
	y := list.Y - a.noteScroll
	removeIdx := -1
	for i, line := range lines {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			row := sdl.Rect{X: list.X, Y: y, W: list.W - scrollBarW, H: lineH}
			if c.hovering(row) {
				c.Fill(row, ColPanelHi)
				// ✕ on the hovered row only — zero chrome at rest.
				x := sdl.Rect{X: row.X + row.W - 16, Y: y + 1, W: 14, H: lineH - 2}
				c.Label(x.X+3, y+2, "✕", ColDanger)
				if c.hovering(x) && c.clicked {
					removeIdx = i
				}
			}
			c.LabelClippedFont(a.elemFontFor(elemNotes, a.logPct, line), list.X+2, y+2, wrapW, line, ColText)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)
	if removeIdx >= 0 {
		a.notebook.Remove(removeIdx)
	}

	iy := r.Y + r.H - inputH
	const copyW = 52
	var add bool
	a.noteInput, add = c.TextField("noteadd", sdl.Rect{X: r.X, Y: iy, W: r.W - copyW - 6, H: inputH}, a.noteInput, "Add a note... (Enter)")
	if add && strings.TrimSpace(a.noteInput) != "" {
		a.notebook.Add(a.noteInput)
		a.noteInput = ""
	}
	if c.Button(sdl.Rect{X: r.X + r.W - copyW, Y: iy, W: copyW, H: inputH}, "Copy") {
		_ = sdl.SetClipboardText(strings.Join(lines, "\r\n"))
	}
}
