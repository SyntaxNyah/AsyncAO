package assets

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The SYMLINK half of the mount fetcher's case-fold refusal, in one file. The refusal
// itself is wider — safepath.UnsafePath refuses every name that is neither a plain
// directory nor a regular file — and the non-link half needs mkfifo, so it lives in the
// unix-only localnonregular_unix_test.go beside the parity gate that drives both byte
// sources over one tree.
//
// The fold (local.go attempt 3) enumerates directories — os.Open + ReadDir — to spell
// a segment the URL got the case of wrong. An adversarial mount review pointed that
// capability at a link and walked straight out of the mount: the two probe shapes it
// used are the first two gates below, and they failed before foldJoin learned to lstat
// its candidate. safepath.WalkFiles refuses the same route for the mount INDEX and says
// why in its doc.
//
// The third gate is the SCOPE line: an exactly-spelled path still reads through a link,
// because that is pre-existing policy for a name the caller wrote out in full and the
// fold was never what granted it. The fourth is the happy path, so the refusal is
// visible as aimed at what a name IS rather than at folding.

// mustSymlink links link -> target, skipping the test when the host cannot make one
// (Windows without developer mode) — the same accommodation safepath's own
// TestWalkFilesSkipsSymlinks makes.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
}

// skipIfTheLoweredPathResolves skips when p — the lowercased spelling the URL builder
// mints — already names something on disk, i.e. the filesystem folds case itself
// (the Windows dev box, a default APFS volume). There the EXACT spelling answers at
// attempt 1 and the fold never runs, so these gates would be measuring os.ReadFile's
// unchanged read-through-a-link policy instead of the fold's refusal.
func skipIfTheLoweredPathResolves(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); err == nil {
		t.Skipf("this filesystem folds case (%s resolves): the exact spelling answers before the fold", p)
	}
}

// TestLocalFetcherFoldRefusesASymlinkedDirectory is the review's first probe, driven
// through Fetch: a pack ships characters/Up pointing OUT of the mount, and the
// lowercased URL characters/up/secret.txt asks the fold to spell "Up" for it. Before
// the lstat refusal the fold enumerated characters/, matched "Up", descended through
// the link and served a file that was never in the mount at all.
//
// The miss it becomes must be an ORDINARY miss: rule 6 memoizes it like any absent
// asset, so a hostile pack cannot turn every re-demand into a fresh directory scan
// (the courtroom re-Prefetches the desk on every IC message). A refusal that returned
// an ERROR instead would skip the memo — deliberately not what this does; there is
// nothing to report to a user about a link, and "missing" is the truth from the URL's
// point of view.
func TestLocalFetcherFoldRefusesASymlinkedDirectory(t *testing.T) {
	mount, outside := t.TempDir(), t.TempDir()
	const secret = "TOPSECRET-OUTSIDE-THE-MOUNT"
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}
	chars := filepath.Join(mount, "characters")
	if err := os.MkdirAll(chars, 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, filepath.Join(chars, "Up"))
	skipIfTheLoweredPathResolves(t, filepath.Join(chars, "up"))

	lf := NewLocalFetcher([]string{mount})
	url := lf.BaseURL() + "characters/up/secret.txt"
	data, err := lf.Fetch(context.Background(), url)
	if err == nil {
		t.Fatalf("the fold served %q through a symlinked directory — a mounted pack can exfiltrate "+
			"any file the client can read", data)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("bytes from outside the mount were returned alongside the error: %q", data)
	}
	// And the refusal memoizes like any other conclusive miss (rule 6): the second
	// fetch answers from the memo without re-scanning characters/.
	if _, err := lf.Fetch(context.Background(), url); err == nil || !strings.Contains(err.Error(), "negative-cached") {
		t.Errorf("second fetch answered %v — a refused fold must memoize as an ordinary miss", err)
	}
}

