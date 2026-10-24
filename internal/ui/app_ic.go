package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

func (a *App) applyThemeAsync() uint64 {
	name, dir := a.d.Prefs.Load().Theme()
	// Per-server theme binding: while this session declares one, it wins
	// over the global pick (set on connect from ServerWarmInfo.Theme;
	// Disconnect/resetSessionState clears it and re-applies the global).
	if a.srvMeta.themeBound != "" {
		name = a.srvMeta.themeBound
	}
	anims := a.d.Prefs.Load().PreferAnimated
	// The AO2 subtheme folder to prefer inside that theme. Read HERE, on the
	// render thread, with every other preference this apply needs — the goroutine
	// below must not take the preferences lock (the -race interleaving against the
	// debounced saver that PanelFonts is snapshotted for).
	sub := a.d.Prefs.Load().SubthemeName
	// Snapshot the per-panel overrides HERE, on the render thread, not inside the
	// goroutine: PanelFonts takes the preferences' read lock, and reading them
	// off-thread while the debounced saver marshals under its own lock is exactly
	// the interleaving -race exists to catch. The map returned is already a copy.
	panelFonts := a.d.Prefs.Load().PanelFonts()
	// The T1 budget, read HERE with every other preference this apply needs: the
	// theme-media allowance is a function of it (render.ThemeMediaByteCap) and the
	// goroutine below must not take the preferences lock.
	texBudgetMiB := a.d.Prefs.Load().TexBudgetMiB()
	gen := a.themeGen.Add(1)
	themeRes := &a.themeRes
	go func() {
		res := themeApply{
			gen:    gen,
			name:   name,
			images: map[string]*assets.Decoded{},
			sounds: map[string]string{},
		}
		// Users persist every shape of path (the root, the themes\ folder
		// itself, one theme inside it, quoted Copy-as-Path) — normalize
		// HERE so every apply resolves like the settings scanner does,
		// not only applies that happened to follow a finished scan.
		root, _ := normalizeThemeRoot(dir)
		// config.ExecutableDir, not a raw os.Executable + filepath.Dir: the loader
		// must walk the SAME root strings the picker (themeSearchRootsNow) and the
		// catalog scan (themecatalog.go) do, or a symlinked install resolves themes
		// from one spelling while the browser badges them from another. Memoized, so
		// this costs a string read after the first apply.
		exeDir := config.ExecutableDir()
		// The config root is resolved here, off-thread, like everything else this
		// goroutine needs. config.ConfigBaseDir is memoized after boot, so this is
		// a map-free string read in practice — but it is not on a draw path either
		// way (hard rule 2).
		configBase, _ := config.ConfigBaseDir()
		t, err := theme.Load(name, sub, themeLoadRoots(name, root, exeDir, configBase))
		if err == nil {
			// Publish the loaded theme itself so the effects-overlay roster resolve
			// walks THIS apply's ladder instead of re-loading the theme per resolve
			// (spec §2.4). Read-only from here on.
			res.themeObj = t
			// The AsyncAO extension tier. theme.Load already parsed it (atomically,
			// from this theme's own directories — never merged, never inherited from
			// the default theme), so this is a pointer copy on the goroutine that
			// owns the load.
			res.sidecar = t.Sidecar()
			// A sidecar that EXISTS and could not be parsed. theme.Load swallows it by
			// design (format rule 1 — reading never refuses), so this is the only place
			// the fact can be picked up at all, and until then nothing picked it up: the
			// theme applied with its whole AsyncAO tier missing and no surface said so.
			if err := t.SidecarErr(); err != nil {
				res.sidecarErr = themeSidecarRefusalLine(name, t.SidecarDir(), err)
				res.sidecarTip = themeSidecarRefusalTip(name, err)
			}
			// The theme's own art: PLANNED against the media budget first, then
			// loaded — so nothing a stranger declared is decoded before it is known to
			// fit, and an entry that does not fit becomes a self-describing placeholder
			// instead of a silent absence (design §Q2).
			res.themeID = themeSanitizeID(name)
			res.mediaPlan = planThemeMedia(res.sidecar, res.themeID, t.SidecarDir(), texBudgetMiB)
			res.loadThemeMediaSet(anims)
			res.iniKeys = t.KeyCount()
			res.probed = t.Dirs()
			res.fontPath = t.FontFile() // the theme's own bundled font, if any (#6)
			// #39: per-element families/sizes/bold (AO2-Client Courtroom::set_fonts,
			// courtroom.cpp:1188). Both the fonts/ walks and the face reads happen
			// here, on this goroutine — never on the render thread (hard rule 2).
			res.buildThemeFontTable(t, systemFontDirs())
			// Font intake: the AsyncAO tier's [fonts]/[fontbind] bind an element to a
			// FILE INSIDE THE THEME, which is what lets a native theme carry its own
			// type instead of naming a family and hoping. After the AO2 table, because
			// it overrides it; before applyPanelFonts, because the USER still wins.
			res.applySidecarFonts(res.sidecar, t.SidecarDir())
			res.msgCol, res.hasMsg = declaredFontInk(t.Font("message"))
			res.nameCol, res.hasName = declaredFontInk(t.Font("showname"))
			// loadOne decodes ONE already-located file into the stem's slot. Split out
			// of loadStem because the chatbox ladder below locates its files a
			// different way (one fixed directory, no candidate list) but must decode
			// and land them identically.
			loadOne := func(stem, path string) bool {
				data, rerr := os.ReadFile(path)
				if rerr != nil {
					return false
				}
				d, derr := assets.DecodeImage(data, anims)
				if derr != nil {
					return false
				}
				res.images[stem] = d
				return true
			}
			loadStem := func(stem string, candidates []string, exts []string) {
				for _, cand := range candidates {
					path, ok := t.FindAsset(cand, exts)
					if !ok {
						continue
					}
					if !loadOne(stem, path) {
						continue
					}
					if stem == themeStemChatbox {
						res.chatboxFile = filepath.Base(path)
						res.chatboxDir = filepath.Dir(path)
						res.chatboxStem = cand
					}
					return
				}
			}
			for stem, candidates := range themeImageStems() {
				loadStem(stem, candidates, themeImageExts)
			}
			for key, candidates := range themeButtonStems() {
				loadStem(themeBtnPrefix+key, candidates, themeButtonExts)
			}
			// The showname widen-and-swap ladder's two wider skins (4.7c). AO2 names
			// them by appending to the RESOLVED base skin's own path
			// (courtroom.cpp:3358/:3362), so they are looked up here — after the base
			// has resolved and in the base's own directory — rather than from the
			// static candidate table above.
			// MEMORY, because pinned theme pages sit on top of the T1 budget rather
			// than inside it (render.TextureStore.UploadPinned): this is bounded by
			// construction at exactly two files, and they are the same class and — on
			if res.chatboxStem != "" {
				for _, rung := range chatboxLadderRungs {
					if path, ok := theme.FindAssetIn(res.chatboxDir, rung.fileStem(res.chatboxStem), themeImageExts); ok {
						loadOne(rung.texStem(), path)
					}
				}
			}
			// Readability guard: drop theme ink that has no contrast
			// against the skin it ships with (see the constants above).
			if box := res.images[themeStemChatbox]; box != nil && len(box.Frames) > 0 {
				skin := avgSkinLuma(box.Frames[0], lumaSampleStep)
				var dropped []string
				if res.hasMsg && absInt(colLuma(res.msgCol)-skin) < minInkSkinContrast {
					res.hasMsg = false
					dropped = append(dropped, "message")
				}
				if res.hasName && absInt(colLuma(res.nameCol)-skin) < minInkSkinContrast {
					res.hasName = false
					dropped = append(dropped, "showname")
				}
				if len(dropped) > 0 {
					res.inkGuard = fmt.Sprintf("theme %s color unreadable on its chatbox skin (luma %d) — using client default",
						strings.Join(dropped, "+"), skin)
				}
			}
			// Courtroom geometry (the part that makes a theme LOOK like
			// itself): rects in design-space pixels, plus the emote grid
			// cell metrics.
			res.layout = make(map[string]theme.Rect, len(themeLayoutKeys))
			for _, key := range themeLayoutKeys {
				if r, ok := t.ElementRect(key); ok {
					res.layout[key] = r
				}
			}
			// Then AO2's own per-key fallback (#21). It must run AFTER the loop
			// above so a theme's own rect always wins over the stock default.
			// Its position relative to the unbound report below is immaterial —
			// unboundDesignKeys reads the *theme.Theme, not this map.
			applyAO2DefaultRects(res.layout)
			// Then the AsyncAO tier's own [overrides], which are the author's edits
			// laid OVER that result (docs/THEME-FORMAT.md:176-190). AFTER the backstop
			// for the same reason the backstop runs after the theme's file: an
			// override binds to "the AO2 design", and for a theme whose
			// courtroom_design.ini declares only `courtroom` + `viewport` the design
			// IS the backstop — running it first would leave every row with nothing
			// to bind to. See foldSidecarOverrides (themeoverrides.go) for why this
			// tier existed unapplied until now, and what it was doing to the elements
			// anchored on top of it.
			res.tabBarSeeded = seedTabBarThroughOverrides(res.layout, res.sidecar)
			// #21 label 16: measure the theme's own art behind each font element, so
			// the render thread can guard the per-element ink without touching a
			// decode buffer. AFTER the defaults, so an element positioned by AO2's
			// stock rect is measured where it will actually draw.
			res.sampleThemeBackdrops()
			// Unbound-key report (#21), to the DEBUG LOG and never to the player's
			// screen. AO2 does the same thing inverted: set_size_and_pos qWarns for
			// every identifier the THEME is silent about (courtroom.cpp:1336), while
			// the defect this issue found is keys the theme HAS and the client
			// ignores. Runs here, on the apply goroutine, once per theme apply.
			res.unbound = unboundDesignKeys(t)
			res.emoteCell = designPair(t, "emote_button_size", defaultEmoteCellPx, defaultEmoteCellPx)
			res.emoteGap = designPair(t, "emote_button_spacing", defaultEmoteGapPx, defaultEmoteGapPx)
			// effects_icon_size (courtroom.cpp:979). Defaults to 0,0 — "the theme is
			// silent" — rather than to a stock number, because AO2's own default theme
			// declares it and a theme that does not simply has no opinion; the picker
			// then falls back to its own row metrics.
			res.effectsIcon = designPair(t, "effects_icon_size", 0, 0)
			// showname_align: a plain string design value, so DesignValue rather
			// than ElementRect. A missing key yields "" and parseShownameAlign's
			// default arm, which is AO2's own `else` (courtroom.cpp:3352).
			alignRaw, _ := t.DesignValue(shownameAlignKey)
			res.shownameAlign = parseShownameAlign(alignRaw)
			// showname_extra_width: a bare integer, so DesignValue + the same
			// tolerant Atoi AO2 uses (QString::toInt() yields 0 for anything it
			// cannot parse, and AO2 feeds the result straight into the ladder's
			// `extra_width > 0` test). A missing key, a blank one and a garbage one
			// therefore all land on 0 = ladder disabled, which is AO2's answer too.
			extraRaw, _ := t.DesignValue(shownameExtraWidthKey)
			res.shownameExtra = parseShownameExtraWidth(extraRaw)
			// Char select's twelve keys, resolved against the embedded default-theme
			// table (#20). Off-thread with the rest of the INI work — hard rules 1
			// and 2 — and landed by pollThemeApply.
			res.charSel = readCharSelectDesign(t)
			// The QSS palette ("css stuff"): AO2 ≥ 2.10 themes color the
			// client through courtroom_stylesheets.css.
			if path, ok := t.FindAsset("courtroom_stylesheets", []string{".css"}); ok {
				if data, rerr := os.ReadFile(path); rerr == nil {
					res.palette = theme.ParseStylesheet(data)
				}
			}
		}
		// Sound names resolve even with no theme on disk: the stock
		// fallbacks keep WT/CE/verdict/modcall audible on bare installs.
		for _, sk := range themeSoundKeys {
			val := sk.fallback
			if t != nil {
				for _, alias := range sk.aliases {
					if v, ok := t.SoundName(alias); ok && v != "" {
						val = v
						break
					}
				}
			}
			if val != "" {
				res.sounds[sk.key] = val
			}
		}
		if t != nil {
			for _, pk := range themePenaltyKeys {
				if v, ok := t.PenaltyValue(pk); ok && v != "" {
					res.sounds[pk] = v
				}
			}
		}
		// The USER's own per-panel fonts, OUTSIDE the theme-loaded branch — which is
		// the whole point of it living here.
		// The theme's directories are used when there IS one, so a font it ships can
		// still be picked; with no theme, the user's own font folder and the system
		// folders are the search path, and both work on their own.
		var themeDirs []string
		if t != nil {
			themeDirs = t.Dirs()
		}
		// Created before resolving, so the drop folder is a real search tier on this
		// very pass and a path the warning below can name.
		res.userFontsDir = ensureUserFontsDir()
		res.applyPanelFonts(panelFonts, themeDirs, systemFontDirs())
		// The missing-font warning's wording depends on whether this installation
		// has ANY fonts on disk, which is a disk probe — decided here rather than on
		// the render thread. Skipped when nothing is missing, the common case.
		if len(res.themeFontsMissing) > 0 {
			anyOnDisk := t != nil && t.HasAnyFontFile()
			res.themeFontWarn = themeFontWarning(res.themeFontsMissing, anyOnDisk, res.userFontsDir)
		}
		// THE IMPORT REPORT (themereport.go). Built HERE — after the fonts, which
		// are one of its three inputs — because every input is already on this
		// goroutine and the render thread must never walk the notes (hard rule 2).
		// It is the surface that stops Sidecar.Notes() being collected and read by
		// nothing but tests, its own metadata degrade included: a
		// degrade-with-a-note whose note reaches nobody is a silent truncation with
		// extra steps.
		res.report = buildThemeReport(res.sidecar, res.themeFontsMissing, res.unbound)
		// Newest-wins publish: never overwrite a higher-gen result (this
		// load may have been outraced by a later pick).
		for {
			old := themeRes.Load()
			if old != nil && old.gen > gen {
				res.releaseDecoded() // outraced by a later pick: this load never lands
				return
			}
			if themeRes.CompareAndSwap(old, &res) {
				if old != nil {
					old.releaseDecoded() // displaced before the render thread consumed it
				}
				return
			}
		}
	}()
	return gen
}

