# AsyncAO — FINAL IMPLEMENTATION PROMPT
## Maximum-Performance AO2 Client in Go: Zero-Fallback Asset System + Pairing

**Repo:** `github.com/SyntaxNyah/AsyncAO` (this repo — currently empty; everything lands here).
**Language:** Go ≥ 1.24, CGO enabled. **Git:** commit locally in milestone-sized commits; **never push, never open PRs.**

**Reference implementations on disk (read them, mirror them, do not vendor them):**
- `../AO2-Client` — canonical AO2 protocol & courtroom semantics. **If this prompt conflicts with AO2-Client on protocol behavior, AO2-Client wins.**
- `../webAO` — asset URL probing & path conventions over HTTP.
- `../AO-SDL`, `../ferris-ao-switch` — SDL2 rendering/thread model.

---

## 0. Mission

Build the fastest AO2 client physically possible. Two pillars:

1. **Zero fallbacks by default.** If the user prefers WebP, they get WebP or a visible warning — exactly **one probe per asset** until fallbacks are explicitly enabled. This cuts cold-load probing ~80% on typical servers and pushes content creators toward modern formats.
2. **Every millisecond counts.** Lock-free hot paths, learned formats, three-tier caching, predictive prefetch, zero-allocation render loop, GC tuned to a hard memory budget.

Plus one protocol feature beyond the old drafts: **full AO2 ≥ 2.6/2.8 pairing** (two characters on screen, offsets, flip, z-order), wired through the same asset pipeline at full speed.

---

## 1. Performance targets — hard gates, benchmarked in CI

| Metric | Target | Method |
|---|---|---|
| Cold icon load (char select, 40 icons) | < 50 ms | PNG-only probing, parallel fetch+decode |
| Cold sprite load (first character) | < 100 ms | Learned format + parallel streams |
| Steady-state sprite swap | < 5 ms | T1 texture cache hit |
| **Paired message, steady-state (both sprites)** | < 8 ms | Both sides resolved in parallel |
| **Paired message, cold (both sprites)** | ≈ single-sprite cold (±20%) | Parallel HIGH-priority fetch |
| Typewriter cadence | 18 ms/char, < 1 ms frame cost | Pre-rasterized texture, src-rect reveal |
| Room change (background reload) | < 200 ms | Prefetch while typing + learned format |
| Memory (steady-state, 200-char server) | < 256 MiB | `GOMEMLIMIT` + byte-budget caches + pools |
| GC pause (p99) | < 2 ms | Minimal allocs, soft memory limit |
| Network probes (cold load, zero-fallback) | ≤ 1 probe/asset, ≤ 450 total | Format policy + learned formats |
| Network requests (steady-state) | ≤ 2/message (≤ 4 first paired msg) | Prefetch hit rate > 95% |
| Resolver fast path | < 100 ns, ≤ 1 alloc/op | Atomic learned snapshot + pooled buffers |
| Frame budget | 16 ms (60 FPS), 0 allocs steady-state | Single render pass |

---

## 2. Technology stack — corrected; do **not** substitute the old draft picks

