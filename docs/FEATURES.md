# AsyncAO feature inventory

What the client does beyond plain IC chat, where each piece lives, and the
canonical reference it mirrors. AO2-Client wins every semantic conflict
(see CLAUDE.md); citations name the source spot that was mirrored.

## Court state (2.6–2.10 protocol parity)

| Feature | Wire | Where | Notes |
|---|---|---|---|
| Penalty bars | `HP` in/out | `session.go`, `court_extras.go` drawHPBar | Theme art `defensebar0..10`/`prosecutionbar0..10`, procedural pip strip fallback; direction-keyed sfx from `penalty/penalty.ini` (set_hp_bar) |
| WT / CE / verdict splashes | `RT` in/out | handleWTCE + drawCourtOverlays | `witnesstestimony_bubble`/`crossexamination_bubble`/`notguilty_bubble`/`guilty_bubble` theme art (animated ok), text-banner fallback; `testimony1#1` ends the looping **Testimony** badge (courtroom.cpp:4846) |
| Judge controls | `JD` | judgeVisible + drawJudgeRow | −1 pos-dependent (pos == `jud`), 0 hide, 1 show; row sends RT/HP |
| Modcall | `ZZ` in/out | drawModcallDialog, EventModcall | Reason dialog (`{reason, "-1"}` per courtroom.cpp:6530); receive = OOC pin + `mod_call` sound + taskbar flash + an **optional desktop toast** (Settings → Audio & Chat, OFF by default; fires even from a backgrounded server tab, streamer-mode-suppressed, rate-limited) |
| Area statuses | `ARUP` | session + drawAreaList | players `[n]`, STATUS color-keyed (LFP green / CASING amber / RECESS blue), `[locked]`/`[spec]`, `CM:` column |
| Server clocks | `TI` | session Timers + overlay chips | five clocks, deadline-based (start/pause/show/hide; ms ≤ 0 stops) |
| Mod login | `AUTH` (+legacy CT line) | session, EventAuth | gated on `auth_packet`; servers without it grant on the exact "Logged in as a moderator." OOC line (append_server_chatmessage) |
| Case announcements | `CASEA`/`SETCASE` | session, drawCasingRow | role bits def/pro/judge/jury/steno; subscribe on Ready + live re-subscribe from Settings; alert = OOC pin + `case_call` + flash. Legacy 2.6–2.9 wire (upstream removed it; tsuserver still ships it) |
| Position list | `SD`, `SP` | session PosList, drawPosCycler | `SD` splits on `*`; `SP` forces our side |
| Evidence | `LE`/`PE`/`DE`/`EE` + MS field | session, drawEvidencePanel | grid + inspector + editor; present arms the NEXT message (wire id +1, 0 = none, courtroom.cpp:2160); incoming presented evidence pops the image + IC log line; images stream as exact URLs (`evidence/<file.ext>` — extension ships in the name, zero probing) |
| Effects | MS Realization/Screenshake/Effects | courtroom fireMessageEffects + viewport | realization flash (white fade), screenshake (decaying sinusoid over the whole stage), 2.8 `fx|folder|sound` field (sound always plays; flash/shake built-ins render; named overlay art needs the theme-effects engine — staged) |
| Frame-synced effects | MS `FrameSFX`/`FrameRealize`/`FrameShake` | buildFrameTriggers + viewport frame report | per-frame sound / realization flash / screenshake fire as the speaker's sprite reaches authored animation frames (bounded `maxFrameTriggers`; kept→source frame map survives decimation; forward-only cursors fire once per playback); outgoing fills from char.ini `[<emote>_Frame*]`; shake/flash still gated by Reduce-motion + the ScreenEffects toggle |
| Custom shouts (2.10) | MS `4&<stem>` | charini [Shouts], shout row | streaming clients can't list `custom_objections/` — the char.ini `<stem>_name` keys are the discoverable source; ▾ cycles picks; receive resolves `custom_objections/<stem>` art+sfx |
| Per-emote audio | MS SFXName/Delay/Looping/Blipname | charini SoundN/T/L + Blips | emotes now send their char.ini sounds; 2.9.1 per-emote blips; **`SFX_DELAY` honored** — the sound fires at wire-value × 40 ms into the preanimation (AO2 `time_mod`), and a preanim message's screenshake fires at that same moment |
| Mute | `MU`/`UM` | session reducer, IC input | server mute of your cid (or `-1` mute-all) sets a persistent "muted" chip and refuses the IC send with a notice (keep-until-echo leaves your line intact); clears on rejoin (SI) / char change (PV), mirroring AO2 `set_mute` |
| Additive text (2.8) | MS `additive` field, `FeatureAdditive` | typewriter accumulator, IC checkbox | an incoming `ADDITIVE=1` line appends to the previous message (the typewriter pre-reveals the prior text, crawls only the appended tail); an **Additive** checkbox shows when the server advertises it; a default-ON **Additive text** setting can disable it entirely (falls back to replace) |
| Desk mods | MS deskmod field, `FeatureExpandedDeskMods` | `deskVisible` phase machine | phase-aware desk visibility (preanim vs talk/idle) for mods 0–3; mods 4/5 hide the pair + zero the speaker offset (decoupled per AO2); outgoing 2–5 clamps down to legacy hide/show when the server lacks the expanded feature so a strict validator can't reject the line |

## Streaming & performance

- **Format autodetect (default ON)**: `<origin>/extensions.json` (webAO
  convention — every server ships its own mix) seeds the per-host learned
  formats on connect, so each asset class costs ~one probe stone cold.
  Manual per-type probing in Settings stays authoritative when autodetect
  is off and covers manifest-less servers. `.webp.static`-style pseudo
  suffixes are normalized away (animation is a payload property here).
- **Desks default to WebP** (Settings → Assets): desk overlays stay on `.webp`
  even when a server's `extensions.json` declares another format for its
  background class (which desks share) — so a PNG-background server can't
  silently drag desks off WebP. Untick "Always use WebP for desks" to let them
  follow the manifest; the per-type format picker covers every format either way.
- **Live-scene self-heal**: if the background, desk, **or a character sprite**
  is evicted from the texture cache mid-message (memory pressure in a packed
  room, or a hover-preview fetch), it is re-demanded at high priority within a
  paced window instead of vanishing to black. The held-frame safeguard (which
  keeps an evicted layer's last frame on screen while it reloads, rather than
  showing black) now covers the **current speaker and their pair partner**, not
  only scenery — so a mid-scene eviction of the on-stage character briefly shows
  its frozen frame instead of the last remaining black-flash case. The rule that
  a single decoded asset may use only a bounded share of the texture budget now
  lives in **one place** (a page can no longer evict the majority of the
  on-screen working set).
- **Cold-load sprite modes** (Settings → Power user → Renderer): what happens while
  a NEW, uncached sprite is still streaming + decoding (the cold-load gap — worse on
  huge art / high ping). **"Keep the previous one"** (default as of v1.55.0,
  webAO-style: the layer's last drawn sprite stays on screen until the new one
  lands), **"Show nothing"** (the original behaviour, the blank flash), or **"Hold
  the message"** (client-AO
  -style: the message stays **off-stage** until its speaker's idle sprite has
  decoded). Hold-previous is pure render path (`render.SpriteLoadMode`): the held
  sprite is resolved by BASE string through the store each frame (never a stashed
  page pointer, so an eviction just falls back to blank + self-heals), and it's done
  **only in the draw path** — `resolve()`/`Update` never see the held page, so the
  preanim lifecycle and packed-room catch-up pacing are untouched. **Wait** is a
  courtroom **message-lifecycle gate** (`Courtroom.SpriteWait` + a `SpriteReady`
  residency callback): the held message parks at the head of the IC queue, its
  sprites are prefetched at HIGH on arming, and a **tunable timeout** (50 ms–30 s,
  default 1.5 s) releases it regardless — so a 404/decode failure can only ever
  *delay* a message, never hang the room; **shouts play instantly** (AO2 parity) and
  **packed-room catch-up always wins** (a backlog never waits). Two strictness ticks
  extend the gate to the **pair partner's idle** and the **preanimation** — and a
  preanim that is *conclusively missing* (packs that fill the field with a dummy
  `-<n>` on every emote) releases the gate on the 404 signal (`NotifyAssetMissing`)
  instead of running out the full timeout (v1.55.0); on a plain timeout the renderer
  falls back to hold-previous so the stage still never flashes.
  Hold-previous has its own knobs: a **max-age** cap on the stand-in (0 = forever)
  and a **diagnostic amber tint** so you can SEE it bridging. A cached scene is
  **byte-identical** whatever the mode, and holding is **0-alloc**
  (`TestSpriteLoadHoldPrevious`, `TestHoldMaxAgeAndTint`, `TestSpriteWaitGate`).
- **Core message timings + queue knobs** (Settings → Power user, every slider's far
  left = the canonical default): **shout bubble duration** (~0.72 s), **preanim wait
  cap** (2.5 s), **IC backlog queue depth** (64; hard-floored ≥ 1 so the queue stays
  bounded whatever the pref says) and **catch-up flash linger** (0 = one per frame).
  A **⟲ Reset ALL power-user options** button (two-click confirm) reverts the whole
  tab — TLS, both Origin overrides, folder casing, renderer modes + knobs, sprite
  mask, timings — while user *data* (saved mod chips, learned formats) survives
  (`TestResetPowerUser` pins the scope).
- **Sprite thumbnail cache** (Settings → Power user, **OFF by default**): the
  opt-in persistent low-quality stand-in store (`internal/assets/thumbcache.go`).
  Every character sprite that decodes leaves a **~1 KB WebP still** (frame 0,
  CatmullRom-shrunk to a tunable height, tunable quality) in `cache/thumbs/` —
  its **own DiskCache instance** beside T3, so thumbs outlive the full sprites
  they stand in for. On a cold sprite the paced heal path requests the thumb, a
  dedicated loader reads + decodes it **off-thread**, the render thread uploads
  it under a **`thumb://` T1 key** (the `theme://` scheme-prefix precedent), and
  the miss path draws it **before** hold-previous (right character > right
  quality; the key is precomputed per base-change so the miss path allocates
  nothing). Every hand-off queue is **bounded and sheds** (thumbs are
  speculative); the store hook rides the Manager's decode completion
  (char-sprite type only, non-blocking); a webpenc-less fallback build simply
  never stores. **Free when off** (one atomic gate per entry point); knobs:
  height (32–160 px), quality (5–60), Clear button; the nuke reset disables it
  but keeps the stored thumbs (Clear is deliberate). Pinned by
  `TestThumbCacheRoundTrip`, `TestThumbStandIn` (draw order + 0-alloc) and
  `TestThumbDefaultsPinned` (config↔assets constants).
- **Frame limiter + event-driven renderer** (the GPU-burn fix; Settings → Power
  user → "Frame rate & GPU"): the main loop paces to `App.FramePace(focused)` /
  `App.HardCapBudget(focused)` instead of the monitor. **Shipped defaults
  (v1.55.0): active = ∞ / vsync** while you interact (`NoteInput`, an
  input-responsiveness hold measured in FRAMES, `NoteMotion`) or anything animates
  (`wantsFullRate` / `NoteAnimating`: message ceremony/queue, shake/flash,
  transmitted sprite motion, replay/maker/export/voice, reaction floats, toasts,
  the pinned second courtroom, the perf HUD, always-animating FX), **idle = off** —
  a static screen renders *nothing at all* (near-zero GPU) — and
  **background/unfocused = 5 fps**; minimized draws nothing. Each is a slider (the
  ∞ / off sentinels included), nuke-scoped. The active and background caps are
  INVIOLABLE ceilings, slept uninterruptibly, so an input flood — above all mouse
  motion, which streams an event every few ms — can never bust them
  (`HardCapBudget`). vsync stays on for tear-free presents but is no longer the
  throttle (165 Hz panels burned GPU idle; some windowed present paths never
  blocked at all). A LIVE stage animation keeps its own frame schedule
  (`Viewport.NextAnimDue`): the pacer wakes exactly at the next flip.
  With the **event-driven renderer** (default ON; the kill switch reverts to the
  classic sleep-paced loop), a static screen stops redrawing *entirely* between
  real signals — input wakes an OS-level event wait instantly, network packets and
  finished decodes push a wake event (`PushWake`), and a blinking caret / ticking
  clock / due animation redraws exactly one frame when it's scheduled
  (`NextWakeDelay`, `NoteDeadline`). idle = off then means *genuinely zero* redraws
  until something changes. A **per-event mouse redraw** (default ON as of v1.55.1)
  renders one frame per motion event instead of holding full rate while the cursor
  moves. **v1.55.2** hardened the census that decides "is anything moving": a
  clock-driven on-screen surface — animated theme chrome, a looping hover sprite
  preview, animated chatbox Text FX — reports through `NoteAnimating` from its DRAW
  site, a self-clearing per-frame census, never a bare state flag that can outlive
  its draw. That fixed a sprite preview left open across a screen switch, which
  latched the pace at the active cap until restart, and idle Text FX that animated
  without keeping frames coming. `TestFramePace`, `TestHardCapBudget`,
  `TestMotionGrace`, `TestAnimatedTextAnimates` and the `TestSkipFrame*` set pin the
  transitions.
