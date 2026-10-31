package ui

// The AO2 screen-effect overlay picker (ui_effects_dropdown) and what the live overlay needs on the UI side.
// Identifiers are prefixed overlay: internal/theme owns EffectBind/EffectKind and internal/courtroom owns TextEffect*.

import (
	"bytes"
	"context"
	"os"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// overlayManifestCap bounds the settled roster cache per (origin, misc folder);
	// past it the map resets and re-resolves.
	// In-flight resolves live in overlayRosterInFlight so this reset can't spawn a duplicate that races the first.
	overlayManifestCap = 16
	// overlayRosterFetchCap bounds concurrent roster resolves (hard rule 4);
	// at the cap a folder resolves a frame later.
	overlayRosterFetchCap = 4
	// overlayManifestResCap equals overlayRosterFetchCap,
	// so the non-blocking send can never drop a result and strand a fetch.
	overlayManifestResCap = overlayRosterFetchCap
	// themeOverlayLoadCap bounds concurrent theme-tier art reads: a dropdown can ask for a dozen icons in one frame.
	themeOverlayLoadCap = 2
	// overlayTexResCap sizes the theme-tier art result channel.
	overlayTexResCap = 8
	// overlayNoneRow is the label AO2 prepends and selects (courtroom.cpp:5600-5604); we send "" for it, as webAO does
	// (client.html:489-494), which every receiver clears on and which can't be a real effect name.
	overlayNoneRow = "None"
	// overlayThemeKeyPrefix namespaces theme-tier overlay art in T1,
	// so an effect called "chat" can't shadow the chatbox skin.
	// Uploaded evictable, never pinned: an effects folder can hold ~100 MiB of art,
	// and pinned bytes are eviction-exempt.
	overlayThemeKeyPrefix = "theme://fx/"
	// overlayFetchTimeout bounds the misc-manifest fetch.
	overlayFetchTimeout = 8 * time.Second
	// overlayEffectsDDID is the widget id of ui_effects_dropdown;
	// a mismatch in overlayPreviewCloseEdge strands the preview.
	// Constant, so the tooltip's "-tip" concat folds at compile time.
	overlayEffectsDDID = "effectsdd"
	// overlayIconRowH and overlayIconThumbW size rows when the theme declares no effects_icon_size, like posThumbRowH.
	overlayIconRowH   = int32(28)
	overlayIconThumbW = int32(36)
	// overlayRealizationEffect is the one name canon special-cases: char.ini [Options] realization overrides its sound
	// (get_effect_property, text_file_functions.cpp:889-892).
	overlayRealizationEffect = courtroom.OverlayRealizationEffect
)

// overlayIconChipColor is the empty icon chip's fill; a package var so the settled dropdown draw doesn't allocate.
var overlayIconChipColor = sdl.Color{R: 10, G: 10, B: 14, A: 255}

// overlayRoster is one resolved effects roster; a miss still produces one, so map presence means settled
// and a pack without effects.ini is asked exactly once.
type overlayRoster struct {
	set *theme.OverlayFXSet
	// art and icon map a lowercased effect name to a local file;
	// absent leaves the misc origin chain as the only source.
	art  map[string]string
	icon map[string]string
}

// overlayRosterFetch is one settled resolve, posted from the worker goroutine.
type overlayRosterFetch struct {
	key string
	// gen is the themeAppliedGen the resolve started under; a resolve can outlast an apply, and its paths belong
	// to the old theme, so pollOverlayRosters drops a mismatch.
	gen    uint64
	roster *overlayRoster
}

type overlayTexLoad struct {
	key string
	dec *assets.Decoded
}

// overlayRosterKey keys the cache by origin and misc folder, so per-server separation is structural.
func (a *App) overlayRosterKey(folder string) string {
	return a.urls.Origin() + "|" + strings.ToLower(strings.TrimSpace(folder))
}

