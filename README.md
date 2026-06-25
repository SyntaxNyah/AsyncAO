# AsyncAO

**A maximum-performance Attorney Online 2 client written in Go.** Zero-fallback
asset streaming, learned formats, three-tier caching, lock-free hot paths, a
zero-allocation render loop  and full AO2 ≥ 2.6/2.8 pairing.

Made by **SyntaxNyah**, because people shouldn't have to download 20 gigabytes
of files to play, client lookups shouldn't take ages, and the stock AO2 client
is, let's be honest, a bit slow. AsyncAO streams exactly the assets it needs,
learns what formats your server ships (one probe per asset, ever), caches
everything in memory and on disk, and renders without allocating.

<p align="center">
  <img src="internal/ui/assets/mayo.png" alt="Mayo — the AsyncAO mascot and app icon" width="220"><br>
  <sub><b>Mayo</b> — the AsyncAO mascot &amp; app icon, by <a href="https://www.instagram.com/hlenbchan2">hlenbchan</a> (see <a href="#mayo--the-mascot--app-icon">Credits</a>)</sub>
</p>

## Highlights

- **Zero fallbacks by default** — prefer WebP? You get WebP or a visible
  warning. Exactly one network probe per asset until you opt into legacy
  format chains. Cold-loading a 200-character server costs ~285 probes, not
  thousands.
- **Learned formats** — the first successful probe per server teaches the
  client what that host ships; every later load resolves in <100 ns without
  touching the network.
- **Three cache tiers** — GPU textures (64 MiB), raw bytes (128 MiB), and an
  on-disk store. Each server's assets are cached separately by full URL: no
  conflicts, ever.
- **Full pairing** — two characters on screen with offsets, flip, and z-order,
  fetched in parallel so a paired message costs the same wall-clock as a solo
  one.
- **WebSocket-only** — modern AO2 protocol (2.11-compatible). Legacy raw-TCP
  servers are not supported; the lobby pins them to the bottom in black with
  a polite note that their owners should upgrade.
- **Server phone book** — favorite any server (master-list or private), pin it
  to the top, direct-connect by `ip:port` / `url:port` / `ws(s)://`.
- **AO2 theme support** — point AsyncAO at your existing `themes/` folder and
  `courtroom_design.ini` keeps working.
- **Local asset mode** — a checkbox turns off streaming entirely and reads
  from any folders you choose (AO2-style mount paths), for servers without an
  asset URL.
- **Discord Rich Presence (optional)** — show what you're playing on your
  Discord profile — server, character, showname, area — with per-field privacy
  toggles. Off by default, and there is **no DLL and no Discord dependency** of
  any kind: it speaks Discord's local IPC pipe in pure Go, so a missing or
  closed Discord never stops AsyncAO from launching. **Don't want Discord at
  all?** Every CI run also builds a **Discord-free** flavour — grab it from
  **GitHub Actions** (the **Actions** tab → latest run → **Artifacts**, named
  `asyncao-<platform>-nodiscord`; downloading artifacts needs a free GitHub
  account). It's compiled with `-tags nodiscord`, which strips out **all** the
  Discord code *including the settings UI*, so the binary is a little smaller and
  carries nothing Discord-related. See [docs/DISCORD.md](docs/DISCORD.md).
- **Niceties** — hover an emote or character icon for 3 seconds (or
  right-click) to preview the full sprite; saved shownames; live-loading
  character grid with search; per-type format toggles.
- **Quality-of-life suite** — a non-blocking **floating Extras box** (drag any
  widget out into its own movable, resizable box; the courtroom stays live
  underneath); a **player list** that groups a `/gas` by area and jumps you
  there on a click; a cross-server **jukebox** for the `/play` links DJs share;
  **custom window size + borderless fullscreen** (F11, with a "Fit to screen"
  rescue); per-speaker name colours; callword alerts; highlighted friends; and
  music changes logged right in the IC log. See
  [docs/FEATURES.md](docs/FEATURES.md) for the full inventory.

## Quick start

Download a CI build (Actions → latest run → artifacts) or build from source —
see **[BUILDING.md](BUILDING.md)** for the detailed per-OS guide.

```text
asyncao                 # open the lobby
asyncao -server ws://my.private.server:50001
asyncao -debug          # pprof on localhost:6060
```

## Documentation

| Doc | What's in it |
|---|---|
| [BUILDING.md](BUILDING.md) | Step-by-step build guide (Windows/Linux/macOS) |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Thread model, cache tiers, pipelines |
| [docs/PERFORMANCE.md](docs/PERFORMANCE.md) | Tuning, PGO capture, profiling |
| [docs/BENCHMARKS.md](docs/BENCHMARKS.md) | Recorded numbers for every gate |
| [docs/adr/0001-zero-fallback-by-default.md](docs/adr/0001-zero-fallback-by-default.md) | Why zero fallbacks |
| [docs/PROTOCOL.md](docs/PROTOCOL.md) | The AO2 wire protocol as implemented |
| [docs/DISCORD.md](docs/DISCORD.md) | Discord Rich Presence: setup, privacy, building without it |
| [docs/user/lobby.md](docs/user/lobby.md) | Lobby, phone book, direct connect |
| [docs/user/asset-preferences.md](docs/user/asset-preferences.md) | Format settings guide |
| [docs/user/pairing.md](docs/user/pairing.md) | Pairing guide |
| [docs/user/themes.md](docs/user/themes.md) | Migrating AO2 themes |
| [docs/user/local-assets.md](docs/user/local-assets.md) | No-streaming legacy mode |

