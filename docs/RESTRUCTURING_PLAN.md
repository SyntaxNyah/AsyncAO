# AsyncAO restructuring plan (codebuster-take2)

Approved by the maintainer on 2026-10-26. This file is the handoff document: an agent picking
up the work should read CLAUDE.md, then this file, then continue at the first unchecked step
in **Status**. Update Status in the same commit that completes a step.

## Status

- [x] 0. Prep: delete `internal/debuglog`; CLAUDE.md facts + new rules; plan in repo;
      golangci-lint `mnd` in CI (9 already-clean packages enforced from the start)
- [x] 1. `internal/helper/{value,text,parse}` + duplicate removal + config double-clamp removal;
      mnd now enforced on every lower package step 1 touched (setter pre-clamps: see Part 3)
- [x] 2. Bug fixes 1–3, 5–7 (one commit each, with tests; 6 landed in step 1) → **merge checkpoint** (maintainer pushes)
- [x] 2a. UI spike: RmlUi 6.3 on SDL_GPU (Vulkan, software Vulkan fallback) (Part 5e); throwaway code outside the module
      — see the Result in Part 5e
- [x] 2b. SDL facade (`internal/sdl`, `ttf`, `mix`) over go-sdl2; switch all importers
      (`render/mixerx` became `internal/mix`; `render/scalemode_cgo.go` still calls SDL C directly, see 2c)
- [ ] 2c. SDL3 switch: SDL_GPU on Vulkan (hardware, else bundled software Vulkan), device-reset
      re-upload; drop the SDL Mixer X fork; replace platform code
  - [x] binding over SDL3/SDL3_ttf/SDL3_mixer, audio on `MIX_Track`, fork and go-sdl2 dropped,
        scale-mode shim moved into the binding; deps scripts + CI (cached setup-sdl build on
        Linux/macOS, MSYS2 packages on Windows)
  - [x] SDL_GPU renderer on one device (`render.OpenBackend`: Vulkan, then lavapipe, then SDL's
        own renderer, each a `render.Backend`), `-software-gpu` flag; lavapipe staged by
        build.ps1 and the release job
  - [x] device loss (decided 2026-10-28): `render.GPUWatcher` restarts the client, restoring
        its tabs; in-process rebuild with T1 re-upload moves to after step 3 (Part 5d)
  - [x] platform skeleton: `internal/platform` (`Detect(probe)`, environment, capabilities, report);
        no consumers yet, nothing moved (Part 5g, Review outcome, sequence item 2)
  - [ ] the rest of Part 5g's sequence, items 3–7, which includes the backend name in the debug
        overlay and About, and the platform code (dialogs, DPI seeding, URL opening, preview
        ownership, clipboard)
