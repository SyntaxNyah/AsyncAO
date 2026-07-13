# ROADMAP — requested features

Playtest-driven backlog (Skrapegropen / Discord). Newest requests at the top of
each section. This is the single place every ask is captured so nothing is lost;
items move to `docs/FEATURES.md` as they ship.

**Standing constraints (every item):**
- **Zero performance degradation** — nothing added may cost the live render loop;
  `BenchmarkRenderFrame` must stay at **0 allocs/op**. New work lives off the hot
  path (settings, popups, overlays, off-thread I/O).
- Local commits only (never pushed); `go test -race -p 1 ./...` green before each
  commit; document every shipped item in `docs/FEATURES.md`.

---

## Planned

- **Custom screen effects — AO2 `effects.ini` system (v1.55.7 follow-up).** The
  inline codes `\s`/`\f`/`\n`/`\p` and a dedicated "Enable screen effects" toggle
  **shipped in v1.55.7**; the remaining half is AO2's named custom-effect system so
  people can make their own beyond the built-in shake/flash. Plan (mirrors
  AO2-Client `AOApplication::get_effect` + `effects.ini`):
  - **Assets:** add `AssetTypeEffect` (`internal/assets/types.go` + `typeNames`) and
    `URLBuilder.Effect(name)` → `effects/<name>` (webAO/AO2 convention); stream the
    overlay sprite like the shout bubble (one probe, `PrefetchWithFallback`).
  - **Manifest:** parse `effects.ini` (theme + char/misc) via `internal/theme/ini.go`
    (`ParseINI` / `SectionKeys`) into named effects with properties (sprite, sound,
    loop / sticky).
  - **Render:** `Scene.EffectBase` + a `drawFill`-style animated overlay in
    `internal/render/viewport.go` — **must join the NoteAnimating census and
    self-clear when the clip ends** (the recurring frame-pacing trap); reduce-motion
    + the ScreenEffects toggle gate it.
  - **Trigger + UI:** the 2.8 `EFFECTS` field is already parsed (`courtroom.go`
    `fireMessageEffects` — today it plays only the named effect's *sound*); hook it to
    play the `effects.ini` art, and add an effect picker to the IC bar (AO2 effect-
    dropdown parity) so custom effects are selectable and sendable.
  Zero hot-path cost (cached-texture overlay blits, free when idle). The inline-code
  half is done and revert-clean, so this lands cleanly as a follow-up (v1.55.8).
- **Screenshot annotator (#72)** — quick arrows/boxes/text on a captured
  screenshot before sharing. Deferred from the v1.50.0 batch (the studio +
  playtest-fix stream ate the session); the natural entry point is an
  "Annotate last screenshot" action in Extras + the Ctrl+Space palette, with
  the marks rendered through render.CaptureTarget and saved as
  `-annotated.png`. Next batch's lead item.
- **Crisp text at scale (#77)** — see the standing "Crisp
  resolution-independent UI text" track below, re-diagnosed 2026-07-03 for the
  v1.53.5 round (Tifera: the window is a blurry mess at >100%, and 100% is too
  small). Confirmed: it is NOT Windows DPI virtualization — the process is
  already per-monitor-v2 aware (`SDL_WINDOWS_DPI_AWARENESS` in main.go) and
  nearest/linear is already a Settings pref — the residual blur is OUR OWN
  `ren.SetScale` linearly stretching text that was rasterized at 96 dpi.
  **PART A LANDED (the blur fix):** the global UI scale now folds into font
  POINT size — chrome (`c.font`/`c.fontBig` → device siblings `fontDev`/
  `fontBigDev`), the chat/log sets (device siblings `chatSetDev`/`logSetDev`,
  built via `fontsForDev`), and the message raster (`render.Rasterize*` take a
  `devScale`, store it on `MessageRaster`, and `Draw` divides the device dst back
  to logical). `ren.SetScale` STAYS active (geometry + mouse unprojection
  unchanged), so glyphs rasterize at final device size and blit 1:1 — crisp at
  any scale. Measurement (`TextWidth`) stays LOGICAL; the round-half-up rule
  lives in `render.logicalFromDevice` == `ui.uiLogicalFromDevice`. Exports and
  the pinned-tab pass are handled (exports BRACKET `textDevPct` to 100 for native
  resolution; the split pass inherits ambient and composes at 1:1). Sprites /
  backgrounds / viewport art are untouched (they keep GPU linear scaling — they
  are photographic, not vector text). **Known Part-A follow-ups (deferred, not
  blocking):** the ANIMATED text path (`AnimatedText`/`GlyphCache`, `msAnim`)
  stays on the logical face — an effects message is correctly-sized but still soft
  at >100% (clean seam: `msAnim` XOR `msRaster`, one per message). **PART B LANDED
  (DPI seeding):** a HiDPI monitor's *default* physical size is now correct without
  the user finding the slider. The auto-scale path already combined a DPI component
  (`dpiScalePct`) with the window-size factor; the gap was that `sdl.GetDisplayDPI`
  reports a flat 96 under per-monitor-v2 awareness, so detection stayed at 100. The
  fix makes the DPI *input* reliable: `App.SeedDisplayDPIScale` queries Windows
  `user32!GetDpiForWindow` for the window's monitor (via the SDL `SysWMInfo` HWND;
  plain Win32 syscall through `syscall.NewLazyDLL`, no new dependency), falling back
  to `sdl.GetDisplayDPI` off Windows (`internal/ui/dpiseed_{windows,other}.go`).
  The pure `config.DPIScalePercent` (96 dpi → 100%, 144 → 150%, round-half-up,
  floored at `MinAutoUIScalePercent` = 100) is the seam; the boot query replaces the
  old `main.go` block and a `WINDOWEVENT_DISPLAY_CHANGED`/`MOVED` handler re-seeds
  when the window changes monitor (gated on `lastDPIDisplayIndex` so a same-monitor
  move is free). **The seed is RUNTIME-ONLY** — it never writes the UI-scale pref, so
  an explicitly saved scale always wins (the manual slider requires `UIScaleAuto`
  off, and `UIScale()` then ignores the detected value; `UIScaleAuto` *is* the
  "user chose it" marker — no new pref). The never-below-100 floor (#6) is kept.
  100% still means 96dpi-logical (slider semantics unchanged). **Consequence (by
  design, not a bug):** turning Auto OFF on a HiDPI monitor snaps to the manual
  `uiScalePct` default (100%), because copying the seed into the saved scale would
  persist it as a user choice — the decision forbids that. **Issue #77 is now
  closable pending live verification** on a real scaled monitor (this dev box is at
  100%; a fresh profile there must still start at 100%). Interim answer for players
  on pre-Part-A builds: 200% + Smooth OFF is pixel-exact; non-integer scales stay
  soft until Part A ships.

_Playtest backlog cleared (2026-06-21) — every Discord/playtest request shipped
(see `docs/FEATURES.md`). New asks land here. The only milestone left is the
gamepad track below._

- ~~**More power-user knobs — the menu**~~ — **all shipped in v1.40.0** (404 TTL,
  deadline multiple, downscale override, texture budget, crossfade — see FEATURES),
  along with the **adaptive frame pacing** that fixed the idle GPU burn. Both
  follow-ups from that conversation **shipped in v1.55.0**: the **wake-on-input /
  event-driven loop** (`SDL_WaitEventTimeout` instead of poll+sleep, so idle input
  latency is ~0) and **skip-when-nothing-changed rendering** (a static screen skips
  render+present entirely — `SkipFrame` — so idle=off is genuinely zero GPU, not
  just paced), together with a real frame limiter (inviolable active/idle/background
  caps) and audio decoupled from the frame rate. A finer per-tile damage/dirty-rect
  pass (only re-composite the changed region of a *rendered* frame) remains the SDL3
  track below.
- ~~**Low-quality persistent sprite cache**~~ — **shipped in v1.40.0** (Settings →
  Power user → "Sprite thumbnail cache", default OFF; see FEATURES), including the
  byte-budget auto-prune (oldest-first past the cap).
- ~~**Cold-load per-stage profiling**~~ — **shipped in v1.40.0**: the debug overlay
  carries a `cold-load · fetch · decode · upload` EWMA line (F8 / Settings → Power
  user → Diagnostics). Original note kept for context: add per-stage timing
  (fetch TTFB/transfer · decode+CatmullRom-downscale · upload) to the metrics
  cold-load report so the bottleneck is measured, not asserted. Confirmed by hand:
  the dominant cost for an uncached sprite is **network transfer + latency**, but the
  **CatmullRom downscale of huge (2000²) sprites** runs in the decode pool and is a
  real secondary cost (the old "blurry huge WebP" was this path pre-fix). Hold-
  previous is **bottleneck-agnostic** — it covers the gap whatever the cause — so it
  was the right first move regardless.
- ~~**Config presets**~~ — **shipped in v1.40.0** (Settings → Data → "Setting
  presets": named full-settings bundles, apply-on-restart via the import path).
  Original ask (Nightingale, 2026-06-29) — the settings file is
  comprehensive (~130 KB once everything's learned), which is great for power
  users but heavy if you just want a couple of named "profiles" to switch
  between. Idea: a small, separate preset layer — pick/save a handful of named
  setting bundles — on top of the existing one-JSON store, so the full file
  stays the source of truth and presets are an opt-in convenience. Tracked
  separately from the v1.19.0 portable-config work (which moved the file beside
  the exe and is shipped). No transmitted/wire impact; off the hot path.

---

## Already shipped (rebuild to get them)

These were requested again but are already in the client — if they're missing,
it's a stale build (`scripts\build.ps1 -Release`).

- **Esc leaves the server through the confirm prompt** (Nightingale, 2026-07-01) —
  pressing **Esc** on the courtroom or char-select screen routes through the
  Disconnect confirm (unless "Instant disconnect" is on), so an accidental tap can't
  boot you. Fixed **2026-06-29** (`725f9a2` + `97f127c`, in HEAD/v1.33.5). If Esc
  still disconnects instantly, it's an **older build** (older paths called
  `Disconnect()` directly). v1.40.0 adds a "Don't ask again" tick to that prompt.
- **"Show volume sliders" (Vol strip) persists** — the log-panel **Vol** toggle
  survives restarts (`133c9ff`, in HEAD). v1.40.0 also persists the **Music menu's**
  volume-sliders view (that one really was session-only).
- **Callword/alert volume separate from SFX** — `AlertVolume` is its own slider,
  independent of SFX volume (Settings → Audio).
- **Add-to-friends from the player popup** — double-click a player → the popup has
  a friend toggle (+ the per-row "+ Friend" button).

---

## In flight / larger (separate tracks)

- **Voice chat — Nyathena-gated** *(#17, requested for v1.2)*. Server-relayed over
  the existing WebSocket — **not** P2P/WebRTC (confirmed: `../LemmyAO/src/voice/
  voice.ts` "There is no WebRTC"), so peer IPs never leak. Wire (canonical, from the
  Nyathena/LemmyAO `aolib` `VS_*` packets): `VS_CAPS` (caps advert) · `VS_PEERS`
  (uid list) · `VS_JOIN`/`VS_LEAVE` · `VS_SPEAK` (speaking toggle) · `VS_FRAME`
  (c2s opus) · `VS_AUDIO` (s2c opus). 48 kHz mono, 20 ms frames.

  **Shipped in v1.19.0:**
  - **Slice 1 — protocol + signaling** (`internal/courtroom/voice.go`): VS_* parse/
    build + per-session presence (caps, peers, speaking), all bounded; gated on
    `VS_CAPS` so non-Nyathena servers have a byte-identical wire. Unit-tested.
  - **Slice 2 — Opus codec** (`internal/voice`, libopus CGO, SDL-free): encode/decode
    round-trip + PLC, unit-tested. Opus is BSD (AGPL-compatible).
  - **Slice 3 — presence UI** (`internal/ui/voicepanel.go`): a Nyathena-gated
    floating panel (Extras → "Voice (Nyathena)", hidden elsewhere) — Join/Leave, the
    live peer list with speaking indicators, and your own speak toggle. Two AsyncAO
    clients can see each other in voice + who's talking.

  **Remaining — live mic audio (the next slice):** wire SDL2 audio (capture +
  playback, queue API — `internal/render`, on the render thread per hard rule #1) to
  the codec + signaling: PTT/open-mic capture → encode → `VS_FRAME`; `VS_AUDIO` →
  decode → **mix N peers** (bounded per-peer buffers) → output, with per-peer
  volume/mute. Frames funnel through the session loop (single send path; never the
  audio thread). Fail-safe init (any device/codec error → voice silently disabled,
  never fatal) and opt-in (default off) so the audio path is unreachable for general
  users. **Blocked on** the user's Nyathena server + a mic to validate; advisor-check
  before committing the audio engine. Build surface when it lands: libopus in every
  CI build (release.yml + flatpak) + `build.ps1` DLL staging (auto via the ldd
  closure) — `ci.yml` already has `libopus-dev`.

- **M16 Scene studio** — recording, replay player, scene maker, GIF + animated
  WebP export, crop/trim, per-line effects, **proportional timeline strip with
  draggable In/Out handles _and drag-to-reorder_** (#75 + follow-up, shipped),
  and **Instant Replay** — an opt-in rolling buffer that clips the last window
  (10 s … 1 h) of conversation with no recording started in advance (shipped) —
  all in `docs/FEATURES.md`. Possible later tweak: continuous-playback scrubbing
  on the timeline strip.
- ~~**Shareable scene/server deep-link** *(#52)*~~ — **closed** (2026-06-21, by
  request): the gif/WebP export half shipped; the deep-link half is covered by
  the existing **Direct Connect** field (paste a `ws://`/`wss://` URL in the phone
  book) and the `--server` launch flag, so no bespoke link or `asyncao://` scheme
  was built.
- ~~**M8 Gamepad support** *(#44)*~~ — **dropped** (2026-06-21, by request — no
  need for it). The whole milestone backlog is now closed.

## Future / larger tracks (not scheduled)

- **SDL3 migration — real GPU/shader pipeline.** The post-processing FX (vignette,
  scanlines, grain, chroma / glitch, depth-of-field) are currently a
  cached-texture multi-blit *approximation* because SDL2's renderer has no shader
  stage and no per-texture scale-mode control. SDL3's GPU API (render passes +
  shaders) would make those real, cheaper, and composable — but it's a large,
  cross-cutting port (every `internal/render` call site, the texture tiers, the
  SDL_mixer audio back-end) and stays parked until the FX / perf win clearly
  justifies the churn.
- **Crisp resolution-independent UI text (#77).** _Status: Part A (blur) + Part B
  (DPI seeding) both LANDED — see the tracked #77 entry near the top of this file;
  only the ANIMATED-text follow-up remains. This is the original design sketch, kept
  for context._ The global UI scale WAS applied
  with `ren.SetScale`, which bitmap-upscaled already-rasterized text — correct
  size but soft above 100% (see `SetAutoScaleFromWindow` and the v1.2.0 #6 fix).
  Re-scoped 2026-07-03 (the v1.53.5 DPI dig): the proper fix rasterizes text at
  the *native device* pixel size — fonts opened at `pt × scale`, the resulting
  texture drawn into the unchanged *logical* rect, so with the renderer scale
  active the glyph pixels land 1:1 on device pixels and any scale stays sharp.
  The kit's coordinate system doesn't change; what does is every place that
  assumes texture px == logical px:
  - `TextWidth` / measurement must return LOGICAL units (device ÷ scale), and
    the width caches + text atlas need the scale in their keys (a scale change
    is a cache generation bump, like `fontChainGen`).
  - `blitLabel` / `LabelClipped*` dst rects become `tex.size ÷ scale` with
    rounding audited (off-by-one at odd scales is the classic failure).
  - The message raster + typewriter reveal indexes (per-rune positions) and the
    emoji fallback raster measure with the scaled faces; GIF/comic export paths
    pick their own scale explicitly (they render offscreen at 100%).
  - The IC/OOC log wrap caches key on font scale already — they just need the
    same generation bump on a UI-scale change.
  All of it **without** regressing the 0-alloc render gate (`BenchmarkRenderFrame`),
  which is why it's its own milestone and was explicitly not rushed into the
  v1.50.0 or v1.53.5 batches. The window-relative auto-scale landed the *sizing*;
  this lands the *sharpness*.
