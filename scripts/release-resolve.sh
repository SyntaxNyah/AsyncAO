#!/usr/bin/env bash
# Decides whether this commit releases, and what. Writes proceed, version and
# prerelease to GITHUB_OUTPUT. Needs a full clone and GH_TOKEN.
# Usage: scripts/release-resolve.sh changelog|<version>
#   changelog  release the CHANGELOG's newest version, unless it is already tagged
set -euo pipefail

SOURCE="${1:?usage: release-resolve.sh changelog|<version>}"
CHANGELOG=internal/ui/assets/CHANGELOG.md

fail() {
	echo "::error::$1"
	exit 1
}

tagged_commit() {
	git rev-parse --quiet --verify "refs/tags/$1^{commit}"
}

version="$SOURCE"
if [[ "$SOURCE" == changelog ]]; then
	version="$(awk '/^## v/ { print $2; exit }' "$CHANGELOG")"
	[[ -n "$version" ]] || fail "no '## vX.Y.Z' heading in $CHANGELOG"
	if tagged_commit "$version" >/dev/null; then
		echo "::notice::$version is already tagged, nothing to release."
		echo "proceed=false" >>"$GITHUB_OUTPUT"
		exit 0
	fi
fi

if ! [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
	fail "'$version' is not a vMAJOR.MINOR.PATCH[-prerelease] version"
fi

prerelease=false
if [[ "$version" == *-* ]]; then
	prerelease=true
else
	git merge-base --is-ancestor HEAD origin/main ||
		fail "stable version $version must be released from main; only hyphenated prereleases may come from other branches"
	awk -v ver="$version" '$1 == "##" && $2 == ver { found = 1 } END { exit !found }' "$CHANGELOG" ||
		echo "::warning::$CHANGELOG has no '## $version' section: the notes fall back to the tag annotation, and What's New won't list it."
fi

# A published version is never replaced; a leftover draft is, by the publish job.
if [[ "$(gh release view "$version" --json isDraft --jq .isDraft 2>/dev/null)" == false ]]; then
	fail "$version is already published; release a new patch version instead"
fi
if tagged="$(tagged_commit "$version")" && [[ "$tagged" != "$(git rev-parse HEAD)" ]]; then
	fail "tag $version already exists on another commit: $tagged"
fi

{
	echo "proceed=true"
	echo "version=$version"
	echo "prerelease=$prerelease"
} >>"$GITHUB_OUTPUT"
