package ui

// The AO2 screen-effect overlay picker (ui_effects_dropdown) and everything the
// live overlay needs on this side of the fence: the per-(origin, misc folder)
// roster resolve, the theme-tier art loader, the per-row icons, the right-click
// preview, and the outgoing "<name>|<folder>|<sound>" field.
//
// NAMING FENCE (see docs/wip/EFFECTS-OVERLAY-SPEC.md §"Naming fence"):
// internal/theme owns EffectBind/EffectKind (widget decoration) and
// internal/courtroom owns TextEffect*/EffectSpan (animated chat TEXT). Every
// identifier here is prefixed overlay… / Overlay….

import (
	"bytes"
	"context"
	"os"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// overlayManifestCap bounds the SETTLED roster cache: one entry per (origin,
	// misc folder) pair. A busy server shows a handful of effect packs; past the cap
	// the map resets, because it is a cache and a re-resolve heals (the charMetaCap
	// idiom).
	//
	// In-flight resolves are NOT in this map (overlayRosterInFlight is its own set),
	// precisely so this reset cannot wipe one: a wiped marker lets the next ask
	// spawn a second goroutine for a key already being resolved, and the two
	// results then race — the loser writing the OLDER walk's paths over the newer.
	overlayManifestCap = 16
	// overlayRosterFetchCap bounds CONCURRENT roster resolves (hard rule 4). Each
	// one holds an 8 s network timeout and a bounded disk walk; four is past every
	// realistic burst (the speaker's folder, our own, and a couple of tab switches)
	// and, unlike the old shape, it is a bound BY CONSTRUCTION rather than an
	// assumption about how often the cap-reset runs. At the cap a new folder simply
	// resolves a frame later — overlayResolve reports Pending and the courtroom
	// re-asks.
	overlayRosterFetchCap = 4
	// overlayManifestResCap sizes the async result channel. Deliberately EQUAL to
	// overlayRosterFetchCap: at most that many resolves exist at once, so every
	// accepted resolve has a buffer slot waiting and the non-blocking send in
	// overlayRosterFetchOne can never actually drop one. That matters now that the
	// in-flight marker lives in its own set — a dropped result would strand a
	// fetch slot, where before it only stranded a cache entry.
	overlayManifestResCap = overlayRosterFetchCap
	// themeOverlayLoadCap bounds concurrent theme-tier art reads. Overlay art is
	// local files, but a roster can name 128 of them and a dropdown can ask for a
	// dozen icons in one frame.
	themeOverlayLoadCap = 2
	// overlayTexResCap sizes the theme-tier art result channel.
	overlayTexResCap = 8
	// overlayNoneRow is the label AO2 prepends to the list and selects
	// (courtroom.cpp:5600-5604). AO2 sends the TRANSLATED word; we send "" for it
	// (webAO's "||" shape, client.html:489-494) — every receiver clears identically
	// on both, and "" can never be mistaken for a real effect name.
	overlayNoneRow = "None"
	// overlayThemeKeyPrefix namespaces theme-tier overlay art in T1. It rides the
	// existing theme:// scheme (which cannot collide with an asset URL) with its
	// own sub-namespace so an effect called "chat" cannot shadow the chatbox skin.
	//
	// These pages are uploaded EVICTABLE, never UploadPinned: a theme's effects
	// folder is ~18 animated WebPs and at 256x192x4 x 30 frames that is ~6 MiB
	// each — pinning them would put ~100 MiB on TOP of the 256 MiB budget
	// (textures.go: pinned bytes are eviction-exempt AND count-uncapped), which is
	// the third repeat of the "all emote buttons identical" class.
	overlayThemeKeyPrefix = "theme://fx/"
	// overlayFetchTimeout bounds the misc-manifest fetch.
	overlayFetchTimeout = 8 * time.Second
	// overlayEffectsDDID is the widget id of ui_effects_dropdown. Named because
	// FOUR call sites must agree on it — the dropdown itself, the row right-click,
	// the tooltip, and overlayPreviewCloseEdge's "is the list still open" test —
	// and a typo in the last one would silently strand the preview box open.
	// Constant, so the tooltip's `+ "-tip"` folds at compile time (no per-frame
	// concat on the zero-alloc courtroom path).
	overlayEffectsDDID = "effectsdd"
	// overlayIconRowH / overlayIconThumbW size the open dropdown's rows when the
	// theme declares no effects_icon_size. Same shape as the Pos selector's
	// posThumbRowH / posThumbW, which this picker is a copy of by design.
	overlayIconRowH   = int32(28)
	overlayIconThumbW = int32(36)
	// overlayRealizationEffect is the ONE effect name canon special-cases: for it,
	// char.ini [Options] realization overrides the effects.ini `sound` outright
	// (get_effect_property, text_file_functions.cpp:889-892 → get_custom_realization
	// :896-903). It is the courtroom's constant, not a second spelling — the send
	// side is here and the receive side is there, and a private copy would drift.
	overlayRealizationEffect = courtroom.OverlayRealizationEffect
)

