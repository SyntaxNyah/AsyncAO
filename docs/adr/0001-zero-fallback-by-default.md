# ADR-0001: Zero fallbacks by default

## Status

Accepted.

## Context

Historical AO clients probe a ladder of file extensions for every asset
(`.webp → .apng → .gif → .png`, audio similarly). On a cold load of a
200-character server that multiplies every miss by the chain length — easily
1000+ requests, most of them guaranteed 404s, while the user stares at the
character grid. Server operators meanwhile have no signal pushing them toward
modern formats: the client silently papers over whatever mix they ship.

Earlier drafts of this client also invented a `.webp.animated` extension and
probed it *in addition to* `.webp`, doubling sprite probes for a format
distinction that does not exist on any server: animation is a property of the
WebP payload (the VP8X `ANIM` flag), not of its file name.

## Decision

1. **The default probe list per asset type is exactly one format**: `.png` for
   character icons (servers don't ship WebP icons), `.webp` for every other
   image type, `.opus` for audio. With fallbacks off this list is the entire
   universe — one probe per asset, ever.
2. **Fallback chains are opt-in** (per type or globally). When enabled they
   append the legacy chain, deduplicated, order preserved.
3. **A miss is loud**: if every candidate 404s, the client shows a visible
   warning naming the asset and the formats tried — "enable fallbacks or ask
   the server to ship .webp". Content problems become operator-visible
   instead of being quietly absorbed as latency.
4. **`.webp.animated` is abolished.** Decode-time sniffing (VP8X ANIM flag,
   APNG acTL chunk, GIF frame count) decides animation. `Play Animations` is
   purely a decode/render toggle and never causes an extra probe.
5. **Learned formats** make even the single probe disappear over time: the
   first success per (host, type) is remembered and persisted, so steady
   state resolution is a memory lookup.

## Consequences

- Cold loads cost N probes for N assets (measured: 285/285 on a 200-char
  server, budget ≤ 450) instead of N × chain length.
- Users on mixed-format servers must tick a fallback box once — and the
  warning tells them exactly when and why.
- Content creators get a gentle, factual push toward WebP/Opus.
- A server that repacks formats invalidates its learned entry automatically
  (the stale format 404s once, the client re-probes the full list and
  re-learns).
