# AsyncAO — Changelog

What changed, newest first. The "What's New" screen renders this embedded file,
so every build ships its own history offline. The version you're running is
tagged "installed" below.

## v1.55.0-test9 — 2026-07-05 (test build)

Frame limiting off by default.

- **The flicker tracks sparse frames — so frames aren't sparse anymore.**
  Live testing pinned it down: with a low idle rate the single-frame
  window flicker appears; with the framerate uncapped it doesn't. On
  some driver setups, presenting frames infrequently is itself what
  glitches. So this build ships with ALL frame limiting bypassed by
  default — no static skip, no idle/background/active rates — the
  client just renders continuously at your monitor's rhythm, like any
  game. GPU use goes up compared to the limited builds; that trade is
  deliberate while the flicker is the enemy.
- **The limiters became a toggle.** Settings → Power user → Frame rate
  & GPU → "Frame limiting OFF" — untick it to re-enable the whole
  pacing stack and compare. Applies live, no restart.
- The longer-term plan stays as discussed: rebuild the renderer around
  redrawing only what changes, at a steady frame rhythm — cheap frames
  rather than absent frames.

## v1.55.0-test6 — 2026-07-04 (test build)

A reset to the good state, plus one targeted fix.

- **Rolled back to test2.** The test3–test5 rounds (the literal-zero
  heartbeat removal, the hover census, the 0 fps knobs, the
  park-deadline pacing) stacked too much change at once and made
  things worse — test4 could freeze the client outright. This build is
  exactly test2 plus the fixes below; those ideas will return one at a
  time, each proven on its own.
- **Random max-fps bursts with animated sprites — fixed.** With a big
  animated sprite on stage, the client periodically jumped to max fps
  for about a second, exactly as if you'd touched something. Cause:
  window and driver housekeeping events (repaint requests, render
  resets after heavy texture traffic) were treated as user input and
  armed the one-second full-rate burst. They now repaint one frame and
  nothing more; only real input (mouse, keyboard, wheel, typing,
  drag-and-drop) holds full rate.
- **New churn readout in the F8 debug panel.** The diag line gains
  "sceneReloads": how many times an on-stage sprite/background had to
  be re-loaded after being evicted. If this climbs while a big animated
  character just idles, the stage's working set doesn't fit the texture
  budget and is cycling — the visible symptom is the sprite blinking
  out for a moment. That readout tells us whether the remaining "bad
  redraws" are this, and guides the next fix.

## v1.55.0-test2 — 2026-07-04 (test build)

Fast follow-ups from the first test round — thanks to Nightingale for
the frame-pacing reports.

- **Text no longer crawls at the idle rate over animated characters.**
  With a character whose sprite animates (lip flaps and the like), the
  whole message — the typewriter text AND its blips — ran at the idle
  frame rate; static characters were fine. The scheduler was letting the
  sprite's animation clock own the frame budget. Every moving part now
  schedules independently and the earliest deadline wins: the text crawl
  keeps its own pace, a slow sprite loop just flips whenever its time
  comes, and a fast lip flap can still pull extra frames when IT needs
  them. This also un-chunks the blips, which fire on the same clock.
- **The frame-rate sliders take exact numbers and "unlimited".** Each of
  the three rates (Active / Idle / Background) now has a type-a-number
  box next to the slider and an ∞ toggle: ∞ on Active removes the cap
  entirely (vsync paces the frames), ∞ on Idle or Background means
  "never slow down in that state".
- **Waving the mouse no longer holds a full second of max fps.** Bare
  pointer motion now renders live only while the pointer actually moves
  (a fifth of a second of grace); clicks, keys and scrolling keep the
  full one-second burst. Only with the event-driven renderer on.

## v1.55.0-test1 — 2026-07-04 (test build)

A renderer round, on the experimental channel first because it reaches deep
into how every frame is produced. Thanks to Nightingale for the redraw
reports.

- **The emote grid stops flickering.** Every so often the whole button row
  blanked and streamed back in cell by cell — same for player-list and
  char-select icons. Cause: buttons and icons shared one texture budget with
  the big sprites, and any heavy sprite burst swept them all out at once.
  They now live in their own protected slice of the same budget; sprite
  streaming can't touch them anymore (and a 4000-icon character scroll can
  no longer wipe the sprites on stage, either).
- **Event-driven renderer (EXPERIMENTAL, on by default in this build).**
  Like a proper desktop app, AsyncAO now stops redrawing entirely when
  nothing changes: a static screen renders zero frames between real
  signals. Moving the mouse or typing wakes the renderer instantly (a
  real OS event wait, not a timed nap), an incoming message or finished
  download rings its own doorbell, and a blinking caret or ticking clock
  redraws exactly one frame right when it's due — instead of the old
  30 fps idle loop doing all of that by brute force. Sounds, the
  connection and message pacing are fully decoupled: they never wait for
  the renderer, so a message that arrives mid-idle starts playing
  immediately. Settings → Power user → "Event-driven renderer" is the
  kill switch back to the classic pacing loop — if anything looks frozen
  or choppy, flip it off and report what it was.

## v1.54.5 — 2026-07-04

An input round — thanks to Nightingale for the layering report, the
player-menu design and both preview catches, and to Tifera for the
text-selection report.

- **Overlays own the mouse now.** Scrolling the patch-notes pop-up also
  scrolled the server list behind it — and more generally, panels layered
  over other panels let the wheel, hover glows and drags leak through.
  One frame's scroll now goes to exactly one surface, the What's New
  pop-up blocks everything under its dimmed backdrop on every screen,
  and the floating hotkey sheet catches the mouse wherever it's parked.
- **Player rows grew a "…" menu.** The row of Pair / UID / IPID / Friend /
  Ignore buttons crowded the names out — zoomed, it sat right on top of
  the IPID line. Rows are back to clean two-line height; every action for
  a single player now lives in one menu, opened from the row's "…" button
  or by right-clicking the row. New there: **Message (PM)** — DM anyone
  straight from the list, no friending needed. The UI… popup's ticks now
  hide individual menu entries, and future per-player tools (mod/CM
  commands) will slot into the same menu.
- **Text boxes behave like real text boxes.** Drag to select part of what
  you typed (shift-click and shift-arrows extend, double-click grabs a
  word, triple-click everything), copy/cut/paste exactly the selection —
  and Ctrl+Z / Ctrl+Y are a real undo/redo in every field. Undo also
  recovers lines the client takes from you: the sent line the server's
  echo clears, a draft a palette command template overwrote, text a
  macro key ate.
- **Select exactly the words you mean.** Dragging across the chatbox
  used to light the entire message; now it selects character by
  character, double-click picks the word under the cursor, triple-click
  the whole message — and copying takes just the highlighted part. The
  themed chatbox gains selection too (it had none), and in the IC/OOC
  logs double-click now selects a word, triple-click the line.
- **A test-build update channel.** Settings → Power user can switch the
  updater from stable releases to experimental builds published off the
  test branch — clearly labelled "Test build" everywhere, riskier by
  nature, and the way back is one toggle plus the next stable release.
  Flipping the switch re-checks immediately.
- **Right-click previews work with hover previews off.** Turning off the
  on-hover sprite pop-ups silently killed the right-click preview too;
  the toggle now disables only the hover part, and an explicit
  right-click always previews.
- **The sprite preview opens big enough to see.** The pop-up defaulted
  to a tiny 192 px and had to be corner-dragged bigger every session; it
  now opens at 384 px and Settings → General has a "Preview box height"
  slider (96–720 px) for the default — the corner grip still fine-tunes
  per session.

## v1.54.0 — 2026-07-04

A style round — thanks to Groceries for catching the hue-paint bug and
Tifera for the style wishlist.

- **Hue paint actually keeps the sprite bright now.** Turning it on used
  to look like the plain dark recolour switching itself on: the paint was
  built by multiplying the colour over a grayscale copy, and a multiply
  can only remove light — every strong hue crushed the highlights. The
  paint is now a true colorize: shadows stay dark, highlights stay bright,
  the midtones carry your hue. Same wire as before, so every build sees
  your paint (older ones just render it the old, darker way). Also fixed:
  with Invert or a Restyle active, hue paint silently degraded to the bare
  dark tint — the modes now cleanly hand off to each other instead.
- **Two-tone paint.** Hue paint can split the sprite at an adjustable
  line: one colour above, another below — head red, rest blue. The split
  is feathered so it doesn't cut a hard edge through the body, and it
  travels like the rest of your style (older AsyncAO builds show the
  single upper colour).
- **The glitch got options.** Five looks, cycled like the Restyle button:
  Classic, Heavy (wider, harder, more often), Torn (VHS band tearing),
  Static (jitter plus signal-loss flicker), Echo (far trailing ghosts).
  And the fringe is no longer locked to red and blue — pick a preset
  pair or type a hex colour per ghost. Transmitted; older builds show
  the classic red/blue.
- **The Sprite Style box previews you live.** The top of the box now
  shows your own character — current emote, your position's real
  background — with the style applied through the exact same code the
  stage uses, so paint, restyles, glitch and motion all show before you
  send anything. Hide it with one click if you want the compact box.
- **A colour wheel for your area colour.** The area-list highlight colour
  in Settings gains a Wheel button next to the hex field — drag the disc
  and brightness bar instead of typing codes, or snap back to the stock
  green.
