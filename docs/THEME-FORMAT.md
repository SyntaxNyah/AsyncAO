# The AsyncAO theme format

**Status: stable, format version 1.** This document is the complete, normative
specification of `asyncao_theme.ini`. It is written so a third party can
implement a reader or a writer without reading AsyncAO's source.

The one-sentence version:

> **An AsyncAO theme IS an AO2-Client theme folder plus one extra file.**
> Every AsyncAO theme is a valid AO2 theme; every AO2 theme is already an
> AsyncAO theme.

That property is not negotiable and cannot be retrofitted, which is why the
extension tier is a sibling INI and not a native container. AO2-Client only ever
opens `courtroom_design.ini`, `courtroom_fonts.ini`, `courtroom_sounds.ini`,
`courtroom_stylesheets.css` and `penalty/penalty.ini`; a folder carrying
`asyncao_theme.ini` still loads byte-identically there.

---

## 1. On-disk layout

```
themes/umineko-meta-world/
├─ courtroom_design.ini          AO2 — never written by AsyncAO in the normal path
├─ courtroom_fonts.ini           AO2 — untouched
├─ courtroom_sounds.ini          AO2 — untouched
├─ courtroom_stylesheets.css     AO2 (QSS palette) — untouched
├─ penalty/penalty.ini           AO2 — untouched
├─ chatbox.png  hold_it.png  …   AO2 art — untouched
├─ misc/butterfly/chat.png       AO2 per-character chatbox tier
├─ night/courtroom_design.ini    AO2 subtheme
├─ fonts/
│  ├─ EBGaramond-Italic.ttf
│  └─ EBGaramond-OFL.txt         font licence — travels with the face
├─ asyncao/
│  └─ media/
│     ├─ butterflies.webp
│     └─ paper.png
└─ asyncao_theme.ini             ← the only file AsyncAO writes in the normal path
```

Rules:

- **Media** lives under `<theme>/asyncao/media/` — a directory AO2 never scans.
- **Fonts** live under `<theme>/fonts/` — where AO2 themes already keep them.
- Everything else belongs to AO2 and is preserved byte-for-byte, **including
  files this client does not understand**.
- **Identity is the FOLDER NAME**, sanitized and lowercased. `[theme] name` is a
  display string only: re-shared archives get renamed constantly, and a
  manifest-name identity would make two different themes the same theme.

The transport form is `.aotheme`: a zip whose single root entry is the theme
folder, sniffed by the `PK\x03\x04` magic rather than by its extension, so a
plain `.zip` works identically.

---

## 2. Lexical rules — the INI dialect

`asyncao_theme.ini` is read with the same parser AsyncAO and AO2-Client use for
`courtroom_design.ini`: Qt's `QSettings::IniFormat` as implemented against Qt
6.5.3. A conforming implementation must match these, because themes in the wild
depend on them:

| Rule | Behaviour |
|---|---|
| Sections | `[name]`, matched case-**insensitively**; section names are folded to lowercase |
| Keys | `key = value`, split on the **first** `=`; keys are folded to lowercase and trimmed |
| Comments | `;` starts a comment **anywhere on a line** and truncates the **whole line** |
| `#` | a comment **only at the start of a line**; inside a value it is ordinary text |
| BOM | a leading UTF-8 BOM is stripped, and only from the first line |
| Repeats | a repeated key: the **last** declaration wins |
| Whitespace | leading/trailing whitespace is trimmed from keys and values |
| Encoding | UTF-8, no BOM preferred; line endings LF or CRLF, per file |

Two Qt sub-rules are knowingly **not** implemented, both measured at zero
occurrences across a ~80-pack / 517-file reference corpus: quoted values do not
suspend the comment rule, and a backslash does not escape a `;`.

### 2.1 Free text and the four escapes

Because `;` truncates a line, a value that needs one cannot be written raw.
Free-text values are therefore **percent-encoded on exactly four characters**:

| Character | Escape |
|---|---|
| `%` | `%25` |
| `;` | `%3B` |
| LF | `%0A` |
| CR | `%0D` |

**Nothing else is escaped.** A literal `%20` a human typed stays `%20`; that is
consistent, because a literal `%` is always written `%25`. Readers should accept
lowercase hex (`%3b`); writers emit uppercase.

The escaping applies to **exactly these keys, and only inside
`asyncao_theme.ini`**:

`[theme] name`, `author`, `license`, `credit`, `description`, and
`[element.*] text`.

It must **never** be applied to an AO2 root-section value: inventing an escape
AO2 does not have would break every theme authored against it.

### 2.2 Value grammars

