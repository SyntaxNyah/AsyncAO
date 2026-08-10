package assets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TestLocalFetcherResolvesAnAuthoredCaseMount pins the cross-platform half of the
// same problem TestLocalFetcherDecodesSpaces pins the escaping half of. The
// URLBuilder LOWERCASES every segment it mints (courtroom seg/charSeg — asset
// hosts are case-sensitive, webAO-convention mirrors ship lowercase), while a
// mount folder carries the case its author typed. NTFS and a default APFS volume
// call those the same file; ext4 does not, so on the Linux AppImage/Flatpak every
// asset of a capitalised pack folder missed — the whole character, not an edge.
//
// Nesting is incidental to it: this is the FLAT case, the ~99% one, and it failed
// exactly as the nested one did (internal/courtroom
// TestNestedCharacterResolvesOnALocalMount).
func TestLocalFetcherResolvesAnAuthoredCaseMount(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "characters", "Phoenix Wright", "(a)normal.png")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("SPRITE"), 0o644); err != nil {
		t.Fatal(err)
	}
	lf := NewLocalFetcher([]string{dir})

	// Byte-for-byte what URLBuilder.Emote("Phoenix Wright", "normal", EmoteIdle)
	// plus the resolver's ".png" candidate produces.
	data, err := lf.Fetch(context.Background(), lf.BaseURL()+"characters/phoenix%20wright/(a)normal.png")
	if err != nil {
		t.Fatalf("an authored-case mount folder must resolve: %v", err)
	}
	if string(data) != "SPRITE" {
		t.Errorf("got %q, want SPRITE", data)
	}
	// And a case-insensitive walk must not make misses inconclusive: an absent
	// sibling is still a miss, in the same folder the walk just resolved.
	if _, err := lf.Fetch(context.Background(), lf.BaseURL()+"characters/phoenix%20wright/(b)normal.png"); err == nil {
		t.Error("an absent sibling resolved — the fold walk is inventing hits")
	}
}

