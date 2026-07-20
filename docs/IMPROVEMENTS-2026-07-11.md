# AsyncAO improvement audit — 2026-07-11

Code-grounded improvement list from a 7-scout multi-agent audit (render/pacing,
assets/network/caching, protocol parity vs `../AO2-Client`, UX, reliability,
code health, accessibility) plus an adversarial critic pass that verified every
claim against the tree, merged duplicate findings, and deprioritized weak ones.
62 raw findings → the 53 items below.

- **Item numbers are the canonical reference** ("build #N" from this list —
  distinct from the 95-item idea list of 2026-07-02).
- file:line refs were verified at audit time (HEAD `6307a7b`); re-verify before
  acting.
- Standing constraints apply to every item: `BenchmarkRenderFrame` stays
  **0 allocs/op**, everything bounded with named caps, no SDL off the render
  thread, no sync disk I/O on hot paths. Standing exclusions (raw TCP, stinger
  SFX, data-saver, gamepad, anti-tamper, typing indicator, colored-span log
  raster) were respected — nothing below touches them.

---

## A. Bugs — verified broken today (start here)

### 1. Auto-reconnect never fires on a real network drop — it's wired backwards — SHIPPED v1.57.0-test1
**Impact: high · Effort: small · reliability**
`scheduleAutoReconnect` is called ONLY from the `EventDisconnect` handler, and
`EventDisconnect` is generated ONLY by KK/KB/BD (kick/ban) packets. A genuine
drop (Wi-Fi blip, server restart, read error) surfaces as the Incoming channel
closing, and that path calls `a.Disconnect()` — which CANCELS any pending retry
— and never schedules one. So the exact case the feature was built for
(FEATURES.md: auto-reconnect after "an unexpected drop") never auto-reconnects,
while a BAN retries 8 times (which can read as ban evasion to servers). Fix:
schedule the retry in the closed-channel branch of `pumpConnection`, suppress it
for `Banned:` disconnect texts.
Evidence: `internal/ui/app.go:2898-2908` (socket-close branch: Disconnect, no
schedule), `internal/ui/app.go:3107-3108` (kick/ban branch arms it),
`internal/courtroom/session.go:696-701` (EventDisconnect only from KK/KB/BD),
`internal/ui/reconnect.go:45-50`, `docs/FEATURES.md:878-885`.

### 2. Corrupt cached assets poison themselves forever — wire the T2/T3 purge — SHIPPED v1.57.0-test1
**Impact: high · Effort: small · caching/reliability** *(merged twin finding)*
`DiskCache.Delete` is documented for exactly this flow ("the decoder found the
payload corrupt and the manager wants a clean refetch") but has ZERO callers,
and nothing removes bad bytes from T2 either. A torn download that passed
Content-Length fails decode → `MarkFailed` backs off for `decodeFailTTL` (30s)
→ the retry walks the tiers, hits the SAME corrupt blob (T3 re-promotes to T2),
fails again — forever, across sessions, until the user clears the whole disk
cache. Fix: on a non-transient decode failure, evict the URL from T2 and call
`disk.Delete` so the next demand refetches clean bytes (the negative cache
still paces retries: one refetch per 30s window at worst).
Evidence: `internal/cache/disk.go:205-209` (Delete unused),
`internal/render/pump.go:59-68` (decode error → MarkFailed only),
`internal/assets/manager.go:343-348` (disk hit re-promoted every pass),
`internal/render/textures.go:221-242` (decodeFailTTL).

### 3. A corrupt prefs file is silently replaced with defaults, then overwritten — SHIPPED v1.57.0-test1
**Impact: high · Effort: small · config persistence**
If config.json fails to parse, `load()` returns all-defaults and main just
`log.Printf`s (invisible — release builds are windowed, no console). The corrupt
file is left in place until the first debounced save — seconds after any
interaction — atomically overwrites it with defaults, destroying favorites,
wardrobes, server logins, macros, and learned formats with no recovery copy.
Fix: on parse failure, rename to `config.json.corrupt-<timestamp>` before the
saver can clobber it, and surface a startup notice via the toast/warnLine
machinery.
Evidence: `internal/config/preferences.go:1616-1630` (defaults on parse fail),
`cmd/asyncao/main.go:97-100` (error only logged),
`internal/config/preferences.go:2244-2258` (SaveNow atomically replaces).

### 4. Audio chunk-cache FIFO can Free a chunk that is still playing (UAF) — SHIPPED v1.57.0-test1
**Impact: medium (crash-class) · Effort: small · audio**
`loadChunk` and `PlayFile` evict the oldest cached chunk with unconditional
`old.Free()` once `chunkCacheMax` (64) is reached. A chunk can still be playing
on one of the 16 mixer channels when it becomes the victim (burst of 64 distinct
SFX/blips while a long SFX plays — busy servers with custom-SFX spam make this
reachable). `Mix_FreeChunk` on a playing chunk is use-after-free inside
SDL_mixer's C audio callback: garbage audio at best, a crash at worst, invisible
to `-race` (C thread). Fix: before freeing, scan allocated channels via
`mix.GetChunk`/`mix.Playing` and halt that channel first or rotate to the
next-oldest non-playing victim.
Evidence: `internal/render/audio.go:465-472` and `:517-524` (both eviction
sites, no playing check), `audio.go:22` (mixChannelCount=16), `audio.go:38`
(chunkCacheMax=64), `audio.go:496-498`.

### 5. LocalFetcher never percent-decodes — local mounts break on names with spaces — SHIPPED v1.57.0-test1
**Impact: high · Effort: small · local mounts**
The URLBuilder percent-escapes every path segment regardless of origin, and
`local://` origins feed those escaped paths straight to `LocalFetcher.Fetch`,
which uses the raw rel as a filesystem path. A mounted AO pack with "Phoenix
Wright" (spaces are ubiquitous) is requested as `phoenix%20wright` and
`os.ReadFile` misses — the asset 404s in no-streaming mode even though the file
exists. Fix: `PathUnescape` each segment in Fetch (try the raw path first so
exported scene archives, which write escaped names symmetrically, keep
working), re-checking the `..` guard after decoding.
Evidence: `internal/assets/local.go:70-92` (rel used verbatim),
`internal/courtroom/urlbuilder.go:112-136` (segments escaped unconditionally),
`internal/ui/app.go:3217-3218`.

### 6. Reduce-motion misses transmitted custom paths, hue-cycle, and glitch — SHIPPED v1.57.0-test1
**Impact: high · Effort: ~one line · accessibility/photosensitivity**
The one place transmitted sprite styles are stripped under ReduceMotion clears
only Wobble, Spin, and the named Motion enum. A speaker's CUSTOM drawn path
(PathLen>=2 OVERRIDES Motion, so the sprite keeps looping its motion), HueCycle
(continuous rainbow), and Glitch all survive — including **GlitchStatic**, whose
own doc says "the sprite flickers": the closest thing in the client to a
photosensitivity hazard another player can impose. This contradicts the code's
own comments (spritestyle.go and the picker UI both claim ReduceMotion drops
the path) and the client's own doctrine for animated TEXT (everything pinned
static under reduce-motion). Fix: also zero PathLen, HueCycle, Glitch(+Mode) at
the existing strip; the animating census then also stops burning frames.
Evidence: `internal/courtroom/courtroom.go:714-718` (the strip),
`internal/courtroom/spritestyle.go:44-52` + `:92-97`,
`internal/ui/stylebox.go:687-689`, `internal/render/animtext.go:52-57`
(precedent), `internal/ui/app.go:4311-4314`, `internal/render/viewport.go:589-590`.

