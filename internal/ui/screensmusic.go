package ui

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// musicFilterKey memoizes the music filter so a list of thousands isn't re-lowered every frame. Keyed by the query plus
// a cheap list identity (len, first and last track), which changes when the server resends SM.
type musicFilterKey struct {
	q           string
	n           int
	first, last string
}

// refreshMusicFilter recomputes the matching track indices when the query or list identity changes. It matches the raw
// entry: MusicDisplayName only slices, so the display is a substring, and a directory ("songs/") stays findable.
func (a *App) refreshMusicFilter(query string) {
	n := len(a.sess.Music)
	key := musicFilterKey{q: query, n: n}
	if n > 0 {
		key.first, key.last = a.sess.Music[0], a.sess.Music[n-1]
	}
	if key == a.musicFilterMemo {
		return
	}
	a.musicFilterMemo = key
	a.musicFiltered = a.musicFiltered[:0]
	for i, track := range a.sess.Music {
		if strings.Contains(strings.ToLower(track), query) {
			a.musicFiltered = append(a.musicFiltered, i)
		}
	}
}

// musicRow is one row of the Music tab: a category header (a "==Category==" entry with no audio extension, see
// courtroom.HasAudioExt) or a track, by original index into a.sess.Music.
type musicRow struct {
	ti     int
	header bool
}

// musicGroupKey memoizes the grouped row layout like musicFilterKey, plus musicCollapseGen so a fold invalidates it
// without re-keying on the whole map.
type musicGroupKey struct {
	q           string
	n           int
	first, last string
	collapseGen uint64
}

// refreshMusicGroups rebuilds the Music tab's visible rows when its key changes. A search bypasses collapse state:
// matches always show, with their header for context, and manual state resumes when the query clears.
func (a *App) refreshMusicGroups(query string) {
	n := len(a.sess.Music)
	key := musicGroupKey{q: query, n: n, collapseGen: a.musicCollapseGen}
	if n > 0 {
		key.first, key.last = a.sess.Music[0], a.sess.Music[n-1]
	}
	if key == a.musicGroupMemo && a.musicRows != nil {
		return
	}
	a.musicGroupMemo = key
	a.musicRows = a.musicRows[:0]

	if query == "" {
		collapsed := false
		for i, entry := range a.sess.Music {
			if !courtroom.HasAudioExt(entry) {
				collapsed = a.musicCollapsed[i]
				a.musicRows = append(a.musicRows, musicRow{ti: i, header: true})
				continue
			}
			if collapsed {
				continue
			}
			a.musicRows = append(a.musicRows, musicRow{ti: i})
		}
		return
	}

	pendingHeader, headerShown := -1, false
	for i, entry := range a.sess.Music {
		if !courtroom.HasAudioExt(entry) {
			pendingHeader = i
			headerShown = strings.Contains(strings.ToLower(entry), query)
			if headerShown {
				a.musicRows = append(a.musicRows, musicRow{ti: i, header: true})
			}
			continue
		}
		if !strings.Contains(strings.ToLower(entry), query) {
			continue
		}
		if pendingHeader >= 0 && !headerShown {
			a.musicRows = append(a.musicRows, musicRow{ti: pendingHeader, header: true})
			headerShown = true
		}
		a.musicRows = append(a.musicRows, musicRow{ti: i})
	}
}

// setAllMusicCollapsed sets every header's fold state and bumps musicCollapseGen once, not once per category.
func (a *App) setAllMusicCollapsed(collapsed bool) {
	if !collapsed {
		if len(a.musicCollapsed) == 0 {
			return // already all-expanded (the default, empty-map state) — no-op
		}
		a.musicCollapsed = nil
		a.musicCollapseGen++
		return
	}
	if a.musicCollapsed == nil {
		a.musicCollapsed = make(map[int]bool)
	}
	for i, entry := range a.sess.Music {
		if !courtroom.HasAudioExt(entry) {
			a.musicCollapsed[i] = true
		}
	}
	a.musicCollapseGen++
}

// areaFilterKey memoizes the areas filter like musicFilterKey. Not keyed on AreaInfo: it changes on every area packet
// and the name filter doesn't depend on it.
type areaFilterKey struct {
	q           string
	n           int
	first, last string
}

