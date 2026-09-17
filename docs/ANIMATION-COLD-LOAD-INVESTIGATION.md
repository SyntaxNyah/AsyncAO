# Animation Cold-Load Stutter — Investigation

> **Status:** Phase 1a (AVIF `maxThreads`) and Phase 2 (streaming/incremental
> decode) are shipped, plus a **Phase 2 crash fix** (redundant re-decode no longer
> shrinks a streaming page under the viewport's playback cursor). Phase 1b
> (warm-on-join) and the larger benchmark fixture remain open.
> **Date:** 2026-09-17
> **Relates to:** #110 (animation fps / choppy-animation fix), #100 (predictive
> prefetch), #17 (networked frame effects), #72 (mounted content packs),
> #119 (evidence system).
> **Commits in play:** `ef34522` (animation swap-stutter fix), `7fe8aec`
> (DXT/BC texture compression + animated memory tuning), `ea0b5b1` (WebP ANMF
> frame delays + animation diagnostics), `7bb6067` (larger animated frame
> budget), `8d993ea` (loop-aware decimation), `10a4e51` (animation fps).

## 1. The report

> "first load of the animation files it struggles with, but once it caches(?)
> them it's able to show them no issue." — Crystalwarrior

The symptom is a **cold-load** problem, not a playback bug: a character sprite
or preanimation that has not been seen yet this session is slow to appear / play,
while any sprite seen before is instant. "Caches" here means the **T1 texture
cache** (decoded-and-uploaded frames held by `render.TextureStore`), not the
byte caches (T2 RAM / T3 disk).

## 2. TL;DR

Animated assets (WebP / AVIF / APNG / GIF) decode **every authored frame** into
RGBA on first appearance. A progressive first-frame delivery already exists and
gets frame 0 on screen quickly, but **playback is held until the entire clip is
decoded and uploaded** — so a brand-new sprite shows a static first frame and
"wakes up" only when the full decode lands. The three levers that already soften
this (progressive frame 0, the Markov prefetcher, and T3 disk) all leave the
**first appearance** cold, and full decodes are serialized through `animGate`
(default concurrency 2).

**Phase 2 shipped a fix for this:** definitely-animated WebP/AVIF now stream
frame-by-frame (an establishing frame-0 chunk, then one append per remaining
frame), so a cold sprite *plays from its decoded prefix* instead of holding
frame 0. GIF/APNG keep the classic progressive+full path.

## 3. The cold-load pipeline, end to end

A sprite demand originates in the courtroom scene layer and flows through the
`internal/assets` manager, the decode pool, and the render pump:

1. **Demand.** The scene layer calls `Manager.Prefetch` /
   `PrefetchWithFallback` / `PrefetchChain` (`internal/assets/manager.go:576`,
   `:589`, `:605`). AO sprite naming needs the prefixed → bare spelling chain
   (`(a)<emote>` / `(b)<emote>` / bare `<emote>`). The Markov prefetcher calls
   `PrefetchChainSpeculative` (`manager.go:617`) for *predicted* (not yet
   demanded) assets at `PriorityLow`.
2. **Resolve.** `resolveChain` (`manager.go:1130`) walks the tier ladder for
   each spelling:
   - T1 short-circuit (`manager.go:1144`) — if `TextureStore` already holds the
     texture under `base`, it returns immediately (zero work). This is the
     entire reason re-showing is instant.
   - mount layer (`manager.go:1161`, `:1172`) — user local packs.
   - `tryBase` (`manager.go:1164` onward) probes **T2** (in-RAM bytes,
     `manager.go:1284`), **T3** (disk bytes, `manager.go:1282`), then the
     **network** (`manager.go:1293`).
3. **Decode.** Fetched bytes route through `deliver` (`manager.go:1323`), which
   submits a `DecodeRequest` to the decode pool (`manager.go:1329`).
   `DecoderPool.runJob` (`internal/assets/decoder.go:721`) does two things:
   - **Progressive first frame** (`decoder.go:723–737`): if the payload sniffs
     animated (`sniffMaybeAnimated`, `decoder.go:766`), it decodes only frame 0
     (`DecodeImage(data, false)`), marks it `Partial`, and delivers it — the
     sprite appears after *one* frame-decode.
   - **Full decode** (`decoder.go:748–760`): `DecodeImageSized(data, true,
     spriteCap)` materializes **all** frames, then `fit` (downscale) and
     `compressIfEnabled` (DXT) run before `OnDone`. The full decode is gated by
     `animGate` (`decoder.go:743–746`).