// overlayIconChipColor is the empty icon chip's fill — the "no art here" cell the
// None row and every unresolved row draw. A package-level var (not a per-call
// composite literal) so the settled dropdown draw stays allocation-free.
var overlayIconChipColor = sdl.Color{R: 10, G: 10, B: 14, A: 255}

// overlayRoster is one resolved effects roster: the display order, the merged
// property table and the LOCAL art/icon paths for every name in it.
// A resolve that MISSED still produces a roster (empty maps, empty set), so the
// map entry itself is the "settled" marker and a pack without an effects.ini is
// asked exactly once — the charMeta `done` idiom, expressed as presence rather
// than a flag. "In flight" is a separate set (overlayRosterInFlight) so the
// settled cache's cap-reset can never wipe a marker and spawn a duplicate.
type overlayRoster struct {
	set *theme.OverlayFXSet
	// art / icon map a lowercased effect name to a resolved LOCAL file path.
	// Absent = not on disk; the misc origin chain is then the only source.
	art  map[string]string
	icon map[string]string
}

// overlayRosterFetch is one settled resolve, posted from the worker goroutine.
type overlayRosterFetch struct {
	key string
	// gen is the themeAppliedGen the resolve was STARTED under. A resolve holds an
	// 8 s network timeout, so one begun before a theme apply can land long after
	// it — and its art/icon paths, its theme-tier property files and its display
	// order all belong to the OLD theme. Landing that as the new theme's roster is
	// the v1.81 theme-font stale-gen bug in another costume; pollOverlayRosters
	// drops anything whose gen no longer matches.
	gen    uint64
	roster *overlayRoster
}

// overlayTexLoad is one decoded theme-tier overlay image on its way to T1.
type overlayTexLoad struct {
	key string
	dec *assets.Decoded
}

// overlayRosterKey keys the cache by ORIGIN and misc folder, so per-server
// separation is structural (the repo's cache-key convention) and two servers'
// packs of the same name can never be confused.
func (a *App) overlayRosterKey(folder string) string {
	return a.urls.Origin() + "|" + strings.ToLower(strings.TrimSpace(folder))
}

