package ui

// Per-element theme fonts: AO2-Client Courtroom::set_fonts / set_font (courtroom.cpp:1188 / :1212) read
// courtroom_fonts.ini and give each courtroom element its own family ("<id>_font"), point size ("<id>") and weight
// ("<id>_bold").
//
// The resolved table is a fixed-size array, so an element lookup on the render path is one indexed load, never a map
// probe or a font open. It is populated off-thread on theme apply (hard rule 2) and landed by pollThemeApply behind the
// themeAppliedGen stale-generation guard. The zero table means "this theme dresses nothing": every accessor then
// returns the client's own font.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/platform"
	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// themeFontElem indexes the per-element font table. The order must match theme.FontElements: buildThemeFontTable fills
// slot i from FontElements[i].
type themeFontElem int

const (
	elemShowname themeFontElem = iota
	elemMessage
	elemICChatlog
	elemServerChatlog
	elemMusicList
	elemMusicName
	elemAreaList
	// elemDebugLog dresses the themed debug panel at the ms_chatlog rect; AO2 gives that widget its own identifier
	// (courtroom.cpp:1198). Appended, never inserted: the index is the position in theme.FontElements.
	elemDebugLog
	// AsyncAO's own panels, which AO2 has no identifier for; appended for the same reason.
	elemPlayerList
	elemNotes
	elemFriends
	themeFontElemCount // array size — keep last
)

// themeElemFont is one resolved courtroom_fonts.ini element.
type themeElemFont struct {
	// pct is the declared point size folded onto AsyncAO's percent scale; 0 = none declared.
	pct int
	// face is 1-based into Ctx.themeFaceData; 0 = no family named or it didn't resolve, so the client's own font chain
	// is used.
	face int
	// bold mirrors "<id>_bold = 1"; drawn as SDL_ttf's STYLE_BOLD, which synthesises the weight as QFont::setBold does
	// when the family has no bold file. A second draw pass smeared at a fractional UI scale (see Ctx.textTextureBold).
	bold bool
	// color / colorSet are "<id>_color", the element's foreground ink (courtroom.cpp:1300 stamps it as `<class> {
	// color: rgba(...) }`). colorSet false = none named, so the draw site keeps the kit's colour.
	//
	// elemShowname / elemMessage are left unpopulated: their colours already ride a.themeMsgCol / a.themeNameCol behind
	// the chatbox-skin guard, and filling both would apply one colour through two readability guards.
	color    sdl.Color
	colorSet bool
	// userFont marks that the user, not the theme, gave this element a font or size. It arms the panel-wide chrome
	// scope and is narrower than dressed(): a theme that only sizes music_list must not resize the jukebox's buttons
	// and search box. AO2 stamps its stylesheet on the list widget, not the toolbar around it.
	userFont bool
}

// dressed reports whether the theme said ANYTHING about this element.
func (f themeElemFont) dressed() bool { return f.pct != 0 || f.face != 0 }

// faceIdx converts the 1-based face to a Ctx.themeFaceData index (-1 = none).
func (f themeElemFont) faceIdx() int { return f.face - 1 }

// themeFontTable is the applied theme's font data, a value type copied on apply: no locking or pointer chasing at draw
// time.
type themeFontTable struct {
	e [themeFontElemCount]themeElemFont
}

// themeFaceCap bounds the distinct theme-declared face files held at once. Real themes reuse one or two families, so 4
// (matching fontChainCap) is enough; past it an element keeps the client font (hard rule 4).
const themeFaceCap = 4

// qtLogicalDPI / qtPointsPerInch are Qt's point-to-pixel conversion: P points is P*96/72 pixels. courtroom_fonts.ini
// declares points (QFont::setPointSize, courtroom.cpp:1217) and AsyncAO's percent scale is in pixels.
const (
	qtLogicalDPI    = 96
	qtPointsPerInch = 72
)

