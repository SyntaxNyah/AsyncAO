# ROADMAP — requested features

Playtest-driven backlog (Skrapegropen / Discord). Newest requests at the top of
each section. This is the single place every ask is captured so nothing is lost;
items move to `docs/FEATURES.md` as they ship.

**Standing constraints (every item):**
- **Zero performance degradation** — nothing added may cost the live render loop;
  `BenchmarkRenderFrame` must stay at **0 allocs/op**. New work lives off the hot
  path (settings, popups, overlays, off-thread I/O).
- Local commits only (never pushed); `go test -race -p 1 ./...` green before each
  commit; document every shipped item in `docs/FEATURES.md`.

---

## Planned

- **Bake character icons at the theme's cell size (#20 follow-up).** The themed
  char-select grid draws cells at AO2's `button_width` scaled by the canvas
  factor, but the icon TEXTURE is still decoded once at 64 px and scaled on copy
  into whatever cell the theme asks for. That is what AO2 itself does —
  `AOCharButton::setCharacter` applies the icon as a `border-image: … stretch
  stretch` into the 60x60 button — so it is parity, not a shortcut, and on the
  shipped Native 1:1 default the cell is 60 px against a 64 px bake, i.e. a
  6% downscale nobody can see. It only becomes visible on a theme whose canvas is
  upscaled well past 1:1 (Crop, or Custom zoom). Making the bake size
  theme-driven is NOT a small change: the decode size is not part of the cache
  key, `prefetchChain` bails on a previously failed decode so icons that failed
  once would never re-decode at the new size, a mass re-fetch on a
  several-thousand-character roster is a disk-tier burst, and the same textures
  feed at least nine other draw sites — several at 18–22 px. It needs the size in
  the key and a per-size page, not a global flip.
