<!--
DRAFT. Paste-ready for internal/ui/assets/CHANGELOG.md at release time.

Covers the COMPLETE v1.90.0 arc: 34 commits, d52ce9e (W0, 2026-08-03) through
54494a0 (field batch 7, 2026-08-09). `git log origin/main..HEAD` is the source;
every bullet below is backed by one of those commits. Nothing in this release
has been seen on a screen. The hands-on checklist is
docs/wip/LIVE-VERIFY-v1.90.0.md.

FOUR THINGS TO DO BEFORE THIS SHIPS:
  1. Set the date in the "## v1.90.0" header (it reads 2026-08-XX here).
  2. PLACEMENT. release.yml extracts with `index($0, "## " ver) == 1`, i.e. a
     PREFIX match, so the stable "## v1.90.0" section must sit ABOVE any
     "## v1.90.0-testN" sibling, directly under the file header and above
     "## v1.89.1". autotag.yml reads field 2 of the first `^## v` line, so the
     first token after "## " must be exactly the tag.
  3. THE ONE EM DASH. The style rule for these notes is no em dashes, and the
     body has none. The header line keeps the U+2014 separator between the tag
     and the date because that is the format every other section in the shipped
     file uses and it is structure, not prose. Neither extractor needs it, so
     delete it if you would rather the file have none at all.
  4. Re-check the live-derived numbers if anything lands after 54494a0. Three
     move with ordinary edits:
       - "294 combinations" = 21 files under internal/theme/presets/layout/
         times 14 under .../style/. The Gallery header builds its own label
         from lib.Combinations(), so the client cannot go stale; this file can.
       - "38 fields" = editFieldCount-1 in internal/ui/themeeditordoc.go.
       - "49 files" = 14 themes/<id>/asyncao_theme.ini plus the 35 fragments.

PACKAGING (owner decision, 2026-08-05, BUILT): the fourteen themes stay in the
repository and are deliberately NOT staged into release assets. Cloning the
repo or GitHub's Code -> Download ZIP is the distribution. The client's pointer
is the "Get themes" row in Settings -> Theme (landed 97f6cf0), which opens the
repository through the About screen's own aboutRepoURL. The bullets below name
that row. No release-asset staging is owed.

CREDIT RULE (standing): the KFO batch, field batches 5 to 7 and the blip fixes
came from live play. The commit log is NOT name-free: three bodies in the arc
(0fc8f38, b44b186, ae7d530) open with "Field reports from Northgate and a live
session". The credit line below is the reader-facing record, not the only one.
What the rule actually forbids still holds everywhere: never quote or paraphrase
playtest conversation, describe the symptom only, and credit by name only.

VOCABULARY (standing): the three-dot button is an "overflow menu". Never the
other word for it, not here and not in a commit message.
-->

## v1.90.0 — 2026-08-XX

AsyncAO gets a **theme editor**. Start a theme from blank, from a template, or
from a copy of the one you are using. Drag the courtroom around at full window
size, drop in your own pictures and animated WebPs, and pick from a gallery of
**294 layout and style combinations**. Save the result as a **`.aotheme`** file
and hand it to a friend. **Fourteen finished themes** ship in the repository,
free to drop in.

An imported AO2 theme keeps working exactly as it did. Everything new here is
opt-in, in files AO2 does not read.

Thanks to Crystalwarrior and Northgate for the field reports.

### Theme editor

- **The editor has its own Settings tab.** It sits directly under Theme, so you
  no longer have to scroll the Theme tab to find it.
- **It opens on the theme you are using, at full window size.** The canvas is
  the real courtroom, not a preview of one.
- **Two translucent rails float over the frame.** Hold Space and they fade out
  of the way.
- **Start a new theme three ways: blank, from a layout and style pair, or as a
  copy of the theme you have on.** It lands in your themes folder and applies
  straight away.
- **A theme that is not yours to write offers to copy itself first.** The button
  copies it into your themes folder and opens the copy, instead of just telling
  you to.
- **Everything drags.** Widgets and a theme's own decorations both, with eight
  resize handles, a magnet that snaps edges flush, and arrow-key nudge (Ctrl
  jumps to the next grid line).
