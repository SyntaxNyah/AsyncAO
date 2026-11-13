package ui

import (
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// SFX Browser: opt-in modal that expands the IC-bar SFX dropdown. A streaming client can't list sounds/general/, so the
// set is starred favourites, this character's char.ini sounds, and a free-text name. Picking writes the same
// sfxChoiceIdx override as the dropdown.

const (
	sfxBrowserRowH = int32(28)
	sfxBrowserW    = int32(480)
	sfxBrowserH    = int32(460)
)

// toggleSfxBrowser opens or closes the browser; opening clears the stale query.
func (a *App) toggleSfxBrowser() {
	a.showSfxBrowser = !a.showSfxBrowser
	if a.showSfxBrowser {
		a.sfxBrowserQuery = ""
		a.sfxBrowserScroll = 0
	}
}

// selectSFXOverride points the IC-bar override at a sound name via sfxChoiceIdx: select it if listed, else append it.
// ensureSFXChoices drops the appended entry on a character switch, which suits a transient pick.
func (a *App) selectSFXOverride(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for i := 1; i < len(a.sfxChoices); i++ { // idx 0 is "SFX: auto" — never the override
		if strings.EqualFold(a.sfxChoices[i], name) {
			a.sfxChoiceIdx = i
			return
		}
	}
	a.sfxChoices = append(a.sfxChoices, name)
	a.sfxChoiceIdx = len(a.sfxChoices) - 1
}

// previewSFX plays a sound through the normal SFX channel; needs a connected origin (the lobby has no host).
func (a *App) previewSFX(name string) {
	name = strings.TrimSpace(name)
	if name == "" || a.urls.Origin() == "" {
		return
	}
	a.d.Audio.PlaySFX(a.urls.SFX(name), 0)
}

// sfxRowRightClickPreview auditions the dropdown row under the pointer without selecting it. It consumes the click so
// it doesn't reach the widget under the open list; row 0 ("SFX: auto", no name to play) is refused after that consume,
// because the consume belongs to every in-range row.
func (a *App) sfxRowRightClickPreview() {
	row, ok := a.ctx.DropdownRowRightClicked("sfxdd")
	if !ok || row <= 0 || row >= len(a.sfxChoices) {
		return
	}
	a.previewSFX(a.sfxChoices[row]) // same origin guard: silent in the lobby
}

// sfxBrowserRow is one displayed sound: its name and whether it's currently starred.
type sfxBrowserRow struct {
	name string
	fav  bool
}

// sfxBrowserRows builds the list: starred favourites first, then this character's other emote sounds, filtered by the
// case-insensitive query. Allocates; runs only while the modal is open.
func (a *App) sfxBrowserRows() []sfxBrowserRow {
	q := strings.ToLower(strings.TrimSpace(a.sfxBrowserQuery))
	match := func(name string) bool { return q == "" || strings.Contains(strings.ToLower(name), q) }

	favs := a.d.Prefs.Load().SfxFavoritesList() // lowercased bare names
	seen := make(map[string]bool, len(favs)+len(a.sfxChoices))
	rows := make([]sfxBrowserRow, 0, len(favs)+len(a.sfxChoices))
	for _, f := range favs {
		seen[f] = true
		if match(f) {
			rows = append(rows, sfxBrowserRow{name: f, fav: true})
		}
	}
	a.ensureSFXChoices()                     // make sure this character's sounds are populated
	for i := 1; i < len(a.sfxChoices); i++ { // skip idx 0 ("SFX: auto")
		name := a.sfxChoices[i]
		if seen[strings.ToLower(name)] { // already shown as a favourite
			continue
		}
		if match(name) {
			rows = append(rows, sfxBrowserRow{name: name, fav: false})
		}
	}
	return rows
}

// drawSfxBrowser paints the modal: a search / free-text field, a "use the typed name" row, and a scrollable list.
func (a *App) drawSfxBrowser(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 200}) // backdrop dim
	if c.escPressed {
		a.showSfxBrowser = false
		return
	}
	bw, bh := sfxBrowserW, sfxBrowserH
	panel := sdl.Rect{X: (w - bw) / 2, Y: (h - bh) / 2, W: bw, H: bh}
	// A click on the dimmed backdrop closes; a click inside is handled by the widgets below.
	if c.clicked && !pointIn(c.mouseX, c.mouseY, panel) {
		a.showSfxBrowser = false
		return
	}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	x := panel.X + 16
	innerW := bw - 32

	c.Heading(x, panel.Y+12, "SFX Browser", ColText)
	if c.Button(sdl.Rect{X: panel.X + bw - 16 - 64, Y: panel.Y + 12, W: 64, H: btnH}, "Close") {
		a.showSfxBrowser = false
		return
	}

	y := panel.Y + 46
	a.sfxBrowserQuery, _ = c.TextField("sfxquery", sdl.Rect{X: x, Y: y, W: innerW, H: fieldH}, a.sfxBrowserQuery, "search sounds — or type any sound name…")
	y += fieldH + 6

	// Free-text row: Use / preview / star the exact typed name; hidden when the query is already an exact list entry.
	typed := strings.TrimSpace(a.sfxBrowserQuery)
	if typed != "" && !a.sfxBrowserHasExact(typed) {
		rowR := sdl.Rect{X: x, Y: y, W: innerW, H: sfxBrowserRowH}
		a.drawSfxRow(rowR, typed, a.d.Prefs.Load().IsSfxFavorite(typed), "Use “"+typed+"”")
		y += sfxBrowserRowH + 4
	}

	c.Label(x, y, "Favourites ★ and this character's sounds:", ColTextDim)
	y += 20

	listR := sdl.Rect{X: x, Y: y, W: innerW, H: panel.Y + bh - 16 - y}
	rows := a.sfxBrowserRows()
	if len(rows) == 0 {
		c.LabelClipped(listR.X+4, listR.Y+6, listR.W-8, "No matches. Type a sound name above to use it directly.", ColTextDim)
		return
	}
	c.Border(listR, ColPanelHi)
	if !c.ctrlHeld {
		a.sfxBrowserScroll -= c.WheelIn(listR) * scrollStepPx
	}
	contentH := int32(len(rows)) * sfxBrowserRowH
	track := sdl.Rect{X: listR.X + listR.W - scrollBarW, Y: listR.Y, W: scrollBarW, H: listR.H}
	a.sfxBrowserScroll = c.VScrollbar("sfxbrowserlist", track, a.sfxBrowserScroll, contentH, listR.H)
	clipPrev, clipHad := c.pushClip(listR)
	defer c.popClip(clipPrev, clipHad)
	rowW := listR.W - scrollBarW - 4
	rowY := listR.Y - a.sfxBrowserScroll
	for i := range rows {
		if rowY > listR.Y+listR.H {
			break
		}
		if rowY >= listR.Y-sfxBrowserRowH {
			a.drawSfxRow(sdl.Rect{X: listR.X, Y: rowY, W: rowW, H: sfxBrowserRowH}, rows[i].name, rows[i].fav, "")
		}
		rowY += sfxBrowserRowH
	}
}

