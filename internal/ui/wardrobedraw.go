package ui

// Painting and input for the Wardrobe window, in the approved mockup's look; the state and actions are in
// wardrobewin.go. Rounded shapes and line icons come from shapes.go.

import (
	"errors"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ui/wardrobe"
)

// Sizes, in logical pixels, taken from the mockup.
const (
	wardBannerH int32 = 46
	// wardTrackH is a pill group's inset track around its segments.
	wardTrackH     int32 = wardSegH + 3*wardOutline
	wardDrawerW    int32 = 200
	wardDrawerMinW int32 = 160
	// wardDrawerShare is the window share the drawer takes before it reaches its full width: a fifth.
	wardDrawerShare int32 = 5
	// wardNameMinW is the narrowest the bottom bar's name block gets before actions move into More.
	wardNameMinW int32 = 90
	// wardNickMinW is the narrowest the nickname field gets.
	wardNickMinW   int32 = 100
	wardRowH       int32 = 34
	wardRowGap     int32 = 2
	wardToolbarH   int32 = 50
	wardBarH       int32 = 52
	wardCardW      int32 = 84
	wardCardGap    int32 = 12
	wardCardTextH  int32 = 18
	wardListRowH   int32 = 56
	wardListIcon   int32 = 40
	wardGroupHeadH int32 = 34
	wardIconPx     int32 = 16
	wardToolBtn    int32 = 24
	wardSegH       int32 = 26
	wardSegPad     int32 = 10
	wardToggleW    int32 = 30
	wardBtnH       int32 = 30
	wardFieldH     int32 = 30
	wardCloseW     int32 = 70
	wardSearchW    int32 = 280
	wardSearchMinW int32 = 140
	wardSortW      int32 = 170
	wardSwatch     int32 = 22
	wardChoice     int32 = 30
	wardCheckPx    int32 = 18
	wardCheckIcon  int32 = 10
	wardDotPx      int32 = 8
	wardPanelPad   int32 = 8
	wardFolderColW int32 = 160
	wardWornColW   int32 = 96
	wardMenuRowH   int32 = 28
	wardMenuW      int32 = 220
	wardAddW       int32 = 580
	wardAddH       int32 = 560
	wardAddTextH   int32 = 96
	wardAddRowH    int32 = 38
	wardNickFieldW int32 = 220
	wardTagPx      int32 = 12
	wardTagIcon    int32 = 8
	wardOutline    int32 = 2
	wardGhostOffX  int32 = 12
	wardGhostOffY  int32 = 8
	// Corner radii.
	wardRadPanel int32 = 8
	wardRadCard  int32 = 8
	wardRadRow   int32 = 6
	wardRadBadge int32 = 4
	wardRadSeg   int32 = 5
	wardRadDlg   int32 = 10
	// wardSelectedTint / wardSelectedEdge mix a folder's colour into its open row's fill and outline, in percent.
	wardSelectedTint = 16
	wardSelectedEdge = 55
	// wardSelectedMix mixes the highlight colour into a selected card's fill, in percent.
	wardSelectedMix = 22
	// wardMadeHereTint mixes the check disc's green into the drawer, in percent.
	wardMadeHereTint = 16
	// wardDimAlpha covers a card while it is dragged.
	wardDimAlpha uint8 = 150
	// Seconds in the last-worn steps.
	secondsPerMinute = 60
	secondsPerHour   = 60 * secondsPerMinute
	secondsPerDay    = 24 * secondsPerHour
	secondsPerWeek   = 7 * secondsPerDay
	wardPercent      = 100
)

// The mockup's palette.
var (
	wcPanel      = sdl.Color{R: 36, G: 36, B: 42, A: 255}
	wcDrawer     = sdl.Color{R: 31, G: 31, B: 36, A: 255}
	wcCard       = sdl.Color{R: 43, G: 43, B: 50, A: 255}
	wcHover      = sdl.Color{R: 46, G: 46, B: 53, A: 255}
	wcInset      = sdl.Color{R: 24, G: 24, B: 28, A: 255}
	wcLine       = sdl.Color{R: 52, G: 52, B: 60, A: 255}
	wcLineHi     = sdl.Color{R: 68, G: 68, B: 77, A: 255}
	wcButton     = sdl.Color{R: 52, G: 52, B: 60, A: 255}
	wcText       = sdl.Color{R: 235, G: 235, B: 235, A: 255}
	wcSoft       = sdl.Color{R: 208, G: 208, B: 213, A: 255}
	wcDim        = sdl.Color{R: 160, G: 160, B: 166, A: 255}
	wcFaint      = sdl.Color{R: 102, G: 102, B: 108, A: 255}
	wcAccentInk  = sdl.Color{R: 16, G: 20, B: 28, A: 255}
	wcDanger     = sdl.Color{R: 235, G: 80, B: 80, A: 255}
	wcDangerSoft = sdl.Color{R: 240, G: 140, B: 140, A: 255}
	wcDangerLine = sdl.Color{R: 90, G: 52, B: 52, A: 255}
	wcDangerBg   = sdl.Color{R: 42, G: 34, B: 38, A: 255}
	wcGreen      = sdl.Color{R: 111, G: 217, B: 138, A: 255}
	wcYellow     = sdl.Color{R: 230, G: 195, B: 92, A: 255}
	wcYellowInk  = sdl.Color{R: 28, G: 24, B: 16, A: 255}
	wcOverlay    = sdl.Color{R: 12, G: 12, B: 15, A: 184}
)

// mixColor blends pct percent of top over base, opaque.
func mixColor(base, top sdl.Color, pct int) sdl.Color {
	m := func(b, t uint8) uint8 { return uint8((int(b)*(wardPercent-pct) + int(t)*pct) / wardPercent) }
	return sdl.Color{R: m(base.R, top.R), G: m(base.G, top.G), B: m(base.B, top.B), A: base.A}
}

// wardrobeColor is a folder colour choice.
type wardrobeColor struct {
	id, name string
	col      sdl.Color
}

// wardrobeColors are the folder colours, in swatch order; the first is the default.
var wardrobeColors = [...]wardrobeColor{
	{"blue", "Blue", sdl.Color{R: 120, G: 170, B: 255, A: 255}},
	{"green", "Green", sdl.Color{R: 111, G: 217, B: 138, A: 255}},
	{"yellow", "Yellow", sdl.Color{R: 230, G: 195, B: 92, A: 255}},
	{"orange", "Orange", sdl.Color{R: 240, G: 160, B: 96, A: 255}},
	{"red", "Red", sdl.Color{R: 240, G: 140, B: 140, A: 255}},
	{"pink", "Pink", sdl.Color{R: 240, G: 140, B: 200, A: 255}},
	{"purple", "Purple", sdl.Color{R: 180, G: 156, B: 255, A: 255}},
	{"grey", "Grey", sdl.Color{R: 160, G: 160, B: 166, A: 255}},
}

// wardrobeIcons are the built-in folder icons, in picker order; the first is the default.
var wardrobeIcons = [...]struct {
	id, name string
	shape    int
}{
	{"folder", "Folder", iconShapeFolder}, {"star", "Star", iconShapeStar}, {"shield", "Shield", iconShapeShield},
	{"scales", "Scales", iconShapeScales}, {"person", "Person", iconShapePerson}, {"heart", "Heart", iconShapeHeart},
}

// wardrobeReservedNames are the drawer's built-in rows; a folder can't take their names.
var wardrobeReservedNames = [...]string{"All favourites", "Unfiled", "Server list"}

func wardrobeColorOf(id string) sdl.Color {
	for _, c := range wardrobeColors {
		if c.id == id {
			return c.col
		}
	}
	return wardrobeColors[0].col
}

func wardrobeIconShape(id string) int {
	for _, ic := range wardrobeIcons {
		if ic.id == id {
			return ic.shape
		}
	}
	return iconShapeFolder
}

// wardrobeNameError is why name can't name a folder other than exceptID, or "" when it can.
func (a *App) wardrobeNameError(name, exceptID string) string {
	for _, r := range wardrobeReservedNames {
		if strings.EqualFold(strings.TrimSpace(name), r) {
			return "That name belongs to a built-in list."
		}
	}
	switch err := a.ward.store.CheckFolderName(name, exceptID); {
	case errors.Is(err, config.ErrWardrobeNameEmpty):
		return "Give the folder a name."
	case errors.Is(err, config.ErrWardrobeNameTaken):
		return "You already have a folder with that name."
	}
	return ""
}

// Button looks.
const (
	wbNormal = iota
	wbPrimary
	wbGhost
	wbDanger
	wbDangerSolid
	wbDisabled
)

// wardButton draws a rounded button and reports a click; a disabled one never reports.
func (a *App) wardButton(r sdl.Rect, label string, look int) bool {
	c := a.ctx
	hover := c.hovering(r) && look != wbDisabled
	fill, edge, ink := wcButton, wcLineHi, wcText
	switch look {
	case wbPrimary:
		fill, edge, ink = a.ward.accent, a.ward.accent, wcAccentInk
	case wbGhost:
		fill, edge, ink = wcPanel, wcLineHi, wcSoft
	case wbDanger:
		fill, edge, ink = wcPanel, wcDangerLine, wcDangerSoft
	case wbDangerSolid:
		fill, edge, ink = wcDanger, wcDanger, wcText
	case wbDisabled:
		fill, edge, ink = wcButton, wcButton, wcFaint
	}
	if hover {
		fill = mixColor(fill, wcText, wardHoverMix)
	}
	c.RoundRect(r, wardRadRow, 1, fill, edge)
	if label != "" {
		c.LabelClipped(r.X+(r.W-min(c.TextWidth(label), r.W))/2, r.Y+(r.H-a.wardLineH())/2, r.W, label, ink)
	}
	return look != wbDisabled && c.ClickedIn(r)
}

