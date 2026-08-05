# Migrating AO2 themes

AsyncAO reads standard AO2-Client theme folders.

## Where to put them

Copy your theme directory (the one containing `courtroom_design.ini`) into:

- `<config dir>/AsyncAO/themes/<theme name>/`
  - Windows: `%APPDATA%\AsyncAO\themes\...`
  - Linux: `~/.config/AsyncAO/themes/...`
  - macOS: `~/Library/Application Support/AsyncAO/themes/...`
- or a `themes/` folder next to the executable.

You can also just drop the theme folder itself on the window. A theme downloaded
as a zip unpacks to a bare folder with no `themes/` parent, and that works too.

The client shows you the exact folder it uses in **Settings → Theme → Your
themes**, with an **Open folder** button beside it — that is the one it writes
into, and the config-dir path above is now searched even without setting a custom
theme folder.

## What's supported

- `courtroom_design.ini` — element positions/sizes (`element = x, y, w, h`),
  read with AO2's lookup ladder: your theme first, then the `default` theme.
- **Subthemes** — a folder inside your theme with its own
  `courtroom_design.ini` (AO2's `subtheme` option). Pick one in **Settings →
  Theme → Subtheme**; its files win over the theme's, and anything it leaves out
  still comes from the theme. As in AO2, the `default` theme fallback has no
  subtheme tier, so a variant only ever reaches inside the theme that owns it.
- `courtroom_fonts.ini` — sizes, `<element>_color = r, g, b`, `<element>_bold`.
- `courtroom_sounds.ini` — sound names.
- Theme images (chatbox, chat arrow, shout bubbles...) in any of
  `.webp/.apng/.gif/.png`, looked up theme-first then default.

## The rule: an AO2 theme gets AO2's layout

Inside your theme's design canvas, **only AO2 widgets draw, at the rects you
declare**. AsyncAO does not mix its own arrangement into your layout.

Four consequences worth knowing when you author a theme:

1. **AsyncAO binds to AO2's own keys.** A control that exists in both clients is
   placed by the AO2 key you already know — `text_color`, `pre`,
   `pre_no_interrupt`, `flip`, `additive`, `guard`, `slide_enable`,
   `showname_enable`, `sfx_dropdown`, `realization`, `screenshake`. There is no
   parallel AsyncAO spelling to learn.
2. **A rect you don't declare draws nothing.** That is what AO2 does too —
   `set_size_and_pos` warns and hides the widget — so a deliberate omission is
   honoured rather than second-guessed.
3. **A rect you don't declare still gets AO2's stock default.** Point 2 is about
   your *canvas*, not about losing controls: if you leave `text_color` out,
   AsyncAO falls back to AO2's own default-theme rect for it, exactly as AO2
   falls through to `base/themes/default`. You only lose a widget if you
   deliberately place it off-canvas.
4. **AsyncAO's own extras live outside your canvas.** Anything with no AO2
   equivalent — the Extras box, Restyle, the emoji picker, Text FX, the mod and
   CM panels — is in the menu bar above the canvas, not painted over your art.

If a theme declares a rect AsyncAO does not read, the debug log names it on theme
apply, so you can tell "not supported yet" from "you typed the key wrong".

## Optional: pulling AsyncAO's extras INTO your canvas

If you *want* an AsyncAO-only control inside your layout, these optional keys
place it, in the same `x, y, w, h` design-space format as any AO2 element:

| Key | Control |
|---|---|
| `asyncao_ic_color` | the text-colour swatch + dropdown |
| `asyncao_ic_immediate` | the "Immediate" (non-interrupting preanim) checkbox |
| `asyncao_ic_pre` | the "Pre" (preanimation) checkbox |
| `asyncao_ic_sfx` | the per-message sound picker |
| `asyncao_ic_emoji` | the emoji-picker button |
| `asyncao_ic_fx` | the Text-FX (shake / wave / rainbow) button |
| `asyncao_toolbox` | the compact bottom-right toolbox grip |
| `asyncao_tabbar` | the server-tab strip |

These are an **override tier**: where one overlaps an AO2 key for the same
control, the `asyncao_` key wins. They are opt-in, so a stock AO2 theme that
declares none of them is unaffected by their existence.

`asyncao_ic_react` was removed — the React button no longer exists, so the key is
ignored.

In the DEFAULT (non-themed) layout these same pieces are instead each draggable
in the in-app **Edit Layout** editor.
