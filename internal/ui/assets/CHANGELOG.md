# AsyncAO — Changelog

What changed, newest first. The "What's New" screen renders this embedded file,
so every build ships its own history offline. The version you're running is
tagged "installed" below.

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
