package assets

import (
	"strconv"
	"strings"
	"testing"
)

func layerFor(t *testing.T, files map[string]string, origins ...string) *MountLayer {
	t.Helper()
	dir := t.TempDir()
	writePack(t, dir, files)
	ix, errs := BuildMountIndex([]string{dir})
	if len(errs) != 0 {
		t.Fatalf("build errors: %v", errs)
	}
	t.Cleanup(ix.Retire)
	return NewMountLayer(ix, []string{dir}, origins)
}

// TestMountLayerLocalOriginMatchesLocalFetcher pins that the pack's transport
// URLs land in the SAME keyspace the rest of the client already uses for local
// bytes. Recomputing the mount-set hash independently would split T2 and the
// quarantine round trip on any difference in cleaning, empty-filtering or
// separator.
func TestMountLayerLocalOriginMatchesLocalFetcher(t *testing.T) {
	dir := t.TempDir()
	ix, _ := BuildMountIndex([]string{dir})
	defer ix.Retire()
	l := NewMountLayer(ix, []string{dir}, []string{"http://h/base/"})
	if want := NewLocalFetcher([]string{dir}).BaseURL(); l.LocalOrigin() != want {
		t.Errorf("local origin %q, want %q — the keyspaces have split", l.LocalOrigin(), want)
	}
}

// TestMountLayerRelOfLongestOriginWins pins the disambiguation between two
// servers sharing a host but differing in base path. The shorter origin would
// otherwise claim the other's URLs and yield a rel carrying the extra segment,
// which can never match an index key.
func TestMountLayerRelOfLongestOriginWins(t *testing.T) {
	l := layerFor(t, map[string]string{"characters/witch/(a)normal.png": "x"},
		"http://h/", "http://h/base/")

	rel, ok := l.RelOf("http://h/base/characters/witch/(a)normal.png")
	if !ok {
		t.Fatal("no origin matched")
	}
	if rel != "characters/witch/(a)normal.png" {
		t.Errorf("rel = %q, want the path under the LONGER origin", rel)
	}
}

// TestMountLayerRelOfRejectsForeignURL pins that the layer never claims a URL it
// has no business answering — an external music link, another server's host.
func TestMountLayerRelOfRejectsForeignURL(t *testing.T) {
	l := layerFor(t, map[string]string{"characters/witch/(a)normal.png": "x"}, "http://h/base/")
	for _, u := range []string{
		"https://cdn.example.com/music/track.opus",
		"http://other/base/characters/witch/(a)normal.png",
		"local://m-deadbeef/characters/witch/(a)normal.png",
	} {
		if _, ok := l.RelOf(u); ok {
			t.Errorf("RelOf claimed a foreign URL: %s", u)
		}
	}
}

// TestMountLayerUnnormalizedOriginIsTheInertTrap documents the failure the origin
// contract exists to prevent: an origin without its trailing slash yields a rel
// with a LEADING slash, which cannot match any index key, so the pack goes
// silently inert while the index still reports healthy.
func TestMountLayerUnnormalizedOriginIsTheInertTrap(t *testing.T) {
	l := layerFor(t, map[string]string{"characters/witch/(a)normal.png": "x"}, "http://h/base")
	rel, ok := l.RelOf("http://h/base/characters/witch/(a)normal.png")
	if !ok {
		t.Fatal("fixture: the origin should still prefix-match")
	}
	if !strings.HasPrefix(rel, "/") {
		t.Fatalf("fixture drifted: rel = %q", rel)
	}
	if l.Covers("http://h/base/characters/witch/(a)normal.png") {
		t.Error("an unnormalized origin appeared to work — the trap would go unnoticed")
	}
}

// TestMountLayerPackURLRoundTrip pins LocalURLFor and PackRelOf as an exact
// inverse pair over the ONE key space, including the folded names that actually
// occur in AO content.
func TestMountLayerPackURLRoundTrip(t *testing.T) {
	l := layerFor(t, map[string]string{"characters/witch/(a)normal.png": "x"}, "http://h/base/")
	for _, key := range []string{
		"characters/witch/(a)normal.png",
		"characters/phoenix wright/(a)normal.png",
		"evidence/my file.png",
		"sounds/music/track.opus",
	} {
		got, ok := l.PackRelOf(l.LocalURLFor(key))
		if !ok {
			t.Errorf("PackRelOf rejected our own URL for %q", key)
			continue
		}
		if got != key {
			t.Errorf("round trip: %q -> %q", key, got)
		}
	}
}