// overlayRosterFor answers from the cache and fires one async resolve on a miss, nil until it lands;
// render thread only.
func (a *App) overlayRosterFor(folder string) *overlayRoster {
	folder = strings.TrimSpace(folder)
	// A theme change invalidates every roster: the theme tier contributes names, properties and art paths.
	if a.overlayRosterGen != a.themeAppliedGen {
		a.overlayRosterGen = a.themeAppliedGen
		a.overlayRosters = nil
		a.overlayInvalidateMemos()
	}
	// Memoised because this runs on the zero-alloc courtroom frame and the cache key is a concatenation.
	if a.overlayMemoValid && a.overlayMemoFolder == folder && a.overlayMemoOrigin == a.urls.Origin() {
		return a.overlayMemoRoster
	}
	key := a.overlayRosterKey(folder)
	r, cached := a.overlayRosters[key]
	if !cached && !a.overlayRosterInFlight[key] {
		// Past the cap the settled cache resets;
		// the in-flight set is never wiped, or this call could spawn a duplicate.
		if len(a.overlayRosters) >= overlayManifestCap {
			a.overlayRosters = nil
		}
		if len(a.overlayRosterInFlight) < overlayRosterFetchCap {
			if a.overlayRosterInFlight == nil {
				a.overlayRosterInFlight = make(map[string]bool, overlayRosterFetchCap)
			}
			a.overlayRosterInFlight[key] = true // exactly one resolve per key
			a.overlayRosterFetchOne(key, folder)
		}
		r = nil
	}
	a.overlayMemoOrigin, a.overlayMemoFolder, a.overlayMemoValid = a.urls.Origin(), folder, true
	a.overlayMemoRoster = r
	return r
}

// overlayInvalidateMemos drops the per-frame memos when a theme apply, resolve or char.ini changes their inputs.
func (a *App) overlayInvalidateMemos() {
	a.overlayMemoValid = false
	a.overlayMemoRoster = nil
	a.overlayMyFolderValid = false
	a.overlayChoicesValid = false
	a.overlayChoicesRoster = nil
	a.overlayChoicesCache = nil
	// The baked icon keys use the same row numbers and are correct only for the roster that produced them.
	a.overlayIconKeys, a.overlayIconPaths = nil, nil
}

// overlayRosterFetchOne resolves one roster off the render thread: local ladder, origin manifest, merge, art paths.
// It stays demand-driven because every step depends on the misc folder, which arrives from a speaker's char.ini
// and is unknown at theme apply; walking every possible folder there would be speculative prefetch.
func (a *App) overlayRosterFetchOne(key, folder string) {
	if a.overlayRosterRes == nil {
		a.overlayRosterRes = make(chan overlayRosterFetch, overlayManifestResCap)
	}
	// Snapshot on the render thread: goroutine pref reads race the debounced saver. The theme is the object
	// pollThemeApply published, not a reload.
	t := a.overlayTheme
	// Compared when the result lands: a theme applied meanwhile moved every path this walk resolved.
	gen := a.themeAppliedGen
	manifestURL := ""
	if folder != "" && a.urls.Origin() != "" {
		manifestURL = a.urls.OverlayEffectManifest(folder)
	}
	mgr := a.d.Manager
	// Captured so the worker reads nothing off App (hard rule 8).
	res := a.overlayRosterRes
	go func() {
		roster := &overlayRoster{
			art:  map[string]string{},
			icon: map[string]string{},
		}
		var themeNames, localMiscNames []string
		var files [][]theme.OverlayFX
		if t != nil {
			themeFirst, themeAll := t.OverlayFXFiles()
			miscFirst, miscAll := t.OverlayFXMiscFiles(folder)
			themeNames = theme.OverlayFXNames(themeFirst)
			localMiscNames = theme.OverlayFXNames(miscFirst)
			// get_effect_property: every file of both tiers, merged last-wins.
			files = append(files, themeAll...)
			files = append(files, miscAll...)
		}
		// misc/<folder>/effects.ini, the one origin rung; a miss caches with the roster, so a pack is asked once.
		var remoteNames []string
		if manifestURL != "" && mgr != nil {
			ctx, cancel := context.WithTimeout(context.Background(), overlayFetchTimeout)
			// Layered: a pack in the user's own folders ships its effects.ini beside its art.
			if data, ferr := mgr.FetchRawLayered(ctx, manifestURL); ferr == nil {
				if remote, perr := theme.ParseOverlayFX(bytes.NewReader(data)); perr == nil {
					remoteNames = theme.OverlayFXNames(remote)
					// Last in the property pass: a manifest beside the art is the most specific file in the ladder.
					files = append(files, remote)
				}
			}
			cancel()
		}
		// get_effects: the first file of each tier, theme first, duplicates kept,
		// no re-sort (text_file_functions.cpp:770-831).
		// Deliberate divergence: a local walk can't interleave a network file,
		// so a theme-local misc effects.ini wins outright;
		// this differs from canon only when a theme has themes/<t>/effects.ini and the origin ships a misc manifest.
		list := append([]string(nil), themeNames...)
		if len(localMiscNames) > 0 {
			list = append(list, localMiscNames...)
		} else {
			list = append(list, remoteNames...)
		}
		roster.set = theme.MergeOverlayFX(list, files...)
		if t != nil {
			for _, name := range roster.set.Names() {
				lower := strings.ToLower(name)
				if _, seen := roster.art[lower]; seen {
					continue // duplicates in the display list resolve once
				}
				if p, ok := t.FindOverlayElement(folder, name); ok {
					roster.art[lower] = p
				}
				if p, ok := t.FindOverlayElementIcon(folder, name); ok {
					roster.icon[lower] = p
				}
			}
		}
		select {
		case res <- overlayRosterFetch{key: key, gen: gen, roster: roster}:
		default:
			// Unreachable while the caps are equal; kept so a future cap change cannot deadlock the goroutine.
		}
	}()
}

