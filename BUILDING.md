# Building AsyncAO

AsyncAO is Go ≥ 1.24 with CGO. The C dependencies are:

| Library | Used for |
|---|---|
| SDL2 | window, renderer, input |
| SDL2_ttf | font rasterization |
| SDL2_mixer (≥ 2.6, with opusfile) | Opus/OGG/MP3/WAV decoding in C |
| libwebp + libwebpdemux | static & animated WebP decode (SIMD) |

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
             mingw-w64-ucrt-x86_64-SDL2_mixer mingw-w64-ucrt-x86_64-libwebp
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

- **Debian/Ubuntu:** `libsdl2-dev libsdl2-ttf-dev libsdl2-mixer-dev libwebp-dev`
- **Fedora:** `SDL2-devel SDL2_ttf-devel SDL2_mixer-devel libwebp-devel`
- **Arch:** `sdl2 sdl2_ttf sdl2_mixer libwebp`

## macOS

```bash
brew install sdl2 sdl2_ttf sdl2_mixer webp pkg-config
go build -pgo=auto -trimpath -ldflags "-s -w" -o asyncao ./cmd/asyncao
./asyncao
```

Works on Apple Silicon and Intel.

## Android / iOS

Out of scope: the SDL2 + CGO toolchain story on mobile is a project of its
own. Desktop Windows/Linux/macOS are the supported targets.

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

## Discord (never required)

**Building AsyncAO never requires Discord, a Discord SDK, or any
Discord-related dependency.** The optional Rich Presence integration
(`internal/presence`) is pure Go standard library: it talks to a locally
running Discord client over its IPC pipe (`\\.\pipe\discord-ipc-N` on
Windows, `$XDG_RUNTIME_DIR/discord-ipc-N` elsewhere) — nothing is linked,
fetched, or vendored.

- **Default build**: the feature exists but is OFF until the user enables
  it in Settings. Without Discord running it idles silently (one paced
  reconnect probe per 30 s, only while updates are pending).
- **`-tags nodiscord`**: even that code compiles out; the package becomes
  a no-op stub with the same API. Use this if your distro policy wants
  zero proprietary-service integration in the binary.
- To get the AsyncAO icon on profiles, create a Discord application named
  "AsyncAO" in the developer portal, upload the icon under the asset key
  `appicon`, and paste the application ID into Settings → Discord.

## Tests & benchmarks

```bash
go test -race ./...                          # full suite, race detector
go test -run=NONE -bench=. -benchmem ./...   # alloc-gated benchmarks
```

The pure-Go packages (`config`, `cache`, `network`, `protocol`, `assets`,
`courtroom`, `theme`) test fine with `CGO_ENABLED=0`; `render`/`ui` need the
SDL2 toolchain and use SDL's dummy video driver headlessly.
