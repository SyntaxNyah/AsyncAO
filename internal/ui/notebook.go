package ui

// Case notebook UI: the Notes tab in the courtroom right column. Pins
// arrive from the IC log (right-click a line), the evidence panel (Pin
// button), or the free-form input here; storage is per-server
// (config.Notebook, async I/O).

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// pinNote adds one line to the current server's notebook (silently a
// no-op pre-load — the off-thread load lands within a frame or two of
// connecting; there is nothing meaningful to pin before that).
func (a *App) pinNote(line string) {
	if a.notebook == nil {
		return
	}
	a.notebook.Add(line)
	a.pushDebug("notebook: pinned " + clampLine(line))
}

// pollNotebook lands a per-server notebook load: on the active session
// when the key matches, else on the parked tab it belongs to (a tab
// switch between request and landing must not cross notebooks).
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
		// No owner anymore (tab closed): drop.
	default:
	}
}

// drawNotesTab renders the notebook list (wrapped, newest at the bottom),
// per-row delete, the free-form input, and a copy-all button.
func (a *App) drawNotesTab(r sdl.Rect) {
	c := a.ctx
	if a.notebook == nil {
		c.Label(r.X+4, r.Y+4, "Notebook loading...", ColTextDim)
		return
	}

	const inputH = 26
	list := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - inputH - 6}
	// Snapshot cache: Lines() copies, so take it only when the notebook
	// actually changed (rev) — never per frame.
	if rev := a.notebook.Rev(); a.noteCache == nil || rev != a.noteCacheRev {
		a.noteCache, a.noteCacheRev = a.notebook.Lines(), rev
	}
	lines := a.noteCache
	font := c.LogFont(a.logPct)
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
			c.LabelClippedFont(c.LogFontFor(a.logPct, line), list.X+2, y+2, wrapW, line, ColText)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)
	if removeIdx >= 0 {
		a.notebook.Remove(removeIdx)
	}

	// Free-form note input + copy-all.
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