| Type | Grammar | Notes |
|---|---|---|
| rect | `x, y, w, h` | integers; written with `", "` |
| colour | `#rrggbb`, `#rrggbbaa`, or `r, g, b[, a]` | `#` is safe: it is only a comment at line start |
| angle | `0..255` | 360/256; `64` = 90°; values wrap |
| percent | `0..100` (`amp_pct`), `10..400` (`speed_pct`) | clamped on load |
| bool | `yes` / `no` / `1` / `0` / `true` / `false` | anything else is false |
| enum | lowercase wire name | **an unknown value degrades, never fails** |
| id | `[a-z0-9_-]{1,24}` | element, pane, media and clock ids |
| list | `a, b, c` | trimmed, empty entries dropped |
| free text | percent-encoded per §2.1 | only the six keys listed there |

An unparseable integer reads as `0`, deliberately: that is `QString::toInt()`'s
behaviour, and a stricter reader would drop tuples that render fine in AO2.

### 2.3 Integer widths — normative

Integers are not all the same width, and the width is part of the grammar: a
reader must land on the same number as every other reader, and a writer must not
rewrite a value merely because it was wide.

| Keys | Width | Out of range |
|---|---|---|
| `z`, `slice`, `nine_slice` | signed 16-bit, −32768..32767 | **clamped** |
| `phase_ms`, `effect_period_ms`, `period_ms` | signed 32-bit, ±2147483647 (−2147483648 low) | **clamped** |
| `opacity`, `stroke_px`, `divider_px` | 0..255 | clamped |
| `size` | 0..256 | clamped |
| `amp_pct`, `effect_amp_pct` | 0..100 | clamped |
| `speed_pct` | 10..400 (`0` = absent) | clamped |
| `clock` | 0..15 | falls back to `0`, the shared anchor |
| `rot`, `grad_angle` | 0..255 | **wraps** (an angle is modular) |
| `revision`, `shape_tier` | unbounded | — |
| colour components in the `r, g, b[, a]` form | 0..255 | clamped |
| rect components (`rect`, and every `[overrides]` value) | whatever `QString::toInt()` gives — AO2 parity, deliberately unbounded | — |

**Clamped, never truncated or wrapped.** A 32-bit *truncation* of
`phase_ms = 3000000000` yields a negative phase — a different animation, not a
clamped one — and it does so only on hosts whose native integer is wider than 32
bits, so the same file would mean two things on two machines.

Because a clamped value is what the file already *means*, a writer must not
write the clamped number back over it: see §7's writer rules.

---

## 3. `asyncao_theme.ini` — the sections

Every section and key is **optional**. A file containing only `[theme] format`
is valid, and so is a theme with no sidecar at all.

### `[theme]`

| Key | Type | Meaning |
|---|---|---|
| `format` | int | schema major. **Readers never refuse on it** (§7). |
| `revision` | int, unbounded | the author's own bump. Informational. |
| `name` | free text | display name. Not the identity — the folder is. |
| `author` | free text | |
| `license` | free text | e.g. `CC-BY-4.0`. |
| `credit` | free text | attributions that must travel with the theme. |
| `description` | free text | |
| `min_client` | text | **informational, never enforced** — enforcing it would brick themes on downgrade. |
| `base` | text | terminal AO2 fallback theme (`default`). |
| `subtheme` | text | AO2 subtheme folder to prefer; `""` = none. |
| `layout_preset` | text | the layout preset this theme started from. |
| `style_preset` | text | the style preset this theme started from. |
| `generated_by` | text | authoring tool stamp. |

### `[overrides]` and `[rotations]`

Design-space edits laid **over** `courtroom_design.ini`, so the author's own
file stays byte-identical to what they shipped.

```ini
[overrides]
ic_chatlog  = 24, 320, 460, 260      ; key = x, y, w, h — AO2's own grammar

[rotations]
ic_chatlog = 64                      ; 360/256 angle byte
```

Only keys that already **exist** in the AO2 design and are editable are
honoured. Up to 160 entries each.

### `[palette]`

Overlays the palette derived from `courtroom_stylesheets.css`. Absent roles
inherit the stylesheet.

| Key | Role |
|---|---|
| `text` | base text |
| `panel` | window/panel background |
| `panel_hi` | raised surfaces |
| `accent` | borders and highlights |
| `danger` | error text |

### `[chrome]`

AsyncAO chrome **outside** the AO2 design canvas — the surfaces AO2 has no
concept of. `shape` (a silhouette name), `shape_tier` (int, unbounded),
`tabbar_hex` (colour).

