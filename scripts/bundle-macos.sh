#!/usr/bin/env bash
# bundle-macos.sh — make a Homebrew-built macOS binary self-contained.
#
# THE PROBLEM this fixes: a plain `go build` on a macOS CI runner links the
# CGO deps (SDL2, SDL2_ttf, SDL2_mixer, libwebp, libavif and their transitive
# chain) against their Homebrew install paths, e.g. /opt/homebrew/opt/sdl2/lib/
# libSDL2-2.0.0.dylib. On any Mac that lacks those exact formulas, dyld aborts
# at launch with "Library not loaded: /opt/homebrew/..." — one missing lib at a
# time. This is the macOS analogue of what scripts/build.ps1 -Release does for
# Windows: collect the runtime library closure NEXT TO the binary so it runs on
# a clean machine.
#
# This script runs ONLY on a macOS CI runner (it shells out to macOS-only tools:
# dylibbundler, install_name_tool, otool, codesign). The Windows dev box never
# executes it — `bash -n` is the only local check.
#
# Usage:  scripts/bundle-macos.sh <path-to-built-binary> [staging-dir]
#   <path-to-built-binary>  the `go build` output (rewritten in place)
#   [staging-dir]           output folder holding the binary + lib/ (default:
#                           a "<binary>.bundle" sibling); created if absent.
#
# On success the staging dir contains the rewritten binary and a lib/ holding
# every non-system dependency with @rpath install names. The binary itself is
# also left rewritten in place so it can ship as the bare self-update asset
# (which, next to a bundled lib/, still loads its libs — see the rpath below).
set -euo pipefail

if [ "$#" -lt 1 ]; then
  echo "usage: $0 <path-to-built-binary> [staging-dir]" >&2
  exit 2
fi

BIN="$1"
if [ ! -f "$BIN" ]; then
  echo "bundle-macos: binary not found: $BIN" >&2
  exit 1
fi

# The base name is reused inside the staging dir so a bare download and a
# bundled download run the same executable name.
BIN_NAME="$(basename "$BIN")"
STAGE="${2:-${BIN}.bundle}"
LIBDIR="$STAGE/lib"

mkdir -p "$LIBDIR"
# Put the (soon-to-be-rewritten) binary in the staging dir alongside lib/. We
# rewrite the copy AND the original: dylibbundler edits the file it is given, so
# we point it at the staged copy, then mirror the rpath onto the original too.
cp -f "$BIN" "$STAGE/$BIN_NAME"

# dylibbundler recursively finds every non-system dylib the binary depends on,
# copies them into lib/, rewrites their inter-dependency install names, and
# rewrites the binary's references to them — all pointing at a chosen prefix.
#   -od  overwrite the destination dir contents if they already exist
#   -b   also fix the dependencies' own install names (bundle transitively)
#   -x   the executable/library to process
#   -d   the directory to copy the collected dylibs into
#   -p   the install-name PREFIX to write into every reference
# @rpath/ means "resolve against the binary's rpath list" — we add that rpath
# just below, so the very same rewrite works whether the binary sits inside the
# staging dir (bundled install) or is dropped as a bare self-update asset next
# to an existing lib/.
dylibbundler -od -b \
  -x "$STAGE/$BIN_NAME" \
  -d "$LIBDIR" \
  -p "@rpath/"

# Teach the binary WHERE @rpath points, in priority order. First hit wins, so:
#   1. @executable_path/lib  — the bundled libs we just staged. THIS is the fix:
#      a bundled install (or a bare self-update binary dropped beside lib/) finds
#      its exact libraries here and never touches Homebrew.
#   2. /opt/homebrew/lib     — Apple-Silicon Homebrew fallback, so a user who
#      downloads ONLY the bare binary (no lib/) and HAS brew still runs.
#   3. /usr/local/lib        — Intel / Rosetta Homebrew fallback for the same.
#
# `install_name_tool -add_rpath` ABORTS if the rpath is already present, and this
# step is a BLOCKING CI gate under `set -e` — so guard each add against a
# pre-existing LC_RPATH. Today Go's darwin linker emits none (our ldflags are
# only -s -w -X) and dylibbundler with `-p @rpath/` adds none of its own, but a
# future toolchain/bundler that did would otherwise turn every macOS CI run red.
# otool -l prints each rpath as: "         path <val> (offset N)".
add_rpath() {
  local rp="$1" f="$2"
  otool -l "$f" | grep -q "path $rp " || install_name_tool -add_rpath "$rp" "$f"
}
add_rpath "@executable_path/lib" "$STAGE/$BIN_NAME"
add_rpath "/opt/homebrew/lib"    "$STAGE/$BIN_NAME"
add_rpath "/usr/local/lib"       "$STAGE/$BIN_NAME"

