# CLAUDE.md — AsyncAO

Guidance for Claude (and humans) working in this repository. Read this before
touching anything.

> "The spec" / "spec §N" in code comments refers to the original engineering
> spec (PROMPT.md), removed from the working tree at project completion.
> Read it any time with: `git show fab9392:PROMPT.md`

> **Restructuring in progress.** The App split / SDL3 move follows the approved
> plan in `docs/RESTRUCTURING_PLAN.md`. Read its **Status** section before any
> structural change and continue at the first unchecked step.

## What this is

AsyncAO is a **maximum-performance Attorney Online 2 client in Go** (CGO for
SDL2 + libwebp). Two pillars: **zero-fallback asset streaming** (exactly one
network probe per asset by default, learned formats per server) and **every
millisecond counts** (lock-free hot paths, three cache tiers, zero-allocation
render loop, 256 MiB memory budget). Plus full AO2 ≥ 2.6/2.8 **pairing**.

WebSocket-only. **Legacy raw-TCP AO servers are deliberately unsupported** —
the lobby shows them black, pinned to the bottom, with an upgrade note. Never
add TCP framing back.

## Hard rules (violations get rejected — spec §17, kept in force)

1. **No SDL calls off the render thread.** SDL is reached through our own
   binding packages, `internal/sdl`, `internal/ttf` and `internal/mix`; nothing
   else imports go-sdl2. Only `internal/render`, `internal/ui` and `cmd/asyncao`
   may import the binding, and everything there runs on the locked main thread.
   `internal/assets`' decoder outputs plain `image.RGBA`.
2. **No synchronous disk I/O** on render/decode/resolver paths. Preferences
   use the debounced saver; the disk cache has one async writer goroutine.
3. **No per-asset preference writes** — learning marks dirty, the saver flushes.
4. **No unbounded** goroutines, channels, queues, or caches. Everything has a
   named cap.
5. **No mutex on the resolver read path** — `atomic.Pointer` snapshot only;
   writes are copy-on-write + CAS.
6. **Never re-probe a cached 404** inside its TTL; singleflight collapses
   duplicate in-flight fetches.
7. **Results are never dropped or stolen.** Backpressure sheds *speculative
   jobs* (oldest low-priority), and every accepted job's `Run(stale)` is
   invoked exactly once.
8. **Race-detector clean.** `go test -race ./...` must pass before any commit.
9. **No magic numbers, ever** — production and test code alike. Every number
   is a named `const` (or an `iota` enum when the values form a set), never a
   variable; local `const` when one function uses it. Exemptions, and only
   these: `0`, `1`, `-1` as structural values (empty, start, offset, sentinel);
   `2` for halving/doubling in geometry (`(w-tw)/2`); values inside a named
   data table or struct literal whose table and field names already say what
   they mean (theme preset RGB rows, AO-spec index tables). Enforced by
   golangci-lint's `mnd` linter, opted in per package: a package joins the
   enforced set once clean and never leaves it.
10. **No push, no PRs from automation.** Local commits only; the user pushes.
11. **Tests prove behaviour through the public API.** No `go/ast` or
    source-scanning tests: a test that parses this package's own files to
    pin where, or in what order, production code calls something is pinning
    implementation, not behaviour, and is rejected. Invariants are enforced by
    compiler boundaries (`internal/`, unexported types, and compile-time checks
    `var _ I = (*T)(nil)`) or behavioural tests — never by a test that parses
    source. No comment cites a test file by name; such a citation rots and is
    deleted with the test it points at.

    **Permanent exception — the HDID privacy gate** (`internal/ui/hdidgate_test.go`):
    the per-server HDID is derived from the address being dialled, at the call;
    every wrong answer is a well-shaped id, so no behavioural test can distinguish
    it. It is keyed by function name for any receiver, and is removed the day the
    id becomes a value the session constructor is handed. Keep the HDID derivation
    inside `internal/ui` so this gate keeps seeing it.

    **Legacy, being retired:** five more source-scan tests still exist
    (`srcgate_test.go`, the `allocgate_test.go` census, `themeeditorseam_test.go`,
    `themeeditorpreview_test.go`, `zerowidthlanes_test.go`). Each is converted to a
    behavioural test or deleted before the restructuring step that would break it;
    no new ones may be added.

