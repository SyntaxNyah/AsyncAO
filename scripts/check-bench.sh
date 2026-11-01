#!/usr/bin/env bash
# Fails when a gated benchmark is absent from the results or allocates over its limit.
# Usage: scripts/check-bench.sh <go-test-bench-output>
set -euo pipefail

RESULTS="${1:?usage: check-bench.sh <go-test-bench-output>}"
failed=0

check() { # benchmark max-allocs
	local allocs
	allocs="$(awk -v name="$1" '$1 ~ "^" name "(-[0-9]+)?$" { for (i = 2; i <= NF; i++) if ($i == "allocs/op") print $(i - 1) }' "$RESULTS")"
	if [[ -z "$allocs" ]]; then
		echo "$1: no result" >&2
		failed=1
	elif ((allocs > $2)); then
		echo "$1: $allocs allocs/op, limit $2" >&2
		failed=1
	fi
}

check BenchmarkBuildCandidates_Learned 1
check BenchmarkCacheHit_T1 0
check BenchmarkCacheHit_T2 0
check BenchmarkRenderFrame 0

exit "$failed"