// TestLocalFetcherFoldRefusesASymlinkedFile is the review's second probe and the
// FILE half of the policy decision. A pack ships characters/Evil/(a)Normal.png as a
// link to /outside/id_rsa; every sprite URL the client mints is lowercase, so only the
// fold can reach that name — and reaching it meant decoding a private key as a texture.
//
// The decision is to refuse it. Refusing only DIRECTORY links would close the traversal
// and leave the exfiltration — the exact case safepath.WalkFiles' doc describes ("a link
// at characters/x/(a)a.png pointing at ~/.ssh/id_rsa would be indexed, read, and uploaded
// as a texture"), and that walk skips a linked FILE just as flatly as a linked directory.
// The rest of WalkFiles' shape — every OTHER non-regular entry — is pinned by
// localnonregular_unix_test.go; the first draft of this refusal claimed that shape while
// covering only the links these two gates drive.
func TestLocalFetcherFoldRefusesASymlinkedFile(t *testing.T) {
	mount, outside := t.TempDir(), t.TempDir()
	const key = "PRIVATE KEY"
	secret := filepath.Join(outside, "id_rsa")
	if err := os.WriteFile(secret, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(mount, "characters", "Evil")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, secret, filepath.Join(folder, "(a)Normal.png"))
	skipIfTheLoweredPathResolves(t, filepath.Join(mount, "characters", "evil"))

	lf := NewLocalFetcher([]string{mount})
	data, err := lf.Fetch(context.Background(), lf.BaseURL()+"characters/evil/(a)normal.png")
	if err == nil {
		t.Fatalf("the fold served %q through a symlinked file — a lowercased sprite URL read a file "+
			"outside the mount", data)
	}
	if strings.Contains(string(data), key) {
		t.Fatalf("bytes from outside the mount were returned alongside the error: %q", data)
	}
}

// TestLocalFetcherExactSpellingStillReadsThroughASymlink is the SCOPE pin, and it exists
// to make the line visible rather than to praise it: attempts 1-2 are one os.ReadFile on
// a path the caller spelled byte-for-byte, with no enumeration behind them, and they
// have followed links since the fetcher was written. The fold is what was new in this
// batch and what must not WIDEN that reach; narrowing the exact path is a separate
// decision with separate consequences (an exported scene archive may legitimately be a
// tree of links), and it is deliberately not taken here.
//
// It needs no case-sensitivity skip: the exact spelling resolves on every filesystem,
// which is the point.
func TestLocalFetcherExactSpellingStillReadsThroughASymlink(t *testing.T) {
	mount, outside := t.TempDir(), t.TempDir()
	const body = "LINKED-SPRITE"
	target := filepath.Join(outside, "sprite.png")
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(mount, "characters", "Evil")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, target, filepath.Join(folder, "(a)Normal.png"))

	lf := NewLocalFetcher([]string{mount})
	// The exact on-disk spelling, escaped the way an exported archive addresses it.
	data, err := lf.Fetch(context.Background(), lf.BaseURL()+"characters/Evil/(a)Normal.png")
	if err != nil {
		t.Fatalf("an exactly-spelled path through a link stopped resolving: %v — the fold's symlink "+
			"policy has leaked onto attempts 1-2, which is a behavior change, not this fix", err)
	}
	if string(data) != body {
		t.Errorf("got %q, want %q", data, body)
	}
}

// TestLocalFetcherFoldStillResolvesAPlainDirectoryBesideASymlinkedOne is the happy-path
// no-regression pin, and it puts the link IN THE DIRECTORY THE SCAN READS. A refusal
// written one level too coarse — abandoning the scan when it meets a link, or refusing
// the parent that contains one — would pass both gates above while quietly making
// authored-case packs unreachable again on Linux, which is the whole reason the fold
// exists.
func TestLocalFetcherFoldStillResolvesAPlainDirectoryBesideASymlinkedOne(t *testing.T) {
	mount, outside := t.TempDir(), t.TempDir()
	chars := filepath.Join(mount, "characters")
	real := filepath.Join(chars, "Phoenix")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "SPRITE"
	if err := os.WriteFile(filepath.Join(real, "(a)Normal.png"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// A link sitting beside the real pack, inside the very directory the fold scans, so
	// the scan meets a refused entry on its way to the one it must answer with.
	mustSymlink(t, outside, filepath.Join(chars, "Aunt"))
	skipIfTheLoweredPathResolves(t, filepath.Join(chars, "phoenix"))

	lf := NewLocalFetcher([]string{mount})
	data, err := lf.Fetch(context.Background(), lf.BaseURL()+"characters/phoenix/(a)normal.png")
	if err != nil {
		t.Fatalf("an authored-case pack stopped resolving next to a symlink: %v", err)
	}
	if string(data) != body {
		t.Errorf("got %q, want %q", data, body)
	}
}