# install_name_tool invalidates any existing code signature, and arm64 macOS
# refuses to run code with a broken/absent signature. Ad-hoc sign (`-s -`)
# EVERYTHING we modified so the staged bundle runs on a clean machine even with
# no Apple secrets. When real Developer-ID secrets exist, the release workflow
# re-signs properly AFTER this script (order matters — see release.yml); an
# ad-hoc signature here does no harm because that later step uses --force.
# Sign inside-out: the libraries first, then the binary that references them.
for dylib in "$LIBDIR"/*.dylib; do
  [ -e "$dylib" ] || continue          # no dylibs (statically linked?) -> skip
  codesign --force -s - "$dylib"
done
codesign --force -s - "$STAGE/$BIN_NAME"

# Mirror the rewritten, signed binary back over the original path so the caller's
# bare asset (same name as before) is ALSO self-contained-capable: identical
# rpath list, so dropping it next to a lib/ folder (or on a brew machine) works.
cp -f "$STAGE/$BIN_NAME" "$BIN"

# --- Regression guard -------------------------------------------------------
# The whole point of this script is that NO reference to a Homebrew prefix
# survives. Walk otool -L for the binary and every bundled dylib and fail loudly
# on any /opt/homebrew or /usr/local path. System locations (/usr/lib,
# /System/...) are fine — those exist on every Mac. install-name rpath lines in
# otool output (the "@rpath/..." and the "(compatibility ...)" tail) are checked
# too, but the fallback rpaths we ADDED live in LC_RPATH, not otool -L's link
# table, so they don't trip this. This assertion is the forever-testable proof
# the fix held, run in both CI and the release pipeline.
check_clean() {
  local target="$1"
  local bad
  # otool -L lists linked libraries, one per indented line. Grep the offending
  # prefixes; grep exits 1 (no match) on a clean binary, which is what we want.
  bad="$(otool -L "$target" | grep -E '/opt/homebrew|/usr/local' || true)"
  if [ -n "$bad" ]; then
    echo "bundle-macos: FAIL — $target still references a Homebrew path:" >&2
    echo "$bad" >&2
    return 1
  fi
}

check_clean "$STAGE/$BIN_NAME"
bundled=0
for dylib in "$LIBDIR"/*.dylib; do
  [ -e "$dylib" ] || continue
  check_clean "$dylib"
  bundled=$((bundled + 1))
done

# --- Must-bundle assertion ----------------------------------------------------
# check_clean proves nothing WRONG is referenced; this proves everything NEEDED
# is PRESENT. dylibbundler only walks hard LC_LOAD_DYLIB links. Today's Homebrew
# sdl2_mixer formula hard-links every music codec (autotools --enable-music-*
# with each *-shared dlopen path disabled), so the codec dylibs ride into lib/
# transitively. But if the formula ever flips to SDL_mixer's dlopen shims
# (autotools --enable-music-*-shared, or the CMake build whose
# SDL2MIXER_DEPS_SHARED default compiles the *_DYNAMIC/SDL_LoadObject paths),
# those libraries vanish from the link table: dylibbundler stages nothing, the
# no-Homebrew guard above still passes, and on a clean Mac SDL_mixer's
# dlopen("libopusfile.0.dylib") finds no /opt/homebrew — music playback breaks
# silently in the shipped tarball. (The field symptom that motivated this was
# .opus SEEK failing on macOS; a missing codec dylib is the harsher cousin —
# no playback at all.) Fail loudly instead: ci.yml runs this script as a
# blocking gate, so a formula change turns CI red before a release ships.
check_present() {
  local glob="$1" why="$2"
  local f
  # An unmatched glob stays a literal string (bash default, no nullglob), so
  # -e is false and we fall through to the failure — exactly what we want.
  for f in "$LIBDIR"/$glob; do
    if [ -e "$f" ]; then
      return 0
    fi
  done
  echo "bundle-macos: FAIL — required dylib missing from $LIBDIR: $glob ($why)." >&2
  echo "  dylibbundler only follows hard LC_LOAD_DYLIB links; the Homebrew" >&2
  echo "  sdl2_mixer formula likely switched this codec to a dlopen shim" >&2
  echo "  (--enable-music-*-shared / SDL2MIXER_DEPS_SHARED), so the bundle" >&2
  echo "  would ship without it and music would break on a clean Mac." >&2
  return 1
}

# The mixer itself plus the music-codec closure playback/seek depends on.
check_present "libSDL2_mixer*.dylib" "SDL2_mixer — all music + SFX"
check_present "libopusfile*.dylib"   "Ogg-Opus demux/decode/seek — .opus music"
check_present "libvorbisfile*.dylib" "Ogg-Vorbis — .ogg music"
check_present "libmpg123*.dylib"     "MP3 music"
check_present "libFLAC*.dylib"       "FLAC music"

echo "bundle-macos: OK — $STAGE/$BIN_NAME is self-contained ($bundled bundled dylibs)"
