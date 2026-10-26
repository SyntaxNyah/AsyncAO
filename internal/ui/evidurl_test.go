package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestEvidURL pins the evidence panel's "stream from server" toggle resolution:
// local (default) reads the cached local:// origin, stream reads the server URL,
// and a local request with no mounts falls back to streaming.
func TestEvidURL(t *testing.T) {
	a := &App{}
	a.urls = courtroom.NewURLBuilder("https://server/base")

	// Default (local) with a cached local:// origin.
	a.evidLocalOrigin = "local://m-x/"
	if got := a.evidURL("knife"); got != "local://m-x/evidence/knife.png" {
		t.Errorf("local = %q, want %q", got, "local://m-x/evidence/knife.png")
	}

	// Streaming from the server.
	a.evidStream = true
	if got := a.evidURL("knife"); got != "https://server/base/evidence/knife.png" {
		t.Errorf("stream = %q, want %q", got, "https://server/base/evidence/knife.png")
	}

	// Local requested but no mounts → fall back to streaming.
	a.evidStream = false
	a.evidLocalOrigin = ""
	if got := a.evidURL("knife"); got != "https://server/base/evidence/knife.png" {
		t.Errorf("no-mount fallback = %q, want %q", got, "https://server/base/evidence/knife.png")
	}
}