// sfxBrowserHasExact reports whether a name is already an exact (case-insensitive) list entry, so the free-text row
// doesn't duplicate it.
func (a *App) sfxBrowserHasExact(name string) bool {
	for _, r := range a.sfxBrowserRows() {
		if strings.EqualFold(r.name, name) {
			return true
		}
	}
	return false
}

// drawSfxRow paints one row: a star toggle, a preview button, and the name, whose click Uses the sound and closes the
// browser. The buttons consume their click so the body Use doesn't also fire. useLabel replaces the name.
func (a *App) drawSfxRow(r sdl.Rect, name string, fav bool, useLabel string) {
	c := a.ctx
	if c.hovering(r) {
		c.Fill(r, ColPanelHi)
	}
	consumed := false

	star := sdl.Rect{X: r.X + 4, Y: r.Y + (r.H-20)/2, W: 26, H: 20}
	starTxt := "☆" // the glyph alone conveys starred (★) vs not (☆)
	if fav {
		starTxt = "★"
	}
	if c.Button(star, starTxt) {
		a.d.Prefs.ToggleSfxFavorite(name)
		consumed = true
	}

	play := sdl.Rect{X: star.X + star.W + 4, Y: r.Y + (r.H-20)/2, W: 30, H: 20}
	if c.Button(play, "▶") {
		a.previewSFX(name)
		consumed = true
	}

	labelX := play.X + play.W + 8
	label := name
	if useLabel != "" {
		label = useLabel
	}
	c.LabelClipped(labelX, r.Y+(r.H-14)/2, r.X+r.W-labelX-4, label, ColText)
	// Right-click auditions without using, via previewSFX; consumed so it can't reach the backdrop's close-on-click.
	if c.hovering(r) && c.rightClicked {
		c.rightClicked = false
		a.previewSFX(name)
		return
	}
	if !consumed && c.hovering(r) && c.clicked {
		a.selectSFXOverride(name)
		a.warnLine = clampLine("SFX for your next message: " + name)
		a.warnAt = a.now()
		a.showSfxBrowser = false
	}
}
