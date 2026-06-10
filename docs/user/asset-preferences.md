# Asset Preferences

Open **Settings** from the lobby or the courtroom.

## Format probing

AsyncAO asks the server for exactly **one file format per asset type** by
default:

| Asset | Default | Why |
|---|---|---|
| Character icons | `.png` | servers ship PNG icons, not WebP |
| Sprites, backgrounds, bubbles, misc | `.webp` | modern, small, animated-capable |
| Sounds, music, blips | `.opus` | modern, small |

If your server still ships older formats you have two options:

- **Per-type checkboxes** — tick the extra formats (`.apng`, `.gif`, `.png`,
  `.jpg`) you want probed for that type. The type's default stays first.
- **Enable format fallbacks globally** — appends the classic legacy chain to
  every type (sprites: `.apng → .gif → .png`, audio: `.ogg → .wav → .mp3`).

When an asset doesn't exist in *any* enabled format you get a visible warning
naming the formats tried — that's your cue to tick a fallback box or ask the
server owner to upgrade their pack.

## Learned formats

The first format that works for a server is remembered (per server, per asset
type) and persisted. From then on that server costs one perfectly-aimed
request per asset — and after the disk cache warms, zero. **Clear Learned
Formats** resets this memory; changing a type's format list resets it for
that type automatically.

## Play Animations

Off = animated assets render their first frame only (low-end machines,
accessibility). This is purely a rendering toggle: it never changes what gets
downloaded.

## Caches

- **Clear disk cache** removes every downloaded asset blob.
- Each server's assets are cached under keys containing the full asset URL —
  servers can never see (or poison) each other's cache entries.
