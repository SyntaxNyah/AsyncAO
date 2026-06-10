#!/usr/bin/env bash
# AsyncAO Linux/macOS dependency bootstrap (SDL2, SDL2_ttf, SDL2_mixer, libwebp).
set -euo pipefail

if [[ "$(uname)" == "Darwin" ]]; then
    brew install sdl2 sdl2_ttf sdl2_mixer webp pkg-config
elif command -v apt-get >/dev/null; then
    sudo apt-get update
    sudo apt-get install -y libsdl2-dev libsdl2-ttf-dev libsdl2-mixer-dev libwebp-dev pkg-config
elif command -v dnf >/dev/null; then
    sudo dnf install -y SDL2-devel SDL2_ttf-devel SDL2_mixer-devel libwebp-devel pkgconf
elif command -v pacman >/dev/null; then
    sudo pacman -S --needed sdl2 sdl2_ttf sdl2_mixer libwebp pkgconf
else
    echo "Unknown package manager — install SDL2, SDL2_ttf, SDL2_mixer and libwebp dev packages manually." >&2
    exit 1
fi
echo "Done. Build with: go build -o asyncao ./cmd/asyncao"