// pollOverlayRosters drains landed resolves and theme-tier art, and runs the right-click preview's close edge.
// The close edge lives here because both painter call sites are conditional, and a picker that stops drawing
// would strand a pinned preview box; this runs every frame on every screen.
func (a *App) pollOverlayRosters() {
	// One sprite-preview box per frame: set by whichever site draws it and cleared here, ahead of every screen draw.
	a.emoteGridDrewPreview = false
	a.overlayPreviewCloseEdge()
	if a.overlayRosterRes != nil {
		for drained := false; !drained; {
			select {
			case res := <-a.overlayRosterRes:
				// Freed whatever the verdict: a dropped result must still let the key resolve again.
				delete(a.overlayRosterInFlight, res.key)
				if res.gen != a.themeAppliedGen {
					// A theme apply landed in flight, so these paths and names belong to the previous theme;
					// overlayRosterFor
					// already dropped the map, so the next ask re-resolves. != says "not the theme on screen" and
					// survives a counter reset.
					continue
				}
				if a.overlayRosters == nil {
					a.overlayRosters = make(map[string]*overlayRoster, overlayManifestCap)
				}
				a.overlayRosters[res.key] = res.roster
				a.overlayInvalidateMemos()
				a.overlayIconPagesReset() // the row count just changed; cachedPage keys by INDEX
			default:
				drained = true
			}
		}
	}
	if a.overlayTexRes == nil {
		return
	}
	for {
		select {
		case res := <-a.overlayTexRes:
			delete(a.overlayTexLoading, res.key)
			if res.dec == nil {
				continue
			}
			if a.d.Store == nil {
				res.dec.Release()
				continue
			}
			// Upload, not UploadPinned (see overlayThemeKeyPrefix); an eviction means the next play re-reads the file.
			if err := a.d.Store.Upload(res.key, res.dec); err != nil {
				a.pushDebug("effect overlay upload failed: " + err.Error())
			}
		default:
			return
		}
	}
}

func overlayThemeKey(path string) string { return overlayThemeKeyPrefix + strings.ToLower(path) }