// wardLineH is the line height of the face the Wardrobe draws with: its own panel font when one is set.
func (a *App) wardLineH() int32 {
	if f := a.ctx.chromeFace(); f != nil {
		return int32(f.Height())
	}
	return a.fontLineH()
}

// wardHoverMix lightens a hovered button, in percent.
const wardHoverMix = 8

// wardButtonW is a label's button width.
func (a *App) wardButtonW(label string) int32 { return a.ctx.TextWidth(label) + 2*wardSegPad }

// wardSegment draws one segment of a pill group and reports a click; a disabled one never reports.
func (a *App) wardSegment(r sdl.Rect, label string, on, enabled bool) bool {
	c := a.ctx
	ink := wcSoft
	switch {
	case !enabled:
		ink = wcFaint
	case on:
		c.FillRound(r, wardRadSeg, a.ward.accent)
		ink = wcAccentInk
	case c.hovering(r):
		c.FillRound(r, wardRadSeg, wcLine)
	}
	if label != "" {
		c.Label(r.X+(r.W-c.TextWidth(label))/2, r.Y+(r.H-a.wardLineH())/2, label, ink)
	}
	return enabled && c.ClickedIn(r)
}

// wardPillGroup draws the inset track a run of segments sits in; inner is the segments' area.
func (a *App) wardPillGroup(outer sdl.Rect) {
	a.ctx.RoundRect(outer, wardRadPanel, 1, wcInset, wcLine)
}

// wardField is a rounded text field: the kit's field without its frame, inside a box that turns blue while focused.
func (a *App) wardField(id string, r sdl.Rect, value, placeholder string) (string, bool) {
	c := a.ctx
	edge := wcLineHi
	if c.focusID == id {
		edge = a.ward.accent
	}
	c.RoundRect(r, wardRadRow, 1, wcInset, edge)
	return c.TextFieldBare(id, sdl.Rect{X: r.X + wardRowGap, Y: r.Y + (r.H-fieldH)/2, W: r.W - 2*wardRowGap, H: fieldH}, value, placeholder)
}

// drawWardrobePanel is the courtroom modal's draw.
func (a *App) drawWardrobePanel(w, h int32) {
	c := a.ctx
	ward := &a.ward
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemWardrobe, DefaultScalePct)))
	ward.iconPx = int32(a.d.Prefs.Load().WardrobeIconPx())
	rgb := a.d.Prefs.Load().WardrobeAccentRGB()
	ward.accent = sdl.Color{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 255}
	ward.selected = mixColor(wcPanel, ward.accent, wardSelectedMix)
	a.refreshWardrobeView()
	a.updateWardrobeDrag()
	chromeH := a.topChromeH()
	panel := sdl.Rect{X: pad * 3, Y: chromeH + pad*3, W: w - pad*6, H: h - chromeH - pad*6}
	c.RoundRect(panel, wardRadPanel, 1, wcPanel, a.ward.accent)
	if ward.store == nil {
		c.Heading(panel.X+pad, panel.Y+pad, "Wardrobe", wcText)
		c.Label(panel.X+pad, panel.Y+wardBannerH, "Loading your Wardrobe…", wcDim)
		if a.wardButton(sdl.Rect{X: panel.X + panel.W - wardCloseW - pad, Y: panel.Y + pad, W: wardCloseW, H: wardBtnH}, "Close", wbNormal) {
			a.closeWardrobe()
		}
		return
	}
	// A dialog or menu on top takes the pointer from everything under it.
	overlay := ward.addOpen || ward.moveMenu || ward.moreMenu
	if overlay {
		c.fencePointer()
	}
	if a.drawWardrobeBanner(panel) {
		c.unfencePointer()
		return
	}
	body := sdl.Rect{X: panel.X + 1, Y: panel.Y + wardBannerH, W: panel.W - 2, H: panel.H - wardBannerH - 1}
	c.Fill(sdl.Rect{X: body.X, Y: body.Y, W: body.W, H: 1}, wcLine)
	drawerW := min(max(body.W/wardDrawerShare, wardDrawerMinW), wardDrawerW) // narrower on small windows
	a.drawWardrobeDrawer(sdl.Rect{X: body.X, Y: body.Y + 1, W: drawerW, H: body.H - 1})
	content := sdl.Rect{X: body.X + drawerW, Y: body.Y + 1, W: body.W - drawerW, H: body.H - 1}
	a.drawWardrobeContent(content)
	if overlay {
		c.unfencePointer()
		switch {
		case ward.addOpen:
			a.drawWardrobeAdd(panel)
		case ward.moreMenu:
			a.drawWardrobeMoreMenu()
		default:
			a.drawWardrobeMoveMenu(content)
		}
	}
	a.finishWardrobeDrag()
	if ward.dragging && ward.dragID != "" {
		label := ward.dragID
		if ward.dragKind == wardDragFolder {
			label = a.wardrobeFolderName(ward.dragID)
		}
		g := sdl.Rect{X: c.mouseX + wardGhostOffX, Y: c.mouseY + wardGhostOffY, W: c.TextWidth(label) + 2*wardPanelPad + wardPanelPad, H: wardMenuRowH}
		c.RoundRect(g, wardRadRow, 1, wcInset, a.ward.accent)
		c.Label(g.X+wardPanelPad, g.Y+(g.H-a.wardLineH())/2, label, a.ward.accent)
	}
}

// drawWardrobeBanner draws the title row and reports whether Close was pressed.
func (a *App) drawWardrobeBanner(panel sdl.Rect) bool {
	c := a.ctx
	ward := &a.ward
	right := panel.X + panel.W - pad
	midY := panel.Y + wardBannerH/2
	closeR := sdl.Rect{X: right - wardCloseW, Y: midY - wardBtnH/2, W: wardCloseW, H: wardBtnH}
	if a.wardButton(closeR, "Close", wbNormal) {
		a.closeWardrobe()
		return true
	}
	// Grid / List.
	trackH := wardTrackH
	togTrack := sdl.Rect{X: closeR.X - pad - 2*wardToggleW - 3*wardOutline, Y: midY - trackH/2, W: 2*wardToggleW + 3*wardOutline, H: trackH}
	a.wardPillGroup(togTrack)
	gridR := sdl.Rect{X: togTrack.X + wardOutline, Y: midY - wardSegH/2, W: wardToggleW, H: wardSegH}
	listR := sdl.Rect{X: gridR.X + wardToggleW + wardOutline, Y: gridR.Y, W: wardToggleW, H: wardSegH}
	if a.wardIconSegment(gridR, iconShapeGrid, !ward.listView) {
		ward.listView = false
	}
	c.Tooltip(gridR, "Grid view")
	if a.wardIconSegment(listR, iconShapeList, ward.listView) {
		ward.listView = true
	}
	c.Tooltip(listR, "List view")
	// User / Server.
	serverOK := len(ward.serverList) > 0
	usrW, srvW := a.wardButtonW("User"), a.wardButtonW("Server")
	srcTrack := sdl.Rect{X: togTrack.X - pad - usrW - srvW - 3*wardOutline, Y: togTrack.Y, W: usrW + srvW + 3*wardOutline, H: trackH}
	a.wardPillGroup(srcTrack)
	usrR := sdl.Rect{X: srcTrack.X + wardOutline, Y: gridR.Y, W: usrW, H: wardSegH}
	srvR := sdl.Rect{X: usrR.X + usrW + wardOutline, Y: gridR.Y, W: srvW, H: wardSegH}
	if a.wardSegment(usrR, "User", ward.source == wardrobe.SourceUser, true) {
		a.setWardrobeSource(wardrobe.SourceUser)
	}
	if a.wardSegment(srvR, "Server", ward.source == wardrobe.SourceServer, serverOK) {
		a.setWardrobeSource(wardrobe.SourceServer)
	}
	switch {
	case ward.serverBusy:
		c.Tooltip(srvR, "Loading this server's iniswaps.ini…")
	case !serverOK:
		c.Tooltip(srvR, "This server publishes no iniswaps.ini")
	default:
		c.Tooltip(srvR, "The characters this server lists in its iniswaps.ini")
	}
	name := a.serverName
	if name == "" {
		name = "this server"
	}
	// One line: the dim label, then the server's name.
	x := panel.X + pad + wardPanelPad
	textY := midY - a.wardLineH()/2
	const lead = "THE WARDROBE FOR"
	c.Label(x, textY, lead, wcDim)
	x += c.TextWidth(lead) + wardPanelPad
	c.LabelClippedFontWeight(c.chromeFace(), x, textY, srcTrack.X-x-pad, name, wcText, true)
	return false
}

