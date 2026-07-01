# AsyncAO — Changelog

What changed, newest first. The "What's New" screen renders this embedded file,
so every build ships its own history offline. The version you're running is
tagged "installed" below.

## v1.40.0 — 2026-07-01

Renderer polish and power-user knobs, plus a couple of fixes — a big batch of
playtest feedback. **Thanks to Nightingale for the bulk of these ideas** — the
renderer work and the optimisation / UX suggestions.

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
