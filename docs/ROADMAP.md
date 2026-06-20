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

### Bugs
- **Player list shows the wrong character for AsyncAO users** *(#88, diagnosing)*
  — picking a char (e.g. #43 Phoenix) shows them as a different char (e.g. #0) in
  the player list, while their IC sprite is correct. The list shows the server's
  PU char (folder name), and IC is right because MS carries the name directly —
  so the server recorded the wrong char_id. Our CC send looks correct in code
  (absolute SC index), so debug logs were added (CC char_id sent + PV char_id
  assigned) to pin client-vs-server on the next playtest. **Needs: Debug-overlay
  repro, or which screen the pick came from (main grid / Wardrobe tab / switch).**

### Player list / social
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