// wardIconSegment is a segment showing an icon instead of a label.
func (a *App) wardIconSegment(r sdl.Rect, icon int, on bool) bool {
	c := a.ctx
	hit := a.wardSegment(r, "", on, true)
	ink := wcDim
	if on {
		ink = wcText
		c.FillRound(r, wardRadSeg, wcLine)
	}
	c.Icon(icon, sdl.Rect{X: r.X + (r.W-wardIconPx)/2, Y: r.Y + (r.H-wardIconPx)/2, W: wardIconPx, H: wardIconPx}, ink)
	return hit
}

func (a *App) setWardrobeSource(s wardrobe.Source) {
	ward := &a.ward
	if ward.source == s {
		return
	}
	ward.source, ward.sel, ward.scroll = s, "", 0
	ward.confirmRemove, ward.nickOpen, ward.moveMenu = false, false, false
	if s == wardrobe.SourceServer && (ward.sort == wardrobe.SortCustom || ward.sort == wardrobe.SortFolder) {
		ward.sort = wardrobe.SortName
	}
}

// drawWardrobeFolderIcon paints a folder's image, else its icon in its colour.
func (a *App) drawWardrobeFolderIcon(f config.WardrobeFolder, r sdl.Rect) {
	c := a.ctx
	if page, ok := a.wardrobeImagePage(f.Image); ok {
		c.cgoRect = r
		_ = c.Ren.Copy(page.Frames[0], nil, &c.cgoRect)
		return
	}
	c.Icon(wardrobeIconShape(f.Icon), r, wardrobeColorOf(f.Color))
}

// wardrobeDrawerRow is one drawer row: a folder, or a built-in list.
type wardrobeDrawerRow struct {
	folder   config.WardrobeFolder
	builtin  bool
	dropOK   bool // a dragged character can be filed here
	count    int
	countS   string
	madeHere bool
}

func (a *App) wardrobeDrawerRows() []wardrobeDrawerRow {
	ward := &a.ward
	if ward.source == wardrobe.SourceServer {
		return []wardrobeDrawerRow{{folder: config.WardrobeFolder{ID: wardrobe.AllFolders, Name: "Server list", Icon: "folder", Color: "grey"}, builtin: true, count: len(ward.serverList), countS: strconv.Itoa(len(ward.serverList))}}
	}
	counts := map[string]int{}
	for _, e := range ward.entries {
		if len(e.Folders) == 0 {
			counts[wardrobe.Unfiled]++
		}
		for _, f := range e.Folders {
			counts[f]++
		}
	}
	rows := make([]wardrobeDrawerRow, 0, len(ward.folders)+2)
	rows = append(rows, wardrobeDrawerRow{folder: config.WardrobeFolder{ID: wardrobe.AllFolders, Name: "All favourites", Icon: "star", Color: "yellow"}, builtin: true, count: len(ward.entries)})
	for _, f := range ward.folders {
		rows = append(rows, wardrobeDrawerRow{folder: f, dropOK: true, count: counts[f.ID], madeHere: f.Server != "" && f.Server == a.serverKey})
	}
	rows = append(rows, wardrobeDrawerRow{folder: config.WardrobeFolder{ID: wardrobe.Unfiled, Name: "Unfiled", Icon: "folder", Color: "grey"}, builtin: true, dropOK: true, count: counts[wardrobe.Unfiled]})
	for i := range rows {
		rows[i].countS = strconv.Itoa(rows[i].count)
	}
	return rows
}

// drawWardrobeDrawer is the folder list on the left.
func (a *App) drawWardrobeDrawer(r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	c.Fill(r, wcDrawer)
	c.Fill(sdl.Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: r.H}, wcLine)
	head := "MY FOLDERS"
	if ward.source == wardrobe.SourceServer {
		head = "SERVER"
	}
	c.Label(r.X+2*wardPanelPad, r.Y+2*wardPanelPad, head, wcDim)
	footH := wardBtnH + 3*wardPanelPad
	if ward.creating {
		footH = wardFieldH + a.wardLineH() + 4*wardPanelPad + wardBtnH
	}
	if ward.source == wardrobe.SourceServer {
		footH = 3 * a.wardLineH()
	}
	listTop := r.Y + 2*wardPanelPad + a.wardLineH() + wardPanelPad
	listR := sdl.Rect{X: r.X, Y: listTop, W: r.W - 1, H: r.Y + r.H - footH - listTop}
	ward.drawerScroll = max(ward.drawerScroll-c.WheelIn(listR)*scrollStepPx, 0)
	prev, had := c.pushClip(listR)
	y := listR.Y - ward.drawerScroll
	for _, row := range ward.rows {
		y = a.drawWardrobeDrawerRow(row, sdl.Rect{X: r.X + wardPanelPad, Y: y, W: r.W - 2*wardPanelPad - 1, H: wardRowH}) + wardRowGap
	}
	c.popClip(prev, had)
	if over := y + ward.drawerScroll - listR.Y - listR.H; ward.drawerScroll > max(over, 0) {
		ward.drawerScroll = max(over, 0)
	}
	c.Fill(sdl.Rect{X: r.X, Y: r.Y + r.H - footH, W: r.W - 1, H: 1}, wcLine)
	foot := sdl.Rect{X: r.X + wardPanelPad + wardPanelPad/2, Y: r.Y + r.H - footH + wardPanelPad + wardPanelPad/2, W: r.W - 3*wardPanelPad - 1, H: footH - 2*wardPanelPad}
	if ward.source == wardrobe.SourceServer {
		c.LabelClipped(foot.X, foot.Y, foot.W, "Published by the server in its", wcDim)
		c.LabelClipped(foot.X, foot.Y+a.wardLineH(), foot.W, "iniswaps.ini.", wcDim)
		return
	}
	a.drawWardrobeNewFolder(foot)
}

// drawWardrobeDrawerRow draws one row and any editor open under it, returning the y below them.
func (a *App) drawWardrobeDrawerRow(row wardrobeDrawerRow, r sdl.Rect) int32 {
	c := a.ctx
	ward := &a.ward
	f := row.folder
	if ward.renaming == f.ID && !row.builtin {
		return a.drawWardrobeRename(f, r)
	}
	col := wardrobeColorOf(f.Color)
	active := ward.source == wardrobe.SourceServer || ward.folder == f.ID
	hover := c.hovering(r)
	canDrop := (ward.dragKind == wardDragChar && row.dropOK) || (ward.dragKind == wardDragFolder && !row.builtin && ward.dragID != f.ID)
	dropping := canDrop && a.offerWardrobeDrop(r, wardDragFolder, f.ID)
	switch {
	case dropping:
		c.RoundRect(r, wardRadRow, wardOutline, mixColor(wcDrawer, col, wardSelectedTint), col)
	case active:
		c.RoundRect(r, wardRadRow, 1, mixColor(wcDrawer, col, wardSelectedTint), mixColor(wcDrawer, col, wardSelectedEdge))
	case hover && !(ward.dragging && ward.dragKind == wardDragFolder && ward.dragID == f.ID):
		c.FillRound(r, wardRadRow, wcHover)
	}
	if ward.dragging && ward.dragKind == wardDragFolder && ward.dragID == f.ID {
		c.FillRound(r, wardRadRow, sdl.Color{R: wcDrawer.R, G: wcDrawer.G, B: wcDrawer.B, A: wardDimAlpha})
	}
	if !row.builtin && hover && c.dropped != "" {
		a.importWardrobeImage(f.ID, c.dropped)
		c.dropped = ""
	}
	icon := sdl.Rect{X: r.X + wardPanelPad + wardOutline, Y: r.Y + (r.H-wardIconPx)/2, W: wardIconPx, H: wardIconPx}
	a.drawWardrobeFolderIcon(f, icon)
	textX := icon.X + icon.W + wardPanelPad + wardOutline
	right := r.X + r.W - wardPanelPad - wardOutline
	tools := !row.builtin && hover && !ward.dragging
	if tools {
		right -= 2*wardToolBtn + wardPanelPad
	} else {
		right -= c.TextWidth(row.countS)
		c.Label(right, r.Y+(r.H-a.wardLineH())/2, row.countS, wcDim)
		right -= wardPanelPad
	}
	if row.madeHere {
		right -= wardCheckPx
		chk := sdl.Rect{X: right, Y: r.Y + (r.H-wardCheckPx)/2, W: wardCheckPx, H: wardCheckPx}
		c.FillRound(chk, wardCheckPx/2, mixColor(wcDrawer, wcGreen, wardMadeHereTint))
		c.Icon(iconShapeCheck, sdl.Rect{X: chk.X + (chk.W-wardCheckIcon)/2, Y: chk.Y + (chk.H-wardCheckIcon)/2, W: wardCheckIcon, H: wardCheckIcon}, wcGreen)
		c.Tooltip(chk, "Made on this server: its characters are known to load here")
		right -= wardPanelPad / 2
	}
	ink := wcSoft
	if active {
		ink = wcText
	}
	c.LabelClipped(textX, r.Y+(r.H-a.wardLineH())/2, right-textX, f.Name, ink)
	if tools && a.drawWardrobeRowTools(f, r) {
		return r.Y + r.H
	}
	if hover && !ward.dragging {
		switch {
		case !row.builtin && (c.dblClick || (c.focusID == "" && c.keyPressed == sdl.K_F2)):
			ward.renaming, ward.renameText, ward.lookFor, ward.deleting = f.ID, f.Name, "", ""
			c.FocusField("wardrename")
		case c.ClickedIn(r) && ward.source == wardrobe.SourceUser:
			ward.folder, ward.sel, ward.scroll = f.ID, "", 0
			ward.confirmRemove, ward.nickOpen = false, false
		}
		if !row.builtin && c.mouseDown && !c.prevMouseDown {
			a.startWardrobeDrag(wardDragFolder, f.ID)
		}
		if !row.builtin {
			c.Tooltip(r, "Double-click or F2 to rename · drag to reorder · drop an image here to use it as the icon")
		}
	}
	y := r.Y + r.H
	switch {
	case row.builtin:
	case ward.deleting == f.ID:
		y = a.drawWardrobeDeleteConfirm(f, row.count, sdl.Rect{X: r.X, Y: y + wardRowGap*2, W: r.W})
	case ward.lookFor == f.ID:
		y = a.drawWardrobeLookPanel(f, sdl.Rect{X: r.X, Y: y + wardRowGap*2, W: r.W})
	}
	return y
}