// themeFontPx is a courtroom_fonts.ini point size in pixels, rounded as Qt rounds.
func themeFontPx(points int) int {
	return (points*qtLogicalDPI + qtPointsPerInch/2) / qtPointsPerInch
}

// themeFontPct converts a courtroom_fonts.ini point size to AsyncAO's percent scale. AO2 renders the INI sizes through
// QFont::setPointSize times a fixed option (Options::themeScalingFactor), not the window, so AsyncAO doesn't scale them
// with the window either; the per-panel Ctrl+wheel zoom folds in through elemPct.
//
// It rounds up so the round trip is exact: the face reopens at UIFontSize*pct/100 with integer division, so 11 px wants
// 91.67%, and 91% reopens at 10 px while 92% reopens at 11.
func themeFontPct(points int) int {
	px := themeFontPx(points)
	return (px*DefaultScalePct + UIFontSize - 1) / UIFontSize
}

// themeFontMinPct / themeFontMaxPct bound the folded per-element scale to 3 pt through 48 pt. Deliberately not the
// per-panel user clamps (config.MinLogScalePercent): real themes ship music_list = 6, below the user zoom floor, and
// must survive.
// the user zoom floor and must survive.
const (
	themeFontMinPct = 34  // 3 pt → 4 px
	themeFontMaxPct = 534 // 48 pt → 64 px
)

// elemPct is the draw scale for element el: the theme's point size folded with the user's per-panel zoom; an unsized
// element returns userPct unchanged.
func (a *App) elemPct(el themeFontElem, userPct int) int {
	f := a.themeFonts.e[el]
	if f.pct == 0 {
		return userPct
	}
	return value.Clamp(f.pct*userPct/DefaultScalePct, themeFontMinPct, themeFontMaxPct)
}

// elemChat reports whether el draws out of the chat font set (showname + message) rather than the log one.
func elemChat(el themeFontElem) bool { return el == elemShowname || el == elemMessage }

// Undressed log elements get their own font set too. The IC log, OOC log, music list and area list each have their own
// Ctrl+wheel zoom and a themed courtroom paints all of them in one frame, so sharing c.logSet would rebuild it (and
// purgeTextCache) repeatedly every frame. The undressed case therefore routes to themeElemSets[el] with face = -1,
// which makes themeFontsFor use the client's own chain: same metrics, no sharing. The sets are a fixed
// [themeFontElemCount]fontSet array on Ctx (hard rule 4).
//
// The chat pair (showname + message) still routes to c.chatSet: the GIF/comic exports build the message raster at a
// caller-fitted pct (messageFontFor), and pinning that to an element set would make an export thrash the live chatbox's
// set.

// elemFontFor is the one call an element draw site makes: the covering face for text at el's resolved scale and
// declared family.
func (a *App) elemFontFor(el themeFontElem, userPct int, text string) *ttf.Font {
	return a.elemFontAtPct(el, a.elemPct(el, userPct), text)
}

// elemFontAtPct is elemFontFor with the scale already resolved, for the themed chatbox, which folds the theme canvas
// scale into the percent first (chatboxfit.go). Routing is identical either way.
func (a *App) elemFontAtPct(el themeFontElem, pct int, text string) *ttf.Font {
	f := a.themeFonts.e[el]
	if !f.dressed() && elemChat(el) {
		return a.ctx.ChatFontFor(pct, text)
	}
	return a.ctx.ThemeFontFor(el, f.faceIdx(), pct, text) // faceIdx() == -1 when undressed
}

// elemFont is elemFontFor's metrics twin (line height, column measure): the set's primary face, no per-line coverage
// pick.
func (a *App) elemFont(el themeFontElem, userPct int) *ttf.Font {
	f := a.themeFonts.e[el]
	pct := a.elemPct(el, userPct)
	if !f.dressed() && elemChat(el) {
		return a.ctx.ChatFont(pct)
	}
	return a.ctx.ThemeFont(el, f.faceIdx(), pct) // faceIdx() == -1 when undressed
}