// overlayRosterFor answers from the cache and fires ONE async resolve on a miss.
// Returns nil until it lands — the speaker's NEXT message (or the next frame the
// dropdown draws) picks it up, the same streaming shape as sprites and char.ini
// metadata. Render thread only.
func (a *App) overlayRosterFor(folder string) *overlayRoster {
	folder = strings.TrimSpace(folder)
	// A theme change invalidates every roster: the theme tier contributes both
	// names and properties, and its art paths move with it.
	if a.overlayRosterGen != a.themeAppliedGen {
		a.overlayRosterGen = a.themeAppliedGen
		a.overlayRosters = nil
		a.overlayInvalidateMemos()
	}
	// The memo, because this runs on the zero-alloc courtroom frame and the cache
	// KEY is a concatenation. Plain string compares, no allocation.
	if a.overlayMemoValid && a.overlayMemoFolder == folder && a.overlayMemoOrigin == a.urls.Origin() {
		return a.overlayMemoRoster
	}
	key := a.overlayRosterKey(folder)
	r, cached := a.overlayRosters[key]
	if !cached && !a.overlayRosterInFlight[key] {
		// The SETTLED cache is a cache: past the cap it resets and a re-resolve heals.
		// The in-flight SET is not part of it and is never wiped here — see
		// overlayManifestCap. A reset that dropped a marker would let this very call
		// spawn a duplicate goroutine for a key already being resolved.
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

// overlayInvalidateMemos drops the per-frame memos. Called whenever one of their
// inputs changes: a theme apply (new rosters), a landed resolve, and a landed
// char.ini (the character's own effects folder may have just arrived).
func (a *App) overlayInvalidateMemos() {
	a.overlayMemoValid = false
	a.overlayMemoRoster = nil
	a.overlayMyFolderValid = false
	a.overlayChoicesValid = false
	a.overlayChoicesRoster = nil
	a.overlayChoicesCache = nil
	// The baked icon keys belong to that cache — they are indexed by the SAME row
	// numbers and are only correct for the roster that produced them.
	a.overlayIconKeys, a.overlayIconPaths = nil, nil
}

// overlayRosterFetchOne resolves one roster OFF the render thread: the local
// effects.ini ladder, the one origin manifest, the merge, and the local art/icon
// paths. Every step is disk or network work — hard rule 2 keeps all of it here.
//
// SPEC §2.4, and the one piece of it that cannot be honoured literally.
// The spec puts the whole local half on the theme-apply goroutine ("parse the
// theme-tier effects.ini files … resolve PATHS ONLY for each named effect's art
// and icon … store name → path in the theme result"). The theme-tier half is
// exactly that now: applyThemeAsync publishes the loaded *theme.Theme and this
// goroutine walks it instead of re-running theme.Load, so a resolve no longer
// re-reads four INIs and rebuilds the directory ladder per (origin, folder).
//
// The rest CANNOT move, and it is worth being explicit about why rather than
// leaving it looking like an oversight: every remaining step is a function of the
// character's misc FOLDER, which is not known at theme-apply time and never can
// be. Rungs 5-6 (themes/T/[S/]misc/M/<E>), rung 8's local twin (misc/M/<E>) and
// the whole misc-tier effects.ini list all take M as an argument, and M arrives
// from a SPEAKER's char.ini — a per-message, per-server value. Resolving them at
// theme apply would mean either resolving nothing (the theme knows no folders) or
// speculatively walking every folder the server might ever name, which is the
// prefetch-at-theme-load that §2.4 exists to forbid. So the folder-dependent walk
// stays demand-driven, once per (origin, folder), cached, and off-thread — which
// is the same shape §2.4 prescribes for the misc manifest one paragraph later.
func (a *App) overlayRosterFetchOne(key, folder string) {
	if a.overlayRosterRes == nil {
		a.overlayRosterRes = make(chan overlayRosterFetch, overlayManifestResCap)
	}
	// Snapshot everything the goroutine needs on the RENDER thread: reading prefs
	// off-thread while the debounced saver marshals is the interleaving -race
	// exists to catch (the applyThemeAsync precedent).
	//
	// The THEME is snapshotted as the already-loaded object pollThemeApply
	// published, not re-loaded here (spec §2.4 — the theme-tier parse belongs to the
	// theme apply). It also removes the last two preference reads this goroutine
	// used to need: the theme name/dir/subtheme are baked into the loaded object,
	// which is by definition the ladder the rest of the UI is drawing from.
	t := a.overlayTheme
	// The apply generation this resolve belongs to. Compared again when the result
	// lands: a theme applied in the meantime moved every path this walk resolved.
	gen := a.themeAppliedGen
	manifestURL := ""
	if folder != "" && a.urls.Origin() != "" {
		manifestURL = a.urls.OverlayEffectManifest(folder)
	}
	mgr := a.d.Manager
	// The RESULT CHANNEL is captured here, on the render thread, rather than read
	// as a.overlayRosterRes from inside the goroutine. Race-free today only because
	// the field is written once (just above) and never reset — any future teardown
	// that nils it would be an instant -race failure (hard rule 8) on a field a
	// worker is reading concurrently. A captured local cannot go stale, and it also
	// says out loud that the goroutine owns nothing on App.
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
			// get_effect_property: EVERY file of both tiers, merged LAST-WINS.
			files = append(files, themeAll...)
			files = append(files, miscAll...)
		}
		// The one ORIGIN rung: misc/<folder>/effects.ini. Its miss caches with the
		// roster itself (done either way), so a pack without one is asked once.
		var remoteNames []string
		if manifestURL != "" && mgr != nil {
			ctx, cancel := context.WithTimeout(context.Background(), overlayFetchTimeout)
			if data, ferr := mgr.FetchRaw(ctx, manifestURL); ferr == nil {
				if remote, perr := theme.ParseOverlayFX(bytes.NewReader(data)); perr == nil {
					remoteNames = theme.OverlayFXNames(remote)
					// LAST in the property pass: a manifest shipped beside the art is
					// the most specific file in the ladder, which is where canon puts
					// the base-misc rung too.
					files = append(files, remote)
				}
			}
			cancel()
		}
		// get_effects: the FIRST file of EACH tier, theme first, CONCATENATED with
		// duplicates preserved and no re-sort (text_file_functions.cpp:770-831).
		//
		// DIVERGENCE, small and deliberate: canon's misc tier is one ordered path
		// list in which the base-misc rung (8) sits between the theme-local misc
		// rungs (5-6) and the theme-root rungs (9-11). Our local walk cannot
		// interleave a NETWORK file, so a theme-local misc effects.ini wins the list
		// outright and the origin manifest supplies it otherwise. The only shape
		// that diverges is a theme shipping themes/<t>/effects.ini (rung 9) while the
		// origin also ships misc/<folder>/effects.ini — a file at the theme ROOT
		// rather than in effects/, which no theme in the reference corpus has.
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
			// Unreachable by construction (overlayManifestResCap == overlayRosterFetchCap),
			// kept so a future cap change cannot deadlock the resolver goroutine.
		}
	}()
}