// overlayDemandTheme queues one local overlay file for an off-thread decode, bounded by themeOverlayLoadCap;
// idempotent.
func (a *App) overlayDemandTheme(path, key string) {
	if path == "" || key == "" || a.d.Store == nil {
		return
	}
	if a.d.Store.Contains(key) {
		return
	}
	if a.overlayTexLoading == nil {
		a.overlayTexLoading = make(map[string]bool, themeOverlayLoadCap)
	}
	if a.overlayTexLoading[key] || len(a.overlayTexLoading) >= themeOverlayLoadCap {
		return
	}
	if a.overlayTexRes == nil {
		a.overlayTexRes = make(chan overlayTexLoad, overlayTexResCap)
	}
	a.overlayTexLoading[key] = true
	anims := a.d.Prefs.Load().PreferAnimated
	// Captured on the render thread: the worker must read nothing off App (hard rule 8).
	out := a.overlayTexRes
	go func() {
		res := overlayTexLoad{key: key}
		if data, err := os.ReadFile(path); err == nil {
			if dec, derr := assets.DecodeImage(data, anims); derr == nil {
				res.dec = dec
			}
		}
		select {
		case out <- res:
		default:
			if res.dec != nil {
				res.dec.Release()
			}
		}
	}()
}

// overlayResolve is the courtroom's OverlayFor hook: an effect name and folder become merged properties and a T1 key.
// Local first, as canon orders the ladder; only a local miss runs the origin chain.
// ok=false means no art anywhere, which makes do_effect's clear reachable; Pending means the roster is still resolving.
func (a *App) overlayResolve(name, folder string) (courtroom.OverlayResolved, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return courtroom.OverlayResolved{}, false
	}
	out := courtroom.OverlayResolved{}
	roster := a.overlayRosterFor(folder)
	if roster == nil {
		// The roster carries the art paths and properties,
		// so reaching the origin now would fetch art the theme may hold
		// and arm the effect with zero-valued properties nothing corrects. Report Pending; the courtroom re-asks.
		out.Pending = true
		return out, true
	}
	if fx, ok := roster.set.Get(name); ok {
		out.FX = fx
	}
	if path, ok := roster.art[strings.ToLower(name)]; ok {
		key := overlayThemeKey(path)
		a.overlayDemandTheme(path, key)
		out.Base = key
		return out, true
	}
	// Rung 8, the one network rung; with no folder, origin or Manager the effect does not exist, and canon clears
	// (courtroom.cpp:3188-3194). Without the origin guard URLBuilder returns a relative path that becomes a junk T1
	// key.
	// Known limit: a .demo watched while connected resolves rung 8 against the connected host, not the recording's,
	// until the roster cache is keyed by the room's origin; the local ladder is unaffected.
	if a.urls.Origin() == "" || a.d.Manager == nil {
		return out, false
	}
	cands := a.urls.OverlayEffectMisc(folder, name)
	if len(cands) == 0 {
		return out, false
	}
	out.Base = cands[0]
	// AssetType: Effect (the one network rung of the effect ladder)
	a.d.Manager.PrefetchChain(cands[0], cands[1:], assets.AssetTypeEffect, network.PriorityHigh)
	return out, true
}

// overlayEffectsFolderFor is the EffectsFolderFor hook: char.ini [Options] effects from the cached char.ini fetch.
func (a *App) overlayEffectsFolderFor(char string) string {
	return a.charMetaFor(char).effects
}

// overlayCustomRealizationFor is the CustomRealizationFor hook:
// char.ini realization as an SFX url, "" if none or no origin.
func (a *App) overlayCustomRealizationFor(char string) string {
	name := a.charMetaFor(char).realization
	if name == "" || a.urls.Origin() == "" {
		return ""
	}
	return a.urls.SFX(name) // AssetType: SFX (per-character realization)
}

// overlayEffectSound is get_effect_property "sound", with realization replaced (text_file_functions.cpp:889-892).
// Deliberate divergence: canon puts a resolved local path on the wire for char.ini (courtroom.cpp:2299-2307),
// meaningless to other clients, so we send the bare name.
func (a *App) overlayEffectSound(folder, name string) string {
	if strings.EqualFold(strings.TrimSpace(name), overlayRealizationEffect) {
		if own := a.charMetaFor(a.activeCharName()).realization; own != "" {
			return own
		}
		return a.themeSounds[overlayRealizationEffect]
	}
	if fx, ok := a.overlayRosterFXFor(folder, name); ok {
		return fx.Sound
	}
	return ""
}