- **A zero-allocation gate for the character-select screen (#20 follow-up).**
  Every other whole-screen draw has one; this screen cannot pass one yet. The
  grid layout itself is now alloc-free (`charSelectGridPlan`, `cellAt`,
  `charHoverID` and the cached heading are all gated), but `drawCharCell` still
  rebuilds each visible character's icon URL from scratch every frame, and that
  URL depends on the session's asset origin, so caching it means invalidating on
  an origin change as well as on a roster change. Worth doing — it is the last
  per-cell allocation on the screen — but it is a cache with a second
  invalidation source, not a one-line fix.
- **Categories in the character list (#20 follow-up).** AO2 groups its
  char-select name list by category, and the category is read from each
  character's own `char.ini` (`get_category`), not from the wire. A streaming
  client would have to fetch one INI per character to build that grouping — on a
  four-thousand-character server that is four thousand requests to draw a list.
  It needs a server-side or manifest-side source before it can be built.
- **Character passwords (`char_password`, #20 follow-up).** AO2's char select has
  a password box and sends its contents in a `PW` packet immediately before the
  character-pick packet. AsyncAO implements no `PW` anywhere, so the themed
  char-select screen deliberately leaves that theme rect empty: a password box
  that collects a secret and drops it would be worse than none. The rect itself is
  parsed and resolved, so the field can appear the day the packet exists. Its
  sibling `char_passworded` is a filter checkbox that stock AO2 itself leaves
  dead — the filter branch is commented out with a note that character
  passwording is unimplemented — so it is omitted for the same reason and would
  only become meaningful alongside real `PW` support.
- **Showname / chatbox text fitting inside the theme's rects (#25 sibling).**
  Reported alongside the log-padding issue: showname and chatbox text does not sit
  inside its theme rect the way it does in AO2. It is NOT a margin declared
  anywhere: a corpus-wide
  grep finds `padding` exactly once per theme file and it is always an unrelated
  clock rule, so nothing in a theme asks for one. What is genuinely missing is
  four separate pieces of AO2 behaviour, none of which shipped with the themed-log
  inset:
  - ~~**Text-to-box SCALE PARITY.**~~ **SHIPPED** for the chatbox pair
    (`chatboxfit.go`, `foldCanvasFontPct`), on top of the structural half the
    `ThemeFitNative` default already delivered. AO2 multiplies design rects AND
    `courtroom_fonts.ini` point sizes by the same `themeScalingFactor`, so
    `rect width ÷ font size` is a theme-authored invariant; AsyncAO scaled the
    rect by window÷design and never scaled the font, so the capacity ratio moved
    by exactly that factor (below 1.0 the message overruns its rect and reads as
    "no margins"; above it the text floats in a huge box). Measured over the
    corpus at 1152x864 Letterbox — the shipped window on the mode that keeps a
    theme's aspect — capacity against AO2 ran **0.37x … 3.38x, a 9x spread, with
    19 of 72 themes inside 10%**; folding `emoteCellScale(scaleX, scaleY)` into
    the resolved element percent takes that to **1.00x … 1.14x with 65 of 72
    inside 10%**. The residue is `themeFontPct`'s point-size-to-pixel truncation
    (a declared 10 pt resolves to 83%, which opens a 9 px face), which predates
    this and is not the fold's to fix. Three things worth knowing before touching
    it:
    - At **Native** on a window at least as large as the canvas the factor is
      exactly 1.0 and the fold is a literal identity return, so the default
      chatbox is byte-identical to the pre-fold one. Everything below is the
      customization path.
    - The theme's DECLARED percent is clamped first (`themeFontMinPct` /
      `themeFontMaxPct`; `microsoft surface 4k webao` declares `message = 50`,
      i.e. 416%, and clips to 400% before any canvas is involved), then the canvas
      multiplies OUTSIDE that clamp, then the product hits its own
      `themedCanvasFontMaxPct` (1600% = 192 px). That cap never binds in an
      aspect-preserving mode on any corpus theme at any real window — the corpus
      maxima are 844% (Letterbox) and 1500% (Crop) at 3840x2160 — it exists for
      `ThemeFitCustom`, whose manual zoom would take the 256x256 theme past 2500%.
    - Under **Stretch** the fold takes the tighter axis (`emoteCellScale`), so a
      badly stretched window under-fills its message rect by up to 3.8x. That is
      the same trade Stretch already makes for the emote grid, in the one mode
      that by definition abandons the theme's aspect.
  - **Fold the canvas scale into the LOG / LIST fonts too.** *Open, deliberately
    not done in the pass that shipped the chatbox fold.* The IC log, the server
    log, the music list, the area list and the music name sit in window-absolute
    scaled rects and have exactly the same parity problem — AO2 scales their point
    sizes by the same factor. They were left out because each also carries the
    user's own per-panel Ctrl+wheel zoom and re-wraps through caches keyed on it,
    so folding them is a larger visible change than the chatbox pair, and because
    the panel that changes size mid-drag would re-wrap AND re-raster every log
    line rather than one message. The machinery is already there when it is wanted
    — `foldCanvasFontPct` and `themeLayoutCache.textPct` are element-agnostic.
  - ~~**The message rect's 4 px inset.**~~ **SHIPPED** (`chatboxfit.go`,
    `themedMessageInsetPx`). `ui_vp_message` is a frameless `QTextEdit` with both
    scrollbars off, so Qt's default `QTextDocument::documentMargin` insets it on
    ALL FOUR sides. Confirmed by measurement rather than reading: a probe built
    against the Qt 6.5.3 kit on the dev box, constructing the widget the way
    `courtroom.cpp` does and sizing it to the stock theme's `message` rect,
    reports `documentMargin` 4.0, a position-0 text cursor at (4, 4) and a
    first-block bounding rect 234 px wide inside a 242 px viewport. Symmetric,
    unlike the chatlog inset — the message has no scrollbar, so the asymmetric
    helper is deliberately not reused.
  - ~~**`showname_align`.**~~ **SHIPPED** (`chatboxfit.go`, `shownameSpan`).
    Declared by 66 of the corpus's 97 design files (50 `center`, 16 `left`, none
    `right` or `justify`) and previously read by nothing, so two thirds of themes
    had their showname drawn hard left. `justify` maps to centre because that is
    the branch AO2 wrote for it.
  - ~~**Showname vertical centring.**~~ **DISPROVEN — do not build it.** The
    premise was that `ui_vp_showname` v-centres by `QLabel` default while AsyncAO
    top-anchors it. It does not: AO2 calls `setAlignment` with a HORIZONTAL-only
    flag, both at construction and again for every message in the
    `showname_align` block, and `QLabel::setAlignment` clears the vertical mask —
    so Qt's default `AlignVCenter` is gone and the text lands at the top of the
    widget. Measured on Qt 6.5.3 with a 232x120 label (the tall
    `microsoft surface 4k webao` showname rect): ink starts at y=54 under Qt's
    default alignment and at y=2 under `AlignLeft` alone, which is what AO2 sets.
    AsyncAO's existing top anchor IS the parity behaviour; centring it would be a
    divergence. No theme can override this either — nothing in the corpus or the
    shipped AO2 themes uses `qproperty-alignment`.
  - ~~**The `med` / `big` chatbox skin swap.**~~ **SHIPPED, both rungs**
    (`chatboxfit.go`, `shownameLadder` / `chatboxSkinLadder`). AO2 measures the
    showname and, if it overflows the box the theme drew, widens by
    `showname_extra_width` and swaps the chatbox art to `<stem>med`; if it still
    overflows, widens by twice that and swaps to `<stem>big`; past that it cuts
    the text off. A theme shipping neither variant leaves the showname unresized —
    documented AO2 behaviour, reproduced exactly, not worked around. Four things
    worth knowing before touching it:
    - **The zero-fallback objection turned out not to apply.** Theme art is
      LOCAL content, not streamed: `applyThemeAsync` resolves it with `os.Stat` /
      `os.ReadFile` on the theme goroutine and `pollThemeApply` pins the result in
      T1. So the two variants cost **two extra `os.Stat` calls per theme apply**,
      beside the two dozen a theme already pays — not one per message, and nothing
      on the network. The one-probe-per-asset rule governs character / background /
      evidence URLs, which is a different pipeline. The negative is cached by
      construction: a theme that ships no variant leaves the stems out of
      `themeTex`, and `themePage`'s first line is that map probe, so "no med skin"
      is one map read per frame forever.
    - **The variant's stem is derived, never hardcoded.** AO2 appends to the
      RESOLVED base image's own path, so the file is `<whatever base resolved>med`
      in the base's own directory. 64 of the 74 corpus themes resolve `chat`,
      `P5Theme` resolves `chatbox`, two resolve only `chatblank`; and the
      directory scoping matters because our theme loader falls through to a
      `default` theme dir, which `theme.FindAssetIn` deliberately does not.
    - **`showname_extra_width` is scaled by the canvas**, baked into
      `themeLayoutCache.shownameExtra` on the cold rebuild so the draw path stays
      integer-only. AO2 does not scale it (rects go through
      `themeScalingFactor`, this value does not), but AO2 ships that factor at 1,
      so the two agree everywhere AO2 runs; ours is 1 only at the Native default
      on a window that fits the canvas. Floored at 1 px so a downscaled canvas
      cannot silently drop the skin swap.
    - **Corpus behaviour**, measured on all 74: 60 ship both variants, 14 ship
      neither, and **none ships `med` without `big`** — so the `med`-only rung is
      pinned by test, not by a real theme. Six themes ship a `chatmed.png` with no
      base skin at all; AO2 resolves their base through its bundled default theme,
      a streaming client has no such tree, so their chatbox is the flat panel and
      the ladder correctly does not run. 65 declare a positive
      `showname_extra_width` (32 of them 24, 14 of them 10, 4 of them 48,
      `P5Theme` 225); the other 9 declare 0, which switches the mechanism off.
      **The ladder therefore fires on 52 of 74 themes**, and `P5Theme` — the 225 px
      stress case — costs nothing, because it ships neither variant and AO2 never
      widens it either.
    Past the last rung the behaviour is unchanged: a showname wider than the
    biggest skin the theme shipped is left-anchored and clipped on the right at
    every alignment (`shownameSpan` floors the offset at zero). AO2's Qt offset
    goes negative there and clips the name's HEAD instead; losing the beginning of
    a name is the worse of the two.
  - **The classic (un-themed) chatbox overlay is deliberately excluded.** It has
    no `showname` design rect to overflow — its name box is the whole chatbox
    width less the overlay padding — and no design width for AO2's comparison, so
    the ladder has nothing to compare against and nothing to widen. It still draws
    the theme's base chatbox skin, as it always did.
  **Mirror site — now structural.** `drawGifThemedChatbox`
  (`internal/ui/gifexport.go`) used to repeat the live chatbox's rect resolution
  with its own copy of the arithmetic. Both now call `chatboxTextRects`, and
  `TestThemedChatboxRectsResolvedInOnePlace` fails if either grows a second copy.
  The export's showname takes the canvas fold through the same shared
  `themedChatFace`, against the layout built for the CAPTURE frame. Its *message*
  deliberately does not: `exportChatPct` already derives a size from the capture
  height and `fitChatRaster` then shrinks it until the block fits the box, so a
  third factor would only fight that fit loop. The med/big ladder runs on both
  sites for the same reason and is guarded the same way —
  `TestShownameLadderReachesBothDrawSites` fails if either draws a themed chatbox
  without it, because a long name on the wide plate on screen and spilling off the
  narrow one in the exported file is exactly the drift this consolidation ended.
  **Clip caution:** AO2 does not clip `ui_vp_message` to the chatbox (it is
  parented to the courtroom with only an origin offset) and themes legitimately
  overhang — 8 design files in the corpus do, `CCSmol` (message right edge 259 in
  a 256-wide chatbox), `Mobile` (393 in 392) and `HDF-Fullscreen (1080p)` (770 in
  765) worst. The clip stays on the chatbox rect; tightening it to the message
  rect would cut those themes' last column.
