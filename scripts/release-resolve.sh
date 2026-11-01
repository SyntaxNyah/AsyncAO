#!/usr/bin/env bash
# Decides whether this run releases, and what. Writes proceed, version and
# prerelease to GITHUB_OUTPUT. Needs a full clone and GH_TOKEN.
# Usage: scripts/release-resolve.sh changelog|tag <version-if-tag>
set -euo pipefail

SOURCE="${1:?usage: release-resolve.sh changelog|tag <version-if-tag>}"
CHANGELOG=internal/ui/assets/CHANGELOG.md

proceed=true
if [[ "$SOURCE" == changelog ]]; then
	version="$(awk '/^## v/ { print $2; exit }' "$CHANGELOG")"
	if [[ -z "$version" ]]; then
		echo "::error::no '## vX.Y.Z' heading in $CHANGELOG"
		exit 1
	fi
	if git ls-remote --exit-code origin "refs/tags/$version" >/dev/null; then
		echo "::notice::$version is already tagged, nothing to release."
		proceed=false
	fi
else
	version="${2:?tag version required}"
fi

if ! [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
	echo "::error::'$version' is not a vMAJOR.MINOR.PATCH[-prerelease] version"
	exit 1
fi

prerelease=false
if [[ "$version" == *-* ]]; then
	prerelease=true
elif [[ "$SOURCE" == tag ]] && ! git merge-base --is-ancestor HEAD origin/main; then
	echo "::error::stable tag $version is not on main; only hyphenated prerelease tags may come from other branches"
	exit 1
fi

# A published version is never replaced; a leftover draft is, by the publish job.
if [[ "$proceed" == true ]] && [[ "$(gh release view "$version" --json isDraft -q .isDraft 2>/dev/null)" == false ]]; then
	echo "::error::$version is already published; release a new patch version instead"
	exit 1
fi

{
	echo "proceed=$proceed"
	echo "version=$version"
	echo "prerelease=$prerelease"
} >>"$GITHUB_OUTPUT"