- **Video exports can't die with a phantom "disk full" anymore.** The
  export mux padded its audio with an unbounded silence stream and let
  ffmpeg trim it, which could overflow an internal queue on a busy
  machine and abort the export claiming the disk was full. The padding
  is now measured to the video's exact length up front.

## v1.53.5 — 2026-07-03

A same-day patch round — thanks to Tifera for the report list, DagDag and
Dag4 for catching the silent rejoin, and Groceries for the recolour idea.

- **The Music tab's volume sliders actually change the volume.** They
  read and wrote the global levels, which a connected server's own audio
  profile overrides — so once you'd touched volume anywhere else, these
  four sliders moved numbers nothing was listening to. They now drive the
  same per-server levels as every other volume control.
- **The Players tab follows people between rooms.** A player moving areas
  arrives as an update that changes nothing but their area — and the list's
  change detector didn't compare areas, so the room grouping froze until
  someone joined or left. Moves re-sort the list the moment they happen.
- **The wardrobe's Iniswaps section stops listing backgrounds.** Some
  servers publish one combined list of everything streamable; on one
  playtest server half its iniswap entries were background folders, which
  wear like a broken character. Known backgrounds are filtered out now
  (a character that genuinely shares a background's name can still be
  worn by typing it).
- **Wrapped messages read as one message.** Continuation lines now hang
  slightly right of the first line instead of looking like brand-new
  paragraphs — and hovering a link highlights the whole message block,
  not just the one line under the cursor (a wrapped link used to light
  up a single row and look like a separate message).
- **The UI scale slider sticks.** Dragging it and letting go snapped
  straight back to the old value: the release frame overwrote the
  pending scale with the un-applied one, so the commit was a no-op. The
  drag now commits exactly what you previewed, on release.
- **Ambience can't silence the music anymore.** Servers stream area
  ambience as a music packet on its own channel — including one on every
  join — and the client played it as the room's song, stopping or
  replacing the real track. Ambience channels are recognised now, so
  rejoining a server that replays its current song picks the music
  straight back up.
- **New: Hue paint.** A recolour that only changes the hue: the plain
  tint multiplies colours (a colourful sprite just gets darker), while
  Hue paint sets every pixel to one hue and keeps the sprite's own light
  and shadow. Tick "Hue paint" in the Sprite Style box and drag the new
  Hue slider — the Rainbow toggle cycles the paint too, and other
  AsyncAO players see it exactly as you do (older builds included).

## v1.53.0 — 2026-07-03

A playtest round — thanks to Nightingale and Tifera for the reports and
ideas.

- **Pair placement no longer haunts you across restarts.** Offsets and flip
  were quietly saved and came back on the next launch. Pairing is
  session-only now: each tab keeps its own placement while you play, and a
  fresh session always starts centered.
- **The pair menu's offset boxes update live.** Nudging with the −/+
  buttons or the mousewheel changed the value, but the number in the box
  froze until you clicked somewhere else. It follows every edit now —
  dragging the sprite on the little stage updates the boxes too.
- **The pair menu shows your actual sprite again.** The preview always
  looked for a sprite named "normal", which plenty of packs simply don't
  have — so the stage sat empty. It now previews the emote you have
  selected, and your partner appears as they last stood on stage.
- **Your OOC name stays in its tab.** A name typed in one server's OOC box
  showed up in every other tab — and overwrote your saved default on top.
  It's per-tab now; the permanent default lives in Settings → Identity and
  seeds new tabs.
- **The volume sliders stay shown.** The log panel's Vol strip and the
  Music tab's slider view forgot they were on the moment you connected
  anywhere. If you show them, they stay shown — across servers and
  restarts — until you hide them yourself.
- **The Players list keeps names readable.** With everything enabled a row
  carried up to seven buttons that squeezed the name and UID out of view
  unless the panel was very wide. The buttons now drop to their own line
  whenever they'd crowd the name, and each one (Pair, UID, IPID, Ignore,
  Profile) can be hidden individually in the UI… popup — like Follow
  always could.
- **Settings got reorganised.** Streamer mode is the first thing in
  General → Display & behaviour. The message-send extras (auto-random
  emote, random / rainbow message colours) moved to Chat → Text & typing.
  And every stage look — CRT / retro TV, vignette, scanlines, film grain,
  the stage frame, spotlight, idle breathing, reflection, weather and
  friends — now lives on the Theme tab under "Stage & viewport effects".
- **The layout editor aligns like a real editor.** While dragging, a box's
  edges and centre now snap to every other box and to the window's edges
  and centre, with green guide lines showing what you lined up with —
  flush placement just happens instead of fighting the grid. The snap grid
  itself is adjustable too (a Grid chip cycles 4 / 8 / 16 / 32 px), and
  Shift still bypasses everything for pixel-precise moves.
- **Pin layout boxes to the window.** Hover a box in Edit Layout and press
  A to pin it to a corner or the centre. A pinned box keeps its size and
  stays glued to that spot when the window resizes, instead of drifting
  and stretching with everything else. Press A again to cycle corners or
  unpin; pinned boxes show a small green dot.
- **The log's search bar and the log no longer overlap.** With a large
  custom UI font, the search box's text spilled out of its fixed-height
  row onto the first log line. The row now grows with your font — and on
  a very narrow log panel the search field takes the full row instead of
  vanishing behind the export buttons.

## v1.52.0 — 2026-07-03

A feature round built from playtest feedback — thanks to Tifera for
calling all of this out.

- **Chat in ANY colour.** The IC colour dropdown gains "Custom…": a colour
  wheel with a hex code box. Other AsyncAO players see your exact colour;
  everyone else automatically sees the closest standard AO colour, so
  nothing breaks for them. Click the colour swatch to adjust it any time.
- **Colour individual parts of the layout.** Settings → AsyncAO appearance
  → "Layout part colours": the IC log column, the OOC box, the emote grid
  and the chatbox each take their own background colour, picked on the
  same colour wheel as the custom UI scheme.
- **The control-button block is resizable now.** Drag its side edges in
  Edit Layout and the buttons re-wrap to fit — stack them into a side
  column or a compact corner cluster. It was move-only before.
- **Ctrl+Z / Ctrl+Y work in the layout editor.** The shortcut system was
  swallowing the keys before the editor ever saw them — undo did nothing
  and Ctrl+Y even fired its normal shortcut mid-edit.
- **The layout editor's top strip no longer piles up.** Box name tags, the
  server-tab strip and panels stopped drawing over the editor's own banner
  and buttons.
- **Ctrl+Z brings back chat text the client removed.** If your typed line
  disappears after Enter — the send clearing it, a /command, a palette
  insert — press Ctrl+Z in that box to restore it; press again to swap
  back to what you were typing.

## v1.51.2 — 2026-07-03

- **Characters with broken pre-animation entries no longer freeze before
  speaking.** Some packs list a pre-animation on every emote that doesn't
  actually exist; each of their messages held a blank stage for a fixed
  2.5 seconds — even fully cached, however fast the server. The client now
  notices the file can never arrive and starts the message immediately,
  matching AO2's behaviour.

## v1.51.1 — 2026-07-03

- **Losing a send race no longer eats your message.** When two people send
  at nearly the same moment, many servers silently drop the slower one —
  and the client had already wiped your input box, so the whole line was
  gone. The box now clears only once the server actually accepts your
  message; if it was swallowed, your text is still sitting there — press
  Enter again. Evidence you presented stays armed for the retry too.
- **Your custom font can now cover the whole client.** Settings → Fonts
  has a new toggle: "Use the font everywhere" applies your custom font (or
  the dyslexia-friendly font) to every menu, button, list and tab — not
  just the chat and log. Off by default.
- Downloads now identify themselves as `AsyncAO/<version>` to asset
  servers, so bot filters stop mistaking the client for a scraper and
  server owners can find it in their logs.

## v1.51.0 — 2026-07-02

Server compatibility fixes, tested live against the reported server.

- Sprites stored in `(a)`/`(b)` folders or as bare nested files now load —
  characters that never appeared before show up.