### `[fonts]` and `[fontbind]`

```ini
[fonts]
Meta World  = EBGaramond-Italic.ttf   ; family = file, resolved under <theme>/fonts/
Truth Serif = TruthSerif.otf

[fontbind]
message  = Meta World                 ; an AO2 courtroom_fonts.ini element id
showname = Truth Serif
```

Up to 6 families and 32 bindings. `[fontbind]` keys are the AO2 font-element
ids (`showname`, `message`, `ic_chatlog`, `server_chatlog`, `music_list`,
`music_name`, `area_list`, `debug_log`, plus AsyncAO's `player_list`, `notes`,
`friends`).

A binding names a **file the theme ships**, which is the whole difference from
`courtroom_fonts.ini`: that file names a *family* and leaves finding it to the
system font database, so a theme downloaded on its own renders in the client's
own face and says nothing. A `[fontbind]` row is resolved under
`<theme>/fonts/<file>` and then `<theme>/<file>`; a path that leaves the theme
folder is refused. It **overrides** `courtroom_fonts.ini` for that element, and
the reader's own per-panel font override still wins over both. A family the
theme's `[fonts]` never declares, or a file that is not there, leaves the
element on whatever the AO2 tier gave it and is reported by name.

`bold` stays where AO2 puts it — `<id>_bold` in `courtroom_fonts.ini`. There is
no per-binding style flag in format 1.

**Dropping a `.ttf` / `.otf` on the client** does not install it: the client
names the folder it belongs in and the two keys to add. It is claimed as a font
rather than ignored so that it can never be mistaken for a dropped *theme
folder*, which would repoint your themes root at wherever the font came from.

**Font licensing:** if you bundle a face you did not make, ship its licence
beside it (`fonts/<NAME>-LICENSE.txt`). OFL specifically requires the licence to
travel with the font.

### `[media]`

```ini
[media]
butterflies = asyncao/media/butterflies.webp, 1843200, 9f2c1a4b8e0d1177
paper       = asyncao/media/paper.png, 262144, 4410ff02c9a3b510
```

`id = file, bytes, xxhash64`. The path is theme-relative with forward slashes.
The size and hash are the author's declaration, so an importer can **budget and
verify before it decodes a stranger's file**; both are optional and a bare file
name is legal. Up to 24 entries.

### `[clock.N]`

Authoring clock groups, `N` in `1..15`. **Clock 0 is the shared theme anchor and
is never declared.**

| Key | Type | Meaning |
|---|---|---|
| `speed_pct` | 10..400 | playback rate relative to the shared anchor. Absent = 100. |

### `[pane.<id>]`

Split-screen. A pane **reparents** the listed widget keys into a second rect:
each host draws **exactly once**, at its new position.

```ini
[pane.evidence]
rect       = 380, 60, 320, 400
space      = courtroom
hosts      = ic_chatlog, ms_chatlog
divider    = #c8a34a
divider_px = 2
```

Structural rules, enforced on load:

- Host sets are **disjoint**. A key claimed by a second pane is dropped from it.
- `courtroom` and `viewport` can **never** be hosted — they are the spaces every
  other rect lives inside.
- Panes do not nest.

Up to 8 panes, 8 hosts each.

**A `[pane.*]` paints nothing by itself.** `divider` and `divider_px` are only
read when an `[element.<id>]` with `kind = pane` is anchored to that pane — the
element is the thing that draws, and the pane's divider folds into that
element's `stroke` when the element declares no stroke colour of its own.

That element still has to be worth drawing. A `kind = pane` element with no
`fill`, no `fill2` **and** no `stroke` of its own is **inert**: it is skipped
before the divider fold is ever reached, so a theme that writes
`divider`/`divider_px` and leaves the element otherwise empty paints nothing at
all. Give the element a `fill` (the plate the divider frames) or its own
`stroke`, and set `stroke_px` — a stroke of zero width is zero pixels wide
whatever colour it is.

### `[element.<id>]`

A free element: a box the AO2 registry has no row for. Up to 96 per theme.