// drawWardrobeRowTools draws the edit and delete buttons on a hovered folder and reports a click on one.
func (a *App) drawWardrobeRowTools(f config.WardrobeFolder, r sdl.Rect) bool {
	c := a.ctx
	ward := &a.ward
	del := sdl.Rect{X: r.X + r.W - wardPanelPad/2 - wardToolBtn, Y: r.Y + (r.H-wardToolBtn)/2, W: wardToolBtn, H: wardToolBtn}
	edit := sdl.Rect{X: del.X - wardToolBtn - wardRowGap*2, Y: del.Y, W: wardToolBtn, H: wardToolBtn}
	iconIn := func(b sdl.Rect) sdl.Rect {
		return sdl.Rect{X: b.X + (b.W-wardCheckIcon-wardOutline)/2, Y: b.Y + (b.H-wardCheckIcon-wardOutline)/2, W: wardCheckIcon + wardOutline, H: wardCheckIcon + wardOutline}
	}
	editFill, delFill := wcButton, wcDangerBg
	if c.hovering(edit) {
		editFill = wcLineHi
	}
	if c.hovering(del) {
		delFill = wcDangerLine
	}
	c.RoundRect(edit, wardRadSeg, 1, editFill, wcLineHi)
	c.Icon(iconShapePencil, iconIn(edit), wcSoft)
	c.Tooltip(edit, "Name, icon and colour")
	c.RoundRect(del, wardRadSeg, 1, delFill, wcDangerLine)
	c.Icon(iconShapeCross, iconIn(del), wcDangerSoft)
	c.Tooltip(del, "Delete folder")
	switch {
	case c.ClickedIn(edit):
		if ward.lookFor == f.ID {
			ward.lookFor = ""
		} else {
			ward.lookFor, ward.lookName, ward.deleting, ward.renaming = f.ID, f.Name, "", ""
		}
		return true
	case c.ClickedIn(del):
		ward.deleting, ward.lookFor, ward.renaming = f.ID, "", ""
		return true
	}
	return false
}

// wardNameField is a folder-name field with its error line, Save/Create and Cancel; it reports ok or cancel.
func (a *App) wardNameField(id string, r sdl.Rect, text *string, exceptID, okLabel string) (bottom int32, ok, cancel bool) {
	c := a.ctx
	var enter bool
	*text, enter = a.wardField(id, sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: wardFieldH}, *text, "Folder name")
	msg := a.wardrobeNameError(*text, exceptID)
	y := r.Y + wardFieldH + wardRowGap*2
	if msg != "" && strings.TrimSpace(*text) != "" {
		c.LabelClipped(r.X, y, r.W, msg, wcDangerSoft)
		y += a.wardLineH() + wardRowGap
	}
	half := (r.W - wardRowGap*2) / 2
	look := wbPrimary
	if msg != "" {
		look = wbDisabled
	}
	okHit := a.wardButton(sdl.Rect{X: r.X, Y: y, W: half, H: wardBtnH - wardPanelPad/2}, okLabel, look)
	cancelHit := a.wardButton(sdl.Rect{X: r.X + half + wardRowGap*2, Y: y, W: half, H: wardBtnH - wardPanelPad/2}, "Cancel", wbGhost)
	return y + wardBtnH, (okHit || enter) && msg == "", cancelHit || (c.escPressed && c.focusID == id)
}

// drawWardrobeRename replaces a row with its name field while renaming.
func (a *App) drawWardrobeRename(f config.WardrobeFolder, r sdl.Rect) int32 {
	ward := &a.ward
	box := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: wardFieldH + wardBtnH + 3*wardPanelPad}
	a.ctx.RoundRect(box, wardRadRow, 1, wcCard, wcLineHi)
	bottom, ok, cancel := a.wardNameField("wardrename", sdl.Rect{X: r.X + wardPanelPad, Y: r.Y + wardPanelPad, W: r.W - 2*wardPanelPad}, &ward.renameText, f.ID, "Save")
	switch {
	case ok:
		_ = ward.store.RenameFolder(f.ID, ward.renameText)
		ward.renaming = ""
	case cancel:
		ward.renaming = ""
	}
	return max(bottom, box.Y+box.H) + wardPanelPad/2
}

// drawWardrobeDeleteConfirm asks how to delete a folder, under its row.
func (a *App) drawWardrobeDeleteConfirm(f config.WardrobeFolder, count int, r sdl.Rect) int32 {
	c := a.ctx
	ward := &a.ward
	btnH := wardBtnH - wardPanelPad/2
	h := a.wardLineH() + 3*btnH + 5*wardPanelPad
	box := sdl.Rect{X: r.X + wardRowGap, Y: r.Y, W: r.W - 2*wardRowGap, H: h}
	c.RoundRect(box, wardRadRow, 1, wcDangerBg, wcDangerLine)
	in := sdl.Rect{X: box.X + wardPanelPad + wardRowGap, Y: box.Y + wardPanelPad + wardRowGap, W: box.W - 2*wardPanelPad - 2*wardRowGap}
	c.LabelClipped(in.X, in.Y, in.W, "Delete "+f.Name+"?", wcText)
	y := in.Y + a.wardLineH() + wardPanelPad
	withLabel := "Delete folder"
	if count > 0 {
		withLabel = "Delete with its " + strconv.Itoa(count) + " characters"
	}
	if a.wardButton(sdl.Rect{X: in.X, Y: y, W: in.W, H: btnH}, withLabel, wbDangerSolid) {
		ward.store.DeleteFolder(f.ID, false)
		ward.deleting = ""
	}
	y += btnH + wardPanelPad/2 + 1
	if a.wardButton(sdl.Rect{X: in.X, Y: y, W: in.W, H: btnH}, "Keep characters, move to Unfiled", wbNormal) {
		ward.store.DeleteFolder(f.ID, true)
		ward.deleting = ""
	}
	y += btnH + wardPanelPad/2 + 1
	if a.wardButton(sdl.Rect{X: in.X, Y: y, W: in.W, H: btnH}, "Cancel", wbGhost) {
		ward.deleting = ""
	}
	return box.Y + box.H + wardPanelPad
}

