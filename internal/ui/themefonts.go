package ui

// Per-element theme fonts (#39) — AO2-Client Courtroom::set_fonts /
// Courtroom::set_font (courtroom.cpp:1188 / :1212) read courtroom_fonts.ini and
// give EACH courtroom element its own family ("<id>_font"), point size ("<id>")
// and weight ("<id>_bold"). AsyncAO used to consume exactly two things out of
// that file — the "message" family, applied GLOBALLY to every surface in the
// client, and the message/showname colours — so a theme that sized its IC log at
// 11 pt and its music list at 6 pt rendered both at the client's own scale.
//
// This file holds the resolved table and the accessors every draw site calls.
// The table is a fixed-size ARRAY, not a map: an element lookup on the render
// path is one indexed load, never a map probe or a font open (the 0-alloc
// whole-screen gate). It is populated OFF-THREAD on theme apply (hard rule 2 —
// no synchronous disk I/O on a draw path) and landed by pollThemeApply behind
// the existing themeAppliedGen stale-generation guard.
//
// The ZERO table means "this theme dresses nothing", and every accessor then
// returns exactly what the pre-#39 code returned — so a client with no theme, or
// with the Theme fonts setting off, is byte-identical to before.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// themeFontElem indexes the per-element font table. The order MUST match
// theme.FontElements — buildThemeFontTable fills slot i from FontElements[i],
// and TestThemeFontElemOrder pins the two together.
type themeFontElem int

const (
	elemShowname themeFontElem = iota
	elemMessage
	elemICChatlog
	elemServerChatlog
	elemMusicList
	elemMusicName
	elemAreaList
	// elemDebugLog dresses the themed debug panel drawn at the ms_chatlog rect.
	// AO2 gives that widget its OWN identifier, not server_chatlog's
	// (courtroom.cpp:1198). APPENDED, never inserted: the slot index is the
	// position in theme.FontElements.
	elemDebugLog
	themeFontElemCount // array size — keep last
)

// themeElemFont is one resolved courtroom_fonts.ini element.
type themeElemFont struct {
	// pct is the declared point size folded onto AsyncAO's percent scale;
	// 0 = the theme declared no size for this element.
	pct int
	// face is 1-BASED into Ctx.themeFaceData: 0 = the element named no family
	// (or it didn't resolve on disk), so the zero value means "use the client's
	// own font chain" and a zero table is the untouched pre-#39 path.
	face int
	// bold mirrors "<id>_bold = 1"; drawn with the existing 1px-shifted
	// faux-bold second pass, not a separate bold face (AO2 uses QFont::setBold,
	// which synthesises the weight the same way when the family has no bold cut).
	bold bool
	// color / colorSet are "<id>_color" — the element's foreground ink. AO2 stamps
	// it as `<class> { color: rgba(...) }` on the widget (courtroom.cpp:1300), i.e.
	// one FOREGROUND per element and never a background. colorSet false = the theme
	// named no colour for this element and the draw site keeps the kit's own.
	//
	// elemShowname / elemMessage are deliberately left UNPOPULATED here: their
	// colours already ride a.themeMsgCol / a.themeNameCol behind the chatbox-skin
	// guard, and filling both would apply one colour twice through two different
	// readability guards.
	color    sdl.Color
	colorSet bool
}

// dressed reports whether the theme said ANYTHING about this element.
func (f themeElemFont) dressed() bool { return f.pct != 0 || f.face != 0 }

// faceIdx converts the 1-based face slot to a Ctx.themeFaceData index
// (-1 = none).
func (f themeElemFont) faceIdx() int { return f.face - 1 }

// themeFontTable is the whole applied theme's font parity data. A value type,
// copied wholesale on apply — no locking and no pointer chasing at draw time.
type themeFontTable struct {
	e [themeFontElemCount]themeElemFont
}

// themeFaceCap bounds the DISTINCT theme-declared face files held at once.
// courtroom_fonts.ini names one family per element and real themes reuse one or
// two across all of them (DRRetribution: 1 — Arial everywhere; 3DS Widescreen: 2
// — Igiari Cyrillic + Ace Name; Lymantriina: 2 — IBM Plex Serif + Mono), so 4 —
// matching fontChainCap — covers every shipped theme with headroom. Past it an
// element keeps the client font rather than growing the cache (hard rule 4).
const themeFaceCap = 4

