package archive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

func buildManager(t *testing.T, source assets.Fetcher, localMode bool) (*assets.Manager, *assets.Resolver) {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	resolver := assets.NewResolver(prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	mgr := assets.NewManager(assets.ManagerDeps{
		Resolver: resolver, Prefs: prefs, T2: t2, Disk: disk,
		Source: source, LocalMode: localMode, Pool: pool, Decoder: decoder,
	})
	return mgr, resolver
}

func writeFixture(t *testing.T, dir, rel string, data []byte) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExportReplayRoundTrip is the definition of done for the self-contained
// archive: export a scene's assets from a SOURCE origin into a folder, then
// resolve them back through a SEPARATE replay manager over that folder, with the
// source gone. If the bytes round-trip, a bundled .aorec keeps its visuals when
// the CDN dies.
func TestExportReplayRoundTrip(t *testing.T) {
	// SOURCE: a local "server" holding assets. Derive every fixture path FROM the
	// URL builder (don't hand-construct) — seg() lowercases + percent-encodes, so
	// hand-built paths only match on a case-insensitive filesystem. This is the
	// same export/replay symmetry the feature relies on.
	srcDir := t.TempDir()
	srcLocal := assets.NewLocalFetcher([]string{srcDir})
	origin := srcLocal.BaseURL()
	urls := courtroom.NewURLBuilder(origin)
	rel := func(base string) string {
		r, _ := strings.CutPrefix(base, origin)
		return r + config.ExtWebP
	}
	bgPart, deskPart := courtroom.PositionScene("wit")
	idleBytes := []byte("PHOENIX-IDLE")
	writeFixture(t, srcDir, rel(urls.Emote("Phoenix", "normal", courtroom.EmoteIdle)), idleBytes)
	writeFixture(t, srcDir, rel(urls.Emote("Phoenix", "normal", courtroom.EmoteTalk)), []byte("PHOENIX-TALK"))
	writeFixture(t, srcDir, rel(urls.Background("gs4", bgPart)), []byte("BG"))
	writeFixture(t, srcDir, rel(urls.Background("gs4", deskPart)), []byte("DESK"))
	srcMgr, _ := buildManager(t, srcLocal, true)

	events := []courtroom.Event{
		{Kind: courtroom.EventBackground, Text: "gs4"},
		{Kind: courtroom.EventMessage, Message: &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Side: "wit"}},
	}

	// EXPORT into a fresh archive folder.
	archDir := t.TempDir()
	res, err := ExportAssets(context.Background(), srcMgr, origin, "", events, archDir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files < 4 {
		t.Fatalf("expected >=4 bundled assets (idle/talk/bg/desk), got %d", res.Files)
	}
	if _, err := os.Stat(filepath.Join(archDir, filepath.FromSlash(rel(urls.Emote("Phoenix", "normal", courtroom.EmoteIdle))))); err != nil {
		t.Fatalf("idle sprite not written into the archive: %v", err)
	}

	// REPLAY: a SEPARATE manager over the archive (source is gone), seeded from
	// the export's format manifest — exactly what replaying a bundled .aorec does.
	repLocal := assets.NewLocalFetcher([]string{archDir})
	repMgr, repResolver := buildManager(t, repLocal, true)
	SeedFormats(repResolver, repLocal.BaseURL(), res.Formats)

	repUrls := courtroom.NewURLBuilder(repLocal.BaseURL())
	idle := repUrls.Emote("Phoenix", "normal", courtroom.EmoteIdle)
	_, data, ok := repMgr.ResolveRaw(idle, assets.AssetTypeCharSprite)
	if !ok {
		t.Fatal("speaker idle sprite did NOT resolve from the archive — round-trip broken")
	}
	if string(data) != string(idleBytes) {
		t.Errorf("archive idle bytes = %q, want %q", data, idleBytes)
	}
}

// TestExportNestedBackgroundSubfolder pins issue #40: a scene whose background
// nests in a subfolder ("cases/case1") must bundle onto real subfolders, never a
// folder literally named "cases%2Fcase1". The bug was a single-segment escaper
// on the background name; the exporter writes the origin-relative URL path
// verbatim, so a "%2F" URL became a "%2F" directory on disk.
func TestExportNestedBackgroundSubfolder(t *testing.T) {
	srcDir := t.TempDir()
	srcLocal := assets.NewLocalFetcher([]string{srcDir})
	origin := srcLocal.BaseURL()
	urls := courtroom.NewURLBuilder(origin)
	rel := func(base string) string {
		r, _ := strings.CutPrefix(base, origin)
		return r + config.ExtWebP
	}
	const nestedBg = "cases/case1"
	bgPart, deskPart := courtroom.PositionScene("wit")
	writeFixture(t, srcDir, rel(urls.Emote("Phoenix", "normal", courtroom.EmoteIdle)), []byte("IDLE"))
	writeFixture(t, srcDir, rel(urls.Emote("Phoenix", "normal", courtroom.EmoteTalk)), []byte("TALK"))
	writeFixture(t, srcDir, rel(urls.Background(nestedBg, bgPart)), []byte("BG"))
	writeFixture(t, srcDir, rel(urls.Background(nestedBg, deskPart)), []byte("DESK"))
	srcMgr, _ := buildManager(t, srcLocal, true)

	events := []courtroom.Event{
		{Kind: courtroom.EventBackground, Text: nestedBg},
		{Kind: courtroom.EventMessage, Message: &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Side: "wit"}},
	}

	archDir := t.TempDir()
	if _, err := ExportAssets(context.Background(), srcMgr, origin, "", events, archDir); err != nil {
		t.Fatal(err)
	}

	// The background must land under real "background/cases/case1/…" subfolders.
	wantBg := filepath.Join(archDir, filepath.FromSlash(rel(urls.Background(nestedBg, bgPart))))
	if _, err := os.Stat(wantBg); err != nil {
		t.Fatalf("nested background not bundled at its subfolder path %q: %v", wantBg, err)
	}
	// And NO directory anywhere in the bundle carries a literal "%2F".
	_ = filepath.Walk(archDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.Contains(p, "%2F") {
			t.Errorf("bundle path carries an escaped separator: %q", p)
		}
		return nil
	})
}

