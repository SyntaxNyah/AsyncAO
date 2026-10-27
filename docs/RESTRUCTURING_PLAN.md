# AsyncAO restructuring plan (codebuster-take2)

Approved by the maintainer on 2026-10-26. This file is the handoff document: an agent picking
up the work should read CLAUDE.md, then this file, then continue at the first unchecked step
in **Status**. Update Status in the same commit that completes a step.

## Status

- [x] 0. Prep: delete `internal/debuglog`; CLAUDE.md facts + new rules; plan in repo;
      golangci-lint `mnd` in CI (9 already-clean packages enforced from the start)
- [ ] 1. `internal/helper/{value,text,parse}` + duplicate removal + config double-clamp removal
- [ ] 2. Bug fixes 1–3, 5–7 (one commit each, with tests) → **merge checkpoint** (maintainer pushes)
- [x] 2a. UI spike: RmlUi 6.3 on SDL_GPU (Vulkan, software Vulkan fallback) (Part 5e); throwaway code outside the module
      — see the Result in Part 5e
- [ ] 2b. SDL facade (`internal/sdl`, `ttf`, `mix`) over go-sdl2; switch all importers
- [ ] 2c. SDL3 switch: SDL_GPU on Vulkan (hardware, else bundled software Vulkan), device-reset
      re-upload; drop the SDL Mixer X fork; replace platform code
