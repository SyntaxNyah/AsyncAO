# AsyncAO — Changelog

What changed, newest first. The "What's New" screen renders this embedded file,
so every build ships its own history offline. The version you're running is
tagged "installed" below.

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