`kind` is the one key whose unknown values do **not** degrade: an element whose
`kind` a reader does not know is **skipped**, and its section is preserved
verbatim. See §7.4 — it is the single exception to the degrade rule, and a
reader that treated `kind = hologram` as an `image` would put a stranger's
element on screen as something it is not.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `kind` | enum | `image` | `image`, `shape`, `gradient`, `gen`, `text`, `pane`. **An unknown value skips the element** (§7.4). |
| `band` | enum | `backdrop` | `backdrop`, `mid`, `overlay` (see §4) |
| `z` | int16, clamped | `0` | order **within** the band; ties break on declaration order |
| `space` | enum | `courtroom` | `courtroom`, `chatbox`, `viewport`, `music_display`, `window`, `pane` |
| `anchor` | text | `""` | a widget key, or a pane id when `space = pane`; `""` = the space's origin |
| `rect` | rect | `0, 0, 0, 0` | a **signed delta** from the anchor, or absolute with no anchor — see below |
| `rot` | angle | `0` | 360/256 |
| `media` | id | `""` | `kind = image`: an id from `[media]` |
| `shape` | id | `""` | `kind = shape`: a silhouette name — see below |
| `generator` | id | `""` | `kind = gen`: a generator name (§5) |
| `gen_params` | list of `k=v` | `""` | ordered; up to 8 |
| `fit` | enum | `stretch` | `stretch`, `tile`, `contain`, `cover`, `nine` |
| `slice` | 4 × int16, clamped | `0, 0, 0, 0` | 9-slice insets in **source** px, `left, top, right, bottom` — see §5 |
| `nine_slice` | int16, clamped | — | read-only alias: one inset on all four edges |
| `fill` | colour | transparent | gradient stop A / shape fill / pane plate |
| `fill2` | colour | transparent | gradient stop B |
| `grad_angle` | angle | `0` | `0` = left→right, `64` = top→bottom |
| `grad_radial` | bool | `no` | radial instead of linear |
| `stroke` | colour | transparent | outline colour |
| `stroke_px` | 0..255 | `0` | outline width |
| `tint` | colour | absent | multiplied into `image`/`gen`; absent = no tint. `opacity` folds into its alpha |
| `opacity` | 0..255 | absent | applies to **every** kind — the colours for the procedural ones, the tint for `image`/`gen`. **`0` means absent and draws opaque** |
| `text` | free text | `""` | `kind = text`; up to 240 runes |
| `font` | text | `""` | a `[fontbind]` element id or a `[fonts]` family. **Carried, not drawn in format 1** — see below |
| `size` | 0..256 | `0` | points; `0` = inherit. **Carried, not drawn in format 1** |
| `color` | colour | absent | text colour; absent = white |
| `align` | enum | `left` | `left`, `center`, `right` |
| `clock` | 0..15 | `0` | the clock group; out of range falls back to the shared anchor |
| `phase_ms` | int32, clamped | `0` | per-element phase offset. **Free** — it costs no clock group. |
| `loop` | bool | `no` | `no` = play once, `yes` = loop |
| `effect` | enum | `none` | `none`, `fade`, `pulse`, `breathe`, `glow`, `drift`, `spin`, `shake`, `slam` — see below |
| `effect_period_ms` | int32, clamped | `0` | `0` = the default period (about a second) |
| `effect_amp_pct` | 0..100 | `0` | `0` is a supported way to author a **still** element that still names its effect |
| `decimate` | bool | `yes` | `no` is honoured only if the undecimated art fits the media budget |
| `visible_when` | condition | `always` | see below |
| `locked` | bool | `no` | editor-only: excluded from marquee and drag |
| `hidden` | bool | `no` | authored-off; still in the file |

**`rect` against an anchor is a signed delta.** With a non-empty `anchor`, the
element's box is the anchor's box plus the rect, component by component:
`X = anchor.X + rect.x`, `Y = anchor.Y + rect.y`, `W = anchor.W + rect.w`,
`H = anchor.H + rect.h`. Every component is signed, and every component is
defined independently of the other three — a partly-zero rect is not a special
case but the ordinary reading of it, so `0, 0, 0, 12` is the anchor's box twelve
pixels taller and `-6, -6, 12, 12` is the anchor's box inflated by six on every
side. All-zero is exactly the anchor's rect.

With an empty `anchor`, `rect` is **absolute in its declared space**. That is the
whole rule, and it is what lets a layout move a widget and a style decorate it
without either knowing the other exists.

**`shape` names a silhouette.** The vocabulary is the one the client already
persists for its own chrome, extended:

```
sharp | rounded | pill | hex | ribbon | tape
```

`sharp` is a plain box, `rounded` has a modest corner and `pill` is a full-radius
capsule (its corner is half the short side, which is why it is its own name and
not `rounded` with a number). `rect` is an accepted alias of `sharp`. An unknown
name draws as `sharp` and keeps its fill, stroke and position — rule 3.

**`font` and `size` are carried, not drawn in format 1.** An element's text is
rendered in the client's own chrome face at the chrome size. Both keys are
parsed, bounds-checked and written back unchanged on save — a theme that sets
them loses nothing and is forward-compatible by construction (rule 4) — but no
format-1 renderer reads them.

