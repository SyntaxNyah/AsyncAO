#!/usr/bin/env bash
# AsyncAO Linux/macOS dependency bootstrap (SDL3, SDL3_ttf, SDL3_mixer, libwebp,
# libavif, opus). AsyncAO needs SDL 3.4 or newer: Homebrew, Fedora and Arch
# ship it; on Debian/Ubuntu it is built from source by scripts/build-sdl3.sh.
set -euo pipefail

if [[ "$(uname)" == "Darwin" ]]; then
    brew install sdl3 sdl3_ttf sdl3_mixer webp libavif opus opusfile pkg-config
elif command -v apt-get >/dev/null; then
    sudo apt-get update
    sudo apt-get install -y libwebp-dev libavif-dev libopus-dev libopusfile-dev \
        libfreetype-dev libharfbuzz-dev cmake ninja-build pkg-config git \
        libx11-dev libxext-dev libxrandr-dev libxcursor-dev libxi-dev libxss-dev libxtst-dev \
        libwayland-dev libxkbcommon-dev wayland-protocols libegl-dev \
        libasound2-dev libpulse-dev libpipewire-0.3-dev libdbus-1-dev libudev-dev
    # /usr/local is in the default include/lib search path, so no extra
    # CGO_CFLAGS/CGO_LDFLAGS are needed after this.
    sudo bash scripts/build-sdl3.sh /usr/local
elif command -v dnf >/dev/null; then
    sudo dnf install -y SDL3-devel SDL3_ttf-devel SDL3_mixer-devel libwebp-devel libavif-devel opus-devel opusfile-devel pkgconf
elif command -v pacman >/dev/null; then
    sudo pacman -S --needed sdl3 sdl3_ttf sdl3_mixer libwebp libavif opus opusfile pkgconf
else
    echo "Unknown package manager. Install SDL3 (3.4+), SDL3_ttf, SDL3_mixer, libwebp, libavif, opus and opusfile dev packages manually." >&2
    exit 1
fi

echo "Done. Build with: go build -o asyncao ./cmd/asyncao"
