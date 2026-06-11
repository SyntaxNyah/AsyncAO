# AO2 Protocol — as implemented by AsyncAO

Reference: AO2-Client 2.11 source (which wins every conflict) and live server
behavior. Everything here is implemented in `internal/protocol` and
`internal/courtroom/session.go`, with tests pinning each rule.

## Transport

WebSocket text frames only (`ws://` / `wss://`), one packet per frame.
Legacy raw-TCP framing is **deliberately not implemented**.

```
HEADER#field1#field2#…#%
```

Field escaping (apply on send per field; unescape after `#`-split on
receive), in AO2-Client's exact order:

| Char | Escape |
|---|---|
| `#` | `<num>` |
| `%` | `<percent>` |
| `$` | `<dollar>` |
| `&` | `<and>` |

Quirk kept for compatibility: `SC` character entries are split on `&` and
then percent-decoded *again* per sub-element (AO legacy double decode).

## Handshake (fast-loading; the only flow modern servers use)

| Server → client | Client replies | Notes |
|---|---|---|
| `decryptor#…` | `HI#<hdid>` | FantaCrypt is dead; HI goes plain |
| `ID#<player id>#<software>#…` | `ID#AsyncAO#<version>` | |
| `PN#<cur>#<max>[#desc]` | `askchaa` | player counts; the reply is what requests SI (webAO handshake.ts; AO2-Client networkmanager.cpp `join_to_server`) |
| `FL#<feature>…` | — | see Features |
| `ASS#<url>` | — | asset repo URL, percent-decoded (2.9.2+) |
| `SI#<chars>#<evidence>#<music>` | `RC` | begin fast loading |
| `SC#<name[&desc]>…` | `RM` | character list |
| `SM#<areas…><music…>` | `RD` | split at the first audio-extension entry |
| `DONE` | — | joined; char select usable |
| `CharsCheck#…` | — | taken flags (`-1` = taken) |
| `PV#<id>#CID#<char id>` | — | our character confirmed |
| `BN#<background>[#pos]` | — | background change |
| `MC#<track>#<char id>#…` | — | music / area transfer |
| `CT#<name>#<text>` | — | OOC chat |
| `KK/KB/BD#<reason>` | — | kick / ban notices |
| `checkconnection` | `CH#<char id>` | keepalive |

Outgoing actions: `CC#<player id>#<char id>#<hdid>` (pick character),
`MS#…` (chat), `CT#<name>#<text>` (OOC), `MC#<track>#<char id>` (music
**and** area transfers — an area name in place of a track moves rooms),
`ZZ[#reason]` (mod call).

Iniswap: the `char_name` field of outgoing `MS` is the folder receivers
stream sprites from; it need not match the server-slot character (servers
relay it as-is). AsyncAO populates it from the active iniswap override —
the slot, `CC`, and `PV` are untouched. The custom list itself comes from
`<asset origin>/iniswap.txt`, one folder name per line.

## Features (`FL`), wire names

`yellowtext flipping customobjections fastloading noencryption deskmod
evidence cccc_ic_support arup casing_alerts modcall_reason looping_sfx
additive effects y_offset expanded_desk_mods auth_packet prezoom
custom_blips` — matching is case-insensitive.

Gating rules implemented:
- MS fields ≥ 15 are honored only with `cccc_ic_support`.
- Pair `^order` is sent only with `effects`.
- `x&y` offsets are sent only with `y_offset` (else x-only).
- Custom objection names (`4&name`) require `customobjections`.
- `flipping` gates whether pair/self flips are rendered.

## MS — in-character message

Incoming indices (AO2-Client `CHAT_MESSAGE` enum): minimum **15** fields,
maximum **32**.

| # | Field | Parsing notes |
|---|---|---|
| 0 | desk_mod | non-numeric legacy `chat` → 0; EX modes 2–5 |
| 1 | pre_emote | `-`/empty = none |
| 2 | char_name | sprite folder |
| 3 | emote | `(a)`/`(b)` prefixes added client-side |
| 4 | message | |
| 5 | side | `def pro wit jud hld hlp jur sea` or unique pos |
| 6 | sfx_name | `0`/`1` = none |
| 7 | emote_mod | 0 idle, 1 preanim, 5 zoom, 6 preanim-zoom; legacy 2→1, 4→6, junk→0 |
| 8 | char_id | validated −1 ≤ id < len(chars) |
| 9 | sfx_delay | ms |
| 10 | objection_mod | `1` holdit `2` objection `3` takethat `4` custom; 2.8: `4&<name>` |
| 11 | evidence_id | |
| 12 | flip | `1` = mirrored |
| 13 | realization | |
| 14 | text_color | palette index |
| 15 | showname | overrides folder name |
| 16 | other_charid | `<id>` or 2.8 `<id>^<order>`; −1 = unpaired |
| 17 | other_name | pair folder; empty disables pairing |
| 18 | other_emote | pair plays looping `(a)<emote>` |
| 19 | self_offset | `<x>` or 2.9 `<x>&<y>`, percent of viewport |
| 20 | other_offset | same forms |
| 21 | other_flip | |
| 22 | immediate | preanim alongside text |
| 23 | looping_sfx | |
| 24 | screenshake | |
| 25–27 | frame_screenshake / _realization / _sfx | per-frame effect packs |
| 28 | additive | append to previous message |
| 29 | effects | `effect|folder|sound` |
| 30 | blipname | custom blip set |
| 31 | slide | 2.11 slide toggle |

### Pairing semantics (golden-tested)

- Active pair = `other_charid != -1` **and** `other_name` non-empty.
- `^0` → **speaker renders in front** (default when no `^`); `^1` → speaker
  renders behind the partner.
- Offsets move sprites by percent of viewport width/height (−100..100).
- Pair partner always plays the looping idle `(a)` animation.
- Pair display is skipped while the speaker zooms (emote_mod 5/6).

### Outgoing MS is asymmetric

The client never sends `other_name`, `other_emote`, `other_offset`,
`other_flip` — the server injects the partner's data when relaying. The
outgoing CCCC block is exactly: showname, other_charid(±`^order`), offset,
immediate. Field count therefore varies by server features (15 bare → 28
full); see `OutgoingMS.Fields`.

## Asset URL conventions (webAO-mirrored)

```
characters/<char>/char_icon.png        icons (PNG only by default)
characters/<char>/(a)<emote>.webp      idle    — (b) talk, bare preanim
characters/<char>/<shout>.opus         shout cries
characters/<char>/<shout>_bubble.webp  bubbles (fallback misc/default/)
background/<bg>/<part>.webp            defenseempty, stand, …
sounds/general/<sfx>.opus
sounds/blips/<blip>.opus
sounds/music/<track>                   track name carries its extension
```

All segments lowercased and encodeURI-escaped (parentheses literal). Side →
part mapping and the 2.8 unique-position convention (`<pos>` /
`<pos>_overlay`) follow AO2-Client `path_functions.cpp`.
