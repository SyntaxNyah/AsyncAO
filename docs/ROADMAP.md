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
- **Friend nickname in the IC log** *(#82 follow-up)* — the nickname + custom
  colour now show on the **player-list row**; rendering the nickname in the IC
  log too (as `nick (showname)`) was deliberately deferred — it crosses the
  anti-impersonation force-char path, so it's a separate, careful change. The
  per-friend glow **colour** already applies in the IC log.

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
  WebP export, crop/trim, per-line effects, **proportional timeline strip with
  draggable In/Out handles** (#75, shipped) — all in `docs/FEATURES.md`. Possible
  later tweaks: continuous-playback scrubbing, drag-to-reorder on the strip.
- **M8 Gamepad support** *(#44)* — the last untouched milestone.