// elemLabelFont is elemFont for a surface that used the fixed chrome face (the "Now playing" line): an undressed
// element keeps that face, resolved through chromeFaceFor so symbols and other scripts use the fallback chain.
func (a *App) elemLabelFont(el themeFontElem, userPct int, text string) *ttf.Font {
	if !a.themeFonts.e[el].dressed() {
		return a.ctx.chromeFaceFor(text)
	}
	return a.elemFont(el, userPct)
}

// elemLabelFontAtPct is elemLabelFont with the scale already resolved (see elemFontAtPct).
//
// The undressed arm still returns the fixed chrome face and ignores pct: a theme that declares nothing for the element
// must draw like no theme, so an undressed music_name can't shrink with the canvas.
func (a *App) elemLabelFontAtPct(el themeFontElem, pct int, text string) *ttf.Font {
	f := a.themeFonts.e[el]
	if !f.dressed() {
		return a.ctx.chromeFaceFor(text)
	}
	// Dressed, so this is elemFont's dressed arm with pct handed in.
	return a.ctx.ThemeFont(el, f.faceIdx(), pct)
}

// elemEmoji is the colour-emoji face at el's resolved scale; the baseline must match the text face in the same row.
func (a *App) elemEmoji(el themeFontElem, userPct int) *ttf.Font {
	return a.ctx.EmojiFont(a.elemPct(el, userPct))
}

// elemBold reports el's "<id>_bold", OR'd into each draw site's existing bold gate.
func (a *App) elemBold(el themeFontElem) bool { return a.themeFonts.e[el].bold }

// elemChromeFont is the face a panel should dress itself in, or nil to leave the chrome alone; handed to
// Ctx.pushPanelFont. Armed only when the user picked a font or size for that element, never on a theme's own dressing,
// so with nothing set it returns nil and widgets read the usual chrome pointer.
//
// Not a replacement for elemFont/elemFontFor at a row site: those zoom with Ctrl+wheel even when undressed (they route
// through the element's set); this returns the fixed chrome face then, so a row swapped to it stops following the
// panel's zoom.
func (a *App) elemChromeFont(el themeFontElem, userPct int) *ttf.Font {
	if !a.themeFonts.e[el].userFont {
		return nil
	}
	return a.elemFont(el, userPct)
}

// themeFaceNameFor is the file name of the face element el resolved to, or "" when it uses the client's chain. Settings
// only: it tells a user what an empty per-panel font box currently gives them.
func (a *App) themeFaceNameFor(el themeFontElem) string {
	i := a.themeFonts.e[el].faceIdx()
	if i < 0 || i >= len(a.themeFaceNames) {
		return ""
	}
	return a.themeFaceNames[i]
}

// messagePct is the chatbox message's resolved draw scale — the theme's
// "message" point size folded with the Text zoom knob (a.chatPct).
func (a *App) messagePct() int { return a.elemPct(elemMessage, a.chatPct) }

// messageFontFor is the chatbox message face at an already-resolved scale. Separate from elemFontFor because the
// GIF/comic exports fit their own pct to the capture frame: they take the theme's family but not its point size, so the
// size fold happens in messagePct, never here.
func (a *App) messageFontFor(pct int, text string) *ttf.Font {
	f := a.themeFonts.e[elemMessage]
	if !f.dressed() {
		return a.ctx.ChatFontFor(pct, text)
	}
	return a.ctx.ThemeFontFor(elemMessage, f.faceIdx(), pct, text)
}