// myOverlayFolder is our character's effects folder for the outgoing field's middle part (courtroom.cpp:2296-2308);
// memoised because charMetaFor concatenates and this runs on the zero-alloc courtroom frame.
func (a *App) myOverlayFolder() string {
	char := a.activeCharName()
	if a.overlayMyFolderValid && a.overlayMyFolderChar == char {
		return a.overlayMyFolder
	}
	a.overlayMyFolderChar, a.overlayMyFolder, a.overlayMyFolderValid = char, a.overlayEffectsFolderFor(char), true
	return a.overlayMyFolder
}

// overlayChoices builds the dropdown list:
// "None", then roster order with duplicates, which keep row numbering canonical.
// Baked once per roster because this runs on every courtroom frame.
func (a *App) overlayChoices() []string {
	folder := a.myOverlayFolder()
	if a.overlayIconFolder != folder {
		// Icon pages are keyed by row index, so two same-size packs would draw each other's icons.
		a.overlayIconFolder = folder
		a.overlayIconPagesReset()
	}
	roster := a.overlayRosterFor(folder)
	if a.overlayChoicesValid && a.overlayChoicesRoster == roster {
		return a.overlayChoicesCache
	}
	a.overlayChoicesRoster, a.overlayChoicesValid, a.overlayChoicesCache = roster, true, nil
	a.overlayIconKeys, a.overlayIconPaths = nil, nil
	if roster == nil || roster.set == nil {
		return nil
	}
	names := roster.set.Names()
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names)+1)
	out = append(out, overlayNoneRow)
	a.overlayChoicesCache = append(out, names...)
	a.bakeOverlayIconKeys(folder, roster)
	return a.overlayChoicesCache
}

// bakeOverlayIconKeys precomputes each row's icon key and local file: dropdownEx paints the closed pick every frame,
// which must not allocate. Row 0 is empty: AO2 asks (courtroom.cpp:5606-5610) but no theme ships one, so no probe.
func (a *App) bakeOverlayIconKeys(folder string, roster *overlayRoster) {
	choices := a.overlayChoicesCache
	a.overlayIconKeys = make([]string, len(choices))
	a.overlayIconPaths = make([]string, len(choices))
	origin := a.urls.Origin()
	for i := 1; i < len(choices); i++ {
		name := choices[i]
		if p, ok := roster.icon[strings.ToLower(name)]; ok {
			a.overlayIconKeys[i], a.overlayIconPaths[i] = overlayThemeKey(p), p
			continue
		}
		if origin == "" {
			// Same origin guard as rung 8: an origin-less builder yields a relative path demandAsset would submit.
			continue
		}
		if cands := a.urls.OverlayEffectMiscIcon(folder, name); len(cands) > 0 {
			a.overlayIconKeys[i] = cands[0]
		}
	}
}

// overlayEffectsVisible mirrors AO2's hide rules (courtroom.cpp:5582-5613): spectating or an empty roster.
func (a *App) overlayEffectsVisible() bool {
	if a.sess == nil || a.sess.MyCharID < 0 {
		return false
	}
	return len(a.overlayChoices()) > 1 // "None" alone is an empty roster
}