| Concern | Use | Why (and what the drafts got wrong) |
|---|---|---|
| Rendering/window/input | `github.com/veandco/go-sdl2` (`sdl`, `ttf`) | Mirrors ferris-ao-switch / AO-SDL |
| Audio | `go-sdl2/mix` (SDL2_mixer ≥ 2.6 built with opusfile) | Decodes Opus/OGG/MP3/WAV in C at native speed from memory. Draft picks were wrong: `faiface/beep` has **no Opus decoder**; pure-Go decode wastes CPU. Keep `jfreymuth/oggvorbis` + `hajimehoshi/go-mp3` only behind a `nocgo_audio` fallback build tag. |
| WebP (static + animated) | Thin CGO binding over **libwebp + libwebpdemux** (`WebPAnimDecoder` API, ~100 lines) | SIMD decode straight into RGBA. `kolesa-team/go-webp` is static-only. Pure-Go `golang.org/x/image/webp` (static) behind fallback tag. |
| APNG | `github.com/kettek/apng` | Draft listed `kylelemons/godebug` — that is a **diff library**, not APNG. |
| GIF (multi-frame) | stdlib `image/gif` (`DecodeAll`) | Already does multi-frame; no third-party dep needed. |
| PNG/JPEG | stdlib `image/png`, `image/jpeg` | Sufficient for icons/legacy. |
| HTTP | `net/http` (+ `golang.org/x/net/http2` only if h2c flag is implemented) | See §7 — `ForceAttemptHTTP2` does nothing on plain `http://`. |
| Dedup | `golang.org/x/sync/singleflight` | N concurrent identical fetches → 1 request. |
| LRU | `github.com/hashicorp/golang-lru/v2` + `/expirable` (404 cache) | Already thread-safe — **no wrapper mutex** (draft wrapped it in an RWMutex and even RLock'd a mutating `Get`). |
| Hashing (cache keys) | `github.com/cespare/xxhash/v2` | MD5 is slow and pointless for non-crypto keys. |
| Config | stdlib `encoding/json` | No TOML dep needed. |

No other dependencies without written justification in ARCHITECTURE.md. House style: **no magic numbers** — every constant named.

---

## 3. Repository layout

```
AsyncAO/
├── cmd/asyncao/main.go            // wiring, runtime.LockOSThread, GOMEMLIMIT, SDL init
├── internal/
│   ├── assets/
│   │   ├── types.go               // AssetType enum, Decoded, ResolvedAsset, errors
│   │   ├── resolver.go            // lock-free AssetResolutionEngine
│   │   ├── manager.go             // AssetManager: tiers → net → decode handoff
│   │   ├── decoder.go             // decode worker pool (NO SDL calls here)
│   │   ├── prefetcher.go          // predictive + pair-aware prefetch
│   │   └── *_test.go
│   ├── config/
│   │   ├── preferences.go         // AssetPreferences: debounced async save
│   │   ├── defaults.go            // default format orders + legacy chains
│   │   └── preferences_test.go
│   ├── cache/
│   │   ├── memory.go              // T1 texture-budget + T2 raw-bytes LRU (byte-budgeted)
│   │   ├── disk.go                // T3: async writer, tmp+rename, xxhash keys
│   │   └── cache_test.go
│   ├── network/
│   │   ├── client.go              // singleflight, 404 TTL cache, tuned transport, DNS pre-resolve
│   │   ├── pool.go                // priority worker pool, epoch cancellation
│   │   └── *_test.go
│   ├── protocol/
│   │   ├── ms.go                  // MS packet parse/build (all 2.6/2.8 fields)
│   │   ├── pairing.go             // pair field parse (^order, x&y offsets)
│   │   └── *_test.go
│   ├── courtroom/
│   │   ├── courtroom.go           // state machine, BeginMessage, pair state
│   │   ├── render.go              // zero-alloc render loop, texture uploads
│   │   └── typewriter.go          // pre-rasterized reveal
│   ├── ui/
│   │   ├── settings.go
│   │   ├── asset_prefs_panel.go   // format order, fallback toggles, cache buttons
│   │   └── pair_panel.go          // partner picker, offset sliders, flip
│   └── metrics/profiler.go
├── docs/                          // ARCHITECTURE.md, PERFORMANCE.md, BENCHMARKS.md,
│   ├── adr/0001-zero-fallback-by-default.md
│   └── user/{asset-preferences.md, pairing.md}
├── test/fixtures/                 // webp(anim+static), apng, gif, png, opus, ogg samples
└── default.pgo                    // PGO profile (see §14)
```

---

## 4. Asset model & zero-fallback policy

```go
type AssetType int

const (
    AssetTypeCharIcon AssetType = iota
    AssetTypeCharSprite
    AssetTypeBackground
    AssetTypeDeskOverlay
    AssetTypeShoutBubble
    AssetTypeMisc
    AssetTypeSFX
    AssetTypeMusic
    AssetTypeBlip
    AssetTypeCount // sentinel for fixed-size tables
)
```

**Default formats (fallbacks OFF — this is the entire probe list):**

| Asset type | Default probe | Legacy chain appended when fallbacks ON |
|---|---|---|
| CharIcon | `.png` | `.webp` |
| CharSprite / Background / DeskOverlay / ShoutBubble / Misc | `.webp` | `.apng → .gif → .png` |
| SFX / Blip | `.opus` | `.ogg → .wav → .mp3` |
| Music | `.opus` | `.ogg → .mp3` |

**CRITICAL CORRECTION — `.webp.animated` is abolished.** It was a fictional extension from earlier drafts; AO servers ship plain `.webp` whether animated or not. Animation is a property of the payload: detect the VP8X `ANIM` flag at decode time. This halves sprite probes vs the draft (1 instead of 2). `PreferAnimated` is redefined as a decode/render toggle: ON = play animation frames; OFF = render first frame only (low-end/accessibility), never an extra network probe.

**`FormatList(type)` semantics (resolves the drafts' self-contradiction):** fallbacks OFF → return exactly the configured list for that type (defaults above are length 1 → 1 probe). Fallbacks ON (global OR per-type) → configured list + legacy chain, deduplicated, order preserved. If every candidate 404s, surface a visible in-client warning naming the asset and the formats tried ("enable fallbacks or ask the server to ship .webp").

---

## 5. Preferences (`internal/config`) — no deadlocks, no per-asset disk writes

Persisted JSON at `filepath.Join(os.UserConfigDir(), "AsyncAO", "asset_preferences.json")`:

```go
type AssetPreferences struct {
    GlobalFallbacksEnabled bool                      `json:"globalFallbacksEnabled"` // default false
    PreferAnimated         bool                      `json:"preferAnimated"`         // default true (decode-level, §4)
    AssetTypes             map[string]AssetTypePrefs `json:"assetTypes"`
    LearnedFormats         map[string][]string       `json:"learnedFormats"` // key: "<host>|<AssetType>"
    PairOffsetX, PairOffsetY int                     `json:"pairOffsetX","pairOffsetY"` // last-used, −100..100
    PairFlip               bool                      `json:"pairFlip"`
    // unexported: mu sync.RWMutex, dirty chan struct{} (buffered 1), saver goroutine
}
```

**CRITICAL CORRECTION — the drafts deadlock.** Draft `SetFormatOrder` held `mu.Lock()` then called `Save()`, which takes `mu.RLock()` — Go RWMutexes are not reentrant; that is a guaranteed deadlock. Required pattern:

- Mutators (`SetFormatOrder`, `SetFallbacksEnabled`, `RecordLearned`, …) take `mu.Lock()`, mutate, then non-blockingly signal `dirty` — **they never write disk**.
- A single saver goroutine debounces (250 ms after last signal), marshals under `mu.RLock()`, writes `*.tmp`, `os.Rename` over the real file (crash-safe).
- `SaveNow()` (synchronous flush) exists only for shutdown and Settings-Apply.
- Add a regression test: `SetFormatOrder` must complete under `-timeout 10s`.

Learned formats are **keyed per server host + asset type** (different servers ship different formats) and invalidated when: that type's format order changes, fallbacks toggle for that type, or globally on "Clear Learned Formats".

---

## 6. Resolution engine (`internal/assets/resolver.go`) — lock-free hot path

- Learned table published as an atomic snapshot: `atomic.Pointer[learnedTable]` where `learnedTable` maps host → `[AssetTypeCount]string`. Reads are a single atomic load + array index. Writes are copy-on-write + `CompareAndSwap` loop (retry on contention); a successful learn also marks prefs dirty (§5) for lazy persistence.
- `BuildCandidates(base string, t AssetType, host string) []string`: learned hit → exactly 1 candidate; miss → `FormatList` order. Candidate slices come from a `sync.Pool` (cap ≤ 8 pooled; callers return them via `PutCandidates`).
- **Allocation gate:** ≤ 1 alloc/op on the learned path (the joined path string itself), < 100 ns — enforced by `BenchmarkBuildCandidates_Learned` with `b.ReportAllocs()`. If you want true zero, build into a pooled `[]byte` and convert with `unsafe.String` — only if the benchmark proves it matters.
- `RecordSuccess(host, t, ext)` learns the **first** working format per (host, type); subsequent loads of that type probe once.
- `Invalidate(host, t)` / `InvalidateAll()` on settings change and server switch.

---

## 7. Network (`internal/network`) — every request earns its RTT

`Client`:
- **singleflight** keyed by URL: concurrent requests for the same asset collapse into one upstream call.
- **404 cache:** `expirable.LRU[string, struct{}]`, 1024 entries, 5 min TTL. A cached 404 returns `ErrAssetNotFound` without touching the network. Never re-probe a missing URL inside the TTL.
- **Transport tuning:** `MaxConnsPerHost: 16`, `MaxIdleConnsPerHost: 8`, `IdleConnTimeout: 90s`, `DisableCompression: true` (assets are pre-compressed), `ForceAttemptHTTP2: true`, `TLSHandshakeTimeout: 2s`, `TLSClientConfig.ClientSessionCache` set (resumption).
- **CORRECTION:** HTTP/2 in `net/http` only negotiates over TLS. Most AO asset hosts are plain `http://` — those run tuned HTTP/1.1 keep-alive and that is fine. Optional `--h2c` config flag may use `x/net/http2` with `AllowHTTP` for known-good hosts; do not block on it.
- **DNS pre-resolve:** custom `DialContext` consulting a host→IP cache populated at server connect, refreshed every 5 min, so the first probe never blocks on DNS.
- **Reads:** when `ContentLength > 0`, take a pooled `[]byte`, size it, `io.ReadFull`. Else `bytes.Buffer` with `Grow(64<<10)`. (Draft's "zero-copy `io.ReadAll`" was neither zero-copy nor pooled.)
- Per-request deadline 5 s via context; failed hosts back off exponentially.

`WorkerPool` (8 workers):
- **Two priority lanes** (`high`, `low` buffered channels); workers drain `high` first.
- **Epoch cancellation:** room/server change atomically bumps an epoch counter; queued jobs carry their epoch and no-op if stale. No goroutine spawn per task.
- Job IDs from `atomic.Int64` (draft used `rand.Int63()` — collision-prone).
- **CORRECTION — banned:** the draft's `Queue` overflow handler did `<-resultCh` to "make room", silently stealing another caller's result. When `low` is full, shed the **oldest low-priority job** (it's speculative); `high` blocks the producer briefly instead. Results are never dropped.

---

## 8. Fetch → decode → upload pipeline

`AssetManager.Prefetch(ctx, base, t, prio)`: T1 texture hit → done; T2 raw hit → queue decode; T3 disk hit → promote to T2, queue decode, `RecordSuccess`; else iterate candidates over the network, first 200 → T2 + async T3 write + `RecordSuccess` + queue decode; all 404 → warning surfaced (§4).

`DecoderPool` (`max(2, NumCPU/2)` workers):
- Format sniffed by **magic bytes**, never extension: `89 50 4E 47` PNG (then APNG chunk scan), `RIFF….WEBP` (VP8X ANIM flag → animated), `GIF8`, `FF D8` JPEG, `OggS`, MP3 sync.
- Output is plain memory: `Decoded{Frames []*image.RGBA, Delays []time.Duration, Animated bool}`. Frame pixel buffers come from a pool sized by w×h×4.
- **HARD RULE — SDL thread affinity:** the draft had decode workers calling `renderer.CreateTextureFromSurface` from goroutines. With an accelerated renderer that is undefined behavior/crash. Decoder goroutines touch **zero** SDL APIs. `main` calls `runtime.LockOSThread()`; the render thread drains a `chan *Decoded` each frame and uploads textures. Upload budget: assets belonging to the **live** message upload immediately; speculative (prefetch-ahead) uploads capped at 2 textures or 4 MiB per frame to keep 16 ms.
- Audio skips Go decode entirely: bytes → `sdl.RWFromMem` → `mix.LoadWAVRW` (SFX/blips, fully decoded `Chunk`s, cached) / `mix.LoadMUSRW` (music, streamed). Blips are pre-loaded chunks; playing one is a pointer pass.

---

## 9. Caching — three tiers, byte-budgeted, bounded

| Tier | Holds | Budget | Notes |
|---|---|---|---|
| T1 | Decoded textures (`*sdl.Texture` + frame metadata) | 64 MiB (Σ w×h×4 per frame) | Eviction destroys textures **on the render thread** via a destroy queue |
| T2 | Raw fetched bytes | 128 MiB | Promotes from T3 on hit |
| T3 | Disk: `os.UserCacheDir()/AsyncAO/assets/<xx>/<xxhash64-hex>` | unbounded (user-clearable) | Single async writer goroutine; `*.tmp` + rename; never written from hot paths |

- hashicorp LRU v2 is thread-safe; wrap only to add **byte accounting** (evict-until-under-budget), not locking.
- Hit/miss counters: `atomic.Int64`.
- Startup: `debug.SetMemoryLimit(256 << 20)` (configurable). `GOMAXPROCS` stays default — the draft's "+2 for I/O" is a myth; the netpoller already handles blocking I/O.

---

## 10. Prefetcher — predictive, adaptive, pair-aware

`OnMessageStart(msg)`:
1. **HIGH:** speaker preanim + `(b)`/`(a)` emote, shout bubble, blip set.
2. **HIGH:** **pair partner idle sprite** (§11) — needed the same frame the message renders; fetched in parallel with the speaker so paired wall-clock ≈ single.
3. **LOW:** predicted next speaker's `(a)normal` — Markov chain over a sliding window of the last 32 messages; the current pair partner gets a 2× prior (paired characters talk back-to-back).
4. Background/desk: HIGH on room join, LOW for speculative position swaps.

Adaptive: per-host latency EWMA recorded on every fetch; reorder probe formats by success rate **only when fallbacks are enabled** (with zero-fallback there is nothing to reorder). Room change bumps the pool epoch (§7) cancelling stale speculation.

---

## 11. PAIRING (new feature — AO2 ≥ 2.6, 2.8 extensions)

Mirror `../AO2-Client` field indices exactly; cross-check `../webAO`. Implement in `internal/protocol/pairing.go` + courtroom rendering.

**Incoming `MS` extra fields (2.6+):**
- `other_charid` — int; **2.8:** may be `"<id>^<order>"` where order `0` = pair in front, `1` = pair behind.
- `other_name` — pair character folder; `other_emote` — pair emote.
- `self_offset`, `other_offset` — percent of viewport width, −100..100; **2.8:** `"<x>&<y>"` adds a vertical percent.
- `other_flip` — mirror the pair sprite horizontally.

**Rendering rules:**
- Pair partner renders the **idle** `(a)<other_emote>` animation from `characters/<other_name>/`, flipped per flag, dest rect shifted by offset percentages (x of viewport width, y of viewport height).
- Speaker plays its normal preanim → `(b)` talk → `(a)` idle sequence at `self_offset`.
- Z-order: explicit `^order` wins; otherwise speaker in front. Desk/`pos` interaction copies AO2-Client semantics verbatim.
- `other_charid == -1` (or absent) → unpaired; render single, centered, exactly as before.

**Asset pipeline:** the pair sprite is just another `AssetTypeCharSprite` — same resolver, learned formats, caches, decode pool. Both sides fetch at HIGH priority in parallel (§10). Hard gate: paired cold load ≈ single cold load wall-clock (±20%), paired steady-state < 8 ms for both textures.

**Outgoing:** `internal/ui/pair_panel.go` — partner picker (char list), X/Y offset sliders (−100..100, step 5), flip toggle; values embedded in outgoing `MS` packets and persisted in prefs (§5). Chat commands for parity with AO2-Client: `/pair <id>`, `/unpair`, `/offset <x> [y]`. Offsets apply from the next message (AO semantics — no retroactive re-render).

**Tests:** fixture packets for 2.6 and 2.8 variants (with and without `^` and `&`); golden tests for z-order and flip; a prefetch test asserting both sprites resolve concurrently (total ≤ max(single) + ε, not sum).

---

## 12. Courtroom render loop — zero allocations, jitter-free

- `runtime.LockOSThread()` before SDL init; fixed-timestep `Update(dt)` + single-pass `Render()`.
- **Steady-state frames allocate nothing.** Pre-allocated asset-pending arrays, reused rects, no `fmt.Sprintf`/string concat in the loop. Verified by a frame benchmark with `b.ReportAllocs()` → 0.
- **Typewriter:** rasterize the full message once via `ttf` into one texture + per-glyph advance table; reveal by widening the src rect per tick (default 18 ms/char, configurable). Blip `Chunk` fires every `blipRate` visible chars (skip-spaces option). No per-character layout, no texture churn.
- Animation frame timing tables are precomputed at decode; the loop only bumps an index and swaps texture pointers. Preanims play once; idles loop.
- UI chrome textures created once at startup and cached forever.
- Texture destroys (T1 eviction) drain from the destroy queue here, budgeted.

---

## 13. Settings UI (`internal/ui`)

Asset Preferences panel:
- Per-type ordered format list (drag-to-reorder), per-type **Enable Fallbacks** checkbox, **Reset to Default**.
- Global toggles: **Enable Format Fallbacks Globally**, **Play Animations** (renamed PreferAnimated, §4).
- Actions: **Clear Disk Cache**, **Clear Learned Formats**.
- Debug HUD toggle: live probe count, cache hit rate, frame time, heap.

Pairing panel: §11. All mutations go through the debounced-save prefs API (§5) and invalidate learned formats where relevant (§6).

---

## 14. Build & tuning — take the free speed

- `go build -pgo=auto -trimpath -ldflags "-s -w"`; ship `default.pgo` captured from a scripted 5-minute courtroom session (document the capture script in PERFORMANCE.md).
- Release matrix includes `GOAMD64=v3` builds (document the baseline-compat fallback).
- `CGO_ENABLED=1` (SDL2, SDL2_ttf, SDL2_mixer w/ opusfile, libwebp+libwebpdemux). Document MSYS2/mingw setup for Windows in README.
- CI: `go vet`, `staticcheck`, full test suite under `-race`, benchmarks with alloc gates compared against `BENCHMARKS.md`.

---

## 15. Tests, benchmarks, validation — all are merge gates

**Unit:** FormatList semantics (off=exact list, on=+chain, dedup); prefs deadlock regression (§5); learned record/invalidate + CAS race test; 404 TTL cache; singleflight (N concurrent fetches → exactly 1 upstream hit, assert via `httptest` counter); disk cache tmp+rename crash-safety; MS parse for 2.6/2.8 incl. pairing fields.

**Benchmarks (`-benchmem`, alloc gates enforced):**
- `BenchmarkBuildCandidates_Learned` — < 100 ns, ≤ 1 alloc.
- `BenchmarkCacheHit_T1/T2` — 0 allocs.
- `BenchmarkResolveAssets` — < 1 ms.
- `BenchmarkRenderFrame` — < 16 ms, 0 allocs.
- `BenchmarkDecodeWebP_256x192` — < 3 ms.

**Integration (local `httptest` asset servers):**
1. **WebP-only server:** cold load succeeds with fallbacks OFF; probe count == asset count (assert exactly 1 probe/asset).
2. **PNG-only server:** sprites fail with fallbacks OFF → warning surfaced; enable fallbacks at runtime → assets load without restart.
3. **Persistence:** change orders/toggles, restart, settings survive.
4. **Learned warm start:** second cold load issues exactly N probes for N assets, all first-try.
5. **Pairing end-to-end:** paired MS packet → both sprites on screen with correct offsets/flip/z-order; probe/timing assertions per §11.
6. **Probe-count benchmark:** scripted 200-char server session; report cold-load probes vs the ≤ 450 budget.

**`internal/metrics/profiler.go`:** cold-load report line (`Cold load: 87 ms, 212 probes, 3 misses`), 1 Hz sampler (hit rate, heap via `runtime/metrics`, GC p99), `net/http/pprof` behind `--debug`.

---

## 16. Documentation deliverables

- `docs/ARCHITECTURE.md` — thread model diagram (main/render, decode pool, net pool, saver, disk writer), cache tiers, prefetch flow, pairing pipeline.
- `docs/PERFORMANCE.md` — tuning notes, PGO capture script, profiling guide.
- `docs/BENCHMARKS.md` — recorded numbers for every gate in §1/§15 (kept current).
- `docs/adr/0001-zero-fallback-by-default.md` — the design rationale, including the `.webp.animated` removal.
- `docs/user/asset-preferences.md`, `docs/user/pairing.md` — user guides.
- Inline: every `Prefetch` call site tagged with a `// AssetType: X` comment.

---

## 17. Hard rules — violations are rejected outright

1. **No SDL calls off the render thread.** Ever.
2. **No synchronous disk I/O** on render, decode, or resolver paths (async writer + debounced saver only).
3. **No per-asset prefs writes** — learning marks dirty; the saver flushes.
4. **No unbounded** goroutines, channels, queues, or caches.
5. **No mutex on the resolver read path** — atomic snapshot only.
6. **No re-probe of a cached 404** within TTL; no duplicate in-flight fetches (singleflight).
7. **Results are never dropped or stolen** to relieve backpressure — shed speculative *jobs* only.
8. **Race-detector clean.** All of it.
9. **No magic numbers** — named constants throughout.
10. **No push, no PRs** — local commits only.

---

## 18. Milestones (one commit each, tests green at every step)

1. Module init, `internal/config` (prefs + debounced saver + tests incl. deadlock regression).
2. `internal/cache` (3 tiers, byte budgets, async disk writer + tests).
3. `internal/network` (client + priority pool + singleflight/404/epoch tests).
4. `internal/assets` resolver + manager + decoder pool (+ alloc-gated benchmarks).
5. `internal/protocol` MS + pairing parse (+ fixtures).
6. Courtroom render loop + typewriter + pairing render; SDL wiring in `cmd/asyncao`.
7. UI panels, prefetcher, metrics; integration suite (§15); docs; PGO profile.
