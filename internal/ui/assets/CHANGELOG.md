# AsyncAO — Changelog

What changed, newest first. The "What's New" screen renders this embedded file,
so every build ships its own history offline. The version you're running is
tagged "installed" below.

## v2.1.1 - 2026-11-28

A bug-fix release for v2.1.0.

- A sprite saved as an animated WebP with a single frame shows again. Before,
  it never appeared, and with strict sequencing every message using it waited
  10 seconds.
- A message waiting on a missing file no longer sits out the whole 10 seconds
  when the missing-file notice gets lost; it asks again every second.
- Repeated blankposts from the same speaker show once in the IC log, as in AO2.
- The sprite preview uses the character's own scaling from the first frame, so
  pixel-art characters no longer preview blurry until they speak.
- Effect icons in the effects dropdown keep their shape instead of stretching.
- The SFX Browser opens in front of the Extras box instead of behind it.
- Each list's ⋮ menu shows only its own options: the player list no longer
  offers music fade and loop, and the music list no longer offers roster
  options.
- A theme without `courtroom_design.ini` no longer draws the drag hint over
  its notice in the theme fit preview.
- Fixed a crash when several servers sent their first packets at once.
- New Wardrobe setting in Settings ▸ General: **Click: Selects / Wears**. With
  Wears, one click wears a character. Off by default.

## v2.1.0 - 2026-11-14

AsyncAO moves from SDL2 to SDL3. It draws with Direct3D 11 on Windows and
Metal on macOS, falls back to a much faster CPU renderer when the graphics
driver can't be used, and brings a rebuilt Wardrobe, a reworked Assets tab and
an Updates tab.

### Before you update
- **Video export is gone.** The 🎥 Video button, its sound mixing and the
  subtitle files are removed with the ffmpeg dependency they needed. GIF, WebP
  and Comic exports stay and need no extra program.
- **Voice chat is gone.**
- **The Discord-free builds are gone.** Players on one are moved to the regular
  build. Rich Presence is on by default there; turn it off in Settings.
- **PAC and SOCKS5 proxies are no longer supported**, and the server list no
  longer pings each lobby. HTTP proxies still work for all traffic, including
  the proxy set in Windows or macOS.
- **Packed-room catch-up is now off by default**, and turned off once if it was
  on, so the courtroom plays every message in order like the original client.
- **Your asset folders no longer win over the server's cached copy.** A file
  AsyncAO already downloaded and keeps in its cache is used before the same
  file in your folders; clear the cache to make yours show for it. Files the
  cache doesn't hold still come from your folders first.
- **Your favourites are now shared by every server.** On first start, each
  server's saved characters merge into one list and their folders come along.
- **Gone from the Wardrobe:** the Backgrounds tab (the Background picker keeps
  your starred backgrounds), Quick-swap (Ctrl+J), character key binds, number
  keys for filing, the "From your base" list, the Iniswap chip in the menu bar,
  and cycling a character's emotes in the hover preview.
- **The server's iniswap list is now `iniswaps.ini`**, the name AO2 uses,
  instead of `iniswap.txt`. Server owners who published `iniswap.txt` should
  rename it.
- **Also gone:** the single-asset downloader and its download badges, predictive
  prefetch with **Preload a character's whole sprite set**, the connection ping
  chip, the missing-asset warnings chip (missing assets are still listed in
  Debug ▸ Log), **Loop preanimations** (preanimations play once, as in AO2),
  sprite thumbnails and disk cache compression. Files the compression stored
  are downloaded again the first time they're needed.
- **Clients from v1.95.1 or older can't update themselves to this.** They open
  the release page instead; download the bundle from there.

### Graphics
- **SDL3, drawing with Direct3D 11 on Windows and Metal on macOS.** If the
  graphics device resets, AsyncAO restarts and reopens your tabs.
- **When the graphics driver freezes**, AsyncAO notices after a few seconds and
  restarts drawing on the CPU instead of staying a black "Not responding"
  window. It remembers this; **Draw on the CPU (for graphics drivers that
  freeze)** in Settings ▸ Power user switches back after a driver update.
