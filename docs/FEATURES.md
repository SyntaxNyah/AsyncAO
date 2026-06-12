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
  AO palette).
- **Callwords**: comma-separated highlight words; IC/OOC match = taskbar
  flash + `word_call` sound.
- **Hotkeys** (Ctrl+key, configurable in Settings): shouts 1..4, pos
  cycle, music stop (`~stop.mp3` fake-track request, courtroom.cpp
  music_stop), log jump, screenshot (`screenshots/` beside the exe).
- **Hideable chrome**: shout row, layout knobs, emote grid, right column,
  OOC row, HP bars, clocks, badge, judge row — persisted per user.
- **Stop music** button on the Music tab.
- Position cycler, evidence ● armed indicator, modcall dialog, casing
  role checkboxes — all in the courtroom chrome or Settings.
