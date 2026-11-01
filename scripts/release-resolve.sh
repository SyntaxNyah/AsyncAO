#!/usr/bin/env bash
# Refuses a version that must not be released from this commit, and writes
# prerelease to GITHUB_OUTPUT. Needs a full clone and GH_TOKEN.
# Usage: scripts/release-resolve.sh <version>
set -euo pipefail

VERSION="${1:?usage: release-resolve.sh <version>}"
CHANGELOG=internal/ui/assets/CHANGELOG.md

fail() {
	echo "::error::$1"
	exit 1
}

if ! [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
	fail "'$VERSION' is not a vMAJOR.MINOR.PATCH[-prerelease] version"
fi

prerelease=false
if [[ "$VERSION" == *-* ]]; then
	prerelease=true
else
	git merge-base --is-ancestor HEAD origin/main ||
		fail "stable version $VERSION must be released from main; only hyphenated prereleases may come from other branches"
	awk -v ver="$VERSION" '$1 == "##" && $2 == ver { found = 1 } END { exit !found }' "$CHANGELOG" ||
		fail "$CHANGELOG has no '## $VERSION' section"
fi

# A published version is never replaced; a leftover draft is, by the publish job.
if [[ "$(gh release view "$VERSION" --json isDraft --jq .isDraft 2>/dev/null)" == false ]]; then
	fail "$VERSION is already published; release a new patch version instead"
fi
if tagged="$(git rev-parse --quiet --verify "refs/tags/$VERSION^{commit}")" && [[ "$tagged" != "$(git rev-parse HEAD)" ]]; then
	fail "tag $VERSION already exists on another commit: $tagged"
fi

echo "prerelease=$prerelease" >>"$GITHUB_OUTPUT"
