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

### Frame limiter + event-driven renderer + fps-independent audio — main loop (v1.55)
The GPU-burn fix. The loop paces to `App.FramePace` / `App.HardCapBudget` instead
of vsync (which tied it to the panel — a 165 Hz screen burned GPU while idle, and
some windowed present paths never blocked at all). Shipped defaults: active =
∞ / vsync while you interact or anything animates, **idle = off** (a static screen
renders nothing — near-zero GPU), background/unfocused = 5 fps; each a slider. The
active and background caps are inviolable ceilings, slept UNINTERRUPTIBLY, so an
input flood (mouse motion streams an event every few ms) can never bust them. The
**event-driven renderer** (default ON) parks a static screen on an OS event wait
between real signals — input, `PushWake` from packets/decodes, or a caret / clock /
due-animation deadline (`NextWakeDelay` / `NoteDeadline`) — so idle=off is genuinely
zero redraws. **Audio stays independent of the frame rate** (v1.55.1): a typing
message advances the courtroom and plays its blips at a fine ~60 Hz cadence,
threaded through the same two-tier split-sleep (hard-cap floor uninterruptible), so
blips never batch to a low present rate; incoming SFX/pings wake the parked loop.
SDL_mixer stays on the render thread — no separate audio thread. Pinned by
`TestFramePace`, `TestHardCapBudget`, the `TestSkipFrame*` set, `TestAudioPaceActive`
and `TestAudioActive`.

### Steady-state lookup caches — `ui`
Two per-frame costs removed after the layout engine landed: theme
overlay textures now resolve through a store-generation-keyed page cache
(one map probe per draw, zero LRU locks/recency churn until an upload or
eviction bumps the generation — the same trick the char-grid uses), and
the IC log's search filter caches its index slice against a (mutation
seq, query) key instead of scanning 1024 lines and allocating per frame.
Pinned by `TestICLogFilterCache`.

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
