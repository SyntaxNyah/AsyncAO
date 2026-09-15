#!/usr/bin/env bash
# AsyncAO Linux/macOS dependency bootstrap (SDL2, SDL2_ttf, the SDL Mixer X
# fork, libwebp, libavif).
set -euo pipefail

if [[ "$(uname)" == "Darwin" ]]; then
    brew install sdl2 sdl2_ttf webp libavif opus opusfile cmake ninja pkg-config
elif command -v apt-get >/dev/null; then
    sudo apt-get update
    sudo apt-get install -y libsdl2-dev libsdl2-ttf-dev libwebp-dev libavif-dev libopus-dev libopusfile-dev cmake ninja-build pkg-config
elif command -v dnf >/dev/null; then
    sudo dnf install -y SDL2-devel SDL2_ttf-devel libwebp-devel libavif-devel opus-devel opusfile-devel cmake ninja-build pkgconf
elif command -v pacman >/dev/null; then
    sudo pacman -S --needed sdl2 sdl2_ttf libwebp libavif opus opusfile cmake ninja pkgconf
else
    echo "Unknown package manager. Install SDL2, SDL2_ttf, libwebp, libavif, opus, opusfile, cmake and ninja dev packages manually." >&2
    exit 1
fi

# Build + install the SDL Mixer X fork (AsyncAO fork) for crossfade + gapless
# loop points. /usr/local is in the default include/lib search path, so no extra
# CGO_CFLAGS/CGO_LDFLAGS are needed after this.
bash scripts/build-sdl-mixer-x.sh /usr/local

echo "Done. Build with: go build -o asyncao ./cmd/asyncao"
