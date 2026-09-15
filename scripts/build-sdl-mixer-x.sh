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

# Refresh the dynamic-linker cache so the freshly installed shared library is
# found at RUNTIME (test binaries link against libSDL2_mixer_ext.so.2; linking
# only finds the -l/-L path, the loader needs the cache). No-op on macOS, which
# resolves dylibs via install_name instead of a cache.
if command -v ldconfig >/dev/null 2>&1; then
  ldconfig
fi

# macOS: the fork installs the shared library with a BARE install name
# (LC_ID_DYLIB = "libSDL2_mixer_ext.2.dylib" — no @rpath, no directory). The
# linker bakes that bare name into every consumer, and dylibbundler can't
# resolve it transitively (it loops on "does not exist. Try again"). Rewrite it
# to a full path (Homebrew-style) so the reference is resolvable and dylibbundler
# can locate + bundle the dylib.
if command -v install_name_tool >/dev/null 2>&1; then
  soname="$PREFIX/lib/$(readlink "$PREFIX/lib/libSDL2_mixer_ext.dylib")"
  real="$(python3 -c 'import os,sys;print(os.path.realpath(sys.argv[1]))' "$soname")"
  install_name_tool -id "$soname" "$real"
fi

echo "SDL Mixer X installed to $PREFIX"
echo "  header: $PREFIX/include/SDL2/SDL_mixer.h"
echo "  lib:    $PREFIX/lib/libSDL2_mixer_ext.so"
