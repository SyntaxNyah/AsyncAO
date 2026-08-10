package safepath

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestUnsafeRelRefusesPathEscape is the predicate gate, MOVED here from
// internal/assets (it was TestMountIndexRefusesPathEscape and tested nothing
// about mounting). The table keeps the mount cases and gains the archive ones,
// because zip entry names are the same predicate's other caller.
func TestUnsafeRelRefusesPathEscape(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		// The mount cases, verbatim from the test this replaces.
		"../outside.png", "a/../../outside.png", "/etc/passwd",
		`\windows\system32`, "C:/windows", "",
		// Zip-slip: hostile ARCHIVE entry names, which arrive with either
		// separator because the name is attacker-controlled metadata.
		"../../evil.png", `..\..\evil.png`, `a\..\..\evil.png`,
		"characters/../../evil.png", `C:\windows\system32\evil.png`,
		// Windows alternate data stream: "png" is a stream on the real file.
		"sprite.png:hidden",
	} {
		if !UnsafeRel(rel) {
			t.Errorf("UnsafeRel(%q) = false, want true", rel)
		}
		if _, err := Join(root, rel); err == nil {
			t.Errorf("Join accepted %q — the refusal must reach the caller as an error", rel)
		}
	}
	for _, rel := range []string{
		"characters/witch/(a)normal.png", "background/court/defenseempty.webp",
		"sounds/music/track.opus",
		// A name that merely CONTAINS ".." is a legal filename; only a whole
		// ".." SEGMENT can traverse.
		"characters/a..b/(a)normal.png", "misc/....png",
		// Theme-shaped paths, the second caller this package exists for.
		"themes/umineko/courtroom_design.ini", "asyncao/media/butterflies.webp",
	} {
		if UnsafeRel(rel) {
			t.Errorf("UnsafeRel(%q) = true, want false — a legitimate path was refused", rel)
		}
	}
}

// TestJoinStaysInsideItsRoot pins that an accepted rel really does land under
// root, and that a refused one yields ErrUnsafe rather than a silently cleaned
// path (the failure mode where the guard reads as if it works).
func TestJoinStaysInsideItsRoot(t *testing.T) {
	root := t.TempDir()
	got, err := Join(root, "characters/witch/(a)normal.png")
	if err != nil {
		t.Fatalf("Join on a legitimate rel: %v", err)
	}
	if !strings.HasPrefix(got, root+string(filepath.Separator)) {
		t.Errorf("Join = %q, which is not under %q", got, root)
	}
	if _, err := Join(root, "../secret.txt"); err == nil {
		t.Error("Join accepted a traversal")
	} else if err != ErrUnsafe {
		t.Errorf("Join error = %v, want ErrUnsafe so callers can tell refusal from absence", err)
	}
}

