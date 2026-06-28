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

## AsyncAO additions — splitting the IC bar

AsyncAO has a few IC controls AO2 themes don't (a text-colour picker, an
Immediate toggle, a per-message sound picker, and emoji / Text-FX / React
buttons). By default they sit inside the `ao2_ic_chat_message` rect — but a theme
can place each one **separately** with these OPTIONAL `courtroom_design.ini`
keys, in the same `x, y, w, h` design-space format as any AO2 element:

| Key | Control |
|---|---|
| `asyncao_ic_color` | the text-colour swatch + dropdown |
| `asyncao_ic_immediate` | the "Immediate" (non-interrupting preanim) checkbox |
| `asyncao_ic_sfx` | the per-message sound picker |
| `asyncao_ic_emoji` | the emoji-picker button |
| `asyncao_ic_fx` | the Text-FX (shake / wave / rainbow) button |
| `asyncao_ic_react` | the React button |

When a key is present the control draws at that rect and frees its space in the
message field; when a key is absent the control stays in the combined IC row,
exactly as before — so **existing themes are unchanged**. The showname box keeps
its standard `ao2_ic_chat_name` rect.

In the DEFAULT (non-themed) layout these same pieces are instead each draggable
in the in-app **Edit Layout** editor.
