# Performance roadmap

Status of the performance program. As of 2026-06 every item from the
original list is **shipped**; this file keeps the design notes and the
measurements that justified each one. Shipped-earlier work (manifest
autodetect, master-list ETag, HTTP/2 pre-warm, typing speculation, emote
chains, per-server pre-warm, AVIF) is cataloged in docs/FEATURES.md.

## Shipped

### Progressive animated decode (frame 0 first) — `assets.DecoderPool.runJob`
Animated payloads (WebP anim / AVIF sequences / APNG / GIF) with playback
enabled deliver twice from one decode job: frame 0 immediately via the
cheap first-frame path (`Partial=true`), then the full set, which
replaces the page at upload. A giant preanim is on screen after ONE
frame-decode instead of after the whole sequence. The pump drops a
partial that arrives after its own full set (the speculative-budget carry
can reorder); statics that merely sniff "maybe animated" (GIF/APNG)
deliver once. Pinned by `TestProgressiveAnimatedDecode`.

### Adaptive per-host deadlines — `network.Client`
The "adaptive worker pool" goal, implemented as the bounded version that
actually frees workers: each host's time-to-first-byte EWMA (weight 1/4,
sampled when response headers arrive) sets that host's request deadline
at 8× EWMA, clamped to [2 s, the global timeout]. A degrading mirror
stops pinning fetch workers for the full global timeout while healthy
hosts never notice (their floored deadline dwarfs real responses). The
worker COUNT stays fixed — deadlines are what return workers to the
lane. Pinned by `TestAdaptiveTimeout`.

### Zstd T3 tier — `cache.DiskCache`, Settings toggle (default OFF)
Level-1 zstd in the single disk-writer goroutine; a compressed blob is
kept only when it actually shrank, so pre-compressed WebP/AVIF sprites
never pay a decompress tax. The on-disk format self-describes via the
zstd frame magic — mixed caches and toggling at any time work forever.
Measured on this dev box (`BenchmarkDiskZstd`): INI-like text round-trips
at ~48 µs / 35 KiB (≈ 726 MB/s, 2 allocs); incompressible noise costs one
~81 µs encode and stays raw. Pinned by `TestDiskZstdRoundTrip`.

### Label texture atlas — `ui.Ctx` text cache
The documented bind storm was the label cache: one texture per label and
~1200 distinct labels per 4K-char-grid frame. Labels now pack into shared
1024² pages (≤ 4; shelf packer, 1 px pad against filter bleed), so a
text-heavy frame costs a handful of texture binds; oversized labels fall
back to dedicated textures and the purge resets pages wholesale. The
same pass moved the kit's per-draw cgo rects onto Ctx scratch fields
(the Viewport trick) — label draws no longer heap-escape a rect each.
Packer math pinned by `TestShelfPacker`.

### Frame pacing — main loop + typewriter gate
`maxFrameDelta` (100 ms) clamps one frame's dt after a stall (window
drag, AV freeze, sleep wake) so the typewriter resumes smoothly instead
of bursting its backlog and machine-gunning blips. The typewriter's
accumulator carries remainders exactly; `TestTypewriter144HzZeroMiss`
gates it at a simulated 144 Hz: every rune must reveal within one frame
of its schedule and the whole message within one frame of its ideal
duration.

## Deferred

### HTTP/3
Requires `quic-go` (a large dependency with its own TLS stack) while most
AO asset hosts are plain http:// where it cannot help at all. Revisit
when a major asset CDN advertises h3 and the handshake savings can
actually be measured.

## Measurement harness

`internal/metrics` samples heap/GC/hit rates at 1 Hz and prints the
cold-load report; the benchmarks above live next to their packages and
run in CI with the alloc gates (docs/BENCHMARKS.md).