// pollOverlayRosters is the effects picker's per-frame housekeeping: it drains
// landed resolves and landed theme-tier art, and runs the right-click preview's
// close edge (one call per frame from the poll cluster, app.go; no-op with
// nothing pending).
//
// The close edge lives HERE rather than in drawEffectsPicker because BOTH call
// sites of the painter are conditional — the classic row draws it only while
// overlayEffectsVisible() and the bar has room (screens.go:6614), the themed
// layout only when the design.ini declares the rect (theme_layout.go:1129) — so a
// picker that stops drawing (a PV to spectator, a roster emptied by a theme
// change, a window narrowed past the fit guard) would strand a PINNED preview box
// with only its own x to close it. The poll cluster runs every frame on every
// screen, which is exactly the coverage a pin needs.
func (a *App) pollOverlayRosters() {
	// One frame, one sprite-preview box. The emote grid owns the box's primary draw
	// site and drawSpritePreviewFallback stands in for it when the grid did not run
	// (hidden panel, no themed `emotes` rect, char.ini still loading) — the right-
	// click effect preview is opened from the IC bar and must appear in all of them.
	// The latch is set by whichever site draws and cleared here, once per frame,
	// ahead of every screen draw (app.go's poll cluster).
	a.emoteGridDrewPreview = false
	a.overlayPreviewCloseEdge()
	if a.overlayRosterRes != nil {
		for drained := false; !drained; {
			select {
			case res := <-a.overlayRosterRes:
				// The fetch slot is freed whatever the verdict — a dropped result must
				// still let the key be resolved again.
				delete(a.overlayRosterInFlight, res.key)
				if res.gen != a.themeAppliedGen {
					// A theme apply landed while this was in flight. Its art/icon paths,
					// its theme-tier property files and its display order all belong to
					// the PREVIOUS theme, so filing it under the new one would show the
					// old theme's effects (and load the old theme's art) until something
					// else invalidated the cache. This is the v1.81 theme-font stale-gen
					// fix applied to the same class of async landing. overlayRosterFor
					// has already dropped the whole map for the new gen, so the next ask
					// re-resolves against the theme that is actually applied.
					//
					// `!=`, where the cited precedent writes `<`, is deliberate and the
					// two coincide: themeAppliedGen only ever moves FORWARD (pollThemeApply
					// assigns the landing gen, and applyThemeAsync hands out increasing
					// ones), so a result can never carry a gen NEWER than the applied one
					// and `res.gen < applied` and `res.gen != applied` name the same set.
					// The inequality is the honest spelling of the rule being enforced —
					// "this result belongs to a different theme than the one on screen" —
					// and it stays correct if the counter ever wraps or is reset, which
					// `<` would not.
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
			// Upload, NOT UploadPinned — see overlayThemeKeyPrefix. An eviction
			// simply means the next play re-reads the file.
			if err := a.d.Store.Upload(res.key, res.dec); err != nil {
				a.pushDebug("effect overlay upload failed: " + err.Error())
			}
		default:
			return
		}
	}
}

// overlayThemeKey is the T1 key a LOCAL overlay file lives under.
func overlayThemeKey(path string) string { return overlayThemeKeyPrefix + strings.ToLower(path) }

// overlayDemandTheme queues one local overlay file for an off-thread read +
// decode, bounded by themeOverlayLoadCap in flight. Idempotent: a key already
// resident or already loading costs a map probe. Render thread only.
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
	anims := a.d.Prefs.AnimationsEnabled()
	// Captured on the render thread for the reason overlayRosterFetchOne captures
	// its own: the worker must read nothing off App (hard rule 8).
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

// overlayResolve is the courtroom's OverlayFor hook: an effect NAME + misc
// FOLDER become merged properties and a T1 key / URL.
//
// LOCAL FIRST, exactly as canon orders the ladder: a path the roster already
// resolved (rungs 1-11 on disk, resolved off-thread when the roster was built) is
// read + decoded off-thread and landed by the poll cluster. Only when nothing is
// local does the ONE origin chain run — PrefetchChain over the ≤2 spellings of
// misc/<folder>/<name>, exactly like the shout bubble.
//
// It returns ok=false only when there is no art anywhere to point at, which is
// what makes do_effect's "unknown effect name clears the overlay" reachable — and
// Pending while the folder's roster is still resolving, because until it lands
// neither half of the local answer (the rung 1-7 art paths, the merged
// properties) exists and the ladder cannot be walked in order.
// Render thread only.
func (a *App) overlayResolve(name, folder string) (courtroom.OverlayResolved, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return courtroom.OverlayResolved{}, false
	}
	out := courtroom.OverlayResolved{}
	roster := a.overlayRosterFor(folder)
	if roster == nil {
		// The roster resolve for this (origin, folder) is IN FLIGHT — the call above
		// started it. It carries both halves of the local answer: the art paths for
		// ladder rungs 1-7 (resolved off-thread from disk) and the merged effects.ini
		// properties. Falling through to the origin chain here would ask the network
		// for art the theme may already hold on disk — spec §1.f, "rung 8 is the ONLY
		// network rung", reached only when the whole LOCAL ladder misses — and would
		// arm the effect with a zero-valued OverlayFX (chat layer, no loop/stretch/
		// cull, no max_duration) that nothing would ever correct, which is precisely
		// the marquee case: the first message from any character shipping its own
		// misc effects pack.
		//
		// So report PENDING. The courtroom holds the request and re-asks
		// (courtroom/overlayfx.go tickOverlayPending) until this resolve lands, then
		// walks the full ladder with the real properties. Nothing is fetched
		// speculatively: the resolve was demanded by this very effect firing (§2.4,
		// "on demand, NEVER at theme load").
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
	// The misc tier at the ORIGIN — rung 8, the one network rung. Nothing to ask
	// when the character declares no folder, when there is no origin, or when there
	// is no asset Manager to ask with: an effect with neither local art nor a
	// reachable origin simply does not exist, and canon clears for exactly that
	// (courtroom.cpp:3188-3194).
	//
	// The origin guard is the one EVERY origin-dependent path in this package
	// carries (charMetaFor charmeta.go:58, previewSFX sfxbrowser.go:56, warmCharINI
	// app.go:7510). Without it URLBuilder concatenates an empty host and hands back
	// the RELATIVE "misc/<folder>/<name>", which would be submitted to the worker
	// pool (manager.go only rejects an EMPTY base) and stored as
	// Scene.Overlay.Base — a permanent junk T1 key. That is reachable today: a
	// .demo replayed from the LOBBY has no origin at all.
	//
	// KNOWN LIMIT, recorded rather than papered over: these hooks close over the
	// APP's a.urls, while a replay room is built with its own
	// courtroom.NewURLBuilder(rec.Origin) (replay.go:609-615). So a .demo watched
	// while connected somewhere else resolves rung 8 against the CONNECTED host
	// instead of the recording's — a miss on that host, ≤2 404-cached probes, and
	// the origin-free local ladder (rungs 1-7) is unaffected. Threading the room's
	// own builder into OverlayFor is OWED: it needs the roster cache keyed by the
	// ROOM's origin rather than the App's.
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

// overlayEffectsFolderFor is the courtroom's EffectsFolderFor hook: the
// speaker's char.ini [Options] effects, answered from the SAME per-URL cache and
// the SAME single fetch that already back the blip set and the chatbox skin — so
// honouring a character's own effect pack costs zero extra network work.
func (a *App) overlayEffectsFolderFor(char string) string {
	return a.charMetaFor(char).effects
}

// overlayCustomRealizationFor is the courtroom's CustomRealizationFor hook: the
// speaker's char.ini [Options] realization as a full SFX url, "" when the
// character declares none (the courtroom then falls back to the theme's
// courtroom_sounds.ini value, exactly as canon does).
//
// Same origin guard every other origin-dependent path in this package carries —
// an origin-less URLBuilder would hand back a relative "sounds/general/<name>".
func (a *App) overlayCustomRealizationFor(char string) string {
	name := a.charMetaFor(char).realization
	if name == "" || a.urls.Origin() == "" {
		return ""
	}
	return a.urls.SFX(name) // AssetType: SFX (per-character realization)
}

// overlayEffectSound is get_effect_property(fx, char, folder, "sound") — the
// merged effects.ini value, with canon's ONE special case applied:
// fx == "realization" has its sound replaced wholesale by get_custom_realization
// (text_file_functions.cpp:889-892), i.e. by char.ini [Options] realization,
// falling back to the theme's courtroom_sounds.ini "realization".
//
// DIVERGENCE, deliberate: canon's get_custom_realization returns a RESOLVED LOCAL
// PATH for the char.ini case (get_sfx_suffix(get_sounds_path(...)), :902) and AO2
// then puts that path straight on the wire (courtroom.cpp:2299-2307). A local
// absolute path is meaningless to every other client and to our own URL builder,
// so we send the bare NAME — which is what the theme branch sends in canon too,
// and what every receiver can resolve.
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

// myOverlayFolder is OUR OWN character's effects folder — the value that rides
// the outgoing field's middle slot (courtroom.cpp:2296-2308).
//
// Memoised on the active character name (a plain string compare) because the
// underlying charMetaFor builds a char.ini URL by concatenation and this is asked
// on every courtroom frame, which is zero-alloc-gated. pollCharMeta drops the
// memo when a fetch lands.
func (a *App) myOverlayFolder() string {
	char := a.activeCharName()
	if a.overlayMyFolderValid && a.overlayMyFolderChar == char {
		return a.overlayMyFolder
	}
	a.overlayMyFolderChar, a.overlayMyFolder, a.overlayMyFolderValid = char, a.overlayEffectsFolderFor(char), true
	return a.overlayMyFolder
}

// overlayChoices builds the dropdown's option list: "None" at index 0, then the
// roster's display order verbatim (theme file ++ misc file, duplicates preserved
// — the wire carries the NAME, not the index, so a duplicate is harmless and
// deduping would diverge from every other client's row numbering).
// Baked once per roster: the copy and the prepend both allocate, and this is
// asked on every courtroom frame (the zero-alloc gate).
func (a *App) overlayChoices() []string {
	folder := a.myOverlayFolder()
	if a.overlayIconFolder != folder {
		// An iniswap or character swap into a DIFFERENT effects pack: the icon pages
		// are keyed by ROW INDEX (cachedPage), and cachedPage only reallocates when
		// the row COUNT changes — two packs of the same size would otherwise draw
		// each other's icons. A plain string compare, so the settled frame pays
		// nothing.
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

// bakeOverlayIconKeys precomputes every row's icon page KEY, and for the theme
// tier the LOCAL file that key is decoded from, at roster-build time — spec §3,
// "icon page keys are precomputed when the list is built, not per frame".
//
// This is load-bearing rather than tidiness. dropdownEx paints the CLOSED
// control's current pick through the same thumb painter on EVERY frame
// (ui.go:3938-3942), so a painter that concatenated overlayThemeKey(path) — or
// called URLBuilder.OverlayEffectMiscIcon, which mints a candidate SLICE —
// allocated on every settled courtroom frame from the moment an effect was
// picked, under the whole-screen AllocsPerRun gate. Baked here it is two slice
// reads.
//
// Row 0 ("None") deliberately gets an EMPTY key: AO2 asks for an icon for it too
// (courtroom.cpp:5606-5610) but no theme in the corpus ships one, so it draws the
// empty chip rather than burning a probe per open.
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
			// Same origin guard as the resolve's rung 8: an origin-less builder yields
			// a RELATIVE "misc/<folder>/icons/<name>", and demandAsset would submit it.
			continue
		}
		if cands := a.urls.OverlayEffectMiscIcon(folder, name); len(cands) > 0 {
			a.overlayIconKeys[i] = cands[0]
		}
	}
}

// overlayEffectsVisible mirrors AO2's hide rules for ui_effects_dropdown
// (courtroom.cpp:5582-5613): hidden while SPECTATING (m_cid == -1) and hidden
// when the roster is empty.
func (a *App) overlayEffectsVisible() bool {
	if a.sess == nil || a.sess.MyCharID < 0 {
		return false
	}
	return len(a.overlayChoices()) > 1 // "None" alone is an empty roster
}

// drawEffectsPicker draws ui_effects_dropdown at rect. Shared by the classic IC
// row and the themed layout so the two can never drift, exactly like
// drawThemedSFXPicker.
//
// The visibility gate lives HERE, not at the call sites: the classic row also
// consults overlayEffectsVisible to decide whether to advance its cursor, but the
// themed layout draws straight into a design.ini rect and had no gate at all,
// which showed the picker to SPECTATORS (courtroom.cpp:5586-5590 hides it on
// m_cid == -1). One gate inside the shared painter is the only shape in which the
// two paths cannot drift again.
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
		// Bound ONCE (the posThumbFn idiom): a per-frame closure here would
		// allocate on the settled path, which the whole-screen alloc gate catches.
		a.overlayThumbFn = a.overlayIconDraw
	}
	rowH, thumbW := overlayIconRowH, overlayIconThumbW
	if a.overlayIconSize[0] > 0 && a.overlayIconSize[1] > 0 {
		// effects_icon_size (courtroom.cpp:979), plus the padding every thumbnail
		// dropdown row carries.
		thumbW = int32(a.overlayIconSize[0]) + 4
		if h := int32(a.overlayIconSize[1]) + 6; h > rowH {
			rowH = h
		}
	}
	next, changed := c.DropdownThumbs(overlayEffectsDDID, rect, choices, a.overlayChoiceIdx, rowH, thumbW, a.overlayThumbFn)
	if changed {
		a.overlayChoiceIdx = next
	}
	// Right-click a row → PREVIEW it (idiom A, the HoverPreview shape): the
	// overlay art in the preview box and the effect's own sound, without touching
	// the selection. AO2's context menu here only offers "open the effects folder
	// in your file browser", which is meaningless for a streaming client.
	if row, ok := c.DropdownRowRightClicked(overlayEffectsDDID); ok {
		a.previewOverlayRow(choices, row)
	}
	c.TooltipAfter(overlayEffectsDDID+"-tip", rect, "Screen effect sent with your messages (AO2 effects.ini). It stays picked until you set it back to None. Right-click a row to preview it without picking it.")
}

