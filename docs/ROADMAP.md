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

---

## Already shipped (rebuild to get them)

These were requested again but are already in the client — if they're missing,
it's a stale build (`scripts\build.ps1 -Release`).

- **Callword/alert volume separate from SFX** — `AlertVolume` is its own slider,
  independent of SFX volume (Settings → Audio).
- **Add-to-friends from the player popup** — double-click a player → the popup has
  a friend toggle (+ the per-row "+ Friend" button).

---

## In flight / larger (separate tracks)
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
