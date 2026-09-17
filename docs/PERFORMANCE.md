# Performance Notes

## Build flags (take the free speed)

```bash
go build -pgo=auto -trimpath -ldflags "-s -w" ./cmd/asyncao   # release
GOAMD64=v3 go build ...                                        # AVX2 builds (2013+ CPUs)
```

`-pgo=auto` picks up `default.pgo` at the repo root. Ship both a v3 and a
baseline build; v3 refuses to start on pre-Haswell CPUs.

## Memory budget

`cmd/asyncao` sets `debug.SetMemoryLimit(700 MiB)` at startup. The byte
budgets stack under it: T1 64 MiB textures + T2 128 MiB bytes + working set.
Animated sprites are the deliberate exception: they are no longer decimated to
fit T1 (#110) — instead each animation is downscaled to fit a 500 MiB budget,
so a 60-frame 1600×1200 clip decodes at native and only very long clips shrink.
The per-frame downscale is a fast area-average (box) filter — CatmullRom's
float64 kernel was the decode bottleneck. The 700 MiB limit covers the Go heap
(a clip's decoded RGBA stays live there until its textures upload); the
textures themselves live in VRAM.
`GOMAXPROCS` stays default — the netpoller already covers blocking I/O; the
old "+2 for I/O" advice is a myth.

## Memory tuning (Settings -> Power user)

The animated-sprite memory spike is governed by three levers, all in the
Power user tab's "Decode downscale & texture memory" group:

- **Animated sprite memory** (default 128 MiB) - the per-sprite decoded-frame
  budget. 128 keeps typical sprites native and downscales only very large clips
  to fit; lowering it downscales more (softer, a fraction of the RAM). LIVE:
  new decodes pick it up without a restart.
- **Concurrent animated decodes** (default 2) - how many long clips decode at
  once. A streaming clip holds one native frame in the decoder at a time (its
  downscaled frames accumulate on the GPU), but it still holds its `animGate`
  slot for the whole decode, so this bounds concurrent decode bursts. LIVE.
- **Animated sprite height cap** (default off) - an extra height clamp for
  animated sprites only (still art untouched).
- **Texture compression** (default DXT5) - how decoded art is stored on the GPU.
  Off = raw RGBA (lossless 32 bpp); DXT5 = BC3 (8 bpp, smooth alpha, ~4x
  smaller); DXT1 = BC1 (4 bpp, 1-bit alpha, ~8x smaller). Both DXT modes are
  lossy 4x4-block formats, apply only to canvases whose width/height are
  multiples of 4, and only when the GPU advertises the format (the software
  fallback silently stays Off). This is the webAO-parity lever - browsers store
  compressed GPU textures, which is most of the ~4-8x RAM gap on integrated
  GPUs. Restart-applied.

Under the hood the pixel pool gained an 8 MiB class: native 1600x1200 frames
(~7.3 MiB) used to land in the 16 MiB class (~2x waste per frame), and with
`NumCPU/2` decode workers that waste multiplied into the multi-GiB spike. The
8 MiB class keeps per-frame overhead near 1.1x and halves the burst.

Rough expectations (DXT5 compression on): 128 MiB budget ~= ~33 MiB per
animation on the GPU; 256 MiB ~= ~67 MiB; 500 MiB ~= ~110 MiB. At 128 MiB +
DXT5, ~15 animations fit the oversized map, so emote switches on a two-sprite
character stop re-decoding.


## PGO capture

`default.pgo` is a CPU profile from a scripted courtroom session. To
re-capture after significant changes:

```bash
# 1. run the client with pprof enabled
./asyncao -debug &        # pprof on localhost:6060
# 2. drive a session: connect, char select, ~5 minutes of paired IC messages
#    (scripts/pgo-session.md documents the manual script)
# 3. capture while the session is active
curl -o default.pgo "http://localhost:6060/debug/pprof/profile?seconds=120"
# 4. rebuild with -pgo=auto and commit the new profile
```

A profile captured during real courtroom traffic (typewriter + animations +
fetch/decode) optimizes the paths that matter: decode loops, render copies,
LRU lookups.

## Profiling a live client

```bash
./asyncao -debug
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30   # CPU
go tool pprof http://localhost:6060/debug/pprof/heap                 # heap
go tool pprof http://localhost:6060/debug/pprof/allocs               # alloc churn
```

Watch for:
- any allocation inside `render.Viewport.Render` / `Update` (the gate is 0),
- `runtime.cgocall` overhead spikes (batching texture ops beats chatty calls),
- `GC pause p99` in the 1 Hz sampler (budget < 2 ms; the 256 MiB soft limit
  plus low allocation rates keep cycles rare and short).

## Hot-path inventory

| Path | Technique |
|---|---|
| Format resolution | atomic snapshot, pooled candidates, 1 alloc — and the unlearned path reads a generation-cached format table (no prefs lock, no rebuild) |
| Cache hits | lock-free-ish LRU (internal lock, no wrapper), atomics for stats |
| Fetch dedup | singleflight; 404 LRU; per-host backoff off the hot path |
| Decode | worker pool, size-classed pixel pools, SIMD libwebp via CGO |
| Texture upload | budgeted per frame; live message bypasses the budget |
| Typewriter | rasterize once per message; reveal = src-rect width per frame |
| Animations | precomputed delay tables; frame advance is an index bump |
| Render | zero allocations steady-state (cgo-escape pitfalls: never take the address of a stack rect for `Renderer.Copy` — use a reused field); texture pages are generation-cached per layer, so steady frames do zero LRU lookups |
| Animated decode | composition canvas + DisposalPrevious snapshot from the pixel pool — allocations limited to the output frames |
| HTTP transport | 3 s response-header timeout fails stalled hosts before the 5 s deadline, freeing the per-host connection slot |
