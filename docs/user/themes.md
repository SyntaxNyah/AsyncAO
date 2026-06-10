# Migrating AO2 themes

AsyncAO reads standard AO2-Client theme folders.

## Where to put them

Copy your theme directory (the one containing `courtroom_design.ini`) into:

- `<config dir>/AsyncAO/themes/<theme name>/`
  - Windows: `%APPDATA%\AsyncAO\themes\...`
  - Linux: `~/.config/AsyncAO/themes/...`
  - macOS: `~/Library/Application Support/AsyncAO/themes/...`
- or a `themes/` folder next to the executable.

## What's supported

- `courtroom_design.ini` — element positions/sizes (`element = x, y, w, h`),
  read with AO2's lookup ladder: your theme first, then the `default` theme.
- `courtroom_fonts.ini` — sizes, `<element>_color = r, g, b`, `<element>_bold`.
- `courtroom_sounds.ini` — sound names.
- Theme images (chatbox, chat arrow, shout bubbles...) in any of
  `.webp/.apng/.gif/.png`, looked up theme-first then default.

Anything a theme doesn't define falls back to the default theme, exactly like
AO2-Client. Subtheme support and animated theme chrome follow the same file
conventions.
