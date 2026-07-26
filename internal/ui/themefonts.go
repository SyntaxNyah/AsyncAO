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

	"github.com/veandco/go-sdl2/ttf"

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

// themeFontPct converts a courtroom_fonts.ini point size to AsyncAO's percent
// scale. Every scaled face is opened at UIFontSize×pct/100 (see buildSet), so P
// points is exactly pct = P×100/UIFontSize. AO2 renders the INI's point sizes
// through QFont::setPointSize (courtroom.cpp:1217) times a FIXED user option
// (Options::themeScalingFactor, default 1.0) — it does NOT scale them with the
// window, which is why AsyncAO doesn't either; the per-panel Ctrl+wheel zoom is
// the user's equivalent knob and folds in through elemPct below.
func themeFontPct(points int) int { return points * DefaultScalePct / UIFontSize }

// themeFontMinPct / themeFontMaxPct bound the FOLDED per-element scale (3 pt …
// 48 pt at UIFontSize = 12). Deliberately NOT the per-panel user clamps
// (config.MinLogScalePercent = 75): real themes ship music_list = 6 (3DS
// Widescreen), which is legitimately below the user zoom floor and must survive.
const (
	themeFontMinPct = 25
	themeFontMaxPct = 400
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
	f := a.themeFonts.e[el]
	pct := a.elemPct(el, userPct)
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
		}
	}
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
	tbl := res.fontTable
	switch {
	case !a.d.Prefs.ThemeFontsOn():
		// Opt-out: the zero table restores pre-#39 behaviour everywhere.
		tbl = themeFontTable{}
		a.ctx.SetThemeFaces(nil)
	case fontChainSource(a.d.Prefs.DyslexiaFontOn(), a.d.Prefs.FontPaths()) == fontSourceDyslexia,
		strings.TrimSpace(a.d.Prefs.FontPaths()) != "":
		// A manual font override / the dyslexia toggle is the USER's pick and
		// outranks the theme's FAMILIES — the same ladder applyFontConfig uses for
		// the global chain. The theme's SIZES still apply: AO2 has no user font
		// override to lose to, and size is the substance of #39.
		for i := range tbl.e {
			tbl.e[i].face = 0
		}
		a.ctx.SetThemeFaces(nil)
	default:
		a.ctx.SetThemeFaces(res.faceData)
	}
	a.themeFonts = tbl
	// The chatbox message raster bakes its face and scale; re-raster it so a
	// theme swap doesn't leave the old size on screen until the next message.
	a.rasterText = ""
}

// systemFontDirs lists the OS font folders theme.FontFiles falls back to for a
// family the theme doesn't ship — the tier that makes a theme declaring plain
// "Arial" (DRRetribution, KFO qHD) resolve at all. AO2 gets this free from Qt's
// system font database (get_qfont, courtroom.cpp:1263). Windows only: the mac /
// Linux font stores are laid out per-family and per-user, so probing them by
// file stem would resolve the wrong cut more often than the right one — those
// platforms keep the client font, exactly as today. Called off-thread.
func systemFontDirs() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	win := os.Getenv("WINDIR")
	if win == "" {
		return nil
	}
	return []string{filepath.Join(win, "Fonts")}
}