- **Multi-select moves as one group.** Decorations select ahead of the widgets
  under them, so a 12 px badge is never swallowed by the emote grid it sits on.
- **One drag is one undo.** Ctrl+Z, Ctrl+Y and Ctrl+Shift+Z all answer, and the
  ring holds 128 steps.
- **The right rail is an inspector with 38 fields**, grouped into Transform,
  Fill, Text, Motion and Behaviour. Which rows appear depends on what you
  selected.
- **The generator and shape rows list what this build has and still accept a
  typed name.** Generator parameters are checked by the reader's own parser, so
  the rail cannot write a row the client would drop.
- **Rename an element from the Id row.** The section in the file is renamed in
  place, comments intact, instead of a second copy being appended.
- **An AO2 widget's rail is geometry and a Reset to theme button.** Reset
  removes the override row rather than restating it.
- **Dragging a widget writes a row in the theme's file, never a preference.**
  Undoing the first drag removes the row it created.
- **Drop a picture on the window with the editor open.** PNG, GIF, APNG and
  animated WebP all work, and the copy, the declaration and the element that
  draws it are one undo step.
- **The rail has a + Image button for the same thing.** It opens the in-app file
  browser.
- **Putting your own art in a theme no longer needs a hand edit.** Add the image
  in the editor, then point an element at it from the Media row.
- **Fonts have a panel.** Drop a `.ttf` to install it into the theme, bind it to
  a text class, and the courtroom's text really changes face.
- **Freeze the live preview when you want the picture to stop.** The document
  stays fully editable, undoable and saveable while it is frozen.
- **A save cannot produce a file the client would refuse.** Every save runs the
  reader's checks first and a refusal names the offender.
- **Saving a layout nudge no longer restarts the theme's animation.** Only an
  edit that changes which fonts resolve re-applies the theme.
- **Back on a dirty document arms a chip and a second press discards.** You land
  on your last save, not on the state the editor opened at.
- **A delete sticks through a save, and a discarded delete restores the file
  byte for byte.**
- **Unsaved edits survive a background theme reload.** A texture eviction
  re-applies the theme, and that used to be able to pull the rug.
- **Edits show on the same frame.** Changing a generator's parameters, swapping
  an element's kind, adding or deleting one: the canvas redraws from the edited
  model as you work.
- **A modal over the editor gets the keys.** Delete, H, L and the arrows no
  longer reach the element underneath an open file browser.
- **A closed editor costs nothing.** If you never open it, nothing about the
  client gets slower.

### Themes and presets

- **Fourteen complete themes ship in the repository.** Four Higurashi arc moods,
  five Umineko registers, two Steins;Gate, three Danganronpa trials.
- **They are not inside the client.** Settings -> Theme -> **Get themes** names
  the repository's `themes/` folder, the clone route and the Code -> Download
  ZIP route, with an **Open the repo** button.
- **Installing one is dragging it.** Drop the folder on the window, or copy it
  into your themes folder, then pick it by name in Settings -> Theme.
- **Their layouts apply now.** Every one of the fourteen puts its arrangement in
  the theme file, and nothing read it, so all fourteen drew AO2's stock
  courtroom with their decoration anchored to stock positions.
- **Thirteen of the fourteen dress their buttons.** Chips, bezels, keylines and
  plaques anchored to the controls themselves, instead of franchise scenery over
  a stock control deck.
- **Every theme places the screen-effect picker itself.** At AO2's stock spot it
  sat inside one theme's chat log and inside four more themes' chat boxes.
- **A theme folder is a few text files and a few dozen kilobytes.** Every
  colour, shape, gradient and tile is computed from numbers at load.
- **Gallery, in the editor header: 294 combinations, 21 layouts by 14 styles.**
  Pick a row on each axis and either build a new theme from the pair or re-style
  the theme you have open.
- **One undo puts a re-style back exactly.** A layout owns geometry and a style
  owns paint and type, so no key is ever claimed twice and every pair merges
  cleanly.
- **Each style names its author** beside the pair it would build, before you
  press anything.
- **Six of the layouts use panes.** A pane is a plate that holds a widget inside
  it.