- [ ] 3. Engine: `internal/scene`, then `client.Session` per tab (async connect; tabs held by
      pointer, no park/activate copies; fixes bug 4, #115, #141, #84)
- [ ] 4. `internal/uikit`: extract the kit, then the input model (press/release + capture,
      wheel routing, layer stack)
- [ ] 5. uikit scaling, layout and text (logical units, RmlUi layout, nine-slice, scroll
      container) + shared widgets; one text-colour palette in the engine
- [ ] 6. Config API: `Set*` forwarder removal; engine option structs; engine stops importing config
- [ ] 7. Feature loop (jukebox → notebook → … → courtroom screen), extract then move
- [ ] 8. Remaining lower-layer work (Settings grouping, Manager `Source`, render debug sink)
- [ ] 9. Fold `cmd/asyncao-cache` into the client with its own UI (needs a mature uikit)

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
              internal/rmlui         our cgo binding to RmlUi: Go API, our SDL_GPU render
                                     interface, RmlUi's SDL3 platform backend (C++)
all layers ──► internal/helper/{value,text,parse}
```

**Import rule.** App layer → uikit, engine, config. Feature → uikit, engine (never
`internal/ui`, never a sibling feature, never config). uikit → render, theme, sdl, rmlui.
Only uikit and `cmd/asyncao` import rmlui. Engine → engine and helper only. Cross-feature needs (#114: OOC log → jukebox) use a small
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
func ClampSet[T integer](v, lo, hi T) T           // 0 = "unset" stays 0, else Clamp (the ~30 config sentinel blocks);
                                                  // integer is declared locally: the stdlib has no cmp.Integer

// internal/helper/text — rune-safe string shaping
func Truncate(s string, maxRunes int) string
func TruncateBytes(s string, maxBytes int) string // never splits a rune

// internal/helper/parse — untrusted input → values (future home of config value parsing)
func AtoiOr(s string, fallback int) int           // trims space
func HexColor(s string) (color.NRGBA, bool)       // optional '#', 6/8 digits, straight alpha
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
  setter pre-clamps (in the helper series). **Done in step 1, narrowed:** only setters whose
  setter golden stays identical lost their clamp (name sat/val, audio volumes, text stay,
  catch-up, input grace). The rest are not duplicates: for a sentinel field (0 = unset)
  `SetX(0)` stores the minimum, `Update` stores "unset", so a slider dragged to 0 would jump
  to the default. Others clamp a field `normalizeSettings` does not touch (export, sprite
  opacity, panel font, profile, reflect, weather kind, chrome tier, animated cap, chat rate,
  theme fit + pan); moving those into `normalizeSettings` also clamps them on load, a
  behaviour change for step 6 to decide. The ~7 ui re-clamps feed labels and local widget
  state before any setter runs, so they stay until the ranged widgets of steps 4-5.
  Delete pure single-caller `Set*` forwarders, keep setters that carry logic or have ≥3
  callers (C5), before the first feature move.
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
    SDL3_mixer; rewrite audio onto `MIX_Track`; reimplement `internal/mix` (formerly `render/mixerx`)
    and delete the fork build script and the CI fork steps. Move render's texture scale-mode cgo
    shim (`scalemode_cgo.go`) into `internal/sdl`, so only the binding calls SDL. Pin the loop-end behaviour with a test. Renderer: SDL_GPU on
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
9. **Fold the cache tool into the client.** `cmd/asyncao-cache` (stats, inspect, prune, warm for
   the T3 disk cache) becomes part of the client once uikit has matured: its own screen, opened
   from Settings or with a command-line flag on `asyncao`, then the separate command is deleted.
   Until then it stays as it is. It currently builds only with cgo (it imports `internal/assets`
   for the `extensions.json` parser), though its doc comment says it is pure Go, and no build
   script or CI job builds it.

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

So the fork goes: the SDL Mixer X code in `internal/mix` (formerly `render/mixerx`), `scripts/build-sdl-mixer-x.sh`, the CI fork builds,
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
   rewritten onto `MIX_Track`. `internal/mix` drops SDL Mixer X; the fork goes.
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
  bundled software Vulkan driver (lavapipe, open item 1, pointed to with the Vulkan loader's
  driver-file setting). SDL does not fall back by itself: on a machine with no Vulkan driver
  (verified on a Hyper-V VM, 2026-10-27) device creation fails with `vulkan unsupported`.
  No per-OS driver lists beyond this, no runtime switching between backends.
- Our compositor draws through SDL_Renderer's `gpu` backend on the same device, so the
  courtroom viewport is a texture RmlUi can show.
- Measured on the maintainer's machine (RTX 3070, 2026-10-28), lobby open, private memory of
  the whole process: hardware Vulkan 307 MB, lavapipe 223 MB, SDL's D3D11 renderer 146 MB.
  The difference is driver memory, outside the Go heap that GOMEMLIMIT governs, but it counts
  against the 256 MiB budget. Follow-up: find what the Vulkan driver holds (pipeline cache,
  swapchain images, staging buffers) and what SDL's `gpu` renderer lets us trim.
- One setting / command-line flag forces the software driver, for broken GPU drivers.
- Device loss: SDL's `gpu` renderer sends no reset events (Part 5e open item 5), so
  `render.GPUWatcher` treats three failed presents in a row as a lost device. Recovery, decided
  2026-10-28, is a clean restart through the existing relaunch, reopening the open tabs: every
  texture belongs to the lost renderer, and about twenty owners (the label atlas, T1, viewport
  effect textures, rasters, parked-tab copies) would each have to release first, where one
  missed owner is a use-after-free on a path that almost never runs. A loss within a minute of
  a restart escalates to the software driver, and then to quitting with a message instead of
  restarting in a loop. Rebuilding in-process (new device and renderer, T1 re-uploaded from
  the decoded tier) follows step 3, when per-tab sessions remove the parked copies and texture
  ownership is in a few places.
- The active backend name is shown in the debug overlay and the About screen.
- Custom shaders beyond the viewport post pass (Part 5e) are out of scope; a new one is decided
  when a feature needs it, and ships as SPIR-V for the SDL_GPU device.

## Part 5e — UI spike and toolkit decision

**Decided (2026-10-26, revised the same day):** SDL3 stays the platform and **RmlUi** is the UI
library (customer decision: web-style authoring with RML/RCSS for themes and widgets). Clay was
dropped with it. Rendering is SDL_GPU on Vulkan on hardware, and a bundled CPU Vulkan driver
(lavapipe, chosen in open item 1) as the software tier, so RmlUi, our compositor (SDL_Renderer's "gpu"
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
step 2c; 3, 4, 6, 7, 9, 10 and 11 before step 4). None reopens the toolkit choice.
1. **Software driver — decided (2026-10-27): lavapipe.** RmlUi is verified on lavapipe and
   not on SwiftShader, and one driver to make fast beats two dependencies. Measured on the
   Hyper-V VM (no GPU), a 1920×1080 courtroom-like frame through SDL's `gpu` renderer, synced
   every frame:

   | Driver | Size raw / gzip | Frame avg / p95 | Private memory | Start |
   |---|---|---|---|---|
   | today's default (D3D11, WARP) | — | 10.7 / 11.9 ms | 68 MB | 17 ms |
   | msys2 lavapipe (LLVM as DLL) | 172 / 47 MB | 22.6 / 31.4 ms | 240 MB | 77 ms |
   | lavapipe, static LLVM (mesa-dist-win) | 62 / 21 MB | 25.5 / 34.1 ms | 238 MB | 1.8 s |
   | SwiftShader (Edge's build) | 4.5 / 1.8 MB | 32.2 / 35.0 ms | 161 MB | 68 ms |

   Windows ships msys2's lavapipe (`vulkan_lvp.dll`, `libLLVM-23.dll`, `libSPIRV-Tools.dll`)
   and the Vulkan loader (`vulkan-1.dll`, 0.7 MB) for machines that have none; Linux uses the
   distro's Mesa. Optimising that route is follow-up work: a trimmed static build, and the
   ~240 MB of driver memory, which leaves the 256 MiB budget no room on the software tier.
2. **macOS.** SDL_GPU uses Metal there and cannot load SPIR-V; choose MoltenVK or translating
   shaders to Metal (SDL_shadercross) and test it. Until then (decided 2026-10-27) macOS does
   not name a driver: SDL picks Metal, which the compositor's `gpu` renderer supports (it
   ships MSL shaders); only RmlUi's SPIR-V shaders need this item.
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
   IME composition through RmlUi. Known SDL_ttf 3.2.2 bug (2026-10-28): a glyph from a
   fallback font is placed by that font's ascent, not the line's (fixed on SDL_ttf main, not
   yet released). Labels compose runs on the primary's baseline (`Ctx.renderLabel`) and
   messages use the per-run raster until msys2 and the distros ship a fixed release; then
   mixed text can draw through the fallback chain in one call again.
5. **Device loss — decided (2026-10-27): detect and rebuild.** SDL 3.4's `gpu` renderer sends
   no `RENDER_DEVICE_RESET`, `RENDER_TARGETS_RESET` or `DEVICE_LOST` event, and SDL_GPU's
   Vulkan backend only turns `VK_ERROR_DEVICE_LOST` into a failed call. A failed present or
   submit therefore marks the device lost; the next frame destroys and recreates the renderer
   and device and re-uploads T1 from the decoded tier. A test forces a loss. Revised
   2026-10-28: the client restarts instead, until step 3 makes in-process rebuild safe
   (Part 5d "Renderer").
6. **C++ allocations.** The allocation gate counts Go allocations only. Measure RmlUi's heap
   use per frame in steady state against the 256 MiB budget.
7. **uikit scope.** Steps 4–5 and the uikit export surface (Part 3) were written for our own
   kit: our input model (press/release, capture, wheel routing), scroll container, text
   layout and widgets. RmlUi already provides click-on-release, scrollbars, wheel routing,
   text layout and form controls (pass criterion 3). Rewrite steps 4–5 to say which of these
   uikit still owns (likely the AO theme screens and the courtroom, which stay on our
   compositor) and which become RML/RCSS. Part 5f answers most of this; what remains is
   rewriting steps 4–5 to match it.
8. **Platform layer — decided (2026-10-27): `internal/rmlui`, a fourth binding package.**
   - It sits next to `sdl`, `ttf` and `mix` in the Platform row (Part 3). It holds the Go API
     (context, documents, data models, input forwarding, update and render), our SDL_GPU render
     interface (item 3), and RmlUi's upstream SDL3 platform backend. The render interface
     includes SDL headers, so hard rule 1 names `rmlui` as a binding package when it lands.
     Only uikit and `cmd/asyncao` import it, and everything there runs on the locked main
     thread.
   - `internal/sdl` owns the window, the SDL_GPU device (Vulkan, then software Vulkan; Part 5d)
     and the `gpu` SDL_Renderer on that device. rmlui is handed the window and device the way
     `ttf` hands surfaces back: an `unsafe.Pointer` across the package boundary
     (`sdl.WrapSurface` today), because cgo's C types are local to each package.
   - RmlUi itself is not in the repo. msys2 has no RmlUi package, so a script builds a pinned
     RmlUi 6.3 tag as a static library, with the FreeType font engine, into a local prefix
     (`scripts/build-rmlui.sh`, run under msys2 bash on Windows), and CI caches it like
     `setup-sdl3`. RmlUi stays unmodified.
   - Shaders are compiled to SPIR-V by a generation script and committed as Go-embedded bytes,
     so a normal build needs no shader compiler (the same approach as the committed
     `versioninfo_windows.syso`).
   - Hard rule 2: RmlUi's file and texture callbacks run on the render thread and are
     synchronous. Our file interface reads only from memory (embedded RML/RCSS and fonts, and
     theme bundles loaded off-thread). Our `LoadTexture` serves from the decoded tier: if the
     image isn't ready, it returns a placeholder, queues a fetch, and releases the texture when
     the decode lands so RmlUi loads it again. Images then go through our own decoder (WebP,
     AVIF, APNG), so **SDL3_image is not used**; the spike needed it only because the sample's
     `LoadTexture` calls `IMG_Load` from disk.
   - Image sizes without a layout jump: only an `<img>` with no RCSS width or height takes its
     size from the image (decorators and nine-slice draw into a box the layout already sized).
     For a bundle image, `LoadTexture` reads width and height from the image header in the
     in-memory bundle (PNG, WebP, AVIF and GIF store them in the first bytes; no disk, no
     decode) and returns them with a placeholder of that size, so the first layout is final
     and only the pixels arrive later. Server images (character icons, evidence) sit in boxes
     the theme sizes; the bundle validator warns when one has no size.
   - The binding also provides what Part 5f needs: several documents in one context, data
     models registered from Go, events routed to Go commands, and our own custom elements
     registered with RmlUi (`ElementInstancer`).
   - macOS: rmlui builds there, but its SPIR-V shaders don't load on Metal until item 2 is
     settled.

9. **Capability API v1.** Name the first set of parts (Part 5f), their data, attributes and
   commands, how the API is versioned, and how data model updates and commands stay free of
   per-frame allocations. Settled before step 4.
10. **Bundle safety.** Caps on bundle size, document count, data model sizes and image sizes
    (hard rule 4); file access confined to the bundle through `safepath`; what the validator
    rejects and what it only warns about. Settled before step 4.
11. **Separate windows (Part 5f).** A small spike: two OS windows on one SDL_GPU device, an
    RmlUi context in each with a data model fed from the same Go state, the courtroom
    viewport's GPU texture drawn in the second window (an SDL_Renderer texture belongs to one
    window's renderer, so this needs the underlying `SDL_GPUTexture`), dragging a panel
    across windows, and a forced device loss rebuilding both. Settled before step 4, because
    the binding's context handling and the part registration depend on it.

Decision: keep RmlUi on SDL_GPU/Vulkan, with the open items above settled on that schedule.

## Part 5f — Theme bundles and widgets (decided 2026-10-28)

The reason for the bundle format is custom widgets: a theme can ship its own panels, for
example an audio mixer with different controls than ours, that the user can move around the
UI and dock. Our own editor makes bundles easier to build. Old AO themes keep working as they
do today (absolute rectangles, scaled uniformly; Part 3).

**Parts, not panels.** The client publishes fine-grained parts that do not belong to any
panel: `ooc.log`, `ooc.input`, `audio.channels`, `audio.nowplaying`, `area.list`, … A widget
declares which parts it uses, from any feature: a mixer that also holds the OOC input, while
the OOC log sits in another widget, is a valid layout, and so is one part in two widgets.
Parts come at two levels:
- **Data and commands** for simple things (a volume, a track name, `audio.play`, `ooc.send`),
  through RmlUi data binding. A theme builds any control it likes from them.
- **Built-in elements** for parts with real behaviour: custom tags such as `<ao-chatlog>`
  (a virtual list over thousands of lines, zero allocations per frame), `<ao-ooc-input>` (IME,
  emoji, history, Enter to send) and `<ao-viewport>`. A theme places, configures (attributes)
  and styles (RCSS) them; the behaviour stays ours, so a bundle cannot break the chat.

**No scripting.** Bundles hold RML, RCSS, fonts, images and a manifest; nothing executes.
RmlUi's Lua plugin is not linked. Revisited only if a real widget cannot be built from parts.

**State lives in the client; documents are views.** Shared data (log lines, volumes, the area
list) is the same in every widget showing it. Per-instance state (an input's draft text,
scroll position, focus) is owned by the client, keyed by widget instance. Commands are global.
Any document can be destroyed and recreated without losing anything, which is what moving and
docking, hot reload in the editor and device-loss recovery all rely on.

**Parts follow the active tab.** Parts read from the active tab's `client.Session` (step 3),
so switching tabs changes what every widget shows without reopening any document. The
capability API sits on `client.Session`.

**Loading.** The manifest lists each widget: id, title, the parts and API version it needs,
default dock position, minimum size, whether it can be moved. On load the client validates the
bundle and connects each widget to its parts. A widget asking for a missing part or a newer
API is turned off with a visible message and the built-in panel takes its place; a broken
bundle never takes the client down.

**Docking is the client's.** The client owns the dock system (layers, drop targets, the
saved arrangement per theme in config). A theme declares panels and defaults; the user moves
them. Docking within the main window ships first; detaching a panel into its own OS window,
and docking it back by dragging it onto the main window, follows (decided 2026-10-28). The
design allows for it from the start:
- One RmlUi context per OS window, each with its own `dp` ratio, so a window on another
  monitor scales on its own. Parts are registered in every context that shows them, all fed
  from the same client state, so nothing is copied or synchronised.
- One swapchain per window on the shared SDL_GPU device (`SDL_ClaimWindowForGPUDevice`);
  device-loss recovery rebuilds every window.
- SDL events carry the window id and go to that window's context. Dragging a window back
  onto the main window's drop targets uses the global mouse position.
- The saved arrangement records which panel is in which window, and each window's position
  and size per monitor.
- The detached preview window (`render/previewowner_windows.go`, replaced by SDL3 parent
  windows in step 2c) is the first user of the same mechanism.

**Our own UI uses the same format.** The default theme is a bundle and our panels are built
from the public parts. If our own OOC panel cannot be built from them, a theme author cannot
build one either, so gaps in the API show up in our work first.

**Scaling and resizing.** Layout is RCSS: `dp` units follow display DPI and the UI scale,
percentages and flexbox reflow on resize, media queries switch layouts at breakpoints (all
verified in the spike, Part 5e). `img { width: 100%; height: auto; }` keeps an image's aspect
ratio from the header size (item 8). For sharpness at high scale the manifest can list
resolution variants of an image (1×, 2×, …); the loader picks the nearest to the current
scale and swaps on a scale change without moving anything. Theme options (compact chat,
colour variants, …) are declared in the manifest and switched with RmlUi themes
(`@media (theme: …)`), shown in Settings with no per-option code.

**Manifest contents.** Documents and stylesheets, fonts, images (size, hash, resolution
variants), widgets (above), theme options, required capability API version.

**Editor.** A palette of parts with live fake data (a fake `client.Session` through
constructor injection, rule 12), so a widget can be designed without a server; changes show
through hot reload.

**Plan impact.** Open item 7 is mostly answered: panels, docking and the built-in widgets
become RML documents in the client's dock system, and our own kit keeps the courtroom
viewport and the legacy AO theme screens. Steps 4–5 are rewritten with item 7. Focus and
keyboard routing across widgets (Esc, Enter, global hotkeys following the focused part)
belong to the client's input handling, not to themes.

## Part 5g — Platform layer and graphics (PROPOSED 2026-10-28, awaiting approval)

**Why.** Platform knowledge is scattered: 22 OS-tagged files in seven packages (`hwid`,
`netproxy`, `presence`, `winexec`, `render`, `ui`, `cmd`) and 19 `runtime.GOOS` uses in 13
files, twelve of those files in `ui` opening URLs, revealing paths, picking folders, copying files,
showing notifications and locating fonts on their own. Nothing at startup knows what the
client can do where it runs; each feature finds out by trying. Mobile (Android, iOS) would
add a third and fourth branch to every one of them.

**The boundary (decided 2026-10-28, option C of the F4 review).** The platform layer hides
the operating system, the app's lifecycle and hardware acquisition. It does not hide SDL:
SDL is the portability layer for windows, input, 2D drawing, text, audio and the GPU, and
every layer above the platform is written against it, but only through our binding
(`internal/sdl`, `ttf`, `mix`, `rmlui`), never SDL's C API or a third-party wrapper. The
binding is the replaceable layer: if SDL ever had to go, its API is re-implemented on
something else and the code above it does not change, the same way steps 2b and 2c moved
the client from go-sdl2/SDL2 to SDL3. Graphics backends are what SDL offers (SDL_GPU on
Direct3D 12, Vulkan, Metal; SDL's renderer on Direct3D 11, OpenGL, OpenGL ES, software). Not
pursued: hiding SDL behind our own drawing, text, input and audio interfaces (option B),
which only a web build would need, since Go's WebAssembly target has no cgo. In one line:
above the platform, code knows SDL's drawing and input API through our binding, and never
which OS, driver or device it runs on.

**The layer.** `internal/platform` is the platform abstraction layer and the bootstrap
backbone: the first thing `main` builds, before SDL, and the only package that knows which
OS it is on.
- `Environment` (plain data, detected once): OS and architecture; form factor (desktop,
  mobile); packaging (dev build, portable, installed, Flatpak, AppImage, macOS bundle, app
  store); sandboxed; input (mouse and keyboard, touch); whether paths are case-insensitive;
  the main-loop style the OS wants (a blocking loop, or SDL's main callbacks on iOS); and the
  paths (config, cache, data, logs, user fonts).
- `Capability` (an enum) and `Capabilities` (the set present): open a URL, reveal a file,
  pick a folder or file, copy files to the clipboard, system notifications, relaunch the
  client, self-update, an external video encoder (ffmpeg), system font discovery, proxy
  discovery, machine id, Discord presence, extra OS windows, background rendering.
- One service interface per capability, each with one job (`URLOpener`, `FileRevealer`,
  `FilePicker`, `Notifier`, `ClipboardFiles`, `Relauncher`, `Updater`, `ProcessRunner`,
  `FontLocator`, `ProxyDiscoverer`, `MachineID`, `PresenceDialer`, …). A capability is present
  exactly when its service is: `Platform` hands a service out as `(service, ok)`, and the
  capability set is derived from the services, so the two cannot disagree.
- `GraphicsAPIs()`: the graphics APIs this platform offers, in order, as plain descriptors
  (API name, has an SDL_GPU device, software, Vulkan driver manifest). Windows: Direct3D 12,
  then Direct3D 11; Linux: Vulkan, then lavapipe, then OpenGL; macOS: Metal; Android: Vulkan,
  then OpenGL ES; iOS: Metal.

**The toolkit** (revised 2026-10-28): everything the client needs from the machine it runs
on, grouped by job. Each group is a few single-job services; a group is not an object.
Pure Go in `internal/platform` unless marked *host* (SDL-backed, Part "The host" below).
Rows marked *mobile* are reserved for the mobile milestone and not built before it.

| Group | Services | Comes from today |
|---|---|---|
| Identity | `AppIdentity` (name, version, channel, executable, packaging); `DeviceFingerprint` (the machine's stable fingerprint) | `update.Version`, `ui/datadir.go`; `hwid/hwid_{windows,darwin,linux,other}.go` |
| System | `SystemInfo` (OS and version, arch, CPU count, total memory, form factor) | `runtime.GOOS` branches; memory budget in `main` |
| Paths and storage | `Paths` (config, cache, data, logs and crash log, user fonts, desktop, temp; portable mode); `FileSystem` (roots, case-insensitive paths) | `config/location.go`, `ui/datadir.go`, `setupCrashOutput`, `USERPROFILE` in `ui/debugpanel.go`, `ui/demobrowser.go`, `ui/app_frame.go` |
| Process | `ProcessRunner` (hidden console, find a tool such as ffmpeg); `Relauncher`; `Updater` (release asset, permissions, quarantine) | `winexec`, `videoenc`, `update/apply.go`, `ui/update_ui.go` |
| Network and IPC | `ProxyDiscoverer` (the OS proxy settings); `PresenceDialer` (Discord's socket or pipe) | `netproxy/discover_*.go`; `presence/dial_*.go`, `XDG_RUNTIME_DIR` |
| Fonts | `FontLocator` (system font folders, well-known faces per role) | `ui/fontcensus.go`, `fontfallback.go`, `themefonts.go`, `emoji.go`, `WINDIR`, `LOCALAPPDATA` |
| Desktop integration (*host*) | `URLOpener`, `FileRevealer`, `FilePicker`, `ClipboardFiles`, `Notifier`, `WindowIcon`, `Windows` (extra OS windows) | `ui/about.go`, `openpath.go`, `settingstabs.go`, `settingswidgets.go`, `clipfile.go`, `ostoast.go`, `mascot_icon_*.go`, `render/previewowner_*.go` |
| OS preferences (*host*) | `DisplayScale`; system light/dark theme and reduced motion (*mobile*) | `ui/dpiseed_*.go` |
| Hardware (*host*) | window, `Graphics`, audio device, displays, input devices (touch); power and low-memory warnings (*mobile*) | `cmd/asyncao/main.go`, `render` audio |
| Lifecycle (*host*) | frame loop in the OS's style; background and foreground; GPU loss | `cmd/asyncao/main.go` |

Two moves keep existing logic where it is and inject only the OS part. The per-server HDID
stays derived at the dial site in `internal/ui` (`hwid.For(fingerprint, address)`), so the
privacy gate (CLAUDE.md rule 11) keeps seeing it; only the fingerprint's OS detection moves.
`netproxy` keeps its policy and transport and receives the discovered OS settings.

**The host: app init and hardware acquisition** (decided 2026-10-28: the platform layer owns
the whole initialisation, from process setup through SDL to the GPU, so platform differences
are settled before SDL starts). The SDL-backed half of the layer, `internal/platform/host`,
starts the app and acquires the hardware it runs on, so `render` and `ui` only consume what
they are handed. Before `SDL_Init`, from what `platform.Detect()` found:
- Process setup: lock the main OS thread where SDL needs it (not on Android, where SDL calls
  the app's main on its own thread); the memory limit from `SystemInfo` instead of a fixed
  700 MiB (`memoryBudgetBytes`; the 256 MiB in older text predates 7fe8aec2); the crash log in the platform's log path (today it goes next to the executable,
  which is not writable under Program Files, in a macOS bundle or in a Flatpak).
- SDL hints, none of which are set today: the app id and name (Wayland, Flatpak and the
  audio mixer show "AsyncAO"), the Wayland or X11 preference, Windows DPI awareness,
  touch-to-mouse behaviour on mobile.
- The subsystems to start and the window flags (high-DPI on macOS), and the main-loop style
  (blocking, or SDL's main callbacks on iOS), which must be chosen before SDL owns the entry
  point.
Then:
- **Init and run.** `host.Start(platform)` initialises SDL's subsystems, opens the main window
  with the flags the platform asks for (high-DPI on macOS), and runs the frame loop in the
  style the environment asks for: a blocking loop on desktop, SDL's main callbacks on iOS. It
  turns lifecycle events (entering and leaving the background) into calls on the app.
- **GPU acquisition.** `host.Driver` (interface: `Name`, `Open(window, vsync)`) has two
  implementations, `gpuDeviceDriver{api, shader format, driver files}` (an SDL_GPU device on a
  native API and SDL's gpu renderer on it; Vulkan driver files set only while the device is
  created) and `sdlRendererDriver{name}` (SDL's own renderer, no device). They are built from
  the platform's `GraphicsAPIs()`; `host.AcquireGraphics` walks them in order and returns
  `Graphics`, which owns renderer and device (`Renderer`, `Device`, `Info`, `Present`,
  `Suspend`/`Resume`, `Close`). `render` and `ui` receive the renderer; RmlUi the device.
- **GPU loss.** `LossDetector` (pure), `RecoveryPolicy` (pure, clock injected) and
  `RecoveryActions` (interface: relaunch through the platform's `Relauncher` on desktop,
  re-acquire `Graphics` in place on mobile once texture ownership allows it), composed by
  `GPUWatcher`, all in the host. The frame loop observes each present; nothing outside the
  host knows a device can be lost, only that the app may be relaunched or asked to re-upload.
- **Other hardware**: the audio device (SDL3_mixer), displays and their scale, input devices
  (touch present or not), acquired the same way and handed out the same way.
- **SDL-backed services** (URL opening and file reveal through `SDL_OpenURL`, file dialogs,
  clipboard, extra windows) are added to the `Platform` by the host after SDL starts.

**Entry and exit: the platform is the app's begin and end point** (decided 2026-10-28).
`cmd/asyncao`'s `main` is one line, `host.Main(build)`. The platform drives the app's whole
lifetime and calls back into `cmd` for the one thing only `cmd` knows, how to compose the
client:
1. Detect and pre-init: `platform.Detect()` (environment, paths, the services that need no
   SDL), then process setup and SDL hints (above).
2. SDL and hardware: SDL starts, the window opens, graphics (from `GraphicsAPIs()`) and the
   audio device are acquired; SDL's services and facts join the platform.
3. Report: environment, hardware acquired ("direct3d12 (NVIDIA GeForce RTX 3070)"),
   capabilities present, capabilities missing and why ("self-update: off in a Flatpak"), to
   the log, the About screen and the debug overlay.
4. Build: the platform calls `build(runtime)`, `cmd`'s composition function. `Runtime` holds
   what was acquired and offered (graphics, audio device, window, services); `build` wires
   the client by constructor injection and returns an `Application`.
5. Run: the frame loop in the OS's style. The platform owns how it waits, presents and
   watches the GPU; the `Application` decides when a frame is worth drawing (its pacing).
   Lifecycle events become `Suspend`/`Resume` calls.
6. Shutdown: `Application.Shutdown()` (flush prefs, remember tabs, close logs), then the
   hardware is released in reverse order, then the process relaunches (if the app asked the
   `Relauncher`, or GPU-loss recovery did) or exits.

`Application` (interface, crosses the platform → app boundary): `Event`, `Frame`, its pacing,
`Suspend`, `Resume`, `Shutdown`. The App layer implements it.

**Who sees what.** `cmd/asyncao` (its `build` function) is the only importer of `platform` and `host` besides the
App layer. It hands each consumer the services and hardware it uses through its constructor
(rule 12; no services bag). The App layer reads `Capabilities` to decide what to show: no
"Browse…" without a file picker, no "Restart to apply" without a relauncher. Features never
branch on the OS.

**Hard rule 1** names `internal/platform/host` beside `render`, `ui` and `cmd/asyncao`: it
imports the SDL binding and runs on the main thread. `internal/platform` itself stays SDL-free.

**Enforcement.** golangci-lint `forbidigo` rejects `runtime.GOOS` outside `internal/platform`
and the binding packages, and `depguard` rejects `os/exec` and `syscall` outside them. OS
build-tagged files exist only there (review rule; a lint cannot see file names).

**Graphics decisions carried over** (replaces the 03324c58 design). Vulkan is no longer
required everywhere (decided 2026-10-28): RmlUi's shaders ship as SPIR-V, DXIL and MSL, and
the Direct3D 11 and OpenGL ES tiers have no SDL_GPU device (open: RmlUi on SDL's renderer
there, or a device-level floor such as Android 10).

**Sequence (replaces step 2c's "platform code" item).** (1) `platform` skeleton: Environment,
Capabilities, Detect, report; (2) the host: init, window, frame loop, graphics acquisition
on `GraphicsAPIs()` and GPU loss, bootstrap in `main`; (3) the audio device and displays move
into the host; (4) the services, one commit each, deleting the OS branches they replace: URL
and reveal, dialogs, notifications, clipboard, fonts, proxy, machine id, presence, process
runner and ffmpeg, updater and relaunch, paths; (5) turn on the lints. Android and iOS
implementations, and main callbacks, come with the mobile milestone.

### Review outcome (2026-10-30; replaces the text above where they differ)

The adversarial review of 2026-10-28 (findings F1–F11) changed this design. Comparison page:
https://claude.ai/artifact/LmDrwvSC42cuwLLagFpLkV.

**Decided by the maintainer:** the platform layer owns initialisation from process setup
through SDL to the GPU; it is the app's begin and end point; the boundary is option C (above).
Decided 2026-10-30:
- **No browser build.** Nothing is designed around WebAssembly.
- **No memory budget.** The fixed limit (`debug.SetMemoryLimit`, 700 MiB today, 256 MiB in older
  text) and the process-wide budget are dropped as goals, and the host sets no limit. Every
  budget check in this plan (Part 5d, open items 1 and 6) is void; those measurements remain
  information. Hard rule 4 and the zero-allocation render loop stay. One commit removes the
  limit, the budget line in `ui/perfhud.go`, the "fits the budget" limits and text in `config`
  and the Settings power tab, and the wording in README and docs.
- **RmlUi stays, unmodified, on SDL_GPU; no OpenGL.** Windows: Direct3D 12. Linux and Android:
  Vulkan. macOS and iOS: Metal. Linux's software tier is lavapipe; Windows' (lavapipe or WARP) is
  chosen when the render interface runs on both. OpenGL, OpenGL ES and SDL's Direct3D 11 renderer
  are not tiers, and SDL's renderer stops being a fallback when RmlUi lands.
- **Our own render interface, with the least code that does the job (settles open item 3).** It
  lives in `internal/rmlui`. We use no third-party fork of RmlUi or of its renderers. It starts
  from upstream's SDL_GPU sample and ports from upstream's OpenGL 3 renderer: clip masks,
  gradients, layers, blur and drop shadow (and so `box-shadow`), colour filters and opacity,
  `mask-image` and `backdrop-filter`. Nothing beyond what those need. Shaders have one HLSL
  source, compiled ahead of time to SPIR-V, DXIL and MSL and committed, so a build needs no
  shader tools. It is written before the rest of step 4, and an effect counts as done only after
  it has run on a real GPU (RTX 3070) through Direct3D 12 and Vulkan; Metal needs a Mac.

**Adopted as the working plan, the maintainer may overrule any of them:**
- **Frame loop (F1).** Loop shape S4 now, S3 as the target. S4: `host.Main(build)` does pre-init,
  SDL, hardware, report, then `app.Run(loop)`, then shutdown and relaunch; the loop body keeps
  its behaviour but calls host primitives (event wait, wake event, waits, `Present` through the
  GPU watcher). S3: `Application` is events, `Plan`, `Draw`, `Suspend`, `Resume`, `Shutdown`, and
  the loop body lives in the host. A 30-method `Application` shaped around today's App is
  rejected: the platform would know about features.
- **Causes first (F1 follow-up).** Of the 34 App methods the loop calls, most are patches and are
  deleted, not moved. Patches are removed only after their cause is fixed: sessions on their
  own goroutines (step 3), blips on the audio clock, one `FrameScheduler` replacing the 14
  pacing methods and the classic loop mode (step 4), the preview as an ordinary window.
- **`FrameScheduler`** lives in `internal/platform`: pure Go, clock and waker injected, the user's
  frame caps passed as options by `build`, `Invalidate` and `RequestFrameAt` atomic and callable
  from any goroutine. Producers declare a two-method interface themselves and import nothing
  from the platform. Its acceptance tests are the bug reports in today's comments: no GPU burn
  when idle, instant input, one follow-up frame after a click, true animation speed at any frame
  rate with no catch-up burst, even blips at any cap, connections alive while minimised, the
  user's caps respected.
- **Hardware (F2).** The host offers a window factory (every window gets the same driver order
  and the same GPU watcher) and audio devices (playback, capture, enumeration; capture is
  consent-aware). `render/previewwindow.go`, `ui/voiceaudio.go`, `ui/voicemictest.go` and
  `render/audio.go` stop opening devices themselves.
- **GPU reset (F3).** "Nothing outside the host knows a device can be lost" holds only for the
  desktop restart. In-place re-acquisition adds a `GraphicsReset` event on `Application`.
- **Capability and availability (F5).** A capability is static, decided at detection, with a
  reason when absent. Availability is the service call's result at the time, as a typed error
  (not installed, denied, unavailable). The UI hides by capability and handles availability
  errors. "Present exactly when its service is" no longer applies.
- **Driver lists (F6)** are per-OS files in the host; `GraphicsAPIs()` descriptors are dropped.
- **`Runtime` (F7)** never leaves `build`. `ui.Deps` grows until step 7 moves services to their
  features; this is known debt.
- **Globals (F8).** The wake event moves into the host. `netproxy`'s policy stays global, as
  decision C3 already says; only its OS discovery moves.
- **Testing and paths (F9, F10).** `Detect(probe)` takes an injected OS probe. Config and data
  locations keep today's paths for existing installs, with an explicit precedence, a portable
  marker and a test pinning them.

**Sequence (F11; every commit builds and passes the gate).**
1. Restore the working tree to `03324c58`, keeping this plan.
2. Platform skeleton: `Detect(probe)`, capabilities, report. No consumers yet.
3. `host.Main(build)` owns pre-init, SDL, hardware and shutdown; the loop runs on host primitives.
4. Window factory and audio devices in the host.
5. Driver lists and the GPU watcher in the host; backend name in the debug overlay and About.
6. The services, one commit each, deleting the OS branches they replace.
7. Lints on. This closes step 2c.
8. Step 3, with blips on the audio clock.
9. Steps 4–5, rewritten per open item 7, with `FrameScheduler`, one loop mode, the preview as a
   window, and the loop body moved into the host (S3).
10. Steps 6–9. Android and iOS follow as their own milestone.

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
