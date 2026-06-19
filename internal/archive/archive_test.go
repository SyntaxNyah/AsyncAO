package archive

import (
	"context"
	"os"
	"path/filepath"
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
	// SOURCE: a local "server" holding assets at the webAO paths.
	srcDir := t.TempDir()
	bgPart, deskPart := courtroom.PositionScene("wit")
	idleBytes := []byte("PHOENIX-IDLE")
	writeFixture(t, srcDir, "characters/Phoenix/(a)normal"+config.ExtWebP, idleBytes)
	writeFixture(t, srcDir, "characters/Phoenix/(b)normal"+config.ExtWebP, []byte("PHOENIX-TALK"))
	writeFixture(t, srcDir, "background/gs4/"+bgPart+config.ExtWebP, []byte("BG"))
	writeFixture(t, srcDir, "background/gs4/"+deskPart+config.ExtWebP, []byte("DESK"))
	srcLocal := assets.NewLocalFetcher([]string{srcDir})
	srcMgr, _ := buildManager(t, srcLocal, true)
	origin := srcLocal.BaseURL()

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
	if _, err := os.Stat(filepath.Join(archDir, "characters", "Phoenix", "(a)normal"+config.ExtWebP)); err != nil {
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
