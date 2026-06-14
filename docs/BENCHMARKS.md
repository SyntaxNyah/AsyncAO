# Benchmarks

Recorded gates (spec §1/§15). Reference machine: Intel i7-7700K,
Windows 11, Go 1.24.4, MSYS2 UCRT64 gcc 16.1, libwebp 1.6.0. Re-run with:

```bash
go test -run=NONE -bench=. -benchmem ./...
go test -race -count=1 ./...   # the assertion-style gates live in tests
```

| Gate (budget) | Measured | Where |
|---|---|---|
| Resolver fast path < 100 ns, ≤ 1 alloc | **76.5 ns/op, 1 alloc (64 B)** | `BenchmarkBuildCandidates_Learned` |
| Resolver MISS path (unlearned) | **72.5 ns/op, 1 alloc** — generation-cached format lists make the cold path as cheap as the learned one | `BenchmarkBuildCandidates_Miss` |
| Resolve 200 assets < 1 ms | **14.6 µs** | `BenchmarkResolveAssets` |
| T1 cache hit, 0 allocs | **41.2 ns/op, 0 allocs** | `BenchmarkCacheHit_T1` |
| T2 cache hit, 0 allocs | **41.2 ns/op, 0 allocs** | `BenchmarkCacheHit_T2` |
| WebP 256×192 decode < 3 ms | **0.179 ms/op** | `BenchmarkDecodeWebP_256x192` |
| Render frame < 16 ms, 0 allocs | **2.34 ms/op (software renderer), 0 allocs** — generation-cached texture pages, zero LRU ops per steady frame | `BenchmarkRenderFrame` + `TestRenderFrameZeroAllocs` |
| UI emote-grid counter, 0 allocs | **0 allocs/op steady state** — the "page x/y · N emotes" label is memoized; the `fmt.Sprintf` runs only when page/total change | `TestEmotePageCounterMemoized` |
| UI HP-bar key, 0 allocs | **0 allocs/op** — `defensebar<N>`/`prosecutionbar<N>` keys are a precomputed table, not a per-frame string concat (ran ≤ 4×/frame) | `TestHPBarStemNoAlloc` |
| UI tab-chip label, 0 allocs | **0 allocs/op** steady state — the "name (N)" strip label is memoized per tab; the strip asks ~3×/tab/frame and now hits the cache until name/state/unread change | `TestTabChipLabelMemoized` |
| UI server-clock chips, 0 allocs | **0 allocs/op** for a stable/paused clock — labels memoized per displayed second into a reused scratch slice (was a slice + `fmt.Sprintf`/visible-timer every frame); a running clock rebuilds ≤ 1×/sec | `TestTimerChipLabelsMemoized` |
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
- The viewport's steady-state texture lookups are generation-cached
  (`animState.resolve`): zero LRU operations per frame until an upload,
  eviction, or purge bumps `TextureStore.Generation` (re-measured above).
- GIF/APNG composition canvases and DisposalPrevious snapshots come from the
  pixel pool: animated decodes now allocate only their output frames.
- Keep this file current: update measurements when touching any gated path.