- Emote buttons no longer stop loading partway through a session (a missing
  optional file was making the client forget the server's formats).
- Live music and area list updates from the server now apply instantly.

## v1.50.10 — 2026-07-02

Investigated a server whose players reported files going missing on AsyncAO.
Its asset mirror intermittently drops requests under load — and AsyncAO was
turning each hiccup into lasting damage. Not any more:

- **One network hiccup no longer blanks a file for 30 seconds.** A failed
  download was being remembered in the same penalty box as corrupt images —
  so a perfectly good sprite whose fetch flaked once stayed missing until
  the timer ran out. Network failures now heal the moment the server does.
- **Downloads retry once.** A momentary server error (the classic Cloudflare
  522) gets a single, quick second attempt before giving up — on flaky
  mirrors that recovers most fetches invisibly. Real 404s are never retried.
- **Server-error storms now trip the download backoff properly** (they never
  counted toward it before), so a genuinely struggling server gets breathing
  room instead of a request flood — and when it recovers, everything streams
  in again immediately instead of in ragged 30-second waves.

Also verified on that server while investigating: its layout (lowercase
paths, opus sounds, png misc art) matches everything v1.50.9 fixed — the
custom chatbox its characters declare streams correctly on this build.

## v1.50.9 — 2026-07-02

Live-fire round two: tested against the playtest server's real asset mirror,
character by character, until the boxes actually appeared.

### Custom chatboxes — streaming for real now
v1.50.8 fixed the wrong-filename bug; probing the live mirror exposed three
more layers stacked under it, all real, all fixed:

- **Both casings.** Some mirrors lowercase every path (`chat=YTTD` serves
  `misc/yttd/…`), others keep the authored case (`misc/HallA/…`). Both
  spellings are probed now, lowercase first, in AO2's chat→chatbox order.
- **Both formats.** One and the same server ships `chat.png` for one pack
  and `chatbox.webp` for another — misc art now probes PNG then WebP by
  default, and the server's `extensions.json` no longer wrongly forces its
  *emote* format onto chatbox art (it never declared misc art at all).
- **Power-user control.** The misc (chatbox skin) probe list is hand-tunable
  in Settings → Power user → Image formats — even while format auto-detect
  is on, since auto-detect can never learn misc art.

Verified live: two characters with opposite conventions both stream their
own boxes now. Nothing is bundled or hardcoded — if the mirror ships the
art, you see it; if it doesn't (dorothy's box isn't up there in any
spelling), you get the normal box. Blip sets gained the same
authored-casing fallback while we were in there.

### You are here
- **The area you're in is highlighted** in the player list and the mod
  dashboard — the whole row washes green with a matching edge bar (it was
  just a thin accent line before). Recolour it in Settings → Chat →
  Area list; blank = the stock green.

## v1.50.8 — 2026-07-02

The deep-dive patch. Three standing reports — custom chatboxes not showing,
the window repainting out of nowhere, idle animations still stuttering —
each traced to its root cause this time, not papered over.

### Custom chatboxes, for real this time
- **We fetched the wrong file.** AO2 loads a character's box art as
  `misc/<chat>/chat.png` first, `chatbox.png` second; AsyncAO only ever asked
  for `chatbox`, so packs shipping the modern name never showed. Both
  spellings are probed now, in AO2's order.
- **Folder names survive as authored.** `chat=` values were being lowercased
  and their slashes escaped away — `chat=HallA` asked for `misc/halla/…`,
  and nested packs like `VA-11/Jill` produced a dead URL. The value now
  reaches the server exactly as the pack wrote it.
- Heads-up: the art also has to exist on the asset host. A char.ini can point
  at a `misc/` folder the mirror never shipped — no client can conjure that;
  the theme skin / flat panel covers it, as always.

### The random repaint, found
- While frames were being skipped on a quiet stage, a busy download burst
  could **evict the picture that was on screen** from the texture cache —
  the next heartbeat frame then blinked the stage back in. The live stage
  (background, desk, both characters, the chat skin) is now pinned
  most-recently-used every tick, drawn or skipped, so a burst spends other
  textures first and the screen never pays for it.

### Idle animations — two real bugs this time
- **Unfocused is not invisible.** Clicking into another window dropped the
  client to the flat unfocused rate even while a sprite was mid-loop — the
  stutter you saw side-by-side with chat. The pacer now follows the
  animation's own frame schedule while unfocused too: one render per frame
  flip, right on the flip — typically fewer frames than the old flat rate,
  just at the correct moments.
- **Slow motion at low caps.** The anti-stall guard treated any gap over
  100 ms as a freeze and clamped it away — including our own deliberate
  pacing sleeps, so low FPS settings played every animation slower than
  real time. Scheduled naps are exempt now; genuine stalls still clamp.

Performance is untouched where it counts: the fixes are cache touches and
pacing arithmetic — the render loop's zero-allocation gates all still pass.

## v1.50.7 — 2026-07-02

Live-server reports, same evening — characters' own sounds and art now come
through like they do on webAO.

### Every character sounds and looks like themselves
- **Custom blips.** A speaker's own text noise (their char.ini blip set) now
  plays even when their client doesn't transmit it — AsyncAO reads the
  character's char.ini from the asset host, once, and remembers it. A blip
  sent on the wire still wins.
- **Custom chatboxes.** Characters with their own chatbox art (char.ini
  `chat=`) now show it while they speak — character art over theme skin over
  the flat panel, exactly AO2's priority. Default ON (it's canonical AO);
  Settings → Chat turns it off, which also stops those fetches. Works in
  replays and the Scene Maker preview too.
- The sprite preview now spawns **centred on the right** instead of
  bottom-right, so it stops layering over the IC bar.

## v1.50.6 — 2026-07-02

- **Non-Latin caret, actually fixed.** v1.50.5 corrected the caret's font but
  read the wrong raster shape for fallback text, so the cursor drew pinned at
  the field's left edge. It now reads the exact per-letter positions of both
  raster shapes — the cursor sits where you type, in every script.
- **Hide anything, round two:** the Hold It / Objection / Take That buttons
  are individually hideable (the group toggle still hides the row), and the
  IC bar's **emoji button** can be hidden too — both in the UI… popup and the
  editor toolbox.

## v1.50.5 — 2026-07-02

Same-day patch from the v1.50.0 playtest — **thanks Nightingale** for the
rapid-fire reports.

### Typing in any language
- **Fixed the Cyrillic caret** — the cursor sat several letters away from
  where it really was in non-Latin text. The field now measures the caret,
  the scroll, and click positioning through the exact glyphs it draws.
- **Fixed the size jump** — "as soon as I typed ТЕКСТ it all went up a size."
  Non-Latin text rendered at the log-zoom size inside input boxes; it now
  always matches the field's normal text size, whatever your log zoom is.

### The "sent to the movie room" bug
- Clipped lists (Areas, Music, rosters…) could **hit-test past their panel
  edge**: an invisible, half-scrolled row under the IC bar took the hover and
  the click — so pressing FX joined an area. Input now respects clipping
  everywhere: if you can't see it, you can't click it.

### Layout editor, round two
- **The IC input bar panel is gone.** Every element — colour, showname,
  Immediate, SFX, emoji, FX, the text input — is its own independent movable
  **and resizable** piece. The showname box and colour picker actually resize
  now (they showed the handles but ignored them).
- **The /pos selector is movable + resizable** (it was the one stuck piece).
- **Hidden buttons no longer ghost in the editor** — hide something and its
  handles disappear with it (the toolbox chip remains the way to bring it
  back).
- **The hide list is complete**: Pair, Pos, Group Chat, Voice, Disconnect
  (Esc still leaves the server), and the emote grid's Rand char + ★ Favs
  filter — which are now movable pieces too, not welded to the grid corner.

## v1.50.0 — 2026-07-02

A huge batch: AO2 demo backwards compatibility, a full studio upgrade, group
chats that actually look like chats, a command palette, smarter settings
search — plus a same-day stream of playtest fixes. **Thanks to Nightingale
for the live feedback that shaped half of this.**

### Backwards compatibility: AO2 .demo files
- **AsyncAO reads AO2's demo recordings.** Drop a `.demo` in `recordings\`
  (or drag it onto the window) and it appears in Settings → Studio: **Play**
  it in the replay player, **✎ Edit** it in the Scene Maker, or export it
  straight to GIF/WebP/Video/Comic. Broken pre-2.9.1 demos (the wait-desync
  bug) are repaired on load the same way AO2 repairs them.
- **And writes them.** The Scene Maker's new **⇄ .demo** button exports any
  scene as a demo vanilla AO2 can watch — full server-shape messages, a
  self-consistent character list, real timing.

### Studio
- **Chapters:** every replay gets a ☰ jump list of its beats — scene changes,
  music changes, shouts. Jumping seeds the landing point (right background,
  track, and speaker) without replaying anything in between.
- **Timeline scrubbing:** drag the new lane under the scene-maker timeline and
  the preview replays each line as you sweep.
- **Subtitles:** video exports can write `.srt` + `.vtt` beside the file,
  cue-timed to the exported frames (Settings → Studio → Export).
- **Watermark:** opt-in corner stamp on GIF/WebP/Video exports — your text, or
  the recording's server + date.
- **Copy to clipboard:** opt-in — a finished export lands on the clipboard as
  a real file; paste it straight into Discord.
