package assets

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestLocalFetcherDecodesSpaces pins #5: the URLBuilder percent-escapes every
// segment (it builds http URLs), so a mounted pack folder with spaces arrives
// here escaped ("phoenix%20wright"). Fetch must fall back to the percent-
// DECODED spelling so the real on-disk file resolves.
func TestLocalFetcherDecodesSpaces(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join("characters", "Phoenix Wright", "(a)normal.webp")
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("SPRITE"), 0o644); err != nil {
		t.Fatal(err)
	}

	lf := NewLocalFetcher([]string{dir})
	// The escaped URL the URLBuilder would produce for a spaced folder name.
	escaped := lf.BaseURL() + "characters/Phoenix%20Wright/(a)normal.webp"
	data, err := lf.Fetch(context.Background(), escaped)
	if err != nil {
		t.Fatalf("escaped fetch failed: %v", err)
	}
	if string(data) != "SPRITE" {
		t.Errorf("got %q, want SPRITE", data)
	}
}

// TestLocalFetcherRawStillResolves proves the raw (already-unescaped) spelling
// keeps working — exported scene archives write real names to disk and address
// them with escaped URLs; the raw-first attempt must not regress a plain name.
func TestLocalFetcherRawStillResolves(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "background", "court", "defenseempty.webp")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("BG"), 0o644); err != nil {
		t.Fatal(err)
	}
	lf := NewLocalFetcher([]string{dir})
	data, err := lf.Fetch(context.Background(), lf.BaseURL()+"background/court/defenseempty.webp")
	if err != nil {
		t.Fatalf("raw fetch failed: %v", err)
	}
	if string(data) != "BG" {
		t.Errorf("got %q, want BG", data)
	}
}

// TestLocalFetcherServesADottedNameThatIsNotATraversal is the LOOSENING half of
// the safepath unification: ".." only traverses as a whole SEGMENT, so a real
// on-disk name that merely CONTAINS two dots must still resolve. A guard written
// as strings.Contains(rel, "..") passes every traversal test above and silently
// makes files like "a..b" unfetchable — a miss that looks exactly like a missing
// asset, which is the quietest way for a security tightening to break real packs.
func TestLocalFetcherServesADottedNameThatIsNotATraversal(t *testing.T) {
	dir := t.TempDir()
	// Both a dotted DIRECTORY segment and a dotted FILE stem: the folder name is
	// the case a segment-blind guard eats, and the file name is the case a
	// path-cleaning one does.
	full := filepath.Join(dir, "characters", "a..b", "(a)nor..mal.png")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("DOTS"), 0o644); err != nil {
		t.Fatal(err)
	}
	lf := NewLocalFetcher([]string{dir})
	data, err := lf.Fetch(context.Background(), lf.BaseURL()+"characters/a..b/(a)nor..mal.png")
	if err != nil {
		t.Fatalf("a dotted, non-traversing name was refused: %v", err)
	}
	if string(data) != "DOTS" {
		t.Errorf("got %q, want DOTS", data)
	}
}

// TestLocalFetcherRejectsEncodedTraversal pins the #5 security recheck: a
// "%2e%2e" that decodes into ".." must still be rejected AFTER decoding — the
// guard runs on the decoded rel, not only the raw one.
func TestLocalFetcherRejectsEncodedTraversal(t *testing.T) {
	dir := t.TempDir()
	// A secret one directory ABOVE the mount that a traversal would reach.
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(secret, []byte("TOPSECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(secret) })

	lf := NewLocalFetcher([]string{dir})
	// %2e%2e decodes to ".." — the decoded attempt must be refused. Raw
	// "%2e%2e" is not a real file (miss), and the decoded form hits the ".."
	// guard, so the net result is an error (not-found or path-escape refusal)
	// and NEVER the secret bytes.
	data, err := lf.Fetch(context.Background(), lf.BaseURL()+"%2e%2e/secret.txt")
	if err == nil {
		t.Fatalf("encoded traversal was NOT rejected — got %q (mount escape possible)", data)
	}
}
