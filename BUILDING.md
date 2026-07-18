# Building AsyncAO

AsyncAO is Go ≥ 1.24 with CGO. The C dependencies are:

| Library | Used for |
|---|---|
| SDL2 | window, renderer, input |
| SDL2_ttf | font rasterization |
| SDL2_mixer (≥ 2.6, with opusfile) | Opus/OGG/MP3/FLAC/WAV decoding in C |
| libwebp + libwebpdemux | static & animated WebP decode (SIMD) |
| libavif (+ dav1d/aom) | AVIF image decode |
| opus | Opus (voice chat + Opus music via SDL2_mixer) |

Everything else is pure Go and fetched by `go build` automatically.

---

## Windows (MSYS2 / UCRT64) — the easy way

1. **Install Go ≥ 1.24** from <https://go.dev/dl/> (the normal Windows MSI).

2. **Install the C toolchain + libraries** (installs MSYS2 if needed):

   ```powershell
   powershell -ExecutionPolicy Bypass -File scripts\setup-deps.ps1
   ```

   Or by hand in an *MSYS2 UCRT64* shell:

   ```bash
   pacman -S mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-pkgconf \
             mingw-w64-ucrt-x86_64-SDL2 mingw-w64-ucrt-x86_64-SDL2_ttf \
             mingw-w64-ucrt-x86_64-SDL2_mixer mingw-w64-ucrt-x86_64-libwebp \
             mingw-w64-ucrt-x86_64-libavif mingw-w64-ucrt-x86_64-opus
   ```

3. **Build** (sets all the CGO env vars for you, stages runtime DLLs):

   ```powershell
   powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Release
   bin\asyncao.exe
   ```

   Manual equivalent:

   ```powershell
   $env:PATH        = "C:\msys64\ucrt64\bin;$env:PATH"
   $env:CGO_ENABLED = "1"
   $env:CC          = "C:\msys64\ucrt64\bin\gcc.exe"
   $env:CGO_CFLAGS  = "-IC:\msys64\ucrt64\include"
   $env:CGO_LDFLAGS = "-LC:\msys64\ucrt64\lib"
   $env:PKG_CONFIG_PATH = "C:\msys64\ucrt64\lib\pkgconfig"
   go build -pgo=auto -trimpath -ldflags "-s -w" -o bin\asyncao.exe .\cmd\asyncao
   ```

   To run the exe outside an MSYS2 shell, the SDL2/webp DLLs must sit next to
   it — `scripts\build.ps1` copies them for you.

### Windows troubleshooting

- **`gcc: fatal error: cannot execute 'cc1'`** — you have a partial MinGW
  somewhere on PATH (e.g. `C:\ProgramData\mingw64`). Use the full MSYS2
  UCRT64 toolchain; set `CC` explicitly as above.
- **pacman SSL errors** (`unable to get local issuer certificate`) — corporate
  AV/proxy intercepts TLS. Edit `C:\msys64\etc\pacman.conf`, uncomment
  `XferCommand` and add `-k` to the curl line. Packages remain GPG-verified.
- **`Access is denied` running freshly built test binaries** — overzealous
  antivirus scanning new exes. Re-run; consider excluding `%TEMP%\go-build*`.
- **`collect2: fatal error: cannot find 'ld'`** — your antivirus quarantined
  MSYS2's linker. Restore it with `pacman -S mingw-w64-ucrt-x86_64-binutils`
  and add `C:\msys64` to the AV's exclusions.
- **Missing DLL on launch** — run `scripts\build.ps1` (stages DLLs) or launch
  from a UCRT64 shell where `C:\msys64\ucrt64\bin` is on PATH.

---

## Linux

```bash
./scripts/setup-deps.sh        # Debian/Ubuntu, Fedora, Arch auto-detected
go build -pgo=auto -trimpath -ldflags "-s -w" -o asyncao ./cmd/asyncao
./asyncao
```

Package names if you'd rather install manually:

- **Debian/Ubuntu:** `libsdl2-dev libsdl2-ttf-dev libsdl2-mixer-dev libwebp-dev libavif-dev libopus-dev`
- **Fedora:** `SDL2-devel SDL2_ttf-devel SDL2_mixer-devel libwebp-devel libavif-devel opus-devel`
- **Arch:** `sdl2 sdl2_ttf sdl2_mixer libwebp libavif opus`