### 7. Half-dead connections are undetectable — SendErr has zero callers, no read watchdog — SHIPPED v1.57.0-test1
**Impact: high · Effort: medium · connection lifecycle**
`Session.reply` records only the FIRST failed write into sendErr and silently
swallows every subsequent outgoing packet; `SendErr()` is documented as "the
connection teardown signal" but is never called anywhere. `lastPktAt` is
tracked but only shown in the debug overlay. If the link dies silently (NAT
timeout, no FIN), `ws.Read` hangs forever, CH keepalives buffer into the void,
and the user types into a quiet room for minutes until TCP errors. Fix: poll
`sess.SendErr()` in pumpConnection (non-nil → toast + Disconnect +
auto-reconnect), and add a staleness watchdog — no packet for ~2×
keepalivePeriod fires `conn.Ping` (already implemented, concurrency-safe) and
declares the conn dead on timeout.
Evidence: `internal/courtroom/session.go:462-472`, `internal/ui/app.go:2910`,
`internal/ui/debug.go:60-61`, `internal/protocol/conn.go:206-212`,
`internal/ui/app.go:2881-2886` (write-only keepalive).

### 8. Self-update stages unverified downloads over the exe
**Impact: high · Effort: medium · updater**
The download-and-swap path explicitly skips integrity verification:
`update.VerifyChecksum` is fully plumbed but "no published checksum source yet,
so it's skipped". `Download` accepts ANY 200 body — a captive portal, AV proxy,
or CDN error page returning 200 HTML (or a 0-byte body) is staged and renamed
over the running exe, bricking the install until the user restores the `.old`
backup. Fix: publish a SHA256SUMS asset from release.yml (it already runs
signtool), feed it to VerifyChecksum before StageReplace, and reject staged
files that are tiny or lack PE/ELF/Mach-O magic.
Evidence: `internal/ui/update_ui.go:150-152` (skip admitted),
`internal/update/apply.go:60-94` (no size floor/content check),
`internal/update/apply.go:100-118` (VerifyChecksum ready),
`.github/workflows/release.yml:107-108`.

