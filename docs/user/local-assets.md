# Local assets — your own folders and .zip packs

**Settings → Assets → Asset source** picks where AsyncAO reads art, sound and
music from. There are three choices:

| Mode | What it does |
|---|---|
| **Stream everything from the server** *(default)* | Every asset comes from the server's asset URL. |
| **Use my folders first, then stream the rest** | Your folders and `.zip` packs are read first; anything you don't have still streams. |
| **Only use my folders — never stream** | Nothing is streamed. For legacy servers that provide no asset URL at all. |

## Layering your own content over a server

The middle mode is the one most people want. Add a folder (or a `.zip`), pick
it, and your files are used **instead of the server's copies at the same
paths** — while everything you *don't* have keeps streaming normally.

One exception: a file AsyncAO already downloaded from the server and keeps in
its cache wins over yours. Clear the cache (Settings → Cache) to make your copy
show for a file the server has already sent you.

That makes it useful for two different things:

- **Adding** content the server lacks — a private pack of characters, a few
  extra backgrounds.
- **Replacing** content the server does have — your own re-skin of a character
  everyone else sees the stock version of.

**Put your files at the same paths the server uses.** Layering matches
file-for-file: a pack that ships `characters/phoenix/normal.png` does *not*
override a server that ships `characters/phoenix/(a)normal.webp`, because those
are different paths. The extension does not have to match — a `.png` pack works
over a `.webp` server — but the folder and file *name* must.

## Mounts

- A mount is a **folder or a `.zip` pack**. A zip is just a folder someone
  handed you as one file; the layout inside is identical.
- Mounts are searched **in order, first hit wins** — the same rule AO2-Client
  uses for mount paths, so you can stack a small override pack above a full
  base install.
- Any layout works; nothing is hardcoded to `/base`.
- **Case doesn't matter.** A pack authored on Windows resolves the same way on
  Linux and macOS, so `Phoenix Wright/(a)Normal.png` and
  `phoenix wright/(a)normal.png` are the same file to AsyncAO.
- Up to 16 mounts.

## Adding and changing files

AsyncAO looks in a folder the first time it needs a file from it, and notices
new, removed and renamed files by itself: it looks again when a file is missing
and its last look is more than 30 seconds old, and every time you come back to
the AsyncAO window.

Press **Check folders now** after changing a file the game is already showing:
the art drawn from your folders reloads, and anything that failed to load is
tried again.

## When a file in your pack is damaged

If a file in your folders can't be read or decoded, AsyncAO **uses the server's
copy for that one asset instead** and tells you how many files that happened
to. Your pack is not disabled, and the server's copy is not penalised for your
file being broken. Fix the file and press Check folders now.

One exception: a damaged music or sound file is only re-tried the next time
that track plays.

## Sharing a scene that used your pack

The **content report** resolves through your folders too, so it describes what
*you* actually see. Anything your folders answered is tagged **"your folders"**
and counted separately from the server's own content — because when you send a
report or a recording to someone, that count is exactly what they will be
missing.

**Exporting a self-contained scene archive** will include those files so the
bundle plays correctly for whoever you send it to. Because that copies your own
art into a file you hand to someone else, AsyncAO asks first and tells you how
many files it means. Declining exports nothing at all — a bundle missing
precisely the art that isn't on the server would play wrong for the recipient,
which is worse than no bundle.

## What layering never does

- **Your folders are never uploaded**, and never shared with the server or
  other players. The one exception is a scene archive you deliberately export
  after confirming it — see above.
- **Your files are never written to the disk cache**, so removing a mount
  removes its content completely.
- Layering **replaces** art for things the server lists; it does not **add**
  entries to the character list, the music list, or the background picker.
  Those come from the server.
- The server's `extensions.json` is never read from a pack, so a pack carved
  out of some other server's base can't confuse this server's format handling.

## Performance

With no folders configured, nothing is read and no folder is looked at.

With folders configured, AsyncAO lists a folder once, when a file in it is
first needed, and answers from that listing after. A `.zip` pack is indexed the
first time it is needed. A file your pack provides costs **no network request
at all**.

## Legacy: never stream

The third mode is the old behaviour, for servers that provide no asset URL.
Everything must come from your folders; nothing falls back to the network.
