#!/usr/bin/env bash
#
# Build a self-contained AppImage for AsyncAO (Linux x86_64).
#
# Output: dist/AsyncAO-x86_64.AppImage — a single executable that bundles the
# SDL2 / SDL2_ttf / SDL2_mixer / libwebp / libavif runtime (resolved from the
# binary's NEEDED libraries by linuxdeploy), so it runs on any reasonably modern
# x86_64 desktop with no install step. Mirrors the CI "AppImage (Linux x86_64)"
# job; run it locally after installing the dev deps (scripts/setup-deps.sh).
#
# Usage:
#   scripts/build-appimage.sh                  # build the release binary, then package
#   scripts/build-appimage.sh path/to/asyncao  # package an already-built binary
#
# Env: APPIMAGE_OUTPUT overrides the output filename (default
# AsyncAO-x86_64.AppImage) — used by CI to name the Discord-free variant.
#
# Note: SDL2_mixer codec back-ends that are dlopen()ed at runtime (rather than
# linked) are not visible to ldd and so are not bundled; the host's SDL2_mixer
# codecs fill in. Music/SFX of the common formats (Opus/OGG/MP3/WAV) work on
# desktops that ship SDL2_mixer, which is the overwhelming majority.
set -euo pipefail

# --- locate the repo root (this script lives in scripts/) --------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT"

DIST="$ROOT/dist"
TOOLS="$ROOT/.deps/appimage-tools"          # cached under the gitignored /.deps/
STAGE="$ROOT/.deps/appimage-stage"          # binary staged with the exact basename
APPDIR="$ROOT/AppDir"
DESKTOP="$ROOT/packaging/linux/asyncao.desktop"
ICON="$ROOT/internal/ui/assets/mayo.png"    # the Mayo mascot is the app icon

# linuxdeploy only publishes a rolling "continuous" release.
LD_URL="https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-x86_64.AppImage"
LD_PLUGIN_URL="https://github.com/linuxdeploy/linuxdeploy-plugin-appimage/releases/download/continuous/linuxdeploy-plugin-appimage-x86_64.AppImage"

# CI runners (and many containers) have no FUSE; run the tool AppImages by
# self-extraction instead of mounting them.
export APPIMAGE_EXTRACT_AND_RUN=1

# --- 1. the binary -----------------------------------------------------------
SRC_BIN="${1:-}"
if [[ -z "$SRC_BIN" ]]; then
	echo ">> building asyncao (release flags)"
	SRC_BIN="$ROOT/asyncao"
	CGO_ENABLED=1 go build -pgo=auto -trimpath -ldflags "-s -w" -o "$SRC_BIN" ./cmd/asyncao
fi
if [[ ! -x "$SRC_BIN" ]]; then
	echo "error: '$SRC_BIN' is not an executable binary" >&2
	exit 1
fi
# The desktop file's Exec=asyncao must match the bundled binary's basename, so
# stage it under that name regardless of what was passed in.
rm -rf "$STAGE"
mkdir -p "$STAGE"
cp -f "$SRC_BIN" "$STAGE/asyncao"
chmod +x "$STAGE/asyncao"
BIN="$STAGE/asyncao"

# --- 2. fetch linuxdeploy + the appimage plugin (cached) ---------------------
mkdir -p "$TOOLS"
fetch() {  # fetch <url> <dest>
	local url="$1" dest="$2"
	if [[ ! -f "$dest" ]]; then
		echo ">> downloading $(basename "$dest")"
		curl -fsSL "$url" -o "$dest"
		chmod +x "$dest"
	fi
}
fetch "$LD_URL"        "$TOOLS/linuxdeploy"
fetch "$LD_PLUGIN_URL" "$TOOLS/linuxdeploy-plugin-appimage"
export PATH="$TOOLS:$PATH"

# --- 3. assemble the AppDir + package ----------------------------------------
rm -rf "$APPDIR"
mkdir -p "$DIST"

# linuxdeploy populates the AppDir from the binary (bundling its shared-library
# dependencies + patching rpaths), the .desktop file and the icon; the appimage
# plugin then packs it. --icon-filename renames the icon to match the desktop
# "Icon=asyncao" key.
export OUTPUT="${APPIMAGE_OUTPUT:-AsyncAO-x86_64.AppImage}"
"$TOOLS/linuxdeploy" \
	--appdir "$APPDIR" \
	--executable "$BIN" \
	--desktop-file "$DESKTOP" \
	--icon-file "$ICON" \
	--icon-filename asyncao \
	--output appimage

mv -f "$OUTPUT" "$DIST/$OUTPUT"
echo ">> built $DIST/$OUTPUT"
