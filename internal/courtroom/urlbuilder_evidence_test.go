package courtroom

import "testing"

// TestEvidenceEscapedPath pins #127: an evidence image may crawl OUT of the
// evidence folder with ".." (AO asset reuse — e.g. "../characters/Ridelle/
// char_icon.png"), resolving to the real asset path clamped to the origin.
// Flat and nested names are unchanged.
func TestEvidenceEscapedPath(t *testing.T) {
	const origin = "http://x/base/"
	u := NewURLBuilder(origin)

	if got := u.Evidence("knife.png"); got != origin+"evidence/knife.png" {
		t.Errorf("flat evidence = %q, want %q", got, origin+"evidence/knife.png")
	}
	if got := u.Evidence("cases/knife.png"); got != origin+"evidence/cases/knife.png" {
		t.Errorf("nested evidence = %q, want %q", got, origin+"evidence/cases/knife.png")
	}
	// #127: ".." crawls out of evidence/ and resolves (clamped) under the origin.
	if got := u.Evidence("../characters/Ridelle/char_icon.png"); got != origin+"characters/ridelle/char_icon.png" {
		t.Errorf("escaped evidence = %q, want %q", got, origin+"characters/ridelle/char_icon.png")
	}
	// The clamp can never escape the origin root.
	if got := u.Evidence("../../../../etc/passwd.png"); got != origin+"etc/passwd.png" {
		t.Errorf("traversal evidence = %q, want %q (clamped)", got, origin+"etc/passwd.png")
	}
	// Bare names still default to .png.
	if got := u.Evidence("knife"); got != origin+"evidence/knife.png" {
		t.Errorf("bare evidence = %q, want %q", got, origin+"evidence/knife.png")
	}
}