// drawEffectsPicker draws ui_effects_dropdown, for the classic row and themed layout alike; visibility is checked here
// because a themed rect has no check of its own (courtroom.cpp:5586-5590 hides it from spectators).
func (a *App) drawEffectsPicker(rect sdl.Rect) {
	c := a.ctx
	if !a.overlayEffectsVisible() {
		return
	}
	choices := a.overlayChoices()
	if len(choices) <= 1 {
		return
	}
	if a.overlayChoiceIdx < 0 || a.overlayChoiceIdx >= len(choices) {
		a.overlayChoiceIdx = 0
	}
	if a.overlayThumbFn == nil {
		// Bound once: a per-frame closure would allocate on the settled path.
		a.overlayThumbFn = a.overlayIconDraw
	}
	rowH, thumbW := overlayIconRowH, overlayIconThumbW
	if a.overlayIconSize[0] > 0 && a.overlayIconSize[1] > 0 {
		// effects_icon_size (courtroom.cpp:979), plus thumbnail row padding.
		thumbW = int32(a.overlayIconSize[0]) + 4
		if h := int32(a.overlayIconSize[1]) + 6; h > rowH {
			rowH = h
		}
	}
	next, changed := c.DropdownThumbs(overlayEffectsDDID, rect, choices, a.overlayChoiceIdx, rowH, thumbW, a.overlayThumbFn)
	if changed {
		a.overlayChoiceIdx = next
		// on_effects_dropdown_changed also calls focus_ic_input (courtroom.cpp:5653-5657). Here and not in the arm
		// helpers:
		// toggleRealization goes through selectOverlayEffectByName and bounces on its own, which would double it.
		a.bounceFocusToIC()
	}
	// Right-click previews the art and sound without touching the selection;
	// AO2's menu here opens a folder, meaningless to us.
	if row, ok := c.DropdownRowRightClicked(overlayEffectsDDID); ok {
		a.previewOverlayRow(choices, row)
	}
	c.TooltipAfter(overlayEffectsDDID+"-tip", rect, "Screen effect sent with your messages (AO2 effects.ini). It stays picked until you set it back to None. Right-click a row to preview it without picking it.")
}

// overlayIconDraw paints one row's icon from the deferred overlay paint, only for the visible rows.
func (a *App) overlayIconDraw(i int, tr sdl.Rect) {
	c := a.ctx
	n := len(a.overlayChoices())
	if i < 0 || i >= n || i >= len(a.overlayIconKeys) {
		return
	}
	base := a.overlayIconKeys[i]
	path := a.overlayIconPaths[i]
	if base == "" {
		// Row 0 and rows with no art get the empty chip, never a probe.
		c.Fill(tr, overlayIconChipColor)
		c.Border(tr, ColPanelHi)
		return
	}
	if path != "" {
		// Idempotent, and it heals the page after an eviction: overlay art is uploaded evictable.
		a.overlayDemandTheme(path, base)
	}
	if page, ok := a.cachedPage(&a.overlayIconPages, &a.overlayIconGen, n, i, base); ok && len(page.Frames) > 0 {
		// Through the shared cgo scratch, never &tr: a parameter's address heap-escapes on every cgo call.
		c.cgoRect = tr
		_ = c.Ren.Copy(page.Frames[0], nil, &c.cgoRect)
	} else {
		c.Fill(tr, overlayIconChipColor)
		if path == "" {
			// AssetType: Effect (dropdown row icon, visible rows only, LOW priority)
			a.demandAsset(&a.overlayIconAsk, n, i, base, assets.AssetTypeEffect)
		}
	}
	c.Border(tr, ColPanelHi)
}

// previewOverlayRow opens the preview box on one effect's art and plays its sound, leaving the selection alone.
// The box is pinned because the pointer sits on a modal row, which would make closeSpritePreviewOnLeave kill it;
// overlayPreviewCloseEdge undoes the pin.
func (a *App) previewOverlayRow(choices []string, row int) {
	if row <= 0 || row >= len(choices) {
		// Clearing on "None" would fight the close-on-leave corridor.
		return
	}
	name := choices[row]
	folder := a.myOverlayFolder()
	res, ok := a.overlayResolve(name, folder)
	if ok && res.Base != "" {
		a.previewBase = res.Base
		a.previewFor = "" // restart the preview clock on the new pick (screens.go:1331-1335)
		a.previewPinned = true
		a.previewNameFor = res.Base
		a.previewName = name
		a.overlayPreviewBase = res.Base // ownership: the close edge takes down only this box
	}
	// OverlaySoundPlayable filters KFO's `sound = 0`,
	// which would otherwise burn a probe and 404-cache entries per format.
	// overlayEffectSound is what the send path uses, so the preview includes the realization override.
	if sound := a.overlayEffectSound(folder, name); courtroom.OverlaySoundPlayable(sound) {
		a.previewSFX(sound)
	}
}