// TestTooDeepBoundsRecursion pins the depth cap on both separators: a hostile
// archive that used backslashes would otherwise walk as deep as it liked.
func TestTooDeepBoundsRecursion(t *testing.T) {
	shallow := "characters/witch/(a)normal.png"
	if TooDeep(shallow, MaxDepth) {
		t.Errorf("TooDeep(%q) = true — a normal AO path was refused", shallow)
	}
	deep := strings.Repeat("a/", MaxDepth+2) + "sprite.png"
	if !TooDeep(deep, MaxDepth) {
		t.Errorf("TooDeep(%q) = false, want true", deep)
	}
	backslashed := strings.Repeat(`a\`, MaxDepth+2) + "sprite.png"
	if !TooDeep(backslashed, MaxDepth) {
		t.Error("a backslash-separated path escaped the depth bound")
	}
	// Exactly AT the cap is refused, one below is allowed — the bound is the
	// same ">=" the mount index has always used.
	if TooDeep(strings.Repeat("a/", MaxDepth-1)+"x.png", MaxDepth) {
		t.Error("one level below the cap was refused")
	}
	if !TooDeep(strings.Repeat("a/", MaxDepth)+"x.png", MaxDepth) {
		t.Error("exactly at the cap was allowed")
	}
}

// TestWalkFilesSkipsSymlinks is the safepath-level twin of
// TestMountIndexSkipsSymlinks: it closes an exfiltration route where a
// third-party pack ships a link at a plausible asset path pointing at a private
// file, which would be read and uploaded as a texture. The ".." guard does not
// cover it, because the link's own path contains no "..".
func TestWalkFilesSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "characters", "witch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "characters", "witch", "(b)normal.png"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "characters", "witch", "(a)normal.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err) // Windows without dev mode
	}

	got := walkAll(t, root)
	if len(got) != 1 || got[0] != "characters/witch/(b)normal.png" {
		t.Errorf("walk yielded %v — a symlinked file was visited", got)
	}
}

// TestUnsafePathAsksLstatNotStat drives the by-path refusal over the answers that
// separate it from a stat-based lookalike. THE STAT VERSION PASSES A NAIVE TEST:
// os.Stat on a link to a regular file says "regular file", so a gate that only checked
// "link to a file is refused, plain file is not" would stay green on an implementation
// that follows the link and can never refuse anything.
//
// The link-to-a-DIRECTORY case is the one the mount fetcher's fold depends on (a
// stat-based answer calls it a directory and descends straight out of the mount), and
// the DANGLING case is the one that must not be reported as absent — foldLookup finds
// it by name, so "not refused" there is a refusal that never fires.
//
// The two ALLOWED answers carry as much weight as the refusals: a directory must stay
// traversable or foldJoin cannot descend at all, and a regular file must stay readable
// or the fold serves nothing. A predicate that refuses everything is the other way to
// pass a refusal-only gate. The non-regular NON-links this also refuses need a device
// node to demonstrate — safepath_unix_test.go holds that half.
func TestUnsafePathAsksLstatNotStat(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(root, "real.png")
	if err := os.WriteFile(file, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(root, "up")
	if err := os.Symlink(outside, dirLink); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err) // Windows without dev mode
	}
	fileLink := filepath.Join(root, "linked.png")
	if err := os.Symlink(file, fileLink); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "gone.png")
	if err := os.Symlink(filepath.Join(outside, "never-written"), dangling); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		what string
		path string
		want bool
	}{
		{"a link to a directory", dirLink, true},
		{"a link to a regular file", fileLink, true},
		{"a dangling link", dangling, true},
		{"a regular file", file, false},
		{"the directory the link points at", outside, false},
		{"a plain directory the fold must descend into", root, false},
		{"a name nothing is at", filepath.Join(root, "absent.png"), false},
	} {
		if got := UnsafePath(tc.path); got != tc.want {
			t.Errorf("UnsafePath(%s) = %v, want %v", tc.what, got, tc.want)
		}
	}
}

// TestWalkFilesYieldsSlashedRelsAndStops pins the contract the mount index and
// the theme packer both build on: forward-slashed root-relative names, and a
// false return that stops the walk (that is how each caller enforces its own cap).
func TestWalkFilesYieldsSlashedRelsAndStops(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"characters/witch/(a)normal.png",
		"background/court/defenseempty.webp",
		"courtroom_design.ini",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := walkAll(t, root)
	want := []string{"background/court/defenseempty.webp", "characters/witch/(a)normal.png", "courtroom_design.ini"}
	if len(got) != len(want) {
		t.Fatalf("walk yielded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("walk[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	seen := 0
	if err := WalkFiles(root, MaxDepth, func(string) bool { seen++; return false }); err != nil {
		t.Fatalf("WalkFiles: %v", err)
	}
	if seen != 1 {
		t.Errorf("the walk visited %d entries after fn returned false, want 1", seen)
	}
}

// TestWalkFilesDropsPathsPastTheDepthCap pins that the bound is applied DURING
// the walk, so a pathological tree cannot cost the caller anything but the walk
// itself.
func TestWalkFilesDropsPathsPastTheDepthCap(t *testing.T) {
	root := t.TempDir()
	deepRel := strings.Repeat("a/", MaxDepth+2) + "sprite.png"
	p := filepath.Join(root, filepath.FromSlash(deepRel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Skipf("host refused a %d-deep tree: %v", MaxDepth+2, err) // path length limits
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Skipf("host refused a deep file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "shallow.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := walkAll(t, root)
	if len(got) != 1 || got[0] != "shallow.png" {
		t.Errorf("walk yielded %v, want only the shallow file", got)
	}
}

// TestWalkFilesReportsAnUnreadableRoot pins the one error that must surface: a
// missing mount is the mount itself failing, not a subtree we can skip.
func TestWalkFilesReportsAnUnreadableRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")
	if err := WalkFiles(missing, MaxDepth, func(string) bool { return true }); err == nil {
		t.Error("walking a missing root reported success")
	}
}

// walkAll collects every rel a walk yields, sorted so the assertion does not
// depend on directory order.
func walkAll(t *testing.T, root string) []string {
	t.Helper()
	var got []string
	if err := WalkFiles(root, MaxDepth, func(rel string) bool {
		got = append(got, rel)
		return true
	}); err != nil {
		t.Fatalf("WalkFiles: %v", err)
	}
	sort.Strings(got)
	return got
}