// qtLogicalDPI / qtPointsPerInch are Qt's own point-to-pixel conversion. A POINT
// is 1/72 inch and Qt renders at a logical 96 DPI, so P points is P×96/72 pixels —
// 8 pt is 11 px, not 8. courtroom_fonts.ini declares POINTS (QFont::setPointSize,
// AO2-Client courtroom.cpp:1217) and AsyncAO's percent scale is in PIXELS, so the
// conversion has to happen or every themed element renders a third too small.
const (
	qtLogicalDPI    = 96
	qtPointsPerInch = 72
)

// themeFontPx is a courtroom_fonts.ini point size in pixels, rounded as Qt rounds.
func themeFontPx(points int) int {
	return (points*qtLogicalDPI + qtPointsPerInch/2) / qtPointsPerInch
}

// themeFontPct converts a courtroom_fonts.ini point size to AsyncAO's percent
// scale. AO2 renders the INI's point sizes through QFont::setPointSize times a
// FIXED user option (Options::themeScalingFactor, default 1.0) — it does NOT
// scale them with the window, which is why AsyncAO doesn't either; the per-panel
// Ctrl+wheel zoom is the user's equivalent knob and folds in through elemPct.
//
// TWO truncations used to eat the size and they compounded. Points were treated
// as pixels (8 pt → 8 px instead of 11), and then the percent was rounded DOWN
// again when the face was opened at UIFontSize×pct/100 — so this theme's 8 pt
// music list resolved to a 7 px face, and its 24 pt message to 24 px where Qt
// gives 32. Rounding UP here is what makes the round trip exact: 11 px wants
// 91.67%, and 91% reopens at 10 px while 92% reopens at 11.
func themeFontPct(points int) int {
	px := themeFontPx(points)
	return (px*DefaultScalePct + UIFontSize - 1) / UIFontSize
}

// themeFontMinPct / themeFontMaxPct bound the FOLDED per-element scale, holding
// the SAME 3 pt … 48 pt range they always documented. They moved with the
// point-to-pixel fix rather than in spite of it: 48 pt is 64 px, which is 534% of
// a 12 px base, so the old 400 would have silently capped every element above
// 36 pt — the corpus's 50 pt outlier lands at 534 now instead of being cut to
// three quarters of its authored size.
//
// Deliberately NOT the per-panel user clamps (config.MinLogScalePercent = 75):
// real themes ship music_list = 6 (3DS Widescreen), which is legitimately below
// the user zoom floor and must survive.
const (
	themeFontMinPct = 34  // 3 pt → 4 px
	themeFontMaxPct = 534 // 48 pt → 64 px
)

// elemPct is the draw scale for element el: the theme's declared point size
// folded with the user's per-panel zoom. An element the theme didn't size
// returns userPct UNCHANGED, which is the byte-identical pre-#39 path. Pure
// integer arithmetic on a fixed array — no map, no lock, no prefs read.
func (a *App) elemPct(el themeFontElem, userPct int) int {
	f := a.themeFonts.e[el]
	if f.pct == 0 {
		return userPct
	}
	return clampInt(f.pct*userPct/DefaultScalePct, themeFontMinPct, themeFontMaxPct)
}

// elemChat reports whether element el draws out of the CHAT font set rather than
// the LOG one — the chatbox pair (showname + message) versus every list/log
// surface. Matches which accessor each draw site used before #39.
func elemChat(el themeFontElem) bool { return el == elemShowname || el == elemMessage }

// UNDRESSED LOG ELEMENTS GET THEIR OWN SET TOO. A dressed element has always been
// routed to themeElemSets[el] (its own fontSet), and TestThemeElemSetsPinTheirOwnScale
// spells out why: two elements drawn in the same frame at different point sizes would
// otherwise rebuild one shared set — and purgeTextCache with it — twice per frame,
// forever. That reasoning has nothing to do with the THEME: the IC log, the OOC log,
// the music list and the area list each carry their OWN Ctrl+wheel zoom (a.logPct /
// a.oocPct / a.musicPct / a.areaPct), a themed courtroom paints all of them at once,
// and one zoom notch on any of them makes the percents differ. Routed to the shared
// c.logSet, that measured ~42 allocations per frame with a full text-atlas teardown.
// So the undressed case takes the same route with face = -1 (out of range ⇒
// themeFontsFor uses the client's OWN chain — the path
// TestThemeElemSetsCapAndOutOfRangeFace already pins), which is metrically identical
// to the old shared set, just no longer shared.
//
// Bounded by construction (hard rule 4): the element sets are a fixed
// [themeFontElemCount]fontSet array on Ctx, not a growing per-pct cache.
//
// The CHAT pair (showname + message) deliberately still routes to c.chatSet: the
// message raster is also built by the GIF/comic exports at a caller-fitted pct
// (messageFontFor), and pinning that to an element set would make an export thrash
// the live chatbox's set instead.

