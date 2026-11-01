#!/usr/bin/env bash
# Prints a version's release notes: its CHANGELOG section, the same text as the
# in-app "What's New" tab, else the tag annotation, else a bare title.
# Usage: scripts/release-notes.sh <version>
set -euo pipefail

VERSION="${1:?usage: release-notes.sh <version>}"
CHANGELOG="$(dirname "${BASH_SOURCE[0]}")/../internal/ui/assets/CHANGELOG.md"

notes="$(awk -v ver="$VERSION" '
	$1 == "##" && $2 == ver { grab = 1; next }
	grab && /^## / { exit }
	grab { print }
' "$CHANGELOG" | sed '/./,$!d')"

if [[ -z "$notes" ]]; then
	notes="$(git tag -l --format='%(contents)' "$VERSION")"
fi
echo "${notes:-AsyncAO $VERSION}"