- **Audio independent of the frame rate** (v1.55.1): while a message types, the
  courtroom advances — and plays its blips — at a fine ~60 Hz cadence even when the
  present rate is capped low, so audio never batches to the frame rate ("blips only
  once per screen refresh at a 1 fps cap"). It's threaded through the same two-tier
  split-sleep as the caps, so the inviolable hard-cap floor stays uninterruptible;
  incoming SFX and pings wake the parked loop and play the instant they arrive.
  `App.AudioActive` / `AudioPaceActive`, `Courtroom.AudioActive`. SDL_mixer stays
  on the render thread — no separate audio thread (rule §17.1). Pinned by
  `TestAudioPaceActive` + `TestAudioActive`.
- **Network / decode / texture / pacing knob suite** (Settings → Power user,
  each with an in-depth WHAT-IT-DOES): 404 negative-cache TTL
  (`NewClientNotFoundTTL`, restart-applied), per-host adaptive-deadline
  multiple (`SetAdaptiveLatencyMultiple`, live), decode-downscale percent /
  off-switch (`config.EffectiveSpriteCap` → `DecoderPool.SetSpriteCap`, live
  for new decodes), T1 texture budget (`NewTextureStoreBudget`,
  restart-applied), speaker-swap **crossfade** (render: the new sprite
  alpha-ramps over the old — `animState.fadeLeft`, armed in `syncAnim`, ticked
  only while resident so a cold load never eats the fade; Reduce-motion zeroes
  it; `TestSpeakerSwapCrossfade` pins blend/completion/0-alloc), thumbnail
  store **byte budget** (oldest-mtime auto-prune on the encode worker;
  `TestThumbPruneBudget`), and the **cold-load profiler line** in the debug
  overlay (fetch TTFB / decode / upload EWMAs — `Client.AvgTTFB`,
  `DecoderStats.AvgDecode`, `TextureStore.AvgUpload`).
- **Setting presets** (Settings → Data): named full-settings bundles under
  `presets/<name>.json` — `SavePreset` rides the password-stripping export,
  `ApplyPreset` rides the validated atomic import (restart-applied), capped at
  16, names sanitized (`TestSettingPresets`). User data — the power-user nuke
  never touches them.
- **WS-handshake Origin override** (Settings → Power user → Origin overrides): the
  connection-side sibling of the asset Origin — sent on the WebSocket upgrade for
  the rare server that allowlists only its own web client's origin. Blank (default)
  = no header, byte-identical handshake (`TestDialWSOriginHeader`).
- **Viewport sprite mask** (Settings → Power user, **ON by default**): character
  sprites are clipped to the stage rect, so a big **pair / reposition offset** can't
  spill a sprite over the chatbox or the log. Only the sprite draws are clipped (the
  bg/desk already fill the stage) and only when nothing else owns a clip, so
  screenshake and the reflection's own clip are untouched; **off → no `SetClipRect`
  at all → byte-identical** (`TestSpriteMaskClipsToStage`, 0-alloc with the mask on).
- **Full-character sprite preload** (#127, Settings → Assets, **OFF by default**):
  on a character load the default grabs only the first few emotes' idle sprites;
  with this on, AsyncAO pre-grabs the character's **whole** set — every emote's
  idle *and* talk (with the bare-spelling fallback) — at **LOW priority**, so
  switching emotes is instant. Speculative, so backpressure **sheds** it before
  any live HIGH fetch and singleflight collapses anything already cached; it just
  costs more bandwidth + cache up front, hence opt-in. Keybind `Ctrl+\`.
- **Connection-quality chip** (#128, Settings → Assets, **OFF by default**): a tiny
  signal-bar icon (bottom-left) showing the WebSocket round-trip — four bars,
  green / amber / red by latency — with the **exact ms on hover**. A single
  background goroutine pings the **active** connection (`Conn.Ping` → a WS ping/pong,
  safe alongside the read loop) and stores the RTT atomically; the frame loop
  starts / stops / retargets it as the active conn changes (connect, tab switch,
  reconnect, disconnect) or the toggle flips. **Off → no goroutine at all**, so it's
  genuinely zero-cost by default; on, the chip draw is 0-alloc (Fill bars + a cached
  tooltip rebuilt only when the ms moves). Keybind `Ctrl+\``.
- **CM / mod dashboard — server-software-aware** (#130, **Extras → Mod / CM**, or
  `Ctrl+/`; a standalone panel, never bloats the player list): one place to moderate
  that **builds each command for the server you're actually on**. AO moderation is
  OOC slash-commands whose syntax **differs per server software**, so a `/ban` copied
  from another server silently fails. The dashboard detects the software from the ID
  packet on join (`KFO/tsuserver`, `Athena`, `Nyathena`, `Akashi`, `Whisker`) — or
  you override it with **Change** — then **Ban** / **Kick** open a box with a
  duration picker, a reason field, and a **live preview of the exact command** before
  it sends, so you see precisely what goes out. It picks the right identifier per
  software (IPID for KFO/Akashi, UID for Whisker, `-i`/`-u` flags for Athena/Nyathena);
  when a server bans by **IPID** (mod-only) and it hasn't surfaced yet, the box says so
  and offers a one-click **/getarea** fetch rather than a dead button. The duration
  picker's preset chips are joined by **savable custom durations** ("45m", "2 days",
  "perma" — validated via `courtroom.CanonicalBanDuration`, stored as canonical short
  tokens, rendered in each software's own format by `BanCommandToken`, Edit → × to
  remove; the bulk box shows them too), alongside the **savable reason chips**. The target is
  keyed by **UID** (never a roster row index) and **frozen** when the box opens, so a
  join/leave can't repoint a ban at the wrong person. Room (**CM**) controls — claim /
  release CM, lock / unlock, kick-from-area — appear when you're CM (Claim sits outside
  that gate so you can become CM), and a per-software **command reference** is always
  shown. The ban/kick/CM *syntax* is shared between Athena and Nyathena (byte-identical
  on the wire — verified from both servers' sources); Nyathena is a distinct family only
  for its **richer** area toolkit + auto-detection (it must announce `Nyathena` in its
  ID packet, else it reads as Athena and still works). **Closed by default = zero cost**;
  detection + command building run only while the panel draws (never per frame). The
  per-software builders live in `internal/courtroom/modcommands.go` (unit-tested against
  every format). Keybind `Ctrl+/`.
- **High-res art is downscaled at decode, in high quality** (automatic, no
  setting): packs increasingly ship **huge** sprites (Skrapegropen's are
  ~2000×2000 px), and shrinking a 2000 px source into a ~700 px viewport with
  the GPU's single-pass bilinear *undersamples* it — visibly **less smooth** than
  a browser's mipmapped downsample (the exact gap players spotted vs webAO). So
  any sprite/background taller than the **display height** is **CatmullRom**
  -downscaled **once, in the decode pool** (off the render thread) to a near-
  display-size texture; the per-frame `CopyEx` then works at a gentle ratio and
  looks sharper. It's a **net speedup**: a smaller cached texture samples faster
  and uses far less VRAM (a 2000² RGBA frame is ~16 MB → ~4 MB at 1080p), easing
  the 256 MiB budget. Downscale-only, so already-small art is untouched — and the
  WebP/AVIF *decode* stays exact; this is purely the resample that used to fall
  to the GPU every frame.
- **Missing-asset banner is opt-in** (Settings → Assets, **default OFF**): the
  red on-screen warning naming an asset that failed every format is off by
  default (it was noisy on sparse-pack servers); the failures still stream to
  the **debug overlay** (the dedicated failure log) regardless.
- **Decode-failure backoff + corrupt-cache purge**: a payload that downloads but
  won't *decode* (a corrupt/truncated file — e.g. a mangled TLS stream — distinct
  from a 404, which the network tier already caches) goes into a short-lived
  negative cache, so one bad asset isn't re-fetched + re-decoded every retry
  interval (it can't render regardless). The failure logs **once** per window
  instead of flooding, and a transient failure still recovers after it. On a
  **non-transient** decode failure the poisoned bytes are now **purged from the
  memory (T2) and disk (T3) caches** by their full fetch URL (the disk delete
  routes through the single async writer, no synchronous I/O on the render path),
  so the next demand refetches clean bytes instead of re-promoting the same bad
  blob from disk on every retry — across sessions — until the whole cache was
  cleared by hand.
- **Learned-format export/import**: `learned-formats.json` beside the exe;
  one player's warm state seeds another's.
- **AVIF**: `.avif` probe format; `ftyp avif/avis` sniffing, libavif CGO
  decode (stills + animated sequences), Settings chip like every format.
- **Typing-driven speculation**: picking an emote prefetches its idle/talk/
  preanim/SFX at LOW priority; the Markov predictor now also learns
  per-character **emote chains** and warms the predicted next speaker's
  predicted next emote. The predictor now warms **both the idle and talk sprite
  through the full spelling chain** (`(a)`/`(b)`/bare), so a bare-named pack no
  longer 404s every prediction in all formats — and a speculative miss no longer
  fires a missing-asset warning (real demand warnings are unchanged; the 404
  cache and singleflight are untouched, so nothing re-probes).
- **Per-server pre-warm**: last-used character + last-seen background are
  remembered per server (ws URL key, capped) and prefetched on Ready.
- **Master-list ETag**: lobby Refresh sends `If-None-Match`; unchanged
  lists cost a 304 and zero payload bytes.
- **HTTP/2**: on for https hosts (ForceAttemptHTTP2 + TLS session cache);
  the manifest fetch doubles as the per-host connection pre-dial.
- **Progressive animated decode**: frame 0 of an animated WebP/AVIF/APNG/
  GIF shows after one frame-decode; the full set replaces it when ready.
- **Adaptive per-host deadlines**: a host's TTFB EWMA caps its request
  deadlines (8×, clamped) so a dying mirror can't pin the fetch lane.
- **Backoff counts once per window**: a brief origin blip no longer freezes a
  host's assets for the full 30 s. The 16 fetch workers timing out concurrently
  used to push ~16 failures in one burst and saturate the backoff formula at the
  cap instantly; failures now count **at most once per backoff window** (inside
  the current window the delay extends without incrementing), so a 2-second CDN
  hiccup stays at the first-failure tier while a genuinely-down host still climbs
  across windows.
- **Zstd disk cache** (Settings, default off): self-describing zstd
  blobs, kept only when smaller — sprites stay raw, INIs shrink 2–4×.
- **Label texture atlas**: UI labels pack into ≤4 shared pages — the 4K
  char grid's ~1200 labels cost a handful of binds instead of 1200.
- **Frame pacing**: dt clamp after stalls + a 144 Hz zero-missed-reveal
  typewriter gate in CI.
- See docs/PERFORMANCE-ROADMAP.md for designs and measurements.

## Themes

- Apply pipeline loads **27 theme textures** off-thread (chatbox skin,
  splashes, badge, 22 HP bar states) plus the courtroom/penalty sound
  names, publishes gen-stamped (a stale load can never clobber a fresh
  pick), self-heals T1 eviction, and reports a verdict line ("Theme X
  applied: chatbox.png + N images + M INI keys").
- Chatbox skin candidates `chat` → `chatbox` → `chatblank`
  (courtroom.cpp:3328); every pasted path shape normalizes (root, themes\
  itself, single theme, quoted Copy-as-Path).
- **Theme picker** (Settings → Theme): a **searchable-scroll dropdown** jumps
  straight to any theme in your collection (#86) — no more clicking **`< >`**
  dozens of times — with the `< >` buttons kept for fine stepping and a live
  "(N found)" count. The default-jump dropdown scrolls, so a big themes folder
  is one click away.
- **Stock "default" is always selectable** (#87): theme packs ship their own
  `themes/default`, and because a custom themes folder is searched first, that
  pack's default used to **shadow the built-in** one — so once you set a folder,
  picking "default" loaded the custom look and the stock default was
  unreachable. The stock **default** now resolves against the app directory
  only, so it's always there; custom *named* themes still use your folder (and
  still fall back to its default for missing keys).
- Theme text colors apply **only over their own skin** — on the flat
  fallback panel the client's readable defaults win (black-on-dark fix).
- **Ink readability guard**: at load time the theme's message/showname
  colors are luma-checked against the actual decoded chatbox pixels;
  ink with no contrast against its own skin (real themes ship dark ink
  on dark skins) is dropped for the client default, with a debug-log
  verdict. Choosing White in the IC color dropdown always reads.
- **AO2 inline colors** (the real thing — interoperable): the same bare
  delimiters AO2/webAO use now color your text, so a colored message you send
  renders in color on **every** AO2 client, and colored messages from them
  render here too. The stock character table (from AO2-Client's default theme)
  is: **`` `text` `` green**, **`~text~` red**, **`|text| orange`**,
  **`(text)` blue**, **`ºtextº` yellow**, **`№text№` magenta**, **`√text√`
  cyan**, **`[text]` gray**. The toggle pairs (`` ` ~ | º № √ ``) are *consumed*
  (they don't show); the bracket pairs `( )` and `[ ]` **stay visible** and take
  the color, exactly as in AO2. Spans nest (an inner color returns to the outer
  one when it closes), and an unterminated span runs to the end of the line.
  Because these characters ride the wire untouched, this is genuinely
  cross-client — unlike the render-only scheme below. **This changes how a
  literal backtick, tilde, pipe, `º`, `№`, `√`, or a matched `( )` / `[ ]` pair
  renders in chat: they now color text. Type `\` before one to keep it literal**
  (`` \` `` → a literal backtick), the same escape AO2 uses.
- **Select-and-color from the dropdown** (AO2 parity): highlight some words in the
  message box, then pick a color from the IC color dropdown — it wraps just that
  selection in the matching AO2 color (Ctrl+Z undoes the wrap), exactly like AO2's
  own color dropdown does with a selection. With nothing selected, the dropdown
  sets the whole-message color as before. (Colors with no AO2 markup — the default
  white — and the AsyncAO-only extras — extended, rainbow, random, custom hex —
  don't wrap: they only ever set the whole-message color.)
- **AsyncAO-native inline codes + bold/italic** (type them in your message):
  `\c1`…`\c8` switch the text color mid-sentence (1 green, 2 red, 3 orange,
  4 blue, 5 yellow, 6 pink, 7 cyan, 8 gray), **`\cr` is rainbow** (each letter a
  different color, flowing across the line), **`\c<letter>` / `\c#RRGGBB`** are
  the extended and free-hex colors, and **`\b` / `\i` toggle bold / italic** (they
  nest and combine with colors, and with the AO2 markup above — both can appear in
  one message). Write `\\` for a literal backslash; any other `\x` is left as-is,
  so ordinary text and file paths aren't eaten. Unlike the AO2 markup, this scheme
  is **AsyncAO-native and render-only**: the codes are stripped from the wire, so
  they color only on another AsyncAO client (extended/hex colors carry a
  nearest-standard fallback so stock clients still see a sensible color). Colored
  messages type out normally — each color is its own span, revealed letter by
  letter with the usual zero-cost reveal. The IC *log* and blankpost/callword
  matching all see the clean text (every code stripped, identical to the chatbox).
- **Inline screen-effect codes** (v1.55.7, type them in your message): **`\s`**
  shakes the screen, **`\f`** flashes (realization), **`\n`** is a line break, and
  **`\p`** pauses the text crawl (`\p500` for 500 ms; a bare `\p` waits a second) —
  AO2-Client parity, fired the instant the reveal reaches the code as the line
  types out. The codes ride the wire untouched, so **other AO2/webAO players see
  the effects too**; a skip/recall drops any the crawl didn't reach (no burst of
  shakes), and the codes never show as text (the IC log and chatbox both strip
  them). Turn the shake/flash off with **Settings → Stage & viewport effects →
  "Enable screen effects"** (on by default; the accessibility "Reduce motion" also
  suppresses them, and the effect sound still plays either way). Custom
  `effects.ini` overlay effects are the next step (see `docs/ROADMAP.md`).
- **Random / rainbow message colour** (M61, Settings → General, both OFF):
  **Random colour** picks a fresh palette colour for each IC message you send
  (the standard TextColor field — every client sees it); **Rainbow** prefixes
  `\cr` so your text cycles the palette per letter (renders on clients that read
  inline colour; rainbow wins if both are on). Applied at send (`funColor`), so
  zero render cost. Both are **also pickable right in the IC colour dropdown**
  (#79): **Rainbow** and **Random** sit at the end of the palette list, so you
  switch modes the same way you pick a colour — no trip to Settings — and the
  swatch previews the active mode. The dropdown and the Settings checkboxes are
  the same setting, so they stay in sync. (The extended list is built once, so
  the IC input row stays allocation-free.)
- **Extended chat colours** (#98): the IC colour dropdown adds **Purple, Magenta,
  Teal, Lime, Gold, Coral, Sky, Lavender** beyond AO's nine, in **both** the
  classic and themed input rows. These ride as inline `\c<letter>` markup (the
  same mechanism as Rainbow's `\cr`), so **another AsyncAO user sees the exact
  colour**, while the message's wire `text_color` carries the **nearest standard
  colour** as a fallback — so a stock AO2 client still shows a sensible colour and
  never breaks. The AO wire `text_color` field itself stays 0–8 (strict clients
  never receive an out-of-range value). Picked like any colour (mutually exclusive
  with Rainbow/Random); resolved once at send (`funColor`) and once per message at
  raster build, so there is **zero per-frame cost** and the render loop stays
  0-alloc. The parser gate set (`courtroom.ExtColorCodes`) and the render palette
  are pinned equal by a test, so the two AsyncAO clients can never disagree on
  letter→colour.
- **Sprite colour FX** (Settings → General, all OFF by default): a render-side
  colour wash over the on-stage characters (speaker **and** pair) — pure **local
  eye-candy**, nothing on the wire, nobody else sees it. A `SetColorMod`
  bracketed around the existing sprite blit (alpha untouched, so the transparent
  cutout stays transparent), with a clutch of knobs:
  - **Rainbow** cycles the hue, with a **Speed** slider (the rotation period)
    and a **Vividness** slider (the channel floor — subtle tint ↔ vivid neon;
    `SetColorMod` multiplies, so a higher floor keeps more of the art's own
    brightness instead of crushing it to a silhouette).
  - **Desync pair** offsets the two characters half a cycle apart, so they show
    different hues at once.
  - **Solid colour tint** washes sprites in one fixed `RRGGBB` colour instead of
    a cycle (rainbow wins if both are on).
  - **Neon glow** switches the blend to additive (`BLENDMODE_ADD`) so the tint
    **adds light** and the character glows — it becomes a translucent neon ghost
    (the room shows through, by design).
  - **Different hue per character** offsets each character's hue by a hash of its
    name, so several characters on stage cycle to **different colours at once** —
    a packed room turns into a riot.
  - **Wobble** sways the sprites gently and continuously, and **Spin** rotates
    them slowly — pure-math motion off a free-running clock, independent of the
    colour wash (each takes the clock modulo its own period, so no wrap glitch).

  The hue clock lives on the viewport and everything is pure integer/float math
  (the rainbow period is hard-floored above zero so the per-frame modulo/divide
  can never panic). The frame **still allocates zero with every effect on at
  once** — rainbow + glow + desync + per-char hue + wobble + spin, and the solid
  wash + glow + motion, are both pinned by `TestRenderFrameRainbowZeroAllocs` —
  and with it all off there is **no cost at all** (the blit is byte-identical:
  no colour-mod, zero offset, zero angle). The App mirrors the prefs onto the
  viewport once per frame (a few uncontended RLocks, no caching layer).
- **Sprite Style — transmitted, cross-AsyncAO** (#103, **Extras → Sprite Style**):
  the **transmitted cousin** of the local wash above. You style **your own**
  character — **recolour** (tint, fun-colour presets + RGB), **opacity** (a ghost,
  floored so nobody can post an invisible sprite), **neon glow**, and **wobble /
  spin** — and **every other AsyncAO player sees it on your sprite**, while
  **AO2 / webAO see a normal, unstyled character** (and unaffected chat text). The
  style rides as an **invisible zero-width marker** appended to your message text
  (`internal/courtroom/spritestyle.go`): the message-text field is the only
  channel that survives an arbitrary server to other clients (the same reason
  `\cN` colours ride there), and zero-width keeps standard clients truly
  unaffected — a 5-byte payload (R,G,B,opacity,flags) with a **benign failure
  mode** (a server that mangles it just yields no style, never a corrupted
  message). It's decoded + stripped before the typewriter, blankpost test, IC log,
  and callword matcher, so the visible text is always clean. The renderer reuses
  the **exact same** `SetColorMod` / `SetAlphaMod` / `BLENDMODE_ADD` bracket
  per-layer, so a received style is **0-alloc** (pinned by
  `TestTransmittedSpriteStyleZeroAlloc`) and an unstyled sprite is byte-identical.
  Sticky (persisted); shows in replays and the GIF/WebP/video export for free (it
  rides in the replayed text). The picker is a **floating, draggable, non-blocking
  panel** (#104, the **Sprite Style box** — Extras → Sprite Style) built on the
  same surface as the Extras / Favourite-Emotes boxes, so you recolour **on the
  fly while still chatting** (presets + a live swatch, a Fade/opacity slider, and
  glow / wobble / spin), not a modal that takes over the screen. **Viewer
  controls:** Reduce-motion strips a received style's animation entirely — wobble,
  spin, custom motion paths, the hue-cycle rainbow, and glitch — and **Settings →
  General** has a "**Hide other players' sprite styles**" off-switch. *(Per-pixel
  effects — invert / grayscale — are a planned follow-up that builds cached
  variant textures.)* **Style presets (#126):** the box's bottom section saves the
  current look — **style + text colour + the selected emote (by name)** — as a named
  **mood**; click to apply, or **bind a bare key** to it (a key-capture flow like the
  showname keybinds) and **swap moods hands-free** in the courtroom. Bounded, persisted;
  the bind machinery keeps one key → one preset.
- **Character profile** (#101, **Settings → General → Your profile**, OFF by
  default): a small **card** — name, **pronouns**, a one-line **tagline**, a short
  **bio**, and **URLs** for art / theme-song — that other AsyncAO players will see
  on the list, while AO2 / webAO are unaffected (they just see the normal list).
  **Configurable**: an **Enable** master switch and a "**Show my profile on the
  player list**" toggle. Every field is **length-clamped** (the pref file stays
  bounded), and the editor shows a **live preview** of the card. **Cross-client
  (slice 2):** your **pronouns + tagline** ride an invisible **zero-width marker** on
  your IC messages (the same channel as the transmitted sprite style), so other
  AsyncAO players see them on your card **after you speak** — sent **only on change**,
  so the marker rides at most your first post-join message. Standard AO2 / webAO
  clients render nothing and are unaffected. The bigger fields (**bio, theme song,
  art image**) stay **local** — too large for the message channel — so the remote card
  shows name · pronouns / tagline only. *(A richer hosted version with the full card
  is a possible later slice.)*
- **Player-list status flags** (#M1, the **"Status:"** button in the Players-tab
  header): set yourself **AFK / Busy / Writing / LFRP** (cycle the button) and other
  AsyncAO players see a **coloured chip** on your roster row. Like the profile it rides
  the invisible **zero-width** IC channel, **send-on-change**, so AO2 / webAO see clean
  text and the chip updates for others **after you next speak**. Your own chip shows
  immediately. Standard clients are unaffected; **0-alloc** on the render path (a plain
  map lookup per row, chip drawn only when a status is set).
- **Animated chat text — transmitted, cross-AsyncAO** (#M5): make spans of your
  IC message **shake**, **wave**, or **rainbow**. **One-click** from the **IC-bar
  emoji button → Text FX strip** (wraps your whole message), or per-word with
  inline **`[shake]…[/shake]` / `[wave]…[/wave]` / `[rainbow]…[/rainbow]`** markup.
  Every other AsyncAO player sees the animation; **AO2 / webAO see the plain
  message** — the markup is stripped before send and the effect spans ride an
  **invisible zero-width frame** on the same channel as the sprite style / profile
  / status (told apart by a magic byte, so the four coexist on one message). The
  span indices align with the receiver's visible text because the effect tags and
  `\cN` chat markup use disjoint characters (the two strips commute). **Performance:
  plain messages are byte-identical to before** — only a message that actually
  carries effects takes the per-glyph path, which renders each glyph to a **white
  texture once** and then **displaces + tints it per frame** with cheap scalar
  math (no re-rasterise), pinned **0-alloc** by `BenchmarkAnimatedTextDraw` /
  `TestAnimatedTextDrawZeroAllocs`. The chatbox marks the frame-pacing census
  (`AnimatedText.Animates` → `NoteAnimating`) while a moving effect is on screen,
  so the motion keeps playing at a low or zero idle frame rate instead of freezing
  between redraws (v1.55.2); a gradient-only band and Reduce-motion render static
  and hold no extra frames. **Viewer control:** Reduce-motion pins rainbow
  to a static hue and stops all displacement (the photosensitivity floor).
  **Colour composes:** an inline `\cN` colour (or the wire `text_color`) rides
  *with* the motion, so you can send a **red shaking** word (the rainbow effect
  still overrides colour per glyph). *(Emoji/CJK per-glyph fallback and bold/italic
  inside an animated message are still single-base — a planned follow-up.)*
- **Sprite outline / drop-shadow / glitch — transmitted** (#8 / #13, **Extras →
  Sprite Style**): three more **transmitted** sprite effects on the same invisible
  channel as the style above. **Outline** draws a white silhouette border,
  **drop-shadow** a soft dark offset, and **glitch** a chromatic-aberration shimmer
  with an occasional jolt — every AsyncAO player sees them on your sprite; AO2 / webAO
  see a normal character. Because the style's flags byte was full, these ride a
  **second flags byte appended only when one is set**, so the wire stays the original
  length for a plain style and an **older AsyncAO client still decodes the rest** (the
  version stays 1 — a bump would make old clients drop the whole style). Render builds
  **one cached white silhouette** variant, `SetColorMod`-tinted and offset-blitted
  behind the sprite (8 directions for the outline, 1 for the shadow) and overlays
  red/blue ghost copies for the glitch; the scratch rects live on the viewport and both
  colour **and** alpha mods are restored on the shared page, so a received effect is
  **0-alloc** and an unset one is byte-identical.
- **Real reactions — transmitted, cross-AsyncAO** (#2, the **React button** on the IC
  bar): react to the last message with an emoji from a fixed palette; every AsyncAO
  player sees it **float up over the stage** (rise + fade), while AO2 / webAO see clean
  text. AO has no immediate side-channel (the wall that killed an earlier reactions
  attempt), so the reaction **piggybacks on your next IC message** as an **invisible
  zero-width frame** (a new magic byte, alongside style / profile / status / effects).
  It names its target by a **content-stable hash** of the message's character name +
  clean text — computed identically on every client — so the float anchors to the right
  message everywhere; a stray / late-join reference simply matches nothing (benign). The
  active-float ring is **bounded** and the overlay is a **0-alloc early return when
  nothing is floating** (and 0-alloc with floats active — pinned by a dedicated ui test,
  since the overlay isn't on `render.Viewport`). The target is **snapshotted when you
  open the palette**, so a message landing mid-pick can't shift it; the pick **rides your
  next message** and clears (your own reaction floats when that message echoes back, so
  there's no separate local echo). Drawn in **both** the classic and themed courtroom.
  **Viewer opt-out:** Settings → "Hide other players' emoji reactions" (default OFF =
  show), like received sprite styles.
- **Inline emotes** (#18): Discord-style **`:joy:` / `:fire:` / `:thumbsup:`** shortcodes
  render as a **colour emoji** in **both the IC log and the live chatbox** (~31
  built-ins). **No wire** — the shortcode text travels **literally**, so AO2 / webAO show
  the readable `:joy:` while AsyncAO substitutes the emoji at display time through the
  existing colour-emoji path (no new render machinery). The parser
  (`courtroom.ExpandInlineEmotes`, resolver-param so the chatbox path in the courtroom
  reducer shares the ui-side registry) is **known-shortcode-only**, so a URL's `http://`,
  a `12:30`, or an unknown `:foo:` is never touched, and it's **0-alloc** when a line
  carries no shortcode. The chatbox substitution happens in `begin()` **after the effect
  spans are decoded and before the typewriter starts**, so the per-rune reveal, the
  blankpost test, and the raster all consume the same text — and it is **gated off when
  the message carries `[shake]`/`[wave]` spans** (whose wire indices were measured over
  the literal text). Expansion is **display-only**: the input box still shows `:joy:` as
  you type, and the wire `msg.Message` stays literal, so the reaction ref and recordings
  are unaffected. *(Custom image-URL emotes — an actual fetched picture instead of an
  emoji — are a possible later slice.)*
- **Local viewport FX** (Settings, all **OFF by default**, pure local eye-candy —
  nothing on the wire, nobody else sees it): a batch of viewer-side polish, each
  **0-alloc** when on and **byte-identical** when off (pinned per feature, and
  `BenchmarkRenderFrame` stays 0/op). **Shout screen-punch** pops the stage scale when an
  interjection lands (composes with the existing screenshake); **per-character chatbox
  tint** blends the speaker's name-colour hue into the chatbox so you can tell who's
  talking at a glance; **post-processing** overlays a **vignette / scanlines / film
  grain** (each a cached texture in one stretched blit — the practical "GPU-accelerated
  shader" path for SDL2's 2D renderer, which has no fragment-shader entry point, #47);
  **entrance slide-in** drifts a new speaker's sprite in from the side on the first line;
  **depth-of-field** soft-focuses + dims the background behind the speaker (a multi-offset
  smear, since go-sdl2 exposes no per-texture scale mode for a true blur); **speaker
  spotlight** (#121) dims the non-speaker layers — the pair partner + the desk — toward
  shadow so the talker pops, with a Dim slider (the background is left to depth-of-field, so
  the two compose without double-dimming); and **idle breathing** (#122) gives every static
  sprite a gentle vertical bob + breathing scale-pulse so it feels alive, with Amount / Speed
  sliders and per-component (bob / scale) toggles — pure math off the free-running clock,
  suppressed by Reduce-motion like wobble/spin; and **glass-floor reflection** (#123) mirrors
  the characters below the floor line as a flipped, faded copy (one `CopyEx` with
  `FLIP_VERTICAL`, clipped to the stage, drawn before the desk so it occludes naturally), with
  an Opacity slider. Each has a **rebindable keybind** (Settings → Controls; the Ctrl+letter
  space is full so the viewer-FX toggles default to the free symbol keys — spotlight `Ctrl+[`,
  breathing `Ctrl+]`, reflection `Ctrl+;`).
- **Particle weather** (#124, Settings → General, **OFF by default**): an ambient overlay of
  **snow / rain / sakura / embers** drifting over the scene. A fixed, bounded particle pool
  (§17.4) drawn from **one cached soft-dot texture**, tinted + shaped per weather (rain
  stretches into streaks, embers rise + glow additively + fade), with an **Intensity** slider
  that scales the active count. Positions are viewport fractions (resolution-independent);
  motion + respawn are pure math off a per-particle PRNG, so the overlay is **0-alloc** per
  frame (`TestParticleWeatherZeroAlloc` covers the uniform-alpha and the fade/additive paths)
  and **byte-identical when off** (early return). Confined to the stage with the same
  don't-stomp-the-zoom-clip guard as the reflection. The picker cycles None → Snow → Rain →
  Sakura → Embers; keybind `Ctrl+'` cycles it hands-free.
- **Animated theme art plays**: chatbox skins, `btn/` buttons, screen
  backdrops, HP bars, and the settings preview step their frames on a
  per-apply animation clock (`pageFrameLoop`) instead of freezing on
  frame 0 — splashes/badge already animated. The hover sprite preview
  loops its idle too, keeping frames coming through the same `NoteAnimating`
  census while it's drawn; it's torn down on any screen switch so a box left
  open across one can't latch the pace or eat clicks/scroll underneath (v1.55.2).
- **courtroom_stylesheets.css works** ("the css stuff"): a QSS-subset
  parser extracts the palette (QWidget/QPushButton/QLineEdit/etc colors;
  `#rgb`/`#rrggbb`/Qt `#aarrggbb`/`rgb()`/named) and recolors the whole
  client kit — panels, buttons, text, accents — restoring the stock
  palette exactly on theme switch. List-widget backgrounds are treated
  as refinements, not the window look.
- **Qt-geometry sanitizing**: AO2 themes relied on Qt clipping children
  at the fixed window edge. Scaled rects now clamp into the stage
  (shift inward; shrink only when oversized), the 11037 hide convention
  applies to both axes, degenerate sub-6px rects are rejected, shownames
  clip inside the chatbox, button labels clip inside their rects, and
  themes that stack the IC/OOC logs on one rect (AO2's ooc_toggle
  pattern) render as tabs instead of drawing on top of each other —
  nothing flies off screen, whatever the theme author did.
- **Theme fit modes** (Settings → Theme): an AO2 theme has a FIXED design size,
  so scaling it to a differently-shaped window leaves bars. Pick how it fills:
  **Stretch** (default — edge-to-edge, slight distortion, the webAO behaviour),
  **Letterbox** (keep exact proportions, theme-coloured bars), **Crop** (scale up
  to fill, overflow runs off-screen), or **Custom** (a manual **zoom + pan** to
  crop the theme to taste). **Custom** opens a **big interactive preview** shaped
  to your window — **drag to pan, scroll to zoom** (or use the sliders) — so the
  crop you see is the crop you get; the live courtroom re-fits as you go.
  Per-axis scaling is folded into the geometry cache, so it stays a resize-only
  rebuild — zero per-frame cost.
- **Plain lobby** (Settings → Theme, **ON by default**): the lobby/server list
  keeps AsyncAO's readable backdrop instead of the theme's `lobbybackground` —
  an AO2 lobby image (built for AO2's own list) often renders our server list
  unreadable. Untick it to use the theme's lobby; the **courtroom still uses the
  theme** either way, so you keep the rest of the theme for free.
- **"For server owners" help screen** (the button beside the legacy-servers
  notice in the lobby): explains how to get a raw-TCP-era server speaking
  WebSockets (and WSS for the green lobby tier), then a **scrollable catalog of
  every modern AO2 server software**. Each entry shows **WS / WSS / Players** as
  colour-coded chips — **WS** a yellow ✓ (all of them), **WSS** a green ✓ when
  the server terminates TLS itself or a red ✕ when it needs a reverse proxy, and
  **Players** a green ✓ (native live player list), a yellow ✓ "plugin" (added by a
  plugin, e.g. Whisker), or a red ✕ — with a legend. Servers are grouped **base →
  fork**: each fork (witches-akashi-party → Akashi, Nyathena → Athena, KFO-Server
  & tsuserverCC → tsuserver3) is indented under its upstream with a drawn
  connector line and a "fork of X" tag. Every project gets a **~7-sentence
  description** so an owner knows what they're getting, a full **contributor
  credit list** pulled from its git history, and clickable repo links. Covers
  Akashi, witches-akashi-party, Athena, Nyathena, Whisker, tsuserver3 (flagged
  deprecated), KFO-Server, tsuserverCC, Ferris-AO, Alibi and Kagami.
- Settings shows a live chatbox preview with the applied colors.
- **Floating Extras box** (the "★ Extras" button / **Ctrl+X**): a legacy AO2
  `courtroom_design.ini` has no element keys for AsyncAO's own features
  (Wardrobe, Jukebox, Background, Theater, Pair, Evidence, Login, Settings, …),
  so when a theme drives the courtroom layout they'd be unreachable. Pressing
  **★ Extras** (pinned bottom-left) — or **Ctrl+X** — opens a **non-blocking
  floating box** of all of them: the courtroom, chat and logs stay live
  underneath (a per-frame pointer fence, not a modal). **Drag the title bar** to
  move it, **drag the bottom-right corner** to resize it, and **drag any widget
  out** of the grid to pop it into its own little movable, resizable box that
  persists even when the main box is closed (close one to send its widget back to
  the grid). Closing the box drops a one-shot hint naming the reopen key. The
  button is **hideable** via **UI… → chrome** for a pure-theme look (Ctrl+X still
  reopens it). All of this stays on the zero-allocation render path — the
  box-closed courtroom frame is byte-identical.
- **Hover toolbox**: a slim, semi-transparent grip (a drawn hamburger, no glyph
  dependency) in the bottom-right corner of normal play (classic and themed).
  Hovering expands it left into small labelled chips — **Theater**, **Edit
  layout**, **Hide UI** — so those actions no longer live only inside the Hide-UI
  dialog (whose footer is now just **Done**). The toolbox is itself hideable (its
  own entry in the hide list) and is a pure hover reveal, so it stays on the
  zero-allocation render path and can't wake the render loop at idle.
- **The "Hide UI pieces" panel scrolls.** On short windows (768p laptops,
  minimum window height) its growing checkbox lists and footer buttons could clip
  off-screen and become unreachable; the lists now scroll (input-aware clipping,
  so clicks don't leak past the edge) between a fixed title header and a fixed
  Done footer, keeping every control reachable.

## Diagnostics

- **Debug overlay** (Settings toggle): bounded ring (120) of failures —
  missing assets with the formats tried, theme apply verdicts, disconnect
  reasons, `extensions.json` results, **unhandled/malformed packets**
  (EventDebug from the session reducer) — plus a health line: handshake
  phase · server software · last packet age. A hung server reads as a
  stuck phase with a climbing age. A second **diagnostics line** reports live
  structural counts — open tabs · current area · IC play-queue depth · IC/OOC
  scrollback sizes · **goroutine count** — so a leak (goroutines climbing) or a
  stuck queue is obvious at a glance. Computed only while the overlay is open.
- Settings cache browser: live T2 stats (entries/bytes/budget/hit rate),
  on-demand T3 size measurement, open-in-Explorer, clear buttons. A **disk-cache
  size limit** slider sits next to Measure / Clear: set a cap and the oldest
  cached assets are pruned (by modification time) to fit — the sweep runs on the
  existing async disk-writer goroutine, at launch and periodically, so it never
  adds synchronous I/O. The **default is 0 = unlimited**, which never deletes
  anything (T3's unboundedness is a deliberate design choice — no update silently
  wipes a cache).
- **Reset to defaults** (Settings, with a confirmation pop-up): two scopes —
  **Reset settings** reverts the whole settings page (scales, volumes, theme,
  hotkeys, colours, toggles) to defaults but KEEPS your favourites, wardrobes,
  servers & logins, callwords, learned formats, the jukebox library, your case
  notebooks and the disk cache; **Wipe everything** is a fresh-install factory
  reset that also erases all of those — the jukebox library AND the per-server
  notebooks (each lives in its own file, so the wipe clears them explicitly) —
  plus the logins/passwords and the disk cache. Robust by construction — the reset
  copies a fresh-defaults struct over every setting via reflection, so a
  newly-added option resets automatically (a guard test pins the preserved-data
  field names). Applies live (no restart) and re-pulls the derived UI state.
- **Corrupt-preferences protection**: if `config.json` fails to parse, the client
  used to boot silently on defaults and then let the first debounced save
  overwrite the only copy — destroying favourites, wardrobes, server logins,
  macros and learned formats with no recovery. Now a corrupt existing file is
  **renamed aside to a timestamped `config.json.corrupt-<timestamp>` backup**
  before the saver can touch it, and a **one-time startup banner** names the
  backup. A missing file (first run) and an unreadable file are left untouched.

## Quality of life

- **Scene recording & replay** (M16, its own **Settings → Studio** tab, **off by
  default**):
  record a courtroom scene to a tiny **`.aorec`** replay file and play it back
  **natively at perfect quality**. The trick: it records the **event stream**
  (who spoke, emote, text, bg/music, timing) — **not pixels** — so recording is
  near-free and the file is tiny + shareable to anyone else on AsyncAO with the
  same asset base. **Record** with **Ctrl+W** (or an optional small on-stage
  **● Record** button, toggled on in Settings); files save under `recordings/`
  beside the exe. **Replay** the latest with **Ctrl+I**, or pick any saved file
  from the **Settings list** (▶ Play); the stage spins up a throwaway courtroom
  pointed at the recorded asset origin (sprites/bg stream over HTTP, no live
  server needed), seeds the starting background, and feeds each line **when the
  stage goes idle** so the courtroom's own pacing — not raw timestamps — times
  it (recorded wall-clock gaps between lines are discarded, so a replay never
  inherits dead air). A **player transport** sits under the stage — **⏭ Next
  message** (fast-forward the current line straight to the next), **⏸ Pause /
  ▶ Play**, **⏮ Restart**, and a **message X / Y** readout — plus an **■ Stop
  replay** button. A **Playback speed** slider (Studio tab, 25–200 %, **100 %
  default**)
  paces the replay: it's deliberately **slower than live chat** so the whole
  message types out and lingers long enough to read, and the slider adjusts it
  **live** — drag it mid-replay and the next line picks up the new speed.
- **Instant replay** (clip what just happened, **Settings → Studio**, **off by
  default**): the "I wish I'd been recording that" button. Tick **Pre-record
  recent conversation** and AsyncAO keeps a small **rolling buffer** of the recent
  scene events; press the **clip key (Ctrl+.)** and the **last window** is written
  as a `.aorec` — **with no recording started in advance**. The **capture window
  is a slider** you set from **10 seconds up to a full hour**. While it's armed, a
  small **accent dot in the stage's top-right corner** (dim until you hover, with a
  tooltip) shows the buffer is live and **clicks to clip** — so the
  otherwise-invisible feature is discoverable. The clip opens on
  the **right stage and track** (it carries the background and music that were
  playing *before* the window, so it's never blank or silent) and lands in
  `recordings/` ready to **open in the Scene Maker to trim/export**. It's
  **opt-in** and costs nothing until you enable it: the buffer captures on the
  event stream (a human-paced trickle of messages), **never the render loop**, is
  a **bounded ring** (a busy hour can't balloon memory), and **resets on a server
  switch** so a clip can never mix two servers' assets. Ignored players are left
  out of the clip (the same drop that hides them live). Off, it's a single
  pointer check per event.
- **Local timer / alarm** (#97, **Extras → Timer**, **opt-in**): a **personal
  countdown** for keeping RP and casing moving — set a duration with the
  **Minutes / Seconds sliders** or a **1m / 3m / 5m / 10m preset**, then
  **Start / Pause / Reset**, with an optional **Repeat**. When it reaches zero it
  **pings** (the built-in alert sound), **flashes the window** and shows a
  **"Timer finished!"** banner. While it runs, a small **"Timer mm:ss" chip** sits
  over the stage (top-right, **blinking red in the final ten seconds**) so the
  urgency is always visible — independent of the server's own courtroom clocks,
  which are untouched. It's entirely **off until you start one**: the per-frame
  check returns immediately while idle, and the chip only draws while a countdown
  is live, so it costs nothing when unused. Your last **duration** and the
  **Repeat** toggle are remembered between sessions; the running state is not.
- **Scene maker** (M16, the same **Studio** tab): a full **in-app scene editor**
  over the very same `.aorec` model — **build a scene from scratch or edit a
  recording**. **🎬 New scene** (works **offline**, no server needed) or **✎ Edit**
  any saved recording opens a full-window editor: a left **event list** you can
  **add / reorder (▲▼) / duplicate (⎘) / delete** lines on, and a right panel to
  set each line's **character, emote, showname, text, position, colour, flip,
  desk**, an optional **pre-animation**, and per-line **effects** —
  **screenshake**, **realization flash**, **move the character (X/Y sliders)**, and
  a **sound effect** — plus **background** and **music** events. The visual effects
  (shake / flash / move) render in **both the live preview and the GIF/WebP
  export** (the export ignores the live *reduce-motion* accessibility pref so the
  scene you authored is what you get); the sound plays in Preview / a recording but
  a GIF/WebP has no audio. **Quality-of-life:** **+ Line inherits the previous speaker** (so a
  back-and-forth doesn't make you retype the character every line — you just edit
  the text), **⎘ Dup** clones a line, **▶ Preview from this line** iterates a late
  line without replaying the whole scene, and **character + background
  autocompletes** suggest matching folders from the connected server's roster /
  discovered background list as you type (real searchable pickers — they stay
  cheap even on a 4000-character server, where a flat dropdown would be useless).
  **⏮ Set In / ⏭ Set Out** mark a **crop range** so **Preview** and **Export**
  (GIF/WebP) cover only that slice — grab a funny moment or cut the bloat (the
  excluded lines **dim** in the list; an inverted range safely falls back to the
  whole scene, and a mid-scene crop carries the right background so it isn't
  blank). A **📂 Open** button loads any
  saved recording straight into the editor (no trip out to Settings), and a
  **live WYSIWYG preview pane** on the right renders the **selected line**
  — character, emote, pose, background, desk — so you build the scene *visually*
  instead of guessing from text fields (it rebuilds only when the line's look
  changes, so typing never re-triggers it). A new scene seeds the **live
  background and a visible desk** so Preview lands on a real, grounded scene
  instead of a character floating on black. Set the **Origin/CDN** the assets
  load from (your own host, or a server's base), then **▶ Preview** it (played
  through the replay engine) and **💾 Save** a fresh timestamped `.aorec` (Save
  never overwrites the file you opened). Because a scene is just a list of events
  and `.aorec` is **pretty-printed JSON**, you can also open one in **any text
  editor** and tweak it by hand. The editor is a drawn-only-while-open overlay,
  so it costs **nothing** on the live render path.
- **Timeline strip** (#75): a horizontal **film-strip** of the whole scene along
  the bottom of the maker — the one view the vertical list can't give. Each
  block's **width is proportional to how long that event was on screen in the
  recording** (its `OffsetMs` pacing), so you *see* the rhythm: which line
  dominates the runtime, where the dead air is — exactly what you need to **find
  the funny moment or cut the bloat**. Blocks are tinted by kind (dialogue ·
  scene change · music), the **selected event** is outlined (the playhead), and
  the excluded part dims under the crop. **Click a block to select it**; **drag
  the ⟦ ⟧ handles to set In/Out directly** (snapping to event boundaries — no
  more select-row-then-click-Set-In). **Drag a block sideways to reorder it** —
  an insertion caret shows where it'll land, the strip **auto-scrolls** at the
  edges so you can drop past the visible window, and the **selection and the crop
  In/Out follow their events** through the move (not their old slots), so a crop
  set around the funny moment survives a reorder. A small move threshold keeps a
  plain click a select, not an accidental drag. Honest axis: an idle AFK gap is
  **clamped** so it can't swallow the strip, every block keeps a **clickable
  minimum width** (the strip scrolls when they overflow), and a hand-built scene
  with no recorded pacing falls back to **even widths**. Drawn only while the
  maker is open.
- **Self-contained archive** (CDN-proof, M16): a plain `.aorec` is a tiny script
  whose **visuals stream from the recorded Origin**, so a scene built against a
  CDN goes blank if that CDN dies. **📦 Export archive** (in the maker) fixes
  that: it downloads **every asset the scene needs** — character sprites,
  backgrounds, desks, music — into `recordings\<name>-archive\` (webAO layout)
  and writes a **bundled `.aorec`** beside them, so the scene **replays with the
  CDN gone, forever**. It's **de-duplicated** (a 100-line, 3-character scene
  bundles three characters' art, not a hundred copies) to stay as small as
  possible, and resolves each asset through the **same candidate logic** the
  renderer uses, so export and replay are symmetric by construction (proven by
  an export→replay round-trip test). Opening a bundled archive (picker or
  **Ctrl+I**) plays it straight from its folder — the shared asset manager is
  temporarily pointed at the archive (an atomic source override), so textures
  still upload through the normal pipeline. *(Tip: export while connected to the
  origin so the assets are reachable to download.)*
- **Scene → GIF export** (M16, **🎞 Export GIF** in the maker, **🎞 GIF** per
  recording in **Settings → Studio**): render a recorded courtroom — *people
  talking* — to a **shareable animated GIF**. It renders the scene through a
  throwaway replay room into a **fixed off-screen target** (a capped, configurable
  size — default 480×360 — so it's small + memory-bounded) and **composites the
  conversation chatbox** over
  each frame — the speaker's name and their line **typing out rune-by-rune**, the
  same content the live/replay chatbox draws (so the GIF actually animates, it
  isn't a silent stage). Before the first frame it **pre-warms the scene's
  sprites/backgrounds** (prefetch + wait for decode, behind a "Loading scene
  assets…" bar with **▶ Start now**) because the export advances ~4× faster than a
  replay and would otherwise outrun the async fetch and capture an empty stage.
  **File size** is kept down by an **inter-frame diff**: frames are quantized
  *without* dithering and each frame ships only the **pixels that changed** from
  the previous one (transparent elsewhere, "do not dispose"), and an unchanged
  frame folds into the previous one's delay — a mostly-static courtroom compresses
  to a fraction of the naive size (proven by a composite-back round-trip test).
  Each frame's source RGBA is dropped at once (only 1-byte/px paletted frames are
  kept, hard-capped at ~33 s), and it encodes off-thread to `recordings\<name>.gif`.
  It runs **incrementally behind a progress bar** (with **■ Stop & save**) so the
  window never freezes, and it's **off by default** — zero cost on the live render
  path (the render loop stays at 0 allocs/op).
- **Scene → animated WebP** (**🎬 WebP**, beside every 🎞 GIF button — Settings →
  Studio per recording, and in the maker): the **higher-quality** export. Same
  capture pipeline (chatbox composited, sprites pre-warmed), but each frame
  **streams into a libwebp `WebPAnimEncoder`** (`internal/webpenc`, a thin CGO
  binding over libwebpmux) and is compressed **as it's captured** — so it's
  **true-colour** (no GIF 256-colour banding), typically **smaller**, and its
  memory stays flat no matter how long the scene (unlike GIF, which must hold
  every frame for the final encode). Saved to `recordings\<name>.webp`. Falls back
  to a clear message if a build was made without the encoder; the GIF path always
  works.
- **Scene → MP4 / WebM video** (**🎥 Video**, beside every 🎞 GIF / 🎬 WebP
  button — in the maker and Settings → Studio per recording): a **real video file**
  for content creation. Same capture pipeline, but each raw frame is **streamed
  into a system `ffmpeg`** (`internal/videoenc`, pure-Go — it shells out, no CGO)
  over a stdin pipe: `rawvideo` → **H.264/MP4** (plays everywhere) or **VP9/WebM**
  (smaller, open). **ffmpeg is runtime-optional** — the app boots and runs fully
  without it; only the 🎥 Video button disables (with an "install ffmpeg" hint),
  and GIF/WebP still work. Format is picked in **⚙ Export options**; the **quality
  %** slider drives the codec CRF and the **size / frame-rate** apply as usual.
  Because it **never holds frames** (each is written to ffmpeg and dropped — memory
  stays flat no matter the length), video can run **hours long** — the only limit is a
  generous **24-hour** wedge-brake so a stuck export can't encode forever. GIF, WebP,
  and Comic keep their **much shorter** caps because they accumulate frames/panels in
  memory. Saved to `recordings\<name>.mp4|webm`.
  **Audio is baked in** (#99): the scene's **music bed** and each **SFX / shout
  cry** are captured during the (silent, faster-than-realtime) render and, after
  the encode, mixed into the video's audio track by a second ffmpeg pass — so the
  saved file plays **with sound**. It **degrades cleanly** — full mix → music-only
  bed → silent — so a missing track or an ffmpeg hiccup never breaks the export
  (blips are excluded to keep the mix sane). The sound version is saved as
  `<name>-audio.mp4|webm` (subtitle sidecars, if any, follow that name).
- **Import an AO2 `.demo`** — **drag it anywhere onto the window**, or use the
  **📥 Import .demo…** button in the **`.demo → video`** call-out at the top of
  **Settings → Studio**. The button opens an **in-app file browser** (on **every
  OS**) — navigate your PC, jump to Home / Downloads / Desktop / `recordings\`
  (and your drives on Windows), and click any `.demo`/`.aorec` to pick it. (This
  replaced the old Windows-only native file dialog, which could open behind the
  app.) A dropped/picked `.demo` is **copied
  into `recordings\`** (with a `-2` / `-3` suffix if the name is taken) so it joins
  the recordings library, where its rows get the **same buttons as a native
  `.aorec`** — **▶ Play**, **✎ Edit** (in the Scene Maker), and **🎞 GIF / 🎬 WebP /
  🎥 Video / 🖼 Comic** export. The `.demo` is converted to AsyncAO's scene model on
  the fly — no separate format, so everything downstream just works. (The Scene
  Maker's **⇄ .demo** button does the reverse: it writes a scene back out as a
  `.demo` that AO2's own demo player can watch.)
- **`.demo → video` in one step**: the **📥 Import .demo…** in-app browser (or a drop
  onto the Studio tab) both **imports** the file and **kicks off a video export** of it —
  turn a raw AO2 session recording into a shareable MP4/WebM without opening the
  editor. Import is **bounded** (hard rule — no unbounded buffers): the **50,000-event
  scene cap** is sized to swallow a **whole real session** (the largest real fixture is
  ~8,900 events), so a full demo imports intact; anything past it is dropped with a
  coherent leading prefix kept (the timeline stays consistent), and the **Debug panel**
  notes it, e.g. *"stopped at the 50000-event scene cap (N later events not
  imported)"*. Non-scene packets the model doesn't cover (SC/CT/HP/…) are
  **skipped** with their own separate count in that same note, so you can tell
  "this demo has chatter" from "this demo is longer than the cap." If ffmpeg isn't
  on PATH the import **still lands in Recordings** (GIF/WebP export it there); only
  the video step needs ffmpeg.
- **Where a `.demo`'s assets come from** — a `.demo` records only bare asset *names*
  (songs, sprites, backgrounds), never the server they live on. On import, AsyncAO
  stamps the recording with the **asset host of the server you're connected to at
  that moment** — so for full sound and sprites, **join the demo's home server
  first, then import**. Importing with no connection stores no host: the demo still
  plays/exports, but silently and with a bare stage, and a warning says so at
  import. Remedies after the fact: open the recording in the **Scene Maker** and
  set its **Origin/CDN** field to any asset base URL that carries the content, or
  — for a server that's gone — enable **Settings → Assets → Local assets** with a
  mount of the content folder you have on disk *before* importing, and the
  recording resolves everything (including exported-video music) from your own
  files, no network needed.
- **Export options** (**⚙ Export** in the maker, and **Settings → Studio**): set
  the **size** (Small 384×288 → XL 720×540), **frame rate** (8–24 fps), **WebP
  quality**, **chat text size**, **loop on/off**, and **playback speed** — all
  **sticky** (persisted in prefs) and applied to every GIF/WebP. The chatbox text
  is sized to the **output frame** (not your live chat zoom), so long lines fit the
  small capture instead of overflowing; the **Text size %** knob nudges it. The frame cap is **memory-budgeted**: a
  bigger size keeps the ~69 MB paletted-frame budget by allowing proportionally
  fewer frames (the GIF gets shorter, never over-budget; WebP streams compressed so
  it can run longer). The panel shows the resulting max length and notes that
  screenshake / busy backgrounds bloat a GIF (every pixel changes each frame).
- **Screenshot** the whole window to a **PNG** under `screenshots/` — **Ctrl+S**
  or the **Extras → Screenshot** button — written off the render thread; ~10×
  smaller than the old BMP and it previews inline in Discord etc.
- **Log browser / search** (lobby **Logs** button, or **Extras → Logs**): browse
  and search every saved transcript — pick any **server**, then any **session**,
  and **filter the lines by text** (name, word, phrase) across the whole scope;
  click a line to copy it. Reads the per-server `logs/` files **off the render
  thread**, hard-bounded (files / lines capped), with the live filter memoized so
  typing never re-scans. Needs detailed logging on (Settings → Audio & Chat).
- **Window-relative UI scale.** Auto UI scale now follows the **window size** (a
  reference height = 100%, larger windows scale up, capped) as well as the display
  DPI, and is **floored at 100%** so an unreliable DPI reading can never auto-shrink
  the UI — fixing "everything is tiny" on big monitors / maximized windows ([#6]).
  Manual scale (Settings → General) still overrides.
- **Call-mod is a floating panel.** The Call Mod window is now a movable / resizable
  **non-blocking** floating box (like Evidence / Pair) — keep talking and watching
  the courtroom while it's open.
- **Theme fonts.** An AO theme that ships its own font (a `.ttf`/`.otf` in the theme
  folder, named by its `courtroom_fonts.ini` `*_font` family) now loads it for the
  IC/OOC text — below a manual font override and the dyslexia font ([#6]).
- **TLS certificate validation** (Settings → Account → **Security**; power users,
  **OFF by default**): strictly verify a `wss://` server's certificate. Off by
  default so the many community servers on self-signed certs stay reachable; on for
  those who want to be sure the encrypted connection is to the real server.
- **IC log**: 1024-line color-preserving scrollback with search, Copy to
  clipboard, and TXT/HTML export (`logs/` beside the exe; HTML keeps the
  AO palette). Lines **word-wrap to the list width** (cached against
  log/width/font-scale — never re-wrapped per frame). **Local timestamps**
  prefix each line (`14:32  Phoenix: …`) so you can see when people spoke —
  **ON by default**, toggle in **Settings → Audio & Chat**. The time is stamped
  once when the line arrives, never formatted per frame (the toggle state is part
  of the wrap-cache key, so flipping it re-wraps exactly once).
- **Live player list** (the Players tab): a **truly live roster** built from the
  server's **`PR`/`PU` player-state stream** (the Akashi/Nyathena
  `PlayerStateObserver`), pushed to every client from connect with **no `/getarea`
  polling and no opt-in** — zero floodguard, event-driven, zero per-frame cost.
  Every player is a row keyed by their **server UID**, carrying showname, OOC name
  and area, updated as people **join, leave, switch character, or change area**; a
  player with no character shows as a **Spectator**, so spectators appear and
  vanish live. Rows **group by area** (a header you **click to jump there**); a
  **Rooms** button orders those area groups — by default **your current area
  first, then the server's `/gas` order** (so the room you're in is on top, not
  whichever room holds the lowest-UID player), or **A–Z**, or **most players
  first**. Both the **Rooms** and player **Sort** choices are **remembered across
  sessions**. Each
  row has a char icon, role highlights (you · the current speaker · friends),
  Spectator/CM chips, a player **sort toggle** (UID · name · speakers-first), a **`/pair
  <uid>` button**, **Copy-UID**, and (opt-in) a **Follow** button (M3) — straight
  from the live UID, no `/getarea` needed. **Follow is OFF by default**: tick the
  **Follow** box in the player-list header to switch the per-row Follow buttons on
  (the toggle is remembered; turning it off also stops any active trail). When on,
  **Follow** trails a player across areas: AsyncAO **auto-jumps you to their area
  whenever they move** (debounced; the row reads *Following*, click again to stop)
  — a mod tailing a suspect or catching up to a friend, riding the same live PR/PU
  area data. The
  **Areas tab** also keeps a **Recent:** strip — one-click chips to jump straight
  back to areas you've just passed through (newest first, current area excluded) —
  and a **search box** (with a shown/total counter, matching the Music tab beside
  it) so a hub server with hundreds of areas is filterable, not scroll-only.
  **IPID** is the only field the stream omits: a mod's **"Refresh
  details"** (and an auto-pull on mod-login + as joiners arrive) runs **`/gas`** —
  the all-areas roster, since the live list spans every area — and merges the IPID
  back **by UID** (exact, not name-matched), shown whenever present (in-session,
  never persisted). `/gas`, **not** `/getareas`: Athena/Nyathena register only the
  short `ga`/`gas` alias, which Akashi/tsuserver accept too. A
  **"Legacy snapshot" tick box (off by default)** shows the raw `/getarea` roster
  instead. **Ctrl+wheel zooms** the list text (rows + icon scale with it); the
  click-to-pair popup zooms the same way. The displayed name falls back showname →
  OOC name → character.
  - **Fallback** (servers without a PlayerStateObserver, e.g. older tsuserver):
    the list reverts to the webAO-style **CharsCheck + ARUP head-count** roster —
    one row per taken character plus anonymous **Spectator rows** from
    `head-count − taken chars`, enriched by a `/getarea` snapshot (matched by name
    **or** showname to cover Akashi `char (showname)` vs Athena/Nyathena
    `showname (char)`, including Akashi's one-line mod form
    `char (showname) (ipid): ooc`).
- **Ignore / block a player** (#81): an **Ignore** button on every player-list row
  (and *Unignore* once set) drops that player's messages **entirely** — IC *and*
  OOC, with **no log line, no sprite, and no blip**: an ignored IC packet is
  skipped before it reaches the courtroom, so it's as if the server never sent
  it. Matched by **showname-else-character** (the only identity the MS wire
  carries — so, like friend highlights, it can be spoofed), stored **per server**.
  Manage the list (and **un-ignore someone who has left**, when their row is gone)
  in **Settings → Ignored players**. Free when unused: the match is one lock and
  zero iterations per message on an empty list, and nothing touches the render
  loop.
- **Reconnect, manual + automatic** (M2): when a connection drops or a join fails,
  the lobby shows a **"Reconnect to &lt;server&gt;"** button that re-dials the last
  server you tried (the name + ws URL are remembered on every connect attempt). On
  an **unexpected drop**, AsyncAO also **auto-reconnects** with exponential backoff
  (≈2 s → 30 s, capped, several tries) until the server returns — the lobby shows
  the retry status and a **Stop** button. A **deliberate Disconnect never
  reconnects**, and a manual Reconnect / fresh Join takes over. A **server kick or
  ban never auto-reconnects** either (re-joining after a ban reads as ban evasion),
  though the manual Reconnect button still works. A silently dead link (NAT
  timeout, no FIN) is detected by a **read-staleness watchdog** that pings on
  prolonged silence and treats a missing pong as a drop. Toggle in **Settings →
  Audio & Chat** ("Auto-reconnect after a dropped connection", ON by default). Idle
  it costs one time-compare per frame.
- **Auto-connect on launch** (OFF by default): turn this on in **Settings →
  Messages & connection** to open straight onto the **server you last used** — no
  lobby detour — even after you'd disconnected. The checkbox names the remembered
  server once you've connected to one. Tab-restore (M7) wins if both are on (it
  reopens exactly what you had). The same saved server is also reachable any time
  via a **"Connect to last server" keybind** (default **Ctrl+Q**, rebindable in
  the Controls tab) that works offline in the lobby. Off, it's one bool check on
  the first frame and nothing after — boot stays byte-identical.
- **Disconnect confirmation**: the **Disconnect** button is easy to fat-finger, so
  by default it pops a **confirm modal** ("Disconnect from the server? — Yes will
  disconnect you, you'll return to the lobby") with **Yes / Cancel** before it
  acts; the modal **fences the pointer** so a click can't fall through to the
  courtroom underneath. A **Settings → General "Instant disconnect"** toggle (OFF
  by default) skips the prompt for those who want one-click. Clicking a background
  **tab's ✕** confirms the same way (its own "Close this tab?" modal names the
  server — it's easy to hit by accident), and the same *Instant disconnect* toggle
  is its escape hatch too; an already-dead tab closes at once (nothing live to
  lose). Only the *user-facing* Disconnect buttons and the manual tab-✕ confirm —
  an automatic disconnect (a dropped connection, auto-reconnect teardown, an
  already-dead tab reaped on the next connect) is unaffected. Drawn only while
  open: zero cost on the render loop otherwise.
- **Showname presets**: a global, persisted list of shownames managed in
  **Settings → General** (add, *Save current*, **Use** to apply one — the active
  preset is marked — remove with ×; cleared only by a factory reset). **Ctrl+H**
  swaps to a random saved preset and **Ctrl+B** cycles to the next (both
  rebindable). Each preset also takes a **per-preset keybind** — click *Bind key*,
  press a key, and that key swaps your showname to that preset in the courtroom
  (right-click the button to clear). The bind survives all but a factory wipe and
  drops automatically if you remove its preset. **On the courtroom screen** a tiny
  **▾ picker** sits next to the showname box (and the OOC-name box) — click it to
  pick a saved name with the mouse, no Settings trip or keybind needed (great for
  RP name-changing). It appears only when you have presets, fits inside the box so
  nothing shifts, and the option list is cached so the chat row stays alloc-free.
  Works in the classic and themed chatbox layouts.
- **Music changes in the IC log** (webAO/AO2 parity): when someone plays a song
  the log shows "*&lt;name&gt; has played a song: &lt;song&gt;*" (and "*has
  stopped the music*" on stop), named by the MC showname or the character. The
  **`~stop` sentinel halts playback immediately** instead of fetching a track
  that 404s, and **disconnecting from a server stops the music** too. The MC
  **looping** flag (field 3) and **MUSIC_EFFECT** flags (field 5) are honored: a
  no-repeat / play-once track plays once instead of looping forever, and a
  fade-in track ramps up (via SDL_mixer's native fade, toward your volume so a
  slider drag never fights the ramp) instead of hard-cutting. Fade-out and
  sync-position are documented-skipped (single stream / no cheap mid-fetch seek);
  a short or malformed MC packet degrades to the AsyncAO default (loop forever).
- **OOC links survive word-wrap**: a long shared link (e.g. a Discord CDN URL
  with a `&`-laden query string) that wraps across rows still opens / copies /
  saves whole — resolved from the source entry, not the visible fragment.
- **Custom window size + fullscreen** (Settings → Window): pick a size from
  presets, **Fit to screen**, or a custom W×H — or go **borderless fullscreen**
  (**F11** toggles it from any screen). Every applied size is clamped to the
  display and recentered, so a window bigger than the monitor can't be stranded
  off-screen; the choice persists across launches.
- **Double-click any text field** to select all its text — a quick replace or
  clear without holding backspace.
- **Per-area scrollback** (Settings → Audio & Chat, **OFF by default**): when
  on, each area keeps its own IC log — clicking an area in the Areas list saves
  the current log and swaps in that area's history (empty on first visit, your
  earlier lines on return), so a busy lobby's chat doesn't bleed into a quiet
  courtroom. Best-effort: AO only signals the area on an explicit click, so a
  mod-initiated move keeps the current log (the default continuous behavior).
  Bounded to the last 64 visited areas; OFF ⇒ one continuous log as before.
- **Detailed logging** (Settings → Audio & Chat, **OFF by default**): appends
  every IC line to `logs/transcript.log` beside the exe — `timestamp | server |
  area | CharName (Showname) | message` — a full casing record across all
  connected servers (each line names its server). All disk writes run on a
  single background goroutine fed by a bounded queue, so the message path never
  blocks (and a flood sheds rather than stalls); the file flushes when caught up
  and closes on exit. OFF ⇒ nothing is opened or written.
- **Per-speaker name colours** (Settings → General, **OFF by default**): each
  speaker's name gets its own **stable** colour, derived from a hash of the name
  (same name → same colour every session — no random reshuffles). Two sliders —
  **saturation** (grey ↔ vivid) and **brightness** (floored so a name can't go
  unreadable-dark on the panel) — tune the palette, with a live preview of
  sample names. Colours the chatbox showname (the big name above each message)
  **and** each speaker's name in **both** the IC and OOC logs (the name prefix
  on each entry's first line; system lines — `[MOD CALL]`, `CLIENT:`, `SERVER:`,
  evidence — and partially-wrapped long names fall back cleanly, because the
  speaker is stored at append time, never re-parsed from the `": "`). Computed
  inline (a few-byte hash + HSV, no allocation, no cache to stale), so it's free
  per frame and the default OFF path is byte-identical (RenderFrame stays 0
  allocs/op).
- **See-through chatbox** (Settings → Theme, **"Chatbox opacity %"**): a slider
  for the IC chatbox **panel opacity** (0 = fully transparent — text only — through
  100 = solid; default 84 ≈ the old fixed look) so you can see more of the scene
  behind it. Only the **flat fallback** panel fades; a theme's own chatbox skin
  keeps its art, and the border + text stay solid for legibility. The viewport
  render path is untouched (this is the UI chat overlay), so it's free.
- **Callwords** (M15 **manager**): a managed highlight-word list — type a word
  (or paste `a, b, c` to add several at once) + **Add**, and each shows below
  with a **×** to remove (replaces the old single comma field; lowercased,
  deduped, capped at 32). Words match as **whole words**: `tif` fires on
  `hi tif` but **not** on `motif` or `artifact`, so a short callword can't
  self-ping on a coincidental substring. For the old loose shorthand — one
  callword that catches a whole word family — append a **trailing `*`**: `obj*`
  matches at a **word start** without needing a word end, so it still catches
  `objection` / `objecting`, while `tif*` still won't fire on `motif` (`tif`
  isn't a word start there). A lone `*` matches nothing. An IC/OOC match = taskbar
  flash + a sound — a **custom sound file** you point at in Settings
  (`.wav`/`.ogg`/`.mp3`/**`.opus`**), else a **built-in ping**. The ping is the
  reliable default: we deliberately do **not** route through the theme's
  `word_call` (a theme that *names* it but ships no loadable file would play
  nothing and silence the alert — the "callwords don't work" report). A **Test
  sound** button next to the field plays exactly what an alert fires, so you can
  confirm it's audible. The same custom-file → built-in-ping rule covers the
  friend-speaks sound. The built-in ping is a **~1.5 s trill** (the old 160 ms
  ping "played too short"), and both ping and custom sound play on a **dedicated
  reserved audio channel** so a chat blip can never cut them off (it used to: the
  alert landed on the free blip channel at message arrival and the first blip
  halted it). The alert has its **own volume** (Settings → Audio · "Callword/alert
  volume"), **independent of SFX** — quietening or muting SFX never silences your
  name-pings. An **optional toast** (Settings → **ON by default**) names the
  heard word, like the modcall/friend toasts; a further **desktop (OS) notification**
  (#M4, OFF by default) pops a real Windows toast when your callword lands. All three
  **desktop toasts** (callword, friend, modcall) now fire **only while AsyncAO is
  tabbed away or minimised** — when it's focused the in-app toast/flash already covers
  it. (Opus support needs the codec DLLs loaded at startup — see the audio note below.)
- **Do Not Disturb** (M15; **Ctrl+D**, or Settings → Audio & Chat): a one-click
  mode that **mutes the personal pings** — callword *and* friend alerts (their
  sound, toast, and window flash) — while **duty signals still come through**
  (modcalls, case alerts, server notices). The **Ctrl+D** keybind is rebindable
  (Settings → Controls, like every other shortcut). By default it's
  **session-only** (clears every launch) so it can never silently kill your
  callwords days later — but an opt-in **"Remember Do Not Disturb across restarts"**
  setting makes it persist if you want. Either way a persistent
  **"● Do Not Disturb — alerts muted"** badge sits above the OOC box while it's on
  (**click it to turn DND off**), and the toast beside it is clipped so the two
  never overlap. The friend *glow* in the log is passive, so it stays on.
- **Mod-command feedback sounds** (Settings → Mod tools, each **OFF by
  default**): a distinct sound when a **ban / kick / mute** happens — handy for a
  moderator. Each of the three actions has **its own toggle and an optional
  custom file** (`.wav/.ogg/.mp3/.opus`; blank = a built-in synthesized default,
  and a **Test** button auditions it). It fires on **both** signals: the exact
  `KK`/`KB` disconnect packets (when *you* get kicked/banned) **and** a scan of
  **server** OOC lines for the action (so your own `/ban`·`/kick`·`/mute`
  landing — and visible mod actions — sound too). The classifier is server-origin
  gated (a player typing "ban" can't trip it), excludes `un-`/negated forms
  (`unbanned`, "not muted"), picks **one** sound per line (fixed precedence), and
  a short **per-action cooldown** collapses the usual confirmation+broadcast
  double-line into a single play. As a **duty** signal it deliberately plays
  **through Do Not Disturb**. *(The OOC keywords are hardcoded constants tuned to
  tsuserver-family wording — a one-line edit per server, not a config knob.)*
- **Per-SFX mute** (M11): silence an emote sound effect that's grating — the
  **last SFX you hear** gets a one-click **"Mute last SFX: &lt;name&gt;"** toggle in
  **Settings → Audio & Chat**, with the muted list (× to unmute) below it. Global
  and persisted; matching SFX are skipped at the courtroom's play site, so muting
  costs nothing while a sound isn't playing.
- **Per-character blip volume** (M11): quiet a character whose typing **blips**
  are too loud without touching the global blip slider. The **last speaker** gets
  a quick **0–100% slider** in **Settings → Audio & Chat**; everyone you've
  adjusted is listed below with their own slider and a **×** that resets to 100%
  (= unchanged). Global and persisted, keyed by character folder name. The scale
  is read **once per message** in the courtroom and multiplied into the blip
  channel's volume **at play time** — the render loop and per-frame hot paths are
  untouched, so an unadjusted roster costs nothing. The map only stores genuine
  adjustments (resetting to 100% drops the entry) and is bounded.
- **Highlighted friends** (Settings, **OFF by default**): a **per-server** list
  of shownames whose IC messages **glow** (a warm tint behind the line) so you
  can spot your friends in a busy log — saved per server (cached like the char
  list), and it works for backgrounded tabs too. **Add or remove a friend right
  from the player list** — each row carries a **+ Friend** button that flips to
  **Unfriend** once they're added (one click each way; matches by showname, no UID
  needed), not just the Settings text field. The button is **ON by default but can
  be hidden** in Settings → Friends for anyone who finds it clutters the panel.
  Matches the **displayed** name
  (custom showname, falling back to the character), so — like any showname — it
  can be spoofed; that's noted in Settings. Gated entirely on the toggle, so a
  no-friend log draws byte-identical and the detection costs nothing when off.
  **Optional signals** (each its own toggle, all OFF): a **notification** (an
  in-app toast "*X just spoke on \<server\>*" + taskbar flash) that fires **even
  from a backgrounded server tab** — so you see a friend pop up on another
  server — and a **sound** (the default ping, or a **custom file** you choose),
  and a real **desktop OS notification** (a Windows toast, rate-limited so a
  chatty friend can't storm it). Streamer mode suppresses them all.
  **Per-friend colour + nickname** (#82): append `=RRGGBB` to a name in the list
  (e.g. `blank=ff4488`) to give that friend a custom colour — it tints both their
  IC-log **glow** and their **name in the player list** (lifted toward a readable
  brightness so a dark colour can't vanish on the dark panel); names without one
  use the default warm tint. Append a third field — `name=RRGGBB=Nickname` (or
  `name==Nickname` for a nickname with no colour) — to set a **personal nickname**
  shown in their **player-list row** (in place of their showname, with their
  character still shown after the `·`) **and in the IC log** as `nick (showname):`,
  so you recognise a friend even when they iniswap or change showname. In the IC
  log the **real showname stays in parentheses** and is what double-click-to-pair
  and the per-speaker colour key off; the nickname is **suppressed in force-char
  (anti-impersonation) mode**, where the character name is what matters. Per
  server, like the rest of the list; nicknames can't contain commas (the list is
  comma-separated). **Pulse the glow** is its own toggle — a gentle
  breathing animation on the glow (obeys reduce-motion, so it holds steady when
  that's on). A configurable **toggle keybind** (default **Ctrl+U**, rebindable
  in Settings → Hotkeys) flips the highlight on/off mid-session.
- **Hotkeys** (Ctrl+key, configurable in Settings): shouts 1..4, pos
  cycle, music stop (`~stop.mp3` fake-track request, courtroom.cpp
  music_stop), log jump, screenshot (`screenshots/` beside the exe).
  **Per-menu shortcuts** jump straight to a menu without opening the Extras box:
  Characters / Wardrobe / Jukebox / Background / Evidence / Pairing on
  **Ctrl+5..0**, Call-mod / UI-chrome / Settings on Ctrl+O / F / **,** (comma —
  Ctrl+Z stays free for the layout editor's undo). **Random
  character** (webAO `/randomchar` — swap to a uniformly-random FREE character)
  on **Ctrl+R**, also a "Random char" button in the Extras box. All rebindable
  and listed as defaults in Settings → Hotkeys.
- **Keybind remap screen** (M7; Settings → Hotkeys): every action is a
  **click-to-capture** binding — click it, press a key, done (no typing key
  names). **Right-click resets** that one to its default; **"Reset all to
  defaults"** clears every override at once. **Conflict detection** outlines any
  key bound to more than one action in red (the dispatch fires only the first, so
  a clash would otherwise silently dead-end the later action), with a tooltip
  naming it. Esc cancels a capture.
- **Drag-resize the layout** (Settings → General → Stage, **ON by default**):
  grab the **viewport's right edge** and drag to make the stage bigger / the log
  smaller (it scales the whole 4:3 viewport, reusing the View knob's own clamp +
  persistence). The grab is claimed *before* the zoom/sprite-drag handlers so it
  never fights them, and it's only active in drag mode. Uncheck it to bring back
  the classic **+/− View/Text/MsgBox/Log/Input knob buttons** instead (the text
  scales are also always Ctrl+wheel over each region). *(Themed layouts have a
  separate full drag-to-move/resize editor.)*
- **Hideable chrome**: shout row, layout knobs, emote grid, right column,
  OOC row, HP bars, clocks, badge, judge row — persisted per user.
- **Now-Playing + reliable Stop** on the Music tab: a "Now playing: <track>"
  line shows the current song (a streaming link shows its filename), and **Stop
  music** now **halts your own playback immediately** (and cancels a track still
  fetching) instead of only asking the server to stop a fake `~stop` track —
  which often failed, so the music kept going. It still sends the server-side
  stop too, so a DJ stops it for the room. A **search box** filters the server's
  track list (AO2/webAO parity), memoized so a list of thousands isn't re-scanned
  per frame; an "N / M" count shows how many match.
- **Jukebox playlist** (Wardrobe → **Jukebox** tab): a library of the music
  links DJs/CMs `/play` in OOC (YouTube/Discord/etc.), organized into named
  playlists (folders) so you click instead of paste. Per song: a labelled
  **Play** button (clicking the song title plays it too — both send `/play
  <url>` in OOC and autoplay), **Open** (browser), **Share** (posts the raw
  link to OOC in one press, for others to grab), and remove. **Shuffle** one
  playlist or everything (random `/play`). **Bare-key binds**: assign a key to
  a song (plays it) or to a playlist (shuffles it) and fire it from the
  courtroom with no text field focused — these take priority over character
  keybinds and the emote 1–9 digits (a deliberate DJ trade). Memoized search.
  **Save shared links**: hover any **OOC log line** that carries a link and a
  **+ Jukebox** button files it into a "Saved from chat" playlist (dedup by
  URL) — one click to keep a song someone just `/play`ed.
  **Share a whole config**: **Export…** writes the library to
  `jukebox-playlists.json` beside the exe; a friend drops that file beside
  their exe and clicks **Import file** to fold it into theirs — additive
  (same-named playlists gain links they don't have, dedup by URL, unknown
  playlists added whole, all within the caps), and an imported key bind is
  kept only where it doesn't collide with one of yours (yours win, so a shared
  config can't hijack a courtroom key). **Paste & merge** does the same from a
  JSON copied to the clipboard, no file needed.
  The library is **global** — shared across every server (the links are) — and
  lives in its own async-written `jukebox.json` (capped: 200 playlists / 50000
  links / one debounced writer), so a huge collection never bloats or
  re-serializes the prefs file. Play/Share need a live connection (and DJ/CM
  rights, which the server enforces). Kept by **Reset settings**, erased by
  **Wipe everything**.
- **Recently played** (M12; Jukebox tab → **Recently played** toggle, **ON by
  default**): a session list of the songs that played **in the room**, captured
  from the same music-change event the IC "*has played a song*" line uses — but
  **only for an allowlisted set of "unique" host domains** (the kind people host
  their own songs on). The default allowlist is **catbox.moe, file.garden,
  youtube.com, youtu.be, discordapp.com, cdn.discordapp.com**, editable in
  **Settings → Audio & Chat** (add/remove entries; paste a full URL and it's
  normalized to the bare host). The server's own music (bare names, the server
  host) **still plays — it just isn't recorded** (you already have it on the
  server; this feature is for grabbing songs from elsewhere). **Discord records
  audio files only** (`.mp3`/`.opus`/…), since most Discord CDN links are images.
  **Nothing is hardcoded to a specific server.** An entry can also be a
  **host/folder** (`host/path`) to record **only the audio files under that
  path** — e.g. add `miku.pizza/base/youtube` to save a server's user-rip folder
  while the rest of that host (its own library) stays unsaved. Folder rules are
  **opt-in**: you add the one you want.
  Each row shows the cleaned song name + who played it, with **Save** (files it
  into its own dedicated **"Music history"** playlist, dedup by URL), **Play**,
  and **Share**. Open that **Music history** playlist and its songs are **grouped
  under domain-name headers** (catbox.moe, youtube.com, …) so you can see at a
  glance who hosts what — the grouping is memoized against the library revision,
  so scrolling it stays allocation-free. It's the "*what was that song?*" grab-it
  scratchpad — **in-memory
  for the session** (the persisted half is the playlists), newest-first, deduped
  (a replay moves to the top), and **bounded** (30). A tick box turns the whole
  thing off (records nothing, hides the toggle). Costs nothing on the hot path:
  the allowlist check + capture are event-driven (once per song, never per
  frame), the row labels are precomputed at capture, and the toggle's count label
  is cached — the render loop stays **0 allocs/op**.
- **Favorites ★** (M12; Jukebox): click a song's **★** to star it; a top-level
  **"★ Favorites (N)"** toggle (beside *Recently played*) collects your starred
  songs from **every playlist** in one place — Play / Share / Open / un-★ — with
  each song's home playlist shown. The star persists with the library (in
  `jukebox.json`), and the favorites list is **memoized against the library
  revision** (rebuilt only when something changes, the same self-invalidating
  snapshot the search and domain-grouping use), so the per-frame draw just walks a
  cached slice — **allocation-free**. Stars survive Export/Import; a merged-in
  shared config arrives un-starred (it can't restar your library).
- **Audio codecs** (the "audio note"): only WAV is built into SDL_mixer; the
  **Opus / Ogg-Vorbis / MP3** decoders ship as separate DLLs and are loaded at
  startup via `Mix_Init` (best-effort — a missing codec just loses that one
  format, the rest still play). This is what lets **`.opus`** work everywhere it
  matters: Discord `/play` CDN links (which are `.opus`) **and** a custom
  `.opus` callword/friend alert sound. Decoding always runs in C off the Go
  side (spec §8).
- **Real dropdowns** for the IC text color (named colors + live swatch)
  and the position selector (AO2 ui_pos_dropdown parity, SD list when
  sent) — open lists draw above everything, auto-widen to their options,
  flip at window edges, and modally capture the pointer.
- **Tab / Shift+Tab cycles focus** across visible text inputs in draw
  order (IC → OOC → search ...), wrapping both ways.
- Clicking an emote **refocuses the IC input** (AO2 focus_ic_input) —
  pick and keep typing.
- **Music ducking** (Settings → Audio & Chat, off by default): dips the
  music while a message is on stage (shout/preanim/talking) and restores it
  at idle, so dialogue stays clear. Transition-driven — the mixer volume is
  touched only when the duck state flips, never per frame.
- **Packed-room catch-up** (Settings → Audio & Chat, ON by default): keeps the
  IC stage real-time. **By default (threshold 1)** the newest message always
  types out **in full — sprite, name, typewriter and effects** — while any
  backlog stacked behind it flashes past, so when someone fires off several
  lines a second the textbox jumps straight to the latest one instead of
  crawling seconds behind. Normal back-and-forth (nothing waiting behind the
  current line) plays every message in full, so you lose no animation in a
  calm scene. The IC log keeps every line regardless (it stays the complete
  record). Perf-positive (fewer frames spent on backlog). Raise the threshold
  in Settings to watch more of a backlog animate; it sits alongside the
  plain-English Text crawl / stay / chat-limit knobs.
- **IC character counter** (ON by default, Settings → Audio & Chat): a live count
  to the right of the IC box that turns red past ~256 chars, where many servers
  truncate. The count string is cached, so the courtroom frame stays 0-alloc.
- **Emote grid pages** (`<` / `>` + a `page x/y · N emotes` counter) when
  a character ships more emotes than fit — both the classic and themed
  layouts. The arrow row only appears when paging is needed; loading a
  character resets to page 1. The **mouse-wheel over the grid pages too** (scroll
  up = previous, down = next). A **Random** button picks any emote (and jumps
  to its page), and **number keys 1–9** pick the emote in that grid position
  on the current page when the chat box isn't focused (picking focuses IC).
  **Ctrl+E** (rebindable) **cycles to the next emote**, wrapping at the end and
  auto-paging into view — keyboard-only emote stepping that walks the list in
  order (vs. Random's jump). **Auto-random emote** (Settings, **OFF by
  default**): when on, every IC message you send rolls a *different* emote from
  the current character's set (and scrolls the grid to it) — for people who'd
  rather not click the grid, and to surface sprites they'd never pick. It only
  runs on an accepted send, so it costs nothing while idle and never fires on a
  rate-limited or command line. A **missing-frame warning** appears the moment you
  select or send an emote whose sprite is absent (the streaming layer probes on
  selection and the banner names what 404'd — see also Settings → formats).
  **Hovering an emote previews it** large (3 s, or instantly on right-click):
  if the emote has a **pre-animation** that flourish plays (looped) so you can
  watch it before sending; otherwise its talking sprite. Both layouts. The box
  stays up while you **move the cursor onto it** (it keeps a travel corridor open
  so it doesn't vanish on the way), where the **mouse-wheel zooms** in/out and a
  **left-drag repositions** it; it closes once the cursor leaves the box (or you
  click).
- **Emote favourites** (#77/#85): characters ship dozens of emotes but you use a
  handful. Every emote button carries a **★ in its corner** — **dim grey** when
  not yet a favourite, **gold** once it is — so favouriting is always one click
  away (it isn't a hover-only secret), while an idle grid stays subtle. Click the
  ★ to **favourite that emote for that character**. Three ways to use them:
  - **★ Favourite-emotes box** (the headline — Settings → General, the Extras
    menu's **★ Fav Emotes**, or **Ctrl+A**, rebindable; **OFF by default**): a
    small **movable, closeable floating box** of just that character's starred
    emotes as clickable sprite buttons — your go-to emotes one click away,
    no paging. Press one to select it (it sends on your next message), exactly
    like the grid. Non-blocking (the scene/chat stay live underneath) and it
    shares the Extras surface's input model, so it can't steal clicks from the
    scene. The open state persists; its position is per-session.
  - **Show favourite emotes only** (Settings → General, or the **★ Favs** button
    in the classic grid): the main grid hides everything but your stars — paging,
    **Random**, **number keys**, and **Ctrl+E cycling** then all operate on just
    the favourites.
  - The ★ itself, to curate from anywhere in the grid.

  Favourites are **per character and persisted** (kept across a settings reset,
  like your wardrobe). They're keyed by the emote's **position, not its name** —
  emote labels and talking sprites *duplicate* within a character (Apollo has
  three distinct "normal" emotes that share a sprite), so a name key would merge
  them into one star. **Zero render cost**: a star toggle rebuilds a small lookup
  set + the visible/box index lists once; every steady-state frame is a single
  guard compare (verified 0 allocs/op), the box draws only while open, and the
  always-on render loop (`render.Viewport`) is untouched.
- **Sprite preview magnifier**: every sprite preview pop-up (character select,
  wardrobe, emote hover, background picker) has **− / + zoom controls** along
  its bottom OR the **mouse wheel** (in and out) over the box; past 1× the
  preview becomes a **magnifying glass** — move the mouse over it to pan around
  the magnified sprite and inspect pixel detail. **Left-drag the box** to move
  it anywhere on screen (it stays put across previews) if it covers something.
  (The courtroom **stage** has its own zoom: **Ctrl+wheel** zooms toward the
  cursor, Ctrl+drag pans — the "hyperfocus" camera.)
- **Sprite hover-previews are configurable** (Settings → General): the preview
  pop-up is **ON by default** with a **5 s hover dwell** you tune with a slider
  (0.5–15 s), or switch off entirely.
- **Reposition sprites by dragging** (Settings → General, **default OFF**): when
  enabled, drag any character in the viewport to move them (the override sticks
  per character; right-click a sprite to reset it, or "Reset all moved sprites"
  in Settings). Off by default so a stray click can't nudge a sprite.
- **Hide a sprite ("Missingno")** (Settings → General, **default ON**): for
  players who'd rather not see certain art — **right-click a character sprite** in
  the viewport, confirm, and it's **hidden from the viewport for the session** (the
  layer is dropped; the message text still shows). The confirm modal **fences the
  pointer** so the click can't fall through. **Reshow all** with the
  *"Reshow hidden sprites"* key (Controls tab, default **Ctrl+Y**) or the Settings
  button; turn the right-click off entirely in Settings. The hidden set is
  session-only (not persisted). Free on the render path: the suppression check
  short-circuits when nothing is hidden (the `RenderFrame` 0-alloc gate is in
  `render.Viewport`, untouched).
- **Hide the desk** (Settings → General, **default OFF**): suppress the foreground
  courtroom desk so the **full character** shows (no table in front). Toggle it
  live with the **"Hide / show the desk"** key (Controls tab, default **Ctrl+V**)
  or the Settings checkbox. Persisted. Suppressed in the same per-frame
  sprite-override pass (one pref read, then the existing short-circuit) — the
  render gate is untouched.
- **★ favourite a character from Character Select**: a star on each character
  icon adds it straight to your **per-server Wardrobe** (LemmyAO-style), so it
  rides along on every connect. One click on, one click off; the Wardrobe tab
  and courtroom Wardrobe menu show the same stars.
- **Auto-login toast** (Settings → General, **ON by default**): when a saved
  auto-login fires on join, a one-shot in-app toast **and a desktop
  notification** name who/where you signed in as (masked in streamer mode), so
  a mod knows "am I logged in?" without checking. Toggle it off if you don't
  want the popup. The login lines still send paced, one at a time (so a
  two-step Akashi prompt is answered in order).
- **Background picker** (courtroom Background button): a thumbnail grid of
  every background, modeled on the wardrobe menu. AO has no "list
  backgrounds" packet, so the set is discovered by fetching the asset
  host's `background/` directory and parsing its autoindex
  (nginx/apache/caddy) — same idea as iniswap.txt seeding the wardrobe;
  the current + last-seen backgrounds always seed it so it's never empty.
  The discovered list is **cached per server** (like the char list), so the
  next session's picker and slideshow show it **instantly** while a fresh
  listing refreshes in the background.
  Each cell shows a `defenseempty` thumbnail; hover or click previews it
  large; a `/bg <name>` button asks the server to change it for the area
  (rehearsal applies the pick locally). Hosts with directory listing off
  just show a clean "no listing" note and the seeds. Thumbnails are
  full-resolution backgrounds, so to stay inside the 64 MiB texture budget
  the picker only loads as many as fit (byte-budgeted off the observed size);
  on HD-background servers the cells past the budget show their name until you
  scroll, instead of thrashing the cache and flickering. (Small-background
  servers fit everything and show every thumbnail.)
- **Favorite backgrounds** (the ★ on each picker cell): pin the backgrounds
  you use, exactly like the wardrobe star. Favorites are **saved per server**,
  float to the top of the list, and a **"Favorites only"** checkbox filters the
  grid to just them — so your go-to scenes are one click away even on a host
  with no directory listing. Click the star again to unpin. Your favorites also
  show up in the **Wardrobe's Backgrounds section** (below), where you can sort
  them into folders and change the room to one with a click.
- **Background slideshow** (Settings → **OFF by default**): when the courtroom
  is **idle** (no message on stage), the stage cycles through the server's
  backgrounds every few seconds (configurable, 3–600 s) as ambiance. It's a
  pure render-time overlay — it never changes the area for anyone and never
  touches a live scene, so **the instant a message arrives the real background
  is back**. The desk is hidden while it cycles so the scenery reads clean.
  Backgrounds are discovered the same way the picker finds them.
- The custom shout button appears **only for characters that ship one**
  (char.ini `custom_name` or `[Shouts]`; a streaming client can't stat
  `custom.gif` the way AO2-Client does).
- **Wheel scrolling is hover-gated everywhere** — lists only scroll
  under the pointer (music list used to scroll from anywhere).
- The Settings showname field writes through to preferences; it can no
  longer clobber a name set from the courtroom with a stale copy on
  Back.
- Evidence ● armed indicator, modcall dialog, casing role checkboxes —
  all in the courtroom chrome or Settings.
- **Settings page wheel-scrolls** with a right-edge bar; spinbox rows
  keep first claim on the wheel (hover-tune never page-scrolls).
- **HiDPI auto-scale** (default ON): the global UI scale follows the display
  DPI (96 = 100%, snapped to the settings step); untick for a manual **UI scale
  slider** (75–200%, drag or wheel). The scale is a true whole-frame zoom — text
  *and* sprites grow together. Below it sit two independent **text-size sliders**
  — **Chat log text** and **Chatbox text** — that resize just the IC/OOC log +
  message text *without* zooming the courtroom art (the same persisted scales the
  in-courtroom Ctrl+wheel zoom tunes, so a change here shows at once and survives
  restart). **Theater mode** (Ctrl+T, the UI... panel, Esc exits): the
  borderless stage alone — viewport, chat overlay, splashes — session-
  only by design.
- **AsyncAO chrome themes** (#M3, **Settings → AsyncAO appearance**): pick the
  **client UI** palette — **Dark** (default), **Soft Dark** and **Warm** (the
  eye-friendly pair: gentler contrast, one calm accent, dim-but-readable
  secondary text — Warm is low-blue for long sessions), **Midnight**, **Light**,
  or **High contrast** — applied to AsyncAO's own panels, **separate from AO2
  courtroom themes**. The choice is the *base* palette; a server theme that ships chrome
  colours (`courtroom_stylesheets.css`) still overlays it, and a built-in
  readability floor keeps text legible either way. 100% local, persisted, and
  free on the render loop (the kit colours are package vars read as values —
  reassigned only on a theme/preset change, never per frame).
- **Font override with CJK fallback chain** (Settings → IC/OOC font):
  semicolon-separated TTF/TTC paths; every message and log line picks
  the first chain font covering all its runes (CJK fonts cover Latin, so
  mixed text lands right), embedded font as last resort. Files read
  off-thread; picks memoized per line (no per-frame glyph probing). A persisted
  **Dyslexia-friendly font** toggle (OFF by default) drives the chat + log text
  with the **bundled OpenDyslexic** (SIL OFL 1.1, embedded in the binary — no
  install needed) and takes precedence over the manual override; both resolve
  through a single launch/live path, so the toggle survives a restart.
- **Colour emoji** (per-glyph font fallback): messages that mix text and emoji
  (`hi 😀`, `❤️`, ZWJ families like 👨‍👩‍👧, skin tones, flags) render the emoji in
  **full colour** from the system emoji face (Segoe UI Emoji on Windows) while the
  text stays on the chat font — split per glyph and baseline-aligned. **Zero cost
  to plain messages:** a one-message byte scan keeps text-only lines on the
  untouched single-font fast path; only a message that actually contains emoji
  takes the fallback build, and the per-frame draw + the render alloc gate are
  unchanged. The emoji face is read **off-thread on first use** (so a player who
  never types emoji never pays the ~12 MB read) and pre-warmed at the chat size.
  Compound sequences (variation selectors, ZWJ, keycaps) are absorbed into one
  emoji run so they don't fragment. (Where there's no system emoji font, emoji
  fall back to the chat font as before.) The **IC / OOC input boxes** render
  through the same per-glyph fallback, so emoji + non-Latin scripts you **type**
  show real glyphs instead of tofu — gated on a non-ASCII byte so a plain message
  keeps the field's exact single-font fast path (the caret is exact for ASCII).
- **Case notebook** (Notes tab, per server): right-click an IC log line
  or hit "Pin to notebook" on evidence; free-form notes + copy-all; one
  JSON per server, async writes, capped.
- **Clickable links in the IC log**: when a message contains an `http(s)://`
  or bare `www.` link, hovering its line in the log highlights it (and shows
  the URL); a **left-click opens it in your browser** (a bare `www.` link is
  opened as `https://…`). The whole message line is the hit target. Right-click
  still pins the line to the notebook.
- **Links in the OOC log too**: hovering an OOC line with an `http(s)://` or
  bare `www.` link highlights it; **left-click opens it**, **right-click copies
  the URL** to the clipboard (the IC log pins on right-click, so OOC takes
  copy). A wrapped/linked message highlights as one block, keyed by its source
  entry — two adjacent messages sharing the same URL stay independent. The URL
  is detected only on the hovered line, so it costs nothing per frame.
- **Select & copy log text** (IC and OOC): **drag to highlight** any span of
  characters across lines, **Ctrl+C** copies it. The selection is anchored to
  content (not screen rows), so scrolling or new lines never corrupt it, and
  the hit-test binary-searches glyph widths only on the line under the cursor
  (no per-frame cost). The **highlight colour is configurable** in Settings via
  an **HSV colour wheel** (drag hue/saturation), a **brightness slider**, and a
  **hex code** field.
  A drag never also opens a link or pins a line; a plain click clears.
- **Force character names** (Settings, off by default): show every speaker's
  **character** name instead of their custom showname, in both the chatbox and
  the IC log — true-roleplay immersion and anti-impersonation for casing.
- **Per-server wardrobe**: custom character lists no longer carry
  between servers; the pre-split collection migrates once to the first
  server joined after updating.
- **Wardrobe folders** (app-drawer style): folders are real objects you open,
  not just filters. At the top level the grid leads with a **folder icon** per
  category; **click one to open it** (the grid then shows only that folder's
  characters) and a **‹ All folders** button takes you back. **Drag a character
  cell onto a folder icon to file it**; drag it onto the back button to take it
  out again. To make a new folder, type its name in the **New folder** box — a
  folder icon appears immediately so you can drop the first character onto it
  (the folder becomes permanent once it has a member). A plain click still
  wears the character; a small drag threshold tells a click from a drag. Other
  ways to file: **right-click → "move to folder"** (the only cross-folder move
  once you're inside one; this menu also has **× Remove from wardrobe**),
  **number keys 1-9** (file the hovered character into that-numbered folder,
  0 = take out), or add with `folder/char`. **Search spans every folder.**
  Folders are per server.
- **Removing things**: the **★** on a cell removes that character from the
  wardrobe (or unstars a background); right-click a character → **× Remove from
  wardrobe**. **Delete a whole folder** with the **×** that appears on a folder
  icon when you hover it — a confirmation then offers **"Delete + N items"**
  (removes the folder's characters/backgrounds from your favorites) or **"Keep
  items"** (just dissolves the folder, leaving everything unfiled). A folder
  also disappears on its own once nothing is filed under it.
- **Three wardrobe sections — Characters, Backgrounds, Iniswaps**: tabs at the
  top of the Wardrobe switch between them. **Characters** is *your* curated set
  only — the iniswaps you've starred or filed into folders — so it stays clean
  instead of listing the whole server. **Iniswaps** is a flat browse of the
  server's `iniswap.txt` **only** — favourites that aren't on the server's list
  don't show here, they stay on Characters: search it, hover to preview, click
  to try one on, and hit **★** to add it to your Characters wardrobe (where you
  can then file it into folders). A server that publishes no `iniswap.txt` just
  shows a note and an empty list (nothing to fetch). Both grids render from the
  same backing list with the same indices, so switching tabs never repaints the
  wrong thumbnail (the index-keyed icon cache stays valid).
  The Backgrounds section uses the *same* navigable folders: drag a background
  onto a folder icon to file it, open a folder to see inside, and click a
  background to change the room to it (`/bg`). Its ★ removes a background from
  favorites. Favorites are added by starring in the Background picker; the
  section shows a hint pointing there when it's empty. Background folders are
  saved per server, just like character folders.
- **Per-server character keybinds**: a key badge on each wardrobe cell
  binds a plain key (press A → wear that character instantly); fires
  only with no text field focused. Right-click the badge to clear.
- **Quick-swap (Ctrl+J by default)**: cycles through this server's wardrobe
  characters and wears the next one, wrapping around — a fast way to flip
  between your starred cast without opening the menu. It advances from
  whatever you're wearing now, so the cycle stays predictable even after a
  manual pick. The key is rebindable in Settings → Hotkeys (and listed in the
  F1 cheat sheet); an empty wardrobe just shows a hint.
- **Try-before-wear preview**: hovering a wardrobe character pops the usual
  sprite preview, but now you can **flip through that character's emotes**
  right in the preview box — the `<` `>` buttons or the Left/Right arrow keys
  cycle them, with a caption naming each one. It reads the character's emote
  list from the char.ini that was already warmed on hover (no extra download),
  so you can see a character's range *before* committing to wear them. Nothing
  is sent — wearing in the courtroom only affects your own next line anyway.
- **Settings export/import**: Export writes the complete preferences
  file (knobs, favorites, wardrobes, keybinds, learned formats) beside
  the exe; Import = arm the button and drop the .json — applied on
  restart, with the saver frozen so the import can't be clobbered.
  **Saved passwords are stripped from the export** (the bundle is built
  to travel to another machine) — the username and the auto-login choice
  ride along, so you re-type just the password on the new PC.
- **Offset ghost editor** (pair panel): drag your idle sprite on a
  miniature stage to set self-offsets; the partner shows as a
  translucent ghost at their last-known placement. **Arrow keys nudge** your
  offset 1% at a time (when no text field is focused) for fine placement.
- **Click-to-pair** (`/pair <UID>` shortcut for servers that sync pairs via the
  OOC command): **double-click a speaker's IC line** to open a pair popup.
  AO's IC packets carry only the character, not the player UID, so the UID is
  harvested from **`/getarea`** (parsed passively) — the popup pre-fills it on a
  confident match, **else you type it** (always-available fallback), and a
  **Refresh** button runs `/getarea` to fill a **clickable roster** where each
  row carries the real UID (no name-matching needed). Single-click stays free
  for read/select; links keep their single-click.
- **Macro system** (Settings → Macros): name + optional plain-key bind +
  a sequence of OOC lines (separate steps with `|`), sent paced so
  prompt-style flows work. Keys fire in the courtroom with no text box
  focused; macro binds win a key conflict over character binds. Caps:
  64 macros × 8 lines × 256 chars.
- **IC quick-phrases** (Settings → Controls): the **IC counterpart to macros** —
  bind a bare key to a canned line your **character says in IC** (e.g. `E →
  "Happy Pride Month"`). Pressing it sends the line through the normal IC path
  (your current emote/colour/character) **without disturbing your draft**; a
  `/command` phrase runs as the command. Keys fire only with no text box focused,
  so typing never triggers one, and they show on the F1 cheat sheet. Global +
  persisted, bounded.
- **Built-in account login** — for ANY server with a `/login` account
  system (member perks, donator ranks, mod powers — not just staff):
  credentials are keyed by the server's connection URL/IP (saved in
  PLAIN TEXT — the UI says so; password boxes render as asterisks for
  screenshare safety, and streamer mode masks the username too). The
  wire flow picks itself from the announced server software:
  Akashi = `/login` then `user pass` answering its prompt (not echoed
  into OOC); KFO = `/login pass` (no usernames); Athena/Nyathena/
  Whisker/unknown = `/login user pass`. **Auto-login is OFF by
  default** — ticked on, a join logs you in the instant the handshake
  completes; manual mode fires the same saved flow only when you
  trigger it (courtroom Login... button or the Ctrl+G hotkey).
  Settings → Auto-login configures ANY known server ahead of time via
  a server picker (lobby + phone-book entries, the connected server
  first) — no connection needed; the flow preview names exactly what
  will be sent. Auto-login fires **at most once per join** — the "ready"
  (`DONE`) signal can arrive more than once (the WAP/Akashi fork and area
  changes re-send it), which used to re-fire the saved login and spam OOC;
  a per-session latch (`autoLoginTried`, cleared on reconnect) caps it at
  one attempt (v1.55.4). **Every family — stock Akashi and the WAP /
  witches-akashi-party fork (`SoftwareWitches`, announced "WAP-Akashi")
  included — auto-logs in once on join** with its own flow (v1.55.6); a
  manual login (courtroom Login... button / Ctrl+G) always works too.
- **OOC identity**: a default OOC name in Settings applies on every
  join (like the showname); when blank, commands and macros send as a
  sticky random `AsyncAO<1-200>` minted once per run — OOC commands
  always work even with no name set.
- **Perf HUD (F3, any screen)**: live frame-time graph (last 120 frames;
  green under 16.7 ms, amber to 33 ms, red past it, with the 60 fps line
  drawn in), average/worst frame + fps, heap vs the 256 MiB GOMEMLIMIT
  budget (amber at 75%, red over), GC pause p99, cache hit rate, network
  probes, and cached 404s — rendered from the 1 Hz sampler that already
  powered `--debug` logging. F3 again hides it.
- **Blankposting**: Enter on an empty IC input sends the AO single-space
  message — your sprite plays with no text (truly empty messages get
  server-rejected; the space is the cross-server convention). When a
  blankpost is on stage the whole chatbox — frame, name and text — is
  hidden, so only the sprite shows (great for silent animated posts). The
  box stays hidden through the preanim too: it never flashes empty. Any
  whitespace-only or markup-only message counts as a blankpost.
- **Per-server theme bindings** (Settings → "Bind theme to server"):
  pick any known server and bind the selected theme to it — joining
  that server always applies it (tabs and rehearsal included), leaving
  restores the global theme. Unbind any time; the row shows the current
  binding.
- **Live layout editor** (UI... → Edit layout, themed courtrooms): drag
  any widget across the screen, grab its corner grip to shrink/grow,
  right-click to reset one, Reset all for the theme. A **Snap** toggle
  (on by default) rounds moves and resizes to a tidy grid so widgets line
  up; flip it off for free-hand placement. **Ctrl+Z / Ctrl+Y undo & redo**
  any move, resize, or reset (each restores and re-persists the rects).
  Edits persist per theme as
  design-space overrides (window resizes keep working; the theme's own
  files are never touched). While editing, the real UI is input-fenced so
  nothing misfires.
- **Live layout editor on the default & Legacy layouts** (the same "Edit
  Layout" button — control row, Extras box, or UI…): the non-themed
  courtroom is editable too, and **both** the new default *and* the Legacy
  Developer theme share it. **Drag a box to move it; grab an edge to resize
  one dimension (horizontal *or* vertical) or a corner for both** — 8 handles
  per box. The **stage/viewport** moves *and* freely resizes (the scene fills
  it; while un-edited the View knob still owns its 4:3 size, and resetting the
  box hands size back to the knob); the **log / right column** (both themes),
  the **OOC box** (new default), the **emote grid** (it pages within whatever
  rect it gets), the **IC input bar** (colour · showname · Immed · Additive · emoji/FX/
  React · text — widen it for a longer input, the text field never collapses;
  Additive only shows on 2.8 servers that advertise it),
  the **Legacy bottom OOC bar**, and the **control-button block** (both button
  rows + the scale knobs, dragged as one unit — mostly vertically, since it
  stays full width) all move independently. Right-click a
  box to reset it, **Reset all** to clear every override, **Snap** to tidy
  placement, **Ctrl+Z / Ctrl+Y** to undo / redo (right-click only goes back to
  *default* — undo gets a previous *custom* spot back), and **Tab** to cycle the
  stack of boxes under the cursor when they overlap (the bottom hint shows how
  many are stacked). Overrides are saved as window *fractions* (config `classicLayout`),
  so they survive window resizes and persist across sessions — with **zero
  render-loop cost** when you haven't edited anything (the slot path is
  allocation-free; an un-edited courtroom is pixel-identical to before).
- **Tear-off tab "menus"**: in the layout editor a tray (just under the banner at
  the top, so it never covers bottom-anchored bars) lets you **pop a
  log tab (Music, Areas, Players, Notes, Friends) out of the docked strip into
  its own movable, resizable floating panel** that renders the tab's real
  content — drag/resize it like any other box, and it persists across sessions.
  The docked strip compacts to fill the freed space; **Log** stays put as the
  home tab. Click the tray chip again (or right-click the panel, or **Reset
  all**) to redock — so the default is still plain buttons. Each torn tab is
  just another layout *slot* under a `tab:<name>` key, so it reuses the editor's
  drag/resize/persist machinery at **zero render cost when nothing is torn**.
  (The music list may become its own slot in a later slice; it's largely
  covered already since the Music tab tears off.)
- **OOC: own box ⇄ log tab** — by default OOC chat gets its own box in the right
  column, but the layout editor's tray has an **OOC** chip that puts it back as a
  **tab in the log panel** (the old layout). In tab mode it's a **hybrid**: the OOC
  tab is a complete OOC chat (scrollback + its own input + your OOC name), and a
  **full-width OOC bar across the bottom** is a *second*, always-visible input — type
  from either; they share one draft. Saved across sessions; hidden on the Legacy
  theme (which always tabs OOC). OOC only ever has an **OOC name** (set once, in the
  box/tab) and the **OOC chat** — no IC showname (that lives on the IC bar and in
  Settings), and no duplicate name box on the bottom bar.
- **Tabbed settings**: the settings screen is split into category tabs
  (General · Theme · Assets · Audio & Chat · Account · Hotkeys) instead of
  one long scroll — click a tab, scroll within it, each tab remembers its
  own scroll position. Async work (theme scans, folder picks, dropped
  files, import/export status) runs regardless of the active tab. A
  **search box** (settings header) jumps to the tab that has a term —
  type "blip", "password", "catch up"… and press Enter.
- **Hotkey cheat-sheet** (press **F1** on any screen, the courtroom's **Hotkeys**
  button, or the **Extras → Hotkeys** entry — #79/#96): a translucent two-column
  panel listing **every** shortcut in one place — the Ctrl-chord actions resolved
  to your keys, the fixed function keys, **and your own custom bindings**: macros,
  character keys, showname keys, and IC quick-phrases. Anything *you* remapped or
  created shows its key in **gold**, so your bindings stand out from the defaults.
  Section headers group them; an ✕ or F1 closes it. The rows are built once per
  open (never per frame) and only drawn while open, so it's zero cost closed.
  A **Hotkeys** button now sits on the main courtroom screen in both layouts — on
  the classic utility bar (after the Pos selector) and beside **★ Extras** at the
  bottom-left in themed mode — so the list is one click away without recalling F1.
- **Mute SFX hotkey** (Ctrl+K by default, rebindable): a session-only
  "shush" that silences sound effects without touching your saved volumes
  or the music/blip channels.
- **Reduce motion** (Settings → General, accessibility): suppresses the
  screen shake and realization flash (the effect *sounds* still play);
  also governs the text effects added later. It now strips **every**
  continuously-animating style a speaker can transmit — not only wobble / spin /
  the named motion enum but also a **custom drawn motion path**, the **hue-cycle
  rainbow**, and **glitch** (including the glitch-static flicker, a
  photosensitivity hazard) — so nothing another player sends can keep the stage
  moving while Reduce motion is on.
- **Viewport camera zoom (hyperfocus)**: Ctrl+wheel over the stage zooms
  toward the cursor (up to 6×) — sprites, preanims, and effects magnify
  together; Ctrl+drag pans while zoomed; the 1× chip (or zooming out)
  resets. Sprite dragging pauses while zoomed.
- **IC/OOC logs auto-scroll**: stuck to the newest line until YOU scroll
  up (then they hold position); scrolling back to the bottom — or the
  jump-to-newest hotkey — re-sticks. Replaces the old near-bottom
  heuristic that broke whenever a wrapped message added several rows.
  While you're scrolled up reading backlog, a **"↓ N new" pill** appears at
  the bottom of the IC log showing how many messages arrived since you caught
  up; **left-click snaps straight to the newest message**, **right-click jumps
  to the FIRST unread line** (read forward from where you left off — the
  Jump-logs hotkey also goes to newest), and a thin accent **divider** marks the
  read/unread boundary.
- **Text zoom (log + chatbox)**: **Ctrl+wheel** resizes the text one step at a
  time; **hold the middle (wheel) button and scroll** to zoom **fast** (5× the
  step) — a quick way to size text up or down. One shared handler drives the IC
  log, the OOC log, the whole right-column panel, and the chatbox message text,
  so the gesture is identical everywhere; the zoom consumes the wheel, so it
  never also scrolls.
- **Multi-server tabs** (default 6, **configurable up to 99** in Settings →
  General → "Max server tabs"): Join while connected opens a NEW tab —
  the old session parks and keeps running (its packets drain on a
  per-frame budget into its own logs; unread counts and callword
  flashes still fire). A floating chip strip (top-center, every screen)
  switches tabs, shows unread, closes background tabs (×), and clicking
  the active chip drops you to the lobby with the session still live. A
  **"+" chip** at the end of the strip (accent-bordered, with a hover hint)
  is the discoverable way to open another server — it parks the current
  session and opens the lobby to connect a new tab; it hides at the cap.
  Rooms exist only for the active tab (nothing animates off-screen);
  activation rebuilds the courtroom from the session state. Every
  per-session setting — your **iniswap, /pos side, and pair placement**
  included — is parked with its tab, so switching servers never carries one
  tab's character or pairing into another. Caches need nothing: asset keys
  are full URLs, per-server separation is structural. Rehearsal never
  backgrounds (it owns the offline gate).
  **Drag-reorder**: grab a chip and drag it along the strip to reorder —
  it lifts (accent border) and the other chips slide live as you cross
  them; the active tab keeps its place in the lineup. A small drag is told
  from a click by a 6px threshold, so a plain click still switches/closes
  and a drag never does both. (The right × hot-zone still only closes.)
  **Reopen tabs on launch** (Settings → General, **OFF by default**): when on,
  the open servers are remembered on exit and reconnected next launch — the app
  reopens each one (one reconnect per frame, so the dials never pile into a
  single freeze, and each on a short timeout so a since-dead server can't hang
  startup). Off by default and zero boot cost when off: nothing is read or
  reconnected, and nothing is persisted, so the default launch is byte-identical.
  A dead remembered server just shows its error and is skipped.
- **Connect-time ("Ping") sort** (lobby **Ping** button, opt-in): probes every
  joinable server's TCP-connect RTT on a bounded worker pool (8 at a time, 2 s
  cap each) and re-sorts the list by it — favorites still pinned, then fastest
  first, unprobed/unreachable last. Each row shows `NNms` (`…` while probing,
  `✕` unreachable); press again for the player-count order. It's a *connect*
  time (rough relative latency, not ICMP, and undercounts the TLS leg) — the
  button tooltip says so. Nothing probes until you press it (default lobby is
  byte-identical), and the cache clears on Refresh.
- **AsyncAO Server Phone Book** (lobby **★ Phone Book** toggle): a dedicated page
  showing **only your saved servers** (the list filters to favorites), with an
  **Add server** form (name + `host:port` / `ws://` / `wss://`). Saved servers
  live in Favorites — which **survives "Reset settings"** and is cleared only by
  **"Wipe everything"** — so they stay forever. Click a row to connect; the ★
  removes it. **Copy/Paste export-import** shares the whole book via the
  clipboard (additive, dedup by URL) — render-thread-safe, no file needed.
- **Rehearsal mode** (lobby → select a visited server → Rehearse):
  browse its character roster and play emotes entirely offline from the
  cache — the manager's network gate closes structurally, nothing
  probes, nothing sends. The viewport carries a REHEARSAL badge.
- **`asyncao-cache` companion CLI** (pure Go, `CGO_ENABLED=0`): stats /
  inspect / prune (-older, -max-bytes, -all) / warm a URL list or char
  icons via a server's `extensions.json` into T3 — pre-seed a fresh
  install before ever connecting.
- **Built-in single-asset downloader** (Settings, **OFF by default**): for
  hosts that serve a directory listing, grab one character or background
  straight off the server instead of a multi-GB pack. Turn it on and a
  download (↓) badge appears on each character (char-select) and background
  (Background menu) cell; it walks that folder's autoindex and saves the
  files under `downloads/` — and a downloaded character also pulls the
  `sounds/general` sfx and `sounds/blips` its `char.ini` names (those live
  outside the folder, so a plain grab would be silent — the `char.ini` is
  re-read from the server so this never depends on the just-walked copy).
  A floating progress chip ("Downloading … — N files, X MiB") shows from any
  screen while a grab runs, and the cell is marked. Bounded (file / byte /
  depth caps), off-thread, cancelable, and path-traversal-guarded. Point
  "Read assets from local folders" at the downloads folder to use the grabs
  offline / in rehearsal. **Local mounts now resolve names with spaces and
  parentheses**: a mounted pack folder like "Phoenix Wright" was requested as
  `phoenix%20wright` and missed on disk — the local reader now also tries the
  percent-decoded path (the `..` traversal guard runs on the decoded form too, so
  an encoded `%2e%2e` can't escape the mount).
  **v2:** grabs **queue** instead of refusing while one runs (the chip shows
  "+N queued"); **right-click** a background cell to queue it; **Pause / Resume**
  the active grab and the whole queue (Settings); and an optional **bandwidth
  cap** (KiB/s, 0 = unlimited) paces downloads so a big grab doesn't hog your
  connection.
- **Discord Rich Presence**: **enabled by default** on a normal build (untick
  in Settings → Discord, or run a `-tags nodiscord` build), per-field privacy
  checkboxes, zero build/run dependency. The official AsyncAO **Application ID
  is baked in** (no user-editable ID box), so it shows "Playing AsyncAO" out of
  the box whenever Discord is running — full guide in [DISCORD.md](DISCORD.md).
- **Update check + "What's New"** (Settings → on by default): one async check
  of the GitHub Releases API at launch — fired once, after the window is up,
  **never on the boot path** (zero startup cost), and a dev build skips it
  entirely. When a newer version is published, a top-right **"Update N
  available"** chip appears and a **What's New** panel shows that release's
  **patch notes** (scrollable). Turn the setting off for no outbound call.
  **"Get the update"** then downloads the new build next to the running exe,
  verifies it, and **stages an atomic self-replace** (rename the old aside →
  move the new in, **rolling back** if anything fails — so an AV lock mid-swap
  can't break the install); restart to finish, and the old binary is cleaned
  up on the next launch. A read-only install (Program Files) or a release with
  no matching asset degrades to opening the release page. Verification is
  **integrity** (size / SHA-256), not yet authenticity — release signing can
  slot in later.
- **Settings & About — sidebar + cards layout**: both pages were re-laid-out for
  a cleaner, more professional look (**all the text is unchanged**). **Settings**
  now has a **left category sidebar** (General · Theme · Assets · Audio & Chat ·
  Account · Hotkeys · Studio) instead of the cramped top chip row, and each
  section is a **card** on a distinct surface inside a width-constrained content
  column, so labels and controls line up and the page no longer stretches
  edge-to-edge on wide monitors. **About** reflows its prose into a **centered
  reading column** (paragraphs wrap to the window instead of fixed hard-wrapped
  lines), keeps the Mayo portrait in its section, and groups the credits/links
  into **titled cards**. Pure layout work — the About wrap is cached by width
  (no per-frame allocations), and the `BenchmarkRenderFrame` alloc gate is
  unchanged.
- **Linux AppImage** (CI + `scripts/build-appimage.sh`): a self-contained,
  single-file Linux download that **bundles the SDL2 / SDL2_ttf / SDL2_mixer /
  libwebp / libavif runtime** (resolved from the binary by `linuxdeploy`) so it
  runs with no install step — the Linux equivalent of the Windows DLL bundle. CI
  builds it on every push in **two flavors**, default and **Discord-free**
  (`-tags nodiscord`), uploaded as `asyncao-linux-x86_64-AppImage` and
  `…-AppImage-nodiscord`. Build it locally with `scripts/build-appimage.sh`
  (see [BUILDING.md](../BUILDING.md)).