// elemFontFor is the ONE call an element draw site makes: the covering face for
// text, at el's resolved scale, in el's own declared family.
func (a *App) elemFontFor(el themeFontElem, userPct int, text string) *ttf.Font {
	return a.elemFontAtPct(el, a.elemPct(el, userPct), text)
}

// elemFontAtPct is elemFontFor with the scale ALREADY resolved — the split
// exists for the themed chatbox, which folds the theme canvas's own scale into
// the percent before it asks for a face (chatboxfit.go). Element ROUTING (whose
// set, whose family) is identical either way; only the arithmetic in front of it
// differs, so it belongs in one place and the routing in another.
func (a *App) elemFontAtPct(el themeFontElem, pct int, text string) *ttf.Font {
	f := a.themeFonts.e[el]
	if !f.dressed() && elemChat(el) {
		return a.ctx.ChatFontFor(pct, text)
	}
	return a.ctx.ThemeFontFor(el, f.faceIdx(), pct, text) // faceIdx() == -1 when undressed
}

// elemFont is elemFontFor's metrics twin (line height / column measure): the
// element set's PRIMARY face, no per-line coverage pick.
func (a *App) elemFont(el themeFontElem, userPct int) *ttf.Font {
	f := a.themeFonts.e[el]
	pct := a.elemPct(el, userPct)
	if !f.dressed() && elemChat(el) {
		return a.ctx.ChatFont(pct)
	}
	return a.ctx.ThemeFont(el, f.faceIdx(), pct) // faceIdx() == -1 when undressed
}

// elemLabelFont is elemFont for a surface that used the fixed CHROME face before
// #39 (the "Now playing" line): an undressed element keeps that chrome face, so
// the label is byte-identical, while a theme that sizes/names music_name gets
// its own.
func (a *App) elemLabelFont(el themeFontElem, userPct int) *ttf.Font {
	if !a.themeFonts.e[el].dressed() {
		return a.ctx.font
	}
	return a.elemFont(el, userPct)
}

// elemEmoji is the colour-emoji face at el's resolved scale — the baseline must
// match the text face the same row draws in.
func (a *App) elemEmoji(el themeFontElem, userPct int) *ttf.Font {
	return a.ctx.EmojiFont(a.elemPct(el, userPct))
}

// elemBold reports el's "<id>_bold", OR'd into each draw site's existing
// faux-bold gate.
func (a *App) elemBold(el themeFontElem) bool { return a.themeFonts.e[el].bold }

// themeFaceNameFor is the FILE NAME of the face element el resolved to, or ""
// when it is using the client's own chain. Settings-only: it tells a user what
// an empty per-panel font box is currently giving them, which is the difference
// between "this setting does nothing" and "this setting is already set".
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

// messageFontFor is the chatbox message face at an ALREADY-RESOLVED scale. Kept
// separate from elemFontFor because the message raster is also built by the
// GIF/comic exports, which fit their own pct to the capture frame: they must get
// the theme's FAMILY without the theme's point size overriding that fit, so the
// size fold happens at the caller (messagePct) and never in here.
func (a *App) messageFontFor(pct int, text string) *ttf.Font {
	f := a.themeFonts.e[elemMessage]
	if !f.dressed() {
		return a.ctx.ChatFontFor(pct, text)
	}
	return a.ctx.ThemeFontFor(elemMessage, f.faceIdx(), pct, text)
}