// drawWardrobeLookPanel edits a folder's name, icon and colour, under its row.
func (a *App) drawWardrobeLookPanel(f config.WardrobeFolder, r sdl.Rect) int32 {
	c := a.ctx
	ward := &a.ward
	lh := a.wardLineH()
	btnH := wardBtnH - wardPanelPad/2
	h := 3*lh + wardFieldH + wardChoice + wardSwatch + 2*btnH + 9*wardPanelPad
	box := sdl.Rect{X: r.X + wardRowGap, Y: r.Y, W: r.W - 2*wardRowGap, H: h}
	c.RoundRect(box, wardRadRow, 1, wcCard, wcLineHi)
	in := sdl.Rect{X: box.X + wardPanelPad + wardRowGap, Y: box.Y + wardPanelPad + wardRowGap, W: box.W - 2*wardPanelPad - 2*wardRowGap}
	y := in.Y
	c.Label(in.X, y, "NAME", wcDim)
	y += lh + wardRowGap
	var enter bool
	ward.lookName, enter = a.wardField("wardlookname", sdl.Rect{X: in.X, Y: y, W: in.W, H: wardFieldH}, ward.lookName, "Folder name")
	msg := a.wardrobeNameError(ward.lookName, f.ID)
	y += wardFieldH + wardPanelPad
	if msg != "" {
		c.LabelClipped(in.X, y-wardPanelPad/2, in.W, msg, wcDangerSoft)
		y += lh
	}
	c.Label(in.X, y, "ICON", wcDim)
	y += lh + wardRowGap
	x := in.X
	fcol := wardrobeColorOf(f.Color)
	for _, ic := range wardrobeIcons {
		b := sdl.Rect{X: x, Y: y, W: wardChoice, H: wardChoice}
		on := f.Image == "" && (f.Icon == ic.id || (f.Icon == "" && ic.id == wardrobeIcons[0].id))
		switch {
		case on:
			c.RoundRect(b, wardRadSeg, wardOutline, mixColor(wcCard, fcol, wardSelectedTint), fcol)
		case c.hovering(b):
			c.RoundRect(b, wardRadSeg, 1, wcHover, wcLineHi)
		default:
			c.RoundRect(b, wardRadSeg, 1, wcDrawer, wcLineHi)
		}
		ink := wcSoft
		if on {
			ink = fcol
		}
		c.Icon(ic.shape, sdl.Rect{X: b.X + (b.W-wardIconPx)/2, Y: b.Y + (b.H-wardIconPx)/2, W: wardIconPx, H: wardIconPx}, ink)
		if c.ClickedIn(b) {
			ward.store.SetFolderLook(f.ID, ic.id, f.Color)
		}
		c.Tooltip(b, ic.name)
		x += wardChoice + wardRowGap*2
	}
	y += wardChoice + wardPanelPad/2
	imgLook := wbNormal
	if f.Image != "" {
		imgLook = wbPrimary
	}
	imgR := sdl.Rect{X: in.X, Y: y, W: in.W, H: btnH}
	if a.wardButton(imgR, "Image…", imgLook) {
		a.browseWardrobeImage(f.ID)
	}
	c.Tooltip(imgR, "Use your own PNG, WebP or GIF, or drop one onto the folder")
	y += btnH + wardPanelPad
	c.Label(in.X, y, "COLOUR", wcDim)
	y += lh + wardRowGap
	x = in.X
	n := int32(len(wardrobeColors))
	gap := wardPanelPad - wardOutline
	swatch := min(wardSwatch, (in.W-(n-1)*gap-2*wardOutline)/n)
	for _, sw := range wardrobeColors {
		b := sdl.Rect{X: x + wardOutline, Y: y + wardOutline, W: swatch, H: swatch}
		if f.Color == sw.id || (f.Color == "" && sw.id == wardrobeColors[0].id) {
			c.FillRound(sdl.Rect{X: b.X - wardOutline, Y: b.Y - wardOutline, W: b.W + 2*wardOutline, H: b.H + 2*wardOutline}, wardSwatch, wcText)
		}
		c.FillRound(b, wardSwatch/2, sw.col)
		if c.ClickedIn(b) {
			ward.store.SetFolderColor(f.ID, sw.id)
		}
		c.Tooltip(b, sw.name)
		x += swatch + gap
	}
	y += wardSwatch + wardPanelPad
	look := wbNormal
	if msg != "" {
		look = wbDisabled
	}
	if (a.wardButton(sdl.Rect{X: in.X, Y: y, W: in.W, H: btnH}, "Done", look) || enter) && msg == "" {
		_ = ward.store.RenameFolder(f.ID, ward.lookName)
		ward.lookFor = ""
	}
	return box.Y + box.H + wardPanelPad
}

// drawWardrobeNewFolder is the drawer's footer: the New folder button, or its name field while creating.
func (a *App) drawWardrobeNewFolder(r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	if !ward.creating {
		b := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: wardBtnH}
		fill := wcDrawer
		if c.hovering(b) {
			fill = wcHover
		}
		c.RoundRect(b, wardRadRow, 1, fill, wcLineHi)
		label := "New folder"
		lw := c.TextWidth(label)
		ix := b.X + (b.W-lw-wardIconPx-wardPanelPad)/2
		c.Icon(iconShapePlus, sdl.Rect{X: ix, Y: b.Y + (b.H-wardCheckIcon-wardOutline*2)/2, W: wardCheckIcon + wardOutline*2, H: wardCheckIcon + wardOutline*2}, wcText)
		c.Label(ix+wardIconPx+wardPanelPad/2, b.Y+(b.H-a.wardLineH())/2, label, wcText)
		if c.ClickedIn(b) {
			ward.creating, ward.newName = true, ""
			ward.renaming, ward.lookFor, ward.deleting = "", "", ""
			c.FocusField("wardnewfolder")
		}
		return
	}
	_, ok, cancel := a.wardNameField("wardnewfolder", r, &ward.newName, "", "Create")
	switch {
	case ok:
		color := wardrobeColors[(len(ward.folders)+1)%len(wardrobeColors)].id
		if id, err := ward.store.AddFolder(ward.newName, a.serverKey, wardrobeIcons[0].id, color); err == nil {
			ward.folder, ward.sel, ward.scroll = id, "", 0
		}
		ward.creating = false
	case cancel:
		ward.creating = false
	}
}

// drawWardrobeContent is the toolbar, the grid or list, and the bottom bar.
func (a *App) drawWardrobeContent(r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	a.drawWardrobeToolbar(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: wardToolbarH})
	listR := sdl.Rect{X: r.X + 2*wardPanelPad, Y: r.Y + wardToolbarH, W: r.W - 4*wardPanelPad + wardPanelPad, H: r.H - wardToolbarH - wardBarH - wardPanelPad}
	a.drawWardrobeItems(listR)
	a.drawWardrobeBar(sdl.Rect{X: r.X, Y: r.Y + r.H - wardBarH, W: r.W, H: wardBarH})
	// Enter on a selection with no field focused does what a double-click does.
	if ward.sel != "" && c.focusID == "" && c.keyPressed == sdl.K_RETURN && !ward.addOpen {
		if _, ok := a.wardrobeSelected(); ok {
			a.wardrobeUse(ward.sel)
		}
	}
}

// wardrobeSorts are the Sort dropdown's entries, in order.
var wardrobeSorts = [...]wardrobe.Sort{wardrobe.SortCustom, wardrobe.SortName, wardrobe.SortLastWorn, wardrobe.SortFolder}

// wardrobeSortLabels label wardrobeSorts; the server list offers only the middle two.
var (
	wardrobeSortLabels       = []string{"Sort: Custom", "Sort: Name", "Sort: Last worn", "Sort: Folder"}
	wardrobeServerSortLabels = []string{"Sort: Name", "Sort: Last worn"}
)

func (a *App) drawWardrobeToolbar(r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	y := r.Y + (r.H-wardBtnH)/2
	label := "Add"
	if ward.source == wardrobe.SourceServer {
		label = "Import…"
	}
	addW := c.TextWidth(label) + 2*wardSegPad + wardIconPx
	// Right to left: the sort buttons (a dropdown when they don't fit), then the Add button and count; the search
	// takes what's left.
	sortR := a.drawWardrobeSort(r, r.X+2*wardPanelPad+wardSearchMinW+addW+3*wardPanelPad)
	countW := c.TextWidth(ward.countLabel)
	x := r.X + 2*wardPanelPad
	searchW := min(wardSearchW, sortR.X-x-3*wardPanelPad-addW-countW-2*wardPanelPad)
	if searchW < wardSearchMinW {
		searchW, countW = min(wardSearchW, sortR.X-x-3*wardPanelPad-addW), 0 // too narrow: drop the count first
	}
	box := sdl.Rect{X: x, Y: y, W: searchW, H: wardBtnH}
	edge := wcLine
	if c.focusID == "wardsearch" {
		edge = a.ward.accent
	}
	c.RoundRect(box, wardRadRow, 1, wcInset, edge)
	c.Icon(iconShapeSearch, sdl.Rect{X: box.X + wardPanelPad + wardRowGap, Y: box.Y + (box.H-wardCheckIcon-wardOutline*2)/2, W: wardCheckIcon + wardOutline*2, H: wardCheckIcon + wardOutline*2}, wcDim)
	fieldX := box.X + wardPanelPad*3 + wardOutline
	ward.search, _ = c.TextFieldBare("wardsearch", sdl.Rect{X: fieldX, Y: box.Y + (box.H-fieldH)/2, W: box.X + box.W - fieldX - wardPanelPad/2, H: fieldH}, ward.search, "Search characters")
	x += searchW + wardPanelPad + wardRowGap
	addR := sdl.Rect{X: x, Y: y, W: addW, H: wardBtnH}
	fill := wcButton
	if c.hovering(addR) {
		fill = wcLineHi
	}
	c.RoundRect(addR, wardRadRow, 1, fill, wcLineHi)
	c.Icon(iconShapePlus, sdl.Rect{X: addR.X + wardSegPad - wardRowGap, Y: addR.Y + (addR.H-wardCheckIcon-wardOutline)/2, W: wardCheckIcon + wardOutline, H: wardCheckIcon + wardOutline}, wcText)
	c.LabelClippedFontWeight(c.chromeFace(), addR.X+wardSegPad+wardIconPx-wardRowGap, addR.Y+(addR.H-a.wardLineH())/2, addR.W, label, wcText, true)
	if c.ClickedIn(addR) {
		a.openWardrobeAdd(ward.source == wardrobe.SourceServer)
	}
	if countW > 0 {
		c.Label(x+addW+wardPanelPad+wardRowGap, y+(wardBtnH-a.wardLineH())/2, ward.countLabel, wcDim)
	}
}

// wardrobeSortSegs label wardrobeSorts as pill buttons.
var wardrobeSortSegs = [...]string{"Custom", "Name", "Last worn", "Folder"}