- **A theme can draw its own boxes, gradients and text.** Up to 96 free
  elements: shapes with outlines, two-stop gradients, plain text and panes, none
  of which collide with AO2's own rects.
- **A theme can ship its own images**, up to 24, under a hard byte budget taken
  out of the texture tier. Five fits are available: stretch, tile, contain,
  cover and 9-slice.
- **Seventeen procedural generators** cover scanlines, halftone, grid, hex,
  noise, woodgrain, gradient, glow, stripes, hatch, plate, frame, radial,
  mottle, checkerdisc, skyline and wingmark.
- **Elements can be pinned to the layout.** Name a part of the theme and the
  element is measured from there, so it moves and scales with what it decorates.
- **Three layers, and conditions.** An element draws behind the stage, over the
  stage, or over everything the theme draws, and can be set to appear only in
  certain states such as while a shout is up.
- **Eight element effects.** `pulse`, `breathe`, `glow`, `drift` and `spin` run
  continuously; `fade`, `shake` and `slam` play once and settle back.
- **A theme can lay a glow or a fade over its own parts.** Name the objection
  button, the chat box or the stage; it is paint only, so nothing moves away
  from where you click.
- **Motion is grouped.** A theme declares up to fifteen clock groups and every
  element picks one, so a whole register of ambient motion speeds up or slows
  down together.
- **Reduce motion turns all of it off**, including the theme's own chrome, which
  used to keep animating over frozen elements.
- **A theme that uses none of this costs nothing.** A plain AO2 theme runs
  exactly as fast as it did before.
- **A theme file this build cannot read never breaks the theme.** Anything
  written by a newer AsyncAO is preserved untouched, and a file that will not
  parse leaves you a working AO2 theme with a chip that says so.
- **Editing a theme file keeps your file.** Comments, key spelling and unknown
  sections survive byte for byte, so a save that changes one value changes
  exactly one value.
- **All 49 shipped theme and preset files got readable comments.** They are 75%
  shorter and say what each block does, and no key or value changed.
