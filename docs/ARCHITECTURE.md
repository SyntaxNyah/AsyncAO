# AsyncAO Architecture

## Thread model

```
┌────────────────────────────── main / render thread (LockOSThread) ─────────────────────────────┐
│ SDL init → event poll → session reducer → courtroom Update(dt) → viewport anim clocks          │
│ → audio.Frame (chunk loads, pending plays) → pump.Frame (texture uploads, budgeted)            │
│ → destroy-queue drain (budgeted) → UI screens → Render → Present                               │
└───────────────▲───────────────────────▲──────────────────────────▲────────────────────────────┘
                │ decoded chan (64)      │ audio chan (64)          │ warning chan (32)
        ┌───────┴────────┐      ┌────────┴─────────┐      ┌─────────┴────────┐
        │ decode pool    │      │ asset manager    │      │ (same manager)   │
        │ max(2,NumCPU/2)│◄─────┤ tier walk on     │      └──────────────────┘
        │ magic sniffing │ jobs │ fetch pool       │
        └────────────────┘      │ workers (8, two  │
                                │ lanes, epochs)   │
                                └───────▲──────────┘
                                        │ singleflight HTTP / local mounts
        other goroutines: WebSocket read loop → incoming chan (256, drained per frame)
                          prefs saver (debounced 250 ms, tmp+rename)
                          disk cache writer (bounded queue 256, tmp+rename)
                          1 Hz metrics sampler
```

**Rule zero:** only `internal/render`, `internal/ui`, `cmd/asyncao` touch SDL,
and only on this thread. The decode pool outputs plain `image.RGBA`; texture
creation, destruction (via the bounded destroy queue) and font rasterization
all happen here.

## Asset pipeline (spec §8)

```
Prefetch(base, type, prio)
  └─ fetch pool job (epoch-tagged; room change cancels speculation)
       inflight dedup (one pass per base)
       resolver.BuildCandidates(base, type, host)
         learned hit → exactly 1 URL          ← atomic snapshot, no locks
         miss        → FormatList(type)       ← zero-fallback default = 1 format
       per candidate: T1 contains? → done
                      T2 bytes?    → decode
                      T3 disk?     → promote to T2 + learn + decode
                      source fetch → T2 + async T3 + learn + decode
       every candidate 404 + learned was used → invalidate + one full-list retry
       still nothing → Warning{base, formats tried} → UI banner
  decode pool: sniff magic bytes (never extensions) → RGBA frames (pooled px)
  render pump: live-message uploads immediate; speculative ≤ 2 textures / 4 MiB per frame
  audio types skip decode entirely: bytes → SDL_mixer (C decodes opus/ogg/mp3/wav)
```

## Cache tiers (§9)

| Tier | Holds | Budget | Keying | Eviction |
|---|---|---|---|---|
| T1 | `*sdl.Texture` pages + frame timing | 64 MiB (Σ w×h×4) | asset **base** | byte-budget LRU → destroy queue on render thread |
| T2 | raw fetched bytes | 128 MiB | full URL | byte-budget LRU |
| T3 | disk blobs | unbounded, user-clearable | `xxhash64(full URL)`, sharded `assets/<xx>/<hash>` | manual / Clear button |

Full-URL keys make per-server separation structural: two servers (or two
local mount sets — their origin embeds a mount-list hash) can never collide.

## Resolution engine (§6)

`learnedTable` is an immutable `map[host]*[AssetTypeCount]string` behind an
`atomic.Pointer`. Reads: one load + map index + array index (~68 ns, 1 alloc —
the joined URL). Writes: copy-on-write + CAS retry loop; a successful learn
marks preferences dirty for the debounced saver. Learned entries persist
per `<host>|<type>` and survive restarts (warm start = N probes for N assets).

## Network (§7)

- `singleflight.DoChan` keyed by URL — concurrent identical fetches share one
  upstream call; a caller's context cancels only that caller's wait.
- Negative cache: expirable LRU (1024 entries / 5 min). Cached 404s never
  touch the wire.
- Transport: 16 conns/host, 8 idle, 90 s idle timeout, compression off
  (assets are pre-compressed), TLS session cache, 2 s TLS handshake cap.
  HTTP/2 engages automatically on https hosts; plain-http AO hosts ride tuned
  HTTP/1.1 keep-alive.
- DNS pre-resolve at server connect + lazy 5 min refresh inside the dialer.
- Per-host exponential backoff (500 ms → 30 s) on transport failure.
- Fetch pool: 8 workers, HIGH lane (live message — blocks producer briefly,
  never sheds) and LOW lane (speculation — sheds **oldest** job when full).
  Epoch counter cancels queued jobs on room/server change; cancelled jobs
  still get `Run(stale=true)` so no waiter hangs.

### Known-length reads (documented deviation)

spec §7 suggested pooled read buffers. Payloads are retained indefinitely
by T2/T3, so a pooled buffer could never return to its pool — pooling would
add one copy and zero reuse. Known-length responses therefore read with a
single exact-size allocation + `io.ReadFull` (no growth, no copy); unknown
lengths accumulate in a pooled scratch buffer copied out once.

## Protocol (§ + AO2-Client 2.11)

WebSocket text frames; `HEADER#field#...#%` with `<num>/<percent>/<dollar>/<and>`
escaping. Fast-loading handshake only (`decryptor→HI, ID→ID, FL, SI→RC, SC→RM,
SM→RD, DONE`). MS parsing honors `MS_MINIMUM=15`, gates fields ≥ 15 on
`cccc_ic_support`, normalizes legacy emote mods, and parses pairing
(`id^order`, `x&y` offsets) with AO2-Client's exact z-order semantics
(`^0` = speaker in front). Outgoing MS reproduces AO2-Client's feature-gating
ladder and its asymmetry (the server injects partner fields when relaying).

## Pairing fast path (§11)

`Courtroom.begin` prefetches the speaker's idle/talk/preanim AND the pair
partner's idle sprite at HIGH priority in the same instant; the pool runs them
on parallel workers and singleflight dedups any overlap, so paired cold load
≈ single cold load (test-gated). Render draws pair layers by `SpeakerInFront`,
offsets as percent of viewport, flips via `RendererFlip` — no extra cost over
a solo sprite.

## Dependency justifications (§2 + additions)

| Dependency | Why |
|---|---|
| `veandco/go-sdl2` | SDL2/ttf/mixer bindings (the stack the references use) |
| `hashicorp/golang-lru/v2` (+expirable) | thread-safe LRU; wrapped only for byte accounting |
| `golang.org/x/sync` singleflight | fetch dedup (pinned v0.17.0 for Go 1.24) |
| `cespare/xxhash/v2` | fast non-crypto cache keys |
| `kettek/apng` | APNG decode (the draft's pick was a diff library!) |
| `golang.org/x/image` | pure-Go WebP fallback + embedded Go font |
| `coder/websocket` | **addition:** AO2 ≥ 2.11 is WebSocket-only; stdlib has no WS client. Zero-dependency, maintained, context-aware. |
