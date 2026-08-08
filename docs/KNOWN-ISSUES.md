# Known issues / future work

Tracked limitations that need more than a localized fix.

## Chrome that paints over a widget must also claim its input (issue #37)

The kit is immediate-mode and single-pass, so a widget drawn early has no way of
knowing that a widget drawn later will paint over it. Left to itself, every piece
of client chrome that floats above a screen leaks the pointer to whatever is
underneath: the reported symptom is a button press that also presses the control
it is covering. This is a bug **class**, not a bug — it recurs every time a new
overlay is added, which is why the client had already grown three ad-hoc answers
to it (`c.modalOn`, `c.ddOpenList` consulted by the floating-box code, and
`a.boxFencesPointer` / `c.fencePointer()`).

The generalized primitive now exists: `fenceOverlay` (`internal/ui/overlayfence.go`)
publishes an overlay's painted rect into a bounded, frame-scoped registry, and
`hovering()` (`internal/ui/ui.go`) reads it after the `modalOn` and `clipOn`
checks, so anything beneath a published rect reads as not-hovered. It nests, it
cannot strand (it is cleared in `BeginFrame`, unlike `modalOn`), and an occluder's
own chips stay live because it hit-tests from inside its own ownership scope.

**Two limits keep this open.** First, adoption: only the compact toolbox strip and
the menu bar (its strip, its panes and its submenus) publish today. Every other
overlay still relies on the older per-case fences or on nothing at all. Second,
reach: `hovering()` is the **only** consumer, so the registry covers
`hovering()`-based hit tests only — raw `pointIn()` sites (the layout editors' drag
tests, a dropdown's own list rows) still see straight through a fence unless they
call `c.overlayFenced` themselves.

The rule for anything new: **an overlay that paints over pixels a screen already
drew must publish that rect**, from **inside** the pass that draws it — never from
a site that also runs on frames where that pass didn't draw, because a latched
position is stale then and a fence over pixels nothing painted is the mirror image
of the leak it fixes. Closing this entry means sweeping every remaining overlay
onto the primitive and deciding, per raw `pointIn()` site, whether it should honour
a fence — an audit across the whole kit rather than a localized change.

## Theme font `_sharp` is parsed and deliberately not applied

AO2's `set_font` reads `<element>_sharp` and, when it is the literal string `1`,
builds the font with `QFont::NoAntialias` (`../AO2-Client/src/courtroom.cpp:1237`,
`:1268-1274`). AsyncAO parses the flag into `theme.FontSpec.Sharp` and stops
there. That looks like a trivial gap — branch `RenderUTF8Blended` to
`RenderUTF8Solid` — and it was specced as such. **Measurement killed it.** Keeping
the numbers here so it is not re-attempted from the same wrong premise.

**It is a no-op where it was supposed to matter.** `aceattorney2x` sets `_sharp`
on `showname` and `message`, in pixel-art faces it ships itself
(`ace_name_regular.ttf`, `igiari-cyrillic.ttf`) — which is why it looked like the
fix for "rendered it badly". But AsyncAO opens those at 16 px and 32 px
(`themeFontPx` × `themeFontPct`, `UIFontSize` = 12), and at those sizes the faces
already rasterise fully binary: `ace_name_regular` @16 px measures 1204 opaque /
**0 antialiased** pixels under *both* Blended and Solid, and `igiari-cyrillic`
@32 px measures 1712 / 0 under both. Identical censuses. These are pixel-grid
faces at a design-multiple ppem, so FreeType's normal rasteriser is already
producing what `_sharp` asks for.

**Where it does bite, it makes things worse.** The only elements with real
antialiasing are the ones resolving to system Arial — `server_chatlog`,
`music_list`, `area_list` at 8 pt → 11 px (657 antialiased pixels → 0). Those are
the small-text list panels, and rendering 11 px Arial with antialiasing off is a
legibility regression, not a fidelity win.

**And Solid carries a colour artefact.** SDL_ttf writes the INVERSE of the
foreground colour into its transparent texels, where Blended writes the
foreground colour itself: measured `rgb@alpha0` = `000000` under Solid for white
text, `FFFFFF` under Blended (and the reverse for black text). AsyncAO runs
`SDL_HINT_RENDER_SCALE_QUALITY = "1"` (`cmd/asyncao/main.go`), so at any non-1:1
blit those texels bleed into the glyph edges — a dark halo on the white showname,
a light halo on the black message body. An alpha-only census cannot see this,
which is how the first feasibility probe missed it.

Two further implementation hazards, recorded because they are not obvious:
colour emoji renders **completely blank** under Solid (measured 576/576 fully
transparent pixels, zero chroma, on the bundled COLRv0 Twemoji), so any
implementation needs an exact carve-out for the emoji face; and the showname
MEASURE path (`chatboxfit.go`) rasterises through the same cache as the draw
path, so threading only the draw sites silently doubles the raster and desyncs
the widen ladder from what is drawn.

Also unimplemented in the same `set_font` body, and a *larger* visible delta on
the same widget: `outlined` / `outline_color` / `outline_width`
(`courtroom.cpp:1239-1256`), which `FontSpec` does not model at all. If theme
typography is picked up again, that is the better first target.

## A missing BACKGROUND still holds the previous room's, unlike the desk

The desk layer releases when its image is conclusively missing — that was issue
#44, and `syncAnimDesk` (`internal/render/viewport.go`) documents its two release
conditions. The background layer deliberately does **not**: `syncAnimSticky` holds
the current background until the incoming one is resident, so a background that
404s leaves the previous room on screen indefinitely.

This was considered during the issue #21 work and **deliberately not changed**,
because the obvious fix is wrong. AO2 does not hide a missing background — it
falls back to `wit` *within the same background folder* first, and hides the layer
only if that is missing too (`../AO2-Client/src/courtroom.cpp:4613-4625`):

```cpp
if (file_exists(... get_background_path(pos.background))) { show(); load(pos.background); }
else if (file_exists(... get_background_path("wit"))) { show(); load(... "wit"); }
else { ui_vp_background->hide(); }
```

Releasing the held background would therefore replace a stale-but-plausible room
with a black stage — strictly worse than today, *and* still not what AO2 does.
Closing this properly means implementing the `wit` fallback: on a conclusive
background miss, probe `<same folder>/wit`, bind that, and release to black only
when both are gone.

Two things make that more than a localized change. The probe is a second network
request on a path the client otherwise keeps to exactly one probe per asset, so it
needs the same conclusively-missing bookkeeping the desk already has to avoid
re-probing inside the 404 TTL. And the desk is derived from the same `pos` pair, so
a background that fell back to `wit` should arguably take `wit`'s desk with it
rather than the requested position's — a courtroom-side question about
`deskResolution`, not a renderer one.

Until then the current behaviour stands, and it is the safer of the two wrong
answers.

## Automated senders whose wire rate depends on whether a frame was drawn

A producer that runs in `App.Background` (which keeps ticking while the window
is minimized) feeding a consumer that runs only in `App.Frame` (which does not)
is a bug **class**, not a bug: the backlog builds for the whole occlusion and
leaves as one burst on the first restored frame, which servers read as flooding.
That exact shape caused the minimized disconnect (resolved below, and the reason
the OOC automation queue is now paced at the drain and drained on both paths).

One instance of the same shape survives, deliberately unfixed.
`maybeFollowJump` (`internal/ui/follow.go:48-77`) runs off the roster events
pumped in `Background`, and the jump it performs — `jumpToArea`
(`internal/ui/playerlist.go:881-896`) — sends its `MC` area transfer
(`Session.RequestMusic`) **straight to the socket**, with no queue and no
minimum gap. It is safe as it stands: `followJumpDebounce` is 2 s, so the worst
case is 0.5 packets/s against a typical server budget of ~1.4 messages/s, the
feature is opt-in (`FollowEnabledOn`), and a jump only fires when the followed
player actually changes area — a rate their client is itself paced at. But that
safety comes from a *debounce constant*, not from a paced sender, so it would
lapse the moment either number moved.

The rule for anything new: an automated sender (one the user did not press a
key for) must enforce its minimum gap where the packet is **sent**, never only
where it is scheduled — a stalled queue whose entries are all "due" flushes as
a burst no matter how politely it was scheduled — and it must be drained
identically from `App.Background` and `App.Frame`, so its wire rate cannot
depend on window state. Closing this entry means routing `MC` (and any other
direct sender) through a paced queue like `processOOCQueue`, which is a
transport-shaped change rather than a localized one.

## The player list learns pairing by polling, so a partner's client never updates

The Players tab is not fed by a live channel. `drawPlayerList`
(`internal/ui/playerlist.go`) builds its rows by parsing the server's `/getarea`
reply, and the pair column comes from there — AO has no per-player packet, only
ARUP area counts, so the roster is refresh-driven and stamped as of its last
fetch. That fetch used to re-run itself on join and leave (`maybeRefetchRoster`);
since 2026-08-08 the whole automatic tier is DELETED (see "The automatic roster
poll is gone" below), so it re-runs only when the user asks.

A pair change is neither a join nor a leave, so nothing re-runs it. The client
that set the pair renders it immediately because it knows locally; the partner's
client is still holding a snapshot taken before the pair existed and shows
nothing. The reported symptom is one-sided: the indicator is visible to whoever
paired and invisible to the person they paired with.

The obvious fix — refetch the roster when pairing changes — is **deliberately not
taken**. It adds an automated OOC command on a user-triggered event, which is the
same shape as the roster poll that caused the minimized-disconnect flood-kicks
(see the entry above, and the sender-pacing rule it ends with). A pairing feature
is not worth a disconnect.

The real fix is to stop polling for something the wire can carry:

- **Read AO2's own fields.** An incoming `MS` already carries the speaker's
  pairing (`other_name` / `other_emote` / `other_offset` / `other_flip`, injected
  server-side), and the client already parses them to render paired sprites. That
  makes a pair knowable to every client the moment either party speaks, with no
  protocol extension, no new packet and no extra traffic. It is still gated on
  someone speaking, but it costs nothing and the data is already in the session.
- **An AsyncAO packet on pair change** is the only answer that is live rather than
  speak-gated, and it is the expensive one: it needs the server to relay a header
  it does not know, which most AO servers will not do. Viable on server families
  we control, with the standing requirement that a transmitted feature interops
  AsyncAO-to-AsyncAO and degrades gracefully everywhere else.

Riding the existing zero-width profile channel is a third option and a worse one:
it needs the same "after they speak" precondition as reading `MS`, while also
requiring an AsyncAO peer.

Closing this entry means surfacing the `MS` pairing fields onto the roster, and
deciding separately whether a live indicator justifies a server-side change.

**The indicator's presentation is a separate, unstarted change.** Today a paired
row carries a coloured chip containing the *partner's character name*, which reads
as an unexplained coloured bar — it does not say "paired", and the name in it is
not information anyone was looking for. What is wanted instead is a visual **link**
between the two rows: a shared accent on both, assigned per pair so that several
pairs in one area stay distinguishable, and non-intrusive enough to sit inside an
existing row without crowding the badges, UID, showname and IPID already there.
Hue alone is not sufficient — it fails for colour-blind users and it cannot be
told apart at a glance once there are several pairs — so the accent needs a small
shared glyph or index alongside it. This is a pure client-side rendering change
with no wire or traffic implications, and it is independent of the polling problem
above: it is worth doing whether or not the roster ever learns pairing live.

## ~~Color emoji & supplementary-plane characters render as boxes~~ — RESOLVED

Color emoji now render (per-glyph font fallback, `internal/render/emoji.go` +
`renderRaster` in `internal/ui/screens.go`). The original diagnosis here assumed
SDL_ttf 2.0.18, but the toolchain had since moved to **SDL_ttf 2.24** (+ freetype
+ harfbuzz), which renders COLR/CBDT colour glyphs via the normal
`RenderUTF8Blended` path — so the blocker was never the library, only the app's
single-font rasterizer.

The fix: a message that contains emoji (detected by a cheap per-message byte scan
— supplementary-plane lead bytes, plus the VS16 that promotes BMP emoji like
`❤️`) is split per glyph onto the **system emoji face** (Segoe UI Emoji, read
off-thread on first use) and the chat font, baseline-aligned, reusing the
existing styled-span structure and its 0-alloc draw. Plain messages keep the
untouched single-font fast path, so the perf-sensitive IC/OOC draw and the render
alloc gate are unchanged. Compound sequences (VS16, ZWJ families, keycaps, skin
tones) are absorbed into one emoji run. No SDL_ttf API bump or `fontCovers`
rework was needed. Linux/macOS (no system emoji font wired yet) still fall back
to the chat font; bundling a cross-platform face is the only remaining follow-up.

## ~~Idling with the window minimized dropped the connection~~ — RESOLVED

The symptom: a client left connected with its window minimized lost the socket
after a while, typically noticed the instant the window came back, while
AO2-Client and webAO idled on the same server indefinitely. Four releases
(v1.81.3 through v1.81.6) attributed it to the transport — a watchdog, the
drop-to-lobby path, the keepalive riding the render loop, then reader/pong
starvation. Each fixed a real bug; none of them was this one, and none of them
introduced it either — the regression is much older (see "Why older builds were
immune" below).

**Why the diagnosis kept going wrong: the close frame carries no information.**
Every server-side close path on these servers emits an identical **1000 with an
empty reason**, so the frame means only "some server code called `Close()`" —
never "idle timeout" or "polite shutdown", which is how it was read for four
releases (the wire detail is in PROTOCOL.md, "Rate limits and close codes").
The absence of a reason in fact points the other way: it is the signature of an
automated guard, whose explanation is queued asynchronously and loses the race
against a synchronous close.

**The actual mechanism.** The live-roster poll queued a `/gas` OOC command every
`rosterRefetchDebounce` (3 s, `internal/ui/liveroster.go`) — that poll no longer
exists, see the next entry. Its **producer** ran
in `App.Background`, which keeps running while the window is minimized; the
**drain**, `processOOCQueue`, ran only in `App.Frame`, which does not —
`cmd/asyncao/main.go` continues the loop before ever reaching `Frame` in that
state. The queue therefore filled for the entire occlusion, every entry stamped
due, and released *all* of them in a single pass on the first restored frame.
Servers count OOC per IP and every message kind per client and kick on breach
(see PROTOCOL.md, "Rate limits and close codes"), so the burst bought an
immediate kick — which is why the drop always seemed to land exactly when the
user returned.

**Why older builds were immune.** The regression is `1d759ce` (first shipped in
v1.74.5), which added `a.frameNow = time.Now()` to `Background`. Before it,
`a.now()` returned a **frozen** clock while minimized, so the 3 s debounce could
never elapse and at most one `/gas` was queued for the whole occlusion. That —
not better networking — is why v1.40.0/v1.50.0 could sit minimized all day. The
restamp is correct in itself (every other consumer is a timer or animation clock
that wants real time); it merely removed the accident that had been rate-limiting
the poll.

**Why some servers never showed it.** Akashi registers only `getareas`
(`akashi/src/aoclient.cpp`) — there is no `gas` — so it answered "Invalid
command", the client latched `rosterCmdUnsupported` and disabled the poll
permanently for that session, and the queue never grew. Nyathena registers
`gas`, so the poll ran for the life of the connection. The server-dependence
was a command-registration difference, never a transport difference.

**The fix**, three layers, each sufficient on its own: `processOOCQueue` releases
at most one line per `oocSendMinGap` (1 s) enforced at the **drain**, so a
backlog can never leave as a burst however long the drain was stalled;
`App.Background` drains the queue too, so the send rate is identical whether the
window is focused, unfocused or minimized; and `fetchRoster` skips when a `/gas`
is already pending (`oocQueuePending`), so a slow drain cannot stack duplicates.

Deliberately **not** fixed by reconnecting. A correct client should not be
disconnecting in the first place, so the v1.81.4 behaviour (drop to the lobby,
auto-reconnect off by default) is unchanged.

## The automatic roster poll is gone — no self-updating player list without PR/PU

**Decision, not a defect** (user order, 2026-08-08, from a live session).

The client used to re-pull the all-areas roster (`/gas`) off the server's own
`CharsCheck` / `ARUP` / `PU` pushes on a 3 s debounce (`maybeRefetchRoster`,
`internal/ui/liveroster.go`). On an old KFO hub that answers it with
"You cannot see players in all areas in this hub!" — a line that is neither an
area list nor a recognised command error, so neither the echo suppression nor the
unsupported-command latch caught it — the client pasted that reply into OOC every
three seconds for the whole session. The instruction was to delete the legacy
tier outright rather than teach it another special case: even a server that will
never answer must at least not be spammed.

It also closes the rate-limit class above for good. A timed OOC command whose
producer runs in `Background` is the exact shape that got clients flood-kicked;
the drain-side fix bounded the burst, and with no timed producer left there is
nothing to burst.

**What survives.** The server-pushed PR/PU live roster (unchanged — it is the
roster wherever the server speaks it), and the four fetches a PERSON asks for:
the Players panel's on-open / area-change pull, the "Refresh roster details" menu
row, the mod ban box's `/getareas`, and the one-shot after a successful `/login`.
A server that refuses the command now says so in OOC, once per press, instead of
being silently latched off — with nothing sending it on a clock, the answer to a
button belongs on screen.

**The cost, stated plainly.** A server with no PR/PU (old KFO, the
Athena/tsuserver family) has NO self-updating player list beyond the pushed
CharsCheck/ARUP rows: its UIDs, IPIDs and OOC names refresh only when the user
asks. That is the accepted trade, not an oversight.

Pinned by `TestNoSessionEverFiresATimedRosterPoll` and
`TestOnlyUserActionsFetchTheRoster` (`internal/ui/rosterpoll_test.go`).
