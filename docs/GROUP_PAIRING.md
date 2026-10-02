# Group Pairing (`/grouppair`) — AsyncAO Client

> Server: **Nyathena**. See Nyathena `docs/GROUP_PAIRING.md` for the full wire
> contract (GP packet, `additional_chars`, ordering, offset escaping, `side`).

## Overview

Unbounded group pairing. N players form a group; when any member speaks, all
members render front→back behind the speaker (speaker included in the ordered
list). There is no size limit.

## Wire channels

- **`GP`** — JSON-only roster snapshot, sent to **group members** on group
  change. Decoded in `internal/packetutil/gp.go`.
- **`additional_chars`** — JSON field on every `MS`, broadcast to **everyone**
  in the area, present only when a group member speaks. Read from
  `aolib.MSToClient.Extras["additional_chars"]`.

## Data flow

1. `internal/protocol/inbound.go` — `s.OnMS(func(p *aolib.MSToClient){ enq(p) })`
   enqueues the typed MS (with its `Extras`).
2. `internal/courtroom/session.go` — `case "MS":` runs
   `protocol.ParseMS(p.Fields, ...)`, then
   `msg.Additional = protocol.ParseAdditionalChars(typed.Extras["additional_chars"])`.
3. `internal/courtroom/courtroom.go` — `begin()` builds `Scene.Group` from
   `msg.Additional` (**per-message**), not `Session.GroupPair`.
4. `internal/render/viewport.go` — `Render()` draws `Scene.Group` behind the
   speaker/pair; each layer's `OffsetX/Y` is applied in `drawSprite`.

## Key files

| file | role |
|---|---|
| `internal/packetutil/gp.go` | `GP` / `GPMember` JSON decode (`char_id`, `offset{x,y}`, `flip`, `order`, `side`) |
| `internal/protocol/grouppair.go` | `GroupPairMember`, `FromGP`, `ParseAdditionalChars` |
| `internal/protocol/ms.go` | `ChatMessage` (adds `Additional []GroupPairMember`) |
| `internal/courtroom/session.go` | MS → `ParseAdditionalChars` → `msg.Additional` |
| `internal/courtroom/courtroom.go` | `begin()` → `Scene.Group` from `msg.Additional` |
| `internal/render/viewport.go` | group draw loop; `reduceInactiveAnimations`; `drawSprite` offset |

## Gotchas / pitfalls

- **Render from `additional_chars`, not `Session.GroupPair`.** The roster is
  only for group lifecycle. (This single change fixes both the new-joiner blank
  and the "4th partner" ghost.)
- **`ChatMessage` now contains a slice (`Additional`).** Any `==` / `!=`
  comparison of a `ChatMessage` value no longer compiles — use
  `reflect.DeepEqual` in tests (see `demoms_test.go`).
- **Prefetch is eager.** `begin()` prefetches each member's idle sprite with
  `network.PriorityHigh`, so the first frame isn't missing layers.
- **Offset is not forced.** `drawSprite` applies `layer.OffsetX/Y` (percent of
  viewport) per layer; the value flows per member from the roster/MS, never
  zeroed except for AO2-faithful desk-mod handling on the speaker.
- **`ParseAdditionalChars`** converts the JSON `additional_chars` array
  (`charid`, `name`, `emote`, `side`, `offset`, `flip`, `order`) into
  `[]GroupPairMember`; it returns `nil` for nil / non-array input.