The reason is a cost this format will not pay for decoration. Every face in this
client lives in a set that holds exactly **one** point size, so an element drawn
at a different size from the widget it borrows its face from rebuilds that set,
and purges the text cache with it, on every frame both are on screen. Giving
elements their own face pool instead spends one of a theme's four face slots.
Measured against what it would buy: the fourteen themes shipped with this
release all write `font =`, none of them ships a face *file*, and a family that
names no file resolves to the client's chain either way — so honouring `font`
changes nothing on any theme that exists, and only `size` would.

**Type that must render exactly is `[fonts]` + `[fontbind]` territory** (above).
Those bind a *file the theme ships* to an AO2 courtroom element, they cost
nothing per frame, and they are the reason a native theme renders the same on a
machine that has never heard of the family.

**The nine effects.** Five are **periodic** — they run forever, on the element's
clock group, at `effect_period_ms` per cycle:

| `effect` | what it does |
|---|---|
| `pulse` | rides the element's alpha down to `100 − amp_pct` percent and back |
| `breathe` | scales the element about its centre, in and out |
| `glow` | swells *and* brightens back to full on one cycle |
| `drift` | walks the element around a slow ellipse |
| `spin` | turns continuously; `amp_pct` scales the **rate** (100 = one turn per period) |

`spin` turns the element's **art**, so it shows on `image` and `gen` — the two
kinds that blit a texture, and the same two the static `rot =` applies to. On
`shape`, `gradient`, `text` and `pane` the rotation resolves and there is nothing
to turn; use `drift` or `breathe` on those.

Four are **one-shot**: they play once, from the moment the element first draws,
and **decay to nothing** — the element is left exactly as it was authored:

| `effect` | what it does |
|---|---|
| `none` | the default: no motion at all |
| `fade` | fades in from `100 − amp_pct` percent alpha |
| `shake` | a decaying judder |
| `slam` | lands oversized and settles |

A one-shot restarts when its element *comes back* — so `visible_when` is also its
trigger: `visible_when = shout:objection` plus `effect = slam` is a stamp that
lands with the shout. (An explicit `trigger =` key is reserved and not
implemented; see §7.)

`amp_pct = 0` is always **still**, whatever the effect names, and a still element
never holds the frame rate up. A moving one does — deliberately, and the client's
existing frame-rate settings all still bound it.

**Reduce motion wins over every one of them.** With the client's accessibility
option on, every effect resolves neutral, every animated element holds its first
frame, and the screen is genuinely still.

`visible_when` is **one axis, one value** — deliberately not an expression
language, because anything richer needs a parser on the frame path:

```
always | speaking | pos:<name> | char:<folder> | side:<name> | shout:<name>
```

A `shout:` value may be spelled either way AO2 spells a shout: the **design key**
the rest of the file uses (`hold_it`, `objection`, `take_that`,
`custom_objection` — the same names `anchor =` takes) or the **asset stem** the
wire and the art use (`holdit`, `objection`, `takethat`, `custom`). Both resolve
to the same message.

An unknown axis leaves the element **visible**: an element a future condition
would have hidden is better shown than lost.

**Anchors may not chain.** An anchor is a widget key or a pane id, never another
element. That is what makes one bake pass enough, with no cycle detection and no
depth cap — and it is what makes decoration survive a layout change: a frame
anchored to the chatbox at `-6, -6, 12, 12` follows the chatbox wherever a
preset puts it.

**Elements are additive paint only.** They cannot move, hide or restyle an AO2
widget. The only ways to move an AO2 widget are `[overrides]` and `[pane.*]`,
both explicit.

### `[effect.<widget key>]`

An effect attached to an **existing AO2 widget** rather than to a free element.

| Key | Type |
|---|---|
| `effect` | enum, as above |
| `color` | colour — **required**; a binding without one paints nothing |
| `period_ms` | int32, clamped |
| `amp_pct` | 0..100 — the wash's **peak opacity**; required, `0` is inert |
| `clock` | 0..15 |

**A binding is additive paint, like every other element** (see the rule above):
it draws a `color` wash over the widget's own box, above everything else in the
overlay band, and the effect animates *that*. It does not move, scale, rotate or
restyle the widget itself — a widget's box is also its **click target**, and an
effect that slid its pixels away from where it can be clicked would be a bug, not
a feature. The two sanctioned ways to move an AO2 widget stay `[overrides]` and
`[pane.*]`, both explicit and both static.

