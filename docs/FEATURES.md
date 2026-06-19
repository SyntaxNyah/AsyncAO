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
| Custom shouts (2.10) | MS `4&<stem>` | charini [Shouts], shout row | streaming clients can't list `custom_objections/` — the char.ini `<stem>_name` keys are the discoverable source; ▾ cycles picks; receive resolves `custom_objections/<stem>` art+sfx |
| Per-emote audio | MS SFXName/Delay/Looping/Blipname | charini SoundN/T/L + Blips | emotes now send their char.ini sounds; 2.9.1 per-emote blips |

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
  paced window instead of vanishing to black.
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
- **Decode-failure backoff**: a payload that downloads but won't *decode*
  (a corrupt/truncated file — e.g. an AV mangling the TLS stream — distinct
  from a 404, which the network tier already caches) goes into a short-lived
  negative cache, so one bad asset isn't re-fetched + re-decoded every retry
  interval (it can't render regardless). The failure logs **once** per window
  instead of flooding, and a transient failure still recovers after it.
- **Learned-format export/import**: `learned-formats.json` beside the exe;
  one player's warm state seeds another's.
- **AVIF**: `.avif` probe format; `ftyp avif/avis` sniffing, libavif CGO
  decode (stills + animated sequences), Settings chip like every format.
- **Typing-driven speculation**: picking an emote prefetches its idle/talk/
  preanim/SFX at LOW priority; the Markov predictor now also learns
  per-character **emote chains** and warms the predicted next speaker's
  predicted next emote.
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
- Theme text colors apply **only over their own skin** — on the flat
  fallback panel the client's readable defaults win (black-on-dark fix).
- **Ink readability guard**: at load time the theme's message/showname
  colors are luma-checked against the actual decoded chatbox pixels;
  ink with no contrast against its own skin (real themes ship dark ink
  on dark skins) is dropped for the client default, with a debug-log
  verdict. Choosing White in the IC color dropdown always reads.
- **Inline text colors + bold/italic** (type them in your message): `\c1`…`\c8`
  switch the text color mid-sentence (1 green, 2 red, 3 orange, 4 blue, 5 yellow,
  6 pink, 7 cyan, 8 gray), **`\cr` is rainbow** (each letter a different color,
  flowing across the line), and **`\b` / `\i` toggle bold / italic** (they nest
  and combine with colors). Write `\\` for a literal backslash; any other `\x`
  is left as-is, so ordinary text and file paths aren't eaten. The colored
  message types out normally — each color is its own span, revealed letter by
  letter with the usual zero-cost reveal. This is **AsyncAO-native and
  render-only**: incoming messages from other clients are unaffected, and the
  markup you send won't color on stock AO clients (they'd see the `\c2`).
  The IC *log* shows the clean text (codes stripped, same as the chatbox);
  coloring the log itself is a later step.
- **Random / rainbow message colour** (M61, Settings → General, both OFF):
  **Random colour** picks a fresh palette colour for each IC message you send
  (the standard TextColor field — every client sees it); **Rainbow** prefixes
  `\cr` so your text cycles the palette per letter (renders on clients that read
  inline colour; rainbow wins if both are on). Applied at send (`funColor`), so
  zero render cost.
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
- **Animated theme art plays**: chatbox skins, `btn/` buttons, screen
  backdrops, HP bars, and the settings preview step their frames on a
  per-apply animation clock (`pageFrameLoop`) instead of freezing on
  frame 0 — splashes/badge already animated. The hover sprite preview
  loops its idle too.
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
  on-demand T3 size measurement, open-in-Explorer, clear buttons.
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
  inherits dead air). An **■ Stop replay** button shows on the stage while one
  plays. A **Playback speed** slider (Studio tab, 25–200 %, **100 % default**)
  paces the replay: it's deliberately **slower than live chat** so the whole
  message types out and lingers long enough to read, and the slider adjusts it
  **live** — drag it mid-replay and the next line picks up the new speed.
