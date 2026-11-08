package update

import (
	"runtime/debug"
	"sync"
	"time"
)

// BuildTime is when the binary was compiled, RFC 3339 in UTC, stamped by the linker with -X (see scripts/package.sh).
var BuildTime string

// shortCommitLen matches the abbreviated hashes git log prints in this repository.
const shortCommitLen = 8

// Build describes the running binary.
type Build struct {
	Version  string
	Commit   string    // full revision; "" when the build carried no VCS info
	Modified bool      // the working tree had uncommitted changes at build time
	BuiltAt  time.Time // zero when unstamped
}

// Current reads the commit from the Go toolchain's VCS stamp and the compile time from BuildTime, once.
var Current = sync.OnceValue(func() Build {
	bi, _ := debug.ReadBuildInfo()
	return buildFrom(Version, BuildTime, bi)
})

func buildFrom(version, builtAt string, bi *debug.BuildInfo) Build {
	b := Build{Version: version}
	if t, err := time.Parse(time.RFC3339, builtAt); err == nil {
		b.BuiltAt = t
	}
	if bi == nil {
		return b
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Commit = s.Value
		case "vcs.modified":
			b.Modified = s.Value == "true"
		}
	}
	return b
}

// ShortCommit is the abbreviated revision, or "" when unknown.
func (b Build) ShortCommit() string {
	if len(b.Commit) > shortCommitLen {
		return b.Commit[:shortCommitLen]
	}
	return b.Commit
}

// Label names the build for the window title: the release tag, or "dev (<commit>)" for an unstamped build.
func (b Build) Label() string {
	if b.Version != "" && b.Version != devVersion {
		return b.Version
	}
	if c := b.ShortCommit(); c != "" {
		return devVersion + " (" + c + ")"
	}
	return devVersion
}
