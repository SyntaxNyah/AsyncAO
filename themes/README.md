# AsyncAO standalone themes

Fourteen complete, hand-authored themes for AsyncAO. Each folder here is a
**standalone theme**, not a preset: it carries both axes at once — layout
geometry *and* style — so it looks finished the moment you pick it, with no
preset pairing required.

These are distinct from the **embedded presets** that ship inside the client
(`internal/theme/presets/`). Presets are half-themes you combine; these are
whole ones you install.

---

## Install: drag and drop

1. Drag the theme's **folder** (or a `.aotheme` / `.zip` of it) onto the
   AsyncAO window, **or** copy it into your themes directory:
   - Windows `%APPDATA%\AsyncAO\themes\`
   - macOS `~/Library/Application Support/AsyncAO/themes/`
   - Linux `~/.local/share/AsyncAO/themes/`
2. Open **Settings → Theme** and pick it by name.

That is the whole install. Nothing to unpack by hand, nothing to configure.

Each folder holds exactly two files (three where a showcase is offered):

```
<theme>/
├─ courtroom_design.ini    the AO2 half — canvas + stage, so the folder is a
│                          valid AO2-Client theme and the sidecar has real
│                          slot keys to bind its [overrides] to
├─ asyncao_theme.ini       the AsyncAO half — the entire theme
└─ CHARACTERS.txt          how to drop your own character art in (showcase
                           themes only)
