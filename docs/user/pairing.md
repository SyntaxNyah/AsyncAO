# Pairing

Pairing puts two characters on screen at once (AO2 ≥ 2.6 servers with
`cccc_ic_support`).

## Pairing up

1. In the courtroom press **Pair...** and pick a partner from the character
   list (or type `/pair <character id>` in the IC input; `/unpair` to stop).
2. Both players pair with *each other*. When either of you speaks, the other
   character appears alongside.

## Offsets, flip, order

- **Offset X/Y sliders** (or `/offset <x> [y]`) move your sprite in percent
  of the viewport (−100..100, steps of 5). They apply from your **next**
  message — AO semantics, no retroactive re-render.
- **Flip** mirrors your sprite horizontally (server must advertise
  `flipping`).
- **Render me in front** controls z-order on 2.8+ servers with `effects`:
  the speaker decides whether they render in front of or behind the partner.

Your offsets and flip persist between sessions.

## Performance note

The pair partner's sprite is fetched **in parallel** with the speaker's at
top priority — a paired message costs the same wall-clock time as a solo one,
even on a cold cache.