- **Drag-and-drop import:** drop a `.aorec` or `.demo` on the window — it's
  imported into `recordings\` and starts playing.

### Group chats, de-boring-ified
- **Char icons everywhere** — thread bubbles, member rows, DM headers — with
  per-person name colours and HH:MM timestamps.
- **Real chat bubbles** (yours right in accent, theirs left with their icon),
  wrapped text, newest at the bottom.
- **Group identity:** a per-group colour chip, member count, an ★ owner mark —
  and **unread badges** in the chat list ("# case prep (3)").

### Command palette — Ctrl+Space
- One fuzzy search over **every** client action plus the connected server's
  slash commands (software-aware). Enter runs an action; picking a command
  inserts its form into the OOC box for you to fill in. Rebindable.

### Settings search that gathers
- Search "sprite" and the page becomes a list of **every matching setting
  across every tab** — click one (or Enter) to jump straight to that row,
  flashed so your eye lands on it. No matches? It suggests the tab that
  covers the concept.

### Performance (the "it should be lightweight on real hardware" thread)
- **Static skip:** a genuinely static courtroom now skips rendering entirely —
  idle GPU cost drops to ~zero (a heartbeat frame twice a second keeps
  everything honest).
- **Content-cadence redraws:** an animated sprite/background wakes the
  renderer exactly when its next frame is due — smooth at ANY idle-FPS
  setting, never faster than your cap. Fixes "idle animations went choppy".
- **Talk rate:** a message over all-static art paces to the text crawl instead
  of full rate — and **blips keep their cadence at any frame rate**, focused
  or tabbed out (they used to thin out at low FPS).
- Fixed the biggest "redraws for no reason": a nonzero crossfade *setting*
  held full rate forever, even with no fade running.

### Feel & fixes (playtest stream)
- **Smooth log scrolling** — the IC log glides instead of teleporting, wheel
  and new-message jumps both.
- **Every slider takes the mouse wheel** (1% steps on percent sliders).
- **The Vol strip is readable now** — "Master 70%" labels with live percents
  over full-width sliders (labels still click-to-mute), instead of the old
  bare-bones thumb row.
- **Sprite preview overhaul:** consistent size for every character (192 px
  tall, resizable via a corner grip), a "source × scale" caption line, and it
  can finally be dragged **out of the viewport**.
- **Stage frames:** eight decorative viewport borders (Brass, Neon, Film
  strip, Wood…) in Settings — pure looks, zero cost when Off.
- The layout editor's **4:3 button acts the moment you click it** (and grid
  snap no longer breaks the ratio mid-drag).
- The **emoji picker no longer hides** the Extras box and torn-off tabs
  ("pressing emoji made all the areas disappear").
- The **current area is marked** in the grouped player/mod rosters — accent
  bar + "(current)" instead of a mystery row pinned on top.
- **React button removed** (unused). Incoming reactions from others still
  show, and Hide-reactions still hides them.
- Nyathena ban weirdness diagnosed: it's the server's flag parsing (flags
  must come before the reason; `-r` isn't a flag there). The Mod/CM
  dashboard's generated commands were already in the correct shape.

## v1.40.1 — 2026-07-02

### Text cleanup
- **The Power-user tab reads like a settings page again.** Every explanation
  there was rewritten to be short and factual — same options, same defaults,
  just far less to read.

## v1.40.0 — 2026-07-01

Renderer polish, a huge power-user knob suite, and a real GPU fix — a big batch
of playtest feedback. **Thanks to Nightingale for the bulk of these ideas** —
the renderer work and the optimisation / UX suggestions.

### The GPU fix (adaptive frame pacing)
- **AsyncAO no longer redraws the world for no reason.** The client used to
  re-render + present the entire interface every monitor refresh — on a
  144/165 Hz panel that's a full-screen GPU composite 165×/second while showing
  a static image, and on some windowed present paths it spun even faster
  (fans + heat for nothing). The loop now paces itself **adaptively**: **full
  rate (default 60 fps) the instant you interact or anything animates** —
  typing, messages, shouts, replays, effects — a **calm idle rate (default 30)
  when the screen is genuinely static**, and a **trickle (default 10) when
  another window has focus** (minimized still draws nothing). Input snaps it
  back to full rate immediately, so responsiveness is untouched — this only
  removes wasted redraws. All three rates are **sliders** (Settings → Power
  user → "Frame rate & GPU").

### More power-user knobs (Settings → Power user, every one explained in-tab)
- **Network tuning:** the **404 memory** (how long a missing asset stays
  "missing" before a re-probe — 30 s–60 min, restart-applied) and **slow-host
  patience** (each host's request deadline = N × its observed response average,
  ×2–×32, live).
- **Decode downscale & texture memory:** scale the automatic sprite downscale
  target (50–200 % of your display height) or **disable it entirely** (exact
  source art); set the **GPU texture budget** (32–128 MiB, restart-applied).
- **Speaker-swap crossfade:** the new sprite fades in over the old one
  (50–1000 ms; 0 = off, the default hard swap). Suppressed by Reduce motion.
- **Thumbnail store budget:** a hard cap on the thumbnail folder (8–512 MiB) —
  past it the oldest thumbnails auto-delete.
- **Cold-load profiler in the debug overlay:** a per-stage line — `fetch ·
  decode · upload` averages — so "what's the bottleneck on a cold sprite?" is
  measured, not guessed (Settings → Power user → Diagnostics, or F8).
- The **⟲ nuke reset** covers all of the new knobs too.

### Setting presets
- **Save your whole setup under a name and switch between them** (Settings →
  Data): a "casing" bundle, a "casual" one, a streaming one… A preset is a full
  settings snapshot (passwords never included); applying one takes effect on the
  next restart, exactly like an import. Up to 16.

### Renderer (power user)
- **Cold-load sprite modes — no more blank flash.** When someone speaks with a
  sprite you haven't downloaded yet, there's a moment while it streams + decodes
  (worse on huge art or high ping) where the sprite used to flash empty. A new
  **Settings → Power user → Renderer** option chooses what shows in that gap:
  **"Show nothing"** (the default — the current behaviour) or **"Keep the previous
  one"**, which holds the last sprite on screen until the new one is ready (like
  webAO) so the stage never blanks between speakers. Purely cosmetic, fully isolated
  on the render path, and **free once a sprite is cached** (the render loop stays at
  0 allocs/op). *(A third "wait until it loads" mode — hold the whole message
  off-stage — is planned; it lives in the message queue, not the renderer.)*
- **Viewport sprite mask (default ON).** Character sprites are now clipped to the
  stage, so a big **pair / reposition offset** can't spill a sprite over the chatbox
  or the log. A **Settings → Power user** toggle turns it off for freeform placement.

### Renderer & core knobs (power user — every one labeled in Settings)
- **Third cold-load mode: "hold the message"** (client-AO-style). The uncached-sprite
  picker gains **Wait**: a message stays off-stage until its speaker's sprite has
  decoded, with a **tunable hold cap** (50 ms – 30 s; a broken sprite can only ever
  *delay* a message) — and if the cap expires, the previous sprite is kept rather
  than flashing blank. Two **strictness ticks** widen the gate to the **pair
  partner's sprite** and the **pre-animation**. Shouts always play instantly, and a
  packed-room backlog never waits (catch-up wins).
- **Hold-previous fine-tuning.** A **max-age slider** caps how long the previous
  sprite may bridge a still-loading one (far left = forever), and a **diagnostic
  amber tint** shows stand-ins while you tune.
- **Core message timings, exposed.** **Shout bubble duration** (~0.72 s default),
  **pre-animation wait cap** (2.5 s default), **IC backlog queue depth** (64
  default) and **catch-up flash linger** (0 default) are all sliders now — every
  far-left position is the canonical AO2-matched default.
- **⟲ Reset ALL power-user options** — one (two-click) button puts the whole tab
  back to its out-of-the-box values. Saved mod-menu chips and per-server learned
  formats are user data and survive it.
- **Connection Origin override.** Beside the asset-fetch Origin there's now an
  Origin override for the **WebSocket handshake itself** — for the rare server that
  only accepts its own web client (e.g. one requiring `webao.<its domain>`).

### Sprite thumbnail cache (power user, OFF by default)
- **Tiny low-quality stand-ins for cold sprites** — the "compressed 1 KB sprite"
  idea from the playtest. Turn it on and every character sprite that finishes
  loading also leaves a **~1 KB heavily-compressed thumbnail** in its own folder
  beside the asset cache (independent lifetime — thumbs are ~100× smaller, so they
  stick around long after the full sprite was evicted). The next time that sprite
  is needed cold, the thumbnail shows **instantly** — the **right character**,
  visibly low quality — while the full-quality one streams in, then swaps. It
  composes with the cold-load modes: the thumbnail covers the case where there's
  **no previous sprite to hold**, and it always wins over hold-previous (the right
  character at low quality beats the previous one at full quality). **Height** and
  **quality** sliders tune the size/crunch trade, and a **Clear stored thumbnails**
  button empties the store. Completely free while off.

### Mod / CM dashboard
- **Savable custom ban durations.** Beside the preset chips you can now **save your
  own** ("45m", "2 days", "1w", "perma") — validated, rendered in each server
  software's own duration format, one click to apply, Edit → × to remove. They ride
  the same live command preview, and the bulk box shows them too. (Reason chips
  could already be saved — the two now match.)

### Fixes
- **Dropdown options over a floating panel take clicks again.** In a custom layout,
  an open dropdown list that flipped up over a torn-off tab had dead rows (the
  panel's click-shield swallowed them — "can't select higher than Gray"). The open
  list now owns the pointer wherever it is, and a click it handles can't also land
  on anything beneath.
- **"Don't ask again" on the disconnect prompt.** The confirm dialog — which the
  Disconnect button **and Esc** both go through — now has a **"Don't ask again"**
  tick that switches you to instant disconnect from then on (the same pattern as the
  Quit prompt).
- **The Music menu's volume view is remembered.** If you switch the Music tab to the
  volume sliders, it stays on that view across restarts instead of reverting to the
  track list.

## v1.33.5 — 2026-07-01

### Diagnostics
- **The Debug panel now has a keybind and a Settings home.** Press **F8** anywhere
  to open or close it (it's the interactive superset of the F3 performance HUD), or
  open it from **Settings → Power user → Diagnostics**. It's still on **Extras →
  Debug** too, and it's listed in the **F1** hotkey sheet.

## v1.33.0 — 2026-07-01

A looks-and-quality-of-life release: a one-click CRT filter, pixel-art sprites,
name-mention alerts and a live cache view — plus a bit of hidden fun on About.

### Looks & effects
- **CRT / retro-TV filter.** One toggle (Settings, with the other post-processing
  overlays) layers **scanlines + an RGB phosphor grille + a vignette** for the whole
  old-TV look. Local eye-candy, off by default. *(True screen-curvature and bloom
  need the whole stage rendered to a texture first — a bigger job saved for later.)*
- **Pixel-art sprite restyle.** A new **Pixel art** entry in the Sprite Style box's
  **Restyle** picker mosaics your character into flat, palette-reduced anime pixel
  art — transmitted to other AsyncAO players like the other restyles (AO2 / webAO
  still see your normal sprite).

### Chat & quality of life
- **A "↓ Latest" button in the IC log.** When you've scrolled up to re-read
  something and nothing new has arrived, a jump-to-newest button now appears so you
  don't have to drag the scrollbar all the way back down. (The "↓ N new" pill still
  covers the case where messages arrived while you were scrolled up.)
- **Get alerted when someone says your name.** A new option treats your **showname
  and character name as callwords** — matched as **whole words** (so "Max" doesn't
  fire on "maximum") and **never on your own messages**. Off by default; turn it on
  at the top of **Settings → Callwords**, where it shares the same sound / in-app
  toast / desktop-notification options as your other callwords.

### Diagnostics
- **A Cache tab in the Debug panel** (Extras → *Debug* → *Cache*): where your assets
  loaded from this session (memory / disk / network), each cache tier's fill vs its
  budget and hit rate, learned per-server formats, and network probe counts.

### Fun
- **Pet Mayo.** Click the mascot on the About page to pet her — she wiggles and
  counts your pets through a long line of milestones, and if you keep going she'll
  start bouncing around the screen.

## v1.32.0 — 2026-07-01

A big looks-and-power-user release: two fresh grab-bags of transmitted effects,
a real diagnostics panel, and a tidier, safer Settings screen.

### Looks & effects
- **10 new animated text effects.** The IC bar's **FX** button is now a picker (13
  effects were too many to cycle): **Bounce, Sway, Shiver, Wobble, Tremble, Float**
  move your letters, and **Pulse, Gradient, Blink, Sparkle** colour them. Other
  AsyncAO players see the animation; AO2 / webAO see plain text. You can still type
  `[bounce]…[/bounce]` inline for a single word.
- **10 new sprite restyles.** A new **Restyle** picker in the Sprite Style box:
  **Redscale, Greenscale, Bluescale, Solarize, Threshold, Duotone, Warm, Cool, Neon**
  and **Infrared** — one-click per-pixel looks, transmitted to other AsyncAO players.
- **Colour your character's outline** instead of just white — three RGB sliders
  appear when Outline is on (transmitted, stacks with everything else).
- **Custom movement paths can have up to 16 points** now (was 6), with an **Undo
  point** button for building a path click by click.

### Moderation
- **The ban button fills the IPID from the player list on WAP-Akashi servers.** The
  witches-akashi-party fork streams the mod-only IPID live in the roster, so a
  logged-in mod's ban box fills in with no `/getarea`. (Stock Akashi still fetches
  it, now via `/getareas`, which also covers a target in another area.)

### Diagnostics
- **A proper Debug panel** (Extras → *Debug*): tabs for **Session** (server
  software, live ping, connection), a **packet inspector** (recent packets + in/out
  counts), **performance** (frame graph, heap vs the memory budget, GC, cache hit
  rate, prefetch probes), and the failure log.

### Settings
- **A dedicated "Power user" tab** on the left gathers the options that can break
  things if set wrong — TLS certificate validation, the Asset Origin header,
  character-folder casing, and image-format tuning — behind a reveal button with red
  warnings, so they stay clear of everyday settings.
- **Optional capitalised asset fetching** (power-user, OFF by default): for the rare
  server whose character folders are capitalised, fetch as *Phoenix wright*, *Phoenix
  Wright*, or **Auto-detect** — which probes one character per server once and learns
  the casing, staying on lowercase unless lowercase actually fails. ⚠ Check your server
  first; a wrong *manual* choice makes every character load nothing.

### Fixes
- **The chatbox grows to fit a long message** instead of clipping the bottom lines —
  most noticeable after resizing the viewport narrower.
- **Long Settings descriptions word-wrap** inside the card now (the Hotkeys, TLS and
  Data notes ran off the edge before).

## v1.31.0 — 2026-06-30

### Newcomer-friendly phone book
- **Privacy and Glossary are front-and-centre** now. They're two prominent buttons
  in the middle of the phone-book header — the two things a newcomer most needs:
  *what a server can see about you*, and *what all the AO jargon means*. They used to
  be hidden inside a generic "Help" button off in the corner.

### Audio
- **Blip-rate slider in the Music menu's volume view**, right under the Blip volume —
  tune the typing-sound cadence without leaving the Music tab. It was only on the
  compact volume strip and in Settings before.

## v1.30.0 — 2026-06-30

A big quality-of-life release — most of it straight from **Nightingale's** ideas
and UI suggestions, with bug-finds from **Dag** and the playtest crew. Thank you.

### Layout & the courtroom
- **Save and switch layout presets.** Settings → Theme → *Layout presets*: save the
  whole default-courtroom arrangement under a name and flip between setups, plus
  one-click *Theater / Wide / Compact* stage presets. Stored as window fractions, so a
  preset looks right at any window size.
- **One toolbox to show or hide every UI piece.** The layout editor (*Edit Layout*)
  now has a single strip listing every panel and control button as a chip — click to
  show/hide, **drag a chip onto the stage to place it**, or **drag a piece onto the
  strip to hide it**. Replaces the old separate "hide UI pieces" checkbox menu.
- **The log panel borders in the accent colour** like the rest of the UI, instead of
  being the lone grey outline.
- **"Show volume sliders" stays on** across restarts now.

### Keyboard & navigation
- **Esc leaves the server** from the courtroom or character select — through the
  confirm, so a stray tap can't drop you. Handy in fullscreen.
- **Back is always top-right.** On character select it used to sit mid-screen while
  *Disconnect* took the top-right spot, so it was easy to leave by mistake. Back is
  top-right like every other screen now, and Disconnect is red and out of the way.

### Chat & reading
- **IC timestamps are off by default** (cleaner log; still toggleable in Settings → Chat).
- **Speaker names — and the timestamp — are bold** for readability (on by default).
- **"X is typing…" between AsyncAO users** (opt-in, OFF by default): a small caption
  above the IC box when other AsyncAO players are composing.
- **Settings search jumps to the section,** not just the tab — search "scene maker"
  and you land right on it.
- The **New group chat** invite list shows people's shownames + OOC names and flags
  who's on AsyncAO, like the player list.

### Looks
- **A lot more in the Sprite Style box.** New **Sepia** and **Posterize** effects, more
  recolour swatches, and transmitted **movement** — make your sprite **Orbit, Bounce, Sway,
  Drift, Shake, Spiral or Pendulum** on the viewport, or **draw your own looping path** for
  it to follow. Other AsyncAO players see it; it stacks with every other effect.
- A green **"unread" dot on *What's New*** after an update, until you open it.
- A **new player flashes** briefly in the player list as they join.

### Privacy & help
- A **Glossary** and a deep **Privacy** explainer (from the server-select screen):
  what each server can see, how AsyncAO handles your HDID, WS vs WSS (and why WSS
  stops a man-in-the-middle), VPN advice, and how the voice chat avoids leaking your IP.

### Voice & performance
- **Test your mic without joining a call:** Settings → Voice → *Test microphone* shows
  a live level meter and can play your mic back (sidetone) so you can check levels.
- A **predictive-prefetch** slider (Settings → Assets) to tune how aggressively the
  next sprites are warmed.

### Fixes
- **"Fix stuck / repeated images"** (Settings → Assets → Cache): if a character's emote
  buttons all show the same image, this clears the learned format and cache together so
  the art re-fetches. (Thanks Dag.)
- Emoji no longer render at the wrong size when the same one shows in two places at once.

## v1.20.0 — 2026-06-30

- **The Ban / Kick menus are a movable box now, not a screen-blocking pop-up.**
  Opening Ban or Kick used to dim the whole courtroom so you couldn't see or use
  chat while it was up. It's a floating box now — drag it by the title bar, resize
  it from the bottom-right corner, and keep chatting and watching the room behind
  it. The frozen target and the live "this is the exact command that will send"
  preview are unchanged, so it's no easier to mis-ban.
- **Emoji render in colour again, even after the latest Windows update.** A 2025
  Windows update changed the system emoji font to a format the renderer couldn't
  draw (COLRv1), so emoji — and `:skull:`-style shortcodes — turned into empty
  boxes. AsyncAO now bundles a colour-emoji font (Twemoji) and uses it whenever
  the system font can't be drawn, so emoji show in colour again. Where the system
  emoji font still works, it's used as before.
- **Hide the courtroom control buttons you don't use.** Open *UI…* → **Control
  buttons** and untick any of Character, Wardrobe, Restyle, Background, Evidence,
  Mods, Settings, Edit Layout, Hotkeys, About or Login to drop it from the button
  row — the row compacts with no gap and your choice is remembered. (New-default
  layout.)
- **Long hover tooltips wrap instead of running off the screen.** A long server
  description (or any long hint) now word-wraps into a tidy box that always stays
  fully on-screen, instead of spilling past the window edge.
- **Voice chat holds up better on a shaky connection.** Voice now skips
  transmitting silence (Opus DTX) and rides an adaptive jitter buffer that deepens
  only when the audio actually stutters and eases back when it's smooth, with a
  small chip showing the latency it's adding — trading a little delay for fewer
  dropouts, and only when the connection needs it.
- **The Discord-free downloads are now also voice-free.** The
  `asyncao-…-nodiscord` builds compile out the optional voice chat as well — its
  panel, buttons, settings and codec — a leaner build for anyone who wants neither
  integration. Music still plays normally (Opus included); this only affects the
  separate Discord-free download, not the standard build.

## v1.19.9 — 2026-06-29

- **Fixed the OOC box overlapping the log tabs.** If you'd moved the OOC box in
  Edit Layout (or changed UI scale afterwards), its saved position could land on
  top of the Log / Music / Areas / … tab row. The OOC box now keeps clear of the
  tab strip so those stay visible and clickable.

## v1.19.8 — 2026-06-29

**Voice chat is confirmed working** on Nyathena — talk and hear in a
voice-enabled area. This patch fixes two things found right after:

- **Esc actually closes menus now.** It turns out Esc was never reaching the
  close handler (an input-routing bug), so it did nothing on any build. Fixed — Esc
  now backs out of popups/panels and the menus as intended (and offers to quit
  from the lobby).
- **Clicking a floating box no longer moves you to another room.** The Voice,
  Group Chat and Call Mod panels weren't blocking clicks from falling through to
  the area list underneath, so pressing their buttons could swap your area.
  They now fence the click like the other panels.
- **Group invites just work now.** The invite picker lists **everyone in the
  room** instead of only "detected" AsyncAO players (which needed them to send a
  special message first). Invite anyone — AsyncAO users get the full group chat,
  others get a one-off PM and never see a menu. A note in the picker says so.
- **Push-to-talk key** (Settings → Voice → **Push-to-talk**): bind a key that
  toggles your mic on/off while in voice.
- **Desk-visibility hotkey works** again: it defaults to Ctrl+V, which was being
  eaten by clipboard paste — now it fires when no text field is focused. *(Bug
  found by cherripop — thanks!)*

## v1.19.7 — 2026-06-29

More playtest fixes + voice/quit quality-of-life.

- **Esc closes any open menu or popup.** One press backs out of the topmost
  thing — a dropdown, a confirm, then the floating panels (Voice, Group Chat,
  Evidence, Call Mod, Mod, CM, Pair) — on the courtroom or over a menu screen.
- **Quit from the lobby with Esc.** In the lobby (handy in fullscreen, where the
  window's X is out of reach), Esc asks **"Quit AsyncAO?"** — with a **"Don't ask
  again"** tick so it just quits next time.
- **Voice settings tab** (Settings → **Voice**): pick your **microphone**
  (system default unless you choose another) and set the **output volume**.
- **Live-mic indicator:** while you're transmitting, the **Voice** button turns
  into a red **● Voice** and the panel shows **● MIC LIVE**, so you always know
  when your mic is hot.
- **Group Chat fix:** "+ New Group" no longer says *"needs you fully connected"*
  when you already are — it now checks the connection, not a player-id that some
  servers (Nyathena) report as 0.

## v1.19.6 — 2026-06-29

A small follow-up from playtesting v1.19.5.

- **Voice button in the bottom button row.** When you move into an **area that
  supports voice** (Nyathena VS_CAPS), a **Voice** button now appears in the
  courtroom controls — not just in Extras — and a one-time note points you to it.
  It hides again in areas without voice.
- **Group Chat: "+ New Group" now opens the invite picker** straight away, with a
  clear message when no other AsyncAO players have been detected yet (they show
  up once they've spoken — look for the AO badge). Creating a group also tells you
  if you're not fully connected yet, instead of doing nothing.

## v1.19.5 — 2026-06-29

Voice chat can actually talk now, and Group Chat is much easier to find — both
from playtest feedback on Skrapegropen.

### Voice chat — live audio
- **You can talk and hear now.** On a **Nyathena** server, open **Voice
  (Nyathena)**, **Join voice**, and hit **Talk** — your mic is Opus-encoded and
  relayed to the room, and you hear everyone else mixed together. Plus an
  **output volume** slider and **Mute others**.
- It's **opt-in** (nothing runs until you join a voice channel) and **fail-safe**:
  no microphone falls back to **listen-only**, no audio device falls back to
  **presence-only** — it never interferes with anything else.
- **Each voice row shows the person's character portrait, `[UID]` + name, and
  their custom profile** (pronouns · tagline), and the icon gets an **accent
  frame while they're talking** so you can see who's speaking at a glance.

### Group Chat — easier to find
- **A main "Group Chat" button** in the courtroom button row (on by default —
  hide it in **Settings → Chat**), so it's not buried in Extras.
- **Start a group from the Friends list** — a **"+ New group chat"** button opens
  the panel ready to invite your friends.
- Fixed the Group Chat chip overlapping the OOC chip in the layout editor.

## v1.19.0 — 2026-06-29

A big one — private messaging between AsyncAO players, the first slice of
Nyathena voice chat, portable settings, a much more powerful log browser, and
groundwork for signed builds. **Thanks to Nightingale** (config simplicity &
portability) and **Gaygay** (the UI-scale report) for the feedback that drove it.

### Message other AsyncAO players — DMs & group chats
- **Group Chat** (Extras → **Group Chat**, or its Edit-Layout slot) — a floating,
  movable/resizable/hideable panel for **private 1:1 DMs and group chats** with
  other AsyncAO players. The client **auto-detects** who else is on AsyncAO and
  badges them (**AO**) in the player list, so starting a chat is one click.
- **Full group chats:** create a group, **invite** other AsyncAO users, and as the
  **owner** you can **kick**; anyone can **leave**. Member profiles show in the
  panel. Invites arrive as an **Accept / Decline** banner **and a desktop toast** —
  never auto-join.
- **Confidential and non-disruptive by design.** It rides the **server's `/pm`**
  (Nyathena / Athena), so it's **isolated to the people in the chat** — it never
  posts in an area, normal players can't read other people's messages, and roles
  (who owns a group, who sent what) are **server-attributed**, so they can't be
  spoofed. Server owners can still see `/pm` traffic, as always.
- Zero cost on the render loop — the whole system is off the hot path.

### Voice chat for Nyathena (first slice)
- On a **Nyathena** server (one that advertises voice), **Extras → "Voice
  (Nyathena)"** opens a panel: **join the voice channel**, see **who else is in
  voice and who's currently talking** (live speaking indicators), and **toggle your
  own speak state**. The option is **hidden on servers that don't support voice**,
  exactly like a server wire.
- Under the hood this ships the **full Nyathena `VS_*` voice protocol** (relayed
  over the existing connection — no peer-to-peer, so IPs never leak) and an **Opus
  codec**. **Live microphone audio** (actually talking and hearing) is the **next
  slice**, validated on a live Nyathena server.

### Your settings are portable now
- **Config travels with the program.** AsyncAO now keeps your settings (and
  notebooks, jukebox) in a **`config/` folder right next to the program** when it
  can — so a copied folder or a **USB stick** carries everything with it. If
  there's no portable config and you've an existing one in the OS config folder
  (AppData on Windows), it **keeps using that** — nobody's settings move without
  asking.
- **New Settings → Data tab** puts it all in one place: the **exact path**, whether
  you're portable or not, **Open config folder**, **Open settings file**,
  **Export / Import**, and a **Make portable** button that copies your settings
  beside the program (your old copy is left untouched; takes effect on restart).
- Documented in `docs/user/config-location.md`.

### Log browser — filters, export, stats, modcall clips
- **Filters:** **regex** and **per-speaker** filtering on top of text search, and
  **click any result to jump to its surrounding context**.
- **Export** the current filtered view to `logs/exports/<timestamp>.txt`.
- **Stats:** per-speaker line and word counts, plus totals, for the current scope.
- **Auto-clip on modcall** (on by default): when a mod is called, the recent IC log
  is saved to `logs/<server>/modcalls/` as a frozen record — evidence for mods/CMs.
  Toggle in Settings → Audio & Chat.

### Asset streaming (power users)
- **Origin / CORS header override** (Settings → Assets) for servers that block
  streaming from their own base URL or only allow specific referrers. **Only touch
  this if you know what you're doing** — it's for power users; leave it blank
  otherwise.

### Quality of life
- **Manual UI scale is actually usable.** It now applies **on release** (not while
  you drag, which fought your cursor) and has **preset chips** for quick sizes —
  fixing the "manual scale is super hard to use" report on high-resolution screens.

### Under the hood
- **Signed builds (groundwork).** The release pipeline can now Authenticode-sign
  the Windows `.exe` and Developer-ID-codesign/notarize the macOS binary — **gated
  on signing secrets**, so unsigned builds still ship until certificates are added
  (`docs/CODE-SIGNING.md`). This is the path to fewer SmartScreen / Gatekeeper
  warnings.

## v1.18.0 — 2026-06-29

A fixes-and-features release from playtest feedback — readable UI on big monitors,
a log search browser, AO theme fonts, a screenshot button, a pop-out Call Mod, an
optional strict-TLS mode, and a Linux Flatpak. **Thanks to Crystalwarrior (Alex
Noir)** for the font report (#6).

### Text was too small — fixed
- **The UI scales with the window now.** On a large or maximized window the
  fixed-size widgets used to look like a tiny island in empty space — so the auto
  UI scale now follows the **window size** (bigger window → bigger UI, up to a
  cap) on top of the display DPI. It's also **floored at 100%**, so an unreliable
  display-DPI reading can no longer auto-*shrink* everything below 100% — the root
  of the "default fonts are really tiny" report. (#6, Crystalwarrior) Manual UI
  scale (Settings → General) still overrides — turn auto-scale off there if you
  prefer a fixed size.
- **AO theme fonts load.** A theme that ships its own font — a `.ttf` / `.otf` in
  the theme folder, named by its `courtroom_fonts.ini` `…_font` family — now
  applies to the IC / OOC text instead of being ignored. (A manual font override
  and the dyslexia font still take priority.) (#6)

### Search your logs
- **A log browser** — the **Logs** button in the lobby (and **Extras → Logs**).
  Pick **any server**, then **any session**, and **filter the lines by text** (a
  name, a word, a phrase) across the whole scope; click a line to copy it. It reads
  the per-server transcripts that detailed logging writes (turn it on in Settings →
  Audio & Chat), off the render thread.

### Quality of life
- **Screenshot button.** Saving a PNG of the current frame to `screenshots/` was
  already on **Ctrl+S** — now there's an **Extras → Screenshot** button too.
- **Call Mod is a floating window.** The modcall box is now movable, resizable and
  **non-blocking** (like Evidence / Pair), so you can keep talking and watching the
  courtroom while it's open.

### Security (power users)
- **Strict TLS option** (Settings → Account → **Security**, **off by default**):
  strictly verify a `wss://` server's certificate. It's off by default because most
  community AO servers use self-signed certs and would otherwise be unreachable;
  turn it on if you want to be sure the encrypted connection is to the real server.