## Credits

All credit to the original Attorney Online developers — AsyncAO exists because
of their work and follows their protocol and conventions:

- **FanatSors** — creator of the original Attorney Online. 
- **OmniTroid** — creator of the Attorney Online 2 client.
- **The [AttorneyOnline](https://github.com/AttorneyOnline) organization** and
  every **[AO2-Client](https://github.com/AttorneyOnline/AO2-Client)**
  contributor — the canonical client whose protocol semantics AsyncAO mirrors
- **[webAO](https://github.com/AttorneyOnline/webAO)** — the asset-URL
  conventions over HTTP come from their work
- **[AO-SDL](https://github.com/AttorneyOnline/AO-SDL)** — the SDL2 rendering
  model reference
- The whole AO community at [aceattorneyonline.com](https://aceattorneyonline.com)

### Beta testers

Thank you to everyone who tested AsyncAO while it was closed source — finding
bugs, requesting features, and giving the feedback that shaped it:
**Cocoa Bean, Lala, Peen, Emerald, Extra7, Poki, Xocfti, Dag, CherriPop**.

A special thank-you to **Northgate** — who backed the project, including
financially, and gave the inspiration to keep going. Without that support
AsyncAO wouldn't have come this far this fast.

### Mayo — the mascot & app icon

Meet **Mayo** (shown at the top), AsyncAO's mascot and app icon. The client was
almost called **"MayAO"** (Maya + AO), but became **AsyncAO** — we wanted more
Maya representation, since the AO2 client only ever showed Phoenix and Edgeworth.
So the mascot is Mayo: inspired by **Maya Fey** from *Ace Attorney*, with the
**Go gopher's** blue palette (AsyncAO is written in Go).

Art **commissioned by Nyah** and illustrated by
**[hlenbchan](https://www.instagram.com/hlenbchan2)** (Instagram
**[@hlenbchan2](https://www.instagram.com/hlenbchan2)**) — all credit for the
icon goes to them, please go support their work!

Thank you everyone for spending the time to read this.

## Contributing

**Pull requests, bug fixes and feature requests are welcome!**
→ [github.com/SyntaxNyah/AsyncAO](https://github.com/SyntaxNyah/AsyncAO)

## License & free software

AsyncAO is **free software**, licensed under the **[GNU AGPL v3](LICENSE)** — and
it's free *all the way down*. **Every dependency is open-source under an
AGPL-v3-compatible licence**, with no proprietary or licence-incompatible pieces:

- **Go libraries** — ISC ([coder/websocket](https://github.com/coder/websocket)),
  MIT ([xxhash](https://github.com/cespare/xxhash)), BSD-3
  ([go-sdl2](https://github.com/veandco/go-sdl2),
  [kettek/apng](https://github.com/kettek/apng),
  [klauspost/compress](https://github.com/klauspost/compress), and the
  `golang.org/x/*` libraries), and MPL-2.0
  ([hashicorp/golang-lru](https://github.com/hashicorp/golang-lru)).
- **Bundled engine** — zlib ([SDL2](https://www.libsdl.org) / ttf / mixer); BSD
  ([libwebp](https://chromium.googlesource.com/webm/libwebp),
  [libavif](https://github.com/AOMediaCodec/libavif) + dav1d/aom,
  [Opus/Vorbis](https://xiph.org)); the [FreeType](https://freetype.org),
  [HarfBuzz](https://harfbuzz.github.io), [libpng](https://www.libpng.org) and
  [zlib](https://zlib.net) licences; LGPL (GLib, gettext, mpg123 — used as
  separate DLLs); and the GCC runtime under its **Runtime Library Exception**.
- **Font** — [OpenDyslexic](https://opendyslexic.org), SIL OFL 1.1.

MPL-2.0, LGPL and the GCC exception are all AGPL-compatible, so the whole stack is
genuinely libre. The protocol comes from the GPLv3
[AO2-Client](https://github.com/AttorneyOnline/AO2-Client), reimplemented
clean-room in Go (protocols aren't copyrightable, and GPLv3 is AGPLv3-compatible
regardless). **Full per-dependency list with licences & links:
[docs/THIRD-PARTY-LICENSES.md](docs/THIRD-PARTY-LICENSES.md).**

**Copyright © 2026 SyntaxNyah and the AsyncAO contributors.** Because AsyncAO and
all of its dependencies are AGPL-v3-compatible free software, it may be freely
redistributed — **including binary GitHub releases** — in full compliance with
the AGPL v3 and every dependency's licence. Binary releases should ship the
third-party licence notices listed above alongside the [LICENSE](LICENSE) and a
link to this source. See the [NOTICE](NOTICE) file for the short version.