// TestExportDecodesBundleNames pins the #40 follow-up: a character folder that
// nests AND carries spaces ("drio/byakuya togami") must bundle onto clean,
// human-readable folders ("characters/drio/byakuya togami/…"), never the raw URL
// spelling ("characters/drio%2Fbyakuya%20togami"). A URL must escape the space,
// so the clean name is recovered only at the URL→disk boundary — and the bundle
// must still replay, via the local fetcher's decoded-spelling attempt.
func TestExportDecodesBundleNames(t *testing.T) {
	srcDir := t.TempDir()
	srcLocal := assets.NewLocalFetcher([]string{srcDir})
	origin := srcLocal.BaseURL()
	urls := courtroom.NewURLBuilder(origin)
	rel := func(base string) string {
		r, _ := strings.CutPrefix(base, origin)
		return r + config.ExtWebP
	}
	const char = "drio/byakuya togami" // nests (→%2F) and has a space (→%20)
	idle := []byte("IDLE-BYTES")
	// The SOURCE mount keeps the URL's escaped spelling (LocalFetcher resolves it
	// raw-first); the export is what must decode.
	writeFixture(t, srcDir, rel(urls.Emote(char, "normal", courtroom.EmoteIdle)), idle)
	writeFixture(t, srcDir, rel(urls.Emote(char, "normal", courtroom.EmoteTalk)), []byte("TALK"))
	bgPart, deskPart := courtroom.PositionScene("wit")
	writeFixture(t, srcDir, rel(urls.Background("gs4", bgPart)), []byte("BG"))
	writeFixture(t, srcDir, rel(urls.Background("gs4", deskPart)), []byte("DESK"))
	srcMgr, _ := buildManager(t, srcLocal, true)

	events := []courtroom.Event{
		{Kind: courtroom.EventBackground, Text: "gs4"},
		{Kind: courtroom.EventMessage, Message: &protocol.ChatMessage{CharName: char, Emote: "normal", Side: "wit"}},
	}
	archDir := t.TempDir()
	if _, err := ExportAssets(context.Background(), srcMgr, origin, "", events, archDir); err != nil {
		t.Fatal(err)
	}

	// The character landed at a CLEAN "drio/byakuya togami" subfolder path.
	clean := filepath.Join(archDir, "characters", "drio", "byakuya togami", "(a)normal.webp")
	if _, err := os.Stat(clean); err != nil {
		t.Fatalf("character not bundled at its decoded path %q: %v", clean, err)
	}
	// No escaped names survive anywhere in the tree.
	_ = filepath.Walk(archDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && (strings.Contains(p, "%2F") || strings.Contains(p, "%20")) {
			t.Errorf("bundle path is still URL-escaped: %q", p)
		}
		return nil
	})

	// And it still replays: a separate manager over the clean archive resolves the
	// escaped URL via the local fetcher's decoded-spelling attempt.
	repLocal := assets.NewLocalFetcher([]string{archDir})
	repMgr, _ := buildManager(t, repLocal, true)
	repUrls := courtroom.NewURLBuilder(repLocal.BaseURL())
	_, data, ok := repMgr.ResolveRaw(repUrls.Emote(char, "normal", courtroom.EmoteIdle), assets.AssetTypeCharSprite)
	if !ok {
		t.Fatal("decoded-name character did NOT resolve from the clean archive — round-trip broken")
	}
	if string(data) != string(idle) {
		t.Errorf("archive idle bytes = %q, want %q", data, idle)
	}
}

// TestDiskPathDecodesAndClamps unit-pins the URL→disk mapping: decode escapes,
// turn "%2F" into a real separator, and clamp "../" so a bundle can never write
// outside its root.
func TestDiskPathDecodesAndClamps(t *testing.T) {
	cases := map[string]string{
		"characters/drio%2Fbyakuya%20togami/(a)normal.webp": "characters/drio/byakuya togami/(a)normal.webp",
		"sounds/music/daily%20life/%5Bresign%5D.opus":       "sounds/music/daily life/[resign].opus",
		"background/room/../../../../etc/passwd":            "etc/passwd", // clamped, cannot escape
		"a//b/./c":                                          "a/b/c",
	}
	for in, want := range cases {
		if got := DiskPath(in); got != want {
			t.Errorf("DiskPath(%q) = %q, want %q", in, got, want)
		}
	}
}