**The wash's alpha is the effect itself.** A free element is on screen whether or
not its effect runs, so an effect *modulates* it; a binding's plate exists only
because of the effect, so the effect's strength **is** its opacity. `amp_pct`
sets the peak that strength reaches — so `glow` at `amp_pct = 45` breathes
between invisible and a 45 % tint and never buries the widget, and `fade` at
`amp_pct = 100` is a full-strength curtain that clears completely and stays
cleared. Write `color` as the hue you want at the peak; a bare `#rrggbb` is
correct and does not mean "opaque forever".

A binding with no colour, no amplitude, no effect, or a widget key this layout
does not place is **inert** — it is preserved on save and simply does not draw.

Up to 24 bindings. The widget key is at most **48 runes** — the same bound as
`anchor`, because it is the same kind of name. A section whose key is empty or
longer than that names no widget: it is **ignored, recorded as a note, and its
lines are preserved verbatim** like any other section the reader does not
understand.

### `[sounds]`

AsyncAO-tier sound overrides; files resolve under `<theme>/sounds/`. The AO2
`courtroom_sounds.ini` stays authoritative for AO2's own keys. Up to 16 entries.

### `[import]`

Provenance, written by convert-to-editable-copy. Never authored by hand.

| Key | Meaning |
|---|---|
| `derived_from` | the original theme's folder name |
| `derived_at` | ISO date |
| `derived_hash` | a hash of the original |
| `subthemes` | subtheme folders found in the original |
| `per_misc` | the original used per-character `misc/` scaling — recorded, not applied |
| `lobby_design` | the original shipped a `lobby_design.ini` — recorded, not applied (AsyncAO's lobby is a phone book, not AO2's lobby) |

### `[x.<vendor>.*]` — the reserved vendor namespace

Sections named `x.<vendor>.…` and keys prefixed `x_` are **reserved for third
parties and will never be assigned an official meaning**. AsyncAO preserves them
verbatim on every round trip and will never define a key of that shape. Put your
tool's private state there.

---

## 4. Bands, spaces and paint order

An element paints in exactly one of three **bands**, which bracket the client's
own hand-ordered draw pass:

| Band | Paints |
|---|---|
| `backdrop` | above the courtroom background, below every widget |
| `mid` | between the stage and the panels — **behind the chat log** |
| `overlay` | above everything, below editor chrome |

Within a band, elements paint in ascending `z`, ties broken by declaration
order. There is no global z-order across bands, because the client's pass is
hand-ordered and aborts mid-sequence when a modal is up — a flat sortable list
cannot reproduce that.

The `space` decides what an element's coordinates mean:

| Space | Origin |
|---|---|
| `courtroom` | design space, transformed by the canvas fit (the default) |
| `chatbox` | the chatbox's resolved rect |
| `viewport` | the stage |
| `music_display` | the music display plate |
| `window` | **window pixels**, outside the design canvas entirely |
| `pane` | a `[pane.*]`, named by `anchor` |

`window` is the space AO2 has no analogue for. Use it for the letterbox bars and
the chrome band — the things that live outside the canvas by design.

---

## 5. Generators

`kind = gen` rasterises a **tile**, never a field: at most 256×256, drawn tiled
or 9-sliced. That is what makes procedural decoration cost kilobytes instead of
megabytes, and what makes it survive a window resize without re-rastering — the
tile's cache key carries no width, no height and no theme, so the same generator
at any canvas size is the same texture.

Parameters are an ordered `k = v` list of at most 8 pairs; a generator ignores
what it does not use, and an unknown generator name degrades to a flat fill of
the element's own `fill`. Because the list is ordered and hashed, two elements
with identical parameters share one texture — across elements and across themes.

### The seventeen generators