4. **Upload.** The decoded `DecodedAsset` lands on `decodedCh`; `Pump.Frame`
   (`internal/render/pump.go:56`) drains it each frame within a byte/texture
   budget and uploads to GPU textures (`pump.go:141–145`). The `Partial` page is
   replaced by the full set, with an ordering guard so a deferred partial can
   never overwrite its own full set (`pump.go:125–130`).
5. **Playback.** `viewport.go` advances frames by precomputed delays as pure
   texture-pointer swaps (no per-frame decode — spec §12).

### 3.1 The key shapes

- `assets.Decoded` (`internal/assets/types.go:88`) is the decode output: parallel
  `Frames []*image.RGBA` and `Delays []time.Duration`, plus `Animated`,
  `SourceFrames`, `Partial`, `Width/Height`, and optional `Compressed` (DXT).
- `Partial` (`types.go:106`) marks the progressive frame-0 delivery; the full set
  for the same URL follows from the same job and replaces it on upload.
- `animGate` (`decoder.go:432`) is a live-adjustable semaphore;
  `defaultAnimatedDecodeConcurrency = 2` (`decoder.go:436`) bounds concurrent
  FULL decodes because the per-frame RGBA is the memory spike.

## 4. Where the cold-load time goes

Four sequential costs, ordered by magnitude for a large preanimation:

| # | Stage | Where | Notes |
|---|-------|-------|-------|
| 1 | Network fetch | `manager.go:1293` (`netFetch`) | Multi-MB compressed file. Mitigated **across sessions** by T3 disk; still paid on first-ever load. |
| 2 | Full decode | `decoder.go:749` → `DecodeImageSized` → `webp_cgo.go` / `avif_cgo.go` / `decodeAPNG` / `decodeGIF` | **The dominant cost.** Every frame is materialized eagerly. WebP lossless runs `use_threads=0` (`webp_cgo.go:93`); AVIF runs `maxThreads=1` (`avif_cgo.go:42`); APNG/GIF are composed frame-by-frame in Go. |
| 3 | Per-frame downscale | `downscaleFrame` (`decoder.go:228`, box filter) during decode; `downscaleDecodedAspect` (`decoder.go:140`, CatmullRom) for stills only | Animated decoders downscale during decode with the fast box filter (`decodeTargetDimsBudgeted`, `decoder.go:211`). Verified: no double-downscale in the common path (`fit`, `decoder.go:637`, is a no-op when `d.Height <= cap`). |
| 4 | GPU upload | `Pump.Frame` (`pump.go:56`) | Bounded (`speculativeUploadMaxBytes = 4<<20`, `speculativeUploadMaxTextures = 16`, `pump.go:34–35`); live-message assets bypass the budget (`pump.go:61`). |

The decode stage already carries instrumentation: `[anim-decode]` logs
`format playAnim maxH → WxH frames source animated budget took` on every animated
decode (`decoder.go:814–825`), `[anim-upload]` logs `animated frames bytes`
(`pump.go:132–136`), and `AvgDecode` (decode+fit EWMA, `decoder.go:758`) is
exposed alongside the network TTFB EWMA in the F8 debug overlay.

## 5. Why "first load" specifically

- **T1 residency is the "cache".** Once `TextureStore` holds the decoded frames,
  every later demand is a T1 short-circuit (`manager.go:1144`) and playback is a
  pointer swap. Every *new* sprite — a character's first appearance, a fresh
  preanim, an emote never seen this session — pays the full decode again.
- **Non-active animations are still-framed (v1.97.0).** `TextureStore` keeps only
  the active speaker's animation fully resident; the pair and every
  previously-shown character collapse to a single still frame
  (`ReduceAnimatedExcept`) and re-stream (~20 ms) the instant they become active.
  A long-absent character therefore re-pays the decode on re-entry, but the
  steady-state resident animated tier is bounded to ~one active animation plus
  stills — the memory-side complement to the cold-load latency traced here.
- **The progressive path can't start playback.** It shows frame 0 fast
  (`decoder.go:723–737`) but the animation is a static frame until the full set
  lands and uploads. (`ef34522` fixed the bug where that static frame *froze* a
  swap; the "holds a static frame until the full decode lands" behavior itself
  remained until Phase 2 — streaming WebP/AVIF now plays from the decoded prefix,
  while GIF/APNG keep the static-until-full behavior.)
