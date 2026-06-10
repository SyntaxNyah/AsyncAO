# CLAUDE.md — AsyncAO

Guidance for Claude (and humans) working in this repository. Read this before
touching anything.

> "The spec" / "spec §N" in code comments refers to the original engineering
> spec (PROMPT.md), removed from the working tree at project completion.
> Read it any time with: `git show fab9392:PROMPT.md`

## What this is

AsyncAO is a **maximum-performance Attorney Online 2 client in Go** (CGO for
SDL2 + libwebp). Two pillars: **zero-fallback asset streaming** (exactly one
network probe per asset by default, learned formats per server) and **every
millisecond counts** (lock-free hot paths, three cache tiers, zero-allocation
render loop, 256 MiB memory budget). Plus full AO2 ≥ 2.6/2.8 **pairing**.

WebSocket-only. **Legacy raw-TCP AO servers are deliberately unsupported** —
the lobby shows them black, pinned to the bottom, with an upgrade note. Never
add TCP framing back.

## Hard rules (violations get rejected — spec §17, kept in force)

1. **No SDL calls off the render thread.** Only `internal/render`, `internal/ui`
   and `cmd/asyncao` may import go-sdl2, and everything there runs on the
   locked main thread. `internal/assets`' decoder outputs plain `image.RGBA`.
2. **No synchronous disk I/O** on render/decode/resolver paths. Preferences
   use the debounced saver; the disk cache has one async writer goroutine.
3. **No per-asset preference writes** — learning marks dirty, the saver flushes.
4. **No unbounded** goroutines, channels, queues, or caches. Everything has a
   named cap.
5. **No mutex on the resolver read path** — `atomic.Pointer` snapshot only;
   writes are copy-on-write + CAS.
6. **Never re-probe a cached 404** inside its TTL; singleflight collapses
   duplicate in-flight fetches.
7. **Results are never dropped or stolen.** Backpressure sheds *speculative
   jobs* (oldest low-priority), and every accepted job's `Run(stale)` is
   invoked exactly once.
8. **Race-detector clean.** `go test -race ./...` must pass before any commit.
9. **No magic numbers.** Every constant is named and commented.
10. **No push, no PRs from automation.** Local commits only; the user pushes.

## Build & test commands (Windows dev box)

```powershell
# one-time deps (MSYS2 UCRT64 + SDL2 + libwebp)
powershell -ExecutionPolicy Bypass -File scripts\setup-deps.ps1
# build + stage DLLs
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Release
```

For raw `go` commands set: `PATH+=C:\msys64\ucrt64\bin`, `CGO_ENABLED=1`,
`CC=C:\msys64\ucrt64\bin\gcc.exe`, `CGO_CFLAGS=-IC:\msys64\ucrt64\include`,
`CGO_LDFLAGS=-LC:\msys64\ucrt64\lib`,
`PKG_CONFIG_PATH=C:\msys64\ucrt64\lib\pkgconfig`.

```bash
go test -race -count=1 ./...                       # full gate
go test -run=NONE -bench=. -benchmem ./...         # alloc gates (see docs/BENCHMARKS.md)
gofmt -w . && go vet ./...                         # before every commit
```

Pure-Go packages test with `CGO_ENABLED=0`; SDL packages use the dummy video
driver headlessly (they skip if SDL is unavailable).

## Package map