| Name | Serves | Key parameters |
|---|---|---|
| `scanlines` | VHS/CRT, Steins;Gate, Cyberpunk | `pitch` period, `size` thickness, `fade` |
| `halftone` | Newspaper, Manga screentone | `dot`/`pitch` cell, `angle` (staggers the lattice) |
| `grid` | Vaporwave floor, Cyberpunk wireframe, perfboard | `pitch` cell, `size` line, `pct` perspective, `dots` |
| `hex` | Danganronpa chatbox, Cyberpunk | `pitch` cell, `size` line |
| `noise` | VHS grain, Higurashi | `density`, `amp`, `size` speck, `seed` |
| `woodgrain` | Classic Courtroom | `pitch` band period, `pct` contrast |
| `gradient` | everything | `angle`, `tint` → `accent` |
| `glow` | Cyberpunk neon, Umineko gold | `radius`, `soft` falloff |
| `stripes` | Persona 5, warning tape, hairlines | `pitch`, `size`, `angle`, `bloom`, `fade`, `phase` |
| `hatch` | Manga tone, the over-budget placeholder | `pitch`, `size`, `angle` |
| `plate` | a filled plate with a decorated edge | `size` edge, `count` mode (bevel/chamfer/capsule/notch), `pitch` groove, `pct` |
| `frame` | a hollow frame, 9-sliced | `pitch` band, `size` corner, `count` mode, `gap` |
| `radial` | rays, spikes, rings, discs | `pitch` rays, `size` radius, `count` repeats, `inner`, `squash`, `seed` |
| `mottle` | cork, newsprint, cloud — smooth low-frequency noise | `pitch` feature size, `size` octaves, `count` specks, `amp`, `fade`, `seed` |
| `checkerdisc` | a checkered disc inlay in perspective | `cells`, `ring`, `squash`, `angle` |
| `skyline` | mirrored ridge silhouettes / column bars | `pitch` segment, `size` peak, `count` ranges, `base`, `seed` |
| `wingmark` | scattered two-lobe wing / arc marks | `size`, `count`, `angle`, `amp` (lobe fullness), `seed` |

Colours are always `tint`, `bg`, `accent`, `shadow`. `checkerdisc` is the one
generator whose tile is **transparent outside its figure**, so a disc can be
inlaid over a floor rather than over a square of its own background.

### The parameter vocabulary — complete

There are **25 accepted keys** and they resolve to **11 slots**, globally: the
same key means the same slot in every generator. A generator reads the slots it
documents and ignores the rest, so `angle` on a generator with no angle costs
nothing and is not an error.

| Slot | Accepted spellings | Type |
|---|---|---|
| tint | `tint` | colour |
| bg | `bg` | colour |
| accent | `accent` | colour |
| shadow | `shadow` | colour |
| pitch | `pitch`, `cells`, `dot` | int |
| size | `size`, `radius`, `ring` | int |
| count | `count`, `bloom` | int |
| seed | `seed` | int |
| angle | `angle` | 360/256 |
| pct0 | `pct`, `inner`, `density`, `gap`, `soft`, `base`, `fade` | 0..100 |
| pct1 | `squash`, `amp`, `phase`, `dots` | 0..100 |

**The multiple spellings are deliberate and they are append-only.** Every alias
here is a word themes already write for that slot — `cells` is what a
`checkerdisc` author calls its pitch, `ring` is what they call its size,
`radius` is what a `glow` author calls the same thing. Collapsing them to one
canonical name each would have been tidier and would have silently dropped the
parameter out of every theme that used the other word, because an unrecognised
key is *ignored* rather than reported. So: **a spelling that once resolved keeps
resolving, forever**, and a new generator picks its keys out of this table
instead of inventing a twenty-sixth.

Two consequences worth stating out loud:

- **Two spellings of one slot cannot both be set.** `pct = 40, inner = 60` is
  one slot written twice; the last one wins. No generator's own documented
  vocabulary contains two spellings of the same slot, so this only arises when
  an author mixes families.