// drawWardrobeSort draws the sort control at the toolbar's right, as pill buttons when they fit right of minLeft and
// as a dropdown otherwise, and returns the space it took.
func (a *App) drawWardrobeSort(r sdl.Rect, minLeft int32) sdl.Rect {
	c := a.ctx
	ward := &a.ward
	const lead = "Sort"
	groupW := c.TextWidth(lead) + 2*wardPanelPad + int32(len(wardrobeSortSegs)+1)*wardOutline
	for _, l := range wardrobeSortSegs {
		groupW += a.wardButtonW(l)
	}
	right := r.X + r.W - 2*wardPanelPad
	if right-groupW >= minLeft {
		track := sdl.Rect{X: right - groupW, Y: r.Y + (r.H-wardTrackH)/2, W: groupW, H: wardTrackH}
		a.wardPillGroup(track)
		c.Label(track.X+wardPanelPad+wardOutline, track.Y+(track.H-a.wardLineH())/2, lead, wcDim)
		x := track.X + c.TextWidth(lead) + 2*wardPanelPad
		for i, l := range wardrobeSortSegs {
			s := wardrobeSorts[i]
			seg := sdl.Rect{X: x, Y: track.Y + (track.H-wardSegH)/2, W: a.wardButtonW(l), H: wardSegH}
			enabled := ward.source == wardrobe.SourceUser || (s != wardrobe.SortCustom && s != wardrobe.SortFolder)
			if a.wardSegment(seg, l, ward.sort == s, enabled) {
				ward.sort, ward.scroll = s, 0
			}
			if !enabled {
				c.Tooltip(seg, "The server list keeps the server's order and has no folders")
			}
			x += seg.W + wardOutline
		}
		return track
	}
	sortR := sdl.Rect{X: right - wardSortW, Y: r.Y + (r.H-btnH)/2, W: wardSortW, H: btnH}
	labels, first := wardrobeSortLabels, 0
	if ward.source == wardrobe.SourceServer {
		labels, first = wardrobeServerSortLabels, 1
	}
	cur := 0
	for i, s := range wardrobeSorts[first : first+len(labels)] {
		if s == ward.sort {
			cur = i
		}
	}
	if next, changed := c.Dropdown("wardsort", sortR, labels, cur); changed {
		ward.sort, ward.scroll = wardrobeSorts[first+next], 0
	}
	return sortR
}

// wardrobeReorderable reports whether cards can be dragged to reorder right now.
func (a *App) wardrobeReorderable() bool {
	ward := &a.ward
	return ward.source == wardrobe.SourceUser && ward.sort == wardrobe.SortCustom
}

// wardCardH is a grid card's height.
func (a *App) wardCardH() int32 {
	return a.ward.iconPx + 2*wardPanelPad + wardPanelPad/2 + 2*a.wardLineH()
}

// drawWardrobeItems lays the groups out as cards or rows inside r, scrolling.
func (a *App) drawWardrobeItems(r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	if len(ward.flat) == 0 {
		msg := "No characters match."
		switch {
		case ward.source == wardrobe.SourceUser && len(ward.entries) == 0:
			msg = "Your Wardrobe is empty. Use Add to save characters, or import them from the server."
		case ward.source == wardrobe.SourceUser && ward.search == "":
			msg = "This folder is empty. Drag characters onto it, or use Add."
		}
		mw := min(c.TextWidth(msg), r.W)
		c.LabelClipped(r.X+(r.W-mw)/2, r.Y+wardBannerH/2, r.W, msg, wcDim)
		return
	}
	trackR := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	inner := sdl.Rect{X: r.X, Y: r.Y, W: r.W - scrollBarW - scrollBarGap, H: r.H}
	cardH := a.wardCardH()
	minCardW := max(wardCardW, ward.iconPx+4*wardPanelPad)
	cols := max((inner.W+wardCardGap)/(minCardW+wardCardGap), 1)
	cardW := (inner.W - (cols-1)*wardCardGap) / cols // cards stretch to fill the row, as the mockup's grid does
	rowH := cardH + wardCardGap
	contentH := int32(0)
	for _, g := range ward.groups {
		if g.Titled {
			contentH += wardGroupHeadH
		}
		n := int32(len(g.Items))
		if ward.listView {
			contentH += n * wardListRowH
		} else {
			contentH += (n + cols - 1) / cols * rowH
		}
	}
	ward.scroll -= c.WheelIn(r) * scrollStepPx
	ward.scroll = c.VScrollbar("wardscroll", trackR, ward.scroll, contentH, r.H)
	prev, had := c.pushClip(r)
	y := r.Y + wardRowGap - ward.scroll
	idx := 0
	for gi, g := range ward.groups {
		if g.Titled {
			a.drawWardrobeGroupHead(g, ward.groupHeads[gi], sdl.Rect{X: inner.X, Y: y, W: inner.W, H: wardGroupHeadH})
			y += wardGroupHeadH
		}
		for i, it := range g.Items {
			var cell sdl.Rect
			if ward.listView {
				cell = sdl.Rect{X: inner.X, Y: y + int32(i)*wardListRowH, W: inner.W, H: wardListRowH - wardRowGap}
			} else {
				col, row := int32(i)%cols, int32(i)/cols
				cell = sdl.Rect{X: inner.X + col*(cardW+wardCardGap), Y: y + row*rowH, W: cardW, H: cardH}
			}
			if cell.Y+cell.H > r.Y && cell.Y < r.Y+r.H {
				a.drawWardrobeItem(idx, it, cell)
			}
			idx++
		}
		n := int32(len(g.Items))
		if ward.listView {
			y += n * wardListRowH
		} else {
			y += (n + cols - 1) / cols * rowH
		}
	}
	c.popClip(prev, had)
}

func (a *App) drawWardrobeGroupHead(g wardrobe.Group, title string, r sdl.Rect) {
	c := a.ctx
	col := wcDim
	if i := a.wardrobeFolderIndex(g.Folder); i >= 0 {
		col = wardrobeColorOf(a.ward.folders[i].Color)
	}
	mid := r.Y + r.H/2
	c.FillRound(sdl.Rect{X: r.X, Y: mid - wardDotPx/2, W: wardDotPx, H: wardDotPx}, wardDotPx, col)
	x := r.X + wardDotPx + wardPanelPad
	c.Label(x, mid-a.wardLineH()/2, title, wcSoft)
	x += c.TextWidth(title) + wardPanelPad
	c.Fill(sdl.Rect{X: x, Y: mid, W: r.X + r.W - x, H: 1}, wcLine)
}

// drawWardrobeItem draws one card or row; idx indexes the icon cache.
func (a *App) drawWardrobeItem(idx int, it wardrobe.Item, r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	selected := ward.sel == it.Char
	hover := c.hovering(r) && !ward.dragging
	dropping := a.wardrobeReorderable() && ward.dragKind == wardDragChar && ward.dragID != it.Char && a.offerWardrobeDrop(r, wardDragChar, it.Char)
	if dropping {
		c.FillRound(sdl.Rect{X: r.X - wardOutline*2, Y: r.Y - wardOutline*2, W: r.W + 4*wardOutline, H: r.H + 4*wardOutline}, wardRadCard+wardOutline, a.ward.accent)
		c.FillRound(sdl.Rect{X: r.X - wardOutline, Y: r.Y - wardOutline, W: r.W + 2*wardOutline, H: r.H + 2*wardOutline}, wardRadCard, wcPanel)
	}
	var icon sdl.Rect
	lh := a.wardLineH()
	if ward.listView {
		switch {
		case selected:
			c.RoundRect(r, wardRadRow, 1, a.ward.selected, a.ward.accent)
		case hover:
			c.FillRound(r, wardRadRow, wcHover)
		}
		icon = sdl.Rect{X: r.X + wardPanelPad + wardOutline, Y: r.Y + (r.H-wardListIcon)/2, W: wardListIcon, H: wardListIcon}
	} else {
		fill, edge := wcCard, wcLine
		switch {
		case selected:
			fill, edge = a.ward.selected, a.ward.accent
		case hover:
			fill = wcHover
		}
		c.RoundRect(r, wardRadCard, wardOutline, fill, edge)
		icon = sdl.Rect{X: r.X + (r.W-ward.iconPx)/2, Y: r.Y + wardPanelPad + wardOutline, W: ward.iconPx, H: ward.iconPx}
	}
	a.drawWardrobeCharIcon(idx, it, icon)
	if ward.listView {
		textX := icon.X + icon.W + wardPanelPad + wardPanelPad/2
		textW := r.X + r.W - wardFolderColW - wardWornColW - wardPanelPad*4 - textX
		textY := r.Y + (r.H-lh)/2
		if it.HasNickname {
			textY = r.Y + r.H/2 - lh + wardRowGap
			c.LabelClipped(textX, textY+lh-wardRowGap, textW, it.Char, wcDim)
		}
		c.LabelClipped(textX, textY, textW, it.Name, wcText)
		folder := ward.folderLabels[it.Char]
		if ward.source == wardrobe.SourceServer {
			folder = "Server list"
		}
		c.LabelClipped(r.X+r.W-wardWornColW-wardFolderColW-wardPanelPad*2, r.Y+(r.H-lh)/2, wardFolderColW, folder, wcDim)
		worn := wardrobeWornLabel(it.LastWorn, a.now().Unix())
		c.Label(r.X+r.W-wardPanelPad*2-c.TextWidth(worn), r.Y+(r.H-lh)/2, worn, wcDim)
	} else {
		textW := r.W - wardPanelPad
		textY := icon.Y + icon.H + wardPanelPad - wardRowGap
		nameW := min(c.TextWidth(it.Name), textW)
		c.LabelClipped(r.X+(r.W-nameW)/2, textY, textW, it.Name, wcText)
		if it.HasNickname {
			realW := min(c.TextWidth(it.Char), textW)
			c.LabelClipped(r.X+(r.W-realW)/2, textY+lh-wardRowGap, textW, it.Char, wcDim)
		}
	}
	if ward.dragging && ward.dragKind == wardDragChar && ward.dragID == it.Char {
		rad := wardRadCard
		if ward.listView {
			rad = wardRadRow
		}
		c.FillRound(r, rad, sdl.Color{R: wcPanel.R, G: wcPanel.G, B: wcPanel.B, A: wardDimAlpha})
	}
	a.wardrobeItemInput(it, r)
}