### Linux
- **Flatpak packaging** (AsyncAO is AGPLv3, so it ships one) in **Discord** and
  **Discord-free** variants, like every other platform — built in CI alongside the
  AppImage (`packaging/flatpak/`).

## v1.17.0 — 2026-06-29

A UX-polish batch from playtest feedback — logs, the lobby, menus, and a few
quality-of-life touches.

### Logs
- **Transcripts now save like AO.** Detailed logging (Settings → Audio & Chat)
  writes a file **per server** — `logs/<server>/<date_time>.log`, one per session —
  with a readable line: **`[time] showname (char): message`** (showname first, no
  server column or pipes). Replaces the single `transcript.log` that crammed every
  server together.

### Lobby
- **Hover a server** to see its **connect URL and description** ("MOTD") without
  clicking to expand the row.
- **Keyboard navigation** — **↑ / ↓** move through the joinable servers (skipping
  legacy rows, scrolling to keep the selection in view) and **Enter** joins the
  highlighted one.

### Quality of life
- **Esc reliably leaves a menu.** Settings / About / What's New / the server-owner
  guide already closed on Esc, but a focused search or text box used to swallow the
  key — now Esc first drops the field (or closes an open dropdown), then leaves, so
  it always gets you out. (The reset-confirm cancels on Esc too.)