// buildThemeFontTable resolves every courtroom_fonts.ini element of t into the
// apply result: point size → percent, declared family → an interned face slot,
// bold flag. ALL disk work — the bounded fonts/ walks in theme.FontFiles AND the
// face file reads — happens here, on the theme-apply goroutine (hard rule 2).
// Faces are DEDUPED by resolved path, because real themes name one or two
// families across all seven elements.
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
			// The theme NAMED a family and nothing on this machine answers to it.
			// Recorded rather than shrugged off, because the symptom a user sees is
			// "the showname font is wrong" — the text still renders, in the client's
			// own face, with nothing anywhere to say why. Reported live on two
			// separate themes before this existed.
			res.noteMissingFamily(fam)
		}
		// "<id>_color" (#21 label 16). Skipped for the two chatbox elements — see
		// themeElemFont.color for why they must not be filled from here.
		if !elemChat(themeFontElem(i)) {
			slot.color, slot.colorSet = declaredFontInk(spec)
		}
	}
}

// applyPanelFonts overlays the USER's per-panel font and size on top of whatever
// the theme asked for. Runs on the same off-thread pass and immediately after
// buildThemeFontTable, because both the family resolution and the face read
// touch the disk (hard rule 2).
//
// The user's pick wins over the theme's, and that is the whole point: a theme
// author chose a font for their own design, but only the person reading it knows
// whether they can read it. Sizes and families are INDEPENDENT — someone who
// only wants bigger text keeps the theme's family, and the reverse.
//
// Colour and bold are deliberately untouched. Those are the theme's design
// (and, for colour, already guarded for readability), while size and family are
// the two an accessibility need actually turns on.
func (res *themeApply) applyPanelFonts(overrides map[string]config.PanelFont, contentDirs, sysDirs []string) {
	if len(overrides) == 0 {
		return
	}
	// Resolve every requested family in ONE bounded walk rather than one per
	// element, the same reason theme.FontFiles takes the whole set at once.
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
		// Interned HERE because reading the file is disk work; the decision about
		// whether it wins lives in landThemeFonts, on the render thread, where the
		// rest of the preference ladder is already read. A face that fails to
		// intern (unreadable, oversized, past the cap) records nothing, which
		// leaves the theme's own face in place — the user asked to CHANGE the
		// font, not to lose one.
		if face := res.internFace(p); face != 0 {
			res.panelFace[i] = face
		} else {
			res.panelFontsMissing = append(res.panelFontsMissing, id+" ("+pf.Family+", could not be loaded)")
		}
	}
}

// facesIfReferenced returns data when at least one element in tbl actually
// points into it, and nil otherwise.
//
// The distinction matters because face bytes are not free: Ctx.SetThemeFaces
// holds them for the theme's lifetime, parses a cmap coverage table per face,
// and purges every cached label. A table where every element fell back to the
// client's chain — the theme-fonts opt-out with no per-panel picks, or a global
// font override — must hand back nil so none of that is paid.
func facesIfReferenced(tbl *themeFontTable, data [][]byte) [][]byte {
	for i := range tbl.e {
		if tbl.e[i].face != 0 {
			return data
		}
	}
	return nil
}

// mergePanelFonts stamps the user's per-panel picks onto a table, AFTER whatever
// the theme contributed to it has been decided. Separate from the resolution
// above so the whole precedence ladder is readable in one place (landThemeFonts)
// instead of being split across a thread boundary.
func (res *themeApply) mergePanelFonts(tbl *themeFontTable, faces bool) {
	for i := range tbl.e {
		if res.panelPct[i] != 0 {
			tbl.e[i].pct = res.panelPct[i]
		}
		if faces && res.panelFace[i] != 0 {
			tbl.e[i].face = res.panelFace[i]
		}
	}
}

// pxToScalePct converts a pixel size the user typed into the percent scale the
// table stores, the same conversion themeFontPct does for a theme's points —
// minus the point-to-pixel step, because a user picks the pixels directly.
//
// Rounds UP for the same reason themeFontPct does: the percent is applied to
// UIFontSize with integer division when the face is opened, so rounding down
// here reopens one pixel small and the round trip is not exact.
func pxToScalePct(px int) int {
	if px <= 0 {
		return 0
	}
	return clampInt((px*DefaultScalePct+UIFontSize-1)/UIFontSize, themeFontMinPct, themeFontMaxPct)
}

// themeMissingFamilyCap bounds the reported list. A theme names at most one
// family per element, so this cannot realistically be reached — it exists so a
// corrupt INI cannot grow the slice, and so the warning stays one readable line
// rather than a wall (hard rule 4).
const themeMissingFamilyCap = 8