- **Drawing on the CPU is many times faster.** Sprites, backgrounds and desks
  are kept at their size on screen instead of being rescaled every frame. When
  only an animated background or a typing message changes, only that part of
  the window is redrawn. Text, icons and theme art no longer get slower the
  busier a room gets. In our measurements a busy room's frame went from over
  100 ms to a few milliseconds.
- File and folder pickers are the operating system's own dialogs, and you can
  add several asset folders at once where the dialog allows it.

### Wardrobe
- **Open it** from Extras ▸ Wardrobe… in the menu bar, the Wardrobe button in
  the courtroom or in Character Select, the Extras box, or Ctrl+6.
- **User / Server**: your own favourites, or the characters the server lists.
  **Grid or List** view.
- **Folders** on the left. A character can be in **several folders**. Hover a
  folder to rename it, delete it, or give it an icon, a colour or your own
  image (drop a PNG, WebP or GIF on it). Drag folders to reorder them.
- **Click selects, double-click wears**: it switches to the character when the
  slot is free and wears it as an iniswap otherwise, and tells you which.
  Iniswaps carry a small yellow tag. From Character Select, wearing a saved
  character takes the first free character and enters the courtroom wearing it.
- **Nicknames** show instead of a character's name, with the real name under
  it; only you see them.
- **Sort** by your own order (drag cards), name, last worn, or folder. Click the
  sort that's already on to reverse it; your own order is never changed.
- **Add** characters by typing their folder names, or tick them from the
  server's list. Character Select's ★ saves a character, and a second click
  removes it.
- **Settings ▸ General ▸ Wardrobe**: font, text size, icon size, highlight
  colour, and whether the name sits beside or above the icon.

### Assets
- **Asset source** is one list of everywhere AsyncAO looks, searched top to
  bottom: your folders and .zip packs, the packages folder, then the server and
  its mirrors. Move your folders up or down with ▲ ▼.
- **Use my folders and packs**, **Use packages** and **Stream from the web**
  replace the three old modes, and any of them can be on alone.
- Add a folder, a .zip or a mirror's https:// address in one field, use **Add
  folders…** and **Add packs…**, or drag a folder or .zip onto the Assets tab.
  Each folder is checked when added and shows what it holds, or says **Not an
  AO base** with a button for the right folder.
- Your folders and packs are read when a file in them is first needed, and edits
  to your files show by themselves when you switch back to AsyncAO.
- Assets load through a new media system that asks for file names in AO2's
  order, follows the server's `extensions.json`, and tries a server's extra
  asset hosts after its own.
- **Cache** lists each cache with what it holds, its Clear or Retry and its
  settings. **Pick where the disk cache lives** with **Change…**.
- **Missing sprites** can use your own image.

### Updates
- A new **Updates** tab in Settings shows the version you're running, the commit
  and the date it was built, and the newest version on offer. **Check for
  updates** checks again at any time.
- **Check for updates on launch** and **Include pre-releases** live there.
  Pre-releases reach only players who switch that on.
- The window title shows the version.

### Text, courtroom and more
- Every label falls back glyph by glyph to a font that has the character, so
  mixed scripts and symbols no longer turn into boxes, and colour emoji show in
  every label.
- A speaker's whole animation plays before their message starts.
- The Pos dropdown lists the positions the background defines, and moving
  between them slides across the panorama, as in AO2.
- The desk is the one that belongs to the background that actually loaded.
- Sprites sit where AO2 puts them at every window size.
- Streamed music picks up its loop points.
- Connecting runs in the background, so the window never freezes while a server
  answers.
- The Jukebox has its own window: Extras ▸ Jukebox… in the menu bar, the Extras
  box, or Ctrl+7.
- On Windows, notifications come from AsyncAO with its own name and icon.