// buildThemeFontTable resolves every courtroom_fonts.ini element of t into the apply result: point size to percent,
// family to an interned face, bold. All disk work (the bounded fonts/ walks and the face reads) happens here on the
// theme-apply goroutine (hard rule 2). Faces are deduped by resolved path.
func (res *themeApply) buildThemeFontTable(t *theme.Theme, sysDirs []string) {
	files := t.FontFiles(sysDirs)
	for i, id := range theme.FontElements {
		spec := t.Font(id)
		slot := &res.fontTable.e[i]
		if spec.SizeSet && spec.Size > 0 {
			slot.pct = themeFontPct(spec.Size)
		}
		slot.bold = spec.Bold
		if p := files[id]; p != "" {
			slot.face = res.internFace(p)
		} else if fam := strings.TrimSpace(spec.Font); fam != "" {
			// The theme named a family and nothing on this machine answers to it. Record it: otherwise the text
			// silently renders in the client's face and the showname font just looks wrong.
			res.noteMissingFamily(fam)
		}
		// "<id>_color"; skipped for the two chatbox elements (see themeElemFont.color).
		if !elemChat(themeFontElem(i)) {
			slot.color, slot.colorSet = declaredFontInk(spec)
		}
	}
}

// themeSidecarFontDir is where a native theme's own font files live, relative to the theme folder: AO2's fonts/
// convention, so a theme needn't ship a face twice.
// not have to ship it twice.
const themeSidecarFontDir = "fonts"

// applySidecarFonts binds the AsyncAO tier's [fontbind] rows onto the per-element font table.
//
// courtroom_fonts.ini names a family per element and leaves finding it to Qt's system font database, so shared themes
// arrive with families named and no file. The sidecar's `[fonts]` maps a family to a file inside the theme and
// `[fontbind]` maps an element id to that family, so the theme renders the same on a machine that has never heard of
// the family.
//
// It runs after buildThemeFontTable and overrides it: the sidecar is the one that can point at a file. The user's
// per-panel picks still outrank both (landThemeFonts). Theme-apply goroutine only: it stats and reads files.
func (res *themeApply) applySidecarFonts(sc *theme.Sidecar, sidecarDir string) {
	if sc == nil || sidecarDir == "" || len(sc.FontBind) == 0 {
		return
	}
	for i, id := range theme.FontElements {
		fam, bound := sc.BoundFamily(id)
		if !bound || strings.TrimSpace(fam) == "" {
			continue
		}
		file, declared := sc.FontFamilyFile(fam)
		if !declared || strings.TrimSpace(file) == "" {
			// Bound to a family the theme's [fonts] never declares. Reported, not guessed: a system search would render
			// differently on the author's machine than elsewhere.
			res.noteMissingFamily(fam)
			continue
		}
		path, ok := themeSidecarFontPath(sidecarDir, file)
		if !ok {
			res.noteMissingFamily(fam)
			continue
		}
		if face := res.internFace(path); face != 0 {
			res.fontTable.e[i].face = face
			continue
		}
		// internFace refuses an unreadable, oversized or over-cap file. The element keeps what the AO2 tier gave it,
		// and the family is named so the reason is visible.
		res.noteMissingFamily(fam)
	}
}

// themeSidecarFontPath resolves a [fonts] file inside the theme folder, refusing anything that escapes it
// (internal/safepath is the one copy of those guards; a theme is untrusted-archive input).
//
// Two rungs: <theme>/fonts/<file>, then <theme>/<file> for a hand-authored theme that dropped the .ttf beside its INI.
//
// The author's string goes to safepath raw on both rungs, so rung 1 pre-joins its own root instead of using
// filepath.Join(fontDir, rel): Join cleans, so `../x.ttf` would reach safepath already collapsed and pass a guard that
// never saw a `..`. That would be more lenient than the sibling themeMediaPath for the same untrusted string.
func themeSidecarFontPath(dir, file string) (string, bool) {
	rel := strings.TrimSpace(file)
	fontDir, err := safepath.Join(dir, themeSidecarFontDir)
	if err != nil {
		return "", false // a constant; unreachable, and cheaper to handle than to argue about
	}
	for _, root := range [2]string{fontDir, dir} {
		full, err := safepath.Join(root, rel)
		if err != nil {
			continue
		}
		if info, err := os.Stat(full); err == nil && !info.IsDir() && info.Size() <= fontFileMaxBytes {
			return full, true
		}
	}
	return "", false
}

