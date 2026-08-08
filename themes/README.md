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

1. Drag the theme's **folder** onto the AsyncAO window, **or** copy it into your
   themes directory:
   - Windows `%APPDATA%\AsyncAO\themes\`
   - macOS `~/Library/Application Support/AsyncAO/themes/`
   - Linux `$XDG_CONFIG_HOME/AsyncAO/themes/`, or `~/.config/AsyncAO/themes/`
     when that variable is unset
   - Portable install (unzipped beside the executable): `themes/` next to
     `asyncao` itself
2. Open **Settings → Theme** and pick it by name.

The client always shows the exact folder it is using — **Settings → Theme → Your
themes**, with an **Open folder** button beside it. That is the one it writes
into, and it is the one to copy into if the list above is ambiguous on your
machine.

**A packed theme installs from the drop too.** Drag a `.aotheme` bundle onto the
window and the client reads it — without unpacking anything — and tells you what
is inside: the theme's name, how many files, how big it is packed and unpacked,
and every font it carries along with whether that font's licence travels with it.
**Drop it a second time to import**, and the theme unpacks into your themes
folder and applies immediately. Nothing is written to disk until that second
drop, and a bundle that is too big, too deep, trying to write outside its own
folder, or naming one file twice in two spellings that differ only in case is
refused by name before a single byte lands. That last one matters more than it
sounds: two such entries are one file on Windows and macOS, so what the client
told you about the theme's author and licence would not be what ended up
installed.

A plain `.zip` is not *claimed* by the window (so it cannot steal a drop from
something else), but the importer never trusts a file extension anyway: if you
rename it to `.aotheme` it imports identically, whether it was zipped as a folder
or as the loose files inside one. Unzipping by hand and dropping the resulting
**folder** still works exactly as it always did.

**Two themes with the same folder name are two themes.** A second import of a
name you already have lands as `<name> (2)` and says so — nothing you already
installed is ever overwritten.

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

## What the tests guard

Every folder here is walked by the suite on each run, so the claims above are
measured rather than promised.

`TestRepoThemesParse` (`internal/theme/repothemes_test.go`) asserts, per theme:

1. `asyncao_theme.ini` parses with zero errors, zero refusals, **zero degrade
   notes and zero unknown entries** — including the hard caps in
   `docs/THEME-FORMAT.md` § "Caps — refuse, never truncate", which are
   **refusals, not truncations**: a file over a cap is rejected outright rather
   than silently trimmed;
2. `courtroom_design.ini` exists and declares both `courtroom` and `viewport` —
   the pair `applyAO2DefaultRects` gates on. A standalone theme without it gets
   no canvas, and then every `[overrides]` rect and every anchored element is
   silently dropped;
3. `[media]` is empty in all of them, so the originality claim above is not a
   promise but a test;
4. **every file survives its own writer byte-identically**. These are the only
   INIs in the tree a human typed, so they are the only round-trip corpus our
   own writer cannot make self-fulfilling.

`TestRepoThemesRoundTripThroughTheBundle` and
`TestNoRepoThemeBundlesAFontWithoutItsLicence`
(`internal/themepack/repothemes_test.go`) are the transport half: each folder is
bundled into a `.aotheme`, extracted again and compared file for file, and no
bundle may carry a font without its licence. That is the drag-a-bundle promise
at the top of this file, measured on real themes.

The shipped-vocabulary gates in `internal/ui/themeshippedvocab_test.go` read
these fourteen files directly: every `shape`, `visible_when`, clock and effect
name they use has to resolve against the live tables, and none of them may emit
a degrade note.

## Naming still settling

These themes and the style presets in `internal/theme/presets/style/` share one
generator vocabulary, so the two get renamed together rather than drifting
apart:

- `checkerdisc` writes `cells=` / `ring=` where the generator tables name the
  ints `pitch` / `size`;
- `plate`, `grid` and `woodgrain` write `pct=` for `Pcts[0]`, where `grid`'s
  documented extension name is `persp`;
- `[fonts]` values here are font **classes**, not filenames, and the class
  vocabulary is not pinned yet (`sans` vs `sans-serif`, `mono` vs `monospace`).
  Every one of them degrades identically today, so nothing renders wrong.