- **Scene maker** (M16, the same **Studio** tab): a full **in-app scene editor**
  over the very same `.aorec` model — **build a scene from scratch or edit a
  recording**. **🎬 New scene** (works **offline**, no server needed) or **✎ Edit**
  any saved recording opens a full-window editor: a left **event list** you can
  **add / reorder (▲▼) / duplicate (⎘) / delete** lines on, and a right panel to
  set each line's **character, emote, showname, text, position, colour, flip,
  desk** and an optional **pre-animation** — plus **background** and **music**
  events. **Quality-of-life:** **+ Line inherits the previous speaker** (so a
  back-and-forth doesn't make you retype the character every line — you just edit
  the text), **⎘ Dup** clones a line, **▶ Preview from this line** iterates a late
  line without replaying the whole scene, and **character + background
  autocompletes** suggest matching folders from the connected server's roster /
  discovered background list as you type (real searchable pickers — they stay
  cheap even on a 4000-character server, where a flat dropdown would be useless).
  A **📂 Open** button loads any
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
- **Scene → GIF export** (M16, **🎞 Export GIF** in the maker): render a scene to
  a **shareable animated GIF** — "an animation of people talking." It renders the
  scene through a throwaway replay room into a **fixed off-screen target** (a
  capped 480×360, so it's small + memory-bounded), quantizes each frame and drops
  the source at once (only 1-byte/px paletted frames are kept, hard-capped at
  ~33 s), and encodes off-thread to `recordings\<name>.gif`. It runs
  **incrementally behind a progress bar** (with **■ Stop & save**) so the window
  never freezes, and it's **off by default** — zero cost on the live render path
  (the render loop stays at 0 allocs/op). *(v1 captures the viewport — sprites,
  poses, background animating; the chatbox-text overlay and a higher-quality
  animated-WebP export are the follow-ups.)*
- **Screenshot** the whole window to a **PNG** under `screenshots/` (Ctrl+S),
  written off the render thread; ~10× smaller than the old BMP and it previews
  inline in Discord etc.
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
  vanish live. Rows **group by area** (a header you **click to jump there**). Each
  row has a char icon, role highlights (you · the current speaker · friends),
  Spectator/CM chips, a sort toggle (UID · name · speakers-first), a **`/pair
  <uid>` button**, **Copy-UID**, and (opt-in) a **Follow** button (M3) — straight
  from the live UID, no `/getarea` needed. **Follow is OFF by default**: tick the
  **Follow** box in the player-list header to switch the per-row Follow buttons on
  (the toggle is remembered; turning it off also stops any active trail). When on,
  **Follow** trails a player across areas: AsyncAO **auto-jumps you to their area
  whenever they move** (debounced; the row reads *Following*, click again to stop)
  — a mod tailing a suspect or catching up to a friend, riding the same live PR/PU
  area data. The
  **Areas tab** also keeps a **Recent:** strip — one-click chips to jump straight
  back to areas you've just passed through (newest first, current area excluded).
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
- **Reconnect, manual + automatic** (M2): when a connection drops or a join fails,
  the lobby shows a **"Reconnect to &lt;server&gt;"** button that re-dials the last
  server you tried (the name + ws URL are remembered on every connect attempt). On
  an **unexpected drop**, AsyncAO also **auto-reconnects** with exponential backoff
  (≈2 s → 30 s, capped, several tries) until the server returns — the lobby shows
  the retry status and a **Stop** button. A **deliberate Disconnect never
  reconnects**, and a manual Reconnect / fresh Join takes over. Toggle in
  **Settings → Audio & Chat** ("Auto-reconnect after a dropped connection", ON by
  default). Idle it costs one time-compare per frame.
- **Showname presets**: a global, persisted list of shownames managed in
  **Settings → General** (add, *Save current*, **Use** to apply one — the active
  preset is marked — remove with ×; cleared only by a factory reset). **Ctrl+H**
  swaps to a random saved preset and **Ctrl+B** cycles to the next (both
  rebindable). Each preset also takes a **per-preset keybind** — click *Bind key*,
  press a key, and that key swaps your showname to that preset in the courtroom
  (right-click the button to clear). The bind survives all but a factory wipe and
  drops automatically if you remove its preset.
- **Music changes in the IC log** (webAO/AO2 parity): when someone plays a song
  the log shows "*&lt;name&gt; has played a song: &lt;song&gt;*" (and "*has
  stopped the music*" on stop), named by the MC showname or the character. The
  **`~stop` sentinel halts playback immediately** instead of fetching a track
  that 404s, and **disconnecting from a server stops the music** too.
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
- **Callwords** (M15 **manager**): a managed highlight-word list — type a word
  (or paste `a, b, c` to add several at once) + **Add**, and each shows below
  with a **×** to remove (replaces the old single comma field; lowercased,
  deduped, capped at 32). An IC/OOC match = taskbar
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
  heard word, like the modcall/friend toasts. (Opus support needs the codec DLLs
  loaded at startup — see the audio note below.)
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
  **Per-friend glow colour**: append `=RRGGBB` to a name in the list (e.g.
  `blank=ff4488`) to give that friend's glow a custom colour; names without one
  use the default warm tint. **Pulse the glow** is its own toggle — a gentle
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
  **Settings → Audio & Chat** (add/remove domains; paste a full URL and it's
  normalized to the bare host). The server's own music (bare names, the server
  host) **still plays — it just isn't recorded** (you already have it on the
  server; this feature is for grabbing songs from elsewhere). **Discord records
  audio files only** (`.mp3`/`.opus`/…), since most Discord CDN links are images.
  One **built-in path rule** ships always-on (shown read-only under the list):
  **`miku.pizza/base/youtube/`** — Skrapegropen's folder of user-hosted YouTube
  rips — records **only the audio files under that one path** (`…/youtube/Song.opus`),
  while the rest of `miku.pizza` (the server's own library) stays unsaved as above.
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
  watch it before sending; otherwise its talking sprite. Both layouts.
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
- **Font override with CJK fallback chain** (Settings → IC/OOC font):
  semicolon-separated TTF/TTC paths; every message and log line picks
  the first chain font covering all its runes (CJK fonts cover Latin, so
  mixed text lands right), embedded font as last resort. Files read
  off-thread; picks memoized per line (no per-frame glyph probing). A persisted
  **Dyslexia-friendly font** toggle (OFF by default) drives the chat + log text
  with the **bundled OpenDyslexic** (SIL OFL 1.1, embedded in the binary — no
  install needed) and takes precedence over the manual override; both resolve
  through a single launch/live path, so the toggle survives a restart. *Known
  limitation:* **colour emoji
  and other supplementary-plane characters (U+1F300+, e.g. 💔🥀) render as
  boxes** — the bundled SDL_ttf (2.0.18) has 16-bit glyph metrics and no colour
  glyphs, so a fallback font can't help; needs an SDL_ttf 2.20+ upgrade. See
  [KNOWN-ISSUES.md](KNOWN-ISSUES.md).
- **Case notebook** (Notes tab, per server): right-click an IC log line
  or hit "Pin to notebook" on evidence; free-form notes + copy-all; one
  JSON per server, async writes, capped.
- **Clickable links in the IC log**: when a message contains an `http(s)://`
  link, hovering its line in the log highlights it (and shows the URL); a
  **left-click opens it in your browser**. The whole message line is the hit
  target. Right-click still pins the line to the notebook.
- **Links in the OOC log too**: hovering an OOC line with an `http(s)://` link
  highlights it; **left-click opens it**, **right-click copies the URL** to the
  clipboard (the IC log pins on right-click, so OOC takes copy). The URL is
  detected only on the hovered line, so it costs nothing per frame.
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
  will be sent.
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
- **Tabbed settings**: the settings screen is split into category tabs
  (General · Theme · Assets · Audio & Chat · Account · Hotkeys) instead of
  one long scroll — click a tab, scroll within it, each tab remembers its
  own scroll position. Async work (theme scans, folder picks, dropped
  files, import/export status) runs regardless of the active tab. A
  **search box** (settings header) jumps to the tab that has a term —
  type "blip", "password", "catch up"… and press Enter.
- **Hotkey cheat-sheet** (press **F1** on any screen): a translucent panel
  listing every Ctrl-chord binding (resolved to your keys) plus the fixed
  function keys; F1 again closes it. Off by default, zero cost when closed.
- **Mute SFX hotkey** (Ctrl+K by default, rebindable): a session-only
  "shush" that silences sound effects without touching your saved volumes
  or the music/blip channels.
- **Reduce motion** (Settings → General, accessibility): suppresses the
  screen shake and realization flash (the effect *sounds* still play);
  also governs the text effects added later.
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
  activation rebuilds the courtroom from the session state. Caches need
  nothing: asset keys are full URLs, per-server separation is
  structural. Rehearsal never backgrounds (it owns the offline gate).
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
  offline / in rehearsal.
  **v2:** grabs **queue** instead of refusing while one runs (the chip shows
  "+N queued"); **right-click** a background cell to queue it; **Pause / Resume**
  the active grab and the whole queue (Settings); and an optional **bandwidth
  cap** (KiB/s, 0 = unlimited) paces downloads so a big grab doesn't hog your
  connection.
- **Discord Rich Presence**: optional, per-field privacy checkboxes,
  zero build/run dependency — full guide in [DISCORD.md](DISCORD.md).
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