- **The hash is over the key list as written, in order.** Two elements share a
  texture only if their parameter text matches key for key, value for value, in
  the same order — `cells=6, ring=10` and `ring=10, cells=6` are two tiles that
  look identical and cost twice. Themes that mean to share a tile say so
  (`thh_trial`'s two medallions carry a comment to that effect).

Every generator is **deterministic**: the same parameters produce the same
pixels, always. That is not a nicety — the parameter hash is the texture's cache
key, so a generator that varied would make a theme's appearance depend on which
element the client happened to load first.

### 9-slice — normative

`slice = left, top, right, bottom`, in **source** pixels. `nine_slice = N` is an
accepted alias for the same inset on all four edges.

Corners are drawn at their source size. **Edge regions keep their source
thickness and stretch only along the edge axis** — the top and bottom bands keep
their height and stretch horizontally, the left and right bands keep their width
and stretch vertically — and only the centre stretches on both. That is what
lets a 1 px rule or a fixed-width bezel keep its weight at any element size.

An element smaller than its own frame scales the four bands down together rather
than overlapping them, so a 9-slice never produces negative geometry.

---

## 6. What is deliberately NOT in this format

- **A way to hide or restyle an AO2 widget.** Inside the AO2 design canvas the
  theme is pure AO2; extras go in chrome outside it.
- **Scripting, expressions or conditions with more than one axis.** Everything
  here resolves to integers before the first frame is drawn.
- **Absolute file paths, or any path outside the theme folder.** Every path is
  theme-relative, and a reader must refuse `..` segments, absolute paths and
  symlinks.
- **Anchor chains.** See §3.

---

## 7. Versioning — normative

1. **A reader NEVER refuses to load because of `format`.** A newer theme renders
   with everything the reader understands.
2. `format` greater than the reader's own means the theme is **read-only** in
   that build: it renders, and it can be duplicated for editing, but the copy is
   allowed to lose what the build cannot represent — and the user must be told
   exactly what, before the click.
3. **Unknown keys, unknown sections and `[x.*]` are preserved verbatim.** A
   conforming writer edits the file in place, line by line: it must not rebuild
   the file from its own model, or a save from an older build would silently
   delete a newer build's work — and every comment in a fifteen-year-old
   community theme with it.
4. **Unknown enum values degrade to the nearest safe primitive** (no effect,
   flat fill, `stretch`, `courtroom`, always-visible) **and are still preserved
   on save.** The element keeps drawing.
   **`kind` is the one exception**: an element whose `kind` the reader does not
   know is **skipped — nothing is drawn for it — and its whole section is
   preserved verbatim.** The other enums choose *how* a known box paints, so a
   safe primitive is a truthful answer; `kind` chooses *what the box is*, and
   there is no safe primitive for that. Drawing an unknown kind as an `image`
   would show a stranger's element as something it is not, and the theme is
   still whole the moment a newer client opens it.
5. **Keys are additive-only and are never repurposed.** Every new key's zero
   value must mean "behave as version 1 did". **The presence of a key is its
   version** — there is no minor number.
6. `min_client` is never enforced.

### What a conforming writer must do

- **Stamp `[theme] format`.** It is the one key a save always writes, so a file
  never leaves a reader guessing which grammar it is in.
- Emit a key only when the file does not **already mean** what you are writing.
  The comparison is between parsed values, not bytes, so a differently spelled
  but equivalent value stays as the author wrote it.
  **That comparison must include §2.3's range conversion and §7.4's degrade.** A
  value the reader clamped, wrapped or degraded already *means* the clamped,
  wrapped or degraded thing, so writing that result back over the author's text
  is a diff on a save that changed nothing — and the author's original is then
  gone for the newer client that could have read it.
- Rebuild nothing from the model, **section names included.** `[clock.01]` is a
  legal spelling of clock 1; a writer that re-derived the name from its own index
  would append a second `[clock.1]` beside it and the file would say the same
  thing twice.
- Preserve the file's own line endings; new files use LF. Never add a final
  newline the author did not write.
- Never emit a value the reader would not read back: no `;`, no newlines, no
  leading or trailing whitespace. Free text is percent-encoded first.
- Write tuples in AO2's exact shape, `"x, y, w, h"`.

### Caps — refuse, never truncate

| Limit | Value |
|---|---|
| file size | 1 MiB |
| lines per file | 8192 |
| elements | 96 |
| panes / hosts per pane | 8 / 8 |
| media entries | 24 |
| font families / bindings | 6 / 32 |
| overrides / rotations | 160 / 160 |
| clock groups | 15 (plus the shared anchor) |
| widget effect bindings | 24 |
| sound overrides | 16 |
| element text | 240 runes |
| generator parameters | 8 (key and value 48 runes each) |
| unknown entries carried | 512 |
| ids (element, pane, media, clock, `media`/`shape`/`generator` names) | 24 characters |
| `anchor` | 48 runes |
| `visible_when` value | 64 runes |
| every other text value (names, paths, families) | 240 runes |
| `[import] subthemes` | 16 |
| degrade notes reported | 64 |

A file past any of these is **refused, not truncated**: a silently shortened
theme that then gets saved back has destroyed the author's work.

**`degrade notes reported` is the one exception, and it is not a load bound.**
It caps the *import report*, never the file: a reader that has already collected
64 degrade notes stops recording them and **keeps loading**. A theme is never
refused because it had too many things to say about it — that is forced by §7.1
and §7.4 together, since the unknown-`kind` skip is the forward-compatibility
path and reports once per element, so 96 elements from a newer client must still
load. A conforming reader may report fewer notes than there were problems; it
may not turn the 65th problem into a refusal.

---

## 8. A minimal example

```ini
[theme]
format = 1
name   = Paper

[element.watermark]
kind  = text
band  = overlay
space = window
rect  = 12, 12, 300, 28
text  = made with AsyncAO
color = #c8a34a
```

That is a complete, valid AsyncAO theme when it sits beside a
`courtroom_design.ini`. Larger hand-authored examples ship in the source tree
under `internal/theme/testdata/themes/`.