// applyPanelFonts overlays the user's per-panel font and size on what the theme asked for. It runs off-thread right
// after buildThemeFontTable because the family resolution and face read touch the disk (hard rule 2).
//
// The user's pick wins over the theme's. Sizes and families are independent. Colour and bold stay the theme's design;
// size and family are what an accessibility need turns on.
func (res *themeApply) applyPanelFonts(overrides map[string]config.PanelFont, contentDirs, sysDirs []string) {
	if len(overrides) == 0 {
		return
	}
	// Resolve every requested family in one bounded walk, as theme.FontFiles does.
	var wanted []string
	for _, pf := range overrides {
		if pf.Family != "" {
			wanted = append(wanted, pf.Family)
		}
	}
	paths := theme.ResolveFamilies(wanted, contentDirs, sysDirs)
	for i, id := range theme.FontElements {
		pf, ok := overrides[id]
		if !ok {
			continue
		}
		res.panelFontsAsked++
		if pf.SizePx > 0 {
			res.panelPct[i] = pxToScalePct(pf.SizePx)
		}
		if pf.Family == "" {
			continue
		}
		p := paths[pf.Family]
		if p == "" {
			res.panelFontsMissing = append(res.panelFontsMissing, id+" ("+pf.Family+")")
			continue
		}
		// Interned here because reading the file is disk work; landThemeFonts decides on the render thread whether it
		// wins. A face that fails to intern records nothing, leaving the theme's face in place.
		if face := res.internFace(p); face != 0 {
			res.panelFace[i] = face
		} else {
			res.panelFontsMissing = append(res.panelFontsMissing, id+" ("+pf.Family+", could not be loaded)")
		}
	}
}

// facesIfReferenced returns data when some element in tbl points into it, else nil. Face bytes are not free
// (Ctx.SetThemeFaces holds them for the theme's lifetime, parses a cmap per face and purges every cached label), so a
// table where every element fell back to the client's chain must hand back nil.
func facesIfReferenced(tbl *themeFontTable, data [][]byte) [][]byte {
	for i := range tbl.e {
		if tbl.e[i].face != 0 {
			return data
		}
	}
	return nil
}

// mergePanelFonts stamps the user's per-panel picks onto a table after the theme's contribution is decided, so the
// precedence ladder reads in one place (landThemeFonts).
func (res *themeApply) mergePanelFonts(tbl *themeFontTable, faces bool) {
	for i := range tbl.e {
		if res.panelPct[i] != 0 {
			tbl.e[i].pct = res.panelPct[i]
		}
		if faces && res.panelFace[i] != 0 {
			tbl.e[i].face = res.panelFace[i]
		}
		// Stamped outside the faces gate: under the dyslexia switch landThemeFonts passes faces=false, and a user who
		// picked only a family would otherwise get no scope and see the panel half in each font. The switch only
		// decides which face it gets.
		if res.panelPct[i] != 0 || res.panelFace[i] != 0 {
			tbl.e[i].userFont = true
		}
	}
}

// pxToScalePct converts a pixel size the user typed to the table's percent scale, as themeFontPct does for points minus
// the point-to-pixel step. It rounds up for the same reason: the face reopens with integer division.
func pxToScalePct(px int) int {
	if px <= 0 {
		return 0
	}
	return value.Clamp((px*DefaultScalePct+UIFontSize-1)/UIFontSize, themeFontMinPct, themeFontMaxPct)
}

// themeMissingFamilyCap bounds the reported list so a corrupt INI can't grow the slice and the warning stays one line
// (hard rule 4).
const themeMissingFamilyCap = 8