- **AsyncAO has a themes folder of its own.** `%APPDATA%\AsyncAO\themes\` on
  Windows, `~/Library/Application Support/AsyncAO/themes/` on macOS,
  `~/.config/AsyncAO/themes/` on Linux. A portable install keeps them beside the
  program instead.
- **Settings -> Theme shows that path, with Open folder and Refresh.** If you
  already had a theme folder set, nothing about where your themes resolve from
  has changed.
- **A badge says where the current theme came from and whether it is yours to
  edit**, with its folder beside it.
- **Subthemes work.** An AO2 theme can ship variants in subfolders, and there is
  a dropdown for them with a Clear button when a pick belongs to a theme you are
  no longer using.
- **A subtheme's own fonts resolve.** They used to fall out of every scan.
- **Dropping a file on the window can no longer repoint your theme folder.** A
  dropped `.aotheme` used to silently point it at your Downloads folder, after
  which themes that used to resolve stopped resolving.
- **Make portable takes your themes with it**, and a partial move is reported as
  a partial move instead of a blanket failure.
- **Theme list fixes.** One folder spelled two ways is listed once, the dropdown
  rescans when you open it instead of polling while it is held open, and the
  subtheme row names a pick from another theme instead of reading "(none)".

### Courtroom fixes

- **Preanims stop going missing at random.** A preanim finishing in one room
  could mark the next message in another as already finished, and the next line
  then lost its preanim three different ways.
- **The first frame of a message waits for the talking sprite.** A line with no
  preanim opens on talk, not idle, and only idle was ever waited for, so you saw
  a placeholder.
- **An Immediate post no longer holds the queue.** Its animation plays over the
  line instead of stalling every message behind it.
- **A position with no background of its own falls back to the witness view
  instead of black.** Changing area re-checks it.
- **The server timer keeps time with the server's.** The client started it half
  a round trip late and stayed there, which on a distant server reads as drift.
- **Chat box lines sit at AO2's spacing.** Rows were stacked on the glyph box
  and are now stacked on the font's own line spacing.
- **A theme-bound font gets Qt's row pitch.** The shipped DangitV3IC face laid
  out at 24 px here against AO2's 35, same font, same size.
- **The shout bubble stops stretching.** It scales by height and centres, the
  way AO2 does, instead of being pulled across the whole stage.
- **Narration posts lose the empty name plate.** A message with no showname takes
  the plate-less chat box art, which is AO2's own per-message choice.
- **The screen effects a theme declares actually draw.** You used to hear the
  sound and see nothing; all four layers work now, from behind the characters to
  over the chat box.
- **There is an Effects dropdown on the IC bar**, on the classic layout and on a
  theme's own, with each effect's icon at the size the theme asks for.
- **Right-click a row to preview it** without picking it. The art goes in the
  preview box and the sound through your speakers.
- **Screen effects and their sound fire when the text starts.** They were landing
  a whole preanim early.
- **An effect no longer crawls at the idle frame rate.** A parked event loop had
  no idea the stage was moving, so an effect ran at 10 fps or, with the idle tick
  off, not at all.
- **Effects reach the pinned split pane and comic and video exports.** Both
  surfaces silently left them out.
- **With effects off, a replay stops paying for art it will not draw.**
- **Effect names with spaces work**, including the sounds nested under them.
- **A character in a nested folder works.** A character whose name contains a
  slash had it escaped into the folder name, so its sprites and its `char.ini`
  asked for a path no server has.
- **A missing file in a local pack is looked for once.** The courtroom re-asks
  for the desk on every message, so one absent overlay used to re-walk your
  folders every few seconds all session.
- **Switching tabs after a second mount change no longer strands assets.** A
  parked tab held URLs that nothing would answer.
- **The debug panel folds repeats even when two chatty sources alternate.** A
  flood of them used to push real one-off failures out of the ring.
- **A missing asset is one bounded chip in the menu bar band.** Repeats do not
  stack, and F8 keeps the full detail.
- **Emoji rows stay inside the log panel under an AO2 theme.** They were the only
  rows that ever escaped the clip.

### Input and UI

- **The chat box keeps the keyboard.** Picking an emote, a colour, a sound or a
  toggle hands focus straight back, the way AO2 does after every step of writing
  a line.
- **Start typing with nothing focused and it goes into the IC box.** From the
  first keystroke, not the second.
- **Bare letter shortcuts need Alt now.** Eight of them read plain letters, which
  is exactly why typing anywhere could not work; every chip, label and the
  hotkey sheet were re-spelled to match.
- **Ctrl+Delete, Ctrl+Left and Ctrl+Right work in text boxes**, with Shift to
  extend the selection. Ctrl+Backspace already did.
- **Clicking into a long line no longer slides the text under your pointer.** The
  view scrolls only when the caret would leave it.
- **Typing no longer runs under the character counter.** The field stops short of
  the counter and the muted chip instead of drawing beneath them.
- **The emote grid scrolls one row per wheel notch** instead of jumping a whole
  page. The arrows still page, as AO2's do.
- **Esc opens the Extras box instead of leaving the server.** Disconnect is still
  one click away inside it, behind the same confirm.
- **The Areas view has an overflow menu.** It carries Change the background,
  Refresh the area list, the per-area chat log toggle, and a route to the CM
  controls.
- **The Realization button arms the effect too.** It set the wire flag only, so
  nothing on screen said it was on and no effect went out with it.
- **A theme that declares a `player_list` rect gets the roster drawn there.** A
  theme without one keeps sharing the music list, exactly as before.
- **Not-yet-spoken log lines are drawn faint.** The log gets a message when it
  arrives, so every line used to be readable before it was said.
- **The IC log jumps to a new message instantly again.** Your own wheel and
  scrollbar keep their smoothing.
- **The Players panel header is one row:** Sort, Rooms, Status and an overflow
  menu. The live chip, the refresh button, the raw command row, the legacy tick,
  the Pairs and Follow row and the "12 here" readout are gone from the strip.
- **Everything that did something moved into the overflow menu**, reachable from
  whichever of the three views the panel is showing.
- **Refreshing the roster sends a command your server family answers.** The one
  spelling AsyncAO used came back as "unknown command" on some of them.
- **The roster no longer polls in the background.** On some old hubs that poll
  pasted a refusal into OOC every three seconds; the cost is that a server with
  no live player list now refreshes only when you ask.
- **The themed roster chip says where it takes you** instead of reading "Player
  List" in both states.
- **The Areas view inside a theme's canvas uses the theme's own search box**
  instead of drawing a second one beside it.
- **The music panel header lost its clutter.** Loop and the fade toggles fold
  into the overflow menu, expand, collapse and random became icon buttons with
  tooltips, and the volume view took back the search row.
- **A theme that squeezes the music panel cannot stack its header.** It shrinks,
  then wraps, then drops controls in a stated order, and the overflow menu is
  never the one dropped.
- **The stray stop-music button** moved from the player list's corner into the
  music controls.
- **Iniswap has a permanent button.** It sits in the menu bar band above every
  theme's canvas, and it drops whole rather than overlapping when the bar runs
  out of room.
- **The Wardrobe can browse your base.** It lists every character folder the
  server offers, taken or not, and every character folder in your local mounts,
  both searchable and wearable by clicking.
- **A plain web base says why it cannot be listed.** The type-a-name field stays
  and now carries the reason.
- **The sprite preview pins.** Latched, draggable, remembered across a restart,
  with its pin and close controls drawn above the art.
- **Tooltips are exclusive and clamped.** One tip at a time, inside the window,
  instead of three labels stacked over the buttons they describe.
- **Text stays crisp past the auto-scale step.** One pixel wider than 1344x840
  the UI goes to 105%, and glyphs now re-rasterise at that scale.
- **Bold names stopped smearing.** They were drawn twice, one pixel apart, which
  at a scaled window is a visible double image.
- **Settings rows end in an ellipsis instead of running off the card.** A
  shortened checkbox keeps its full sentence in a tooltip and search still finds
  the full text.
- **Menus draw over the F3 performance graph and the debug overlay.**
- **Two tabs on one server write two chat log files.** A backgrounded tab keeps
  appending to its own, and a long session with many reconnects no longer stops
  logging partway through.
- **Hearts, sparkles and stars stop drawing as boxes.** Symbol runes that are not
  emoji were never offered to the colour fallback, and now are, but only when no
  installed text font covers them.
- **A new creator easter egg.** Say the right name in IC and see.

### Sound and music

- **Blips no longer bunch up after a stall.** One blip per letter, like AO2, so
  a frame hitch makes the crawl late instead of chunky.
- **The first character of a message blips**, spaces stay silent but still shift
  the rhythm, and a line break does neither. That is AO2's exact phase.
- **Messages type out at AO2's speed.** The default was 18 ms per letter against
  AO2's 40, and the crawl is also the blip clock, so blips fired at over twice
  the rate of every other client.
- **Characters that blip on every other client blip here now.** AO2 resolves a
  blip name to up to three files and AsyncAO only ever asked for one.
- **A blip set named with capitals works on a case-sensitive mirror.** Both the
  lowercase and the authored spelling are probed.
- **A char.ini's old `gender =` key reaches the same ladder**, so content written
  against `sfx-blipmale.wav` sounds again.
- **A base that already worked pays nothing new.** The extra spellings are only
  tried after the first choice misses, and misses are cached.
- **The Studio content report and the offline pack downloader agree with the live
  client** about which blip files exist and where.
- **The emote's own sound only goes out when Pre is ticked.** A sound you picked
  by hand still goes out either way, which is AO2's rule.
- **A plain idle message no longer plays an emote sound on arrival.** AO2 starts
  that sound from the preanim, so an idle line never had one.
- **Right-click an SFX row to hear it** without arming it for your next message.
- **The now-playing title scrolls instead of being cut.** AO2 marquees it, and
  the marquee is measured at the weight it draws at, so a bold title fits.
- **A shortened title no longer ends in a box.** The ellipsis falls back to "..."
  and then to nothing at all when the theme's own font has no glyph for it.
- **A character's own realization sound is honoured**, and effects other people
  send draw for you.
- **The picture and the sound are governed separately**, matching AO2: turning
  screen effects off or Reduce motion on suppresses the visual and the sound
  still plays.

### Settings and defaults

- **Desks follow the server's format list by default.** Desks were the one image
  class that ignored the server manifest, so a player whose desks went missing
  had to find Settings -> Formats and untick a box.
- **Your existing setting changes once, on the first launch of this build.**
  Re-tick "Always use WebP for desks" afterwards and it stays ticked.
- **Chat logs save to disk by default.**
- **This one also changes your setting only once.** Turn it back off and it
  stays off.
- **The 40 ms text crawl reaches existing installs once too.** A speed you
  picked yourself is kept.
- **New: Settings -> Chat -> Smooth log scrolling**, off by default. It brings
  the old glide back.
- **New: Settings -> Chat -> Ghost text**, on by default. It draws the log lines
  that have not been said yet.
- **New: three AO2 options for whether your sound, effect and preanim picks
  survive a send.** All three are on out of the box, which is what AO2 does, so
  nothing changes until you turn one off.
- **New: play a hand-picked sound on an idle line.** Off by default, matching
  AO2's own `sfx_on_idle`.
- **New: keep the full frame rate while the window is not focused.** Off by
  default.
- **The Theme editor tab took the Import row with it.** Searching for editor
  terms lands on the new tab, and the Theme tab carries a link across to it.

### Sharing

- **Share, in the editor header, bundles your theme into a `.aotheme` file.** It
  lands in a `shared` folder inside your themes folder.
- **A `.aotheme` is the theme folder, zipped.** There is no lossy step, so export
  then import gives back the identical theme, proven over all fourteen shipped
  ones.
- **Shift+Share exports a bundle AO2-Client can read.** Your layout edits are
  baked into a copy of `courtroom_design.ini`, and `ASYNCAO-LOST.txt` inside the
  bundle names every element AO2 will not draw.
- **Neither export touches your own `courtroom_design.ini` on disk.**
- **Importing takes two drops.** The first reads the bundle without unpacking
  anything and tells you the name, file count, both sizes and every font it
  carries; the second, within 30 seconds, installs it and applies it.
- **Settings -> Theme editor -> Import is the same funnel** for a file you would
  rather browse to. That row also accepts a plain `.zip`.
- **Nothing you have is overwritten.** A name clash lands as `<name> (2)` and
  says so.
- **Hostile bundles are refused by name before a byte lands.** Too many entries
  (512), too big (192 MiB unpacked, 64 MiB per file), path tricks, and two
  spellings of one file differing only in case.
- **A theme file that will not parse is a warning, not a refusal.** The bundle
  still installs as a plain AO2 theme and the reason reaches you.
- **An overlong credit line is truncated with a note** instead of costing a
  stranger their whole theme.
- **What an import could not apply is on screen.** The editor rail says how many
  notes came with it and opens the full list: skipped elements, fonts with no
  file, rect keys no widget reads.

### For server owners

- **KFO-Server implements the 2.11 live player list.** Its row in the server
  owners table says so now, and AsyncAO picks the roster up with nothing to
  configure.

### Not yet, on purpose

- **An element's own font and size are saved but not drawn.** The inspector says
  so on any element that sets them and points at the theme-wide font binding that
  does render.
- **Widget rotation is saved but not applied yet.** A widget's box is also its
  click target, so turning one has to wait; a free element's own rotation works
  today.
- **Presented evidence still hides after four seconds.** AO2 clears it when the
  next message arrives, and that fix is coming.
- **"Fade out previous" music still cuts instead of fading.** SDL_mixer holds one
  stream where AO2's audio backend holds two.
- **HP bar effects and per-pack effect scaling are not wired.**
- **A character's own chat box skin does not override the theme's yet.**
- **Editor conveniences still owed:** marquee select and Ctrl+A, the style
  clipboard, Alt+drag and Ctrl+D duplicate, autosave, hover cross-highlight, a
  snap readout, and a thumbnail strip in the image picker.

<!-- STILL OPEN AT RELEASE TIME:
  - the date in the header, and the placement rule at the top of this file.
  - the credit line is written (Crystalwarrior and Northgate, under the intro).
    It is not the only record: 0fc8f38, b44b186 and ae7d530 name Northgate in
    their commit bodies. No playtest conversation is quoted anywhere.
  - nothing in this release has been verified on a screen. The live pass
    (docs/wip/LIVE-VERIFY-v1.90.0.md) may change what these entries say.
-->