### 9. Extend the held:// frame bridge to character layers (the last black-flash hole) — SHIPPED v1.57.0-test1
**Impact: high · Effort: medium · render/eviction** *(merged twin finding; the
known gap tracked since v1.56.1)*
The held-frame bridge steals an evicted page's first frame only for scenery:
`IsLiveScenery` deliberately excludes Speaker.Active/Pair.Active. When the
CURRENT speaker's page is evicted mid-display (a cap-sized incoming upload can
force it), drawSprite's miss path has no fallback: hold-previous requires
`lastGood != base` (they're equal — that sprite was just drawn), the thumb path
is opt-in default-OFF, and drawSprite never probes `anim.heldKey` (precomputed
in animState.reset but only drawFill/drawBackgroundDoF call resolveHeld). The
character blanks until the futility-latched heal re-decodes — or forever once
sceneHealAllowed's churn budget (3) is spent. Fix: widen the liveScenery probe
to the drawn speaker/pair bases, grow heldSceneryMax 2→4, add a resolveHeld
probe to drawSprite's miss path (drawHeldSprite already renders a single-frame
stand-in), and add a store-level regression test mirroring heldscenery_test.go
with a speaker base.
Evidence: `internal/render/textures.go:289-301` (steal gate), `:324`
(heldSceneryMax=2), `:330-333` (SetLiveScenery only gate),
`internal/ui/app.go:2496-2521` (IsLiveBase lists speaker/pair; IsLiveScenery
doesn't), `internal/render/viewport.go:1029-1054` (miss path), `:66-69`
(heldKey unused by drawSprite), `internal/ui/app.go:2311-2334`
(sceneHealAllowed), `internal/render/heldscenery_test.go:30-89`.

### 10. One source of truth for the decode-cap / T1-budget ratio + invariant test — SHIPPED v1.57.0-test1
**Impact: high · Effort: small · budget arithmetic** *(merged twin finding)*
"A decoded animation may use half the texture budget" is written independently
in three places: the decoder default (`cache.DefaultT1BudgetBytes / 2`), the
live override in main (`TexBudgetMiB<<20 / 2`), and the render tier split it
must stay compatible with (main tier = budget − budget/8). A single page can be
~57% of the main tier, so one landing page evicts most of the on-screen working
set — the confirmed root arithmetic of the stage-flash class the held:// bridge
patches downstream. Fix: one named helper (e.g. derive from splitT1Budget's
main share, main/3 or main/4) used by both call sites, plus a cross-package
test asserting `maxDecodedAssetBytes(b) <= splitT1Budget(b).main` for default
and edge budgets (cmd/asyncao has no tests today). Frame decimation already
keeps long clips spanning at a smaller cap, so quality cost is minimal.
Evidence: `internal/assets/decoder.go:49`, `cmd/asyncao/main.go:256-260`,
`internal/render/textures.go:55` + `:71-80` (splitT1Budget), `:173-179`
(comment admitting the mismatch).

---

## B. Protocol parity vs AO2-Client (all verified missing)

### 11. Play the MC ambience channel (channel 1+) instead of dropping it
**Impact: high · Effort: medium**
`Session.HandlePacket`'s MC case no-ops any non-zero channel ("We don't render
ambience channels (yet)") — AO2-Client plays channel 1+ as ambient streams
alongside master music. WAP-family servers (umineko.online included) stream
area ambience on channel 1 — including one on every join — so AsyncAO users sit
in silence where AO2 users hear the room. Fix: a second bounded music stream in
render.Audio (own volume slider, same pending/TTL machinery; `~stop` clears).
Zero render-loop cost (playback is SDL_mixer-side).
Evidence: `internal/courtroom/session.go:651-660`,
`internal/render/audio.go:600-623` (single stream); AO2-Client
`src/courtroom.cpp:4749-4778`.

### 12. Honor MS SFX_DELAY + fire preanim screenshake at the SFX moment — SHIPPED v1.57.0-test1
**Impact: medium · Effort: small**
The wire SFX delay is parsed and passed to the audio sink, but `Audio.PlaySFX`
discards the duration (`_ time.Duration`) — its own comment claims "Delay is
honored by the courtroom phase machine", which is false. AO2-Client starts
sfx_delay_timer at wire-value × 40ms so a whip-crack lands mid-preanim; AO2
also fires SCREENSHAKE=1 inside play_sfx for preanim messages, while
fireMessageEffects gates shake to IDLE/ZOOM mods only — a preanim message's
shake is dropped entirely. Fix: a pending-SFX deadline on the courtroom Update
tick (units × 40ms) that plays the SFX and fires the shake for preanim mods.
Evidence: `internal/render/audio.go:562-564`,
`internal/courtroom/courtroom.go:812-815` + `:1003-1005`; AO2-Client
`src/courtroom.cpp:4052-4054`, `:4590-4596`, `src/courtroom.h:428`.

### 13. Handle MU/UM: show muted state and disable IC input — SHIPPED v1.57.0-test1
**Impact: medium · Effort: small**
MU/UM fall through to the default unhandled-debug lane. AO2-Client shows a
"muted" overlay and disables the IC input when the target cid is yours. On
AsyncAO a muted player keeps typing and every send is silently swallowed —
with keep-until-echo the line never clears, which reads as a client bug. Fix:
EventMuted in the reducer, gate the IC send, show a notice chip.
Evidence: `internal/courtroom/session.go:900-905`,
`internal/ui/screens.go:5639-5647`; AO2-Client
`src/packet_distribution.cpp:483-497`, `src/courtroom.cpp:4689-4711`.

### 14. Implement 2.8 additive text — incoming append + outgoing toggle — SHIPPED v1.57.0-test1
**Impact: medium · Effort: medium**
`ChatMessage.Additive` is parsed and OutgoingMS serializes it behind
FeatureAdditive, but nothing consumes it: `begin()` always resets the message
text, and the UI never sets out.Additive (no checkbox). AO2-Client appends when
ADDITIVE=1 and shows the checkbox whenever the server advertises it.
Narration-style RP servers rely on it; today each additive fragment replaces
the previous one. The typewriter can start with a pre-revealed prefix so pacing
and blips only run on the appended tail.
Evidence: `internal/protocol/ms.go:116,179` (parsed, only demoms.go touches
it), `internal/ui/screens.go:5600-5625` (no Additive out); AO2-Client
`src/courtroom.cpp:1638-1644`, `:2292-2295`, `:4187-4192`, `:4225-4230`.

### 15. Honor MC music-effect flags (fade in/out, sync-pos) and the looping field — SHIPPED v1.57.0-test1
**Impact: medium · Effort: medium**
The MC handler reads only fields 0–2 and 4; field 3 (looping) and field 5
(MUSIC_EFFECT bit flags FADE_IN/FADE_OUT/SYNC_POS/NO_REPEAT) are ignored, and
startMusic always plays loopForever. tsuserver `/play` with fade args sets
these flags, so tracks that should crossfade hard-cut, and a no-loop signal
still loops. Fix: short volume ramp driven from the existing `Audio.Frame()`
render-thread tick (no new goroutine), plumb looping/effect ints through
EventMusic; NO_REPEAT maps to Play(0).
Evidence: `internal/courtroom/session.go:647-673`,
`internal/render/audio.go:615-616`, `:419`; AO2-Client `src/datatypes.h:95-101`,
`src/courtroom.cpp:4764-4787`.

### 16. Expanded desk mods: per-phase desk visibility + clamp outgoing 2–5 — SHIPPED v1.57.0-test1
**Impact: medium · Effort: small**
`deskVisible()` collapses desk mods to one static bool per message ("EX modes
refine per-phase later") — mod 2 (DESK_EMOTE_ONLY) wrongly shows the desk
during the preanim, mod 3 (DESK_PRE_ONLY) wrongly hides it during the preanim;
AO2 flips visibility between preanim and talk/idle phases, and mods 4/5 also
hide the pair + zero the offset. Separately, `FeatureExpandedDeskMods` is
defined but never consumed: the UI ships emote.DeskMod raw, while AO2 clamps
2–5 down to hide/show when the server lacks the feature — a strict-validator
compat risk (same class as the KFO/LemmyAO fixes already in ms.go).
Evidence: `internal/courtroom/courtroom.go:1326-1335`,
`internal/protocol/features.go:24` (zero consumers),
`internal/ui/screens.go:5601`; AO2-Client `src/courtroom.cpp:2021-2031`,
`:4075-4091`, `:4134-4152`.

### 17. Frame-synced SFX / realization / screenshake networking (FRAME_* fields) — SHIPPED v1.57.0-test1
**Impact: medium · Effort: large**
FrameShake/FrameRealize/FrameSFX are parsed off the wire and even faked for KFO
compat, but never played — fireMessageEffects' comment admits the gap.
AO2-Client decodes `<emote>^<frame>&<sfx>^` into a per-frame effect map and
fires sound/flash/shake as the sprite reaches those frames; outgoing fills from
the sender's char.ini `_FrameSFX` tags. The viewport already tracks the
animated sprite's current frame index, so a bounded frame→trigger table on the
SpriteLayer + callbacks into existing shake/flash/PlaySFX covers incoming.
Distinct from the ROADMAP effects.ini overlay item (verified).
Evidence: `internal/protocol/ms.go:37-39,176-178`,
`internal/courtroom/courtroom.go:976-978`; AO2-Client
`src/courtroom.cpp:2779-2787`, `:2262-2290`, `src/animationlayer.cpp:472-569`.

### 18. 2.10 slide transitions: consume MSSlide with a viewport pan + Slide toggle
**Impact: medium · Effort: large**
`ChatMessage.Slide` and `OutgoingMS.Slide` exist on the wire but nothing
consumes the incoming flag and no UI sets the outgoing one. AO2's do_transition
pans background/desk between the old and new position origins with easing
(gated on sender SLIDE=1, matching backgrounds, per-position origins from the
background's design.ini, and a client Slide checkbox). AsyncAO already
crossfades speaker swaps, so the frame exists; new work is per-position origin
parsing + an eased scroll offset in the compositor (joins the NoteAnimating
census, respects ReduceMotion).
Evidence: `internal/protocol/ms.go:43,119,182,273,353` (zero consumers);
AO2-Client `src/courtroom.cpp:2996-3125`, `:326-329`, `:1096-1098`, `:2314`.

---

## C. UX

### 19. Lobby server list has no search/filter field
**Impact: high · Effort: small**
On a 100+ entry master list, finding a server means scrolling — while char
select, music, and the pair panel all have memoized search boxes. First screen
every player hits. Reuse the queryCache pattern so the per-frame filter
allocates nothing.
Evidence: `internal/ui/screens.go:64-300` (drawLobby, no filter); contrast
`screens.go:611` (charSearch), `screens.go:3960` (musicsearch).

### 20. Areas tab has no search filter (the Music tab beside it does) — SHIPPED v1.57.0-test1
**Impact: medium · Effort: small**
Hub servers with hundreds of areas are scroll-only; the Music tab in the same
right column already has the exact pattern (search TextField + memoized filter
+ shown/total counter). Port it over `a.sess.Areas`.
Evidence: `internal/ui/screens.go:2882-2986` vs `:3958-3968`.

### 21. Emote grid has paging and a favs filter but no name search
**Impact: medium · Effort: small**
Hundreds of emotes = 12 pages of trial and error. Emotes carry searchable text
(e.Comment/e.Anim) and refreshEmoteView already builds the visible-index list
the favs filter narrows — a query filter slots into exactly that seam, 0-alloc
via the same guard-key rebuild.
Evidence: `internal/ui/screens.go:4739-4855`, `internal/ui/emotefav.go:22-27`.

### 22. "Hide UI pieces" popup overflows short windows — controls unreachable — SHIPPED v1.57.0-test1
**Impact: medium · Effort: small**
drawUICfgPanel computes a fixed ~792px panel centered on h/2 with no scroll or
clamp; at 768p laptop heights the top checkboxes and/or Theater/Done clip
offscreen, unreachable. The hideable lists grow every release, so this worsens.
Add a scroll (like drawHelp) or clamp-and-two-column.
Evidence: `internal/ui/court_extras.go:1022-1031`, lists at `:90-106` +
`:114-143`; MinWindowH=480 at `internal/config/preferences.go:633-634`.

### 23. Rate-limited IC sends are dropped silently
**Impact: low · Effort: small**
sendIC returns early inside the chat_ratelimit window: the text stays in the
input, but nothing says why Enter did nothing — reads as "the client ate my
message" during fast RP. One warnLine toast ("sending too fast") or auto-resend
when the window opens.
Evidence: `internal/ui/screens.go:5502-5506` vs the warnLine idiom at
`:806-814` and `:4714-4722`.

### 24. Evidence editor: image filename typed blind — live thumbnail + suggestions
**Impact: medium · Effort: medium**
Adding/editing evidence takes a raw filename with no validation, preview, or
suggestions — you learn it's wrong after saving. The exact-URL fetch pipeline
exists two functions up (demandEvidence + Store.Get): render a live thumbnail
beside the field (one paced PrefetchExact, 404-cached) and suggest image names
already seen in this session's LE list (a streaming client can't dir-list).
Evidence: `internal/ui/court_extras.go:858-859`, `:787-796`, `:901-908`.

### 25. Layout editors: arrow-key nudge (the pair ghost already has the pattern)
**Impact: medium · Effort: small**
Both the themed editor and the classic slot editor move/resize only by mouse
drag; fine placement needs Shift+drag with a steady hand. The pair-offset ghost
already nudges 1% on arrow keys when no field is focused — add the same (plus
Shift+arrow grid step) to the armed box in both editors, riding
pushLayoutUndo/persist.
Evidence: `internal/ui/layoutedit.go:179`, `:295-347`; the pattern at
`internal/ui/screens.go:5318-5332`.

### 26. Inline "/" command suggestions in the OOC input
**Impact: medium · Effort: medium**
Server slash commands are only discoverable via Ctrl+Space. The per-software
CommandReference table and an autocomplete-chip UI pattern (scene maker) both
exist — show a small chip row of matching commands above the OOC input while
the draft starts with "/", bounded to a handful of rows, drawn only while
relevant.
Evidence: `internal/ui/palette.go:72-84`, `:114-119`;
`internal/ui/scenemaker.go:1114`; `internal/ui/screens.go:2863`.

### 27. Finish the toolbox consolidation (#27 slice 2) — SHIPPED v1.57.0-test1
**Impact: medium · Effort: medium**
The toolbox's own header comment records the unfinished plan: the playtest ask
was "one toolbox, not a separate scuffed menu", but the old Hide-UI dialog must
stay because it alone hosts Theater mode and the themed Edit-layout entry —
show/hide config is split across three places. Add a Theater chip and a
themed-edit entry to the toolbox strip; retire (or shrink) the dialog.
Evidence: `internal/ui/courttoolbox.go:12-17`,
`internal/ui/court_extras.go:1073-1084`.

---

## D. Hardening & tests

### 28. Whole-screen 0-alloc gate for the live courtroom draw — SHIPPED v1.57.0-test1
**Impact: high · Effort: medium**
UI alloc discipline is enforced by many per-widget AllocsPerRun tests — each
added reactively AFTER a per-frame allocation shipped. BenchmarkRenderFrame
gates only internal/render. UI tests already build a real App+Ctx headlessly
and exercise drawCourtroom (tabs_test.go): stage a settled scene, warm up a few
frames, assert `AllocsPerRun(drawCourtroom)==0` (and one for drawLobby). One
test catches the entire class in 6,000-line screens.go forever.
Evidence: `internal/ui/court_ux_test.go:201,230,259,303`,
`internal/ui/reactions_test.go:153-163`, `internal/ui/tabs_test.go:374-420`,
`internal/render/render_test.go:151`.

### 29. Reflection-based save/load parity test for preferences
**Impact: high · Effort: medium**
Saving marshals the struct directly; loading goes through the hand-mirrored
prefsJSON DTO + manual overlay — two parallel field lists that must stay in
sync, and the "saves-but-doesn't-load" class has recurred repeatedly (dozens of
hand-written per-pref round-trip tests exist). Add one reflection test: (a)
every json tag the save side emits exists in prefsJSON; (b) sentinel fixed-point
— write a prefs file with every field set to a non-default sentinel, Load,
SaveNow, diff (allowlist deliberate non-round-trips like the redacted
LoginPass). Future fields missing from the DTO fail immediately.
Evidence: `internal/config/preferences.go:1050-1052`, `:2253`, `:1627`;
per-pref tests at `internal/config/preferences_test.go:324,1077,1102,1166`.

### 30. Fix the release-notes extractor's prefix match
**Impact: medium · Effort: small**
release.yml extracts the tag's CHANGELOG section with
`awk 'index($0, "## " ver) == 1'` — a pure prefix match, so tag v1.56.0 also
matches "## v1.56.0-test1" and whichever section appears first wins; held
together only by an editing convention (stable above -testN). The in-app parser
already compares the first whitespace token exactly. Make the awk exact-token
and add a Go test replaying the extraction against the embedded CHANGELOG.md
for every "## v" header — the ordering convention stops being load-bearing.
Evidence: `.github/workflows/release.yml:295-299` vs
`internal/ui/changelog.go:104-113`, `:24`.

### 31. Migrate interactive scrolled panels from raw Ren.SetClipRect to pushClip — SHIPPED v1.57.0-test1
**Impact: medium · Effort: medium**
Raw SetClipRect clips drawing only; clicks/hovers leak past the clip edge — the
class that shipped the v1.55.8 char-select bug. drawAbout still draws clickable
link Buttons inside a raw clip with a guard that only excludes FULLY offscreen
buttons: one straddling the header edge stays clickable in its hidden half.
21 raw call sites in internal/ui (about, changelog, help, serverhelp, screens,
theme_layout, vpzoom, ...) — audit each for interactive content, convert those
to pushClip/popClip, delete the obsoleted per-button guards.
Evidence: `internal/ui/ui.go:1403-1421`, `internal/ui/about.go:249-294`; grep
`Ren.SetClipRect` for the full list.

### 32. One cross-family keybind conflict scan + fix the F1 macro mislabel
**Impact: medium · Effort: medium** *(merged twin finding)*
Conflict detection covers only hotkeyDefs against itself, but (a) the dangerous
collisions are with out-of-band Ctrl chords handled before the hotkey switch
(Ctrl+C/V/X/A clipboard, Ctrl+Z/Y undo — defaults dodge them purely by
curation, a rebind silently dead-ends), and (b) bare keys are shared by SIX
independent namespaces (macros, jukebox, showname presets, IC quick-phrases,
style presets, char keybinds) dispatched first-match-wins with zero warning at
bind time. Build one scan across all of it (+ emote digits 1-9), surface in
each binder row and Settings like the existing Ctrl-chord flag. Also: the F1
cheat sheet renders every macro as "Ctrl+KEY" when macros fire on BARE keys
only (dispatch rejects ctrlHeld) — following the sheet misfires a different
action. Drop the prefix or mark "bare key".
Evidence: `internal/ui/qol.go:163-181`, `:291-306`, `:117`, `:128-129`;
`internal/ui/macros.go:121-137`, `:479-493`;
`internal/ui/settings.go:3545-3574`; `internal/ui/hotkeysheet.go:62-67`.

### 33. Fuzz the hostile-input parsers — SHIPPED v1.57.0-test1
**Impact: high · Effort: small-medium** *(critic's gap find)*
Zero `func Fuzz` in the repo, yet the wire framing/escaping (`#%$&` decode,
SC/LE double-decode), MS parse, char.ini/theme INI, and extensions.json all
consume attacker-controlled server bytes. Go native fuzzing on these pure-Go
packages is cheap and pins the malformed-server robustness the client's
reliability story depends on.

### 34. In-app byte-budget auto-prune for the T3 disk cache — **ship default-OFF** — SHIPPED v1.57.0-test1
**Impact: high · Effort: medium · caveat from the critic**
T3 is "unbounded (user-clearable)" — multi-GB on a 4000-char server; in-app
remedies are Measure and full Clear only (auto-prune exists only in the
asyncao-cache CLI). The ThumbCache already ships the exact idiom: budget knob +
oldest-mtime sweep on its own worker, run at open and every N stores. Port to
DiskCache (sweep on the async writer goroutine, Settings slider next to Clear).
**Critic's flag: T3's unboundedness is a deliberate spec exception — default
must be 0/unlimited so no user's cache is silently deleted.**
Evidence: `internal/cache/disk.go:57-60`, `internal/ui/settings.go:2109-2125`,
`internal/assets/thumbcache.go:261-301`, `docs/FEATURES.md:1675-1678`.

---

## E. Accessibility & settings ergonomics

### 35. An "Accessibility" gathering section in Settings
**Impact: medium · Effort: small**
Reduce motion (General, mid-tab), OpenDyslexic (General → Fonts), High contrast
(one of six Theme presets), screen-effects toggle (Stage FX), text scaling —
scattered, and the "accessibility" search keyword resolves only to the General
tab as a whole. Add a compact section card near the top of General that hosts
or cross-links them via the existing scrollToSection jump machinery; add
keywords to Theme/Audio.
Evidence: `internal/ui/settings.go:851-868`, `:1230-1244`, `:164-178`,
`:36-48`; `internal/ui/chrome.go:52-56`.

### 36. Char select has no keyboard path
**Impact: medium · Effort: small**
The lobby got full keyboard nav (#18); the very next screen — a 4000-char grid
on big servers — has none: the search field's enter-pressed return is discarded
and the grid has no arrow selection. Minimum: Enter wears the first/only
visible match (mirroring the emote number-key pattern); fuller: arrow-key cell
selection reusing the lobby pattern.
Evidence: `internal/ui/screens.go:611` (enter discarded), `:277-300` (lobby
pattern), `internal/ui/qol.go:245-260`.

### 37. IME composition input (CJK/JP)
**Impact: medium · Effort: medium** *(critic's gap find)*
internal/ui handles sdl.TextInputEvent only — no TextEditingEvent (inline
composition preview) and no SetTextInputRect (candidate-window placement)
anywhere, so IME users compose blind with the candidate popup at the wrong
position. Notable given the umineko.online community.

### 38. Colorblind-safe redundancy
**Impact: medium · Effort: small-medium** *(critic's gap find)*
Grep for colorblind/deuteranopia returns nothing repo-wide — HP bars, the
green/yellow/black server security tiers, and status dots are color-coded with
no shape/pattern redundancy. Add shape/letter/pattern fallbacks (and optionally
a palette preset).

---

## F. Additional verified findings

### 39. Named cap on asset response size in Client.readBody
**Impact: medium · Effort: small · networking**
readBody trusts Content-Length blindly (`make([]byte, n)` for any n>0) and the
unknown-length path reads to EOF unbounded — a hostile/misconfigured host
declaring 8 GiB triggers an immediate multi-GiB allocation inside a 256 MiB
process, ×16 concurrent workers. maxMasterResponseBytes (4 MiB) is the
precedent; add maxAssetPayloadBytes (~64 MiB), reject oversized Content-Length
pre-alloc, LimitReader the unknown path.
Evidence: `internal/network/client.go:417-437`, `internal/network/master.go:26-27`.

### 40. 404 negative cache (1024 entries) thrashes on large rosters
**Impact: medium · Effort: small · networking**
PrefetchChain probes up to 3 spellings per sprite; sparse packs on 4000+-char
servers generate thousands of distinct 404 URLs in one char-select browse — the
LRU evicts entries long before their 5-min TTL, so the same missing URLs
re-probe repeatedly (violating hard rule #6 at exactly the scale it matters).
Entries are tiny; raise to ~16384 or derive from roster size (named).
Evidence: `internal/network/client.go:53-56`, `:200`,
`internal/assets/manager.go:277-302`,
`internal/courtroom/urlbuilder.go:234-239`.

### 41. Host backoff inflates under parallel failures — one blip freezes a host 30s — SHIPPED v1.57.0-test1
**Impact: medium · Effort: small · networking**
recordFailure increments per failed request; 16 workers timing out concurrently
on one origin hiccup push failures to ~16 in a burst — the delay formula
(base << failures-1) saturates at the 30s cap, so a 2-second CDN blip blanks
the whole server's assets for 30s (the "files go missing in waves" class).
Count at most one failure per backoff window (if now < b.until, extend without
incrementing). A genuinely-down host still climbs across windows.
Evidence: `internal/network/client.go:475-488`, `:58-60`,
`internal/network/pool.go:14`, `internal/assets/manager.go:40-43`.

### 42. Markov prefetcher misses bare-named packs and never warms the talk sprite — SHIPPED v1.57.0-test1
**Impact: medium · Effort: small · prefetch**
The predictor warms only `urls.Emote(char, emote, EmoteIdle)` via plain
Prefetch — no EmoteAlts chain, no (b) talk sprite — while the live path
prefetches idle+talk with the full spelling chain. On bare-named packs every
prediction 404s in all formats AND fires reportMissing (a missing-asset warning
for pure speculation); even on prefixed packs the sprite seen FIRST (the (b)
talking loop) stays cold. Fix: hand back (base, alts) or a warm callback using
PrefetchChain for EmoteIdle+EmoteTalk at PriorityLow; suppress reportMissing
for speculative passes.
Evidence: `internal/ui/app.go:3672-3677`,
`internal/assets/prefetcher.go:127-129`,
`internal/courtroom/courtroom.go:725-728`,
`internal/assets/manager.go:517-519`, `internal/ui/app.go:6532-6552`.

### 43. Seed the manifest's FULL fallback order per host (standing owed since v1.0.6)
**Impact: medium · Effort: medium · probe strategy**
SeedLearned records only exts[0]; the manifest's declared order beyond the
first entry is discarded, so when a file deviates from the server's primary
format the one-shot re-probe walks the USER's global FormatList instead of the
server's own declared order — dead probes and possible misses. Extend the
learned table (or a per-host list layer between learned-slot and global
default).
Evidence: `internal/assets/manifest.go:139-162`,
`internal/assets/resolver.go:31`, `:143-149`,
`internal/assets/manager.go:541-551`.

### 44. Blocking dials freeze the whole UI (10s manual, 4s per reconnect attempt)
**Impact: medium · Effort: medium · reliability**
protocol.Dial runs inline on the frame loop: a manual Join blocks the render
thread up to dialTimeout (10s) against a black-holed server — the window ghosts
("Not Responding"), no "Connecting..." can even paint; pollAutoReconnect
freezes the lobby up to 4s per backoff attempt (×8). Fix: dial on a goroutine,
deliver the *Conn through a bounded channel polled per frame (the lobbyResult
pattern), with a "Connecting to X... (Cancel)" state. Session setup stays on
the frame loop after delivery.
Evidence: `internal/ui/app.go:2563-2565`, `:2601`,
`internal/protocol/conn.go:16-17`, `internal/ui/reconnect.go:98-117`.

### 45. Partial-handshake hang: "Handshaking..." forever, no watchdog
**Impact: medium · Effort: small · reliability**
A server that accepts the WebSocket but stalls the AO handshake (never sends
PN, or SI but never DONE) leaves a static "Handshaking with server..." label
indefinitely — no elapsed time, no stuck-phase hint (only the F8 overlay knows),
no timeout; the only exit is Disconnect. Add a watchdog: ~20s without a phase
transition → "Server stuck at <phase>" + Retry button; optionally auto-abort
into the lobby's Reconnect flow so restore-on-launch tabs can't wedge.
Evidence: `internal/ui/screens.go:602-609`,
`internal/courtroom/session.go:19-25`, `:499-507`, `internal/ui/debug.go:60-68`.

### 46. A parked tab dying is near-silent
**Impact: medium · Effort: small · multi-tab**
A backgrounded tab's socket closing marks it dead with only a debug-lane line;
a kick/ban on a parked tab appends one OOC line without bumping unread.
Modcalls and friend messages on parked tabs toast — a full disconnect does not,
so a background server dying looks like the room went quiet. Fix: bump t.unread
+ fire the existing toast/OS-notification on both parked-death branches;
consider extending auto-reconnect to parked tabs.
Evidence: `internal/ui/tabs.go:341-351`, `:428-433`; contrast `:417-425`.

### 47. No session resume after reconnect — re-pick char, re-walk to your area
**Impact: medium · Effort: medium · reconnect**
Every reconnect lands on ScreenCharSelect with a fresh session — character,
area, position gone, even though the session knew MyCharID and the roster
tracked our area. Fix: snapshot (charID, area, pos) on unexpected drop; after
PhaseReady on a reconnect to the same URL, re-send PickCharacter + the area MC
(or a one-click "Resume as <char> in <area>" bar), falling back to char select
if the slot is taken.
Evidence: `internal/ui/app.go:2620-2626`, `internal/ui/reconnect.go:103-124`,
`internal/courtroom/session.go:391-397`, `:953-970`.

### 48. Account variant/paint pages and pinned/held bytes in the texture budget
**Impact: medium · Effort: medium · budget accounting**
Style-variant pages (invert/grayscale/silhouette/hue-paint, up to 10 per base,
each up to full page size) are never charged to any tier budget (LRU cost fixed
at page.bytes on Add, pre-variants); pinnedBytes (theme chrome + held://) has
writers but no reader. Real GPU residency can exceed TexBudget invisibly — the
same class as the "all emote buttons identical" GPU-exhaustion incident.
Minimum: fold pinnedBytes + a variants counter into Stats() so F8 tells the
truth; better: charge variant bytes to the owning tier.
Evidence: `internal/render/variant.go:67-87`, `:42`,
`internal/render/textures.go:107`, `:557-569`, `:164`.

### 49. Reuse one render-target texture across buildVariant's frame loop
**Impact: low · Effort: small · render**
readbackFrame creates a TEXTUREACCESS_TARGET texture, does a full-pipeline-sync
ReadPixels, and destroys the target — once PER FRAME, all inside one
render-thread call: first draw of a styled 30+ frame sprite = 30+ target
create/destroys + 30+ GPU syncs in one frame. Frames share W/H — hoist one
target across the loop; optionally time-slice the build (the caller already
draws the base unstyled on (nil,false)).
Evidence: `internal/render/variant.go:111-140`, `:146-151`, `:166`, `:25`.

### 50. Surface pump errors, held-bridge steals, and the pacer's tier reason in F8 — SHIPPED v1.57.0-test1
**Impact: low · Effort: small · diagnostics**
Pump.uploadErrs/transientErrs exist "for debug visibility" but only a test
reads them; the held:// bridge has no steal/release counter (v1.56.1 live
verify relies on eyeballs); the frame pacer — whose census pattern has
regressed repeatedly — has no readout of WHY it's at its tier. Add one pump
line, one bridge line, one pacer line to the F8 panel so the next latch or
asset-wave incident is diagnosable from a playtester screenshot.
Evidence: `internal/render/pump.go:23-27`,
`internal/ui/debugpanel.go:235-259`, `internal/ui/app.go:4549-4560`,
`:4602-4640`, `internal/render/textures.go:343-374`.

### 51. Decompose drawICControls (667 lines) and the other hot-file monsters — SHIPPED v1.57.0-test1
**Impact: medium · Effort: large · code health**
drawICControls spans screens.go:4062-4729 — layout math, input handling, and
send logic in one every-frame function; App.Frame is 426 lines; three settings
tab bodies are ~540-580 lines each (the shape that bred the settings-form-origin
shadow-pad bug class). Split drawICControls first (shout row, color strip,
input row, toggles) with pure layout/logic helpers extracted for unit tests;
helpers take rects by value. No behavior change, no new per-frame cost.
Evidence: `internal/ui/screens.go:4062-4729`, `internal/ui/app.go:5723-6149`,
`internal/ui/settings.go:756-1303,2345-2883,2928-3512`.

### 52. volumeRow/numberRow: join the gather-search + give volumeRow wheel support — SHIPPED v1.57.0-test1
**Impact: low · Effort: small · settings consistency**
The #26 gather-search index builds from c.onRow callbacks, which Checkbox and
sliderRow emit — but volumeRow and numberRow never call it, so "master volume"
can't list/flash-jump like other settings. And volumeRow is the only value-row
helper without wheel-over-row stepping (numberRow and sliderRow both have it;
one bespoke row hand-rolled it) — the five volume sliders + per-char blip
volumes are drag-only. Two tiny additions to two shared helpers fix every call
site.
Evidence: `internal/ui/settings.go:4245-4253`, `:4257-4278`, `:4294-4308`,
`:958-961`, `:2715`; `internal/ui/ui.go:1615-1618`.

### 53. Repay docs/dependency drift + delete the stray NUL file
**Impact: medium · Effort: small · docs/code health**
Two deps lack the required docs/ARCHITECTURE.md justification: klauspost/
compress (zstd — imported directly by the T3 disk cache yet marked
`// indirect` in go.mod: tidy drift) and libopus CGO (internal/voice).
docs/BENCHMARKS.md omits three existing benchmarks (BenchmarkDiskZstd,
BenchmarkAnimatedTextDraw, BenchmarkClientInputSnapshot) despite "Keep this
file current". Delete the stray untracked `NUL` file at the repo root (Windows
reserved-device name from a bash-style `> NUL` redirect). Consider a CI step or
test asserting go.mod is tidy.
Evidence: `internal/cache/disk.go:15` vs `go.mod:20`,
`docs/ARCHITECTURE.md:308-321`, `docs/BENCHMARKS.md:11-30`,
`internal/cache/cache_test.go:296`, `internal/render/animtext_test.go:68`,
`internal/ui/tabs_test.go:425`; `git status` shows the untracked `NUL`.

---

## G. Deprioritized by the critic — don't build without new evidence

- **TCP/TLS preconnect at server connect** — claim true (PreResolve is
  DNS-only) but benefit speculative: the 16-lane transport parallelizes
  handshakes at first burst anyway, idle lanes get reaped by CDNs, and
  speculative HEAD probes would feed the 404 cache/backoff unless routed around
  them. Measure cold-load first.
- **First-join coach marks** — nothing broken, no playtest ask; F1/help/palette
  exist. The backlog is playtest-driven.
- **Pump.carry named cap** — "unbounded" is overstated: growth is structurally
  bounded upstream (bounded pool + decode queue + 64-cap channel). A rule-4
  cosmetic tidy at most.
- **Post-downscale decimation re-budgeting** — the native-size-budget claim is
  real but the 3-4x figure is unmeasured, and it rewires the freshly-stabilized
  v1.55.0-test17 decimation path + the CGO webp path. Regression risk > unproven
  win.
- **Time-slicing live uploads** — the hitch is inferred, not measured
  (uploadNsEWMA exists — cite a number first); live pages are deliberately
  never deferred per spec §8, and slicing the on-screen message's upload risks
  fighting wait-mode's residency probe.
- **TI clock RTT compensation** — verified accurate (the TI comment admits it;
  Conn.Ping exists) but the error is at most half an RTT on a countdown clock.
  Real, tiny.

## H. Leads worth investigating (no scout covered them)

- **T3 disk-writer failure behavior** — what does the single async writer do on
  ENOSPC / locked files? Silent drop, log flood, or repeated rewrites? Distinct
  from (and prerequisite to) #34.
- **Master-list outage lobby UX** — favorites synthesize offline via
  DirectEntry (`internal/network/master.go:78-93`), but nothing persists the
  last-good full master list, so a master-server outage empties the lobby for
  every non-favorite server.
