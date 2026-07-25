# Known issues / future work

Tracked limitations that need more than a localized fix.

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

**The actual mechanism.** The live-roster poll queues a `/gas` OOC command every
`rosterRefetchDebounce` (3 s, `internal/ui/liveroster.go`). Its **producer** runs
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
(`akashi/src/aoclient.cpp`) — there is no `gas` — so it answers "Invalid
command", the client latches `rosterCmdUnsupported` and disables the poll
permanently for that session, and the queue never grows. Nyathena registers
`gas`, so the poll runs for the life of the connection. The server-dependence
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