- **The prefetcher needs history.** The Markov prefetcher
  (`internal/assets/prefetcher.go:41`) *does* decode ahead —
  `PrefetchChainSpeculative` still walks `resolveChain → deliver → decoder.Submit`,
  so it warms bytes AND decode — but it only predicts the next speaker/emote
  *after* learning transitions (`predictLocked`, `prefetcher.go:249`). A
  character's **first** appearance is never predicted, and the speculation runs
  `PriorityLow` with the upload budget applied.
- **`animGate` serializes bursts.** With concurrency 2, a burst of new animations
  (a pair swapping emotes on first meeting) queues through the gate, so the Nth
  sprite's full decode waits for the first two.

## 6. Existing mitigations inventory

Every mechanism already in place, so a future fix builds on (not against) them:

| Mechanism | What it does | Where |
|-----------|--------------|-------|
| Progressive frame 0 | Decodes only frame 0 first, delivers it `Partial`, so a sprite appears after one frame-decode instead of N | `decoder.go:723–737` |
| Markov prefetch | Predicts the next speaker (and their next emote) and warms bytes **and decode** ahead at low priority | `prefetcher.go:41`, `manager.go:617` |
| T3 disk cache | Persists compressed bytes across sessions, so the network fetch is skipped on relaunch | `manager.go:1282` |
| `#110` downscale-to-fit | Downscales an over-budget clip *during* decode so every authored frame fits `maxAnimatedDecodedAssetBytes` (default 128 MiB) rather than dropping frames | `decoder.go:211`, `decoder.go:52` |
| Sprite caps | Display-height ceiling (`spriteCap`) + optional animated-only cap (`animatedSpriteCap`) | `decoder.go:637`, `:589` |
| DXT texture compression | BC1/BC3 GPU storage to cut VRAM (default DXT5) | `decoder.go:623`, `dxt.go` |
| `animGate` | Bounds concurrent full decodes to 2 (memory safety) | `decoder.go:432–479` |
| ThumbCache | Opt-in ~1 KB low-quality stand-in for a cold sprite | `thumbcache.go:19` |
| `ef34522` swap fix | A progressive partial no longer freezes a swap / latches a playOnce preanim | `viewport.go`, `textures.go` |

Animations are ON by default: `defaultPreferAnimated = true`
(`internal/config/preferences.go:69`), gated by `AnimationsEnabled()`
(`preferences.go:3452`). The animated memory knobs live in Settings → Power user
(`internal/ui/settings.go:4342` is the animated-sprite-memory slider).

## 7. Root-cause gaps

The concrete reasons a first appearance still stutters, with severity:

| # | Gap | Where | Severity |
|---|-----|-------|----------|
| 1 | AVIF animations decode single-threaded (`maxThreads=1`); dav1d is heavily threaded and would scale | `avif_cgo.go:42` | High (easy, safe win) |
| 2 | WebP lossless (VP8L) is inherently sequential; only lossy VP8 would benefit from `use_threads` — so this is *not* a quick win for the common case | `webp_cgo.go:93` | Informational |
| 3 | Playback cannot start until the **entire** frame set is decoded + uploaded (frame 0 holds static meanwhile) | `decoder.go:721–760`, `pump.go:56` | High (the real "stutter") — **RESOLVED by Phase 2 streaming** |
| 4 | A character's first appearance is never predicted/warmed; speculative upload is byte-capped | `prefetcher.go:249`, `pump.go:34–35` | Medium |
| 5 | `animGate=2` queues bursts of new animations (memory-vs-latency tradeoff) | `decoder.go:436` | Medium |

## 8. Fix options + tradeoffs

1. **Enable AVIF multithreading** (`maxThreads = NumCPU` in `avif_cgo.go:42`).
   ~one line, low risk, immediate win for AVIF packs. No help for WebP / APNG /
   GIF. The decode pool already provides cross-*job* parallelism; this adds
   intra-*animation* parallelism.
2. **True streaming / incremental decode.** Instead of "frame 0 → then all of
   them", deliver frames as they decode, so the animation *plays from a decoded
   prefix* while the rest finishes. This is the complete fix for gap #3 but the
   most complex: `Decoded` delivery must support frame-appends, and the render
   side must grow a resident frame set without stutter. **SHIPPED (Phase 2)** —
   `streamAnimated` + `decodeWebPAnimStream`/`decodeAVIFAnimStream` +
   `AppendStream`, with encapsulation tests in
   `internal/render/streaming_test.go`.
3. **Warm earlier + wider.** Warm the pair partner and the current speaker's
   idle/talk sprite at connect/join (not only after Markov learning), and/or
   raise the speculative upload budget. Low risk, complements the others, does
   not fix the very first sprite.