// pollThemeApply lands theme pieces on the render thread: upload (or
// drop) the chatbox skin, adopt the colors, force a chat re-raster, and
// report what happened (settings status line + debug log).
func (a *App) pollThemeApply() {
	res := a.themeRes.Swap(nil)
	if res == nil {
		return
	}
	// Consume-side newest-wins: a slower OLDER-gen load can publish AFTER a newer
	// one was already consumed (the publish guard only protects an unconsumed
	// result). Landing it would revert to the stale theme and — because an older
	// boot/global "default" apply carries an empty fontPath — clear the chat font
	// (the "applies then reverts" bug). Drop anything older than what's applied.
	if res.gen < a.themeAppliedGen {
		res.releaseDecoded()
		return
	}
	a.themeAppliedGen = res.gen
	// Upload every loaded stem into the PINNED tier — theme chrome must
	// never lose an eviction fight against streaming sprites (the cause
	// of the black-flashing backdrop / glitching buttons) — and drop
	// residents the new theme doesn't ship (plain stems and btn/ art).
	for stem, d := range res.images {
		if err := a.d.Store.UploadPinned(themeTexKey(stem), d); err == nil {
			a.themeTex[stem] = true
		}
	}
	for stem := range a.themeTex {
		if res.images[stem] == nil {
			a.d.Store.Remove(themeTexKey(stem))
			delete(a.themeTex, stem)
		}
	}
	// Geometry: pristine design rects kept aside, the user's layout-editor
	// overrides applied on a copy, scaled cache invalidated.
	// The server-tab strip's synthesized key was seeded on the apply goroutine,
	// into this same pristine map, before the [overrides] fold (see there). It is
	// in themeRectsOrig for reset/undo to restore from, and in the copy below for
	// applyRectOverrides — which only rewrites keys that already exist — to
	// re-apply a drag after a reload. Only the FLAG crosses here.
	a.tabBarSeeded = res.tabBarSeeded
	a.themeRectsOrig = res.layout
	rects := make(map[string]theme.Rect, len(res.layout))
	for k, v := range res.layout {
		rects[k] = v
	}
	a.themeRects = a.applyRectOverrides(rects)
	a.themeEmoteCell, a.themeEmoteGap = res.emoteCell, res.emoteGap
	// The effects picker's per-row icon size, and a roster/icon-cache drop: the new
	// theme contributes different names, properties AND art paths, so a cached
	// roster (or an icon page keyed by row index) from the old one would be wrong.
	a.overlays.overlayIconSize = res.effectsIcon
	a.overlays.overlayTheme = res.themeObj
	a.overlays.overlayRosters = nil
	a.overlays.overlayIconPagesReset()
	// Lands beside the geometry it belongs to. A themeless apply carries the zero
	// value, which is Left — so dropping a theme restores AO2's own default rather
	// than leaving the previous theme's alignment behind.
	a.themeShownameAlign = res.shownameAlign
	a.themeShownameExtra = res.shownameExtra
	// Char select's own geometry lands beside the courtroom's (#20). No
	// applyRectOverrides pass: these keys are deliberately not editable — they are not
	// in a.themeRects, so the layout editor never sees them and there is nothing to
	// re-apply. Both scaled caches then go together (invalidateThemeCanvases).
	a.charSelDesign = res.charSel
	// The free-element tier lands with the geometry it decorates, and BEFORE the
	// canvases are invalidated: the next themeLayoutIn rebuild is what bakes it, and
	// a sidecar landing after the invalidation would not be baked until something
	// else happened to invalidate again. nil (a stock AO2 theme) clears the previous
	// theme's elements, which is the whole of the theme-swap prune for this tier.
	a.elemBake.themeSidecar = res.sidecar
	// …and immediately back again if the EDITOR owns this theme's model. Applies are
	// not user-initiated — healTheme re-kicks one on any T1 eviction of a theme
	// texture, the texture-filter row kicks one, boot kicks two — so without this line
	// every unsaved edit is discarded by a background event, with nothing on screen to
	// say why. THE ONE NAMED CALL SITE (themeeditor.go carries the argument); it is
	// guarded on the theme NAME, so switching themes mid-session correctly loses the
	// document rather than pasting the old theme's elements onto the new one.
	a.reclaimThemeDoc()
	// The theme's own art, landed BEFORE the canvases are invalidated for the
	// same reason the sidecar is: the next themeLayoutIn rebuild is what bakes the
	// elements, and an element resolving its page against a key that has not landed
	// yet would bake a nil and paint nothing until something else invalidated.
	a.landThemeMedia(res)
	// The refusal surface lands with it, and the acknowledgement is keyed on the
	// REASON rather than re-armed unconditionally.
	if a.fxPool.themeSidecarErr != res.sidecarErr {
		a.fxPool.themeSidecarErrAck = false
	}
	a.fxPool.themeSidecarErr, a.fxPool.themeSidecarTip = res.sidecarErr, res.sidecarTip
	a.invalidateThemeCanvases()
	a.themeSounds = res.sounds
	a.toolbox.themeAt = time.Now() // restart the theme-art animation clock
	// The element clock groups re-anchor WITH it (design §Q3), so a theme reload
	// restarts every phase together instead of leaving the incoming theme's motion
	// mid-cycle. A nil sidecar zeroes the pool, which is what makes a swap back to a
	// stock AO2 theme run at exactly the shared anchor again.
	a.applyThemeClocks(res.sidecar)
	// Apply (or restore) the stylesheet palette; label textures are
	// color-keyed, so purge the text cache to re-rasterize in new colors.
	a.themePalette = res.palette // remember it so a chrome-preset change can re-overlay it (#M3)
	applyThemePalette(res.palette)
	a.ctx.purgeTextCache()
	a.pushRealizationToRoom()
	a.themeMsgCol, a.themeHasMsg = res.msgCol, res.hasMsg
	a.themeNameCol, a.themeHasName = res.nameCol, res.hasName
	a.raster.rasterText = "" // re-raster the current message with theme colors
	a.toolbox.themeAppliedName = res.name
	// Theme font (#6): an AO theme that ships its own .ttf now applies it (below a
	// manual font / the dyslexia toggle). Re-resolve only when it actually changed,
	// so a same-font theme switch doesn't re-kick the off-thread font read.
	if a.themeFontFile != res.fontPath {
		a.themeFontFile = res.fontPath
		a.applyFontConfig()
	}
	// Per-panel font diagnostics, for the Settings screen: the file names each
	// element resolved to (so an empty box can say what it is already using) and
	// the families that resolved to nothing (so a typo is visible where it was
	// typed, not only in the debug log).
	a.themeFaceNames = res.faceNames
	a.panelFontMissing = res.panelFontsMissing
	a.landThemeFonts(res)
	// A theme whose fonts are missing renders in the client's own face and says
	// nothing about it — from the user's side that reads as "the client got the
	// font wrong", which is exactly how it was reported. Say so, once per apply,
	// on screen AND in the debug log, and name the fix rather than the fault.
	a.themeFontWarn = res.themeFontWarn
	if res.themeFontWarn != "" {
		// The debug log ALWAYS, the modal at most once per theme. A theme with
		// missing fonts has them missing on every apply, so warning every time
		// would make the client unusable for anyone not intending to fix it — and
		// the second showing is what teaches people to dismiss without reading.
		a.debug.push(res.themeFontWarn)
		a.maybeWarnMissingFonts(res.name, res.themeFontsMissing, res.themeFontWarn, res.userFontsDir)
	}
	line := themeApplySummary(res)
	settings.statusLine = clampLine(line)
	a.debug.push(line)
	if res.inkGuard != "" {
		a.debug.push(res.inkGuard)
	}
	// A refused sidecar goes to the debug log on EVERY apply (pushDebug's ×N collapse
	// keeps a re-kicked heal from flooding it) and takes over the Settings status
	// line, which is the surface the user is looking at when they pick a theme. The
	// chip is the third surface and the only one that survives leaving that screen.
	if res.sidecarErr != "" {
		a.debug.push(res.sidecarErr)
		settings.statusLine = clampLine(res.sidecarErr)
	}
	// THE IMPORT REPORT (themereport.go). It SUBSUMES the #21 unbound-key
	// line, which used to be pushed to the debug log here and nowhere else: the
	// report carries it as one entry, still logged, now also counted on a surface
	// a human looks at. Every other input to it — the sidecar's own degrade notes,
	// the families that resolved to no file — was in the same position, computed
	// and thrown at a log, which is how the format's "degrade with a note" promise
	// came to have no note in it.
	a.noteThemeReport(res.name, res.report)
	// #21: drop a stale ms_chatlog drag override. Before the themeSlots registry
	// that key was ingested AND editable, so a user could drag it — and it fed the
	// OOC log, because the layout treated it as a server_chatlog alias. It is now
	// AO2's debug-log rect (courtroom.cpp:831) and inert, so a saved override
	// would silently place a box for a different widget. Self-limiting rather than
	// stamped: the key is no longer editable, so it cannot come back, and the
	// probe is one map read per theme apply.
	if res.name != "" && a.d.Prefs.Load().HasThemeRectOverride(res.name, "ms_chatlog") {
		a.d.Prefs.ClearThemeRectOverride(res.name, "ms_chatlog")
		a.debug.push("cleared a stale ms_chatlog layout override (it now means AO2's debug log)")
	}
	// Theme chrome uploads PINNED (above), so applying a theme can evict
	// emote-button art from T1. The gen-keyed page cache re-Gets on the
	// bumped generation, but evicted slots then sit in the text fallback
	// until demandAsset's per-slot retry window elapses. Force a fresh
	// re-demand so the emote grid heals immediately after a theme switch
	// (the same invariant pollCharINI applies on a character change).
	a.rcache.emoteBtnOff, a.rcache.emoteBtnOn, a.rcache.emoteIconPages = nil, nil, nil
	a.emotepick.emoteAsk, a.rcache.emoteIconAsk = nil, nil
	// #35: last, because it needs the geometry landed above (themeRectsOrig) and
	// because a resize is the most disruptive thing this function can do. No-op
	// unless THIS apply's generation is the one a user-initiated pick armed —
	// note that the stale-generation guard above already returned for anything
	// older than what is applied, so such a landing can never spend the arming.
	a.maybeResizeToThemeDesign(res.gen)
	// And the copy-for-editing hand-off, armed by the same kind of one-shot:
	// "Copy for editing" is one act, and the editor can only open over a theme
	// that has APPLIED — which is this landing (themecopy.go).
	a.maybeOpenEditorAfterCopy(res.gen)
}