- [ ] 3. Engine: `internal/scene`, then `client.Session` per tab (async connect; tabs held by
      pointer, no park/activate copies; fixes bug 4, #115, #141, #84)
- [ ] 4. `internal/uikit`: extract the kit, then the input model (press/release + capture,
      wheel routing, layer stack)
- [ ] 5. uikit scaling, layout and text (logical units, RmlUi layout, nine-slice, scroll
      container) + shared widgets; one text-colour palette in the engine
- [ ] 6. Config API: `Set*` forwarder removal; engine option structs; engine stops importing config
- [ ] 7. Feature loop (jukebox → notebook → … → courtroom screen), extract then move
- [ ] 8. Remaining lower-layer work (Settings grouping, Manager `Source`, render debug sink)

Diagrams live in the shared diagram page, not in this file:
https://claude.ai/artifact/APmF2gN8QTGfPyB13mAhQv. The open-issue review behind the
three-layer revision (2026-10-26): https://claude.ai/artifact/W6eEze4MnLmdLKXoyGYXRn.

## Context

The first refactor branch (`codebusters-cleanup`) regrouped `App` fields into ~45
sub-structs without moving behaviour, then was rewound (`9b69007f`). The user set four
ground rules for the second attempt:

1. Full analysis first — map types and interactions before acting.
2. Split `App` first; changes there cascade into the lower layers, so the pain is taken once.
3. Evaluate several target shapes. Legibility and logical reasonability come first:
   no god classes, and no swarm of micro-classes that mirror a big class split for idealism.
4. One solid helper for the commonly repeated validation snippets (KISS, DRY).

Baseline: `codebuster-take2` @ `b60adb1f`, full `go test -race -p 1 ./...` green on the
Windows box (toolchain now installed: MSYS2 UCRT64 + SDL Mixer X).

---

## Part 1 — Codebase map (analysis results)

### Layering (non-test imports)

```
cmd/asyncao ── wires everything, owns main loop, LockOSThread
   │
internal/ui  (114.6k prod lines, ONE package, App = shell + every feature)      [SDL]
   │  Deps{Prefs, Resolver, Manager, Pool, Client, Store, Viewport, Pump, Audio,
   │       Preview, Presence, Profiler, MasterURL, ConfigQuarantine, AutoMounts}
   │  + ui-constructed: protocol.Conn, courtroom.Session/Courtroom, theme.Theme/Sidecar,
   │                    render.MessageRaster/AnimatedText/splitVP
   ▼
render (SDL) ─► courtroom ─► assets ─► network ─► cache
   │               │            │          └► config(FavoriteServer), netproxy, update(version only)
   │               ├► protocol ─► packetutil, netproxy
   │               └► theme ─► safepath
   └► config ─► theme          leaves: cache safepath packetutil netproxy update metrics
                               presence voice hwid gifenc webpenc winexec debuglog(orphan)
```

ui → lower-layer reference counts: theme 707, courtroom 637 (+313 `a.sess.X` field reads),
config 618 (+`Prefs.Load()` 779 / `Prefs.Update` 238), render 237, assets 155.

### Lower-layer god / micro types (cascade targets)

| Type | Size | Smell |
|---|---|---|
| `config.Settings` + `AssetPreferences` | 312 fields/144 methods + 179 methods (87 `Set*` wrappers over `Update`) | god + redundant wrappers; load path clamps twice |
| `courtroom.Courtroom` | 63 fields / 70 methods, ~29 exported knobs set field-by-field by ui | god + knob soup |
| `courtroom.Session` | exported mutable data bag, 313 ui reads | no API |
| `render.Viewport` | 56 fields / 51 methods; shared by 5 room modes; `OnPreanimDone/OnFrameShown` re-hooked in 7 ui files | god + unclear ownership |
| `assets.Manager` | 69 methods (fetch + mounts + archive + disk admin + decoder knobs + stats) | god |
| `theme.Sidecar` | 77 methods | large but cohesive (document) |
| `courtroom.URLBuilder` | 2 fields / 36 methods | fine (pure functions on a base) |
| `packetutil.VS_*` | 8 structs, 1–2 fields each | micro (wire codec, acceptable) |
| ui resolver adapters (`roomAssetResolver` …) | 4 types wrapping `*App` | micro, exist only to implement courtroom interfaces |

### internal/ui today

- `App` (app.go): **560 fields + embedded `sessionState` (521 fields), 1,714 methods over ~200 files.**
- Field coupling (measured): of ~1,110 App+session fields, **623 are used by exactly one file,
  339 by 2–3 files, 121 by 4–9, only 28 by 10+.** The state is already feature-local; only
  the methods are pooled on `App`.
- Truly cross-cutting: `d` (Deps; Prefs 1,355 refs), `ctx` (752 refs/141 files), `sess`/`room`/
  `urls`/`serverKey`, `screen`/`prevScreen`, toast `warnLine`/`warnAt` (178 refs, ~50 files
  hand-assign both), frame clock, damage/`uiDirty`, `pushDebug`, theme runtime, the
  per-frame fence/latch protocol in `Frame`.
- Per frame (`app_frame.go:171` `Frame`): global keys → ~50 pumps/polls → scene driver
  precedence (gif export > replay > maker > editor preview > room) → audio/pump/warm →
  fences → screen switch (`app_frame.go:707`) → overlays/modals → `ctx.FinishFrame`.
  `Background` repeats a subset.
- Tabs: active session state is the embedded `sessionState`; parked tabs hold a copy
  (`courtTab.state`); `parkActive`/`activateTab`/`resetSessionState` in tabs.go.
- Async: 79 `go` statements, **7 different result patterns** (global cap-1 channel + poll,
  gen counter, atomic.Pointer slot, job pointer + private done chan, channel inside
  sessionState, package-global state + channel, long-lived workers). Polls are partly
  central, partly inside draw functions; wake-ups (`PushWake`) and panic recovery are
  inconsistent; gen counters have mixed types. Possible bug: a session-scoped result
  dropped by a `serverKey` mismatch leaves `iniBusy`/`charINIBusy` stuck in a parked tab.
- Package-global singletons bypassing App: `settings`, `contentPanel`, `demoBrowser`,
  `baseWizard`, `aboutGroupsCache`, `exeDataRoot`.
- Already-owned subsystems (good precedent): `te *themeEditor`, `bgPicker`, `downloader`,
  `iniBaseBrowser`, `logBrowserState`, `gifExportJob`, `voiceEngine`.

### Feature boundaries (fields from the agent map, files from the coupling measure)

| Feature | ~Fields | Scope | Coupling |
|---|---|---|---|
| Jukebox | 35 | global | Prefs, Audio, toast — easiest |
| Self-update | 12 | global | one channel pair, modal, chip |
| Lobby + phone book + ping + server help | 30 | global | Connect, Prefs |
| Log views (filter/wrap caches + selection) | 55 | global, keyed by session epoch | session logs, Ctx fonts |
| Studio (record/replay/maker/export/content/demo) | 90 | global (+2 package globals) | shared Viewport, scene precedence |
| Effects overlay picker | 26 | global | themeAppliedGen |
| Split / pinned client | 30 | global | tabs, input snapshot |
| Hover sprite preview + preview window | 35 | global | previewEmoteRes, d.Preview |
| Font pipeline | 20 | global | feeds Ctx only |
| Layout editors (themed + classic + toolbox + extras) | 75 | global | fences/latches in courtroom draw |
| Messaging + voice | ~20 | split App/session | small |
| Settings screen (+package globals `settings`, `baseWizard`) | 63+ | global | Prefs |
| Theme runtime (apply, fonts, elements, media, layout cache) | ~100 | global | read by every themed draw |
| Session sub-areas: evidence 34, mod/CM 22, roster/pair/follow 70, wardrobe/iniswap 34, composer (IC/OOC input, emotes, arms) 33, logs 37, pacer 24 (belongs on App) | | per tab | |

### Validation duplication (helper inventory)

- Scalar clamp body duplicated 7× (`config.clampPercent` 142 callers, `theme.clampInt`,
  `ui.clampInt`, `ui.taClamp`, `ui.clampI32`/`clampInt32`, `ui.clampF`/`clampF64`/`clampFrac`,
  `render.clampF`/`clampF32`, `ui.clampDur`) + 80 inline lo/hi pairs; `clampF` vs `clampF64`
  differ on inverted ranges.
- `ToLower(TrimSpace(x))` 94×, `TrimSpace(x)==""` 47×, file-stem idiom 7×.
- Truncation: 6 implementations; `config.clampStr` cuts bytes and can split UTF-8 (bug).
- Atoi-with-fallback: 6 helpers (`courtroom.atoiOr`, `protocol.atoiDefault`,
  `packetutil.AtoiOrZero` (no trim), `theme.atoiTrim`, `ui.genAtoi`, `ui.atoiClamped`).
- Hex colour: 5 parsers, 2 identical `hexNibble`; `R,G,B` tuples wrap vs clamp inconsistently.
- Path safety bugs: `archive.writeAsset` and `ui.writeBundleFile` use `strings.Contains(p,"..")`
  (rejects legal names, misses `:`/rooted); `update/apply.go:273` weaker than `safepath.UnsafeRel`.
- Contains/remove/clone loops: 16/23/50 hand-rolled despite Go 1.25 `slices`/`maps`.
- config: `normalizeSnapshot` repeats ~30 clamps that `normalizeSettings` re-runs; 22 setters
  pre-clamp before `Update` clamps; ~7 ui sites re-clamp config ranges by hand.

### Loose ends found

- `internal/debuglog` is orphaned (ui re-implements the ring in app_ic.go).
- CLAUDE.md drift: Go/x-sync versions; "sole exception" claim vs 5 remaining source-scan tests;
  CGO_ENABLED=0 claim (assets/courtroom/logbrowser don't build without cgo).
- `setup-deps.ps1` lacks SDL Mixer X (moot after the SDL3 move removes the fork).

---

### Bugs found (each gets its own fix commit + behavioural test, never inside a refactor)

1. **Sprite downscale cap lost after connect** (verified): `spriteCapBase` lives in
   `sessionState` (apptypes.go:82); `resetSessionState` (tabs.go) rebuilds the literal without
   it, so after any connect the next downscale-slider change (`applySpriteCap`,
   app_room.go:1553) computes the cap from base 0 = no cap.
2. **Per-part layout tints wiped by connect**: `partColors`/`partColorOn` in `sessionState`
   (apptypes.go:69-70), only refreshed at boot and on Settings edit.
3. **Parked tab's iniswap list stuck busy**: per-tab `iniBusy` + global `iniRes` channel keyed by
   `serverKey` (app_room.go:1996-2045); result dropped after a tab switch leaves `iniBusy=true`.
4. **Suspected**: damage/animation flags (`uiDirty`, `drawn*`, `frameAnimChrome`) are per-tab, but
   the pinned pass (app_room.go:581-605) and editor preview (themeeditorpreview.go:466) swap a
   different `sessionState` in by value → marks land in the wrong copy. Write the failing test first.
5. Path checks: `archive.writeAsset` / `ui.writeBundleFile` (`strings.Contains(p,"..")`) and
   `update/apply.go:273` → `safepath`.
6. `config.clampStr` cuts bytes and can split UTF-8 in user text.
7. Doc rot: newroom.go cites two tests that no longer exist.

---

## Part 2 — Shapes considered

| Shape | Verdict |
|---|---|
| In-package feature types only | Low churn, but boundaries rely on discipline. Not enough on its own. |
| Screen objects only | Courtroom screen would hold ~70% of ui → new god object. Kept only as a thin dispatch interface. |
| Sub-packages straight away | Compiler boundaries from day one, but uikit (~90 private Ctx members, 141 files) blocks every feature, and the first visible result is many commits away. |
| **Extract into types first, then move into sub-packages; screens composed of feature panels** (decided, C1) | Each feature first becomes a type in `internal/ui` that owns its state *and* methods, with the boundary checked by a script. When it has no unexported dependencies left, it is moved with `git mv` into `internal/ui/<feature>` and the compiler takes over. **Chosen.** |

**Granularity rule** (prevents both god and micro types):
- A feature package exists only if it is a noun the UI already uses (Jukebox, Notes, Evidence,
  Mod dashboard), its code mostly touches its own state + uikit + the engine, it imports no sibling
  feature, and it exports ≤ ~20 identifiers.
- Inside a feature: at most two exported types — `Panel` (global UI, constructor-injected
  dependencies) and `Tab` (per-tab UI state, callable on a parked tab).
- Merge two packages when they need each other's state, one is only driven by the other, or
  one needs a back-pointer to App. Never group unrelated features to reach a size.

---

## Part 2c — Terminology (common names only)

Names in code, comments, commit messages and docs use the plain words a developer new to the
project would expect. No invented jargon or metaphors. When existing code is touched, its jargon
is renamed in the same commit; no sweep commits just for renaming.

| Use | Instead of |
|---|---|
| `AsyncResult[T]` | Pending, slot, mailbox |
| `client.Session` (one server connection) | Link, Connection service |
| `SendOOC` on `client.Session` | Outbox |
| `FrameState` (frame clock + redraw flags) | Pace, damage latches |
| `KeyCapture` | Keys, armed bind |
| theme styling (in uikit) | themert |
| extract into a type / move into a sub-package | own / lift |
| boundary check script | lint gate |
| per-package opt-in (lint) | ratchet |
| boundary | seam |
| test / check | gate, census, teeth, oracle |
| overlay / modal / input blocking | fence, latch (existing code: rename when touched) |
| "touch each call site once" | "hit the pain once" |

---

## Part 3 — Target architecture (three layers, adopted 2026-10-26)

The first draft put a `shell` package (a shared `Services` struct) and a `themeruntime`
package between features and the kit. The review of all 60 open issues showed that no issue
needs either, while the two largest issue groups (input, layout/text) live in the kit itself and
a third (per-tab state, blocking connect) lives in the session. The structure is therefore three
layers, each with one job, and nothing in between.

```
App layer     internal/ui            App: Frame order, tabs, screens, connect/disconnect, wiring.
                                     The ONLY layer that reads config.
              internal/ui/<feature>  jukebox, notebook, lobby, evidence, roster, wardrobe, mod,
                                     logs, composer, studio, split, layout editors, settings, …
     │  features draw through uikit and read/act on a *client.Session; nothing else
     ▼
UI platform   internal/uikit         Ctx; input model; logical-unit scaling; RmlUi layout; text
                                     measure/wrap; nine-slice; scroll container; widgets
                                     (combobox+autocomplete, reorderable list, menu, …);
                                     Dock/Panel/Screen; Toast; KeyCapture; FrameState;
                                     AsyncResult[T]; theme styling (reads theme data)    [SDL]
     ▼
Engine        internal/client        Session per tab: async connect, conn, courtroom, logs,
                                     roster, evidence, area/music state; command methods
              internal/scene         Scene data shared by courtroom and render
              render, courtroom, protocol, packetutil, assets, network, cache, theme
                                     (no engine package imports config)        [render: SDL]
Platform      internal/sdl, ttf, mix our thin cgo binding to SDL3 / SDL3_ttf / SDL3_mixer
all layers ──► internal/helper/{value,text,parse}
```

**Import rule.** App layer → uikit, engine, config. Feature → uikit, engine (never
`internal/ui`, never a sibling feature, never config). uikit → render, theme, sdl. Engine →
engine and helper only. Cross-feature needs (#114: OOC log → jukebox) use a small
consumer-side interface wired by App.

### Dependencies are passed, not looked up
No `Services` bag. A feature's constructor takes exactly what it uses
(`jukebox.New(ctx *uikit.Ctx, audio *render.Audio, opts jukebox.Options)`). Per-tab work gets
the session as an argument on each call: `Draw(s *client.Session, t *Tab)`. Because the
session is passed in every frame, nothing caches a room, session or viewport, so the old
swap-safety rule disappears.

### client.Session — one per tab
- Owns everything about one server connection: `protocol.Conn`, the handshake data
  (`courtroom.Session` today), the `courtroom.Courtroom`, IC/OOC logs, roster, evidence, area
  and music lists, `sidePref`, timers. Read through fields/methods; mutated only by its
  own methods (`SendIC`, `SendOOC`, `SetPos`, `Resync`, …).
- `Connect` runs on a goroutine and reports through an `AsyncResult`; the render thread never
  dials (#115).
- One log append rule for every view (#64).
- Takes plain option structs (`client.Options`, `courtroom.Visuals/Timing`), built by App from
  prefs. No config import.
- App keeps `tabs []*courtTab{sess *client.Session; ui tabUI}`. `tabUI` holds only UI state
  (drafts, scroll positions, open panels) as feature `Tab` values. Tabs are held **by
  pointer**: park/activate copy nothing, the pinned pass and the editor preview just draw a
  different tab. This removes bugs 1–4 as a class.
- HDID: derived in `internal/ui` at the dial call and handed to `client.Connect`, so
  `hdidgate_test.go` keeps seeing it (CLAUDE.md rule 11).

### internal/scene
The `Scene` value (what the courtroom shows: sprites, positions, chatbox state, effects) moves
out of courtroom into its own package. render imports scene instead of courtroom, so the
compositor no longer depends on the message state machine. KFO/DRO scene rules (#54, #63,
#111) are tested here without SDL.

### uikit — input model (step 4)
Fixes #86, #113, #87, #34, #32, #103, #60/#100, #105.
- **Click** = press and release on the same widget. The widget under the press owns the
  pointer until release (capture); dragging a scrollbar and releasing over a button does
  nothing.
- A widget can opt into **act-on-press** (emote buttons, #87).
- **Wheel** goes to the topmost hovered scrollable or adjustable widget and is consumed there;
  it never falls through to what is underneath.
- **Layer stack**: base screen < docked panels < floating panels < popovers < modals. Hit
  testing walks top-down, and drawing goes bottom-up from the same list. This replaces the
  four hand-kept lists (`drawFloatingPanels`, `boxFencesPointer`, `closeTopOverlay`,
  `drawTabContent`) and the fence/overlay calls.

### uikit — scaling, layout and text (step 5)
Fixes #62, #76, #77, #90, #116, #53, #67, #97.
- **One unit system.** All layout is in logical units; uikit converts to device pixels at draw
  time using `SDL_GetWindowDisplayScale` × the user's UI scale. Glyphs and nine-slice edges are
  rasterised at device size. The renderer is never scaled, so `SetScale` and
  `beginRenderScaleOverride` are deleted. A display-scale-changed event re-rasterises fonts.
- **RmlUi** (Part 5e) lays out every screen we own from RML/RCSS (flexbox, media queries, `dp`
  units). Resizing becomes reflowing, not per-screen rectangle code. RmlUi measures and draws
  its own text through its FreeType font engine; whether SDL3_ttf stays for the courtroom
  message raster is an open item in Part 5e.
- **AO themes** keep their absolute element rectangles for the theme's design size, scaled
  uniformly to fit with letterboxing. Per-element anchors in the sidecar are a later option.
- Primitives: nine-slice draw, aspect-fit image draw, scroll container (culling by measured
  rows), text layout with tab expansion and coloured spans.
- Shared widgets, built once: combobox with autocomplete and row icons (#71, #79, #96, #104),
  reorderable list (#18, #16), menu (#85), slider styled from the theme (#94, #107).
- **Text colours**: one palette table in the engine (data only), read by render, the logs and
  the colour pickers (#89, #91, #93, #68).

### uikit export surface
Accessors and explicit consume methods instead of ~40 exported fields: `Mouse()`, `Clicked()`,
`Wheel()`/`TakeWheel()`, `Key()`/`TakeKey()`, `Hotkey()`/`TakeHotkey()`, `Esc()`/`TakeEsc()`,
`Mods()`, `Focused()`/`Blur()`/`FocusNext()`, `SuspendModal() ModalToken` + `RestoreModal(t)` (a value
token: no returned closures on draw paths), `PushClip`/`PopClip`, `PushPanelFont`/`PopPanelFont`,
`FontRole` enum, `Layer` enum. The fence/overlay calls go away with the layer stack. Tests drive
input through the public `HandleEvent`/`BeginFrame` with synthetic SDL events.

### AsyncResult[T] — one async primitive (replaces 5 of the 7 patterns), in uikit
```go
func NewAsyncResult[T any](wake func()) *AsyncResult[T]
func (p *AsyncResult[T]) Start(what string, fn func(context.Context) (T, error)) bool // one goroutine, recover → crash log, wake on done; false if busy
func (p *AsyncResult[T]) Busy() bool
func (p *AsyncResult[T]) Take() (T, error, bool) // non-blocking, once
func (p *AsyncResult[T]) Cancel()                // supersede (replaces gen counters)
```
Owned by the `Panel`, `Tab` or `client.Session` that started the work, so a per-tab result
follows its tab (fixes bug 3 by construction). Long-lived workers (voice, ping loop,
downloader, transcripts) keep named bounded channels.

### Panels and screens
```go
type Panel interface { Title() string; Draw(r Rect) }   // content only; the host draws chrome
type PanelOpts struct{ DefW, DefH, MinW, MinH float32; Font FontRole; Tearable bool }
func (d *Dock) Register(id string, p Panel, o PanelOpts)
func (d *Dock) Open/Close/Toggle(id string); IsOpen(id string) bool
func (d *Dock) DrawDocked(id string, r Rect)   // classic slot / themed element / docked tab strip
func (d *Dock) DrawFloating()                  // floating + torn panels on the floating layer
func (d *Dock) CloseTop() bool                 // one Esc step
type Screen interface { Draw(w, h float32); Escape() bool }
```
Per-tab open sets live in `tabUI`; floating geometry is global. The courtroom screen lays out
stage + slots and calls `Dock.DrawDocked` per slot; it imports no feature.

**Frame orchestration stays an explicit ordered list in `App.Frame`** (`a.juke.Poll()`,
`a.notes.Poll()`, …); no hook registry, because the order is load-bearing.

**App afterwards**: `tabs`, the active tab pointer, one pointer per feature, a
`[ScreenN]uikit.Screen` table, and cross-feature orchestration (Esc fallback, palette,
connect/disconnect). Budget ≤ 60 fields, ≤ 250 methods (today 560 + 521 fields, 1,714 methods).

**Granularity rule** (unchanged in spirit): a feature package exists only if it is a noun the UI
already uses, mostly touches its own state, imports no sibling feature and exports ≤ ~20
identifiers; at most two exported types, `Panel` (global UI) and `Tab` (per-tab UI state).

### internal/helper — a library split by category (lands first; decided C6)
Stdlib-only leaf packages, importable from every layer. A category package exists only when it
holds ≥2 functions that each have callers in ≥2 packages, so it can't fragment into micro-packages.
Domain parsing (AO wire, theme INI semantics) stays in its domain package.
```go
// internal/helper/value — keep values inside bounds
func Clamp[T cmp.Ordered](v, lo, hi T) T          // hi < lo → lo (documented)
func ClampSet[T cmp.Integer](v, lo, hi T) T       // 0 = "unset" stays 0, else Clamp (the ~30 config sentinel blocks)

// internal/helper/text — rune-safe string shaping
func Truncate(s string, maxRunes int) string
func TruncateBytes(s string, maxBytes int) string // never splits a rune

// internal/helper/parse — untrusted input → values (future home of config value parsing)
func AtoiOr(s string, fallback int) int           // trims space
func HexColor(s string) (color.RGBA, bool)        // optional '#', 6/8 digits
```
Call sites read as `value.Clamp(...)`, `text.Truncate(...)`, `parse.AtoiOr(...)`. Where a local
name shadows a package (`text` variables are common in ui), import as `htext`.
Replaces: 7 clamp bodies (`config.clampPercent`, `theme.clampInt`, `ui.clampInt/taClamp/clampI32/
clampInt32/clampF/clampF64/clampFrac/clampDur`, `render.clampF/clampF32`) + inline lo/hi pairs;
`config.clampLen`, `ui.truncRunes`, `courtroom.clampWireBytes`, `config.clampStr`;
`courtroom.atoiOr`, `protocol.atoiDefault`, `theme.atoiTrim`, `ui.genAtoi`; `theme.hexNibble`,
`ui.hexNibble`, `ui.parseHexColor`, `ui.parseHex6`, `ui.parseEditHex`.
Stdlib instead of new helpers: `slices.Contains/Index/Delete/Clone`, `maps.Clone`.
Not added (only save one line): fold/blank/stem. Package is the future home of config value
parsing too (user's intent). Cautions: `packetutil.AtoiOrZero` keeps wire semantics (no trim)
unless AO2-Client confirms; list every converted clamp whose range can invert; pin each hex
parser's accepted formats with tests before merging them.

### Lower-layer changes: API first, then move the callers (decided, C2)
Any lower-layer API change that alters ui call sites lands **just before** the features that
call it are owned or moved, so moved code is touched once:
- `render.ViewportOptions` + `Viewport.Configure(opts)` — forced once the split pane is its own
  package (otherwise prefs→setter mapping duplicated; today Frame pushes ~10 setters twice,
  app_frame.go:552-581).
- `courtroom.Visuals` + `courtroom.Timing` with `SetVisuals/SetTiming` — forced because room
  modes get built from several packages; the room factory is built by App and handed to them.
- config: remove the `normalizeSnapshot` clamp block duplicated by `normalizeSettings` and the 22
  setter pre-clamps (in the helper series). Delete pure single-caller `Set*` forwarders, keep
  setters that carry logic or have ≥3 callers (C5), before the first feature move.
- `assets.Manager` forwarders: removed before studio/settings (their main callers) are owned.
- Session writes (4 sites) become methods with the feature that writes them.

---

## Part 4 — Sequence (each step = one or more self-contained commits)

0. **Prep commits**:
   - delete `internal/debuglog`;
   - fix CLAUDE.md (Go 1.25 / x/sync 0.20, cgo-only packages, source-scan section) and record
     the new rules there: magic numbers with exemptions, KISS/DRY/DI, the helper library scope,
     the feature boundary rule;
   - (no SDL Mixer X in `setup-deps.ps1`: the fork is removed by the SDL3 move, Part 5d);
   - add golangci-lint with `mnd` to CI, enforcing only the packages that are already clean.
1. **`internal/helper/{value,text,parse}`** (2–4 commits): packages + tests; convert clamps;
   truncation/atoi/hex; config double-clamp removal.
2. **Bug fixes 1–3, 5–7**, one commit each with a behavioural test.
   → **Merge checkpoint**: phases 0–2 end green and mergeable; user pushes/merges before step 3.
2a. **UI spike (Part 5e):** throwaway code in a separate module outside the repo; the result
    and the decision are recorded in Part 5e. No production code.
2b. **SDL facade (Part 5d steps 1–2):** own cgo binding packages `internal/sdl`, `internal/ttf`,
    `internal/mix`, first implemented over go-sdl2 with identical names. Switch the 187 importing
    files package by package (import-path swap). Still SDL2, always green.
2c. **SDL3 switch (Part 5d steps 3–5):** re-implement the binding over SDL3, SDL3_ttf and
    SDL3_mixer; rewrite audio onto `MIX_Track`; delete `render/mixerx`, the fork build script
    and the CI fork steps. Pin the loop-end behaviour with a test. Renderer: SDL_GPU on
    hardware Vulkan, else the bundled software Vulkan driver (Part 5d "Renderer"). Replace the platform code
    (file/folder dialogs, DPI seeding, URL/file manager opening, preview window ownership,
    clipboard), one commit each. `setup-deps.ps1` and CI install SDL3 packages.
3. **Engine.** In order, one commit series each:
   - `internal/scene`: move `Scene` out of courtroom; render imports scene.
   - write bug 4's failing test; move global-lifetime fields (pacer/damage, `spriteCapBase`,
     `partColors`, warm keepers) out of `sessionState`.
   - `internal/client`: `Session` takes the connection, handshake data, courtroom, logs,
     roster, evidence, area/music state and `sidePref` out of `sessionState`; async `Connect`
     (#115); one log append rule (#64).
   - tabs held by pointer (`[]*courtTab`); park/activate, the pinned pass and the editor
     preview stop copying `sessionState` (fixes bug 4, #141, #84).
4. **uikit, input model.** Extract the kit: kit files lose their App methods, Ctx gets its
   exported API, `git mv` to `internal/uikit`, `newTestApp` replaces the 169 `&App{…}`
   literals, CLAUDE.md rule 1 + package map updated. Then, one commit each with behavioural
   tests driven by synthetic SDL events: press/release + capture (#86, #113 first), act-on-press
   opt-in, wheel routing, the layer stack replacing the four hand-kept panel lists.
5. **uikit, scaling, layout and text.** Logical units + device rasterisation (delete
   `SetScale` and the scale overrides); RmlUi layout (RML/RCSS); nine-slice, aspect-fit,
   scroll container, text layout; shared widgets; the single text-colour palette in the
   engine. Each screen is moved onto RmlUi in its own commit.
6. **Config API first** (C2): delete pure single-caller `Set*` forwarders (C5 keeps the rest);
   engine option structs (`client.Options`, `courtroom.Visuals/Timing`, `ViewportOptions`) built
   by App; engine packages stop importing config (`network` → `config.FavoriteServer` moves).
7. **Feature loop.** For each feature, in this order: jukebox (proves a global feature),
   notebook (proves a per-tab `Tab` + `AsyncResult` + the first `Dock` panel), update, timer,
   lobby (first `Screen`), effects picker, messaging/voice, evidence, roster, wardrobe, mod/CM,
   logs, composer, studio, split, layout editors, settings, courtroom screen composition.
   Theme runtime and the font pipeline go into uikit styling, not a package of their own.
   Each feature goes through:
   - **a. API first:** reshape any lower API the feature calls (Manager forwarders before
     studio/settings; `courtroom.Options` + injected resolvers before composer/split;
     `Viewport.Configure` before split/studio).
   - **b. Extract:** test inventory, then the feature type takes its state *and* methods; App keeps
     only the ordered calls in Frame. Package globals (`settings`, `contentPanel`,
     `demoBrowser`, `baseWizard`) become fields of their feature's type. Boundary check passes
     (`scripts/check-boundaries`: fails when a feature's fields are touched outside its files;
     temporary, each feature drops out once moved).
   - **c. Move:** when the feature has no unexported dependencies left, `git mv` it into
     `internal/ui/<feature>`.

   Jukebox concretely: takes ~28 fields and three `AsyncResult`s; API `Poll, PollBind, HandleKeys,
   Draw, NoteMusic, SaveFromChat, Clear, Close`; call sites app.go, app_frame.go:499/501,
   qol.go:420, app_connect.go:746, screensooc.go:572, screenswardrobe.go:168/191,
   settingstabs.go:1250; files jukebox.go, jukebox_fav.go, music_history.go + their tests.
8. **Remaining lower-layer work** (Part 5b) that doesn't touch ui call sites: `Settings` domain
   grouping via embedding (JSON unchanged, proven by the goldens), the internal `Source` chain in
   `assets.Manager`, render debug sink injection.

The small shared UI pieces that were planned as `shell` services (Toast, Nav, KeyCapture,
FrameState) land in uikit during step 4, each replacing its hand-written copies in one commit
(Toast: the 178 `warnLine/warnAt` assignments).

**Source-scan tests**: before the move that breaks each (srcgate, allocgate census,
themeeditorseam, themeeditorpreview, zerowidthlanes), convert to a behavioural test or delete
under rule 11. HDID derivation stays in `internal/ui` so `hdidgate_test.go` keeps working.

**Commit hygiene**: move commits stay mechanical (`git mv` + minimal edits) so rename detection
holds; receivers 1–2 letters, consistent per type; no comment sweeps; no push (user pushes).
New names follow Part 2c (common names only).

---

## Part 5 — Verification (every commit)

- `gofmt -l`, `go build ./...`, `go vet ./...`, `go test -race -count=1 -p 1 ./...` with
  `C:\msys64\ucrt64\bin` prepended and the CGO env from CLAUDE.md.
- Draw/Frame changes: `go test -run=NONE "-bench=." -benchmem ./internal/ui/... ./internal/uikit`
  (zero-zero-allocation tests).
- Tracked metrics (reported per step): App field + method counts, `sessionState` field count,
  `warnLine` refs (→ 0 after Toast), private Ctx members used outside kit (→ 0 after 4b),
  `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/ui/... ./internal/uikit`
  (no feature → `internal/ui` or sibling), exports per feature ≤ ~20 (`go doc -short`),
  no feature constructor takes more than ~6 dependencies, hand-listed panels in floatbox.go → 0,
  no engine package imports config, `SetScale` calls → 0.
- `golangci-lint run` with `mnd` green for every package already marked clean. The set of
  clean packages only grows (a package never leaves the enforced set).
- Boundary check script (`scripts/check-boundaries`) passes while features live as types inside `internal/ui`:
  no feature field is touched outside that feature's files. It shrinks to nothing as features move into their own packages.
- Smoke run of the client after each feature move (`scripts/build.ps1`, connect to a server,
  open the moved feature, switch tabs).

## Part 5b — KISS / DRY / dependency injection for every object

### Rules (apply to every type, in every package)

- **KISS**
  - A type exists only if it owns behaviour, or it is plain data with one clear meaning.
  - No forwarding methods. A method that only passes the call on to a collaborator is
    deleted; the caller gets the collaborator injected instead.
  - No interface with a single implementation, unless the interface is needed to cross a
    package boundary or break an import cycle.
  - Prefer plain data plus functions over objects when there is no state to protect.
- **DRY**
  - One implementation per concept: clamp, parse, truncate, async result, viewport
    configuration, panel registry.
  - Shared helpers go in `internal/helper`; Go 1.25 `slices`/`maps` replace hand-written loops.
  - Each validation rule lives in exactly one layer. Config ranges are clamped in
    `normalizeSettings` only; setters and ui don't clamp them again.
- **Dependency injection** (used where an object has to be swappable, testable or cross a boundary)
  - Constructor injection only: `New(deps…)`. No setters after construction, except
    documented cycle breaks.
  - Dependencies are concrete types, or small consumer-defined interfaces when there are two
    or more implementations (real plus test fake counts) or a boundary to cross.
  - No mutable package globals. `sync.Once` registries and constant tables are allowed.
  - No service locator and no shared services bag: each constructor names what it uses.
    members, not a lookup registry.
  - `cmd/asyncao/main.go` stays the single composition root. Tests inject fakes through the
    same constructors.

- **No magic numbers, ever** (tightens CLAUDE.md rule 9; production *and* test code)
  - Every number gets a name. Use an `iota` enum when the values form a set, otherwise a
    `const` (package-level, or local when only one function uses it). Use a `const`, not a
    variable: a named number must not be mutable.
  - Exemptions (decided):
    - `0`, `1` and `-1` as structural values: empty, start, offset, sentinel.
    - `2` for halving and doubling in geometry only, such as `(w-tw)/2`.
    - Values inside a named data table or struct literal whose table name and field names
      already say what the values mean (`chromePresets` RGB rows, AO-spec index tables).
  - Test expectations are **not** exempt: expected values in `*_test.go` get named consts too.
  - Enforcement: per-package opt-in. The `mnd` linter runs through golangci-lint in CI,
    configured with exactly these exemptions, and is enabled per package once that package is
    clean. golangci-lint is a CI tool, not a `go.mod` dependency; it is justified in
    docs/ARCHITECTURE.md as the policy requires. staticcheck keeps its own CI step.
  - Rollout fixes literals only when their code is touched anyway (~6,800 in ui, ~1,000
    elsewhere), so every file is touched once:
    - lower packages during the helper phase (Part 4 step 1);
    - each ui feature in its extract step (6b);
    - App's own code during the services steps.

### Object-by-object verdicts (lower layers)

| Object | Problem | Change | Lands |
|---|---|---|---|
| `assets.Manager` (69 methods, one file) | god type: fetch core + 3 source kinds (mount/archive/local) + miss memo + decoder knob forwarders (`SetSpriteCap`, `SetAnimated*`, `SetTextureCompression`, `SetAdaptiveLatencyMultiple`) + disk admin forwarders (`ClearDisk`, `PurgeCorrupt`, `DiskRoot`, `SetDiskBudget`, `SetDiskCompression`, `DiskStats`) | delete the forwarders; ui gets `*DecoderPool`, `*DiskCache` and `*Client` injected (Deps) and calls them directly. The three byte sources become one injected `Source` interface (mount layer, archive, network) walked in order, which removes `serveFromMount`/`packBytes`/`FetchRawLayered`/`ResolveRawLayered` duplication. Manager keeps the prefetch/resolve/deliver core. | after the App split (needs ui call sites moved first) |
| `config.AssetPreferences` + `Settings` (179 + 144 methods, 312 fields) | 87 `Set*` wrappers around `Update`; clamps applied in three layers | Clamps live in `normalizeSettings` only. Pure single-caller `Set*` forwarders are deleted; setters that carry logic or have ≥3 callers stay (C5). `Settings` is grouped into embedded domain structs that mirror the Settings tabs (Audio, Chat, Theme, Network, Export, Layout, …). Embedding keeps both the JSON and `s.Field` access flat, so the load/setter goldens prove nothing changed. `genclone` is taught embedded structs. | helper phase (clamp layering) + after the settings feature moves (grouping) |
| `courtroom.Courtroom` (63 fields, ~29 exported knobs) | knob soup set field by field; resolvers assigned after construction | `NewCourtroom(Deps{URLs, Manager, Session, Audio, Blips, Chars, Assets, Env}, Options{Visuals, Timing, Waits})` plus `SetVisuals/SetTiming`. Resolvers injected through the constructor. Exported knob fields become unexported. | with studio/split (room factory built by App) |
| ui resolver adapters (`roomAssetResolver`, `roomCharResolver`, `roomBlipResolver`, `envResolver`, each wrapping `*App`) | micro types that exist only to reach App | deleted. The char-meta cache object implements `CharResolver` + `BlipResolver`, the viewport binding in App implements `AssetResolver`, and the room factory injects them. | with the charmeta/composer moves |
| `courtroom.Session` (exported data bag, 313 reads, 4 writes) | writes from outside | the 4 ui write sites become methods. Reads stay as fields: it is plain data, and KISS says no getter wall. | with the features that write them |
| `render.Viewport` (56 fields, 11 `Set*` knobs; ~10 setters pushed twice per frame for `d.Viewport` and `splitVP`) | duplicated configuration; hooks re-bound in 7 files | `ViewportOptions` + `Configure(opts)` (one prefs→options mapping); hooks bound in one place in App. | with split/studio |
| `render.Pump` (built after App via `SetPump`, callback into App) | setter injection to break a cycle | ui builds the Pump from injected Store + Manager + a `func(string) bool` live-check from App. No post-construction setter. | step 3 (engine) |
| `render` debug log (package-global `debugLogChan`, `PollDebugLogs`) | mutable global | a debug sink (`func(string)`) injected into the render constructors; the global is removed | step 8 |
| `assets.maxAnimatedDecodedAssetBytes` (global atomic) | mutable global | becomes a `DecoderPool` field | helper phase (assets touch-up) |
| `netproxy` policy (`currentPolicy`, `resolver` globals) | process-global state read by network + protocol | **keep (C3):** genuinely one proxy setting per process, set once by main, and tests can already call `Configure`; injecting it would only add plumbing | — |
| ui package globals (`settings`, `contentPanel`, `demoBrowser`, `baseWizard`) | hidden state | fields of their feature `Panel` | at each feature's move |
| `theme.Sidecar` (77 methods, split over `sidecar_*.go`) | large but cohesive document model | keep. DRY only (helper for parsing, hex, clamps). | helper phase |
| `theme.Element` (37 fields), `protocol.ChatMessage`/`OutgoingMS` (31–32) | big but plain data mirroring wire/INI formats | keep (data, no behaviour to split) | — |
| `courtroom.URLBuilder` (2 fields, 36 methods) | pure functions over a base URL | keep (KISS-compliant) | — |
| `packetutil.VS_*` (8 structs, 1–2 fields) | 1:1 with wire packets | keep the types. If their parse/encode bodies prove duplicated, fold them into one table-driven codec. | verify in helper phase |
| `network.Client`, `Pool`, `cache.*`, `protocol.Conn`, `metrics`, `presence`, `update`, encoders | sized and scoped well | keep; DRY via helper only | helper phase |
| `network → update` import (version string only) | odd edge | **keep (C3):** reads a constant from a leaf package; injecting it is noise | — |

### Sequence impact
API changes that touch ui call sites land just before the features calling them (Part 4 step 6a).
The rest (Settings grouping, Manager `Source` internals, render debug sink) is Part 4 step 7.
The goldens, race tests and zero-allocation tests prove behaviour is unchanged.

## Part 5c — Where I disagreed with the requirements (decisions recorded)

Decided: **C1 accepted** (extract into types first, then move into sub-packages), **C2 accepted** (API first),
**C3 accepted** (DI only where it pays), **C4 rejected** (Settings grouping stays in the plan),
**C5 accepted** (keep setters with logic or ≥3 callers), **C6 replaced** by the user's choice:
`helper` is a library split into category packages (`value`, `text`, `parse`).

**C1. "Sub-packages now" → propose: extract each feature into a type first, move it into a sub-package when it is self-contained.**
- *Cost of "now".* No feature can move until uikit is out: ~90 private Ctx members across
  141 files, plus the 4c big-bang move. Feature moves also churn the white-box tests
  (214 of 317 ui test files call private App methods). The first visible result is many
  commits away.
- *Why the old attempt really failed.* It moved fields but not methods. It did not fail
  for staying in one package.
- *Counterproposal.* Each feature becomes a type in `internal/ui` that owns its state *and*
  its methods, reachable only through its own API. A CI lint script (not a source-scan test,
  so rule 11 isn't involved) fails if code outside the feature's files touches its fields.
  Once a feature has no unexported dependencies left, moving it into
  `internal/ui/<feature>` is a cheap `git mv`. uikit is extracted when the first move needs it.
- *Outcome.* Same end state, value in every commit, and the compiler takes over the
  guarantees step by step instead of all at once.
- *Trade-off.* Between the two steps a lint script enforces the boundary, which is weaker
  than the compiler.

**C2. "Split App first, cascade down" → propose: change the lower APIs that ui calls *before* moving those call sites.**
- *The problem.* Removing the 87 config `Set*` wrappers, the Manager forwarders, and the
  Viewport/Courtroom options all change ui call sites. Moving a feature first and then
  changing these APIs touches every moved call site twice.
- *Your own principle applied.* "Touch each call site once" points the other way here: shape each
  lower-layer API first, then move the feature that calls it.
- *What doesn't matter.* Lower-layer changes that don't touch call sites (Settings grouping
  via embedding, the Manager `Source` internals) can go in any order.

**C3. DI everywhere → propose: inject only where there are ≥2 implementations or a real boundary.**
- *netproxy policy.* Keep it global. It is genuinely process-wide: one proxy setting per
  process, read by network and protocol, set once by main. Injecting it means threading it
  through `DialOptions` and `NewClient` everywhere, and tests can already call `Configure`.
  That plumbing adds no modularity.
- *`network → update` version string.* Keep the import. It reads a constant from a leaf
  package. Injecting a User-Agent string is noise.
- *Where DI is worth it.* Courtroom resolvers, the Manager `Source` chain, the render debug
  sink (removes a mutable global), and the feature boundaries. Each has a real second
  implementation (test fakes, split pane, export) or a real boundary.

**C4. Settings domain grouping → propose: drop it unless it brings real ownership.**
- *What grouping by embedding changes.* Field access stays `s.Field`, so call sites read
  exactly the same. Only the struct definition changes, and it adds genclone and JSON-collision
  risk.
- *When it's worth doing.* When the settings feature is split into per-feature sections,
  each section can own its slice of `Settings` (with its own normalize). That gives real
  ownership instead of a cosmetic split, so do it then or not at all.

**C5. Delete all `Set*` wrappers → propose: delete pure single-caller forwarders, keep setters that carry logic or have ≥3 callers.**
- Replacing `SetSpriteStyle(x)` (39 callers) with
  `Update(func(s *config.Settings){ … })` at every site is more code, not less.
- KISS means the shortest correct call site. A setter whose logic lives once is DRY.

**C6. `internal/helper` → accept the name, but fence its scope.**
- A package named `helper` tends to become a junk drawer, and Go convention discourages
  util/helper names.
- Proposed scope rule for its package doc: "value coercion and parsing of untrusted input
  (numbers, strings, colours, later config values)".
- Anything domain-specific (AO wire parsing, theme INI semantics) stays in its domain
  package. A function goes in only if it has callers in ≥2 packages.

**C7 (superseded 2026-10-26: no Services bag, Part 3). Embedding `*shell.Services` into App → propose: keep embedding, but no service may share a name with an App method.**
- The `Frame` collision is already resolved by naming the service `FrameState`.
- A named field such as `a.services.Prefs` would add ~2,000 longer references for no clarity
  gain, so embedding is the simpler choice once names are kept distinct.

## Part 5d — Move to SDL3 and drop what SDL3 covers

**Correction to the premise.** SDL3 core does *not* absorb SDL_mixer; mixing stays in the
separate SDL3_mixer satellite library. What changes is that **SDL3_mixer 3.x upstream
(zlib, needs SDL ≥ 3.4) now has everything our SDL Mixer X fork exists for**:
- unlimited simultaneous `MIX_Track`s on one mixer (crossfade, multi-music);
- `MIX_PROP_PLAY_LOOP_START_FRAME_NUMBER` + `MIX_PROP_PLAY_MAX_FRAME_NUMBER` (loop points);
- fades (`MIX_PROP_PLAY_FADE_IN_*`, `MIX_StopTrack(track, fadeFrames)`);
- `MIX_SetTrackPlaybackPosition` (seek), per-track gain, tags, a track-stopped callback;
- raw PCM / `SDL_AudioStream` track input (voice playback could use it).

So the fork goes: `internal/render/mixerx`, `scripts/build-sdl-mixer-x.sh`, the CI fork builds,
the loop-point patch. Open check: whether `MAX_FRAME` acts as the loop end on *every*
iteration is not stated in the header. Pin it with a test before relying on it, otherwise loop
end is emulated with the stopped callback.

**Platform code SDL3 replaces (verified call sites):**

| Today | SDL3 replacement |
|---|---|
| PowerShell `FolderBrowserDialog` (settingswidgets.go:389), file picking | `SDL_ShowOpenFolderDialog` / `SDL_ShowOpenFileDialog` (async callback → `AsyncResult`) |
| `dpiseed_windows.go` (`GetDpiForWindow`), `dpiseed_darwin.go`, `dpiseed_other.go` | `SDL_GetWindowDisplayScale` + `SDL_EVENT_WINDOW_DISPLAY_SCALE_CHANGED` |
| `rundll32`/`explorer`/`xdg-open` (about.go, openpath.go) | `SDL_OpenURL` (also `file://` for folders) |
| `render/previewowner_windows.go` (Win32 window ownership for the preview window) | parent windows (`SDL_PROP_WINDOW_CREATE_PARENT_POINTER`) |
| clipboard file/image hand-off via PowerShell (clipfile.go) | SDL3 MIME clipboard (`SDL_SetClipboardData`); verify the Windows file-drop format first |

Keep: OS toast (ostoast.go, no SDL3 equivalent), `winexec` + `os/exec` for ffmpeg (KISS),
libwebp/libavif/opus cgo (not SDL).

**Scale:** go-sdl2 is imported by 187 prod files (ui 163, render 19, cmd 5): ~4,430 references
over ~185 distinct identifiers (sdl 152, mix 26, ttf 7). SDL3 changes the render API
(float `FRect`, `SDL_RenderTexture`), event names/structs, SDL3_ttf 3.x text APIs, and audio.

**Binding (decided: own thin cgo binding; facade early, switch before the uikit move):**
- *go-sdl2 has no SDL3 version.*
- *zyko0/go-sdl3* is purego, v0.1.x, pre-v1, coverage incomplete (COVERAGE.md), embeds DLLs.
  Purego calls go through reflection and may allocate, which conflicts with the zero-alloc
  render loop and the 110 zero-allocation tests.
- *Recommended:* our own thin cgo binding, `internal/sdl` (+`ttf`, `mix`), wrapping exactly the
  ~185 identifiers we use. The cgo toolchain is already required (webp/avif/opus), cgo calls
  don't allocate, and we control the surface. That's KISS, because it covers only what we use.

**Migration method (keeps every commit green; no SDL2/SDL3 coexistence in one binary):**
1. Create `internal/sdl` (and `ttf`, `mix`) as a facade **over go-sdl2** with the same identifier
   names. Switching each file is then an import-path swap (mechanical, reviewable).
2. Switch all 187 files to the facade, package by package. Everything stays on SDL2 and green.
3. Re-implement the facade over SDL3 + SDL3_ttf + SDL3_mixer in one commit series behind it.
   Int→float rect conversion and the event-shape translation live inside the facade. Audio is
   rewritten onto `MIX_Track`. Remove `mixerx` and the fork.
4. Replace the platform code in the table above, one commit each.
5. Remove the facade's own pure-forwarding compatibility shims where call sites can use the
   SDL3 shape directly (KISS); the binding itself stays.
- Toolchain: `setup-deps.ps1` / CI install SDL3, SDL3_ttf and SDL3_mixer packages (msys2 / brew).
  Linux CI may need SDL3 built from source if the runner's distro packages are older than 3.4.
  Verify before step 3.

**Placement:** facade steps 1–2 go right after the merge checkpoint. They touch every SDL call
site once, and features moved later carry the facade import. Step 3 (the SDL3 switch) goes
before the first uikit move, so the exported uikit API is designed on SDL3 types and not
redesigned later. The prep commit no longer adds SDL Mixer X to `setup-deps.ps1`; that fork
is going away.

**Renderer (decided 2026-10-26; revised by the RmlUi decision in Part 5e).**
- Create an SDL_GPU device with the `vulkan` driver named explicitly. SDL's default choice is
  not usable: on Windows it picks D3D12, which rejects the SPIR-V-only shaders RmlUi's backend
  ships (Part 5e). If no hardware Vulkan driver is present, create the device again on the
  bundled software Vulkan driver (lavapipe or SwiftShader, pointed to with the Vulkan loader's
  driver-file setting). SDL does not fall back by itself: on a machine with no Vulkan driver
  (verified on a Hyper-V VM, 2026-10-27) device creation fails with `vulkan unsupported`.
  No per-OS driver lists beyond this, no runtime switching between backends.
- Our compositor draws through SDL_Renderer's `gpu` backend on the same device, so the
  courtroom viewport is a texture RmlUi can show.
- One setting / command-line flag forces the software driver, for broken GPU drivers.
- Handle `SDL_EVENT_RENDER_DEVICE_RESET` / `RENDER_TARGETS_RESET`: drop the GPU texture tier
  (T1) and re-upload from the decoded tier, the way theme textures already heal on eviction.
  Today nothing handles these events, so a device reset (driver update, sleep/resume, Remote
  Desktop) would likely leave textures black until restart.
- The active backend name is shown in the debug overlay and the About screen.
- Custom shaders beyond the viewport post pass (Part 5e) are out of scope; a new one is decided
  when a feature needs it, and ships as SPIR-V for the SDL_GPU device.

## Part 5e — UI spike and toolkit decision

**Decided (2026-10-26, revised the same day):** SDL3 stays the platform and **RmlUi** is the UI
library (customer decision: web-style authoring with RML/RCSS for themes and widgets). Clay was
dropped with it. Rendering is SDL_GPU on Vulkan on hardware, and a bundled CPU Vulkan driver
(lavapipe or SwiftShader) as the software tier, so RmlUi, our compositor (SDL_Renderer's "gpu"
backend on the same device) and viewport shaders share one path. macOS is unverified: SDL_GPU
uses Metal there, which cannot load the SPIR-V-only shaders, so it needs MoltenVK or shaders
translated to Metal (open item below). Earlier in the day the choice was our own kit with
Clay. Dear ImGui was dropped: it fixes input but not scaling, layout or skinning, cimgui-go
has no SDL_Renderer or SDL3 backend (v1.6.1), and its string wrappers are suspected to
allocate per call. The other full toolkits (Gio, Qt, Ebiten, web) were rejected because each
rewrites the compositor or the whole UI; RmlUi was first rejected for the same reason and
then chosen because RML/RCSS authoring is the customer requirement, and it only replaces the
UI layer, not the compositor. Gio was the fallback candidate had the spike failed.

**RmlUi facts found so far (6.3 source):** the SDL_Renderer backend is deprecated in 6.3 and
removed on master (2026-08-29), so SDL_GPU is the only maintained SDL3 path. The SDL_GPU renderer
implements only basic drawing (630 lines vs 2,171 for GL3): no clip masks, gradients,
box-shadow, filters or custom shaders until we port them. Its shaders ship as SPIR-V only. Its
texture handle is a plain `SDL_GPUTexture*`, so the courtroom viewport can be shown as an image.
The RmlUi spike checks: build with MinGW, hardware Vulkan, software Vulkan (lavapipe), data
binding of a list from Go, click semantics on release (#113), dp scaling, hot reload of RML/RCSS,
the viewport texture with a SPIR-V post-effect, and which RCSS features render. The pass
criteria below were written for Clay; criterion 5 does not apply to RmlUi as written (see the
Result).

**Spike scope.** A separate Go module outside the repo (scratch or a sibling folder), throwaway.
It links SDL3 + SDL3_image + RmlUi 6.3 (static, FreeType font engine, upstream SDL3 platform
and SDL_GPU renderer backends) through a C++ shim with a cgo binding, and draws a lobby-like
screen from RML/RCSS: a header, a resizable server list with a scrollbar, a details pane, a
themed nine-slice panel with an aspect-fit icon, buttons, wrapped text with coloured spans, and
the courtroom viewport as a GPU texture with a post-effect.

**Pass criteria** (all must hold, or the decision is reopened):
1. Scaling: moving between 100% and 150% displays and changing the UI scale (125/150/200%)
   re-lays out with crisp text and nine-slice edges, and no `SetScale` anywhere.
2. Resizing: dragging the window down to a minimum size reflows without per-screen
   rectangle code; the list keeps its scroll position.
3. Input: dragging the scrollbar and releasing over a button does not click it (#113); wheel
   over the list scrolls only the list.
4. Allocation: 0 Go allocations per frame in steady state (`testing.AllocsPerRun`).
5. Effort: the Clay shim + layout code for the screen stays small enough to read in one sitting
   (target < 600 lines total); anything bigger means Clay is not making layout easy.
6. Software renderer: the same screen runs with the software renderer forced.

**Result (2026-10-26, `codebuster-take2`): criteria 1–4 and 6 pass; criterion 5 is waived
(it measured Clay shim size and has no RmlUi equivalent) — keep RmlUi 6.3 on SDL_GPU.** The
spike was run by a second developer in a sibling folder `AsyncAO-rmlui-spike` (build/run/test
in its `README.md`, numbers and the feature matrix in `RESULTS.md`). That folder is not on the
maintainer's machine, so the numbers below cannot be rerun from the repo; an independent
retest with a separate, smaller spike follows the list.

1. Scaling: UI 125/150/200% → title 36→45/54/72 px (×1.25/×1.50/×2.00); display 150% →
   ×1.50; only `Context::SetDensityIndependentPixelRatio` is used. Nine-slice edges hold.
2. Resizing: 1024×700→620×460 → list 418→249 px, details 564→329 px, panel stays inside
   the surface, list scroll 4.0→4.0 px; flexbox plus one media query, no rectangle code.
3. Input: press 0 clicks / release 1; scrollbar drag (list scrolled to 89 px) released over
   Refresh → no click; wheel over the list scrolls only the list, wheel over details scrolls
   only details. (The pixel distances first recorded here, 4 px and 12 px, were read while
   RmlUi's smooth scroll was still running and are not scroll distances; see the retest.)
4. Allocation: `AllocsPerRun(200, Frame)` = 0.00; the full suite passes under `-race`.
5. Effort (waived): 663 lines RML+RCSS (583 non-blank) for the screen, against a Clay target
   of < 600 lines. The C++ side is 1,890 lines of SDL_GPU platform layer (window/device/
   pipelines, viewport and post passes, screenshot readback, headless mode, metrics, input
   hooks, hot reload, 3D-card input), not layout. The Clay line target does not compare like
   for like. A replacement effort target for RML/RCSS screens is to be set before step 4.
6. Software Vulkan: lavapipe renders 12 frames at 1280×760; hardware Vulkan (RTX 3060 Ti,
   driver 595.79) renders the same screen.

Also verified: MinGW build with no RmlUi source patches, a Go-owned list through
`RegisterArray`/`data-for`, dp scaling, hot reload (400 ms mtime watch), a live
`SDL_GPUTexture` viewport shared with RmlUi, a SPIR-V post pass around RmlUi (RmlUi
cannot host the shader itself), and 3D UI manipulation — a card with
`perspective`/`rotateX`/`rotateY` renders correctly and is dragged at runtime through
`Element::SetProperty("transform", …)` (a Go-owned data path drives the headless test; a
scripted drag was verified on both lavapipe and the RTX 3060 Ti).

Problems found: the SDL_GPU backend implements no `CompileShader`/`RenderShader`/
`CompileFilter`, so `linear-gradient` logs `Could not generate decorator element data` and
draws nothing, and `box-shadow` corrupts the frame with a garbage quad (evidence in the
spike's `shots/box-shadow-artifact.png`); the backend requires SDL3_image; its sources need
`<algorithm>`/`<cstring>` when built without the samples' PCH; SDL's default D3D12 device
rejects SPIR-V-only custom shaders (`-driver auto` asserts), so Vulkan must be forced;
RmlUi defaults to `display: inline`, so block rules must say so explicitly;
`Context::Update()` takes no delta time in 6.3; SDL window/GPU calls must stay on one OS
thread (the opt-in render test locks its goroutine or it can block in swapchain wait).

**Retest (2026-10-27, maintainer's machine).** An independent spike (same RmlUi 6.3 build
and backends, a 10-test suite: driver, allocations, data binding, click semantics, scrollbar
release, wheel routing, dp scaling, resize, hot reload, RCSS features) was rerun under
`-race`. The machine is a Hyper-V VM with no GPU.
- Hardware Vulkan: not testable here — no Vulkan driver is registered, and SDL_GPU device
  creation fails with `vulkan unsupported` instead of falling back (now handled in Part 5d
  "Renderer").
- lavapipe (Mesa, msys2 `vulkan_lvp.dll`, selected with `VK_DRIVER_FILES`): 0 Go allocations
  per frame; data binding, click on release, scrollbar release over a button, dp scaling
  (ratio 1.50), resize and hot reload pass.
- Wheel routing: the suite's test failed (list 51 → 68 px after wheeling over details) because
  it sampled 3 frames after the wheel while RmlUi's smooth scroll was still moving. The smooth
  scroll runs on wall-clock time, so no frame count is a safe wait. With
  `Context::SetDefaultScrollBehavior(ScrollBehavior::Instant, …)` set for the test, wheel over
  details leaves the list unchanged on every run. Tests of scrolling turn smooth scroll off.
- RCSS on the stock SDL_GPU backend: a strip of twelve tiles, one per advanced feature, on a
  known background, judged by screenshot (`advanced.png` in the spike's shots). **None of the
  twelve renders:**

  | Feature | Stock SDL_GPU backend |
  |---|---|
  | `linear-`, `radial-`, `conic-gradient` | nothing drawn; logs `Could not generate decorator element data` |
  | `box-shadow` (spread, blurred) | **corrupts the frame**: solid, misplaced blocks in the shadow colour, one covering the window title |
  | `filter: blur / drop-shadow / grayscale / opacity` | ignored; the element draws unfiltered |
  | `backdrop-filter` | ignored |
  | `mask-image` | ignored |
  | clipping children to `border-radius` | ignored; corners stay square |

  Nine-slice, images, the viewport texture with its SPIR-V post-effect, text, layout and
  everything in the criteria above do render. The suite's original feature test only logged
  and two of its pixel checks sampled the wrong spot (the white badge text, the viewport
  inside the panel); the spike now reports per feature from fixed sample points.
- Implication for themes: as shipped, a theme may use colours, borders, `border-radius`
  backgrounds, images, nine-slice and the vertical/horizontal *geometry* gradient decorators,
  and must not use the features in the table — `box-shadow` actively breaks the page.

**Open items** — each settled before the step it affects (items 1, 2, 5 and 8 before
step 2c; 3, 4, 6 and 7 before step 4). None reopens the toolkit choice.
1. **Software driver size.** msys2's lavapipe is `vulkan_lvp.dll` (17 MB) plus
   `libLLVM-23.dll` (153 MB) and `libSPIRV-Tools.dll` (2 MB): about 170 MB to ship. Measure a
   statically linked lavapipe build and SwiftShader before choosing one; also decide whether
   to ship the Vulkan loader (`vulkan-1.dll`, 0.7 MB) for machines that have none.
2. **macOS.** SDL_GPU uses Metal there and cannot load SPIR-V; choose MoltenVK or translating
   shaders to Metal (SDL_shadercross) and test it.
3. **Advanced RCSS — decided (2026-10-27): our own render interface.** RmlUi's `Backends/`
   are samples an application is expected to adapt, not part of the library, so we write our
   own SDL_GPU render interface: the SDL_GPU sample's drawing plus RmlUi's advanced methods
   (layers, filters, clip masks, gradient shaders) ported from its GL3 sample. RmlUi itself
   stays unmodified. An experiment during the retest (about 1,400 lines of C++ and 12
   shaders, not kept) made all twelve features in the table render on lavapipe, so the
   approach works. The full render interface is written in step 2c; include MSAA on layers
   if transformed edges need it, and run the per-feature test on a hardware GPU.
4. **Text.** RmlUi draws text with FreeType. Decide whether SDL3_ttf stays (courtroom message
   raster) or goes, and test font fallback for CJK and emoji (today's `fontfallback.go`) and
   IME composition through RmlUi.
5. **Device loss.** Part 5d handles SDL_Renderer's reset events; find and test the SDL_GPU
   equivalent (driver update, sleep/resume, Remote Desktop).
6. **C++ allocations.** The allocation gate counts Go allocations only. Measure RmlUi's heap
   use per frame in steady state against the 256 MiB budget.
7. **uikit scope.** Steps 4–5 and the uikit export surface (Part 3) were written for our own
   kit: our input model (press/release, capture, wheel routing), scroll container, text
   layout and widgets. RmlUi already provides click-on-release, scrollbars, wheel routing,
   text layout and form controls (pass criterion 3). Rewrite steps 4–5 to say which of these
   uikit still owns (likely the AO theme screens and the courtroom, which stay on our
   compositor) and which become RML/RCSS.
8. **Platform layer.** Decide where the RmlUi binding lives and add it to the Platform row of
   the layer diagram (Part 3). The `docs/ARCHITECTURE.md` dependency row for RmlUi is in
   place as "planned"; finalise it when items 1 and 2 are settled.

Decision: keep RmlUi on SDL_GPU/Vulkan, with the open items above settled on that schedule.

## Part 6 — Adversarial review (verified against the code; amendments applied above)

> Reviewed against the first draft. Since the three-layer revision (Part 3): items 1 and 5 no
> longer apply (no Services bag), and item 3 plus the last "claims checked" entry are moot because
> tabs are held by pointer and nothing copies `sessionState`. `AsyncResult` stays pointer-backed.

**Design flaws found and fixed in this plan:**

1. **`Frame` name collision.** `App.Frame(dt,w,h)` is the main-loop entry point; an embedded
   `Services.Frame` field would be shadowed by it (`a.Frame` = the method). → The pacing/damage
   service is named **`FrameState`** (matches the existing pacer API: `FramePace`, `NextWakeDelay`).
2. **`SuspendModal() (restore func())` would allocate.** A returned closure capturing state
   escapes to the heap; it would be called on draw paths covered by 110 zero-allocation tests,
   and `modalOn` is touched at 105 sites. → Value token instead:
   `m := c.SuspendModal(); …; c.RestoreModal(m)`. Same rule for every uikit API on a draw
   path: no returned closures, no per-frame interface boxing of non-pointer values.
3. **`AsyncResult[T]` copy semantics.** The live session is copied by value in four places
   (tabs.go:302/326 park/activate, app_room.go:581-606 and 710-714 pinned pass,
   themeeditorpreview.go:457-485 editor preview, each copying side effects back). A `AsyncResult`
   with value fields (`busy`, `cancel`) is only correct while exactly one copy is "the" tab; any
   stray copy (e.g. a future `saved := a.sessionState`) would diverge silently. → `AsyncResult` is
   pointer-backed: `NewAsyncResult[T]()` returns a handle whose state lives on the heap, so every
   copy of a `Tab` aliases the same slot. Feature `Tab`s therefore need constructors:
   `NewTab(...)`, called from `resetSessionState` (which today seeds prefs-backed defaults
   inline, tabs.go:1428).

**Costs the first draft underestimated:**

4. **Test churn is the dominant cost.** 214 of 317 ui test files call private App methods;
   75 use `&App{…}` literals (169 literals). The jukebox move alone touches 6 test files, one of
   them (layoutnudge_test.go) with 133 private App refs unrelated to jukebox. → Each feature move
   starts with a test inventory: tests of the feature's own logic move into the package (they may
   stay white-box there); App-level tests that drive the feature switch to its exported API;
   unrelated tests that merely mention it are left alone. Budget each move accordingly.
5. **`a.d` → embedded Services rename touches ~1,355 Prefs refs plus Manager/Store/Viewport/Audio.**
   → Done as one isolated, purely mechanical `gopls rename` commit (no other edits), so review
   is "did anything but the identifier change".
6. **Package globals** (`settings`, `contentPanel`, `demoBrowser`, `baseWizard`) are not covered by
   the lifetime audit. → Each becomes a field of its feature's `Panel` in that feature's move
   commit (settings, studio, settings respectively); none survive as globals.
7. **Test time grows** with every new cgo package (one race-instrumented test binary each, and
   `-p 1` on this machine). Accept; note per-phase suite duration in the metrics.

**Sequencing amendments:**

8. **Bug 4 and the lifetime audit are the same change** (moving damage/pacer fields off
   `sessionState`). → Write bug 4's failing test, then the lifetime-audit commit that moves the
   fields fixes it; no separate fix commit.
9. **Merge checkpoint.** Phases 0–2 (prep, helper, bug fixes) are small and independently useful.
   → They end in a green, mergeable state; the user pushes/merges before the long uikit work
   starts, so `main` isn't left behind a months-long branch.
10. **uikit is a long bottleneck** (≈90 private members across 141 files, plus kit files that
    hold App methods: emojilabel.go, shapemask.go, themerowpitch.go). With C1 it is no longer the
    first step. It happens at the first feature move, once several features have been extracted in
    package and have shown which Ctx members really need exporting.

**Claims checked and holding:**

- Swap-safety rule works with the copy-back pattern: all three swap sites copy the pass's side
  effects back to the tab they belong to, so "features hold a pointer to the fixed live slot"
  is sound.
- uikit dependencies are acceptable: kit files import only sdl/ttf, `render` and `theme`
  (`theme.Palette`, `theme.ReadQtRowPitch`), which sit below it; no package global is read by the
  kit itself.
- The panel interface is justified by four existing hand-kept lists, not speculation.
- `AsyncResult` refusing while busy keeps goroutines bounded (hard rule 4).

## Critical files
internal/ui/app.go, app_frame.go, apptypes.go, tabs.go, ui.go, uiwidgets.go, uiwindow.go,
floatbox.go, floatwin.go, torntabs.go, app_room.go, newroom.go, charmeta.go;
internal/config/settings.go, prefs_swap.go, preferences.go; cmd/asyncao/main.go; CLAUDE.md.
