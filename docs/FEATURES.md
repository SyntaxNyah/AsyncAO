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
| Modcall | `ZZ` in/out | drawModcallDialog, EventModcall | Reason dialog (`{reason, "-1"}` per courtroom.cpp:6530); receive = OOC pin + `mod_call` sound + taskbar flash |
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
- Settings shows a live chatbox preview with the applied colors.

## Diagnostics

- **Debug overlay** (Settings toggle): bounded ring (120) of failures —
  missing assets with the formats tried, theme apply verdicts, disconnect
  reasons, `extensions.json` results, **unhandled/malformed packets**
  (EventDebug from the session reducer) — plus a health line: handshake
  phase · server software · last packet age. A hung server reads as a
  stuck phase with a climbing age.
- Settings cache browser: live T2 stats (entries/bytes/budget/hit rate),
  on-demand T3 size measurement, open-in-Explorer, clear buttons.

## Quality of life

- **IC log**: 1024-line color-preserving scrollback with search, Copy to
  clipboard, and TXT/HTML export (`logs/` beside the exe; HTML keeps the
  AO palette). Lines **word-wrap to the list width** (cached against
  log/width/font-scale — never re-wrapped per frame).
- **Callwords**: comma-separated highlight words; IC/OOC match = taskbar
  flash + `word_call` sound.
- **Hotkeys** (Ctrl+key, configurable in Settings): shouts 1..4, pos
  cycle, music stop (`~stop.mp3` fake-track request, courtroom.cpp
  music_stop), log jump, screenshot (`screenshots/` beside the exe).
- **Hideable chrome**: shout row, layout knobs, emote grid, right column,
  OOC row, HP bars, clocks, badge, judge row — persisted per user.
- **Stop music** button on the Music tab.
- **Real dropdowns** for the IC text color (named colors + live swatch)
  and the position selector (AO2 ui_pos_dropdown parity, SD list when
  sent) — open lists draw above everything, auto-widen to their options,
  flip at window edges, and modally capture the pointer.
- **Tab / Shift+Tab cycles focus** across visible text inputs in draw
  order (IC → OOC → search ...), wrapping both ways.
- Clicking an emote **refocuses the IC input** (AO2 focus_ic_input) —
  pick and keep typing.
- **Emote grid pages** (`<` / `>` + a `page x/y · N emotes` counter) when
  a character ships more emotes than fit — both the classic and themed
  layouts. The arrow row only appears when paging is needed; loading a
  character resets to page 1.
- **Background picker** (courtroom Background button): a thumbnail grid of
  every background, modeled on the wardrobe menu. AO has no "list
  backgrounds" packet, so the set is discovered by fetching the asset
  host's `background/` directory and parsing its autoindex
  (nginx/apache/caddy) — same idea as iniswap.txt seeding the wardrobe;
  the current + last-seen backgrounds always seed it so it's never empty.
  Each cell shows a `defenseempty` thumbnail; hover or click previews it
  large; a `/bg <name>` button asks the server to change it for the area
  (rehearsal applies the pick locally). Hosts with directory listing off
  just show a clean "no listing" note and the seeds.
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
- **HiDPI auto-scale** (default ON): the UI scale follows the display
  DPI (96 = 100%, snapped to the settings step); untick for the manual
  spinbox. **Theater mode** (Ctrl+T, the UI... panel, Esc exits): the
  borderless stage alone — viewport, chat overlay, splashes — session-
  only by design.
- **Font override with CJK fallback chain** (Settings → IC/OOC font):
  semicolon-separated TTF/TTC paths; every message and log line picks
  the first chain font covering all its runes (CJK fonts cover Latin, so
  mixed text lands right), embedded font as last resort. Files read
  off-thread; picks memoized per line (no per-frame glyph probing).
- **Case notebook** (Notes tab, per server): right-click an IC log line
  or hit "Pin to notebook" on evidence; free-form notes + copy-all; one
  JSON per server, async writes, capped.
- **Per-server wardrobe**: custom character lists no longer carry
  between servers; the pre-split collection migrates once to the first
  server joined after updating.
- **Per-server character keybinds**: a key badge on each wardrobe cell
  binds a plain key (press A → wear that character instantly); fires
  only with no text field focused. Right-click the badge to clear.
- **Settings export/import**: Export writes the complete preferences
  file (knobs, favorites, wardrobes, keybinds, learned formats) beside
  the exe; Import = arm the button and drop the .json — applied on
  restart, with the saver frozen so the import can't be clobbered.
  **Saved passwords are stripped from the export** (the bundle is built
  to travel to another machine) — the username and the auto-login choice
  ride along, so you re-type just the password on the new PC.
- **Offset ghost editor** (pair panel): drag your idle sprite on a
  miniature stage to set self-offsets; the partner shows as a
  translucent ghost at their last-known placement.
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
  server-rejected; the space is the cross-server convention).
- **Per-server theme bindings** (Settings → "Bind theme to server"):
  pick any known server and bind the selected theme to it — joining
  that server always applies it (tabs and rehearsal included), leaving
  restores the global theme. Unbind any time; the row shows the current
  binding.
- **Live layout editor** (UI... → Edit layout, themed courtrooms): drag
  any widget across the screen, grab its corner grip to shrink/grow,
  right-click to reset one, Reset all for the theme. Edits persist per
  theme as design-space overrides (window resizes keep working; the
  theme's own files are never touched). While editing, the real UI is
  input-fenced so nothing misfires.
- **Tabbed settings**: the settings screen is split into category tabs
  (General · Theme · Assets · Audio & Chat · Account · Hotkeys) instead of
  one long scroll — click a tab, scroll within it, each tab remembers its
  own scroll position. Async work (theme scans, folder picks, dropped
  files, import/export status) runs regardless of the active tab.
- **Viewport camera zoom (hyperfocus)**: Ctrl+wheel over the stage zooms
  toward the cursor (up to 6×) — sprites, preanims, and effects magnify
  together; Ctrl+drag pans while zoomed; the 1× chip (or zooming out)
  resets. Sprite dragging pauses while zoomed.
- **IC/OOC logs auto-scroll**: stuck to the newest line until YOU scroll
  up (then they hold position); scrolling back to the bottom — or the
  jump-to-newest hotkey — re-sticks. Replaces the old near-bottom
  heuristic that broke whenever a wrapped message added several rows.
- **Multi-server tabs (max 3)**: Join while connected opens a NEW tab —
  the old session parks and keeps running (its packets drain on a
  per-frame budget into its own logs; unread counts and callword
  flashes still fire). A floating chip strip (top-center, every screen)
  switches tabs, shows unread, closes background tabs (✕), and clicking
  the active chip drops you to the lobby with the session still live. A
  **"+" chip** at the end of the strip (accent-bordered, with a hover hint)
  is the discoverable way to open another server — it parks the current
  session and opens the lobby to connect a new tab; it hides at the cap.
  Rooms exist only for the active tab (nothing animates off-screen);
  activation rebuilds the courtroom from the session state. Caches need
  nothing: asset keys are full URLs, per-server separation is
  structural. Rehearsal never backgrounds (it owns the offline gate).
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
  outside the folder, so a plain grab would be silent). Bounded (file /
  byte / depth caps), off-thread, cancelable, and path-traversal-guarded.
  Point "Read assets from local folders" at the downloads folder to use
  the grabs offline / in rehearsal.
- **Discord Rich Presence**: optional, per-field privacy checkboxes,
  zero build/run dependency — full guide in [DISCORD.md](DISCORD.md).