- **Read `lobby_design.ini` (theme follow-up).** Half the reference themes ship a
  lobby layout AsyncAO never opens: only `courtroom_design.ini`,
  `courtroom_fonts.ini`, `courtroom_sounds.ini` and the penalty INI are read. The
  lobby is therefore the one screen an imported theme cannot place at all.
- **Aspect-lock the CLASSIC (non-themed) layout (Native-fit follow-up).** The
  theme-fit modes only govern the THEMED courtroom: `themeLayout`
  (`internal/ui/theme_layout.go`) transforms an AO2 theme's design canvas, and
  its new **Native 1:1** default reproduces stock AO2 exactly, because AO2 never
  scales — its window *is* the canvas. The classic layout has no design canvas at
  all: `fracToRect` (`internal/ui/classiclayout.go`) lays every box out as a
  fraction of the live window, so its proportions follow the window shape rather
  than a fixed one. Giving it the same "keep the authored proportions" guarantee
  means choosing a reference canvas for a layout that was never authored against
  one, and re-deriving every slot's anchor from it — a redesign of the classic
  layout model, not a fit mode, which is why it was deliberately left out of the
  Native-fit change. **Not to be confused with** the existing **4:3 lock** in the
  classic layout *editor* (guarded by `a.classicEditKey == slotViewport`): that
  keeps the STAGE box 4:3 while you drag it, is unrelated to theme fit, and stays
  exactly as it is.
