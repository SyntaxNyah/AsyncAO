# Performance Notes

## Build flags (take the free speed)

```bash
go build -pgo=auto -trimpath -ldflags "-s -w" ./cmd/asyncao   # release
GOAMD64=v3 go build ...                                        # AVX2 builds (2013+ CPUs)
```

`-pgo=auto` picks up `default.pgo` at the repo root. Ship both a v3 and a
baseline build; v3 refuses to start on pre-Haswell CPUs.

## Memory budget

`cmd/asyncao` sets `debug.SetMemoryLimit(256 MiB)` at startup. The byte
budgets stack under it: T1 64 MiB textures + T2 128 MiB bytes + working set.
`GOMAXPROCS` stays default — the netpoller already covers blocking I/O; the
old "+2 for I/O" advice is a myth.

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
| Format resolution | atomic snapshot, pooled candidates, 1 alloc |
| Cache hits | lock-free-ish LRU (internal lock, no wrapper), atomics for stats |
| Fetch dedup | singleflight; 404 LRU; per-host backoff off the hot path |
| Decode | worker pool, size-classed pixel pools, SIMD libwebp via CGO |
| Texture upload | budgeted per frame; live message bypasses the budget |
| Typewriter | rasterize once per message; reveal = src-rect width per frame |
| Animations | precomputed delay tables; frame advance is an index bump |
| Render | zero allocations steady-state (cgo-escape pitfalls: never take the address of a stack rect for `Renderer.Copy` — use a reused field) |
