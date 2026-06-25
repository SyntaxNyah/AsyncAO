# Third-party licences — AsyncAO is free software, top to bottom

**AsyncAO is licensed under the [GNU AGPL v3](../LICENSE).** This document lists
every dependency and its licence, and explains why the whole stack is compatible
with the AGPL v3 — i.e. AsyncAO is *genuinely* free/libre software with no
proprietary or licence-incompatible pieces.

> This is a good-faith summary maintained by the project, not legal advice. Each
> linked project's own licence text is authoritative.

## Why it's AGPL-v3-compatible

The AGPL v3 lets you combine in:

- **Permissive licences** — MIT, BSD (2-/3-clause), ISC, zlib, the libpng and
  FreeType licences. One-way compatible: permissive code can be included in an
  AGPL work.
- **GNU family** — (L)GPL v3 and AGPL v3 code, and **LGPL** libraries (an AGPL
  program may use LGPL libraries freely).
- **MPL 2.0** — explicitly GPL/LGPL/AGPL-compatible via its "Secondary Licenses"
  clause (§3.3).
- **GCC runtime** — GPL v3 **with the GCC Runtime Library Exception**, which
  permits linking the runtime into a program under *any* licence.

Nothing AsyncAO ships is under a proprietary or AGPL-incompatible licence.

## Go module dependencies

| Module | Role | Licence |
|---|---|---|
| [coder/websocket](https://github.com/coder/websocket) | WebSocket client (the only transport) | ISC |
| [veandco/go-sdl2](https://github.com/veandco/go-sdl2) | SDL2 / mixer / ttf bindings | BSD-3-Clause |
| [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) | image scaling (Catmull-Rom) & codecs | BSD-3-Clause |
| [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync) | concurrency primitives (singleflight, errgroup) | BSD-3-Clause |
| [golang.org/x/text](https://pkg.go.dev/golang.org/x/text) | text encoding (indirect) | BSD-3-Clause |
| [cespare/xxhash/v2](https://github.com/cespare/xxhash) | fast hashing for cache keys | MIT |
| [hashicorp/golang-lru/v2](https://github.com/hashicorp/golang-lru) | LRU caches | MPL-2.0 |
| [kettek/apng](https://github.com/kettek/apng) | animated-PNG decoding | BSD-3-Clause |
| [klauspost/compress](https://github.com/klauspost/compress) | compression (indirect) | BSD-3-Clause |

## Bundled native libraries (Windows DLLs)

Shipped next to `asyncao.exe` so it runs without MSYS2 on `PATH` (staged by
`scripts/build.ps1`). All free software:

| Library | Role | Licence |
|---|---|---|
| [SDL2](https://www.libsdl.org), SDL2_ttf, SDL2_mixer | windowing, rendering, audio | zlib |
| [libwebp](https://chromium.googlesource.com/webm/libwebp) (+ demux/mux/sharpyuv) | WebP codec | BSD-3-Clause |
| [libavif](https://github.com/AOMediaCodec/libavif) | AVIF codec | BSD-2-Clause |
| [dav1d](https://code.videolan.org/videolan/dav1d), [libaom](https://aomedia.googlesource.com/aom), libyuv, rav1e, SVT-AV1 | AV1 decode/encode | BSD-2/3-Clause |
| [FreeType](https://freetype.org) | font rasterizer | FreeType License (BSD-style) / GPLv2 |
| [HarfBuzz](https://harfbuzz.github.io) | text shaping | MIT (Old MIT) |
| [Opus](https://opus-codec.org), Ogg, Vorbis (Xiph.Org) | audio codecs | BSD-3-Clause |
| [mpg123](https://www.mpg123.de) | MP3 decoding | LGPL-2.1 |
| [WavPack](https://www.wavpack.com), [libxmp](https://xmp.sourceforge.net), [Game Music Emu](https://bitbucket.org/mpyne/game-music-emu) | extra audio formats | BSD-3 / LGPL-2.1 |
| [libpng](https://www.libpng.org), [zlib](https://zlib.net) | PNG & deflate | libpng / zlib |
| [bzip2](https://sourceware.org/bzip2/), [Brotli](https://github.com/google/brotli), [Zstandard](https://github.com/facebook/zstd) | compression | bzip2 (BSD-like) / MIT / BSD-3 |
| [GLib](https://gitlab.gnome.org/GNOME/glib) (gio/gobject/…), graphite2 | FreeType/HarfBuzz support | LGPL-2.1 / tri-licence |
| [gettext](https://www.gnu.org/software/gettext/) (libintl), libiconv | i18n runtime | LGPL-2.1 |
| [PCRE2](https://www.pcre.org) | regex (GLib dep) | BSD-3-Clause |
| GCC runtime — libgcc, libstdc++, libgomp, libquadmath, libatomic | C/C++ runtime | GPL-3.0 **with GCC Runtime Library Exception** |
| [mingw-w64](https://www.mingw-w64.org) winpthreads | threads runtime | MIT / permissive |

## Embedded assets

- **[OpenDyslexic](https://opendyslexic.org)** — the optional dyslexia-friendly
  font, embedded via `//go:embed`. **SIL Open Font License 1.1**; the licence
  ships unmodified at `internal/ui/fonts/OpenDyslexic-LICENSE-OFL.txt`, so the
  Reserved Font Name clause is satisfied.
- **Mayo** — the mascot / app-icon art (`internal/ui/assets/mayo.png`),
  commissioned by Nyah and illustrated by **hlenbchan** (Instagram @hlenbchan2).
  Used with permission; please credit the artist if you reuse it.

## Reference projects (no code is copied)

AsyncAO mirrors Attorney Online's *protocol and courtroom semantics*, which are
not themselves copyrightable, as a **clean-room Go reimplementation** — it does
not copy source from these projects:

- **[AO2-Client](https://github.com/AttorneyOnline/AO2-Client)** — GPL v3, the
  canonical protocol reference. (Even if any GPLv3 material were involved, GPL v3
  is upward-compatible with AGPL v3.)
- **[webAO](https://github.com/AttorneyOnline/webAO)** — asset-URL conventions.
- **[AO-SDL](https://github.com/AttorneyOnline/AO-SDL)** — SDL2 thread-model
  reference.

## Compliance notes

- The full text of each permissive/LGPL licence is available from the linked
  upstream projects; binary distributions should include those notices alongside
  the bundled DLLs (the AGPL does not override their attribution requirements).
- When you distribute AsyncAO (modified or not), provide the corresponding
  source under the AGPL v3 — for unmodified builds, a link to
  <https://github.com/SyntaxNyah/AsyncAO> suffices.