### Linux AppImage (self-contained download)

The bare binary above runs only where the SDL2/webp/avif shared libraries are
already installed. To produce a **single self-contained executable** that
bundles those libraries (the Linux equivalent of the Windows DLL bundle):

```bash
./scripts/setup-deps.sh           # the dev libraries, once
sudo apt-get install -y patchelf desktop-file-utils   # AppImage tooling
./scripts/build-appimage.sh       # → dist/AsyncAO-x86_64.AppImage
```

The script builds the release binary, downloads `linuxdeploy` (cached under
`/.deps/`), bundles the binary's shared-library dependencies, and packs them
with the Mayo icon and `packaging/linux/asyncao.desktop`. The result runs on any
reasonably modern x86_64 desktop with no install step. CI builds the same
AppImage on every push in two flavors (Actions → latest run):
`asyncao-linux-x86_64-AppImage` (default) and
`asyncao-linux-x86_64-AppImage-nodiscord` (the lean build: Discord **and** voice
chat compiled out, `-tags "nodiscord novoice"`). To build the lean one locally:
`go build -tags "nodiscord novoice" -o asyncao ./cmd/asyncao && APPIMAGE_OUTPUT=AsyncAO-nodiscord-x86_64.AppImage ./scripts/build-appimage.sh ./asyncao`.

## macOS

```bash
brew install sdl2 sdl2_ttf sdl2_mixer webp libavif opus opusfile pkg-config
go build -pgo=auto -trimpath -ldflags "-s -w" -o asyncao ./cmd/asyncao
./asyncao
```