12. **KISS, DRY, dependency injection** — for every type in every package.
    A type exists only if it owns behaviour or is plain data with one meaning;
    no forwarding methods; no interface with a single implementation unless it
    crosses a package boundary or breaks a cycle. One implementation per concept:
    shared value coercion and parsing live in `internal/helper/<category>`
    (`value`, `text`, `parse`); Go `slices`/`maps` replace hand-rolled loops; a
    config range is clamped in `normalizeSettings` only. DI is constructor
    injection where an object has ≥2 implementations (test fakes count) or
    crosses a boundary; no setters after construction except documented cycle
    breaks; no mutable package globals (`sync.Once` registries and constant
    tables excepted); `cmd/asyncao/main.go` is the single composition root.
13. **Common names only.** Identifiers, comments, commit messages and docs use
    the plain words a developer new to the project would expect — no invented
    jargon or metaphors (not "seam", "lift", "ratchet", "latch", "census",
    "teeth", "oracle", "slot", "mailbox"). The preferred-term table lives in
    `docs/RESTRUCTURING_PLAN.md` (Part 2c). Existing jargon is renamed when its
    code is touched anyway, never in a rename-only sweep.

## Build & test commands (Windows dev box)

```powershell
# one-time deps (MSYS2 UCRT64 + SDL2 + libwebp)
powershell -ExecutionPolicy Bypass -File scripts\setup-deps.ps1
# build + stage DLLs
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Release
```

For raw `go` commands **PREPEND** `C:\msys64\ucrt64\bin` to PATH — never
append it. `C:\ProgramData\mingw64\mingw64\bin` on the system PATH ships a
rival libwinpthread/libgcc_s_seh: with ucrt64 appended, cc1.exe loses the DLL
race and dies silently (STATUS_DLL_NOT_FOUND, cgo.exe exit 2) and race-built
test exes abort at startup (TSan "failed to allocate ... error code: 87").
Then set `CGO_ENABLED=1`,
`CC=C:\msys64\ucrt64\bin\gcc.exe`, `CGO_CFLAGS=-IC:\msys64\ucrt64\include`,
`CGO_LDFLAGS=-LC:\msys64\ucrt64\lib`,
`PKG_CONFIG_PATH=C:\msys64\ucrt64\lib\pkgconfig`.

```bash
go test -race -count=1 ./...                       # full gate
go test -run=NONE -bench=. -benchmem ./...         # alloc gates (see docs/BENCHMARKS.md)
gofmt -w . && go vet ./...                         # before every commit
```

Pure-Go leaf packages (cache, config, network, protocol, theme, …) test with
`CGO_ENABLED=0`, but `assets` (libwebp/libavif decode) and everything that
imports it — `courtroom`, `logbrowser`, `archive`, `render`, `ui` — needs cgo.
SDL packages use the dummy video driver headlessly (`SDL_VIDEODRIVER=dummy`;
they skip if SDL is unavailable).

`internal/mix` links SDL Mixer X (our fork), which `setup-deps.ps1` does not
install. Until the SDL3 move removes it, build it once from an MSYS2 UCRT64
shell, with Git for Windows on PATH (the login shell does not inherit it):
`MSYSTEM=UCRT64 /c/msys64/usr/bin/bash.exe -lc 'export PATH="$PATH:/c/Program Files/Git/cmd"; pacman -S --noconfirm --needed mingw-w64-ucrt-x86_64-{opus,opusfile,cmake,ninja} && bash <repo>/scripts/build-sdl-mixer-x.sh /ucrt64'`

## Package map

