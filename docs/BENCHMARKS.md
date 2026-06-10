# Benchmarks

Recorded gates (spec §1/§15). Reference machine: Intel i7-7700K,
Windows 11, Go 1.24.4, MSYS2 UCRT64 gcc 16.1, libwebp 1.6.0. Re-run with:

```bash
go test -run=NONE -bench=. -benchmem ./...
go test -race -count=1 ./...   # the assertion-style gates live in tests
```

| Gate (budget) | Measured | Where |
|---|---|---|
| Resolver fast path < 100 ns, ≤ 1 alloc | **67.9 ns/op, 1 alloc (64 B)** | `BenchmarkBuildCandidates_Learned` |
| Resolve 200 assets < 1 ms | **14.6 µs** | `BenchmarkResolveAssets` |
| T1 cache hit, 0 allocs | **40.8 ns/op, 0 allocs** | `BenchmarkCacheHit_T1` |
| T2 cache hit, 0 allocs | **37.7 ns/op, 0 allocs** | `BenchmarkCacheHit_T2` |
| WebP 256×192 decode < 3 ms | **0.20 ms/op** | `BenchmarkDecodeWebP_256x192` |
| Render frame < 16 ms, 0 allocs | **2.44 ms/op (software renderer), 0 allocs** | `BenchmarkRenderFrame` + `TestRenderFrameZeroAllocs` |
| Cold probes ≤ 1/asset, ≤ 450 total | **285 probes / 285 assets** (200-char session) | `TestProbeBudget200CharServer` |
| Paired cold ≈ single (±20%) | **parallel: 0.17 s with 150 ms/request latency** (serial would be ≥ 0.30 s) | `TestPairedPrefetchResolvesConcurrently` |
| Steady-state probes (learned warm start) | **N probes for N assets, all first-try** | `TestManagerLearnedWarmStart` |
| 404 never re-probed in TTL | **1 upstream hit for 11 fetches** | `TestNotFoundCachedWithinTTL` |
| Singleflight | **32 concurrent fetches → 1 upstream request** | `TestSingleflightCollapsesConcurrentFetches` |
| Prefs deadlock regression | **mutators complete instantly** | `TestSetFormatOrderCompletes` |
| Race detector | **clean across all packages** | CI `go test -race ./...` |

Notes:

- `BenchmarkRenderFrame` runs on SDL's *software* renderer (headless CI);
  the accelerated path in real use is significantly faster. The 16 ms budget
  holds even in the worst case.
- The resolver's single allocation is the joined URL string itself — the
  theoretical floor without `unsafe.String` tricks, which the 68 ns reading
  says we don't need.
- Keep this file current: update measurements when touching any gated path.
