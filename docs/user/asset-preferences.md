# Asset Preferences

Open **Settings** from the lobby or the courtroom.

## Formats

AsyncAO reads each server's `extensions.json` when you join and asks for its
art in exactly the formats and order it lists. A server without one is asked
in a built-in order (images: `.webp`, `.png`, `.apng`, `.gif`, `.avif`, `.jpg`;
sounds: `.opus`, `.ogg`, `.mp3`, `.wav`), with the formats that have worked
there going first. Files in your own folders and packs are found in any
format. There is nothing to configure.

## Learned formats

What worked on each server, and the exact file each asset was found as, is
kept in `learned.json` beside your settings, so the next visit asks the right
file first. **Settings → Assets → Cache → Learned formats → Clear** forgets it;
**Fix stuck images** forgets it and clears the disk cache together.

## Play Animations

Off = animated assets render their first frame only (low-end machines,
accessibility). This is purely a rendering toggle: it never changes what gets
downloaded.

## Caches

- **Clear disk cache** removes every downloaded asset blob.
- Each server's assets are cached under keys containing the full asset URL —
  servers can never see (or poison) each other's cache entries.