### Troubleshooting
- **`-debug`** writes everything AsyncAO logs to `asyncao-debug.log` next to
  the program, so a report needs no console. Every 5 seconds it also logs how
  many recent frames redrew only part of the window and why the others redrew
  it all.
- **Debug ▸ Perf** (F8) shows the same redraw line. Keep the F3 overlay closed
  while you measure: it redraws every frame on purpose.
- **`-software-gpu`** starts AsyncAO drawing on the CPU, for testing or for a
  graphics driver that misbehaves.

## v2.0.2 - 2026-10-31

- Emoji and symbols fall back to a font that has them in more places.
- The desktop notification for a group invite is now opt-in, off by default.

## v2.0.1 - 2026-10-06

- Fixed the address of the Umineko master server list.

## v2.0.0 - 2026-10-06

AsyncAO 2.0. The headline this time is the wire: every packet now goes through
aolib-go, the canonical AO2 protocol library, so the client speaks both
FantaCode and JSON instead of hand-rolling FantaCode. That's the foundation the
rest of the release is built on — group pairing is the first feature to ride it.

### The JSON wire (aolib-go)
- Every packet now encodes and decodes through aolib-go, the same protocol
  library family LemmyAO uses. AsyncAO speaks both wires on the same connection
  and auto-detects which one a server wants, so it just works on classic AO2,
  Nyathena and JSON servers alike. The debug overlay shows which wire you're on.
- The old hand-rolled FantaCode framer is gone, and FantaCrypt stays dead — HI
  now goes out plain. Packets are a real typed schema instead of positional
  `HEADER#a#b#…#%` strings.
- Why it matters: it's the AO lib, just AO reimplemented cleaner. A real schema
  means validation, defaults and both wire forms for free, so adding a feature
  is "define the packet and register it" instead of counting `#` fields by
  hand. Group pairing below is the first thing built on it.
- Huge thanks to **OmniTroid**, who created aolib-go and the JSON wire protocol
  and gave a massive amount of help wiring it into AsyncAO. This release doesn't
  exist without them.

### Group pairing (/grouppair)
- Unbounded group pairing: any number of players form a group, and when one of
  them speaks everyone renders front-to-back behind the speaker. No size limit.
- It rides a custom GP packet plus an additional_chars field on each message,
  all over the JSON wire — a clean example of what the new protocol unlocks.
- For now it's Skrapegropen-only (that's the server with the matching server
  side), but it's the template for how much cleaner new features are going to be.

### Pair order
- Pair order is now wire-aware. Classic servers keep the `^0`/`^1` behaviour;
  JSON group rosters get a `/pairorder <uid> front|back` command plus a
  front-to-back roster with per-member up/down buttons.

### Message sequencing
- New "Default" loading mode (now the shipped default): a message waits for
  everything it presents — sprites, background, desk, chatbox, speedlines, SFX
  and blip — before it plays, one at a time, instead of fast-forwarding the
  backlog.
- A "Shouts" toggle (on by default) lets shouts queue instead of skipping, so a
  queued shout waits for its bubble art and cry.

### Controls
- All keyboard hotkeys are now opt-in and off by default, including the
  viewer-FX keybinds and the ambient-weather keybind. No more accidental snow or
  spotlight from a stray Ctrl chord — turn them on in Settings → Hotkeys.

### Multi-server list
- The lobby now polls both the classic and Umineko master lists
  (servers.aceattorneyonline.com and servers.umineko.online) and merges them,
  sorted by player count, so servers on either list show up.

### Fixes & polish
- Camera zoom keeps the scene inside the stage instead of spilling onto the UI.
- KFO community credit (Crystalwarrior + KFO) added to the About dialog.

### Thanks
- **OmniTroid** — aolib-go and the JSON wire, and an enormous amount of help
  getting AsyncAO onto it.
- **Crystalwarrior** and the whole **KFO** community — constant bug testing and
  pushing the client to its limits.
- Everyone who filed issue reports and feature ideas for this release (#136,
  #138 and more). The ideas are yours.