```

`asyncao_theme.ini` is documented end to end in
[`docs/THEME-FORMAT.md`](../docs/THEME-FORMAT.md). Every one of these files is
heavily commented: they are meant to be read and edited, and they double as
worked examples of the format.

---

## The fourteen

| id (folder) | name | mood | showcase |
|---|---|---|---|
| `onikakushi_dusk` | Onikakushi Dusk | A village at dusk. Violet zenith falling to gold, hills and powerlines on the horizon, a wide translucent reading band, one thin red rule. | no |
| `watanagashi_matsuri` | Watanagashi Matsuri | Festival night. Lantern gold strung over deep summer blue; festive, and not quite at ease. | no |
| `meakashi_frost` | Meakashi Frost | The cold register. Measured blues, thin rules, square corners, one indigo accent; red is reserved for damage and shouts. | **yes** |
| `matsuribayashi_dawn` | Festival Dawn | The other end of the night. Gold sky off the horizon, warm paper chrome, a shoji-lattice dialogue band. | no |
| `meta_study` | Meta Study | A candle-lit mansion study. Walnut wainscot, two sconces, a flat translucent textbox, mincho body text, red truth. | **yes** |
| `golden_land` | Golden Land | Gold-shaft light over a rose garden. Warm white and gilt, ornate hairlines, butterflies adrift. | no |
| `rokkenjima_hall` | Rokkenjima Hall | The mansion main hall as a straight-on elevation. Walnut pilasters, gold-tan frames, a crimson runner, one chandelier off the top of the frame. | no |
| `requiem_chiru` | Requiem (Chiru) | Cold near-black under thin gold. Rain, one candle, and the textbox at its most translucent — no border, no bevel, no filigree. | no |
| `purgatorio` | Purgatorio | A witches' court in red and gold. Drape wall, sigil medallions, a slowly turning magic circle, one figure centre-stage. | **yes** |
| `gadget_lab` | Cold Bench | A hand-built lab bench at 3am. Perfboard deck, cold-cathode counter, rust framing, one polaroid pinned to the stage. | **yes** |
| `amadeus_term` | Amadeus Terminal | Screen-glow cyan on near-black, a handset card holding the log, and one cold-cathode readout in amber. | no |
| `thh_trial` | Trial Floor | A monochrome courtroom crossed by magenta hairlines, over a gold spoke floor with a checker medallion. | **yes** |
| `island_trial` | Island Trial | A trial held in daylight. Bleached sand and sea, frond hairlines, a turning sun, one unbleached violet-magenta wire past every edge. | **yes** |
| `v3_dark` | V3 Dark | The coldest trial room. Near-black void, crossing neon hairlines, a checkered floor disc, and a speech box with no frame at all. | no |

**Showcase** means the theme reserves a place for a character sprite and ships
an empty, clearly-marked placeholder there. Read that theme's `CHARACTERS.txt`:
you drop in your **own** art, and nothing is bundled.

---

## Licensing and originality

**Every one of these themes ships zero bytes of third-party art, font or
audio.** There is no image file, no typeface, no sample and no trace anywhere
in this directory — the whole of what you see on screen is computed at load
from the numbers in the INI: palette colours, flat shapes, two-stop gradients,
procedural generator tiles and plain text.

- **All art is procedural and original.** Colour values and proportions are
  *measurements* expressed as arithmetic. A measurement is a fact, not a copy;
  nothing was sampled, embedded, base64'd or traced from anyone's artwork.
- **No game assets are included, referenced or required.** These themes do not
  read, ship or depend on any file from any commercial release.
- **Franchise names are used nominatively.** Where a theme's folder name or
  description evokes a work, it is descriptive reference only — to say what
  *register* the theme is in. No wordmark, logo, chapter title, character name,
  minigame name or typeface belonging to anyone else is reproduced, and no
  affiliation or endorsement is claimed or implied.
- **Fonts are bound as CLASSES**, never as files: `serif` / `mincho`,
  `rounded-gothic`, `heavy-condensed`, `mono`. A class that a platform cannot
  resolve leaves the element on its inherited face (the format's degrade rule),
  so a missing family can never break a theme.
- Licence: the themes are part of AsyncAO and are **AGPLv3**, and each file's
  `[theme] license` reads `procedural, AGPLv3-compatible`.

---

## Rendering

These render **fully from AsyncAO v1.90.0 onward**. Each file declares
`min_client = 1.90.0`, which is informational and never enforced — an older
build loads the folder and simply draws less of it (unknown sections and keys
are preserved verbatim on every round trip, so nothing is lost by opening one
in an older client). Dropped into AO2-Client, a folder is an ordinary,
perfectly valid AO2 theme that falls through to `default`.

---

## Dev note

**`themes/` should gain a parse gate once W1's sidecar parser is committed.**
Add `TestRepoThemesParse` (in `internal/theme`, walking `../../themes/*/`) that
for every folder here:

1. parses `asyncao_theme.ini` with zero errors and zero refusals — in
   particular the hard caps in `docs/THEME-FORMAT.md` § "Caps — refuse, never
   truncate", which are **refusals, not truncations**: this audit found two
   themes whose `gen_params` lists carried nine entries against the cap of
   eight, which would have made the whole file unloadable;
2. asserts `courtroom_design.ini` exists and declares both `courtroom` and
   `viewport` — the pair `applyAO2DefaultRects` gates on
   (`internal/ui/ao2defaultrects.go:48-54`). A standalone theme without it gets
   no canvas, and then every `[overrides]` rect and every anchored element is
   silently dropped;
3. asserts every `[overrides]` key is in `themeSlots` and passes
   `themeKeyEditable`;
4. asserts every `[element.*] anchor` names a real slot key, and that no rect
   resolves to a zero or negative width/height;
5. asserts `[media]` is empty in all fourteen (the originality claim above is
   testable, so test it).

Three cross-file naming questions are **deliberately left open** for W4, because
the themes here match the style-preset corpus in `docs/wip/preset-drafts/style/`
byte for byte and the two must be decided together, not drifted apart:

- `checkerdisc` writes `cells=` / `ring=` where the generator merge doc names
  the ints `pitch` / `size`;
- `plate`, `grid` and `woodgrain` write `pct=` for `Pcts[0]`, where `grid`'s
  documented extension name is `persp`;
- `[fonts]` values here are font **classes**, not filenames, and the class
  vocabulary is not yet pinned (`sans` vs `sans-serif`, `mono` vs `monospace`).
  Today every one of them degrades identically, so nothing renders wrong — but
  W4 owns the ladder and should pin the list.
