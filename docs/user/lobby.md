# Lobby, server phone book & direct connect

## The list

The lobby merges your **server phone book** with the live master list.
Every row shows the server's name, player count, security tier and — on
hover — its full **description** at the bottom of the screen. Descriptions
come straight from the master list and are saved into your phone book when
you star a server, so they stay visible even if the master list is down.

Color tiers:

| Color | Meaning |
|---|---|
| 🟩 Green | `wss` (TLS WebSocket) — the fastest and secure (https) |
| 🟨 Yellow | plain `ws` only — joinable, but the connection is unencrypted |
| ⬛ Black (bottom) | legacy TCP only — **not supported**; server owners should upgrade their software to WebSockets if they want people to join |

Sort order: ★ favorites first, then joinable servers by player count,
legacy always pinned to the bottom under a NOT SUPPORTED banner.

## Phone book (favorites)

- Click the ★ on any row to save it (name, address **and description**) or
  remove it.
- Favorites pin to the very top and survive restarts.
- Private servers saved via direct connect live here too.

## Direct connect

Type any of these into the direct-connect box:

```
51.81.186.2:27014          → connects as ws:// (tick "TLS (wss)" for wss://)
my.private.server:50001
ws://host:port
wss://host:port
```

The port is required. Tick **Save to phone book** to keep it. This is how
you join private servers that never appear on the master list.

## Character select

- Search box filters the list live; icons stream in as they download.
- Taken characters are dimmed; **Spectate** joins without a character.
- Hover an icon for 3 seconds — or right-click it — to pop up the full-size
  character sprite. Same for emote buttons in the courtroom.