- **Pair status in the player list** (opt-in — the **"Pairs"** toggle next to
  Follow): shows who each player is currently paired with (⇄), tracked from their
  IC messages. Off by default.
- **Floating panels snap** to the screen edges and centre while you drag them, so
  the Mod/CM, Pair, Hotkeys and evidence windows line up cleanly instead of landing
  a few pixels off.
- **Colour-code your tabs** — **Ctrl+click** a server tab to cycle its chip through
  a colour (a stripe along the top). Handy when you're juggling several servers.
- **Hover tooltips on the terse settings** — the slider / number rows (UI scale,
  text sizes, max tabs, chatbox opacity…) now explain themselves on hover, like the
  checkboxes already do in their labels.

## v1.16.0 — 2026-06-28

A moderation fix plus two quality-of-life touches, on top of v1.15.0.

### Moderation
- **Kicking works on Athena / Nyathena again.** The dashboard was sending
  `/kick -i <ipid>` once it had a target's IPID — and those servers silently
  ignore an IPID kick (the IPID is the *ban* identifier). Kicks now go out as
  `/kick -u <uid>`, the connected-client form, so they actually land — from both
  the single Kick box and the bulk kick. (Bans are unchanged; they correctly
  use the IPID so they survive a disconnect.)
- **Editable, saved ban/kick reason chips.** The quick-reason buttons are no
  longer fixed — hit **+ Save reason** to store the one you typed, or **Edit**
  to remove any (the × on each chip). Your list persists across sessions.
