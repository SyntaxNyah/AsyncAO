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
        └────────────────┘      │ workers (16, two │
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

## Frame pacing & the event-driven loop

The main loop is not vsync-bound. Each pass computes a pacing budget from
`App.FramePace(focused)` (the adaptive tier — full while interacting/animating, the
idle rate on a static screen) and `App.HardCapBudget(focused)` (the INVIOLABLE
active/background ceiling). The budget is slept in two tiers: the hard-cap floor
UNINTERRUPTIBLY (so an input flood — mouse motion streams an event every few ms —
can never bust the cap), then any surplus INTERRUPTIBLY (input during a slow idle
tier renders within one ceiling instead of waiting the whole budget out).

With the **event-driven loop** (`EventDrivenLoopOn`, default ON) a static screen
(`SkipFrame` true: no input, nothing animating, nobody talking) renders nothing and
parks on `sdl.WaitEventTimeout`. It wakes on real input, on the cross-thread
`PushWake` doorbell (a user event pushed by the WS read loop / decode delivery — the
one sanctioned cross-thread SDL touch, `wake.go`), or when `NextWakeDelay` reports a
real redraw deadline (idle-rate tick, caret flip, the next animation flip via
`Viewport.NextAnimDue`). idle=off then means genuinely zero redraws until something
changes; `NoteDeadline` / `uiDirty` / `NoteAnimating` are the "redraw one frame"
hooks producers use so an off-thread change still appears at idle=0. `NoteAnimating`
is a **retrospective, self-clearing census**: a clock-driven on-screen surface
(animated theme chrome, a looping sprite preview, animated chatbox Text FX) marks it
from its DRAW site every frame it moves, and `SkipFrame` / `FramePace` read the last
frame's tally — never a bare state flag that can outlive its draw. Keying the skip on
such a flag broke both ways (fixed v1.55.2): a sprite preview orphaned across a screen
switch latched the pace ON with no draw left to clear it, and idle Text FX animated
but reported nothing so the loop parked and froze it.

**Audio is decoupled from the present rate.** While the live courtroom is typing
(`App.AudioActive`), the loop spends its pacing budget advancing the room — and
playing its blips — at a fine ~60 Hz cadence via `Background` (room `Update` + audio
drain, no draw), threaded through the SAME two-tier split-sleep; the next `Frame`
draws the already-current room (`roomPreAdvanced` makes it skip its own
`room.Update`). So blips play at their natural cadence even at a 1 fps cap, with the
hard-cap floor still uninterruptible. SDL_mixer stays on this thread (rule zero) —
there is no separate audio thread.

## Asset pipeline (spec §8)

```
Prefetch(base, type, prio)            PrefetchWithFallback(base, altBase, ...)
  └─ fetch pool job (epoch-tagged; room change cancels speculation)
       inflight dedup (one pass per primary base)
       T1 contains primary base? → done       ← textures key by BASE, so the
                                                 check is by base, pre-chain
       per base in {primary, alt}:
         resolver.BuildCandidates(base, type, host)
           learned hit → exactly 1 URL        ← atomic snapshot, no locks
           miss        → FormatList(type)     ← zero-fallback default = 1 format
         per candidate: T2 bytes?    → decode
                        T3 disk?     → promote to T2 + learn + decode
                        source fetch → T2 + async T3 + learn + decode
         every candidate 404 + learned was used → invalidate + one full-list retry
       still nothing → Warning{base, formats tried} → UI banner (12 s, courtroom
                                                      + char select)
  decode pool: sniff magic bytes (never extensions) → RGBA frames (pooled px),
               animations DECIMATED to maxDecodedAssetBytes (T1 budget / 2 —
               keep evenly-spaced frames spanning the whole clip, fold skipped
               delays forward: a lower-fps full loop beats a truncated one that
               snaps mid-preanim, and both dodge a 250 MB RGBA spike inside the
               256 MiB process budget); fixed-cell types
               (char icons → 64 px, emote buttons → 40 px) thumbnail at
               decode, so a 500×500 pack icon costs ~16 KB of T1 instead of
               ~1 MB and a 4000-char roster fits the texture budget whole
  render pump: live-message uploads immediate; speculative ≤ 16 textures /
               4 MiB per frame (bytes protect 16 ms; the count just bounds
               tiny-upload bursts). A page the LRU refuses is destroyed +
               reported, never leaked.
  audio types skip decode entirely: bytes → SDL_mixer (C decodes opus/ogg/mp3/wav)
```

### Sprite name chain (AO2-Client `CharLayer::load_image`)

Packs ship idle/talk sprites as `(a)<emote>`/`(b)<emote>` **or** as bare
`<emote>` files (`1.webp`, `2.webp`, …). `Courtroom.begin` therefore uses
`PrefetchWithFallback(prefixed, bare)`: the bare spelling is probed only
after every format of the prefixed one 404s, the asset keeps the prefixed
base as its identity (scene layers, T1 key), the 404 cache stops the extra
probe from repeating inside its TTL, and once resident the T1 short-circuit
costs zero probes. Extension learning is unaffected — whichever spelling
hits records the host's format as usual.

### Demand-driven loading (visible = demand, not speculation)

Connect-time bursts are capped (`charIconWarmup` = 128): a 4000-character
server would only shed itself out of the 256-slot low lane. Instead, the
char grid and the emote picker demand exactly what is on screen: at most
`charIconAskPerFrame` (32) submissions per frame from a shared budget, one
re-ask per asset per `charIconRetryInterval` (2 s) — shed jobs are never
re-run by the pool, so the cadence self-heals backpressure, and loaded
textures stop asking via the store lookup that precedes every ask. The
live scene gets the same treatment at HIGH priority (`healScenery`): an
evicted background/desk re-demands on the same cadence, and the viewport
holds the last-resident scenery (`syncAnimSticky`) until the replacement
texture actually lands — a position flip never blanks the viewport.

Hovering any character cell (either grid, the wardrobe too) warms its
char.ini through the decode-free raw lane (`Manager.PrefetchRaw`:
pool-bounded, inflight-deduped, T2 + disk), so the eventual pick loads
its emote list from memory instead of paying an RTT.

## Cache tiers (§9)

| Tier | Holds | Budget | Keying | Eviction |
|---|---|---|---|---|
| T1 | `*sdl.Texture` pages + frame timing | 64 MiB (Σ w×h×4) | asset **base** | byte-budget LRU → destroy queue on render thread |
| T2 | raw fetched bytes | 128 MiB | full URL | byte-budget LRU |
| T3 | disk blobs | unbounded, user-clearable | `xxhash64(full URL)`, sharded `assets/<xx>/<hash>` | manual / Clear button |

Full-URL keys make per-server separation structural: two servers (or two
local mount sets — their origin embeds a mount-list hash) can never collide.

Two generation counters keep hot paths lock-free without staleness:

- `AssetPreferences.FormatGeneration` — bumped by format mutators; the
  resolver's miss path serves probe lists from an atomic per-generation
  snapshot (70 ns/op, 1 alloc — identical to the learned path).
- `TextureStore.Generation` — bumped on upload/eviction/purge; each viewport
  layer caches its `*TexturePage` against it, so steady-state rendering does
  zero LRU operations and a cached pointer can never outlive its textures
  (destroys happen later in the same frame, after the generation check).

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
- Fetch pool: 16 workers (fetches are RTT-bound; the transport is sized
  for 16 conns/host and h2 bases multiplex them over one connection —
  spec §7's original 8 halved cold-viewport fill for nothing), HIGH lane
  (live message — blocks producer briefly, never sheds) and LOW lane
  (speculation — sheds **oldest** job when full).
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
escaping. Fast-loading handshake only — and loading is **client-initiated**:
`decryptor→HI, ID→ID, FL, PN→askchaa, SI→RC, SC→RM, SM→RD, DONE` (without
askchaa every server waits forever; only the askchar2 paging is legacy).
MS parsing honors `MS_MINIMUM=15`, gates fields ≥ 15 on
`cccc_ic_support`, normalizes legacy emote mods, and parses pairing
(`id^order`, `x&y` offsets) with AO2-Client's exact z-order semantics
(`^0` = speaker in front). Outgoing MS reproduces AO2-Client's feature-gating
ladder and its asymmetry (the server injects partner fields when relaying).

## UI kit contract (`internal/ui`)

Immediate-mode over one per-frame input snapshot — **order is law**:
`BeginFrame` (clears the snapshot) → `HandleEvent` per polled SDL event →
draw pass reads it. Feeding events before `BeginFrame` erases every click
before any widget sees it (this shipped once; `TestInputSnapshotOrder`
pins the contract). Mouse coordinates refresh from motion/button events,
so a release hit-tests where it actually happened.

- Clicks fire on left-button **release** over the widget.
- Clipboard: Ctrl+V appends (flattened to one line), Ctrl+C copies,
  Ctrl+X cuts — focused text field only; SDL keeps control chords out of
  TEXTINPUT so nothing double-inserts.
- `VScrollbar` is the only drag-aware widget: `Ctx` tracks the held left
  button plus a drag-owner id, pressing the track centers the thumb there
  (one click to the bottom of a 4000-char list), and its return value
  clamps wheel scrolling to content.
- `HoverPreview` (3 s dwell, right-click instant) pops the full sprite:
  char select previews idle, the emote picker previews the TALKING (b)
  sprite — what plays when the message sends.
- Emote buttons draw `emotions/button<N>_off|_on` art (its own
  `EmoteButton` asset type, WebP-first, Settings-toggleable) with the
  `_off` art + accent ring standing in while `_on` streams.
- Ctrl+A arms select-all on the focused field: the next typed/pasted
  text replaces the whole value, backspace clears it, Ctrl+C/X act on
  everything; a highlight shows while armed.
- Screens never do disk I/O on the render thread: theme-folder scans,
  char.ini fetches, the native folder picker (Browse → PowerShell
  FolderBrowserDialog) and dropped-path resolution (SDL DROPFILE) all
  run on goroutines and land via polled channels, like the lobby fetch.

## Courtroom knobs (all persisted, all live)

- **View − +** resizes the viewport (40–85 % of the window width;
  default 66 ≈ the original 2/3) — log column and chat box reflow.
- **Text − +** zooms the IC message box (100–250 %): a dedicated scaled
  font slot in `Ctx`, raster invalidates on zoom/width change, box
  height grows with the zoom.
- **Log − +** scales log/OOC/music/area list text (75–200 %); the label
  cache keys by font identity so scaled labels cache like any other.
- **Box − +** scales the IC/OOC input field height (75–200 %).
- **OOC tab** (Log | Music | Areas | OOC): full scrollable OOC history
  plus the IC showname (live — outgoing messages read it per send) and
  the permanent OOC name, both persisted.
- **Volumes** (Settings): music/SFX/blip 0–100, applied via SDL_mixer —
  music globally, chunks per playing channel.
- **Format order** (Settings): ticking picks the probe set, clicking an
  order chip promotes that extension one slot toward "probed first".
- The pairing panel picks partners from a searchable click-to-pick list
  (the old one-by-one cycle was unusable against 4000-char rosters).
- While minimized the loop runs `App.Background` (session pump, no
  drawing) at a 50 ms nap — keepalives keep flowing at ~0 % GPU.
- The renderer sets `BLENDMODE_BLEND` for draw ops at startup: alpha
  fills (chat box, taken overlay, selection highlight) actually blend —
  SDL's default NONE silently rendered them opaque.

## Wardrobe & iniswap (custom characters)

The courtroom's Wardrobe button opens a modal char-select-grade grid
merging two sources, wardrobe first:

1. **The wardrobe** — the user's own favourites, persisted in prefs
   (`WardrobeCap` 1024) **across sessions and across servers** (folder
   names are server-agnostic; assets resolve against the current
   origin). Stars on each cell toggle membership; an add box accepts any
   folder name, so no server list is required at all.
2. **`<asset origin>/iniswap.txt`** — one character folder per line —
   the server-curated set, merged underneath minus wardrobe duplicates
   (case-insensitive).

Neither occupies a server slot. Every layer reuses the existing fast
path, nothing bespoke:

- the txt rides `FetchRaw` (T2 + disk cached, singleflight) on a
  goroutine; parse is bounded (`iniswapListCap` 4096), case-insensitively
  deduped + sorted, lowercase names precomputed for the search filter;
- icons are ordinary `AssetTypeCharIcon` traffic: same paced demand
  (shared `demandAsset` budget/cadence), same 64 px decode thumbnails,
  same 404 cache — the **list-character pipeline is untouched**, the menu
  is just a second consumer of it;
- hover previews and, once picked, live sprites go through the normal
  name-chain (`(a)X`/`(b)X` → bare `X`).

Picking an entry only swaps the active folder: outgoing `MS` carries the
custom name in `char_name` (AO2-Client `set_iniswap` semantics — servers
relay the folder, receivers stream it like any speaker), and the emote
list reloads from the custom `char.ini`. Re-picking a list character or
disconnecting clears the override; an in-flight txt fetch is drained on
disconnect so a stale list can't land after reconnecting elsewhere.

## Lobby data

The master list JSON is parsed in full — `ip`, ports, `players`, `name` and
`description`. Starring a server persists name + URL + **description** into
the phone book (`config.FavoriteServer`), and `MergeFavorites` synthesizes
entries for private servers so the lobby shows their descriptions even with
the master list unreachable. The live master description wins for servers
still listed.

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
| MSYS2 `libavif` (CGO, no Go module) | **addition (user request):** `.avif` as a probe format — native dav1d/aom decode for stills *and* AV1 image sequences, bound exactly like the libwebp CGO shim (~100 lines). The pure-Go alternatives embed a WASM runtime (gen2brain/avif → wazero), the opposite of this project's soul. CGO-less builds degrade to a descriptive decode error; sniffing (`ftyp` + `avif`/`avis` brands) stays pure Go. |
| System `ffmpeg` (runtime, no Go module, no CGO) | **addition (user request, #99 scene video export):** the **🎥 Video** button streams captured frames into an external `ffmpeg` process (`internal/videoenc`, pure Go) for H.264/MP4 or VP9/WebM. It is **runtime-optional by design** (user: "it still boots even without em") — `Available()` is a cached PATH lookup, a missing ffmpeg only disables that one action, and there is **no build-time dependency** and nothing linked. Audio (music/SFX) muxing into the video is reserved for a follow-up pass. |
| OpenDyslexic font (embedded asset, no Go module) | **addition (user request, M9):** the "dyslexia-friendly font" toggle bundles `internal/ui/fonts/OpenDyslexic-Regular.otf` via `//go:embed` (~172 KB) so it works for every user with no separate install — a path-on-disk preset only helped the few who'd installed it. SIL OFL 1.1 (license shipped alongside as `OpenDyslexic-LICENSE-OFL.txt`; embedded unmodified, so the Reserved Font Name clause is satisfied). Applied only to the IC/OOC chat + log text (the existing override-chain scope), so chrome widget metrics are untouched. |
