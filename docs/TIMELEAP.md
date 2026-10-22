# TimeLeap (`TL`) — AsyncAO Client Notes

> Client: **AsyncAO** (Go, SDL3, aolib-go v2.6.1)
> Server: **Nyathena** · Sibling client: **LemmyAO**
> Status: **groundwork landed** on branch `timeleap`. POC: save/restore.

**Companion documents**

| Repo | Document |
|---|---|
| AsyncAO (this repo) | `docs/TIMELEAP.md` |
| Nyathena (server, protocol authority) | <https://github.com/SyntaxNyah/Nyathena/blob/timeleap/docs/TIMELEAP.md> |
| LemmyAO (TS client) | <https://github.com/SyntaxNyah/LemmyAO/blob/timeleap/docs/TIMELEAP.md> |
| Background | `docs/GROUP_PAIRING.md` |

The server document is the normative one: wire schema, field semantics,
restore modes and the capability handshake all live there. This file covers what
AsyncAO does with them, and what is still missing.

---

## 0. TL in one paragraph

`TL` is a JSON-only custom packet the server sends on demand. It carries an
entire room snapshot — area identity, background, side, music, status, evidence,
testimony, the cast's render state, and a structured IC transcript. AsyncAO
registers the header through `aolib.RegisterPacket`, receives it through the
conn's `OnCustom` hook, converts it into a render model, and re-stages the active
`Courtroom` from it. It is **second-class**: nothing in `aolib/spec/` changed and
there is no conformance vector.

---

## 1. Status

Branch `timeleap` (base `d2923aa`):

| Commit | What |
|---|---|
| `cf4ea1b` | `fix(fl): send the capability advert positionally` |
| `ad55f34` | `feat(tl): receive TimeLeap snapshots` |
| `8ae181f` | `feat(tl): re-stage the room from a snapshot` |

Tests: `TestAdvertiseFeaturesEmitsPositionalPacket`,
`TestAdvertiseFeaturesTypedPathIsAlsoPositional`,
`TestAdvertiseFeaturesSkipsUnsupported`, `TestHandlePacketTL`,
`TestHandlePacketTLClearsOnUntyped`, `TestTimeLeapFromNonTLPacket`.

---

## 2. Why the FL advert had to change (read this before touching FL)

AsyncAO used to reply to the server's FL with a **typed** packet:

```go
s.reply(protocol.NewTypedPacket(&aolib.FL{Features: []string{protocol.FeatureGroupPair}}))
```

Once `decryptor#JSON` flips `jsonMode` (`inbound.go`), `Conn.Send` encodes typed
packets as **JSON** (`conn.go`). The server models FL as server→client only, so
the client's advert lands in its unknown-header shim, which parsed every frame
with a `#`-splitter — and a `#`-splitter returns a **nil body without an error**
for a JSON object. The advert therefore registered nothing, and actively cleared
whatever the client had advertised before.

GP survived this only because `supportsGroupPair()` ignores FL entirely. TL
cannot: its gate is `JSONMode() && SupportsFeature("timeleap")`.

The fix is positional, and it is one helper:

```go
func (s *Session) advertiseFeatures() {
	var want []string
	if s.Features.Has(protocol.FeatureGroupPair) { want = append(want, protocol.FeatureGroupPair) }
	if s.Features.Has(protocol.FeatureTimeLeap) { want = append(want, protocol.FeatureTimeLeap) }
	if len(want) == 0 { return }
	s.reply(protocol.NewPacket("FL", want...)) // typed == nil => FL#...#%, even in JSON mode
}
```

`Packet` with `typed == nil` takes the `p.String()` branch in `Conn.encode`
(`conn.go`), so the frame stays FantaCode in both wire modes. One frame carries
every supported feature, so an advert can never erase a previous one.

Both dispatch paths call it: `handleTyped` (`case *aolib.FL`) and the positional
`HandlePacket` switch (`case "FL"`).

---

## 3. Receive path

