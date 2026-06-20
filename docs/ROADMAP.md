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

### Themes
- **Faster theme navigation** *(#86)* — selecting a theme is slow when there are
  many; add a quick picker (search / scrollable list / preview) instead of
  cycling one at a time.
- **BUG: default theme not reselectable** *(#87)* — after pointing the client at
  a custom-themes folder, the stock/default theme can no longer be chosen — the
  custom one "overwrites" it. The default must always be selectable.

### Player list / social
- **Ignore / mute a person** *(#81)* — an "Ignore" option in the double-click
  player popup; hide/mute their IC (and OOC) messages.
- **Friend nicknames** *(#82)* — set a personal nickname for a friend, shown in
  the player list / IC.
- **Custom friend colours** *(#82)* — per-friend colour in the list / IC.

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
