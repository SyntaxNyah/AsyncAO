# Local assets (no-streaming mode)

Some servers don't run an asset URL. For those, tick
**Settings → "Read assets from local folders instead of streaming"** and add
one or more **mount folders** — e.g. your existing AO2 `base/` directory, or
any folder laid out the AO way (`characters/`, `background/`, `sounds/`...).

- Mounts are searched **in order; first hit wins** — exactly like AO2-Client
  mount paths, so you can stack content packs over a base install.
- Any folder works; nothing is hardcoded to `/base`.
- Format preferences, learned formats and missing-asset warnings behave
  exactly as in streaming mode.
- The disk cache is skipped (your mounts *are* the disk).

Switch the checkbox off to return to streaming from the server's asset URL.