// themeGenericFontClasses are the CSS/Qt generic family names. A theme naming one names a class, which Qt resolves from
// its alias table with no font file, so `serif` in courtroom_fonts.ini renders upstream. AsyncAO resolves theme faces
// from files, so a generic class resolves to nothing and the element keeps the client's face, as the author asked.
//
// Without this table the missing-font warning fires on data that behaves correctly, including the shipped style
// presets, which bind generic classes deliberately because they must not bundle a face. A warning that is usually wrong
// goes unread when it is right.
//
// Lower-cased spellings only; the probe folds case.
var themeGenericFontClasses = [...]string{
	"serif", "sans", "sans-serif", "sansserif",
	"mono", "monospace", "cursive", "fantasy", "system-ui",
}

// genericFontClass reports a family name that names a class rather than a face. EqualFold, not a lowered copy: this
// runs per element per apply and ToLower allocates on upper-case runes.
func genericFontClass(fam string) bool {
	want := strings.TrimSpace(fam)
	for _, c := range themeGenericFontClasses {
		if strings.EqualFold(c, want) {
			return true
		}
	}
	return false
}

// noteMissingFamily records a declared font family that resolved to no file, deduped and in declaration order (showname
// and message come first and are the two a user notices).
func (res *themeApply) noteMissingFamily(fam string) {
	if genericFontClass(fam) {
		return // a CLASS, not a missing file — see themeGenericFontClasses
	}
	for _, have := range res.themeFontsMissing {
		if strings.EqualFold(have, fam) {
			return
		}
	}
	if len(res.themeFontsMissing) >= themeMissingFamilyCap {
		return
	}
	res.themeFontsMissing = append(res.themeFontsMissing, fam)
}

// themeFontWarning is the line shown when a theme asks for fonts this machine lacks, or "" when everything resolved. It
// names the remedy. AO2 keeps fonts in the base's fonts/ folder and registers it globally at startup (main.cpp), so a
// theme downloaded alone arrives with families named and no files; that is the common case and not the author's
// mistake, so the wording doesn't blame them.
//
// anyFontsOnDisk picks the advice: a content root with a fonts folder means the family is simply not in it; no fonts
// folder anywhere means themes without a base.
func themeFontWarning(missing []string, anyFontsOnDisk bool, dropDir string) string {
	if len(missing) == 0 {
		return ""
	}
	msg := "This theme asks for fonts you don't have: " + strings.Join(missing, ", ") + ". "
	if !anyFontsOnDisk {
		// No fonts anywhere: almost certainly a themes-only download with no base beside it.
		msg += "AO themes name their fonts but don't include them — in AO2 they live in the base's fonts folder. "
	}
	if dropDir != "" {
		msg += "Put the font files in " + dropDir + " (or in the theme's own folder) and reload the theme."
	} else {
		msg += "Put the font files in the theme's own folder and reload the theme."
	}
	return msg
}