// TestLocalFetcherExactNameOutranksACaseFold is the ATTEMPT-ORDER half of the
// containment argument, and only that half: both URLs here name a file that exists
// byte-for-byte, so both are answered by attempt 1's os.ReadFile and the fold walk
// never runs at all. What it therefore pins is that the walk stays the LAST spelling
// tried — a fold hoisted above the exact reads would answer "(a)normal.png" with
// "(a)Normal.png" (or the other way round) and re-point a lookup that already
// worked.
//
// The walk's OWN preference — an exact segment beating a fold-equal sibling INSIDE
// the scan — is a different rule and is pinned by
// TestLocalFetcherFoldPrefersTheExactlyCasedSegment and
// TestFoldLookupReturnsTheExactNameOverASmallerVariant below. Deleting both fold
// mechanisms left this test green, which is what made the distinction worth writing
// down rather than assuming.
func TestLocalFetcherExactNameOutranksACaseFold(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "characters", "phoenix")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ name, body string }{
		{"(a)normal.png", "LOWER"},
		{"(a)Normal.png", "AUTHORED"},
	} {
		if err := os.WriteFile(filepath.Join(folder, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// On a case-FOLDING filesystem (the Windows dev box, a default APFS volume) the
	// second write replaced the first rather than failing, so the situation this
	// pins cannot arise there — and neither can the walk, which only ever runs after
	// an exact spelling has already missed.
	ents, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Skipf("this filesystem folds case (%d entries for two spellings)", len(ents))
	}
	lf := NewLocalFetcher([]string{dir})
	for _, tc := range []struct{ url, want string }{
		{"characters/phoenix/(a)normal.png", "LOWER"},
		{"characters/phoenix/(a)Normal.png", "AUTHORED"},
	} {
		data, err := lf.Fetch(context.Background(), lf.BaseURL()+tc.url)
		if err != nil {
			t.Fatalf("%s: %v", tc.url, err)
		}
		if string(data) != tc.want {
			t.Errorf("%s served %q, want %q — an exact name lost to a case fold", tc.url, data, tc.want)
		}
	}
}

// TestLocalFetcherFoldPrefersTheExactlyCasedSegment is the walk's own containment
// pin, and the one that reaches attempt 3 with a CHOICE to make.
//
// The mount holds two character folders differing only in case, and each holds the
// emote under the OTHER spelling, so the URL misses both exact spellings and the
// fold has to pick a directory. The rule is "an exact name wins outright", which
// foldJoin spells as an os.Stat preference and foldLookup as an early return on an
// exact entry — two expressions of one guarantee. Remove both and the tie-break
// takes over: 'P' (0x50) sorts below 'p' (0x70), so the walk would step into
// characters/Phoenix and serve a DIFFERENT sprite for the same URL, on a rule
// nothing about the request asked for.
//
// (Either mechanism alone still answers correctly — each masks the other for any
// entry a bounded scan can see — so this is the pin on the guarantee rather than on
// one line of it; foldLookup's half is driven directly below.)
func TestLocalFetcherFoldPrefersTheExactlyCasedSegment(t *testing.T) {
	dir := t.TempDir()
	chars := filepath.Join(dir, "characters")
	for _, f := range []struct{ dir, name, body string }{
		{"phoenix", "(a)Wave.png", "EXACTLY-CASED-DIR"},
		{"Phoenix", "(a)wave.png", "FOLDED-DIR"},
	} {
		folder := filepath.Join(chars, f.dir)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	skipIfFilesystemFoldsCase(t, chars)

	lf := NewLocalFetcher([]string{dir})
	// Lowercase, exactly as the URL builder mints it (courtroom seg/charSeg).
	data, err := lf.Fetch(context.Background(), lf.BaseURL()+"characters/phoenix/(a)wave.png")
	if err != nil {
		t.Fatalf("the fold walk found neither spelling: %v", err)
	}
	if string(data) != "EXACTLY-CASED-DIR" {
		t.Errorf("the walk served %q, want EXACTLY-CASED-DIR — an exactly-spelled segment lost to a "+
			"lexicographically smaller case-variant sibling, so the same URL names a different file", data)
	}
}

// TestFoldLookupReturnsTheExactNameOverASmallerVariant drives foldLookup itself,
// which is where the second half of that guarantee lives: the scan meets the exact
// name and returns it on the spot instead of carrying on and letting the
// smallest-name tie-break answer.
//
// It is the seam being driven, not mirrored — the tie-break it must beat is the one
// TestLocalFetcherFoldPicksTheSameNameEveryTime relies on, and 'W' (0x57) sorts
// below 'w' (0x77), so a missing early return is visible in one call.
func TestFoldLookupReturnsTheExactNameOverASmallerVariant(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"(a)Wave.png", "(a)wave.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	skipIfFilesystemFoldsCase(t, dir)

	const want = "(a)wave.png"
	got, ok := foldLookup(dir, want)
	if !ok {
		t.Fatal("foldLookup found nothing for a name that is in the directory")
	}
	if got != want {
		t.Errorf("foldLookup(%q) = %q — an exact entry must win outright, or the smallest fold-equal "+
			"name answers a request that named a file of its own", want, got)
	}
}

// TestLocalFetcherFoldNeverOutranksAnExactHitInALaterMount pins the search ORDER
// the fold was added under: spelling-major, mount-minor. A raw hit in the LAST mount
// already beats a decoded hit in the first, and the fold extends that ladder at the
// bottom rather than joining the per-mount loop.
//
// TWO MOUNTS is what makes the order observable at all: with one mount, foldJoin's
// own os.Stat preference answers identically whichever way round the attempts run,
// so a fold hoisted to the front of Fetch keeps every single-mount gate green while
// silently re-pointing this lookup — a URL that resolves today to the SECOND mount's
// exactly-named file would start answering with the FIRST mount's case-variant.
func TestLocalFetcherFoldNeverOutranksAnExactHitInALaterMount(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	folded := filepath.Join(first, "characters", "Phoenix")
	exact := filepath.Join(second, "characters", "phoenix")
	for _, f := range []struct{ dir, body string }{
		{folded, "FOLDED-IN-THE-FIRST-MOUNT"},
		{exact, "EXACT-IN-THE-SECOND-MOUNT"},
	} {
		if err := os.MkdirAll(f.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.dir, "(a)normal.png"), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(first, "characters", "phoenix")); err == nil {
		t.Skip("this filesystem folds case: the first mount answers the exact spelling too")
	}

	lf := NewLocalFetcher([]string{first, second})
	data, err := lf.Fetch(context.Background(), lf.BaseURL()+"characters/phoenix/(a)normal.png")
	if err != nil {
		t.Fatalf("neither mount answered: %v", err)
	}
	if string(data) != "EXACT-IN-THE-SECOND-MOUNT" {
		t.Errorf("the fetch served %q, want EXACT-IN-THE-SECOND-MOUNT — a case-folded hit in an EARLIER "+
			"mount outranked an exact hit in a later one, which re-points every lookup that resolves today", data)
	}
}

// skipIfFilesystemFoldsCase skips when dir holds fewer entries than were written
// into it — i.e. the second spelling replaced the first instead of joining it, which
// is what the Windows dev box and a default APFS volume do. The situation these
// gates describe cannot arise there, and neither can the walk: it only ever runs
// after an exact spelling has already missed.
func skipIfFilesystemFoldsCase(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Skipf("this filesystem folds case (%d entries in %s for two spellings)", len(ents), dir)
	}
}

// TestLocalFetcherFoldPicksTheSameNameEveryTime pins the tie-break. Two entries
// that differ only in case, NEITHER of them the spelling asked for, is the one
// case where "search the directory" has more than one defensible answer — and
// answering with whichever the directory listed first would make one machine
// serve a different sprite than the next, and the same machine a different one
// after a rehash. The rule is the byte-lexicographically smallest fold-equal
// name, which no filesystem gets a vote in.
func TestLocalFetcherFoldPicksTheSameNameEveryTime(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "characters", "phoenix")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	// 'O' (0x4f) sorts below 'o' (0x6f), so "(a)NORMAL.png" is the answer — and it
	// is deliberately NOT the one a "first entry wins" walk would tend to find on
	// an ext4 hash order, nor the one written first.
	for _, f := range []struct{ name, body string }{
		{"(a)Normal.png", "TITLE"},
		{"(a)NORMAL.png", "SHOUT"},
	} {
		if err := os.WriteFile(filepath.Join(folder, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ents, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Skipf("this filesystem folds case (%d entries for two spellings)", len(ents))
	}
	lf := NewLocalFetcher([]string{dir})
	// Repeated because the fetcher memoizes MISSES only: every call re-walks, so a
	// walk whose answer depended on directory state would show up here.
	const fetchesToProveStability = 8
	for i := 0; i < fetchesToProveStability; i++ {
		data, err := lf.Fetch(context.Background(), lf.BaseURL()+"characters/phoenix/(a)normal.png")
		if err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
		if string(data) != "SHOUT" {
			t.Fatalf("fetch %d served %q, want SHOUT — the tie-break is not the smallest name", i, data)
		}
	}
}

// TestLocalFetcherFoldScansAWholeDirectory pins that the chunked read keeps
// reading. localFoldScanChunk bounds ONE batch, not the scan, and a loop that
// quietly stopped after the first batch would still pass every other test here
// (a character folder holds a handful of files) while silently failing exactly
// the packs big enough to matter.
func TestLocalFetcherFoldScansAWholeDirectory(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "characters", "Phoenix")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	// Comfortably more than one batch, so whatever order the filesystem lists them
	// in, most of these names live past the first chunk.
	const emotes = localFoldScanChunk + localFoldScanChunk/2
	for i := 0; i < emotes; i++ {
		name := fmt.Sprintf("(a)Emote%03d.png", i)
		if err := os.WriteFile(filepath.Join(folder, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lf := NewLocalFetcher([]string{dir})
	for i := 0; i < emotes; i++ {
		want := fmt.Sprintf("(a)Emote%03d.png", i)
		url := lf.BaseURL() + "characters/phoenix/" + strings.ToLower(want)
		data, err := lf.Fetch(context.Background(), url)
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		if string(data) != want {
			t.Fatalf("%s served %q, want %q", url, data, want)
		}
	}
}

// TestLocalFetcherFoldStopsAtTheDepthBound pins localFoldMaxDepth: the walk stats
// and scans per segment, so the segment count is one of the three dimensions rule
// 4 makes it carry a cap on. The pair differs in nothing but depth — one
// separator inside the bound resolves, one AT it does not — so the cap cannot be
// deleted or widened without this failing.
func TestLocalFetcherFoldStopsAtTheDepthBound(t *testing.T) {
	// rel with sep separators, its second segment capitalised so only the fold can
	// resolve it: characters/Deep/sub/.../(a)normal.png.
	build := func(t *testing.T, sep int) (mount, rel string) {
		t.Helper()
		mount = t.TempDir()
		segs := []string{"characters", "Deep"}
		for len(segs) < sep {
			segs = append(segs, "sub")
		}
		segs = append(segs, "(a)normal.png")
		full := filepath.Join(append([]string{mount}, segs...)...)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("SPRITE"), 0o644); err != nil {
			t.Fatal(err)
		}
		return mount, strings.Replace(strings.Join(segs, "/"), "Deep", "deep", 1)
	}

	inside, insideRel := build(t, localFoldMaxDepth-1)
	if _, err := os.Stat(filepath.Join(inside, "characters", "deep")); err == nil {
		t.Skip("this filesystem folds case: the exact spellings answer before the walk")
	}
	if data, err := NewLocalFetcher([]string{inside}).Fetch(context.Background(), assetsLocalURL(inside, insideRel)); err != nil {
		t.Fatalf("a path one separator inside the bound must still heal: %v", err)
	} else if string(data) != "SPRITE" {
		t.Fatalf("got %q, want SPRITE", data)
	}

	past, pastRel := build(t, localFoldMaxDepth)
	if _, err := NewLocalFetcher([]string{past}).Fetch(context.Background(), assetsLocalURL(past, pastRel)); err == nil {
		t.Error("a path at the depth bound healed — the walk is unbounded in depth")
	}
}

// assetsLocalURL is the URL a mount's own fetcher addresses rel by.
func assetsLocalURL(mount, rel string) string {
	return LocalOriginFor([]string{mount}) + rel
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