// wardrobeItemInput selects on click, uses on double-click, and arms a drag on press.
func (a *App) wardrobeItemInput(it wardrobe.Item, r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	if !c.hovering(r) {
		return
	}
	tip := it.Char
	if it.Iniswap {
		tip += " — iniswap: worn without taking a server slot"
	}
	if a.wardrobeReorderable() {
		tip += " · drag to reorder or onto a folder"
	} else if ward.source == wardrobe.SourceUser {
		tip += " · drag onto a folder to file it"
	}
	c.Tooltip(r, tip)
	if ward.dragging {
		return
	}
	if c.mouseDown && !c.prevMouseDown && ward.source == wardrobe.SourceUser {
		a.startWardrobeDrag(wardDragChar, it.Char)
	}
	switch {
	case c.dblClick:
		c.dblClick = false
		a.wardrobeUse(it.Char)
	case c.ClickedIn(r):
		if ward.sel != it.Char {
			ward.sel, ward.confirmRemove, ward.nickOpen, ward.moveMenu = it.Char, false, false, false
		}
	}
}

// drawWardrobeCharIcon draws a character's char_icon, or its initials until it loads, with the yellow tag on iniswaps.
func (a *App) drawWardrobeCharIcon(idx int, it wardrobe.Item, r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	rad := wardRadBadge
	c.FillRound(r, rad, wcInset)
	base := a.urls.CharIcon(it.Char)
	if page, ok := a.cachedPage(&ward.pages, &ward.pagesGen, len(ward.flat), idx, base); ok && len(page.Frames) > 0 {
		c.cgoRect = r
		_ = c.Ren.Copy(page.Frames[0], nil, &c.cgoRect)
	} else {
		a.demandAsset(&ward.ask, len(ward.flat), idx, base, assets.AssetTypeCharIcon) // AssetType: CharIcon (wardrobe)
		initials := it.Char
		if len(initials) > 2 {
			initials = initials[:2]
		}
		c.LabelClippedFontWeight(c.chromeFace(), r.X+(r.W-c.TextWidth(initials))/2, r.Y+(r.H-a.wardLineH())/2, r.W, initials, wcFaint, true)
	}
	if !it.Iniswap {
		return
	}
	tag := sdl.Rect{X: r.X + r.W - wardTagPx + wardOutline + 1, Y: r.Y + r.H - wardTagPx + wardOutline + 1, W: wardTagPx, H: wardTagPx}
	c.FillRound(tag, wardOutline+1, wcYellow)
	c.Icon(iconShapeSwap, sdl.Rect{X: tag.X + (tag.W-wardTagIcon)/2, Y: tag.Y + (tag.H-wardTagIcon)/2, W: wardTagIcon, H: wardTagIcon}, wcYellowInk)
}

// wardrobeWornLabel is "5 min ago" style text for a last-worn time, "Never" for none.
func wardrobeWornLabel(worn, now int64) string {
	if worn == 0 {
		return "Never"
	}
	d := max(now-worn, 0)
	switch {
	case d < secondsPerMinute:
		return "Just now"
	case d < secondsPerHour:
		return strconv.FormatInt(d/secondsPerMinute, 10) + " min ago"
	case d < secondsPerDay:
		return strconv.FormatInt(d/secondsPerHour, 10) + " h ago"
	case d < secondsPerWeek:
		return strconv.FormatInt(d/secondsPerDay, 10) + " d ago"
	}
	return strconv.FormatInt(d/secondsPerWeek, 10) + " wk ago"
}

// Bottom-bar actions, in the order they give up their place to More when the bar is too narrow (last first).
const (
	wardActSwitch = iota
	wardActWear
	wardActFolders
	wardActNickname
	wardActRemove
	wardActAddServer
	wardActCount
)

// wardBarAction is one bottom-bar button.
type wardBarAction struct {
	id    int
	label string
	look  int
}

// wardrobeBarActions lists what the selection can do, into dst, so it never allocates.
func (a *App) wardrobeBarActions(it wardrobe.Item, dst *[wardActCount]wardBarAction) []wardBarAction {
	ward := &a.ward
	out := dst[:0]
	wearLook := wbPrimary
	if a.wardrobeCanSwitch(it.Char) {
		out = append(out, wardBarAction{wardActSwitch, "Switch to character", wbPrimary})
		wearLook = wbNormal
	}
	out = append(out, wardBarAction{wardActWear, "Wear as iniswap", wearLook})
	if ward.source == wardrobe.SourceServer {
		if !it.Saved {
			out = append(out, wardBarAction{wardActAddServer, "Add to a folder…", wbNormal})
		}
		return out
	}
	folders := "Move to folder…"
	if a.wardrobeFolderChecklist() {
		folders = "Folders…"
	}
	return append(out,
		wardBarAction{wardActFolders, folders, wbNormal},
		wardBarAction{wardActNickname, "Nickname…", wbNormal},
		wardBarAction{wardActRemove, "Remove…", wbDanger})
}

// doWardrobeAction runs a bottom-bar action on the selection.
func (a *App) doWardrobeAction(id int, it wardrobe.Item) {
	ward := &a.ward
	ward.moreMenu = false
	switch id {
	case wardActSwitch:
		a.wardrobeSwitch(it.Char)
	case wardActWear:
		a.wardrobeWear(it.Char)
	case wardActFolders, wardActAddServer:
		ward.moveMenu = true
	case wardActNickname:
		ward.nickOpen, ward.nickText = true, ""
		if it.HasNickname {
			ward.nickText = it.Name
		}
		a.ctx.FocusField("wardnick")
	case wardActRemove:
		ward.confirmRemove = true
	}
}

// drawWardrobeBar is the bottom bar: what the selection can do, the nickname editor, or the remove confirmation.
// When the bar is too narrow the name block shrinks first, then actions move into a More menu from the last one back.
func (a *App) drawWardrobeBar(r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	c.Fill(r, wcDrawer)
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, wcLine)
	it, ok := a.wardrobeSelected()
	lh := a.wardLineH()
	y := r.Y + (r.H-wardBtnH)/2
	x := r.X + 2*wardPanelPad
	right := r.X + r.W - 2*wardPanelPad
	ward.moreN = 0
	if !ok {
		c.LabelClipped(x, r.Y+(r.H-lh)/2, r.W-4*wardPanelPad, "Select a character to see what you can do with it. Double-click wears it.", wcDim)
		return
	}
	switch {
	case ward.nickOpen:
		a.drawWardrobeNickname(it, r)
		return
	case ward.confirmRemove:
		keepR := sdl.Rect{X: right - wardCloseW, Y: y, W: wardCloseW, H: wardBtnH}
		removeR := sdl.Rect{X: keepR.X - wardPanelPad - wardCloseW, Y: y, W: wardCloseW, H: wardBtnH}
		c.LabelClipped(x, r.Y+(r.H-lh)/2, removeR.X-x-wardPanelPad, "Remove "+it.Name+" from your favourites? Its folders forget it too.", wcText)
		if a.wardButton(removeR, "Remove", wbDangerSolid) {
			ward.store.Remove(it.Char)
			ward.sel, ward.confirmRemove = "", false
		}
		if a.wardButton(keepR, "Keep", wbNormal) {
			ward.confirmRemove = false
		}
		return
	}
	var buf [wardActCount]wardBarAction
	acts := a.wardrobeBarActions(it, &buf)
	gap := wardPanelPad + wardRowGap
	need := int32(0)
	for _, act := range acts {
		need += a.wardButtonW(act.label) + gap
	}
	nameW := min(max(c.TextWidth(it.Name), c.TextWidth("characters/"+it.Char)), wardFolderColW)
	nameW = max(min(nameW, right-x-2*wardPanelPad-need), wardNameMinW)
	shown := len(acts)
	moreW := a.wardButtonW("More…")
	for shown > 0 && x+nameW+2*wardPanelPad+need > right {
		if shown == len(acts) {
			need += moreW + gap // More takes a place once anything overflows
		}
		shown--
		need -= a.wardButtonW(acts[shown].label) + gap
	}
	c.LabelClippedFontWeight(c.chromeFace(), x, r.Y+r.H/2-lh+wardRowGap, nameW, it.Name, wcText, true)
	c.LabelClipped(x, r.Y+r.H/2+wardRowGap, nameW, "characters/"+it.Char, wcDim)
	x += nameW + 2*wardPanelPad
	for i, act := range acts[:shown] {
		b := sdl.Rect{X: x, Y: y, W: a.wardButtonW(act.label), H: wardBtnH}
		if act.id == wardActRemove && i == len(acts)-1 {
			b.X = right - b.W // Remove sits at the far right when everything fits
		}
		x += b.W + gap
		if a.wardButton(b, act.label, act.look) {
			a.doWardrobeAction(act.id, it)
			return
		}
	}
	for _, act := range acts[shown:] {
		ward.moreIDs[ward.moreN], ward.moreLabels[ward.moreN] = act.id, act.label
		ward.moreN++
	}
	if ward.moreN > 0 {
		ward.moreAt = sdl.Rect{X: x, Y: y, W: moreW, H: wardBtnH}
		if a.wardButton(ward.moreAt, "More…", wbNormal) {
			ward.moreMenu = true
		}
	}
}