Works on Apple Silicon and Intel. Homebrew is a **build-time** dependency here:
a from-source build links against the brew dylibs and needs them present to run.
(`opusfile` — not the bare `opus` codec, which serves voice chat — is what gives
SDL2_mixer Ogg-Opus **music** decode and seeking; the formula hard-links it, so
brew pulls it in anyway, but it's named explicitly to pin the requirement.)

The **prebuilt release** (`asyncao-macos-bundle-arm64.tar.gz`) needs **no
Homebrew** — the release pipeline runs `scripts/bundle-macos.sh` to collect the
dylib closure into a `lib/` folder beside the binary (rewriting install names to
`@rpath/`, the macOS analogue of the Windows DLL staging), so it runs on a clean
Mac. The script asserts both directions: no Homebrew path survives in the
binary, **and** the music-codec dylibs (opusfile/vorbisfile/mpg123/FLAC) made it
into `lib/` — the bundler only follows hard links, so a Homebrew formula switch
to SDL_mixer's dlopen codec shims would otherwise silently ship a tarball whose
music can't play on a brew-less Mac. The bare `asyncao-macos-arm64` asset is the
self-update target; for a first install, `tar -xzf
asyncao-macos-bundle-arm64.tar.gz` (the binary and its `lib/` land flat in the
current folder) and run `./asyncao-macos-arm64`.

## Android / iOS

Out of scope: the SDL2 + CGO toolchain story on mobile is a project of its
own. Desktop Windows/Linux/macOS are the supported targets.

## Supported platforms (why no 32-bit or Windows 7 build)

AsyncAO ships **64-bit Windows 10+ / Linux / macOS** only. A 32-bit Windows 7
build has been asked about; it isn't offered, for two independent reasons:

- **Windows 7/8 — blocked by Go.** Go dropped Windows 7/8 support in **Go 1.21**;
  Go 1.20 was the last release whose binaries run there. AsyncAO is on Go ≥ 1.24,
  so *any* build from this tree (32- or 64-bit) won't start on Windows 7.
  Supporting it would mean pinning the whole project back to Go 1.20, with the
  dependency downgrades and permanent dual-toolchain upkeep that implies.
- **32-bit — no maintained CGO toolchain.** The C dependencies come from MSYS2,
  which dropped 32-bit (i686 / MINGW32) packages — no new ones since Dec 2023,
  and the community 32-bit repo went dark in Nov 2025. The AVIF decode chain is
  the worst offender (SVT-AV1 is x86-64-only), so a 32-bit build would also have
  to drop AVIF. It's a from-scratch toolchain project, not a CI flag.

---

## Build variants

| Command | What you get |
|---|---|
| `go build ./cmd/asyncao` | debug build |
| `go build -pgo=auto -trimpath -ldflags "-s -w" ...` | release (uses `default.pgo`) |
| `GOAMD64=v3 go build ...` | AVX2-tuned build (2013+ CPUs); ship the default build as baseline fallback |
| `go build -tags nocgo_webp ...` | pure-Go WebP fallback (static images only; animated WebP errors visibly) |
| `go build -tags nocgo_avif ...` | no libavif binding (AVIF sniffing stays; decode errors visibly) |
| `go build -tags nodiscord ...` | Rich Presence compiled out entirely (see below) |
| `go build -tags novoice ...` | voice chat compiled out entirely — the LemmyAO/Nyathena VS_* relay + Opus codec, plus its UI, buttons and settings (see below). Opus **music** is unaffected. |
| `CGO_ENABLED=0 go build ./cmd/asyncao-cache` | cache companion CLI (stats/inspect/prune/warm T3) — pure Go, no SDL/CGO, builds anywhere |

## Discord (never required)

**Building AsyncAO never requires Discord, a Discord SDK, or any
Discord-related dependency.** The optional Rich Presence integration
(`internal/presence`) is pure Go standard library: it talks to a locally
running Discord client over its IPC pipe (`\\.\pipe\discord-ipc-N` on
Windows, `$XDG_RUNTIME_DIR/discord-ipc-N` elsewhere) — nothing is linked,
fetched, or vendored.

- **Default build**: the feature exists but is OFF until the user enables
  it in Settings. Without Discord running it idles silently (one paced
  reconnect probe per 30 s, only while updates are pending). There is no
  Discord DLL — the code is in the exe itself; the DLLs staged next to
  `asyncao.exe` on Windows are the SDL2/webp/avif engine and are required
  regardless of Discord, so deleting one breaks the client (it does not
  "remove Discord").
- **`-tags nodiscord`**: even that code compiles out; the package becomes
  a no-op stub with the same API. Use this if your distro policy wants
  zero proprietary-service integration in the binary. CI builds every
  platform in both flavors, so you can also just download the prebuilt
  `asyncao-<platform>-nodiscord` artifact (Actions → latest run) — which is
  also **voice-free** (built `-tags "nodiscord novoice"`; see *Voice chat* below).
- To get the AsyncAO icon on profiles, create a Discord application named
  "AsyncAO" in the developer portal, upload the icon under the asset key
  `appicon`, and paste the application ID into Settings → Discord.

## Voice chat (never required)

The optional **voice chat** — the LemmyAO/Nyathena server-relayed VS_* transport
plus the Opus codec in `internal/voice` — is off unless a server advertises it
(VS_CAPS) and you join. Like Discord, it can be compiled out entirely:

- **`-tags novoice`**: the whole voice stack is removed — the Opus encoder/
  decoder binding, the VS_* protocol reducer, the floating voice panel, the
  courtroom **Voice** button, the Extras → *Voice* entry, and the **Settings →
  Voice** tab. The remaining UI surface is inert no-op stubs, so no voice control
  ever appears.
- **Opus MUSIC keeps working.** Music and SFX play through SDL2_mixer, which
  links `libopusfile`/`libopus` independently of the voice binding, so `.opus`
  tracks still decode in a `novoice` build (those DLLs ship via SDL2_mixer either
  way — the voice binding was never what carried them).
- The **prebuilt Discord-free downloads are also voice-free**: every
  `asyncao-<platform>-nodiscord` artifact is built `-tags "nodiscord novoice"`,
  so the "no Discord" build is the lean build with neither Discord nor voice.
  Build it locally with `go build -tags "nodiscord novoice" ./cmd/asyncao`.

## Tests & benchmarks

```bash
go test -race ./...                          # full suite, race detector
go test -run=NONE -bench=. -benchmem ./...   # alloc-gated benchmarks
```

The pure-Go packages (`config`, `cache`, `network`, `protocol`, `assets`,
`courtroom`, `theme`) test fine with `CGO_ENABLED=0`; `render`/`ui` need the
SDL2 toolchain and use SDL's dummy video driver headlessly.
