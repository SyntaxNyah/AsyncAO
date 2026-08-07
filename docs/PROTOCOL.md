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
| `PR#<id>#<type>` | — | live player roster change: type `0` join, `1` leave (PlayerStateObserver; streamed from connect, no opt-in) |
| `PU#<id>#<type>#<data>` | — | live player field: type `0` OOC name, `1` char folder, `2` showname, `3` area id, `4` IPID (extension — see below); UID-keyed |
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

Desk visibility: the outgoing `MS` `desk_mod` (field 0) is the SELECTED emote's
char.ini `desk_mod` (the optional 5th `[Emotions]` field), defaulting to **show**
(`1`) when the emote omits it — AO2 `get_desk_mod` parity (an absent field reads
as a non-hide value). Only an explicit `0`/`3`/`5` hides the desk. A hardcoded
`1` previously meant a no-desk emote never hid the desk for the room (fixed).

## `PU` type 4 — the mod-only IPID field

Stock `PU` carries no IPID, which is the one field a moderator needs and the only
reason a client has to fall back on polling `/gas` for a roster it already has
live. AsyncAO accepts **`PU#<id>#4#<ipid>`** as a dedicated field.

Because 2.11 addresses `PU` by position, adding a type is backward compatible: a
client that doesn't know type 4 falls through its switch and ignores it.

Notes for server authors:

- **Send it per recipient, only to authenticated moderators.** `PU` is written
  per client, not broadcast once, so this is a routing decision the server owns.
  Sending IPIDs to every client would deanonymize every player to every other
  player regardless of what any client's UI chooses to display — a modified
  client sees whatever it is sent.
- **The value is opaque.** Akashi uses 8 hex characters; Athena/Nyathena use a
  longer base64-ish token. AsyncAO stores it verbatim and validates nothing
  beyond non-empty, so any server's format works.
- **It is sticky.** An omitted or empty type 4 never clears an IPID already held,
  so a server may send it once per player rather than on every update.
- Escape it like any other text field.

The alternative in the wild is the witches/wizards Akashi-party trick of
appending a `(<hex>)` token to the `PU` **name** field. AsyncAO still supports
that, but only for servers identifying as that family, because a player whose OOC
name merely ends in brackets would otherwise be mis-read as an IPID and could
build a wrong-target ban. A dedicated type has no such ambiguity and therefore
needs no family gate — prefer it.

## Rate limits and close codes

Servers flood-guard **inbound** packets and kick on breach. Nyathena's `pktOOC`
runs two counters: `checkIPOOCRateLimit` (`ooc_rate_limit`, counted per **IP** —
every tab behind one address shares the budget) and `CheckRateLimit`
(`message_rate_limit`, counted per client across every message kind). Either
breach runs `KickForRateLimit`. Both budgets are operator-set with no built-in
default — the shipped sample is 4 per 1 s for OOC and 20 per 10 s for messages,
and deployed servers commonly tighten the message budget to nearer 1.4/s, which
is the figure the client's pacing is sized against.

That kick is silent on the wire: the server queues its explanation
**asynchronously** and closes the socket **synchronously**, so the reason
usually loses the race and only the close arrives.

Nor does the close frame say anything. Nyathena wraps each client in
`websocket.NetConn`, and coder/nhooyr's `netConn.Close()` is hardcoded to
`Close(StatusNormalClosure, "")` — so kick, ban, area cleanup and shutdown all
emit an identical **1000, empty reason**. On these servers that code means only
"server code called `Close()`"; it is *not* evidence of an idle timeout or a
graceful goodbye, and a client must not report it as one. If anything it points
the other way: a moderator kick (`KK`) normally lands before the socket goes, so
a reasonless close is the fingerprint of an automated guard.

The implementer's rule: **never emit a burst of automated commands.** Anything
sent without a keypress behind it — roster polls, login sequences, macros —
needs a hard minimum gap enforced where the packet is *sent*, not where it is
scheduled, because a queue that stalls and then flushes its due entries is
indistinguishable from flooding. AsyncAO's automated OOC path (`oocSendMinGap`,
`internal/ui/macros.go`) releases one line per second and is drained from both
the frame loop and the background pump, so the rate never depends on window
state; see KNOWN-ISSUES.md for the disconnect this caused when it didn't.

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

## Music list — display name vs wire name

`SM` ships music entries raw and `MC` echoes them raw; the raw string is also
what `sounds/music/<track>` is built from. What AO2 *shows* is derived, in
`Courtroom::list_music` (courtroom.cpp:1682-1782):

```
listname = entry.left(entry.lastIndexOf("."));
listname = listname.right(listname.length() - (listname.lastIndexOf("/") + 1));
```

Cut at the LAST `.`, *then* keep everything after the LAST `/`. That order is
canonical and is *not* basename-first: `vol.2/name` → `vol`. For
any entry that genuinely ends in an extension the two agree, so
`999/songs/that/slap/[999] Tranquility.ogg` displays as `[999] Tranquility`.
A direct `http(s)://` track is instead shown as `QUrl(f_song).fileName()`
(courtroom.cpp `handle_song`), which drops the query first — necessary because a
signed CDN link carries dots after its extension.

Two classifications ride on the same transform:

- **Category headers.** `QString::left(-1)` returns the whole string, so an
  entry with no `.` comes back unchanged, and AO2 reads `listname == entry` as a
  category — a top-level tree item that the following entries nest under.
  AsyncAO classifies headers by audio extension instead
  (`courtroom.HasAudioExt`, the same rule the `SM` area/music split uses), which
  cannot mis-cut a header that happens to contain a dot.
- **Stop sentinels.** `~stop.mp3`, and any entry that reads "stop" once `=` is
  stripped (`=stop=`), are not tracks — AO2 has no stop packet, so stopping is
  requesting one of those fake entries (courtroom.cpp `music_stop`), and
  `list_music` skips them so they can't be clicked as songs. AsyncAO leaves them
  in the list (its Stop button halts local playback first and requests the
  sentinel second, so a server that lacks one still stops for the listener).

The derived name is **display only**. The `MC` request and the music URL must
carry the entry byte-exact — the transform is lossy (it drops exactly the
directory and extension a fetch needs), so a client that shortens the name it
sends will request a track no server has.

## Asset URL conventions (webAO-mirrored)

```
characters/<char>/char_icon.png        icons (PNG only by default)
characters/<char>/(a)<emote>.webp      idle    — (b) talk, bare preanim
characters/<char>/<shout>.opus         shout cries
characters/<char>/<shout>_bubble.webp  bubbles (fallback misc/default/)
background/<bg>/<part>.webp            defenseempty, stand, …
sounds/general/<sfx>.opus
sounds/blips/<blip>.opus               blip set — then sounds/general/sfx-blip<blip>
                                       (AO1 legacy), then sounds/general/<blip>
sounds/music/<track>                   track name carries its extension
```

A blip name walks all three spellings, each in the lowercase identity casing then
the authored one: AO2's `get_blips` probes the same three against a
case-insensitive local disk (`text_file_functions.cpp:515-527`), and the char.ini
key it came from (`blips` or the legacy `gender`) does not choose between them —
`get_blipname` returns a bare name (`:487-514`). `URLBuilder.BlipRef` is the only
place any of it is built.

All segments lowercased and encodeURI-escaped (parentheses literal). Side →
part mapping and the 2.8 unique-position convention (`<pos>` /
`<pos>_overlay`) follow AO2-Client `path_functions.cpp`.