```
aolib session (JSON frame)
  -> decodeCustom("TL")                packetutil.RegisterTimeLeapCodec
     -> OnCustom("TL")                 inbound.go
        -> Packet{Header:"TL", typed:*TLPacket}
           -> HandlePacket -> case "TL"          session.go
              -> protocol.TimeLeapFromPacket    protocol/timeleap.go
                 -> Session.TimeLeap set + EventTimeLeap
                    -> App.handleSessionEvents   ui/app_connect.go
                       -> App.applyTimeLeap      ui/app_timeleap.go
```

`Session.TimeLeap` is nil when no snapshot is active; an untyped `TL` frame
clears it (the room returns to live). This mirrors GP exactly — `Session.GroupPair`
plus `EventGroupPair` — so there is one mental model for both extensions.

---

## 4. Files

| File | Role |
|---|---|
| `internal/packetutil/tl.go` | `TLPacket`/`TLState`/… and `RegisterTimeLeapCodec` (JSON-only) |
| `internal/protocol/timeleap.go` | `TimeLeap`, `TimeLeapMember`, `TimeLeapLine`, `FromTL`, `TimeLeapFromPacket` |
| `internal/protocol/conn.go` | `registerCodecs()` now also registers TL (inside `registerCodecsOnce`) |
| `internal/protocol/inbound.go` | `OnCustom("TL", …)` beside GP |
| `internal/protocol/features.go` | `FeatureTimeLeap = "timeleap"` |
| `internal/courtroom/session.go` | `EventTimeLeap`, `Session.TimeLeap`, `case "TL"`, `advertiseFeatures` |
| `internal/courtroom/fl_advert_test.go` | the advert must stay positional |
| `internal/courtroom/timeleap_test.go` | reducer + accessor |
| `internal/ui/app_timeleap.go` | re-stages the active room |
| `internal/ui/app_connect.go` | dispatches `EventTimeLeap` |

Registration runs inside `registerCodecsOnce` (`conn.go`), not in `init()`: the
registry is global and not concurrency-safe, and AsyncAO dials lazily.

---

## 5. What a restore does

`applyTimeLeap` re-uses the paths that already exist rather than adding a second
renderer:

1. **Background + side** — `room.HandleEvent(EventBackground{Text: bg, Name: side, Wipe: bg != ""})`.
   The wipe re-stages the viewport, so everything after it lands on a clean stage.
2. **Music** — `room.HandleEvent(EventMusic{...})` with the snapshot's loop flag.
3. **Transcript** — one `Courtroom.RestoreMessage(&protocol.ChatMessage{...})` per
   line, oldest first. `RestoreMessage` is already a *settled* replay with sound
   and flashes suppressed, which is exactly what a rewind needs.
4. **Cast** — not yet placed per member (see P0-2).

Empty transcript lines are skipped: `RestoreMessage` settles a message, and an
empty one would clear the stage the log just built.

---

## 6. Building and testing

Build with the toolchain recipe in `BUILDING.md` → **"Windows (MSYS2 / UCRT64)"**
(it sets `PATH`, `CC`, `CGO_CFLAGS`, `CGO_LDFLAGS` and `PKG_CONFIG_PATH` for you).
Then, from the repo root:

```powershell
go build ./internal/protocol ./internal/packetutil ./internal/courtroom ./internal/ui
go test ./internal/protocol ./internal/packetutil ./internal/courtroom -count=1
go test ./internal/courtroom -run 'TL|Advertise' -count=1 -v
```

`go test ./internal/ui` takes ~50 s and is worth running when the event handling
changes. A `go build ./...` without the env above fails at `runtime/cgo` — that is
the toolchain, not the code.

---

## 7. Known limitations (POC)

| Limitation | Impact |
|---|---|
| Cast not laid out per member | only the last speaker ends up staged; other sprites keep their live positions |
| Evidence/testimony ignored | snapshot carries them, nothing applies them |
| No meter | the divergence value is parsed and stored but never drawn |
| Authoritative vs overlay look identical locally | the mode only describes what the *server* did |
| No way to trigger a save/restore from the UI | `/tl` is typed in OOC like any other command |
| Replay is silent | `RestoreMessage` suppresses audio by design |
| Replay does not touch tabs | only the active room is re-staged |

