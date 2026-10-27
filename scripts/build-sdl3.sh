#!/usr/bin/env bash
# Build + install SDL3, SDL3_ttf and SDL3_mixer from their release tags into
# PREFIX, for systems whose packages are older than the SDL 3.4 AsyncAO needs
# (Debian, Ubuntu). CI uses libsdl-org/setup-sdl with the same versions and
# mixer options instead, which caches the build.
#
# The mixer matches what the client ships everywhere: zlib-only built-in
# decoders (stb_vorbis, dr_mp3, dr_flac) plus Opus through opusfile, linked at
# build time so ldd-based bundling sees it. No MIDI, no tracker or LGPL codecs.
#
# Build dependencies (cmake, ninja, freetype, harfbuzz, opus, opusfile and
# SDL's own X11/Wayland/audio headers) must already be installed — see
# scripts/setup-deps.sh.
set -euo pipefail

PREFIX="${1:-/usr/local}"
SDL_VERSION=3.4.18
TTF_VERSION=3.2.2
MIXER_VERSION=3.2.4

SRC="$(mktemp -d)"
trap 'rm -rf "$SRC"' EXIT

build() { # repo tag [cmake args...]
  local repo="$1" tag="$2"
  shift 2
  git clone --depth 1 --branch "$tag" "https://github.com/libsdl-org/$repo.git" "$SRC/$repo"
  cmake -S "$SRC/$repo" -B "$SRC/$repo/build" -G Ninja \
    -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_PREFIX_PATH="$PREFIX" "$@"
  cmake --build "$SRC/$repo/build" --parallel
  cmake --install "$SRC/$repo/build"
}

build SDL "release-$SDL_VERSION" -DSDL_TESTS=OFF -DSDL_EXAMPLES=OFF
build SDL_ttf "release-$TTF_VERSION" -DSDLTTF_VENDORED=OFF -DSDLTTF_HARFBUZZ=ON -DSDLTTF_SAMPLES=OFF
build SDL_mixer "release-$MIXER_VERSION" -DSDLMIXER_VENDORED=OFF -DSDLMIXER_TESTS=OFF -DSDLMIXER_EXAMPLES=OFF \
  -DSDLMIXER_MIDI=OFF -DSDLMIXER_MOD=OFF -DSDLMIXER_GME=OFF -DSDLMIXER_WAVPACK=OFF \
  -DSDLMIXER_FLAC_LIBFLAC=OFF -DSDLMIXER_MP3_MPG123=OFF -DSDLMIXER_VORBIS_VORBISFILE=OFF \
  -DSDLMIXER_OPUS=ON -DSDLMIXER_DEPS_SHARED=OFF