// internFace returns the 1-based index of an already-read face path, else reads and appends it. 0 = unreadable,
// oversized or past themeFaceCap; the element then uses the client's own chain.
func (res *themeApply) internFace(path string) int {
	for i, p := range res.facePaths {
		if p == path {
			return i + 1
		}
	}
	if len(res.faceData) >= themeFaceCap {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > fontFileMaxBytes {
		return 0
	}
	res.faceData = append(res.faceData, data)
	res.facePaths = append(res.facePaths, path)
	res.faceNames = append(res.faceNames, filepath.Base(path))
	return len(res.faceData)
}

// landThemeFonts installs one apply's font table on the render thread. Called from pollThemeApply, so it sits behind
// the themeAppliedGen stale-generation guard (a slower older load can't revert a newer theme's fonts).
func (a *App) landThemeFonts(res *themeApply) {
	// Precedence, most specific last (last write wins): the theme's own table; a global user font (dyslexia toggle or
	// manual path), which drops the theme's families but keeps its sizes; the user's per-panel pick. Exception: the
	// dyslexia toggle beats even a per-panel pick, being an accessibility switch.
	tbl := res.fontTable
	dyslexia := fontChainSource(a.d.Prefs.Load().DyslexiaFont, a.d.Prefs.Load().FontOverridePaths) == fontSourceDyslexia
	switch {
	case !a.d.Prefs.Load().ThemeFonts:
		// Opt-out: the zero table drops the theme's contribution. The user's per-panel picks are re-applied below; this
		// checkbox means "use the theme's fonts", not "use no fonts but the client's".
		tbl = themeFontTable{}
	case dyslexia, strings.TrimSpace(a.d.Prefs.Load().FontOverridePaths) != "":
		// A manual font override / the dyslexia toggle is the user's pick and outranks the theme's families, as in
		// applyFontConfig. The theme's sizes still apply.
		for i := range tbl.e {
			tbl.e[i].face = 0
		}
	}
	// Sizes always; faces unless the dyslexia switch has claimed them.
	res.mergePanelFonts(&tbl, !dyslexia)
	// Install the face bytes only if something points at them; otherwise they stay alive, re-parse a cmap per face and
	// purge the text cache for nothing.
	a.ctx.SetThemeFaces(facesIfReferenced(&tbl, res.faceData))
	// Readability guard for the per-element ink: render thread, since it compares against ColPanel, which is final only
	// after applyThemePalette landed this theme's stylesheet. The pixel sampling already happened off-thread.
	guardThemeInk(res, &tbl)
	a.themeFonts = tbl
	// The chatbox message raster bakes its face and scale; re-raster so a theme swap doesn't leave the old size.
	a.rasterText = ""
}

// systemFontDirs lists, in order, the fallback font folders theme.FontFiles searches for a family the theme doesn't
// ship: (1) AsyncAO's own fonts folder, a drop folder, first because dropping a file there is explicit and should beat
// an ambient system font of the same name; (2) the OS font folders, which make a theme declaring plain "Arial" resolve
// at all (AO2 gets this from Qt, get_qfont, courtroom.cpp:1263).
//
// Both are scanned flat (depth 1), so files go directly in the folder. Called off-thread: it walks the disk.
func systemFontDirs() []string {
	var dirs []string
	if d := config.UserFontsDir(); d != "" {
		dirs = append(dirs, d)
	}
	return append(dirs, platform.SystemFonts().ThemeDirs...)
}

// userFontsReadme is written into the fonts folder when it is first created, since an empty config folder explains
// nothing.
const userFontsReadme = `Put font files (.ttf, .otf, .ttc) in this folder.

AsyncAO looks here when a theme asks for a font you do not have. AO2 themes name
their fonts but usually do not include them - in AO2 they live in the base's
"fonts" folder, so a theme downloaded on its own arrives with none of them.

Drop the files straight into this folder (not into sub-folders), then use
Settings -> Theme -> Reload theme. The file name is what is matched, so a theme
asking for "Ace Name" wants a file called "Ace Name.ttf" or "acename.ttf".
`

// ensureUserFontsDir creates the drop folder and its README if absent and returns the folder path ("" if it could not
// be resolved or created). Created eagerly on theme apply for discoverability; off-thread and cheap.
func ensureUserFontsDir() string {
	dir := config.UserFontsDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	readme := filepath.Join(dir, "README.txt")
	if _, err := os.Stat(readme); err == nil {
		return dir
	}
	// Best-effort: a read-only config dir still gets the folder path reported in Settings.
	_ = os.WriteFile(readme, []byte(userFontsReadme), 0o644)
	return dir
}

// declaredFontInk converts a parsed courtroom_fonts.ini entry into an ink colour, false when the theme declared no
// "<id>_color".
//
// The gate is FontSpec.ColorSet, not Theme.HasFont: HasFont is true for a size alone and FontSpec.Color carries the
// parser's white default, so gating on HasFont forced white ink on a theme that only sizes its chatbox. AO2 reads the
// colour from its own key and overwrites its local default only when that key parsed (courtroom.cpp:1223-1225).
func declaredFontInk(spec theme.FontSpec) (sdl.Color, bool) {
	if !spec.ColorSet {
		return sdl.Color{}, false
	}
	return sdl.Color{R: spec.Color.R, G: spec.Color.G, B: spec.Color.B, A: 255}, true
}
