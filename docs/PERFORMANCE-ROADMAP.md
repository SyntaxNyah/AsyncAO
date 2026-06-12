# Performance roadmap

Status of the performance program beyond what already shipped. Shipped
work is in docs/FEATURES.md (autodetect manifests, ETag, HTTP/2 pre-warm,
typing speculation, emote chains, per-server pre-warm, AVIF). The items
below are **designed but deliberately staged**: each one is engine surgery
whose value must be measured before it lands, per the project's own bar
(benchmark gates, race-clean, no speculative complexity).

## Staged designs

### Progressive animated-WebP/AVIF decode (frame 0 first)
Goal: giant preanims start instantly.
Design: split `DecodeJob` into a two-phase delivery — the decoder posts a
`Decoded` carrying frame 0 + `Animated=true, Partial=true`, then keeps
walking the demuxer and posts a completion with the full frame set. The
upload pump uploads partials as single-frame pages keyed by base, then
*replaces* the page on completion (TextureStore already swaps pages
atomically per upload; the viewport's animState re-resolves on generation
bump, so the swap is a frame-boundary event). Risks: pixpool accounting
across two deliveries; Release discipline for the partial; the §12
alloc gates must stay flat. Measure: time-to-first-frame on a 5 MB
animated preanim, cold T2, before/after.

### Adaptive fetch-pool sizing (within the fixed cap)
Goal: stop letting one slow host starve the lane.
Design: keep the worker cap (16) but track per-host EWMA of
time-to-first-byte in the dedup client; when a host's EWMA exceeds a
named threshold, cap its in-flight jobs (semaphore per host, derived from
MaxConnsPerHost anyway) so other hosts' jobs keep flowing. This is
bounded self-tuning — never more workers than today, only smarter
distribution. Measure: mixed-host cold load (CDN + slow mirror) wall
clock; the §11 cold-load gate must not regress.

### Zstd-compressed T3 tier (behind a setting, default off)
Goal: more cache in the same disk budget.
Design: `klauspost/compress/zstd` (pure Go, well-maintained — needs the
ARCHITECTURE.md justification row when it lands). Write path: compress in
the single disk-writer goroutine (level 1); blobs gain a 4-byte magic so
mixed caches read both forms forever. Read path: decompress on T3 hit
(off the render thread already). WebP/AVIF sprites are pre-compressed —
expect ~5–15% for those but 2–4× on INI/JSON/PNG; the setting exists
because the CPU cost on potato hardware is real. Measure: T3 hit latency
p50/p99 and on-disk bytes for a 1 GB cache corpus, both settings.

### Texture atlas for UI chrome + blips
Goal: cut per-frame texture binds in the immediate-mode kit.
Design: pack the label cache and recurring chrome quads into a 2048²
atlas page (render thread, SDL_Texture target); the kit's draw calls
become atlas sub-rect copies. The win is real only if profiling shows
bind overhead matters — on a 2D SDL renderer the driver may already
batch. Measure first: GPU frame time at 144 Hz on the lobby + 4000-char
grid; only land if binds show up.

### Frame pacing (vsync-aligned typewriter + animation)
Goal: zero dropped frames at 144 Hz, typewriter ticks that never beat
against vsync.
Design: derive tick timing from the *presented* frame time (SDL_GetTicks
delta after Present) instead of wall-clock dt accumulation; schedule
typewriter reveals on present boundaries. Add a benchmark gate that
replays a 200-rune message at simulated 144 Hz and asserts zero missed
reveal deadlines. Needs a 144 Hz display to validate for real.

### HTTP/3
Evaluated and deferred: requires `quic-go` (large dependency, its own
TLS stack) while most AO asset hosts are plain http:// where it can't
help at all. Revisit when a major asset CDN advertises h3 and the
handshake savings can actually be measured.

## Measurement harness

`internal/metrics` already samples heap/GC/hit rates at 1 Hz and prints
the cold-load report; staged items should extend that report rather than
grow new instrumentation. Every landed item gets a `testing.B` gate in
docs/BENCHMARKS.md like the existing decode/raster gates.
