#!/usr/bin/env bash
# Build + install AsyncAO's SDL Mixer X fork (WohlSoft/SDL-Mixer-X + the runtime
# loop-point setter patch) into PREFIX, for cgo to link via the default search
# paths (or CGO_CFLAGS/CGO_LDFLAGS). ZLib-only: bundled stb_vorbis / dr_flac /
# dr_mp3 plus system opus/opusfile; no LGPL/GPL codecs, no MIDI.
#
# Codec dependencies (SDL2, opus, opusfile, cmake, ninja) must already be
# installed — see scripts/setup-deps.sh and the CI workflows for the per-platform
# package list.
set -euo pipefail

PREFIX="${1:-/usr/local}"
SRC="$(mktemp -d)"
trap 'rm -rf "$SRC"' EXIT

git clone --depth 1 https://github.com/SyntaxNyah/SDL-Mixer-X.git "$SRC"

cmake -S "$SRC" -B "$SRC/build" \
  -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$PREFIX" \
  -DSDL_MIXER_X_SHARED=ON \
  -DSDL_MIXER_X_STATIC=OFF \
  -DMIXERX_ENABLE_LGPL=OFF \
  -DMIXERX_ENABLE_GPL=OFF \
  -DUSE_MIDI=OFF

cmake --build "$SRC/build" --parallel
cmake --install "$SRC/build"

echo "SDL Mixer X installed to $PREFIX"
echo "  header: $PREFIX/include/SDL2/SDL_mixer.h"
echo "  lib:    $PREFIX/lib/libSDL2_mixer_ext.so"
