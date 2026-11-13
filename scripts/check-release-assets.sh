#!/usr/bin/env bash
# Compares a directory of release assets against packaging/release-assets.txt.
# Usage: scripts/check-release-assets.sh <assets-dir>
set -euo pipefail

ASSETS_DIR="${1:?usage: check-release-assets.sh <assets-dir>}"
MANIFEST="$(dirname "${BASH_SOURCE[0]}")/../packaging/release-assets.txt"
failed=0

while read -r kind name; do
	if [[ "$kind" == required && ! -f "$ASSETS_DIR/$name" ]]; then
		echo "missing required asset: $name" >&2
		failed=1
	fi
done < <(grep -v '^#' "$MANIFEST")

for path in "$ASSETS_DIR"/*; do
	name="$(basename "$path")"
	if ! grep -qE "^(required|optional) ${name//./\\.}$" "$MANIFEST"; then
		echo "asset not in the manifest: $name" >&2
		failed=1
	fi
done

exit "$failed"
