#!/usr/bin/env bash
#
# Build a Flatpak bundle for AsyncAO (Linux x86_64). AsyncAO is AGPLv3, so a
# Flatpak is a natural distribution channel.
#
# Output: dist/<name>.flatpak — a single-file bundle (flatpak install --bundle).
# Mirrors the CI "Linux x86_64 Flatpak" job; that job is where this is actually
# validated, because the dev box is Windows and can't run flatpak-builder.
#
# Usage:
#   scripts/build-flatpak.sh                 # Discord-bundled default
#   scripts/build-flatpak.sh nodiscord       # Discord-free variant
#
# Env:
#   ASYNCAO_VERSION   version stamped into the binary (default: git describe / dev)
#   FLATPAK_OUTPUT    output filename (default derived from the variant)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT"

APP_ID="io.github.SyntaxNyah.AsyncAO"
FLATPAK_DIR="$ROOT/packaging/flatpak"
TEMPLATE="$FLATPAK_DIR/$APP_ID.yaml"
GENERATED="$FLATPAK_DIR/_build.yaml" # gitignored; lives beside the template so `path: ../..` still resolves
BUILDDIR="$ROOT/.deps/flatpak-build"
REPO="$ROOT/.deps/flatpak-repo"

VARIANT="${1:-default}"
TAGS=""
SUFFIX=""
if [[ "$VARIANT" == "nodiscord" ]]; then
	TAGS="nodiscord"
	SUFFIX="-nodiscord"
fi
OUT="${FLATPAK_OUTPUT:-AsyncAO-linux-x86_64${SUFFIX}.flatpak}"
VERSION="${ASYNCAO_VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"

# Substitute the template's build tags + version, then build that copy.
sed -e "s/@ASYNCAO_TAGS@/${TAGS}/g" -e "s/@ASYNCAO_VERSION@/${VERSION}/g" "$TEMPLATE" >"$GENERATED"
trap 'rm -f "$GENERATED"' EXIT

# Runtime + SDK + the Go SDK extension (from Flathub).
flatpak --user remote-add --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo
flatpak --user install -y flathub \
	org.freedesktop.Platform//24.08 \
	org.freedesktop.Sdk//24.08 \
	org.freedesktop.Sdk.Extension.golang//24.08

mkdir -p "$ROOT/dist"
flatpak-builder --force-clean --user --repo="$REPO" "$BUILDDIR" "$GENERATED"
flatpak build-bundle "$REPO" "$ROOT/dist/$OUT" "$APP_ID"
echo ">> built dist/$OUT"