// refreshAreaFilter returns matching original indices into a.sess.Areas, which parallels a.sess.AreaInfo; memoized like
// refreshMusicFilter.
// slot names the query source (areaQuerySlotOwn/Themed), not the drawer: panels reading one box share an entry, and two
// boxes can't evict each other.
func (a *App) refreshAreaFilter(query string, slot int) []int {
	n := len(a.sess.Areas)
	key := areaFilterKey{q: query, n: n}
	if n > 0 {
		key.first, key.last = a.sess.Areas[0], a.sess.Areas[n-1]
	}
	if key == a.areaFilterMemo[slot] {
		return a.areaFiltered[slot]
	}
	a.areaFilterMemo[slot] = key
	out := a.areaFiltered[slot][:0]
	for i, area := range a.sess.Areas {
		if strings.Contains(strings.ToLower(area), query) {
			out = append(out, i)
		}
	}
	a.areaFiltered[slot] = out
	return out
}

// drawMusicVolume is the Music tab's volume view, in place of the track list: Master, the three channels and blip rate.
func (a *App) drawMusicVolume(r sdl.Rect) {
	c := a.ctx
	c.LabelClipped(r.X, r.Y, r.W, "Volume — the IC box stays live below, so adjust and keep chatting.", ColTextDim)
	y := r.Y + 30
	// Drive the effective (per-server when connected) volumes, like the strip and Settings; a per-server profile
	// overrides the global prefs, so sliders on those would do nothing.
	master, music, sfx, blip := a.effectiveVolumes()
	row := func(id, label string, val int) int {
		c.Label(r.X, y+6, label, ColText)
		track := sdl.Rect{X: r.X + 80, Y: y + 7, W: r.W - 80 - 56, H: 16}
		nv := int(c.Slider("musicvol:"+id, track, int32(val), 100))
		c.Label(r.X+r.W-48, y+6, strconv.Itoa(nv)+"%", ColTextDim)
		y += 40
		return nv
	}
	if nv := row("master", "Master", master); nv != master {
		a.setEffectiveVolumes(nv, music, sfx, blip)
	}
	if nv := row("music", "Music", music); nv != music {
		a.setEffectiveVolumes(master, nv, sfx, blip)
	}
	if nv := row("sfx", "SFX", sfx); nv != sfx {
		a.setEffectiveVolumes(master, music, nv, blip)
	}
	if nv := row("blip", "Blip", blip); nv != blip {
		a.setEffectiveVolumes(master, music, sfx, nv)
	}
	// Blip rate, the same control as the volume strip's "Rate" column and Settings → Blips; left is faster.
	rate, onSpaces := a.d.Prefs.Load().BlipTyping()
	c.Label(r.X, y+6, "Blip rate", ColText)
	rtrack := sdl.Rect{X: r.X + 80, Y: y + 7, W: r.W - 80 - 56, H: 16}
	nr := value.Clamp(c.Slider("musicvol:rate", rtrack, int32(rate), int32(config.MaxBlipRate)), int32(config.MinBlipRate), int32(config.MaxBlipRate))
	c.Tooltip(rtrack, "Blip cadence: 1 blip every N letters (left = faster)")
	c.Label(r.X+r.W-48, y+6, "1/"+strconv.Itoa(int(nr)), ColTextDim)
	if int(nr) != rate {
		a.d.Prefs.SetBlipTyping(int(nr), onSpaces)
		a.applyTimingToRoom()
	}
}

