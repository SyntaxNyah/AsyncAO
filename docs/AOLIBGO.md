# aolib-go dependency & dual-wire (FantaCode + JSON)

AsyncAO encodes and decodes **every** AO2 packet through
[`github.com/AO-Underground/aolib/go/v2`](https://github.com/AO-Underground/aolib/tree/main/go)
(the Go counterpart of [`aolib-ts`](https://github.com/AO-Underground/aolib/tree/main/ts),
which LemmyAO uses). There is **no** hand-rolled JSON encoder, and no
hand-rolled FantaCode framer left on the wire path: aolib owns both wire forms,
and AsyncAO delegates to it. This document explains the two wire formats, how
the library is wired in, and the exact contract.

## The two wire formats

Every packet has **two** encodings, and AsyncAO speaks both. The connection
auto-detects which one a frame uses and answers the server in kind.

| | FantaCode (classic AO2) | JSON (Nyathena / LemmyAO) |
|---|---|---|
| Framing | `HEADER#a#b#…#%` (positional) | `{"$header":"MS","desk_modifier":"shown",…}` |
| Enums | legacy integers (`0`/`1`/…) | strings (`"shown"`, `"def"`, `"no_preanim"`) |
| Offset | `x&y` | `{"x":0,"y":0}` |
| Booleans | `"1"`/`"0"` | `true`/`false` |
| Effects | `name\|folder\|sound` | `{"name":…,"folder":…,"sound":…}` |

A packet **not** in the canonical spec (the voice `VS_*` family, removed from
aolib in `aa8d0fb`) has *no* canonical JSON form — so AsyncAO registers it as a
both-wire codec (see below) instead of expecting aolib to model it.

### How the wire is chosen

1. **Default** is FantaCode (a client greets a FantaCode server as FantaCode).
2. **Auto-detect on receive** — a frame whose first byte is `{` is JSON; the
   connection flips its outbound wire to JSON immediately (per-frame, so mixed
   traffic is safe).
3. **Capability signal** — Nyathena advertises JSON support in the legacy
   `decryptor` greeting (`decryptor#JSON#%`). AsyncAO flips to JSON the moment it
   sees that, so the rest of the handshake (`HI`/`ID`/…) answers the server in
   kind. `Conn.SetJSONMode`/`Conn.JSONMode` expose this.

The active wire is surfaced in the **debug overlay** health line
(`wire fanta` / `wire json`).

## How aolib-go is wired in

```
WebSocket text frame
        │
        ▼
internal/protocol/conn.go ── readLoop / Send
        │
        ▼
internal/packetutil ── thin bridge over aolib-go
   ├── c2s.go     client→server parsers (typed reconstruction on send)
   ├── s2c.go     server→client decode (Fanta + JSON → positional)
   ├── codec.go   RegisterCodec dispatch + framing helpers
   ├── voice*.go  VS_* both-wire codecs
   └── ms_codec.go MS both-wire codec
        │
        ▼
github.com/AO-Underground/aolib/go/v2  (the canonical library)
```

- **`internal/packetutil`** is the only place AsyncAO reaches for things aolib
  keeps unexported (the FantaCode primitives `Itoa`/`AtoiOrZero`/`BoolToWire`,
  the wire-int enum maps, and a codec registry). Everything canonical is
  delegated straight to aolib.
- **Outbound** (`Conn.Send` → `packetutil.BuildWire`) reconstructs the typed
  packet from AsyncAO's positional fields (`Parse*`) and lets
  `aolib.Encode(typed, mode)` produce the wire. A packet that fails aolib
  validation falls back to positional FantaCode framing so nothing is ever
  silently dropped.
- **Inbound** (`Conn.readLoop` → `packetutil.Decoder.DecodeBody`) decodes Fanta
  directly, and decodes JSON through a `aolib.ServerSession` (so aolib applies
  schema defaults and validation) before folding the typed value back to the
  positional fields the courtroom switch consumes.

## Custom codecs

Canonical packets come from aolib's generated registry. Two non-canonical
surfaces are registered with `packetutil.RegisterCodec` so they still round-trip
on **both** wires:

- **Voice `VS_*`** (`internal/packetutil/voice.go`) — `VS_CAPS`, `VS_PEERS`,
  `VS_AUDIO`, `VS_FRAME`, and the bidirectional `VS_JOIN`/`VS_LEAVE`/`VS_SPEAK`
  (encode the client→server shape, decode the server→client shape). Wire shapes
  match Nyathena/LemmyAO exactly.
- **MS** (`internal/packetutil/ms_codec.go`) — see below.

## MS (in-character chat) — the one deliberate divergence

aolib models the canonical 26-field `MSToServer` (client→server) and 30-field
`MSToClient` (server→client). AO2-Client's MS carries a few **FantaCode-only
extensions** aolib does not model: the custom-shout **name** (`4&name`), the
pair **z-order** (`id^order`), and the 2.9 **blip name + slide** flag.

So the MS codec is intentionally asymmetric:

- **FantaCode** — positional and **lossless** (all fields preserved).
- **JSON** — encoded through aolib's typed `MSToServer`/`MSToClient`, dropping
  those Fanta-only extensions. This is exactly what Nyathena does (its own
  `ms_codec.go` notes the same omission).

## Known limitations

- **aolib-go v2.4.4's typed session surface is incomplete.** Unlike aolib-ts
  (which lets LemmyAO write `server.on.MS(...)` for every header), aolib-go's
  `session_server.go`/`session_client.go` are hand-written and only expose a
  subset of `Send*`/`On*`. AsyncAO therefore bridges the missing typed wrappers
  with `c2sParsers` + the positional fold-back — the *wire* is 100 % aolib; only
  the session-wrapper layer is bridged.
- **MS JSON loses the Fanta-only extensions** listed above (custom-shout name,
  pair z-order, blip, slide). FantaCode MS keeps them.
- **The CH keepalive** is still sent as raw FantaCode (a plain positional frame);
  the server accepts it regardless of wire mode because detection is per-frame.

## Credits

The [aolib-go](https://github.com/AO-Underground/aolib/tree/main/go) library and
the JSON wire protocol it implements were **created by
[OmniTroid](https://github.com/OmniTroid)**, co-authored with
[SyntaxNyah](https://github.com/syntaxnyah). AsyncAO's adoption of it is also
credited in the in-app **About** page.