// drawWardrobeMoreMenu lists the bottom-bar actions that didn't fit, above the More button.
func (a *App) drawWardrobeMoreMenu() {
	c := a.ctx
	ward := &a.ward
	it, ok := a.wardrobeSelected()
	if !ok || ward.moreN == 0 {
		ward.moreMenu = false
		return
	}
	h := int32(ward.moreN)*wardMenuRowH + wardPanelPad
	menu := sdl.Rect{X: max(ward.moreAt.X+ward.moreAt.W-wardMenuW, 0), Y: ward.moreAt.Y - h - wardRowGap*2, W: wardMenuW, H: h}
	c.RoundRect(menu, wardRadRow, 1, wcCard, wcLineHi)
	for i := range ward.moreN {
		row := sdl.Rect{X: menu.X + wardPanelPad/2, Y: menu.Y + wardPanelPad/2 + int32(i)*wardMenuRowH, W: menu.W - wardPanelPad, H: wardMenuRowH}
		if c.hovering(row) {
			c.FillRound(row, wardRadSeg, wcLine)
		}
		ink := wcText
		if ward.moreIDs[i] == wardActRemove {
			ink = wcDangerSoft
		}
		c.LabelClipped(row.X+wardPanelPad, row.Y+(row.H-a.wardLineH())/2, row.W-2*wardPanelPad, ward.moreLabels[i], ink)
		if c.ClickedIn(row) {
			a.doWardrobeAction(ward.moreIDs[i], it)
			return
		}
	}
	if c.clicked && !c.hovering(menu) {
		ward.moreMenu = false
	}
}

// drawWardrobeNickname is the bar while editing a nickname; the real folder name is still what gets worn. The field
// and the label shrink to fit a narrow window.
func (a *App) drawWardrobeNickname(it wardrobe.Item, r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	lh := a.wardLineH()
	y := r.Y + (r.H-wardBtnH)/2
	x := r.X + 2*wardPanelPad
	right := r.X + r.W - 2*wardPanelPad
	cancelR := sdl.Rect{X: right - wardCloseW, Y: y, W: wardCloseW, H: wardBtnH}
	clearR := sdl.Rect{X: cancelR.X - wardPanelPad - wardCloseW, Y: y, W: wardCloseW, H: wardBtnH}
	saveR := sdl.Rect{X: clearR.X - wardPanelPad - wardCloseW, Y: y, W: wardCloseW, H: wardBtnH}
	if !it.HasNickname {
		saveR = clearR
	}
	label := "Nickname for " + it.Char
	if x+c.TextWidth(label)+wardNickMinW+2*wardPanelPad > saveR.X {
		label = "Nickname"
	}
	c.Label(x, r.Y+(r.H-lh)/2, label, wcSoft)
	x += c.TextWidth(label) + wardPanelPad + wardRowGap
	fieldW := min(max(saveR.X-wardPanelPad-x, wardNickMinW), wardNickFieldW)
	var enter bool
	ward.nickText, enter = a.wardField("wardnick", sdl.Rect{X: x, Y: r.Y + (r.H-wardFieldH)/2, W: fieldW, H: wardFieldH}, ward.nickText, it.Char)
	x += fieldW + wardPanelPad + wardRowGap
	if note := "Only you see it; the real name is still sent."; x+c.TextWidth(note) < saveR.X-wardPanelPad {
		c.Label(x, r.Y+(r.H-lh)/2, note, wcDim)
	}
	switch {
	case a.wardButton(saveR, "Save", wbPrimary) || enter:
		ward.store.SetNickname(it.Char, ward.nickText)
		ward.nickOpen = false
	case it.HasNickname && a.wardButton(clearR, "Clear", wbGhost):
		ward.store.SetNickname(it.Char, "")
		ward.nickOpen = false
	case a.wardButton(cancelR, "Cancel", wbNormal) || (c.escPressed && c.focusID == "wardnick"):
		ward.nickOpen = false
	}
}

// wardrobeFolderChecklist reports whether the folder menu ticks folders on and off (in All favourites and Unfiled)
// rather than moving the character out of the folder being viewed.
func (a *App) wardrobeFolderChecklist() bool {
	ward := &a.ward
	return ward.source == wardrobe.SourceUser && (ward.folder == wardrobe.AllFolders || ward.folder == wardrobe.Unfiled)
}

// wardrobeMenuRow is one row of the folder menu: a folder id (Unfiled for none) and whether it is ticked.
type wardrobeMenuRow struct {
	folder string
	name   string
	ticked bool
}

// wardrobeMenuRows are the folder menu's rows for the selected item: every folder with ticks in the checklist, or the
// move targets (Unfiled first, without the folder being viewed), or for a server item where to save it.
func (a *App) wardrobeMenuRows(it wardrobe.Item) []wardrobeMenuRow {
	ward := &a.ward
	rows := make([]wardrobeMenuRow, 0, len(ward.folders)+1)
	checklist := a.wardrobeFolderChecklist()
	if !checklist {
		rows = append(rows, wardrobeMenuRow{folder: wardrobe.Unfiled, name: "Unfiled"})
	}
	for _, f := range ward.folders {
		if !checklist && ward.source == wardrobe.SourceUser && f.ID == ward.folder {
			continue
		}
		rows = append(rows, wardrobeMenuRow{folder: f.ID, name: f.Name, ticked: checklist && wardrobe.InFolder(it.Folders, f.ID)})
	}
	return rows
}

// drawWardrobeMoveMenu lists folders above the bar: ticking folders on and off, moving a saved character, or saving
// a server one. The checklist stays open while you tick.
func (a *App) drawWardrobeMoveMenu(content sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	it, ok := a.wardrobeSelected()
	if !ok {
		ward.moveMenu = false
		return
	}
	checklist := a.wardrobeFolderChecklist()
	rows := a.wardrobeMenuRows(it)
	if len(rows) == 0 {
		rows = append(rows, wardrobeMenuRow{name: "Make a folder first"})
	}
	h := int32(len(rows))*wardMenuRowH + wardPanelPad
	menu := sdl.Rect{X: content.X + 2*wardPanelPad + wardFolderColW + 2*wardPanelPad, Y: content.Y + content.H - wardBarH - h - wardRowGap*2, W: wardMenuW, H: h}
	menu.Y = max(menu.Y, content.Y)
	c.RoundRect(menu, wardRadRow, 1, wcCard, wcLineHi)
	for i, mr := range rows {
		row := sdl.Rect{X: menu.X + wardPanelPad/2, Y: menu.Y + wardPanelPad/2 + int32(i)*wardMenuRowH, W: menu.W - wardPanelPad, H: wardMenuRowH}
		if c.hovering(row) {
			c.FillRound(row, wardRadSeg, wcLine)
		}
		x := row.X + wardPanelPad
		if checklist {
			box := sdl.Rect{X: x, Y: row.Y + (row.H-wardCheckPx)/2, W: wardCheckPx, H: wardCheckPx}
			if mr.ticked {
				c.FillRound(box, wardRadBadge, a.ward.accent)
				c.Icon(iconShapeCheck, sdl.Rect{X: box.X + (box.W-wardCheckIcon)/2, Y: box.Y + (box.H-wardCheckIcon)/2, W: wardCheckIcon, H: wardCheckIcon}, wcAccentInk)
			} else {
				c.RoundRect(box, wardRadBadge, 1, wcInset, wcLineHi)
			}
			x += wardCheckPx + wardPanelPad
		}
		c.LabelClipped(x, row.Y+(row.H-a.wardLineH())/2, row.X+row.W-x-wardPanelPad, mr.name, wcText)
		if !c.ClickedIn(row) || (mr.folder == "" && mr.name != "Unfiled") {
			continue
		}
		switch {
		case ward.source == wardrobe.SourceServer:
			ward.store.Add([]string{it.Char}, mr.folder)
		case checklist && mr.ticked:
			ward.store.RemoveFromFolder(it.Char, mr.folder)
		case checklist:
			ward.store.AddToFolder(it.Char, mr.folder)
		default:
			ward.store.MoveToFolder(it.Char, ward.folder, mr.folder)
		}
		if !checklist {
			ward.moveMenu = false
		}
		return
	}
	if c.clicked && !c.hovering(menu) {
		ward.moveMenu = false
	}
}