- **Export the audit log.** The session's ban/kick record now has a **Copy**
  button (in the dashboard's Audit view) that puts the whole log on your
  clipboard — paste it into a report or Discord.

### Quality of life
- **Press ↑ in the OOC box to bring back your last OOC message** (↑/↓ to walk
  further), exactly like the IC field — with its own separate history.

## v1.15.0 — 2026-06-28

A feature release: a much bigger moderation dashboard, a real SFX browser, and a
batch of quality-of-life fixes from your suggestions. (Numbered 1.15.0 so it sorts
above 1.1.0 in the in-app updater.)

### Moderation dashboard, rebuilt
- **The roster now shows character icons and is grouped by area** — exactly like the
  player list, organised by your server software's own `/gas` / `/getareas` order, with
  a header and count per area. The same warm icon cache feeds both, so faces appear
  instantly.
- **Bulk ban / kick.** Tick the box on any rows (up to 50 at once) and ban or kick the
  whole batch from one box — one shared duration + reason, with a live count of how many
  are ready. Commands are paced out one at a time so a big sweep never floods the server.
- **Quick-reason templates.** One-click chips (Spam, Harassment, NSFW, Disruptive,
  Trolling, Ban evasion, Disrespect) fill the reason field; still fully editable.
- **A session audit log.** Every ban / kick you send from the dashboard is recorded with
  its time, target and exact command — flip the left column to **Audit** to review what
  went out this session.

### Sound
- **New SFX Browser** (Extras → **SFX Browser**, or the tip on the IC-bar sound picker).
  Browse and **preview** (▶) sounds, **favourite** (★) the ones you use — favourites are
  saved and follow you across characters and servers — and **type any sound name** to use
  it directly, since a streaming client can't list the whole server. Picking one fills the
  IC-bar sound picker as before.

### Quality of life
- **Press ↑ in the IC box to bring back your last message** (and ↑/↓ to walk further back),
  shell-style — edit and resend without retyping.
- **Per-channel mute.** Click a channel's label in the sidebar volume strip (Master /
  Music / SFX / Blips) to mute just that channel; click again to restore. Master mute
  silences everything, including the call-word alert.
- **Clearer connection errors.** A failed connect now tells you *why* in plain language —
  host not found, refused, timed out, "that's an `https://` page, try `ws(s)://`", or
  "that's not a WebSocket server" — instead of a raw Go error.
