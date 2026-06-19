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
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"))
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
