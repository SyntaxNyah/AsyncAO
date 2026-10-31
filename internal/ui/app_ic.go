package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

func (a *App) applyThemeAsync() uint64 {
	name, dir := a.d.Prefs.Load().Theme()
	// A per-server theme binding wins over the global pick; resetSessionState clears it.
	if a.themeBound != "" {
		name = a.themeBound
	}
	anims := a.d.Prefs.Load().PreferAnimated
	// Prefs are read here on the render thread: the goroutine below must not take the preferences lock.
	sub := a.d.Prefs.Load().SubthemeName
	// PanelFonts takes the read lock and returns a copy; called off-thread it would race the debounced saver.
	panelFonts := a.d.Prefs.Load().PanelFonts()
	// The theme-media allowance derives from this (render.ThemeMediaByteCap).
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
		// Users persist every shape of path (root, themes folder, one theme, quoted), so every apply normalizes it.
		root, _ := normalizeThemeRoot(dir)
		// Must walk the same root strings as the picker and the catalog scan,
		// or a symlinked install disagrees with itself.
		exeDir := config.ExecutableDir()
		configBase, _ := config.ConfigBaseDir()
		t, err := theme.Load(name, sub, themeLoadRoots(name, root, exeDir, configBase))
		if err == nil {
			// Published so the effects-overlay roster resolves on this apply's ladder without reloading;
			// read-only from here.
			res.themeObj = t
			// Already parsed by theme.Load from this theme's own directories;
			// never merged or inherited from the default theme.
			res.sidecar = t.Sidecar()
			// theme.Load swallows an unparsable sidecar by design,
			// so this is the only place the failure can be picked up.
			if err := t.SidecarErr(); err != nil {
				res.sidecarErr = themeSidecarRefusalLine(name, t.SidecarDir(), err)
				res.sidecarTip = themeSidecarRefusalTip(name, err)
			}
			// Planned against the media budget first, so nothing is decoded before it is known to fit;
			// an entry that does not fit becomes a placeholder.
			res.themeID = themeSanitizeID(name)
			res.mediaPlan = planThemeMedia(res.sidecar, res.themeID, t.SidecarDir(), texBudgetMiB)
			res.loadThemeMediaSet(anims)
			res.iniKeys = t.KeyCount()
			res.probed = t.Dirs()
			res.fontPath = t.FontFile() // the theme's own bundled font, if any (#6)
			// Per-element families, sizes and bold (Courtroom::set_fonts, courtroom.cpp:1188);
			// disk reads stay here (hard rule 2).
			res.buildThemeFontTable(t, systemFontDirs())
			// After the AO2 table because it overrides it; before applyPanelFonts because the user still wins.
			res.applySidecarFonts(res.sidecar, t.SidecarDir())
			res.msgCol, res.hasMsg = declaredFontInk(t.Font("message"))
			res.nameCol, res.hasName = declaredFontInk(t.Font("showname"))
			// The chatbox ladder below locates its files differently but must land them the same way.
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
			// The showname ladder's two wider skins:
			// AO2 appends to the resolved base skin's path (courtroom.cpp:3358/:3362).
			// Two os.Stat calls per apply, never per message;
			// a miss leaves the stem out of res.images, so draws only read a map.
			// They pin on top of the T1 budget, bounded at two files.
			if res.chatboxStem != "" {
				for _, rung := range chatboxLadderRungs {
					if path, ok := theme.FindAssetIn(res.chatboxDir, rung.fileStem(res.chatboxStem), themeImageExts); ok {
						loadOne(rung.texStem(), path)
					}
				}
			}
			// Drop theme ink that has no contrast against the skin it ships with.
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
			// Rects in design-space pixels, plus the emote grid cell metrics.
			res.layout = make(map[string]theme.Rect, len(themeLayoutKeys))
			for _, key := range themeLayoutKeys {
				if r, ok := t.ElementRect(key); ok {
					res.layout[key] = r
				}
			}
			// AO2's per-key fallback; must run after the loop above so a theme's own rect wins.
			applyAO2DefaultRects(res.layout)
			// [overrides] bind to the AO2 design, which for a sparse theme is the fallback rects,
			// so this runs after them.
			// The tab strip's key is seeded with the fold,
			// in the one order that answers an asyncao_tabbar row exactly once.
			res.tabBarSeeded = seedTabBarThroughOverrides(res.layout, res.sidecar)
			// Measures the theme's art behind each font element so the render thread can guard ink without a decode
			// buffer;
			// after the defaults, so an element is measured where it draws.
			res.sampleThemeBackdrops()
			// Debug log only, never the player's screen;
			// AO2's inverse warns on identifiers a theme omits (courtroom.cpp:1336).
			res.unbound = unboundDesignKeys(t)
			res.emoteCell = designPair(t, "emote_button_size", defaultEmoteCellPx, defaultEmoteCellPx)
			res.emoteGap = designPair(t, "emote_button_spacing", defaultEmoteGapPx, defaultEmoteGapPx)
			// effects_icon_size (courtroom.cpp:979);
			// 0,0 means the theme is silent and the picker uses its own row metrics.
			res.effectsIcon = designPair(t, "effects_icon_size", 0, 0)
			// A missing key yields parseShownameAlign's default arm, which is AO2's own else (courtroom.cpp:3352).
			alignRaw, _ := t.DesignValue(shownameAlignKey)
			res.shownameAlign = parseShownameAlign(alignRaw)
			// Tolerant like AO2's QString::toInt: missing, blank or garbage all land on 0, which disables the ladder.
			extraRaw, _ := t.DesignValue(shownameExtraWidthKey)
			res.shownameExtra = parseShownameExtraWidth(extraRaw)
			// Char select's twelve keys, resolved against the embedded default-theme table; landed by pollThemeApply.
			res.charSel = readCharSelectDesign(t)
			// AO2 2.10+ themes color the client through courtroom_stylesheets.css.
			if path, ok := t.FindAsset("courtroom_stylesheets", []string{".css"}); ok {
				if data, rerr := os.ReadFile(path); rerr == nil {
					res.palette = theme.ParseStylesheet(data)
				}
			}
		}
		// Sound names resolve even with no theme on disk: stock fallbacks keep WT/CE/verdict/modcall audible.
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
		// The user's per-panel fonts resolve outside the theme-loaded branch, so they still work when no theme loads.
		// A theme's directories are searched when there is one; the user's font folder and system folders always are.
		var themeDirs []string
		if t != nil {
			themeDirs = t.Dirs()
		}
		// Created before resolving, so the drop folder is a search tier on this pass and the warning can name it.
		res.userFontsDir = ensureUserFontsDir()
		res.applyPanelFonts(panelFonts, themeDirs, systemFontDirs())
		// The warning's wording depends on a disk probe, so it is decided here and not on the render thread.
		if len(res.themeFontsMissing) > 0 {
			anyOnDisk := t != nil && t.HasAnyFontFile()
			res.themeFontWarn = themeFontWarning(res.themeFontsMissing, anyOnDisk, res.userFontsDir)
		}
		// Built here, after the fonts it reads, because the render thread must never walk the notes (hard rule 2).
		res.report = buildThemeReport(res.sidecar, res.themeFontsMissing, res.unbound)
		// Newest-wins publish: never overwrite a higher-gen result.
		// Both arms hand the dropped apply's pixel buffers back to the decoder's pool (see releaseDecoded).
		// Ownership is exclusive: res was never published,
		// and a successful CAS proves the render thread's Swap did not take old.
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

// pollThemeApply lands a finished theme apply on the render thread and reports it in the status line and debug log.
func (a *App) pollThemeApply() {
	res := a.themeRes.Swap(nil)
	if res == nil {
		return
	}
	// A slower older-gen load can publish after a newer one was consumed; landing it would revert the theme and clear
	// the chat font, because an older default apply carries an empty fontPath.
	if res.gen < a.themeAppliedGen {
		res.releaseDecoded()
		return
	}
	a.themeAppliedGen = res.gen
	// Pinned so theme chrome never loses an eviction fight against streaming sprites;
	// stems the new theme lacks are dropped.
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
	// Pristine rects stay aside for reset and undo;
	// layout-editor overrides apply to a copy and only rewrite existing keys.
	// The tab strip's key was seeded on the apply goroutine into the pristine map, so only its flag crosses here.
	a.tabBarSeeded = res.tabBarSeeded
	a.themeRectsOrig = res.layout
	rects := make(map[string]theme.Rect, len(res.layout))
	for k, v := range res.layout {
		rects[k] = v
	}
	a.themeRects = a.applyRectOverrides(rects)
	a.themeEmoteCell, a.themeEmoteGap = res.emoteCell, res.emoteGap
	// A cached roster or icon page from the old theme would have the wrong names, properties and art paths.
	a.overlayIconSize = res.effectsIcon
	a.overlayTheme = res.themeObj
	a.overlayRosters = nil
	a.overlayIconPagesReset()
	// A themeless apply carries the zero value, Left,
	// restoring AO2's default rather than the previous theme's alignment.
	a.themeShownameAlign = res.shownameAlign
	a.themeShownameExtra = res.shownameExtra
	// No applyRectOverrides pass: these keys are deliberately not editable, so the layout editor never sees them.
	a.charSelDesign = res.charSel
	// Must land before the canvases are invalidated, because the next themeLayoutIn rebuild bakes it.
	// nil clears the previous theme's elements.
	a.themeSidecar = res.sidecar
	// Applies also come from background events (healTheme, texture filter, boot);
	// without this, those discard unsaved edits.
	// Guarded on the theme name, so switching themes correctly loses the document.
	a.reclaimThemeDoc()
	// Before the invalidation too: an element resolving against a page not yet landed would bake a nil and paint
	// nothing.
	a.landThemeMedia(res)
	// Keyed on the reason, not re-armed per apply:
	// heals and boot apply repeatedly, and a dismissed chip must stay dismissed.
	// A different failure re-arms it; a successful load clears the fields.
	if a.themeSidecarErr != res.sidecarErr {
		a.themeSidecarErrAck = false
	}
	a.themeSidecarErr, a.themeSidecarTip = res.sidecarErr, res.sidecarTip
	a.invalidateThemeCanvases()
	a.themeSounds = res.sounds
	a.themeAt = time.Now() // restart the theme-art animation clock
	// A theme reload restarts every element clock together; a nil sidecar zeroes the pool, back to the shared anchor.
	a.applyThemeClocks(res.sidecar)
	// Label textures are color-keyed, so the text cache is purged to re-rasterize in the new colors.
	a.themePalette = res.palette // remember it so a chrome-preset change can re-overlay it (#M3)
	applyThemePalette(res.palette)
	a.ctx.purgeTextCache()
	a.pushRealizationToRoom()
	a.themeMsgCol, a.themeHasMsg = res.msgCol, res.hasMsg
	a.themeNameCol, a.themeHasName = res.nameCol, res.hasName
	a.rasterText = "" // re-raster the current message with theme colors
	a.themeAppliedName = res.name
	// Re-resolve only on a change, so a same-font theme switch doesn't re-kick the off-thread font read.
	if a.themeFontFile != res.fontPath {
		a.themeFontFile = res.fontPath
		a.applyFontConfig()
	}
	// File names for an empty Settings box to show,
	// and families that resolved to nothing so a typo shows where it was typed.
	a.themeFaceNames = res.faceNames
	a.panelFontMissing = res.panelFontsMissing
	a.landThemeFonts(res)
	// A theme with missing fonts renders in the client's own face, so say why on screen and in the debug log.
	a.themeFontWarn = res.themeFontWarn
	if res.themeFontWarn != "" {
		// The debug log always, the modal at most once per theme: missing fonts recur on every apply.
		a.pushDebug(res.themeFontWarn)
		a.maybeWarnMissingFonts(res.name, res.themeFontsMissing, res.themeFontWarn, res.userFontsDir)
	}
	line := themeApplySummary(res)
	settings.statusLine = clampLine(line)
	a.pushDebug(line)
	if res.inkGuard != "" {
		a.pushDebug(res.inkGuard)
	}
	// Logged on every apply (pushDebug folds repeats) and shown in the Settings status line,
	// where the user just picked it.
	if res.sidecarErr != "" {
		a.pushDebug(res.sidecarErr)
		settings.statusLine = clampLine(res.sidecarErr)
	}
	// The report carries the unbound-key line as one logged entry.
	a.noteThemeReport(res.name, res.report)
	// ms_chatlog is AO2's debug-log rect (courtroom.cpp:831) and inert here, so a saved drag would place a box for the
	// wrong widget; the key is not editable, so it cannot come back.
	if res.name != "" && a.d.Prefs.Load().HasThemeRectOverride(res.name, "ms_chatlog") {
		a.d.Prefs.ClearThemeRectOverride(res.name, "ms_chatlog")
		a.pushDebug("cleared a stale ms_chatlog layout override (it now means AO2's debug log)")
	}
	// Pinned uploads can evict emote-button art from T1; force a fresh demand so the grid heals at once
	// rather than after demandAsset's retry window (pollCharINI does the same).
	a.emoteBtnOff, a.emoteBtnOn, a.emoteIconPages = nil, nil, nil
	a.emoteAsk, a.emoteIconAsk = nil, nil
	// Last: it needs the landed geometry and a resize is disruptive; no-op unless a user pick armed this generation.
	a.maybeResizeToThemeDesign(res.gen)
	// Also one-shot: the editor can only open over a theme that has applied, which is this landing.
	a.maybeOpenEditorAfterCopy(res.gen)
}

// ensureThemeForSession re-applies the theme when the session's binding disagrees with what is on screen.
func (a *App) ensureThemeForSession() {
	want := a.themeBound
	if want == "" {
		want, _ = a.d.Prefs.Load().Theme()
	}
	if want != "" && want != a.themeAppliedName {
		a.applyThemeAsync()
	}
}

// themeUnboundReportCap keeps the unbound-key debug line to one row at any window width (hard rule 4).
const themeUnboundReportCap = 12

// unboundDesignKeys lists the theme's rect design keys that reach no widget;
// allocates, so apply goroutine only (hard rule 2).
func unboundDesignKeys(t *theme.Theme) string {
	var names []string
	for key := range t.DesignKeys() {
		if _, ok := t.ElementRect(key); !ok {
			continue // pairs, colours and scalars are not widget rects
		}
		// A row that paints nothing still counts as unbound: this report is about pixels, not table membership.
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

// themeApplySummary turns one apply into a verdict, so "nothing happened" differs from "applied, courtroom only".
func themeApplySummary(res *themeApply) string {
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

// themePage fetches a theme texture through a generation-keyed cache,
// so overlay draws skip the LRU lock and recency churn.
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

// now is the frame's clock snapshot, or real time outside a frame.
func (a *App) now() time.Time {
	if a.frameNow.IsZero() {
		return time.Now()
	}
	return a.frameNow
}

// themeElapsed is the animation clock for looping theme art: time since the theme applied.
func (a *App) themeElapsed() time.Duration {
	if a.themeAt.IsZero() {
		return 0
	}
	return a.now().Sub(a.themeAt)
}

// themeFrame picks the current frame for a theme page, sharing elementFrameIndex and themeFXFrozen with elementFrame.
// loop is true because theme chrome has no finished state.
func (a *App) themeFrame(page *render.TexturePage) *sdl.Texture {
	if page == nil || len(page.Frames) == 0 {
		return nil // matches elementFrame: a page with no frames is not a texture
	}
	if len(page.Frames) == 1 {
		return page.Frames[0]
	}
	if a.themeFXFrozen {
		return page.Frames[0] // ReduceMotion, latched once per frame (reduceMotionNow)
	}
	idx, live := elementFrameIndex(page, a.themeElapsed(), true)
	if live {
		a.NoteAnimating()
	}
	return page.Frames[idx]
}

// pushRealizationToRoom hands the courtroom the realization sound URL,
// played at message display as in handle_ic_message.
func (a *App) pushRealizationToRoom() {
	if a.room == nil {
		return
	}
	if name := a.themeSounds["realization"]; name != "" {
		a.room.RealizationSFX = a.urls.SFX(name) // AssetType: SFX (realization)
	} else {
		a.room.RealizationSFX = ""
	}
}

// healTheme re-runs the theme load when T1 evicted a theme texture still in use, once per themeHealPeriod at most.
func (a *App) healTheme() {
	if time.Since(a.themeHealAt) > themeHealPeriod {
		a.themeHealAt = time.Now()
		a.applyThemeAsync()
	}
}

// debugStampLayout prefixes each ring row with the wall clock of its latest sighting.
const debugStampLayout = "15:04:05 "

// debugFoldWindow is how long a repeat folds into its row's ×N counter; wider than the gap between IC messages.
// Folding only changes the display, never which packets are handled or which assets are probed.
const debugFoldWindow = 90 * time.Second

// debugEntry is one row of the failure ring; text is cached so a fold re-renders per push, not per draw.
type debugEntry struct {
	raw   string    // the caller's message — the fold key
	at    time.Time // latest sighting; the fold window is measured from it
	count int       // sightings folded into this row
	text  string    // rendered row: stamp + raw (+ "  ×N")
}

func (e debugEntry) render() string {
	if e.count > 1 {
		return e.at.Format(debugStampLayout) + e.raw + fmt.Sprintf("  ×%d", e.count)
	}
	return e.at.Format(debugStampLayout) + e.raw
}

// pushDebug appends to the failure log, folding repeats inside debugFoldWindow; render thread only.
func (a *App) pushDebug(line string) { a.pushDebugAt(line, time.Now()) }

// logSpeakerAnimDiag logs the speaker sprite's animation stats once per base change.
func (a *App) logSpeakerAnimDiag() {
	if a.d.Viewport == nil {
		return
	}
	si, ok := a.d.Viewport.SpeakerAnimInfo()
	if !ok {
		return
	}
	if si.Base == a.lastSpeakerDiagBase {
		return // same base already logged — don't flood the ring
	}
	a.lastSpeakerDiagBase = si.Base
	a.pushDebug(fmt.Sprintf("speaker anim: %s · %d/%d frames · delays %d/%d/%d ms (min/avg/max)",
		si.Base, si.Kept, si.Source,
		si.MinDelay/time.Millisecond, si.AvgDelay/time.Millisecond, si.MaxDelay/time.Millisecond))
}

// pushDebugAt is pushDebug with the clock injected.
func (a *App) pushDebugAt(line string, now time.Time) {
	// Rows are ordered by latest sighting, so the scan can stop at the first row outside the window.
	for i := len(a.debugLog) - 1; i >= 0; i-- {
		e := a.debugLog[i]
		if now.Sub(e.at) > debugFoldWindow {
			break
		}
		if e.raw != line {
			continue
		}
		e.count++
		e.at = now
		e.text = e.render()
		// Moving to the tail keeps the ordering the early exit relies on.
		copy(a.debugLog[i:], a.debugLog[i+1:])
		a.debugLog[len(a.debugLog)-1] = e
		return
	}
	e := debugEntry{raw: line, at: now, count: 1}
	e.text = e.render()
	a.debugLog = append(a.debugLog, e)
	if len(a.debugLog) > debugLogCap {
		a.debugLog = a.debugLog[len(a.debugLog)-debugLogCap:]
	}
}

// drainThumbs uploads loaded sprite thumbnails into T1 under thumb:// keys, bounded per frame.
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

// drainWarnings empties the manager's bounded missing-asset lane into the debug log and the menu-bar chip.
func (a *App) drainWarnings() {
	// Always drained and logged; only the on-screen chip is opt-in.
	show := a.d.Prefs.Load().ShowAssetWarnings
	for {
		select {
		case w := <-a.d.Manager.Warnings():
			// A missing preanimation must not hold the stage for PreanimTimeout (AO2-Client skips it).
			// One Manager serves every room, so this relays to all; each room ignores bases that are not its own.
			if w.Type == assets.AssetTypeCharSprite {
				if a.room != nil {
					a.room.NotifyAssetMissing(w.Base)
				}
				if a.splitRoom != nil {
					a.splitRoom.NotifyAssetMissing(w.Base)
				}
				if a.replayRoom != nil {
					a.replayRoom.NotifyAssetMissing(w.Base)
				}
				if a.makerPreviewRoom != nil {
					a.makerPreviewRoom.NotifyAssetMissing(w.Base)
				}
				// The viewport draws the AO2 placeholder rather than a stale hold-previous. Warnings fire only on full
				// chain exhaustion
				// and speculative Markov prefetches suppress them, so a predicted-but-absent base is never marked.
				if a.d.Store != nil {
					a.d.Store.MarkMissing(w.Base)
				}
			}
			// A missing desk means AO2's set_scene hides the layer, with no fallback (courtroom.cpp:4628-4634).
			// Without this the sticky scenery check keeps painting the previous room's desk:
			// a 404'd base never becomes resident.
			if w.Type == assets.AssetTypeDeskOverlay {
				if a.room != nil {
					a.room.NotifyDeskMissing(w.Base)
				}
				if a.splitRoom != nil {
					a.splitRoom.NotifyDeskMissing(w.Base)
				}
				if a.replayRoom != nil {
					a.replayRoom.NotifyDeskMissing(w.Base)
				}
				if a.makerPreviewRoom != nil {
					a.makerPreviewRoom.NotifyDeskMissing(w.Base)
				}
				// syncAnimDesk releases the held desk layer on this flag (AO2's ui_vp_desk->hide());
				// clearMissing on upload undoes it.
				if a.d.Store != nil {
					a.d.Store.MarkMissing(w.Base)
				}
			}
			// Releases a message holding on scenery or audio, since SequentialWait has no timeout.
			// No Store.MarkMissing: these have no placeholder, and the renderer's own fallback covers them.
			switch w.Type {
			case assets.AssetTypeBackground, assets.AssetTypeMisc, assets.AssetTypeSFX, assets.AssetTypeBlip, assets.AssetTypeShoutBubble:
				if a.room != nil {
					a.room.NotifyAssetMissing(w.Base)
				}
				if a.splitRoom != nil {
					a.splitRoom.NotifyAssetMissing(w.Base)
				}
				if a.replayRoom != nil {
					a.replayRoom.NotifyAssetMissing(w.Base)
				}
				if a.makerPreviewRoom != nil {
					a.makerPreviewRoom.NotifyAssetMissing(w.Base)
				}
			}
			line := "Missing asset: " + w.Base
			if len(w.Tried) > 0 {
				line += " (tried " + strings.Join(w.Tried, " ") + " — see Settings → formats)"
			}
			a.pushDebug(line) // unconditional: the Debug ▸ Log tab is the detail view
			if show {
				// The chip, not warnLine; see assetmisschip.go for why.
				a.noteAssetMiss(w.Base, w.Type == assets.AssetTypeDeskOverlay, time.Now())
			}
		default:
			return
		}
	}
}

// drainMusicFailures shows the newest failure on the jukebox warn line, always, because the user asked for that track.
func (a *App) drainMusicFailures() {
	for {
		select {
		case f := <-a.d.Manager.MusicFailures():
			line := "Music didn't load (network) — " + shortMusicName(f.URL)
			a.pushDebug(line + ": " + f.Err.Error())
			a.jukeWarn(line)
		default:
			return
		}
	}
}

// shortMusicName trims a music URL to its final path segment so it fits the warn line.
func shortMusicName(url string) string {
	if i := strings.LastIndexByte(url, '/'); i >= 0 && i+1 < len(url) {
		return url[i+1:]
	}
	return url
}

func (a *App) warnActive() bool {
	return a.warnLine != "" && time.Since(a.warnAt) < warnShowDuration
}

// spriteFX builds the sprite colour-FX struct from prefs, once per frame without allocating.
func (a *App) spriteFX() render.SpriteFX {
	if a.d.Prefs.Load().DisableEffects {
		// The master off-switch keeps UserScaling:
		// it sets how big a sprite is drawn, and zeroing it would resize every character.
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

// cycleWeather advances the ambient weather and flashes the new name.
func (a *App) cycleWeather() {
	w := (a.d.Prefs.WeatherType() + 1) % int(render.WeatherCount)
	a.d.Prefs.SetWeatherType(w)
	a.warnLine = "Weather: " + render.WeatherName(render.Weather(w))
	a.warnAt = time.Now()
}

// postFX mirrors the post-processing toggles onto the viewport each frame.
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

// applySpriteOverrides applies the session's hide and move overrides to the active room.
func (a *App) applySpriteOverrides() { a.applySpriteOverridesTo(a.room) }

// applySpriteOverridesTo applies them to one room; they are user-level choices, so every live room needs them.
func (a *App) applySpriteOverridesTo(room *courtroom.Courtroom) {
	if room == nil {
		return
	}
	hideDesk := a.d.Prefs.Load().HideDesk
	if !hideDesk && len(a.spriteOv) == 0 && len(a.hiddenSprites) == 0 {
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
		// Hidden ("Missingno") sprites drop this session;
		// checked before the !Visible skip because they are otherwise visible.
		if _, hidden := a.hiddenSprites[key]; hidden {
			layer.Visible = false
			continue
		}
		if !layer.Visible {
			continue
		}
		if ov, ok := a.spriteOv[key]; ok {
			layer.OffsetX, layer.OffsetY = ov[0], ov[1]
		}
	}
}

// healScenery re-demands a live background, desk or sprite that T1 lost, which nothing else would, paced per base.
func (a *App) healScenery() {
	sc := &a.room.Scene
	now := time.Now()
	// The per-scene budget stops the loop when the working set exceeds T1:
	// each heal would evict another live base forever.
	if sc.BackgroundBase != "" && !(sc.BackgroundBase == a.bgAskBase && now.Sub(a.bgAskAt) < charIconRetryInterval) && !a.d.Store.Contains(sc.BackgroundBase) && a.sceneHealAllowed(sc.BackgroundBase) {
		a.bgAskBase, a.bgAskAt = sc.BackgroundBase, now
		a.d.Manager.Prefetch(sc.BackgroundBase, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	}
	// A conclusively 404'd base never becomes Contains, so without IsMissing this re-demands a known-absent desk.
	// The background arm skips the check: Background is never marked, to keep the viewport's hold-previous behaviour.
	if sc.DeskBase != "" && !(sc.DeskBase == a.deskAskBase && now.Sub(a.deskAskAt) < charIconRetryInterval) && !a.d.Store.Contains(sc.DeskBase) && !a.d.Store.IsMissing(sc.DeskBase) && a.sceneHealAllowed(sc.DeskBase) {
		a.deskAskBase, a.deskAskAt = sc.DeskBase, now
		a.d.Manager.Prefetch(sc.DeskBase, assets.AssetTypeDeskOverlay, network.PriorityHigh) // AssetType: DeskOverlay
	}
	a.healSpriteLayer(&sc.Speaker, &a.spkAskBase, &a.spkAskAt, now)
	a.healSpriteLayer(&sc.Pair, &a.pairAskBase, &a.pairAskAt, now)
}

// healSpriteLayer re-demands one visible sprite layer evicted from T1, through courtroom.SpriteHealAlts,
// paced per layer.
func (a *App) healSpriteLayer(layer *courtroom.SpriteLayer, askBase *string, askAt *time.Time, now time.Time) {
	if !layer.Visible || layer.Active == "" {
		return
	}
	if *askBase == layer.Active && now.Sub(*askAt) < charIconRetryInterval {
		return // recently asked for this exact base; let it land
	}
	if a.d.Store.Contains(layer.Active) {
		return // resident — nothing to heal
	}
	if !a.sceneHealAllowed(layer.Active) {
		return // per-scene futility budget spent (over-tier churn / permanent 404) — quiet until the scene changes
	}
	*askBase, *askAt = layer.Active, now
	a.d.Manager.PrefetchChain(layer.Active, courtroom.SpriteHealAlts(layer.Active), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (heal evicted live sprite: bare + folder)
	// Cold sprite with thumbnails on: also ask for the low-q stand-in; drainThumbs uploads it.
	if th := a.d.Manager.Thumbs(); th != nil && th.Enabled() && !a.d.Store.Contains(render.ThumbKeyPrefix+layer.Active) {
		th.RequestLoad(layer.Active)
	}
}

// icEntry is one IC log line with its AO text color preserved.
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
	// speech marks a line from an IC message, which has a counterpart in the play queue (see ghosttext.go).
	speech bool
	// styles holds inline-colour runs over the body; bodyRuneStart is the rune offset of the body within text.
	styles        []courtroom.StyleRun
	bodyRuneStart int
}

// icStampLayout formats the IC log's local time as 24-hour HH:MM.
const icStampLayout = "15:04"

// icStamp is the arrival time for a new line, formatted once on append so draws pay nothing.
func (a *App) icStamp() string { return a.now().Format(icStampLayout) }

// ignoreSpeaker reports whether an IC message is from an ignored player, by showname or character: MS carries no UID.
func (a *App) ignoreSpeaker(m *protocol.ChatMessage) bool {
	if m == nil || a.serverKey == "" {
		return false
	}
	name := strings.TrimSpace(m.Showname)
	if name == "" {
		name = strings.TrimSpace(m.CharName)
	}
	return a.d.Prefs.Load().ServerIgnoreMatch(a.serverKey, name)
}

// friendMessage reports whether a message is from a highlighted friend, and the friend's glow colour (-1 = default).
func (a *App) friendMessage(serverKey string, m *protocol.ChatMessage) (bool, int32) {
	if m == nil || serverKey == "" {
		return false, -1
	}
	// Skip entirely when no friend signal is on, the default.
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

// signalFriend fires the opt-in alerts when a friend speaks, in any tab; streamer mode and DND suppress them.
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
		// Custom sound, else the built-in ping: a theme's word_call may name a file that does not load.
		if f := a.d.Prefs.Load().FriendSoundFile; f != "" {
			a.d.Audio.PlayFile(f)
		} else {
			a.d.Audio.PlayAlert()
		}
	}
	// Only while tabbed away, and rate-limited so a chatty friend can't storm the desktop.
	if a.d.Prefs.Load().FriendOSToast && !a.ctx.WindowFocused() && time.Since(a.lastOSToast) >= osToastMinInterval {
		a.lastOSToast = time.Now()
		name := strings.TrimSpace(m.Showname)
		if name == "" {
			name = strings.TrimSpace(m.CharName)
		}
		body := name + " just spoke"
		if serverName != "" {
			body += " on " + serverName
		}
		a.d.Desktop.Notify("AsyncAO — friend online", body)
	}
}

// signalModcall pops a desktop toast for a modcall; streamer mode suppresses it because modcall lines can carry IPs.
func (a *App) signalModcall(serverName, notice string) {
	if !a.d.Prefs.Load().ModcallToast || a.d.Prefs.Load().StreamerModeOn || a.ctx.WindowFocused() {
		return // #M4: only toast when tabbed away (the in-app OOC + flash cover the focused case)
	}
	if time.Since(a.lastOSToast) < osToastMinInterval {
		return
	}
	a.lastOSToast = time.Now()
	body := strings.TrimSpace(notice)
	if serverName != "" {
		body = serverName + ": " + body
	}
	a.d.Desktop.Notify("AsyncAO — mod call", body)
}

// switchAreaScrollback swaps the IC log to toArea's history when opt-in per-area scrollback is on, in a bounded FIFO.
// AO only reports the area on an explicit click, so server-initiated moves keep the current log.
func (a *App) switchAreaScrollback(toArea string) {
	if !a.d.Prefs.Load().PerAreaScroll || toArea == a.curArea {
		return
	}
	if a.areaLogs == nil {
		a.areaLogs = map[string][]icEntry{}
	}
	// Save the area being left and track its key order for eviction.
	if _, seen := a.areaLogs[a.curArea]; !seen {
		a.areaLogOrder = append(a.areaLogOrder, a.curArea)
	}
	a.areaLogs[a.curArea] = a.icLog
	for len(a.areaLogOrder) > areaLogCacheMax {
		oldest := a.areaLogOrder[0]
		a.areaLogOrder = a.areaLogOrder[1:]
		if oldest != toArea { // never evict the log we're about to load
			delete(a.areaLogs, oldest)
		}
	}
	// nil means unvisited, so a fresh empty log.
	a.icLog = a.areaLogs[toArea]
	a.icLogSeq++ // invalidate the wrap + filter caches
	a.icScroll, a.icStick = 0, true
	a.icReadMark = len(a.icLog)
}

// logDetailed appends an IC message to the opt-in transcript; the writer does the disk write on its own goroutine.
// session is the calling tab's: the background path routes a parked tab's message while a different tab is live.
func (a *App) logDetailed(server string, session int, m *protocol.ChatMessage) {
	if m == nil || !a.d.Prefs.Load().DetailedLog {
		return
	}
	if w := a.transcriptFor(server, session); w != nil {
		w.write(detailedLogLine(time.Now(), m))
	}
}

// logDetailedOOC appends an OOC line to the same per-session transcript, as AO2 does (courtroom.cpp:1932).
func (a *App) logDetailedOOC(server string, session int, line string) {
	if !a.d.Prefs.Load().DetailedLog {
		return
	}
	if w := a.transcriptFor(server, session); w != nil {
		w.write(oocLogLine(time.Now(), line))
	}
}

// transcriptFor returns this tab session's transcript writer, opening it on first use;
// nil on error or at transcriptWriterCap.
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

// transcriptSeqFor is the 1-based ordinal of the next file for server this run,
// because names are timestamps to the second.
// Not logSession: that is process-global and would number a server's first file by how many tabs opened before it.
// A reconnect in the same second appends to the ended session's file, which is harmless and needs no unbounded map.
func (a *App) transcriptSeqFor(server string) int {
	n := 1
	for k := range a.translogs {
		if k.server == server {
			n++
		}
	}
	return n
}

// transcriptOwned reports whether a transcript key belongs to a session that exists: the live one or a parked tab's.
func (a *App) transcriptOwned(k translogKey) bool {
	if k.server == a.serverName && k.session == a.logSession {
		return true
	}
	for _, t := range a.tabs {
		if t != nil && t.state.serverName == k.server && t.state.logSession == k.session {
			return true
		}
	}
	return false
}

// reapTranscripts retires every transcript writer no session owns,
// by liveness because sessions end from many call sites.
// Retire, never close: the goroutine drains and closes the file so the render thread never waits on a flush (hard rule
// 2).
func (a *App) reapTranscripts() {
	for k, w := range a.translogs {
		if a.transcriptOwned(k) {
			continue
		}
		w.retire()
		delete(a.translogs, k)
	}
}

// CloseTranscript flushes and closes every writer at shutdown, the one path that waits for the flush.
func (a *App) CloseTranscript() {
	for k, w := range a.translogs {
		w.close()
		delete(a.translogs, k)
	}
}

func (a *App) pushIC(line string, color int, friend bool, friendColor int32, speaker string) {
	a.pushICStyled(line, color, friend, friendColor, speaker, nil, 0)
}

// pushICStyled is pushIC plus the inline-colour runs and the body's rune offset within the line.
func (a *App) pushICStyled(line string, color int, friend bool, friendColor int32, speaker string, styles []courtroom.StyleRun, bodyRuneStart int) {
	// Cap first: everything else describes the stored text. extractURLs splits on Fields and no codec rune is
	// whitespace,
	// so a trailing marker would fuse onto a link,
	// and a link past icLineCap would be offered from text no longer stored.
	line = capLogLine(line, speaker)
	url := ""
	if urls := extractURLs(line, 1); len(urls) > 0 {
		url = urls[0]
	}
	a.icLog = append(a.icLog, icEntry{text: line, color: color, url: url, friend: friend, friendColor: friendColor, speaker: speaker, stamp: a.icStamp(), styles: styles, bodyRuneStart: bodyRuneStart})
	if cap := a.icLogCapFor(); cap > 0 && len(a.icLog) > cap {
		copy(a.icLog, a.icLog[len(a.icLog)-cap:])
		a.icLog = a.icLog[:cap]
	}
	a.icLogSeq++ // invalidate the filter cache (len alone lies at the cap)
}

// icLogCapFor returns the live IC scrollback cap, 0 for no cap;
// the pref's 0 means icLogCap, ICLogDepthUnlimited means none.
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

// oocLineCap bounds one OOC entry against hostile servers, wide enough for real /help and MOTD texts,
// which wrap at draw.
const oocLineCap = 16384

// pushOOC appends an OOC line; speaker is "" for system lines,
// and the slices stay parallel so display needn't re-parse.
func (a *App) pushOOC(line, speaker string) {
	// OOC carries AsyncAO's DMs raw, so markers arrive here; the control frame was already decoded by routeIncomingPM.
	// The head is preserved, keeping the "speaker: " trim working and oocSpeakers a substring of oocLog.
	line = stripDisplayTail(line, speaker)
	// Harvest /getarea "[uid] name" rows from the raw text; a "ServerName: " prefix would hide every row.
	text := line
	if speaker != "" && strings.HasPrefix(line, speaker+": ") {
		text = line[len(speaker)+2:]
	}
	a.parseAreaBlock(text)
	isAreaList := looksLikeAreaList(text)
	if isAreaList {
		// Re-merge the IPIDs, the one field PR/PU lacks, so a mod's Refresh shows them at once;
		// rosterEqual stops ordinary lines.
		a.rebuildLiveRoster()
	}
	// A pull's own reply is parsed but kept out of the log;
	// only a fetch we sent arms the window, so others' /getarea shows.
	if isAreaList && a.now().Before(a.suppressAreaEchoUntil) {
		return // the entire /gas reply burst stays out of OOC, not just its first line
	}
	// A refusal is shown: every fetch is a button press, so the answer explains why it did nothing, and it cannot spam.
	// Mirror a received PM into the Friends-tab thread;
	// after the /gas return, so a suppressed burst can't double-fire it.
	a.detectIncomingPM(text)
	if len(line) > oocLineCap {
		line = line[:oocLineCap] + "…"
	}
	a.oocLog = appendCapped(a.oocLog, line, icLogCap)
	a.oocSpeakers = appendCapped(a.oocSpeakers, speaker, icLogCap)
	a.oocSeq++ // invalidate the wrapped-lines cache
	// Every active-tab OOC event funnels through here, like AO2's append_server_chatmessage,
	// so the transcript logs what's shown.
	a.logDetailedOOC(a.serverName, a.logSession, line) // detailed transcript (opt-in), this tab's own file
}

func appendCapped(list []string, line string, cap int) []string {
	list = append(list, line)
	if len(list) > cap {
		copy(list, list[len(list)-cap:])
		list = list[:cap]
	}
	return list
}

// speakerHeadLen is the byte length of the "<speaker>: " head, 0 if none;
// located by index since "Ace (Nick): " nests it.
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

// stripDisplayTail drops the zero-width sidechannel from a line's body and leaves the speaker head byte-identical.
// StripSpriteStyle is gated on the U+2060 sentinel but also deletes ZWJ and ZWNJ, which are ordinary text, so stripping
// the whole line lets a body marker delete runes from a name. Stripping the speaker alone is a no-op on exactly those
// names, and a cleaned name would stop matching the roster and logRowSplit, which need the raw one.
func stripDisplayTail(line, speaker string) string {
	head := speakerHeadLen(line, speaker)
	if head == 0 {
		return courtroom.StripSpriteStyle(line)
	}
	return line[:head] + courtroom.StripSpriteStyle(line[head:])
}

// capLogLine makes a composed line storable: strips the body's sidechannel, then bounds it against hostile servers.
// The order matters: the cap is measured on visible text, so a marker can't eat the budget or be cut in half.
// Every stored icEntry passes through here, and speaker is required so no caller can get the whole-line strip.
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
