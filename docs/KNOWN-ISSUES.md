# Known issues / future work

Tracked limitations that need more than a localized fix.

## Color emoji & supplementary-plane characters render as boxes

**Symptom.** Emoji typed in IC/OOC (💔 🙏 🥀 💎, etc.) — and any other character
above the Basic Multilingual Plane — show as `□` tofu boxes. BMP symbols the
bundled font happens to cover (e.g. `★`) render fine; that's why UI buttons were
switched from glyphs like `▶`/`↗` to plain text labels.

**Root cause.** This is a hard limit of the current text stack, not a missing
font:

- go-sdl2 `v0.4.40` is built against **SDL_ttf 2.0.18**, which exposes only the
  16-bit glyph API (`TTF_GlyphMetrics`, no `*_32`) and has **no colour-glyph
  (COLR/CPAL/CBDT) rendering**.
- The emoji the client sees are supplementary-plane (U+1F300 and up), i.e.
  `rune > 0xFFFF`. `internal/ui/ui.go` `fontCovers` deliberately treats any such
  rune as *uncovered* (the 16-bit metrics API can't even query it), so the font
  chain falls through to the embedded font, which renders the missing glyph as a
  box.

So adding an emoji font to the chain (Settings → IC/OOC font, or by default)
does **not** help: the coverage check rejects the runes before any fallback font
is consulted, and 2.0.18 couldn't render Segoe UI Emoji's colour layers anyway.
There is currently **no user-side workaround** for supplementary-plane emoji.

**Fix paths (future work).**

1. **Upgrade SDL_ttf to ≥ 2.20** — the real fix. It adds `TTF_GlyphMetrics32` /
   `TTF_RenderGlyph32` and colour-glyph rendering. Requires: a go-sdl2 bump (or
   custom cgo) that binds the 2.20 API; staging the newer `SDL2_ttf` DLL in
   `scripts/build.ps1`; and reworking `fontCovers`/`pickFont` to use the 32-bit
   coverage check (drop the `r > 0xFFFF` rejection). Color emoji then render from
   a system emoji font added to the fallback chain.
2. **Monochrome stopgap** (no dep change). Route supplementary-plane runs to a
   bundled monochrome emoji font (e.g. Noto Emoji) — outlines, not colour. Still
   requires lifting the `r > 0xFFFF` rejection and per-run rendering, so it is
   most of the work of (1) for a worse result.

Either path is a rasterizer change on a **perf-sensitive, 0-alloc** path (the
IC/OOC text draw — see `docs/BENCHMARKS.md`), so it must keep `pickCached`
allocation-free. Not a quick win — tracked here deliberately rather than faked.
