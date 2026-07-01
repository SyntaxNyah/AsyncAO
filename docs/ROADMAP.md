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

_Playtest backlog cleared (2026-06-21) — every Discord/playtest request shipped
(see `docs/FEATURES.md`). New asks land here. The only milestone left is the
gamepad track below._

- **More power-user knobs — the menu** (2026-07-01, "add even more"): candidates for
  the next knob batch, each needing its own care because they live in the network /
  cache tiers: **404-TTL override** (how long a missing asset stays "missing"),
  **per-host deadline multiplier** (the TTFB-EWMA 8× cap), **texture-budget
  override** (T1's share of the 256 MiB), **decode-downscale override** (the
  automatic CatmullRom display-height target: off / 50–200 %) and a **speaker-swap
  crossfade** (render: alpha-blend previous→new over N ms — composes with
  hold-previous). The v1.40.0 batch shipped the renderer + courtroom-core set (three
  cold-load modes + wait strictness/timeout, hold max-age + tint, shout/preanim/
  queue/catch-up sliders, the nuke reset).
- **Low-quality persistent sprite cache** (Nightingale, 2026-07-01, **low priority**)
  — an opt-in, power-user-only secondary cache of heavily-compressed ~1 KB sprite
  thumbnails, kept across sessions. On an incoming message: show the tiny thumbnail
  instantly, then swap to the full-quality sprite when it streams in. Complements the
  hold-previous mode (covers the case with **no** previous sprite — first paint / a
  brand-new character). **Default OFF** — full quality is the promise and the client
  stays lightweight by default ("let them optimise it"). Reuse the CatmullRom decode
  path to bake a nearest-neighbour/low-q variant. "Playing around with optimisation",
  so not urgent.
- **Cold-load per-stage profiling** (Nightingale, 2026-07-01) — add per-stage timing
  (fetch TTFB/transfer · decode+CatmullRom-downscale · upload) to the metrics
  cold-load report so the bottleneck is measured, not asserted. Confirmed by hand:
  the dominant cost for an uncached sprite is **network transfer + latency**, but the
  **CatmullRom downscale of huge (2000²) sprites** runs in the decode pool and is a
  real secondary cost (the old "blurry huge WebP" was this path pre-fix). Hold-
  previous is **bottleneck-agnostic** — it covers the gap whatever the cause — so it
  was the right first move regardless.
- **Config presets** (Nightingale feedback, 2026-06-29) — the settings file is
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
- **Crisp resolution-independent UI text.** The global UI scale is applied with
  `ren.SetScale`, which bitmap-upscales already-rasterized text — correct size but
  slightly soft above 100% (see `SetAutoScaleFromWindow` and the v1.2.0 #6 fix).
  The proper fix rasterizes glyphs at the *target* pixel size (`pt × scale`) so any
  scale stays sharp, which means threading the scale through the label atlas /
  glyph cache and every text draw **without** regressing the 0-alloc render gate.
  Big enough to be its own track: the window-relative auto-scale lands the *sizing*
  now (v1.2.0); this lands the *sharpness* later.