// noteMissingFamily records a declared font family that resolved to no file,
// deduped and in declaration order. Order matters for the message: showname and
// message come first in theme.FontElements, and they are the two a user notices.
func (res *themeApply) noteMissingFamily(fam string) {
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

// themeFontWarning is the line the user is shown when a theme asks for fonts
// this machine does not have, or "" when everything resolved.
//
// It names the remedy rather than just the fault, and WHICH remedy depends on
// what we found. AO2 keeps its fonts in the base's own fonts/ folder and
// registers that folder globally at startup (main.cpp), so a theme downloaded on
// its own — from a themes-only repository, which is how most of them are shared
// — arrives with its families named and none of the files. That is the common
// case and it is not the theme author's mistake, so the wording does not read as
// one.
//
// anyFontsOnDisk distinguishes the two situations that need different advice: a
// content root that HAS a fonts folder means the pack is set up correctly and
// this particular family is simply not in it, while no fonts folder anywhere
// means the user has the themes but not a base to go with them.
func themeFontWarning(missing []string, anyFontsOnDisk bool, dropDir string) string {
	if len(missing) == 0 {
		return ""
	}
	msg := "This theme asks for fonts you don't have: " + strings.Join(missing, ", ") + ". "
	if !anyFontsOnDisk {
		// No fonts anywhere in this installation — almost certainly a themes-only
		// download with no base beside it. Say that, because it is the actual
		// situation and it is nobody's mistake.
		msg += "AO themes name their fonts but don't include them — in AO2 they live in the base's fonts folder. "
	}
	if dropDir != "" {
		msg += "Put the font files in " + dropDir + " (or in the theme's own folder) and reload the theme."
	} else {
		msg += "Put the font files in the theme's own folder and reload the theme."
	}
	return msg
}

// internFace returns the 1-based slot of an already-read face path, else reads
// the file and appends it. 0 = unreadable, oversized, or past themeFaceCap — the
// element then falls back to the client's own chain.
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

// landThemeFonts installs one apply's per-element font table on the RENDER
// thread. Called from pollThemeApply, so it already sits behind the
// themeAppliedGen stale-generation guard (a slower older-gen load can never
// revert a newer theme's fonts — the "applies then reverts" class of bug).
func (a *App) landThemeFonts(res *themeApply) {
	// THE PRECEDENCE LADDER, most specific last, because the last write wins:
	//
	//   1. the theme's own table (below)
	//   2. a global user font — the dyslexia toggle or a manual font path —
	//      which drops the theme's FAMILIES but keeps its SIZES
	//   3. the user's PER-PANEL pick, which is more specific than either
	//
	// with one deliberate exception: the dyslexia toggle beats even a per-panel
	// pick. It is an accessibility switch, not a preference — someone who turns
	// it on and still cannot read one panel because of an override they set weeks
	// ago would be right to call that broken.
	tbl := res.fontTable
	dyslexia := fontChainSource(a.d.Prefs.DyslexiaFontOn(), a.d.Prefs.FontPaths()) == fontSourceDyslexia
	switch {
	case !a.d.Prefs.ThemeFontsOn():
		// Opt-out: the zero table restores pre-#39 behaviour for the THEME's
		// contribution. The user's own per-panel picks are re-applied below —
		// that checkbox says "use the theme's fonts", and reading it as "use no
		// fonts but the client's" would silently disable a setting the user made
		// somewhere else entirely.
		tbl = themeFontTable{}
	case dyslexia, strings.TrimSpace(a.d.Prefs.FontPaths()) != "":
		// A manual font override / the dyslexia toggle is the USER's pick and
		// outranks the theme's FAMILIES — the same ladder applyFontConfig uses for
		// the global chain. The theme's SIZES still apply: AO2 has no user font
		// override to lose to, and size is the substance of #39.
		for i := range tbl.e {
			tbl.e[i].face = 0
		}
	}
	// Sizes always; faces unless the dyslexia switch has claimed them.
	res.mergePanelFonts(&tbl, !dyslexia)
	// Install the face bytes ONLY if something now points at them. Both branches
	// above can leave every element on the client's own chain, and handing Ctx a
	// set nothing references would keep those megabytes alive, re-parse a cmap
	// cover per face and purge the whole text cache for nothing.
	a.ctx.SetThemeFaces(facesIfReferenced(&tbl, res.faceData))
	// Readability guard for the per-element ink, on the RENDER thread and not the
	// apply goroutine: it compares against ColPanel for any element with no theme
	// art behind it, and ColPanel is only final once applyThemePalette has landed
	// this theme's stylesheet (pollThemeApply calls that just above us). The pixel
	// sampling that feeds it already happened off-thread; what is left here is
	// integer arithmetic on a landed struct.
	guardThemeInk(res, &tbl)
	a.themeFonts = tbl
	// The chatbox message raster bakes its face and scale; re-raster it so a
	// theme swap doesn't leave the old size on screen until the next message.
	a.rasterText = ""
}

// systemFontDirs lists the fallback font folders theme.FontFiles searches for a
// family the theme itself does not ship, IN ORDER:
//
//  1. AsyncAO's own fonts folder, where the user may drop font files. First,
//     because dropping a file in there is an explicit act and should beat an
//     ambient system font of the same name.
//  2. the OS font folders — the tier that makes a theme declaring plain "Arial"
//     (DRRetribution, KFO qHD) resolve at all. AO2 gets this free from Qt's
//     system font database (get_qfont, courtroom.cpp:1263). Windows only: the
//     mac / Linux font stores are laid out per-family and per-user, so probing
//     them by file stem would resolve the wrong cut more often than the right
//     one, and those platforms keep the client font.
//
// Both are scanned FLAT (theme.FontFiles gives this tier depth 1), which is why
// the user folder is documented as a drop folder: files go directly in it, not
// into subfolders. That is also what keeps the scan cheap on a Windows\Fonts
// with several hundred files.
//
// Called off-thread — it walks the disk.
func systemFontDirs() []string {
	var dirs []string
	if d := config.UserFontsDir(); d != "" {
		dirs = append(dirs, d)
	}
	if runtime.GOOS == "windows" {
		if win := os.Getenv("WINDIR"); win != "" {
			dirs = append(dirs, filepath.Join(win, "Fonts"))
		}
	}
	return dirs
}

// userFontsReadme is written into the fonts folder the first time it is created,
// because an empty folder in a config directory explains nothing. It names the
// families the user's own themes are asking for at that moment, which is the
// difference between "put fonts here" and "put THESE fonts here".
const userFontsReadme = `Put font files (.ttf, .otf, .ttc) in this folder.

AsyncAO looks here when a theme asks for a font you do not have. AO2 themes name
their fonts but usually do not include them - in AO2 they live in the base's
"fonts" folder, so a theme downloaded on its own arrives with none of them.

Drop the files straight into this folder (not into sub-folders), then use
Settings -> Theme -> Reload theme. The file name is what is matched, so a theme
asking for "Ace Name" wants a file called "Ace Name.ttf" or "acename.ttf".
`

// ensureUserFontsDir creates the drop folder and its README if they are absent,
// and returns the folder path ("" if it could not be resolved or created).
//
// Created EAGERLY on theme apply rather than on first use, because the whole
// point is discoverability: a folder that only appears once you have already
// worked out that you need it has not helped anyone. Off-thread, and cheap — a
// stat on the common path where both already exist.
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
	// Best-effort: a read-only config dir still gets the folder path reported in
	// Settings, which is the part the user needs.
	_ = os.WriteFile(readme, []byte(userFontsReadme), 0o644)
	return dir
}

// declaredFontInk converts a parsed courtroom_fonts.ini entry into an ink colour,
// reporting false when the theme declared no "<id>_color" at all.
//
// The gate is FontSpec.ColorSet and NOT Theme.HasFont, which is the whole reason
// this is a named function instead of two inline conditions. HasFont answers
// "did the theme mention this element at all — size OR colour", and FontSpec.Color
// carries the parser's WHITE default whenever no colour was declared. Gating on
// HasFont therefore made a theme that only SIZES its chatbox — "message = 20" with
// no message_color, an entirely ordinary thing to write — force white message and
// showname ink it never asked for, discarding the client's own.
//
// AO2 cannot make that mistake because it reads the colour from its own key and
// lets the size key imply nothing: courtroom.cpp:1223-1225 starts from a local
// default and overwrites it only when that key actually parsed.
func declaredFontInk(spec theme.FontSpec) (sdl.Color, bool) {
	if !spec.ColorSet {
		return sdl.Color{}, false
	}
	return sdl.Color{R: spec.Color.R, G: spec.Color.G, B: spec.Color.B, A: 255}, true
}