// themed means this panel draws inside a theme's canvas, whose music_slider / sfx_slider / blip_slider rects win over
// the Volume toggle.
func (a *App) drawMusicList(r sdl.Rect, themed, searchExternal, nowPlayingExternal bool) {
	c := a.ctx
	if a.musicPct < config.MinLogScalePercent { // uninit / stale → match the log
		a.musicPct = a.logPct
	}
	// After the zoom is settled and before the volume-mode return, so the sliders are dressed too.
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemMusicList, a.musicPct)))
	// Here as well as in the classic log panel, so it works on the themed courtroom; in classic the panel already took
	// the wheel, so no double step.
	a.zoomWheel(r, &a.musicPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	// volMode is forced false, not just its control hidden: musicVolMode persists, so a user who left it on and applied
	// a theme would open to a volume view with no way out.
	volMode := a.musicVolMode && !themed
	// nowPlayingExternal means the theme's music_display plate already painted the name (courtroom.cpp:171), so only
	// the readout is dropped, not the row.
	now := ""
	if a.room != nil {
		now = a.room.Scene.MusicTrack
	}
	nowLabel := musicNothingPlaying
	if now != "" {
		// The short name, matching the list rows and the IC "has played a song" line (AO2 list_music).
		nowLabel = a.musicNowLabel.text(now)
	}
	// The search row is dropped whole when the theme declared music_search or the volume view replaced the track list.
	hideSearchRow := searchExternal || volMode
	total := len(a.sess.Music)
	// The plan needs the count's width before the field draws, but the field returns this frame's text, so measure with
	// last frame's outcome and evaluate the live query after the draw. Otherwise results trail typing by a frame.
	prevShown := total
	if strings.TrimSpace(a.musicSearch) != "" {
		prevShown = len(a.musicFiltered)
	}
	plan := planMusicHeader(r, nowLabel, a.musicCount.text(prevShown, total), hideSearchRow, nowPlayingExternal, c.TextWidth)

	if br, ok := plan.rect(musicItemStop); ok {
		if a.musicIconButton(br, iconStop, "Stop the current song (also the M hotkey)") {
			a.stopMusic()
		}
	}
	if br, ok := plan.rect(musicItemRandom); ok {
		if a.musicIconButton(br, iconDice, "Play a random song from the current playlist") {
			a.playRandomMusic()
		}
	}
	if br, ok := plan.rect(musicItemExpand); ok {
		if a.musicIconButton(br, iconExpandAll, "Expand all categories") {
			a.setAllMusicCollapsed(false)
		}
	}
	if br, ok := plan.rect(musicItemCollapse); ok {
		if a.musicIconButton(br, iconCollapseAll, "Collapse all categories") {
			a.setAllMusicCollapsed(true)
		}
	}
	if br, ok := plan.rect(musicItemMenu); ok {
		if a.musicIconButton(br, iconKebab, "More: playback options, volume sliders (right-click the list opens this too)") {
			// FromButton: the press must be consumed or drawMusicMenu's click-away closes the menu the frame it opened.
			a.openMusicMenuFromButton(sdl.Point{X: br.X, Y: br.Y + br.H}, themed)
		}
	}
	// "music_name" is AO2's id for this line (courtroom.cpp:1201). elemLabelFont keeps the chrome face when the theme
	// says nothing; real themes set music_name_bold = 1.
	if br, ok := plan.rect(musicItemNowPlaying); ok {
		nameFont := a.elemLabelFont(elemMusicName, DefaultScalePct, nowLabel)
		// "music_name_color"; themed is the canvas flag here.
		nameInk := ColTextDim
		if now != "" {
			nameInk = a.elemInkOr(elemMusicName, themed, ColAccent)
		}
		ty := br.Y + (br.H-fontLineH(nameFont))/2
		c.LabelClippedFontWeight(nameFont, br.X, ty, br.W, nowLabel, nameInk, a.elemBold(elemMusicName))
	}
	// searchExternal means the theme drew the field in its own rect, outside music_list (courtroom.cpp:219-222); the
	// plan then skips this row.
	if br, ok := plan.rect(musicItemSearch); ok {
		a.musicSearch, _ = c.TextField("musicsearch", sdl.Rect{X: br.X, Y: br.Y, W: br.W, H: fieldH}, a.musicSearch, "Search music…  (Ctrl+wheel resizes)")
	}
	// After the field, so the filter, count and rows all use the text typed this frame.
	query := strings.ToLower(strings.TrimSpace(a.musicSearch))
	shown := total
	if query != "" {
		a.refreshMusicFilter(query)
		shown = len(a.musicFiltered)
	}
	if br, ok := plan.rect(musicItemCount); ok {
		c.Label(br.X, br.Y+plStripLabelOffY, a.musicCount.text(shown, total), ColTextDim)
	}
	r.Y += plan.h
	r.H -= plan.h
	if volMode { // pure volume menu in place of the track list
		a.drawMusicVolume(r)
		return
	}

	if !c.ctrlHeld { // ctrl+wheel resizes text (musicPct), never scrolls
		a.musicScroll -= c.WheelIn(r) * scrollStepPx
	}
	// The theme's "music_list" size and family, folded with the music zoom.
	font := a.elemFont(elemMusicList, a.musicPct)
	musicBold := a.elemBold(elemMusicList) // music_list_bold → the row's real bold cut
	// "music_list_color" is the track rows' ink. Category headers keep ColAccent: the [-]/[+] marker is AsyncAO's, not
	// one of AO2's tree items.
	trackInk := a.elemInkOr(elemMusicList, themed, ColText)
	lineH := int32(font.Height()) + 10
	a.refreshMusicGroups(query) // grouped + (search: auto-expanded) row layout — cached, see screens.go
	rows := a.musicRows
	contentH := int32(len(rows)) * lineH
	bar := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.musicScroll = c.VScrollbar("musicscroll", bar, a.musicScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r) // partial top/bottom row stays inside the panel
	defer c.popClip(clipPrev, clipHad)
	y := r.Y - a.musicScroll
	for _, mr := range rows {
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-lineH && mr.ti >= 0 && mr.ti < len(a.sess.Music) {
			entry := a.sess.Music[mr.ti]
			row := sdl.Rect{X: r.X, Y: y, W: r.W - scrollBarW - scrollBarGap, H: lineH - 4}
			hover := c.hovering(row)
			if mr.header {
				c.Fill(row, ColPanelHi) // section header, like drawJukeGroupHeader (music_history.go)
				// ASCII fold marker: LogFont's primary face isn't guaranteed to cover Geometric Shapes.
				glyph := "[-] " // expanded
				if a.musicCollapsed[mr.ti] {
					glyph = "[+] " // collapsed
				}
				header := glyph + musicCategoryLabel(entry)
				c.LabelClippedFontWeight(font, r.X+4, y+4, row.W-8, header, ColAccent, musicBold)
				if c.ClickedIn(row) { // toggle just this category, not a track request
					if a.musicCollapsed == nil {
						a.musicCollapsed = make(map[int]bool)
					}
					if a.musicCollapsed[mr.ti] {
						delete(a.musicCollapsed, mr.ti)
					} else {
						a.musicCollapsed[mr.ti] = true
					}
					a.musicCollapseGen++
				}
			} else {
				if hover {
					c.Fill(row, ColPanelHi)
				}
				// AO2's display name (courtroom.MusicDisplayName mirrors Courtroom::list_music). The click sends the
				// raw entry: the MC packet and URL are built from it.
				display := courtroom.MusicDisplayName(entry)
				c.LabelClippedFontWeight(font, r.X+20, y+4, row.W-24, display, trackInk, musicBold) // indented under its category, tree-style
				if hover {
					c.Tooltip(row, entry) // the RAW entry on hover — the full path is still one hover away
				}
				if c.ClickedIn(row) { // press+release in-row: a scrollbar-drag release must not play a track
					a.requestMusicTrack(entry) // wire name byte-exact, plus AO2's showname/flags tail
				}
			}
		}
		y += lineH
	}
	if len(rows) == 0 {
		hint := "Server sent no music list."
		if query != "" {
			hint = "No tracks match your search."
		}
		c.Label(r.X+4, r.Y+6, hint, ColTextDim)
	}
	// Right-click opens the same menu as the ⋮ button; AO2's entry point is on_music_list_context_menu_requested
	// (courtroom.cpp:5821).
	if c.rightClicked && c.hovering(r) {
		a.openMusicMenu(sdl.Point{X: c.mouseX, Y: c.mouseY}, themed)
		c.rightClicked = false
	}
}

// musicCategoryLabel strips a category header's decorative "==" wrapper for display, if the server used it.
func musicCategoryLabel(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, "==") && strings.HasSuffix(trimmed, "==") && len(trimmed) > 4 {
		return strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	}
	return name
}