| Package | Role | SDL? |
|---|---|---|
| `internal/config` | preferences + debounced atomic saver; favorites; local mounts | no |
| `internal/cache` | ByteBudgetLRU (T1/T2) + async disk tier (T3, xxhash full-URL keys) | no |
| `internal/network` | dedup HTTP client (singleflight, 404 TTL, backoff, DNS warm), priority worker pool with epoch cancellation, master list + tiers + direct connect | no |
| `internal/assets` | AssetType enum, lock-free resolver (learned formats), tier-walking manager, decode pool (magic-byte sniffing), WebP CGO binding + fallback, local mounts, Markov prefetcher | no (decoder is SDL-free by rule) |
| `internal/protocol` | AO wire framing/escaping, MS 2.6/2.8 parse/build, pairing semantics, FeatureSet, WebSocket conn | no |
| `internal/courtroom` | session handshake reducer, message lifecycle state machine, typewriter pacing, URL conventions, char.ini | no |
| `internal/theme` | AO2 theme INIs (design/fonts/sounds), asset lookup ladder | no |
| `internal/metrics` | 1 Hz sampler (heap, GC p99, hit rates), cold-load report | no |
| `internal/render` | TextureStore (T1), viewport compositor, message raster, upload pump, SDL_mixer audio | **yes** |
| `internal/ui` | immediate-mode kit + screens (lobby/phone book, char select, courtroom, settings, about) | **yes** |
| `cmd/asyncao` | flags, GOMEMLIMIT, LockOSThread, SDL init, main loop | **yes** |

## Reference repos (read-only, sibling directories)

- `../AO2-Client` — **canonical protocol & courtroom semantics. It wins every
  conflict.** Key files: `src/datatypes.h` (CHAT_MESSAGE enum),
  `src/courtroom.cpp` (pairing/emote-mod/queue), `src/packet_distribution.cpp`
  (handshake), `src/network/websocketconnection.cpp` (framing).
- `../webAO` — asset URL conventions (`characters/<x>/(a)<emote>.<ext>`,
  `background/`, `sounds/{music,blips,general}`; PNG sprites are unprefixed).
- `../AO-SDL`, `../ferris-ao-switch` — SDL2 thread-model references.

Protocol gotchas already encoded in code/tests — don't "fix" them:
- Pair order `^0` = **speaker in front**, `^1` = speaker behind (the spec's
  table was inverted; AO2-Client wins — see `internal/protocol/pairing.go`).
- Outgoing MS is **asymmetric** to incoming: the client never sends
  other_name/other_emote/other_offset/other_flip (server injects them).
- Fields ≥ 15 are honored only with the `cccc_ic_support` feature.
- Wire escaping: `#%$&` → `<num>/<percent>/<dollar>/<and>`, decode after
  `#`-split; SC entries get an extra decode after `&`-split (AO legacy).
- `.webp.animated` does not exist. Animation = VP8X ANIM flag at decode time.

## Environment gotchas (this dev machine)

- The AV intercepts TLS (pacman needed `curl -k`) and sometimes blocks
  freshly-built test exes (`fork/exec ... Access is denied`) — just re-run.
- The AV also throttles PARALLEL loopback httptest servers: running several
  test packages at once can stall one for minutes. **On this machine always
  test with `go test -p 1 ./...`** (CI runners don't need it).
- The AV has also quarantined `C:\msys64\ucrt64\bin\ld.exe` mid-session:
  CGO linking then fails with `collect2: cannot find 'ld'`. Fix:
  `pacman -S mingw-w64-ucrt-x86_64-binutils`. On any sudden CGO link
  failure, check ld.exe exists before debugging anything else.
- `go.mod` pins `go 1.24` and `golang.org/x/sync v0.17.0`; **don't let
  `go get` bump x/sync** to a version requiring Go 1.25.
- devkitPro's MSYS2 at `C:\devkitPro\msys2` belongs to other projects — never
  touch it; AsyncAO uses `C:\msys64`.
- WebP test fixtures were generated with msys2's `cwebp`/`img2webp`
  (see test/fixtures/).

## Conventions

- Comments explain *why*; protocol behavior cites the AO2-Client source spot.
- Every `Prefetch` call site carries an `// AssetType: X` comment.
- Tests pin behavior, including performance gates (`testing.AllocsPerRun`).
- Cache keys are full URLs (host included) — per-server separation is
  structural. Never key by bare path.
- New dependencies require a written justification in docs/ARCHITECTURE.md.
- Milestone-sized local commits; never push.