4. **Persist a higher-quality first frame.** Extend `ThumbCache`
   (`thumbcache.go`) — today a ~1 KB opt-in stand-in — so a cold sprite's
   stand-in is recognizable before even frame 0 decodes.

## 9. Recommended plan (measure first)

The code already instruments the decode stage, so the first step is to confirm
which stage dominates rather than guess:

- **Phase 0 — measure (no behavior change).** Add animated decode benchmarks
  mirroring `BenchmarkDecodeWebP_256x192` (`webp_cgo_test.go:133`):
  `BenchmarkDecodeWebPAnim`, `BenchmarkDecodeAVIFAnim`, plus APNG and GIF, and a
  small harness timing "first-frame → full-set" for a representative 60–150
  frame preanim. Read the `[anim-decode]` / `[anim-upload]` log lines and the
  `AvgDecode` EWMA on a real server to pin down the dominant cost.
- **Phase 1 — low-risk wins.** (a) AVIF `maxThreads = NumCPU` (gap #1);
  (b) warm pair-partner + current-speaker sprites on join (gap #4).
- **Phase 2 — the real fix (DONE).** Streaming/incremental decode is shipped:
  definitely-animated WebP/AVIF now streams frame-by-frame — an establishing
  frame-0 chunk, one append per remaining frame, then a finalize — so the
  resident page GROWS and a cold sprite starts playing from its decoded prefix
  instead of holding frame 0 until the whole clip decodes (gap #3). GIF/APNG
  keep the classic progressive+full path (their `DecodeAll` decodes everything
  up front). The rare decimation-needed case falls back to the full decode so
  `spreadLoopDelays` stays correct.

## 10. How to reproduce & triage

```bash
# run the client with pprof / diagnostics
./asyncao -debug                 # pprof on localhost:6060, F8 overlay

# watch the per-stage numbers in the console log:
#   [anim-decode]  <format> playAnim=<bool> maxH=<px> -> WxH frames=<n> source=<n> animated=<bool> budget=<MiB> took=<dur>
#   [anim-upload]  <base> animated=<bool> frames=<n> bytes=<MiB>

# profile while a new sprite first loads
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30   # CPU
```

Reproduce the symptom: connect to a server, open a chat with a character whose
sprite you have not seen this session, and watch the time between the message and
the sprite *starting to move* (vs. the instant re-show on the next message).

## 11. Open questions / decisions needed

- **Per-format threading policy.** AVIF is a clear win; is the lossy-WebP
  `use_threads` worth enabling given most AO WebP is lossless (VP8L, sequential)?
- **`animGate` memory vs. latency.** Raising concurrency above 2 speeds bursts
  but re-opens the multi-GiB RSS spike it was added to cap (see
  `docs/PERFORMANCE.md`). Should the limit be adaptive (higher when sprites are
  downscaled-to-fit)?
- **Is streaming decode worth the seam churn?** RESOLVED — shipped. The
  `Decoded` contract grew a `Stream`+`FrameOffset` prefix-extension (the page
  grows in the oversized map, never the LRU), `pump.go` routes stream chunks to
  `AppendStream`, and `viewport.go` holds a playOnce layer on the prefix
  boundary. Encapsulation tests: `TestAppendStreamGrowsResidentPage`,
  `TestAppendStreamDropsOutOfOrder`, `TestStreamingPreanimHoldsOnPrefix`,
  `TestReportSpeakerFrameIdentityWhileStreaming`.
- **Warm-on-join scope.** How many characters/sprites to warm at connect without
  competing with demand decodes through `animGate`?

## 12. References

- Commits: `ef34522` (swap-stutter fix), `7fe8aec` (DXT + animated memory
  tuning), `ea0b5b1` (ANMF delays + diagnostics), `7bb6067` (larger frame
  budget), `8d993ea` (loop-aware decimation), `10a4e51` (animation fps).
- Files: `internal/assets/manager.go`, `decoder.go`, `types.go`, `prefetcher.go`,
  `thumbcache.go`, `webp_cgo.go`, `avif_cgo.go`; `internal/render/pump.go`,
  `textures.go`, `viewport.go`; `internal/config/preferences.go`.
- Docs: `docs/PERFORMANCE.md` (animated memory levers, PGO capture),
  `docs/PERFORMANCE-ROADMAP.md`, `docs/BENCHMARKS.md`, `docs/KNOWN-ISSUES.md`.
- Spec: §8 (decode pool / upload budget), §9 (T1 budget), §12 (animation frame
  advance), §15 (decode benchmarks), §17 (hard rules / encapsulation).