- **Carry the theme-import window snap on the theme result itself.** The snap is
  armed as a generation number held beside the apply (`themeResizeArmGen` in
  `internal/ui/app.go`) and spent when the apply carrying that generation lands.
  Because the theme pipeline publishes through a single slot and newest wins, an
  apply started AFTER the armed one — a texture-eviction repair, a server binding
  its own theme — can land first and annihilate the armed result, and the snap is
  then simply lost (it disarms cleanly; nothing misfires, and the manual
  **Settings → Window → Theme's design size** button is one click away). Moving
  the intent onto the result object instead of naming its generation would make
  the snap survive that race outright, but it means threading a UI decision
  through the off-thread load, so it was left out of the pass that fixed the
  stranding.
- **Pace every automated wire producer reachable from `App.Background`
  (v1.82.0 follow-up).** The minimized-disconnect fix paced the OOC automation
  queue at its **drain**: `processOOCQueue` (`internal/ui/macros.go`) releases at
  most one line per `oocSendMinGap`, and `App.Background` drains it as well as
  `App.Frame`, so the send rate is identical whether the window is focused,
  unfocused or minimized. That closed the live-roster `/gas` poll
  (`internal/ui/liveroster.go`), but the *class* is general: anything reachable
  from `App.Background` puts packets on the wire at a rate that otherwise depends
  on whether a frame was drawn, and servers rate-limit per IP and kick on breach
  — a kick that arrives as a bare close with no reason, which is what made this
  one so expensive to find. One instance is left: **follow-a-player**, where
  `maybeFollowJump` (`internal/ui/follow.go`) rides the session-event drain and
  `jumpToArea` (`internal/ui/playerlist.go`) sends its `MC` area transfer through
  `Session.RequestMusic` **unpaced**. It is safe as it stands —
  `followJumpDebounce` is 2 s (0.5 MC/s) against a typical server budget of
  ~1.4 messages/s, and following is opt-in (`FollowEnabledOn`) — so this is
  hardening, not a live bug. The work: generalize the drain-time floor beyond OOC
  into one paced send path for automated packets, and make "does it go through
  the paced path?" the standing review question for every new
  `Background`-reachable producer. Zero hot-path cost — a timestamp compare on a
  drain that already runs.
- **Custom screen effects — AO2 `effects.ini` system (v1.55.7 follow-up).** The
  inline codes `\s`/`\f`/`\n`/`\p` and a dedicated "Enable screen effects" toggle
  **shipped in v1.55.7**; the remaining half is AO2's named custom-effect system so
  people can make their own beyond the built-in shake/flash. Plan (mirrors
  AO2-Client `AOApplication::get_effect` + `effects.ini`):
  - **Assets:** add `AssetTypeEffect` (`internal/assets/types.go` + `typeNames`) and
    `URLBuilder.Effect(name)` → `effects/<name>` (webAO/AO2 convention); stream the
    overlay sprite like the shout bubble (one probe, `PrefetchWithFallback`).
  - **Manifest:** parse `effects.ini` (theme + char/misc) via `internal/theme/ini.go`
    (`ParseINI` / `SectionKeys`) into named effects with properties (sprite, sound,
    loop / sticky).
  - **Render:** `Scene.EffectBase` + a `drawFill`-style animated overlay in
    `internal/render/viewport.go` — **must join the NoteAnimating census and
    self-clear when the clip ends** (the recurring frame-pacing trap); reduce-motion
    + the ScreenEffects toggle gate it.
  - **Trigger + UI:** the 2.8 `EFFECTS` field is already parsed (`courtroom.go`
    `fireMessageEffects` — today it plays only the named effect's *sound*); hook it to
    play the `effects.ini` art, and add an effect picker to the IC bar (AO2 effect-
    dropdown parity) so custom effects are selectable and sendable.
  Zero hot-path cost (cached-texture overlay blits, free when idle). The inline-code
  half is done and revert-clean, so this lands cleanly as a follow-up (v1.55.8).
- **Per-element theme fonts — remaining attributes (#39 follow-up).** The main
  slice **shipped**: `courtroom_fonts.ini` now drives the **family, point size and
  bold** of `showname`, `message`, `ic_chatlog`, `server_chatlog`, `music_list`,
  `music_name` and `area_list` in every layout variant, and the family resolver
  gained the tiers real themes actually need (the theme's own `fonts/` subfolder,
  a recursive base `fonts/`, and the OS font folder for a theme that just says
  "Arial"). What is still deliberately NOT applied:
  - **`<id>_sharp`** — AO2 renders a "sharp" element with `QFont::NoAntialias`
    (`courtroom.cpp:1237`). Parsed into `theme.FontSpec.Sharp` and deliberately
    NOT applied: measured to be a no-op at the sizes it targets and a legibility
    regression where it would actually bite, with a colour artefact on top. Full
    reasoning and measurements in `docs/KNOWN-ISSUES.md` — do not rebuild it from
    an older spec without reading that first.
  - **`<id>_outlined` / `_outline_color` / `_outline_width`** — AO2 outlines only
    `AOChatboxLabel` (`courtroom.cpp:1282-1290`). Unmodelled in `FontSpec`
    entirely, and the better next target in the same `set_font` body now that
    colour has landed: a bigger visible delta on the same widget than `_sharp`.
  - ~~Per-element COLOUR for the list surfaces~~ — **shipped** (#21 label 16,
    `internal/ui/themeink.go`). `<id>_color` now drives `ic_chatlog`,
    `server_chatlog`, `music_list`, `music_name`, `area_list` and `debug_log`,
    canvas-only, behind a per-element backdrop-luma guard that reuses
    `minInkSkinContrast`. What did NOT ride with it, and why: AO2's opaque per-row
    brushes (`found_song_color`, the seven `area_*_color`) are a deliberate
    non-goal — the found/missing split is a synchronous local disk stat per row.
    Still open in the neighbouring `get_color` family:
    `ic_chatlog_selfname_color`, `*_sender_color`, `ic_chatlog_timestamp_color`
    and `label_color`. Note those go through `AOApplication::get_color`
    (`text_file_functions.cpp`), NOT `set_font`'s per-element key, so they do not
    belong on `FontSpec`.
  - **`clock_N`, `evidence_*`** — no distinct AsyncAO surface yet.
    (`debug_log` and the `ms_chatlog` rect DID gain one — the themed debug panel —
    and both are wired.)
  - **Per-character `get_chat(p_char)` font overlay** (AO2
    `text_file_functions.cpp:562`) — a character's `misc/` folder can override the
    chatbox fonts; AsyncAO applies only the theme's.
  - **Local-mount `fonts/` folders** as a resolver tier (AsyncAO's analogue of
    AO2's `Options::mountPaths()`, `internal/assets/local.go` `Mounts()`).
  - **Design-resolution font scaling.** AO2 multiplies its INI point sizes by a
    FIXED user option (`Options::themeScalingFactor`, default 1.0) and does not
    track the window, so neither do we: a theme stretched far from its design
    resolution keeps its declared point sizes and the per-panel Ctrl+wheel zoom
    is the user's knob. Revisit only if the field asks — it would mean folding
    `themeLayoutCache.scaleX/scaleY` into `elemPct`, which must be quantized or a
    drag-resize rebuilds every font set (and purges the label cache) per pixel.
- **Screenshot annotator (#72)** — quick arrows/boxes/text on a captured
  screenshot before sharing. Deferred from the v1.50.0 batch (the studio +
  playtest-fix stream ate the session); the natural entry point is an
  "Annotate last screenshot" action in Extras + the Ctrl+Space palette, with
  the marks rendered through render.CaptureTarget and saved as
  `-annotated.png`. Next batch's lead item.
- **Crisp text at scale (#77)** — see the standing "Crisp
  resolution-independent UI text" track below, re-diagnosed 2026-07-03 for the
  v1.53.5 round (Tifera: the window is a blurry mess at >100%, and 100% is too
  small). Confirmed: it is NOT Windows DPI virtualization — the process is
  already per-monitor-v2 aware (`SDL_WINDOWS_DPI_AWARENESS` in main.go) and
  nearest/linear is already a Settings pref — the residual blur is OUR OWN
  `ren.SetScale` linearly stretching text that was rasterized at 96 dpi.
  **PART A LANDED (the blur fix):** the global UI scale now folds into font
  POINT size — chrome (`c.font`/`c.fontBig` → device siblings `fontDev`/
  `fontBigDev`), the chat/log sets (device siblings `chatSetDev`/`logSetDev`,
  built via `fontsForDev`), and the message raster (`render.Rasterize*` take a
  `devScale`, store it on `MessageRaster`, and `Draw` divides the device dst back
  to logical). `ren.SetScale` STAYS active (geometry + mouse unprojection
  unchanged), so glyphs rasterize at final device size and blit 1:1 — crisp at
  any scale. Measurement (`TextWidth`) stays LOGICAL; the round-half-up rule
  lives in `render.logicalFromDevice` == `ui.uiLogicalFromDevice`. Exports and
  the pinned-tab pass are handled (exports BRACKET `textDevPct` to 100 for native
  resolution; the split pass inherits ambient and composes at 1:1). Sprites /
  backgrounds / viewport art are untouched (they keep GPU linear scaling — they
  are photographic, not vector text). **Known Part-A follow-ups (deferred, not
  blocking):** the ANIMATED text path (`AnimatedText`/`GlyphCache`, `msAnim`)
  stays on the logical face — an effects message is correctly-sized but still soft
  at >100% (clean seam: `msAnim` XOR `msRaster`, one per message). **PART B LANDED
  (DPI seeding):** a HiDPI monitor's *default* physical size is now correct without
  the user finding the slider. The auto-scale path already combined a DPI component
  (`dpiScalePct`) with the window-size factor; the gap was that `sdl.GetDisplayDPI`
  reports a flat 96 under per-monitor-v2 awareness, so detection stayed at 100. The
  fix makes the DPI *input* reliable: `App.SeedDisplayDPIScale` queries Windows
  `user32!GetDpiForWindow` for the window's monitor (via the SDL `SysWMInfo` HWND;
  plain Win32 syscall through `syscall.NewLazyDLL`, no new dependency), falling back
  to `sdl.GetDisplayDPI` off Windows (`internal/ui/dpiseed_{windows,other}.go`).
  The pure `config.DPIScalePercent` (96 dpi → 100%, 144 → 150%, round-half-up,
  floored at `MinAutoUIScalePercent` = 100) is the seam; the boot query replaces the
  old `main.go` block and a `WINDOWEVENT_DISPLAY_CHANGED`/`MOVED` handler re-seeds
  when the window changes monitor (gated on `lastDPIDisplayIndex` so a same-monitor
  move is free). **The seed is RUNTIME-ONLY** — it never writes the UI-scale pref, so
  an explicitly saved scale always wins (the manual slider requires `UIScaleAuto`
  off, and `UIScale()` then ignores the detected value; `UIScaleAuto` *is* the
  "user chose it" marker — no new pref). The never-below-100 floor (#6) is kept.
  100% still means 96dpi-logical (slider semantics unchanged). **Consequence (by
  design, not a bug):** turning Auto OFF on a HiDPI monitor snaps to the manual
  `uiScalePct` default (100%), because copying the seed into the saved scale would
  persist it as a user choice — the decision forbids that. **Issue #77 is now
  closable pending live verification** on a real scaled monitor (this dev box is at
  100%; a fresh profile there must still start at 100%). Interim answer for players
  on pre-Part-A builds: 200% + Smooth OFF is pixel-exact; non-integer scales stay
  soft until Part A ships.

_Playtest backlog cleared (2026-06-21) — every Discord/playtest request shipped
(see `docs/FEATURES.md`). New asks land here. The only milestone left is the
gamepad track below._

- ~~**More power-user knobs — the menu**~~ — **all shipped in v1.40.0** (404 TTL,
  deadline multiple, downscale override, texture budget, crossfade — see FEATURES),
  along with the **adaptive frame pacing** that fixed the idle GPU burn. Both
  follow-ups from that conversation **shipped in v1.55.0**: the **wake-on-input /
  event-driven loop** (`SDL_WaitEventTimeout` instead of poll+sleep, so idle input
  latency is ~0) and **skip-when-nothing-changed rendering** (a static screen skips
  render+present entirely — `SkipFrame` — so idle=off is genuinely zero GPU, not
  just paced), together with a real frame limiter (inviolable active/idle/background
  caps) and audio decoupled from the frame rate. A finer per-tile damage/dirty-rect
  pass (only re-composite the changed region of a *rendered* frame) remains the SDL3
  track below.
- ~~**Low-quality persistent sprite cache**~~ — **shipped in v1.40.0** (Settings →
  Power user → "Sprite thumbnail cache", default OFF; see FEATURES), including the
  byte-budget auto-prune (oldest-first past the cap).
- ~~**Cold-load per-stage profiling**~~ — **shipped in v1.40.0**: the debug overlay
  carries a `cold-load · fetch · decode · upload` EWMA line (F8 / Settings → Power
  user → Diagnostics). Original note kept for context: add per-stage timing
  (fetch TTFB/transfer · decode+CatmullRom-downscale · upload) to the metrics
  cold-load report so the bottleneck is measured, not asserted. Confirmed by hand:
  the dominant cost for an uncached sprite is **network transfer + latency**, but the
  **CatmullRom downscale of huge (2000²) sprites** runs in the decode pool and is a
  real secondary cost (the old "blurry huge WebP" was this path pre-fix). Hold-
  previous is **bottleneck-agnostic** — it covers the gap whatever the cause — so it
  was the right first move regardless.
- ~~**Config presets**~~ — **shipped in v1.40.0** (Settings → Data → "Setting
  presets": named full-settings bundles, apply-on-restart via the import path).
  Original ask (Nightingale, 2026-06-29) — the settings file is
  comprehensive (~130 KB once everything's learned), which is great for power
  users but heavy if you just want a couple of named "profiles" to switch
  between. Idea: a small, separate preset layer — pick/save a handful of named
  setting bundles — on top of the existing one-JSON store, so the full file
  stays the source of truth and presets are an opt-in convenience. Tracked
  separately from the v1.19.0 portable-config work (which moved the file beside
  the exe and is shipped). No transmitted/wire impact; off the hot path.

---

## Already shipped (rebuild to get them)

These were requested again but are already in the client — if they're missing,
it's a stale build (`scripts\build.ps1 -Release`).

- **Esc leaves the server through the confirm prompt** (Nightingale, 2026-07-01) —
  pressing **Esc** on the courtroom or char-select screen routes through the
  Disconnect confirm (unless "Instant disconnect" is on), so an accidental tap can't
  boot you. Fixed **2026-06-29** (`725f9a2` + `97f127c`, in HEAD/v1.33.5). If Esc
  still disconnects instantly, it's an **older build** (older paths called
  `Disconnect()` directly). v1.40.0 adds a "Don't ask again" tick to that prompt.
- **"Show volume sliders" (Vol strip) persists** — the log-panel **Vol** toggle
  survives restarts (`133c9ff`, in HEAD). v1.40.0 also persists the **Music menu's**
  volume-sliders view (that one really was session-only).
- **Callword/alert volume separate from SFX** — `AlertVolume` is its own slider,
  independent of SFX volume (Settings → Audio).
- **Add-to-friends from the player popup** — double-click a player → the popup has
  a friend toggle (+ the per-row "+ Friend" button).

---

## In flight / larger (separate tracks)

- **Voice chat — Nyathena-gated** *(#17, requested for v1.2)*. Server-relayed over
  the existing WebSocket — **not** P2P/WebRTC (confirmed: `../LemmyAO/src/voice/
  voice.ts` "There is no WebRTC"), so peer IPs never leak. Wire (canonical, from the
  Nyathena/LemmyAO `aolib` `VS_*` packets): `VS_CAPS` (caps advert) · `VS_PEERS`
  (uid list) · `VS_JOIN`/`VS_LEAVE` · `VS_SPEAK` (speaking toggle) · `VS_FRAME`
  (c2s opus) · `VS_AUDIO` (s2c opus). 48 kHz mono, 20 ms frames.

  **Shipped in v1.19.0:**
  - **Slice 1 — protocol + signaling** (`internal/courtroom/voice.go`): VS_* parse/
    build + per-session presence (caps, peers, speaking), all bounded; gated on
    `VS_CAPS` so non-Nyathena servers have a byte-identical wire. Unit-tested.
  - **Slice 2 — Opus codec** (`internal/voice`, libopus CGO, SDL-free): encode/decode
    round-trip + PLC, unit-tested. Opus is BSD (AGPL-compatible).
  - **Slice 3 — presence UI** (`internal/ui/voicepanel.go`): a Nyathena-gated
    floating panel (Extras → "Voice (Nyathena)", hidden elsewhere) — Join/Leave, the
    live peer list with speaking indicators, and your own speak toggle. Two AsyncAO
    clients can see each other in voice + who's talking.

  **Remaining — live mic audio (the next slice):** wire SDL2 audio (capture +
  playback, queue API — `internal/render`, on the render thread per hard rule #1) to
  the codec + signaling: PTT/open-mic capture → encode → `VS_FRAME`; `VS_AUDIO` →
  decode → **mix N peers** (bounded per-peer buffers) → output, with per-peer
  volume/mute. Frames funnel through the session loop (single send path; never the
  audio thread). Fail-safe init (any device/codec error → voice silently disabled,
  never fatal) and opt-in (default off) so the audio path is unreachable for general
  users. **Blocked on** the user's Nyathena server + a mic to validate; advisor-check
  before committing the audio engine. Build surface when it lands: libopus in every
  CI build (release.yml + flatpak) + `build.ps1` DLL staging (auto via the ldd
  closure) — `ci.yml` already has `libopus-dev`.

- **M16 Scene studio** — recording, replay player, scene maker, GIF + animated
  WebP export, crop/trim, per-line effects, **proportional timeline strip with
  draggable In/Out handles _and drag-to-reorder_** (#75 + follow-up, shipped),
  and **Instant Replay** — an opt-in rolling buffer that clips the last window
  (10 s … 1 h) of conversation with no recording started in advance (shipped) —
  all in `docs/FEATURES.md`. Possible later tweak: continuous-playback scrubbing
  on the timeline strip.
- ~~**Shareable scene/server deep-link** *(#52)*~~ — **closed** (2026-06-21, by
  request): the gif/WebP export half shipped; the deep-link half is covered by
  the existing **Direct Connect** field (paste a `ws://`/`wss://` URL in the phone
  book) and the `--server` launch flag, so no bespoke link or `asyncao://` scheme
  was built.
- ~~**M8 Gamepad support** *(#44)*~~ — **dropped** (2026-06-21, by request — no
  need for it). The whole milestone backlog is now closed.

## Future / larger tracks (not scheduled)

- **SDL3 migration — real GPU/shader pipeline.** The post-processing FX (vignette,
  scanlines, grain, chroma / glitch, depth-of-field) are currently a
  cached-texture multi-blit *approximation* because SDL2's renderer has no shader
  stage and no per-texture scale-mode control. SDL3's GPU API (render passes +
  shaders) would make those real, cheaper, and composable — but it's a large,
  cross-cutting port (every `internal/render` call site, the texture tiers, the
  SDL_mixer audio back-end) and stays parked until the FX / perf win clearly
  justifies the churn.
- **Crisp resolution-independent UI text (#77).** _Status: Part A (blur) + Part B
  (DPI seeding) both LANDED — see the tracked #77 entry near the top of this file;
  only the ANIMATED-text follow-up remains. This is the original design sketch, kept
  for context._ The global UI scale WAS applied
  with `ren.SetScale`, which bitmap-upscaled already-rasterized text — correct
  size but soft above 100% (see `SetAutoScaleFromWindow` and the v1.2.0 #6 fix).
  Re-scoped 2026-07-03 (the v1.53.5 DPI dig): the proper fix rasterizes text at
  the *native device* pixel size — fonts opened at `pt × scale`, the resulting
  texture drawn into the unchanged *logical* rect, so with the renderer scale
  active the glyph pixels land 1:1 on device pixels and any scale stays sharp.
  The kit's coordinate system doesn't change; what does is every place that
  assumes texture px == logical px:
  - `TextWidth` / measurement must return LOGICAL units (device ÷ scale), and
    the width caches + text atlas need the scale in their keys (a scale change
    is a cache generation bump, like `fontChainGen`).
  - `blitLabel` / `LabelClipped*` dst rects become `tex.size ÷ scale` with
    rounding audited (off-by-one at odd scales is the classic failure).
  - The message raster + typewriter reveal indexes (per-rune positions) and the
    emoji fallback raster measure with the scaled faces; GIF/comic export paths
    pick their own scale explicitly (they render offscreen at 100%).
  - The IC/OOC log wrap caches key on font scale already — they just need the
    same generation bump on a UI-scale change.
  All of it **without** regressing the 0-alloc render gate (`BenchmarkRenderFrame`),
  which is why it's its own milestone and was explicitly not rushed into the
  v1.50.0 or v1.53.5 batches. The window-relative auto-scale landed the *sizing*;
  this lands the *sharpness*.
