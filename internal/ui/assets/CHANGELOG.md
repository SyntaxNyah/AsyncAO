# AsyncAO — Changelog

What changed, newest first. The "What's New" screen renders this embedded file,
so every build ships its own history offline. The version you're running is
tagged "installed" below.

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