// ensureThemeForSession re-applies the theme whenever the session's
// binding (or its absence) disagrees with what is on screen — connect,
// tab switches, and disconnect funnel through here. No-op when the
// right theme is already applied (loads are async but not free).
func (a *App) ensureThemeForSession() {
	want := a.srvMeta.themeBound
	if want == "" {
		want, _ = a.d.Prefs.Load().Theme()
	}
	if want != "" && want != a.toolbox.themeAppliedName {
		a.applyThemeAsync()
	}
}

// themeUnboundReportCap bounds the unbound-key debug line (hard rule 4). Twelve
// names plus a "+N more" tail fits one debug row at every window width and is
// enough to recognise WHICH family of keys a theme is losing; the exact list is a
// fixture test's job, not a log line's.
const themeUnboundReportCap = 12

// unboundDesignKeys lists the theme's rect-shaped design keys that reach no
// widget: not a themeSlots row, not a deliberate deferral, not a char-select key.
// "" when the theme is fully covered.
// This is the observability that makes "a missing rect draws nothing" supportable
// — a player who loses a control can be told which key their theme declares that
// AsyncAO does not read. Allocates, and walks every design key: apply goroutine
// only (hard rule 2).
func unboundDesignKeys(t *theme.Theme) string {
	var names []string
	for key := range t.DesignKeys() {
		if _, ok := t.ElementRect(key); !ok {
			continue // pairs, colours and scalars are not widget rects
		}
		// A row that exists but paints NOTHING is exactly what this report is for —
		// "reaches no widget" is about pixels, not about table membership. Skipping
		// every key with a row made the report blind to the theme in the issue,
		// which has a row for all ~100 of its rects and paints barely half.
		if s := themeSlotFor(key); s != nil && s.state != slotStateInert {
			continue
		}
		if _, deferred := themeSlotDeferred[key]; deferred {
			continue
		}
		if _, charSel := charSelectDefaultRects[key]; charSel {
			continue
		}
		names = append(names, key)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	total := len(names)
	extra := 0
	if total > themeUnboundReportCap {
		extra = total - themeUnboundReportCap
		names = names[:themeUnboundReportCap]
	}
	line := fmt.Sprintf("theme declares %d design rect(s) no widget reads: %s", total, strings.Join(names, ", "))
	if extra > 0 {
		line += fmt.Sprintf(" (+%d more)", extra)
	}
	return line
}

// themeApplySummary turns one apply into a human-readable verdict, so
// "nothing happened" is always distinguishable from "applied fine but this
// theme only restyles the courtroom".
func themeApplySummary(res *themeApply) string {
	// #39: name the font files that actually resolved, so "did the theme's font
	// resolve?" is answerable from the status line without a rebuild — the whole
	// reason the previous attempt couldn't be verified in the field.
	fonts := ""
	if len(res.faceNames) > 0 {
		fonts = " + fonts: " + strings.Join(res.faceNames, ", ")
	}
	switch {
	case res.chatboxFile != "":
		return fmt.Sprintf("Theme %q applied: %s + %d theme images + %d INI keys%s (%s)",
			res.name, res.chatboxFile, len(res.images)-1, res.iniKeys, fonts, res.chatboxDir)
	case len(res.images) > 0 || res.iniKeys > 0:
		return fmt.Sprintf("Theme %q applied: %d theme images + %d INI keys%s, no chatbox skin (flat panel)",
			res.name, len(res.images), res.iniKeys, fonts)
	default:
		return fmt.Sprintf("Theme %q: nothing found — probed %s",
			res.name, strings.Join(res.probed, " ; "))
	}
}

// themePage fetches a resident theme texture page through a generation-
// keyed cache (the grid trick): while the store generation is unchanged,
// repeat lookups cost one plain map probe — no LRU lock, no recency churn
// from overlay draws that run every frame. Misses heal (paced) and cache
// negatively until the next upload/eviction bumps the generation.
func (a *App) themePage(stem string) (*render.TexturePage, bool) {
	if !a.themeTex[stem] {
		return nil, false
	}
	gen := a.d.Store.Generation()
	if gen != a.themePagesGen {
		clear(a.themePages)
		a.themePagesGen = gen
	}
	if page, cached := a.themePages[stem]; cached {
		return page, page != nil
	}
	page, ok := a.d.Store.Get(themeTexKey(stem))
	if !ok || len(page.Frames) == 0 {
		a.themePages[stem] = nil
		a.healTheme()
		return nil, false
	}
	a.themePages[stem] = page
	return page, true
}

// now is the frame's clock snapshot (real time for callers outside a
// frame, e.g. headless tests).
func (a *App) now() time.Time {
	if a.budget.frameNow.IsZero() {
		return time.Now()
	}
	return a.budget.frameNow
}

// themeElapsed is the animation clock for looping theme art: time since
// the theme applied, so every animated stem (chatbox, buttons, backdrops)
// steps with pageFrameLoop instead of freezing on Frames[0]. Reads the
// frame snapshot — themed frames step this at ~10 draw sites.
func (a *App) themeElapsed() time.Duration {
	if a.toolbox.themeAt.IsZero() {
		return 0
	}
	return a.now().Sub(a.toolbox.themeAt)
}

// themeFrame picks the current animation frame for a theme page — static pages
// cost one len check, animated ones loop on the theme clock (and mark the
// frame's animated-chrome census so the static skip keeps stepping them).
// `loop` is true because theme chrome loops by definition — a chatbox skin has
// no "finished" state. That is what themeFrame always did (pageFrameLoop); it
// is now said in the shared function's own vocabulary.
func (a *App) themeFrame(page *render.TexturePage) *sdl.Texture {
	if page == nil || len(page.Frames) == 0 {
		return nil // matches elementFrame: a page with no frames is not a texture
	}
	if len(page.Frames) == 1 {
		return page.Frames[0]
	}
	if a.fxPool.themeFXFrozen {
		return page.Frames[0] // ReduceMotion, latched once per frame (reduceMotionNow)
	}
	idx, live := elementFrameIndex(page, a.themeElapsed(), true)
	if live {
		a.NoteAnimating()
	}
	return page.Frames[idx]
}

// pushRealizationToRoom hands the courtroom the resolved realization sound
// URL (the state machine plays it at message-display time, where AO2's
// handle_ic_message does — the theme INI itself lives UI-side).
func (a *App) pushRealizationToRoom() {
	if a.link.room == nil {
		return
	}
	if name := a.themeSounds["realization"]; name != "" {
		a.link.room.RealizationSFX = a.link.urls.SFX(name) // AssetType: SFX (realization)
	} else {
		a.link.room.RealizationSFX = ""
	}
}

// healTheme re-runs the theme load when T1 evicted a theme texture the UI
// still needs — paced to one ask per themeHealPeriod (healScenery pattern).
func (a *App) healTheme() {
	if time.Since(a.themeHealAt) > themeHealPeriod {
		a.themeHealAt = time.Now()
		a.applyThemeAsync()
	}
}

// logSpeakerAnimDiag logs the active speaker sprite's animation stats to the
// debug log once per base change (#110 frame-rate diagnostic). The debug panel
// cache inspector shows the same numbers live; this puts a record in the log.
func (a *App) logSpeakerAnimDiag() {
	if a.d.Viewport == nil {
		return
	}
	si, ok := a.d.Viewport.SpeakerAnimInfo()
	if !ok {
		return
	}
	if si.Base == a.frame.lastSpeakerDiagBase {
		return // same base already logged — don't flood the ring
	}
	a.frame.lastSpeakerDiagBase = si.Base
	a.debug.push(fmt.Sprintf("speaker anim: %s · %d/%d frames · delays %d/%d/%d ms (min/avg/max)",
		si.Base, si.Kept, si.Source,
		si.MinDelay/time.Millisecond, si.AvgDelay/time.Millisecond, si.MaxDelay/time.Millisecond))
}

// drainThumbs uploads loaded low-q sprite thumbnails into T1 under their
// thumb:// keys (render thread — Upload's home). Bounded per frame so a burst
// of thumb loads can't stall a frame; the channel itself is bounded too.
func (a *App) drainThumbs() {
	th := a.d.Manager.Thumbs()
	if th == nil {
		return
	}
	const perFrame = 2 // thumbs are tiny; two uploads/frame clears any real burst
	for i := 0; i < perFrame; i++ {
		select {
		case r := <-th.Results():
			if r.Asset != nil {
				_ = a.d.Store.Upload(render.ThumbKeyPrefix+r.Base, r.Asset)
			}
		default:
			return
		}
	}
}

// drainWarnings empties the manager's missing-asset lane (bounded by its
// channel cap), feeding the §4 in-client report (the menu-bar count chip).
func (a *App) drainWarnings() {
	// The on-screen report is opt-in (default OFF — it reads as noise on
	// sparse-pack servers); read the toggle once per drain. We still drain
	// the channel (it's bounded) and log every failure to the debug overlay
	// regardless, so nothing is lost — only the chip is gated.
	show := a.d.Prefs.Load().ShowAssetWarnings
	for {
		select {
		case w := <-a.d.Manager.Warnings():
			// A conclusively-missing char sprite also feeds the message
			// ceremony: a preanimation that can never arrive must not hold
			// the stage for the full PreanimTimeout (AO2-Client skips missing
			// preanims instantly — Courtroom.NotifyAssetMissing). One Manager
			// serves every room, so this is the single relay point; each room
			// compares the base against its own current message, so wrong-room
			// bases are a string-compare no-op.
			if w.Type == assets.AssetTypeCharSprite {
				if a.link.room != nil {
					a.link.room.NotifyAssetMissing(w.Base)
				}
				if a.splitRoom != nil {
					a.splitRoom.NotifyAssetMissing(w.Base)
				}
				if a.sel.replayRoom != nil {
					a.sel.replayRoom.NotifyAssetMissing(w.Base)
				}
				if a.sel.makerPreviewRoom != nil {
					a.sel.makerPreviewRoom.NotifyAssetMissing(w.Base)
				}
				// missingno: relay the render-side confirmed-missing signal so the
				// viewport draws the AO2 placeholder for this base instead of a stale
				// hold-previous. This is the ONLY new bridge — it reuses the existing
				// droppable Warning lane (no new channel/goroutine, §17.4). A Warning
				// fires ONLY on full chain exhaustion (reportMissing: every format of
				// every spelling 404'd), and speculative Markov prefetches suppress it,
				// so a predicted-but-absent base is never marked. Both this and the
				// viewport read run on the render thread — the set carries no mutex.
				if a.d.Store != nil {
					a.d.Store.MarkMissing(w.Base)
				}
			}
			// A conclusively-missing DESK overlay means this background ships no
			// desk image for this position, and AO2's set_scene answers that by
			// hiding the desk layer outright — there is deliberately no fallback,
			// unlike the background arm beside it
			// (../AO2-Client/src/courtroom.cpp:4628-4634). Without this relay the
			// viewport's sticky scenery gate kept painting the PREVIOUS room's desk
			// forever, because a 404'd base never becomes resident (#44). Same
			// droppable Warning lane, no new channel/goroutine (§17.4); each room
			// string-compares against its own scene, so wrong-room bases are a no-op.
			if w.Type == assets.AssetTypeDeskOverlay {
				if a.link.room != nil {
					a.link.room.NotifyDeskMissing(w.Base)
				}
				if a.splitRoom != nil {
					a.splitRoom.NotifyDeskMissing(w.Base)
				}
				if a.sel.replayRoom != nil {
					a.sel.replayRoom.NotifyDeskMissing(w.Base)
				}
				if a.sel.makerPreviewRoom != nil {
					a.sel.makerPreviewRoom.NotifyDeskMissing(w.Base)
				}
				// The render-side half: syncAnimDesk releases the held desk layer on
				// this flag, so nothing is blitted (AO2's ui_vp_desk->hide()). The set
				// self-heals — clearMissing runs on upload, so a desk that later
				// appears (server repack, local mount added) recovers with no restart.
				if a.d.Store != nil {
					a.d.Store.MarkMissing(w.Base)
				}
			}
			// Scenery + audio conclusive-404s feed the strict-sequencing gate's
			// shared missing set (Courtroom.NotifyAssetMissing records the base), so
			// a message holding on a background / chatbox / speedlines / SFX / blip
			// releases on the miss signal instead of hanging (SequentialWait has no
			// timeout). Deliberately NO Store.MarkMissing here: unlike a char sprite
			// (missingno) or a desk (hide the layer), a missing background / chatbox
			// / sound has no render-side placeholder to swap in — the renderer's own
			// fallback (black bg / normal chatbox / silent) already covers it.
			switch w.Type {
			case assets.AssetTypeBackground, assets.AssetTypeMisc, assets.AssetTypeSFX, assets.AssetTypeBlip, assets.AssetTypeShoutBubble:
				if a.link.room != nil {
					a.link.room.NotifyAssetMissing(w.Base)
				}
				if a.splitRoom != nil {
					a.splitRoom.NotifyAssetMissing(w.Base)
				}
				if a.sel.replayRoom != nil {
					a.sel.replayRoom.NotifyAssetMissing(w.Base)
				}
				if a.sel.makerPreviewRoom != nil {
					a.sel.makerPreviewRoom.NotifyAssetMissing(w.Base)
				}
			}
			line := "Missing asset: " + w.Base
			if len(w.Tried) > 0 {
				line += " (tried " + strings.Join(w.Tried, " ") + " — see Settings → formats)"
			}
			a.debug.push(line) // unconditional: the Debug ▸ Log tab is the detail view
			if show {
				// #27: the chip, NOT warnLine. See assetmisschip.go for why the
				// shared toast channel was the wrong surface for this class.
				a.noteAssetMiss(w.Base, w.Type == assets.AssetTypeDeskOverlay, time.Now())
			}
		default:
			return
		}
	}
}

// drainMusicFailures empties the manager's transient-music-failure lane
// (bounded) and surfaces the newest to the jukebox warn line (§1.1). Runs on
// the render thread alongside drainWarnings, so touching a.warnLine here is
// race-free. A big track that timed out (or a backing-off host) was otherwise
// completely silent — no playback and no error. Always shown (unlike
// the opt-out asset banner): a music track the user actively /play'd failing
// is a direct, expected-feedback event, not sparse-pack noise.
func (a *App) drainMusicFailures() {
	for {
		select {
		case f := <-a.d.Manager.MusicFailures():
			line := "Music didn't load (network) — " + shortMusicName(f.URL)
			a.debug.push(line + ": " + f.Err.Error())
			a.jukeWarn(line)
		default:
			return
		}
	}
}

// shortMusicName trims a music URL to its final path segment for the warn line
// so a long /play CDN URL doesn't overflow the banner (clampLine also caps it).
func shortMusicName(url string) string {
	if i := strings.LastIndexByte(url, '/'); i >= 0 && i+1 < len(url) {
		return url[i+1:]
	}
	return url
}

// warnActive reports whether the warning banner should still draw.
func (a *App) warnActive() bool {
	return a.warnLine != "" && time.Since(a.warnAt) < warnShowDuration
}

// spriteFX builds the optional sprite colour-FX struct from the user prefs
// (all off by default). A handful of uncontended RLocks once per frame — far
// cheaper than any snapshot/cache layer, and the render path stays 0-alloc
// regardless. The colour is only
// fetched when the solid wash is actually active. Shared by the live + replay
// render paths.
func (a *App) spriteFX() render.SpriteFX {
	if a.d.Prefs.Load().DisableEffects {
		// Master off-switch: every wash, motion and lighting extra goes, but
		// UserScaling STAYS — it is how big a sprite is drawn (char.ini / the
		// enlarging rule), not eye-candy, and zeroing it would silently resize
		// every character the moment a player asked for a calmer screen.
		return render.SpriteFX{UserScaling: courtroom.ScalingMode(a.d.Prefs.SpriteScalingMode())}
	}
	fx := render.SpriteFX{
		Rainbow:         a.d.Prefs.Load().RainbowSprites,
		Solid:           a.d.Prefs.Load().SpriteSolidTint,
		Glow:            a.d.Prefs.Load().RainbowSpriteGlow,
		PairDesync:      a.d.Prefs.Load().RainbowPairDesync,
		PerCharHue:      a.d.Prefs.Load().RainbowPerChar,
		Wobble:          a.d.Prefs.Load().SpriteWobble,
		Spin:            a.d.Prefs.Load().SpriteSpin,
		ShoutPunch:      a.d.Prefs.Load().ShoutPunch,
		Entrance:        a.d.Prefs.Load().AnimateEntrances,
		DoF:             a.d.Prefs.Load().DepthOfField,
		Spotlight:       a.d.Prefs.Load().Spotlight,
		IdleBreath:      a.d.Prefs.Load().IdleBreath && !a.d.Prefs.Load().ReduceMotionOn, // #122; ReduceMotion suppresses (accessibility)
		BreathBob:       a.d.Prefs.Load().BreathBobOn(),
		BreathScale:     a.d.Prefs.Load().BreathScaleOn(),
		Reflection:      a.d.Prefs.Load().Reflection,
		Speed:           a.d.Prefs.Load().RainbowSpriteSpeed,
		Vividness:       a.d.Prefs.Load().RainbowSpriteVividness,
		SpotlightLevel:  a.d.Prefs.Load().SpotlightLevel(),
		BreathAmp:       a.d.Prefs.Load().BreathAmp(),
		BreathSpeed:     a.d.Prefs.Load().BreathSpeed(),
		ReflectStrength: a.d.Prefs.Load().ReflectStrength(),
		UserScaling:     courtroom.ScalingMode(a.d.Prefs.SpriteScalingMode()), // #21 label 15: Auto defers to char.ini, then to the enlarging rule
	}
	if fx.Solid && !fx.Rainbow { // colour only matters for the solid wash, and rainbow wins
		rgb := a.d.Prefs.Load().SpriteTintColorRGB()
		fx.SolidR = uint8(rgb >> 16 & 0xFF)
		fx.SolidG = uint8(rgb >> 8 & 0xFF)
		fx.SolidB = uint8(rgb & 0xFF)
	}
	return fx
}

// cycleWeather advances the #124 ambient weather (None → Snow → Rain → Sakura → Embers → None)
// and flashes the new name — the hands-free keybind.
func (a *App) cycleWeather() {
	w := (a.d.Prefs.WeatherType() + 1) % int(render.WeatherCount)
	a.d.Prefs.SetWeatherType(w)
	a.warnLine = "Weather: " + render.WeatherName(render.Weather(w))
	a.warnAt = time.Now()
}

// postFX mirrors the user's #10 post-processing toggles onto the viewport each frame.
func (a *App) postFX() render.PostFX {
	if a.d.Prefs.Load().DisableEffects { // master off-switch: no overlay over the stage at all
		return render.PostFX{}
	}
	return render.PostFX{
		Vignette:  a.d.Prefs.Load().PostVignette,
		Scanlines: a.d.Prefs.Load().PostScanlines,
		Grain:     a.d.Prefs.Load().PostGrain,
		CRT:       a.d.Prefs.Load().PostCRT,
	}
}

// applySpriteOverrides lets the user's drag positions win over the
// message's offsets every frame (one map probe per visible layer; free
// while no overrides exist).
// applySpriteOverrides applies them to the ACTIVE room. Every other live room
// goes through applySpriteOverridesTo — see there for why they must.
func (a *App) applySpriteOverrides() { a.applySpriteOverridesTo(a.link.room) }

// applySpriteOverridesTo applies the session's hide/move overrides to ONE room.
// Parameterised because these are USER-level choices — the Hide-desk toggle, the
// per-character Missingno hides, the per-character offsets — and a
// user who hides the desk means everywhere, not "in whichever room happens to be
// a.room". The pinned/split pane, the replay room and the scene-maker preview all
// drive their own Update and were silently exempt, so the desk stayed visible in
// the pinned pane with the toggle on.
func (a *App) applySpriteOverridesTo(room *courtroom.Courtroom) {
	if room == nil {
		return
	}
	hideDesk := a.d.Prefs.Load().HideDesk
	if !hideDesk && len(a.roster.spriteOv) == 0 && len(a.dialogs.hiddenSprites) == 0 {
		return // nothing hidden/moved (the common case): one pref read, then out
	}
	sc := &room.Scene
	if hideDesk {
		sc.ShowDesk = false // hide-desk option (Settings toggle)
	}
	for _, layer := range [...]*courtroom.SpriteLayer{&sc.Speaker, &sc.Pair} {
		if layer.Name == "" {
			continue
		}
		key := strings.ToLower(layer.Name)
		// Hidden sprites ("Missingno"): drop the layer entirely this session. The
		// check is before the !Visible skip — a hidden sprite is otherwise visible.
		if _, hidden := a.dialogs.hiddenSprites[key]; hidden {
			layer.Visible = false
			continue
		}
		if !layer.Visible {
			continue
		}
		if ov, ok := a.roster.spriteOv[key]; ok {
			layer.OffsetX, layer.OffsetY = ov[0], ov[1]
		}
	}
}

// healScenery re-demands a live scene asset when T1 lost it (LRU eviction, or
// a prefetch that never landed): without it the viewport can only show black
// until the next position change. Covers the background, the desk, AND the
// speaker/pair sprites — the same eviction that blanks the background can drop
// a character sprite (a hover-preview HIGH fetch, or churn in a busy room), and
// nothing else re-demands it, so it vanishes mid-message. Paced one ask per
// base per charIconRetryInterval; HIGH because this is the live scene.
func (a *App) healScenery() {
	sc := &a.link.room.Scene
	now := time.Now()
	// Each heal consults the shared per-scene futility budget (sceneHealAllowed):
	// when the settled working set exceeds the T1 main tier, every heal evicts
	// another live base and this loop — paced or not — churned decode→upload→
	// evict→re-demand forever (the focused half of the idle-CPU-burn report).
	if sc.BackgroundBase != "" && !(sc.BackgroundBase == a.demand.bgAskBase && now.Sub(a.demand.bgAskAt) < charIconRetryInterval) && !a.d.Store.Contains(sc.BackgroundBase) && a.sceneWarm.healAllowed(sc.BackgroundBase) {
		a.demand.bgAskBase, a.demand.bgAskAt = sc.BackgroundBase, now
		a.d.Manager.Prefetch(sc.BackgroundBase, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	}
	// IsMissing is checked alongside Contains because a base that 404'd
	// conclusively can never become Contains: without this the desk arm re-demanded
	// a known-absent desk every charIconRetryInterval for the whole heal budget,
	// which is what produced the ×5..×9 runs of the same missing-asset line (#27).
	if sc.DeskBase != "" && !(sc.DeskBase == a.demand.deskAskBase && now.Sub(a.demand.deskAskAt) < charIconRetryInterval) && !a.d.Store.Contains(sc.DeskBase) && !a.d.Store.IsMissing(sc.DeskBase) && a.sceneWarm.healAllowed(sc.DeskBase) {
		a.demand.deskAskBase, a.demand.deskAskAt = sc.DeskBase, now
		a.d.Manager.Prefetch(sc.DeskBase, assets.AssetTypeDeskOverlay, network.PriorityHigh) // AssetType: DeskOverlay
	}
	a.sceneWarm.healSpriteLayer(&a.env, &sc.Speaker, &a.demand.spkAskBase, &a.demand.spkAskAt, now)
	a.sceneWarm.healSpriteLayer(&a.env, &sc.Pair, &a.demand.pairAskBase, &a.demand.pairAskAt, now)
}

// healSpriteLayer re-demands one visible character sprite layer if its active
// base was evicted from T1. Walks courtroom.SpriteHealAlts — the same
// bare-then-folder chain EmoteAlts feeds the primary per-message demand —
// paced per layer like the scenery above. No-op while the sprite is resident
// or recently asked.
func (scenewarm *sceneWarmState) healSpriteLayer(e *env, layer *courtroom.SpriteLayer, askBase *string, askAt *time.Time, now time.Time) {
	if !layer.Visible || layer.Active == "" {
		return
	}
	if *askBase == layer.Active && now.Sub(*askAt) < charIconRetryInterval {
		return // recently asked for this exact base; let it land
	}
	if e.d.Store.Contains(layer.Active) {
		return // resident — nothing to heal
	}
	if !scenewarm.healAllowed(layer.Active) {
		return // per-scene futility budget spent (over-tier churn / permanent 404) — quiet until the scene changes
	}
	*askBase, *askAt = layer.Active, now
	e.d.Manager.PrefetchChain(layer.Active, courtroom.SpriteHealAlts(layer.Active), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (heal evicted live sprite: bare + folder)
	// Cold sprite + thumbnails on: also ask for the low-q stand-in (paced by the
	// same retry window above; the loader reads + decodes off-thread and
	// drainThumbs uploads under the thumb:// key the renderer probes).
	if th := e.d.Manager.Thumbs(); th != nil && th.Enabled() && !e.d.Store.Contains(render.ThumbKeyPrefix+layer.Active) {
		th.RequestLoad(layer.Active)
	}
}

// icEntry is one IC log line with its AO text color preserved (rich
// scrollback: render, search, and export keep the color).
type icEntry struct {
	text        string
	color       int
	url         string // first http(s) link in the line ("" = none); makes the line clickable
	friend      bool   // sender is a highlighted friend (showname match) — glows in the log
	friendColor int32  // per-friend glow RGB (0xRRGGBB) from a `name=hex` entry; -1 = default friend tint
	speaker     string // displayed name prefix (for per-speaker name colours); "" = system/evidence line
	pos         string // sender's courtroom position (wire Side, lowercased+trimmed); "" = system line — keys the pos filter
	stamp       string // local arrival time ("15:04"), formatted once on append; prefixed in the log when ICTimestamps is on
	ref         uint32 // content-stable reaction ref (#2): MakeReactionRef(CharName, clean text); 0 = system line
	// speech marks an entry that came from an IC MESSAGE and therefore has a
	// counterpart in the courtroom's play queue — the pairing the ghost-text option
	// counts on (ghosttext.go). Stamped by the EventMessage branch the same way
	// `ref` is, so a system line (music, evidence, a client notice) can never be
	// mistaken for something a character is about to say.
	speech bool
	// styles carries the inline-colour runs over the display BODY (indexing the
	// body portion of text, after the "<speaker>: " head); nil for a plain
	// message or a system line. bodyRuneStart is the rune offset of the body
	// within text, so the renderer can map styles onto the body slice.
	styles        []courtroom.StyleRun
	bodyRuneStart int
}

// icStampLayout formats the IC log's per-line local time (24-hour HH:MM).
const icStampLayout = "15:04"

// icStamp is the local arrival time for a new IC log line, formatted ONCE here on
// append — never in the draw loop — so the timestamp prefix costs nothing per frame.
func (a *App) icStamp() string { return a.now().Format(icStampLayout) }

// friendMessage reports whether a message is from a highlighted friend on the
// given server — its showname, falling back to CharName (the displayName rule),
// matched case-insensitively — and the friend's per-entry glow colour (0xRRGGBB,
// or -1 for the default tint). Gated on the master toggle FIRST, so it's a cheap
// no-op when the feature is off (the default) — and the membership scan
// allocates nothing, so it's safe per message even in a catch-up burst.
// ignoreSpeaker reports whether an IC message is from an ignored player (#81),
func (a *App) ignoreSpeaker(m *protocol.ChatMessage) bool {
	if m == nil || a.srvMeta.serverKey == "" {
		return false
	}
	name := strings.TrimSpace(m.Showname)
	if name == "" {
		name = strings.TrimSpace(m.CharName)
	}
	return a.d.Prefs.Load().ServerIgnoreMatch(a.srvMeta.serverKey, name)
}

func (a *App) friendMessage(serverKey string, m *protocol.ChatMessage) (bool, int32) {
	if m == nil || serverKey == "" {
		return false, -1
	}
	// Cheap gate: if NO friend signal (glow / notify / sound) is enabled, skip
	// entirely — the default, so it costs nothing.
	if !a.d.Prefs.Load().FriendHighlight && !a.d.Prefs.Load().FriendNotify && !a.d.Prefs.Load().FriendSound && !a.d.Prefs.Load().FriendOSToast {
		return false, -1
	}
	name := strings.TrimSpace(m.Showname)
	if name == "" {
		name = strings.TrimSpace(m.CharName)
	}
	if name == "" {
		return false, -1
	}
	friend, color := a.d.Prefs.Load().ServerFriendMatch(serverKey, name)
	return friend, int32(color)
}

// signalFriend fires the opt-in alert signals when a friend speaks: an in-app
// toast + window flash (FriendNotify) and a sound (FriendSound — a custom file,
// else the built-in ping). Streamer mode suppresses them (same as callwords).
// Called at the message seam when friendMessage is true (active OR background
// tab, so you're alerted even while looking at another server). The glow is
// drawn separately in the log.
func (a *App) signalFriend(serverName string, m *protocol.ChatMessage) {
	if a.d.Prefs.Load().StreamerModeOn || a.dndOn {
		return
	}
	if a.d.Prefs.Load().FriendNotify {
		name := strings.TrimSpace(m.Showname)
		if name == "" {
			name = strings.TrimSpace(m.CharName)
		}
		line := name + " just spoke"
		if serverName != "" {
			line += " on " + serverName
		}
		a.warnLine = line
		a.warnAt = time.Now()
		a.ctx.FlashWindow()
	}
	if a.d.Prefs.Load().FriendSound {
		// Custom sound if set, else the built-in ping — always audible. Same fix
		// as callwords: routing through the theme's word_call silenced the alert
		// on themes that name it but ship no loadable file.
		if f := a.d.Prefs.Load().FriendSoundFile; f != "" {
			a.d.Audio.PlayFile(f)
		} else {
			a.d.Audio.PlayAlert()
		}
	}
	// Desktop (OS) toast — only while you're tabbed away (#M4), rate-limited so a chatty
	// friend can't storm it.
	if a.d.Prefs.Load().FriendOSToast && !a.ctx.WindowFocused() && time.Since(a.sel.lastOSToast) >= osToastMinInterval {
		a.sel.lastOSToast = time.Now()
		name := strings.TrimSpace(m.Showname)
		if name == "" {
			name = strings.TrimSpace(m.CharName)
		}
		body := name + " just spoke"
		if serverName != "" {
			body += " on " + serverName
		}
		showOSToast("AsyncAO — friend online", body)
	}
}

// signalModcall pops a desktop toast when a modcall arrives, if the option is on
// (OFF by default) — a "you're being called" ping for mods who alt-tabbed away.
// Streamer mode suppresses it (modcall lines can carry IPs) and it shares the
// friend toast's rate limit so a modcall storm can't flood the desktop. Called
// at both seams (active + background tab).
func (a *App) signalModcall(serverName, notice string) {
	if !a.d.Prefs.Load().ModcallToast || a.d.Prefs.Load().StreamerModeOn || a.ctx.WindowFocused() {
		return // #M4: only toast when tabbed away (the in-app OOC + flash cover the focused case)
	}
	if time.Since(a.sel.lastOSToast) < osToastMinInterval {
		return
	}
	a.sel.lastOSToast = time.Now()
	body := strings.TrimSpace(notice)
	if serverName != "" {
		body = serverName + ": " + body
	}
	showOSToast("AsyncAO — mod call", body)
}

// switchAreaScrollback swaps the IC log to toArea's own history when per-area
// scrollback is on (opt-in). Best-effort: AO only tells us the area on an
// explicit area click (curArea), so server-initiated moves keep the current log
// — same as the default continuous behavior. Runs only on an area click, never
// per frame, and no-ops entirely when off, so it costs the default path nothing.
func (a *App) switchAreaScrollback(toArea string) {
	if !a.d.Prefs.Load().PerAreaScroll || toArea == a.srvMeta.curArea {
		return
	}
	if a.srvMeta.areaLogs == nil {
		a.srvMeta.areaLogs = map[string][]icEntry{}
	}
	// Save the area we're leaving (curArea); track its key order for eviction.
	if _, seen := a.srvMeta.areaLogs[a.srvMeta.curArea]; !seen {
		a.srvMeta.areaLogOrder = append(a.srvMeta.areaLogOrder, a.srvMeta.curArea)
	}
	a.srvMeta.areaLogs[a.srvMeta.curArea] = a.chatlog.icLog
	for len(a.srvMeta.areaLogOrder) > areaLogCacheMax {
		oldest := a.srvMeta.areaLogOrder[0]
		a.srvMeta.areaLogOrder = a.srvMeta.areaLogOrder[1:]
		if oldest != toArea { // never evict the log we're about to load
			delete(a.srvMeta.areaLogs, oldest)
		}
	}
	// Load the target area's saved log (nil = unvisited = a fresh empty log).
	a.chatlog.icLog = a.srvMeta.areaLogs[toArea]
	a.chatlog.icLogSeq++ // invalidate the wrap + filter caches
	a.chatlog.icScroll, a.compose.icStick = 0, true
	a.compose.icReadMark = len(a.chatlog.icLog)
}

// logDetailed appends an IC message to the transcript when detailed logging is
// on (opt-in). The writer is opened lazily and does the disk write on its own
// goroutine, so the message seam never blocks. A no-op (one pref read) when off,
// so the default path is unaffected. Called at both message seams (active +
// background tab), so the transcript captures every server you're connected to.
func (a *App) logDetailed(server string, session int, m *protocol.ChatMessage) {
	if m == nil || !a.d.Prefs.Load().DetailedLog {
		return
	}
	if w := a.transcriptFor(server, session); w != nil {
		w.write(detailedLogLine(time.Now(), m))
	}
}

// logDetailedOOC appends an OOC line to the SAME per-session transcript IC
// uses — AO2 interleaves OOC into one log file (courtroom.cpp:1932), and so
// does this: it reuses transcriptFor rather than opening a second writer or
// file, so OOC inherits the already-solved per-(server, tab) separation for
// free. No-op when detailed logging is off, same as logDetailed. Called at
// both message seams (active + background tab) — see logDetailed's doc for
// why session is the CALLING TAB's sessionState.logSession rather than
// a.logSession read here: the background seam runs while a DIFFERENT tab is
// live.
func (a *App) logDetailedOOC(server string, session int, line string) {
	if !a.d.Prefs.Load().DetailedLog {
		return
	}
	if w := a.transcriptFor(server, session); w != nil {
		w.write(oocLogLine(time.Now(), line))
	}
}

// transcriptFor returns this TAB SESSION's transcript writer, opening its file
// (logs/<server>/<date_time>[-<session>].log) on first use. Bounded by
// transcriptWriterCap so a churn of servers/reconnects can't open unbounded files
// or goroutines. nil on error / at cap.
func (a *App) transcriptFor(server string, session int) *transcriptWriter {
	if a.translogs == nil {
		a.translogs = make(map[translogKey]*transcriptWriter)
	}
	key := translogKey{server: server, session: session}
	if w, ok := a.translogs[key]; ok {
		return w
	}
	if len(a.translogs) >= transcriptWriterCap {
		return nil
	}
	path, err := transcriptPathFor(server, time.Now(), a.transcriptSeqFor(server))
	if err != nil {
		return nil
	}
	w, err := newTranscriptWriter(path)
	if err != nil {
		return nil
	}
	a.translogs[key] = w
	return w
}

// transcriptSeqFor is the 1-based ordinal of the file about to be opened for
// `server` in THIS run — how many writers this server already has, plus one. It
// disambiguates the file NAME, which is a timestamp to the second: two tabs joining
// one server in the same second would otherwise resolve to the same path and both
// append to it, which is the very interleaving the per-tab key exists to end.
func (a *App) transcriptSeqFor(server string) int {
	n := 1
	for k := range a.translogs {
		if k.server == server {
			n++
		}
	}
	return n
}

// transcriptOwned reports whether a transcript key still belongs to a session that
// exists: the live one, or a parked tab's. It is the single definition of "this
// writer still has an owner", so the reap below and any future caller cannot form
// two different opinions about which files are alive.
func (a *App) transcriptOwned(k translogKey) bool {
	if k.server == a.srvMeta.serverName && k.session == a.srvMeta.logSession {
		return true
	}
	for _, t := range a.tabs {
		if t != nil && t.state.srvMeta.serverName == k.server && t.state.srvMeta.logSession == k.session {
			return true
		}
	}
	return false
}

// reapTranscripts retires and forgets every transcript writer no session owns any
// more — one open file and one goroutine released per ended session.
// Retire, never close: the goroutine drains and closes the file on its own so the
// render thread never waits on a disk flush (hard rule §2).
func (a *App) reapTranscripts() {
	for k, w := range a.translogs {
		if a.transcriptOwned(k) {
			continue
		}
		w.retire()
		delete(a.translogs, k)
	}
}

// CloseTranscript flushes and closes every detailed-log writer at shutdown. This is
// the one path that WAITS for the flush — the process is exiting, so a durable file
// matters more than a frame.
func (a *App) CloseTranscript() {
	for k, w := range a.translogs {
		w.close()
		delete(a.translogs, k)
	}
}

func (a *App) pushIC(line string, color int, friend bool, friendColor int32, speaker string) {
	a.pushICStyled(line, color, friend, friendColor, speaker, nil, 0)
}

// pushICStyled is pushIC plus the inline-colour runs and the body's rune offset
// within the line (issue #123): an IC message's inline colour markup now colours
// the log, so the entry carries the runs the renderer maps onto the body.
func (a *App) pushICStyled(line string, color int, friend bool, friendColor int32, speaker string, styles []courtroom.StyleRun, bodyRuneStart int) {
	// Store-form FIRST, then describe it. Everything else on the entry is a statement
	// ABOUT the stored text, so it has to be derived from that exact string. The url
	// below used to be scanned off the original: extractURLs splits on strings.Fields
	// and no codec rune is whitespace, so a trailing marker FUSES onto the last token —
	// a message ending in a link stored a url with invisible runes inside it, and that
	// is the string the tooltip shows and openBrowser is handed. The same ordering also
	// stops a link past icLineCap being offered from text the entry no longer contains.
	line = capLogLine(line, speaker)
	url := ""
	if urls := extractURLs(line, 1); len(urls) > 0 {
		url = urls[0]
	}
	a.chatlog.icLog = append(a.chatlog.icLog, icEntry{text: line, color: color, url: url, friend: friend, friendColor: friendColor, speaker: speaker, stamp: a.icStamp(), styles: styles, bodyRuneStart: bodyRuneStart})
	if cap := a.icLogCapFor(); cap > 0 && len(a.chatlog.icLog) > cap {
		copy(a.chatlog.icLog, a.chatlog.icLog[len(a.chatlog.icLog)-cap:])
		a.chatlog.icLog = a.chatlog.icLog[:cap]
	}
	a.chatlog.icLogSeq++ // invalidate the filter cache (len alone lies at the cap)
}

// icLogCapFor returns the effective IC scrollback cap: 0 = don't cap at all,
// otherwise that many entries. The "IC log depth" pref (Settings → Chat) is
// read live so a change applies to the very next message. The pref's 0 sentinel
// resolves to the shipped icLogCap; ICLogDepthUnlimited resolves to 0 (no cap).
func (a *App) icLogCapFor() int {
	if a.d.Prefs == nil {
		return icLogCap
	}
	switch n := a.d.Prefs.Load().ICLogDepthVal; {
	case n == config.ICLogDepthUnlimited:
		return 0
	case n == 0:
		return icLogCap
	default:
		return n
	}
}

// oocLineCap bounds ONE OOC entry (hostile-server guard). MOTDs and server
// /help output are long multi-line texts — the old clampLine cut them at 120
// chars, which is why they arrived truncated; they now wrap at draw time
// instead. 4096 still cut real /help dumps (a single-paragraph /help runs well
// past 4 KiB), so it's 16 KiB: the OOC log holds at most icLogCap (1024)
const oocLineCap = 16384

// pushOOC appends an OOC log line. speaker is the sender's name for a real OOC
// message (used by name colours), "" for system/[MOD CALL]/CLIENT lines. The
// two slices stay parallel (same cap), so display can look the speaker up by
// entry index without re-parsing the ": " (which would mis-tint system lines).
func (a *App) pushOOC(line, speaker string) {
	// Drop the zero-width sidechannel from the BODY before anything reads the line. OOC
	// is the channel AsyncAO's own DMs and group chat ride on (/pm, courtroom/msgwire.go)
	// and the caller composes us ev.Name + ": " + a RAW ev.Text, so this is the lane
	// where a marker actually arrives; the control frame itself was already decoded
	// upstream (routeIncomingPM runs before this call), so by here it is display text.
	line = stripDisplayTail(line, speaker)
	// Harvest /getarea "[uid] name" rows for click-to-pair, from the RAW text:
	// strip the "speaker: " prefix first, else when a server sends /getarea line
	// by line every "[uid]" row hides behind "ServerName: " and parses as nothing.
	text := line
	if speaker != "" && strings.HasPrefix(line, speaker+": ") {
		text = line[len(speaker)+2:]
	}
	a.parseAreaBlock(text)
	isAreaList := looksLikeAreaList(text)
	if isAreaList {
		// A /getarea snapshot just landed: re-merge its IPIDs (the one field PR/PU
		// lacks) into the live roster NOW, so a mod's Refresh shows them at once
		// instead of lagging until the next PR/PU packet. No-op off the PR/PU path
		// (rosterEqual gates it), so an ordinary chat line never pays for this.
		a.rebuildLiveRoster()
	}
	// A roster pull's own ROSTER reply is parsed for its data but kept OUT of the OOC
	// log — a multi-area /gas is a wall of text nobody asked to read in chat. Only a
	// fetch WE sent arms the window (fetchRoster / fetchAreaForBan / the menu row), so
	// somebody else's /getarea still shows.
	if isAreaList && a.now().Before(a.roster.suppressAreaEchoUntil) {
		return // the entire /gas reply burst stays out of OOC, not just its first line
	}
	// A REFUSAL is deliberately NOT swallowed any more. It used to be — swallowed and
	// latched into rosterCmdUnsupported — because the sender was a 3 s timer and the
	// refusal would have repeated forever. With the automatic poll deleted
	// (liveroster.go) every fetch is a button press, so the server's answer belongs on
	// screen: "Invalid command." or an old KFO hub's "You cannot see players in all
	// areas in this hub!" is the reason the press did nothing, and hiding it is worse
	// than printing it once. It cannot spam: nothing sends the command on its own.
	a.detectIncomingPM(text)
	if len(line) > oocLineCap {
		line = line[:oocLineCap] + "…"
	}
	a.chatlog.oocLog = appendCapped(a.chatlog.oocLog, line, icLogCap)
	a.chatlog.oocSpeakers = appendCapped(a.chatlog.oocSpeakers, speaker, icLogCap)
	a.chatlog.oocSeq++ // invalidate the wrapped-lines cache
	// Every active-tab OOC-ish event (real chat, [MOD CALL], CLIENT auth lines,
	// [CASE], [SERVER] notices/mute) already funnels through this one function,
	// same as AO2's single append_server_chatmessage entry point — so this one
	// call site is the transcript's for the whole channel, "log only what's
	// shown" (the truncated, post-suppression `line` above, not the raw input).
	a.logDetailedOOC(a.srvMeta.serverName, a.srvMeta.logSession, line) // detailed transcript (opt-in), this tab's own file
}

func appendCapped(list []string, line string, cap int) []string {
	list = append(list, line)
	if len(list) > cap {
		copy(list, list[len(list)-cap:])
		list = list[:cap]
	}
	return list
}

// speakerHeadLen is the byte length of the "<speaker>: " head a composed log line
// opens with, or 0 when there is none (a system line with no speaker, or a line the
// speaker does not appear in). icLogEntry's nick arm writes "Ace (Nick): …",
// where the speaker sits INSIDE the head, so the name is located by index rather
// than assumed to start at 0.
func speakerHeadLen(line, speaker string) int {
	if speaker == "" {
		return 0
	}
	i := strings.Index(line, speaker)
	if i < 0 {
		return 0
	}
	head := i + len(speaker)
	if strings.HasPrefix(line[head:], ": ") {
		head += 2
	}
	return head
}

// stripDisplayTail drops the zero-width sidechannel from a composed log line's BODY
// and leaves the speaker head byte-identical.
func stripDisplayTail(line, speaker string) string {
	head := speakerHeadLen(line, speaker)
	if head == 0 {
		return courtroom.StripSpriteStyle(line)
	}
	return line[:head] + courtroom.StripSpriteStyle(line[head:])
}

// capLogLine turns a composed line into a STORABLE IC log entry: the zero-width
// sidechannel comes off the BODY (stripDisplayTail), then the result is bounded
// (hostile-server guard) WITHOUT the old 120-char "…" truncation — the IC log
// word-wraps long lines at draw time (icWrapped). Real IC messages (≤256 on the wire)
// The two steps live together because their ORDER is the point: the cap has to be
// measured on the text a human sees, so an invisible marker can neither eat the
// budget nor be cut in half and left as a partial run.
func capLogLine(s, speaker string) string {
	s = stripDisplayTail(s, speaker)
	if len(s) <= icLineCap {
		return s
	}
	if r := []rune(s); len(r) > icLineCap {
		return string(r[:icLineCap]) + "…"
	}
	return s
}

func clampLine(s string) string {
	runes := []rune(s)
	if len(runes) > logLineMax {
		return string(runes[:logLineMax]) + "…"
	}
	return s
}

func hostOfURL(u string) string {
	const sep = "://"
	i := strings.Index(u, sep)
	if i < 0 {
		return ""
	}
	rest := u[i+len(sep):]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// tierColor maps a server entry to its lobby color.
func tierColor(e network.ServerEntry) sdl.Color {
	switch e.Security() {
	case network.SecurityWSS:
		return ColTierGreen
	case network.SecurityWS:
		return ColTierYellow
	default:
		return ColTierBlack
	}
}
