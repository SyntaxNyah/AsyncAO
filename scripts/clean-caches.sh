#!/usr/bin/env bash
# Deletes this ref's build caches. Needs GH_TOKEN with actions: write and GH_REPO.
# Usage: scripts/clean-caches.sh <ref> [run-id]
#   With a run id, keeps what that finished run used: anything else lost its key to a
#   dependency change. Without one, the ref is gone and everything goes.
set -euo pipefail

REF="${1:?usage: clean-caches.sh <ref> [run-id]}"
RUN_ID="${2:-}"
CACHE_KEYS='^(setup-go-|msys2-pkgs|setup-sdl-)'
TIMESTAMP_LENGTH=19 # seconds precision; the API mixes fractional and whole seconds

filter=".actions_caches[] | select(.key | test(\"$CACHE_KEYS\"))"
if [[ -n "$RUN_ID" ]]; then
	started="$(gh api "repos/$GH_REPO/actions/runs/$RUN_ID" --jq .run_started_at)"
	filter+=" | select(.last_accessed_at[0:$TIMESTAMP_LENGTH] < \"${started:0:$TIMESTAMP_LENGTH}\")"
fi

# Each delete changes the listing, so collect first.
ids="$(gh api --paginate "repos/$GH_REPO/actions/caches?ref=$REF&per_page=100" --jq "$filter | .id")"
for id in $ids; do
	gh api -X DELETE "repos/$GH_REPO/actions/caches/$id" >/dev/null
	echo "deleted cache $id"
done
