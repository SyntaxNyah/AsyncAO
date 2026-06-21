# Known issues / future work

Tracked limitations that need more than a localized fix.

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