// overlayPreviewCloseEdge takes the effect preview down when the list closes, once per frame; an edge, not a state.
// previewBase is shared, so if another trigger re-pointed the box this only forgets its claim.
func (a *App) overlayPreviewCloseEdge() {
	if a.overlayPreviewBase == "" || a.ctx == nil {
		return
	}
	if a.ctx.ddOpen == overlayEffectsDDID {
		return // still open: keep the box up across rows
	}
	if a.previewBase == a.overlayPreviewBase {
		// Through closeSpritePreview, never a raw previewBase = "": it also clears the pin and disarms the dwell id.
		a.closeSpritePreview()
	}
	a.overlayPreviewBase = ""
}

func (a *App) overlayRosterFXFor(folder, name string) (theme.OverlayFX, bool) {
	roster := a.overlayRosterFor(folder)
	if roster == nil || roster.set == nil {
		return theme.OverlayFX{}, false
	}
	return roster.set.Get(name)
}

// outgoingEffectsField builds the MS EFFECTS value "<name>|<folder>|<sound>" (courtroom.cpp:2296-2308), "" for None.
// Sound is "0" with Pre unchecked and an SFX set: AO2's anti-overlap rule.
func (a *App) outgoingEffectsField() string {
	choices := a.overlayChoices()
	if a.overlayChoiceIdx <= 0 || a.overlayChoiceIdx >= len(choices) {
		return ""
	}
	name := choices[a.overlayChoiceIdx]
	folder := a.myOverlayFolder()
	// Includes the realization special case (courtroom.cpp:2299 calls the same accessor).
	sound := a.overlayEffectSound(folder, name)
	if !a.icPreanim && a.sfxChoiceIdx > 0 {
		sound = "0"
	}
	return courtroom.BuildEffectsField(name, folder, sound)
}

// clearEffectsAfterSend applies AO2's post-send reset;
// courtroom.cpp:2348 resets only if the misnamed accessor is false.
// options.cpp:430-433 reads it from "stickyeffects", default true, so stock AO2 never clears unless the
// Sticky Effects box (aooptionsdialog.cpp:417) is unticked; Prefs.StickyEffects follows the same default.
func (a *App) clearEffectsAfterSend() {
	a.overlayPostSendReset(a.d.Prefs.Load().StickyEffects)
}

// overlayPostSendReset is the courtroom.cpp:2348-2354 rule:
// globalSticky keeps every pick, else it clears unless sticky.
func (a *App) overlayPostSendReset(globalSticky bool) {
	if globalSticky {
		return // canon's default: the pick rides every following line
	}
	choices := a.overlayChoices()
	if a.overlayChoiceIdx <= 0 || a.overlayChoiceIdx >= len(choices) {
		return
	}
	if fx, ok := a.overlayRosterFXFor(a.myOverlayFolder(), choices[a.overlayChoiceIdx]); ok && fx.Sticky {
		return // the effect declares sticky: the selection survives even then
	}
	a.overlayChoiceIdx = 0
}

// drawOverlayChatLayer paints the chat-rung overlay; AO2 reparents it onto the Courtroom (courtroom.cpp:3234-3237),
// so it must paint after the chatbox, not in Viewport.Render.
func (a *App) drawOverlayChatLayer(vp sdl.Rect) {
	if a.d.Viewport == nil || a.ctx == nil {
		return
	}
	// A themed layout can reach this with no chatbox rect, so renderScene's a.room deref needs its own guard.
	if a.room == nil && !(a.replaying && a.replayRoom != nil) {
		return
	}
	a.d.Viewport.DrawOverlayChat(a.ctx.Ren, a.renderScene(), vp)
}

// overlayIconPagesReset drops cached icon pages when the roster changes shape, because cachedPage keys by index.
func (a *App) overlayIconPagesReset() {
	a.overlayIconPages = nil
	a.overlayIconAsk = nil
}
