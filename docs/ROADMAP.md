# ROADMAP — requested features

Playtest-driven backlog (Skrapegropen / Discord). Newest requests at the top of
each section. This is the single place every ask is captured so nothing is lost;
items move to `docs/FEATURES.md` as they ship.

**Standing constraints (every item):**
- **Zero performance degradation** — nothing added may cost the live render loop;
  `BenchmarkRenderFrame` must stay at **0 allocs/op**. New work lives off the hot
  path (settings, popups, overlays, off-thread I/O).
- Local commits only (never pushed); `go test -race -p 1 ./...` green before each
  commit; document every shipped item in `docs/FEATURES.md`.

---

## Planned

### Player list / social
- **Ignore / mute a person** *(#81)* — an "Ignore" option in the double-click
  player popup; hide/mute their IC (and OOC) messages.
- **Friend nicknames** *(#82)* — set a personal nickname for a friend, shown in
  the player list / IC.
- **Custom friend colours** *(#82)* — per-friend colour in the list / IC.

### Emotes
- **Emote favourites** *(#77)* — star emotes as favourites per character + a
  **"show favourites only"** toggle in the emote grid (characters can ship dozens
  of emotes but you use 6–8). Persisted per character.

### Chatbox / text
- **Rainbow in the colour selector** *(#79)* — when rainbow IC text is enabled,
  surface a **"Rainbow"** entry in the IC colour selector instead of a separate
  toggle.

### Hotkeys
- **Custom-hotkeys list window** *(#79)* — a window listing all the user's
  custom-made hotkeys (click to view them all in one place).

---

## Already shipped (rebuild to get them)

These were requested again but are already in the client — if they're missing,
it's a stale build (`scripts\build.ps1 -Release`).

- **Callword/alert volume separate from SFX** — `AlertVolume` is its own slider,
  independent of SFX volume (Settings → Audio).
- **Add-to-friends from the player popup** — double-click a player → the popup has
  a friend toggle (+ the per-row "+ Friend" button).

---

## In flight / larger (separate tracks)
- **M16 Scene studio** — recording, replay player, scene maker, GIF + animated
  WebP export, crop/trim, per-line effects (shipped); **timeline playhead** is the
  next optional piece (see `docs/FEATURES.md`).
- **M8 Gamepad support** *(#44)* — the last untouched milestone.