// overlayIconDraw paints one dropdown row's icon. Runs on the render thread from
// the deferred overlay paint (FinishFrame), which is what makes the on-demand
// page lookup and prefetch below legal — and it is called only for the ≤12
// VISIBLE rows, so a 27-effect roster never fires 27 probes in a frame.
func (a *App) overlayIconDraw(i int, tr sdl.Rect) {
	c := a.ctx
	n := len(a.overlayChoices())
	if i < 0 || i >= n || i >= len(a.overlayIconKeys) {
		return
	}
	base := a.overlayIconKeys[i]
	path := a.overlayIconPaths[i]
	if base == "" {
		// Row 0 ("None") and any row with no art anywhere: the empty chip, never a
		// probe. See bakeOverlayIconKeys.
		c.Fill(tr, overlayIconChipColor)
		c.Border(tr, ColPanelHi)
		return
	}
	if path != "" {
		// Idempotent (a resident or already-loading key costs a map probe) and it is
		// what HEALS the page after an eviction — overlay art is uploaded evictable.
		a.overlayDemandTheme(path, base)
	}
	if page, ok := a.cachedPage(&a.overlayIconPages, &a.overlayIconGen, n, i, base); ok && len(page.Frames) > 0 {
		// Through the shared cgo scratch, never &tr: taking the address of a
		// parameter heap-escapes on EVERY cgo call (the ui.go cgoRect contract), and
		// the closed control paints this on every settled frame.
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

// previewOverlayRow opens the sprite-preview box on one effect's art and plays
// its sound — the sound IS part of the effect, so auditioning without it would
// be auditioning half of it.
//
// It deliberately does NOT change the dropdown selection (that is left-click's
// job) and does not touch sfxChoiceIdx. previewBase is a single shared slot, so
// the box takes the overlay-owner mark through drawSpritePreview's own
// pushOverlayOwner.
//
// The box is PINNED while it is up, because the pointer is parked on a dropdown
// ROW: the modal capture blanks hovering() for everything else, so both of
// closeSpritePreviewOnLeave's tests read false and its "strayed off the travel
// path" arm would kill the box on the very next frame. The pin is undone by
// overlayPreviewCloseEdge when the list closes (spec §1.d, "must clear on
// dropdown close") — closeSpritePreview is NOT otherwise on this path, it is
// reached only from the box's own x or a click on a non-modal surface.
func (a *App) previewOverlayRow(choices []string, row int) {
	if row <= 0 || row >= len(choices) {
		// Row 0 is "None": nothing to preview, and clearing here would fight the
		// close-on-leave corridor.
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
	// The SAME sentinel filter the message path uses (courtroom.OverlaySoundPlayable,
	// spec §1.b). previewSFX rejects only "" and the empty origin, and KFO's stock
	// roster writes `sound = 0` for "no sound" — so an unfiltered preview mints
	// urls.SFX("0") and burns a real probe plus a 404-cache entry per format in the
	// SFX fallback chain, once per row right-clicked. That is the exact probe the
	// shared filter exists to stop; a private copy of the list here would drift.
	// The same accessor the send path uses, so the audition is what would actually
	// ride the wire — including the realization override (a character with its own
	// [Options] realization must hear THAT, not the pack's default).
	if sound := a.overlayEffectSound(folder, name); courtroom.OverlaySoundPlayable(sound) {
		a.previewSFX(sound)
	}
}

// overlayPreviewCloseEdge takes the right-click effect preview down when the
// effects list closes — spec §1.d, "must clear on dropdown close".
//
// Called once per frame from pollOverlayRosters (see the coverage note there). It
// is an EDGE, not a state check: while the list is open the box must survive
// (auditioning a second row re-points it), and the instant the list is gone the
// pin that kept it alive has to go with it.
//
// previewBase is one shared slot, so ownership is checked before closing: if some
// other trigger has re-pointed the box since, this only forgets its claim.
func (a *App) overlayPreviewCloseEdge() {
	if a.overlayPreviewBase == "" || a.ctx == nil {
		return
	}
	if a.ctx.ddOpen == overlayEffectsDDID {
		return // still open: keep the box up across rows
	}
	if a.previewBase == a.overlayPreviewBase {
		// Routed through closeSpritePreview, never a raw previewBase = "": it also
		// clears the pin latch and disarms the dwell id (screens.go:1563-1576), the
		// trap the five earlier dismissal sites fell into.
		a.closeSpritePreview()
	}
	a.overlayPreviewBase = ""
}

// overlayRosterFXFor is a small accessor so the preview and the send path do not
// each re-derive the roster lookup.
func (a *App) overlayRosterFXFor(folder, name string) (theme.OverlayFX, bool) {
	roster := a.overlayRosterFor(folder)
	if roster == nil || roster.set == nil {
		return theme.OverlayFX{}, false
	}
	return roster.set.Get(name)
}

// outgoingEffectsField builds the MS EFFECTS value for the message being sent:
// "<name>|<folder>|<sound>" (courtroom.cpp:2296-2308), or "" for the "None" row.
//
// sound is forced to "0" when Pre is UNCHECKED and a custom/dropdown SFX is set —
// AO2's anti-overlap rule (:2302-2305): the effect's own sound would otherwise
// play on top of the sound the user explicitly chose for this line.
func (a *App) outgoingEffectsField() string {
	choices := a.overlayChoices()
	if a.overlayChoiceIdx <= 0 || a.overlayChoiceIdx >= len(choices) {
		return ""
	}
	name := choices[a.overlayChoiceIdx]
	folder := a.myOverlayFolder()
	// get_effect_property(effect, current_char, folder, "sound") — including its
	// realization special case (courtroom.cpp:2299 calls the very same accessor).
	sound := a.overlayEffectSound(folder, name)
	if !a.icPreanim && a.sfxChoiceIdx > 0 {
		sound = "0"
	}
	return courtroom.BuildEffectsField(name, folder, sound)
}

// clearEffectsAfterSend applies AO2's post-send reset to the effects dropdown.
//
// CANON, read carefully because the upstream accessor is misnamed:
// courtroom.cpp:2348 resets only when clearEffectsDropdownOnPlayEnabled() is
// FALSE, and options.cpp:430-433 implements it as
// config.value("stickyeffects", true) — the config KEY is "stickyeffects" and it
// defaults TRUE. So the guard is false by default and a stock AO2 client NEVER
// clears the effects pick after a send; the per-effect `sticky` property only
// starts to matter once the user unticks the Sticky Effects box
// (aooptionsdialog.cpp:417).
//
// AsyncAO therefore answers canon's shipped default. The user-facing toggle is
// OWED, not implemented: this wave adds no settings surface (the parallel
// theme-editor wave owns internal/ui/settings.go), so the global is the named
// constant below and lands as one argument when the pref exists.
func (a *App) clearEffectsAfterSend() {
	a.overlayPostSendReset(overlayEffectsStickyDefault)
}

// overlayEffectsStickyDefault is AO2's shipped `stickyeffects` value (TRUE).
const overlayEffectsStickyDefault = true

// overlayPostSendReset is the courtroom.cpp:2348-2354 rule with the global as a
// parameter, so the OWED preference lands at exactly one call site and the test
// can pin both halves of the rule. globalSticky keeps every pick; with it off the
// pick clears to "None" unless the effect itself declares `sticky`.
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

// wireRoomOverlay attaches the overlay hooks to a room.
//
// EVERY mode that stages messages goes through here — the live courtroom, the
// pinned split pane, replays, the scene maker's preview and the comic/video
// export — so an effect plays identically in all of them. That used to be a
// claim this comment made and the code did not keep: two of the five were built
// raw and never reached this function. It is now STRUCTURAL rather than
// advisory: the only production caller is wireRoomCharMeta, whose only caller
// is App.newRoom (newroom.go), which is the only production call site of
// courtroom.NewCourtroom in this package.
func (a *App) wireRoomOverlay(room *courtroom.Courtroom) {
	if room == nil {
		return
	}
	room.OverlayFor = a.overlayResolve
	room.EffectsFolderFor = a.overlayEffectsFolderFor
	room.CustomRealizationFor = a.overlayCustomRealizationFor
}

// drawOverlayChatLayer paints the `chat`-rung overlay. AO2 reparents that one
// layer OUT of the viewport onto the Courtroom (courtroom.cpp:3234-3237), which
// is why it cannot ride Viewport.Render like the other three: it must paint after
// the chatbox. Called immediately after drawChatOverlay in both layouts.
//
// Free when nothing is on stage — DrawOverlayChat's first test is the base.
func (a *App) drawOverlayChatLayer(vp sdl.Rect) {
	if a.d.Viewport == nil || a.ctx == nil {
		return
	}
	// renderScene dereferences a.room unless a replay owns the stage, and this is
	// called from a draw tail that a themed layout can reach with no chatbox rect
	// (so drawChatOverlay's own deref did not run first).
	if a.room == nil && !(a.replaying && a.replayRoom != nil) {
		return
	}
	a.d.Viewport.DrawOverlayChat(a.ctx.Ren, a.renderScene(), vp)
}

// overlayIconPagesReset drops the cached icon pages — called when the roster
// changes shape so a stale page cannot draw under the wrong row (cachedPage keys
// by INDEX, not URL, exactly like the pos thumbnails).
func (a *App) overlayIconPagesReset() {
	a.overlayIconPages = nil
	a.overlayIconAsk = nil
}