| Package | Role | SDL? |
|---|---|---|
| `internal/safepath` | the ONE copy of the zip-slip / symlink / `..` / depth guards (shared by mounts and theme bundles) | no |
| `internal/config` | preferences + debounced atomic saver; favorites; local mounts | no |
| `internal/cache` | ByteBudgetLRU (T1/T2) + async disk tier (T3, xxhash full-URL keys) | no |
| `internal/network` | dedup HTTP client (singleflight, 404 TTL, backoff, DNS warm), priority worker pool with epoch cancellation, master list + tiers + direct connect | no |
| `internal/assets` | AssetType enum, lock-free resolver (learned formats), tier-walking manager, decode pool (magic-byte sniffing), WebP CGO binding + fallback, local mounts, Markov prefetcher | no (decoder is SDL-free by rule) |
| `internal/protocol` | AO wire (delegated to aolib-go: FantaCode + JSON), MS 2.6/2.8 parse/build, pairing semantics, FeatureSet, WebSocket conn | no |
| `internal/packetutil` | thin bridge over aolib-go: FantaCode primitives, enum maps, typed-parser dispatch, VS_*/MS both-wire codecs | no |
| `internal/courtroom` | session handshake reducer, message lifecycle state machine, typewriter pacing, URL conventions, char.ini | no |
| `internal/theme` | AO2 theme INIs (design/fonts/sounds), asset lookup ladder | no |
| `internal/metrics` | 1 Hz sampler (heap, GC p99, hit rates), cold-load report | no |
| `internal/sdl`, `ttf`, `mix` | our SDL binding: `sdl`/`ttf` forward to go-sdl2 (aliased types, same names), `mix` binds SDL Mixer X; reimplemented over SDL3 in plan step 2c | **yes** |
| `internal/render` | TextureStore (T1), viewport compositor, message raster, upload pump, SDL_mixer audio | **yes** |
| `internal/ui` | immediate-mode kit + screens (lobby/phone book, char select, courtroom, settings, about) | **yes** |
| `cmd/asyncao` | flags, GOMEMLIMIT, LockOSThread, SDL init, main loop | **yes** |

## Reference repos (read-only, sibling directories)

- `../AO2-Client` — **canonical protocol & courtroom semantics. It wins every
  conflict.** Key files: `src/datatypes.h` (CHAT_MESSAGE enum),
  `src/courtroom.cpp` (pairing/emote-mod/queue), `src/packet_distribution.cpp`
  (handshake), `src/network/websocketconnection.cpp` (framing).
- `../webAO` — asset URL conventions (`characters/<x>/(a)<emote>.<ext>`,
  `background/`, `sounds/{music,blips,general}`; PNG sprites are unprefixed).
- `../AO-SDL`, `../ferris-ao-switch` — SDL2 thread-model references.

Protocol gotchas already encoded in code/tests — don't "fix" them:
- Pair order `^0` = **speaker in front**, `^1` = speaker behind (the spec's
  table was inverted; AO2-Client wins — see `internal/protocol/pairing.go`).
- Outgoing MS is **asymmetric** to incoming: the client never sends
  other_name/other_emote/other_offset/other_flip (server injects them).
- Fields ≥ 15 are honored only with the `cccc_ic_support` feature.
- Wire escaping: `#%$&` → `<num>/<percent>/<dollar>/<and>`, decode after
  `#`-split; SC entries get an extra decode after `&`-split (AO legacy).
- `.webp.animated` does not exist. Animation = VP8X ANIM flag at decode time.
- Loading is **client-initiated**: the server only sends SI in reply to our
  `askchaa` (sent on PN). Only the askchar2 *paging* is legacy — drop the
  askchaa reply and every server hangs at "handshaking".
- Sprites have two spellings: `(a)X`/`(b)X` **or** bare `X` (AO2-Client
  CharLayer::load_image probes both). `PrefetchWithFallback` owns that
  chain; the prefixed base stays the asset's identity everywhere.
- MS evidence ids are shifted by 1 on the wire (0 = none, legacy); LE
  shares SC's lossy double-decode — a literal `&` in evidence text cannot
  round-trip, don't "fix" the parse order.