// TestMountLayerPackRelOfRejectsForeignLocalOrigin pins that an archive replay's
// local:// URLs are not mistaken for this layer's, which is what keeps a replay
// from quarantining entries against the live pack's index.
func TestMountLayerPackRelOfRejectsForeignLocalOrigin(t *testing.T) {
	l := layerFor(t, map[string]string{"characters/witch/(a)normal.png": "x"}, "http://h/base/")
	foreign := NewLocalFetcher([]string{t.TempDir()}).BaseURL() + "characters/witch/(a)normal.png"
	if _, ok := l.PackRelOf(foreign); ok {
		t.Error("PackRelOf claimed another mount set's local:// URL")
	}
}

// TestMountLayerCoversBothKeyShapes pins that Covers answers for the two T1 key
// shapes: extensionless bases from the probe path, and full URLs with extensions
// from the exact path (evidence, music). A stem-only test misses the latter
// entirely, which would leave those textures stale after a Rescan.
func TestMountLayerCoversBothKeyShapes(t *testing.T) {
	l := layerFor(t, map[string]string{
		"characters/witch/(a)normal.png": "sprite",
		"sounds/music/track.opus":        "music",
		"evidence/knife.png":             "ev",
	}, "http://h/base/")

	if !l.Covers("http://h/base/characters/witch/(a)normal") {
		t.Error("Covers missed an extensionless base (the probe-path key shape)")
	}
	if !l.Covers("http://h/base/sounds/music/track.opus") {
		t.Error("Covers missed a music URL (the exact key shape)")
	}
	if !l.Covers("http://h/base/evidence/knife.png") {
		t.Error("Covers missed an evidence URL (the exact key shape)")
	}
	if l.Covers("http://h/base/characters/nobody/(a)normal") {
		t.Error("Covers claimed an asset the pack does not hold")
	}
	if l.Covers("theme://chatbox") {
		t.Error("Covers claimed a reserved scheme key")
	}
}

// TestMountLayerExcludesManifestAndListings pins the two rels a pack must never
// answer for. The manifest one is load-bearing: a pack carved out of a server's
// base often ships extensions.json, and answering with it would reseed the
// SERVER host's learned probe order from the pack author's conventions.
func TestMountLayerExcludesManifestAndListings(t *testing.T) {
	l := layerFor(t, map[string]string{
		ManifestFileName:                 "{}",
		"characters/witch/(a)normal.png": "x",
	}, "http://h/base/")

	if !mountLayerExcluded(ManifestFileName) {
		t.Error("the manifest is not excluded — a pack could reseed the server's formats")
	}
	if !mountLayerExcluded("characters/") {
		t.Error("a directory listing rel is not excluded")
	}
	if !mountLayerExcluded("") {
		t.Error("an empty rel is not excluded")
	}
	if mountLayerExcluded("characters/witch/(a)normal.png") {
		t.Error("a normal asset rel was excluded")
	}
	if l.Covers("http://h/base/" + ManifestFileName) {
		t.Error("Covers claims the manifest")
	}
}

// TestMountLayerOriginsAreDedupedAndCapped pins rule 4 without the silent-drop
// failure a hand-picked small cap would cause: several tabs on one server share
// an origin, so deduping comes first.
func TestMountLayerOriginsAreDedupedAndCapped(t *testing.T) {
	dir := t.TempDir()
	ix, _ := BuildMountIndex([]string{dir})
	defer ix.Retire()

	dupes := []string{"http://h/base/", "http://h/base/", "http://h/base/", ""}
	if l := NewMountLayer(ix, []string{dir}, dupes); len(l.Origins()) != 1 {
		t.Errorf("origins = %v, want one deduped entry", l.Origins())
	}

	many := make([]string, mountLayerOriginCap+10)
	for i := range many {
		many[i] = "http://h" + strconv.Itoa(i) + "/base/"
	}
	if l := NewMountLayer(ix, []string{dir}, many); len(l.Origins()) > mountLayerOriginCap {
		t.Errorf("origins grew to %d, past the %d cap", len(l.Origins()), mountLayerOriginCap)
	}
}

// TestMountLayerNilIsSafe pins the no-mounts state: every accessor must tolerate
// a nil layer, because that is the DEFAULT for the overwhelming majority of users
// and the fetch path checks it on every pass.
func TestMountLayerNilIsSafe(t *testing.T) {
	var l *MountLayer
	if l.Covers("http://h/base/x") {
		t.Error("a nil layer claims coverage")
	}
	if _, ok := l.PackRelOf("local://m-x/y"); ok {
		t.Error("a nil layer claims a pack URL")
	}
	if l.Index() != nil {
		t.Error("a nil layer returned a non-nil index")
	}
}
