package hwid

import (
	"regexp"
	"strings"
	"testing"
)

var hdidRe = regexp.MustCompile(`^asyncao-[0-9a-f]{64}$`)

// Compute must be stable within a process, correctly shaped, and never empty —
// servers key bans on it, so an unstable or malformed id breaks moderation.
func TestComputeStableAndShaped(t *testing.T) {
	a := Compute()
	if a == "" {
		t.Fatal("empty HDID")
	}
	if !hdidRe.MatchString(a) {
		t.Errorf("HDID %q does not match %v", a, hdidRe)
	}
	if b := Compute(); a != b {
		t.Errorf("HDID not stable across calls: %q != %q", a, b)
	}
}

// compute() (the un-memoised core) must be deterministic: two runs on the same
// machine produce the same id — that is what makes a ban stick.
func TestComputeDeterministic(t *testing.T) {
	first, second := compute(), compute()
	if first != second {
		t.Errorf("compute() is non-deterministic: %q != %q", first, second)
	}
}

// roots() must not panic and must be deterministic; on a normal machine it reads
// at least one stable root, but a bare environment legitimately has none (then
// compute() uses the hostname), so an empty result is not a failure.
func TestRootsDeterministic(t *testing.T) {
	first, second := roots(), roots()
	if len(first) != len(second) {
		t.Errorf("roots() changed between calls: %d != %d", len(first), len(second))
	}
	for _, r := range first {
		if !strings.Contains(r, "=") {
			t.Errorf("root %q is not label=value", r)
		}
	}
}
