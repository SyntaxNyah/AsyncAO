package update

import (
	"runtime/debug"
	"testing"
	"time"
)

func TestBuildFromReadsStampAndVCS(t *testing.T) {
	const (
		rev     = "8560509d0123456789abcdef0123456789abcdef"
		stamped = "2026-11-08T14:30:00Z"
		year    = 2026
		hour    = 14
		minute  = 30
		day     = 8
	)
	bi := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: rev},
		{Key: "vcs.modified", Value: "true"},
	}}
	b := buildFrom("v2.1.0", stamped, bi)
	if b.Commit != rev || !b.Modified {
		t.Fatalf("commit = %q modified = %v, want %q true", b.Commit, b.Modified, rev)
	}
	want := time.Date(year, time.November, day, hour, minute, 0, 0, time.UTC)
	if !b.BuiltAt.Equal(want) {
		t.Fatalf("BuiltAt = %v, want %v", b.BuiltAt, want)
	}
	if got := b.ShortCommit(); got != "8560509d" {
		t.Fatalf("ShortCommit = %q", got)
	}
	if got := b.Label(); got != "v2.1.0" {
		t.Fatalf("Label = %q, want the tag", got)
	}
}

func TestBuildFromUnstamped(t *testing.T) {
	b := buildFrom(devVersion, "", nil)
	if !b.BuiltAt.IsZero() || b.Commit != "" {
		t.Fatalf("unstamped build = %+v, want zero time and no commit", b)
	}
	if got := b.Label(); got != devVersion {
		t.Fatalf("Label = %q, want %q", got, devVersion)
	}
	b.Commit = "abcdef0123456789"
	if got := b.Label(); got != "dev (abcdef01)" {
		t.Fatalf("Label = %q, want the dev label with the short commit", got)
	}
}

func TestBuildFromIgnoresMalformedTime(t *testing.T) {
	if b := buildFrom("v1.0.0", "yesterday", nil); !b.BuiltAt.IsZero() {
		t.Fatalf("BuiltAt = %v, want zero for a malformed stamp", b.BuiltAt)
	}
}