- CASEA/SETCASE were removed upstream ("No longer used", serverdata.h) but
  tsuserver-family servers still send them — we implement the legacy 2.6–2.9
  wire deliberately.
- 2.10 custom shouts: AO2-Client *scans the local* `custom_objections/`
  dir; a streaming client can't, so the char.ini `[Shouts]` `<stem>_name`
  keys are the discovery source. Receive strips a trailing extension from
  `4&<name>` (other clients send their dir-scan filename).
- Evidence images are EXACT URLs (`evidence/<file.ext>` — the extension
  ships in the LE name): `PrefetchExact`, never the format-probing path.
- Theme chatbox skin candidates are `chat` → `chatbox` → `chatblank`
  (courtroom.cpp:3328) — the stock default theme has NO chatbox.png.
  Theme text colors apply only while the theme's own skin drew: they're
  designed for that skin (black-on-paper), not our flat dark panel.
- `extensions.json` at the asset origin (webAO convention) is the format
  manifest; autodetect (default ON) seeds per-host learned formats from
  it. `.webp.static`/`.webp.animated` pseudo-suffixes normalize to .webp.
- Theme textures live in T1 under `theme://<stem>` keys; the scheme prefix
  can't collide with asset URLs. They heal on eviction via themePage.

## Environment gotchas (this dev machine)

- If CGO linking suddenly fails with `collect2: cannot find 'ld'`, check
  that `C:\msys64\ucrt64\bin\ld.exe` exists. If binutils went missing,
  restore it with `pacman -S mingw-w64-ucrt-x86_64-binutils`.
- `go.mod` pins `go 1.25.0` and `golang.org/x/sync v0.20.0`, and CI's
  `GO_VERSION` must match the `go` directive; don't let `go get` bump either
  without bumping the other.
- devkitPro's MSYS2 at `C:\devkitPro\msys2` belongs to other projects — never
  touch it; AsyncAO uses `C:\msys64`.
- WebP test fixtures were generated with msys2's `cwebp`/`img2webp`,
  AVIF fixtures with `avifenc` (see test/fixtures/).
- **Never round-trip Go sources through Windows PowerShell 5.1 pipes**
  (`Get-Content | Set-Content`): the default codepage mangles UTF-8
  (→ mojibake arrows/dashes). It happened once; the fix was a cp1252
  reverse round-trip via [IO.File]. Use the Edit tooling or `-Encoding`
  with [IO.File] reads/writes.
- In PowerShell 5.1, **quote Go flags containing `=.`** (e.g.
  `go test "-bench=." ...`): unquoted, the parser splits it into a bare
  `.` package argument and the run fails with "no Go files in <root>".
- CGO deps now include `mingw-w64-ucrt-x86_64-libavif` (AVIF decode);
  build.ps1 stages libavif-16/aom/dav1d/yuv/rav1e/SvtAv1Enc DLLs.

## Conventions

- Comments explain *why*; protocol behavior cites the AO2-Client source spot.
- Every `Prefetch` call site carries an `// AssetType: X` comment.
- Tests pin behavior, including performance gates (`testing.AllocsPerRun`).
- Cache keys are full URLs (host included) — per-server separation is
  structural. Never key by bare path.
- New dependencies require a written justification in docs/ARCHITECTURE.md.
- Milestone-sized local commits; never push.
- Naming: name length is proportional to scope. Receivers are 1–2 letters,
  identical for every method of a type, and never shadowed by params.
  Single-letter locals are allowed only within ~15 lines of their declaration
  or from the conventional set (`i`, `j`, `k`, `n`, `err`, `ok`, `t`, `b`,
  `x`, `y`, `w`, `h` in geometry, `r`/`w` for `io.Reader`/`io.Writer`). In
  functions over ~60 lines, anything referenced across more than ~15 lines gets
  a descriptive name. The same short name means the same type throughout a
  package; struct fields and package-level names are descriptive. Prefer
  splitting a long function to renaming inside it.