---

## 8. Next steps (detailed)

### P0 — make the POC behave like the feature it claims to be

**P0-1 Cast layout.** Place every snapshot member at its recorded
`offset`/`flip`, not just the last speaker. The pieces exist: `protocol.TimeLeapMember`
carries `OffsetX/OffsetY/Flip/Order`, and the renderer already positions extra
sprites for GP (`renderGroupPeers`' approach — a container per member, `left/top`
from the offset, `zIndex` from the order). Options: (a) teach `Courtroom` a
`StageSnapshot` that draws members without a message; (b) mirror the GP approach
in a `tl` container. Prefer (a) so TL and a static "scene maker" share one path.
Acceptance: a two-character snapshot restores both sprites at the right
positions with the log's last speaker in front.

**P0-2 Evidence + testimony.** Apply `area.evidence` and `area.testimony` on
restore, or delete them from `packetutil.TLArea` so the schema stops promising
them. If applied: the `LE`/`RT` reducers already exist; reuse them so the
evidence list and testimony badge match a live update.

**P0-3 A control surface.** Today `/tl` must be typed into OOC. Add a small
command palette / menu entry ("Rewind room…") that lists snapshots and calls
`/tl <id>`, plus a save dialog. This is the difference between a demo and
something a player will use. Keep it behind the existing `timeleap` capability so
it never appears against a server that lacks the feature.

**P0-4 Regression test for the advert in a real Conn.** The current tests assert
the *packet* is positional; they do not assert that `Conn.Send` writes `FL#...`
while `jsonMode` is on. Add a test around `Conn.encode` with a fake writer so the
original bug cannot come back through a future `NewTypedPacket` refactor.

### P1 — fidelity and polish

**P1-1 Overlay indicator.** In `overlay` mode the client shows an older scene than
the server's live state. Render a small "rewound" badge (and the snapshot label +
`divergence`) so a player is never confused about which world they are looking at.
Add a "return to live" action that clears `Session.TimeLeap` and re-stages from
`Session.Background` / `LastIC` (the reducer already keeps those).

**P1-2 A native divergence meter.** LemmyAO draws one on a canvas; AsyncAO is
SDL, so this belongs in `internal/render` (a small sprite/text widget using the
existing texture store). Until then, show the value as text in the rewind badge.
Any tube artwork must be operator-supplied, not bundled — see the LemmyAO doc's
licensing note.

**P1-3 Snapshot-aware tabs.** `applyTimeLeap` only touches `a.room`. Decide
whether the split view (`a.splitRoom`) should follow, and whether a backgrounded
tab should be re-staged when it becomes active (it currently is not).

**P1-4 Replay pacing.** 256 `RestoreMessage` calls in a tight loop may be
noticeable on slow machines. Measure, then either cap the replay (e.g. last 40
lines) or add a "skip animation" fast path that stages only the final message.

**P1-5 Settings.** A toggle for "show TimeLeap overlay on restore" and a
preference for how many transcript lines to replay, in the existing preferences
screen. Not required for correctness, but players will ask.

### P2 — housekeeping

* Cross-reference `docs/GROUP_PAIRING.md`: GP has the same advert bug and now
  benefits from the same helper; note it there so nobody re-introduces a typed
  advert.
* `docs/PROTOCOL.md`: add TL to the "custom headers" list with a pointer to the
  Nyathena doc.
* `docs/KNOWN-ISSUES.md`: record the "cast layout not restored yet" limitation so
  it is not mistaken for a regression.
* `docs/ROADMAP.md`: park D2 (forward travel) and the meter as future work.
* If `registerCodecsOnce` ever becomes per-connection, revisit P0-4's test: the
  once-guard is what keeps a second Dial from re-writing the registry under a live
  read loop.