- **Fixed OOC log word-wrap.** Long OOC lines no longer overflow the panel edge — the wrap
  width now matches the OOC text size. ([#1](https://github.com/SyntaxNyah/AsyncAO/issues/1))

### For theme-makers
- The `asyncao_ic_*` `courtroom_design.ini` keys for placing each IC control separately are
  now documented in [docs/user/themes.md](https://github.com/SyntaxNyah/AsyncAO/blob/main/docs/user/themes.md).

## v1.1.0 — 2026-06-28

A playtest-driven release built straight from your GitHub issues. **Huge thanks to
ZeitHeld and Crystalwarrior** for the detailed reports. (Numbered 1.1.0 rather than
1.0.8 so it sorts cleanly above 1.0.75 for the in-app updater.)

### Talking IC is obvious now
- **The IC text input sits directly under the stage** — the classic Attorney Online
  spot — instead of being buried under the control buttons where it read like the OOC
  bar. ("At first I thought there was no way to talk IC at all.") (#8, ZeitHeld)
- **Build-your-own IC bar.** The colour picker, showname box, sound picker, the
  emoji / Text-FX / React buttons and the text input are now each their own box you
  can drag anywhere in **Edit Layout** (Default + Legacy layouts) — no more six things
  crammed into one row. (#4, Crystalwarrior)
- **Theme-makers can split it too.** A custom theme can place each IC control on its
  own via new optional keys in `courtroom_design.ini` — `asyncao_ic_color`,
  `_immediate`, `_sfx`, `_emoji`, `_fx`, `_react`. Themes that don't define them keep
  the old combined row, unchanged. (#4, Crystalwarrior)

### Evidence without losing the room
- **The evidence browser is a movable, resizable floating window** — drag the title
  bar, resize the corner — and the courtroom stays fully live behind it, so you can
  keep talking and follow the conversation while you browse or arm evidence. (#5,
  Crystalwarrior)

### Audio
- **The sidebar "Vol" sliders work again.** They were driving the *global* volumes
  while the rest of the app uses *per-server* volumes, so once you'd touched volume
  anywhere they did nothing audible. Fixed. (#9, ZeitHeld)
- **A "Rate" slider** for the blip cadence now sits next to the volume sliders.
- **Fresh installs start at 70% volume** instead of full blast.

### Note
- The default courtroom layout changed (IC input under the stage). If you'd customised
  the IC bar / control buttons / emote grid, your positions are kept — **Edit Layout →
  "Reset all"** gives you the new default if you'd rather start fresh.

## v1.0.75 — 2026-06-28

A small patch — three bug fixes. **Thanks to Crystalwarrior** for the tab-overlap report.

### Fixes
- **No more crash on kick/ban.** Getting kicked or banned by a server (or a server
  that disconnects you with a notice) could crash the client outright. It now drops
  cleanly back to the lobby.
- **The disconnect reason and Reconnect come back.** After a drop you again see *why*
  in the lobby ("Kicked: …" / "Banned: …"), the one-click **Reconnect** button returns,
  and **auto-reconnect** arms after an unexpected drop — these had been getting wiped
  the instant you were disconnected.
- **The server-tab switcher no longer covers the Log/Music/Areas tabs.** It used to
  float dead-centre on top of the dock tabs, so reaching for "Log" would instead browse
  you back to the lobby and cut the music. It now defaults clear of them (over the
  stage), and you can **drag it anywhere** in **Edit Layout** (it's a move-only box —
  right-click resets it, "Reset all" re-centres it).

## v1.0.7 — 2026-06-27

A focused fix for server asset formats.

### Asset formats
- **A server's own `extensions.json` is honoured again.** v1.0.6 added per-server
  format profiles — and bundled one for the official **vanilla** server — but they
  were skipped whenever you had asset **auto-detect turned off**, so the client
  kept probing its default WebP formats and vanilla content came up broken. A
  per-server profile is an explicit override, not part of auto-detect, so it now
  applies the instant you connect **regardless of the auto-detect toggle**. Join
  the official vanilla server and its formats (`.png` icons, backgrounds and
  sprites) are seeded immediately; your global default is left untouched.

## v1.0.6 — 2026-06-27

Another playtest-driven round — the layout editor grew up, formats got fixed, and
a pile of small annoyances are gone. **Thanks again to Nightingale** for relentless
testing, and to **Crystalwarrior** for the blip-rate report.

### Build-your-own layout
- **Alt+drag = move** anything in the layout editor — small widgets (a single
  button, the right column) stop resizing when you only wanted to move them.
- The **top strip is now usable**: drag widgets up next to the tabs (the editor
  banner went translucent and only its buttons block a drag).
- **4:3 lock**: a toggle in the editor keeps the stage from stretching off 4:3
  while you resize it.
- **Hide any tab** — Music / Areas / Players / Notes / Friends can be fully
  hidden (not just unpinned) in the UI… popup.
- The IC log now **fills the column** when you move or hide the OOC box (no more
  dead empty space), and torn-off panels **redock on right-click** instead of a
  corner "x" you kept hitting by accident.

### Appearance
- **Custom colour scheme → colour wheel**: pick a swatch, then a hue/saturation
  wheel + brightness slider set it (Settings → Theme → Custom).
- **Bold speaker names** by default in the log and chatbox (toggle in Settings).
- The closed **Pos** selector shows the current position's background thumbnail.

### Settings & account
- The "Audio & Chat" tab is split into **Audio**, **Chat**, and a dedicated
  **Reset** tab.
- The **Login** button shows your **account name** once you've saved one —
  left-click views your profile (/account), right-click to log in / switch.

### Chat & audio
- **Blip rate is configurable** (Settings → Audio → Blips): one blip per N letters
  (default 2, Ace Attorney style) plus a "blip on spaces" toggle.
- **OOC callwords are now opt-in** (off by default) — no more constant pings from
  OOC chatter or /ga rosters; turn it on if you want it.

### Asset formats
- **Per-server format profiles**: probe exactly the formats a server uses, seeded
  instantly on connect — so a server's own `extensions.json` is honoured from the
  first frame instead of the default winning the race. Your global default is left
  alone. The official **vanilla** manifest ships as a ready-made example and
  auto-applies on the official vanilla server (Settings → Assets → Server format
  profile).

## v1.0.5 — 2026-06-27

A big quality-of-life and bug-fix release, driven almost entirely by playtest
feedback. **Huge thanks to Nightingale**, who tore the client apart, stress-tested
every corner and bluntly reported the whole pile of annoyances and fixes below —
most of this release exists because of his testing.

### Build-your-own layout
- The IC bar is no longer one fixed strip. The **Immediate** toggle, every
  **control button** (the shouts, Pair, Character, Wardrobe, Restyle, Background,
  Evidence, Mods, Settings, About, Login, Disconnect and more) and the **chatbox**
  itself can each be dragged out into its own spot in the layout editor — scatter
  them anywhere on screen, or lift the chatbox right off the sprites.
- **Precise stage sizing** (Settings → Scale): the courtroom art is 256×192, so
  the stage now snaps to crisp integer multiples (Fit / 1× / 2× / 3× / 4×, plus a
  slider and an exact-pixel box) instead of landing between them and going blurry.
- Hold **Shift** while dragging or resizing in the layout editor for pixel-precise
  placement (bypasses the grid snap), and thin bars resize back down again instead
  of getting stuck tall.

### Appearance & themes
- **Custom colour scheme**: a new "Custom" entry in the theme picker lets you set
  every UI colour (background, panels, accent, text, danger) by hex with a live
  preview. A readability guard stops you painting the text invisible.
- The whole UI was **rebased more compact** — the default no longer looks like
  it's running at 125% system scaling.
- The client now renders **DPI-aware**: crisp on 125% / 150% displays instead of
  being bitmap-upscaled into a blur, and the UI-scale slider is free to go to 100%.

### Areas list
- Each area is now its own **bordered button card** — locked rooms get a red card,
  the area you're in is highlighted, and the player count sits on an indented
  second line. No more flat grey wall where every room looked the same.

### Emotes
- **Right-click an emote** to pin its full-size preview open: it stays until you
  close it, follows your mouse across other emotes, and keeps wheel-cycle / zoom /
  drag — a permanent alternative to the hover preview.
- The emote-name text that overlaid icon-fallback buttons is **gone by default**
  (clean icons); a Settings toggle brings it back if you relied on it.
- Emote **favourite ★ badges are opt-in** now (off by default) so they don't
  clutter the grid for people who don't use them.

### Courtroom & chat
- The **position dropdown** shows a background thumbnail per position, so you can
  see at a glance which positions a background actually supports.
- The **pairing preview** draws the real background behind the ghosts, so you
  place your sprite against the actual scene instead of a black void.
- **OOC and IC-log text sizes are independent** — Ctrl+scroll (or wheel-button) on
  one no longer resizes both. Added an OOC text-size slider in Settings.

### Fixes & polish
- Opening the **theme / folder pickers** (and the screenshot toast / video export)
  no longer flashes an empty console window — a regression from the console-free
  v1.0.0 build.
- **Esc** now closes the full-screen Settings / About / What's-New / Server-help
  views, back to wherever you were.
- The preanim toggle is labelled **"Immediate"** in full (was the cryptic "Immed").
- **"Restart to apply"** after an update actually relaunches the app now.

### Thanks
- **Nightingale** — extensive testing and brutally honest bug reports across the
  whole v1.0.x line; added to the beta-tester credits. This one's for you.

## v1.0.0 — 2026-06-27

The first stable public release. (The earlier v0.1.x previews were withdrawn —
see the note at the bottom.)

### What's New
- New "What's New" version-history screen — this view, reachable any time from the lobby's top bar. After a self-update it also opens automatically with the latest release's patch notes.

### Multi-server & windowing
- Multi-server tabs, each with its own fully isolated session — no state leaks between servers.
- Floating, movable, resizable panels (Pair / Mod-CM / Hotkeys) and Chrome-style tear-off tabs (drag a tab out into its own window, drop it back to redock).
- Built-in second-courtroom view: overlay, move, resize, zoom and pan a second server while you play the first.
- Live drag-and-resize layout editor for the default and legacy layouts, persisted when you log off.

### Courtroom & chat
- KFO-Server master-list compatibility mode (normal servers stay byte-identical on the wire).
- IC-bar sound-effect picker button.
- "~~" center-text prefix, a revamped message editor, a dedicated bottom OOC bar, and drag-select + copy straight from the chatbox.
- Per-server audio settings with a global fallback.

### Security & moderation
- Hardened device ID (HDID) — servers key bans on it, so it now derives from stable per-machine/account roots (Windows account SID + machine GUID, Linux machine-id, macOS hardware UUID), salted-SHA-256 hashed so no raw hardware value leaves your machine. Renaming your PC no longer changes it; fit new parts or reinstall and you simply appear as a new device, never false-flagged against an old ban.

### Updates & packaging
- Self-update from GitHub Releases: one quiet check on launch, shows the patch notes, then downloads and swaps the binary in place.
- Automated, version-stamped release pipeline; Linux AppImage builds; an optional Discord-free build.

## v0.1.0 – v0.1.1 — withdrawn previews

The first public test builds (headlined by KFO-Server support) were pulled and
replaced by v1.0.0. If you ran one of these, update to v1.0.0.
